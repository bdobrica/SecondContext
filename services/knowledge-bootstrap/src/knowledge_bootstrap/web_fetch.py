"""Direct, pinned HTTP connections. No proxies, cookies or ambient credentials."""

import hashlib
import http.client
import io
import ipaddress
import socket
import ssl
import time
from dataclasses import dataclass
from datetime import UTC, datetime
from email.message import Message
from urllib.parse import urljoin, urlsplit

import dns.exception
import dns.resolver

from knowledge_bootstrap.config import Settings
from knowledge_bootstrap.parsers import ParseError
from knowledge_bootstrap.web_urls import normalize_url, public_address

USER_AGENT = "SecondContextKnowledge/0.1"
REDIRECTS = {301, 302, 303, 307, 308}


def remaining(deadline: float) -> float:
    seconds = deadline - time.monotonic()
    if seconds <= 0:
        raise ParseError("web_timeout", "Website ingestion exceeded its time limit")
    return seconds


def resolve_public(host: str, deadline: float) -> list[str]:
    try:
        return [str(public_address(host))]
    except ParseError:
        try:
            ipaddress.ip_address(host)
        except ValueError:
            pass
        else:
            raise
    addresses = []
    resolver = dns.resolver.Resolver()
    try:
        for record_type in ("A", "AAAA"):
            try:
                answer = resolver.resolve(
                    host + ".", record_type, lifetime=remaining(deadline), search=False
                )
                addresses.extend(record.address for record in answer)
            except dns.resolver.NoAnswer:
                continue
    except dns.exception.Timeout:
        raise ParseError("web_timeout", "Website DNS resolution timed out") from None
    except dns.exception.DNSException:
        raise ParseError("dns_failed", "Website hostname could not be resolved") from None
    if not addresses:
        raise ParseError("dns_failed", "Website hostname has no address records")
    # Reject the entire set if even one record is forbidden. Connect to these exact
    # numeric addresses later, with no second hostname resolution by the HTTP stack.
    return list(dict.fromkeys(str(public_address(address)) for address in addresses))


class DeadlineReader(io.RawIOBase):
    """Enforce the absolute deadline on each header/body socket read, including TLS."""

    def __init__(self, sock, deadline):
        super().__init__()
        self.sock = sock
        self.stream = sock.makefile("rb", buffering=0)
        self.deadline = deadline

    def readable(self):
        return True

    def readinto(self, buffer):
        self.sock.settimeout(remaining(self.deadline))
        return self.stream.readinto(buffer)

    def close(self):
        self.stream.close()
        super().close()


class DeadlineTransport:
    def __init__(self, sock, deadline):
        self.sock = sock
        self.deadline = deadline

    def __getattr__(self, name):
        return getattr(self.sock, name)

    def makefile(self, mode):
        if mode != "rb":
            raise ValueError("Only bounded binary response reads are supported")
        return io.BufferedReader(DeadlineReader(self.sock, self.deadline))

    def sendall(self, data):
        self.sock.settimeout(remaining(self.deadline))
        return self.sock.sendall(data)


class PinnedConnection(http.client.HTTPConnection):
    def __init__(self, host: str, port: int, address: str, secure: bool, deadline: float):
        super().__init__(host, port, timeout=remaining(deadline))
        self.address = str(public_address(address))
        self.secure = secure
        self.deadline = deadline

    def connect(self):
        address = public_address(self.address)
        sock = socket.socket(
            socket.AF_INET6 if address.version == 6 else socket.AF_INET, socket.SOCK_STREAM
        )
        try:
            sock.settimeout(remaining(self.deadline))
            sock.connect((self.address, self.port))
            if public_address(sock.getpeername()[0]) != address:
                raise ParseError(
                    "url_forbidden", "Connected peer differs from the validated target"
                )
            if self.secure:
                context = ssl.create_default_context()
                context.set_alpn_protocols(["http/1.1"])
                sock.settimeout(remaining(self.deadline))
                sock = context.wrap_socket(sock, server_hostname=self.host)
            self.sock = DeadlineTransport(sock, self.deadline)
        except BaseException:
            sock.close()
            raise


@dataclass
class FetchResult:
    requested_url: str
    final_url: str
    status: int
    content_type: str
    body: bytes
    retrieved_at: str
    redirects: list[str]

    def metadata(self) -> dict:
        return {
            "requested_url": self.requested_url,
            "final_url": self.final_url,
            "retrieved_at": self.retrieved_at,
            "http_status": self.status,
            "content_type": self.content_type,
            "response_hash": hashlib.sha256(self.body).hexdigest(),
            "redirects": self.redirects,
        }


def _read_response(response, sock, settings: Settings, deadline: float) -> bytes:
    # No transparent decompression: request identity and reject encoded bodies.
    if response.getheader("Content-Encoding", "identity").strip().lower() not in {"", "identity"}:
        raise ParseError(
            "unsupported_content_encoding", "Compressed HTTP responses are not supported"
        )
    length = response.getheader("Content-Length")
    if length is not None:
        try:
            declared = int(length)
        except ValueError:
            raise ParseError("invalid_response", "Invalid HTTP Content-Length") from None
        if declared < 0:
            raise ParseError("invalid_response", "Invalid HTTP Content-Length")
        if declared > settings.web_max_response_bytes:
            raise ParseError("response_too_large", "HTTP response exceeds the byte limit")
    body = bytearray()
    # A length-delimited response can close its final socket reference inside
    # read1. Stop at the advertised length before touching that socket again.
    while length is None or response.chunked or len(body) < declared:
        sock.settimeout(remaining(deadline))
        # read1 performs at most one underlying read; repeated slow data cannot
        # reset the total deadline as it could with an unbounded read(n).
        chunk = response.read1(min(65_536, settings.web_max_response_bytes + 1 - len(body)))
        if not chunk:
            break
        body.extend(chunk)
        if len(body) > settings.web_max_response_bytes:
            raise ParseError("response_too_large", "HTTP response exceeds the byte limit")
    if length is not None and not response.chunked and len(body) != declared:
        raise ParseError("invalid_response", "Incomplete HTTP response body")
    return bytes(body)


def fetch(
    requested_url: str,
    settings: Settings,
    deadline: float,
    *,
    allowed=None,
    before_request=None,
    robots: bool = False,
) -> FetchResult:
    current = normalize_url(requested_url)
    chain = []
    request_deadline = min(deadline, time.monotonic() + settings.web_request_timeout_seconds)
    for hop in range(settings.web_max_redirects + 1):
        if current in chain:
            raise ParseError("redirect_loop", "Website redirect loop detected")
        if allowed is not None and not allowed(current):
            raise ParseError("crawl_scope_violation", "Redirect leaves the configured crawl scope")
        # Hooks apply to every redirect too, so robots and delays cannot be bypassed.
        if before_request is not None:
            before_request(current)
        parts = urlsplit(current)
        addresses = resolve_public(parts.hostname, request_deadline)
        connection = None
        response = None
        try:
            # Try validated addresses in DNS order within the same total deadline.
            for address in addresses:
                connection = PinnedConnection(
                    parts.hostname,
                    443 if parts.scheme == "https" else 80,
                    address,
                    parts.scheme == "https",
                    request_deadline,
                )
                try:
                    connection.connect()
                    break
                except ssl.SSLError:
                    raise ParseError(
                        "tls_failed", "Website TLS verification or handshake failed"
                    ) from None
                except TimeoutError:
                    raise ParseError("web_timeout", "Website connection timed out") from None
                except OSError:
                    connection.close()
            else:
                raise ParseError("fetch_failed", "Could not connect to the website")
            connection.sock.settimeout(remaining(request_deadline))
            target = parts.path + ("?" + parts.query if parts.query else "")
            connection.request(
                "GET",
                target,
                headers={
                    "User-Agent": USER_AGENT,
                    "Accept": "text/plain" if robots else "text/html, application/xhtml+xml",
                    "Accept-Encoding": "identity",
                    "Connection": "close",
                },
            )
            connection.sock.settimeout(remaining(request_deadline))
            # Keep a reference: getresponse can clear connection.sock for close-delimited bodies.
            sock = connection.sock
            response = connection.getresponse()
            status = response.status
            if status in REDIRECTS:
                location = response.getheader("Location")
                if not location:
                    raise ParseError("invalid_redirect", "Website redirect lacks a target URL")
                if hop == settings.web_max_redirects:
                    raise ParseError("too_many_redirects", "Website exceeded the redirect limit")
                chain.append(current)
                current = normalize_url(urljoin(current, location))
                continue  # Never read a redirect body.
            content_type = response.getheader("Content-Type", "")
            if len(content_type) > 200:
                raise ParseError("invalid_response", "HTTP content type is too long")
            message = Message()
            message["content-type"] = content_type
            accepted = {"text/plain"} if robots else {"text/html", "application/xhtml+xml"}
            if 200 <= status < 300 and message.get_content_type().lower() not in accepted:
                raise ParseError(
                    "unsupported_content_type", "Website did not return supported static content"
                )
            body = (
                _read_response(response, sock, settings, request_deadline)
                if 200 <= status < 300
                else b""
            )
            return FetchResult(
                requested_url,
                current,
                status,
                content_type,
                body,
                datetime.now(UTC).isoformat(),
                chain,
            )
        except TimeoutError:
            raise ParseError("web_timeout", "Website request timed out") from None
        except ssl.SSLError:
            raise ParseError("tls_failed", "Website TLS verification or handshake failed") from None
        except (OSError, http.client.HTTPException):
            raise ParseError(
                "fetch_failed", "Website returned an invalid response or connection failed"
            ) from None
        finally:
            if response is not None:
                response.close()
            if connection is not None:
                connection.close()
    raise ParseError("too_many_redirects", "Website exceeded the redirect limit")
