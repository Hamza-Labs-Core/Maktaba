"""The rig's workers can never reach the home of the machine running the rig: no real
home folder (its agent key), no cluster agent, no API that answers. Runs without
Postgres."""

from __future__ import annotations

import os
from pathlib import Path

from tests.components import rig


def test_worker_env_never_points_at_the_real_home(tmp_path: Path) -> None:
    real_home = Path.home()
    caller = {
        "HOME": str(real_home),
        "MAKTABA_CLUSTER_AGENT": "1",
        "MAKTABA_API_URL": "http://127.0.0.1:8080",
        "MAKTABA_HOME_AGENT_KEY": str(real_home / ".maktaba" / "cluster" / "home-agent.key"),
    }
    env = rig.worker_env(caller, tmp_path / "w1")
    assert env["MAKTABA_CLUSTER_AGENT"] == "0"
    assert env["MAKTABA_API_URL"] == rig.DEAD_API_URL
    for key in ("HOME", "USERPROFILE", "MAKTABA_HOME_AGENT_KEY", "MAKTABA_SECRETS_KEY"):
        assert Path(env[key]).is_relative_to(tmp_path), key
        assert not Path(env[key]).is_relative_to(real_home / ".maktaba"), key
    for port in (":8080", ":8088", ":8081", ":9103"):
        assert port not in env["MAKTABA_API_URL"]


def test_worker_home_key_path_is_the_throwaway_one(tmp_path: Path) -> None:
    from maktaba_pipeline.cluster import share

    env = rig.worker_env(dict(os.environ), tmp_path / "w1")
    assert share.home_key_path(env).is_relative_to(tmp_path)
