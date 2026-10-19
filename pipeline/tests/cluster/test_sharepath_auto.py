"""The home converts its stored paths by itself (story 31.3, automatic): registration
from the mounts, the checks, the backup, the pause of the share stages, the apply in
chunks, the verify, the resume; a run cut short, an unmounted share, a revert, the
backup restore, the Plex path map.

Runs on the migrated SQLite always and on Postgres when ``MAKTABA_TEST_PG_URL`` is set
(the scratch server: ``postgresql://postgres@127.0.0.1:55432/postgres``).
"""

from __future__ import annotations

import asyncio
import unicodedata
import uuid
from pathlib import Path
from typing import Any

import pytest

from maktaba_pipeline.engine.sql import dumps, is_pg, loads, now, to_ts
from maktaba_pipeline.sharepath import auto
from maktaba_pipeline.sharepath import backup as bk
from maktaba_pipeline.sharepath import migrate as mig
from maktaba_pipeline.sharepath.autoreg import Lookups
from maktaba_pipeline.sharepath.mounts import Mount
from maktaba_pipeline.sharepath.servers import load_servers
from tests.engine.factory import make_video

pytestmark = pytest.mark.asyncio

NAS_IP = "192.168.50.94"


def _lookups() -> Lookups:
    def resolve(host: str) -> frozenset[str]:
        return (
            frozenset({NAS_IP})
            if host.lower() in {NAS_IP, "home-nas", "home-nas.lan"}
            else (frozenset())
        )

    return Lookups(resolve=resolve, reverse=lambda ip: ["Home-NAS.lan"], netbios=lambda ip: None)


class Home:
    """A Mac home whose NAS share is a temp folder."""

    def __init__(self, tmp: Path) -> None:
        self.mnt = tmp / "Volumes" / "Originals"
        self.root = self.mnt / "Backup" / "Youtube"
        (self.root / "Chan").mkdir(parents=True)
        self.local = self.mnt.as_posix() + "/"
        self.mounted = True
        self.changes = 0

    def path(self, rel: str) -> str:
        return f"{self.root.as_posix()}/{rel}"

    def file(self, rel: str) -> str:
        p = self.root / rel
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_bytes(b"x")
        return self.path(rel)

    def env(self, **kw: Any) -> auto.AutoEnv:
        mounts = [Mount("smb", NAS_IP, "Originals", self.local, "mount")] if self.mounted else []

        async def no_sleep(_s: float) -> None:
            return None

        def changed() -> None:
            self.changes += 1

        return auto.AutoEnv(
            system="Darwin",
            machine="mac-home",
            mounts=mounts,
            lookups=_lookups(),
            sleep=no_sleep,
            poll_sec=0,
            on_change=changed,
            **kw,
        )


async def _library(db: Any, roots: list[str]) -> str:
    lib = str(uuid.uuid4())
    await db.execute(
        "INSERT INTO libraries (id, name, roots) VALUES ($1, $2, $3)",
        lib,
        f"lib-{lib[:6]}",
        roots if is_pg(db) else dumps(roots),
    )
    await db.execute(
        "INSERT INTO library_roots (id, library_id, path, path_canonical) VALUES ($1,$2,$3,$3)",
        str(uuid.uuid4()),
        lib,
        roots[0],
    )
    return lib


async def _seed(db: Any, home: Home, n: int = 5) -> dict[str, Any]:
    lib = await _library(db, [home.root.as_posix()])
    vids = [
        str(await make_video(db, lib, path=home.file(f"Chan/v{i}.mp4")))  # type: ignore[arg-type]
        for i in range(n)
    ]
    nfd = unicodedata.normalize("NFD", "Café.mp4")
    home.file(unicodedata.normalize("NFC", "Chan/Café.mp4"))
    vids.append(str(await make_video(db, lib, path=home.path(f"Chan/{nfd}"))))  # type: ignore[arg-type]
    await db.execute(
        "INSERT INTO extensions (name, enabled, settings, updated_at) VALUES ($1, $2, $3, $4)"
        " ON CONFLICT (name) DO UPDATE SET settings = EXCLUDED.settings",
        "plex",
        True,
        dumps({"url": "http://plex:32400", "path_map": f"{home.local}=/volume1/Originals/"}),
        to_ts(db, now()),
    )
    return {"lib": lib, "vids": vids}


async def _paths(db: Any) -> dict[str, str]:
    rows = await db.fetch("SELECT id, path FROM videos")
    return {str(r["id"]): str(r["path"]) for r in rows}


async def _share_stage(db: Any) -> str:
    row = await db.fetchrow(
        "SELECT stage FROM job_stages WHERE pool = 'nas_read' ORDER BY stage LIMIT 1"
    )
    assert row is not None, "the migrations seed the share stages"
    return str(row["stage"])


async def _paused(db: Any, stage: str) -> tuple[bool, str | None]:
    row = await db.fetchrow("SELECT paused, paused_by FROM job_stages WHERE stage = $1", stage)
    return bool(row["paused"]), row["paused_by"]


async def _plex_map(db: Any) -> str:
    row = await db.fetchrow("SELECT settings FROM extensions WHERE name = 'plex'")
    return str(loads(row["settings"])["path_map"])


async def test_the_full_automatic_flow(db: Any, tmp_path: Path) -> None:
    home = Home(tmp_path)
    ids = await _seed(db, home)
    before = await _paths(db)
    stage = await _share_stage(db)
    # an admin's pause stays as it is
    await db.execute(
        "UPDATE job_stages SET paused = $2, paused_by = 'admin' WHERE stage = ("
        " SELECT stage FROM job_stages WHERE pool = 'nas_write' ORDER BY stage LIMIT 1)"
        " AND stage <> $1",
        stage,
        True,
    )
    seen: list[tuple[bool, str | None]] = []

    async def during(done: int, total: int) -> None:
        seen.append(await _paused(db, stage))

    assert await auto.due(db, home.env()) == "first"
    state = await auto.run_once(db, home.env(after_chunk=during))
    assert state["phase"] == "done", state.get("problems")

    # the NAS was registered from the mount: name from DNS, every address seen
    [server] = await load_servers(db)
    assert server.name == "home-nas"
    assert NAS_IP in server.addresses and "Home-NAS.lan" in server.addresses
    assert home.changes >= 1

    # converted, NFC, canonical
    after = await _paths(db)
    assert after[ids["vids"][0]] == "smb://home-nas/Originals/Backup/Youtube/Chan/v0.mp4"
    assert after[ids["vids"][-1]] == "smb://home-nas/Originals/Backup/Youtube/Chan/Café.mp4"
    roots = await db.fetchval("SELECT roots FROM libraries WHERE id = $1", ids["lib"])
    assert list(loads(roots) if isinstance(roots, str) else roots) == [
        "smb://home-nas/Originals/Backup/Youtube"
    ]

    # the share stages were paused while it wrote, and resumed after
    assert seen and all(s == (True, auto.MARK) for s in seen)
    assert await _paused(db, stage) == (False, None)
    admin_paused = await db.fetch("SELECT stage FROM job_stages WHERE paused_by = 'admin'")
    assert len(admin_paused) == 1

    # one batch, one backup, the plan and the last run reported
    last = state["last"]
    assert last["changes"] == state["plan"]["changes"] == 9
    assert state["run"] is None
    [b] = await bk.list_backups(db)
    assert b["batch"] == last["batch"] and b["values"] >= len(before)
    # the Plex map has the canonical prefix beside the old one
    assert await _plex_map(db) == (
        f"{home.local}=/volume1/Originals/;smb://home-nas/Originals/=/volume1/Originals/"
    )

    # idempotent: nothing changes, not due again until something does
    again = await auto.run_once(db, home.env())
    assert again["phase"] == "done"
    assert await _paths(db) == after
    assert await mig.batches(db) == await mig.batches(db)
    assert len(await mig.batches(db)) == 1
    assert len(await bk.list_backups(db)) == 1
    assert await _plex_map(db) == (
        f"{home.local}=/volume1/Originals/;smb://home-nas/Originals/=/volume1/Originals/"
    )
    assert await auto.due(db, home.env()) is None
    # a new legacy path (an old scanner) is a change: due again
    await make_video(db, ids["lib"], path=home.file("Chan/new.mp4"))
    assert await auto.due(db, home.env()) == "changed"


async def test_an_unmounted_share_waits_and_converts_once_mounted(db: Any, tmp_path: Path) -> None:
    home = Home(tmp_path)
    await _seed(db, home)
    before = await _paths(db)
    # the share is gone: the mount and its folder
    home.mounted = False
    real = home.mnt
    moved = tmp_path / "unmounted"
    real.rename(moved)
    state = await auto.run_once(db, home.env())
    assert state["phase"] == "blocked"
    assert [p["code"] for p in state["problems"]] == ["unmounted"]
    assert await _paths(db) == before
    assert await bk.list_backups(db) == []
    stage = await _share_stage(db)
    assert await _paused(db, stage) == (False, None)
    # not due again before the retry time, then due
    assert await auto.due(db, home.env()) is None
    await db.execute(
        "UPDATE storage_canonicalize SET state = $1 WHERE id = 1",
        dumps({**state, "checked_at": "2000-01-01T00:00:00+00:00"}),
    )
    assert await auto.due(db, home.env()) == "retry"
    # mounted again: converted
    moved.rename(real)
    home.mounted = True
    state = await auto.run_once(db, home.env())
    assert state["phase"] == "done"
    assert all(p.startswith("smb://") for p in (await _paths(db)).values())


async def test_missing_sampled_files_block(db: Any, tmp_path: Path) -> None:
    home = Home(tmp_path)
    ids = await _seed(db, home, n=3)
    before = await _paths(db)
    # the database names files the share does not have (a wrong mount)
    for vid in ids["vids"]:
        await db.execute(
            "UPDATE videos SET path = $2 WHERE id = $1",
            vid,
            home.path(f"Other/{vid}.mp4"),
        )
    state = await auto.run_once(db, home.env())
    assert state["phase"] == "blocked"
    assert {p["code"] for p in state["problems"]} == {"missing"}
    assert (await _paths(db)).keys() == before.keys()
    assert all(not p.startswith("smb://") for p in (await _paths(db)).values())


async def test_a_run_cut_short_resumes_with_the_same_batch(db: Any, tmp_path: Path) -> None:
    home = Home(tmp_path)
    await _seed(db, home, n=6)
    before = await _paths(db)
    stage = await _share_stage(db)
    await db.execute(
        "INSERT INTO app_settings (key, value) VALUES ('rules.storage', $1)"
        " ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value",
        dumps({"batch_size": 100}),
    )
    from maktaba_pipeline.sharepath import migrate

    real_apply = migrate.apply

    async def small_chunks(*a: Any, **kw: Any) -> Any:
        kw["chunk_size"] = 2
        return await real_apply(*a, **kw)

    setattr(migrate, "apply", small_chunks)  # noqa: B010
    try:

        async def stop(done: int, total: int) -> None:
            if done >= 2:
                raise asyncio.CancelledError  # the worker stops mid-way

        with pytest.raises(asyncio.CancelledError):
            await auto.run_once(db, home.env(after_chunk=stop))
        state, _ = await auto.load_state(db)
        assert state["phase"] == "applying"
        batch = state["run"]["batch"]
        assert state["run"]["done"] == 2
        assert await _paused(db, stage) == (True, auto.MARK)
        # two values written (whole chunks), the rest still local
        assert await auto._batch_size(db, batch) == 2  # noqa: SLF001
        assert not all(p.startswith("smb://") for p in (await _paths(db)).values())
        assert await auto.due(db, home.env()) == "resume"

        state = await auto.run_once(db, home.env())
    finally:
        setattr(migrate, "apply", real_apply)  # noqa: B010
    assert state["phase"] == "done"
    assert state["last"]["batch"] == batch
    assert len(await mig.batches(db)) == 1
    assert len(await bk.list_backups(db)) == 1
    assert await _paused(db, stage) == (False, None)
    assert all(p.startswith("smb://") for p in (await _paths(db)).values())
    # the one batch undoes all of it
    got, n = await mig.revert(db, batch)
    assert got == batch and n == state["last"]["changes"]
    assert await _paths(db) == before


async def test_revert_from_the_machines_page_holds_until_resumed(db: Any, tmp_path: Path) -> None:
    home = Home(tmp_path)
    await _seed(db, home)
    before = await _paths(db)
    assert (await auto.run_once(db, home.env()))["phase"] == "done"
    await auto.request(db, "revert", by="admin")
    assert await auto.due(db, home.env()) == "request:revert"
    state = await auto.run_once(db, home.env())
    assert state["phase"] == "held" and state["held"] is True
    assert state["reverted"]["rows"] == 9
    assert await _paths(db) == before
    # held: not due, and a run does nothing
    assert await auto.due(db, home.env()) is None
    assert (await auto.run_once(db, home.env()))["phase"] == "held"
    assert await _paths(db) == before
    # resumed: converted again
    await auto.request(db, "resume", by="admin")
    state = await auto.run_once(db, home.env())
    assert state["phase"] == "done" and not state["held"]
    assert all(p.startswith("smb://") for p in (await _paths(db)).values())


async def test_backup_restore_puts_every_value_back(db: Any, tmp_path: Path) -> None:
    home = Home(tmp_path)
    ids = await _seed(db, home)
    before = await _paths(db)
    roots_before = await db.fetchval("SELECT roots FROM libraries WHERE id = $1", ids["lib"])
    state = await auto.run_once(db, home.env())
    assert state["phase"] == "done"
    got, n = await bk.restore_backup(db)
    assert got == state["last"]["backup"]
    assert n >= len(before)
    assert await _paths(db) == before
    roots = await db.fetchval("SELECT roots FROM libraries WHERE id = $1", ids["lib"])
    assert roots == roots_before
    # the batch counts as reverted: a revert does not run it twice
    assert await mig.revert(db) == (None, 0)


async def test_backups_keep_the_last_ones(db: Any, tmp_path: Path) -> None:
    home = Home(tmp_path)
    await _seed(db, home, n=1)
    ids = [(await bk.take_backup(db, machine="m"))[0] for _ in range(5)]
    dropped = await bk.prune_backups(db, 3)
    assert len(dropped) == 2
    kept = {b["backup"] for b in await bk.list_backups(db)}
    assert len(kept) == 3 and kept <= set(ids)


async def test_switched_off_does_nothing(db: Any, tmp_path: Path) -> None:
    home = Home(tmp_path)
    await _seed(db, home)
    before = await _paths(db)
    await db.execute(
        "INSERT INTO app_settings (key, value) VALUES ('rules.storage', $1)"
        " ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value",
        dumps({"auto_canonicalize": False}),
    )
    assert await auto.due(db, home.env()) is None
    state = await auto.run_once(db, home.env())
    assert state["phase"] == "off"
    assert await _paths(db) == before
    assert await load_servers(db) == []


async def test_the_stage_runs_it_on_the_home_only(db: Any, tmp_path: Path) -> None:
    from maktaba_pipeline.engine.stages import storage_canonicalize as st

    home = Home(tmp_path)
    await _seed(db, home, n=1)
    st.set_env(None)
    res = await st.run(db, None, None)  # type: ignore[arg-type]
    assert res.summary == {"phase": "skipped", "why": "not the home's own machine"}
    st.set_env(home.env())
    try:
        res = await st.run(db, None, None)  # type: ignore[arg-type]
    finally:
        st.set_env(None)
    assert res.summary is not None and res.summary["phase"] == "done"
    assert await auto.enqueue(db, "test")


async def test_cli_status_backups_and_restore(
    db: Any, tmp_path: Path, capsys: pytest.CaptureFixture[str]
) -> None:
    import argparse

    from maktaba_pipeline.sharepath import cli

    home = Home(tmp_path)
    await _seed(db, home, n=2)
    before = await _paths(db)
    state = await auto.run_once(db, home.env())
    assert state["phase"] == "done"

    def args(what: str, **kw: Any) -> argparse.Namespace:
        base: dict[str, Any] = dict(
            what=what, name=None, address=[], char_map="none", export=[], from_mounts=False,
            apply=False, include_local=False, batch=None, backup=None, json=False,
            database_url="x", path_map=None,
        )  # fmt: skip
        base.update(kw)
        return argparse.Namespace(**base)

    assert await cli._with_db(args("status"), db) == 0
    out = capsys.readouterr().out
    assert "phase: done" in out and state["last"]["batch"] in out
    assert await cli._with_db(args("backups"), db) == 0
    assert state["last"]["backup"] in capsys.readouterr().out
    assert await cli._with_db(args("restore-backup"), db) == 0
    assert "values written back" in capsys.readouterr().out
    assert await _paths(db) == before
