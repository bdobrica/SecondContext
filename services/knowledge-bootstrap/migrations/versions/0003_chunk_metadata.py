"""Retain semantic paths, chunk recipe and split provenance on canonical chunks."""

import sqlalchemy as sa
from alembic import op
from sqlalchemy.dialects import postgresql

revision = "0003_chunk_metadata"
down_revision = "0002_source_bytes"
branch_labels = None
depends_on = None


def upgrade():
    op.create_table(
        "knowledge_index_configurations",
        sa.Column("target", sa.String(64), primary_key=True),
        sa.Column("config_json", postgresql.JSONB(), nullable=False),
    )
    op.add_column(
        "knowledge_chunks",
        sa.Column("metadata_json", postgresql.JSONB(), nullable=False, server_default="{}"),
    )


def downgrade():
    op.drop_table("knowledge_index_configurations")
    op.drop_column("knowledge_chunks", "metadata_json")
