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

``compute_changes`` is a pure diff of planned versus actual; ``apply_changes`` performs it.
"""

from __future__ import annotations

import re
import uuid
from dataclasses import dataclass, field
from typing import Any

from ...db.jobs import Job
from ..changes import ChangeSet, apply_or_review
from ..context import StageContext, StageFailure
from ..graph import CH_WRITE_VERSION, StageResult, Subject
from ..hashing import canonical_hash
from ..sql import dumps, is_pg, loads, now, to_ts
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


def content_hash(row: dict[str, Any]) -> str:
    """Hash of the user-visible definition of a channel (what the tool writes)."""
    return canonical_hash({k: row.get(k) for k in _FIELDS})


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


def _desired(plan: dict[str, Any]) -> dict[str, ChannelWrite]:
    out: dict[str, ChannelWrite] = {}
    for ch in plan.get("channels", []):
        m = ch["maktaba"]
        out[ch["key"]] = ChannelWrite(
            key=ch["key"],
            slug=_slug(ch["key"]),
            values={
                "number": int(m["number"]),
                "name": str(m["name"]),
                "category": str(m.get("category") or "general"),
                "mode": str(m["mode"]),
                "mode_config": m["mode_config"],
                "transition": str(m.get("transition") or "cut"),
                "enabled": bool(m.get("enabled", True)),
                "sort_order": int(m.get("sort_order", m["number"])),
            },
        )
    return out


async def _actual(db: Any, ids: list[str]) -> dict[str, dict[str, Any]]:
    out: dict[str, dict[str, Any]] = {}
    for cid in ids:
        r = await db.fetchrow(
            "SELECT id, number, name, category, mode, mode_config, transition, enabled, sort_order"
            " FROM channels WHERE id = $1",
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
            }
    return out


async def compute_changes(db: Any, plan: dict[str, Any]) -> tuple[ChangeSet, WritePlan]:
    """Planned versus actual: what to create, update and remove (nothing is written)."""
    desired = _desired(plan)
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
        if content_hash(row) != want.digest:
            n = want.values["number"]
            if taken_numbers.get(n) not in (None, str(link["external_id"])):
                wp.conflicts.append({"key": key, "number": n, "why": "number taken"})
                continue
            wp.update.append((str(link["external_id"]), want))
    for key, link in links.items():
        if key not in desired and link["created_by_tool"] and str(link["external_id"]) in actual:
            wp.delete.append((key, str(link["external_id"])))
    changed = []
    for cid, want in wp.update:
        cur = actual[cid]
        changed.append(
            {
                "key": want.key,
                "channel_id": cid,
                "fields": sorted(k for k in _FIELDS if cur.get(k) != want.values.get(k)),
            }
        )
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
                " mode_config, transition, enabled, sort_order, created_at, updated_at)"
                " VALUES ($1,NULL,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$11)",
                cid, v["number"], v["name"], w.slug, v["category"], v["mode"],
                dumps(v["mode_config"]), v["transition"], v["enabled"], v["sort_order"], stamp,
            )  # fmt: skip
            await _link(db, w, cid, stamp)
            written[w.key] = cid
        for cid, w in wp.update:
            v = w.values
            await db.execute(
                "UPDATE channels SET number = $2, name = $3, category = $4, mode = $5,"
                " mode_config = $6, transition = $7, enabled = $8, sort_order = $9,"
                " updated_at = $10 WHERE id = $1",
                cid, v["number"], v["name"], v["category"], v["mode"], dumps(v["mode_config"]),
                v["transition"], v["enabled"], v["sort_order"], stamp,
            )  # fmt: skip
            await _link(db, w, cid, stamp)
            written[w.key] = cid
        for cid in written.values():
            await db.execute(
                "INSERT INTO channel_schedule_state (channel_id, stale) VALUES ($1, $2)"
                " ON CONFLICT (channel_id) DO UPDATE SET stale = EXCLUDED.stale",
                cid,
                True if is_pg(db) else 1,
            )
    return written


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
        if content_hash(actual.get(cid, {})) != want[key].digest:
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
    return canonical_hash(
        {
            "plan": None if latest is None else latest["input_hash"],
            "mode": None if mode is None else mode["write_mode"],
            "v": CH_WRITE_VERSION,
        }
    )


async def run(db: Any, job: Job, ctx: StageContext) -> StageResult:
    latest = await _latest_plan(db)
    if latest is None:
        return StageResult(output_hash=canonical_hash("no-plan"), outcome="waiting_inputs")
    plan, _ = latest
    changes, wp = await compute_changes(db, plan)
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
        },
    )


IMPL = StageImpl(run=run, input_fingerprint=input_fingerprint)
