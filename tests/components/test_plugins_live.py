"""plugins-live, a whole server (Epic 54 Phase 1, done means): ``maktaba-server serve`` of
a whole home on a scratch Postgres installs a plugin from a file, an admin grants it and
turns it on, its job runs in the queue in the sandboxed maktaba-plugind, its health and its
own log lines are there, and the admin turns it off and removes it.

The plugin is the host's well-behaved test guest (``plugind/guests/hello``, built by ``make
plugind-guests``), packed with ``shared/go/componentpack/cmd/mkplugin``; its one stage takes
the catalog subject, so the test queues it a job with no media.

    cd pipeline && MAKTABA_TEST_PG_URL=... MAKTABA_COMPONENTS_SERVER_BIN=<a folder with
    maktaba-server, maktaba-api, maktaba-streaming and maktaba-plugind> \\
        uv run --extra dev pytest ../tests/components/test_plugins_live.py
"""

from __future__ import annotations

import json
import os
import subprocess
import sys
import time
import urllib.error
import urllib.request
import uuid
from pathlib import Path
from typing import Any

import pytest

from tests.components import rig as R
from tests.components import test_server_live as live

BIN_DIR = os.environ.get("MAKTABA_COMPONENTS_SERVER_BIN", "")
GUEST = Path(
    os.environ.get(
        "MAKTABA_PLUGIN_GUEST",
        str(R.ROOT / "plugind" / "guests" / "target" / "wasm32-wasip2" / "release" / "hello.wasm"),
    )
)
PID = "dev.example.hello"
CID = "plugin." + PID
#: the catalog subject (processing_jobs_subject_chk)
CATALOG = "00000000-0000-0000-0000-00000000ca7a"

pytestmark = [
    pytest.mark.skipif(not R.PG_URL, reason="MAKTABA_TEST_PG_URL not set"),
    pytest.mark.skipif(not BIN_DIR, reason="MAKTABA_COMPONENTS_SERVER_BIN not set"),
    pytest.mark.skipif(sys.platform == "win32", reason="plugins do not run on Windows yet"),
]

MANIFEST = f"""schema = 1
id = "{PID}"
version = "1.0.0"
plugin_api = "1.0"
maktaba = ">=0.0.1"
licence = "MIT"

[strings]
name = "plugin.name"
summary = "plugin.summary"

[resources]
timeout_s = 30

[[stage]]
name = "hello"
subject = "catalog"
export = "run-hello"
version = 1
"""


def _package(work: Path) -> Path:
    """The hello guest as a .mkplugin."""
    if not GUEST.is_file():
        pytest.skip(f"{GUEST} is missing: run `make plugind-guests`")
    src = work / "hello-src"
    (src / "i18n").mkdir(parents=True, exist_ok=True)
    (src / "manifest.toml").write_text(MANIFEST, encoding="utf-8")
    (src / "i18n" / "en.json").write_text(
        json.dumps({"plugin.name": "Hello", "plugin.summary": "Says hello."}), encoding="utf-8"
    )
    (src / "i18n" / "ar.json").write_text(
        json.dumps({"plugin.name": "مرحبا", "plugin.summary": "تقول مرحبا."}, ensure_ascii=False),
        encoding="utf-8",
    )
    (src / "plugin.wasm").write_bytes(GUEST.read_bytes())
    out = work / "hello.mkplugin"
    subprocess.run(  # noqa: S603 - our own packer
        ["go", "run", "./cmd/mkplugin", str(src), str(out)],  # noqa: S607
        cwd=str(R.ROOT / "shared" / "go" / "componentpack"),
        check=True,
        capture_output=True,
    )
    return out


def _install(api: R.Api, pkg: Path) -> Any:
    """POST /api/plugins/install as the Plugins page sends it: the fields, then the file."""
    boundary = "----maktaba" + uuid.uuid4().hex
    parts: list[bytes] = []
    for name, value in (("enable", "true"), ("grants", "[]"), ("libraries", "[]")):
        parts.append(
            f'--{boundary}\r\nContent-Disposition: form-data; name="{name}"\r\n\r\n{value}\r\n'.encode()
        )
    parts.append(
        f'--{boundary}\r\nContent-Disposition: form-data; name="file"; filename="hello.mkplugin"\r\n'
        "Content-Type: application/octet-stream\r\n\r\n".encode()
        + pkg.read_bytes()
        + b"\r\n"
    )
    parts.append(f"--{boundary}--\r\n".encode())
    req = urllib.request.Request(api.url + "/api/plugins/install", data=b"".join(parts), method="POST")  # noqa: S310
    req.add_header("Content-Type", f"multipart/form-data; boundary={boundary}")
    req.add_header("Accept", "application/json")
    req.add_header("X-Maktaba-CSRF", api._csrf())
    try:
        with api.opener.open(req, timeout=120) as resp:
            return json.loads(resp.read())
    except urllib.error.HTTPError as e:
        raise R.ApiError(e.code, e.read().decode("utf-8", "replace")) from None


def _plugind(supervisor: str) -> dict[str, Any]:
    for p in live._get(supervisor + "/processes")["processes"]:
        if p.get("role") == "plugins":
            return dict(p)
    return {}


def test_a_plugin_from_a_file_runs_in_the_queue_and_goes() -> None:
    R.LOGS.mkdir(parents=True, exist_ok=True)
    home = R.LOGS / "home" / "plugins-live"
    (home / "data").mkdir(parents=True, exist_ok=True)
    log = R.LOGS / "plugins-live.log"
    m: dict[str, Any] = {}
    ports = live._free_ports(12)
    api_url, supervisor = f"http://127.0.0.1:{ports[0]}", f"http://127.0.0.1:{ports[10]}"
    pkg = _package(home)
    with live._empty_database() as db:
        config = live._server_toml(home, db, ports)
        env = live._server_env(home, config, R.LOGS / "plugins-live-ollama.jsonl")
        env["MAKTABA_PLUGIND_BIN"] = str(Path(BIN_DIR) / "maktaba-plugind")
        server = subprocess.Popen(  # noqa: S603 - our own binary
            [str(Path(BIN_DIR) / "maktaba-server"), "serve", "--bin-dir", BIN_DIR, "--no-web"],
            stdout=log.open("ab"),
            stderr=subprocess.STDOUT,
            env=env,
            cwd=str(R.ROOT),
            start_new_session=True,
        )
        try:
            _run(server, db, home, api_url, supervisor, pkg, m)
        finally:
            (R.LOGS / "plugins-live.json").write_text(json.dumps(m, indent=1), encoding="utf-8")
            live._stop(server)


def _run(
    server: subprocess.Popen[bytes],
    db: str,
    home: Path,
    api_url: str,
    supervisor: str,
    pkg: Path,
    m: dict[str, Any],
) -> None:
    until = live._until
    until("the API", lambda: live._answers(api_url + "/api/system/health"), 120, server)
    pw = home / "owner.pw"
    pw.write_text(R.OWNER_PASS + "\n", encoding="utf-8")
    add = subprocess.run(  # noqa: S603 - our own binary
        [str(Path(BIN_DIR) / "maktaba-api"), "adduser", "--password-file", str(pw), R.OWNER],
        env={**os.environ, "DATABASE_URL": db, "HOME": str(home)},
        capture_output=True,
        check=False,
    )
    assert add.returncode == 0, add.stderr
    api = R.Api(api_url, "", server, R.LOGS / "plugins-live.log")
    api.sign_in()
    until("the pipeline", lambda: live._rows(db, "SELECT 1 FROM job_stages LIMIT 1"), 180, server)

    # no plugin: the host is idle and runs no process (N14)
    host = api.component("plugins.host")
    m["host_before"] = {"phase": host["phase"]}
    assert _plugind(supervisor).get("pid", 0) == 0

    # the admin allows unreviewed plugins, installs the file and turns it on
    api.call("PUT", "/api/plugins/policy", {"allow_unreviewed": True})
    t0 = time.time()
    out = _install(api, pkg)
    m["install_s"] = round(time.time() - t0, 3)
    assert out["result"]["plugin_id"] == PID and out["result"]["enabled"], out
    stage = out["plugin"]["stages"][0]["queue"]
    m["stage"] = stage

    # the supervisor starts the host, the pipeline worker loads the plugin's stage
    m["host_running_s"] = until(
        "maktaba-plugind running", lambda: _plugind(supervisor).get("state") == "running", 90, server
    )
    until(
        "the plugin's stage registered",
        lambda: live._rows(db, "SELECT 1 FROM job_stages WHERE stage = $1", stage),
        180,
        server,
    )
    m["plugin_running_s"] = until(
        "the plugin running", lambda: api.get(f"/api/plugins/{PID}")["phase"] == "running", 120, server
    )

    # its job runs in the queue
    live._rows(
        db,
        "INSERT INTO processing_jobs (stage, subject_kind, subject_id, state)"
        " VALUES ($1, 'catalog', $2, 'pending') RETURNING id",
        stage,
        CATALOG,
    )
    m["job_done_s"] = until(
        "the plugin's job done",
        lambda: live._rows(
            db, "SELECT 1 FROM processing_jobs WHERE stage = $1 AND state = 'done'", stage
        ),
        180,
        server,
    )
    detail = api.get(f"/api/plugins/{PID}")
    m["health"] = detail["health"]
    assert detail["health"]["jobs_done"] >= 1 and detail["names"]["name"]["ar"] == "مرحبا"
    # its own log lines, in segments of their own (plugin.<id>)
    seg = home / "data" / "logs" / "segments" / CID
    until("the plugin's log segment", lambda: any(seg.glob("*.ndjson*")), 60, server)

    # turned off: kept off, the host stops (nothing else needs it)
    api.call("POST", f"/api/plugins/{PID}/disable")
    until(
        "the plugin off, kept off",
        lambda: (
            (d := api.get(f"/api/plugins/{PID}"))["phase"] == "off" and d.get("kept_off") is not None
        ),
        60,
        server,
    )
    m["host_stopped_s"] = until(
        "maktaba-plugind stopped", lambda: _plugind(supervisor).get("pid", 0) == 0, 90, server
    )

    # removed: its files and install row go, its data stays
    api.call("DELETE", f"/api/plugins/{PID}")
    until(
        "the plugin uninstalled",
        lambda: not live._rows(db, "SELECT 1 FROM plugin_installs WHERE plugin_id = $1", PID),
        120,
        server,
    )
    left = [p for p in home.rglob(PID) if p.is_dir() and p.parent.name == "plugins"]
    assert not left, f"the plugin's files stayed: {left}"
    m["ok"] = True
