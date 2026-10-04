import json
import logging
from datetime import UTC, datetime


class JsonFormatter(logging.Formatter):
    def format(self, record: logging.LogRecord) -> str:
        entry = {
            "timestamp": datetime.now(UTC).isoformat(),
            "level": record.levelname,
            "service": "knowledge-bootstrap",
            "message": record.getMessage(),
        }
        for field in ("method", "path", "status", "duration_ms", "error_type"):
            if hasattr(record, field):
                entry[field] = getattr(record, field)
        # Deliberately exclude exception messages, request bodies, credentials, and query strings.
        return json.dumps(entry)


def configure_logging(level: str) -> logging.Logger:
    handler = logging.StreamHandler()
    handler.setFormatter(JsonFormatter())
    logger = logging.getLogger("knowledge_bootstrap")
    logger.handlers = [handler]
    logger.setLevel(level)
    logger.propagate = False
    return logger
