from dataclasses import asdict

import pytest
from pydantic import ValidationError

from knowledge_bootstrap.chunking import chunk_blocks, split_long, token_count
from knowledge_bootstrap.parsers import ParseError, parse_text
from knowledge_bootstrap.representation import Block


def small(settings, **overrides):
    return settings.model_copy(
        update={
            "chunk_target_tokens": 48,
            "chunk_max_tokens": 96,
            "chunk_overlap_tokens": 8,
            **overrides,
        }
    )


def test_tiny_sections_and_deep_ancestry(settings):
    parsed = parse_text(
        "# Root\nIntro.\n## One\nTiny.\n### Two\nDeeper.\n## End\nLast.", "markdown", settings
    )
    chunks = chunk_blocks(parsed.blocks, settings)
    assert [c.heading_path for c in chunks] == [
        ["Root"],
        ["Root", "One"],
        ["Root", "One", "Two"],
        ["Root", "End"],
    ]
    assert chunks[1].text == "One\n\nTiny."
    assert all(c.token_count == token_count(c.text) <= 1200 for c in chunks)
    assert all(b["overlap_tokens"] == 0 for c in chunks for b in c.metadata_json["blocks"])


def test_oversized_section_preserves_ancestry(settings):
    blocks = [Block("heading", "Policy", ["Policy"], level=1)] + [
        Block("paragraph", f"Rule {i}: deploy safely. " * 8, ["Policy"]) for i in range(8)
    ]
    chunks = chunk_blocks(blocks, small(settings))
    assert len(chunks) > 1
    assert all(c.heading_path == ["Policy"] for c in chunks)
    assert all(c.token_count <= 96 for c in chunks)
    assert "\n\n".join(c.text for c in chunks) == "\n\n".join(b.text for b in blocks)


@pytest.mark.parametrize("kind", ["code", "table", "list", "structured"])
def test_atomic_blocks_and_unavoidable_splits(settings, kind):
    short = "a | b\nkeep rows" if kind == "table" else "retain indentation\n    value = 12"
    chunks = chunk_blocks([Block(kind, short, ["Section"])], small(settings))
    assert len(chunks) == 1 and chunks[0].text == short
    long = short * 100
    chunks = chunk_blocks([Block(kind, long, ["Section"], path="/key")], small(settings))
    assert len(chunks) > 1
    assert "".join(c.text for c in chunks) == long
    assert all(c.token_count <= 48 for c in chunks)
    assert all(
        b["path"] == "/key" and b["overlap_tokens"] == 0
        for c in chunks
        for b in c.metadata_json["blocks"]
    )


def test_paragraph_packing_and_page_range(settings):
    blocks = [
        Block("paragraph", "First page.", page_start=1, page_end=1),
        Block("paragraph", "Second page.", page_start=2, page_end=2),
    ]
    chunks = chunk_blocks(blocks, settings)
    assert len(chunks) == 1
    assert chunks[0].page_start == 1 and chunks[0].page_end == 2
    assert chunks[0].text == "First page.\n\nSecond page."
    blocks[1].text = "Second page " * 100
    chunks = chunk_blocks(blocks, small(settings))
    assert chunks[0].page_start == chunks[0].page_end == 1
    assert all(c.page_start == c.page_end == 2 for c in chunks[1:])


@pytest.mark.parametrize(
    "text",
    [
        "Sentence with words. " * 300,
        "🧠汉字é" * 300,
        "X" * 5000,
        "<|endoftext|>" * 300,
        "  \n" * 3000,
    ],
)
def test_unicode_lossless_split_and_hard_budget(settings, text):
    pieces = list(split_long(text, 48, 0))
    assert "".join(value for value, _ in pieces) == text
    assert all(0 < token_count(value) <= 48 for value, _ in pieces)
    assert all("\ufffd" not in value for value, _ in pieces)


def test_overlap_only_for_long_unstructured_paragraph(settings):
    text = "Long paragraph context words. " * 300
    chunks = chunk_blocks([Block("paragraph", text)], small(settings))
    assert len(chunks) > 1
    assert chunks[0].metadata_json["blocks"][0]["overlap_tokens"] == 0
    assert all(0 < c.metadata_json["blocks"][0]["overlap_tokens"] <= 8 for c in chunks[1:])
    for left, right in zip(chunks, chunks[1:], strict=False):
        assert any(left.text.endswith(right.text[:i]) for i in range(1, 100))
    structured = chunk_blocks([Block("paragraph", text, ["Heading"])], small(settings))
    assert all(c.metadata_json["blocks"][0]["overlap_tokens"] == 0 for c in structured)


def test_determinism_hash_and_recipe(settings):
    blocks = [Block("paragraph", "Repeated text."), Block("paragraph", "Repeated text.")]
    first = chunk_blocks(blocks, settings)
    assert [asdict(c) for c in first] == [asdict(c) for c in chunk_blocks(blocks, settings)]
    assert first[0].metadata_json["chunking"]["tokenizer"] == "cl100k_base"
    changed = chunk_blocks([Block("paragraph", "Changed text.")], settings)
    assert changed[0].content_hash != first[0].content_hash
    located = chunk_blocks([Block("paragraph", first[0].text, page_start=1, page_end=1)], settings)
    assert located[0].content_hash != first[0].content_hash


def test_empty_and_chunk_limit(settings):
    with pytest.raises(ParseError, match="no usable"):
        chunk_blocks([], settings)
    with pytest.raises(ParseError) as exc:
        chunk_blocks(
            [Block("paragraph", "words " * 1000)], small(settings, max_chunks_per_source=1)
        )
    assert exc.value.code == "chunk_limit_exceeded"


@pytest.mark.parametrize(
    "changes",
    [
        {"chunk_target_tokens": 1300},
        {"chunk_target_tokens": 32, "chunk_overlap_tokens": 32},
        {"qdrant_collection": "../memory_items"},
        {"embedding_dimensions": 5, "embedding_request_dimensions": 6},
    ],
)
def test_settings_validate_recipe(settings, changes):
    with pytest.raises(ValidationError):
        type(settings).model_validate({**settings.model_dump(), **changes})


def test_long_identifier_tokenization_and_character_ceiling(settings):
    text = "X" * 524288
    chunks = chunk_blocks([Block("code", text, ["Code"])], settings)
    assert "".join(c.text for c in chunks) == text
    assert all(len(c.text) <= 16384 and c.token_count <= 1200 for c in chunks)
