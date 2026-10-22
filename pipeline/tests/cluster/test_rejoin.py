"""``maktaba-pipeline join --rejoin``: start over as a new machine."""

from __future__ import annotations

import io
import os
import time
from dataclasses import replace
from pathlib import Path

import pytest

from maktaba_pipeline.cluster import config as node_config
from maktaba_pipeline.cluster.join import JoinError
from tests.cluster.test_join import FakeHome, _join, fake  # noqa: F401 - the fixture


def test_rejoin_starts_over_after_a_confirmation(
    fake: tuple[FakeHome, str],  # noqa: F811
    tmp_path: Path,
) -> None:
    home, url = fake
    path = tmp_path / "node.json"
    first = _join(url, path, io.StringIO())
    # no: nothing changes
    with pytest.raises(JoinError, match="cancelled"):
        _join(url, path, io.StringIO(), rejoin=True, ask=lambda q: "n")
    assert node_config.load(path) == first
    # without a terminal, --yes is needed
    with pytest.raises(JoinError, match="--yes"):
        _join(url, path, io.StringIO(), rejoin=True)
    out = io.StringIO()
    asked: list[str] = []

    def ask(q: str) -> str:
        asked.append(q)
        return "y"

    again = _join(url, path, out, rejoin=True, ask=ask)
    assert first.node_id in asked[0]
    assert again.joined and again.node_id != first.node_id
    assert again.private_key != first.private_key
    assert node_config.load(path.with_name("node.json.bak")) == first
    assert "new one" in out.getvalue() and first.node_id in out.getvalue()
    assert len(home.joins) == 2
    # --yes skips the question
    third = _join(url, path, io.StringIO(), rejoin=True, assume_yes=True)
    assert third.node_id not in (first.node_id, again.node_id)


def test_rejoin_forgets_what_a_failed_write_left(
    fake: tuple[FakeHome, str],  # noqa: F811
    tmp_path: Path,
) -> None:
    """A re-join starts over even beside what a write that could not finish left of the
    old identity (a load would put it back): it goes with node.json, and the old config
    is kept as node.json.bak from the file that held it."""
    _, url = fake
    path = tmp_path / "node.json"
    first = _join(url, path, io.StringIO())
    stamp = f"{int(time.time()) - 5}-4242-0badf00d"
    stale = path.with_name(f".node.json-{stamp}.bak")
    os.replace(path, stale)  # node.json under a backup name, a write still in its minute
    path.with_name(f".node.json-{stamp}.tmp").write_text(stale.read_text())
    again = _join(url, path, io.StringIO(), rejoin=True, assume_yes=True)
    assert again.node_id != first.node_id
    assert node_config.load(path.with_name("node.json.bak")) == first
    assert sorted(p.name for p in tmp_path.iterdir()) == ["node.json", "node.json.bak"]
    assert node_config.load(path) == again


def test_a_config_without_the_cluster_key_points_at_rejoin(
    fake: tuple[FakeHome, str],  # noqa: F811
    tmp_path: Path,
) -> None:
    _, url = fake
    path = tmp_path / "node.json"
    cfg = _join(url, path, io.StringIO())
    old = replace(cfg, home=replace(cfg.home, cluster_pub=""))
    node_config.save(old, path)
    out = io.StringIO()
    assert _join(url, path, out) == old
    assert "join --rejoin" in out.getvalue()
