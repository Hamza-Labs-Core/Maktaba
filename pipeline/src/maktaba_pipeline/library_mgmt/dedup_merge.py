"""Merge a duplicate group without loss (Epic 47, story 47.7: ``dedup_apply``).

One group at a time (``duplicate_groups`` + ``duplicate_members``, written by
``dedup_scan``):

1. **The foreign-key guard.** Every foreign key to ``videos`` in the catalogue
   (``pg_constraint`` / SQLite's ``pragma_foreign_key_list``) must have a rule in
   :data:`RULES`; otherwise nothing runs (:class:`MergeRefused`). A test runs the same
   check, so a migration that adds a table pointing at ``videos`` fails CI until the
   merge knows what to do with it.
2. **The keeper is checked**: ``ffprobe`` reads its file, and for an exact group its full
   BLAKE3 and every other member's are still the group's. If not: the group is
   ``failed`` and nothing moves.
3. **One transaction**: for each other row (a *loser*) the user data moves to the
   keeper by the spec's table (:data:`RULES`), derived data moves only if the keeper
   lacks it, and the loser becomes ``state = 'superseded'``, ``superseded_by = keeper``;
   the group is ``applied``.
4. **The files**: every member path but the keeper's own goes to the library's trash,
   one ``file_actions`` row (kind ``dedup``) each, created ``swapped`` so that recovery
   finishes a move a crash cut short. A move that fails leaves its row ``failed`` and the
   file where it was; the job's retry moves the files only.

``trash_purge`` deletes the file after the grace period, and then the superseded row
(its derived data cascades). **Restore** (``swap.restore``) puts the file back and the
loser back to the state it had (``verify.prior_state``); a restored copy is a recorded
copy again. The user data that moved stays with the keeper (the spec accepts it: a
restore is for a file merged by mistake, and the keeper holds the union).
"""

from __future__ import annotations

import asyncio
import os
from collections.abc import Awaitable, Callable, Mapping
from dataclasses import dataclass, field
from datetime import date
from typing import Any, Literal

from ..engine import paths
from ..engine.sql import is_pg, loads, now, to_ts
from ..identity.hasher import hash_full_file
from . import verify
from .duplicates import keeper_order, load_facts
from .ledger import (
    FileAction,
    advance,
    create_action,
    get_action,
    list_actions,
    update_fields,
)
from .swap import _trash_target
from .trash import TrashError, move_to_trash, purge_after, today

__all__ = [
    "RULES",
    "ApplyOutcome",
    "KeeperUnreadable",
    "MergeRefused",
    "Rule",
    "apply_group",
    "foreign_keys_to_videos",
    "merge_rows",
    "uncovered_foreign_keys",
]

ToLocal = Callable[[str], str]
ProbeFn = Callable[[str], Awaitable[Any]]

RuleKind = Literal["move", "union", "merge", "drop", "keep", "derived"]


@dataclass(frozen=True, slots=True)
class Rule:
    """What a merge does with a loser's rows of one table.

    * ``move``: repointed to the keeper (history is additive);
    * ``union``: repointed unless the keeper has a row with the same ``key`` (the
      keeper's wins the clash), then the loser's rest is deleted;
    * ``merge``: ``playback_state``, the most recent position and ``completed`` if either;
    * ``drop``: deleted (recomputed for the keeper);
    * ``keep``: left on the loser, to cascade when ``trash_purge`` deletes it (per-file
      facts, job history, the ledger);
    * ``derived``: moved, with the rest of its ``family``, only if the keeper has no row
      of the family's first table.

    ``relib``: the table's own ``library_id`` follows the video (a merge across
    libraries keeps the keeper in its library)."""

    kind: RuleKind
    key: tuple[str, ...] = ()
    family: str | None = None
    relib: bool = False


#: one rule per foreign key to ``videos(id)``: ``(table, column)``
RULES: Mapping[tuple[str, str], Rule] = {
    # ---- user data (spec section B, "The merge", table 1)
    ("playback_state", "video_id"): Rule("merge"),
    ("watch_sessions", "video_id"): Rule("move"),
    ("streaming_sessions", "video_id"): Rule("move"),
    ("user_playlist_items", "video_id"): Rule("union", ("playlist_id",)),
    ("user_favorites", "video_id"): Rule("union", ("user_id",)),
    ("collection_items", "video_id"): Rule("union", ("collection_id",)),
    ("video_tags", "video_id"): Rule("union", ("tag_id",)),
    ("episode_tags", "video_id"): Rule("union", ("tag",)),
    ("video_topics", "video_id"): Rule("union", ("topic_id",), relib=True),
    ("series_episodes", "video_id"): Rule("union", ()),  # one series per video
    ("recommendation_dismissals", "video_id"): Rule("union", ("user_id", "reason_kind")),
    ("user_recs", "video_id"): Rule("drop"),
    # ---- what groups, schedules or links the video (kept pointing at a playable file)
    ("topic_items", "video_id"): Rule("union", ("topic_id",)),
    ("clip_group_items", "video_id"): Rule("union", ()),  # one clip group per video
    ("filler_items", "video_id"): Rule("union", ("pool_id",)),
    ("channel_programs", "video_id"): Rule("move"),
    ("promo_links", "video_id"): Rule("union", (), relib=True),
    ("youtube_imports", "video_id"): Rule("union", ("youtube_id",)),
    ("youtube_downloads", "video_id"): Rule("move"),
    ("videos", "superseded_by"): Rule("move"),  # an earlier merge's losers follow
    # ---- derived data: moved only if the keeper lacks it (table 2)
    ("transcripts", "video_id"): Rule("derived", family="transcripts"),
    ("transcript_units", "video_id"): Rule("derived", family="transcripts"),
    ("segment_embeddings", "video_id"): Rule("derived", family="transcripts", relib=True),
    ("speakers", "video_id"): Rule("derived", ("name",), family="transcripts", relib=True),
    ("subtitle_files", "video_id"): Rule("derived", family="subtitle_files"),
    ("chapters", "video_id"): Rule("derived", family="chapters"),
    ("video_embeddings", "video_id"): Rule("derived", family="video_embeddings", relib=True),
    ("media_metadata_enrichment", "video_id"): Rule("derived", family="media_metadata_enrichment"),
    # ---- facts of the loser's own file or its processing: cascade at purge
    ("audio_tracks", "video_id"): Rule("keep"),  # those its moved transcripts use move
    ("audio_cache", "video_id"): Rule("keep"),
    ("media_info", "video_id"): Rule("keep"),
    ("media_features", "video_id"): Rule("keep"),
    ("media_parsed_titles", "video_id"): Rule("keep"),
    ("media_field_provenance", "video_id"): Rule("keep"),
    ("video_signals", "video_id"): Rule("keep"),
    ("video_classification", "video_id"): Rule("keep"),
    ("video_entities", "video_id"): Rule("keep"),
    ("video_topic_tags", "video_id"): Rule("keep"),
    ("video_repairs", "video_id"): Rule("keep"),
    ("integrity_checks", "video_id"): Rule("keep"),
    ("item_checks", "video_id"): Rule("keep"),
    ("item_requests", "video_id"): Rule("keep"),
    ("clip_checks", "video_id"): Rule("keep"),
    ("enrich_jobs", "video_id"): Rule("keep"),
    ("enrichment_decisions", "video_id"): Rule("keep"),
    ("relisten_attempts", "video_id"): Rule("keep"),
    ("transcribe_checkpoints", "video_id"): Rule("keep"),
    ("processing_jobs", "video_id"): Rule("keep"),
    ("plex_items", "video_id"): Rule("keep"),  # Plex's own scan re-links its item
    ("video_copies", "video_id"): Rule("keep"),  # the copies are members: trashed
    ("file_actions", "video_id"): Rule("keep"),  # the ledger (restore needs the loser)
    ("duplicate_members", "video_id"): Rule("keep"),
    ("duplicate_groups", "keeper_video_id"): Rule("keep"),
}

#: the tables of a derived family, the first one deciding whether the keeper "has it"
FAMILIES: Mapping[str, tuple[str, ...]] = {
    "transcripts": ("transcripts", "transcript_units", "segment_embeddings", "speakers"),
    "subtitle_files": ("subtitle_files",),
    "chapters": ("chapters",),
    "video_embeddings": ("video_embeddings",),
    "media_metadata_enrichment": ("media_metadata_enrichment",),
}


class MergeRefused(Exception):
    """A foreign key to ``videos`` that no rule covers: the merge does not run."""

    def __init__(self, uncovered: list[tuple[str, str]]) -> None:
        names = ", ".join(f"{t}.{c}" for t, c in uncovered)
        super().__init__(f"no merge rule for the foreign keys to videos: {names}")
        self.uncovered = uncovered


class KeeperUnreadable(Exception):
    """The keeper's file did not pass the last check: the group failed, nothing moved."""


# ------------------------------------------------------------------ catalogue


async def foreign_keys_to_videos(db: Any) -> list[tuple[str, str]]:
    """Every ``(table, column)`` with a foreign key to ``videos``, from the catalogue."""
    if is_pg(db):
        rows = await db.fetch(
            "SELECT c.conrelid::regclass::text AS tbl, a.attname AS col"
            " FROM pg_constraint c JOIN pg_attribute a"
            "   ON a.attrelid = c.conrelid AND a.attnum = ANY (c.conkey)"
            " WHERE c.contype = 'f' AND c.confrelid = 'videos'::regclass"
        )
    else:
        rows = await db.fetch(
            'SELECT m.name AS tbl, f."from" AS col FROM sqlite_master m'
            " JOIN pragma_foreign_key_list(m.name) f"
            " WHERE m.type = 'table' AND f.\"table\" = 'videos'"
        )
    return sorted({(str(r["tbl"]).split(".")[-1].strip('"'), str(r["col"])) for r in rows})


async def uncovered_foreign_keys(db: Any) -> list[tuple[str, str]]:
    """The foreign keys to ``videos`` that :data:`RULES` does not cover."""
    return [fk for fk in await foreign_keys_to_videos(db) if fk not in RULES]


async def _tables(db: Any) -> set[str]:
    if is_pg(db):
        rows = await db.fetch(
            "SELECT table_name AS t FROM information_schema.tables"
            " WHERE table_schema = current_schema()"
        )
    else:
        rows = await db.fetch("SELECT name AS t FROM sqlite_master WHERE type = 'table'")
    return {str(r["t"]) for r in rows}


# ------------------------------------------------------------------ the merge


@dataclass
class _Ctx:
    keeper: str
    loser: str
    keeper_library: str
    tables: set[str]
    counts: dict[str, int] = field(default_factory=dict)

    def count(self, table: str, n: int) -> None:
        if n:
            self.counts[table] = self.counts.get(table, 0) + n


async def _n(db: Any, table: str, col: str, vid: str) -> int:
    return int(await db.fetchval(f"SELECT COUNT(*) FROM {table} WHERE {col} = $1", vid) or 0)


async def _move(db: Any, c: _Ctx, table: str, col: str, rule: Rule) -> None:
    n = await _n(db, table, col, c.loser)
    if not n:
        return
    relib = ", library_id = $3" if rule.relib else ""
    args = [c.keeper, c.loser] + ([c.keeper_library] if rule.relib else [])
    await db.execute(f"UPDATE {table} SET {col} = $1{relib} WHERE {col} = $2", *args)
    c.count(table, n)


async def _union(db: Any, c: _Ctx, table: str, col: str, rule: Rule) -> None:
    before = await _n(db, table, col, c.loser)
    if not before:
        return
    clash = "".join(f" AND k.{k} = {table}.{k}" for k in rule.key)
    relib = ", library_id = $3" if rule.relib else ""
    args = [c.keeper, c.loser] + ([c.keeper_library] if rule.relib else [])
    await db.execute(
        f"UPDATE {table} SET {col} = $1{relib} WHERE {col} = $2 AND NOT EXISTS"
        f" (SELECT 1 FROM {table} k WHERE k.{col} = $1{clash})",
        *args,
    )
    left = await _n(db, table, col, c.loser)
    await db.execute(f"DELETE FROM {table} WHERE {col} = $1", c.loser)
    c.count(table, before - left)


async def _merge_playback(db: Any, c: _Ctx) -> None:
    """Per user: the most recent position (and its track choices), ``completed`` if
    either is; a user only the loser has moves as it is."""
    if not await _n(db, "playback_state", "video_id", c.loser):
        return
    lo = (
        "(SELECT l.{f} FROM playback_state l"
        " WHERE l.video_id = $2 AND l.user_id = playback_state.user_id)"
    )
    newer = f"{lo.format(f='updated_at')} > playback_state.updated_at"
    await db.execute(
        "UPDATE playback_state SET"
        f" position_sec = CASE WHEN {newer} THEN {lo.format(f='position_sec')}"
        " ELSE position_sec END,"
        f" audio_track_id = CASE WHEN {newer} THEN {lo.format(f='audio_track_id')}"
        " ELSE audio_track_id END,"
        f" subtitle_lang = CASE WHEN {newer} THEN {lo.format(f='subtitle_lang')}"
        " ELSE subtitle_lang END,"
        f" completed = CASE WHEN completed OR {lo.format(f='completed')}"
        " THEN true ELSE false END,"
        f" updated_at = CASE WHEN {newer} THEN {lo.format(f='updated_at')}"
        " ELSE updated_at END"
        " WHERE video_id = $1 AND user_id IN"
        " (SELECT user_id FROM playback_state WHERE video_id = $2)",
        c.keeper,
        c.loser,
    )
    await _union(db, c, "playback_state", "video_id", Rule("union", ("user_id",)))


async def _move_family(db: Any, c: _Ctx, family: str, col: str) -> None:
    tables = [t for t in FAMILIES[family] if t in c.tables]
    if not tables or await _n(db, tables[0], col, c.keeper):
        return  # the keeper has its own
    if not await _n(db, tables[0], col, c.loser):
        return
    if family == "transcripts":
        # transcripts.audio_track_id is NOT NULL and has no ON DELETE: a moved transcript
        # names the keeper's track of the same index, or takes the loser's track along
        for r in await db.fetch(
            "SELECT DISTINCT t.audio_track_id AS aid, a.track_index AS idx"
            " FROM transcripts t JOIN audio_tracks a ON a.id = t.audio_track_id"
            " WHERE t.video_id = $1",
            c.loser,
        ):
            k = await db.fetchval(
                "SELECT id FROM audio_tracks WHERE video_id = $1 AND track_index = $2",
                c.keeper,
                r["idx"],
            )
            if k is not None:
                await db.execute(
                    "UPDATE transcripts SET audio_track_id = $1"
                    " WHERE video_id = $2 AND audio_track_id = $3",
                    k,
                    c.loser,
                    r["aid"],
                )
            else:
                await db.execute(
                    "UPDATE audio_tracks SET video_id = $1 WHERE id = $2", c.keeper, r["aid"]
                )
                c.count("audio_tracks", 1)
    for t in tables:
        rule = RULES[(t, col)]
        if rule.key:
            await _union(db, c, t, col, rule)
        else:
            await _move(db, c, t, col, rule)


async def merge_rows(db: Any, keeper: str, loser: str, *, keeper_library: str) -> dict[str, int]:
    """Move the loser's data to the keeper by :data:`RULES` and supersede the loser.
    Runs in the caller's transaction; returns the rows moved per table."""
    uncovered = await uncovered_foreign_keys(db)
    if uncovered:
        raise MergeRefused(uncovered)
    c = _Ctx(keeper=keeper, loser=loser, keeper_library=keeper_library, tables=await _tables(db))
    families_done: set[str] = set()
    for (table, col), rule in RULES.items():
        if table not in c.tables:
            continue
        if rule.kind == "move":
            await _move(db, c, table, col, rule)
        elif rule.kind == "union":
            await _union(db, c, table, col, rule)
        elif rule.kind == "merge":
            await _merge_playback(db, c)
        elif rule.kind == "drop":
            n = await _n(db, table, col, c.loser)
            await db.execute(f"DELETE FROM {table} WHERE {col} = $1", c.loser)
            c.count(table, n)
        elif rule.kind == "derived" and rule.family not in families_done:
            assert rule.family is not None
            families_done.add(rule.family)
            await _move_family(db, c, rule.family, col)
    await db.execute(
        "UPDATE videos SET state = 'superseded', superseded_by = $2, updated_at = $3 WHERE id = $1",
        loser,
        keeper,
        to_ts(db, now()),
    )
    return c.counts


# ------------------------------------------------------------------ apply


@dataclass
class ApplyOutcome:
    status: Literal["applied", "failed", "noop", "dry_run"]
    group_id: str
    keeper_video_id: str | None = None
    losers: list[str] = field(default_factory=list)
    moved: dict[str, int] = field(default_factory=dict)
    trashed: list[str] = field(default_factory=list)
    failed_moves: list[tuple[str, str]] = field(default_factory=list)
    reason: str | None = None
    #: the files that would go to the trash (a dry run)
    plan: list[str] = field(default_factory=list)

    def summary(self) -> dict[str, Any]:
        return {
            "status": self.status,
            "group_id": self.group_id,
            "keeper_video_id": self.keeper_video_id,
            "losers": self.losers,
            "moved": self.moved,
            "trashed": len(self.trashed),
            "failed_moves": [{"path": p, "error": e} for p, e in self.failed_moves],
            "reason": self.reason,
            **({"plan": self.plan} if self.plan else {}),
        }


@dataclass(frozen=True, slots=True)
class _M:
    video_id: str
    path: str
    is_copy: bool
    full_hash: str | None
    size: int


async def _fail_group(db: Any, gid: str, reason: str) -> None:
    await db.execute(
        "UPDATE duplicate_groups SET status = 'failed', reason = $2 WHERE id = $1", gid, reason
    )


async def _full(local: str) -> str:
    return await asyncio.to_thread(hash_full_file, paths.find_existing(local))


async def apply_group(
    db: Any,
    group_id: str,
    *,
    keeper_video_id: str | None = None,
    decided_by: str | None = None,
    job_id: int | None = None,
    to_local: ToLocal | None = None,
    probe: ProbeFn | None = None,
    day: date | None = None,
    dry_run: bool = False,
) -> ApplyOutcome:
    """Merge one group (see the module doc). A group already ``applied`` only has its
    failed file moves tried again."""
    local = to_local or paths.to_local
    probe_fn = probe or verify.probe
    gid = str(group_id)
    g = await db.fetchrow(
        "SELECT id, library_id, tier, status, keeper_video_id FROM duplicate_groups WHERE id = $1",
        gid,
    )
    if g is None:
        return ApplyOutcome("noop", gid, reason="the group is gone")
    status = str(g["status"])
    if status == "applied":
        return await _retry_moves(db, gid, local, dry_run=dry_run)
    if status == "dismissed":
        return ApplyOutcome("noop", gid, reason="the group was dismissed")
    uncovered = await uncovered_foreign_keys(db)
    if uncovered:
        raise MergeRefused(uncovered)

    rows = await db.fetch(
        "SELECT m.video_id AS video_id, m.path AS path, m.is_copy AS is_copy,"
        " m.score AS score, v.path AS own, v.state AS state, v.library_id AS library_id,"
        " v.content_hash AS content_hash, v.size_bytes AS size_bytes"
        " FROM duplicate_members m JOIN videos v ON v.id = m.video_id"
        " WHERE m.group_id = $1 ORDER BY m.video_id, m.is_copy, m.path",
        gid,
    )
    members: list[_M] = []
    videos: dict[str, Any] = {}
    for r in rows:
        vid = str(r["video_id"])
        if str(r["state"]) in ("superseded", "missing"):
            continue
        if not bool(r["is_copy"]) and str(r["path"]) != str(r["own"]):
            continue  # the row moved since the scan
        score = loads(r["score"]) or {}
        members.append(
            _M(
                vid,
                str(r["path"]),
                bool(r["is_copy"]),
                score.get("full_hash") if isinstance(score, dict) else None,
                int(score.get("size") or r["size_bytes"] or 0)
                if isinstance(score, dict)
                else int(r["size_bytes"] or 0),
            )
        )
        videos[vid] = r
    if len(members) < 2 or len(rows) != len(members):
        reason = "the members changed since the scan: find duplicates again"
        if not dry_run:
            await _fail_group(db, gid, reason)
        return ApplyOutcome("failed", gid, reason=reason)

    vids = sorted(videos)
    keeper = keeper_video_id or (
        None if g["keeper_video_id"] is None else str(g["keeper_video_id"])
    )
    if keeper not in videos:
        facts = await load_facts(db, vids)
        keeper = sorted(facts.values(), key=keeper_order)[0].video_id
    assert keeper is not None
    k = videos[keeper]
    keeper_path = str(k["own"])
    losers = [v for v in vids if v != keeper]
    to_trash = [m for m in members if not (m.video_id == keeper and not m.is_copy)]
    out = ApplyOutcome("applied", gid, keeper, losers)
    if dry_run:
        out.status = "dry_run"
        out.plan = [m.path for m in to_trash]
        return out

    # the last check: the keeper's file is readable; an exact group is still identical
    if await probe_fn(local(keeper_path)) is None:
        reason = f"the keeper's file cannot be read: {keeper_path}"
        await _fail_group(db, gid, reason)
        raise KeeperUnreadable(reason)
    if str(g["tier"]) == "exact":
        expect = next((m.full_hash for m in members if m.path == keeper_path), None)
        for m in [m for m in members if m.path == keeper_path] + to_trash:
            try:
                got = await _full(local(m.path))
            except (OSError, ValueError) as exc:
                got = f"unreadable: {exc}"
            if got != expect:
                reason = f"{m.path} is no longer byte-identical to the keeper"
                await _fail_group(db, gid, reason)
                if m.path == keeper_path:
                    raise KeeperUnreadable(reason)
                return ApplyOutcome("failed", gid, keeper, reason=reason)

    # one transaction: the user data, the losers, the group
    prior = {v: str(videos[v]["state"]) for v in losers}
    async with db.transaction():
        for v in losers:
            moved = await merge_rows(db, keeper, v, keeper_library=str(k["library_id"]))
            for t, n in moved.items():
                out.moved[t] = out.moved.get(t, 0) + n
        await db.execute(
            "UPDATE duplicate_groups SET status = 'applied', keeper_video_id = $2,"
            " decided_by = COALESCE($3, decided_by), decided_at = $4 WHERE id = $1",
            gid,
            keeper,
            decided_by,
            to_ts(db, now()),
        )

    # the files, one ledger row each
    for m in to_trash:
        r = videos[m.video_id]
        lib = str(r["library_id"])
        try:
            trash = await _trash_target(db, lib, m.path, "dedup", local, day or today())
        except (TrashError, OSError) as exc:
            a = await create_action(
                db,
                library_id=lib,
                kind="dedup",
                video_id=m.video_id,
                job_id=job_id,
                state="failed",
                old_path=m.path,
                old_hash=str(r["content_hash"]),
                old_size=m.size,
                verify=_verify(gid, keeper, m, prior),
            )
            await update_fields(db, a.id, error=f"not moved to the trash: {exc}")
            out.failed_moves.append((m.path, str(exc)))
            continue
        async with db.transaction():
            a = await create_action(
                db,
                library_id=lib,
                kind="dedup",
                video_id=m.video_id,
                job_id=job_id,
                state="swapped",
                old_path=m.path,
                old_hash=str(r["content_hash"]),
                old_size=m.size,
                trash_path=trash,
                verify=_verify(gid, keeper, m, prior),
                purge_after=await purge_after(db, lib),
            )
            if m.is_copy:
                await db.execute(
                    "DELETE FROM video_copies WHERE video_id = $1 AND path = $2",
                    m.video_id,
                    m.path,
                )
        await _move_file(db, a, local, out)
    return out


def _verify(gid: str, keeper: str, m: _M, prior: Mapping[str, str]) -> dict[str, Any]:
    v: dict[str, Any] = {"group": gid, "keeper": keeper}
    if m.video_id in prior:
        v["prior_state"] = prior[m.video_id]
    if m.is_copy:
        v["copy_of"] = m.video_id
    return v


async def _move_file(db: Any, a: FileAction, local: ToLocal, out: ApplyOutcome) -> None:
    assert a.old_path is not None and a.trash_path is not None
    src, dest = local(a.old_path), local(a.trash_path)
    try:
        if os.path.lexists(src):
            move_to_trash(src, dest)
        elif not os.path.lexists(dest):
            raise TrashError(f"{a.old_path} is gone and not in the trash")
    except (TrashError, OSError) as exc:
        await advance(db, a.id, "failed", error=f"not moved to the trash: {exc}")
        out.failed_moves.append((a.old_path, str(exc)))
        return
    await advance(db, a.id, "done", error=None)
    out.trashed.append(a.old_path)


async def _retry_moves(db: Any, gid: str, local: ToLocal, *, dry_run: bool) -> ApplyOutcome:
    """An applied group: the file moves that failed are tried again."""
    out = ApplyOutcome("noop", gid, reason="applied already")
    failed = [
        a
        for a in await list_actions(db, kinds=["dedup"], states=["failed"], limit=10_000)
        if (a.verify or {}).get("group") == gid
    ]
    if not failed:
        return out
    out.status = "applied"
    for a in failed:
        if dry_run:
            out.plan.append(str(a.old_path))
            continue
        if a.trash_path is None:
            assert a.old_path is not None
            try:
                trash = await _trash_target(db, a.library_id, a.old_path, "dedup", local, today())
            except (TrashError, OSError) as exc:
                out.failed_moves.append((a.old_path, str(exc)))
                continue
            await update_fields(
                db, a.id, trash_path=trash, purge_after=await purge_after(db, a.library_id)
            )
            got = await get_action(db, a.id)
            assert got is not None
            a = got
        await _move_file(db, a, local, out)
    return out
