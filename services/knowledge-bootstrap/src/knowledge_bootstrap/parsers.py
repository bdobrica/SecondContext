"""Bounded, deterministic text parsers. No database, network or LLM dependencies."""

import hashlib
import json
import math
import re
from dataclasses import asdict, dataclass, field
from typing import Any

import yaml
from markdown_it import MarkdownIt
from yaml.events import AliasEvent, CollectionEndEvent, CollectionStartEvent, ScalarEvent

from knowledge_bootstrap.config import Settings


class ParseError(Exception):
    def __init__(self, code: str, detail: str):
        self.code = code
        self.detail = detail
        super().__init__(detail)


@dataclass
class Block:
    type: str
    text: str
    heading_path: list[str] = field(default_factory=list)
    level: int | None = None
    path: str | None = None  # JSON Pointer; the empty string represents the root.


@dataclass
class ParsedDocument:
    format: str
    text: str
    blocks: list[Block]
    title: str | None = None

    @property
    def content_hash(self) -> str:
        return hashlib.sha256(self.text.encode()).hexdigest()

    def metadata(self) -> dict[str, Any]:
        return {"parser_version": 1, "blocks": [asdict(block) for block in self.blocks]}


def normalize_input(value: str | bytes, settings: Settings, *, allow_blank: bool = False) -> str:
    try:
        raw = value if isinstance(value, bytes) else value.encode("utf-8", errors="strict")
        if len(raw) > settings.max_input_bytes:
            raise ParseError("input_too_large", "Text exceeds the input byte limit")
        text = raw.decode("utf-8-sig", errors="strict")
    except UnicodeError:
        raise ParseError("invalid_encoding", "Text must be valid UTF-8") from None
    if any((ord(c) < 32 and c not in "\n\r\t") or 127 <= ord(c) < 160 for c in text):
        raise ParseError("invalid_text", "Binary or control characters are not supported")
    text = text.replace("\r\n", "\n").replace("\r", "\n")
    if not allow_blank and not text.strip():
        raise ParseError("empty_input", "Text must not be empty")
    return text


def detect_format(text: str) -> str:
    stripped = text.lstrip()
    # A broken object/array still belongs to JSON: surface an error rather than fall back.
    if stripped.startswith(("{", "[")):
        return "json"
    if re.search(r"(?m)^(?: {0,3}#{1,6}\s| {0,3}(?:```|~~~)|.+\n {0,3}(?:={3,}|-{3,})\s*$)", text):
        return "markdown"
    mappings = re.findall(r"(?m)^\s*[\w.-]+:\s+\S", text)
    if stripped.startswith(("---\n", "%YAML ")) or len(mappings) >= 2:
        return "yaml"
    if re.search(r"(?m)^ {0,3}(?:[-*+] |\d+[.)] |> )", text):
        # Lists without other YAML cues are Markdown. Explicit yaml remains available.
        return "markdown"
    return "text"


def _json_depth(text: str, limit: int) -> None:
    depth = 0
    in_string = escaped = False
    for char in text:
        if in_string:
            if escaped:
                escaped = False
            elif char == "\\":
                escaped = True
            elif char == '"':
                in_string = False
        elif char == '"':
            in_string = True
        elif char in "[{":
            depth += 1
            if depth > limit:
                raise ParseError("nesting_too_deep", "Structured input exceeds the nesting limit")
        elif char in "]}":
            depth -= 1


def _unique_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ParseError("invalid_json", "Duplicate JSON mapping keys are not supported")
        result[key] = value
    return result


def _invalid_constant(value):
    raise ParseError("invalid_json", "Non-finite JSON numbers are not supported")


class TextSafeLoader(yaml.SafeLoader):
    """Safe JSON-compatible YAML, with duplicates and merge-key expansion rejected."""

    def construct_mapping(self, node, deep=False):
        mapping = {}
        for key_node, value_node in node.value:
            if key_node.tag == "tag:yaml.org,2002:merge":
                raise ParseError("invalid_yaml", "YAML merge keys are not supported")
            key = self.construct_object(key_node, deep=deep)
            if not isinstance(key, str):
                raise ParseError("invalid_yaml", "YAML mapping keys must be strings")
            if key in mapping:
                raise ParseError("invalid_yaml", "Duplicate YAML mapping keys are not supported")
            mapping[key] = self.construct_object(value_node, deep=deep)
        return mapping


# Keep dates as their original strings rather than silently converting them to Python objects.
TextSafeLoader.yaml_implicit_resolvers = {
    key: [(tag, pattern) for tag, pattern in rules if tag != "tag:yaml.org,2002:timestamp"]
    for key, rules in yaml.SafeLoader.yaml_implicit_resolvers.items()
}


def _yaml_value(text: str, settings: Settings):
    depth = nodes = aliases = 0
    try:
        for event in yaml.parse(text, Loader=TextSafeLoader):
            if isinstance(event, CollectionStartEvent):
                depth += 1
                nodes += 1
            elif isinstance(event, CollectionEndEvent):
                depth -= 1
            elif isinstance(event, ScalarEvent):
                nodes += 1
            elif isinstance(event, AliasEvent):
                aliases += 1
            if depth > settings.max_parse_depth:
                raise ParseError("nesting_too_deep", "YAML exceeds the nesting limit")
            if nodes > settings.max_parse_nodes:
                raise ParseError("structure_too_large", "YAML exceeds the node limit")
            if aliases > settings.max_yaml_aliases:
                raise ParseError("yaml_alias_limit", "YAML exceeds the alias limit")
        return yaml.load(text, Loader=TextSafeLoader)
    except (yaml.YAMLError, ValueError, OverflowError, RecursionError):
        # Parser messages contain submitted content; never expose those messages.
        raise ParseError("invalid_yaml", "Invalid or unsupported YAML") from None


def _bounded_value(value: Any, settings: Settings, fmt: str):
    """Clone shared YAML aliases with bounded expansion; reject cycles and lossy types."""
    count = 0
    scalar_bytes = 0
    ancestors: set[int] = set()

    def charge(text):
        nonlocal scalar_bytes
        normalize_input(text, settings, allow_blank=True)
        scalar_bytes += len(text.encode())
        if scalar_bytes > settings.max_normalized_bytes:
            raise ParseError(
                "normalized_too_large", "Expanded scalar text exceeds the output limit"
            )

    def visit(item, depth):
        nonlocal count
        count += 1
        if count > settings.max_parse_nodes:
            raise ParseError(
                "structure_too_large", "Structured input exceeds the expanded node limit"
            )
        if depth > settings.max_parse_depth:
            raise ParseError("nesting_too_deep", "Structured input exceeds the nesting limit")
        if isinstance(item, (dict, list)):
            if depth >= settings.max_parse_depth:
                raise ParseError("nesting_too_deep", "Structured input exceeds the nesting limit")
            if id(item) in ancestors:
                raise ParseError("yaml_recursive_alias", "Recursive YAML aliases are not supported")
            ancestors.add(id(item))
            try:
                if isinstance(item, list):
                    return [visit(child, depth + 1) for child in item]
                result = {}
                for key in sorted(item):
                    charge(key)
                    result[key] = visit(item[key], depth + 1)
                return result
            finally:
                ancestors.remove(id(item))
        if item is None or type(item) in {str, bool, int}:
            if isinstance(item, str):
                # Escaped NUL/surrogates are also unsuitable for canonical Postgres text.
                charge(item)
            return item
        if type(item) is float and math.isfinite(item):
            return item
        raise ParseError(f"invalid_{fmt}", "Only finite JSON-compatible values are supported")

    # Depth denotes the number of ancestor collections, including expanded aliases.
    return visit(value, 0)


def _structured(text: str, fmt: str, settings: Settings) -> ParsedDocument:
    if fmt == "json":
        _json_depth(text, settings.max_parse_depth)
        try:
            value = json.loads(
                text, object_pairs_hook=_unique_object, parse_constant=_invalid_constant
            )
        except (ValueError, RecursionError):
            raise ParseError("invalid_json", "Invalid JSON") from None
    else:
        value = _yaml_value(text, settings)
    value = _bounded_value(value, settings, fmt)
    normalized = json.dumps(value, sort_keys=True, indent=2, ensure_ascii=False, allow_nan=False)
    blocks = []
    block_bytes = 0

    def walk(item, path):
        nonlocal block_bytes
        # Long keys repeated in descendant paths can be much larger than the original input.
        block_bytes += len(path.encode())
        if block_bytes > settings.max_normalized_bytes:
            raise ParseError("normalized_too_large", "Structural paths exceed the output limit")
        if isinstance(item, dict) and item:
            for key, child in item.items():
                walk(child, path + "/" + key.replace("~", "~0").replace("/", "~1"))
        elif isinstance(item, list) and item:
            for index, child in enumerate(item):
                walk(child, f"{path}/{index}")
        else:
            rendered = json.dumps(item, ensure_ascii=False, allow_nan=False)
            block_bytes += len(rendered.encode())
            if block_bytes > settings.max_normalized_bytes:
                raise ParseError(
                    "normalized_too_large", "Structured blocks exceed the output limit"
                )
            blocks.append(Block("structured", f"{path or '/'}: {rendered}", path=path))

    walk(value, "")
    return ParsedDocument(fmt, normalized, blocks)


def _markdown(text: str, settings: Settings) -> ParsedDocument:
    tokens = MarkdownIt("commonmark", {"maxNesting": settings.max_parse_depth}).parse(text)
    if len(tokens) > settings.max_parse_nodes:
        raise ParseError("structure_too_large", "Markdown exceeds the node limit")
    lines = text.splitlines(keepends=True)
    blocks = []
    headings: list[tuple[int, str]] = []
    title = None
    covered_until = 0
    for index, token in enumerate(tokens):
        if token.map is None or token.map[0] < covered_until:
            continue
        start, end = token.map
        raw = "".join(lines[start:end]).rstrip("\n")
        if token.type == "heading_open":
            level = int(token.tag[1:])
            label = tokens[index + 1].content
            headings = [(depth, text) for depth, text in headings if depth < level]
            headings.append((level, label))
            title = title or label
            blocks.append(Block("heading", label, [text for _, text in headings], level=level))
        else:
            kind = {
                "fence": "code",
                "code_block": "code",
                "bullet_list_open": "list",
                "ordered_list_open": "list",
                "blockquote_open": "quote",
                "hr": "separator",
            }.get(token.type, "paragraph")
            blocks.append(Block(kind, raw, [text for _, text in headings]))
        covered_until = end
    return ParsedDocument("markdown", text, blocks, title)


def parse_text(value: str | bytes, fmt: str | None, settings: Settings) -> ParsedDocument:
    text = normalize_input(value, settings)
    fmt = detect_format(text) if fmt in {None, "auto"} else fmt
    if fmt in {"json", "yaml"}:
        document = _structured(text, fmt, settings)
    elif fmt == "markdown":
        document = _markdown(text, settings)
    elif fmt == "text":
        blocks = [
            Block("paragraph", part) for part in re.split(r"\n[ \t]*\n+", text) if part.strip()
        ]
        if len(blocks) > settings.max_parse_nodes:
            raise ParseError("structure_too_large", "Text exceeds the block limit")
        document = ParsedDocument("text", text, blocks)
    else:
        raise ParseError(
            "unsupported_format", "Supported text formats are text, markdown, json and yaml"
        )
    # Bound both readable content and the actual JSONB representation, including path repetition.
    size = len(document.text.encode()) + len(
        json.dumps(document.metadata(), ensure_ascii=False).encode()
    )
    if size > settings.max_normalized_bytes:
        raise ParseError(
            "normalized_too_large", "Normalized document exceeds the output byte limit"
        )
    return document
