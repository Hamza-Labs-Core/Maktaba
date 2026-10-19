"""Where the cluster files live (design-agent.md section 4)."""

from __future__ import annotations

from pathlib import Path
from typing import Any

import pytest

from maktaba_pipeline.cluster import share
from maktaba_pipeline.engine.paths import PathMap

pytestmark = pytest.mark.unit


def test_common_root() -> None:
    assert share.common_root(["Y:/Videos/Kids", "Y:/Videos/Docs/x"]) == "Y:/Videos"
    assert share.common_root(["/volume1/media/a", "/volume1/media/b"]) == "/volume1/media"
    assert share.common_root(["y:/videos/a", "Y:/Videos/b"]) == "y:/videos"
    assert share.common_root(["//nas/share/a", "//nas/share/b"]) == "//nas/share"
    assert share.common_root(["/volume1/a", "/volume2/b"]) is None  # only "/"
    assert share.common_root(["Y:/a", "O:/b"]) is None
    assert share.common_root(["Y:/a", "Y:/b"]) is None  # only the drive
    assert share.common_root(["//nas/a", "//nas/b"]) is None  # a UNC host is no share
    assert share.common_root(["Y:\\Videos\\Kids"]) == "Y:/Videos/Kids"
    assert share.common_root([]) is None


def test_share_root_rules() -> None:
    libs: list[tuple[list[str], int]] = [(["Y:/Media/Kids"], 10), (["Y:/Media/Docs"], 5)]
    assert share.share_root_from(libs, env={}) == "Y:/Media"
    # nothing in common but the filesystem root: the busiest library's root
    libs = [(["Y:/Kids"], 10), (["O:/Docs", "O:/More"], 500)]
    # its own roots share only the drive: its first root
    assert share.share_root_from(libs, env={}) == "O:/Docs"
    libs = [(["Y:/Kids"], 10), (["O:/Docs/A"], 500)]
    assert share.share_root_from(libs, env={}) == "O:/Docs/A"
    assert share.share_root_from(libs, env={"MAKTABA_SHARE_ROOT": "Z:\\Share\\"}) == "Z:/Share"
    assert share.share_root_from([], env={}) is None


def test_cluster_dir_through_the_path_map() -> None:
    pm = PathMap.parse("Y:/=/Volumes/Y")
    assert share.cluster_dir_for("Y:/Media", pm) == Path("/Volumes/Y/Media/maktaba/cluster")
    assert share.cluster_dir_for("/srv/media", PathMap()) == Path("/srv/media/maktaba/cluster")


class FakeDB:
    def __init__(self, libs: list[dict[str, Any]], counts: list[dict[str, Any]]) -> None:
        self.libs = libs
        self.counts = counts

    async def fetch(self, sql: str, *args: Any) -> list[dict[str, Any]]:
        return self.libs if "FROM libraries" in sql else self.counts


@pytest.mark.integration  # an event loop opens a socket pair
@pytest.mark.asyncio
async def test_resolve_cluster_dir() -> None:
    db = FakeDB(
        [{"id": "l1", "roots": '["Y:/Media/A"]'}, {"id": "l2", "roots": ["Y:/Media/B"]}],
        [{"library_id": "l1", "n": 3}],
    )
    pm = PathMap.parse("Y:/=/mnt/y")
    found, how = await share.resolve_cluster_dir(db, pm, env={})
    assert found == Path("/mnt/y/Media/maktaba/cluster") and "Y:/Media" in how
    found, how = await share.resolve_cluster_dir(db, pm, env={"MAKTABA_CLUSTER_DIR": "/tmp/c"})
    assert found == Path("/tmp/c") and how == "MAKTABA_CLUSTER_DIR"
    found, how = await share.resolve_cluster_dir(None, pm, env={})
    assert found is None
    found, how = await share.resolve_cluster_dir(FakeDB([], []), pm, env={})
    assert found is None and how == "no library roots"


def test_folder_name_and_key_path() -> None:
    assert share.folder_name("fe80::1%en0") == "fe80--1"
    assert share.folder_name("192.168.1.2") == "192.168.1.2"
    assert share.home_key_path({"MAKTABA_CLUSTER_KEY_PATH": "/k/key"}) == Path("/k/key")
    assert share.home_key_path({}).parts[-3:] == (".maktaba", "cluster", "home-agent.key")
