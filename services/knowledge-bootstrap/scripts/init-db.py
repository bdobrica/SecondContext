"""Provision dedicated databases on an existing local Postgres container.

No Python dependencies required. Run once before starting the optional Compose profile.
"""

import argparse
import secrets
import subprocess
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--postgres-container", default="secondcontext-postgres-1")
    parser.add_argument(
        "--database-host", default="postgres", help="Hostname from the API container"
    )
    args = parser.parse_args()
    env_file = ROOT / ".env"
    if env_file.exists():
        parser.error(f"{env_file} already exists; existing credentials will not be replaced")
    password = secrets.token_urlsafe(32)
    token = secrets.token_urlsafe(32)
    # Generated values use only URL-safe characters; all SQL identifiers below are constants.
    sql = (
        f"CREATE ROLE knowledge_bootstrap LOGIN PASSWORD '{password}';\n"
        "CREATE DATABASE knowledge_bootstrap OWNER knowledge_bootstrap;\n"
        "CREATE DATABASE knowledge_bootstrap_test OWNER knowledge_bootstrap;\n"
    )
    subprocess.run(
        [
            "docker",
            "exec",
            "-i",
            args.postgres_container,
            "sh",
            "-c",
            'exec psql -U "$POSTGRES_USER" -d postgres -v ON_ERROR_STOP=1',
        ],
        input=sql,
        text=True,
        check=True,
        stdout=subprocess.DEVNULL,
    )
    # Exclusive creation protects existing local configuration; secrets are never printed.
    with env_file.open("x") as stream:
        stream.write(
            "KNOWLEDGE_DATABASE_URL=postgresql+psycopg://knowledge_bootstrap:"
            f"{password}@{args.database_host}:5432/knowledge_bootstrap\n"
            f'KNOWLEDGE_AUTH_TOKENS={{"local":"{token}"}}\n'
            "KNOWLEDGE_QDRANT_URL=http://qdrant:6333\n"
            "KNOWLEDGE_QDRANT_COLLECTION=knowledge_chunks\n"
        )
    env_file.chmod(0o600)
    print("Created knowledge_bootstrap and knowledge_bootstrap_test on the existing Postgres.")
    print(f"Saved generated credentials to {env_file} (git-ignored).")


if __name__ == "__main__":
    main()
