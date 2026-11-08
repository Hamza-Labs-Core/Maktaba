"""Test sessions running at once (two worktrees, xdist workers, CI jobs on one runner) each
get their own plugin API dev server: its own mint port, socket and build, none of it shared,
and one session ending, or killed, leaves the other's serving."""

from __future__ import annotations

import json
import os
import shutil
import signal
import socket
import subprocess
import sys
import tempfile
import time
import urllib.request
from collections.abc import Iterator
from pathlib import Path
from typing import Any

import pytest

from tests.plugins.firstparty import harness

pytestmark = [pytest.mark.integration]

PIPELINE = Path(__file__).resolve().parents[3]
#: a session building the binary
_BUILD = "from tests.plugins.firstparty import harness as h; h.plugin_api_dev()"
#: a session starting (and ending): it prints its folder
_NEXT_SESSION = "from tests.plugins.firstparty import harness as h; print(h.session_dir())"


@pytest.fixture(scope="module")
def warm() -> Iterator[None]:
    """go's cache and the checkout's seed binary warm (a cold build is ~13 s, once a run)."""
    if shutil.which("go") is None:
        pytest.skip("go is needed for the plugin API's dev server")
    env = {k: v for k, v in os.environ.items() if k != "MAKTABA_PLUGIN_API_DEV"}
    subprocess.run(
        [sys.executable, "-c", _BUILD],
        cwd=PIPELINE,
        env=env,
        check=True,
    )
    yield


def _start() -> subprocess.Popen[str]:
    # built by each session, never a binary given from outside
    env = {k: v for k, v in os.environ.items() if k != "MAKTABA_PLUGIN_API_DEV"}
    return subprocess.Popen(
        [sys.executable, "-m", "tests.plugins.firstparty.dev_session"],
        cwd=PIPELINE,
        env=env,
        stdin=subprocess.PIPE,
        stdout=subprocess.PIPE,
        text=True,
    )


def _info(p: subprocess.Popen[str]) -> dict[str, Any]:
    assert p.stdout is not None
    line = p.stdout.readline()
    assert line, f"the session ended before its server started (exit {p.wait()})"
    info: dict[str, Any] = json.loads(line)
    return info


def _serves(info: dict[str, Any]) -> bool:
    """The mint answers over TCP and the plugin API over its unix socket."""
    try:
        req = urllib.request.Request(
            f"http://{info['mint']}/internal/plugins/invocations/none", method="DELETE"
        )
        with urllib.request.urlopen(req, timeout=2) as r:
            if json.loads(r.read()) != {"ended": True}:
                return False
        with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as s:
            s.settimeout(2)
            s.connect(info["socket"])
            s.sendall(b"GET / HTTP/1.0\r\n\r\n")
            return s.recv(12).startswith(b"HTTP/1.")
    except OSError:
        return False


def _gone(pid: int, within: float = 5.0) -> bool:
    deadline = time.monotonic() + within
    while time.monotonic() < deadline:
        if not harness._alive(pid):
            return True
        time.sleep(0.05)
    return False


def _wait_serving(info: dict[str, Any]) -> None:
    # "ready" comes once the mint listens; the unix socket is a moment behind
    for _ in range(100):
        if _serves(info):
            return
        time.sleep(0.02)
    raise AssertionError(f"session {info['pid']}'s server does not serve")


@pytest.mark.skipif(os.name != "posix", reason="unix sockets and signals of POSIX")
def test_two_sessions_get_their_own_server_and_build(warm: None) -> None:
    a, b = _start(), _start()  # both build at once
    try:
        ia, ib = _info(a), _info(b)
        assert ia["mint"] != ib["mint"]
        assert ia["socket"] != ib["socket"]
        assert ia["binary"] != ib["binary"]
        assert ia["dir"] != ib["dir"]
        for info in (ia, ib):
            _wait_serving(info)
            # each concurrent build is a whole, working binary
            assert os.access(info["binary"], os.X_OK)

        # A ends as a session ends: its server and folder go, B serves on
        assert a.stdin is not None
        a.stdin.close()
        assert a.wait(10) == 0
        assert _gone(ia["server_pid"])
        assert not Path(ia["dir"]).exists()
        assert _serves(ib)

        # B is killed before its clean-up: its server sees its stdin close and exits, and the
        # next session to start removes the folder it left
        b.send_signal(signal.SIGKILL)
        b.wait(10)
        assert _gone(ib["server_pid"])
        assert Path(ib["dir"]).exists()
        started = subprocess.run(
            [sys.executable, "-c", _NEXT_SESSION],
            cwd=PIPELINE,
            capture_output=True,
            text=True,
            check=True,
        )
        assert not Path(ib["dir"]).exists()
        assert not Path(started.stdout.strip()).exists()  # and its own, at its end
    finally:
        for p in (a, b):
            if p.poll() is None:
                p.kill()
                p.wait(10)


def test_promote_replaces_the_seed_whole(tmp_path: Path) -> None:
    """The seed changes only by a rename over it: no session reads half a binary, and no
    part file is left behind."""
    seed = tmp_path / "x.seed"
    seed.write_bytes(b"old")
    built = tmp_path / "built"
    built.write_bytes(b"new binary")
    harness._promote(built, seed)
    assert seed.read_bytes() == b"new binary"
    assert sorted(p.name for p in tmp_path.iterdir()) == ["built", "x.seed"]


def test_sweep_spares_live_sessions_and_old_folders(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """A folder is removed only when its owner's process is gone: a live session's, or one
    with no owner file, stays."""
    monkeypatch.setattr(tempfile, "gettempdir", lambda: str(tmp_path))
    dead = subprocess.Popen([sys.executable, "-c", "pass"])
    dead.wait()
    for name, owner in (("mkp-dead", dead.pid), ("mkp-live", os.getpid()), ("mkp-old", None)):
        (tmp_path / name).mkdir()
        if owner is not None:
            (tmp_path / name / harness._OWNER).write_text(str(owner))
    if os.name != "posix":
        pytest.skip("the sweep runs on POSIX only")
    harness._sweep()
    assert sorted(p.name for p in tmp_path.iterdir()) == ["mkp-live", "mkp-old"]
