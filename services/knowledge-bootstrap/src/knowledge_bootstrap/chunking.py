"""Deterministic semantic packing; only oversized unstructured paragraphs overlap."""

import bisect
import hashlib
import json
from dataclasses import dataclass
from functools import lru_cache

import tiktoken

from knowledge_bootstrap.config import Settings
from knowledge_bootstrap.parsers import ParseError
from knowledge_bootstrap.representation import Block


@lru_cache(maxsize=1)
def tokenizer():
    return tiktoken.get_encoding("cl100k_base")


def token_count(text: str) -> int:
    # Source content can contain literal special-token strings; treat them as ordinary text.
    return len(tokenizer().encode_ordinary(text))


def recipe(settings: Settings) -> dict:
    return {
        "version": 1,
        "tokenizer": "cl100k_base",
        "target_tokens": settings.chunk_target_tokens,
        "max_tokens": settings.chunk_max_tokens,
        "overlap_tokens": settings.chunk_overlap_tokens,
        "max_chars": 16384,
        "split_encoding_window_chars": 4096,
    }


@dataclass
class ChunkSpec:
    text: str
    heading_path: list[str]
    page_start: int | None
    page_end: int | None
    token_count: int
    content_hash: str
    metadata_json: dict


def split_long(text: str, limit: int, overlap: int):
    """Split at token-safe UTF-8 boundaries, preferring nearby line/word boundaries.

    Byte slices never replace or drop Unicode. A suffix is repeated only when requested;
    returned overlap counts describe the actual repeated text, not a guessed token count.
    """
    encoding = tokenizer()
    # Cap each BPE operation: a very long identifier/repeated character run otherwise
    # has pathological tokenization cost. Concatenated windows decode losslessly;
    # each emitted piece is recounted with the exact tokenizer below.
    ids = [
        token
        for i in range(0, len(text), 4096)
        for token in encoding.encode_ordinary(text[i : i + 4096])
    ]
    pieces = [encoding.decode_single_token_bytes(i) for i in ids]
    raw = b"".join(pieces)
    offsets = [0]
    for part in pieces:
        offsets.append(offsets[-1] + len(part))
    start = 0
    repeated = 0

    while start < len(raw):
        token_start = bisect.bisect_right(offsets, start) - 1
        stop = min(offsets[min(token_start + limit, len(ids))], start + 16384)
        # A Unicode code point may span several BPE tokens.
        while stop > start:
            try:
                value = raw[start:stop].decode("utf-8")
                break
            except UnicodeDecodeError:
                stop -= 1
        else:
            raise ParseError("chunking_failed", "Unable to split a Unicode block")
        if stop < len(raw):
            # Prefer lines, then words; retain the delimiter so concatenation is faithful.
            floor = len(value) * 3 // 4
            boundary = value.rfind("\n", floor)
            if boundary < floor:
                boundary = value.rfind(" ", floor)
            if boundary >= floor:
                value = value[: boundary + 1]
                stop = start + len(value.encode())
        while token_count(value) > limit:
            value = value[:-1]
            stop = start + len(value.encode())
        yield value, repeated
        if stop == len(raw):
            break
        next_start = stop
        repeated = 0
        if overlap:
            suffix_ids = encoding.encode_ordinary(value)[-overlap:]
            suffix = encoding.decode_bytes(suffix_ids)
            # Trim an incomplete leading Unicode code point.
            while suffix:
                try:
                    suffix_text = suffix.decode("utf-8")
                    break
                except UnicodeDecodeError:
                    suffix = suffix[1:]
            if suffix and len(suffix) < stop - start:
                next_start -= len(suffix)
                repeated = token_count(suffix_text)
        start = next_start


def chunk_blocks(blocks: list[Block], settings: Settings) -> list[ChunkSpec]:
    result = []
    current: list[tuple[Block, str, int, int]] = []
    ancestry: list[str] = []
    config = recipe(settings)

    def emit():
        if not current:
            return
        text = "\n\n".join(piece for _, piece, _, _ in current)
        pages = [p for b, _, _, _ in current for p in (b.page_start, b.page_end) if p is not None]
        metadata = {
            "chunking": config,
            "blocks": [
                {"index": index, "type": b.type, "path": b.path, "overlap_tokens": repeated}
                for b, _, index, repeated in current
            ],
        }
        identity = {"text": text, "heading_path": ancestry, "pages": pages}
        digest = hashlib.sha256(
            json.dumps(identity, ensure_ascii=False, sort_keys=True).encode()
        ).hexdigest()
        result.append(
            ChunkSpec(
                text,
                ancestry.copy(),
                min(pages) if pages else None,
                max(pages) if pages else None,
                token_count(text),
                digest,
                metadata,
            )
        )
        if len(result) > settings.max_chunks_per_source:
            raise ParseError("chunk_limit_exceeded", "Source exceeds the canonical chunk limit")
        current.clear()

    for index, block in enumerate(blocks):
        if not block.text.strip():
            continue
        if current and block.heading_path != ancestry:
            emit()
        ancestry = block.heading_path.copy()
        size = (
            token_count(block.text) if len(block.text) <= 16384 else settings.chunk_max_tokens + 1
        )
        if size > settings.chunk_max_tokens:
            emit()
            # Code/table/list/structured blocks split only when unavoidable, without overlap.
            unstructured = block.type == "paragraph" and not ancestry and block.path is None
            overlap = settings.chunk_overlap_tokens if unstructured else 0
            for part, repeated in split_long(block.text, settings.chunk_target_tokens, overlap):
                current.append((block, part, index, repeated))
                emit()
            continue
        candidate = "\n\n".join([*(piece for _, piece, _, _ in current), block.text])
        if current and (
            len(candidate) > 16384
            or token_count(candidate) > settings.chunk_max_tokens
            or token_count("\n\n".join(piece for _, piece, _, _ in current))
            >= settings.chunk_target_tokens
        ):
            emit()
        current.append((block, block.text, index, 0))
    emit()
    if not result:
        raise ParseError("chunking_empty", "Document contains no usable semantic blocks")
    return result
