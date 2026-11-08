"""``python -m maktaba_plugin_runner --group <name>`` (Epic 54, story 54.23 §1)."""

from __future__ import annotations

import argparse
import asyncio
import contextlib
import json
import os
import sys
from typing import Any

from . import GROUPS, socket_path

#: what the supervisor never gives a runner (a test asserts the child's environment):
#: refused here too, so a runner started by hand with them does not run
FORBIDDEN_ENV = (
    "DATABASE_URL",
    "MAKTABA_DATABASE_URL",
    "MAKTABA_HOME_SECRETS",
    "MAKTABA_SUPERVISOR_TOKEN_FILE",
    "MAKTABA_PLEX_TOKEN",
    "MAKTABA_CLOUD_TOKEN",
)

EXIT_SANDBOX = 70
EXIT_UNTRUSTED = 71
EXIT_ENV = 72
#: the areas changed: the supervisor starts the runner again with them
EXIT_AREAS_CHANGED = 75
AREAS_POLL_S = 10.0


def members(group: str) -> dict[str, tuple[str, str]]:
    """stage -> (entry, plugin id) of the stages ``group`` serves (``fp``: every stage
    with a fingerprint)."""
    from maktaba_pipeline.plugins import firstparty

    out: dict[str, tuple[str, str]] = {}
    for p in firstparty.plugins():
        for name, s in p.stages.items():
            entry = str(s.get("entry") or "")
            if not entry:
                continue
            fp = s.get("fingerprint") or {}
            if (
                group == "fp"
                and (fp.get("input") or fp.get("output"))
                or firstparty.runner_group(name) == firstparty.group_of_instance(group)
            ):  # noqa: E501
                out[name] = (entry, p.id)
    return out


def log(event: str, **kw: Any) -> None:
    sys.stderr.write(json.dumps({"event": event, "service": "plugin-runner", **kw}) + "\n")
    sys.stderr.flush()


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(prog="maktaba-plugin-runner")
    ap.add_argument("--group", required=True)
    ap.add_argument("--api-socket", default=os.environ.get("MAKTABA_PLUGIN_API_SOCKET", ""))
    ap.add_argument("--run-dir", default="")
    # the Windows launcher's question (maktaba-server runner-sandbox-run): the sandbox it
    # applies from outside, printed as JSON; no plugin code is imported, nothing is served
    ap.add_argument("--policy", action="store_true")
    args = ap.parse_args(argv)
    group = args.group
    if group not in GROUPS and not group.replace("-", "").isalnum():
        ap.error(f"unknown group {group!r}")
    leaked = [k for k in FORBIDDEN_ENV if os.environ.get(k)]
    if leaked:
        log("plugin_runner_env_refused", group=group, keys=leaked)
        return EXIT_ENV
    if sys.platform == "win32":
        from .winpipe import API_ENV, BACK_ENV

        # on Windows a runner serves and calls only through the supervisor's pipes
        missing = [k for k in (BACK_ENV, API_ENV) if not os.environ.get(k)]
        if missing:
            log("plugin_runner_env_refused", group=group, missing=missing)
            return EXIT_ENV
    from maktaba_pipeline.plugins import firstparty

    from . import sandbox, trust
    from .server import Runner, library_roots, load_impl

    served = members(group)
    packages = sorted({entry.split(".", 1)[0] for entry, _ in served.values()})
    try:
        level = trust.check(packages)
    except trust.Untrusted as exc:
        log("plugin_runner_untrusted", group=group, code=exc.code, detail=exc.detail)
        return EXIT_UNTRUSTED
    own = socket_path(group)
    run_dir = args.run_dir or str(own.parent)
    os.makedirs(run_dir, exist_ok=True)
    os.environ["TMPDIR"] = run_dir
    plugins = {pid for _, pid in served.values()}
    tables = [dict(p.trusted) for p in firstparty.plugins() if p.id in plugins]
    if group == "fp":
        tables = [{}]  # the fingerprint runner reads statements only: no files, no tools
    areas = json.loads(os.environ.get("MAKTABA_RUNNER_AREAS") or "{}")
    # the worker writes this machine's areas (the library roots through its share-path
    # mapping); a runner restarts itself when they change (story 54.23 §3)
    areas_digest, areas_file = "", own.parent / "areas.json"
    try:
        doc = json.loads(areas_file.read_text())
        areas.update(doc.get("areas") or {})
        areas_digest = str(doc.get("digest") or "")
    except (OSError, ValueError):
        pass
    policy = sandbox.policy_for(
        tables,
        areas=areas,
        api_socket=args.api_socket,
        own_socket=str(own),
        run_dir=run_dir,
        group=firstparty.group_of_instance(group),
    )
    try:
        if args.policy:
            sys.stdout.write(json.dumps(sandbox.policy_doc(policy)) + "\n")
            sys.stdout.flush()
            return 0
        applied = sandbox.apply(policy)
    except sandbox.SandboxUnavailable as exc:
        log("plugin_runner_sandbox_unavailable", group=group, step=exc.step, detail=str(exc))
        return EXIT_SANDBOX
    sandbox.guard_names(policy.write_named)
    sandbox.guard_scope()  # a file stage's job changes only its subject's files (54.32 §2)
    unavailable: dict[str, str] = {}
    entries: dict[str, str] = {}
    for stage, (entry, _) in served.items():
        try:
            impl = load_impl(entry)  # only now, inside the sandbox
        except ImportError as exc:
            unavailable[stage] = "runtime_missing"
            log("plugin_runner_import_failed", group=group, stage=stage, error=str(exc))
            entries[stage] = entry
            continue
        entries[stage] = entry
        reason = impl.unavailable() if getattr(impl, "unavailable", None) else None
        if reason:
            unavailable[stage] = str(reason)
    runner = Runner(
        group=group,
        api_socket=args.api_socket,
        stages=entries,
        plugin_of={s: pid for s, (_, pid) in served.items()},
        bundle=firstparty.bundle_digest(),
        unavailable=unavailable,
        areas=areas_digest,
        library_roots=library_roots(areas),
    )
    log(
        "plugin_runner_serving",
        group=group,
        stages=sorted(entries),
        trust=level,
        sandbox=applied.level,
        sandbox_missing=list(applied.missing),
        socket=str(own),
    )
    with contextlib.suppress(KeyboardInterrupt):
        asyncio.run(_serve(runner, own, areas_file, areas_digest))
    return EXIT_AREAS_CHANGED if runner.areas_changed else 0


def idle_seconds() -> float:
    """``MAKTABA_RUNNER_IDLE_SEC``: how long a runner the supervisor starts on demand waits
    for work before it exits (0 or unset: it never does)."""
    try:
        return max(0.0, float(os.environ.get("MAKTABA_RUNNER_IDLE_SEC", "0") or 0))
    except ValueError:
        return 0.0


async def _serve(runner: Any, own: Any, areas_file: Any, digest: str) -> None:
    """Serve until the areas the sandbox was built from change on disk, or, when the
    supervisor started it on demand (``MAKTABA_RUNNER_IDLE_SEC``), until it has had nothing
    to do for that long: it then exits 0 and is started again for the next job."""
    serving = asyncio.ensure_future(runner.serve(own))
    idle = idle_seconds()
    while not serving.done():
        if idle > 0 and runner.idle_for() >= idle:
            log("plugin_runner_idle", group=runner.group, idle_sec=idle)
            serving.cancel()
            break
        await asyncio.sleep(AREAS_POLL_S)
        try:
            now = str(json.loads(areas_file.read_text()).get("digest") or "")
        except (OSError, ValueError):
            continue
        if now and now != digest and not runner.running:
            log("plugin_runner_areas_changed", group=runner.group, was=digest, now=now)
            runner.areas_changed = True
            serving.cancel()
    with contextlib.suppress(asyncio.CancelledError):
        await serving


if __name__ == "__main__":
    sys.exit(main())
