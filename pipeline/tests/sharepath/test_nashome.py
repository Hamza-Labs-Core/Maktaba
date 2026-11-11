"""A NAS home's own folders as the SMB shares they are (Epic 59, story 59.13): the
platform rules (the shared fixtures the API's Go copy reads too), the home's resolver, a
node matching the home's share by any of its names."""

from __future__ import annotations

import json
from pathlib import Path

import pytest

from maktaba_pipeline.sharepath.mounts import Mount, parse_mac_mount
from maktaba_pipeline.sharepath.nashome import (
    HomeShares,
    home_names,
    is_host_path,
    local_share_mounts,
    nas_home,
    rule_share,
    setup_shares,
)
from maktaba_pipeline.sharepath.resolver import Resolver, StorageServer, build_resolver

pytestmark = pytest.mark.unit

FIXTURES = json.loads(
    (
        Path(__file__).resolve().parents[3] / "shared/testdata/sharepath/nas_local_roots.json"
    ).read_text(encoding="utf-8")
)


@pytest.mark.parametrize("case", FIXTURES["rule"], ids=lambda c: f"{c['platform']}:{c['path']}")
def test_rule_matches_the_shared_fixtures(case: dict[str, str | None]) -> None:
    got = rule_share(str(case["platform"]), str(case["path"]))
    if case["share"] is None:
        assert got is None
    else:
        assert got == (case["share"], case["root"])


@pytest.mark.parametrize(
    "case", FIXTURES["host_path"], ids=lambda c: f"{c['platform']}:{c['path']}"
)
def test_host_path_matches_the_shared_fixtures(case: dict[str, object]) -> None:
    assert is_host_path(str(case["platform"]), str(case["path"])) is case["host"]


def _home(name: str, exports: dict[str, str], platform: str, *addrs: str) -> HomeShares:
    return HomeShares(
        server=StorageServer(name=name, addresses=addrs, exports=tuple(exports.items())),
        platform=platform,
    )


def _home_resolver(home: HomeShares) -> Resolver:
    return build_resolver(
        servers=[home.server], system="Linux", local=local_share_mounts(home), probe=False
    )


def test_t01_dsm_volume_is_its_share() -> None:
    share, root = rule_share("dsm", "/volume1/Movies") or ("", "")
    home = _home("diskstation", {root: share}, "dsm", "diskstation.local", "192.168.1.20")
    r = _home_resolver(home)
    assert (
        r.canonical("/volume1/Movies/Film (2020)/a.mkv")
        == "smb://diskstation/Movies/Film (2020)/a.mkv"
    )
    assert r.to_local("smb://diskstation/Movies/a.mkv") == "/volume1/Movies/a.mkv"
    # the share map the home reports (the API and the streaming service read it)
    assert r.as_json() == {"smb://diskstation/Movies/": "/volume1/Movies/"}


def test_t02_unraid_user_share() -> None:
    share, root = rule_share("unraid", "/mnt/user/Movies/4K") or ("", "")
    r = _home_resolver(_home("tower", {root: share}, "unraid"))
    assert r.canonical("/mnt/user/Movies/4K/a.mkv") == "smb://tower/Movies/4K/a.mkv"


def test_t03_truenas_share_named_at_setup() -> None:
    assert rule_share("truenas", "/mnt/tank/media") is None  # not derivable from the path
    r = _home_resolver(_home("truenas", {"/mnt/tank/media": "media"}, "truenas"))
    assert r.canonical("/mnt/tank/media/Films/a.mkv") == "smb://truenas/media/Films/a.mkv"


def test_catch_all_mount_named_at_setup() -> None:
    r = _home_resolver(_home("nas", {"/media/Movies": "Movies", "/media/TV": "TV"}, "docker"))
    assert r.canonical("/media/TV/Show/S01E01.mkv") == "smb://nas/TV/Show/S01E01.mkv"
    assert not is_host_path("dsm", "/media/Movies")


def test_local_only_folder_has_no_share() -> None:
    home = _home("diskstation", {"/volume1/Movies": "Movies", "/volume1/Private": ""}, "dsm")
    assert [m.prefix for m in local_share_mounts(home)] == ["smb://diskstation/Movies/"]
    r = _home_resolver(home)
    assert r.canonical("/volume1/Private/a.mkv") is None


@pytest.mark.parametrize("host", ["diskstation.local", "192.168.1.20", "DiskStation"])
def test_t04_node_matches_the_home_by_any_alias(host: str) -> None:
    """A Mac node mounts //<host>/Movies at /Volumes/Movies: it opens the home's
    canonical path with no path map, whichever name it mounted the NAS by."""
    server = StorageServer(
        name="diskstation",
        addresses=("diskstation.local", "192.168.1.20"),
        exports=(("/volume1/Movies", "Movies"),),
    )
    mounts = parse_mac_mount(
        f"//guest@{host}/Movies on /Volumes/Movies (smbfs, nodev, nosuid, mounted by me)"
    )
    node = build_resolver(servers=[server], mounts=mounts, system="Darwin", probe=False)
    assert node.to_local("smb://diskstation/Movies/Film/a.mkv") == "/Volumes/Movies/Film/a.mkv"
    assert node.canonical("/Volumes/Movies/Film/a.mkv") == "smb://diskstation/Movies/Film/a.mkv"


def test_nfs_node_matches_through_the_home_exports() -> None:
    """NFS-only households: a node's export mount of /volume1/Movies is the same share."""
    server = StorageServer(
        name="diskstation", addresses=("192.168.1.20",), exports=(("/volume1/Movies", "Movies"),)
    )
    node = build_resolver(
        servers=[server],
        mounts=[Mount("nfs", "192.168.1.20", "/volume1/Movies", "/mnt/movies/", "proc")],
        system="Linux",
        probe=False,
    )
    assert node.to_local("smb://diskstation/Movies/a.mkv") == "/mnt/movies/a.mkv"


def test_a_network_mount_of_the_share_wins_over_the_local_folder() -> None:
    home = _home("diskstation", {"/volume1/Movies": "Movies"}, "dsm", "192.168.1.20")
    r = build_resolver(
        servers=[home.server],
        mounts=[Mount("smb", "192.168.1.20", "Movies", "/mnt/smb/Movies/", "proc")],
        system="Linux",
        local=local_share_mounts(home),
        probe=False,
    )
    assert r.to_local("smb://diskstation/Movies/a.mkv") == "/mnt/smb/Movies/a.mkv"


def test_local_only_export_is_skipped_for_a_nodes_nfs_mount() -> None:
    server = StorageServer(name="nas", addresses=("10.0.0.2",), exports=(("/volume1/Private", ""),))
    node = build_resolver(
        servers=[server],
        mounts=[Mount("nfs", "10.0.0.2", "/volume1/Private", "/mnt/p/", "proc")],
        system="Linux",
        probe=False,
    )
    # the empty name is not taken for a share: the Synology volume rule names it
    assert node.canonical("/mnt/p/a.mkv") == "smb://nas/Private/a.mkv"


def test_platform_and_setup_from_the_environment() -> None:
    assert nas_home({"MAKTABA_PLATFORM": "DSM"}) == "dsm"
    assert nas_home({"MAKTABA_PLATFORM": "docker"}) == "docker"
    assert nas_home({"MAKTABA_PLATFORM": "darwin"}) is None
    assert nas_home({}) is None
    s = setup_shares(
        {
            "MAKTABA_HOME_SHARES": json.dumps(
                {
                    "server": "TrueNAS",
                    "aliases": ["truenas.local", "10.0.0.5"],
                    "roots": {"/mnt/tank/media/": "media", "/mnt/tank/home": "", "relative": "x"},
                }
            )
        }
    )
    assert s.server == "truenas"
    assert s.aliases == ("truenas.local", "10.0.0.5")
    assert s.roots == (("/mnt/tank/media", "media"), ("/mnt/tank/home", ""))
    assert setup_shares({"MAKTABA_HOME_SHARES": "{not json"}).roots == ()


def test_home_names() -> None:
    names = home_names("DiskStation", ips=["192.168.1.20"], env={"MAKTABA_HOST_IP": "10.0.0.9"})
    assert names == ["diskstation", "diskstation.local", "10.0.0.9", "192.168.1.20"]
