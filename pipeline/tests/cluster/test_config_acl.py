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
from tests.cluster.test_config import _JOINED, _NEW, _icacls, _leftover, _windows_profile

pytestmark = [pytest.mark.integration, pytest.mark.skipif(os.name != "nt", reason="Windows ACLs")]


def _users_only(aces: list[str]) -> bool:
    return (
        len(aces) == 1
        and os.environ["USERNAME"].lower() in aces[0].lower()
        and "(I)" not in aces[0]
    )


def test_windows_claim_keeps_the_files_acl(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    """The claim replaces the file with ReplaceFileW, which keeps its ACL as it was."""
    roaming = _windows_profile(tmp_path, monkeypatch, "pc-a")
    roaming.write_text(json.dumps(_JOINED), encoding="utf-8")
    user = os.environ["USERNAME"]
    for args in (["/inheritance:r", "/grant:r", f"{user}:F"], ["/grant", "*S-1-5-32-545:(R)"]):
        subprocess.run(["icacls", str(roaming), *args], check=True, capture_output=True)
    before = _icacls(roaming)
    cfg = config.load()
    assert cfg is not None and cfg.machine_id == "pc-a"
    assert _icacls(roaming) == before
    # the old file ReplaceFileW kept under its backup name is gone with the success
    assert sorted(p.name for p in roaming.parent.iterdir()) == ["node.json"]


def test_windows_the_temp_file_is_the_users_before_the_secret_is_in_it(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """The temp file a node config is written through is limited to the user while it is
    still empty, before a byte of the database credential is in it (its folder's ACL may
    let others read: Authenticated Users on a second drive)."""
    folder = tmp_path / "Maktaba"
    folder.mkdir()
    subprocess.run(
        ["icacls", str(folder), "/grant", "*S-1-5-11:(OI)(CI)(M)"], check=True, capture_output=True
    )
    seen: list[tuple[int, list[str]]] = []
    fdopen = os.fdopen

    def spy(fd: int, *args: Any, **kw: Any) -> Any:
        (tmp,) = folder.glob(".node.json-*.tmp")
        seen.append((tmp.stat().st_size, _icacls(tmp)))
        return fdopen(fd, *args, **kw)

    monkeypatch.setattr(os, "fdopen", spy)
    config.save(
        NodeConfig(node_id="n1", name="pc", private_key="k1", database_url="postgres://a"),
        folder / "node.json",
    )
    assert len(seen) == 1
    size, aces = seen[0]
    assert size == 0 and _users_only(aces), aces
    assert _users_only(_icacls(folder / "node.json"))


@pytest.mark.parametrize(("bak", "want"), [(_JOINED, _JOINED), (None, _NEW)])
def test_windows_recovered_leftovers_are_the_users_only(
    tmp_path: Path, bak: dict[str, str] | None, want: dict[str, str]
) -> None:
    """A node.json put back from a write's leftovers (its backup, else its temp file) is
    the user's alone."""
    folder = tmp_path / "Maktaba"
    folder.mkdir()
    if bak is not None:
        _leftover(folder, ".node.json-123-abcd1234.bak", json.dumps(bak))
    _leftover(folder, ".node-x1y2z3w4.tmp", json.dumps(_NEW))
    cfg = config.load(folder / "node.json")
    assert (
        cfg is not None and json.loads((folder / "node.json").read_text(encoding="utf-8")) == want
    )
    assert _users_only(_icacls(folder / "node.json"))
    assert sorted(p.name for p in folder.iterdir()) == ["node.json"]
