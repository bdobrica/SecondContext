"""Select independently versioned Docker releases from a complete push range."""

import argparse
import configparser
import json
import os
import re
import subprocess
from pathlib import Path

IMAGES = (
    (".bumpversion.cfg", "secondcontext-gateway", ".", "Dockerfile"),
    (
        "services/knowledge-bootstrap/.bumpversion.cfg",
        "secondcontext-knowledge",
        "services/knowledge-bootstrap",
        "services/knowledge-bootstrap/Dockerfile",
    ),
)
VERSION = re.compile(r"(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)")


def version_at(ref, path):
    # An all-zero 'before' identifies a new branch: there is no baseline to bump.
    if not ref.strip("0"):
        return None
    subprocess.run(
        ["git", "cat-file", "-e", f"{ref}^{{commit}}"], check=True, capture_output=True
    )
    files = subprocess.check_output(
        ["git", "ls-tree", "--name-only", ref, "--", path], text=True
    )
    if not files.strip():
        return None
    config = configparser.ConfigParser(interpolation=None)
    config.read_string(
        subprocess.check_output(["git", "show", f"{ref}:{path}"], text=True)
    )
    version = config.get("bumpversion", "current_version").strip()
    if not VERSION.fullmatch(version) or len(version) > 128:
        raise ValueError(
            f"{path}: current_version must be a numeric major.minor.patch Docker tag"
        )
    return version


def release_matrix(before, after):
    releases = []
    for path, image, context, dockerfile in IMAGES:
        previous = version_at(before, path)
        current = version_at(after, path)
        if previous is None or current is None or previous == current:
            continue
        if tuple(map(int, current.split("."))) <= tuple(map(int, previous.split("."))):
            raise ValueError(f"{path}: version must increase ({previous} -> {current})")
        releases.append(
            {
                "image": image,
                "context": context,
                "dockerfile": dockerfile,
                "version": current,
            }
        )
    return {"include": releases}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--before", required=True)
    parser.add_argument("--after", required=True)
    args = parser.parse_args()
    for ref in (args.before, args.after):
        if not re.fullmatch(r"[0-9a-f]{40}|[0-9a-f]{64}", ref):
            parser.error("commit IDs must be full hexadecimal Git hashes")
    try:
        matrix = release_matrix(args.before, args.after)
    except (ValueError, configparser.Error, subprocess.CalledProcessError) as exc:
        parser.exit(1, f"Release detection failed: {exc}\n")
    encoded = json.dumps(matrix, separators=(",", ":"))
    print(encoded)
    if output := os.environ.get("GITHUB_OUTPUT"):
        with Path(output).open("a") as handle:
            handle.write(f"matrix={encoded}\n")
            handle.write(f"has_releases={str(bool(matrix['include'])).lower()}\n")


if __name__ == "__main__":
    main()
