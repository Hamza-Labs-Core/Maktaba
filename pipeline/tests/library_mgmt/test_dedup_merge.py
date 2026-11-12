"""Story 47.7: ``dedup_apply`` merges a group without loss (real clips, real SQLite;
Postgres with MAKTABA_TEST_PG_URL).

The merge fixture has watch state, playlist items, favourites, collections, tags,
transcripts, chapters and embeddings on both members: the keeper ends up with the union;
the loser is superseded and its file is in the trash; Restore revives it; the purge
deletes it. The foreign-key guard refuses a table it has no rule for.
"""

from __future__ import annotations

import shutil
import uuid
from datetime import UTC, datetime, timedelta
from pathlib import Path
from typing import Any
from unittest.mock import MagicMock

import pytest
from maktaba_fp_dedup.stages import dedup_apply, dedup_scan

from maktaba_pipeline.engine.context import StageContext, StageFailure
from maktaba_pipeline.engine.sql import is_pg, to_ts
from maktaba_pipeline.library_mgmt.dedup_merge import (
    RULES,
    MergeRefused,
    apply_group,
    foreign_keys_to_videos,
    merge_rows,
    uncovered_foreign_keys,
)
from maktaba_pipeline.library_mgmt.ledger import list_actions
from maktaba_pipeline.library_mgmt.swap import restore
from maktaba_pipeline.library_mgmt.trash import purge_due
from maktaba_pipeline.runtime import Database
from tests.library_mgmt.dedup_world import lecture, mark_converted, probe_into_db, scan, video_at
from tests.library_mgmt.epic47_world import World, make_world, same
from tests.library_mgmt.media_fixtures import mp4_faststart, needs_ffmpeg

T0 = datetime(2026, 10, 1, 12, 0, tzinfo=UTC)
U1 = "00000000-0000-4000-8000-0000000000a1"
U2 = "00000000-0000-4000-8000-0000000000a2"


# ------------------------------------------------------------------ the guard


@pytest.mark.asyncio
async def test_every_foreign_key_to_videos_has_a_rule(db: Database) -> None:
    """Fails CI when a migration adds a table pointing at ``videos`` that the merge does
    not know: give it a rule in ``dedup_merge.RULES``."""
    fks = await foreign_keys_to_videos(db)
    assert ("playback_state", "video_id") in fks and ("videos", "superseded_by") in fks
    assert await uncovered_foreign_keys(db) == []
    # and no rule is left over from a table that went (Postgres has them all; SQLite
    # has no segment_embeddings, a pgvector table)
    if is_pg(db):
        assert set(RULES) <= set(fks), sorted(set(RULES) - set(fks))


@pytest.mark.asyncio
async def test_an_uncovered_foreign_key_refuses_the_merge(db: Database, tmp_path: Path) -> None:
    w = await make_world(db, tmp_path)
    k, kp = await w.video("k.mkv", b"K" * 5000)
    lo, lp = await w.video("l.mkv", b"L" * 5000)
    gid = await _group(db, w, "same_video", [(k, kp), (lo, lp)], k)
    vid_type = "UUID" if is_pg(db) else "TEXT"
    await db.execute(
        f"CREATE TABLE lecture_notes (id INTEGER PRIMARY KEY, video_id {vid_type}"
        " REFERENCES videos(id) ON DELETE CASCADE)"
    )
    assert await uncovered_foreign_keys(db) == [("lecture_notes", "video_id")]
    with pytest.raises(MergeRefused, match="lecture_notes.video_id"):
        await apply_group(db, gid, to_local=same)
    with pytest.raises(StageFailure) as err:
        await dedup_apply.run(db, _job(gid, {"apply": True, "group_id": gid}), _ctx())
    assert err.value.kind == "fk_not_covered" and not err.value.retryable
    assert await _status(db, gid) == "open"
    assert lp.exists() and await _state(db, lo) == "ready"


# ------------------------------------------------------------------ helpers


async def _group(
    db: Database,
    w: World,
    tier: str,
    members: list[tuple[Any, Path]],
    keeper: Any,
    copies: list[tuple[Any, Path]] = (),  # type: ignore[assignment]
) -> str:
    gid = str(uuid.uuid4())
    await db.execute(
        "INSERT INTO duplicate_groups (id, library_id, tier, keeper_video_id, members_key)"
        " VALUES ($1, $2, $3, $4, $5)",
        gid,
        str(w.library_id),
        tier,
        str(keeper),
        gid,
    )
    for vid, p in members:
        await db.execute(
            "INSERT INTO duplicate_members (group_id, video_id, path, is_copy, score)"
            " VALUES ($1, $2, $3, $4, $5)",
            gid,
            str(vid),
            str(p),
            False,
            f'{{"size": {p.stat().st_size}}}',
        )
    for vid, p in copies:
        await db.execute(
            "INSERT INTO duplicate_members (group_id, video_id, path, is_copy, score)"
            " VALUES ($1, $2, $3, $4, $5)",
            gid,
            str(vid),
            str(p),
            True,
            f'{{"size": {p.stat().st_size}}}',
        )
    return gid


def _job(gid: str, payload: dict[str, Any]) -> Any:
    job = MagicMock()
    job.id = 7
    job.subject.id = uuid.UUID(gid)
    job.payload = payload
    return job


def _check(vid: str) -> Any:
    """One item's duplicate check (a video's dedup_scan job)."""
    job = MagicMock()
    job.id = 1
    job.subject.id = uuid.UUID(vid)
    job.subject.kind = "video"
    job.payload = {}
    return job


def _ctx() -> Any:
    ctx = MagicMock(spec=StageContext)
    ctx.local_path.side_effect = same
    ctx.review_digests = []

    async def nothing(*a: Any, **k: Any) -> None:
        return None

    ctx.log.side_effect = nothing
    ctx.check_cancel.side_effect = nothing
    ctx.progress.side_effect = nothing
    return ctx


async def _status(db: Database, gid: str) -> str:
    return str(await db.fetchval("SELECT status FROM duplicate_groups WHERE id = $1", gid))


async def _state(db: Database, vid: Any) -> str:
    return str(await db.fetchval("SELECT state FROM videos WHERE id = $1", str(vid)))


async def _n(db: Database, sql: str, *args: Any) -> int:
    return int(await db.fetchval(sql, *args) or 0)


async def _rows(db: Database, sql: str, *args: Any) -> set[tuple[Any, ...]]:
    return {tuple(str(x) for x in r) for r in await db.fetch(sql, *args)}


async def _user_data(db: Database, w: World, k: str, lo: str) -> None:
    """Watch state, playlists, favourites, collections, tags, history, recommendations,
    transcripts, chapters, embeddings and a YouTube link on both members."""

    def ts(d: datetime) -> Any:
        return to_ts(db, d)

    lib = str(w.library_id)
    q = db.execute
    for u, name in ((U1, "u1"), (U2, "u2")):
        await q("INSERT INTO users (id, username, pw_hash) VALUES ($1, $2, 'x')", u, name)
    # playback: u1 on both (the loser's is newer and completed), u2 on the loser only
    await q(
        "INSERT INTO playback_state (user_id, video_id, position_sec, completed, updated_at)"
        " VALUES ($1, $2, 10, false, $3), ($1, $4, 50, true, $5), ($6, $4, 5, false, $3)",
        U1, k, ts(T0), lo, ts(T0 + timedelta(hours=1)), U2,
    )  # fmt: skip
    await q("INSERT INTO user_favorites (user_id, video_id) VALUES ($1, $2), ($1, $3), ($4, $3)",
            U1, k, lo, U2)  # fmt: skip
    p1, p2 = str(uuid.uuid4()), str(uuid.uuid4())
    await q("INSERT INTO user_playlists (id, user_id, name) VALUES ($1, $2, 'P1'), ($3, $2, 'P2')",
            p1, U1, p2)  # fmt: skip
    await q(
        "INSERT INTO user_playlist_items (playlist_id, video_id, position)"
        " VALUES ($1, $2, 1), ($1, $3, 2), ($4, $3, 7)",
        p1, k, lo, p2,
    )  # fmt: skip
    c1, c2 = str(uuid.uuid4()), str(uuid.uuid4())
    await q("INSERT INTO collections (id, library_id, name) VALUES ($1, $2, 'C1'), ($3, $2, 'C2')",
            c1, lib, c2)  # fmt: skip
    await q(
        "INSERT INTO collection_items (collection_id, video_id, position)"
        " VALUES ($1, $2, 0), ($1, $3, 9), ($4, $3, 3)",
        c1, k, lo, c2,
    )  # fmt: skip
    t1, t2 = str(uuid.uuid4()), str(uuid.uuid4())
    await q(
        "INSERT INTO tags (id, name, name_norm)"
        " VALUES ($1, 'Fiqh', 'fiqh'), ($2, 'Seerah', 'seerah')",
        t1,
        t2,
    )
    await q("INSERT INTO video_tags (video_id, tag_id) VALUES ($1, $2), ($3, $2), ($3, $4)",
            k, t1, lo, t2)  # fmt: skip
    await q("INSERT INTO watch_sessions (id, user_id, video_id) VALUES ($1, $2, $3)",
            str(uuid.uuid4()), U1, lo)  # fmt: skip
    await q("INSERT INTO user_recs (user_id, video_id, score) VALUES ($1, $2, 0.5), ($3, $4, 0.9)",
            U1, k, U2, lo)  # fmt: skip
    # derived: the loser alone has a transcript (on its audio track 0), chapters and a
    # YouTube link; both have an embedding
    aid = await db.fetchval(
        "SELECT id FROM audio_tracks WHERE video_id = $1 AND track_index = 0", lo
    )
    tr = str(uuid.uuid4())
    await q(
        "INSERT INTO transcripts (id, video_id, audio_track_id, language, backend, model)"
        " VALUES ($1, $2, $3, 'ar', 'whisper', 'large-v3')",
        tr, lo, aid,
    )  # fmt: skip
    await q(
        "INSERT INTO transcript_units (transcript_id, video_id, unit_index, start_sec, end_sec,"
        " text) VALUES ($1, $2, 0, 0, 2, 'bismillah')",
        tr, lo,
    )  # fmt: skip
    await q("INSERT INTO chapters (video_id, seq, start_sec, title) VALUES ($1, 0, 0, 'Intro')", lo)
    for v in (k, lo):
        await q(
            "INSERT INTO video_embeddings (video_id, library_id, transcript_id, model, dim,"
            " segments, vec) VALUES ($1, $2, $3, $4, 2, 1, $5)",
            v, lib, str(uuid.uuid4()), f"m-{v[:4]}", b"\x00\x01",
        )  # fmt: skip
    await q(
        "INSERT INTO media_metadata_enrichment (id, video_id, provider, external_id)"
        " VALUES ($1, $2, 'youtube', 'abc123')",
        str(uuid.uuid4()), lo,
    )  # fmt: skip


async def _pair(db: Database, tmp_path: Path) -> tuple[World, str, Path, str, Path, str]:
    """A keeper MP4 and a loser MKV of the same lecture, with user data on both, in a
    same_video group."""
    w = await make_world(db, tmp_path)
    (w.root / "k").mkdir()
    kp = mp4_faststart(w.root / "k" / "Lecture.mp4")
    lp = lecture(w.root / "l" / "Lecture.mkv")
    await scan(db, w)
    k, lo = await video_at(db, kp), await video_at(db, lp)
    await probe_into_db(db, k, kp, frames=False)
    await probe_into_db(db, lo, lp, frames=False)
    await _user_data(db, w, k, lo)
    gid = await _group(db, w, "same_video", [(k, kp), (lo, lp)], k)
    return w, k, kp, lo, lp, gid


# ------------------------------------------------------------------ the merge


@needs_ffmpeg
@pytest.mark.asyncio
async def test_the_keeper_gets_the_union(db: Database, tmp_path: Path) -> None:
    w, k, kp, lo, lp, gid = await _pair(db, tmp_path)

    # a same_video group is always reviewed first
    ctx = _ctx()
    parked = await dedup_apply.run(db, _job(gid, {}), ctx)
    assert parked.outcome == "awaiting_review" and len(ctx.review_digests) == 1
    assert await _status(db, gid) == "open" and lp.exists()

    # the queue's Apply approves the digest the review parked
    res = await dedup_apply.run(
        db, _job(gid, {"apply": True, "plan_digests": ctx.review_digests}), _ctx()
    )
    assert res.outcome == "changed", res.summary
    assert res.summary["keeper_video_id"] == k and res.summary["losers"] == [lo]

    # playback: u1 has the loser's newer position and completed; u2's moved
    pb = await _rows(db, "SELECT user_id, position_sec, completed FROM playback_state"
                         " WHERE video_id = $1", k)  # fmt: skip
    pos = {u: (float(p), c in ("True", "1")) for u, p, c in pb}
    assert pos == {U1: (50.0, True), U2: (5.0, False)}
    assert await _n(db, "SELECT COUNT(*) FROM playback_state WHERE video_id = $1", lo) == 0
    # favourites: the union
    assert await _rows(db, "SELECT user_id FROM user_favorites WHERE video_id = $1", k) == {
        (U1,),
        (U2,),
    }
    # playlists: P1 had the keeper already (the loser's entry is dropped); P2 keeps 7
    items = await _rows(
        db,
        "SELECT p.name, i.position FROM user_playlist_items i"
        " JOIN user_playlists p ON p.id = i.playlist_id WHERE i.video_id = $1",
        k,
    )
    assert items == {("P1", "1"), ("P2", "7")}
    # collections and tags: the union, the keeper's position on a clash
    coll = await _rows(
        db,
        "SELECT c.name, i.position FROM collection_items i"
        " JOIN collections c ON c.id = i.collection_id WHERE i.video_id = $1",
        k,
    )
    assert coll == {("C1", "0"), ("C2", "3")}
    assert await _n(db, "SELECT COUNT(*) FROM video_tags WHERE video_id = $1", k) == 2
    # history moved, recommendations of the loser dropped
    assert await _n(db, "SELECT COUNT(*) FROM watch_sessions WHERE video_id = $1", k) == 1
    assert await _n(db, "SELECT COUNT(*) FROM user_recs WHERE video_id = $1", lo) == 0
    assert await _n(db, "SELECT COUNT(*) FROM user_recs WHERE video_id = $1", k) == 1
    # derived: what the keeper lacked moved (on the keeper's audio track 0)
    t = await db.fetchrow(
        "SELECT t.id, a.video_id AS track_of FROM transcripts t"
        " JOIN audio_tracks a ON a.id = t.audio_track_id WHERE t.video_id = $1",
        k,
    )
    assert t is not None and str(t["track_of"]) == k
    assert await _n(db, "SELECT COUNT(*) FROM transcript_units WHERE video_id = $1", k) == 1
    assert await _n(db, "SELECT COUNT(*) FROM chapters WHERE video_id = $1", k) == 1
    assert (
        await _n(db, "SELECT COUNT(*) FROM media_metadata_enrichment WHERE video_id = $1", k) == 1
    )
    # what the keeper had stays its own; the loser's is left to cascade at purge
    emb = await db.fetchval("SELECT model FROM video_embeddings WHERE video_id = $1", k)
    assert emb == f"m-{k[:4]}"
    assert await _n(db, "SELECT COUNT(*) FROM video_embeddings WHERE video_id = $1", lo) == 1

    # the loser: superseded, its file in the trash, a done ledger row
    row = await db.fetchrow("SELECT state, superseded_by FROM videos WHERE id = $1", lo)
    assert str(row["state"]) == "superseded" and str(row["superseded_by"]) == k
    assert not lp.exists() and kp.exists()
    trashed = w.trash_files()
    assert len(trashed) == 1 and "/.maktaba/trash/dedup/" in str(trashed[0])
    (a,) = await list_actions(db, kinds=["dedup"])
    assert a.state == "done" and a.video_id is not None and str(a.video_id) == lo
    assert a.old_path == str(lp) and (a.verify or {})["prior_state"] == "discovered"
    assert await _status(db, gid) == "applied"

    # again: nothing to do
    again = await dedup_apply.run(db, _job(gid, {"apply": True, "group_id": gid}), _ctx())
    assert again.outcome == "unchanged"

    # Restore revives the loser; the user data stays with the keeper
    await restore(db, a.id, to_local=same)
    assert lp.exists()
    row = await db.fetchrow("SELECT state, superseded_by FROM videos WHERE id = $1", lo)
    assert str(row["state"]) == "discovered" and row["superseded_by"] is None
    await w.assert_tracked(uuid.UUID(lo))
    assert await _n(db, "SELECT COUNT(*) FROM user_favorites WHERE video_id = $1", k) == 2


@needs_ffmpeg
@pytest.mark.asyncio
async def test_the_purge_deletes_the_superseded_row(db: Database, tmp_path: Path) -> None:
    """After the grace period the loser's file and row go; the moved transcript (on the
    keeper's audio track) does not stop the delete, and stays."""
    w, k, _, lo, lp, gid = await _pair(db, tmp_path)
    await db.execute(
        "UPDATE libraries SET settings = $2 WHERE id = $1",
        str(w.library_id),
        '{"trash": {"days": 0}}',
    )
    out = await apply_group(db, gid, to_local=same)
    assert out.status == "applied"
    rep = await purge_due(
        db, w.library_id, at=datetime.now(UTC) + timedelta(seconds=5), to_local=same
    )
    assert [str(v) for v in rep.rows_deleted] == [lo]
    assert await db.fetchval("SELECT id FROM videos WHERE id = $1", lo) is None
    assert await _n(db, "SELECT COUNT(*) FROM transcripts WHERE video_id = $1", k) == 1
    assert w.trash_files() == [] and not lp.exists()


@needs_ffmpeg
@pytest.mark.asyncio
async def test_an_unreadable_keeper_fails_the_group(db: Database, tmp_path: Path) -> None:
    w, k, kp, lo, lp, gid = await _pair(db, tmp_path)
    kp.write_bytes(b"not a video any more" * 100)
    with pytest.raises(StageFailure) as err:
        await dedup_apply.run(db, _job(gid, {"apply": True, "group_id": gid}), _ctx())
    assert err.value.kind == "keeper_unreadable" and not err.value.retryable
    assert await _status(db, gid) == "failed"
    assert await _state(db, lo) == "discovered" and lp.exists()
    assert await _n(db, "SELECT COUNT(*) FROM user_favorites WHERE video_id = $1", lo) == 2
    assert await list_actions(db, kinds=["dedup"]) == []


@needs_ffmpeg
@pytest.mark.asyncio
async def test_the_admin_chooses_another_keeper(db: Database, tmp_path: Path) -> None:
    w, k, kp, lo, lp, gid = await _pair(db, tmp_path)
    res = await dedup_apply.run(
        db,
        _job(gid, {"apply": True, "group_id": gid, "keeper_video_id": lo, "decided_by": "admin"}),
        _ctx(),
    )
    assert res.summary["keeper_video_id"] == lo
    assert await _state(db, k) == "superseded" and not kp.exists() and lp.exists()
    g = await db.fetchrow(
        "SELECT keeper_video_id, decided_by FROM duplicate_groups WHERE id = $1", gid
    )
    assert str(g["keeper_video_id"]) == lo and g["decided_by"] == "admin"
    # the dry run of an applied group plans nothing
    dry = await apply_group(db, gid, to_local=same, dry_run=True)
    assert dry.status == "noop"


# ------------------------------------------------------------------ exact copies


@needs_ffmpeg
@pytest.mark.asyncio
async def test_exact_copies_merge_at_once_and_restore(db: Database, tmp_path: Path) -> None:
    w = await make_world(db, tmp_path)
    a = lecture(w.root / "a" / "Lecture.mkv")
    b = w.root / "b" / "Lecture.mkv"
    b.parent.mkdir()
    shutil.copyfile(a, b)
    await scan(db, w)
    vid = str(await db.fetchval("SELECT id FROM videos WHERE library_id = $1", str(w.library_id)))
    own = Path(str(await db.fetchval("SELECT path FROM videos WHERE id = $1", vid)))
    copy = b if own == a else a
    await mark_converted(db, vid)
    found = await dedup_scan.run(db, _check(vid), _ctx())
    (req,) = found.requests
    gid = str(req.subject_id)

    # dry run: the plan, nothing moved
    dry = await dedup_apply.run(db, _job(gid, {"dry_run": True}), _ctx())
    assert dry.summary["status"] == "dry_run" and dry.summary["plan"] == [str(copy)]
    assert copy.exists() and await _status(db, gid) == "open"

    # auto_exact: no review
    res = await dedup_apply.run(db, _job(gid, dict(req.payload or {})), _ctx())
    assert res.outcome == "changed" and res.summary["losers"] == []
    assert own.exists() and not copy.exists()
    assert await _n(db, "SELECT COUNT(*) FROM video_copies") == 0
    assert await _state(db, vid) == "discovered"  # the row stays: only a path went
    g = await db.fetchrow("SELECT status, decided_by FROM duplicate_groups WHERE id = $1", gid)
    assert (g["status"], g["decided_by"]) == ("applied", "auto_exact")
    (act,) = await list_actions(db, kinds=["dedup"])
    assert act.state == "done" and (act.verify or {})["copy_of"] == vid

    await restore(db, act.id, to_local=same)
    assert copy.exists()
    assert await _rows(db, "SELECT video_id, path FROM video_copies") == {(vid, str(copy))}


@needs_ffmpeg
@pytest.mark.asyncio
async def test_an_exact_group_that_changed_is_not_merged(db: Database, tmp_path: Path) -> None:
    w = await make_world(db, tmp_path)
    a = lecture(w.root / "a" / "Lecture.mkv")
    b = w.root / "b" / "Lecture.mkv"
    b.parent.mkdir()
    shutil.copyfile(a, b)
    await scan(db, w)
    vid = str(await db.fetchval("SELECT id FROM videos WHERE library_id = $1", str(w.library_id)))
    await mark_converted(db, vid)
    (req,) = (await dedup_scan.run(db, _check(vid), _ctx())).requests
    own = Path(str(await db.fetchval("SELECT path FROM videos WHERE id = $1", vid)))
    copy = b if own == a else a
    # the middle of the copy changes (same size, head and tail: the partial hash holds)
    data = bytearray(copy.read_bytes())
    mid = len(data) // 2
    data[mid] ^= 0xFF
    copy.write_bytes(bytes(data))
    res = await dedup_apply.run(db, _job(str(req.subject_id), {"auto": True}), _ctx())
    assert res.summary["status"] == "failed" and "byte-identical" in res.summary["reason"]
    assert copy.exists() and own.exists()
    assert await _status(db, str(req.subject_id)) == "failed"


# ------------------------------------------------------------------ parental controls


async def _child(db: Database, user: str, name: str) -> None:
    await db.execute("INSERT INTO users (id, username, pw_hash) VALUES ($1, $2, 'x')", user, name)
    await db.execute("INSERT INTO parental_policies (user_id, max_tier) VALUES ($1, 9)", user)


async def _rule(
    db: Database, user: str, effect: str, video: str, expires: datetime | None = None
) -> None:
    await db.execute(
        "INSERT INTO parental_rules (id, user_id, effect, subject_kind, subject_id, expires_at)"
        " VALUES ($1, $2, $3, 'video', $4, $5)",
        str(uuid.uuid4()), user, effect, video, None if expires is None else to_ts(db, expires),
    )  # fmt: skip


@pytest.mark.asyncio
async def test_a_merge_carries_what_restricts_the_loser_to_the_keeper(
    db: Database, tmp_path: Path
) -> None:
    """Epic 57 review, finding 2: an admin set 18 on copy A and blocked it for a child;
    the merge of A into B left B with no admin tier and no block, so the child could
    open it."""
    w = await make_world(db, tmp_path)
    k, _kp = await w.video("k.mkv", b"K" * 5000)
    lo, _lp = await w.video("l.mkv", b"L" * 5000)
    k, lo = str(k), str(lo)
    await _child(db, U1, "child-one")
    await _child(db, U2, "child-two")
    soon, late = T0 + timedelta(days=1), T0 + timedelta(days=30)
    # the admin's tier is on the loser; the keeper has a milder one
    await db.execute("UPDATE videos SET age_tier_admin = 18 WHERE id = $1", lo)
    await db.execute("UPDATE videos SET age_tier_admin = 9 WHERE id = $1", k)
    # u1: blocked on the loser for good, and on the keeper until tomorrow; allowed on
    # the loser until tomorrow, and on the keeper for a month
    await _rule(db, U1, "block", lo)
    await _rule(db, U1, "block", k, soon)
    await _rule(db, U1, "allow", lo, soon)
    await _rule(db, U1, "allow", k, late)
    # u2: blocked on the loser only
    await _rule(db, U2, "block", lo, late)
    # requests: u1 asked for both copies, u2 for the loser; one old answer on the loser
    for child, video, status in ((U1, lo, "pending"), (U1, k, "pending"),
                                 (U2, lo, "pending"), (U2, lo, "denied")):  # fmt: skip
        await db.execute(
            "INSERT INTO parental_requests (id, child_id, subject_kind, subject_id, status)"
            " VALUES ($1, $2, 'video', $3, $4)",
            str(uuid.uuid4()), child, video, status,
        )  # fmt: skip
    await db.execute(
        "INSERT INTO parental_events (id, child_id, kind, subject_kind, subject_id)"
        " VALUES ($1, $2, 'blocked', 'video', $3)",
        str(uuid.uuid4()), U2, lo,
    )  # fmt: skip
    # the screen found the loser to be 16; the keeper was never screened
    await db.execute(
        "INSERT INTO video_content_signals (video_id, tier, confidence, model, input_hash)"
        " VALUES ($1, 16, 0.9, 'm', 'h')",
        lo,
    )
    versions = dict(await _rows(db, "SELECT user_id, version FROM parental_policies"))
    if is_pg(db):
        await db.execute("DELETE FROM parental_recompute_jobs")

    async with db.transaction():
        moved = await merge_rows(db, k, lo, keeper_library=str(w.library_id))

    # the stricter admin tier is the keeper's
    assert await db.fetchval("SELECT age_tier_admin FROM videos WHERE id = $1", k) == 18
    # no rule names the loser any more
    assert await _n(db, "SELECT COUNT(*) FROM parental_rules WHERE subject_id = $1", lo) == 0
    rules = {
        (str(r["user_id"]), str(r["effect"])): r["expires_at"]
        for r in await db.fetch(
            "SELECT user_id, effect, expires_at FROM parental_rules WHERE subject_id = $1", k
        )
    }
    assert set(rules) == {(U1, "block"), (U1, "allow"), (U2, "block")}
    # two blocks: the longer one (for good); two allows: the shorter one
    assert rules[(U1, "block")] is None
    assert rules[(U1, "allow")] is not None and rules[(U2, "block")] is not None
    assert str(rules[(U1, "allow")])[:10] == soon.date().isoformat()
    assert str(rules[(U2, "block")])[:10] == late.date().isoformat()
    # pending requests follow, one a child; the old answer is history and stays
    asked = await _rows(
        db, "SELECT child_id, subject_id, status FROM parental_requests ORDER BY child_id"
    )
    assert asked == {(U1, k, "pending"), (U2, k, "pending"), (U2, lo, "denied")}
    # the guardian's activity names a video that will still exist
    assert await _rows(db, "SELECT subject_id FROM parental_events") == {(k,)}
    # the detected tier stands in on the keeper until it is screened itself
    sig = await db.fetchrow("SELECT tier, model FROM video_content_signals WHERE video_id = $1", k)
    assert sig is not None and sig["tier"] == 16
    assert moved["videos.age_tier_admin"] == 1 and moved["parental_rules"] == 3
    assert moved["parental_requests"] == 2 and moved["parental_events"] == 1
    assert moved["video_content_signals"] == 1
    # the children's policies moved on a version: a stale edit of theirs is refused
    after = dict(await _rows(db, "SELECT user_id, version FROM parental_policies"))
    assert all(int(after[u]) == int(versions[u]) + 1 for u in (U1, U2))
    # the keeper's tier is worked out again (the API's queue, on Postgres)
    if is_pg(db):
        jobs = await _rows(
            db, "SELECT kind, subject FROM parental_recompute_jobs WHERE subject = $1", k
        )
        assert ("video", k) in jobs


@pytest.mark.asyncio
async def test_a_merge_never_loosens_the_keeper(db: Database, tmp_path: Path) -> None:
    """The other way round: the keeper is the stricter one, and stays so."""
    w = await make_world(db, tmp_path)
    k, _kp = await w.video("k.mkv", b"K" * 5000)
    lo, _lp = await w.video("l.mkv", b"L" * 5000)
    other, _op = await w.video("o.mkv", b"O" * 5000)
    k, lo, other = str(k), str(lo), str(other)
    await _child(db, U1, "child-one")
    await db.execute("UPDATE videos SET age_tier_admin = 16 WHERE id = $1", k)
    await db.execute("UPDATE videos SET age_tier_admin = 0 WHERE id = $1", lo)
    await _rule(db, U1, "block", k)
    await _rule(db, U1, "block", other)  # another video's rule is not this merge's
    for video, tier, reviewed in ((k, 18, None), (lo, 9, None)):
        await db.execute(
            "INSERT INTO video_content_signals (video_id, tier, confidence, model, input_hash,"
            " reviewed_at) VALUES ($1, $2, 0.9, 'm', 'h', $3)",
            video, tier, reviewed,
        )  # fmt: skip
    async with db.transaction():
        moved = await merge_rows(db, k, lo, keeper_library=str(w.library_id))
    assert await db.fetchval("SELECT age_tier_admin FROM videos WHERE id = $1", k) == 16
    assert await db.fetchval("SELECT tier FROM video_content_signals WHERE video_id = $1", k) == 18
    assert await _rows(db, "SELECT subject_id FROM parental_rules") == {(k,), (other,)}
    assert not {"videos.age_tier_admin", "parental_rules", "video_content_signals"} & set(moved)

    # a keeper's signal an admin reviewed is the admin's word: a stricter loser's
    # detected tier does not replace it (the loser's admin tier, if any, still carries)
    lo2, _p = await w.video("l2.mkv", b"M" * 5000)
    lo2 = str(lo2)
    await db.execute(
        "UPDATE video_content_signals SET tier = 9, reviewed_at = $2 WHERE video_id = $1",
        k, to_ts(db, T0),
    )  # fmt: skip
    await db.execute(
        "INSERT INTO video_content_signals (video_id, tier, confidence, model, input_hash)"
        " VALUES ($1, 18, 0.9, 'm', 'h')",
        lo2,
    )
    async with db.transaction():
        await merge_rows(db, k, lo2, keeper_library=str(w.library_id))
    assert await db.fetchval("SELECT tier FROM video_content_signals WHERE video_id = $1", k) == 9
