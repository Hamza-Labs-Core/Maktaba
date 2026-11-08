"""``plan_channels``: suggest live TV channels and their time-slot grids (plan 06-11 section 3.2).

Subject: the single global ``catalog``. Reads what ``detect`` / ``tag`` / ``rate_style`` stored
(seasons, episodes, tags, production style, loose videos, clip groups, promo links) and
runs the planner through its one public entry point,
:func:`maktaba_pipeline.schedule.entry.plan_catalog` (the hook a new planner replaces;
plan 06-11 section 17.6), ported from the prototype's ``plan_channels.load_inputs``.
The result is one ``channel_plans`` row (the latest wins;
20 are kept).

* ``compute_changes`` diffs the new lineup against the previous plan (channels added, removed,
  changed) - exactly what a viewer of the history wants to see;
* ``apply_changes`` stores the plan;
* an unchanged lineup writes nothing and propagates nothing.

Extensions can add per-channel data to the plan through
:func:`register_plan_exporter`; the core knows nothing about what they export.
"""

from __future__ import annotations

import asyncio
from collections.abc import Mapping
from typing import Any

from maktaba_pipeline.db.jobs import Job
from maktaba_pipeline.engine.changes import ChangeSet, apply_or_review
from maktaba_pipeline.engine.context import StageContext
from maktaba_pipeline.engine.graph import StageResult, Subject
from maktaba_pipeline.engine.hashing import canonical_hash
from maktaba_pipeline.engine.sql import dumps, loads, now, to_ts
from maktaba_pipeline.engine.stages._plan_json import build_plan_json, config_json, strip_volatile
from maktaba_pipeline.engine.stages.registry import StageImpl
from maktaba_pipeline.log import get_logger
from maktaba_pipeline.rules import read as read_rules
from maktaba_pipeline.schedule.channel_planner import PLANNER_VERSION
from maktaba_pipeline.schedule.entry import plan_catalog
from maktaba_pipeline.schedule.plan_inputs import compute_changes, load_inputs, plan_exporters

__all__ = ["IMPL"]

_log = get_logger()
SUCCESS = "('changed','unchanged','skipped_unchanged','imported')"


# ------------------------------------------------------------------------------ stage


async def input_fingerprint(db: Any, subject: Subject) -> str | None:
    """Hash of every latest ``detect`` / ``tag`` / ``rate_style`` output plus the planner config."""
    rows = await db.fetch(
        "SELECT j.stage AS stage, j.subject_id AS sid, j.output_hash AS h FROM processing_jobs j"
        " WHERE j.stage IN ('detect','tag','check_tags','check_clips','books','rate_style',"
        "'topic_collections','promo_links','clip_groups') AND j.state = 'done'"
        f" AND j.outcome IN {SUCCESS}"
        " AND j.id = (SELECT max(k.id) FROM processing_jobs k WHERE k.subject_kind = j.subject_kind"
        "  AND k.subject_id = j.subject_id AND k.stage = j.stage AND k.state = 'done'"
        f"  AND k.outcome IN {SUCCESS})"
        " ORDER BY j.stage, j.subject_id"
    )
    return canonical_hash(
        {
            "outputs": [[r["stage"], str(r["sid"]), r["h"]] for r in rows],
            # the rules as set: a changed planner setting plans the channels again
            "config": canonical_hash(config_json((await read_rules(db, "planner"))[0])),
            "exporters": sorted(plan_exporters()),
            "v": PLANNER_VERSION,
        }
    )


async def run(db: Any, job: Job, ctx: StageContext) -> StageResult:
    seasons, loose, notes = await load_inputs(db)
    exporters = plan_exporters()
    plan_input = await input_fingerprint(db, job.subject) or ""

    planner = await ctx.rules("planner")

    def build() -> dict[str, Any]:
        result = plan_catalog(seasons, loose, planner)
        return build_plan_json(result, source="catalog", notes=notes, exporters=exporters)

    plan = await asyncio.to_thread(build)
    plan_hash = canonical_hash(strip_volatile(plan))
    latest = await db.fetchrow(
        "SELECT plan, input_hash FROM channel_plans ORDER BY id DESC LIMIT 1"
    )
    old = None if latest is None else loads(latest["plan"])
    changes = compute_changes(old, plan)
    unchanged = old is not None and canonical_hash(strip_volatile(old)) == plan_hash

    async def apply(_cs: ChangeSet) -> None:
        await _store(db, plan, plan_input)

    if unchanged:
        return StageResult(
            output_hash=plan_hash,
            summary={"channels": len(plan["channels"]), "changes": changes.counts},
        )
    if changes.is_empty:
        await _store(db, plan, plan_input)  # same lineup, different detail: keep the newest plan
    else:
        mode = await apply_or_review(ctx, changes, apply)
        if mode == "review":
            return StageResult(output_hash=canonical_hash("review"), outcome="awaiting_review")
    return StageResult(
        output_hash=plan_hash,
        summary={
            "channels": len(plan["channels"]),
            "lanes": len(plan["lanes"]),
            "changes": changes.counts,
        },
    )


async def _write_page(plan: Mapping[str, Any]) -> None:
    """The stored plan as a page in the reports folder (Reports → Channel plan). A folder
    that cannot be written is logged, never a failed plan: the page is only a view."""
    from maktaba_pipeline.engine.plan_page import write_plan_page

    try:
        await asyncio.to_thread(write_plan_page, plan)
    except OSError as exc:
        _log.warning("plan_page_not_written", error=str(exc))


async def _store(db: Any, plan: Mapping[str, Any], input_hash: str) -> None:
    await _insert(db, plan, input_hash)
    await _write_page(plan)


async def _insert(db: Any, plan: Mapping[str, Any], input_hash: str) -> None:
    await db.execute(
        "INSERT INTO channel_plans (created_at, planner_version, input_hash, plan)"
        " VALUES ($1,$2,$3,$4)",
        to_ts(db, now()),
        PLANNER_VERSION,
        input_hash,
        dumps(plan),
    )


IMPL = StageImpl(run=run, input_fingerprint=input_fingerprint)
