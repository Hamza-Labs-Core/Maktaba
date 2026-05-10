"""CLI entrypoint for ``maktaba-pipeline scan`` (Story 1.4 AC #3).

The dry-run flag walks a configured library root, hashes every
candidate, and emits one JSONL line per would-be insert to stdout.
No database access is required: the CLI synthesises a
:class:`LibraryRecord` from ``--root`` and pipes the orchestrator
through a :class:`DryRunStore`.

The ``--cancel`` flag is the cross-process companion to the API's
``DELETE /api/libraries/{id}/scan``. Implementing it requires the
:class:`ScanControlStore` Postgres backend that lands with Story 1.5
(connection wrapper); until then the CLI prints a deferral message
and exits 64 (EX_USAGE).
"""

from __future__ import annotations

import argparse
import asyncio
import sys
import uuid
from collections.abc import Sequence
from typing import Any

from .dryrun import DryRunStore
from .service import LibraryRecord, Scanner, ScanOptions

__all__ = ["build_parser", "main"]


class _StderrLogger:
    """Minimal structlog-shaped logger that writes WARN/ERROR to stderr.

    The dry-run path reserves stdout for JSONL output, so the CLI uses
    a logger that never touches stdout. INFO and DEBUG events are
    silently dropped — operators rely on the JSONL totals plus the
    process exit code.
    """

    def info(self, event: str, **kwargs: Any) -> Any:
        del event, kwargs
        return None

    def debug(self, event: str, **kwargs: Any) -> Any:
        del event, kwargs
        return None

    def warning(self, event: str, **kwargs: Any) -> Any:
        sys.stderr.write(f"warn: {event} {_fmt(kwargs)}\n")
        return None

    def error(self, event: str, **kwargs: Any) -> Any:
        sys.stderr.write(f"error: {event} {_fmt(kwargs)}\n")
        return None


def _fmt(kwargs: dict[str, Any]) -> str:
    return " ".join(f"{k}={v}" for k, v in kwargs.items())


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="maktaba-pipeline scan",
        description="Scan a library root. With --dry-run, emits JSONL of "
        "would-be inserts and writes nothing to the database.",
    )
    parser.add_argument(
        "--library",
        default="cli-dry-run",
        help="Library name (cosmetic in --dry-run mode).",
    )
    parser.add_argument(
        "--root",
        action="append",
        default=[],
        help="Filesystem root to walk. May be passed multiple times.",
    )
    parser.add_argument(
        "--library-id",
        default=None,
        help="Library UUID. Defaults to a deterministic synthetic UUID "
        "in --dry-run mode so the JSONL output stays diff-stable.",
    )
    parser.add_argument(
        "--follow-symlinks",
        action="store_true",
        help="Follow symbolic links during the walk (default: false).",
    )
    parser.add_argument(
        "--dry-run",
        action="store_true",
        help="Print would-be inserts to stdout; write nothing.",
    )
    parser.add_argument(
        "--cancel",
        action="store_true",
        help="Request cancellation of a running scan and exit. Requires the "
        "Postgres connection wrapper from Story 1.5; not yet wired.",
    )
    return parser


def main(argv: Sequence[str] | None = None) -> int:
    args = build_parser().parse_args(argv)

    if args.dry_run and args.cancel:
        sys.stderr.write("error: --dry-run and --cancel are mutually exclusive\n")
        return 2

    if args.cancel:
        # AC2's cancel path is owned by the API service; the CLI hook
        # is a thin UPDATE wrapper that needs the Story 1.5 connection
        # wrapper. Until that lands the CLI surfaces the deferral
        # explicitly so callers don't think the request was honoured.
        sys.stderr.write(
            "error: --cancel requires the Postgres connection wrapper "
            "from Story 1.5; use DELETE /api/libraries/{id}/scan "
            "instead.\n"
        )
        return 64

    if not args.dry_run:
        sys.stderr.write(
            "error: this CLI only implements --dry-run today. The "
            "non-dry-run path is the responsibility of the API service "
            "(POST /api/libraries/{id}/scan).\n"
        )
        return 64

    if not args.root:
        sys.stderr.write("error: --dry-run requires at least one --root\n")
        return 2

    log = _StderrLogger()

    if args.library_id:
        library_id = uuid.UUID(args.library_id)
    else:
        library_id = _stable_dryrun_uuid(args.library)
    library = LibraryRecord(
        id=library_id,
        name=args.library,
        roots=tuple(args.root),
        disabled=False,
        follow_symlinks=args.follow_symlinks,
    )

    store = DryRunStore(library=library, writer=sys.stdout)
    scanner = Scanner(store=store, log=log)

    result = asyncio.run(scanner.run(library_id, ScanOptions(dry_run=True)))

    return 0 if not result.errors else 1


def _stable_dryrun_uuid(name: str) -> uuid.UUID:
    """Deterministic UUID derived from a library name.

    Used so two ``--dry-run`` invocations against the same library
    name emit identical ``library_id`` fields in the JSONL output —
    handy for diff-based regression tests and for piping the output
    into ``jq`` without the noise of a fresh UUID per run.
    """
    return uuid.uuid5(uuid.NAMESPACE_URL, f"maktaba://dry-run/{name}")


if __name__ == "__main__":  # pragma: no cover — entrypoint shim
    sys.exit(main())
