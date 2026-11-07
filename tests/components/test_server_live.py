"""components-live, a whole server (Epic 51 story 51.04, N3; #641): ``maktaba-server serve``
of the whole machine on a scratch database, its llm component's process a stand-in
(``fakeollama/ollama``), switched off and on through the Components page's calls.

The supervisor must stop the process within the component's drain grace plus 10 s (the
kill time), and start it again when it is on, with no restart of the server; the status
rows and the audit rows say so. The server's log (``server-live.log``), the stand-in's
starts and stops and the measurements (``components-server-live.json``) are kept with the
other components logs.

    cd pipeline && MAKTABA_TEST_PG_URL=... MAKTABA_COMPONENTS_SERVER_BIN=<a folder with
    maktaba-server, maktaba-api and maktaba-streaming> \
        uv run --extra dev pytest ../tests/components/test_server_live.py
"""

from __future__ import annotations

import contextlib
import json
import os
import signal
import subprocess
import sys
import time
import urllib.error
import urllib.request
import uuid
from collections.abc import Iterator
from pathlib import Path
from typing import Any

import pytest

from components import rig as R

#: the folder holding maktaba-server, maktaba-api and maktaba-streaming built from the commit
BIN_DIR = os.environ.get("MAKTABA_COMPONENTS_SERVER_BIN", "")

pytestmark = [
    pytest.mark.skipif(not R.PG_URL, reason="MAKTABA_TEST_PG_URL not set"),
    pytest.mark.skipif(not BIN_DIR, reason="MAKTABA_COMPONENTS_SERVER_BIN not set"),
    pytest.mark.skipif(sys.platform == "win32", reason="the stand-in is a POSIX script"),
]

FAKE_OLLAMA = Path(__file__).resolve().parent / "fakeollama" / "ollama"
#: the supervisor kills a process turned off after its drain grace plus 10 s (catalog
#: default drain grace: llm names none)
STOP_LIMIT_S = 30 + 10


def _free_ports(n: int) -> list[int]:
    """n distinct free ports: every socket stays bound until all are picked, so the OS
    cannot hand one port out twice (the server refuses two parts on one port)."""
    import socket

    socks = []
    try:
        for _ in range(n):
            s = socket.socket()
            s.bind(("127.0.0.1", 0))
            socks.append(s)
        return [int(s.getsockname()[1]) for s in socks]
    finally:
        for s in socks:
            s.close()


@contextlib.contextmanager
def _empty_database() -> Iterator[str]:
    """A new, empty database (the server's API migrates it, as at a first start)."""
    import asyncio

    import asyncpg  # type: ignore[import-untyped]

    name = "maktaba_server_" + uuid.uuid4().hex[:8]

    async def admin(sql: str) -> None:
        conn = await asyncpg.connect(R.PG_URL)
        try:
            await conn.execute(sql)
        finally:
            await conn.close()

    asyncio.run(admin(f'CREATE DATABASE "{name}"'))
    url = R._swap_db(R.PG_URL.split("?", 1)[0], name) + "?sslmode=disable"
    try:
        yield url
    finally:
        asyncio.run(admin(f'DROP DATABASE IF EXISTS "{name}" WITH (FORCE)'))


def _server_toml(home: Path, db: str, ports: list[int]) -> Path:
    p = iter(ports)

    def addr() -> str:
        return f"127.0.0.1:{next(p)}"

    text = f"""
[server]
listen = "{addr()}"
web_listen = "{addr()}"
admin_addr = "{addr()}"
stream_addr = "{addr()}"
streaming_grpc_addr = "{addr()}"
streaming_admin_addr = "{addr()}"
pipeline_grpc_addr = "{addr()}"
pipeline_logs_addr = "{addr()}"
pipeline_search_addr = "{addr()}"
pipeline_index_logs_addr = "{addr()}"
data_dir = "{home / "data"}"
mdns = "off"

[database]
url = "{db}"

[postgres]
enabled = "off"

[supervisor]
admin_addr = "{addr()}"

[pipeline]
instances = 0
index_worker = "off"

[llm]
enabled = "on"
binary = "{FAKE_OLLAMA}"
port = {next(p)}
models = ["qwen2.5:7b"]
pull = "off"

[telemetry]
enabled = "off"

[tv]
slots = "off"
"""
    path = home / "server.toml"
    path.write_text(text, encoding="utf-8")
    return path


def _server_env(home: Path, config: Path, starts: Path) -> dict[str, str]:
    env = {
        k: v
        for k, v in os.environ.items()
        if not k.startswith(("MAKTABA_", "XDG_")) and k not in ("HOME", "DATABASE_URL")
    }
    env.update(
        {
            "HOME": str(home),
            "MAKTABA_CONFIG": str(config),
            "MAKTABA_PYTHON": sys.executable,
            "MAKTABA_UPDATE_DISABLE": "1",
            "MAKTABA_TELEMETRY_URL": "off",
            "MAKTABA_DISCOVERY_MDNS": "0",
            "MAKTABA_LOG_FORMAT": "json",
            "FAKE_OLLAMA_LOG": str(starts),
        }
    )
    return env


def _get(url: str) -> Any:
    with urllib.request.urlopen(url, timeout=5) as resp:  # noqa: S310 - our own server
        return json.loads(resp.read() or b"null")


def _ollama(supervisor: str) -> dict[str, Any]:
    """The supervisor's row of the llm process (/processes)."""
    for p in _get(supervisor + "/processes")["processes"]:
        if p.get("role") == "ollama":
            return dict(p)
    return {}


def _events(starts: Path) -> list[dict[str, Any]]:
    if not starts.exists():
        return []
    return [json.loads(line) for line in starts.read_text(encoding="utf-8").splitlines() if line]


def _until(what: str, cond: Any, timeout: float, proc: subprocess.Popen[bytes]) -> float:
    t0 = time.monotonic()
    while True:
        try:
            if cond():
                return time.monotonic() - t0
        except (OSError, urllib.error.URLError, KeyError, ValueError):
            pass
        if proc.poll() is not None:
            raise RuntimeError(f"the server exited while waiting for {what}")
        if time.monotonic() - t0 > timeout:
            raise TimeoutError(f"{what}: not after {timeout}s")
        time.sleep(0.2)


def test_a_process_component_off_and_on_in_a_whole_server() -> None:
    R.LOGS.mkdir(parents=True, exist_ok=True)
    home = R.LOGS / "home" / "server-live"
    (home / "data").mkdir(parents=True, exist_ok=True)
    starts = R.LOGS / "server-live-ollama.jsonl"
    log = R.LOGS / "server-live.log"
    m: dict[str, Any] = {}
    ports = _free_ports(12)
    api_url, supervisor = f"http://127.0.0.1:{ports[0]}", f"http://127.0.0.1:{ports[10]}"
    with _empty_database() as db:
        config = _server_toml(home, db, ports)
        server = subprocess.Popen(  # noqa: S603 - our own binary
            [str(Path(BIN_DIR) / "maktaba-server"), "serve", "--bin-dir", BIN_DIR, "--no-web"],
            stdout=log.open("ab"),
            stderr=subprocess.STDOUT,
            env=_server_env(home, config, starts),
            cwd=str(R.ROOT),  # the API migrates from shared/db/migrations
            start_new_session=True,
        )
        try:
            _run(server, db, home, api_url, supervisor, starts, m)
        finally:
            (R.LOGS / "components-server-live.json").write_text(
                json.dumps(m, indent=1), encoding="utf-8"
            )
            _stop(server)


def _run(
    server: subprocess.Popen[bytes],
    db: str,
    home: Path,
    api_url: str,
    supervisor: str,
    starts: Path,
    m: dict[str, Any],
) -> None:
    # the server up: the stand-in started by the supervisor, the API migrated and answering
    _until("the llm process", lambda: _ollama(supervisor).get("state") == "running", 120, server)
    _until("the API", lambda: _answers(api_url + "/api/system/health"), 120, server)
    first = _ollama(supervisor)["pid"]
    pw = home / "owner.pw"
    pw.write_text(R.OWNER_PASS + "\n", encoding="utf-8")
    add = subprocess.run(  # noqa: S603 - our own binary
        [str(Path(BIN_DIR) / "maktaba-api"), "adduser", "--password-file", str(pw), R.OWNER],
        env={**os.environ, "DATABASE_URL": db, "HOME": str(home)},
        capture_output=True,
        check=False,
    )
    assert add.returncode == 0, add.stderr
    api = R.Api(api_url, "", server, R.LOGS / "server-live.log")
    api.sign_in()
    # the boot's seed has written the desired rows (the toggle needs llm's generation)
    _until("llm's desired row", lambda: api.component("llm").get("has_row"), 60, server)

    # -- off: the process stops within its grace, the status says off --------------
    t_off = time.time()
    api.set_want("llm", "installed")
    _until(
        "the llm process stopped",
        lambda: any(e["event"] == "stopped" and e["pid"] == first for e in _events(starts)),
        STOP_LIMIT_S + 5,
        server,
    )
    stopped = next(e for e in _events(starts) if e["event"] == "stopped" and e["pid"] == first)
    m["N3_stop_s"] = round(stopped["at"] - t_off, 3)
    assert m["N3_stop_s"] <= STOP_LIMIT_S
    _until("the process held", lambda: _ollama(supervisor)["state"] != "running", 15, server)
    m["off_phase_s"] = _until(
        "llm off", lambda: api.component("llm")["phase"] in ("off", "stopped"), 15, server
    )
    off = api.component("llm")
    _until(
        "the supervisor's row off",
        lambda: _status(db, "llm") == (off["generation"], "off"),
        15,
        server,
    )
    m["off_row"] = {"phase": off["phase"], "generation": off["generation"]}
    time.sleep(3)
    assert _ollama(supervisor)["state"] != "running", "the process came back while off"

    # -- on again: a new process, no server restart --------------------------------
    t_on = time.time()
    api.set_want("llm", "enabled")
    m["N3_running_s"] = _until(
        "the llm process again",
        lambda: (
            _ollama(supervisor).get("state") == "running" and _ollama(supervisor)["pid"] != first
        ),
        60,
        server,
    )
    on = api.component("llm")
    _until(
        "llm's status at the new generation",
        lambda: (
            _status(db, "llm")[0] == api.component("llm")["generation"]
            and _status(db, "llm")[1] not in ("off", "stopped")
        ),
        30,
        server,
    )
    m["on_row"] = {"phase": _status(db, "llm")[1], "generation": on["generation"]}
    started = [e for e in _events(starts) if e["event"] == "started"]
    assert len(started) == 2, started  # the first start and the one after on: no restarts
    m["N3_start_s"] = round(started[1]["at"] - t_on, 3)
    assert server.poll() is None

    # -- the audit rows: one disable and one enable, by the admin, from the page -----
    audit = [a for a in api.component("llm").get("audit", []) if a["action"] != "component.seed"]
    m["audit"] = [(a["action"], a.get("actor_source")) for a in audit]
    assert [a["action"] for a in audit][:2] == ["component.enable", "component.disable"]
    assert all(a.get("actor_source") == "web" for a in audit[:2])


def _answers(url: str) -> bool:
    try:
        urllib.request.urlopen(url, timeout=3)  # noqa: S310 - our own server
    except urllib.error.HTTPError:
        return True  # it answers (401 before a sign-in)
    return True


def _rows(db: str, sql: str, *args: Any) -> list[dict[str, Any]]:
    import asyncio

    import asyncpg

    async def q() -> list[dict[str, Any]]:
        conn = await asyncpg.connect(db)
        try:
            return [dict(r) for r in await conn.fetch(sql, *args)]
        finally:
            await conn.close()

    return asyncio.run(q())


def _status(db: str, cid: str) -> tuple[int, str]:
    """The supervisor's status row of cid on the home: (observed generation, phase)."""
    rows = _rows(
        db,
        "SELECT observed_generation, phase FROM component_status"
        " WHERE machine = 'home' AND id = $1 AND part = 'supervisor'",
        cid,
    )
    return (int(rows[0]["observed_generation"]), str(rows[0]["phase"])) if rows else (-1, "")


def _stop(server: subprocess.Popen[bytes]) -> None:
    if server.poll() is None:
        with contextlib.suppress(ProcessLookupError):
            os.killpg(server.pid, signal.SIGTERM)
        try:
            server.wait(timeout=60)
        except subprocess.TimeoutExpired:
            with contextlib.suppress(ProcessLookupError):
                os.killpg(server.pid, signal.SIGKILL)
            server.wait()
