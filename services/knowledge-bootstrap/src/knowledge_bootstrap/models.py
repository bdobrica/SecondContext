from datetime import datetime
from enum import StrEnum
from typing import Any
from uuid import UUID, uuid4

from sqlalchemy import (
    CheckConstraint,
    DateTime,
    ForeignKeyConstraint,
    Index,
    Integer,
    LargeBinary,
    String,
    Text,
    UniqueConstraint,
    func,
    text,
)
from sqlalchemy.dialects.postgresql import JSONB
from sqlalchemy.orm import DeclarativeBase, Mapped, mapped_column


class SourceKind(StrEnum):
    URL = "url"
    FILE = "file"
    TEXT = "text"


class Stage(StrEnum):
    PENDING = "pending"
    FETCHING = "fetching"
    PARSING = "parsing"
    CHUNKING = "chunking"
    INDEXING = "indexing"
    READY = "ready"
    FAILED = "failed"


TERMINAL = {Stage.READY, Stage.FAILED}
STAGE_CHECK = "status IN ('pending','fetching','parsing','chunking','indexing','ready','failed')"
FORMAT_CHECK = "format IS NULL OR format IN ('html','pdf','docx','markdown','json','yaml','text')"
SCHEMA_REVISION = "0002_source_bytes"


class Base(DeclarativeBase):
    pass


class CanonicalRow:
    id: Mapped[UUID] = mapped_column(primary_key=True, default=uuid4)
    owner_id: Mapped[str] = mapped_column(String(200))
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), server_default=func.now())
    updated_at: Mapped[datetime] = mapped_column(
        DateTime(timezone=True), server_default=func.now(), onupdate=func.now()
    )


class Source(CanonicalRow, Base):
    __tablename__ = "knowledge_sources"
    __table_args__ = (
        UniqueConstraint("id", "owner_id", name="uq_sources_owner"),
        UniqueConstraint("owner_id", "request_key", name="uq_sources_request_key"),
        CheckConstraint("length(trim(owner_id)) > 0", name="ck_sources_owner"),
        CheckConstraint("kind IN ('url','file','text')", name="ck_sources_kind"),
        CheckConstraint(STAGE_CHECK, name="ck_sources_status"),
        CheckConstraint(FORMAT_CHECK, name="ck_sources_format"),
        CheckConstraint(
            "input_bytes IS NULL OR (kind = 'file' AND format IS NOT NULL "
            "AND format IN ('pdf','docx') "
            "AND input_text IS NULL)",
            name="ck_sources_binary_input",
        ),
        Index("ix_sources_owner_created", "owner_id", "created_at", "id"),
    )

    kind: Mapped[str] = mapped_column(String(16))
    name: Mapped[str] = mapped_column(String(500))
    source_uri: Mapped[str | None] = mapped_column(Text)
    content_type: Mapped[str | None] = mapped_column(String(200))
    format: Mapped[str | None] = mapped_column(String(16))
    status: Mapped[str] = mapped_column(String(16), default=Stage.PENDING)
    config_json: Mapped[dict[str, Any]] = mapped_column(JSONB, default=dict, server_default="{}")
    metadata_json: Mapped[dict[str, Any]] = mapped_column(JSONB, default=dict, server_default="{}")
    # Original inputs stay durable for parsing/retries; never included in API views.
    input_text: Mapped[str | None] = mapped_column(Text)
    input_bytes: Mapped[bytes | None] = mapped_column(LargeBinary, deferred=True)
    content_hash: Mapped[str | None] = mapped_column(String(64))
    request_key: Mapped[str | None] = mapped_column(String(128))
    request_hash: Mapped[str | None] = mapped_column(String(64))
    last_ingested_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True))


class Document(CanonicalRow, Base):
    __tablename__ = "knowledge_documents"
    __table_args__ = (
        UniqueConstraint("id", "source_id", "owner_id", name="uq_documents_source_owner"),
        UniqueConstraint("source_id", "uri", name="uq_documents_uri"),
        ForeignKeyConstraint(
            ["source_id", "owner_id"],
            ["knowledge_sources.id", "knowledge_sources.owner_id"],
            ondelete="CASCADE",
            name="fk_documents_source_owner",
        ),
        Index("ix_documents_owner_source", "owner_id", "source_id"),
    )

    source_id: Mapped[UUID]
    uri: Mapped[str] = mapped_column(Text)
    title: Mapped[str] = mapped_column(Text)
    mime_type: Mapped[str | None] = mapped_column(String(200))
    format: Mapped[str | None] = mapped_column(String(16))
    raw_content_or_ref: Mapped[str | None] = mapped_column(Text)
    text_content: Mapped[str] = mapped_column(Text)
    content_hash: Mapped[str] = mapped_column(String(64))
    metadata_json: Mapped[dict[str, Any]] = mapped_column(JSONB, default=dict, server_default="{}")


class Chunk(CanonicalRow, Base):
    __tablename__ = "knowledge_chunks"
    __table_args__ = (
        UniqueConstraint("document_id", "ordinal", name="uq_chunks_ordinal"),
        ForeignKeyConstraint(
            ["document_id", "source_id", "owner_id"],
            [
                "knowledge_documents.id",
                "knowledge_documents.source_id",
                "knowledge_documents.owner_id",
            ],
            ondelete="CASCADE",
            name="fk_chunks_document_source_owner",
        ),
        CheckConstraint("ordinal >= 0 AND token_count >= 0", name="ck_chunks_counts"),
        CheckConstraint(
            "(page_start IS NULL AND page_end IS NULL) OR "
            "(page_start IS NOT NULL AND page_end IS NOT NULL "
            "AND page_start >= 1 AND page_end >= page_start)",
            name="ck_chunks_pages",
        ),
        Index("ix_chunks_owner_source", "owner_id", "source_id"),
        Index("ix_chunks_owner_hash", "owner_id", "content_hash"),
    )

    document_id: Mapped[UUID]
    source_id: Mapped[UUID]
    ordinal: Mapped[int] = mapped_column(Integer)
    text: Mapped[str] = mapped_column(Text)
    heading_path: Mapped[list[str]] = mapped_column(JSONB, default=list, server_default="[]")
    page_start: Mapped[int | None] = mapped_column(Integer)
    page_end: Mapped[int | None] = mapped_column(Integer)
    token_count: Mapped[int] = mapped_column(Integer)
    content_hash: Mapped[str] = mapped_column(String(64))


class IngestionJob(CanonicalRow, Base):
    __tablename__ = "knowledge_ingestion_jobs"
    __table_args__ = (
        ForeignKeyConstraint(
            ["source_id", "owner_id"],
            ["knowledge_sources.id", "knowledge_sources.owner_id"],
            ondelete="CASCADE",
            name="fk_jobs_source_owner",
        ),
        CheckConstraint(STAGE_CHECK, name="ck_jobs_status"),
        CheckConstraint(
            "stage IN ('pending','fetching','parsing','chunking','indexing','ready')",
            name="ck_jobs_stage",
        ),
        CheckConstraint(
            "documents_found >= 0 AND documents_processed >= 0 "
            "AND documents_processed <= documents_found AND chunks_created >= 0",
            name="ck_jobs_counts",
        ),
        CheckConstraint(
            "(status = 'failed' AND error_code IS NOT NULL AND error_detail IS NOT NULL) "
            "OR (status <> 'failed' AND error_code IS NULL AND error_detail IS NULL)",
            name="ck_jobs_error",
        ),
        CheckConstraint(
            "(status IN ('ready','failed') AND finished_at IS NOT NULL) "
            "OR (status NOT IN ('ready','failed') AND finished_at IS NULL)",
            name="ck_jobs_finished",
        ),
        Index("ix_jobs_owner_created", "owner_id", "created_at", "id"),
        Index(
            "uq_jobs_active_source",
            "source_id",
            unique=True,
            postgresql_where=text("status NOT IN ('ready','failed')"),
        ),
    )

    source_id: Mapped[UUID]
    status: Mapped[str] = mapped_column(String(16), default=Stage.PENDING)
    stage: Mapped[str] = mapped_column(String(16), default=Stage.PENDING)
    documents_found: Mapped[int] = mapped_column(Integer, default=0, server_default="0")
    documents_processed: Mapped[int] = mapped_column(Integer, default=0, server_default="0")
    chunks_created: Mapped[int] = mapped_column(Integer, default=0, server_default="0")
    error_code: Mapped[str | None] = mapped_column(String(64))
    error_detail: Mapped[str | None] = mapped_column(Text)
    started_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True))
    finished_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True))
