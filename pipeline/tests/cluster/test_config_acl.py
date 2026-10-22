"""Node config ACLs on Windows, read back with icacls: each check runs an OS tool, which
takes the integration tier's time, not the unit tier's. Temp folders, the machine id
faked (test_config's helpers)."""

from __future__ import annotations

import json
import os
import subprocess
from pathlib import Path
from typing import Any

import pytest

from maktaba_pipeline.cluster import config
from maktaba_pipeline.cluster.config import NodeConfig
from tests.cluster.test_config import (
    _JOINED,
    _NEW,
    _icacls,
    _leftover,
    _named,
    _windows_profile,
)

pytestmark = [pytest.mark.integration, pytest.mark.skipif(os.name != "nt", reason="Windows ACLs")]


def _users_only(aces: list[str]) -> bool:
    return (
        len(aces) == 1
        and os.environ["USERNAME"].lower() in aces[0].lower()
        and "(I)" not in aces[0]
    )


def _grant_signed_in_users(folder: Path) -> None:
    """A second drive's inheritance: every signed-in user may change, Users may read."""
    subprocess.run(
        ["icacls", str(folder), "/grant", "*S-1-5-11:(OI)(CI)(M)", "*S-1-5-32-545:(OI)(CI)(RX)"],
        check=True,
        capture_output=True,
    )


@pytest.mark.parametrize("shape", ["the folder's ACL", "a wider ACL set by hand"])
def test_windows_a_node_json_others_may_read_is_made_the_users(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, shape: str
) -> None:
    """A node.json others may read or change (the folder's inherited ACL #178's writer
    left: Authenticated Users:(M), Users:(RX); or a wider ACL set by hand) is the user's
    alone after a claim and after a load, and the warning says so; one that is already
    the user's is left as it is (nothing said)."""
    from maktaba_pipeline import log
    from tests.cluster.test_config import _Warnings

    roaming = _windows_profile(tmp_path, monkeypatch, "pc-a")
    _grant_signed_in_users(roaming.parent)
    roaming.write_text(json.dumps(_JOINED), encoding="utf-8")
    if shape == "a wider ACL set by hand":
        user = os.environ["USERNAME"]
        for args in (["/inheritance:r", "/grant:r", f"{user}:F"], ["/grant", "*S-1-5-32-545:(R)"]):
            subprocess.run(["icacls", str(roaming), *args], check=True, capture_output=True)
    assert not _users_only(_icacls(roaming))
    warnings = _Warnings(private=True)
    monkeypatch.setattr(log, "get_logger", lambda: warnings)
    cfg = config.load()  # the claim
    assert cfg is not None and cfg.machine_id == "pc-a"
    assert _users_only(_icacls(roaming)), _icacls(roaming)
    assert warnings.events == ["node_config_made_private"]
    # the old file ReplaceFileW kept under its backup name is gone with the success
    assert sorted(p.name for p in roaming.parent.iterdir()) == ["node.json"]

    # claimed already: a load alone makes it the user's again
    _grant_signed_in_users(roaming.parent)
    subprocess.run(["icacls", str(roaming), "/reset"], check=True, capture_output=True)
    assert not _users_only(_icacls(roaming))
    assert config.load() is not None
    assert _users_only(_icacls(roaming))
    assert config.load() is not None  # already the user's: nothing more said
    assert warnings.events == ["node_config_made_private", "node_config_made_private"]


def test_windows_the_temp_file_is_the_users_before_the_secret_is_in_it(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """The temp file a node config is written through is the user's alone from the moment
    it exists, before a byte of the database credential is in it (its folder's ACL may let
    others read: Authenticated Users on a second drive): it is created with that DACL
    (never set afterwards) and with no sharing, so no other handle to it can be opened
    while it is written."""
    folder = tmp_path / "Maktaba"
    folder.mkdir()
    subprocess.run(
        ["icacls", str(folder), "/grant", "*S-1-5-11:(OI)(CI)(M)"], check=True, capture_output=True
    )
    seen: list[tuple[int, list[str], bool]] = []
    fdopen = os.fdopen

    def spy(fd: int, *args: Any, **kw: Any) -> Any:
        (tmp,) = folder.glob(".node.json-*.tmp")
        try:
            tmp.open("rb").close()
            opened = True
        except PermissionError:
            opened = False
        seen.append((tmp.stat().st_size, _icacls(tmp), opened))
        return fdopen(fd, *args, **kw)

    def afterwards(path: str) -> None:
        raise AssertionError(f"the DACL of {path} was set after it was created")

    monkeypatch.setattr(os, "fdopen", spy)
    monkeypatch.setattr(config, "_set_user_only_dacl", afterwards)
    config.save(
        NodeConfig(node_id="n1", name="pc", private_key="k1", database_url="postgres://a"),
        folder / "node.json",
    )
    assert len(seen) == 1
    size, aces, opened = seen[0]
    assert size == 0 and _users_only(aces) and not opened, seen
    assert _users_only(_icacls(folder / "node.json"))


@pytest.mark.parametrize(("bak", "want"), [(_JOINED, _JOINED), (None, _NEW)])
def test_windows_recovered_leftovers_are_the_users_only(
    tmp_path: Path, bak: dict[str, str] | None, want: dict[str, str]
) -> None:
    """A node.json put back from a write's leftovers is the user's alone: the backup, the
    old node.json, as it was; else the temp file, even one an older write left open to
    the folder's ACL (Authenticated Users here)."""
    folder = tmp_path / "Maktaba"
    folder.mkdir()
    subprocess.run(
        ["icacls", str(folder), "/grant", "*S-1-5-11:(OI)(CI)(M)"], check=True, capture_output=True
    )
    if bak is not None:
        _leftover(folder, _named(".bak", age=120), json.dumps(bak), age=3600)
    _leftover(folder, ".node-x1y2z3w4.tmp", json.dumps(_NEW), restrict=False)
    cfg = config.load(folder / "node.json")
    assert (
        cfg is not None and json.loads((folder / "node.json").read_text(encoding="utf-8")) == want
    )
    assert _users_only(_icacls(folder / "node.json"))
    assert sorted(p.name for p in folder.iterdir()) == ["node.json"]
