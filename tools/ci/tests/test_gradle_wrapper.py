"""tools/ci/gradle-wrapper.sh: the Gradle wrapper's jar check and distribution fetch in CI.

The wrapper's own download of services.gradle.org (one try, a 10 s timeout) failed the desktop
Player on Linux and Windows on 2026-11-08, and the jar check's curl of Gradle's checksum had
no retry either. The script fetches the checksum with retries, and the distribution through
fetch-pinned.sh (retried, checked against distributionSha256Sum) into the folder the wrapper
reads, named by the MD5 of distributionUrl in base 36. Here against a local server.
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

ROOT = Path(__file__).resolve().parents[3]
SCRIPT = ROOT / "tools/ci/gradle-wrapper.sh"
JAR = b"wrapper jar bytes"
ZIP = b"gradle distribution zip" * 50
PROJECTS = ["apps/android", "apps/tv/android", "apps/desktop-player"]


def _base36(hexdigest: str) -> str:
    n, out = int(hexdigest, 16), ""
    while True:
        n, r = divmod(n, 36)
        out = "0123456789abcdefghijklmnopqrstuvwxyz"[r] + out
        if n == 0:
            return out


class _Server:
    def __init__(self) -> None:
        self.replies: list[int] = []
        self.paths: list[str] = []
        server = self

        class Handler(http.server.BaseHTTPRequestHandler):
            def do_GET(self) -> None:  # noqa: N802 (http.server's name)
                server.paths.append(self.path)
                bodies = {
                    "/distributions/gradle-1.2-wrapper.jar.sha256": hashlib.sha256(JAR).hexdigest().encode(),
                    "/distributions/gradle-1.2-bin.zip": ZIP,
                }
                status = 404 if self.path not in bodies else (server.replies.pop(0) if server.replies else 200)
                body = bodies[self.path] if status == 200 else b"no"
                self.send_response(status)
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)

            def log_message(self, *args: object) -> None:
                pass

        self.httpd = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self.url = f"http://127.0.0.1:{self.httpd.server_address[1]}/distributions/gradle-1.2-bin.zip"
        threading.Thread(target=self.httpd.serve_forever, daemon=True).start()


@pytest.fixture
def server() -> Iterator[_Server]:
    s = _Server()
    yield s
    s.httpd.shutdown()


def _project(tmp_path: Path, url: str, sha: str = hashlib.sha256(ZIP).hexdigest(), jar: bytes = JAR) -> Path:
    wrapper = tmp_path / "proj/gradle/wrapper"
    wrapper.mkdir(parents=True)
    (wrapper / "gradle-wrapper.jar").write_bytes(jar)
    escaped = url.replace(":", "\\:")
    (wrapper / "gradle-wrapper.properties").write_text(
        f"distributionBase=GRADLE_USER_HOME\ndistributionSha256Sum={sha}\ndistributionUrl={escaped}\nnetworkTimeout=60000\n"
    )
    return tmp_path / "proj"


def _base(server: _Server) -> str:
    return server.url.rsplit("/distributions/", 1)[0]


def _run(*args: str, home: Path, base: str = "") -> subprocess.CompletedProcess[str]:
    env = {
        **os.environ,
        "GRADLE_USER_HOME": str(home),
        "GRADLE_WRAPPER_DELAYS": "0 0 0 0",
        "FETCH_PINNED_DELAYS": "0 0 0 0",
        "RUNNER_OS": "Linux",
        "GRADLE_WRAPPER_DISTRIBUTIONS": f"{base}/distributions" if base else "https://services.gradle.org/distributions",
    }
    return subprocess.run(["bash", str(SCRIPT), *args], capture_output=True, text=True, env=env, timeout=60)


def test_dist_puts_the_zip_where_the_wrapper_looks(server: _Server, tmp_path: Path) -> None:
    proj = _project(tmp_path, server.url)
    r = _run("dist", str(proj), str(tmp_path / "cache"), home=tmp_path / "guh", base=_base(server))
    assert r.returncode == 0, r.stderr
    dist = tmp_path / "guh/wrapper/dists/gradle-1.2-bin" / _base36(hashlib.md5(server.url.encode()).hexdigest())
    assert (dist / "gradle-1.2-bin.zip").read_bytes() == ZIP
    assert (tmp_path / "cache/gradle-1.2-bin.zip").read_bytes() == ZIP


def test_dist_retries_a_504(server: _Server, tmp_path: Path) -> None:
    server.replies = [504, 504]
    proj = _project(tmp_path, server.url)
    r = _run("dist", str(proj), str(tmp_path / "cache"), home=tmp_path / "guh", base=_base(server))
    assert r.returncode == 0, r.stderr
    assert server.paths.count("/distributions/gradle-1.2-bin.zip") == 3


def test_dist_refuses_other_bytes(server: _Server, tmp_path: Path) -> None:
    other = hashlib.sha256(b"x").hexdigest()
    proj = _project(tmp_path, server.url, sha=other)
    r = _run("dist", str(proj), str(tmp_path / "cache"), home=tmp_path / "guh", base=_base(server))
    assert r.returncode != 0
    assert f"the pin says {other}" in r.stderr
    assert not list((tmp_path / "guh").rglob("*.zip")) if (tmp_path / "guh").exists() else True


def test_dist_keeps_a_cached_zip(server: _Server, tmp_path: Path) -> None:
    proj = _project(tmp_path, server.url)
    (tmp_path / "cache").mkdir()
    (tmp_path / "cache/gradle-1.2-bin.zip").write_bytes(ZIP)
    r = _run("dist", str(proj), str(tmp_path / "cache"), home=tmp_path / "guh", base=_base(server))
    assert r.returncode == 0, r.stderr
    assert server.paths == []


def test_dist_fetches_nothing_when_the_wrapper_has_it_installed(tmp_path: Path) -> None:
    # the real URL's folder, as the wrapper named it in CI's log (8wepis63...)
    proj = ROOT / "apps/desktop-player"
    dist = tmp_path / "guh/wrapper/dists/gradle-9.8.2-bin/8wepis63pzuhrlfaiy04bagwu"
    dist.mkdir(parents=True)
    (dist / "gradle-9.8.2-bin.zip.ok").write_text("")
    r = _run("dist", str(proj), str(tmp_path / "cache"), home=tmp_path / "guh")
    assert r.returncode == 0, r.stderr
    assert "nothing to fetch" in r.stdout
    assert not (tmp_path / "cache").exists()


def test_dist_needs_a_distribution_sha256(server: _Server, tmp_path: Path) -> None:
    proj = _project(tmp_path, server.url, sha="")
    r = _run("dist", str(proj), str(tmp_path / "cache"), home=tmp_path / "guh", base=_base(server))
    assert r.returncode != 0
    assert "no distributionSha256Sum" in r.stderr


def test_check_accepts_gradle_s_jar_after_retries(server: _Server, tmp_path: Path) -> None:
    server.replies = [504, 503]
    proj = _project(tmp_path, server.url)
    r = _run("check", str(proj), home=tmp_path / "guh", base=_base(server))
    assert r.returncode == 0, r.stderr
    assert server.paths == ["/distributions/gradle-1.2-wrapper.jar.sha256"] * 3
    assert "again in 0 s" in r.stderr


def test_check_refuses_another_jar(server: _Server, tmp_path: Path) -> None:
    proj = _project(tmp_path, server.url, jar=b"not gradle's")
    r = _run("check", str(proj), home=tmp_path / "guh", base=_base(server))
    assert r.returncode != 0
    assert "is not Gradle's" in r.stderr


def test_check_gives_up_after_five_tries(server: _Server, tmp_path: Path) -> None:
    server.replies = [504] * 10
    proj = _project(tmp_path, server.url)
    r = _run("check", str(proj), home=tmp_path / "guh", base=_base(server))
    assert r.returncode != 0
    assert "did not answer after 5 tries" in r.stderr


def test_the_key_names_the_distribution_and_its_pin(tmp_path: Path) -> None:
    r = _run("--key", str(ROOT / "apps/desktop-player"), home=tmp_path)
    assert r.returncode == 0, r.stderr
    assert re.fullmatch(r"pinned-linux-gradle-9\.8\.2-bin\.zip-[0-9a-f]{16}\n", r.stdout)


@pytest.mark.parametrize("project", PROJECTS)
def test_each_wrapper_retries_and_pins_its_distribution(project: str) -> None:
    props = dict(
        line.split("=", 1)
        for line in (ROOT / project / "gradle/wrapper/gradle-wrapper.properties").read_text().splitlines()
        if "=" in line
    )
    assert re.fullmatch(r"[0-9a-f]{64}", props.get("distributionSha256Sum", "")), f"{project} pins no distribution"
    assert int(props["networkTimeout"]) >= 60000
    assert int(props["retries"]) >= 4


def test_the_wrappers_pin_the_same_distribution() -> None:
    pins = {
        (ROOT / p / "gradle/wrapper/gradle-wrapper.properties").read_text().split("distributionSha256Sum=")[1].split()[0]
        + (ROOT / p / "gradle/wrapper/gradle-wrapper.properties").read_text().split("distributionUrl=")[1].split()[0]
        for p in PROJECTS
    }
    assert len(pins) == 1, pins


@pytest.mark.parametrize(
    ("workflow", "project"),
    [("_android-apps.yml", "apps/android"), ("_android-tv.yml", "apps/tv/android"), ("_desktop-player.yml", "apps/desktop-player")],
)
def test_the_gradle_jobs_check_and_fetch_before_the_build(workflow: str, project: str) -> None:
    text = (ROOT / ".github/workflows" / workflow).read_text(encoding="utf-8")
    check = text.find(f"tools/ci/gradle-wrapper.sh check {project}")
    dist = text.find(f"tools/ci/gradle-wrapper.sh dist {project} build/ci-pinned/gradle")
    build = text.find("./gradlew --no-daemon")
    assert -1 < check < dist < build, workflow
    assert "services.gradle.org" not in text


@pytest.mark.parametrize(
    "url",
    [
        "https://evil.example/distributions/gradle-9.8.2-bin.zip",
        "https://services.gradle.org.evil.example/distributions/gradle-9.8.2-bin.zip",
        "https://services.gradle.org/distributions/gradle-9.8.2-src.zip",
        "https://services.gradle.org/distributions/gradle-x/../9-bin.zip",
    ],
)
@pytest.mark.parametrize("mode", ["check", "dist"])
def test_a_distribution_off_gradle_s_host_is_refused(tmp_path: Path, url: str, mode: str) -> None:
    proj = _project(tmp_path, url)
    args = [mode, str(proj)] + ([str(tmp_path / "cache")] if mode == "dist" else [])
    r = _run(*args, home=tmp_path / "guh")
    assert r.returncode != 0
    assert "distributionUrl" in r.stderr
    assert not (tmp_path / "cache").exists()
