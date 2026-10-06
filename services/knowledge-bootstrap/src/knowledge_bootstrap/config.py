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
    web_request_timeout_seconds: float = Field(default=10, ge=0.1, le=60)
    web_crawl_timeout_seconds: float = Field(default=120, ge=1, le=600)
    web_max_response_bytes: int = Field(default=2_097_152, ge=1, le=8_388_608)
    web_max_redirects: int = Field(default=5, ge=0, le=10)
    web_max_pages: int = Field(default=20, ge=1, le=50)
    web_max_depth: int = Field(default=3, ge=0, le=5)
    web_max_links: int = Field(default=1000, ge=1, le=5000)
    web_max_output_bytes: int = Field(default=8_388_608, ge=1024, le=33_554_432)
    web_crawl_delay_seconds: float = Field(default=1, ge=0.1, le=30)
    web_min_text_chars: int = Field(default=40, ge=1, le=1000)
    chunk_target_tokens: int = Field(default=600, ge=32, le=4000)
    chunk_max_tokens: int = Field(default=1200, ge=32, le=8000)
    chunk_overlap_tokens: int = Field(default=40, ge=0, le=200)
    max_chunks_per_source: int = Field(default=5000, ge=1, le=20000)
    indexing_enabled: bool = True
    index_batch_size: int = Field(default=32, ge=1, le=128)
    index_timeout_seconds: float = Field(default=10, ge=0.1, le=60)
    index_source_timeout_seconds: float = Field(default=120, ge=1, le=600)
    embedding_base_url: str = "https://api.openai.com/v1"
    embedding_api_key: SecretStr = SecretStr("")
    embedding_model: str = Field(default="text-embedding-3-small", min_length=1, max_length=200)
    # Optional API dimensions parameter: leave unset for other compatible providers/models.
    embedding_request_dimensions: int | None = Field(default=None, ge=1, le=65536)
    qdrant_url: str = "http://localhost:6333"
    qdrant_api_key: SecretStr = SecretStr("")
    qdrant_collection: str = Field(default="knowledge_chunks", pattern=r"^[a-zA-Z0-9_-]{1,128}$")
    embedding_dimensions: int = Field(default=1536, ge=1, le=65536)
    search_max_limit: int = Field(default=20, ge=1, le=100)
    search_candidate_limit: int = Field(default=100, ge=1, le=200)
    search_timeout_seconds: float = Field(default=20, ge=1, le=60)

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

    @field_validator("qdrant_url", "embedding_base_url")
    @classmethod
    def validate_qdrant_url(cls, value: str) -> str:
        from urllib.parse import urlsplit

        url = urlsplit(value)
        if url.scheme not in {"http", "https"} or not url.hostname or url.username or url.password:
            raise ValueError("must be an HTTP/HTTPS URL without credentials")
        return value.rstrip("/")

    @model_validator(mode="after")
    def validate_tokens(self) -> Self:
        if self.search_candidate_limit < self.search_max_limit:
            raise ValueError("search candidate limit must cover the maximum result limit")
        if self.chunk_target_tokens > self.chunk_max_tokens:
            raise ValueError("chunk target must not exceed the hard maximum")
        if self.chunk_overlap_tokens >= self.chunk_target_tokens:
            raise ValueError("chunk overlap must be smaller than the target")
        if self.embedding_request_dimensions not in {None, self.embedding_dimensions}:
            raise ValueError("requested embedding dimensions must match the collection dimensions")
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
