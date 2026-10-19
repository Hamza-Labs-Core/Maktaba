"""The cluster folder goes through this machine's resolver, never a home path as-is.

Seen live: a Windows node (O: = the NAS's Originals share) started paused, before its
path map was built, and took the home's ``/Volumes/Originals/Backup/Youtube`` as a local
path, creating ``E:\\Volumes\\Originals\\...`` on the current drive."""

from __future__ import annotations

from pathlib import Path

import pytest

from maktaba_pipeline.cluster import share
from maktaba_pipeline.cluster.agent.files import RETRY_SHARE_SEC, ClusterFiles, make_dirs
from maktaba_pipeline.engine.paths import PathMap
from maktaba_pipeline.sharepath.mounts import Mount
from maktaba_pipeline.sharepath.resolver import StorageServer, build_resolver

NAS = StorageServer(name="home-nas", addresses=("192.168.50.94",), id="s1")
O_DRIVE = Mount("smb", "192.168.50.94", "Originals", "O:/", "drive")
LEGACY = "/Volumes/Originals/Backup/Youtube"
CANONICAL = "smb://home-nas/Originals/Backup/Youtube"
WANT = "O:/Backup/Youtube/maktaba/cluster"


def _on_o(p: str) -> bool:
    return p.replace("\\", "/").lower().startswith("o:/")


def _windows_node() -> PathMap:
    return build_resolver(
        servers=[NAS],
        mounts=[O_DRIVE],
        system="Windows",
        probe=False,
        legacy={"/Volumes/Originals/": "O:/"},
    )


@pytest.mark.unit
@pytest.mark.parametrize("root", [LEGACY, LEGACY + "/", CANONICAL, CANONICAL + "/"])
def test_a_windows_node_files_under_its_own_mount(root: str) -> None:
    found, how, anchor = share.locate_cluster_dir(
        root, _windows_node(), is_dir=_on_o, system="Windows"
    )
    assert found is not None and found.as_posix() == WANT
    assert anchor is not None and anchor.as_posix() == "O:/Backup/Youtube"
    assert how == f"share root {root}"


@pytest.mark.unit
def test_the_home_path_without_a_map_is_refused_on_windows() -> None:
    """The regression: no map yet -> the home's path, which Windows would put on E:."""
    found, how, anchor = share.locate_cluster_dir(
        LEGACY, PathMap(), is_dir=lambda _p: True, system="Windows"
    )
    assert found is None and anchor is None
    assert "another machine's path" in how and LEGACY in how


@pytest.mark.unit
def test_a_canonical_root_no_mount_resolves_is_refused() -> None:
    bare = build_resolver(servers=[NAS], mounts=[], system="Linux", probe=False)
    found, how, _ = share.locate_cluster_dir(
        CANONICAL, bare, is_dir=lambda _p: True, system="Linux"
    )
    assert found is None and "no mount of smb://home-nas/Originals" in how


@pytest.mark.unit
def test_a_share_not_mounted_now_is_refused() -> None:
    found, how, _ = share.locate_cluster_dir(
        LEGACY, _windows_node(), is_dir=lambda _p: False, system="Windows"
    )
    assert found is None and "is not mounted here" in how and "O:/Backup/Youtube" in how


@pytest.mark.unit
def test_a_windows_path_is_refused_on_the_mac() -> None:
    found, how, _ = share.locate_cluster_dir(
        "O:/Backup/Youtube", PathMap(), is_dir=lambda _p: True, system="Darwin"
    )
    assert found is None and "a Windows path" in how


@pytest.mark.integration  # an event loop opens a socket pair
@pytest.mark.asyncio
async def test_resolve_cluster_dir_refuses_an_unmapped_home_path() -> None:
    class DB:
        async def fetch(self, sql: str, *args: object) -> list[dict[str, object]]:
            return [{"library_id": "l1", "path": LEGACY + "/Chan", "videos": 3}]

    found, how = await share.resolve_cluster_dir(
        DB(), PathMap(), env={}, is_dir=lambda _p: True, system="Windows"
    )
    assert found is None and "does not resolve here" in how
    found, _ = await share.resolve_cluster_dir(
        DB(), _windows_node(), env={}, is_dir=_on_o, system="Windows"
    )
    assert found is not None and found.as_posix() == "O:/Backup/Youtube/Chan/maktaba/cluster"


@pytest.mark.unit
def test_make_dirs_never_creates_on_a_bare_root(tmp_path: Path) -> None:
    stray = Path(tmp_path.anchor) / "maktaba-no-such-share-7f3a" / "maktaba" / "cluster"
    with pytest.raises(FileNotFoundError, match="bare root"):
        make_dirs(stray)
    assert not stray.parent.parent.exists()
    with pytest.raises(FileNotFoundError, match="not there"):
        make_dirs(tmp_path / "gone" / "maktaba", anchor=tmp_path / "gone")
    assert not (tmp_path / "gone").exists()
    make_dirs(tmp_path / "share" / "maktaba" / "cluster")  # under an existing directory
    (tmp_path / "root").mkdir()
    make_dirs(tmp_path / "root" / "maktaba" / "cluster", anchor=tmp_path / "root")
    assert (tmp_path / "root" / "maktaba" / "cluster").is_dir()


@pytest.mark.unit
def test_the_files_reach_the_share_once_the_map_is_built(tmp_path: Path) -> None:
    """A node paused at start: local folder with the reason, the share at the next retry
    once its resolver maps the home's path."""
    mount = tmp_path / "O"
    (mount / "Backup" / "Youtube").mkdir(parents=True)
    active: list[PathMap] = [PathMap()]
    locator = share.ShareLocator(LEGACY, "share root", path_map=lambda: active[0], env={})
    t = [0.0]
    files = ClusterFiles(
        None,
        "192.168.50.24",
        "node-1e943eda-1",
        fallback_dir=tmp_path / "local",
        locate=locator,
        clock=lambda: t[0],
    )
    assert not files.on_share and files.dir == tmp_path / "local"
    assert LEGACY in files.reason
    files.tick()
    assert not files.on_share  # no retry before its time

    active[0] = PathMap(entries=(("/Volumes/Originals/", mount.as_posix() + "/"),))
    t[0] += RETRY_SHARE_SEC + 1
    files.tick()
    want = mount / "Backup" / "Youtube" / "maktaba" / "cluster"
    assert files.on_share and files.dir == want and files.reason == ""
    assert (want / "192.168.50.24" / "processes").is_dir()
    assert files.status()["on_share"] is True


@pytest.mark.unit
def test_the_hubs_share_root_goes_through_the_map(tmp_path: Path) -> None:
    mount = tmp_path / "O"
    (mount / "Media").mkdir(parents=True)
    pm = PathMap(entries=(("/Volumes/Originals/", mount.as_posix() + "/"),))
    locator = share.ShareLocator(None, "no library roots", path_map=lambda: pm, env={})
    files = ClusterFiles(None, "10.0.0.9", "w", fallback_dir=tmp_path / "local", locate=locator)
    assert not files.on_share and files.reason == "no library roots"
    locator.share_root = "/Volumes/Originals/Media"
    files.relocate()
    assert files.on_share and files.dir == mount / "Media" / "maktaba" / "cluster"


@pytest.mark.unit
def test_a_share_that_went_away_is_not_recreated(tmp_path: Path) -> None:
    mount = tmp_path / "O"
    (mount / "Media").mkdir(parents=True)
    pm = PathMap(entries=(("/Volumes/Originals/", mount.as_posix() + "/"),))
    locator = share.ShareLocator("/Volumes/Originals/Media", "", path_map=lambda: pm, env={})
    files = ClusterFiles(None, "10.0.0.9", "w", fallback_dir=tmp_path / "local", locate=locator)
    assert files.on_share
    # unmounted: the share root is gone, a write must not create it again
    import shutil

    shutil.rmtree(mount)
    files.write("logs", {"ts": "2026-10-19T08:00:00Z", "event": "x"})
    files.flush()
    assert not files.on_share and not mount.exists()
