"""Build a TV release from the live state plus a change's pending edits (story 33.2,
design 3.2), with the change's diff and kinds (story 33.10 AC3).

``pending`` (``tv_changes.diff.pending``) is what a producer wants:

* ``channels`` - ``{channel id: def}`` for a changed or added channel (an id the live
  lineup does not have is an addition), ``{channel id: null}`` for a removal;
* ``art`` - ``{channel id: {kind: override | null}}``: the hand-set art (null: back to
  the plan's);
* ``sched`` - channel ids whose schedule is regenerated even though their definition
  did not change (a forced regeneration, the ``stale`` flag, a filler pool's clips);
* ``settings`` - ``{"tv" | "tv_output": stored overrides}``: the section as it will be;
* ``links`` / ``unlinks`` / ``pools`` / ``edited`` - ``write_channels`` and editor
  bookkeeping applied at the swap (:mod:`.commit`).

Nothing here changes what is on the air: a release, its channels and the ``staged``
rows of added channels (``channels.lifecycle = 'staged'``, written disabled) are new
rows; the live lineup, ``app_settings`` and generation N are only read.
"""

from __future__ import annotations

import uuid
from dataclasses import dataclass, field
from typing import Any

from ..engine.sql import dumps, is_pg, now, to_ts
from .snapshot import (
    SETTINGS_SECTIONS,
    content_digest,
    lineup_digest,
    live_snapshot,
    release_digest,
    streaming_version,
)

__all__ = [
    "Release",
    "apply_pending",
    "bootstrap",
    "build_release",
    "compute_diff",
    "filler_item_id",
    "load_release",
    "next_gen",
    "sched_fields_changed",
]

#: filler item ids of a pool a staged change writes: the generation it builds names the
#: clips by these, and the swap inserts them with the same ids
_FILLER_NS = uuid.UUID("6d616b74-6162-6174-762d-66696c6c6572")
#: definition fields whose change is a schedule change (design 11.4: grid, rules,
#: content selection); the look (art) is not one
_SCHED_FIELDS = ("library_id", "mode", "mode_config", "source_filter", "enabled")
_LINEUP_FIELDS = ("number", "name", "slug", "category", "sort_order", "enabled")


def filler_item_id(pool_key: str, video_id: str) -> str:
    return str(uuid.uuid5(_FILLER_NS, f"{pool_key}:{video_id}"))


@dataclass(slots=True)
class Release:
    id: str
    sched_gen: int
    digest: str
    kinds: list[str]
    settings: dict[str, Any]
    defs: dict[str, dict[str, Any]]
    art: dict[str, dict[str, Any]]
    sched_changed: set[str] = field(default_factory=set)
    base_release_id: str | None = None
    reused: bool = False


def _bare_mc(mc: Any) -> Any:
    return {k: v for k, v in mc.items() if k != "look"} if isinstance(mc, dict) else mc


def _look(d: dict[str, Any] | None) -> Any:
    mc = (d or {}).get("mode_config")
    return mc.get("look") if isinstance(mc, dict) else None


def sched_fields_changed(old: dict[str, Any] | None, new: dict[str, Any]) -> bool:
    """Whether a definition change is a schedule change (its grid, rules or content
    selection)."""
    if old is None:
        return True
    return any(
        (_bare_mc(old.get(f)) if f == "mode_config" else old.get(f))
        != (_bare_mc(new.get(f)) if f == "mode_config" else new.get(f))
        for f in _SCHED_FIELDS
    )


def apply_pending(base: dict[str, Any], pending: dict[str, Any]) -> dict[str, Any]:
    """The snapshot the change wants: the base with the pending edits laid over it."""
    defs = {k: dict(v) for k, v in base["defs"].items()}
    art = {k: dict(v) for k, v in base["art"].items()}
    for cid, d in (pending.get("channels") or {}).items():
        if d is None:
            defs.pop(cid, None)
            art.pop(cid, None)
        else:
            defs[cid] = {**defs.get(cid, {}), **d, "id": cid}
    for cid, kinds in (pending.get("art") or {}).items():
        if cid not in defs:
            continue
        cur = dict(art.get(cid, {}))
        for kind, o in (kinds or {}).items():
            if o is None:
                cur.pop(kind, None)
            else:
                cur[kind] = o
        if cur:
            art[cid] = cur
        else:
            art.pop(cid, None)
    settings = {k: dict(base["settings"].get(k) or {}) for k in SETTINGS_SECTIONS}
    for section, values in (pending.get("settings") or {}).items():
        if section in SETTINGS_SECTIONS and isinstance(values, dict):
            settings[section] = dict(values)
    return {"defs": defs, "art": art, "settings": settings}


def _summary(d: dict[str, Any]) -> dict[str, Any]:
    return {"id": d.get("id"), "number": d.get("number"), "name": d.get("name")}


def compute_diff(
    base: dict[str, Any],
    target: dict[str, Any],
    pending: dict[str, Any],
    streaming: tuple[tuple[str, str], tuple[str, str]] | None = None,
) -> tuple[dict[str, Any], list[str], set[str]]:
    """``(diff, kinds, channels whose schedule is regenerated)`` between two snapshots."""
    bd, td = base["defs"], target["defs"]
    added = [_summary(td[c]) for c in sorted(td) if c not in bd]
    removed = [_summary(bd[c]) for c in sorted(bd) if c not in td]
    renumbered, renamed, changed, art_changed = [], [], [], []
    sched: set[str] = {c for c in td if c not in bd}
    for cid in sorted(set(bd) & set(td)):
        o, n = bd[cid], td[cid]
        if o.get("number") != n.get("number"):
            renumbered.append({"id": cid, "from": o.get("number"), "to": n.get("number")})
        if o.get("name") != n.get("name"):
            renamed.append({"id": cid, "from": o.get("name"), "to": n.get("name")})
        fields = sorted(
            f
            for f in set(o) | set(n)
            if f not in ("id", "number", "name", "logo_path")
            and (_bare_mc(o.get(f)) if f == "mode_config" else o.get(f))
            != (_bare_mc(n.get(f)) if f == "mode_config" else n.get(f))
        )
        if fields:
            changed.append({**_summary(n), "fields": fields})
        kinds_art = []
        if o.get("logo_path") != n.get("logo_path"):
            kinds_art.append("logo_path")
        if _look(o) != _look(n):
            kinds_art.append("look")
        if base["art"].get(cid) != target["art"].get(cid):
            kinds_art.append("overrides")
        if kinds_art:
            art_changed.append({**_summary(n), "what": kinds_art})
        if sched_fields_changed(o, n):
            sched.add(cid)
    sched |= {str(c) for c in pending.get("sched") or [] if str(c) in td}
    settings: dict[str, list[dict[str, Any]]] = {}
    for section in SETTINGS_SECTIONS:
        o, n = base["settings"].get(section) or {}, target["settings"].get(section) or {}
        keys = sorted(k for k in set(o) | set(n) if o.get(k) != n.get(k))
        if keys:
            settings[section] = [{"key": k, "from": o.get(k), "to": n.get(k)} for k in keys]
    kinds: set[str] = set()
    if (
        added
        or removed
        or renumbered
        or renamed
        or any(set(c["fields"]) & set(_LINEUP_FIELDS) for c in changed)
    ):
        kinds.add("lineup")
    if art_changed:
        kinds.add("art")
    if settings:
        kinds.add("settings")
    if "tv" in settings:
        # rules.tv decides what airs (filler, Ramadan grids, live broadcasts): every
        # channel is planned anew after its boundary
        sched |= set(td)
        if any(x["key"] == "live" for x in settings["tv"]):
            kinds.add("live_slots")
    if sched - {c for c in td if c not in bd}:
        kinds.add("schedule")
    if changed and not kinds & {"lineup", "schedule"}:
        kinds.add("lineup")  # transition and the like: still a definition change
    if pending.get("rollback"):
        # story 33.9 AC4: a cold rollback re-stages a previous release as a change
        kinds.add("rollback")
    binary = None
    if streaming is not None and streaming[0] != streaming[1]:
        kinds.add("binary")
        binary = {"from": streaming[0][0], "to": streaming[1][0]}
    diff = {
        "lineup": {
            "added": added,
            "removed": removed,
            "renumbered": renumbered,
            "renamed": renamed,
            "changed": changed,
        },
        "schedules": [
            {**_summary(td[c]), "reason": "new" if c not in bd else "regenerated"}
            for c in sorted(sched)
        ],
        "settings": settings,
        "art": art_changed,
        "binary": binary,
    }
    order = ("lineup", "schedule", "settings", "art", "live_slots", "binary", "rollback")
    return diff, [k for k in order if k in kinds], sched


async def next_gen(db: Any) -> int:
    """A new schedule generation number."""
    if is_pg(db):
        return int(await db.fetchval("SELECT nextval('tv_sched_gen_seq')"))
    await db.execute("INSERT INTO tv_sched_gen_seq (at) VALUES ($1)", to_ts(db, now()))
    return int(await db.fetchval("SELECT MAX(gen) FROM tv_sched_gen_seq"))


async def _insert_release(
    db: Any,
    *,
    digest: str,
    kinds: list[str],
    sched_gen: int,
    snap: dict[str, Any],
    streaming: tuple[str, str],
    base_release_id: str | None,
    sched: set[str],
    created_by: str | None,
) -> str:
    rid = str(uuid.uuid4())
    await db.execute(
        "INSERT INTO tv_releases (id, digest, kinds, sched_gen, streaming_version,"
        " streaming_sha256, settings, lineup_digest, base_release_id, created_at, created_by)"
        " VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)",
        rid, digest, dumps(kinds), sched_gen, streaming[0], streaming[1], dumps(snap["settings"]),
        lineup_digest(snap["defs"], snap["art"]), base_release_id, to_ts(db, now()), created_by,
    )  # fmt: skip
    for cid, d in snap["defs"].items():
        await db.execute(
            "INSERT INTO tv_release_channels (release_id, channel_id, def, art, sched_changed)"
            " VALUES ($1,$2,$3,$4,$5)",
            rid,
            cid,
            dumps(d),
            dumps(snap["art"].get(cid) or {}),
            (cid in sched) if is_pg(db) else int(cid in sched),
        )
    return rid


async def bootstrap(db: Any, snap: dict[str, Any] | None = None) -> str:
    """The live release, creating release 1 ("what is live now", story 33.2 AC2) and the
    ``tv_routing`` cluster row (generation 0) the first time."""
    row = await db.fetchrow(
        "SELECT live_release_id, live_gen FROM tv_routing WHERE scope = 'cluster'"
    )
    if row is not None and row["live_release_id"] is not None:
        return str(row["live_release_id"])
    gen = int(row["live_gen"]) if row is not None else 0
    existing = await db.fetchrow("SELECT id FROM tv_releases WHERE sched_gen = $1", gen)
    if existing is not None:
        rid = str(existing["id"])
    else:
        snap = snap or await live_snapshot(db)
        streaming = streaming_version()
        content = content_digest(snap, streaming)
        rid = await _insert_release(
            db, digest=release_digest(content, None, []), kinds=["bootstrap"], sched_gen=gen,
            snap=snap, streaming=streaming, base_release_id=None, sched=set(),
            created_by="bootstrap",
        )  # fmt: skip
    if row is None:
        await db.execute(
            "INSERT INTO tv_routing (scope, live_release_id, live_gen, epoch, switched_at)"
            " VALUES ('cluster', $1, $2, 0, $3) ON CONFLICT (scope) DO NOTHING",
            rid,
            gen,
            to_ts(db, now()),
        )
    else:
        await db.execute(
            "UPDATE tv_routing SET live_release_id = $1 WHERE scope = 'cluster'"
            " AND live_release_id IS NULL",
            rid,
        )
    return rid


async def load_release(db: Any, release_id: str) -> Release | None:
    from ..engine.sql import loads

    r = await db.fetchrow("SELECT * FROM tv_releases WHERE id = $1", release_id)
    if r is None:
        return None
    defs: dict[str, dict[str, Any]] = {}
    art: dict[str, dict[str, Any]] = {}
    sched: set[str] = set()
    for c in await db.fetch(
        "SELECT channel_id, def, art, sched_changed FROM tv_release_channels WHERE release_id = $1",
        release_id,
    ):
        cid = str(c["channel_id"])
        defs[cid] = loads(c["def"])
        a = loads(c["art"]) if c["art"] else {}
        if a:
            art[cid] = a
        if bool(c["sched_changed"]):
            sched.add(cid)
    return Release(
        id=str(r["id"]),
        sched_gen=int(r["sched_gen"]),
        digest=str(r["digest"]),
        kinds=list(loads(r["kinds"]) or []),
        settings=loads(r["settings"]) or {},
        defs=defs,
        art=art,
        sched_changed=sched,
        base_release_id=None if r["base_release_id"] is None else str(r["base_release_id"]),
    )


async def build_release(
    db: Any, pending: dict[str, Any], *, created_by: str | None = None
) -> tuple[Release | None, dict[str, Any], list[str]]:
    """The release a change's ``pending`` edits make, on the live state: ``(release,
    diff, kinds)``; release None when the change airs exactly what is live (a noop,
    story 33.10 EC1). An equal release built on the same base before is reused."""
    base = await live_snapshot(db)
    base_id = await bootstrap(db, base)
    target = apply_pending(base, pending)
    live_streaming = streaming_version()
    target_streaming = tuple(pending.get("streaming") or live_streaming)
    diff, kinds, sched = compute_diff(
        base, target, pending, (live_streaming, (target_streaming[0], target_streaming[1]))
    )
    content = content_digest(target, (target_streaming[0], target_streaming[1]))
    if content == content_digest(base, live_streaming) and not sched:
        return None, diff, kinds
    digest = release_digest(content, base_id, sorted(sched))
    got = await db.fetchrow("SELECT id FROM tv_releases WHERE digest = $1", digest)
    if got is not None:
        rel = await load_release(db, str(got["id"]))
        assert rel is not None
        rel.reused = True
        return rel, diff, kinds
    gen = await next_gen(db)
    rid = await _insert_release(
        db, digest=digest, kinds=kinds, sched_gen=gen, snap=target,
        streaming=(target_streaming[0], target_streaming[1]), base_release_id=base_id,
        sched=sched, created_by=created_by,
    )  # fmt: skip
    # an added channel needs a channels row for its programmes' foreign key: staged,
    # disabled (every live reader skips it), its real values in the release
    stamp = to_ts(db, now())
    for cid, d in target["defs"].items():
        if cid in base["defs"]:
            continue
        if await db.fetchrow("SELECT 1 AS x FROM channels WHERE id = $1", cid) is not None:
            continue
        await db.execute(
            "INSERT INTO channels (id, library_id, number, name, slug, logo_path, category, mode,"
            " mode_config, source_filter, transition, enabled, sort_order, created_at,"
            " updated_at, lifecycle) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$14,"
            " 'staged')",
            cid, d.get("library_id"), int(d.get("number") or 0), str(d.get("name") or ""),
            str(d.get("slug") or cid), d.get("logo_path"), str(d.get("category") or "general"),
            str(d.get("mode") or "shuffle"), dumps(d.get("mode_config") or {}),
            None if d.get("source_filter") is None else dumps(d["source_filter"]),
            str(d.get("transition") or "cut"), False if is_pg(db) else 0,
            int(d.get("sort_order") or 0), stamp,
        )  # fmt: skip
    return (
        Release(
            id=rid, sched_gen=gen, digest=digest, kinds=kinds, settings=target["settings"],
            defs=target["defs"], art=target["art"], sched_changed=sched, base_release_id=base_id,
        ),
        diff,
        kinds,
    )  # fmt: skip
