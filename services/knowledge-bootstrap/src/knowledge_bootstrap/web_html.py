"""Static HTML to the shared semantic document representation; no rendering."""

import codecs
import re
from email.message import Message
from urllib.parse import urljoin

from lxml import etree, html

from knowledge_bootstrap.config import Settings
from knowledge_bootstrap.parsers import Block, ParsedDocument, ParseError, validate_document
from knowledge_bootstrap.web_fetch import FetchResult
from knowledge_bootstrap.web_urls import normalize_url

CHROME = {
    "script",
    "style",
    "noscript",
    "nav",
    "header",
    "footer",
    "aside",
    "form",
    "template",
    "svg",
    "canvas",
    "iframe",
}
BLOCK_TAGS = {"p", "pre", "li", "table", "blockquote", "h1", "h2", "h3", "h4", "h5", "h6"}


def _text(element) -> str:
    return re.sub(r"\s+", " ", " ".join(element.itertext())).strip()


def parse_html(result: FetchResult, settings: Settings) -> tuple[ParsedDocument, list[str]]:
    if len(result.body) > settings.web_max_response_bytes:
        raise ParseError("response_too_large", "HTML exceeds the response byte limit")
    if not result.body.strip():
        raise ParseError(
            "html_text_unavailable", "Page has no static text; JavaScript rendering is unsupported"
        )
    message = Message()
    message["content-type"] = result.content_type
    encoding = message.get_content_charset()
    if encoding:
        try:
            codecs.lookup(encoding)
        except LookupError:
            raise ParseError("invalid_html", "HTML declares an unsupported encoding") from None
    try:
        parser = html.HTMLParser(encoding=encoding, no_network=True, huge_tree=False, recover=True)
        root = html.document_fromstring(result.body, parser=parser)
        if any("excessive depth" in error.message.lower() for error in parser.error_log):
            raise ParseError("html_resource_limit", "HTML exceeds the parser depth limit")
    except (etree.LxmlError, ValueError, LookupError):
        raise ParseError("invalid_html", "Malformed or unsupported HTML") from None
    count = 0
    stack = [(root, 0)]
    while stack:
        element, depth = stack.pop()
        count += 1
        if count > settings.max_parse_nodes or depth > settings.max_parse_depth:
            raise ParseError("html_resource_limit", "HTML exceeds the node or nesting limit")
        stack.extend((child, depth + 1) for child in element)
    titles = root.xpath("//title")
    title = _text(titles[0])[:500] if titles else None
    canonical = None
    for element in root.xpath("//link[@href]"):
        if "canonical" in element.get("rel", "").lower().split():
            try:
                canonical = normalize_url(urljoin(result.final_url, element.get("href")))
            except ParseError:
                pass
            break
    # Links are collected before removing navigation, but <base> is deliberately
    # ignored: neither base nor rel=canonical changes identity or authorizes fetches.
    links = []
    seen = set()
    for element in root.iter("a"):
        value = element.get("href")
        if not value:
            continue
        try:
            value = normalize_url(urljoin(result.final_url, value))
        except ParseError:
            continue
        if value not in seen:
            seen.add(value)
            links.append(value)
        if len(links) >= settings.web_max_links:
            break
    for element in list(root.iter()):
        if (
            isinstance(element.tag, str)
            and (
                element.tag.lower() in CHROME
                or "hidden" in element.attrib
                or element.get("aria-hidden", "").lower() == "true"
                or re.search(
                    r"(?:display\s*:\s*none|visibility\s*:\s*hidden)",
                    element.get("style", ""),
                    re.I,
                )
            )
            and element.getparent() is not None
        ):
            element.drop_tree()
    candidates = (
        root.xpath("//main | //*[@role='main']") or root.xpath("//article") or root.xpath("//body")
    )
    content = max(candidates, key=lambda element: len(_text(element))) if candidates else root
    blocks = []
    headings: list[tuple[int, str]] = []

    def add(kind: str, text: str, level=None):
        if not text:
            return
        if kind == "heading":
            while headings and headings[-1][0] >= level:
                headings.pop()
            headings.append((level, text))
        blocks.append(Block(kind, text, [text for _, text in headings], level=level))

    def visit(element):
        tag = element.tag.lower() if isinstance(element.tag, str) else ""
        if tag in BLOCK_TAGS:
            if tag == "pre":
                text = "".join(element.itertext()).replace("\r\n", "\n").replace("\r", "\n").strip()
                add("code", text)
            elif tag == "table":
                rows = []
                for row in element.iter("tr"):
                    cells = [
                        _text(cell).replace("|", "\\|") for cell in row if cell.tag in {"td", "th"}
                    ]
                    if cells:
                        rows.append(" | ".join(cells))
                add("table", "\n".join(rows))
            elif tag.startswith("h") and len(tag) == 2:
                add("heading", _text(element), int(tag[1]))
            else:
                add(
                    "list" if tag == "li" else "paragraph",
                    ("- " if tag == "li" else "") + _text(element),
                )
            return
        # Retain bare text in wrappers as paragraphs without duplicating descendant blocks.
        pending = element.text or ""
        for child in element:
            if not isinstance(child.tag, str):
                pending += child.tail or ""
                continue
            if child.tag.lower() in BLOCK_TAGS or any(
                desc.tag in BLOCK_TAGS for desc in child.iterdescendants()
            ):
                add("paragraph", re.sub(r"\s+", " ", pending).strip())
                pending = ""
                visit(child)
            else:
                pending += " " + _text(child)
            pending += " " + (child.tail or "")
        add("paragraph", re.sub(r"\s+", " ", pending).strip())

    visit(content)
    text = "\n\n".join(block.text for block in blocks)
    if sum(char.isalnum() for char in text) < settings.web_min_text_chars:
        raise ParseError(
            "html_text_unavailable",
            "Too little static text; page may require JavaScript or contain unsupported content",
        )
    metadata = {
        **result.metadata(),
        "canonical_url": canonical or result.final_url,
        "links": links,
        # Conservatively mark an exactly-full list as capped as well. Refresh must
        # not infer absence from a frontier whose discovery was bounded.
        "link_limit_reached": len(links) >= settings.web_max_links,
    }
    document = ParsedDocument("html", text, blocks, title, metadata)
    return validate_document(document, settings), links
