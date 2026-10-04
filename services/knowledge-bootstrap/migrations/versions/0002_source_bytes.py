"""Retain bounded PDF/DOCX uploads for durable parsing and refresh."""

import sqlalchemy as sa
from alembic import op

revision = "0002_source_bytes"
down_revision = "0001_knowledge_core"
branch_labels = None
depends_on = None


def upgrade():
    op.add_column("knowledge_sources", sa.Column("input_bytes", sa.LargeBinary(), nullable=True))
    op.create_check_constraint(
        "ck_sources_binary_input",
        "knowledge_sources",
        "input_bytes IS NULL OR (kind = 'file' AND format IS NOT NULL "
        "AND format IN ('pdf','docx') "
        "AND input_text IS NULL)",
    )


def downgrade():
    op.drop_constraint("ck_sources_binary_input", "knowledge_sources", type_="check")
    op.drop_column("knowledge_sources", "input_bytes")
