"""Where the cluster files live (design-agent.md section 7): the share root follows the
hub's rule (``api/internal/cluster/agent/location.go``)."""

from __future__ import annotations

import uuid
from pathlib import Path
from typing import Any

import pytest

from maktaba_pipeline.cluster import share
from maktaba_pipeline.engine.paths import PathMap
from maktaba_pipeline.engine.sql import dumps, is_pg, now, to_ts
from maktaba_pipeline.runtime import Database
from tests.engine.factory import make_library, make_video


@pytest.mark.unit
def test_common_root() -> None:
    assert share.common_root(["Y:/Videos/Kids", "Y:/Videos/Docs/x"]) == "Y:/Videos"
    assert share.common_root(["/volume1/media/a", "/volume1/media/b"]) == "/volume1/media"
    assert share.common_root(["y:/videos/a", "Y:/Videos/b"]) == "y:/videos"
    assert share.common_root(["//nas/share/x/a", "//nas/share/x/b"]) == "//nas/share/x"
    assert share.common_root(["//nas/share/a", "//nas/share/b"]) is None  # the share itself
    assert share.common_root(["/volume1/a", "/volume2/b"]) is None  # only "/"
    assert share.common_root(["Y:/a", "O:/b"]) is None
    assert share.common_root(["Y:/a", "Y:/b"]) is None  # only the drive
    assert share.common_root(["Y:\\Videos\\Kids\\"]) == "Y:/Videos/Kids"
    assert share.common_root([]) is None
    assert share.split_db_path("//nas/share/a/./b/") == ("//nas/share/", ["a", "b"])


@pytest.mark.unit
def test_share_root_rules() -> None:
    rows = [("l1", "Y:/Media/Kids", 10), ("l2", "Y:/Media/Docs", 5)]
    assert share.share_root_from(rows, env={}) == "Y:/Media"
    # nothing in common but a filesystem root: the busiest library, its roots' common dir
    rows = [("l1", "Y:/Kids", 10), ("l2", "O:/Docs/A", 500), ("l2", "O:/Docs/B", 500)]
    assert share.share_root_from(rows, env={}) == "O:/Docs"
    # ... else its first root (sorted)
    rows = [("l1", "Y:/Kids", 10), ("l2", "O:/Zed", 500), ("l2", "O:/Alpha", 500)]
    assert share.share_root_from(rows, env={}) == "O:/Alpha"
    # ties: the smallest library id
    rows = [("b", "Y:/B", 7), ("a", "O:/A", 7)]
    assert share.share_root_from(rows, env={}) == "O:/A"
    env = {"MAKTABA_SHARE_ROOT": "Z:\\Share\\"}
    assert share.share_root_from(rows, env=env) == "Z:/Share"
    assert share.share_root_from([], env={}) is None


@pytest.mark.unit
def test_cluster_dir_through_the_path_map() -> None:
    pm = PathMap.parse("Y:/=/Volumes/Y")
    assert share.cluster_dir_for("Y:/Media", pm) == Path("/Volumes/Y/Media/maktaba/cluster")
    assert share.cluster_dir_for("/srv/media", PathMap()) == Path("/srv/media/maktaba/cluster")


def _fake_net(
    monkeypatch: pytest.MonkeyPatch, routed: str, nics: dict[str, list[tuple[str, str]]]
) -> None:
    """The route to the home leaves from ``routed``; ``nics``: name -> [(address, mask)]."""
    import socket
    from types import SimpleNamespace

    import psutil

    class FakeSocket:
        def __init__(self, *a: Any) -> None:
            pass

        def __enter__(self) -> FakeSocket:
            return self

        def __exit__(self, *a: Any) -> None:
            pass

        def connect(self, addr: Any) -> None:
            pass

        def getsockname(self) -> tuple[str, int]:
            return (routed, 50000)

    fake = SimpleNamespace(
        AF_INET=socket.AF_INET,
        SOCK_DGRAM=socket.SOCK_DGRAM,
        socket=FakeSocket,
        getaddrinfo=lambda host, port, **kw: [
            (socket.AF_INET, socket.SOCK_DGRAM, 0, "", (host, port))
        ],
    )
    monkeypatch.setattr(share, "socket", fake)  # the module's name only, not socket's
    monkeypatch.setattr(
        psutil,
        "net_if_addrs",
        lambda: {
            name: [SimpleNamespace(family=socket.AF_INET, address=a, netmask=m) for a, m in addrs]
            for name, addrs in nics.items()
        },
    )


@pytest.mark.unit
def test_machine_ip_prefers_the_lan_address_over_a_vpn_route(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """Tailscale's subnet routes send the home's LAN through the VPN: the socket answers
    with the Tailscale address, but the machine's interface on the home's subnet wins."""
    nics = {
        "Tailscale": [("100.104.161.110", "255.255.255.255")],
        "vEthernet (External Switch)": [("192.168.50.24", "255.255.255.0")],
        "vEthernet (WSL)": [("172.27.176.1", "255.255.240.0")],
    }
    _fake_net(monkeypatch, "100.104.161.110", nics)
    url = "ws://192.168.50.177:8080/api/cluster/agent"
    assert share.machine_ip(url, env={}) == "192.168.50.24"
    # the route already leaves through the home's subnet: kept as it is
    nics["Ethernet 2"] = [("192.168.50.31", "255.255.255.0")]
    _fake_net(monkeypatch, "192.168.50.31", nics)
    assert share.machine_ip(url, env={}) == "192.168.50.31"
    # a home only reachable through the VPN: the VPN address it is
    _fake_net(monkeypatch, "100.104.161.110", nics)
    assert share.machine_ip("ws://100.90.1.2:8080", env={}) == "100.104.161.110"
    # the home's own process: its own address
    _fake_net(monkeypatch, "192.168.50.177", {"en0": [("192.168.50.177", "255.255.255.0")]})
    assert share.machine_ip(url, env={}) == "192.168.50.177"
    # the override still wins
    assert share.machine_ip(url, env={share.ENV_IP: "10.1.2.3"}) == "10.1.2.3"


class FakeDB:
    def __init__(self, rows: list[dict[str, Any]]) -> None:
        self.rows = rows

    async def fetch(self, sql: str, *args: Any) -> list[dict[str, Any]]:
        assert "library_roots" in sql
        return self.rows


@pytest.mark.integration  # an event loop opens a socket pair
@pytest.mark.asyncio
async def test_resolve_cluster_dir() -> None:
    db = FakeDB(
        [
            {"library_id": "l1", "path": "Y:/Media/A", "videos": 3},
            {"library_id": "l2", "path": "Y:/Media/B", "videos": 0},
        ]
    )
    pm = PathMap.parse("Y:/=/mnt/y")
    found, how = await share.resolve_cluster_dir(db, pm, env={})
    assert found == Path("/mnt/y/Media/maktaba/cluster") and "Y:/Media" in how
    found, how = await share.resolve_cluster_dir(db, pm, env={"MAKTABA_CLUSTER_DIR": "/tmp/c"})
    assert found == Path("/tmp/c") and how == "MAKTABA_CLUSTER_DIR"
    found, how = await share.resolve_cluster_dir(None, pm, env={})
    assert found is None
    found, how = await share.resolve_cluster_dir(FakeDB([]), pm, env={})
    assert found is None and how == "no library roots"


async def _root(db: Database, lib: uuid.UUID, path: str) -> None:
    await db.execute(
        "INSERT INTO library_roots (id, library_id, path, path_canonical) VALUES ($1,$2,$3,$4)",
        str(uuid.uuid4()),
        str(lib),
        path,
        path.lower(),
    )


@pytest.mark.asyncio
async def test_the_roots_come_from_library_roots_as_the_hub_reads_them(db: Database) -> None:
    """The API edits roots in ``library_roots`` only; ``libraries.roots`` is the old copy."""
    a, b, gone = await make_library(db), await make_library(db), await make_library(db)
    old = ["/old/place"]
    for lib in (a, b):
        await db.execute(
            "UPDATE libraries SET roots = $2 WHERE id = $1",
            str(lib),
            old if is_pg(db) else dumps(old),
        )
    await _root(db, a, "/Volumes/Originals/Channels")
    await _root(db, b, "/Volumes/Originals/Courses")
    await _root(db, gone, "/elsewhere/x")
    await db.execute(
        "UPDATE libraries SET deleted_at = $2 WHERE id = $1", str(gone), to_ts(db, now())
    )
    await make_video(db, a)
    rows = await share.load_library_roots(db)
    assert sorted(p for _, p, _ in rows) == [
        "/Volumes/Originals/Channels",
        "/Volumes/Originals/Courses",
    ]
    assert {lib: n for lib, _, n in rows}[str(a)] == 1
    found, _ = await share.resolve_cluster_dir(db, PathMap(), env={})
    assert found == Path("/Volumes/Originals/maktaba/cluster")  # the hub's share root
