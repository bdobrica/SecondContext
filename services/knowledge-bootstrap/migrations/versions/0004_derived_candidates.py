"""Optional documentary candidates with transactional evidence retractions."""

import sqlalchemy as sa
from alembic import op
from sqlalchemy.dialects import postgresql as pg

revision = "0004_derived_candidates"
down_revision = "0003_chunk_metadata"
branch_labels = None
depends_on = None


def upgrade():
    op.create_table(
        "knowledge_candidates",
        sa.Column("id", pg.UUID(), primary_key=True),
        sa.Column("owner_id", sa.String(200), nullable=False),
        *[
            sa.Column(name, pg.UUID(), nullable=False)
            for name in ("source_id", "document_id", "chunk_id", "extraction_id")
        ],
        sa.Column("kind", sa.String(16), nullable=False),
        sa.Column("statement", sa.Text(), nullable=False),
        *[sa.Column(name, sa.Text()) for name in ("subject", "predicate", "object")],
        sa.Column("evidence_json", pg.JSONB(), nullable=False),
        sa.Column("extraction_json", pg.JSONB(), nullable=False),
        sa.Column("status", sa.String(16), nullable=False),
        sa.Column("retracted_at", sa.DateTime(timezone=True)),
        sa.Column("retraction_reason", sa.String(64)),
        *[
            sa.Column(
                name, sa.DateTime(timezone=True), nullable=False, server_default=sa.func.now()
            )
            for name in ("created_at", "updated_at")
        ],
        sa.CheckConstraint(
            "kind IN ('entity','person','topic','claim','relationship')",
            name="ck_candidates_kind",
        ),
        sa.CheckConstraint(
            "(status = 'active' AND retracted_at IS NULL AND retraction_reason IS NULL) OR "
            "(status = 'retracted' AND retracted_at IS NOT NULL AND retraction_reason IS NOT NULL)",
            name="ck_candidates_status",
        ),
    )
    for name, columns in (
        ("ix_candidates_owner_source", ["owner_id", "source_id"]),
        ("ix_candidates_owner_updated", ["owner_id", "updated_at", "id"]),
        ("ix_candidates_evidence", ["owner_id", "chunk_id"]),
    ):
        op.create_index(name, "knowledge_candidates", columns)
    # Triggers cover worker, operator and FK-cascade mutations atomically, including
    # the parsing/chunking gap. Retractions roll back with a failed refresh/delete.
    op.execute("""
        CREATE FUNCTION knowledge_retract_candidates() RETURNS trigger
        LANGUAGE plpgsql AS $$
        BEGIN
            IF TG_TABLE_NAME = 'knowledge_sources' THEN
                UPDATE knowledge_candidates SET status = 'retracted',
                    retracted_at = clock_timestamp(), updated_at = clock_timestamp(),
                    retraction_reason = 'source_deleted'
                WHERE owner_id = OLD.owner_id AND source_id = OLD.id;
            ELSIF TG_TABLE_NAME = 'knowledge_documents' THEN
                IF TG_OP = 'DELETE' OR OLD.content_hash IS DISTINCT FROM NEW.content_hash
                    OR OLD.title IS DISTINCT FROM NEW.title OR OLD.uri IS DISTINCT FROM NEW.uri
                    OR OLD.text_content IS DISTINCT FROM NEW.text_content
                    OR OLD.format IS DISTINCT FROM NEW.format
                    OR OLD.metadata_json->'blocks' IS DISTINCT FROM NEW.metadata_json->'blocks'
                THEN
                    UPDATE knowledge_candidates SET status = 'retracted',
                        retracted_at = clock_timestamp(), updated_at = clock_timestamp(),
                        retraction_reason = CASE WHEN TG_OP = 'DELETE'
                            THEN 'document_deleted' ELSE 'document_changed' END
                    WHERE owner_id = OLD.owner_id AND document_id = OLD.id AND status = 'active';
                END IF;
            ELSE
                UPDATE knowledge_candidates SET status = 'retracted',
                    retracted_at = clock_timestamp(), updated_at = clock_timestamp(),
                    retraction_reason = 'evidence_removed'
                WHERE owner_id = OLD.owner_id AND chunk_id = OLD.id AND status = 'active';
            END IF;
            RETURN OLD;
        END $$
    """)
    for table, event in (
        ("knowledge_sources", "BEFORE DELETE"),
        ("knowledge_documents", "AFTER UPDATE OR DELETE"),
        ("knowledge_chunks", "AFTER UPDATE OR DELETE"),
    ):
        op.execute(
            f"CREATE TRIGGER retract_candidates {event} ON {table} "
            "FOR EACH ROW EXECUTE FUNCTION knowledge_retract_candidates()"
        )


def downgrade():
    for table in ("knowledge_sources", "knowledge_documents", "knowledge_chunks"):
        op.execute(f"DROP TRIGGER retract_candidates ON {table}")
    op.execute("DROP FUNCTION knowledge_retract_candidates()")
    op.drop_table("knowledge_candidates")
