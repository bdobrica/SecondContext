import argparse
import sys
from uuid import UUID

from pydantic import ValidationError
from sqlalchemy.exc import SQLAlchemyError

from knowledge_bootstrap.config import Settings
from knowledge_bootstrap.database import make_engine, make_sessions
from knowledge_bootstrap.models import Stage
from knowledge_bootstrap.schemas import JobTransition, JobView
from knowledge_bootstrap.service import ServiceError, transition_job


def main() -> None:
    parser = argparse.ArgumentParser(description="Knowledge service operator tools")
    commands = parser.add_subparsers(dest="command", required=True)
    transition = commands.add_parser(
        "transition-job", help="Exercise K1 or update a job as an operator"
    )
    transition.add_argument("job_id", type=UUID)
    transition.add_argument("status", choices=list(Stage))
    transition.add_argument("--owner", required=True)
    transition.add_argument("--documents-found", type=int)
    transition.add_argument("--documents-processed", type=int)
    transition.add_argument("--chunks-created", type=int)
    transition.add_argument("--error-code")
    transition.add_argument("--error-detail")
    args = parser.parse_args()
    engine = None
    try:
        settings = Settings()
        if args.owner not in settings.auth_tokens:
            parser.error("--owner must be a configured knowledge owner")
        engine = make_engine(settings)
        change = JobTransition(
            status=args.status,
            documents_found=args.documents_found,
            documents_processed=args.documents_processed,
            chunks_created=args.chunks_created,
            error_code=args.error_code,
            error_detail=args.error_detail,
        )
        with make_sessions(engine)() as session:
            job = transition_job(session, args.owner, args.job_id, change)
            print(JobView.model_validate(job).model_dump_json(indent=2))
    except ServiceError as exc:
        print(f"{exc.code}: {exc.detail}", file=sys.stderr)
        sys.exit(1)
    except (ValidationError, SQLAlchemyError) as exc:
        # Avoid printing URLs, credentials or submitted input from exception messages.
        print(f"{type(exc).__name__}: check configuration, arguments and database", file=sys.stderr)
        sys.exit(1)
    finally:
        if engine is not None:
            engine.dispose()


if __name__ == "__main__":
    main()
