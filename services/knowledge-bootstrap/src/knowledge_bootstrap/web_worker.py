"""Set process limits before importing network/HTML libraries."""

import json
import math
import resource
import sys
from dataclasses import asdict
from pathlib import Path


def main():
    input_path, output_path = map(Path, sys.argv[1:])
    payload = json.loads(input_path.read_text(encoding="utf-8"))
    limits = payload["limits"]
    memory = limits["parser_memory_bytes"]
    cpu = math.ceil(limits["web_crawl_timeout_seconds"]) + 1
    resource.setrlimit(resource.RLIMIT_AS, (memory, memory))
    resource.setrlimit(resource.RLIMIT_CPU, (cpu, cpu + 1))
    maximum = limits["web_max_output_bytes"] + 65_536
    resource.setrlimit(resource.RLIMIT_FSIZE, (maximum, maximum))
    resource.setrlimit(resource.RLIMIT_CORE, (0, 0))
    resource.setrlimit(resource.RLIMIT_NOFILE, (64, 64))

    from knowledge_bootstrap.config import Settings
    from knowledge_bootstrap.parsers import ParseError
    from knowledge_bootstrap.web_crawl import crawl
    from knowledge_bootstrap.web_urls import CrawlConfig

    try:
        result = asdict(
            crawl(
                payload["url"],
                CrawlConfig.model_validate(payload["config"]),
                Settings.model_construct(**limits),
            )
        )
        encoded = json.dumps(result, ensure_ascii=False, separators=(",", ":"))
        if len(encoded.encode()) > maximum:
            raise ParseError("crawl_output_too_large", "Crawl result exceeds the output byte limit")
    except ParseError as exc:
        encoded = json.dumps({"error": {"code": exc.code, "detail": exc.detail}})
    except MemoryError:
        encoded = json.dumps(
            {
                "error": {
                    "code": "crawler_resource_limit",
                    "detail": "Website crawler exceeded its memory limit",
                }
            }
        )
    except Exception:
        encoded = json.dumps(
            {
                "error": {
                    "code": "crawler_failed",
                    "detail": "Website crawler could not process this source",
                }
            }
        )
    output_path.write_text(encoded, encoding="utf-8")


if __name__ == "__main__":
    main()
