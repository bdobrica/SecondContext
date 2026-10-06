"""Parser-neutral semantic blocks shared by every parser and downstream chunking."""

import hashlib
from dataclasses import asdict, dataclass, field
from typing import Any


@dataclass
class Block:
    type: str
    text: str
    heading_path: list[str] = field(default_factory=list)
    level: int | None = None
    path: str | None = None  # JSON Pointer; the empty string represents the root.
    page_start: int | None = None
    page_end: int | None = None


@dataclass
class ParsedDocument:
    format: str
    text: str
    blocks: list[Block]
    title: str | None = None
    extra_metadata: dict[str, Any] = field(default_factory=dict)
    # Parsers may know a URL; ingestion binds a stable source URI for uploaded/pasted input.
    uri: str | None = None

    @property
    def content_hash(self) -> str:
        return hashlib.sha256(self.text.encode()).hexdigest()

    def metadata(self) -> dict[str, Any]:
        return {
            "parser_version": 1,
            "representation_version": 1,
            "title": self.title,
            "uri": self.uri,
            "format": self.format,
            **self.extra_metadata,
            "blocks": [asdict(block) for block in self.blocks],
        }
