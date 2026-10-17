"""The Plex extension: season collections with a compute / apply split."""

from __future__ import annotations

import asyncio
from dataclasses import dataclass
from typing import Any
from uuid import UUID

import pytest

from maktaba_pipeline.engine.changes import ChangeSet
from maktaba_pipeline.engine.graph import Subject
from maktaba_pipeline.engine.sql import loads, now, to_ts
from maktaba_pipeline.extensions.links import get_link, get_links
from maktaba_pipeline.extensions.plex import PlexExtension, health_probes
from maktaba_pipeline.extensions.plex import extension as default_extension
from maktaba_pipeline.extensions.plex.collection import (
    Desired,
    PlexRemote,
    RemoteCollection,
    compute_changes,
)
from maktaba_pipeline.integrations.plex import PlexClient
from maktaba_pipeline.runtime import Database
from tests.engine.factory import make_library, make_series_and_season, make_video
from tests.extensions.harness import (
    FakePlex,
    add_episode,
    all_event_text,
    error_of,
    events,
    run_stage,
    set_membership,
    setup_runtime,
    summary_of,
)

TOKEN = "SECRET-PLEX-TOKEN"
SETTINGS = {"plex": {"url": "http://plex.test", "token": TOKEN, "path_map": "Y:/=/volume1/share/"}}


def make_extension(fake: FakePlex) -> PlexExtension:
    return PlexExtension(client_factory=lambda s: PlexClient(s["url"], s["token"], transport=fake))


@dataclass
class World:
    library: UUID
    series: UUID
    season: UUID
    videos: list[UUID]
    fake: FakePlex


async def make_world(db: Any, n: int = 3, *, in_plex: bool = True) -> World:
    lib = await make_library(db, "Lib")
    series, season = await make_series_and_season(db, lib, name="Show")
    fake = FakePlex()
    videos: list[UUID] = []
    for i in range(1, n + 1):
        v = await make_video(db, lib, path=f"Y:/Show/e{i}.mp4", title=f"Episode {i}")
        await add_episode(db, series, v, episode=i)
        videos.append(v)
        if in_plex:
            fake.add_item(str(100 + i), f"Episode {i}", f"/volume1/share/Show/e{i}.mp4")
    return World(lib, series, season, videos, fake)


async def collection_job(db: Any, rt: Any, w: World, **kw: Any) -> Any:
    return await run_stage(db, rt, "plex_collection", "season", w.season, **kw)


# ------------------------------------------------------------------ pure compute


def test_compute_changes_is_pure_and_typed() -> None:
    desired = Desired("T", ("1", "2", "3"), "1")
    create = compute_changes(desired, PlexRemote())
    assert create.added == ("1", "2", "3") and create.meta["action"] == "create"
    same = compute_changes(desired, PlexRemote(RemoteCollection("c", "T", ("1", "2", "3"))))
    assert same.is_empty
    moved = compute_changes(desired, PlexRemote(RemoteCollection("c", "T", ("3", "1", "2"))))
    assert moved.reordered == ("1", "2", "3") and not moved.added and not moved.removed
    grew = compute_changes(desired, PlexRemote(RemoteCollection("c", "Old", ("1", "9"))))
    assert grew.added == ("2", "3") and grew.removed == ("9",)
    assert grew.changed[0]["key"] == "title"
    # JSON round trip: a change set is stored in job_events
    assert grew.to_json()["counts"]["added"] == 2
    foreign = compute_changes(desired, PlexRemote(None, foreign_title=True))
    assert foreign.is_empty and foreign.meta["action"] == "conflict"


# ------------------------------------------------------------------ the stage


@pytest.mark.asyncio
async def test_creates_collection_and_stores_the_applied_change_set(db: Database) -> None:
    w = await make_world(db)
    rt = await setup_runtime(db, [make_extension(w.fake)], SETTINGS)
    job = await collection_job(db, rt, w)
    assert job["state"] == "done" and job["outcome"] == "changed"

    ((rk, col),) = w.fake.collections.items()
    assert col["title"] == "Lib - Show"
    assert col["children"] == ["101", "102", "103"]

    # the compute / apply split: the diff was stored in a job_events row of kind 'applied'
    (applied,) = await events(db, job["id"], "applied")
    assert applied["changes"]["kind"] == "plex_collection"
    assert applied["changes"]["added"] == ["101", "102", "103"]
    assert applied["changes"]["counts"] == {"added": 3, "removed": 0, "changed": 0, "reordered": 0}
    assert summary_of(job)["changes"]["added"] == ["101", "102", "103"]

    link = await get_link(db, "plex_collection", "season", str(w.season))
    assert link is not None and link.external_id == rk and link.created_by_tool
    items = await get_links(db, "plex_item", "video", [str(v) for v in w.videos])
    assert [items[str(v)].external_id for v in w.videos] == ["101", "102", "103"]
    assert not any(i.created_by_tool for i in items.values())
    # the token went in a header, never into job history
    assert TOKEN not in await all_event_text(db)
    assert w.fake.token_seen == {TOKEN}


@pytest.mark.asyncio
async def test_second_run_with_identical_remote_state_writes_nothing(db: Database) -> None:
    w = await make_world(db)
    rt = await setup_runtime(db, [make_extension(w.fake)], SETTINGS)
    first = await collection_job(db, rt, w)
    writes = list(w.fake.writes)
    assert writes

    # the first run cached the rating keys, but the fingerprint covers membership and settings
    # only, so the very next trigger is already skipped without reaching the handler
    second = await collection_job(db, rt, w)
    assert second["outcome"] == "skipped_unchanged"
    assert w.fake.writes == writes
    third = await collection_job(db, rt, w)
    assert third["outcome"] == "skipped_unchanged"
    assert w.fake.writes == writes

    # forced run: the handler runs, computes an empty change set, writes nothing
    forced = await collection_job(db, rt, w, force=True)
    assert forced["outcome"] == "unchanged"
    assert forced["output_hash"] == first["output_hash"]
    assert w.fake.writes == writes
    assert await events(db, forced["id"], "applied") == []
    assert summary_of(forced)["status"] == "noop"


@pytest.mark.asyncio
async def test_reorder_deletes_before_recreating_and_reads_back(db: Database) -> None:
    w = await make_world(db)
    rt = await setup_runtime(db, [make_extension(w.fake)], SETTINGS)
    await collection_job(db, rt, w)
    old_key = next(iter(w.fake.collections))
    w.fake.calls.clear()

    # episode order changes in the database (detect would also bump the membership hash)
    await db.execute(
        "UPDATE series_episodes SET episode = 4 - episode WHERE series_id = $1", str(w.series)
    )
    await set_membership(db, w.season, "h1")
    job = await collection_job(db, rt, w)
    assert job["outcome"] == "changed"

    (applied,) = await events(db, job["id"], "applied")
    assert applied["changes"]["reordered"] == ["103", "102", "101"]
    (col,) = w.fake.collections.values()
    assert col["children"] == ["103", "102", "101"]
    assert old_key not in w.fake.collections
    methods = [m for m, _ in w.fake.writes]
    assert methods.index("DELETE") < methods.index("POST")  # delete before recreate
    link = await get_link(db, "plex_collection", "season", str(w.season))
    assert link is not None and link.external_id == next(iter(w.fake.collections))


@pytest.mark.asyncio
async def test_readback_mismatch_fails_the_job(db: Database) -> None:
    w = await make_world(db)
    w.fake.corrupt = True
    rt = await setup_runtime(db, [make_extension(w.fake)], SETTINGS)
    job = await collection_job(db, rt, w)
    assert job["state"] != "done"
    assert error_of(job)["kind"] == "readback_mismatch"
    # nothing was reported as applied
    assert await events(db, job["id"], "applied") == []
    # stale-key protection: the cached rating keys are dropped so the next try re-resolves them
    assert await get_links(db, "plex_item", "video", [str(v) for v in w.videos]) == {}


@pytest.mark.asyncio
async def test_a_collection_the_tool_did_not_create_is_never_touched(db: Database) -> None:
    w = await make_world(db)
    mine = w.fake.add_collection("Lib - Show", ["101"])  # the user's own, same title
    rt = await setup_runtime(db, [make_extension(w.fake)], SETTINGS)
    job = await collection_job(db, rt, w)
    assert job["outcome"] == "waiting_inputs", error_of(job)
    assert summary_of(job)["reason"] == "title_conflict"
    assert w.fake.writes == []
    assert w.fake.collections[mine]["children"] == ["101"]
    assert await get_link(db, "plex_collection", "season", str(w.season)) is None


@pytest.mark.asyncio
async def test_a_link_not_marked_created_by_tool_is_treated_as_foreign(db: Database) -> None:
    w = await make_world(db)
    mine = w.fake.add_collection("Lib - Show", ["101"])
    await db.execute(
        "INSERT INTO external_links (target, subject_kind, subject_id, external_id, title,"
        " content_hash, created_by_tool)"
        " VALUES ('plex_collection','season',$1,$2,'Lib - Show','x',$3)",
        str(w.season),
        mine,
        False,
    )
    rt = await setup_runtime(db, [make_extension(w.fake)], SETTINGS)
    job = await collection_job(db, rt, w)
    assert job["outcome"] == "waiting_inputs", error_of(job)
    assert w.fake.writes == []


@pytest.mark.asyncio
async def test_members_missing_from_plex_wait_for_inputs(db: Database) -> None:
    w = await make_world(db, in_plex=False)
    rt = await setup_runtime(db, [make_extension(w.fake)], SETTINGS)
    job = await collection_job(db, rt, w)
    assert job["outcome"] == "waiting_inputs", error_of(job)
    assert summary_of(job)["reason"] == "no_plex_items"
    assert w.fake.writes == []


@pytest.mark.asyncio
async def test_partial_membership_leaves_unmatched_episodes_out(db: Database) -> None:
    w = await make_world(db)
    del w.fake.items["102"]
    rt = await setup_runtime(db, [make_extension(w.fake)], SETTINGS)
    job = await collection_job(db, rt, w)
    assert job["outcome"] == "changed"
    (col,) = w.fake.collections.values()
    assert col["children"] == ["101", "103"]
    assert summary_of(job)["missing"] == 1


@pytest.mark.asyncio
async def test_retired_season_removes_only_our_collection(db: Database) -> None:
    w = await make_world(db)
    other = w.fake.add_collection("Someone else's", ["101"])
    rt = await setup_runtime(db, [make_extension(w.fake)], SETTINGS)
    await collection_job(db, rt, w)
    await db.execute(
        "UPDATE series_seasons SET retired_at = $2 WHERE id = $1",
        str(w.season),
        to_ts(db, now()),
    )
    job = await collection_job(db, rt, w)
    assert job["state"] == "done"
    assert w.fake.titles() == ["Someone else's"] and other in w.fake.collections
    assert await get_link(db, "plex_collection", "season", str(w.season)) is None
    (applied,) = await events(db, job["id"], "applied")
    assert applied["changes"]["meta"]["action"] == "delete"


@pytest.mark.asyncio
async def test_plex_down_is_a_retryable_failure(db: Database) -> None:
    w = await make_world(db)
    w.fake.down = True
    rt = await setup_runtime(db, [make_extension(w.fake)], SETTINGS)
    job = await collection_job(db, rt, w)
    assert error_of(job)["kind"] == "plex_unreachable"
    assert error_of(job)["retryable"] is True


@pytest.mark.asyncio
async def test_review_mode_parks_the_change_set(db: Database) -> None:
    w = await make_world(db)
    rt = await setup_runtime(db, [make_extension(w.fake)], SETTINGS)
    await db.execute("UPDATE job_stages SET write_mode = 'review' WHERE stage = 'plex_collection'")
    job = await collection_job(db, rt, w)
    assert job["outcome"] == "awaiting_review"
    assert w.fake.writes == []
    assert len(await events(db, job["id"], "review")) == 1


@pytest.mark.asyncio
async def test_apply_writes_exactly_the_reviewed_change_set(db: Database) -> None:
    w = await make_world(db)
    rt = await setup_runtime(db, [make_extension(w.fake)], SETTINGS)
    await db.execute("UPDATE job_stages SET write_mode = 'review' WHERE stage = 'plex_collection'")
    parked = await collection_job(db, rt, w)
    digests = (loads(parked["output_summary"]) or {})["review_digests"]
    assert parked["outcome"] == "awaiting_review" and len(digests) == 1

    # an approval for another plan (it changed since the review) is parked again
    stale = await collection_job(
        db, rt, w, force=True, payload={"apply": True, "plan_digests": ["not-this-plan"]}
    )
    assert stale["outcome"] == "awaiting_review" and w.fake.writes == []

    # the operator's Apply: the reviewed digest is approved, the stage stays in review mode
    applied = await collection_job(
        db, rt, w, force=True, payload={"apply": True, "plan_digests": digests}
    )
    assert applied["state"] == "done" and applied["outcome"] != "awaiting_review"
    assert w.fake.writes != []
    assert len(await events(db, applied["id"], "applied")) == 1


# ------------------------------------------------------------------ fingerprints and health


@pytest.mark.asyncio
async def test_dry_fingerprint_is_stable_database_only_and_sensitive(db: Database) -> None:
    w = await make_world(db)
    ext = make_extension(w.fake)
    await setup_runtime(db, [ext], SETTINGS)
    fp = ext.impls()["plex_collection"].input_fingerprint
    assert fp is not None
    subject = Subject("season", w.season)
    a = await fp(db, subject)
    assert a is not None and a == await fp(db, subject)
    assert w.fake.calls == []  # never touches the network

    await collection_job(db, await setup_runtime(db, [ext], SETTINGS), w)
    cached = await fp(db, subject)
    assert cached == a  # the rating-key cache is not an input: a first run settles in one pass
    assert cached == await fp(db, subject)

    await set_membership(db, w.season, "h2")
    assert await fp(db, subject) != cached
    await db.execute("UPDATE job_stages SET write_mode = 'review' WHERE stage = 'plex_collection'")
    assert await fp(db, subject) != cached


@pytest.mark.asyncio
async def test_health_probe_reports_ms_or_minus_one() -> None:
    fake = FakePlex()
    ext = make_extension(fake)
    probes = ext.health_probes(SETTINGS["plex"])
    assert set(probes) == {"net.plex_ms"}
    assert await probes["net.plex_ms"]() >= 0
    fake.down = True
    assert await probes["net.plex_ms"]() == -1
    assert ext.health_probes({"url": "http://x"}) == {}  # not configured: no probes
    assert health_probes({}) == {}


def test_manifest_and_configuration() -> None:
    m = default_extension.manifest
    assert m.name == "plex" and m.secret_keys() == {"token"}
    assert {f.key: f.env for f in m.settings} == {
        "url": "MAKTABA_PLEX_URL",
        "token": "MAKTABA_PLEX_TOKEN",
        "path_map": "MAKTABA_PATH_MAP",
    }
    assert default_extension.is_configured({"url": "u", "token": "t"})
    assert not default_extension.is_configured({"url": "u"})
    assert not default_extension.is_configured({"token": "t"})
    assert isinstance(ChangeSet("k").to_json(), dict)
    assert asyncio.iscoroutinefunction(default_extension.impls()["plex_collection"].run)
