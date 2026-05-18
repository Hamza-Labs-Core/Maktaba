"""Pipeline daemon entry point.

``python -m maktaba_pipeline`` boots the worker loop:

1. Parses CLI flags + env vars (``MAKTABA_DATABASE_URL``,
   ``MAKTABA_PIPELINE_STAGES``, ``MAKTABA_PIPELINE_WORKER_ID``, …).
2. Connects to the database via :class:`maktaba_pipeline.runtime.Database`.
3. Starts the :class:`~maktaba_pipeline.pipeline.runner.ClaimLoop`
   alongside the heartbeat ticker (per-job, scoped inside the
   dispatch) and the periodic :class:`~maktaba_pipeline.pipeline.reaper.Reaper`.
4. Optionally starts the in-process gRPC server when
   ``MAKTABA_PIPELINE_GRPC_ADDR`` is set so the API can call
   ``Embed`` / ``ListBackends`` / ``ExtractEmbeddedSubtitle`` over
   the wire (architecture §9.9).
5. Blocks until SIGTERM/SIGINT, then drains in-flight work and exits.

``python -m maktaba_pipeline doctor`` short-circuits to a probe-only
exit so the compose healthcheck has somewhere to land before the
operator has a real DB on hand.
"""

from __future__ import annotations

import argparse
import asyncio
import os
import sys
from typing import Any

from . import __version__
from .db.jobs import Stage
from .handlers import build_real_dispatch
from .log import init as init_log
from .runtime import Database, RuntimeConfig, run

_DEFAULT_STAGES = (
    Stage.PROBE,
    Stage.EXTRACT,
    Stage.TRANSCRIBE,
    Stage.SUBTITLE_GEN,
    Stage.INDEX,
    Stage.THUMBNAIL,
)


def _doctor(log: Any) -> int:
    """Probe-only startup. Always returns 0 until Story 22.3 §2.7 fills in
    the real DB / Chroma / ffmpeg / MLX checks."""

    log.info("doctor stub OK", version=__version__)
    return 0


def _bootstrap_log() -> Any:
    return init_log(
        service="pipeline",
        env=os.getenv("MAKTABA_ENV", "dev"),
        version=__version__,
    )


def _parse_stages(value: str | None) -> tuple[Stage, ...]:
    if not value:
        return _DEFAULT_STAGES
    out: list[Stage] = []
    for token in value.split(","):
        name = token.strip().lower()
        if not name:
            continue
        try:
            out.append(Stage(name))
        except ValueError as exc:
            raise SystemExit(
                f"unknown stage {name!r}; valid: {', '.join(s.value for s in Stage)}",
            ) from exc
    if not out:
        return _DEFAULT_STAGES
    return tuple(out)


def _build_runtime_config(args: argparse.Namespace) -> RuntimeConfig:
    return RuntimeConfig(
        database_url=args.database_url,
        stages=_parse_stages(args.stages),
        worker_id=args.worker_id,
        heartbeat_sec=args.heartbeat_sec,
        stale_claim_sec=args.heartbeat_sec * 18.0,
        reaper_interval_sec=args.reaper_interval_sec,
        claim_poll_sec=args.claim_poll_sec,
        claim_poll_max_sec=args.claim_poll_max_sec,
    )


async def _serve(args: argparse.Namespace, log: Any) -> int:
    cfg = _build_runtime_config(args)
    log.info(
        "pipeline_starting",
        stages=[s.value for s in cfg.stages],
        worker_id=cfg.worker_id,
        database_url_scheme=cfg.database_url.split(":", 1)[0],
    )
    database = await Database.connect(cfg.database_url)

    grpc_addr = args.grpc_addr
    grpc_task: asyncio.Task[None] | None = None
    grpc_server: Any = None
    if grpc_addr:
        try:
            # Lazy import so callers without grpcio installed can still
            # run the queue worker on its own.
            from .grpc_server import serve_grpc

            grpc_server, grpc_task = await serve_grpc(addr=grpc_addr)
            log.info("pipeline_grpc_listening", addr=grpc_addr)
        except Exception as exc:  # noqa: BLE001 — startup-only branch
            log.warning("pipeline_grpc_disabled", reason=str(exc))

    try:
        # Track R1: feed the real per-stage adapter map. Stages without
        # a thin-wrapper adapter (TRANSCRIBE, SUBTITLE_GEN,
        # INDEX, THUMBNAIL) are absent from the map and keep the
        # runtime's placeholder handler until their real orchestration
        # lands — see maktaba_pipeline.handlers.
        return await run(cfg, db=database, dispatch_overrides=build_real_dispatch())
    finally:
        if grpc_server is not None:
            await grpc_server.stop(grace=1.0)
        if grpc_task is not None:
            try:
                await asyncio.wait_for(grpc_task, timeout=2.0)
            except (TimeoutError, asyncio.TimeoutError):  # noqa: UP041
                grpc_task.cancel()
        await database.close()


def _build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="maktaba-pipeline",
        description="Maktaba pipeline worker daemon (Stories 1–6, gated on Epic 7's enqueues).",
    )
    parser.add_argument("--version", "-V", action="store_true", help="Print version and exit.")
    subparsers = parser.add_subparsers(dest="command")

    run_p = subparsers.add_parser("run", help="Run the worker loop (default).")
    run_p.add_argument(
        "--database-url",
        default=os.getenv("MAKTABA_DATABASE_URL", "sqlite:///:memory:"),
        help="Database URL (postgres://… or sqlite:///…). Default reads MAKTABA_DATABASE_URL.",
    )
    run_p.add_argument(
        "--stages",
        default=os.getenv("MAKTABA_PIPELINE_STAGES"),
        help="Comma-separated stage filter (probe,extract,transcribe,subtitle_gen,index,…).",
    )
    run_p.add_argument(
        "--worker-id",
        default=os.getenv("MAKTABA_PIPELINE_WORKER_ID"),
        help="Override the host/pid/uuid worker id used by the reaper.",
    )
    run_p.add_argument(
        "--heartbeat-sec",
        type=float,
        default=float(os.getenv("MAKTABA_PIPELINE_HEARTBEAT_SEC", "5.0")),
        help="Heartbeat cadence; stale_claim_sec = 18× this value.",
    )
    run_p.add_argument(
        "--reaper-interval-sec",
        type=float,
        default=float(os.getenv("MAKTABA_PIPELINE_REAPER_INTERVAL_SEC", "30.0")),
        help="Reaper sweep cadence.",
    )
    run_p.add_argument(
        "--claim-poll-sec",
        type=float,
        default=float(os.getenv("MAKTABA_PIPELINE_CLAIM_POLL_SEC", "1.0")),
        help="Safety-net claim-poll cadence (notify path normally wakes sooner).",
    )
    run_p.add_argument(
        "--claim-poll-max-sec",
        type=float,
        default=float(os.getenv("MAKTABA_PIPELINE_CLAIM_POLL_MAX_SEC", "30.0")),
        help="Exponential-backoff ceiling for the claim poll when the queue is empty.",
    )
    run_p.add_argument(
        "--grpc-addr",
        default=os.getenv("MAKTABA_PIPELINE_GRPC_ADDR"),
        help="If set, bind the in-process gRPC server (architecture §9.9) on this host:port.",
    )

    subparsers.add_parser("doctor", help="Probe-only startup check.")
    return parser


def main(argv: list[str] | None = None) -> int:
    parser = _build_parser()
    if argv is None:
        argv = sys.argv[1:]
    # Default to the `run` subcommand. argparse has no native default-
    # subparser mechanism, so we prepend it ourselves when the caller
    # passed no subcommand. Without this, run-only flags like
    # --database-url never attach to the namespace and _serve crashes
    # with AttributeError. Both the prod ENTRYPOINT (`python -m
    # maktaba_pipeline`) and the dev supervisor invoke us this way.
    _subcommands = {"run", "doctor"}
    _top_level_flags = {"--version", "-V", "--help", "-h"}
    if not any(a in _subcommands for a in argv) and not any(a in _top_level_flags for a in argv):
        argv = ["run", *argv]
    args = parser.parse_args(argv)

    if args.version:
        sys.stdout.write(f"{__version__}\n")
        sys.stdout.flush()
        return 0

    log = _bootstrap_log()

    command = args.command or "run"
    if command == "doctor":
        return _doctor(log)
    if command == "run":
        return asyncio.run(_serve(args, log))

    parser.print_help()
    return 2


if __name__ == "__main__":
    raise SystemExit(main())
