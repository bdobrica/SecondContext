"""Canonical knowledge sources, documents, chunks and durable ingestion jobs.

This migration is a fixed snapshot: it deliberately does not import application models.
"""

import sqlalchemy as sa
from alembic import op
from sqlalchemy.dialects import postgresql

revision = "0001_knowledge_core"
down_revision = None
branch_labels = None
depends_on = None


def canonical_columns():
    return [
        sa.Column("id", sa.Uuid(), primary_key=True, nullable=False),
        sa.Column("owner_id", sa.String(200), nullable=False),
        sa.Column(
            "created_at", sa.DateTime(timezone=True), nullable=False, server_default=sa.func.now()
        ),
        sa.Column(
            "updated_at", sa.DateTime(timezone=True), nullable=False, server_default=sa.func.now()
        ),
    ]


def upgrade():
    op.create_table(
        "knowledge_sources",
        *canonical_columns(),
        sa.Column("kind", sa.String(16), nullable=False),
        sa.Column("name", sa.String(500), nullable=False),
        sa.Column("source_uri", sa.Text()),
        sa.Column("content_type", sa.String(200)),
        sa.Column("format", sa.String(16)),
        sa.Column("status", sa.String(16), nullable=False),
        sa.Column("config_json", postgresql.JSONB(), nullable=False, server_default="{}"),
        sa.Column("metadata_json", postgresql.JSONB(), nullable=False, server_default="{}"),
        sa.Column("input_text", sa.Text()),
        sa.Column("content_hash", sa.String(64)),
        sa.Column("request_key", sa.String(128)),
        sa.Column("request_hash", sa.String(64)),
        sa.Column("last_ingested_at", sa.DateTime(timezone=True)),
        sa.UniqueConstraint("id", "owner_id", name="uq_sources_owner"),
        sa.UniqueConstraint("owner_id", "request_key", name="uq_sources_request_key"),
        sa.CheckConstraint("length(trim(owner_id)) > 0", name="ck_sources_owner"),
        sa.CheckConstraint("kind IN ('url','file','text')", name="ck_sources_kind"),
        sa.CheckConstraint(
            "status IN ('pending','fetching','parsing','chunking','indexing','ready','failed')",
            name="ck_sources_status",
        ),
        sa.CheckConstraint(
            "format IS NULL OR format IN ('html','pdf','docx','markdown','json','yaml','text')",
            name="ck_sources_format",
        ),
    )
    op.create_index(
        "ix_sources_owner_created", "knowledge_sources", ["owner_id", "created_at", "id"]
    )
    op.create_table(
        "knowledge_documents",
        *canonical_columns(),
        sa.Column("source_id", sa.Uuid(), nullable=False),
        sa.Column("uri", sa.Text(), nullable=False),
        sa.Column("title", sa.Text(), nullable=False),
        sa.Column("mime_type", sa.String(200)),
        sa.Column("format", sa.String(16)),
        sa.Column("raw_content_or_ref", sa.Text()),
        sa.Column("text_content", sa.Text(), nullable=False),
        sa.Column("content_hash", sa.String(64), nullable=False),
        sa.Column("metadata_json", postgresql.JSONB(), nullable=False, server_default="{}"),
        sa.UniqueConstraint("id", "source_id", "owner_id", name="uq_documents_source_owner"),
        sa.UniqueConstraint("source_id", "uri", name="uq_documents_uri"),
        sa.ForeignKeyConstraint(
            ["source_id", "owner_id"],
            ["knowledge_sources.id", "knowledge_sources.owner_id"],
            ondelete="CASCADE",
            name="fk_documents_source_owner",
        ),
    )
    op.create_index("ix_documents_owner_source", "knowledge_documents", ["owner_id", "source_id"])
    op.create_table(
        "knowledge_chunks",
        *canonical_columns(),
        sa.Column("document_id", sa.Uuid(), nullable=False),
        sa.Column("source_id", sa.Uuid(), nullable=False),
        sa.Column("ordinal", sa.Integer(), nullable=False),
        sa.Column("text", sa.Text(), nullable=False),
        sa.Column("heading_path", postgresql.JSONB(), nullable=False, server_default="[]"),
        sa.Column("page_start", sa.Integer()),
        sa.Column("page_end", sa.Integer()),
        sa.Column("token_count", sa.Integer(), nullable=False),
        sa.Column("content_hash", sa.String(64), nullable=False),
        sa.UniqueConstraint("document_id", "ordinal", name="uq_chunks_ordinal"),
        sa.ForeignKeyConstraint(
            ["document_id", "source_id", "owner_id"],
            [
                "knowledge_documents.id",
                "knowledge_documents.source_id",
                "knowledge_documents.owner_id",
            ],
            ondelete="CASCADE",
            name="fk_chunks_document_source_owner",
        ),
        sa.CheckConstraint("ordinal >= 0 AND token_count >= 0", name="ck_chunks_counts"),
        sa.CheckConstraint(
            "(page_start IS NULL AND page_end IS NULL) OR "
            "(page_start IS NOT NULL AND page_end IS NOT NULL "
            "AND page_start >= 1 AND page_end >= page_start)",
            name="ck_chunks_pages",
        ),
    )
    op.create_index("ix_chunks_owner_source", "knowledge_chunks", ["owner_id", "source_id"])
    op.create_index("ix_chunks_owner_hash", "knowledge_chunks", ["owner_id", "content_hash"])
    op.create_table(
        "knowledge_ingestion_jobs",
        *canonical_columns(),
        sa.Column("source_id", sa.Uuid(), nullable=False),
        sa.Column("status", sa.String(16), nullable=False),
        sa.Column("stage", sa.String(16), nullable=False),
        sa.Column("documents_found", sa.Integer(), nullable=False, server_default="0"),
        sa.Column("documents_processed", sa.Integer(), nullable=False, server_default="0"),
        sa.Column("chunks_created", sa.Integer(), nullable=False, server_default="0"),
        sa.Column("error_code", sa.String(64)),
        sa.Column("error_detail", sa.Text()),
        sa.Column("started_at", sa.DateTime(timezone=True)),
        sa.Column("finished_at", sa.DateTime(timezone=True)),
        sa.ForeignKeyConstraint(
            ["source_id", "owner_id"],
            ["knowledge_sources.id", "knowledge_sources.owner_id"],
            ondelete="CASCADE",
            name="fk_jobs_source_owner",
        ),
        sa.CheckConstraint(
            "status IN ('pending','fetching','parsing','chunking','indexing','ready','failed')",
            name="ck_jobs_status",
        ),
        sa.CheckConstraint(
            "stage IN ('pending','fetching','parsing','chunking','indexing','ready')",
            name="ck_jobs_stage",
        ),
        sa.CheckConstraint(
            "documents_found >= 0 AND documents_processed >= 0 "
            "AND documents_processed <= documents_found AND chunks_created >= 0",
            name="ck_jobs_counts",
        ),
        sa.CheckConstraint(
            "(status = 'failed' AND error_code IS NOT NULL AND error_detail IS NOT NULL) "
            "OR (status <> 'failed' AND error_code IS NULL AND error_detail IS NULL)",
            name="ck_jobs_error",
        ),
        sa.CheckConstraint(
            "(status IN ('ready','failed') AND finished_at IS NOT NULL) "
            "OR (status NOT IN ('ready','failed') AND finished_at IS NULL)",
            name="ck_jobs_finished",
        ),
    )
    op.create_index(
        "ix_jobs_owner_created", "knowledge_ingestion_jobs", ["owner_id", "created_at", "id"]
    )
    op.create_index(
        "uq_jobs_active_source",
        "knowledge_ingestion_jobs",
        ["source_id"],
        unique=True,
        postgresql_where=sa.text("status NOT IN ('ready','failed')"),
    )


def downgrade():
    op.drop_table("knowledge_ingestion_jobs")
    op.drop_table("knowledge_chunks")
    op.drop_table("knowledge_documents")
    op.drop_table("knowledge_sources")
