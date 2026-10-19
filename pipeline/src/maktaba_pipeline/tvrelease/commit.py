"""The swap commit (design 4.6 and 8.3): a staged release goes on the air in one
transaction - its lineup into ``channels``, its art and settings, the producer's
bookkeeping (links, filler pools), and ``tv_routing`` flipped to its generation.

**With TV slots (stories 33.1 / 33.7, the default on a Postgres home)** the commit is the
database half of the swap: in the same transaction it writes the front door's swap
operation - ``tv_swap_ops`` (kind ``swap``) and one ``tv_channel_switch`` row per channel
of both lineups with its planned instant (swap_wait ``boundary``: the end of the
programme on the air in generation N at the commit, capped at
``boundary_max_wait_minutes``; ``immediate``, an added channel: the commit instant; a
removed channel: its boundary, flagged ``removed``). The change stays ``swapping``; the
front door switches every channel make-before-break at its instant (HLS stitch, TS
splice, the push relay's source switch) and the controller moves the change to ``live``
when the operation is done (api ``tvchange/swap.go``). The guide and the lineup change at
the commit; for a channel that switches later the stream agrees with the guide because
N+1 equals N before ``B_c``.

**Without slots** (one TV process: SQLite, ``[tv] slots = off``, or no live slot beating)
the commit is the whole swap, at each channel's programme boundary by construction: the
commit first re-anchors the generation at the commit instant, so the engine, which
re-reads the live view as time advances, plays the programme on the air to its end and
the new schedule from ``B_c``; the change is ``live`` at once.
"""

from __future__ import annotations

from datetime import datetime, timedelta
from typing import Any

from .. import rules
from ..engine.sql import dumps, is_pg, loads, now, to_ts
from ..log import get_logger
from .build import Release, filler_item_id, load_release
from .generation import boundary, reanchor
from .snapshot import DEF_COLUMNS, SETTINGS_SECTIONS, live_snapshot
from .store import event, get_change, transition, update_change

__all__ = ["commit_change", "readback", "slots_runtime", "write_swap_op"]

_log = get_logger()
_ART_TABLE = "channel_art_overrides"
#: the advisory lock every swap / rollback commit takes (design 8.3)
SWAP_LOCK = "tv_swap"


async def _lock(db: Any) -> None:
    if is_pg(db):
        await db.execute("SELECT pg_advisory_xact_lock(hashtext($1))", SWAP_LOCK)


async def _apply_lineup(
    db: Any, rel: Release, stamp: Any, *, removed_lifecycle: str = "retired"
) -> dict[str, int]:
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
    # removals first: a new channel may take a removed one's number
    retired = 0
    for cid, life in have.items():
        if life == "live" and cid not in rel.defs:
            retired += 1
            if removed_lifecycle == "staged":
                # a rollback: a channel the rolled-back release added is staged again
                # (its definition stays in that release for a re-stage)
                await db.execute(
                    "UPDATE channels SET lifecycle = 'staged', retired_at = NULL, enabled = $3,"
                    " updated_at = $2 WHERE id = $1",
                    cid,
                    stamp,
                    False if is_pg(db) else 0,
                )
                continue
            await db.execute(
                "UPDATE channels SET lifecycle = 'retired', retired_at = $2, enabled = $3,"
                " updated_at = $2 WHERE id = $1",
                cid,
                stamp,
                False if is_pg(db) else 0,
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


async def slots_runtime(db: Any) -> bool:
    """Whether a front door with TV slots runs the swap (a live slot beat lately)."""
    if not is_pg(db):
        return False
    try:
        return bool(
            await db.fetchval(
                "SELECT EXISTS (SELECT 1 FROM tv_slots WHERE role = 'live'"
                " AND beat_at > now() - interval '30 seconds')"
            )
        )
    except Exception as exc:  # noqa: BLE001 - a database before slot 0129
        if "does not exist" in str(exc):
            return False
        raise


async def write_swap_op(
    db: Any,
    *,
    change_id: str | None,
    kind: str,
    release_id: str | None,
    from_release_id: str | None,
    epoch: int,
    plan: dict[str, tuple[datetime, bool]],
    reason: str = "",
    committed: bool,
    created_by: str,
) -> int:
    """Write a front-door operation and its channel rows (``channel id -> (switch_at,
    removed)``). Returns its id."""
    op = await db.fetchval(
        "INSERT INTO tv_swap_ops (change_id, kind, release_id, from_release_id, epoch, reason,"
        " committed, created_by) VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id",
        change_id, kind, release_id, from_release_id, epoch, reason, committed, created_by,
    )  # fmt: skip
    for cid, (at, removed) in sorted(plan.items()):
        await db.execute(
            "INSERT INTO tv_channel_switch (op_id, change_id, channel_id, switch_at, removed)"
            " VALUES ($1,$2,$3,$4,$5)",
            op, change_id, cid, to_ts(db, at), removed,
        )  # fmt: skip
    return int(op)


async def _switch_plan(
    db: Any,
    rel: Release,
    old_live: set[str],
    old_gen: int,
    clock: datetime,
    wait: str,
    max_wait: timedelta,
) -> dict[str, tuple[datetime, bool]]:
    """Each channel's planned switch instant (design 8.2): the end of the programme on
    the air in generation N at the commit (capped), at once for an added channel or
    with ``swap_wait = immediate``; removed channels at their boundary, flagged."""
    plan: dict[str, tuple[datetime, bool]] = {}
    for cid in sorted(set(rel.defs) | old_live):
        removed = cid not in rel.defs
        at = clock
        if cid in old_live and wait != "immediate":
            b = await boundary(db, cid, old_gen, clock)
            at = min(b, clock + max_wait) if max_wait > timedelta(0) else b
        plan[cid] = (at, removed)
    return plan


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
    front_door = await slots_runtime(db)
    wait = str(ch.row.get("swap_wait") or cfg.swap_wait or "boundary")
    max_wait = timedelta(minutes=int(cfg.boundary_max_wait_minutes))
    try:
        moved = await reanchor(db, rel, at=clock, pending=pending, clock=clock)
        stamp = to_ts(db, clock)
        async with db.transaction():
            await _lock(db)
            route = await db.fetchrow("SELECT * FROM tv_routing WHERE scope = 'cluster'")
            old_rel = None if route is None else route["live_release_id"]
            old_gen = 0 if route is None else int(route["live_gen"] or 0)
            epoch = 0 if route is None else int(route["epoch"] or 0)
            old_live = {
                str(r["id"])
                for r in await db.fetch("SELECT id FROM channels WHERE lifecycle = 'live'")
            }
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
                "interim": not front_door,
            }
            if front_door:
                # story 33.7: the front door switches each channel at its instant;
                # the controller makes the change live when the operation is done
                plan = await _switch_plan(db, rel, old_live, old_gen, clock, wait, max_wait)
                op = await write_swap_op(
                    db, change_id=change_id, kind="swap", release_id=rel.id,
                    from_release_id=None if old_rel is None else str(old_rel), epoch=epoch + 1,
                    plan=plan, committed=True, created_by=actor_source,
                )  # fmt: skip
                last = max((at for at, _ in plan.values()), default=clock)
                report.update(
                    {"front_door": True, "op_id": op, "swap_wait": wait,
                     "last_switch_at": last.isoformat(), "channels": len(plan)}
                )  # fmt: skip
                await update_change(db, change_id, swapped_at=clock)
                await event(
                    db, change_id, from_state="swapping", to_state="swapping",
                    action="swap_commit", actor_source=actor_source, detail=report,
                )  # fmt: skip
            else:
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
