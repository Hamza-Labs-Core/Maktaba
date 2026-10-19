"""The stored-path migration: dry run, apply, idempotence, revert (story 31.3).

Here with the cluster tests so the Windows job runs it too (database tests open the
event loop's sockets, so they are not in the unit tier).

Runs on the migrated SQLite always and on Postgres when ``MAKTABA_TEST_PG_URL`` is set
(the scratch server: ``postgresql://postgres@127.0.0.1:55432/postgres``).
"""

from __future__ import annotations

import unicodedata
from typing import Any

import pytest

from maktaba_pipeline.engine.sql import dumps, is_pg, loads
from maktaba_pipeline.sharepath import migrate as mig
from maktaba_pipeline.sharepath.mounts import Mount
from maktaba_pipeline.sharepath.resolver import StorageServer, build_resolver
from maktaba_pipeline.sharepath.servers import load_servers, save_server
from tests.engine.factory import make_video

pytestmark = pytest.mark.asyncio

ROOT = "/Volumes/Originals/Backup/Youtube"
MAC_MOUNTS = [
    Mount("smb", "192.168.50.94", "Originals", "/Volumes/Originals/", "mount"),
]


async def _library(db: Any, roots: list[str]) -> str:
    import uuid

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


async def _seed(db: Any) -> dict[str, Any]:
    lib = await _library(db, [ROOT, "/Users/me/Movies"])
    nfd_name = unicodedata.normalize("NFD", "Café.mp4")
    v1 = await make_video(db, lib, path=f"{ROOT}/Chan/a.mp4")  # type: ignore[arg-type]
    v2 = await make_video(db, lib, path=f"{ROOT}/Chan/{nfd_name}")  # type: ignore[arg-type]
    v3 = await make_video(db, lib, path="/Users/me/Movies/local.mp4")  # type: ignore[arg-type]
    v4 = await make_video(db, lib, path="smb://home-nas/Originals/already.mp4")  # type: ignore[arg-type]
    await db.execute(
        "INSERT INTO subtitle_files (video_id, language, format, source, path)"
        " VALUES ($1, 'en', 'srt', 'generated', $2)",
        str(v1),
        f"{ROOT}/Chan/a.en.srt",
    )
    return {"lib": lib, "v1": str(v1), "v2": str(v2), "v3": str(v3), "v4": str(v4)}


async def _path(db: Any, vid: str) -> str:
    row = await db.fetchrow("SELECT path FROM videos WHERE id = $1", vid)
    return str(row["path"])


async def _roots(db: Any, lib: str) -> list[str]:
    row = await db.fetchrow("SELECT roots FROM libraries WHERE id = $1", lib)
    r = row["roots"]
    return list(loads(r) if isinstance(r, str) else r)


async def test_servers_round_trip(db: Any) -> None:
    await save_server(db, StorageServer(name="Home-NAS", addresses=("192.168.50.94",)))
    await save_server(db, StorageServer(name="home-nas", addresses=("home-nas.local",)))
    [s] = await load_servers(db)
    assert s.name == "home-nas"
    assert s.addresses == ("192.168.50.94", "home-nas.local")
    with pytest.raises(ValueError):
        await save_server(db, StorageServer(name="x", char_map="0x22"))


async def test_dry_run_apply_idempotent_and_revert(db: Any) -> None:
    ids = await _seed(db)
    [server] = [await save_server(db, StorageServer(name="home-nas", addresses=("192.168.50.94",)))]
    resolver = build_resolver(servers=[server], mounts=MAC_MOUNTS, system="Darwin", probe=False)

    p = await mig.plan(db, resolver, verify=0, machine="mac-home")
    by = {(c.table, c.column): c for c in p.columns}
    videos = by[("videos", "path")]
    assert (videos.rows, videos.converted, videos.already, videos.unmatched) == (4, 2, 1, 1)
    assert videos.unmatched_samples == ["/Users/me/Movies/local.mp4"]
    assert by[("libraries", "roots")].converted == 1
    assert by[("library_roots", "path")].converted == 1
    assert by[("library_roots", "path_canonical")].converted == 1
    assert by[("subtitle_files", "path")].converted == 1
    # the dry run wrote nothing
    assert await _path(db, ids["v1"]) == f"{ROOT}/Chan/a.mp4"

    batch = await mig.apply(db, p)
    assert batch is not None
    assert await _path(db, ids["v1"]) == "smb://home-nas/Originals/Backup/Youtube/Chan/a.mp4"
    # stored NFC
    assert await _path(db, ids["v2"]) == "smb://home-nas/Originals/Backup/Youtube/Chan/Café.mp4"
    assert await _path(db, ids["v3"]) == "/Users/me/Movies/local.mp4"
    assert await _roots(db, ids["lib"]) == [
        "smb://home-nas/Originals/Backup/Youtube",
        "/Users/me/Movies",
    ]
    row = await db.fetchrow("SELECT path FROM subtitle_files WHERE video_id = $1", ids["v1"])
    assert row["path"] == "smb://home-nas/Originals/Backup/Youtube/Chan/a.en.srt"

    # idempotent: a second run finds nothing to do
    again = await mig.plan(db, resolver, verify=0)
    assert again.total == 0
    assert await mig.apply(db, again) is None

    listed = await mig.batches(db)
    assert listed[0]["batch"] == batch and listed[0]["changes"] == p.total

    # reverse: every value back as it was (the NFD spelling too)
    got_batch, n = await mig.revert(db)
    assert got_batch == batch and n == p.total
    assert await _path(db, ids["v1"]) == f"{ROOT}/Chan/a.mp4"
    assert await _path(db, ids["v2"]) == f"{ROOT}/Chan/" + unicodedata.normalize("NFD", "Café.mp4")
    assert await _roots(db, ids["lib"]) == [ROOT, "/Users/me/Movies"]
    # and a revert is not done twice
    assert await mig.revert(db) == (None, 0)


async def test_revert_leaves_a_value_changed_since(db: Any) -> None:
    ids = await _seed(db)
    server = await save_server(db, StorageServer(name="home-nas", addresses=("192.168.50.94",)))
    resolver = build_resolver(servers=[server], mounts=MAC_MOUNTS, system="Darwin", probe=False)
    await mig.apply(db, await mig.plan(db, resolver, verify=0))
    await db.execute("UPDATE videos SET path = $2 WHERE id = $1", ids["v1"], "smb://home-nas/x.mp4")
    await mig.revert(db)
    assert await _path(db, ids["v1"]) == "smb://home-nas/x.mp4"


async def test_include_local_writes_file_urls(db: Any) -> None:
    ids = await _seed(db)
    server = await save_server(db, StorageServer(name="home-nas", addresses=("192.168.50.94",)))
    resolver = build_resolver(servers=[server], mounts=MAC_MOUNTS, system="Darwin", probe=False)
    await mig.apply(db, await mig.plan(db, resolver, include_local=True, verify=0))
    assert await _path(db, ids["v3"]) == "file:///Users/me/Movies/local.mp4"


async def test_verify_samples_resolve_through_the_resolver(db: Any) -> None:
    await _seed(db)
    server = await save_server(db, StorageServer(name="home-nas", addresses=("192.168.50.94",)))
    resolver = build_resolver(servers=[server], mounts=MAC_MOUNTS, system="Darwin", probe=False)
    p = await mig.plan(db, resolver, verify=5, exists=lambda local: local.endswith("a.mp4"))
    videos = next(c for c in p.columns if c.table == "videos")
    assert {v[1] for v in videos.verified} >= {"/Volumes/Originals/Backup/Youtube/Chan/a.mp4"}
    assert any(v[2] for v in videos.verified)


async def test_every_value_comes_out_nfc(db: Any) -> None:
    """A path on no share is left local but made NFC (the Mac stored names NFD)."""
    name = "اخسأ، فلن تعدو قدرك 🔥🔥🔥.mp4"
    nfd = unicodedata.normalize("NFD", name)
    assert nfd != name
    lib = await _library(db, ["/Users/me/Movies"])
    vid = await make_video(db, lib, path=f"/Users/me/Movies/{nfd}")  # type: ignore[arg-type]
    canon = await make_video(db, lib, path=f"smb://home-nas/Originals/{nfd}")  # type: ignore[arg-type]
    server = await save_server(db, StorageServer(name="home-nas", addresses=("192.168.50.94",)))
    resolver = build_resolver(servers=[server], mounts=MAC_MOUNTS, system="Darwin", probe=False)
    p = await mig.plan(db, resolver, verify=0)
    videos = next(c for c in p.columns if c.table == "videos")
    assert videos.nfc_only == 1
    await mig.apply(db, p)
    assert await _path(db, str(vid)) == f"/Users/me/Movies/{name}"
    assert await _path(db, str(canon)) == f"smb://home-nas/Originals/{name}"
    await mig.revert(db)
    assert await _path(db, str(vid)) == f"/Users/me/Movies/{nfd}"


async def test_cli_server_add_dry_run_apply_and_revert(
    db: Any, monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]
) -> None:
    import argparse

    from maktaba_pipeline.sharepath import cli, servers

    ids = await _seed(db)
    monkeypatch.setattr(cli, "discover", lambda *_a: MAC_MOUNTS)
    monkeypatch.setattr(servers, "discover", lambda *_a: MAC_MOUNTS)

    def args(what: str, **kw: Any) -> argparse.Namespace:
        base: dict[str, Any] = dict(
            what=what, name=None, address=[], char_map="none", export=[], from_mounts=False,
            apply=False, include_local=False, batch=None, json=False, database_url="x",
            path_map=None,
        )  # fmt: skip
        base.update(kw)
        return argparse.Namespace(**base)

    assert await cli._with_db(args("server-add", from_mounts=True, name="home-nas"), db) == 0
    [s] = await load_servers(db)
    assert (s.name, s.addresses) == ("home-nas", ("192.168.50.94",))
    assert await cli._with_db(args("canonicalize"), db) == 0
    out = capsys.readouterr().out
    assert "dry run: 6 values would change" in out
    assert "videos.path: 4 rows, 2 to convert" in out
    assert await _path(db, ids["v1"]) == f"{ROOT}/Chan/a.mp4"
    assert await cli._with_db(args("canonicalize", apply=True), db) == 0
    assert "applied: 6 values" in capsys.readouterr().out
    assert (await _path(db, ids["v1"])).startswith("smb://home-nas/Originals/")
    assert await cli._with_db(args("revert"), db) == 0
    assert await _path(db, ids["v1"]) == f"{ROOT}/Chan/a.mp4"
