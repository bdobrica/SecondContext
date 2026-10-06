import json
import signal
import subprocess
import sys
import time
from dataclasses import asdict
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import Mock

import dns.exception
import dns.resolver
import pytest
from pydantic import ValidationError

from knowledge_bootstrap import web, web_crawl, web_fetch
from knowledge_bootstrap.parsers import ParseError
from knowledge_bootstrap.schemas import SourceCreate
from knowledge_bootstrap.web_crawl import crawl
from knowledge_bootstrap.web_fetch import FetchResult, PinnedConnection, fetch, resolve_public
from knowledge_bootstrap.web_html import parse_html
from knowledge_bootstrap.web_urls import CrawlConfig, in_scope, normalize_url, public_address

FIXTURE = Path(__file__).parent / "fixtures/web/handbook.html"


def response(
    url="https://example.com/docs", body=None, status=200, mime="text/html; charset=utf-8"
):
    return FetchResult(
        url,
        url,
        status,
        mime,
        FIXTURE.read_bytes() if body is None else body,
        "2026-10-04T12:00:00+00:00",
        [],
    )


@pytest.mark.parametrize(
    "address",
    [
        "127.0.0.1",
        "0.0.0.0",
        "10.1.2.3",
        "172.16.1.1",
        "192.168.1.1",
        "169.254.169.254",
        "100.100.100.200",
        "100.64.0.1",
        "224.0.0.1",
        "255.255.255.255",
        "192.0.2.1",
        "168.63.129.16",
        "::",
        "::1",
        "fe80::1",
        "fc00::1",
        "ff02::1",
        "2001:db8::1",
        "::ffff:8.8.8.8",
        "64:ff9b::a9fe:a9fe",
        "2002:7f00:1::1",
        "2001:0000::1",
    ],
)
def test_forbidden_addresses(address):
    with pytest.raises(ParseError, match="public|Special") as error:
        public_address(address)
    assert error.value.code == "url_forbidden"


@pytest.mark.parametrize("value", ["8.8.8.8", "1.1.1.1", "2606:4700:4700::1111"])
def test_public_addresses(value):
    assert str(public_address(value)) == value


@pytest.mark.parametrize(
    "url",
    [
        "file:///etc/passwd",
        "ftp://example.com/x",
        "https://user:pass@example.com",
        "https://@example.com",
        "http://127.0.0.1",
        "http://[::1]",
        "http://169.254.169.254",
        "http://example.com:8080",
        "https://example.com:80",
        "http://example.com\\@evil.com",
        "https://example.com/a\n",
        "https://example.com/%xy",
        "http://localhost",
        "http://[fe80::1%25eth0]",
        "https://example.com:",
    ],
)
def test_unsafe_url_input(url):
    with pytest.raises((ParseError, ValueError)):
        normalize_url(url)


def test_normalization_and_query_policy():
    assert (
        normalize_url("HTTPS://EXAMPLE.COM.:443/docs/%7efoo/../setup#heading")
        == "https://example.com/docs/setup"
    )
    assert (
        normalize_url("https://example.com/café?z=2&a=1#x")
        == "https://example.com/caf%C3%A9?z=2&a=1"
    )
    assert normalize_url("http://example.com") == "http://example.com/"
    assert normalize_url("https://example.com/a//b") == "https://example.com/a//b"
    assert normalize_url("https://example.com/a?q=1&q=2") != normalize_url(
        "https://example.com/a?q=2&q=1"
    )


@pytest.mark.parametrize(
    "target,expected",
    [
        ("https://example.com/docs/setup", True),
        ("http://example.com/docs", True),
        ("https://example.com/docs-other", False),
        ("https://other.example.com/docs", False),
        ("https://example.com/docs/%2e%2e/secret", False),
        ("https://example.com/docs/%252e%252e/secret", False),
        ("https://example.com/docs/..;/secret", False),
    ],
)
def test_path_boundaries(target, expected):
    assert in_scope(target, "https://example.com/docs", "path") == expected


def test_host_scope_and_config_validation():
    assert in_scope("https://example.com/other", "https://example.com/docs", "host")
    assert not in_scope("https://sub.example.com/", "https://example.com/docs", "host")
    assert CrawlConfig().model_dump() == {"scope": "page", "max_pages": 1, "max_depth": 0}
    assert CrawlConfig(scope="path").max_pages == 10
    for options in (
        {"scope": "domain"},
        {"max_pages": 2},
        {"scope": "path", "max_pages": True},
        {"ignore_robots": True},
    ):
        with pytest.raises(ValidationError):
            CrawlConfig(**options)
    with pytest.raises(ValidationError):
        SourceCreate(kind="url", source_uri="https://example.com", format="pdf")


def test_hostname_resolution_validates_every_address(monkeypatch):
    resolver = Mock()
    resolver.resolve.side_effect = [
        [SimpleNamespace(address="8.8.8.8")],
        [SimpleNamespace(address="::1")],
    ]
    monkeypatch.setattr(web_fetch.dns.resolver, "Resolver", lambda: resolver)
    with pytest.raises(ParseError) as error:
        resolve_public("example.com", time.monotonic() + 2)
    assert error.value.code == "url_forbidden"
    assert [call.args[:2] for call in resolver.resolve.call_args_list] == [
        ("example.com.", "A"),
        ("example.com.", "AAAA"),
    ]


@pytest.mark.parametrize(
    "failure,code",
    [(dns.exception.Timeout(), "web_timeout"), (dns.resolver.NXDOMAIN(), "dns_failed")],
)
def test_dns_failure(monkeypatch, failure, code):
    resolver = Mock()
    resolver.resolve.side_effect = failure
    monkeypatch.setattr(web_fetch.dns.resolver, "Resolver", lambda: resolver)
    with pytest.raises(ParseError) as error:
        resolve_public("example.com", time.monotonic() + 2)
    assert error.value.code == code


def test_pinned_connect_avoids_second_dns_and_checks_peer(monkeypatch):
    sock = Mock()
    sock.getpeername.return_value = ("8.8.8.8", 80)
    monkeypatch.setattr(web_fetch.socket, "socket", lambda *args: sock)
    monkeypatch.setattr(
        web_fetch.socket, "getaddrinfo", Mock(side_effect=AssertionError("second DNS"))
    )
    connection = PinnedConnection("example.com", 80, "8.8.8.8", False, time.monotonic() + 2)
    connection.connect()
    sock.connect.assert_called_once_with(("8.8.8.8", 80))
    connection.close()
    sock.getpeername.return_value = ("127.0.0.1", 80)
    with pytest.raises(ParseError) as error:
        connection.connect()
    assert error.value.code == "url_forbidden"
    sock.close.assert_called()


def test_tls_retains_original_hostname_and_verification(monkeypatch):
    sock = Mock()
    sock.getpeername.return_value = ("8.8.8.8", 443)
    context = web_fetch.ssl.create_default_context()
    assert context.check_hostname and context.verify_mode == web_fetch.ssl.CERT_REQUIRED
    wrapper = Mock(wraps=context)
    wrapper.wrap_socket.return_value = sock
    monkeypatch.setattr(web_fetch.ssl, "create_default_context", lambda: wrapper)
    monkeypatch.setattr(web_fetch.socket, "socket", lambda *args: sock)
    PinnedConnection("example.com", 443, "8.8.8.8", True, time.monotonic() + 2).connect()
    wrapper.wrap_socket.assert_called_once_with(sock, server_hostname="example.com")


@pytest.fixture
def scripted_http(monkeypatch):
    routes, calls = {}, []
    monkeypatch.setattr(web_fetch, "resolve_public", lambda host, deadline: ["8.8.8.8"])

    class Response:
        def __init__(self, status, headers, body):
            self.status, self.headers, self.body = status, headers, body
            self.chunked = False

        def getheader(self, name, default=None):
            return self.headers.get(name, default)

        def read1(self, count):
            result, self.body = self.body[:count], self.body[count:]
            return result

        def close(self):
            pass

    class Connection:
        def __init__(self, host, port, address, secure, deadline):
            self.origin = ("https" if secure else "http") + "://" + host
            self.sock = Mock()

        def connect(self):
            pass

        def request(self, method, target, headers):
            self.url = self.origin + target
            calls.append((self.url, headers))

        def getresponse(self):
            value = routes[self.url]
            if isinstance(value, Exception):
                raise value
            return Response(*value)

        def close(self):
            pass

    monkeypatch.setattr(web_fetch, "PinnedConnection", Connection)
    return routes, calls


@pytest.mark.parametrize(
    "target", ["http://127.0.0.1/secret", "http://169.254.169.254/latest", "file:///etc/passwd"]
)
def test_redirect_target_is_revalidated(scripted_http, settings, target):
    routes, calls = scripted_http
    routes["https://example.com/"] = (302, {"Location": target}, b"ignored")
    with pytest.raises(ParseError):
        fetch("https://example.com/", settings, time.monotonic() + 5)
    assert len(calls) == 1


def test_redirect_host_resolving_private(scripted_http, settings, monkeypatch):
    routes, calls = scripted_http
    routes["https://example.com/"] = (302, {"Location": "https://private.example/"}, b"")

    def resolve(host, deadline):
        return [str(public_address("127.0.0.1" if host == "private.example" else "8.8.8.8"))]

    monkeypatch.setattr(web_fetch, "resolve_public", resolve)
    with pytest.raises(ParseError) as error:
        fetch("https://example.com/", settings, time.monotonic() + 5)
    assert error.value.code == "url_forbidden" and len(calls) == 1


@pytest.mark.parametrize(
    "case,code",
    [
        ("loop", "redirect_loop"),
        ("limit", "too_many_redirects"),
        ("missing", "invalid_redirect"),
        ("timeout", "web_timeout"),
        ("mime", "unsupported_content_type"),
        ("encoding", "unsupported_content_encoding"),
        ("declared", "response_too_large"),
        ("streamed", "response_too_large"),
        ("truncated", "invalid_response"),
    ],
)
def test_fetch_bounds(scripted_http, settings, case, code):
    routes, _ = scripted_http
    url = "https://example.com/"
    headers = {"Content-Type": "text/html"}
    status, body = 200, b"x"
    settings = settings.model_copy(update={"web_max_response_bytes": 10, "web_max_redirects": 1})
    if case == "loop":
        status, headers = 302, {"Location": "/"}
    elif case == "limit":
        status, headers = 302, {"Location": "/2"}
        routes[url + "2"] = (302, {"Location": "/3"}, b"")
    elif case == "missing":
        status, headers = 302, {}
    elif case == "mime":
        headers["Content-Type"] = "application/pdf"
    elif case == "encoding":
        headers["Content-Encoding"] = "gzip"
    elif case == "declared":
        headers["Content-Length"] = "11"
    elif case == "streamed":
        body = b"x" * 11
    elif case == "truncated":
        headers["Content-Length"] = "2"
    routes[url] = TimeoutError() if case == "timeout" else (status, headers, body)
    with pytest.raises(ParseError) as error:
        fetch(url, settings, time.monotonic() + 5)
    assert error.value.code == code


def test_html_semantics_title_links_and_determinism(settings):
    parsed, links = parse_html(response(), settings)
    again, _ = parse_html(response(), settings)
    assert asdict(parsed) == asdict(again)
    assert parsed.title == "Engineering Handbook"
    assert parsed.format == "html" and len(parsed.content_hash) == 64
    assert "café" in parsed.text and "țară" in parsed.text
    assert (
        "discard" not in parsed.text
        and "Hidden text" not in parsed.text
        and "Invisible text" not in parsed.text
    )
    assert {block.type for block in parsed.blocks} == {
        "heading",
        "paragraph",
        "list",
        "code",
        "table",
    }
    assert next(block for block in parsed.blocks if block.type == "code").heading_path == [
        "Engineering",
        "Deployment",
    ]
    assert parsed.extra_metadata["canonical_url"] == "https://example.com/docs/handbook"
    assert links == ["https://example.com/docs/setup", "https://example.com/docs/setup?lang=en"]
    assert parsed.extra_metadata["retrieved_at"]
    assert parsed.extra_metadata["response_hash"]


@pytest.mark.parametrize(
    "body",
    [b"<html><body><div id='app'></div><script>render()</script></body></html>", b"", b"<p>Hi</p>"],
)
def test_low_empty_html(settings, body):
    with pytest.raises(ParseError) as error:
        parse_html(response(body=body), settings)
    assert error.value.code in {"html_text_unavailable", "invalid_html"}


@pytest.mark.parametrize(
    "limit,value",
    [
        ("max_parse_nodes", 2),
        ("max_parse_depth", 2),
        ("max_normalized_bytes", 20),
        ("web_max_response_bytes", 20),
    ],
)
def test_html_resource_limits(settings, limit, value):
    with pytest.raises(ParseError):
        parse_html(response(), settings.model_copy(update={limit: value}))


@pytest.fixture
def crawl_site(monkeypatch):
    routes, calls = {}, []

    def fake_fetch(url, settings, deadline, *, before_request=None, allowed=None, robots=False):
        if allowed and not allowed(url):
            raise ParseError("crawl_scope_violation", "Outside scope")
        if before_request:
            before_request(url)
        calls.append(url)
        if url.endswith("/robots.txt"):
            return response(url, routes.get(url, b"User-agent: *\nDisallow:\n"), mime="text/plain")
        if isinstance(routes[url], Exception):
            raise routes[url]
        return response(url, routes[url])

    monkeypatch.setattr(web_crawl, "fetch", fake_fetch)
    # Skip physical waiting in unit tests; pacing has its own test below.
    monkeypatch.setattr(web_crawl.time, "sleep", lambda seconds: None)
    return routes, calls


def page(*links):
    return (
        "<main><h1>Guide</h1><p>Useful reference information on deployment "
        "and engineering practice.</p>"
        + "".join(f'<a href="{link}">Next</a>' for link in links)
        + "</main>"
    ).encode()


def test_breadth_first_path_depth_and_duplicates(crawl_site, settings):
    routes, calls = crawl_site
    root = "https://example.com/docs"
    routes[root] = page(
        "/docs/a#first", "/docs/a#second", "/docs/b", "/outside", "https://sub.example.com/docs"
    )
    routes[root + "/a"] = page("/docs/deeper/x")
    routes[root + "/b"] = page()
    result = crawl(root, CrawlConfig(scope="path", max_pages=10, max_depth=1), settings)
    assert [doc.extra_metadata["final_url"] for doc in result.documents] == [
        root,
        root + "/a",
        root + "/b",
    ]
    assert calls.count(root + "/a") == 1
    assert calls.count("https://example.com/robots.txt") == 1
    assert [doc.extra_metadata["crawl_depth"] for doc in result.documents] == [0, 1, 1]


def test_page_and_attempt_bounds(crawl_site, settings):
    routes, calls = crawl_site
    root = "https://example.com/docs"
    routes[root] = page("/docs/a", "/docs/b")
    result = crawl(root, CrawlConfig(), settings)
    assert len(result.documents) == 1 and len(calls) == 2
    routes[root + "/a"] = ParseError("http_status", "Failed page")
    result = crawl(root, CrawlConfig(scope="path", max_pages=2, max_depth=2), settings)
    assert len(result.documents) == 1 and result.metadata["attempted_pages"] == 2
    assert result.metadata["page_limit_reached"]
    assert result.metadata["skipped_pages"][0]["error_code"] == "http_status"
    assert root + "/b" not in calls


def test_host_scope(crawl_site, settings):
    routes, _ = crawl_site
    root = "https://example.com/docs"
    routes[root] = page("/elsewhere")
    routes["https://example.com/elsewhere"] = page()
    assert len(crawl(root, CrawlConfig(scope="host", max_pages=2), settings).documents) == 2


def test_robots_disallow_seed_and_links(crawl_site, settings):
    routes, calls = crawl_site
    root = "https://example.com/docs"
    routes[root] = page("/docs/private", "/docs/public")
    routes["https://example.com/robots.txt"] = b"User-agent: *\nDisallow: /docs/private\n"
    routes[root + "/public"] = page()
    result = crawl(root, CrawlConfig(scope="path", max_pages=3), settings)
    assert len(result.documents) == 2
    assert result.metadata["skipped_pages"] == [
        {"url": root + "/private", "error_code": "robots_denied"}
    ]
    assert root + "/private" not in calls
    routes["https://example.com/robots.txt"] = b"User-agent: *\nDisallow: /\n"
    with pytest.raises(ParseError) as error:
        crawl(root, CrawlConfig(), settings)
    assert error.value.code == "robots_denied"


def test_robots_delay_and_time_budget(crawl_site, settings, monkeypatch):
    routes, _ = crawl_site
    root = "https://example.com/docs"
    routes[root] = page()
    routes["https://example.com/robots.txt"] = (
        b"User-agent: *\nDisallow:\nCrawl-delay: 2\nRequest-rate: 1/4\n"
    )
    sleeps = []
    monkeypatch.setattr(web_crawl.time, "sleep", sleeps.append)
    crawl(root, CrawlConfig(), settings)
    assert sleeps and 3.9 < sleeps[0] <= 4
    with pytest.raises(ParseError) as error:
        crawl(root, CrawlConfig(), settings.model_copy(update={"web_crawl_timeout_seconds": 1}))
    assert error.value.code == "web_timeout"


def test_total_crawl_output_and_frontier_bounds(crawl_site, settings):
    routes, _ = crawl_site
    root = "https://example.com/docs"
    routes[root] = page(*(f"/docs/{i}" for i in range(20)))
    for i in range(20):
        routes[root + f"/{i}"] = page()
    routes[root + "/0"] = page("/docs/next")
    limited = settings.model_copy(update={"web_max_pages": 2, "web_max_links": 1})
    result = crawl(root, CrawlConfig(scope="path", max_pages=2), limited)
    assert result.metadata["frontier_truncated"]
    with pytest.raises(ParseError) as error:
        crawl(root, CrawlConfig(), settings.model_copy(update={"web_max_output_bytes": 20}))
    assert error.value.code == "crawl_output_too_large"


def test_isolated_crawler_timeout_cleans_up_and_has_no_credentials(settings, monkeypatch):
    original = subprocess.run
    paths = []

    def slow_run(args, **kwargs):
        paths.append(Path(args[-2]).parent)
        data = json.loads(Path(args[-2]).read_text())
        assert "database_url" not in data["limits"] and "auth_tokens" not in data["limits"]
        assert set(kwargs["env"]) == {"PATH", "LANG", "PYTHONIOENCODING", "PYTHONHASHSEED"}
        # Use a real killed/reaped child without sleeping for the crawl's 5s startup allowance.
        kwargs["timeout"] = 0.05
        return original([sys.executable, "-c", "import time; time.sleep(5)"], **kwargs)

    monkeypatch.setattr(web.subprocess, "run", slow_run)
    with pytest.raises(ParseError) as error:
        web.ingest_website("https://example.com/", {}, settings)
    assert error.value.code == "web_timeout" and not paths[0].exists()


@pytest.mark.parametrize(
    "returncode,code",
    [(-signal.SIGXCPU, "web_timeout"), (-signal.SIGKILL, "crawler_resource_limit")],
)
def test_child_resource_failures(settings, monkeypatch, returncode, code):
    monkeypatch.setattr(
        web.subprocess, "run", lambda *args, **kwargs: SimpleNamespace(returncode=returncode)
    )
    with pytest.raises(ParseError) as error:
        web.ingest_website("https://example.com/", {}, settings)
    assert error.value.code == code


def test_robots_wildcards_specific_allow_and_fractional_delay(crawl_site, settings, monkeypatch):
    routes, calls = crawl_site
    root = "https://example.com/docs"
    routes[root] = page("/docs/private/a", "/docs/private/public")
    routes["https://example.com/robots.txt"] = (
        b"User-agent: *\nDisallow: /docs/private/*\n"
        b"Allow: /docs/private/public$\nCrawl-delay: 1.5\n"
    )
    routes[root + "/private/public"] = page()
    sleeps = []
    monkeypatch.setattr(web_crawl.time, "sleep", sleeps.append)
    result = crawl(root, CrawlConfig(scope="path", max_pages=3), settings)
    assert len(result.documents) == 2
    assert root + "/private/a" not in calls
    assert sleeps and sleeps[0] > 1.4


@pytest.mark.parametrize(
    "status,code", [(403, "robots_denied"), (503, "robots_denied"), (200, "robots_unavailable")]
)
def test_robots_status_and_type_fail_closed(scripted_http, settings, status, code):
    routes, calls = scripted_http
    routes["https://example.com/robots.txt"] = (
        status,
        {"Content-Type": "text/html"},
        b"<p>Error</p>",
    )
    with pytest.raises(ParseError) as error:
        crawl("https://example.com/docs", CrawlConfig(), settings)
    assert error.value.code == code
    assert len(calls) == 1


def test_robots_checked_for_each_page_redirect(scripted_http, settings):
    routes, calls = scripted_http
    routes["https://example.com/robots.txt"] = (
        200,
        {"Content-Type": "text/plain"},
        b"User-agent: *\nDisallow: /private\n",
    )
    routes["https://example.com/docs"] = (302, {"Location": "/private"}, b"")
    with pytest.raises(ParseError) as error:
        crawl(
            "https://example.com/docs",
            CrawlConfig(),
            settings.model_copy(update={"web_crawl_delay_seconds": 0.1}),
        )
    assert error.value.code == "robots_denied"
    assert [url for url, _ in calls] == [
        "https://example.com/robots.txt",
        "https://example.com/docs",
    ]


def test_path_redirect_does_not_leave_scope(scripted_http, settings):
    routes, calls = scripted_http
    routes["https://example.com/robots.txt"] = (404, {}, b"")
    routes["https://example.com/docs"] = (302, {"Location": "/other"}, b"")
    with pytest.raises(ParseError) as error:
        crawl(
            "https://example.com/docs",
            CrawlConfig(scope="path"),
            settings.model_copy(update={"web_crawl_delay_seconds": 0.1}),
        )
    assert error.value.code == "crawl_scope_violation"
    assert len(calls) == 2


def test_robots_private_redirect_is_blocked(scripted_http, settings):
    routes, calls = scripted_http
    routes["https://example.com/robots.txt"] = (302, {"Location": "http://127.0.0.1/"}, b"")
    with pytest.raises(ParseError) as error:
        crawl("https://example.com/docs", CrawlConfig(), settings)
    assert error.value.code == "url_forbidden" and len(calls) == 1
