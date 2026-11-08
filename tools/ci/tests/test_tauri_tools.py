"""tools/release/linux-app/tauri-tools.sh: what tauri's AppImage bundler fetches, pinned.

tauri-cli's AppImage bundler fetches AppRun, linuxdeploy and linuxdeploy-plugin-appimage
from GitHub releases, and the plugin's appimagetool fetches the AppImage type2 runtime from
type2-runtime's `continuous` release while it bundles, none of it retried or checked (an
aarch64 bundle failed in linuxdeploy with its output hidden, run 42480410454). The script
fetches each by SHA-256 through fetch-pinned.sh, lays the tools out where tauri looks, and
hands appimagetool the runtime through LDAI_RUNTIME_FILE. Here against a local server, and
the pins file and the workflows that run it, read as they are.
"""

from __future__ import annotations

import hashlib
import http.server
import os
import re
import subprocess
import threading
from collections.abc import Iterator
from pathlib import Path

import pytest
import yaml

ROOT = Path(__file__).resolve().parents[3]
SCRIPT = ROOT / "tools/release/linux-app/tauri-tools.sh"
PINS = ROOT / "tools/release/linux-app/tauri-tools.pins"
BODIES = {
    "AppRun-aarch64": b"apprun\n",
    "linuxdeploy-aarch64.AppImage": b"linuxdeploy\n",
    "linuxdeploy-plugin-appimage-aarch64.AppImage": b"plugin\n",
    "runtime-aarch64": b"type2 runtime\n",
}
KINDS = {
    "apprun": "AppRun-aarch64",
    "linuxdeploy": "linuxdeploy-aarch64.AppImage",
    "plugin": "linuxdeploy-plugin-appimage-aarch64.AppImage",
    "runtime": "runtime-aarch64",
}


def _sha(b: bytes) -> str:
    return hashlib.sha256(b).hexdigest()


class _Server:
    def __init__(self) -> None:
        self.bodies = dict(BODIES)
        self.requests: list[str] = []
        server = self

        class Handler(http.server.BaseHTTPRequestHandler):
            def do_GET(self) -> None:  # noqa: N802 (http.server's name)
                server.requests.append(self.path)
                body = server.bodies.get(self.path.rsplit("/", 1)[-1])
                self.send_response(200 if body is not None else 404)
                out = body if body is not None else b"Not Found"
                self.send_header("Content-Length", str(len(out)))
                self.end_headers()
                self.wfile.write(out)

            def log_message(self, *args: object) -> None:
                pass

        self.httpd = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self.url = f"http://127.0.0.1:{self.httpd.server_address[1]}"
        threading.Thread(target=self.httpd.serve_forever, daemon=True).start()


@pytest.fixture
def server() -> Iterator[_Server]:
    s = _Server()
    yield s
    s.httpd.shutdown()


def _pins(tmp: Path, server: _Server, **sha: str) -> Path:
    lines = [
        f"{kind} aarch64 {sha.get(kind, _sha(BODIES[name]))} {server.url}/rel/{name}" for kind, name in KINDS.items()
    ]
    p = tmp / "tauri-tools.pins"
    p.write_text("# test pins\n" + "\n".join(lines) + "\n", encoding="utf-8")
    return p


def _run(tmp: Path, pins: Path, *args: str) -> subprocess.CompletedProcess[str]:
    env = {k: v for k, v in os.environ.items() if k not in ("GITHUB_TOKEN", "GH_TOKEN")}
    env.update(
        FETCH_PINNED_DELAYS="0 0 0 0",
        TAURI_TOOLS_PINS=str(pins),
        TAURI_TOOLS_DOWNLOADS=str(tmp / "dl"),
        TAURI_TOOLS_DIR=str(tmp / "tauri"),
        GITHUB_ENV=str(tmp / "github_env"),
    )
    return subprocess.run(["bash", str(SCRIPT), *args], capture_output=True, text=True, env=env, timeout=60)


def test_lays_the_tools_out_and_hands_over_the_runtime(server: _Server, tmp_path: Path) -> None:
    r = _run(tmp_path, _pins(tmp_path, server), "aarch64")
    assert r.returncode == 0, r.stderr
    tools = tmp_path / "tauri"
    for name, body in [
        ("AppRun-aarch64", b"apprun\n"),
        ("linuxdeploy-07333c6-aarch64.AppImage", b"linuxdeploy\n"),
        ("linuxdeploy-plugin-appimage.AppImage", b"plugin\n"),
    ]:
        assert (tools / name).read_bytes() == body
        assert os.access(tools / name, os.X_OK), name
    runtime = tmp_path / "dl" / "runtime-aarch64"
    assert runtime.read_bytes() == b"type2 runtime\n"
    assert (tmp_path / "github_env").read_text(encoding="utf-8") == f"LDAI_RUNTIME_FILE={runtime}\n"


def test_a_second_run_downloads_nothing(server: _Server, tmp_path: Path) -> None:
    pins = _pins(tmp_path, server)
    assert _run(tmp_path, pins, "aarch64").returncode == 0
    server.requests.clear()
    r = _run(tmp_path, pins, "aarch64")
    assert r.returncode == 0, r.stderr
    assert server.requests == [], "a restored cache with the pins is not downloaded again"
    assert r.stderr.count("not downloaded") == 4


def test_a_runtime_that_is_not_the_pin_fails(server: _Server, tmp_path: Path) -> None:
    server.bodies["runtime-aarch64"] = b"another runtime\n"
    r = _run(tmp_path, _pins(tmp_path, server), "aarch64")
    assert r.returncode != 0
    assert "::error::" in r.stderr and "runtime-aarch64" in r.stderr
    assert not (tmp_path / "github_env").exists() or "LDAI_RUNTIME_FILE" not in (tmp_path / "github_env").read_text()


def test_a_moved_plugin_warns_and_leaves_it_to_tauri(server: _Server, tmp_path: Path) -> None:
    server.bodies["linuxdeploy-plugin-appimage-aarch64.AppImage"] = b"a newer continuous build\n"
    r = _run(tmp_path, _pins(tmp_path, server), "aarch64")
    assert r.returncode == 0, r.stderr
    assert "::warning::linuxdeploy-plugin-appimage" in r.stdout
    assert not (tmp_path / "tauri" / "linuxdeploy-plugin-appimage.AppImage").exists()
    assert "LDAI_RUNTIME_FILE=" in (tmp_path / "github_env").read_text(encoding="utf-8")


def test_the_key_follows_every_pin(server: _Server, tmp_path: Path) -> None:
    a = _run(tmp_path, _pins(tmp_path, server), "--key", "aarch64").stdout.strip()
    b = _run(tmp_path, _pins(tmp_path, server, runtime="0" * 64), "--key", "aarch64").stdout.strip()
    assert re.fullmatch(r"pinned-linux-tauri-tools-aarch64-[0-9a-f]{16}", a)
    assert a != b


def test_an_unknown_arch_fails(server: _Server, tmp_path: Path) -> None:
    r = _run(tmp_path, _pins(tmp_path, server), "riscv64")
    assert r.returncode != 0 and "no tauri tool pins for riscv64" in r.stderr


# --- the pins and the workflows, as they are ----------------------------------------------


def _pin_lines() -> list[list[str]]:
    return [line.split() for line in PINS.read_text(encoding="utf-8").splitlines() if line and not line.startswith("#")]


def test_every_kind_is_pinned_for_both_arches() -> None:
    got = {(k, a) for k, a, _, _ in _pin_lines()}
    assert got == {(k, a) for k in KINDS for a in ("x86_64", "aarch64")}
    for kind, arch, sha, url in _pin_lines():
        assert re.fullmatch(r"[0-9a-f]{64}", sha), (kind, arch)
        assert url.startswith("https://github.com/") and arch in url.rsplit("/", 1)[-1], (kind, url)


def test_the_runtime_is_a_tagged_build_not_continuous() -> None:
    for kind, _, _, url in _pin_lines():
        if kind == "runtime":
            assert "/AppImage/type2-runtime/releases/download/" in url
            assert "/continuous/" not in url, url


def _steps(workflow: str, job: str) -> list[dict[str, object]]:
    doc = yaml.safe_load((ROOT / ".github/workflows" / workflow).read_text(encoding="utf-8"))
    return doc["jobs"][job]["steps"]


@pytest.mark.parametrize(("workflow", "job"), [("_linux-app.yml", "payload"), ("desktop-release.yml", "desktop")])
def test_the_linux_bundles_run_it_before_bundling(workflow: str, job: str) -> None:
    steps = _steps(workflow, job)
    runs = [str(s.get("run", "")) for s in steps]
    tools = next(i for i, r in enumerate(runs) if "tools/release/linux-app/tauri-tools.sh" in r and "--key" not in r)
    bundle = next(i for i, r in enumerate(runs) if "make linux-app" in r or "linux-app/bundle.sh" in r)
    assert tools < bundle
    env = steps[bundle].get("env") or {}
    assert isinstance(env, dict) and str(env.get("LDAI_VERBOSE")) == "1", "appimagetool's own output (-v)"
    assert "GITHUB_TOKEN" in (steps[tools].get("env") or {}), "the API's route needs the job's token"


def test_bundle_sh_is_run_verbose() -> None:
    assert '"$SCRIPT_DIR/bundle.sh" "$version" --verbose' in (ROOT / "tools/release/linux-app/build-app.sh").read_text()
    assert "linux-app/bundle.sh \"$TAG\" --verbose" in (ROOT / ".github/workflows/desktop-release.yml").read_text()
