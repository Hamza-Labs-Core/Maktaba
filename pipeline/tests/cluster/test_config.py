"""Node config: per-OS paths, the override, atomic private writes."""

from __future__ import annotations

import json
import os
import platform
import stat
from pathlib import Path
from typing import Any

import pytest

from maktaba_pipeline.cluster import config
from maktaba_pipeline.cluster.config import HomeRef, NodeConfig

pytestmark = pytest.mark.unit

HOME = Path("/home/u")


def test_paths_per_os() -> None:
    assert config.config_path(system="Darwin", env={}, home=HOME) == (
        HOME / "Library" / "Application Support" / "Maktaba" / "node.json"
    )
    assert config.config_path(system="Linux", env={}, home=HOME) == (
        HOME / ".config" / "maktaba" / "node.json"
    )
    assert config.config_path(system="Linux", env={"XDG_CONFIG_HOME": "/x/cfg"}, home=HOME) == (
        Path("/x/cfg") / "maktaba" / "node.json"
    )
    assert (
        config.config_path(
            system="Windows", env={"LOCALAPPDATA": r"C:\Users\u\AppData\Local"}, home=HOME
        )
        == Path(r"C:\Users\u\AppData\Local") / "Maktaba" / "node.json"
    )
    # no LOCALAPPDATA: the usual place under the profile
    assert config.config_path(system="Windows", env={}, home=HOME) == (
        HOME / "AppData" / "Local" / "Maktaba" / "node.json"
    )


def test_windows_keeps_a_node_config_from_the_roaming_profile(tmp_path: Path) -> None:
    """Epic 38: a new join writes %LOCALAPPDATA%; a machine that joined before keeps its
    %APPDATA% file, one node config per machine, even with a stray file in the new
    place (maktaba-server never copies it, story 38.1)."""
    env = {"LOCALAPPDATA": str(tmp_path / "Local"), "APPDATA": str(tmp_path / "Roaming")}
    cur = tmp_path / "Local" / "Maktaba" / "node.json"
    old = tmp_path / "Roaming" / "Maktaba" / "node.json"
    assert config.config_path(system="Windows", env=env, home=HOME) == cur
    old.parent.mkdir(parents=True)
    old.write_text("{}", encoding="utf-8")
    assert config.config_path(system="Windows", env=env, home=HOME) == old
    cur.parent.mkdir(parents=True)
    cur.write_text("{}", encoding="utf-8")
    assert config.config_path(system="Windows", env=env, home=HOME) == old


def test_windows_rejoin_and_hand_edit_land_in_the_one_node_config(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """A re-join (save) and a hand edit of a machine that joined before Epic 38 both
    land in its %APPDATA% node.json, the file maktaba-server and streaming read too."""
    monkeypatch.delenv(config.ENV_VAR, raising=False)
    monkeypatch.setenv("LOCALAPPDATA", str(tmp_path / "Local"))
    monkeypatch.setenv("APPDATA", str(tmp_path / "Roaming"))
    monkeypatch.setattr(platform, "system", lambda: "Windows")
    old = tmp_path / "Roaming" / "Maktaba" / "node.json"
    first = NodeConfig(node_id="first", name="pc", private_key="k1", database_url="postgres://a")
    config.save(first, old)
    rejoined = NodeConfig(
        node_id="rejoined", name="pc", private_key="k2", database_url="postgres://b"
    )
    assert config.save(rejoined) == old  # where it resolved: the one it read
    loaded = config.load()
    assert loaded is not None and loaded.node_id == "rejoined"
    data = json.loads(old.read_text(encoding="utf-8"))
    data["node_id"] = "edited"
    old.write_text(json.dumps(data), encoding="utf-8")
    loaded = config.load()
    assert loaded is not None and loaded.node_id == "edited"
    assert not (tmp_path / "Local" / "Maktaba" / "node.json").exists()


def _windows_profile(tmp_path: Path, monkeypatch: pytest.MonkeyPatch, this_pc: str) -> Path:
    """A Windows profile under tmp_path, this PC's id faked; the roaming node config."""
    monkeypatch.delenv(config.ENV_VAR, raising=False)
    monkeypatch.setenv("LOCALAPPDATA", str(tmp_path / "Local"))
    monkeypatch.setenv("APPDATA", str(tmp_path / "Roaming"))
    monkeypatch.setenv(config.MACHINE_ID_ENV, this_pc)
    monkeypatch.setattr(platform, "system", lambda: "Windows")
    roaming = tmp_path / "Roaming" / "Maktaba" / "node.json"
    roaming.parent.mkdir(parents=True)
    return roaming


_JOINED = {"node_id": "n1", "name": "pc", "private_key": "k1", "database_url": "postgres://a"}


def test_windows_an_unclaimed_roaming_config_is_claimed(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """The first new Maktaba that loads a node config from before Epic 38 claims it for
    its PC: machine_id written in, every other key kept, and no second file."""
    roaming = _windows_profile(tmp_path, monkeypatch, "pc-a")
    roaming.write_text(json.dumps({**_JOINED, "extra": 1}), encoding="utf-8")
    cfg = config.load()
    assert cfg is not None and cfg.node_id == "n1" and cfg.machine_id == "pc-a"
    data = json.loads(roaming.read_text(encoding="utf-8"))
    assert data == {**_JOINED, "extra": 1, "machine_id": "pc-a"}
    assert config.config_path() == roaming
    assert not (tmp_path / "Local" / "Maktaba" / "node.json").exists()


def test_windows_this_pcs_roaming_config_wins(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    roaming = _windows_profile(tmp_path, monkeypatch, "pc-a")
    roaming.write_text(json.dumps({**_JOINED, "machine_id": "PC-A"}), encoding="utf-8")
    assert config.config_path() == roaming
    cfg = config.load()
    assert cfg is not None and cfg.joined


def test_windows_another_pcs_roaming_config_is_skipped(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """A roaming node config another PC claimed is not this PC's: it is not joined, its
    own join writes %LOCALAPPDATA%, and the other PC's identity stays as it was."""
    roaming = _windows_profile(tmp_path, monkeypatch, "pc-a")
    theirs = json.dumps({**_JOINED, "machine_id": "pc-b"})
    roaming.write_text(theirs, encoding="utf-8")
    local = tmp_path / "Local" / "Maktaba" / "node.json"
    assert config.config_path() == local
    assert config.load() is None
    mine = NodeConfig(node_id="n2", name="pc", private_key="k2", machine_id="pc-a")
    assert config.save(mine) == local  # a join on this PC
    loaded = config.load()
    assert loaded is not None and loaded.node_id == "n2"
    assert roaming.read_text(encoding="utf-8") == theirs


@pytest.mark.skipif(os.name != "nt", reason="Windows ACLs")
def test_windows_claim_keeps_the_files_acl(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    """The claim replaces the file with ReplaceFileW, which keeps its ACL as it was."""
    import subprocess

    roaming = _windows_profile(tmp_path, monkeypatch, "pc-a")
    roaming.write_text(json.dumps(_JOINED), encoding="utf-8")
    user = os.environ["USERNAME"]
    for args in (["/inheritance:r", "/grant:r", f"{user}:F"], ["/grant", "*S-1-5-32-545:(R)"]):
        subprocess.run(["icacls", str(roaming), *args], check=True, capture_output=True)

    def acl() -> str:
        out = subprocess.run(["icacls", str(roaming)], check=True, capture_output=True, text=True)
        return out.stdout

    before = acl()
    cfg = config.load()
    assert cfg is not None and cfg.machine_id == "pc-a"
    assert acl() == before
    # the old file ReplaceFileW kept under its backup name is gone with the success
    assert sorted(p.name for p in roaming.parent.iterdir()) == ["node.json"]


def _icacls(path: Path) -> list[str]:
    """The ACEs icacls lists for path, one per line."""
    import subprocess

    out = subprocess.run(["icacls", str(path)], check=True, capture_output=True, text=True).stdout
    aces: list[str] = []
    for line in out.splitlines():
        if line.startswith("Successfully processed"):
            break
        line = line.replace(str(path), "", 1).strip()
        if line:
            aces.append(line)
    return aces


class _Warnings:
    """A logger that keeps what it was told to warn."""

    def __init__(self) -> None:
        self.events: list[str] = []

    def warning(self, event: str, **_: object) -> None:
        self.events.append(event)


def _claim_seam(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, replace_file: Any
) -> tuple[Path, _Warnings]:
    """A roaming node config to claim, ReplaceFileW replaced, warnings caught."""
    from maktaba_pipeline import log

    roaming = _windows_profile(tmp_path, monkeypatch, "pc-a")
    roaming.write_text(json.dumps(_JOINED), encoding="utf-8")
    monkeypatch.setattr(config, "_replace_file", replace_file)
    monkeypatch.setattr(config, "_claim_failed", set())
    warnings = _Warnings()
    monkeypatch.setattr(log, "get_logger", lambda: warnings)
    return roaming, warnings


def _windows_error(code: int) -> OSError:
    return OSError(0, f"ReplaceFileW failed ({code})", None, code)


@pytest.mark.skipif(os.name != "nt", reason="ReplaceFileW")
def test_windows_claim_survives_replacement_not_moved(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """ERROR_UNABLE_TO_MOVE_REPLACEMENT (1176) with the old file gone: the new copy is the
    only one, so it takes the name, limited to the user first; nothing is left over."""

    def gone(path: Path, tmp: Path, backup: Path) -> None:
        path.unlink()
        raise _windows_error(1176)

    roaming, warnings = _claim_seam(tmp_path, monkeypatch, gone)
    cfg = config.load()
    assert cfg is not None and cfg.machine_id == "pc-a"
    assert json.loads(roaming.read_text(encoding="utf-8")) == {**_JOINED, "machine_id": "pc-a"}
    aces = _icacls(roaming)
    user = os.environ.get("USERNAME", "")
    assert len(aces) == 1 and user and user.lower() in aces[0].lower(), (aces, user, warnings.events)
    assert sorted(p.name for p in roaming.parent.iterdir()) == ["node.json"]
    assert warnings.events == []


@pytest.mark.skipif(os.name != "nt", reason="ReplaceFileW")
def test_windows_claim_failing_with_both_files_kept(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """1176 with a backup name (both files keep their names): the old file stays exactly
    as it was, the node runs on unclaimed, and it is said once."""

    def kept(path: Path, tmp: Path, backup: Path) -> None:
        raise _windows_error(1176)

    roaming, warnings = _claim_seam(tmp_path, monkeypatch, kept)
    before = roaming.read_bytes()
    for _ in range(2):
        cfg = config.load()
        assert cfg is not None and cfg.joined and cfg.machine_id == ""
    assert roaming.read_bytes() == before
    assert sorted(p.name for p in roaming.parent.iterdir()) == ["node.json"]
    assert warnings.events == ["node_config_claim_failed"]


@pytest.mark.skipif(os.name != "nt", reason="ReplaceFileW")
def test_windows_claim_failing_with_the_old_file_moved_aside(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """ERROR_UNABLE_TO_MOVE_REPLACEMENT_2 (1177): the old file is under the backup name;
    it is put back, the same file with the same ACL, and nothing is left over."""

    def aside(path: Path, tmp: Path, backup: Path) -> None:
        os.replace(path, backup)
        raise _windows_error(1177)

    roaming, warnings = _claim_seam(tmp_path, monkeypatch, aside)
    before, acl = roaming.read_bytes(), _icacls(roaming)
    cfg = config.load()
    assert cfg is not None and cfg.joined
    assert roaming.read_bytes() == before and _icacls(roaming) == acl
    assert sorted(p.name for p in roaming.parent.iterdir()) == ["node.json"]
    assert warnings.events == ["node_config_claim_failed"]


def test_windows_claim_failing_before_the_replace(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """A claim whose new copy cannot even be written leaves the file byte-identical and
    no temp file, and the node runs on."""

    def unflushable(fd: int) -> None:
        raise OSError("the disk is full")

    roaming, warnings = _claim_seam(tmp_path, monkeypatch, None)
    before = roaming.read_bytes()
    monkeypatch.setattr(os, "fsync", unflushable)
    cfg = config.load()
    assert cfg is not None and cfg.joined
    assert roaming.read_bytes() == before
    assert sorted(p.name for p in roaming.parent.iterdir()) == ["node.json"]
    assert warnings.events == ["node_config_claim_failed"]


def test_the_override_wins_on_every_os() -> None:
    env = {config.ENV_VAR: "/elsewhere/n.json", "APPDATA": "C:/a", "XDG_CONFIG_HOME": "/x"}
    for system in ("Darwin", "Linux", "Windows"):
        assert config.config_path(system=system, env=env, home=HOME) == Path("/elsewhere/n.json")


def test_default_path_reads_the_environment(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    monkeypatch.setenv(config.ENV_VAR, str(tmp_path / "n.json"))
    assert config.config_path() == tmp_path / "n.json"


def test_save_and_load_round_trip(tmp_path: Path) -> None:
    path = tmp_path / "sub" / "node.json"
    assert config.load(path) is None
    cfg = NodeConfig(
        node_id="6f1c2a3b-4d5e-4f60-8a71-92b3c4d5e6f7",
        name="linux-box",
        private_key="AQID",
        home=HomeRef(url="http://192.168.1.20:8080", instance_id="0b9a", name="home-mac"),
        database_url="postgres://u:p@h/db",
        joined_at="2026-10-18T00:00:00Z",
    )
    config.save(cfg, path)
    assert config.load(path) == cfg
    assert cfg.node8 == "6f1c2a3b" and cfg.joined
    if os.name != "nt":
        assert stat.S_IMODE(path.stat().st_mode) == 0o600
    # a second save replaces the file and leaves no temp files behind
    config.save(NodeConfig(node_id=cfg.node_id, name="n2", private_key="AQID"), path)
    loaded = config.load(path)
    assert loaded is not None and loaded.name == "n2" and not loaded.joined
    assert sorted(p.name for p in path.parent.iterdir()) == ["node.json"]


def test_a_garbage_file_is_an_error(tmp_path: Path) -> None:
    path = tmp_path / "node.json"
    path.write_text("[1]", encoding="utf-8")
    with pytest.raises(ValueError):
        config.load(path)


def test_a_file_that_does_not_parse_names_itself(tmp_path: Path) -> None:
    """A typo or a truncated file is never "not joined"; the error says which file."""
    path = tmp_path / "node.json"
    path.write_text('{"node_id": "6f1c2a3b", "database_url": "postgres://n:pw@h', encoding="utf-8")
    with pytest.raises(ValueError, match=r"node\.json: not a node config \(") as err:
        config.load(path)
    assert "pw@h" not in str(err.value)


def test_a_config_without_its_identity_is_an_error(tmp_path: Path) -> None:
    """A KeyError from a half-written file read as a crash; it names the file instead."""
    path = tmp_path / "node.json"
    path.write_text('{"node_id": "6f1c2a3b", "private_key": " ", "name": "n"}', encoding="utf-8")
    with pytest.raises(ValueError, match=r"node\.json: incomplete node config: no private_key"):
        config.load(path)
    path.write_text('{"database_url": "postgres://u:p@h/db"}', encoding="utf-8")
    with pytest.raises(ValueError, match="no node_id or private_key"):
        config.load(path)
