# ruff: noqa: E501  (long SQL string literals read better unwrapped)
"""``rate_style``: production style of a season - talk / produced / cinematic / static.

Ported from the prototype's ``scan_visual.run``. The pure classifier is
:func:`maktaba_pipeline.classify.production_style.classify_style`; per-file measurements come
from the ``style_sample`` stage (``video_signals.style_metrics``), so thresholds can change here
without re-reading any video.

Flow: pick ``SAMPLE`` episodes spread over the season (not the first or last: pilots, finales and
compilations are the least typical); ask ``style_sample`` for the ones not measured yet and finish
``waiting_inputs`` - each finished sample re-triggers this stage through the
``style_sample -> rate_style`` edge. A borderline result (low confidence, or "talk" with a dark
look) is re-rated with ``RESAMPLE`` episodes. A user override (``season_style.override_style``)
wins and needs no sampling.
"""

from __future__ import annotations

from typing import Any
from uuid import UUID

from ...classify.production_style import (
    STYLE_VERSION,
    TIERS,
    StyleMetrics,
    classify_style,
    metrics_from_dict,
)
from ...db.jobs import Job
from ..changes import ChangeSet, apply_or_review
from ..context import StageContext
from ..graph import Request, StageResult, Subject
from ..hashing import canonical_hash
from ..sql import dumps, loads, now, to_ts, uid
from ._common import load_season, season_episode_ids
from .registry import StageImpl

__all__ = ["IMPL", "RESAMPLE", "SAMPLE", "pick_samples"]

SAMPLE = 3
RESAMPLE = 8
SUCCESS = "('changed','unchanged','skipped_unchanged','imported')"


def pick_samples(items: list[UUID], n: int) -> list[UUID]:
    """``n`` items from the centres of ``n`` equal slices (avoids the first and last episode)."""
    if len(items) <= n:
        return list(items)
    return [items[int((i + 0.5) * len(items) / n)] for i in range(max(1, n))]


async def _override(db: Any, season_id: UUID) -> str | None:
    r = await db.fetchrow(
        "SELECT override_style FROM season_style WHERE season_id = $1", uid(season_id)
    )
    return None if r is None or r["override_style"] is None else str(r["override_style"])


async def input_fingerprint(db: Any, subject: Subject) -> str | None:
    s = await load_season(db, subject.id)
    if s is None:
        return canonical_hash("gone")
    picked = pick_samples(await season_episode_ids(db, subject.id), SAMPLE)
    outs: list[list[Any]] = []
    for v in picked:
        r = await db.fetchrow(
            "SELECT output_hash FROM processing_jobs WHERE subject_kind = 'video' AND subject_id = $1"
            f" AND stage = 'style_sample' AND state = 'done' AND outcome IN {SUCCESS}"
            " ORDER BY id DESC LIMIT 1",
            uid(v),
        )
        outs.append([str(v), None if r is None else r["output_hash"]])
    return canonical_hash(
        {
            "membership": s.membership_hash,
            "samples": outs,
            "override": await _override(db, subject.id),
            "v": STYLE_VERSION,
        }
    )


async def _metrics(db: Any, ids: list[UUID]) -> dict[UUID, StyleMetrics]:
    out: dict[UUID, StyleMetrics] = {}
    for v in ids:
        r = await db.fetchrow(
            "SELECT style_metrics, style_version FROM video_signals WHERE video_id = $1", uid(v)
        )
        if r is not None and r["style_metrics"] and r["style_version"] == STYLE_VERSION:
            out[v] = metrics_from_dict(loads(r["style_metrics"]))
    return out


async def _gave_up(db: Any, video_id: UUID) -> bool:
    """The latest ``style_sample`` job of this video failed for good."""
    r = await db.fetchrow(
        "SELECT state, attempts, max_attempts FROM processing_jobs WHERE subject_kind = 'video'"
        " AND subject_id = $1 AND stage = 'style_sample' ORDER BY id DESC LIMIT 1",
        uid(video_id),
    )
    return r is not None and r["state"] == "failed"


async def _need(db: Any, ids: list[UUID], have: dict[UUID, StyleMetrics]) -> list[UUID]:
    """Videos still to be sampled (not measured and not given up on)."""
    return [v for v in ids if v not in have and not await _gave_up(db, v)]


def _requests(season_id: UUID, missing: list[UUID]) -> tuple[Request, ...]:
    return tuple(
        Request(
            "style_sample",
            "video",
            v,
            "sample for a season's production style",
            payload={"seasons": [str(season_id)]},
        )
        for v in missing
    )


async def run(db: Any, job: Job, ctx: StageContext) -> StageResult:
    season_id = job.subject.id
    s = await load_season(db, season_id)
    if s is None or s.retired:
        return StageResult(output_hash=canonical_hash("retired"), summary={"rated": False})
    all_ids = await season_episode_ids(db, season_id)
    override = await _override(db, season_id)
    if override is not None:
        style, tier, conf, sampled = override, TIERS[override], 1.0, 0
        reasons: dict[str, Any] = {"reasons": ["set by user"], "scores": {}}
    else:
        picked = pick_samples(all_ids, SAMPLE)
        have = await _metrics(db, picked)
        need = await _need(db, picked, have)
        if need:
            return StageResult(
                output_hash=canonical_hash({"waiting": True}),
                outcome="waiting_inputs",
                requests=_requests(season_id, need),
                summary={"waiting_for": len(need)},
            )
        if not have:
            return StageResult(output_hash=canonical_hash("no-samples"), summary={"rated": False})
        res = classify_style([have[v] for v in picked if v in have])
        used = [v for v in picked if v in have]
        borderline = res.confidence < 0.5 or (
            res.style == "talk" and res.scores.get("lowkey", 0) >= 0.35
        )
        if borderline and len(all_ids) > len(picked):
            more = pick_samples(all_ids, RESAMPLE)
            have2 = await _metrics(db, more)
            need2 = await _need(db, more, have2)
            if need2:
                return StageResult(
                    output_hash=canonical_hash({"waiting": True}),
                    outcome="waiting_inputs",
                    requests=_requests(season_id, need2),
                    summary={"waiting_for": len(need2), "borderline": True},
                )
            if have2:
                res = classify_style([have2[v] for v in more if v in have2])
                used = [v for v in more if v in have2]
        style, tier, conf, sampled = res.style, res.tier, res.confidence, len(used)
        reasons = {"reasons": res.reasons, "scores": res.scores}

    old = await db.fetchrow(
        "SELECT style, tier, confidence FROM season_style WHERE season_id = $1", uid(season_id)
    )
    new_state = {"style": style, "tier": tier}
    old_state = (
        None
        if old is None or old["style"] is None
        else {"style": old["style"], "tier": int(old["tier"])}
    )
    changes = ChangeSet(
        "rate_style",
        added=(new_state,) if old_state is None else (),
        changed=()
        if old_state is None or old_state == new_state
        else ({"from": old_state, "to": new_state},),
        meta={"season_id": str(season_id), "confidence": conf, "sampled": sampled},
    )

    async def apply(_cs: ChangeSet) -> None:
        await _write(db, season_id, style, tier, conf, reasons, sampled)

    if changes.is_empty:
        await _write(db, season_id, style, tier, conf, reasons, sampled)  # refresh confidence only
    else:
        mode = await apply_or_review(ctx, changes, apply)
        if mode == "review":
            return StageResult(output_hash=canonical_hash("review"), outcome="awaiting_review")
    return StageResult(
        output_hash=canonical_hash(new_state),
        summary={"style": style, "tier": tier, "confidence": conf, "sampled": sampled},
    )


async def _write(
    db: Any,
    season_id: UUID,
    style: str,
    tier: int,
    conf: float,
    reasons: dict[str, Any],
    sampled: int,
) -> None:
    await db.execute(
        "INSERT INTO season_style (season_id, style, tier, confidence, reasons, sampled,"
        " style_version, updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)"
        " ON CONFLICT (season_id) DO UPDATE SET style = EXCLUDED.style, tier = EXCLUDED.tier,"
        " confidence = EXCLUDED.confidence, reasons = EXCLUDED.reasons,"
        " sampled = EXCLUDED.sampled, style_version = EXCLUDED.style_version,"
        " updated_at = EXCLUDED.updated_at",
        uid(season_id),
        style,
        tier,
        conf,
        dumps(reasons),
        sampled,
        STYLE_VERSION,
        to_ts(db, now()),
    )


IMPL = StageImpl(run=run, input_fingerprint=input_fingerprint)
