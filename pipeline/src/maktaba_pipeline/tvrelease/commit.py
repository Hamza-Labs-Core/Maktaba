"""The swap commit (design 4.6 and 8.3): a staged release goes on the air in one
transaction - its lineup into ``channels``, its art and settings, the producer's
bookkeeping (links, filler pools), and ``tv_routing`` flipped to its generation.

**Interim (until stories 33.1 / 33.4 / 33.7 land).** With one TV process there is no
second slot to switch to: the commit is the whole swap. It is still a swap at each
channel's programme boundary, by construction: generation N+1 equals N up to every
changed channel's boundary ``B_c`` (:mod:`.generation`), and the commit first re-anchors
the generation at the commit instant (a boundary that already passed moves to the end
of the programme on the air now), so the live engine, which re-reads the live view as
time advances, plays the same programme to its end and the new schedule from ``B_c``.
The lineup (numbers, names, added and removed channels) changes at the commit, as a
direct write does today. The two-slot runtime keeps this commit and moves the
per-channel switch into the front door (``tv_channel_switch``).
"""

from __future__ import annotations

from datetime import datetime, timedelta
from typing import Any

from .. import rules
from ..engine.sql import dumps, is_pg, loads, now, to_ts
from ..log import get_logger
from .build import Release, filler_item_id, load_release
from .generation import reanchor
from .snapshot import DEF_COLUMNS, SETTINGS_SECTIONS, live_snapshot
from .store import get_change, transition, update_change

__all__ = ["commit_change", "readback"]

_log = get_logger()
_ART_TABLE = "channel_art_overrides"
#: the advisory lock every swap / rollback commit takes (design 8.3)
SWAP_LOCK = "tv_swap"


async def _lock(db: Any) -> None:
    if is_pg(db):
        await db.execute("SELECT pg_advisory_xact_lock(hashtext($1))", SWAP_LOCK)


async def _apply_lineup(db: Any, rel: Release, stamp: Any) -> dict[str, int]:
    """``channels`` := the release's lineup: updates and additions (staged rows become
    live), removals retired. Numbers and slugs step aside first, so a renumbering never
    trips the live-unique indexes half way."""
    ids = sorted(rel.defs)
    have = {
        str(r["id"]): str(r["lifecycle"])
        for r in await db.fetch("SELECT id, lifecycle FROM channels")
    }
    for i, cid in enumerate(ids):
        if cid in have:
            await db.execute(
                "UPDATE channels SET number = $2, slug = $3 WHERE id = $1",
                cid,
                -(i + 1),
                f"~swap-{cid}",
            )
    added = 0
    for cid in ids:
        d = rel.defs[cid]
        vals = (
            d.get("library_id"), int(d.get("number") or 0), str(d.get("name") or ""),
            str(d.get("slug") or cid), d.get("logo_path"), str(d.get("category") or "general"),
            str(d.get("mode") or "shuffle"), dumps(d.get("mode_config") or {}),
            None if d.get("source_filter") is None else dumps(d["source_filter"]),
            str(d.get("transition") or "cut"),
            bool(d.get("enabled", True)) if is_pg(db) else int(bool(d.get("enabled", True))),
            int(d.get("sort_order") or 0), stamp,
        )  # fmt: skip
        if cid in have:
            if have[cid] != "live":
                added += 1
            await db.execute(
                "UPDATE channels SET library_id = $2, number = $3, name = $4, slug = $5,"
                " logo_path = $6, category = $7, mode = $8, mode_config = $9, source_filter = $10,"
                " transition = $11, enabled = $12, sort_order = $13, updated_at = $14,"
                " lifecycle = 'live', retired_at = NULL WHERE id = $1",
                cid,
                *vals,
            )
        else:
            added += 1
            await db.execute(
                "INSERT INTO channels (id, library_id, number, name, slug, logo_path, category,"
                " mode, mode_config, source_filter, transition, enabled, sort_order, created_at,"
                " updated_at, lifecycle) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,"
                " $14,'live')",
                cid,
                *vals,
            )
    retired = 0
    for cid, life in have.items():
        if life == "live" and cid not in rel.defs:
            retired += 1
            await db.execute(
                "UPDATE channels SET lifecycle = 'retired', retired_at = $2, enabled = $3,"
                " updated_at = $2 WHERE id = $1",
                cid,
                stamp,
                False if is_pg(db) else 0,
            )
    return {"channels": len(ids), "added": added, "retired": retired}


async def _apply_art(db: Any, rel: Release, stamp: Any) -> None:
    from .snapshot import _has_table  # noqa: PLC0415

    if not await _has_table(db, _ART_TABLE):
        return
    for cid in sorted(rel.defs):
        await db.execute(f"DELETE FROM {_ART_TABLE} WHERE channel_id = $1", cid)
        for kind, o in sorted((rel.art.get(cid) or {}).items()):
            await db.execute(
                f"INSERT INTO {_ART_TABLE} (channel_id, kind, path, settings, base, updated_at)"
                " VALUES ($1,$2,$3,$4,$5,$6)",
                cid,
                kind,
                o.get("path"),
                None if o.get("settings") is None else dumps(o["settings"]),
                None if o.get("base") is None else dumps(o["base"]),
                stamp,
            )


async def _apply_settings(db: Any, rel: Release, stamp: Any) -> list[str]:
    changed: list[str] = []
    for section in SETTINGS_SECTIONS:
        want = rel.settings.get(section) or {}
        key = f"rules.{section}"
        row = await db.fetchrow("SELECT value FROM app_settings WHERE key = $1", key)
        cur = loads(row["value"]) if row is not None else {}
        if (cur or {}) == want:
            continue
        changed.append(section)
        if not want:
            await db.execute("DELETE FROM app_settings WHERE key = $1", key)
            continue
        await db.execute(
            "INSERT INTO app_settings (key, value, updated_at) VALUES ($1, $2, $3)"
            " ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value,"
            " updated_at = EXCLUDED.updated_at",
            key,
            dumps(want),
            stamp,
        )
    return changed


async def _apply_bookkeeping(db: Any, pending: dict[str, Any], stamp: Any) -> None:
    """``write_channels``' links and filler pools, the editor's edited marks."""
    from ..engine.stages.write_channels import SUBJECT_KIND, TARGET

    for key in pending.get("unlinks") or []:
        await db.execute(
            "DELETE FROM external_links"
            " WHERE target = $1 AND subject_kind = $2 AND subject_id = $3",
            TARGET,
            SUBJECT_KIND,
            key,
        )
    for link in pending.get("links") or []:
        await db.execute(
            "INSERT INTO external_links (target, subject_kind, subject_id, external_id, title,"
            " content_hash, created_by_tool, updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)"
            " ON CONFLICT (target, subject_kind, subject_id) DO UPDATE SET"
            " external_id = EXCLUDED.external_id, title = EXCLUDED.title,"
            " content_hash = EXCLUDED.content_hash, updated_at = EXCLUDED.updated_at",
            TARGET, SUBJECT_KIND, link["key"], link["channel_id"], link["title"], link["digest"],
            True if is_pg(db) else 1, stamp,
        )  # fmt: skip
    pools = pending.get("pools") or {}
    for _, gone in pools.get("delete") or []:
        await db.execute("DELETE FROM filler_items WHERE pool_id = $1", gone)
        await db.execute(
            "DELETE FROM filler_pools WHERE id = $1 AND external_key IS NOT NULL", gone
        )
    import uuid

    for pw in pools.get("write") or []:
        key = str(pw["key"])
        pid = pw.get("pool_id")
        if pid is None:
            pid = str(uuid.uuid5(uuid.NAMESPACE_URL, f"maktaba:filler_pool:{key}"))
            if await db.fetchrow("SELECT 1 AS x FROM filler_pools WHERE id = $1", pid) is None:
                await db.execute(
                    "INSERT INTO filler_pools (id, channel_id, name, external_key, created_at,"
                    " updated_at) VALUES ($1, NULL, $2, $3, $4, $4)",
                    pid,
                    pw["name"],
                    key,
                    stamp,
                )
        else:
            await db.execute(
                "UPDATE filler_pools SET name = $2, updated_at = $3 WHERE id = $1",
                pid,
                pw["name"],
                stamp,
            )
        items = {str(k): int(v) for k, v in (pw.get("items") or {}).items()}
        for vid in pw.get("drop") or []:
            await db.execute(
                "DELETE FROM filler_items WHERE pool_id = $1 AND video_id = $2", pid, vid
            )
        for vid in pw.get("add") or []:
            await db.execute(
                "INSERT INTO filler_items (id, pool_id, video_id, type, duration_ms, created_at)"
                " VALUES ($1, $2, $3, 'filler', $4, $5) ON CONFLICT (pool_id, video_id) DO NOTHING",
                filler_item_id(key, vid),
                pid,
                vid,
                items.get(vid, 0),
                stamp,
            )
        for vid in pw.get("retime") or []:
            await db.execute(
                "UPDATE filler_items SET duration_ms = $3 WHERE pool_id = $1 AND video_id = $2",
                pid,
                vid,
                items.get(vid, 0),
            )
    for cid in pending.get("edited") or []:
        await db.execute("UPDATE channels SET edited_at = $2 WHERE id = $1", cid, stamp)
    for cid in pending.get("follow") or []:
        await db.execute("UPDATE channels SET edited_at = NULL WHERE id = $1", cid)


async def readback(db: Any, rel: Release) -> dict[str, Any]:
    """The lineup read back after the commit, against the release (story 33.12 AC4)."""
    snap = await live_snapshot(db)
    bad = [
        cid
        for cid, d in rel.defs.items()
        if {k: snap["defs"].get(cid, {}).get(k) for k in DEF_COLUMNS}
        != {k: d.get(k) for k in DEF_COLUMNS}
    ]
    extra = sorted(set(snap["defs"]) - set(rel.defs))
    return {"ok": not bad and not extra, "mismatch": sorted(bad), "unexpected": extra}


async def commit_change(
    db: Any, change_id: str, *, clock: datetime | None = None, actor_source: str = "system"
) -> dict[str, Any]:
    """Put a ``swapping`` change on the air. Returns a report (also stored on the
    change); a failure leaves nothing switched (one transaction) and the change back
    in ``ready`` (design 12: "swap commit fails")."""
    clock = clock or now()
    ch = await get_change(db, change_id)
    if ch is None or ch.state != "swapping" or not ch.row.get("release_id"):
        raise LookupError(f"change {change_id} is not swapping")
    rel = await load_release(db, str(ch.row["release_id"]))
    if rel is None:
        raise LookupError(f"release of change {change_id} is gone")
    cfg, _, _ = await rules.read(db, "tv_staging")
    pending = ch.pending
    try:
        moved = await reanchor(db, rel, at=clock, pending=pending, clock=clock)
        stamp = to_ts(db, clock)
        async with db.transaction():
            await _lock(db)
            route = await db.fetchrow("SELECT * FROM tv_routing WHERE scope = 'cluster'")
            old_rel = None if route is None else route["live_release_id"]
            old_gen = 0 if route is None else int(route["live_gen"] or 0)
            epoch = 0 if route is None else int(route["epoch"] or 0)
            lineup = await _apply_lineup(db, rel, stamp)
            await _apply_art(db, rel, stamp)
            settings = await _apply_settings(db, rel, stamp)
            await _apply_bookkeeping(db, pending, stamp)
            until = to_ts(db, clock + timedelta(minutes=int(cfg.keep_warm_minutes)))
            if route is None:
                await db.execute(
                    "INSERT INTO tv_routing (scope, live_release_id, live_gen, epoch, switched_at)"
                    " VALUES ('cluster', $1, $2, 1, $3)",
                    rel.id,
                    rel.sched_gen,
                    stamp,
                )
            else:
                await db.execute(
                    "UPDATE tv_routing SET live_release_id = $1, live_gen = $2,"
                    " previous_release_id = $3, previous_gen = $4, epoch = $5, switched_at = $6,"
                    " previous_until = $7 WHERE scope = 'cluster'",
                    rel.id, rel.sched_gen, old_rel, old_gen, epoch + 1, stamp, until,
                )  # fmt: skip
            prev = await db.fetch(
                "SELECT id FROM tv_changes WHERE state = 'live' AND id <> $1", change_id
            )
            for p in prev:
                await transition(
                    db, str(p["id"]), from_states=("live",), to="superseded", action="supersede",
                    detail={"by": change_id}, superseded_by=change_id, ended_at=clock,
                    end_reason="superseded",
                )  # fmt: skip
            report = {
                "lineup": lineup,
                "settings": settings,
                "reanchored": moved,
                "epoch": epoch + 1,
                "from_gen": old_gen,
                "to_gen": rel.sched_gen,
                "interim": True,
            }
            await transition(
                db, change_id, from_states=("swapping",), to="live", action="swap",
                actor_source=actor_source, detail=report, swapped_at=clock,
            )  # fmt: skip
            if is_pg(db):
                await db.execute("SELECT pg_notify('tv_routing', $1)", str(epoch + 1))
    except Exception as exc:
        _log.exception("tv_swap_failed", change_id=change_id)
        try:
            await transition(
                db, change_id, from_states=("swapping",), to="ready", action="swap_failed",
                detail={"error": str(exc)[:500]},
            )  # fmt: skip
        except Exception:  # noqa: BLE001 - keep the original error
            _log.exception("tv_swap_failed_revert", change_id=change_id)
        raise
    report["readback"] = await readback(db, rel)
    report["plex_livetv"] = await _enqueue_plex(db, change_id)
    vr = ch.row.get("verify_report") or {}
    await update_change(db, change_id, verify_report={**vr, "swap": report})
    _log.info("tv_swap_committed", change_id=change_id, gen=rel.sched_gen, epoch=report["epoch"])
    return report


async def _enqueue_plex(db: Any, change_id: str) -> str:
    """Plex's channel map and guide follow the new lineup (``plex_livetv``, forced:
    reason ``tv_swap``) - the only path to Plex with staging on (story 33.12 EC3)."""
    from ..engine.enqueue import enqueue_job
    from ..engine.graph import CATALOG_ID

    try:
        res = await enqueue_job(
            db,
            subject_kind="catalog",
            subject_id=CATALOG_ID,
            stage="plex_livetv",
            force=True,
            reason={"why": "tv_swap", "by": "system", "change_id": change_id},
        )
    except Exception as exc:  # noqa: BLE001 - Plex must never undo a swap
        _log.warning("tv_swap_plex_enqueue_failed", error=str(exc))
        return "failed"
    return str(getattr(res, "outcome", res))
