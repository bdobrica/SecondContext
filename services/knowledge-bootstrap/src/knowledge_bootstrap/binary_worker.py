"""One disposable binary-parser process; set resource limits before parser imports."""

import json
import math
import resource
import sys
from pathlib import Path


def main():
    fmt, limits_json, input_path, output_path = sys.argv[1:]
    limits = json.loads(limits_json)
    memory = limits["parser_memory_bytes"]
    cpu = math.ceil(limits["parser_timeout_seconds"]) + 1
    resource.setrlimit(resource.RLIMIT_AS, (memory, memory))
    resource.setrlimit(resource.RLIMIT_CPU, (cpu, cpu + 1))
    output = limits["max_normalized_bytes"] + 65_536
    resource.setrlimit(resource.RLIMIT_FSIZE, (output, output))
    resource.setrlimit(resource.RLIMIT_CORE, (0, 0))
    resource.setrlimit(resource.RLIMIT_NOFILE, (64, 64))

    from knowledge_bootstrap.binary import document_payload
    from knowledge_bootstrap.config import Settings
    from knowledge_bootstrap.file_parsers import parse_file
    from knowledge_bootstrap.parsers import ParseError

    settings = Settings.model_construct(**limits)  # No database credentials or environment loading.
    try:
        with Path(input_path).open("rb") as stream:
            data = stream.read(settings.max_file_bytes + 1)
        if len(data) > settings.max_file_bytes:
            raise ParseError("input_too_large", "File exceeds the upload byte limit")
        payload = document_payload(parse_file(data, fmt, settings))
    except ParseError as exc:
        payload = {"error": {"code": exc.code, "detail": exc.detail}}
    except MemoryError:
        payload = {
            "error": {
                "code": "parser_resource_limit",
                "detail": "Document parser exceeded its memory limit",
            }
        }
    except Exception:
        payload = {"error": {"code": "invalid_" + fmt, "detail": "Malformed or unsupported file"}}
    Path(output_path).write_text(
        json.dumps(payload, ensure_ascii=False, separators=(",", ":")), encoding="utf-8"
    )


if __name__ == "__main__":
    main()
