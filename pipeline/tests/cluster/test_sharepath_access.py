"""Canonical paths at the file-system boundary (story 31.3): the per-share storage
check, the scanner writing canonical paths through the resolver, a stage opening a
canonical path, and the home reporting its shares. Temp dirs stand in for the mounts.
"""

from __future__ import annotations

import asyncio
import json
from pathlib import Path
from typing import Any
from uuid import uuid4

import pytest

from maktaba_pipeline.cluster.node import WorkerAgent
from maktaba_pipeline.cluster.stages import StageSelector
from maktaba_pipeline.cluster.storage import LibrarySample, check_shares, infer_path_map
from maktaba_pipeline.engine.context import StageContext
from maktaba_pipeline.engine.paths import active_path_map, set_active_path_map, to_db, to_local
from maktaba_pipeline.engine.sql import loads
from maktaba_pipeline.scanner import LibraryRecord, Scanner
from maktaba_pipeline.sharepath.mounts import Mount
from maktaba_pipeline.sharepath.resolver import StorageServer, build_resolver
from maktaba_pipeline.sharepath.servers import save_server
from tests.scanner.test_scanner import FakeScanStore, _LoggerSink

NAS = StorageServer(name="nas", addresses=("192.168.50.94",))


def _mounted(tmp_path: Path) -> tuple[Path, Any]:
    share = tmp_path / "Originals"
    (share / "Backup" / "Youtube" / "Chan").mkdir(parents=True)
    resolver = build_resolver(
        servers=[NAS],
        mounts=[Mount("smb", "192.168.50.94", "Originals", str(share), "mount")],
        system="Linux",
    )
    return share, resolver


@pytest.fixture
def _restore_map() -> Any:
    before = active_path_map()
    yield
    set_active_path_map(before)


def test_check_shares_reports_each_share(tmp_path: Path) -> None:
    share, resolver = _mounted(tmp_path)
    samples = [
        LibrarySample(
            "lib1",
            ("smb://nas/Originals/Backup/Youtube", "smb://nas/Music"),
            (("smb://nas/Originals/Backup/Youtube/Chan/a.mp4", 1),),
        )
    ]
    rows = {r["share"]: r for r in check_shares(samples, resolver, "abcd1234")}
    o = rows["smb://nas/Originals"]
    assert (o["read"], o["write"], o["source"]) == ("ok", "ok", "mount")
    assert o["local"].rstrip("/") == str(share).replace("\\", "/")
    assert not list(share.rglob(".maktaba-*.tmp"))  # the probe is gone
    music = rows["smb://nas/Music"]
    assert (music["local"], music["read"], music["detail"]) == (None, "error", "not mounted here")


def test_inference_keeps_server_and_share_of_a_canonical_sample(tmp_path: Path) -> None:
    root = tmp_path / "O"
    f = root / "Backup" / "a.mp4"
    f.parent.mkdir(parents=True)
    f.write_bytes(b"xx")
    samples = [LibrarySample("l", (), (("smb://nas/Originals/Backup/a.mp4", 2),))]
    got = infer_path_map(samples, [str(root)])
    assert got == {"smb://nas/Originals/": str(root).replace("\\", "/") + "/"}


@pytest.mark.asyncio
async def test_scanner_writes_canonical_paths(tmp_path: Path, _restore_map: Any) -> None:
    share, resolver = _mounted(tmp_path)
    (share / "Backup" / "Youtube" / "Chan" / "a.mp4").write_bytes(b"video-a")
    set_active_path_map(resolver)
    store = FakeScanStore()
    lib = LibraryRecord(
        id=uuid4(),
        name="yt",
        roots=("smb://nas/Originals/Backup/Youtube",),
        disabled=False,
        follow_symlinks=False,
    )
    store.libraries[lib.id] = lib
    res = await Scanner(store, log=_LoggerSink()).run(lib.id)
    assert res.files_inserted == 1 and res.errors == []
    [v] = store.videos.values()
    assert v.path == "smb://nas/Originals/Backup/Youtube/Chan/a.mp4"
    # a second scan sees the same canonical path: unchanged
    res2 = await Scanner(store, log=_LoggerSink()).run(lib.id)
    assert res2.files_unchanged == 1


def test_stage_context_opens_a_canonical_path(tmp_path: Path, _restore_map: Any) -> None:
    share, resolver = _mounted(tmp_path)
    f = share / "Backup" / "Youtube" / "Chan" / "a.mp4"
    f.write_bytes(b"x")
    ctx = StageContext(db=None, job=None, stage=None, path_map=resolver)  # type: ignore[arg-type]
    local = ctx.local_path("smb://nas/Originals/Backup/Youtube/Chan/a.mp4")
    assert Path(local).read_bytes() == b"x"
    set_active_path_map(resolver)
    assert Path(to_local("smb://nas/Originals/Backup/Youtube/Chan/a.mp4")) == f
    # a file a stage writes beside the media goes back as a canonical path
    assert to_db(str(f.with_suffix(".en.srt"))) == (
        "smb://nas/Originals/Backup/Youtube/Chan/a.en.srt"
    )
    # nothing maps a cache path: it stays as it is
    assert to_db("/home/me/.maktaba/cache/x.jpg") == "/home/me/.maktaba/cache/x.jpg"


@pytest.mark.asyncio
async def test_home_reports_its_shares(db: Any, tmp_path: Path, _restore_map: Any) -> None:
    share = tmp_path / "Originals"
    (share / "lib").mkdir(parents=True)
    await save_server(db, StorageServer(name="nas", addresses=("192.168.50.94",)))
    await db.execute(
        "INSERT INTO instance_identity (id, instance_id, name) VALUES (1, $1, 'home')"
        " ON CONFLICT (id) DO NOTHING",
        str(uuid4()),
    )
    lib = str(uuid4())
    roots = ["smb://nas/Originals/lib"]
    await db.execute(
        "INSERT INTO libraries (id, name, roots) VALUES ($1, 'l', $2)",
        lib,
        roots if db.dialect == "postgres" else json.dumps(roots),
    )
    agent = WorkerAgent(
        db=db,
        worker_id="w1",
        selector=StageSelector(
            graph=[], requested=None, excluded=frozenset(), handlers=frozenset()
        ),
        graph=[],
        shutdown=asyncio.Event(),
        mounts=[Mount("smb", "192.168.50.94", "Originals", str(share), "mount")],
    )
    await agent.start_home()
    assert to_local("smb://nas/Originals/lib").rstrip("/") == str(share / "lib").replace("\\", "/")
    row = await db.fetchrow("SELECT storage FROM instance_identity WHERE id = 1")
    storage = loads(row["storage"]) if isinstance(row["storage"], str) else row["storage"]
    [s] = storage["shares"]
    assert (s["share"], s["read"], s["write"]) == ("smb://nas/Originals", "ok", "ok")
    assert storage["path_map"]["smb://nas/Originals/"].rstrip("/") == str(share).replace("\\", "/")


def test_share_root_of_canonical_roots() -> None:
    from maktaba_pipeline.cluster.share import common_dir

    got = common_dir(
        ["smb://home-nas/Originals/Backup/Youtube", "smb://home-nas/Originals/Backup/Talks"]
    )
    assert got == ("smb://home-nas/Originals/Backup", True)
