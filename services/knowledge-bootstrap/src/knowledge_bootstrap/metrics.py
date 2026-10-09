"""Owner-scoped operational aggregates, without a monitoring dependency or input labels."""

import time
from contextlib import contextmanager
from threading import Lock

from sqlalchemy import func, select

from knowledge_bootstrap.models import Chunk, Document, IngestionJob, Source, Stage

BOUNDS = (0.1, 0.5, 1.0, 5.0, 20.0, 60.0)


class SearchMetrics:
    """Bounded process-local counters. Never label by query, URI, UUID or error text."""

    def __init__(self, owners):
        self.lock = Lock()
        self.rows = {
            owner: {
                mode: {
                    "requests": 0,
                    "failures": 0,
                    "seconds_sum": 0.0,
                    "buckets": [0] * len(BOUNDS),
                }
                for mode in ("dense", "sparse", "hybrid")
            }
            for owner in owners
        }

    @contextmanager
    def measure(self, owner, mode):
        start = time.monotonic()
        failed = False
        try:
            yield
        except Exception:
            failed = True
            raise
        finally:
            duration = time.monotonic() - start
            with self.lock:
                row = self.rows[owner][mode]
                row["requests"] += 1
                row["failures"] += int(failed)
                row["seconds_sum"] += duration
                for i, bound in enumerate(BOUNDS):
                    row["buckets"][i] += int(duration <= bound)

    def snapshot(self, owner):
        with self.lock:
            return {
                "scope": "this API process; resets on restart; validated authenticated searches",
                "modes": {
                    mode: {
                        "requests": row["requests"],
                        "failures": row["failures"],
                        "seconds_sum": row["seconds_sum"],
                        "latency_buckets": [
                            {"le_seconds": bound, "count": count}
                            for bound, count in zip(BOUNDS, row["buckets"], strict=True)
                        ],
                    }
                    for mode, row in self.rows[owner].items()
                },
            }


def operational_metrics(session, owner, searches):
    # Aggregation stays in Postgres: no loading source contents or unbounded job rows.
    jobs = session.execute(
        select(IngestionJob.status, func.count())
        .where(IngestionJob.owner_id == owner)
        .group_by(IngestionJob.status)
    ).all()
    failures = session.execute(
        select(IngestionJob.stage, func.count())
        .where(IngestionJob.owner_id == owner, IngestionJob.status == Stage.FAILED)
        .group_by(IngestionJob.stage)
    ).all()
    seconds = func.extract("epoch", IngestionJob.finished_at - IngestionJob.started_at)
    latency = session.execute(
        select(func.count(), func.coalesce(func.sum(seconds), 0), func.max(seconds)).where(
            IngestionJob.owner_id == owner,
            IngestionJob.finished_at.is_not(None),
            IngestionJob.started_at.is_not(None),
        )
    ).one()
    return {
        "canonical": {
            model.__tablename__.removeprefix("knowledge_"): session.scalar(
                select(func.count()).select_from(model).where(model.owner_id == owner)
            )
            for model in (Source, Document, Chunk)
        },
        "ingestion": {
            "scope": "retained canonical jobs; includes refresh/reindex; excludes queue wait",
            "jobs_by_status": dict(jobs),
            "failures_by_stage": dict(failures),
            "latency": {
                "completed_attempts": latency[0],
                "seconds_sum": float(latency[1]),
                "seconds_max": float(latency[2]) if latency[2] is not None else None,
            },
        },
        "search": searches.snapshot(owner),
    }
