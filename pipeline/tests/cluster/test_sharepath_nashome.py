"""A NAS home's own record and the canonical conversion of its folders (Epic 59, story
59.13): the record made from its names and the platform's rule, setup's names, an
admin's edit kept, and the automatic conversion turning ``/volume1/Movies/...`` (or a
named catch-all folder) into ``smb://<nas>/<share>/...``.

Runs on the migrated SQLite always and on Postgres when ``MAKTABA_TEST_PG_URL`` is set.
"""

from __future__ import annotations

import json
import sys
import uuid
from pathlib import Path
from typing import Any

import pytest
import pytest_asyncio

from maktaba_pipeline.engine.sql import dumps, is_pg
from maktaba_pipeline.sharepath import auto
from maktaba_pipeline.sharepath.autoreg import Lookups
from maktaba_pipeline.sharepath.nashome import SetupShares, ensure_home_server
from maktaba_pipeline.sharepath.resolver import StorageServer
from maktaba_pipeline.sharepath.servers import load_servers, save_server
from tests.engine.factory import make_video

pytestmark = pytest.mark.asyncio

NAMES = ["diskstation", "diskstation.local", "192.168.1.20"]


@pytest_asyncio.fixture(autouse=True)
async def _no_map_wait(db: Any) -> None:
    await db.execute(
        "INSERT INTO app_settings (key, value) VALUES ('rules.storage', $1)"
        " ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value",
        dumps({"map_settle_minutes": 0}),
    )


async def _library(db: Any, roots: list[str]) -> str:
    lib = str(uuid.uuid4())
    await db.execute(
        "INSERT INTO libraries (id, name, roots) VALUES ($1, $2, $3)",
        lib,
        f"lib-{lib[:6]}",
        roots if is_pg(db) else dumps(roots),
    )
    for root in roots:
        await db.execute(
            "INSERT INTO library_roots (id, library_id, path, path_canonical) VALUES ($1,$2,$3,$3)",
            str(uuid.uuid4()),
            lib,
            root,
        )
    return lib


async def test_record_made_from_the_names_and_the_rule(db: Any) -> None:
    home = await ensure_home_server(
        db,
        ["/volume1/Movies/en", "/volume1/Movies/ar", "/mnt/elsewhere"],
        platform="dsm",
        names=NAMES,
    )
    assert home is not None
    assert home.server.name == "diskstation"
    assert set(home.server.addresses) >= {"diskstation.local", "192.168.1.20"}
    assert dict(home.server.exports) == {"/volume1/Movies": "Movies"}
    assert home.unnamed == ("/mnt/elsewhere",)
    # again: nothing new, the same record
    again = await ensure_home_server(db, ["/volume1/Movies/en"], platform="dsm", names=NAMES)
    assert again is not None and dict(again.server.exports) == {"/volume1/Movies": "Movies"}
    assert [s.name for s in await load_servers(db)] == ["diskstation"]


async def test_an_admins_share_name_stands(db: Any) -> None:
    await save_server(
        db,
        StorageServer(
            name="diskstation",
            addresses=("192.168.1.20",),
            exports=(("/volume1/Movies", "Films"),),
        ),
    )
    home = await ensure_home_server(db, ["/volume1/Movies"], platform="dsm", names=NAMES)
    assert home is not None
    assert dict(home.server.exports) == {"/volume1/Movies": "Films"}
    assert "diskstation.local" in home.server.addresses  # the names are added


async def test_setup_names_seed_the_record(db: Any) -> None:
    setup = SetupShares(
        server="truenas",
        aliases=("truenas.local", "10.0.0.5"),
        roots=(("/mnt/tank/media", "media"), ("/mnt/tank/private", "")),
    )
    home = await ensure_home_server(
        db, ["/mnt/tank/media", "/mnt/tank/private"], platform="truenas", names=["box"], setup=setup
    )
    assert home is not None
    assert home.server.name == "truenas"
    assert dict(home.server.exports) == {"/mnt/tank/media": "media", "/mnt/tank/private": ""}
    assert home.unnamed == ()


async def test_not_a_nas_home(db: Any) -> None:
    assert (
        await ensure_home_server(db, ["/Users/me/Movies"], platform="darwin", names=NAMES) is None
    )
    assert await load_servers(db) == []


def _env(**kw: Any) -> auto.AutoEnv:
    async def no_sleep(_s: float) -> None:
        return None

    return auto.AutoEnv(
        system="Linux",
        machine="nas",
        mounts=[],
        lookups=Lookups(
            resolve=lambda h: frozenset(), reverse=lambda ip: [], netbios=lambda ip: None
        ),
        sleep=no_sleep,
        poll_sec=0,
        **kw,
    )


async def _paths(db: Any) -> list[str]:
    return sorted(str(r["path"]) for r in await db.fetch("SELECT path FROM videos"))


async def test_dsm_home_converts_its_folders_to_the_share(db: Any) -> None:
    lib = await _library(db, ["/volume1/Movies"])
    for name in ("a.mkv", "أفلام/b.mkv"):
        await make_video(db, lib, path=f"/volume1/Movies/{name}")  # type: ignore[arg-type]
    state = await auto.run_once(
        db,
        _env(platform="dsm", home_names=NAMES, exists=lambda p: True, isdir=lambda p: True),
    )
    assert state["phase"] == "done", state
    assert await _paths(db) == [
        "smb://diskstation/Movies/a.mkv",
        "smb://diskstation/Movies/أفلام/b.mkv",
    ]


@pytest.mark.skipif(sys.platform == "win32", reason="a NAS home is Linux: its folders are /paths")
async def test_catch_all_home_converts_with_setup_names(
    db: Any, tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """The old compose file's /media catch-all: setup named the folder's share."""
    folder = tmp_path / "media" / "Movies"
    folder.mkdir(parents=True)
    (folder / "a.mkv").write_bytes(b"x")
    root = folder.as_posix()
    monkeypatch.setenv(
        "MAKTABA_HOME_SHARES",
        json.dumps({"server": "nas", "aliases": ["nas.local"], "roots": {root: "Movies"}}),
    )
    lib = await _library(db, [root])
    await make_video(db, lib, path=f"{root}/a.mkv")  # type: ignore[arg-type]
    state = await auto.run_once(db, _env(platform="docker", home_names=["box"]))
    assert state["phase"] == "done", state
    assert await _paths(db) == ["smb://nas/Movies/a.mkv"]


async def test_no_platform_no_change(db: Any, tmp_path: Path) -> None:
    folder = tmp_path / "Movies"
    folder.mkdir()
    lib = await _library(db, [folder.as_posix()])
    await make_video(db, lib, path=f"{folder.as_posix()}/a.mkv")  # type: ignore[arg-type]
    await auto.run_once(db, _env(platform=""))
    assert await _paths(db) == [f"{folder.as_posix()}/a.mkv"]
    assert await load_servers(db) == []
