"""Plan preview: the channel planner as a dry run, and the stored plan as a page.

The control centre's twin of mediaintel's ``/api/plan-preview``. ``start("preview")`` plans
the channels from what the stages stored with the planner settings as they are *now*
(Settings → Rules) and writes ``channel-plan-preview.html`` into the reports folder: no
``channel_plans`` row, no channel, no external write. ``start("stored")`` renders the latest
stored plan to ``channel-plan.html`` (for a plan stored before pages were written).

Planning a big catalog takes minutes, so this runs as one background task per process and the
API polls :meth:`PlanPreview.status` (through gRPC ``PlanPreviewStatus``). One run at a time.
"""

from __future__ import annotations

import asyncio
import time
from collections.abc import Mapping
from typing import Any, Literal

from ..log import get_logger
from ..rules import read as read_rules
from ..schedule.entry import plan_catalog
from .plan_page import PLAN_PAGE, PREVIEW_PAGE, write_plan_page
from .sql import loads
from .stages._plan_json import build_plan_json
from .stages.plan_channels import compute_changes, load_inputs
from .stages.review_pages import reviews_dir

__all__ = ["PlanPreview", "PreviewBusy"]

_log = get_logger()
Kind = Literal["preview", "stored"]
KINDS: tuple[Kind, ...] = ("preview", "stored")
_LOG_LINES = 30


class PreviewBusy(RuntimeError):
    """A preview is already being planned."""


class PlanPreview:
    """One background preview at a time over ``db`` (anything with ``fetch``/``fetchrow``)."""

    def __init__(self, db: Any) -> None:
        self._db = db
        self._task: asyncio.Task[None] | None = None
        self._kind: Kind | None = None
        self._phase = ""
        self._error: str | None = None
        self._started: float | None = None
        self._finished: float | None = None
        self._summary: dict[str, Any] = {}
        self._log: list[str] = []

    @property
    def running(self) -> bool:
        return self._task is not None and not self._task.done()

    def start(self, kind: str) -> dict[str, Any]:
        """Begin ``kind`` ("preview" or "stored") in the background (``PreviewBusy`` while one runs)."""
        if kind not in KINDS:
            raise ValueError(f"plan_preview: kind must be one of {', '.join(KINDS)}")
        if self.running:
            raise PreviewBusy("A preview is already being planned.")
        self._kind = kind
        self._error, self._summary, self._log = None, {}, []
        self._started, self._finished = time.time(), None
        self._task = asyncio.create_task(self._run(self._kind), name="plan-preview")
        return {"started": True, "kind": kind}

    def status(self) -> dict[str, Any]:
        folder = reviews_dir()
        return {
            "running": self.running,
            "kind": self._kind,
            "phase": self._phase if self.running else "",
            "error": self._error,
            "started_at": self._started,
            "finished_at": self._finished,
            "summary": self._summary,
            "log": self._log[-_LOG_LINES:],
            "ready": {
                "preview": (folder / PREVIEW_PAGE).is_file(),
                "plan": (folder / PLAN_PAGE).is_file(),
            },
        }

    def _note(self, phase: str) -> None:
        self._phase = phase
        self._log.append(phase)

    async def _run(self, kind: Kind) -> None:
        try:
            self._summary = await (self._preview() if kind == "preview" else self._stored())
        except Exception as exc:  # noqa: BLE001 — the task is the boundary; the page shows why
            _log.warning("plan_preview_failed", kind=kind, error=str(exc))
            self._error = str(exc) or type(exc).__name__
        finally:
            self._finished = time.time()
            self._phase = ""

    async def _latest(self) -> Mapping[str, Any] | None:
        row = await self._db.fetchrow("SELECT plan FROM channel_plans ORDER BY id DESC LIMIT 1")
        return None if row is None else loads(row["plan"])

    async def _preview(self) -> dict[str, Any]:
        self._note("reading seasons, episodes and tags")
        seasons, loose, notes = await load_inputs(self._db)
        planner = (await read_rules(self._db, "planner"))[0]
        self._note(f"choosing the lineup and grids from {len(seasons)} seasons")

        def build() -> dict[str, Any]:
            return build_plan_json(
                plan_catalog(seasons, loose, planner), source="preview", notes=notes
            )

        plan = await asyncio.to_thread(build)
        self._note("comparing with the stored plan")
        changes = compute_changes(await self._latest(), plan)
        self._note("writing the page")
        await asyncio.to_thread(
            write_plan_page,
            plan,
            PREVIEW_PAGE,
            title="Channel plan (preview)",
            banner="Preview with the current settings. Nothing was written: no channel changed.",
        )
        return {
            "channels": len(plan["channels"]),
            "lanes": len(plan["lanes"]),
            "changes": changes.counts,
            "added": [c["name"] for c in changes.added][:20],
            "removed": [c["name"] for c in changes.removed][:20],
            "changed": [c["name"] for c in changes.changed][:20],
        }

    async def _stored(self) -> dict[str, Any]:
        self._note("reading the stored plan")
        plan = await self._latest()
        if plan is None:
            raise RuntimeError("There is no stored plan yet: run the plan_channels queue first.")
        self._note("writing the page")
        await asyncio.to_thread(write_plan_page, plan)
        return {"channels": len(plan.get("channels", [])), "lanes": len(plan.get("lanes", {}))}
