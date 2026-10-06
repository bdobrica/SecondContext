"""Disposable, credential-free process boundary for bounded website ingestion."""

import json
import os
import signal
import subprocess
import sys
from pathlib import Path
from tempfile import TemporaryDirectory

from knowledge_bootstrap.config import Settings
from knowledge_bootstrap.parsers import Block, ParsedDocument, ParseError, validate_document
from knowledge_bootstrap.web_crawl import CrawlResult
from knowledge_bootstrap.web_urls import CrawlConfig

LIMIT_FIELDS = (
    "parser_memory_bytes",
    "max_normalized_bytes",
    "max_parse_nodes",
    "max_parse_depth",
    "web_request_timeout_seconds",
    "web_crawl_timeout_seconds",
    "web_max_response_bytes",
    "web_max_redirects",
    "web_max_pages",
    "web_max_depth",
    "web_max_links",
    "web_max_output_bytes",
    "web_crawl_delay_seconds",
    "web_min_text_chars",
)


def ingest_website(url: str, options: dict, settings: Settings) -> CrawlResult:
    try:
        config = CrawlConfig.model_validate(options)
    except ValueError:
        raise ParseError(
            "invalid_crawl_config", "Stored URL source has unsupported crawl options"
        ) from None
    if os.name != "posix":
        raise ParseError(
            "crawler_unavailable", "Website ingestion requires Linux/POSIX resource limits"
        )
    limits = {field: getattr(settings, field) for field in LIMIT_FIELDS}
    with TemporaryDirectory(prefix="knowledge-web-") as directory:
        input_path, output_path = Path(directory) / "input.json", Path(directory) / "result.json"
        input_path.write_text(
            json.dumps({"url": url, "config": config.model_dump(), "limits": limits}),
            encoding="utf-8",
        )
        try:
            result = subprocess.run(
                [
                    sys.executable,
                    "-m",
                    "knowledge_bootstrap.web_worker",
                    str(input_path),
                    str(output_path),
                ],
                stdin=subprocess.DEVNULL,
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL,
                env={
                    "PATH": os.defpath,
                    "LANG": "C.UTF-8",
                    "PYTHONIOENCODING": "utf-8",
                    "PYTHONHASHSEED": "0",
                },
                timeout=settings.web_crawl_timeout_seconds + 5,
                check=False,
            )
        except subprocess.TimeoutExpired:
            raise ParseError("web_timeout", "Website crawler exceeded its time limit") from None
        except OSError:
            raise ParseError(
                "crawler_unavailable", "Website crawler could not be started"
            ) from None
        if result.returncode:
            if result.returncode == -signal.SIGXCPU:
                raise ParseError("web_timeout", "Website crawler exceeded its CPU limit")
            raise ParseError("crawler_resource_limit", "Website crawler exceeded resource limits")
        try:
            with output_path.open("rb") as stream:
                encoded = stream.read(settings.web_max_output_bytes + 65_537)
            if len(encoded) > settings.web_max_output_bytes + 65_536:
                raise ParseError(
                    "crawl_output_too_large", "Crawl result exceeds the output byte limit"
                )
            payload = json.loads(encoded)
            if "error" in payload:
                raise ParseError(**payload["error"])
            documents = []
            if not 1 <= len(payload["documents"]) <= min(config.max_pages, settings.web_max_pages):
                raise ValueError
            for doc in payload["documents"]:
                doc["blocks"] = [Block(**block) for block in doc["blocks"]]
                documents.append(validate_document(ParsedDocument(**doc), settings))
            return CrawlResult(documents, payload["metadata"])
        except (OSError, ValueError, KeyError, TypeError):
            raise ParseError(
                "crawler_failed", "Website crawler did not return a valid result"
            ) from None
