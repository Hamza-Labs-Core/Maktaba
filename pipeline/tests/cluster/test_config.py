"""Node config: per-OS paths, the override, atomic private writes."""

from __future__ import annotations

import os
import stat
from pathlib import Path

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
            system="Windows", env={"APPDATA": r"C:\Users\u\AppData\Roaming"}, home=HOME
        )
        == Path(r"C:\Users\u\AppData\Roaming") / "Maktaba" / "node.json"
    )
    # no APPDATA: the usual place under the profile
    assert config.config_path(system="Windows", env={}, home=HOME) == (
        HOME / "AppData" / "Roaming" / "Maktaba" / "node.json"
    )


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


def test_a_config_without_its_identity_is_an_error(tmp_path: Path) -> None:
    """A KeyError from a half-written file read as a crash; it names the file instead."""
    path = tmp_path / "node.json"
    path.write_text('{"node_id": "6f1c2a3b", "private_key": " ", "name": "n"}', encoding="utf-8")
    with pytest.raises(ValueError, match=r"node\.json: incomplete node config: no private_key"):
        config.load(path)
    path.write_text('{"database_url": "postgres://u:p@h/db"}', encoding="utf-8")
    with pytest.raises(ValueError, match="no node_id or private_key"):
        config.load(path)
