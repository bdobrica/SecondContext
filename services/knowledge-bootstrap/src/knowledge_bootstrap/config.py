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
    max_request_bytes: int = Field(default=10_485_760, ge=1024, le=16_777_216)
    max_input_bytes: int = Field(default=524_288, ge=1, le=8_388_608)
    max_file_bytes: int = Field(default=8_388_608, ge=1, le=8_388_608)
    parser_timeout_seconds: float = Field(default=15, ge=0.1, le=120)
    parser_memory_bytes: int = Field(default=536_870_912, ge=67_108_864, le=2_147_483_648)
    max_pdf_pages: int = Field(default=500, ge=1, le=2000)
    min_pdf_text_chars: int = Field(default=20, ge=1, le=1000)
    max_pdf_stream_bytes: int = Field(default=8_388_608, ge=1, le=33_554_432)
    max_document_objects: int = Field(default=50_000, ge=1, le=200_000)
    max_archive_members: int = Field(default=512, ge=1, le=2048)
    max_decompressed_bytes: int = Field(default=33_554_432, ge=1, le=134_217_728)
    max_compression_ratio: int = Field(default=200, ge=1, le=1000)
    max_normalized_bytes: int = Field(default=2_097_152, ge=1, le=16_777_216)
    max_parse_depth: int = Field(default=32, ge=1, le=64)
    max_parse_nodes: int = Field(default=10_000, ge=1, le=100_000)
    max_yaml_aliases: int = Field(default=32, ge=0, le=100)
    text_worker_enabled: bool = True
    worker_poll_seconds: float = Field(default=1, ge=0.05, le=60)
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
