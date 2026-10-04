from sqlalchemy import create_engine
from sqlalchemy.orm import sessionmaker

from knowledge_bootstrap.config import Settings


def make_engine(settings: Settings):
    timeout = settings.database_timeout_seconds
    return create_engine(
        settings.database_url.get_secret_value(),
        pool_pre_ping=True,
        pool_size=settings.database_pool_size,
        max_overflow=0,
        pool_timeout=timeout,
        connect_args={
            "connect_timeout": timeout,
            "options": f"-c statement_timeout={timeout * 1000} -c lock_timeout={timeout * 1000}",
        },
        hide_parameters=True,
    )


def make_sessions(engine):
    return sessionmaker(engine, expire_on_commit=False)
