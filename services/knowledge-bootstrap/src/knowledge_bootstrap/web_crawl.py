"""Breadth-first, exact-host crawling with robots and globally bounded work."""

import json
import time
from collections import deque
from dataclasses import asdict, dataclass
from urllib.parse import urlsplit, urlunsplit

from protego import Protego

from knowledge_bootstrap.config import Settings
from knowledge_bootstrap.parsers import ParsedDocument, ParseError
from knowledge_bootstrap.web_fetch import USER_AGENT, fetch, remaining
from knowledge_bootstrap.web_html import parse_html
from knowledge_bootstrap.web_urls import CrawlConfig, in_scope, normalize_url


@dataclass
class CrawlResult:
    documents: list[ParsedDocument]
    metadata: dict


def crawl(requested_url: str, config: CrawlConfig, settings: Settings) -> CrawlResult:
    if config.max_pages > settings.web_max_pages or config.max_depth > settings.web_max_depth:
        raise ParseError("crawl_limit_exceeded", "Source crawl options exceed server limits")
    seed = normalize_url(requested_url)
    deadline = time.monotonic() + settings.web_crawl_timeout_seconds
    policies = {}
    last_request = {}

    def pace(url: str, delay: float):
        # Share pacing across HTTP/HTTPS for a hostname, including robots/redirects.
        host = urlsplit(url).hostname
        wait = max(0, last_request.get(host, 0) + delay - time.monotonic())
        if wait >= remaining(deadline):
            raise ParseError(
                "web_timeout", "Required crawl delay exceeds the remaining time budget"
            )
        if wait:
            time.sleep(wait)
        last_request[host] = time.monotonic()

    def before_page(url: str):
        parts = urlsplit(url)
        origin = urlunsplit((parts.scheme, parts.netloc, "", "", ""))
        if origin not in policies:
            robots_url = origin + "/robots.txt"
            try:
                result = fetch(
                    robots_url,
                    settings,
                    deadline,
                    robots=True,
                    allowed=lambda target: urlsplit(target).hostname == parts.hostname,
                    before_request=lambda target: pace(target, settings.web_crawl_delay_seconds),
                )
            except ParseError as exc:
                # Keep security/timeout errors explicit rather than masking SSRF failures.
                if exc.code in {"url_forbidden", "invalid_url", "web_timeout"}:
                    raise
                raise ParseError(
                    "robots_unavailable",
                    f"Could not safely read robots.txt ({exc.code}); ingestion is denied",
                ) from None
            if result.status in {404, 410}:
                policy = Protego.parse("User-agent: *\nDisallow:\n")
            elif 200 <= result.status < 300:
                try:
                    rules = result.body.decode("utf-8-sig", errors="strict")
                    if len(rules.splitlines()) > settings.max_parse_nodes:
                        raise ParseError("robots_unavailable", "robots.txt exceeds the rule limit")
                    policy = Protego.parse(rules)
                except UnicodeError:
                    raise ParseError("robots_unavailable", "robots.txt must be UTF-8") from None
            else:
                raise ParseError(
                    "robots_denied", "robots.txt denied access or returned an unavailable status"
                )
            delay = max(settings.web_crawl_delay_seconds, policy.crawl_delay(USER_AGENT) or 0)
            rate = policy.request_rate(USER_AGENT)
            if rate:
                delay = max(delay, rate.seconds / max(rate.requests, 1))
            policies[origin] = policy, delay
        policy, delay = policies[origin]
        if not policy.can_fetch(url, USER_AGENT):
            raise ParseError("robots_denied", "robots.txt disallows this page")
        pace(url, delay)

    queue = deque([(seed, 0, requested_url)])
    scheduled = {seed}
    final_urls = set()
    documents = []
    skipped = []
    attempts = 0
    queue_limit = settings.web_max_pages * settings.web_max_links
    # Bound frontier independently of attacker-controlled link breadth.
    queue_limit = min(queue_limit, 5000)
    truncated = False
    while queue and attempts < config.max_pages:
        remaining(deadline)
        url, depth, original = queue.popleft()
        if url in final_urls:
            continue
        attempts += 1
        try:
            result = fetch(
                original,
                settings,
                deadline,
                allowed=(
                    None
                    if config.scope == "page"
                    else lambda target: in_scope(target, seed, config.scope)
                ),
                before_request=before_page,
            )
            if not 200 <= result.status < 300:
                raise ParseError("http_status", "Website returned an unsuccessful HTTP status")
            if result.final_url in final_urls:
                continue
            document, links = parse_html(result, settings)
        except ParseError as exc:
            if not documents or exc.code in {"web_timeout", "crawl_limit_exceeded"}:
                raise
            skipped.append({"url": url, "error_code": exc.code})
            continue
        final_urls.add(result.final_url)
        document.extra_metadata["crawl_depth"] = depth
        documents.append(document)
        if (
            len(json.dumps([asdict(doc) for doc in documents], ensure_ascii=False).encode())
            > settings.web_max_output_bytes
        ):
            raise ParseError(
                "crawl_output_too_large", "Crawl exceeds the total normalized output limit"
            )
        if config.scope != "page" and depth < config.max_depth:
            for target in links:
                if target not in scheduled and in_scope(target, seed, config.scope):
                    if len(scheduled) >= queue_limit:
                        truncated = True
                        break
                    scheduled.add(target)
                    queue.append((target, depth + 1, target))
    metadata = {
        "scope": config.scope,
        "attempted_pages": attempts,
        "documents": len(documents),
        "skipped_pages": skipped,
        "frontier_truncated": truncated,
        "page_limit_reached": bool(queue and attempts >= config.max_pages),
        "robots_policy": "respect; missing 404/410 allows; other failures deny",
        "concurrency": 1,
    }
    return CrawlResult(documents, metadata)
