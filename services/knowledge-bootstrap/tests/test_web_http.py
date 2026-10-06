"""Real HTTP fixture server; production has no private-network override."""

import ipaddress
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

import pytest

from knowledge_bootstrap import web_fetch
from knowledge_bootstrap.parsers import ParseError
from knowledge_bootstrap.web_crawl import crawl
from knowledge_bootstrap.web_fetch import fetch
from knowledge_bootstrap.web_urls import CrawlConfig

pytestmark = pytest.mark.integration


@pytest.fixture
def http_server(monkeypatch):
    calls = []

    class Handler(BaseHTTPRequestHandler):
        protocol_version = "HTTP/1.1"

        def log_message(self, *args):
            pass

        def do_GET(self):
            calls.append(self.path)
            try:
                if self.path == "/slow-headers":
                    for byte in b"HTTP/1.1 200 OK\r\nContent-Type: text/html\r\n\r\n":
                        self.wfile.write(bytes([byte]))
                        self.wfile.flush()
                        time.sleep(0.02)
                    return
                if self.path == "/private":
                    self.send_response(302)
                    self.send_header("Location", "http://169.254.169.254/latest/meta-data")
                    self.end_headers()
                    return
                if self.path == "/robots.txt":
                    self.send_response(404)
                    self.send_header("Content-Length", "0")
                    self.end_headers()
                    return
                self.send_response(200)
                self.send_header("Connection", "close")
                self.send_header("Content-Type", "text/html; charset=utf-8")
                if self.path != "/streamed":
                    self.send_header("Content-Length", "100")
                self.end_headers()
                if self.path == "/slow-body":
                    for _ in range(100):
                        self.wfile.write(b"x")
                        self.wfile.flush()
                        time.sleep(0.02)
                else:
                    self.wfile.write(b"<main><p>" + b"x" * 78 + b"</p></main>xx")
            except (BrokenPipeError, ConnectionResetError):
                pass

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    original = web_fetch.PinnedConnection

    class LocalFixtureConnection(original):
        def __init__(self, host, port, address, secure, deadline):
            super().__init__(host, server.server_port, "127.0.0.1", False, deadline)

    # Only this fixture substitutes a local socket after public URL validation.
    monkeypatch.setattr(web_fetch, "public_address", ipaddress.ip_address)
    monkeypatch.setattr(web_fetch, "resolve_public", lambda *args: ["8.8.8.8"])
    monkeypatch.setattr(web_fetch, "PinnedConnection", LocalFixtureConnection)
    try:
        yield calls
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=2)


@pytest.mark.parametrize("path", ["/normal", "/streamed"])
def test_real_close_delimited_and_content_length_reads(http_server, settings, path):
    result = fetch("http://example.com" + path, settings, time.monotonic() + 3)
    assert len(result.body) == 100 and result.status == 200
    assert http_server == [path]


@pytest.mark.parametrize("path", ["/slow-body", "/slow-headers"])
def test_real_slow_data_obeys_total_request_deadline(http_server, settings, path):
    fast = settings.model_copy(update={"web_request_timeout_seconds": 0.15})
    started = time.monotonic()
    with pytest.raises(ParseError) as error:
        fetch("http://example.com" + path, fast, started + 5)
    assert error.value.code == "web_timeout" and time.monotonic() - started < 0.5


def test_real_unknown_length_response_is_bounded(http_server, settings):
    with pytest.raises(ParseError) as error:
        fetch(
            "http://example.com/streamed",
            settings.model_copy(update={"web_max_response_bytes": 99}),
            time.monotonic() + 3,
        )
    assert error.value.code == "response_too_large"


def test_real_redirect_cannot_reach_metadata(http_server, settings):
    with pytest.raises(ParseError):
        fetch("http://example.com/private", settings, time.monotonic() + 3)
    assert http_server == ["/private"]


def test_real_missing_robots_allows_seed(http_server, settings):
    result = crawl(
        "http://example.com/normal",
        CrawlConfig(),
        settings.model_copy(update={"web_crawl_delay_seconds": 0.1}),
    )
    assert len(result.documents) == 1
    assert http_server == ["/robots.txt", "/normal"]
