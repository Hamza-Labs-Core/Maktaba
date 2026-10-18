# ruff: noqa: E501  (long SQL string literals read better unwrapped)
"""``write_channels``: write the planned lineup to Maktaba's own Live TV (plan 06-11 section 3.2).

Core only: this creates, updates and removes rows of ``channels`` (Epic 27) for the latest
``channel_plans`` row and marks their schedules stale so the scheduler regenerates. Writing the
lineup to an external system (dizqueTV) is an extension stage of its own.

Safety rules carried over from the prototype's exporters:

* the tool only ever touches channels it created (``external_links.created_by_tool``);
* a channel somebody edited since the tool last wrote it (its stored content hash no longer matches
  the row) is left alone and reported, never overwritten;
* a planned channel whose number or slug is taken by a channel the tool did not create is skipped
  and reported (a ``conflict``);
* after writing, the rows are read back and verified (``readback_mismatch``).

What the dizqueTV export gives a channel, this gives it too, so Maktaba's own scheduler airs
the same: the plan's filler pools are written to ``filler_pools`` / ``filler_items`` (the
same clips as the dizqueTV filler lists, ``dz_filler_max_items`` of each; a pool is the
tool's by its ``external_key``, and only those are ever changed or removed), each channel's
filler lists with their weights and cooldowns (``mode_config.filler``), the grid's filler
rules (``slot_max_filler_minutes``, ``filler_between_episodes``) and its look: the logo
(``logo_path``, the file the dizqueTV export uploads) and ``mode_config.look`` (corner
watermark, loading picture, loudness).

``compute_changes`` is a pure diff of planned versus actual; ``apply_changes`` performs it.
"""

from __future__ import annotations

import re
import uuid
from dataclasses import dataclass, field
from typing import Any

from ...db.jobs import Job
from ...schedule.tv_channel import filler_items, filler_lists, plan_art
from ...schedule.tv_settings import MaktabaTvConfig
from ..changes import ChangeSet, apply_or_review
from ..context import StageContext, StageFailure
from ..graph import CH_WRITE_VERSION, StageResult, Subject
from ..hashing import canonical_hash
from ..sql import dumps, is_pg, loads, now, to_ts
from ._plan_json import pool_items
from .registry import StageImpl

__all__ = ["IMPL", "ChannelWrite", "WritePlan", "compute_changes", "content_hash"]

TARGET = "maktaba_channel"
SUBJECT_KIND = "catalog"
_FIELDS = (
    "number",
    "name",
    "category",
    "mode",
    "mode_config",
    "transition",
    "enabled",
    "sort_order",
)
#: the art is not part of the definition: a logo, watermark or loading picture set by
#: hand (``channel_art_overrides``, slot 0114) is never a user edit; it is compared on
#: its own (:func:`_art`)
_ART_KINDS = ("logo", "watermark", "loading")


def content_hash(row: dict[str, Any]) -> str:
    """Hash of the user-visible definition of a channel (what the tool writes), without
    its art (``logo_path``, ``mode_config.look``)."""
    out = {k: row.get(k) for k in _FIELDS}
    mc = out.get("mode_config")
    if isinstance(mc, dict):
        out["mode_config"] = {k: v for k, v in mc.items() if k != "look"}
    return canonical_hash(out)


def _changed_fields(cur: dict[str, Any], want: dict[str, Any]) -> list[str]:
    """What a rewrite changes: the definition's fields (``mode_config`` without its look),
    then the art (``logo_path``, ``look``)."""

    def bare(mc: Any) -> Any:
        return {k: v for k, v in mc.items() if k != "look"} if isinstance(mc, dict) else mc

    out = [
        k
        for k in _FIELDS
        if (bare(cur.get(k)) if k == "mode_config" else cur.get(k))
        != (bare(want.get(k)) if k == "mode_config" else want.get(k))
    ]
    old, new = _art(cur), _art(want)
    return sorted(out) + [
        n for n, a, b in zip(("logo_path", "look"), old, new, strict=True) if a != b
    ]


def _art(row: dict[str, Any]) -> tuple[Any, Any]:
    """A channel's art as stored: ``(logo_path, mode_config.look)``."""
    mc = row.get("mode_config")
    return row.get("logo_path"), (mc.get("look") if isinstance(mc, dict) else None)


def effective_art(
    logo: str | None, look_: dict[str, Any] | None, overrides: dict[str, dict[str, Any]]
) -> tuple[str | None, dict[str, Any] | None]:
    """The plan's art with what was set by hand on top (``channel_art_overrides`` rows by
    kind): an override's image replaces the plan's, a watermark override's settings
    (position, width_pct, margin_pct) replace the plan's settings."""
    if not overrides:
        return logo, look_
    out = dict(look_ or {})
    if (o := overrides.get("logo")) and o.get("path"):
        logo = str(o["path"])
    if o := overrides.get("watermark"):
        wm = dict(out.get("watermark") or {})
        if o.get("path"):
            wm["path"] = str(o["path"])
        if isinstance(o.get("settings"), dict):
            wm.update(o["settings"])
        out["watermark"] = wm or None
    if (o := overrides.get("loading")) and o.get("path"):
        out["loading_path"] = str(o["path"])
    return logo, out


async def _overrides(db: Any, channel_id: str) -> dict[str, dict[str, Any]]:
    """The art set by hand on a channel (slot 0114), by kind; none on a database that
    does not have the table yet."""
    if not await _has_overrides(db):
        return {}
    rows = await db.fetch(
        "SELECT kind, path, settings FROM channel_art_overrides WHERE channel_id = $1", channel_id
    )
    return {
        str(r["kind"]): {
            "path": r["path"],
            "settings": loads(r["settings"]) if r["settings"] else None,
        }
        for r in rows
        if str(r["kind"]) in _ART_KINDS
    }


async def _has_overrides(db: Any) -> bool:
    if is_pg(db):
        return bool(
            await db.fetchval(
                "SELECT count(*) FROM information_schema.tables"
                " WHERE table_name = 'channel_art_overrides' AND table_schema = current_schema()"
            )
        )
    return bool(
        await db.fetchval(
            "SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'channel_art_overrides'"
        )
    )


def _slug(key: str) -> str:
    s = re.sub(r"[^\w]+", "-", key.strip().lower(), flags=re.UNICODE).strip("-")
    return f"live-{s or 'x'}"


@dataclass(frozen=True, slots=True)
class ChannelWrite:
    key: str
    slug: str
    values: dict[str, Any]

    @property
    def digest(self) -> str:
        return content_hash(self.values)


@dataclass(slots=True)
class WritePlan:
    create: list[ChannelWrite] = field(default_factory=list)
    update: list[tuple[str, ChannelWrite]] = field(default_factory=list)  # (channel id, desired)
    delete: list[tuple[str, str]] = field(default_factory=list)  # (key, channel id)
    conflicts: list[dict[str, Any]] = field(default_factory=list)
    user_edited: list[dict[str, Any]] = field(default_factory=list)
    pools: PoolPlan = field(default_factory=lambda: PoolPlan())


@dataclass(slots=True)
class PoolWrite:
    key: str
    name: str
    items: dict[str, int]  # video id -> duration (ms)
    pool_id: str | None = None  # None: a new pool
    add: list[str] = field(default_factory=list)
    drop: list[str] = field(default_factory=list)
    retime: list[str] = field(default_factory=list)
    rename: bool = False


@dataclass(slots=True)
class PoolPlan:
    write: list[PoolWrite] = field(default_factory=list)
    delete: list[tuple[str, str]] = field(default_factory=list)  # (key, pool id)


def _filler_spec(ch: dict[str, Any], cfg: dict[str, Any]) -> dict[str, Any]:
    """The channel's filler lists as the dizqueTV export gives them (weight, cooldown)
    and the repeat cooldown of a clip (``dz_filler_cooldown_ms``)."""
    pools = list(ch.get("filler_pools") or [])
    lists = [
        {"pool": f["pool"], "weight": int(f["weight"]), "cooldown_ms": int(f.get("cooldown") or 0)}
        for f in filler_lists(str(ch["key"]), str(ch.get("kind") or ""), pools, cfg)
    ]
    return {"lists": lists, "repeat_cooldown_ms": int(cfg.get("dz_filler_cooldown_ms", 1_800_000))}


def look(plan: dict[str, Any], key: str, tv: MaktabaTvConfig) -> tuple[str | None, dict[str, Any]]:
    """``(logo_path, mode_config.look)`` of channel ``key``: the plan's art
    (:func:`plan_art`) and the loudness rule."""
    logo, art = plan_art(plan, key)
    return logo, {
        **art,
        "loudnorm": {"enabled": bool(tv.loudnorm), "target_lufs": float(tv.loudnorm_target_lufs)},
    }


def _desired(plan: dict[str, Any], tv: MaktabaTvConfig | None = None) -> dict[str, ChannelWrite]:
    tv = tv or MaktabaTvConfig()
    cfg = plan.get("config") or {}
    out: dict[str, ChannelWrite] = {}
    for ch in plan.get("channels", []):
        m = ch["maktaba"]
        logo, ch_look = look(plan, str(ch["key"]), tv)
        if "logo_path" in m or "look" in (m.get("mode_config") or {}):
            # the plan's own art (what the API shows as the plan's value)
            logo = m.get("logo_path")
            ch_look = {**(m["mode_config"].get("look") or {}), "loudnorm": ch_look["loudnorm"]}
        mode_config = {
            **m["mode_config"],
            "filler": _filler_spec(ch, cfg),
            # the grid's filler rules, as the dizqueTV time slots have them
            "slot_max_filler_minutes": int(cfg.get("slot_max_filler_minutes", 20)),
            "filler_between_episodes": bool(cfg.get("filler_between_episodes", True)),
            "look": ch_look,
        }
        out[ch["key"]] = ChannelWrite(
            key=ch["key"],
            slug=_slug(ch["key"]),
            values={
                "number": int(m["number"]),
                "name": str(m["name"]),
                "category": str(m.get("category") or "general"),
                "mode": str(m["mode"]),
                "mode_config": mode_config,
                "transition": str(m.get("transition") or "cut"),
                "enabled": bool(m.get("enabled", True)),
                "sort_order": int(m.get("sort_order", m["number"])),
                "logo_path": logo,
            },
        )
    return out


def _used_pools(plan: dict[str, Any]) -> list[str]:
    """Every pool a planned channel fills from: its filler lists and its slots' pools."""
    have = plan.get("filler_pools") or {}
    out: list[str] = []
    for ch in plan.get("channels", []):
        mc = (ch.get("maktaba") or {}).get("mode_config") or {}
        slots = list(mc.get("slots") or []) + list((mc.get("ramadan") or {}).get("slots") or [])
        for k in [*(ch.get("filler_pools") or []), *(s.get("filler_pool") for s in slots)]:
            if k and k in have and k not in out:
                out.append(k)
    return out


async def _pool_plan(db: Any, plan: dict[str, Any]) -> PoolPlan:
    """The plan's filler pools versus the ones the tool wrote (``external_key``)."""
    have = plan.get("filler_pools") or {}
    keys = _used_pools(plan)
    wanted: dict[str, dict[str, int]] = {}
    for k in keys:
        clips: dict[str, int] = {}
        for it in filler_items(plan, pool_items(plan, have[k])):
            try:
                vid = str(uuid.UUID(str(it.get("path"))))
            except ValueError:
                continue
            ms = int(round(float(it.get("duration_s") or 0) * 1000))
            if ms > 0:
                clips.setdefault(vid, ms)
        wanted[k] = clips
    live = await _live_ids(db, sorted({v for c in wanted.values() for v in c}))
    actual = {
        str(r["external_key"]): (str(r["id"]), str(r["name"]))
        for r in await db.fetch(
            "SELECT id, external_key, name FROM filler_pools WHERE external_key IS NOT NULL"
        )
    }
    pp = PoolPlan()
    for k in keys:
        want = {v: ms for v, ms in wanted[k].items() if v in live}
        name = str(have[k].get("name") or k)
        cur = actual.get(k)
        if cur is None:
            pp.write.append(PoolWrite(k, name, want, add=sorted(want)))
            continue
        rows = await db.fetch(
            "SELECT video_id, duration_ms FROM filler_items WHERE pool_id = $1", cur[0]
        )
        old = {str(r["video_id"]): int(r["duration_ms"] or 0) for r in rows}
        w = PoolWrite(
            k,
            name,
            want,
            pool_id=cur[0],
            add=sorted(set(want) - set(old)),
            drop=sorted(set(old) - set(want)),
            retime=sorted(v for v in set(want) & set(old) if want[v] != old[v]),
            rename=cur[1] != name,
        )
        if w.add or w.drop or w.retime or w.rename:
            pp.write.append(w)
    pp.delete = [(k, pid) for k, (pid, _) in actual.items() if k not in wanted]
    return pp


async def _live_ids(db: Any, ids: list[str]) -> set[str]:
    out: set[str] = set()
    for i in range(0, len(ids), 500):
        part = ids[i : i + 500]
        cast = "::uuid" if is_pg(db) else ""
        marks = ",".join(f"${n}{cast}" for n in range(1, len(part) + 1))
        rows = await db.fetch(
            f"SELECT id FROM videos WHERE id IN ({marks}) AND deleted_at IS NULL", *part
        )
        out.update(str(r["id"]) for r in rows)
    return out


async def _actual(db: Any, ids: list[str]) -> dict[str, dict[str, Any]]:
    out: dict[str, dict[str, Any]] = {}
    for cid in ids:
        r = await db.fetchrow(
            "SELECT id, number, name, category, mode, mode_config, transition, enabled, sort_order,"
            " logo_path FROM channels WHERE id = $1",
            cid,
        )
        if r is not None:
            out[cid] = {
                "number": int(r["number"]),
                "name": str(r["name"]),
                "category": str(r["category"]),
                "mode": str(r["mode"]),
                "mode_config": loads(r["mode_config"]),
                "transition": str(r["transition"]),
                "enabled": bool(r["enabled"]),
                "sort_order": int(r["sort_order"]),
                "logo_path": None if r["logo_path"] is None else str(r["logo_path"]),
            }
    return out


async def compute_changes(
    db: Any, plan: dict[str, Any], tv: MaktabaTvConfig | None = None
) -> tuple[ChangeSet, WritePlan]:
    """Planned versus actual: what to create, update and remove (nothing is written)."""
    desired = _desired(plan, tv)
    links = {
        str(r["subject_id"]): r
        for r in await db.fetch(
            "SELECT subject_id, external_id, content_hash, created_by_tool FROM external_links"
            " WHERE target = $1 AND subject_kind = $2",
            TARGET,
            SUBJECT_KIND,
        )
    }
    actual = await _actual(db, [str(r["external_id"]) for r in links.values()])
    has_ov = await _has_overrides(db)
    wp = WritePlan()
    taken_numbers = {
        int(r["number"]): str(r["id"])
        for r in await db.fetch("SELECT id, number FROM channels WHERE library_id IS NULL")
    }
    taken_slugs = {
        str(r["slug"]): str(r["id"])
        for r in await db.fetch("SELECT id, slug FROM channels WHERE library_id IS NULL")
    }
    tool_ids = {str(r["external_id"]) for r in links.values() if r["created_by_tool"]}
    for key, want in desired.items():
        link = links.get(key)
        row = actual.get(str(link["external_id"])) if link is not None else None
        if link is None or row is None:
            clash_n = taken_numbers.get(want.values["number"])
            clash_s = taken_slugs.get(want.slug)
            if (clash_n and clash_n not in tool_ids) or (clash_s and clash_s not in tool_ids):
                wp.conflicts.append(
                    {"key": key, "number": want.values["number"], "why": "number or slug taken"}
                )
                continue
            wp.create.append(want)
            continue
        if content_hash(row) != str(link["content_hash"]):
            wp.user_edited.append({"key": key, "channel_id": str(link["external_id"])})
            continue
        ov = await _overrides(db, str(link["external_id"])) if has_ov else {}
        if ov:
            logo, art_look = effective_art(
                want.values["logo_path"], want.values["mode_config"].get("look"), ov
            )
            want = ChannelWrite(
                want.key,
                want.slug,
                {**want.values, "logo_path": logo,
                 "mode_config": {**want.values["mode_config"], "look": art_look}},
            )  # fmt: skip
        if content_hash(row) != want.digest or _art(row) != _art(want.values):
            n = want.values["number"]
            if taken_numbers.get(n) not in (None, str(link["external_id"])):
                wp.conflicts.append({"key": key, "number": n, "why": "number taken"})
                continue
            wp.update.append((str(link["external_id"]), want))
    for key, link in links.items():
        if key not in desired and link["created_by_tool"] and str(link["external_id"]) in actual:
            wp.delete.append((key, str(link["external_id"])))
    changed: list[dict[str, Any]] = []
    for cid, want in wp.update:
        cur = actual[cid]
        changed.append(
            {
                "key": want.key,
                "channel_id": cid,
                "fields": _changed_fields(cur, want.values),
            }
        )
    wp.pools = await _pool_plan(db, plan)
    for pw in wp.pools.write:
        changed.append(
            {
                "filler_pool": pw.key,
                "new": pw.pool_id is None,
                "added": len(pw.add),
                "removed": len(pw.drop),
                "retimed": len(pw.retime),
            }
        )
    changed.extend({"filler_pool": k, "deleted": True} for k, _ in wp.pools.delete)
    cs = ChangeSet(
        "write_channels",
        added=tuple(
            {"key": w.key, "number": w.values["number"], "name": w.values["name"]}
            for w in wp.create
        ),
        removed=tuple({"key": k, "channel_id": cid} for k, cid in wp.delete),
        changed=tuple(changed),
        meta={"conflicts": wp.conflicts, "user_edited": wp.user_edited},
    )
    return cs, wp


async def apply_changes(db: Any, wp: WritePlan) -> dict[str, str]:
    """Perform the plan in one transaction; returns ``key -> channel id`` for what was written."""
    written: dict[str, str] = {}
    stamp = to_ts(db, now())
    async with db.transaction():
        for key, cid in wp.delete:
            await db.execute("DELETE FROM channels WHERE id = $1", cid)
            await db.execute(
                "DELETE FROM external_links WHERE target = $1 AND subject_kind = $2 AND subject_id = $3",
                TARGET,
                SUBJECT_KIND,
                key,
            )
        for w in wp.create:
            cid = str(uuid.uuid4())
            v = w.values
            await db.execute(
                "INSERT INTO channels (id, library_id, number, name, slug, category, mode,"
                " mode_config, transition, enabled, sort_order, created_at, updated_at, logo_path)"
                " VALUES ($1,NULL,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$11,$12)",
                cid, v["number"], v["name"], w.slug, v["category"], v["mode"],
                dumps(v["mode_config"]), v["transition"], v["enabled"], v["sort_order"], stamp,
                v["logo_path"],
            )  # fmt: skip
            await _link(db, w, cid, stamp)
            written[w.key] = cid
        for cid, w in wp.update:
            v = w.values
            await db.execute(
                "UPDATE channels SET number = $2, name = $3, category = $4, mode = $5,"
                " mode_config = $6, transition = $7, enabled = $8, sort_order = $9,"
                " updated_at = $10, logo_path = $11 WHERE id = $1",
                cid, v["number"], v["name"], v["category"], v["mode"], dumps(v["mode_config"]),
                v["transition"], v["enabled"], v["sort_order"], stamp, v["logo_path"],
            )  # fmt: skip
            await _link(db, w, cid, stamp)
            written[w.key] = cid
        pools_changed = await _apply_pools(db, wp.pools, stamp)
        stale = set(written.values())
        if pools_changed:
            # what fills the gaps changed: every channel the tool made plans its filler anew
            stale |= {
                str(r["external_id"])
                for r in await db.fetch(
                    "SELECT external_id FROM external_links WHERE target = $1 AND subject_kind = $2",
                    TARGET,
                    SUBJECT_KIND,
                )
            }
        for cid in sorted(stale):
            if await db.fetchrow("SELECT 1 AS x FROM channels WHERE id = $1", cid) is None:
                continue
            await db.execute(
                "INSERT INTO channel_schedule_state (channel_id, stale) VALUES ($1, $2)"
                " ON CONFLICT (channel_id) DO UPDATE SET stale = EXCLUDED.stale",
                cid,
                True if is_pg(db) else 1,
            )
    return written


async def _apply_pools(db: Any, pp: PoolPlan, stamp: Any) -> bool:
    """Write the filler pools (only the tool's own: by ``external_key``). True when
    anything changed."""
    for _, gone in pp.delete:
        await db.execute("DELETE FROM filler_items WHERE pool_id = $1", gone)
        await db.execute(
            "DELETE FROM filler_pools WHERE id = $1 AND external_key IS NOT NULL", gone
        )
    for pw in pp.write:
        pid: str
        if pw.pool_id is None:
            pid = str(uuid.uuid4())
            await db.execute(
                "INSERT INTO filler_pools (id, channel_id, name, external_key, created_at,"
                " updated_at) VALUES ($1, NULL, $2, $3, $4, $4)",
                pid,
                pw.name,
                pw.key,
                stamp,
            )
        else:
            pid = pw.pool_id
            await db.execute(
                "UPDATE filler_pools SET name = $2, updated_at = $3 WHERE id = $1",
                pid,
                pw.name,
                stamp,
            )
        if pw.drop:
            for i in range(0, len(pw.drop), 500):
                part = pw.drop[i : i + 500]
                cast = "::uuid" if is_pg(db) else ""
                marks = ",".join(f"${n + 2}{cast}" for n in range(len(part)))
                await db.execute(
                    f"DELETE FROM filler_items WHERE pool_id = $1 AND video_id IN ({marks})",
                    pid,
                    *part,
                )
        if pw.add:
            await db.executemany(
                "INSERT INTO filler_items (id, pool_id, video_id, type, duration_ms, created_at)"
                " VALUES ($1, $2, $3, 'filler', $4, $5) ON CONFLICT (pool_id, video_id) DO NOTHING",
                [(str(uuid.uuid4()), pid, v, pw.items[v], stamp) for v in pw.add],
            )
        if pw.retime:
            await db.executemany(
                "UPDATE filler_items SET duration_ms = $3 WHERE pool_id = $1 AND video_id = $2",
                [(pid, v, pw.items[v]) for v in pw.retime],
            )
    return bool(pp.write or pp.delete)


async def _link(db: Any, w: ChannelWrite, channel_id: str, stamp: Any) -> None:
    await db.execute(
        "INSERT INTO external_links (target, subject_kind, subject_id, external_id, title,"
        " content_hash, created_by_tool, updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)"
        " ON CONFLICT (target, subject_kind, subject_id) DO UPDATE SET external_id = EXCLUDED.external_id,"
        " title = EXCLUDED.title, content_hash = EXCLUDED.content_hash, updated_at = EXCLUDED.updated_at",
        TARGET,
        SUBJECT_KIND,
        w.key,
        channel_id,
        w.values["name"],
        w.digest,
        True if is_pg(db) else 1,
        stamp,
    )


async def _verify(db: Any, wp: WritePlan, written: dict[str, str]) -> None:
    """Read the rows back and compare with what was meant to be written."""
    want = {w.key: w for w in wp.create} | {w.key: w for _, w in wp.update}
    actual = await _actual(db, list(written.values()))
    for key, cid in written.items():
        got = actual.get(cid, {})
        if content_hash(got) != want[key].digest or _art(got) != _art(want[key].values):
            raise StageFailure(
                "readback_mismatch", f"channel {key} differs after writing", retryable=True
            )
    for _, cid in wp.delete:
        if await db.fetchrow("SELECT 1 AS x FROM channels WHERE id = $1", cid) is not None:
            raise StageFailure("readback_mismatch", f"channel {cid} still exists", retryable=True)


async def _latest_plan(db: Any) -> tuple[dict[str, Any], str] | None:
    r = await db.fetchrow("SELECT plan, input_hash FROM channel_plans ORDER BY id DESC LIMIT 1")
    return None if r is None else (loads(r["plan"]), str(r["input_hash"]))


async def input_fingerprint(db: Any, subject: Subject) -> str | None:
    latest = await db.fetchrow("SELECT id, input_hash FROM channel_plans ORDER BY id DESC LIMIT 1")
    mode = await db.fetchrow("SELECT write_mode FROM job_stages WHERE stage = 'write_channels'")
    from ... import rules

    _, tv_changes, _ = await rules.read(db, "tv")
    return canonical_hash(
        {
            "plan": None if latest is None else int(latest["id"]),  # a new row = a new lineup
            "mode": None if mode is None else mode["write_mode"],
            "v": CH_WRITE_VERSION,
            "tv": tv_changes,  # the loudness rule is part of a channel's look
        }
    )


async def run(db: Any, job: Job, ctx: StageContext) -> StageResult:
    latest = await _latest_plan(db)
    if latest is None:
        return StageResult(output_hash=canonical_hash("no-plan"), outcome="waiting_inputs")
    plan, _ = latest
    tv: MaktabaTvConfig = await ctx.rules("tv")
    changes, wp = await compute_changes(db, plan, tv)
    written: dict[str, str] = {}

    async def apply(_cs: ChangeSet) -> None:
        nonlocal written
        written = await apply_changes(db, wp)
        await _verify(db, wp, written)

    mode = await apply_or_review(ctx, changes, apply)
    if mode == "review":
        return StageResult(output_hash=canonical_hash("review"), outcome="awaiting_review")
    final = await db.fetch(
        "SELECT subject_id, content_hash FROM external_links WHERE target = $1 AND subject_kind = $2"
        " ORDER BY subject_id",
        TARGET,
        SUBJECT_KIND,
    )
    return StageResult(
        output_hash=canonical_hash([[str(r["subject_id"]), r["content_hash"]] for r in final]),
        summary={
            "created": len(wp.create),
            "updated": len(wp.update),
            "removed": len(wp.delete),
            "conflicts": wp.conflicts,
            "user_edited": wp.user_edited,
            "filler_pools": {
                "written": len(wp.pools.write),
                "removed": len(wp.pools.delete),
            },
        },
    )


IMPL = StageImpl(run=run, input_fingerprint=input_fingerprint)
