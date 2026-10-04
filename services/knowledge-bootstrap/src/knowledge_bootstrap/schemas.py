from datetime import datetime
from typing import Any, Literal, Self
from urllib.parse import urlsplit
from uuid import UUID

from pydantic import BaseModel, ConfigDict, Field, field_validator, model_validator

from knowledge_bootstrap.models import SourceKind, Stage

Format = Literal["html", "pdf", "docx", "markdown", "json", "yaml", "text"]
TextFormat = Literal["auto", "markdown", "json", "yaml", "text"]


class SourceCreate(BaseModel):
    model_config = ConfigDict(extra="forbid")

    kind: SourceKind
    name: str = Field(default="Untitled", min_length=1, max_length=500)
    source_uri: str | None = Field(default=None, max_length=8192)
    content_type: str | None = Field(default=None, max_length=200)
    format: Format | Literal["auto"] | None = None
    config_json: dict[str, Any] = Field(default_factory=dict)
    metadata_json: dict[str, Any] = Field(default_factory=dict)
    text: str | None = Field(default=None, max_length=8_388_608)

    @field_validator("name")
    @classmethod
    def validate_name(cls, value: str) -> str:
        if not value.strip():
            raise ValueError("name must not be blank")
        return value.strip()

    @model_validator(mode="after")
    def validate_origin(self) -> Self:
        if self.kind == SourceKind.URL:
            url = urlsplit(self.source_uri or "")
            if url.scheme not in {"http", "https"} or not url.hostname:
                raise ValueError("URL sources require an HTTP/HTTPS source_uri")
            if url.username or url.password:
                raise ValueError("URL credentials are not supported")
        if self.kind == SourceKind.FILE and not (self.source_uri or "").strip():
            raise ValueError("file sources require an original filename in source_uri")
        if self.kind == SourceKind.URL and self.text is not None:
            raise ValueError("text is not supported for URL sources")
        if self.text is not None and self.format in {"html", "pdf", "docx"}:
            raise ValueError("textual input supports text, markdown, json and yaml only")
        if self.text is not None and (not self.text.strip() or "\x00" in self.text):
            raise ValueError("text must be nonblank and contain no NUL bytes")
        return self


class RowView(BaseModel):
    model_config = ConfigDict(from_attributes=True)

    id: UUID
    owner_id: str
    created_at: datetime
    updated_at: datetime


class SourceView(RowView):
    kind: SourceKind
    name: str
    source_uri: str | None
    content_type: str | None
    format: Format | None
    status: Stage
    config_json: dict[str, Any]
    metadata_json: dict[str, Any]
    content_hash: str | None
    last_ingested_at: datetime | None


class JobView(RowView):
    source_id: UUID
    status: Stage
    stage: Stage
    documents_found: int
    documents_processed: int
    chunks_created: int
    error_code: str | None
    error_detail: str | None
    started_at: datetime | None
    finished_at: datetime | None


class SourceAccepted(BaseModel):
    source: SourceView
    job: JobView


class DocumentView(RowView):
    source_id: UUID
    uri: str
    title: str
    mime_type: str | None
    format: Format | None
    text_content: str
    content_hash: str
    metadata_json: dict[str, Any]


class ChunkView(RowView):
    document_id: UUID
    source_id: UUID
    ordinal: int
    text: str
    heading_path: list[str]
    page_start: int | None
    page_end: int | None
    token_count: int
    content_hash: str


class JobTransition(BaseModel):
    model_config = ConfigDict(extra="forbid")

    status: Stage
    documents_found: int | None = Field(default=None, ge=0, le=2_147_483_647)
    documents_processed: int | None = Field(default=None, ge=0, le=2_147_483_647)
    chunks_created: int | None = Field(default=None, ge=0, le=2_147_483_647)
    error_code: str | None = Field(default=None, pattern=r"^[a-z][a-z0-9_]{0,63}$")
    error_detail: str | None = Field(default=None, min_length=1, max_length=2000)

    @model_validator(mode="after")
    def validate_failure(self) -> Self:
        if self.status == Stage.FAILED:
            if not self.error_code or not (self.error_detail or "").strip():
                raise ValueError("failed jobs require error_code and error_detail")
        elif self.error_code is not None or self.error_detail is not None:
            raise ValueError("error fields are only valid for failed jobs")
        return self
