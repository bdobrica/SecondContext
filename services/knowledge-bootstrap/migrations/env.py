import os

from alembic import context
from sqlalchemy import create_engine
from sqlalchemy.pool import NullPool

from knowledge_bootstrap.models import Base

database_url = os.environ.get("KNOWLEDGE_DATABASE_URL")
if not database_url:
    raise RuntimeError("KNOWLEDGE_DATABASE_URL is required for migrations")
if not database_url.startswith("postgresql+psycopg://"):
    raise RuntimeError("KNOWLEDGE_DATABASE_URL must use postgresql+psycopg")


def run_online(connection):
    context.configure(connection=connection, target_metadata=Base.metadata)
    with context.begin_transaction():
        context.run_migrations()


if context.is_offline_mode():
    context.configure(url=database_url, target_metadata=Base.metadata, literal_binds=True)
    with context.begin_transaction():
        context.run_migrations()
elif context.config.attributes.get("connection") is not None:
    run_online(context.config.attributes["connection"])
else:
    engine = create_engine(
        database_url,
        poolclass=NullPool,
        hide_parameters=True,
        connect_args={"connect_timeout": 5},
    )
    try:
        with engine.connect() as connection:
            run_online(connection)
    finally:
        engine.dispose()
