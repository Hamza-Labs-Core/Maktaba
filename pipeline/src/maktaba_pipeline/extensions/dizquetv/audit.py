"""The ``dizquetv_audit`` stage: is what each dizqueTV channel airs about the channel?

Ported from ``scripts/mediaintel/audit_channels.py``; the judging is the shared
:mod:`maktaba_pipeline.schedule.channel_audit` (the same code the stand-alone tool runs).

Subject: the catalog; it follows ``dizquetv_channels``. Read-only towards dizqueTV: it reads
every channel with its programmes and the filler lists they use, traces each item back to its
Plex item (``external_links``), video, season and tags, and judges it against the channel's
theme with the planner's rules (``rules.planner``: the themes and ``topic_min_score``).

The result is kept twice: a row per channel in ``channel_audit_channels`` (under one
``channel_audits`` row; the latest ``keep`` audits are kept), and the same report as
``channel-audit.html`` / ``channel-audit.txt`` in the media cache beside the review pages
(``~/.maktaba/cache/reviews``), for the control centre to serve. A theme channel's dawn Quran
hour is counted as designed (``theme.kind != "quran"``).
"""

from __future__ import annotations

import asyncio
import os
import posixpath
from collections import defaultdict
from collections.abc import Callable, Mapping, Sequence
from pathlib import Path
from typing import Any

from ...engine.context import StageContext, StageFailure
from ...engine.graph import StageResult, Subject
from ...engine.hashing import canonical_hash
from ...engine.sql import dumps, loads, now, to_ts
from ...engine.stages import review_pages
from ...integrations.dizquetv import DizqueError, DizqueTV
from ...integrations.plex import TransportError
from ...schedule.channel_audit import (
    AuditLookup,
    audit,
    html_report,
    summarize,
    text_report,
)
from ...schedule.channel_planner import PlannerConfig
from ...schedule.tv_settings import ChannelAuditConfig

__all__ = [
    "AuditService",
    "build_lookup",
    "fingerprint",
    "read_dizquetv",
    "report_dir",
    "run_audit",
]

_CHUNK = 400
REPORT_NAME = "channel-audit"


def report_dir() -> Path:
    """Where the report goes: beside the review pages."""
    return review_pages.reviews_dir()


class AuditService:
    def __init__(self, dz_factory: Callable[[Mapping[str, str]], DizqueTV]) -> None:
        self.dz = dz_factory


# ------------------------------------------------------------------ reading dizqueTV


def read_dizquetv(dz: DizqueTV) -> tuple[list[dict[str, Any]], dict[str, dict[str, Any]]]:
    """Every channel (with its programmes) and the filler lists they use (GET only)."""
    nums = [int(n["number"] if isinstance(n, dict) else n) for n in dz.get("/api/channels")]
    channels = [dz.get(f"/api/channel/{n}") for n in sorted(nums)]
    ids = {str(fc.get("id")) for ch in channels for fc in ch.get("fillerCollections", [])}
    return channels, {i: dz.get(f"/api/filler/{i}") for i in sorted(ids)}


def _rating_keys(
    channels: Sequence[Mapping[str, Any]], fillers: Mapping[str, Mapping[str, Any]]
) -> list[str]:
    keys: set[str] = set()
    for ch in channels:
        keys.update(
            str(p.get("ratingKey")) for p in ch.get("programs", []) if not p.get("isOffline")
        )
    for f in fillers.values():
        keys.update(str(p.get("ratingKey")) for p in f.get("content", []))
    keys.discard("None")
    return sorted(keys)


# ------------------------------------------------------------------ tracing items


def _marks(start: int, n: int) -> str:
    return ",".join(f"${i + start}" for i in range(n))


async def build_lookup(
    db: Any, channels: Sequence[Mapping[str, Any]], fillers: Mapping[str, Mapping[str, Any]]
) -> AuditLookup:
    """Plex rating key -> video -> season -> tags, for the items the channels air."""
    item_of: dict[str, str] = {}
    keys = _rating_keys(channels, fillers)
    for i in range(0, len(keys), _CHUNK):
        chunk = keys[i : i + _CHUNK]
        for r in await db.fetch(
            "SELECT subject_id, external_id FROM external_links WHERE target = 'plex_item'"
            f" AND subject_kind = 'video' AND external_id IN ({_marks(1, len(chunk))})",
            *chunk,
        ):
            item_of[str(r["external_id"])] = str(r["subject_id"])
    vids = sorted(set(item_of.values()))
    libs = {str(r["id"]): str(r["name"]) for r in await db.fetch("SELECT id, name FROM libraries")}
    series_of: dict[str, str] = {}
    library_of: dict[str, str] = {}
    folder_of: dict[str, str] = {}
    stags: dict[str, dict[str, tuple[float, str]]] = defaultdict(dict)
    itags: dict[str, dict[str, float]] = defaultdict(dict)
    for i in range(0, len(vids), _CHUNK):
        chunk = vids[i : i + _CHUNK]
        marks = _marks(1, len(chunk))
        for r in await db.fetch(
            "SELECT e.video_id AS vid, ss.id AS sid, ss.library_id AS lib FROM series_episodes e"
            " JOIN series_seasons ss ON ss.series_id = e.series_id"
            "  AND ss.season = COALESCE(e.season_override, e.season)"
            f" WHERE ss.retired_at IS NULL AND e.video_id IN ({marks})",
            *chunk,
        ):
            sid = str(r["sid"])
            series_of[str(r["vid"])] = sid
            if r["lib"] is not None:
                library_of[sid] = libs.get(str(r["lib"]), "")
        for r in await db.fetch(f"SELECT id, path FROM videos WHERE id IN ({marks})", *chunk):
            folder_of[str(r["id"])] = posixpath.basename(
                posixpath.dirname(str(r["path"]).replace("\\", "/"))
            )
        for r in await db.fetch(
            f"SELECT video_id, tag, score FROM video_topic_tags WHERE video_id IN ({marks})",
            *chunk,
        ):
            itags[str(r["video_id"])][str(r["tag"])] = float(r["score"])
    seasons = sorted(set(series_of.values()))
    for i in range(0, len(seasons), _CHUNK):
        chunk = seasons[i : i + _CHUNK]
        for r in await db.fetch(
            "SELECT season_id, tag, score, evidence FROM season_tags"
            f" WHERE season_id IN ({_marks(1, len(chunk))})",
            *chunk,
        ):
            why = (loads(r["evidence"]) or {}).get("why", "") if r["evidence"] else ""
            stags[str(r["season_id"])][str(r["tag"])] = (float(r["score"]), str(why))
    return AuditLookup(item_of, series_of, library_of, folder_of, stags, itags)


# ------------------------------------------------------------------ the stage


async def _last_channels_job(db: Any) -> int:
    row = await db.fetchrow(
        "SELECT max(id) AS id FROM processing_jobs"
        " WHERE stage = 'dizquetv_channels' AND state = 'done'"
    )
    return int(row["id"]) if row is not None and row["id"] is not None else 0


async def fingerprint(db: Any, subject: Subject) -> str | None:
    """The switch, the planner rules the judging uses and the last channel write."""
    from ... import rules

    _, audit_changes, _ = await rules.read(db, "audit")
    _, planner_changes, _ = await rules.read(db, "planner")
    return canonical_hash(
        {
            "audit": audit_changes,
            "planner": planner_changes,
            "channels_job": await _last_channels_job(db),
        }
    )


def _wrap(exc: Exception) -> StageFailure:
    down = isinstance(exc, TransportError) or isinstance(exc.__cause__, TransportError)
    kind = "dizquetv_unreachable" if down else "dizquetv_error"
    return StageFailure(kind, str(exc), retryable=True)


def _write_report(name: str, text: str) -> Path:
    out = report_dir() / name
    out.parent.mkdir(parents=True, exist_ok=True)
    tmp = out.with_name(out.name + ".part")
    tmp.write_text(text, encoding="utf-8")
    os.replace(tmp, out)
    return out


async def _store(
    db: Any, cfg: ChannelAuditConfig, plan_hash: str | None, page: Path, rows: list[dict[str, Any]]
) -> int:
    async with db.transaction():
        audit_id = await db.fetchval(
            "INSERT INTO channel_audits (created_at, plan_hash, window_days, channels, html_path)"
            " VALUES ($1,$2,$3,$4,$5) RETURNING id",
            to_ts(db, now()),
            plan_hash,
            cfg.window_days,
            len(rows),
            str(page),
        )
        for r in rows:
            await db.execute(
                "INSERT INTO channel_audit_channels (audit_id, number, name, theme, hours,"
                " shares, filler_clips, filler_share, detail) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)",
                audit_id,
                r["number"],
                r["name"],
                r["theme"],
                r["hours"],
                dumps(r["share"]),
                r["filler_clips"],
                None if r["filler_share"] is None else dumps(r["filler_share"]),
                dumps({"shows": r["shows"], "filler_unrelated": r["filler_unrelated"]}),
            )
        # the latest ``keep`` audits stay
        await db.execute(
            "DELETE FROM channel_audits WHERE id NOT IN"
            " (SELECT id FROM channel_audits ORDER BY id DESC LIMIT $1)",
            cfg.keep,
        )
    return int(audit_id)


async def run_audit(db: Any, job: Any, ctx: StageContext, service: AuditService) -> StageResult:
    cfg: ChannelAuditConfig = await ctx.rules("audit")
    if not cfg.enabled:
        return StageResult(
            output_hash=canonical_hash("dizquetv_audit:off"),
            outcome="unchanged",
            summary={"enabled": False},
        )
    planner: PlannerConfig = await ctx.rules("planner")
    dz = service.dz(ctx.extension_settings("dizquetv"))
    await ctx.progress("reading dizqueTV", 0, 3, force=True)
    try:
        channels, fillers = await asyncio.to_thread(read_dizquetv, dz)
    except (DizqueError, TransportError) as exc:
        raise _wrap(exc) from exc
    await ctx.progress("tracing items", 1, 3, f"{len(channels)} channels", force=True)
    lookup = await build_lookup(db, channels, fillers)
    await ctx.progress("judging", 2, 3, force=True)
    audits = await asyncio.to_thread(
        audit, channels, fillers, lookup, planner, None, window_days=cfg.window_days
    )
    text = text_report(audits, cfg.window_days)
    page = _write_report(f"{REPORT_NAME}.html", html_report(audits, cfg.window_days))
    _write_report(f"{REPORT_NAME}.txt", text)
    rows = [summarize(a) for a in sorted(audits, key=lambda a: a.number)]
    plan = await db.fetchrow("SELECT input_hash FROM channel_plans ORDER BY id DESC LIMIT 1")
    audit_id = await _store(db, cfg, None if plan is None else str(plan["input_hash"]), page, rows)
    unrelated = sum(1 for r in rows if r["share"].get("none", 0) >= 50)
    return StageResult(
        output_hash=canonical_hash(rows),
        summary={
            "enabled": True,
            "audit_id": audit_id,
            "channels": len(rows),
            "mostly_unrelated": unrelated,
            "page": str(page),
            "channel_rows": [
                {"number": r["number"], "hours": r["hours"], "share": r["share"]} for r in rows
            ],
        },
    )
