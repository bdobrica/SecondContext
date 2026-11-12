"""Meeting notes reuse ingestion parsers; personal imports leave no reference copy."""

from datetime import date
from typing import Annotated, Literal

from fastapi import File, Form, Header, UploadFile

from knowledge_bootstrap.binary import MIME_TYPES, parse_binary, select_upload_format
from knowledge_bootstrap.parsers import ParseError, normalize_input, parse_text
from knowledge_bootstrap.schemas import SourceCreate, UploadFormat
from knowledge_bootstrap.service import ServiceError, create_source


def install_notes(app, settings, owner_dependency, database_dependency):
    @app.post("/v1/notes")
    def ingest_note(
        owner: owner_dependency,
        session: database_dependency,
        purpose: Annotated[Literal["personal", "reference"], Form()],
        name: Annotated[str, Form(min_length=1, max_length=500)],
        format: Annotated[UploadFormat, Form()] = "text",
        text: Annotated[str | None, Form()] = None,
        meeting_date: Annotated[str, Form()] = "",
        file: Annotated[UploadFile | None, File()] = None,
        idempotency_key: Annotated[str | None, Header(min_length=1, max_length=128)] = None,
    ):
        if not name.strip() or (file is None) == (text is None):
            raise ServiceError("invalid_request", "Provide a name and either text or file", 422)
        if idempotency_key is not None and not idempotency_key.strip():
            raise ServiceError("invalid_request", "Idempotency-Key must not be blank", 422)
        if meeting_date:
            try:
                if date.fromisoformat(meeting_date).isoformat() != meeting_date:
                    raise ValueError
            except ValueError:
                raise ServiceError(
                    "invalid_request", "Meeting date must be YYYY-MM-DD", 422
                ) from None
        if file is not None:
            filename = file.filename or "upload.txt"
            if len(filename) > 8192 or "\x00" in filename:
                raise ServiceError("invalid_request", "Invalid filename", 422)
            data = file.file.read(max(settings.max_input_bytes, settings.max_file_bytes) + 1)
            selected = select_upload_format(data, filename, file.content_type, "auto")
            if selected not in MIME_TYPES:
                selected = format
            binary = selected in MIME_TYPES
            if binary and len(data) > settings.max_file_bytes:
                raise ParseError("input_too_large", "File exceeds the upload byte limit")
            normalized = None if binary else normalize_input(data, settings)
        else:
            filename, data, binary = None, None, False
            selected, normalized = format, normalize_input(text, settings)
            if selected in MIME_TYPES:
                raise ServiceError("invalid_request", "Binary formats require a file", 422)
        if purpose == "reference":
            source, job = create_source(
                session,
                owner,
                SourceCreate(
                    kind="file" if file else "text",
                    name=name.strip(),
                    source_uri=filename,
                    format=selected,
                    text=normalized,
                    metadata_json={"meeting_date": meeting_date},
                ),
                idempotency_key,
                input_bytes=data if binary else None,
            )
            return {"purpose": purpose, "source_id": str(source.id), "status": job.status}
        parsed = (
            parse_binary(data, selected, settings)
            if binary
            else parse_text(normalized, selected, settings)
        )
        # One bounded capture, never silently truncate a long meeting.
        if len(parsed.text.encode()) > 65536:
            raise ParseError("note_too_large", "Personal notes support 64 KiB of parsed text")
        return {"purpose": purpose, "text": parsed.text, "format": parsed.format}
