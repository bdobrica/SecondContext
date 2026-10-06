"""URL identity, public-address policy and strict per-source crawl options."""

import ipaddress
import re
from typing import Literal, Self
from urllib.parse import quote, unquote, urlsplit, urlunsplit

from pydantic import BaseModel, ConfigDict, Field, model_validator

from knowledge_bootstrap.parsers import ParseError

# Block transition mechanisms too: an apparently public IPv6 destination must not
# tunnel a private IPv4 endpoint. Azure's special platform address is not public web.
FORBIDDEN_NETWORKS = tuple(
    ipaddress.ip_network(value)
    for value in ("64:ff9b::/96", "64:ff9b:1::/48", "2002::/16", "2001::/32", "168.63.129.16/32")
)


def public_address(value: str):
    try:
        address = ipaddress.ip_address(value)
    except ValueError:
        raise ParseError("url_forbidden", "Target address is not a public IP address") from None
    if (
        not address.is_global
        or address.is_multicast
        or address.is_reserved
        or getattr(address, "ipv4_mapped", None) is not None
    ):
        raise ParseError("url_forbidden", "Only public Internet addresses may be fetched")
    if any(
        address.version == network.version and address in network for network in FORBIDDEN_NETWORKS
    ):
        raise ParseError("url_forbidden", "Special network targets may not be fetched")
    return address


def _percent(value: str, safe: str) -> str:
    if re.search(r"%(?![0-9a-fA-F]{2})", value):
        raise ParseError("invalid_url", "Malformed URL percent encoding")
    value = quote(value, safe=safe + "%")
    # Decode unreserved bytes only; encoded separators retain their meaning.
    return re.sub(
        r"%([0-9a-fA-F]{2})",
        lambda match: (
            chr(int(match[1], 16))
            if chr(int(match[1], 16))
            in "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~"
            else "%" + match[1].upper()
        ),
        value,
    )


def normalize_url(value: str) -> str:
    if (
        not value
        or len(value) > 8192
        or any(c.isspace() or ord(c) < 32 or ord(c) == 127 for c in value)
    ):
        raise ParseError(
            "invalid_url", "URL is empty, too long or contains whitespace/control characters"
        )
    if "\\" in value:
        raise ParseError("invalid_url", "Backslashes are not supported in URLs")
    try:
        parts = urlsplit(value)
        if parts.scheme.lower() not in {"http", "https"} or not parts.hostname:
            raise ValueError
        if parts.username is not None or parts.password is not None:
            raise ValueError
        scheme = parts.scheme.lower()
        host = parts.hostname.rstrip(".").encode("idna").decode("ascii").lower()
        if "%" in host:
            raise ValueError
        port = parts.port
        if parts.netloc.endswith(":"):
            raise ValueError
        if port is not None and port != (443 if scheme == "https" else 80):
            raise ValueError
        try:
            address = ipaddress.ip_address(host)
        except ValueError:
            if (
                len(host) > 253
                or not all(
                    re.fullmatch(r"[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?", label)
                    for label in host.split(".")
                )
                or "." not in host
            ):
                raise ValueError from None
        else:
            public_address(str(address))
            host = f"[{address.compressed}]" if address.version == 6 else str(address)
        path = _percent(parts.path or "/", "/:@!$&'()*+,;=-._~")
        # Resolve dot segments without merging significant repeated slashes.
        segments = []
        for segment in path.split("/"):
            if segment == "..":
                if len(segments) > 1:
                    segments.pop()
            elif segment != ".":
                segments.append(segment)
        if path.endswith(("/.", "/..")):
            segments.append("")
        path = "/".join(segments) or "/"
        query = _percent(parts.query, "/?:@!$&'()*+,;=-._~")
        return urlunsplit((scheme, host, path, query, ""))
    except (ValueError, UnicodeError):
        raise ParseError(
            "invalid_url", "Use an HTTP/HTTPS URL without credentials on its default port"
        ) from None


class CrawlConfig(BaseModel):
    model_config = ConfigDict(extra="forbid")

    scope: Literal["page", "path", "host"] = "page"
    max_pages: int | None = Field(default=None, ge=1, le=50, strict=True)
    max_depth: int | None = Field(default=None, ge=0, le=5, strict=True)

    @model_validator(mode="after")
    def defaults(self) -> Self:
        self.max_pages = (
            self.max_pages if self.max_pages is not None else (1 if self.scope == "page" else 10)
        )
        self.max_depth = (
            self.max_depth if self.max_depth is not None else (0 if self.scope == "page" else 2)
        )
        if self.scope == "page" and (self.max_pages != 1 or self.max_depth != 0):
            raise ValueError("page scope requires max_pages=1 and max_depth=0")
        return self


def in_scope(url: str, seed: str, scope: str) -> bool:
    target, root = urlsplit(url), urlsplit(seed)
    if target.hostname != root.hostname:
        return False
    if scope == "host":
        return True
    if scope == "page":
        return url == seed
    # Decode conservatively for path comparison; encoded separators/dot traversal
    # must not escape a path boundary under a server's interpretation.
    path, prefix = target.path, root.path
    for _ in range(5):
        decoded = unquote(path)
        decoded_prefix = unquote(prefix)
        if (decoded, decoded_prefix) == (path, prefix):
            break
        path, prefix = decoded, decoded_prefix
    if "%" in path or ";" in path:
        return False
    prefix = prefix.rstrip("/")
    if any(part in {".", ".."} for part in path.split("/")) or "\\" in path:
        return False
    return path == prefix or path.startswith(prefix + "/")
