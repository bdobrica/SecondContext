"""Upload selection and bounded subprocess boundary for local binary parsers."""

import json
import os
import signal
import subprocess
import sys
from dataclasses import asdict
from pathlib import Path
from tempfile import TemporaryDirectory

from knowledge_bootstrap.config import Settings
from knowledge_bootstrap.parsers import Block, ParsedDocument, ParseError, validate_document

MIME_TYPES = {
    "pdf": "application/pdf",
    "docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
}
LIMIT_FIELDS = (
    "max_file_bytes",
    "parser_timeout_seconds",
    "parser_memory_bytes",
    "max_pdf_pages",
    "min_pdf_text_chars",
    "max_pdf_stream_bytes",
    "max_document_objects",
    "max_archive_members",
    "max_decompressed_bytes",
    "max_compression_ratio",
    "max_normalized_bytes",
    "max_parse_depth",
    "max_parse_nodes",
)


def select_upload_format(data: bytes, filename: str, mime: str | None, override: str) -> str:
    """Signatures select binary candidates; conflicting PDF/DOCX hints are rejected.

    ZIP signatures are only candidates. Full DOCX package validation belongs to the
    isolated worker. Generic/text MIME and suffixes remain untrusted provenance.
    """
    actual = (
        "pdf"
        if data.startswith(b"%PDF-")
        else "docx"
        if data[:4] in {b"PK\x03\x04", b"PK\x05\x06", b"PK\x07\x08"}
        else None
    )
    extension = filename.rsplit(".", 1)[-1].lower()
    mime = (mime or "").split(";", 1)[0].strip().lower()
    hints = {extension} & MIME_TYPES.keys()
    hints.update(fmt for fmt, content_type in MIME_TYPES.items() if mime == content_type)
    if override in MIME_TYPES:
        hints.add(override)
    if hints and (actual is None or hints != {actual}):
        raise ParseError("file_type_mismatch", "File bytes do not match the PDF/DOCX format hints")
    if actual and override not in {"auto", actual}:
        raise ParseError(
            "file_type_mismatch", "Binary file bytes conflict with the format override"
        )
    return actual or override


def parse_binary(data: bytes, fmt: str, settings: Settings) -> ParsedDocument:
    """No parser runs inside the API process. Timeout kills and reaps the child.

    Only parsing limits and temporary paths cross this boundary, never DB/auth settings.
    The child has bounded address space, CPU, output files and no core dumps on Linux.
    Temporary files are private and removed after success, failure or timeout.
    """
    if len(data) > settings.max_file_bytes:
        raise ParseError("input_too_large", "File exceeds the upload byte limit")
    if fmt not in MIME_TYPES:
        raise ParseError("unsupported_format", "Supported binary formats are PDF and DOCX")
    if os.name != "posix":
        raise ParseError(
            "parser_unavailable", "Binary parsing requires Linux/POSIX resource limits"
        )
    limits = {field: getattr(settings, field) for field in LIMIT_FIELDS}
    with TemporaryDirectory(prefix="knowledge-parse-") as directory:
        original, result_path = Path(directory) / "input", Path(directory) / "result.json"
        original.write_bytes(data)
        try:
            result = subprocess.run(
                [
                    sys.executable,
                    "-m",
                    "knowledge_bootstrap.binary_worker",
                    fmt,
                    json.dumps(limits),
                    str(original),
                    str(result_path),
                ],
                stdin=subprocess.DEVNULL,
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL,  # Third-party messages may include submitted content.
                env={
                    "PATH": os.defpath,
                    "LANG": "C.UTF-8",
                    "PYTHONIOENCODING": "utf-8",
                    "PYTHONHASHSEED": "0",
                },
                timeout=settings.parser_timeout_seconds,
                check=False,
            )
        except subprocess.TimeoutExpired:
            raise ParseError("parser_timeout", "Document parser exceeded its time limit") from None
        except OSError:
            raise ParseError("parser_unavailable", "Document parser could not be started") from None
        if result.returncode:
            if result.returncode == -signal.SIGXCPU:
                raise ParseError("parser_timeout", "Document parser exceeded its CPU limit")
            raise ParseError("parser_resource_limit", "Document parser exceeded resource limits")
        # Bound IPC too, including metadata and any error response.
        try:
            with result_path.open("rb") as output:
                encoded = output.read(settings.max_normalized_bytes + 65_537)
            payload = json.loads(encoded)
        except (OSError, ValueError):
            raise ParseError(
                "parser_failed", "Document parser did not return a valid result"
            ) from None
        if len(encoded) > settings.max_normalized_bytes + 65_536:
            raise ParseError("normalized_too_large", "Parser result exceeds the output byte limit")
        if "error" in payload:
            raise ParseError(**payload["error"])
        payload["blocks"] = [Block(**block) for block in payload["blocks"]]
        return validate_document(ParsedDocument(**payload), settings)


def document_payload(document: ParsedDocument) -> dict:
    return asdict(document)
