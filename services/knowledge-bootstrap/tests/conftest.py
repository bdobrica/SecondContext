import os
from pathlib import Path
from uuid import uuid4

import pytest
from alembic import command
from alembic.config import Config
from fastapi.testclient import TestClient
from sqlalchemy import delete
from sqlalchemy.engine import make_url

from knowledge_bootstrap.app import create_app
from knowledge_bootstrap.config import Settings
from knowledge_bootstrap.database import make_engine, make_sessions
from knowledge_bootstrap.models import Source

ROOT = Path(__file__).resolve().parents[1]


def pytest_addoption(parser):
    parser.addoption(
        "--require-postgres", action="store_true", help="Fail if Postgres tests cannot run"
    )


@pytest.fixture
def settings():
    return Settings(
        database_url="postgresql+psycopg://unused:unused@localhost:1/knowledge_bootstrap_test",
        auth_tokens={"owner-a": "test-owner-a-token-123", "owner-b": "test-owner-b-token-456"},
        text_worker_enabled=False,
    )


@pytest.fixture(scope="session")
def postgres_url(request):
    url = os.environ.get("KNOWLEDGE_TEST_DATABASE_URL")
    if not url:
        if request.config.getoption("--require-postgres"):
            pytest.fail("KNOWLEDGE_TEST_DATABASE_URL is required")
        pytest.skip("set KNOWLEDGE_TEST_DATABASE_URL to run Postgres integration tests")
    name = make_url(url).database or ""
    if not name.endswith(("_test", "_integration")):
        pytest.fail(
            "Postgres integration tests require a dedicated database ending in _test/_integration"
        )
    return url


@pytest.fixture(scope="session")
def migrated_url(postgres_url):
    # Migrations, never metadata.create_all: exercise the actual deployable schema.
    previous = os.environ.get("KNOWLEDGE_DATABASE_URL")
    os.environ["KNOWLEDGE_DATABASE_URL"] = postgres_url
    try:
        command.upgrade(Config(str(ROOT / "alembic.ini")), "head")
        yield postgres_url
    finally:
        if previous is None:
            os.environ.pop("KNOWLEDGE_DATABASE_URL", None)
        else:
            os.environ["KNOWLEDGE_DATABASE_URL"] = previous


@pytest.fixture
def db_settings(migrated_url):
    unique = uuid4().hex
    return Settings(
        database_url=migrated_url,
        auth_tokens={
            f"owner-a-{unique}": "test-owner-a-token-123",
            f"owner-b-{unique}": "test-owner-b-token-456",
        },
        text_worker_enabled=False,
    )


@pytest.fixture
def sessions(db_settings):
    engine = make_engine(db_settings)
    factory = make_sessions(engine)
    yield factory
    with factory.begin() as session:
        session.execute(delete(Source).where(Source.owner_id.in_(db_settings.auth_tokens)))
    engine.dispose()


@pytest.fixture
def owners(db_settings):
    return tuple(db_settings.auth_tokens)


@pytest.fixture
def client(db_settings, sessions):
    app = create_app(db_settings)
    with TestClient(app) as client:
        client.headers["Authorization"] = "Bearer test-owner-a-token-123"
        yield client
