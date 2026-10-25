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

``python -m maktaba_pipeline doctor`` runs the health checks (database,
schema revision, job queue, ffmpeg/ffprobe, writable storage; see
:mod:`maktaba_pipeline.doctor`) and exits non-zero when one fails; it is
the compose healthcheck.
"""

from __future__ import annotations

import argparse
import asyncio
import contextlib
import os
import sys
import threading
from typing import Any

from . import __version__
from .cluster import exitcodes
from .cluster.config import NodeConfig
from .cluster.config import load as load_node_config
from .db.jobs import Stage
from .engine.workers import stable_hostname
from .handlers import build_real_dispatch
from .log import init as init_log
from .log.hooks import asyncio_exception_handler
from .log.hooks import install as install_log_hooks
from .runtime import Database, RuntimeConfig, run

# Only stages with a REAL handler in handlers.build_real_dispatch()
# (SCAN, PROBE, EXTRACT, TRANSCRIBE, SUBTITLE_GEN, INDEX, THUMBNAIL)
# belong here. THUMBNAIL now has a real adapter
# (`handlers.thumbnail_handler`, registered in `build_real_dispatch`),
# so a default worker claiming a THUMBNAIL job runs the real FFmpeg
# poster/sprite/chapter generation rather than the silent no-op drain —
# it is therefore safe in the defaults. See the defaults-vs-real-handlers
# invariant in tests/pipeline/test_real_dispatch_wiring.py. INDEX enqueues
# the THUMBNAIL job once the video reaches INDEXED.
#
# Gap-closure (HLB-257/255): SCAN now has a real library-scoped
# adapter (`handlers.scan_handler`, registered in `build_real_dispatch`)
# backed by slot 0058 + `SqlScanStore`, so it is safe in the defaults —
# a default worker claiming a SCAN job runs the real walk + per-video
# PROBE enqueue, not the silent no-op drain. SCAN leads the tuple
# because it is the pipeline's entry stage (it produces the videos every
# later stage consumes); the per-video PROBE -> EXTRACT -> TRANSCRIBE
# chain follows, then TRANSCRIBE fans out to SUBTITLE_GEN + INDEX in
# parallel (the order between those two is functionally irrelevant —
# both consume the same activated transcript and share the replay-
# guarded TRANSCRIBED -> INDEXED FSM edge).
_DEFAULT_STAGES = (
    Stage.SCAN,
    Stage.PROBE,
    Stage.EXTRACT,
    Stage.TRANSCRIBE,
    Stage.SUBTITLE_GEN,
    Stage.INDEX,
    Stage.THUMBNAIL,
)


def _doctor(args: argparse.Namespace, log: Any) -> int:
    """The health checks; 1 when any fails. The database is the one ``run`` would use:
    ``--database-url`` / ``MAKTABA_DATABASE_URL``, else this machine's joined node."""
    from .doctor import run_doctor

    url = args.database_url
    if not url:
        node = load_node_config()
        url = node.database_url if node is not None and node.joined else None
    code = asyncio.run(run_doctor(url, out=sys.stdout, quiet=args.quiet))
    log.info("doctor", version=__version__, healthy=code == 0)
    return code


def _bootstrap_log() -> Any:
    ring_cap_raw = os.getenv("MAKTABA_LOG_RING_CAPACITY")
    ring_capacity = (
        int(ring_cap_raw) if ring_cap_raw and ring_cap_raw.lstrip("-").isdigit() else None
    )
    log = init_log(
        service="pipeline",
        env=os.getenv("MAKTABA_ENV", "dev"),
        version=__version__,
        ring_capacity=ring_capacity,
    )
    # library (stdlib logging) warnings and uncaught thread/process exceptions go
    # through the same structured chain
    install_log_hooks()
    return log


def _parse_stages(value: str | None) -> tuple[str, ...]:
    """Explicit ``--stages`` list; empty means "every stage this worker has a handler for".

    Names are validated against the stage graph when the worker starts (extension
    stages are only known after discovery), so a typo is reported there.
    """
    if not value:
        return ()
    out = [t.strip().lower() for t in value.split(",") if t.strip()]
    return tuple(out)


_FALLBACK_DATABASE_URL = "sqlite:///:memory:"


def _node_for(args: argparse.Namespace) -> NodeConfig | None:
    """Node mode (Epic 31 design section 7): no ``--database-url`` (nor
    ``MAKTABA_DATABASE_URL``) and a joined node config on this machine; ``--node``
    (``MAKTABA_NODE=1``) asks for it whatever the database URL says."""
    if args.database_url and not getattr(args, "node", False):
        return None
    node = load_node_config()
    return node if node is not None and node.joined else None


def _build_runtime_config(
    args: argparse.Namespace, node: NodeConfig | None = None
) -> RuntimeConfig:
    worker_id = args.worker_id
    host = args.host
    database_url = args.database_url or _FALLBACK_DATABASE_URL
    if node is not None:
        database_url = node.database_url
        worker_id = worker_id or f"node-{node.node8}-{os.getpid()}"
        host = host or stable_hostname()
    return RuntimeConfig(
        database_url=database_url,
        stages=_parse_stages(args.stages),
        exclude_stages=_parse_stages(args.exclude_stages),
        host=host,
        path_map=args.path_map or "",
        engine=not args.no_engine,
        share=not getattr(args, "no_share", False),
        worker_id=worker_id,
        role="node" if node is not None else "home",
        node_id=node.node_id if node is not None else None,
        heartbeat_sec=args.heartbeat_sec,
        stale_claim_sec=args.heartbeat_sec * 18.0,
        reaper_interval_sec=args.reaper_interval_sec,
        claim_poll_sec=args.claim_poll_sec,
        claim_poll_max_sec=args.claim_poll_max_sec,
        shutdown_drain_sec=args.drain_sec,
    )


async def _serve(args: argparse.Namespace, log: Any) -> int:
    # "Task exception was never retrieved" and failing callbacks, as structured lines
    asyncio.get_running_loop().set_exception_handler(asyncio_exception_handler)
    try:
        node = _node_for(args)
    except ValueError as exc:
        # a node config it cannot run as (no node_id or private_key): 78, not a
        # traceback on every restart
        sys.stderr.write(f"{exc}\n")
        return exitcodes.EX_CONFIG
    if getattr(args, "node", False) and node is None:
        sys.stderr.write("this machine has not joined a home: run maktaba-pipeline join\n")
        return exitcodes.EX_CONFIG
    cfg = _build_runtime_config(args, node)
    if cfg.engine is False and node is not None:
        log.error("node_mode_needs_the_engine")
        return 2
    log.info(
        "pipeline_starting",
        stages=[str(s) for s in cfg.stages] or "all",
        worker_id=cfg.worker_id,
        database_url_scheme=cfg.database_url.split(":", 1)[0],
        role=cfg.role,
        node_id=cfg.node_id,
        home=node.home.name or node.home.url if node is not None else None,
    )
    try:
        database = await Database.connect(cfg.database_url)
    except Exception as exc:
        if node is not None and exitcodes.login_revoked(exc):
            # the home revoked this machine (password rotated, NOLOGIN): for good
            log.error("cluster_node_login_refused", error=str(exc))
            return exitcodes.EX_NOPERM
        if exitcodes.is_auth_error(exc):
            # refused for another reason (no pg_hba.conf entry, ...): fixable on the
            # home, so exit 1 and let the service try again
            log.error("database_login_refused", error=str(exc))
            return 1
        raise

    # Recent-logs HTTP endpoint (/logs/recent) on a daemon thread so the
    # API's diagnostics export can proxy this service's ring buffer.
    # Opt-in via --logs-addr / $MAKTABA_PIPELINE_LOGS_ADDR.
    log_server: Any = None
    heartbeat_task: asyncio.Task[None] | None = None
    if args.logs_addr:
        try:
            from .log.http import start_log_server

            log_server = start_log_server(args.logs_addr)
            log.info("pipeline_logs_listening", addr=args.logs_addr)
        except Exception as exc:  # noqa: BLE001 — startup-only branch
            log.warning("pipeline_logs_disabled", reason=str(exc))
        else:
            # /healthz on the same port reports this loop's heartbeat; the
            # maktaba-server supervisor restarts the process when it stalls.
            from .log.liveness import LoopHeartbeat, run_heartbeat, set_heartbeat

            heartbeat = LoopHeartbeat()
            set_heartbeat(heartbeat)
            heartbeat_task = asyncio.create_task(run_heartbeat(heartbeat))

    grpc_addr = args.grpc_addr
    grpc_task: asyncio.Task[None] | None = None
    grpc_server: Any = None
    if grpc_addr:
        try:
            # Lazy import so callers without grpcio installed can still
            # run the queue worker on its own.
            from .grpc_server import serve_grpc

            grpc_server, grpc_task = await serve_grpc(
                addr=grpc_addr,
                database=database,
                semantic=not getattr(args, "no_semantic", False),
            )
            log.info("pipeline_grpc_listening", addr=grpc_addr)
        except Exception as exc:  # noqa: BLE001 — startup-only branch
            log.warning("pipeline_grpc_disabled", reason=str(exc))

    code = 1  # run raised
    failure: BaseException | None = None
    try:
        # Track R1: feed the real per-stage adapter map. All seven stages
        # (SCAN, PROBE, EXTRACT, TRANSCRIBE, SUBTITLE_GEN, INDEX,
        # THUMBNAIL) now have a thin-wrapper adapter and are in the map,
        # so every stage in _DEFAULT_STAGES runs real work rather than a
        # silent no-op drain — see maktaba_pipeline.handlers.
        code = await run(cfg, db=database, dispatch_overrides=build_real_dispatch())
        return code
    except BaseException as exc:
        failure = exc
        raise
    finally:
        if grpc_server is not None:
            await grpc_server.stop(grace=1.0)
        if grpc_task is not None:
            try:
                await asyncio.wait_for(grpc_task, timeout=2.0)
            except (TimeoutError, asyncio.TimeoutError):  # noqa: UP041
                grpc_task.cancel()
        if heartbeat_task is not None:
            heartbeat_task.cancel()
        if log_server is not None:
            log_server.shutdown()
        await database.close()
        await _close_search_index(log)
        await _exit_if_threads_linger(code, log, failure)


#: after the runtime stopped: how long a write to the search index may still take,
#: and then how long the other threads still running (a read from a share that does not
#: answer, a download) may take before the process exits without them
_INDEX_CLOSE_WAIT_SEC = 10.0
_EXIT_WAIT_SEC = 5.0


async def _close_search_index(log: Any) -> None:
    """No new write to the search index, wait for the one running, close the Chroma
    clients (search/gate.py): the files are whole when the process ends."""
    from .search import gate, semantic

    gate.close()
    if not await asyncio.to_thread(gate.wait_idle, _INDEX_CLOSE_WAIT_SEC):
        # never close the client under a write: the exit waits for that thread below
        log.warning("search_index_write_still_running", after_sec=_INDEX_CLOSE_WAIT_SEC)
        return
    closed = await asyncio.to_thread(semantic.close_clients)
    if closed:
        log.info("search_index_closed", clients=closed)


def _lingering_threads() -> list[threading.Thread]:
    me = threading.current_thread()
    return [t for t in threading.enumerate() if t is not me and t.is_alive() and not t.daemon]


async def _exit_if_threads_linger(
    code: int, log: Any, failure: BaseException | None = None
) -> None:
    """The worker stopped: exit now, without the threads still running. Python's exit
    waits for every one of them (asyncio's executor up to 5 minutes), and the supervisor
    killed the process at the end of its grace instead: a kill during a search index
    write broke the index. The write itself is waited for (_close_search_index)."""
    from .search import gate

    # a search index write still running is waited for, never cut (a second wait:
    # _close_search_index gave up on it): past it the process exits the slow way, once
    # the write's thread ended, or at the supervisor's kill at the end of its grace
    if not await asyncio.to_thread(gate.wait_idle, _INDEX_CLOSE_WAIT_SEC):
        log.warning("search_index_write_still_running", after_sec=2 * _INDEX_CLOSE_WAIT_SEC)
        return
    from .search import semantic

    await asyncio.to_thread(semantic.close_clients)  # when the write ended late
    loop: Any = asyncio.get_running_loop()  # the base loop's shutdown takes a timeout
    with contextlib.suppress(Exception):  # the threads are counted below
        await loop.shutdown_default_executor(timeout=_EXIT_WAIT_SEC)
    left = _lingering_threads()
    if not left:
        return
    if failure is not None and not isinstance(failure, asyncio.CancelledError):
        log.error("pipeline_runtime_failed", exc_info=failure)  # its traceback, said once
    log.warning("pipeline_exit_threads_left", threads=[t.name for t in left], exit_code=code)
    for stream in (sys.stdout, sys.stderr):
        with contextlib.suppress(Exception):
            stream.flush()
    os._exit(code)


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
        default=os.getenv("MAKTABA_DATABASE_URL") or None,
        help="Database URL (postgres://… or sqlite:///…). Default reads MAKTABA_DATABASE_URL; "
        "without either, a machine that joined a home (`join`) runs as its node.",
    )
    run_p.add_argument(
        "--node",
        action="store_true",
        default=os.getenv("MAKTABA_NODE") == "1",
        help="Run as this machine's node of a home (MAKTABA_NODE=1); exit 78 when it has "
        "not joined one. Exit codes: 77 when the home refused this machine for good "
        "(its sealed revocation, or the database: revoked, removed, login refused): do not "
        "restart; 78 for --node without a "
        "joined node config.",
    )
    run_p.add_argument(
        "--stages",
        default=os.getenv("MAKTABA_PIPELINE_STAGES"),
        help="Comma-separated stage filter (probe,extract,transcribe,subtitle_gen,index,…). "
        "Default: every stage in the graph this worker has a handler for.",
    )
    run_p.add_argument(
        "--exclude-stages",
        default=os.getenv("MAKTABA_PIPELINE_EXCLUDE_STAGES"),
        help="Comma-separated stages this worker must NOT serve, for example "
        "extract,transcribe,subtitle_gen,index on a host that is not the GPU worker.",
    )
    run_p.add_argument(
        "--host",
        default=os.getenv("MAKTABA_WORKER_HOST"),
        help="This host's name in the worker registry (default: the machine hostname).",
    )
    run_p.add_argument(
        "--path-map",
        default=os.getenv("MAKTABA_WORKER_PATH_MAP"),
        help="This host's mount points, 'db_prefix=local_prefix;...', for example "
        "'Y:/=/Volumes/Y;O:/=/Volumes/O' on a Mac. The database keeps POSIX paths as scanned.",
    )
    run_p.add_argument(
        "--no-engine",
        action="store_true",
        default=os.getenv("MAKTABA_PIPELINE_NO_ENGINE") == "1",
        help="Run the original per-stage loop without the queue-graph engine.",
    )
    run_p.add_argument(
        "--no-share",
        action="store_true",
        default=os.getenv("MAKTABA_PIPELINE_SHARE") == "off",
        help="Never touch the media share (MAKTABA_PIPELINE_SHARE=off): no storage checks, "
        "no share probes, no path conversion. The supervisor's index worker runs so: a "
        "share that stalls cannot hold it, nor get it killed in the middle of a write.",
    )
    run_p.add_argument(
        "--no-semantic",
        action="store_true",
        default=os.getenv("MAKTABA_PIPELINE_SEMANTIC") == "off",
        help="The gRPC server answers no Embed / SemanticSearch and never opens the search "
        "index (MAKTABA_PIPELINE_SEMANTIC=off): another worker (the index worker) holds it, "
        "and Chroma wants one process per directory.",
    )
    run_p.add_argument(
        "--drain-sec",
        type=float,
        default=float(os.getenv("MAKTABA_PIPELINE_DRAIN_SEC", "15.0")),
        help="On shutdown, how long the jobs in flight may take before they are cancelled "
        "(MAKTABA_PIPELINE_DRAIN_SEC; the supervisor gives the index worker longer).",
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
    run_p.add_argument(
        "--logs-addr",
        default=os.getenv("MAKTABA_PIPELINE_LOGS_ADDR"),
        help="If set, bind the recent-logs HTTP endpoint (/logs/recent) on this host:port.",
    )

    doctor_p = subparsers.add_parser(
        "doctor",
        help="Check the database, schema, queue, ffmpeg and storage; exit 1 on a failure.",
    )
    doctor_p.add_argument(
        "--database-url",
        default=os.getenv("MAKTABA_DATABASE_URL"),
        help="Database to check (default: MAKTABA_DATABASE_URL, else the joined node's).",
    )
    doctor_p.add_argument("--quiet", action="store_true", help="Print only the checks that fail.")

    join_p = subparsers.add_parser(
        "join",
        help="Ask the Maktaba home on this network to take this machine as a worker.",
    )
    join_p.add_argument(
        "--home",
        default=None,
        help="The home's address (http://192.168.1.20:8080); skips discovery.",
    )
    join_p.add_argument("--name", default=None, help="This machine's name on the home.")
    join_p.add_argument(
        "--run", action="store_true", help="Start working as soon as the home approves."
    )
    join_p.add_argument(
        "--rejoin",
        action="store_true",
        help="Start over on a machine that already joined (or joined before the home had a "
        "cluster key): the old settings are kept as node.json.bak and the home sees a new "
        "machine (remove the old one there, set its path map and switches again).",
    )
    join_p.add_argument("--yes", "-y", action="store_true", help="Do not ask before re-joining.")
    from .cluster.cli import add_parser as add_cluster_parser

    add_cluster_parser(subparsers)
    from .sharepath.cli import add_parser as add_storage_parser

    add_storage_parser(subparsers)
    from .models.fetch_cli import add_parser as add_models_parser

    add_models_parser(subparsers)
    return parser


def _join(args: argparse.Namespace, parser: argparse.ArgumentParser, log: Any) -> int:
    from .cluster.join import JoinError, join

    interactive = sys.stdin.isatty()
    try:
        join(
            home_url=args.home,
            name=args.name,
            ask=input if interactive else None,
            rejoin=args.rejoin,
            assume_yes=args.yes,
        )
    except JoinError as exc:
        sys.stderr.write(f"join failed: {exc}\n")
        return 1
    except KeyboardInterrupt:
        sys.stderr.write("\njoin cancelled\n")
        return 130
    if not args.run:
        sys.stdout.write("Start it with: maktaba-pipeline run\n")
        return 0
    run_args = parser.parse_args(["run"])
    # it just joined: run as that node, whatever MAKTABA_DATABASE_URL says
    run_args.database_url = None
    return asyncio.run(_serve(run_args, log))


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
    _subcommands = {"run", "doctor", "join", "cluster", "storage", "models"}
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
        return _doctor(args, log)
    if command == "run":
        return asyncio.run(_serve(args, log))
    if command == "cluster":
        from .cluster.cli import main as cluster_main

        return cluster_main(args)
    if command == "storage":
        from .sharepath.cli import main as storage_main

        return storage_main(args)
    if command == "join":
        return _join(args, parser, log)
    if command == "models":
        from .models.fetch_cli import main as models_main

        return models_main(args)

    parser.print_help()
    return 2


if __name__ == "__main__":
    raise SystemExit(main())
