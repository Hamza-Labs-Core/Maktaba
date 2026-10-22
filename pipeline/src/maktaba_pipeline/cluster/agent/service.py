"""Starting the agent with a worker (``runtime.py``), and stopping it with it.

:func:`run_agent` is one background task of every engine worker, both roles, unless
``MAKTABA_CLUSTER_AGENT=0``. It finds the home (a machine: its node config's home URL,
pairing key and the home's cluster key; a home process: ``MAKTABA_API_URL``, default
``http://127.0.0.1:8080``, and the home agent key file), this machine's folder on the
share, starts the files writer, the log shipper and the job sinks, attaches the LLM
door, and runs the channel; the worker cancels it when it stops. It never raises: a
failure to start is logged and the worker carries on without an agent.
"""

from __future__ import annotations

import asyncio
import contextlib
import os
import platform
import re
from collections.abc import Awaitable, Callable, Mapping
from pathlib import Path
from typing import Any

from ... import __version__
from ... import llm as llm_door
from ... import relay as relay_door
from ...engine.paths import active_path_map
from ...llm.runtimes import LLMRuntimes
from ...llm.service import LocalLLM
from ...log import get_logger
from .. import crypto, exitcodes, share
from .client import AgentIdentity, ChannelAgent, Commands
from .files import ClusterFiles, safe_name
from .jobs import JobTracker, tracker
from .logship import AGENT_LOGGER, LogShipper, normalize_level
from .metrics import MetricsCollector, PipelineView

__all__ = [
    "DEFAULT_API_URL",
    "JobsFileSink",
    "build_identity",
    "run_agent",
    "run_for_worker",
    "status_dir",
]

DEFAULT_API_URL = "http://127.0.0.1:8080"
#: a safe IP folder name (the hub's rule)
_IP_DIR = re.compile(r"[0-9A-Za-z.:%-]{1,64}")
#: a job's progress goes to the jobs file at most this often (section 7.5)
PROGRESS_FILE_SEC = 10.0

_log = get_logger(logger=AGENT_LOGGER)


def status_dir() -> Path:
    """Where each local process writes its agent status (``cluster status`` reads it)."""
    return share.local_dir() / "status"


def build_identity(
    *, role: str, worker_id: str, host: str, env: Mapping[str, str] | None = None
) -> AgentIdentity | None:
    env = os.environ if env is None else env
    if role == "node":
        from .. import config as node_config

        cfg = node_config.load()
        if cfg is None or not cfg.home.url:
            _log.warning("cluster_agent_no_node_config")
            return None
        home_static: bytes | None = None
        if cfg.home.cluster_pub:
            try:
                home_static = crypto.b64d(cfg.home.cluster_pub)
            except ValueError:
                home_static = None
        if home_static is None or len(home_static) != 32:
            home_static = None
            _log.error(
                "cluster_agent_rejoin_needed",
                reason="the node config has no home cluster key: re-join this machine with "
                "`maktaba-pipeline join --rejoin` (the worker keeps pulling jobs without "
                "the channel)",
            )
        return AgentIdentity(
            kind="node",
            url=cfg.home.url,
            worker_id=worker_id,
            host=host,
            role=role,
            node_id=cfg.node_id,
            name=cfg.name,
            node_private=crypto.b64d(cfg.private_key),
            home_static=home_static,
        )
    return AgentIdentity(
        kind="home",
        url=(env.get("MAKTABA_API_URL") or DEFAULT_API_URL).strip(),
        worker_id=worker_id,
        host=host,
        role=role,
        name=host,
        home_key_path=share.home_key_path(env),
    )


class JobsFileSink:
    """Job events to the jobs file: the frame without ``t``; a job's ``progress`` at
    most every 10 s (every other event is written)."""

    def __init__(self, files: ClusterFiles, *, every: float = PROGRESS_FILE_SEC) -> None:
        import time

        self.files = files
        self.every = every
        self.clock = time.monotonic
        self._last: dict[int, float] = {}

    def __call__(self, event: dict[str, Any]) -> None:
        job_id = int(event.get("job_id") or 0)
        if event.get("event") == "progress":
            now = self.clock()
            if now - self._last.get(job_id, float("-inf")) < self.every:
                return
            self._last[job_id] = now
        elif event.get("event") != "claimed":
            self._last.pop(job_id, None)
        self.files.write("jobs", {k: v for k, v in event.items() if k != "t"})


def _settings_hook(
    files: ClusterFiles, locator: share.ShareLocator | None = None
) -> Callable[[Mapping[str, Any]], None]:
    """``welcome.settings``: the hub's share root wins over this process's own guess;
    its retention and cap apply unless this machine's environment sets them."""

    def apply(settings: Mapping[str, Any]) -> None:
        files.apply_settings(
            retention_days=_int(settings.get("retention_days")),
            file_mb=_int(settings.get("file_mb")),
        )
        loop = asyncio.get_running_loop()  # probing a share can block: off the loop
        ip = settings.get("ip")
        if (
            isinstance(ip, str)
            and _IP_DIR.fullmatch(ip)
            and ".." not in ip
            and share.folder_name(ip) != files.ip
        ):
            # the hub names the folder it files this process under
            loop.run_in_executor(None, files.set_ip, share.folder_name(ip))
        root = settings.get("share_root")
        if isinstance(root, str) and root.strip() and not os.environ.get(share.ENV_CLUSTER_DIR):
            if locator is not None:
                # resolved through this machine's map, now and at every retry
                locator.share_root, locator.how = root.strip(), f"share root {root.strip()}"
                loop.run_in_executor(None, files.relocate)
            else:
                target, how, anchor = share.locate_cluster_dir(root.strip(), active_path_map())
                if target != files.share_dir:
                    loop.run_in_executor(None, files.set_share_dir, target, how, anchor)

    return apply


def _refused(shutdown: asyncio.Event) -> Callable[[str], None]:
    """The home revoked this machine, in a sealed frame: the worker stops, exit code 77."""

    def stop(reason: str) -> None:
        exitcodes.mark_refused(reason)
        shutdown.set()

    return stop


def _int(value: Any) -> int | None:
    return int(value) if isinstance(value, int | float) and not isinstance(value, bool) else None


async def run_agent(
    *,
    database: Any,
    role: str,
    worker_id: str,
    host: str,
    shutdown: asyncio.Event,
    stages: Callable[[], tuple[str, ...]] = tuple,
    stages_off: Callable[[], dict[str, str]] = dict,
    drain: Callable[[bool], Awaitable[str]] | None = None,
    reload_stages: Callable[[], Awaitable[str]] | None = None,
    health: Callable[[bool], Mapping[str, Any]] | None = None,
    check_membership: Callable[[], Awaitable[None]] | None = None,
    job_tracker: JobTracker | None = None,
    paused: Callable[[], str | None] | None = None,
    pause_listeners: list[Callable[[], None]] | None = None,
) -> None:
    files: ClusterFiles | None = None
    shipper: LogShipper | None = None
    agent: ChannelAgent | None = None
    jobs_sink: JobsFileSink | None = None
    jt = job_tracker or tracker()
    try:
        ident = build_identity(role=role, worker_id=worker_id, host=host)
        if ident is None:
            return
        ip = await asyncio.to_thread(share.machine_ip, ident.url)
        ident.ip = ip
        ident.os, ident.version = AgentIdentity.describe_os()
        root: str | None
        try:
            root, how = await asyncio.wait_for(share.find_share_root(database), 30)
        except (TimeoutError, asyncio.TimeoutError):  # noqa: UP041
            root, how = None, "share lookup timed out"
        # the share root goes through the path map active when the files are placed
        # (and at every retry): a node paused at start has no map yet
        locator = share.ShareLocator(root, how, path_map=active_path_map)
        files = await asyncio.to_thread(
            ClusterFiles, None, ip, worker_id, reason=how, locate=locator
        )
        await asyncio.to_thread(
            files.set_identity,
            {
                "worker_id": worker_id,
                "kind": ident.kind,
                "node_id": ident.node_id,
                "node_name": ident.name,
                "host": host,
                "os": platform.system().lower(),
                "arch": platform.machine().lower(),
                "pid": ident.pid,
                "version": __version__,
                "ip": ip,
            },
        )
        files.start()
        shipper = LogShipper(
            normalize_level(os.environ.get("MAKTABA_CLUSTER_LOG_LEVEL")),
            file_sink=files.sink("logs"),
        )
        shipper.install()
        local = LocalLLM(LLMRuntimes(), record=files.sink("llm"))
        collector = await asyncio.to_thread(
            MetricsCollector,
            PipelineView(role=role, stages=stages, stages_off=stages_off),
            llm_stats=local.runtimes.stats,
            files_status=files.status,
            health=health,
            tracker=jt,
        )
        agent = ChannelAgent(
            ident,
            shipper=shipper,
            collector=collector,
            files=files,
            llm=local,
            commands=Commands(
                drain=drain,
                reload_stages=reload_stages,
                revoked=_refused(shutdown),
                check_membership=check_membership,
            ),
            on_settings=_settings_hook(files, locator),
            status_path=status_dir() / f"{safe_name(worker_id)}.json",
            paused=paused,
        )
        if pause_listeners is not None:
            # the status file follows the pause (the channel's state may not change)
            pause_listeners.append(agent.write_status)
        jobs_sink = JobsFileSink(files)
        jt.add_sink(jobs_sink)
        jt.add_sink(agent.job_sink)
        llm_door.attach(local, agent)
        relay_door.attach(agent)
        _log.info(
            "cluster_agent_starting",
            kind=ident.kind,
            home=ident.url,
            ip=ip,
            files=str(files.machine_dir),
            on_share=files.on_share,
            files_reason=files.reason or None,
        )
        await agent.run(shutdown)
    except asyncio.CancelledError:
        raise
    except Exception:  # noqa: BLE001 - the worker runs on without its agent
        _log.exception("cluster_agent_failed")
    finally:
        llm_door.detach()
        relay_door.detach()
        if agent is not None:
            jt.remove_sink(agent.job_sink)
            if pause_listeners is not None and agent.write_status in pause_listeners:
                pause_listeners.remove(agent.write_status)
        if jobs_sink is not None:
            jt.remove_sink(jobs_sink)
        if shipper is not None:
            shipper.uninstall()
        if files is not None:
            with contextlib.suppress(Exception):
                await asyncio.to_thread(files.stop)


async def run_for_worker(
    database: Any,
    *,
    worker_id: str,
    host: str,
    shutdown: asyncio.Event,
    worker: Any,
    health: Any = None,
) -> None:
    """The agent of an engine worker: ``worker`` is its stage selection
    (:class:`~..node.WorkerAgent`: ``cmd drain`` holds it, ``cmd reload_stages``
    re-reads it now), ``health`` its :class:`~...engine.health.HealthCollector`."""
    from ...engine.health import health_frame

    selector = worker.selector

    async def drain(hold: bool) -> str:
        selector.held = hold
        await worker.apply()
        return f"serving {len(selector.served)} stage(s)"

    async def reload_stages() -> str:
        await worker.beat()  # a node: revoked, drain, path map
        await worker.refresh()  # versions, switches, storage when due
        return f"serving {len(selector.served)} stage(s): {', '.join(selector.served)}"

    await run_agent(
        database=database,
        role=selector.role,
        worker_id=worker_id,
        host=host,
        shutdown=shutdown,
        stages=lambda: tuple(selector.served),
        stages_off=lambda: dict(selector.stages_off),
        drain=drain,
        reload_stages=reload_stages,
        health=None if health is None else (lambda final: health_frame(health, final=final)),
        # a node's heartbeat reads its cluster row: revoked or removed -> stop (exit 77)
        check_membership=worker.beat,
        # the local status file says so while the home is gone (nothing reaches it)
        paused=lambda: selector.paused or selector.unreachable,
        pause_listeners=worker.pause_listeners,
    )
