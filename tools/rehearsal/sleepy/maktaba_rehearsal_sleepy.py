"""The update rehearsal's sleepy stages (docs/update-rehearsal.md, scenarios 16.x).

tools/rehearsal/venvs.sh puts this module and a ``.pth`` line that imports it into each
release's pipeline environment. It does nothing unless ``MAKTABA_REHEARSAL_SLEEPY`` names
stages (comma separated). Then each named stage's work becomes a sleep of
``MAKTABA_REHEARSAL_SLEEPY_SECONDS`` (default 8), wherever the release runs it:

- in the worker, through ``maktaba_pipeline.engine.stages.registry.get_impl`` (every
  release has it), so an in-process job sleeps;
- in a first-party runner, through ``maktaba_plugin_runner.server.load_impl`` (the
  releases with plugins), so a job of a stage whose ``job_stages.impl`` is ``plugin``
  sleeps in its runner.

Only a stage's work changes: its claims, its queue, its runner, the flip and the
update's drain are the release's own code. So the jobs are really in flight when an
update stops the release. Each run writes a step to the job's log (``job_events``)
when it starts, with ``data.rehearsal = "start"``, the side that ran it (``inproc`` or
``plugin``) and the release, and ends with that side and release in its summary. The
scenario counts the starts per job (a job whose run was cut and started again shows two)
and reads where each ran.

The input fingerprint is the subject's own (``rehearsal:<id>``): the seed's files are not
there, and a job the rehearsal enqueues runs whatever the subject's last real success was.
"""

from __future__ import annotations

import asyncio
import importlib.abc
import importlib.machinery
import os
import sys
from typing import Any

_NAMED = os.environ.get("MAKTABA_REHEARSAL_SLEEPY", "")
_STAGES = frozenset(s.strip() for s in _NAMED.split(",") if s.strip())
_REGISTRY = "maktaba_pipeline.engine.stages.registry"
_SERVER = "maktaba_plugin_runner.server"


def _seconds() -> float:
    try:
        return float(os.environ.get("MAKTABA_REHEARSAL_SLEEPY_SECONDS", "8"))
    except ValueError:
        return 8.0


def _sleepy(stage: str, impl: Any) -> Any:
    """``impl`` (a StageImpl, or None) with its work a sleep."""
    from dataclasses import replace

    from maktaba_pipeline import __version__
    from maktaba_pipeline.engine.graph import StageResult
    from maktaba_pipeline.engine.stages.registry import StageImpl

    side = "plugin" if _SERVER in sys.modules else "inproc"
    # the release the rehearsal's Python started this process for (next and after run cur's
    # environment, whose own version is cur's)
    release = os.environ.get("MAKTABA_REHEARSAL_RELEASE") or __version__

    async def run(_db: Any, job: Any, ctx: Any) -> Any:
        info = {"rehearsal": "start", "impl": side, "release": release, "pid": os.getpid()}
        await ctx.log("info", f"rehearsal: {stage} sleeps ({side}, {release})", info)
        await asyncio.sleep(_seconds())
        return StageResult(
            output_hash=f"rehearsal:{stage}:{job.id}",
            outcome="unchanged",
            summary={"rehearsal": {"impl": side, "release": release}},
        )

    async def fingerprint(_db: Any, subject: Any) -> str:
        return f"rehearsal:{subject.id}"

    fields = {
        "run": run,
        "input_fingerprint": fingerprint,
        "output_fingerprint": None,
        "unavailable": None,
        "still_current": None,
    }
    if impl is None:
        known = StageImpl.__dataclass_fields__
        return StageImpl(**{k: v for k, v in fields.items() if k in known})
    known = type(impl).__dataclass_fields__
    return replace(impl, **{k: v for k, v in fields.items() if k in known})


def _patch_registry(mod: Any) -> None:
    real = mod.get_impl

    def get_impl(name: str) -> Any:
        impl = real(name)
        return _sleepy(name, impl) if name in _STAGES else impl

    mod.get_impl = get_impl


def _patch_server(mod: Any) -> None:
    real = mod.load_impl

    def load_impl(entry: str) -> Any:
        impl = real(entry)
        from maktaba_pipeline.plugins import firstparty

        for name in _STAGES:
            if firstparty.impl_entry(name) == entry:
                return _sleepy(name, impl)
        return impl

    mod.load_impl = load_impl


_PATCHES = {_REGISTRY: _patch_registry, _SERVER: _patch_server}


class _Loader(importlib.abc.Loader):
    def __init__(self, real: Any, patch: Any) -> None:
        self.real, self.patch = real, patch

    def create_module(self, spec: Any) -> Any:
        return self.real.create_module(spec)

    def exec_module(self, module: Any) -> None:
        self.real.exec_module(module)
        self.patch(module)


class _Finder(importlib.abc.MetaPathFinder):
    """Patches the two modules as they load: the worker's ``engine.run`` imports
    ``get_impl`` by name, so the patch must be in place before that import ends."""

    def find_spec(self, name: str, path: Any, target: Any = None) -> Any:
        patch = _PATCHES.get(name)
        if patch is None:
            return None
        spec = importlib.machinery.PathFinder.find_spec(name, path)
        if spec is None or spec.loader is None:
            return None
        spec.loader = _Loader(spec.loader, patch)
        return spec


if _STAGES:
    sys.meta_path.insert(0, _Finder())
