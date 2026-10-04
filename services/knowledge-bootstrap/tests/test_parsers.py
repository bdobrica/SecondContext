from pathlib import Path

import pytest

from knowledge_bootstrap.parsers import ParseError, detect_format, normalize_input, parse_text

FIXTURES = Path(__file__).parent / "fixtures" / "text"


@pytest.mark.parametrize(
    "fmt,filename",
    [
        ("text", "handbook.txt"),
        ("markdown", "handbook.md"),
        ("json", "policy.json"),
        ("yaml", "policy.yaml"),
    ],
)
def test_fixtures_and_repeatable_normalization(settings, fmt, filename):
    raw = (FIXTURES / filename).read_bytes()
    first = parse_text(raw, "auto", settings)
    assert first == parse_text(raw, fmt, settings) == parse_text(raw, "auto", settings)
    assert first.format == fmt
    assert first.content_hash == parse_text(raw.replace(b"\n", b"\r\n"), fmt, settings).content_hash
    assert first.blocks


def test_paragraphs_and_meaningful_blank_lines(settings):
    document = parse_text((FIXTURES / "handbook.txt").read_bytes(), "text", settings)
    assert "\n\n\n" in document.text
    assert len(document.blocks) == 2
    assert document.blocks[0].text.endswith("rollout.")


def test_markdown_heading_list_and_code_provenance(settings):
    document = parse_text((FIXTURES / "handbook.md").read_bytes(), "markdown", settings)
    assert document.title == "Engineering"
    code = next(block for block in document.blocks if block.type == "code")
    assert code.heading_path == ["Engineering", "Deployment", "Commands"]
    assert code.text.startswith("```bash") and "\n\n# This" in code.text
    assert code.text.endswith("```")
    assert next(block for block in document.blocks if block.type == "list").text.startswith(
        "- Verify"
    )
    assert document.blocks[-1].heading_path == ["Engineering", "Rollback"]
    assert len([block for block in document.blocks if block.type == "heading"]) == 4


def test_setext_headings_and_indented_code(settings):
    document = parse_text("Title\n=====\n\n    code\n    more code\n", "auto", settings)
    assert document.format == "markdown"
    assert document.blocks[0].type == "heading"
    assert document.blocks[-1].type == "code"


def test_structured_fidelity_paths_and_cross_format_normalization(settings):
    json_doc = parse_text((FIXTURES / "policy.json").read_bytes(), "json", settings)
    yaml_doc = parse_text((FIXTURES / "policy.yaml").read_bytes(), "yaml", settings)
    assert json_doc.text == yaml_doc.text
    assert json_doc.blocks == yaml_doc.blocks
    assert json_doc.content_hash == yaml_doc.content_hash
    paths = {block.path for block in json_doc.blocks}
    assert {"/deployment/approvers", "/deployment/commands/1", "/a~1b/~0key", "/notes"} <= paths
    assert (
        parse_text('{"z":2,"a":1}', "json", settings).text
        == parse_text('{"a":1,"z":2}', "json", settings).text
    )


@pytest.mark.parametrize(
    "raw,fmt",
    [
        ("Just a sentence.", "text"),
        ("Note: restart the service.", "text"),
        ("https://example.com/path", "text"),
        ("true", "text"),
        ("123", "text"),
        ("- check\n- deploy", "markdown"),
        ("port: 8090\nhost: localhost", "yaml"),
        ("---\nport: 8090", "yaml"),
        ('{"broken":', "json"),
        ("[unfinished", "json"),
        ("# Title", "markdown"),
    ],
)
def test_ambiguous_format_policy(raw, fmt):
    assert detect_format(raw) == fmt


def test_explicit_override_and_scalar_formats(settings):
    assert parse_text("# Title", "text", settings).blocks[0].type == "paragraph"
    assert parse_text("port: 8090", "yaml", settings).blocks[0].path == "/port"
    assert parse_text("true", "json", settings).text == "true"
    assert parse_text("2026-10-04", "yaml", settings).text == '"2026-10-04"'


@pytest.mark.parametrize(
    "raw,code",
    [
        (b"\xffbad", "invalid_encoding"),
        (b"abc\x00def", "invalid_text"),
        (b"\x01text", "invalid_text"),
        ("\ud800", "invalid_encoding"),
        (" \n\t", "empty_input"),
    ],
)
def test_invalid_text_and_encoding(settings, raw, code):
    with pytest.raises(ParseError) as exc:
        parse_text(raw, "auto", settings)
    assert exc.value.code == code


def test_input_bytes_and_bom(settings):
    assert normalize_input(b"\xef\xbb\xbfhello\r\nworld\r", settings) == "hello\nworld\n"
    small = settings.model_copy(update={"max_input_bytes": 5})
    with pytest.raises(ParseError, match="byte limit"):
        parse_text("ééé", "text", small)


@pytest.mark.parametrize(
    "raw,fmt,code",
    [
        ('{"x":', "json", "invalid_json"),
        ('{"x":1,"x":2}', "json", "invalid_json"),
        ('{"x":NaN}', "json", "invalid_json"),
        ('{"x":1e999}', "json", "invalid_json"),
        ('{"x":"\\u0000"}', "json", "invalid_text"),
        ('{"\\ud800":1}', "json", "invalid_encoding"),
        ("key: [", "yaml", "invalid_yaml"),
        ("x: 1\nx: 2", "yaml", "invalid_yaml"),
        ("!!python/object/apply:os.system ['echo secret']", "yaml", "invalid_yaml"),
        ("!!binary SGVsbG8=", "yaml", "invalid_yaml"),
        ("!!set {x: null}", "yaml", "invalid_yaml"),
        ("1: value", "yaml", "invalid_yaml"),
        ("x: .inf", "yaml", "invalid_yaml"),
        ("x: &x [*x]", "yaml", "yaml_recursive_alias"),
        ("x: &x {a: 1}\ny: {<<: *x}", "yaml", "invalid_yaml"),
        ("---\nx: 1\n---\nx: 2", "yaml", "invalid_yaml"),
    ],
)
def test_malformed_and_unsafe_structures(settings, raw, fmt, code):
    with pytest.raises(ParseError) as exc:
        parse_text(raw, fmt, settings)
    assert exc.value.code == code
    assert "secret" not in exc.value.detail


@pytest.mark.parametrize("fmt", ["json", "yaml"])
def test_depth_bounded_before_recursive_parse(settings, fmt):
    raw = "[" * 1000 + "0" + "]" * 1000
    with pytest.raises(ParseError) as exc:
        parse_text(raw, fmt, settings)
    assert exc.value.code == "nesting_too_deep"
    # Brackets inside JSON strings do not count toward nesting.
    assert parse_text('{"x":"' + "[" * 1000 + '"}', "json", settings).blocks


def test_yaml_alias_and_expansion_limits(settings):
    assert (
        parse_text("x: &x [1, 2]\ny: *x", "yaml", settings).text
        == parse_text('{"x":[1,2],"y":[1,2]}', "json", settings).text
    )
    limited = settings.model_copy(update={"max_yaml_aliases": 0})
    with pytest.raises(ParseError) as exc:
        parse_text("x: &x [1]\ny: *x", "yaml", limited)
    assert exc.value.code == "yaml_alias_limit"
    limited = settings.model_copy(update={"max_parse_nodes": 30})
    raw = "a: &a [1, 2, 3]\nb: &b [*a, *a, *a]\nc: [*b, *b, *b]"
    with pytest.raises(ParseError) as exc:
        parse_text(raw, "yaml", limited)
    assert exc.value.code == "structure_too_large"


def test_node_and_output_bounds(settings):
    limited = settings.model_copy(update={"max_parse_nodes": 3})
    with pytest.raises(ParseError) as exc:
        parse_text("[1,2,3,4]", "json", limited)
    assert exc.value.code == "structure_too_large"
    limited = settings.model_copy(update={"max_normalized_bytes": 100})
    with pytest.raises(ParseError) as exc:
        parse_text("hello" * 40, "text", limited)
    assert exc.value.code == "normalized_too_large"
    with pytest.raises(ParseError) as exc:
        parse_text('a: &a "' + "hello" * 15 + '"\nb: [*a, *a]', "yaml", limited)
    assert exc.value.code == "normalized_too_large"


@pytest.mark.parametrize("fmt", ["json", "yaml"])
def test_empty_strings_keys_and_whitespace_values_are_faithful(settings, fmt):
    raw = '{"": "", "spaces": "  ", "newline": "\\n"}'
    document = parse_text(raw, fmt, settings)
    assert [block.path for block in document.blocks] == ["/", "/newline", "/spaces"]
    assert '"": ""' in document.text
    assert '"spaces": "  "' in document.text


def test_expanded_yaml_depth_is_bounded(settings):
    settings = settings.model_copy(update={"max_parse_depth": 2})
    with pytest.raises(ParseError) as exc:
        parse_text("a: &a []\nb: [*a]", "yaml", settings)
    assert exc.value.code == "nesting_too_deep"
