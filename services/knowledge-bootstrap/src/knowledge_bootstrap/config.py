from typing import Self

from pydantic import Field, SecretStr, field_validator, model_validator
from pydantic_settings import BaseSettings, SettingsConfigDict
from sqlalchemy.engine import make_url


class Settings(BaseSettings):
    model_config = SettingsConfigDict(env_prefix="KNOWLEDGE_", extra="ignore")

    database_url: SecretStr
    # One credential per caller/workspace; owners need no identity table.
    auth_tokens: dict[str, SecretStr] = Field(min_length=1, repr=False)
    log_level: str = "INFO"
    database_pool_size: int = Field(default=5, ge=1, le=50)
    database_timeout_seconds: int = Field(default=5, ge=1, le=60)
    max_request_bytes: int = Field(default=1_048_576, ge=1024, le=16_777_216)
    # Future search projection; intentionally not connected or needed in K1.
    qdrant_url: str = "http://localhost:6333"
    qdrant_api_key: SecretStr = SecretStr("")
    qdrant_collection: str = Field(default="knowledge_chunks", min_length=1, max_length=128)
    embedding_dimensions: int = Field(default=1536, ge=1, le=65536)

    @field_validator("database_url")
    @classmethod
    def validate_database_url(cls, value: SecretStr) -> SecretStr:
        try:
            url = make_url(value.get_secret_value())
        except Exception:
            raise ValueError("must be a valid Postgres URL") from None
        if url.drivername != "postgresql+psycopg" or not url.database:
            raise ValueError("must use postgresql+psycopg and name a dedicated database")
        return value

    @field_validator("log_level")
    @classmethod
    def validate_log_level(cls, value: str) -> str:
        value = value.upper()
        if value not in {"DEBUG", "INFO", "WARNING", "ERROR", "CRITICAL"}:
            raise ValueError("unsupported log level")
        return value

    @field_validator("qdrant_url")
    @classmethod
    def validate_qdrant_url(cls, value: str) -> str:
        from urllib.parse import urlsplit

        url = urlsplit(value)
        if url.scheme not in {"http", "https"} or not url.hostname or url.username or url.password:
            raise ValueError("must be an HTTP/HTTPS URL without credentials")
        return value.rstrip("/")

    @model_validator(mode="after")
    def validate_tokens(self) -> Self:
        tokens = []
        for owner, secret in self.auth_tokens.items():
            if not owner.strip() or owner != owner.strip() or len(owner) > 200:
                raise ValueError("owner IDs must be nonblank and at most 200 characters")
            token = secret.get_secret_value()
            if len(token) < 16 or token != token.strip() or any(c.isspace() for c in token):
                raise ValueError("bearer tokens must have at least 16 characters and no whitespace")
            tokens.append(token)
        if len(tokens) != len(set(tokens)):
            raise ValueError("each owner must have a distinct bearer token")
        return self
