#!/usr/bin/env python3
"""Run one nas-shapes cell (story 59.15): the server image started in one NAS shape.

    cell.py --image IMAGE --cell NAME --fixtures DIR --out DIR [--local PORT] [--keep]

A cell (tests/nas/cells.json) is a shape a NAS gives the container: another uid, a data
folder owned by someone else, a read-only or foreign-owned library, a memory limit, a
seccomp profile without Landlock. The cell:

  1. makes its volumes with a root helper (the image itself, as uid 0): the data folder
     with the cell's owner and mode, the library (the fixture clips copied in) with its
     owner and mode, and a root nobody may list when the cell asks for one;
  2. writes server.toml first (a small speech model, no second listen, no language model),
     then runs setup headlessly in a throwaway container (`setup --answers /dev/stdin`,
     what `docker compose run --rm maktaba setup` runs), the answers on stdin;
  3. starts the server container, waits for ready (the public health route, one sign-in,
     then every role running) and checks the cell's assertions through the admin API;
  4. keeps everything, pass or fail, in --out: the container's log, `docker inspect`, the
     setup log, the server's and each worker's log and plugind's (state/maktaba/logs and
     data/maktaba/logs from the volume), the launcher's log, the API's view
     (/api/system/processes, /api/libraries, /api/server/machine), report.json and
     summary.md.

The API is reached on 127.0.0.1: on CI the cell runs on the host network, a NAS's default.
--local PORT is for a developer's Docker Desktop, which has no host network: the cell runs
on a bridge network with PORT published, and a relay in the cell's network namespace hands
it to 127.0.0.1:8088 inside, so the server sees a local client either way.

An assertion or a cell whose `needs` are not all in cells.json's `landed` is skipped and
reported as pending. Exit 0 when every assertion that ran passed, 1 otherwise, 2 on a
usage error.
"""

from __future__ import annotations

import argparse
import json
import os
import re
import secrets
import shlex
import subprocess
import sys
import time
import traceback
import urllib.error
import urllib.request
from dataclasses import dataclass, field
from datetime import datetime, timedelta, timezone
from pathlib import Path
from typing import Any, Callable

HERE = Path(__file__).resolve().parent
CELLS = HERE / "cells.json"

# Where the image keeps everything (deploy/docker/server/Dockerfile)
VOLUME = "/var/lib/maktaba"
CONFIG_DIR = f"{VOLUME}/config/maktaba"
SERVER = "/opt/maktaba/server/bin/maktaba-server"
# The library: mounted at a NAS-like path that is the same inside and out (README §6.9)
MEDIA = "/volume1/Media"
NOACCESS = "/volume1/Private"
LIBRARIES = {"en": 20, "ar": 20}  # tests/nas/fixtures.sh
LIBRARY_NAMES = {"en": "English lessons", "ar": "دروس عربية", "noaccess": "Private"}
ADMIN = "nas-admin"
ROLES = ("postgres", "api", "streaming", "pipeline")

# Kept small and offline-friendly: no second listen (no Hugging Face token on CI), no
# language model (5 GB), and the smallest Whisper the pipeline loads
SERVER_TOML_TEMPLATE = """\
# tests/nas/cell.py: a small speech model, no second listen, no language model
[transcription]
model = "Systran/faster-whisper-tiny"
refine = "off"

[llm]
enabled = "off"

# no quiet hours (but in the quiet-hours-hold cell): the Light and Standard tiers' default
# (01:00-06:00, story 59.02) holds a library's first scan until then, and a daytime cell
# would find nothing.
[schedule]
quiet_hours = "{quiet}"
"""


def server_toml(quiet: str = "", speech: bool = False) -> str:
    """The cells' server.toml; `quiet` is its [schedule] quiet_hours ("" for none). A speech
    cell (story 59.06) turns speech on and keeps the tier's model."""
    out = SERVER_TOML_TEMPLATE.replace("{quiet}", quiet)
    if speech:
        out = out.replace('model = "Systran/faster-whisper-tiny"', 'speech = "on"')
    return out


#: Story 59.06: each model and architecture's word error rate on the bundled clips, the first
#: runs' (a rise of more than STT_WER_RISE over it fails the cell)
STT_BASELINE = HERE / "stt-baseline.json"
STT_WER_RISE = 0.05
#: the speech engine's packages, the stt extra's on Linux aarch64 (no torch, no transformers)
STT_ENGINE = "ctranslate2 faster_whisper"


def stt_engine_requirements(constraints: str) -> str:
    """The pins of constraints-stt.txt that make the speech engine on Linux aarch64 (those
    with no marker, such as NumPy, which the core venv lacks, and those whose marker names
    aarch64), markers dropped so that pip installs them on either architecture, hashes kept."""
    out: list[str] = []
    block: list[str] = []

    def flush() -> None:
        # the pins every platform's extra takes (no marker: NumPy) and the aarch64 ones
        if block and (" ;" not in block[0] or "platform_machine == 'aarch64'" in block[0]):
            head = block[0].split(" ;", 1)[0].rstrip(" \\")
            tail = " \\" if block[0].rstrip().endswith("\\") else ""
            out.extend([head + tail, *block[1:]])
        block.clear()

    for line in constraints.splitlines():
        if line and not line[0].isspace():
            flush()
            if line.startswith("#") or line.startswith("--"):
                continue
        if line.strip() and not line.startswith("#"):
            block.append(line)
    flush()
    return "\n".join(out) + "\n"


def wer_rise(baseline: dict[str, Any], key: str, clips: list[dict[str, Any]]) -> list[str]:
    """The clips whose word error rate rose more than STT_WER_RISE over `baseline[key]`."""
    base = baseline.get(key) or {}
    return [f"{c['lang']} {c['wer']:.3f} over {base[c['lang']]:.3f}" for c in clips
            if c.get("lang") in base and c.get("wer", 1) > base[c["lang"]] + STT_WER_RISE]


SERVER_TOML = server_toml()


def quiet_window_ahead(now: datetime, hours: int = 3) -> tuple[str, str]:
    """A one-hour quiet window opening `hours` from now (UTC, the container's TZ), so that
    `now` is outside it, and its start as the queue page shows it."""
    start = (now + timedelta(hours=hours)).replace(minute=0, second=0, microsecond=0)
    end = start + timedelta(hours=1)
    return f"{start:%H:%M}-{end:%H:%M}", f"{start:%H:%M}"


def log(msg: str) -> None:
    print(f"[nas-cell] {time.strftime('%H:%M:%S')} {msg}", flush=True)


# --- the matrix ---------------------------------------------------------------------------


def load_cells(path: Path = CELLS) -> dict[str, Any]:
    with path.open(encoding="utf-8") as f:
        return json.load(f)


def assertion_name(a: str | dict[str, Any]) -> str:
    return a if isinstance(a, str) else a["name"]


def assertion_needs(a: str | dict[str, Any]) -> list[str]:
    return [] if isinstance(a, str) else list(a.get("needs", []))


def missing(needs: list[str], landed: list[str]) -> list[str]:
    return [n for n in needs if n not in landed]


# --- docker -------------------------------------------------------------------------------


PG_PSS_LIMIT_KIB = 512 * 1024  # story 59.02: Postgres's PSS sum on the Light tier


def pss_sum(listing: str) -> tuple[int, int]:
    """The PSS (KiB) summed over the processes of `<pid> Pss: <n> kB` lines, and their count;
    a process without a Pss line (it exited between the listing and the read) is skipped."""
    total = n = 0
    for line in listing.splitlines():
        parts = line.split()
        if len(parts) >= 3 and parts[1] == "Pss:" and parts[2].isdigit():
            total += int(parts[2])
            n += 1
    return total, n


# The daemon docker() talks to: the runner's own, or (the compose cells) the Docker engine
# in a docker:<engine>-dind container, tcp://127.0.0.1:<port>; outer() is always the runner's.
TARGET: dict[str, str] = {}


def docker(*args: str, input: str | None = None, check: bool = True, timeout: float | None = 600,
           capture: bool = True, outer: bool = False) -> subprocess.CompletedProcess[str]:
    cmd = ["docker", *args]
    env = None if outer or not TARGET else {**os.environ, **TARGET}
    p = subprocess.run(cmd, input=input, text=True, capture_output=capture, timeout=timeout, env=env)
    if check and p.returncode != 0:
        raise RuntimeError(f"{shlex.join(cmd)} exited {p.returncode}: {(p.stderr or '').strip()[-800:]}")
    return p


def as_root(image: str, script: str, *mounts: str) -> None:
    """Run a shell script as uid 0 in the image (its entrypoint bypassed)."""
    args = ["run", "--rm", "--user", "0:0", "--entrypoint", "sh"]
    for m in mounts:
        args += ["-v", m]
    docker(*args, image, "-c", script)


# DSM's own folders in a share (story 59.03's skipped names; 59.09): its index (@eaDir, a
# video's poster and a preview clip in it), its recycle bin and a snapshot, each holding a
# copy of a real clip that a scan must never find.
DSM_FOLDERS = ("@eaDir", "#recycle", "#snapshot")


def dsm_extras(d: str) -> str:
    """A shell line adding DSM's folders to the library at d (after the fixtures' copy)."""
    clip = f'"$(ls {d}/en/*.mp4 | head -n 1)"'
    return (f"mkdir -p '{d}/en/@eaDir/lesson.mp4' '{d}/en/#recycle' '{d}/ar/#snapshot/GMT+03-2026.11.01-02.00.00/ar'"
            f" && cp {clip} '{d}/en/@eaDir/lesson.mp4/SYNOVIDEO_PREVIEW.mp4'"
            f" && cp {clip} '{d}/en/#recycle/deleted.mp4'"
            f" && cp {clip} '{d}/ar/#snapshot/GMT+03-2026.11.01-02.00.00/ar/kept.mp4'")


# --- HTTP ---------------------------------------------------------------------------------


class API:
    def __init__(self, base: str) -> None:
        self.base = base
        self.token = ""
        self.credentials: dict[str, str] | None = None  # to sign in again when the token expires

    def login(self) -> tuple[int, Any]:
        assert self.credentials is not None
        self.token = ""
        s, d = self.call("POST", "/api/auth/login", self.credentials, retry=False)
        if s == 200 and isinstance(d, dict) and d.get("access_token"):
            self.token = d["access_token"]
        return s, d

    def call(self, method: str, path: str, body: Any = None, raw: bool = False,
             timeout: float = 30, retry: bool = True) -> tuple[int, Any]:
        s, d = self._call(method, path, body, raw, timeout)
        if s == 401 and retry and self.token and self.credentials:
            self.login()  # the access token lasts minutes; a cell's waits are longer
            s, d = self._call(method, path, body, raw, timeout)
        return s, d

    def _call(self, method: str, path: str, body: Any, raw: bool, timeout: float) -> tuple[int, Any]:
        url = path if path.startswith("http") else self.base + path
        headers = {"Content-Type": "application/json", "X-Maktaba-Client": "native"}
        if self.token:
            headers["Authorization"] = "Bearer " + self.token
        data = None if body is None else json.dumps(body).encode()
        req = urllib.request.Request(url, data=data, headers=headers, method=method)
        try:
            with urllib.request.urlopen(req, timeout=timeout) as resp:
                b = resp.read()
                return resp.status, b if raw else _json(b)
        except urllib.error.HTTPError as e:
            b = e.read()
            return e.code, b if raw else _json(b)

    def get(self, path: str, **kw: Any) -> tuple[int, Any]:
        return self.call("GET", path, **kw)


#: the test plugin the image job packs (plugind's hello guest, tests/components/test_plugins_live.py)
TEST_PLUGIN = "dev.example.hello"


def install_plugin(api: API, pkg: Path, confirm: str) -> tuple[int, Any]:
    """POST /api/plugins/install as the Plugins page sends it: the fields, then the file."""
    boundary = "----nascell" + secrets.token_hex(12)
    parts: list[bytes] = []
    for name, value in (("enable", "true"), ("grants", "[]"), ("libraries", "[]"), ("confirm_name", confirm)):
        parts.append(f'--{boundary}\r\nContent-Disposition: form-data; name="{name}"\r\n\r\n{value}\r\n'.encode())
    parts.append(f'--{boundary}\r\nContent-Disposition: form-data; name="file"; filename="{pkg.name}"\r\n'
                 "Content-Type: application/octet-stream\r\n\r\n".encode() + pkg.read_bytes() + b"\r\n")
    parts.append(f"--{boundary}--\r\n".encode())
    headers = {"Content-Type": f"multipart/form-data; boundary={boundary}", "X-Maktaba-Client": "native",
               "Authorization": "Bearer " + api.token}
    req = urllib.request.Request(api.base + "/api/plugins/install", data=b"".join(parts), headers=headers, method="POST")
    try:
        with urllib.request.urlopen(req, timeout=300) as resp:
            return resp.status, _json(resp.read())
    except urllib.error.HTTPError as e:
        return e.code, _json(e.read())


def _json(b: bytes) -> Any:
    try:
        return json.loads(b) if b else None
    except ValueError:
        return b[:500].decode("utf-8", "replace")


# the render node the VA-API cells (59.05) give the container
RENDER_NODE = "/dev/dri/renderD128"


def wait(what: str, check: Callable[[], Any], seconds: float, every: float = 5) -> Any:
    """Call check until it returns something truthy; raise after seconds."""
    deadline = time.monotonic() + seconds
    last: Any = None
    while True:
        try:
            got = check()
            if got:
                return got
        except Exception as e:  # noqa: BLE001 - a probe that failed is tried again
            last = e
        if time.monotonic() > deadline:
            raise AssertionError(f"{what}: not after {int(seconds)} s" + (f" (last: {last})" if last else ""))
        time.sleep(every)


# --- the cell -----------------------------------------------------------------------------


@dataclass
class Cell:
    spec: dict[str, Any]
    image: str
    fixtures: Path
    out: Path
    local_port: int | None
    landed: list[str]
    password: str = field(default_factory=lambda: "Nas-" + secrets.token_urlsafe(18))
    results: list[dict[str, Any]] = field(default_factory=list)
    api: API | None = None
    refused: tuple[int, str] = (0, "")
    scans: int = 0
    first_counts: dict[str, int] = field(default_factory=dict)
    quiet: tuple[str, str] = ("", "")  # the cell's quiet window and its start, once chosen
    plugin: Path | None = None  # a third-party test plugin (.mkplugin, the image job's), for 59.07

    @property
    def name(self) -> str:
        return self.spec["name"]

    @property
    def container(self) -> str:
        return f"nascell-{self.name}"

    def vol(self, what: str) -> str:
        return f"nascell-{self.name}-{what}"

    @property
    def user(self) -> str:
        return self.spec.get("user", "")

    @property
    def uid(self) -> int:
        return int((self.user or "1000:1000").split(":")[0])

    @property
    def media(self) -> str:
        """Where the library is mounted: /volume1/Media, or the cell's `media.path` (the
        Synology cells' /volume1/Movies, the compose file's share)."""
        return str(self.spec.get("media", {}).get("path", MEDIA))

    @property
    def roots(self) -> list[str]:
        roots = [f"{self.media}/{lib}" for lib in LIBRARIES]
        if self.spec.get("noaccess_root"):
            roots.append(NOACCESS)
        return roots

    # -- the container's shape

    def mounts(self) -> list[str]:
        media = self.spec.get("media", {})
        m = ["-v", f"{self.vol('data')}:{VOLUME}",
             "-v", f"{self.vol('media')}:{self.media}" + (":ro" if media.get("ro") else "")]
        if self.spec.get("noaccess_root"):
            m += ["-v", f"{self.vol('noaccess')}:{NOACCESS}"]
        return m

    def shape(self) -> list[str]:
        """docker run arguments every container of this cell gets."""
        a: list[str] = []
        if self.user:
            a += ["--user", self.user]
        if self.spec.get("seccomp"):
            a += ["--security-opt", f"seccomp={HERE / self.spec['seccomp']}"]
        for k, v in self.spec.get("env", {}).items():
            a += ["-e", f"{k}={v}"]
        a += list(self.spec.get("docker", []))
        return a + self.mounts()

    def network(self) -> list[str]:
        if self.local_port:
            return ["-p", f"127.0.0.1:{self.local_port}:28088"]
        return ["--network", "host"]

    # -- steps

    def prepare(self) -> None:
        log(f"volumes for {self.name}")
        self.teardown(quiet=True)
        data = self.spec.get("data", {})
        owner = data.get("owner", "1000:1000")
        mode = data.get("mode", "0750")
        for v in ("data", "media", "noaccess"):
            docker("volume", "create", self.vol(v))
        # mounted at its own path first, so Docker's copy of the image's folder into an empty
        # volume (owned by 1000) happens before the owner is set
        as_root(self.image, f"chown -R {owner} {VOLUME} && chmod {mode} {VOLUME}", f"{self.vol('data')}:{VOLUME}")
        media = self.spec.get("media", {})
        m_owner = media.get("owner", "1000:1000")
        m_mode = media.get("mode", "0755")
        as_root(self.image,
                f"cp -a /src/. /d/ && {dsm_extras('/d') if media.get('dsm_extras') else 'true'}"
                f" && chown -R {m_owner} /d && chmod -R u=rwX,go=rX /d && chmod {m_mode} /d",
                f"{self.fixtures}:/src:ro", f"{self.vol('media')}:/d")
        if self.spec.get("noaccess_root"):
            as_root(self.image, "mkdir -p /d/hidden && chown -R 0:0 /d && chmod 000 /d",
                    f"{self.vol('noaccess')}:/d")
        # server.toml before setup (as e2e.sh writes it), as the cell's user
        if self.spec.get("expect") != "refused":
            run = ["run", "--rm", "-i", "--entrypoint", "sh"]
            if self.user:
                run += ["--user", self.user]
            docker(*run, "-v", f"{self.vol('data')}:{VOLUME}", self.image, "-c",
                   f"umask 077 && mkdir -p {CONFIG_DIR} && cat > {CONFIG_DIR}/server.toml",
                   input=server_toml(self.quiet_window(), speech=bool(self.spec.get("speech"))))

    def quiet_window(self) -> str:
        """The cell's [schedule] quiet_hours: none, or with `quiet_hours_ahead` a window that
        opens hours from now, so a first scan is held for it."""
        if self.spec.get("quiet_hours_ahead") and not self.quiet[0]:
            self.quiet = quiet_window_ahead(datetime.now(timezone.utc))
        return self.quiet[0]

    def setup(self) -> None:
        answers = {
            "admin": {"username": ADMIN, "password": self.password},
            "libraries": {"roots": [r for r in self.roots if r != NOACCESS], "path_map": []},
            "models": {"source": "huggingface", "hf_token": ""},
            "plex": {"action": "skip"},
        }
        log("setup --answers (headless, in a throwaway container)")
        t0 = time.monotonic()
        p = docker("run", "--rm", "-i", *self.shape(), *(["--network", "host"] if not self.local_port else []),
                   self.image, "setup", "--answers", "/dev/stdin", "--lang", "en", *self.bundle(),
                   input=json.dumps(answers), check=False, timeout=45 * 60)
        (self.out / "setup.log").write_text((p.stdout or "") + (p.stderr or ""), encoding="utf-8")
        self.timing("setup", t0)
        if p.returncode != 0:
            raise AssertionError(f"setup exited {p.returncode}: {(p.stdout + p.stderr).strip()[-600:]}")

    def bundle(self) -> list[str]:
        """59.01's core bundle: the venv from the payload's wheelhouse, no speech wheels (a
        speech cell adds the speech engine itself: assert_stt_engine)."""
        return ["--bundle", "core"] if "59.01" in self.landed else []

    def start(self) -> None:
        log("start the server container")
        docker("run", "-d", "--name", self.container, *self.shape(), *self.network(), self.image)
        if self.local_port:
            docker("run", "-d", "--name", self.container + "-relay", "--network", f"container:{self.container}",
                   "alpine/socat", "TCP-LISTEN:28088,fork,reuseaddr", "TCP:127.0.0.1:8088")
        self.api = API(f"http://127.0.0.1:{self.local_port or 8088}")

    def wait_ready(self) -> None:
        api = self.api
        assert api is not None
        t0 = time.monotonic()

        def running() -> bool:
            st = docker("inspect", "-f", "{{.State.Running}} {{.State.ExitCode}}", self.container).stdout.split()
            if st[0] != "true":
                raise AssertionError(f"the container stopped (exit {st[1]})")
            return True

        def health() -> bool:
            running()
            s, _ = api.get("/api/system/health", timeout=5)
            return s == 200

        wait("the web app's health route", health, 15 * 60)

        # one sign-in at a time: the login route allows 10 a minute
        def login() -> bool:
            running()
            api.credentials = {"username": ADMIN, "password": self.password}
            s, d = api.login()
            if api.token:
                return True
            if s in (401, 403, 423):
                raise AssertionError(f"sign-in refused: {s} {d}")
            return False

        wait("the admin's sign-in", login, 10 * 60, every=10)

        def roles() -> bool:
            running()
            s, d = api.get("/api/system/processes")
            if s != 200 or not isinstance(d, dict):
                return False
            up = {p.get("role") for p in d.get("processes", []) if p.get("state") == "running"}
            return set(ROLES) <= up

        wait(f"every role ({', '.join(ROLES)}) running", roles, 10 * 60)
        self.timing("ready", t0)

    def make_libraries(self) -> None:
        """One library per root, made through the admin API as the web app's first screen makes
        them (setup's roots only feed the folder picker); one with an Arabic name. The root no
        one may list is offered too: what the server answers is recorded (59.03 shows it as No
        access)."""
        assert self.api is not None
        have = {r for lib in self.libraries() for r in lib.get("roots") or []}
        wanted = [(LIBRARY_NAMES[k], f"{self.media}/{k}") for k in LIBRARIES]
        if self.spec.get("noaccess_root"):
            wanted.append((LIBRARY_NAMES["noaccess"], NOACCESS))
        for name, root in wanted:
            if root in have:
                continue
            s, d = self.api.call("POST", "/api/libraries", {"name": name, "roots": [root]})
            self.results.append({"name": f"library:{root}", "ok": None if root == NOACCESS else s in (200, 201),
                                 "detail": f"POST /api/libraries: {s}" + ("" if s in (200, 201) else f" {d}")})
            if root != NOACCESS and s not in (200, 201):
                raise AssertionError(f"POST /api/libraries {root}: {s} {d}")

    def timing(self, what: str, t0: float) -> None:
        self.results.append({"name": f"time:{what}", "ok": True, "detail": f"{time.monotonic() - t0:.0f} s"})

    # -- assertions

    def check(self, a: str | dict[str, Any]) -> None:
        name = assertion_name(a)
        lacking = missing(assertion_needs(a), self.landed)
        if lacking:
            self.results.append({"name": name, "ok": None, "detail": f"pending: needs {', '.join(lacking)}"})
            log(f"{name}: pending ({', '.join(lacking)})")
            return
        fn = getattr(self, "assert_" + name, None)
        t0 = time.monotonic()
        try:
            if fn is None:
                raise AssertionError(f"no assertion called {name} in cell.py")
            detail = fn() or ""
            self.results.append({"name": name, "ok": True, "detail": detail,
                                 "seconds": round(time.monotonic() - t0)})
            log(f"{name}: ok {detail}")
        except Exception as e:  # noqa: BLE001 - every failure is reported, and the rest run
            self.results.append({"name": name, "ok": False, "detail": f"{type(e).__name__}: {e}",
                                 "seconds": round(time.monotonic() - t0)})
            log(f"{name}: FAILED {e}")

    def assert_ready(self) -> str:
        return "health, sign-in and " + ", ".join(ROLES) + " running"

    def assert_only_uid(self) -> str:
        try:
            top = docker("top", self.container, "-eo", "uid,pid,comm").stdout.splitlines()[1:]
        except RuntimeError as e:
            # a daemon whose ps is busybox's (docker:dind, the compose cell) has no uid column:
            # its user column shows the uid as a number when the daemon's host has no name for
            # it, and a name (root) when it has one; a name is never the cell's uid
            if "bad -o argument" not in str(e):
                raise
            top = docker("top", self.container, "-o", "user,pid,comm").stdout.splitlines()[1:]
        procs = [line.split(None, 2) for line in top if line.strip()]
        others = [p for p in procs if not p[0].isdigit() or int(p[0]) != self.uid]
        if others:
            raise AssertionError(f"processes not running as {self.uid}: {others[:10]}")
        return f"{len(procs)} processes, all uid {self.uid}"

    def libraries(self) -> list[dict[str, Any]]:
        assert self.api is not None
        s, d = self.api.get("/api/libraries")
        if s != 200 or not isinstance(d, dict):
            raise AssertionError(f"GET /api/libraries: {s} {d}")
        return d.get("items") or []

    def videos(self) -> list[dict[str, Any]]:
        """Every video of every library (setup may make one library of both roots)."""
        assert self.api is not None
        out: list[dict[str, Any]] = []
        for lib in self.libraries():
            cursor = ""
            while True:
                s, d = self.api.get(f"/api/videos?library={lib['id']}&limit=200" + (f"&cursor={cursor}" if cursor else ""))
                if s != 200 or not isinstance(d, dict):
                    raise AssertionError(f"GET /api/videos: {s} {d}")
                out += d.get("items") or []
                cursor = d.get("next") or ""
                if not cursor:
                    break
        return out

    def under(self, root: str) -> list[dict[str, Any]]:
        return [v for v in self.videos() if (v.get("path") or "").startswith(root.rstrip("/") + "/")]

    def scan_all(self) -> None:
        assert self.api is not None
        for lib in self.libraries():
            if NOACCESS in (lib.get("roots") or []):
                continue
            s, d = self.api.call("POST", f"/api/libraries/{lib['id']}/scan")
            if s not in (200, 201, 202, 409):
                raise AssertionError(f"POST /api/libraries/{lib['id']}/scan: {s} {d}")
        self.scans += 1

    def counted(self) -> dict[str, int]:
        return {k: len(self.under(f"{self.media}/{k}")) for k in LIBRARIES}

    def assert_first_scan(self) -> str:
        """The first scan after the libraries were made finds every clip (no second scan)."""
        if not self.first_counts:
            raise AssertionError("run after scan")
        short = {k: n for k, n in self.first_counts.items() if n < LIBRARIES[k]}
        if short:
            raise AssertionError(f"the first scan found {self.first_counts} of {LIBRARIES}")
        return f"first scan: {self.first_counts}"

    def assert_scan(self) -> str:
        """Every clip found. A scan that ran before the plugin runner was given the new
        library's root walks nothing (EACCES in its sandbox, the runner restarted with the
        grant a moment later): the scan is asked again each minute, and the count recorded."""
        libs = self.libraries()
        roots = sorted(r for lib in libs for r in lib.get("roots") or [])
        want_roots = sorted(f"{self.media}/{k}" for k in LIBRARIES)
        if not set(want_roots) <= set(roots):
            raise AssertionError(f"the libraries' roots are {roots}, not {want_roots}")
        deadline = time.monotonic() + 12 * 60
        while True:
            self.scan_all()
            settle = time.monotonic() + 60
            while time.monotonic() < settle:
                got = self.counted()
                if all(got[k] >= n for k, n in LIBRARIES.items()):
                    break
                time.sleep(10)
            if self.scans == 1:
                self.first_counts = dict(got)
            if all(got[k] >= n for k, n in LIBRARIES.items()):
                return ", ".join(f"{k}: {v} videos" for k, v in got.items()) + f" after {self.scans} scan(s)"
            if time.monotonic() > deadline:
                raise AssertionError(f"after {self.scans} scans: {got} of {LIBRARIES}")

    def hevc_video(self) -> dict[str, Any]:
        for v in self.under(f"{self.media}/en"):
            if "HEVC" in (v.get("path") or ""):
                return v
        raise AssertionError("the HEVC clip is not in the library")

    def assert_thumbnail(self) -> str:
        assert self.api is not None
        api = self.api
        def poster() -> str:
            for v in self.under(f"{self.media}/ar"):
                s, b = api.get(f"/api/videos/{v['id']}/poster?size=small", raw=True)
                if s == 200 and len(b) > 100:
                    return f"{v.get('filename')}: {len(b)} bytes"
            return ""

        return wait("a poster for an Arabic clip", poster, 15 * 60, every=15)

    def assert_transcode(self) -> str:
        return self.transcode(force_software=True)

    def assert_transcode_auto(self) -> str:
        """A transcode on the encoder the server picked (59.05): libx264 after VA-API fell back."""
        return self.transcode(force_software=False)

    def transcode(self, force_software: bool) -> str:
        assert self.api is not None
        api = self.api
        v = self.hevc_video()
        body: dict[str, Any] = {"video_id": v["id"], "force_transcode": True}
        if force_software:
            body["force_software"] = True
        s, d = api.call("POST", "/api/stream/sessions", body)
        if s not in (200, 201) or not isinstance(d, dict):
            raise AssertionError(f"POST /api/stream/sessions: {s} {d}")
        if d.get("mode") != "transcode" or not d.get("manifest_url"):
            raise AssertionError(f"not a transcode: {d}")
        master = urljoin(api.base, d["manifest_url"])

        def fetch(url: str) -> bytes:
            s, b = api.get(url, raw=True, timeout=60)
            if s != 200:
                raise AssertionError(f"GET {url}: {s} {b[:200]!r}")
            return b

        def first_uri(text: str) -> str:
            for line in text.splitlines():
                if line and not line.startswith("#"):
                    return line.strip()
            return ""

        rendition = wait("the master playlist", lambda: first_uri(fetch(master).decode()), 120, every=3)
        index = urljoin(master, rendition)
        segment = wait("a segment in the rendition's playlist", lambda: first_uri(fetch(index).decode()), 180, every=3)
        body = wait("the first segment", lambda: fetch(urljoin(index, segment)), 180, every=3)
        if len(body) < 1000:
            raise AssertionError(f"the first segment is {len(body)} bytes")
        api.call("DELETE", f"/api/stream/sessions/{d.get('session_id')}")
        return f"{v.get('filename')}: transcoded, first segment {len(body)} bytes"

    def assert_no_oom(self) -> str:
        oom = docker("inspect", "-f", "{{.State.OOMKilled}}", self.container).stdout.strip()
        if oom == "true":
            raise AssertionError("the container was OOM-killed")
        p = docker("exec", self.container, "sh", "-c",
                   "cat /sys/fs/cgroup/memory.current /sys/fs/cgroup/memory.max /sys/fs/cgroup/memory.events 2>/dev/null",
                   check=False)
        lines = p.stdout.split()
        if len(lines) >= 2 and lines[1] != "max":
            current, limit = int(lines[0]), int(lines[1])
            if current > limit:
                raise AssertionError(f"memory.current {current} over memory.max {limit}")
            events = dict(zip(lines[2::2], lines[3::2]))
            if int(events.get("oom_kill", "0")) > 0:
                raise AssertionError(f"a process was OOM-killed in the container: {events}")
            return f"memory.current {current >> 20} MiB of {limit >> 20} MiB, no OOM kill"
        return "no memory limit; not OOM-killed"

    def assert_quiet_hold(self) -> str:
        """Story 59.02: outside the quiet hours a library's first scan waits for them (the
        scan queue says quiet_hours and when the window opens, and no clip is found), and
        the admin's Run now on the queue (POST /api/queues/scan/run-now) starts it at once:
        the held jobs run and none stays paused. Every clip is then found, as in `scan`.
        (Before #1095 a scan run within seconds of the library being made walked nothing:
        the runner had no grant for the new root yet.)"""
        assert self.api is not None
        api = self.api
        window, opens = self.quiet
        if not window:
            raise AssertionError("the cell has no quiet window (quiet_hours_ahead)")
        self.scan_all()

        def held() -> str:
            s, d = api.get("/api/queues")
            if s != 200 or not isinstance(d, dict):
                raise AssertionError(f"GET /api/queues: {s} {d}")
            q = next((q for q in d.get("queues") or [] if q.get("stage") == "scan"), None)
            if q and q.get("waiting_reason") == "quiet_hours":
                if q.get("quiet_at") != opens:
                    raise AssertionError(f"the scan waits for {q.get('quiet_at')!r}, not {opens!r}")
                return f"scan queue waiting for quiet hours ({opens})"
            return ""

        said = wait("the first scan held for the quiet hours", held, 5 * 60, every=5)
        got = self.counted()
        if any(got.values()):
            raise AssertionError(f"clips found while the first scan was held: {got}")
        s, d = api.call("POST", "/api/queues/scan/run-now")
        jobs = d.get("jobs") if isinstance(d, dict) else None
        if s != 200 or not jobs:
            raise AssertionError(f"POST /api/queues/scan/run-now: {s} {d}")

        def ran() -> str:
            s, d = api.get("/api/queues")
            q = next((q for q in (d.get("queues") if isinstance(d, dict) else None) or []
                      if q.get("stage") == "scan"), None)
            if s != 200 or q is None:
                raise AssertionError(f"GET /api/queues: {s}")
            c = q.get("counts") or {}
            if q.get("waiting_reason") == "quiet_hours" or c.get("paused") or c.get("pending") or c.get("running"):
                return ""
            return f"{c.get('done_24h')} scan(s) done"

        done = wait("the held scans run after Run now", ran, 5 * 60, every=5)
        clips = self.assert_scan()
        return f"{said}, window {window}; Run now: {jobs} job(s) started, {done}; then {clips}"

    def assert_postgres_pss_512m(self) -> str:
        """Story 59.02: the bundled Postgres's processes together hold under 512 MB (PSS: shared
        pages counted once over the processes sharing them), the Light tier's tuning applied."""
        p = docker("exec", self.container, "sh", "-c",
                   'for d in /proc/[0-9]*; do [ "$(cat $d/comm 2>/dev/null)" = postgres ] && '
                   'echo "${d#/proc/} $(grep -m1 ^Pss: $d/smaps_rollup 2>/dev/null)"; done; true')
        (self.out / "postgres-pss.txt").write_text(p.stdout, encoding="utf-8")
        kib, n = pss_sum(p.stdout)
        if n == 0:
            raise AssertionError(f"no postgres process with a PSS reading: {p.stdout[-300:]!r}")
        if kib >= PG_PSS_LIMIT_KIB:
            raise AssertionError(f"Postgres PSS {kib >> 10} MiB over {n} processes, not under 512 MiB")
        return f"Postgres PSS {kib >> 10} MiB over {n} processes (limit 512 MiB)"

    def assert_media_untouched(self) -> str:
        """The library is exactly the fixtures: nothing written in it (no .maktaba left behind
        when the mount is read-only or not the user's)."""
        listing = docker("run", "--rm", "--user", "0:0", "--entrypoint", "sh", "-v", f"{self.vol('media')}:/d:ro",
                         self.image, "-c", "cd /d && find . -mindepth 1 | sort").stdout.splitlines()
        want = sorted("./" + str(p.relative_to(self.fixtures)) for p in self.fixtures.rglob("*"))
        extra = sorted(set(listing) - set(want))
        if extra:
            raise AssertionError(f"files written in the library: {extra[:10]}")
        return f"{len(listing)} entries, as the fixtures"

    # -- story 59.03: media permissions

    def check_access(self, lib: dict[str, Any]) -> dict[str, Any]:
        """POST /api/libraries/{id}/check-access (the page's Check access): the access panel."""
        assert self.api is not None
        s, d = self.api.call("POST", f"/api/libraries/{lib['id']}/check-access")
        if s != 200 or not isinstance(d, dict):
            raise AssertionError(f"POST /api/libraries/{lib['id']}/check-access: {s} {d}")
        return d

    def media_libraries(self) -> list[dict[str, Any]]:
        libs = [lib for lib in self.libraries() if any(r.startswith(self.media + "/") for r in lib.get("roots") or [])]
        if not libs:
            raise AssertionError(f"no library under {self.media}")
        return libs

    def assert_access_ro(self) -> str:
        """A library Maktaba may read but not write shows Read only, with the fix in the
        platform's words; a :ro mount is named as one. Nothing is written in it
        (media_untouched)."""
        ro_mount = bool(self.spec.get("media", {}).get("ro"))
        seen = []
        for lib in self.media_libraries():
            rep = self.check_access(lib)
            if rep.get("access") != "ro":
                raise AssertionError(f"{lib['name']}: access {rep.get('access')!r}, not ro: {rep}")
            for root in rep.get("roots") or []:
                if root.get("access") != "ro" or not root.get("fix"):
                    raise AssertionError(f"{lib['name']}: root {root}")
                if ro_mount and not root.get("mount_read_only"):
                    raise AssertionError(f"{root.get('path')}: a :ro mount not named as one")
            listed = next((x for x in self.libraries() if x["id"] == lib["id"]), {})
            if listed.get("access") != "ro":
                raise AssertionError(f"GET /api/libraries: {lib['name']} access {listed.get('access')!r}")
            seen.append(lib["name"])
        return f"read only{' (mount :ro)' if ro_mount else ''}: {', '.join(seen)}"

    def assert_convert_409(self) -> str:
        """A writing action on a read-only library answers 409 library_read_only and queues
        nothing."""
        assert self.api is not None
        for lib in self.media_libraries():
            for body in ({}, {"dry_run": True}):
                s, d = self.api.call("POST", f"/api/libraries/{lib['id']}/convert", body)
                code = d.get("code") if isinstance(d, dict) else None
                if s != 409 or code != "library_read_only":
                    raise AssertionError(f"POST /api/libraries/{lib['id']}/convert {body}: {s} {d}")
        return "convert: 409 library_read_only"

    def assert_access_none(self) -> str:
        """A folder Maktaba cannot list: adding it is refused (not-readable); a library whose
        folder loses access later shows No access after Check access, and no scan runs."""
        assert self.api is not None
        detail = next((r["detail"] for r in self.results if r["name"] == f"library:{NOACCESS}"), "")
        lib = next((x for x in self.libraries() if NOACCESS in (x.get("roots") or [])), None)
        if lib is None:
            if "422" not in detail:
                raise AssertionError(f"adding {NOACCESS} (mode 000): {detail or 'never asked'}, not 422")
            # the folder is opened, added, and closed again
            as_root(self.image, "chmod 755 /d", f"{self.vol('noaccess')}:/d")
            try:
                s, d = self.api.call("POST", "/api/libraries", {"name": LIBRARY_NAMES["noaccess"], "roots": [NOACCESS]})
                if s not in (200, 201) or not isinstance(d, dict):
                    raise AssertionError(f"POST /api/libraries {NOACCESS} (opened): {s} {d}")
                lib = d
            finally:
                as_root(self.image, "chmod 000 /d", f"{self.vol('noaccess')}:/d")
        rep = self.check_access(lib)
        if rep.get("access") != "none":
            raise AssertionError(f"{NOACCESS}: access {rep.get('access')!r} after Check access, not none: {rep}")
        roots = rep.get("roots") or []
        if not roots or roots[0].get("access") != "none" or not roots[0].get("fix"):
            raise AssertionError(f"{NOACCESS}: roots {roots}")
        s, d = self.api.call("POST", f"/api/libraries/{lib['id']}/scan")
        code = d.get("code") if isinstance(d, dict) else None
        if s != 409 or code != "library_no_access":
            raise AssertionError(f"POST /api/libraries/{lib['id']}/scan on no access: {s} {d}")
        return f"adding refused ({detail}); after losing access: none, scan 409 library_no_access"

    def assert_logs_clean(self) -> str:
        """No permission error in the server's log: every path it uses is its own."""
        p = docker("exec", self.container, "sh", "-c",
                   f"cat {VOLUME}/state/maktaba/logs/server.log 2>/dev/null", check=False, timeout=60)
        # share_root_unreachable: the scan that raced the runner's new grant (first_scan
        # reports it)
        bad = [line for line in p.stdout.splitlines()
               if re.search(r"permission denied|EACCES|EPERM", line, re.I) and "share_root_unreachable" not in line]
        if bad:
            raise AssertionError(f"{len(bad)} permission errors in server.log, first: {bad[0][:300]}")
        return "no permission error in server.log"

    def machine(self) -> dict[str, Any]:
        """GET /api/server/machine (59.04): the tier, why, and the hardware it read."""
        assert self.api is not None
        s, d = self.api.get("/api/server/machine")
        if s != 200 or not isinstance(d, dict) or not d.get("available"):
            raise AssertionError(f"GET /api/server/machine: {s} {d}")
        return d

    def limit_bytes(self) -> int:
        """The container's memory limit as Docker set it (0: none)."""
        return int(docker("inspect", "-f", "{{.HostConfig.Memory}}", self.container).stdout.strip() or 0)

    def tier_is(self, want: str) -> str:
        d = self.machine()
        tier = d.get("tier")
        limit = ((d.get("machine") or {}).get("memory") or {}).get("limit_bytes")
        docker_limit = self.limit_bytes()
        if docker_limit and limit != docker_limit:
            raise AssertionError(f"the memory limit read is {limit}, not the container's {docker_limit}")
        if tier != want:
            raise AssertionError(f"tier {tier!r} (detected {d.get('detected')!r}), not {want!r}; limit {limit}")
        defaults = d.get("defaults") or {}
        if defaults.get("tier") not in (None, want):
            raise AssertionError(f"the defaults are {defaults.get('tier')!r}'s, not {want!r}'s")
        return f"{tier}: limit {limit >> 20 if limit else 0} MiB, Postgres {defaults.get('shared_buffers')}, {defaults.get('pipeline_workers')} worker(s)"

    def video(self) -> dict[str, Any]:
        """The streaming role's video verdict in GET /api/server/machine (59.05), once written."""
        return wait("the video verdict", lambda: self.machine().get("video"), 120, every=3)

    def video_software(self, reason: str) -> dict[str, Any]:
        v = self.video()
        if v.get("encoder") != "software" or not v.get("software_only") or v.get("reason") != reason:
            raise AssertionError(f"video {v}, want software ({reason})")
        return v

    def render_nodes(self) -> list[str]:
        return [r.get("path", "") for r in ((self.machine().get("machine") or {}).get("gpu") or {}).get("render") or []]

    def assert_video_no_device(self) -> str:
        """No /dev/dri: VA-API is not tried, Settings says no_device, libx264 encodes."""
        if nodes := self.render_nodes():
            raise AssertionError(f"render nodes in a cell without any: {nodes}")
        self.video_software("no_device")
        return "software, no_device"

    def assert_video_device_failed(self) -> str:
        """A render node that is not a GPU (/dev/null): its test encode fails, the reason is kept."""
        if RENDER_NODE not in self.render_nodes():
            raise AssertionError(f"{RENDER_NODE} not among {self.render_nodes()}")
        v = self.video_software("device_failed")
        if v.get("device") != RENDER_NODE or not v.get("error"):
            raise AssertionError(f"the failed device's reason: {v}")
        return f"software, {RENDER_NODE} could not encode: {v.get('error', '')[:120]}"

    def assert_video_group_add(self) -> str:
        """A render node this user may not open: the fix names the user and group_add."""
        v = self.video_software("group_add")
        uid = (self.user or "1000:1000").split(":")[0]
        if v.get("device") != RENDER_NODE or str(v.get("uid")) != uid:
            raise AssertionError(f"the group_add reason: {v}, want {RENDER_NODE} and uid {uid}")
        return f"software, user {uid} can't open {RENDER_NODE}: add group {v.get('gid')}"

    # -- speech to text (story 59.06)

    def exec_sh(self, script: str, timeout: int = 1800) -> Any:
        """A shell script in the running container, as the cell's user."""
        user = ["-u", self.user] if self.user else []
        return docker("exec", *user, self.container, "sh", "-c", script, check=False, timeout=timeout)

    def venv_python(self) -> str:
        p = self.exec_sh(f"find {VOLUME} -maxdepth 5 -path '*/venv/bin/python' | head -n 1")
        py = p.stdout.strip()
        if not py:
            raise AssertionError(f"no pipeline venv under {VOLUME}: {p.stderr.strip()[-300:]}")
        return py

    def assert_stt_engine(self) -> str:
        """T06: the speech engine goes into the pipeline's venv and imports, with no torch and
        no transformers (the second listen is x86_64 + CUDA). On arm64 from the payload's
        wheelhouse alone (--no-index: no internet needed); on amd64, where the payload carries
        no speech wheels, the same pins from PyPI."""
        py = self.venv_python()
        arch = self.exec_sh("uname -m").stdout.strip()
        wheels = "/opt/maktaba/server/pipeline/wheels"
        cons = docker("exec", self.container, "cat", "/opt/maktaba/server/pipeline/constraints-stt.txt",
                      check=False)
        if cons.returncode != 0:
            raise AssertionError("the payload has no pipeline/constraints-stt.txt")
        reqs = stt_engine_requirements(cons.stdout)
        where = ["--no-index", "--find-links", wheels] if arch == "aarch64" else []
        user = ["-u", self.user] if self.user else []
        p = docker("exec", "-i", *user, self.container, py, "-m", "pip", "install", "--quiet",
                   "--disable-pip-version-check", "--no-deps", "--require-hashes", *where, "-r", "/dev/stdin",
                   input=reqs, check=False, timeout=1800)
        (self.out / "stt-install.log").write_text(p.stdout + p.stderr, encoding="utf-8")
        if p.returncode != 0:
            raise AssertionError(f"pip install of the speech engine ({arch}): {(p.stdout + p.stderr).strip()[-600:]}")
        probe = ("import importlib.util as u, ctranslate2, faster_whisper;"
                 "bad=[m for m in ('torch','transformers') if u.find_spec(m)];"
                 "assert not bad, bad;"
                 "print(ctranslate2.__version__, sorted(ctranslate2.get_supported_compute_types('cpu')))")
        p = docker("exec", *user, self.container, py, "-c", probe, check=False)
        if p.returncode != 0:
            raise AssertionError(f"the speech engine does not import: {(p.stdout + p.stderr).strip()[-600:]}")
        how = "offline, from the payload's wheelhouse" if where else "from PyPI"
        return f"{arch}: ctranslate2 {p.stdout.strip()}, {how}; no torch, no transformers"

    def speech(self) -> dict[str, Any]:
        assert self.api is not None
        s, d = self.api.get("/api/server/speech")
        if s != 200 or not isinstance(d, dict) or not d.get("available"):
            raise AssertionError(f"GET /api/server/speech: {s} {d}")
        (self.out / "api-speech.json").write_text(json.dumps(d, indent=2, ensure_ascii=False), encoding="utf-8")
        return d

    def assert_stt_measure(self) -> str:
        """T01, T05: the server's speech measure (an admin's Measure again) transcribes the
        Arabic and English clips with the tier's model; each word error rate is recorded and
        compared with the baseline of its model and architecture (stt-baseline.json): a rise
        of more than 5 points fails. No baseline yet: recorded only."""
        assert self.api is not None
        d = self.speech()
        sp = d.get("speech") or {}
        if not sp.get("on"):
            raise AssertionError(f"speech is off: {sp}")
        s, body = self.api.call("POST", "/api/server/speech/measure")
        if s not in (202, 409):
            raise AssertionError(f"POST /api/server/speech/measure: {s} {body}")

        def done() -> bool:
            m = self.speech().get("measure") or {}
            return not self.speech().get("measuring") and m.get("model") == sp.get("repo") and (
                bool(m.get("error")) or (m.get("rtf") or 0) > 0)

        wait("the speech measure", done, 30 * 60, every=15)
        d = self.speech()
        m = d.get("measure") or {}
        if m.get("error"):
            raise AssertionError(f"the measure of {m.get('model')}: {m['error']}")
        clips = m.get("clips") or []
        if sorted(c.get("lang") for c in clips) != ["ar", "en"]:
            raise AssertionError(f"the clips measured: {clips}")
        arch = m.get("arch") or "?"
        key = f"{sp.get('model')}/{arch}"
        baseline = json.loads(STT_BASELINE.read_text(encoding="utf-8")) if STT_BASELINE.is_file() else {}
        wers = ", ".join(f"{c['lang']} WER {c['wer']:.3f} at {c['rtf']}×" for c in clips)
        self.results.append({"name": "stt:wer", "ok": True, "detail": f"{key}: {wers}"})
        risen = wer_rise(baseline, key, clips)
        if risen:
            raise AssertionError(f"{key}: word error rate rose over the baseline: {'; '.join(risen)}")
        note = "" if key in baseline else f"; no baseline for {key} yet: add it to stt-baseline.json"
        return f"{key}: {m.get('rtf')}× real time on {m.get('threads')} threads; {wers}{note}"

    def assert_stt_light_off(self) -> str:
        """Q7: on the Light tier speech is off by default, with its reason; this cell turned
        it on (server.toml), and the tier's model is base, in the quiet hours only."""
        sp = self.speech().get("speech") or {}
        if sp.get("default_on") or sp.get("reason") != "light" or sp.get("model") != "base" or not sp.get("quiet_only"):
            raise AssertionError(f"Light's speech: {sp}")
        if sp.get("arch") == "arm64" and sp.get("second_listen"):
            raise AssertionError("the second listen is offered on arm64")
        return f"off by default ({sp.get('reason')}), base, quiet hours only; second listen {sp.get('second_listen')}"

    def assert_tier_light(self) -> str:
        return self.tier_is("light")

    def assert_isolation_container(self) -> str:
        """59.07: without Landlock and user namespaces the home's plugin isolation level is
        Container (the machine's page and the Plugins page say so); a third-party plugin an
        admin installs and turns on stays off, isolation_held, until the admin allows plugins
        with container-only isolation (the password typed again); then it runs, in plugind
        with its required layers, reported partial."""
        assert self.api is not None
        api = self.api
        iso = (self.machine().get("plugin_isolation") or {})
        if iso.get("level") != "container":
            raise AssertionError(f"GET /api/server/machine plugin_isolation {iso}, not container")
        s, home = api.get("/api/plugins/isolation")
        if s != 200 or not isinstance(home, dict) or home.get("level") != "container" or home.get("held") is not True:
            raise AssertionError(f"GET /api/plugins/isolation: {s} {home}")
        if self.plugin is None or not self.plugin.is_file():
            raise AssertionError("no test plugin (--plugin): the image job's nas-plugin artifact")
        s, d = api.call("PUT", "/api/plugins/policy", {"allow_unreviewed": True, "password": self.password})
        if s != 200:
            raise AssertionError(f"PUT /api/plugins/policy: {s} {d}")
        s, d = install_plugin(api, self.plugin, confirm="Hello")
        if s not in (200, 201):
            raise AssertionError(f"POST /api/plugins/install: {s} {d}")

        def plugin_state() -> dict[str, Any]:
            s, d = api.get(f"/api/plugins/{TEST_PLUGIN}")
            return d if s == 200 and isinstance(d, dict) else {}

        held = wait("the plugin held for the isolation level",
                    lambda: (p := plugin_state()).get("reason") == "isolation_held" and p, 5 * 60)
        # the switch is the admin's, with the password again
        s, d = api.call("PUT", "/api/plugins/isolation", {"allow": True})
        if s != 401:
            raise AssertionError(f"PUT /api/plugins/isolation without the password: {s} {d}, not 401")
        s, d = api.call("PUT", "/api/plugins/isolation", {"allow": True, "password": self.password})
        if s != 200 or not isinstance(d, dict) or d.get("allow_container_isolation") is not True or d.get("held"):
            raise AssertionError(f"PUT /api/plugins/isolation: {s} {d}")
        running = wait("the plugin running after the admin's yes",
                       lambda: (p := plugin_state()).get("phase") in ("running", "degraded") and p, 5 * 60)

        def host_partial() -> dict[str, Any] | None:
            # Degraded keeps Ready true, so the list's reason (a false condition's) is empty:
            # sandbox_partial is on the Ready condition of the home's part rows in the detail
            s, d = api.get("/api/components/plugins.host")
            if s != 200 or not isinstance(d, dict) or d.get("phase") != "degraded":
                return None
            for m in d.get("machines") or []:
                for part in m.get("parts") or []:
                    for c in part.get("conditions") or []:
                        if c.get("reason") == "sandbox_partial":
                            return {"phase": d.get("phase"), "machine": m.get("machine"), "condition": c}
            return None

        host = wait("plugins.host Degraded, sandbox_partial (plugind's required layers on)", host_partial, 5 * 60)
        (self.out / "api-plugin-isolation.json").write_text(json.dumps(
            {"machine": iso, "home": home, "held": held, "running": running, "host": host}, indent=1),
            encoding="utf-8")
        missing = (host.get("condition") or {}).get("params", {}).get("missing", "")
        return (f"container (missing {','.join(iso.get('missing') or [])}); held, then {running.get('phase')} "
                f"after the admin's yes; plugins.host degraded, sandbox_partial ({missing})")

    def assert_tier_standard(self) -> str:
        return self.tier_is("standard")

    # -- cells that must not start

    # -- story 59.09: Synology

    def assert_platform_dsm(self) -> str:
        """GET /api/server/machine names the platform dsm, from MAKTABA_PLATFORM."""
        m = self.machine().get("machine") or {}
        if m.get("platform") != "dsm":
            raise AssertionError(f"platform {m.get('platform')!r} ({m.get('platform_from')!r}), not dsm")
        return f"dsm ({m.get('platform_from')})"

    def assert_dsm_skipped(self) -> str:
        """DSM's index, recycle bin and snapshots in a share (@eaDir, #recycle, #snapshot, each
        with a real clip in it) are never library content: the scan finds the fixtures, exactly."""
        found = self.videos()
        bad = [v.get("path") for v in found if any(f"/{d}/" in (v.get("path") or "") for d in DSM_FOLDERS)]
        if bad:
            raise AssertionError(f"{len(bad)} videos from DSM's folders, first: {bad[0]}")
        got = self.counted()
        if got != LIBRARIES:
            raise AssertionError(f"found {got}, want exactly {LIBRARIES}")
        return f"{', '.join(DSM_FOLDERS)} skipped; {got}"

    def share(self, path: str) -> str:
        """/volume1/Movies/en → Movies."""
        return path.strip("/").split("/")[1]

    def assert_fix_dsm(self) -> str:
        """Each root that needs one (Read only, No access) has DSM's fix (library.fix.dsm),
        naming its shared folder."""
        seen = []
        libs = [lib for lib in self.libraries() if any(r.startswith("/volume") for r in lib.get("roots") or [])]
        for lib in libs:
            for root in self.check_access(lib).get("roots") or []:
                if root.get("access") == "rw":
                    continue
                fix = root.get("fix") or {}
                want = self.share(root.get("path") or "")
                if fix.get("key") != "library.fix.dsm" or (fix.get("params") or {}).get("share") != want:
                    raise AssertionError(f"{root.get('path')}: fix {fix}, want library.fix.dsm for {want}")
                seen.append(f"{root.get('path')} → {want} ({root.get('access')})")
        if not seen:
            raise AssertionError("no library on a DSM share")
        return "; ".join(seen)

    def assert_dsm_shares(self) -> str:
        """The compose file's two shares as the guide grants them: Movies (:ro, Read only for
        the DSM user) shows Read only, Recordings (Read/Write) Read and write."""
        assert self.api is not None
        ro = self.assert_access_ro()
        root = "/volume1/Recordings"
        lib = next((x for x in self.libraries() if root in (x.get("roots") or [])), None)
        if lib is None:
            s, d = self.api.call("POST", "/api/libraries", {"name": "Recordings", "roots": [root]})
            if s not in (200, 201) or not isinstance(d, dict):
                raise AssertionError(f"POST /api/libraries {root}: {s} {d}")
            lib = d
        rep = self.check_access(lib)
        if rep.get("access") != "rw":
            raise AssertionError(f"{root}: access {rep.get('access')!r}, not rw: {rep}")
        return f"{ro}; Recordings: read and write"

    def assert_compose_engine(self) -> str:
        """The compose file was taken unchanged by Docker 24.0.x (`docker compose config`, then
        `up`): what Container Manager runs on DSM 7.2."""
        info = getattr(self, "engine", {})
        if not info:
            raise AssertionError("not a compose cell")
        want = str(self.spec["compose"]["engine"])
        if not info.get("version", "").startswith(want):
            raise AssertionError(f"engine {info.get('version')}, not {want}")
        return f"Docker {info['version']} (API {info.get('api')}), compose {info.get('compose')}: config and up"

    def assert_refused_write_dsm(self) -> str:
        """On DSM the write-denied message adds DSM's own screens, in both languages."""
        self.assert_refused_write()
        _, text = self.refused
        for want in ("Control Panel → Shared Folder →", "File Station →", "لوحة التحكم ← المجلد المشترك"):
            if want not in text:
                raise AssertionError(f"the message does not name {want!r}: {text[-500:]}")
        return "exit 78 with DSM's screens in English and Arabic"

    def expect_refused(self) -> None:
        log("start, expecting a refusal")
        p = docker("run", "--name", self.container, *self.shape(), *self.network(), self.image,
                   check=False, timeout=60)
        (self.out / "refused.log").write_text((p.stdout or "") + (p.stderr or ""), encoding="utf-8")
        self.refused = (p.returncode, (p.stdout or "") + (p.stderr or ""))

    def assert_refused_write(self) -> str:
        code, text = self.refused
        if code != 78:
            raise AssertionError(f"exit {code}, not 78: {text[-400:]}")
        for want in (VOLUME, str(self.uid), "لا يستطيع"):
            if want not in text:
                raise AssertionError(f"the message does not name {want!r}: {text[-400:]}")
        return "exit 78, path, uid and the Arabic line"

    def assert_refused_privileged(self) -> str:
        code, text = self.refused
        if code != 78:
            raise AssertionError(f"exit {code}, not 78: {text[-400:]}")
        return "exit 78: " + text.strip().splitlines()[0][:160]

    # -- logs, kept pass or fail

    def collect(self) -> None:
        out = self.out
        log(f"logs into {out}")

        def keep(name: str, *args: str) -> None:
            try:
                p = docker(*args, check=False, timeout=120)
                (out / name).write_text((p.stdout or "") + (p.stderr or ""), encoding="utf-8")
            except Exception as e:  # noqa: BLE001
                (out / name).write_text(f"not kept: {e}\n", encoding="utf-8")

        keep("container.log", "logs", "-t", self.container)
        keep("inspect.json", "inspect", self.container)
        keep("processes-top.txt", "top", self.container, "-eo", "uid,gid,pid,ppid,rss,comm")
        keep("cgroup-memory.txt", "exec", self.container, "sh", "-c",
             "for f in memory.max memory.current memory.peak memory.events; do echo \"$f: $(cat /sys/fs/cgroup/$f 2>/dev/null)\"; done")
        for src, dst in ((f"{VOLUME}/state/maktaba/logs", "state-logs"),
                         (f"{VOLUME}/data/maktaba/logs", "data-logs"),
                         (f"{VOLUME}/data/maktaba/updates/launcher.log", "launcher.log")):
            p = docker("cp", f"{self.container}:{src}", str(out / dst), check=False, timeout=300)
            if p.returncode != 0:
                (out / f"{dst}.missing").write_text(p.stderr or "", encoding="utf-8")
        if self.api and self.api.token:
            for path, name in (("/api/system/processes", "api-processes.json"),
                               ("/api/libraries", "api-libraries.json"),
                               ("/api/server/machine", "api-machine.json"),
                               ("/api/system/version", "api-version.json")):
                try:
                    s, d = self.api.get(path)
                    (out / name).write_text(json.dumps({"status": s, "body": d}, indent=2, ensure_ascii=False),
                                            encoding="utf-8")
                except Exception as e:  # noqa: BLE001
                    (out / name).write_text(json.dumps({"error": str(e)}), encoding="utf-8")

    def teardown(self, quiet: bool = False) -> None:
        for c in (self.container + "-relay", self.container):
            docker("rm", "-f", c, check=False)
        for v in ("data", "media", "noaccess"):
            docker("volume", "rm", "-f", self.vol(v), check=False)

    # -- the whole cell

    def run(self) -> bool:
        self.out.mkdir(parents=True, exist_ok=True)
        lacking = missing(list(self.spec.get("needs", [])), self.landed)
        if lacking:
            self.results.append({"name": "cell", "ok": None, "detail": f"pending: needs {', '.join(lacking)}"})
            self.report()
            return True
        try:
            self.prepare()
            if self.spec.get("expect") == "refused":
                self.expect_refused()
            else:
                self.setup()
                self.start()
                self.wait_ready()
                self.make_libraries()
                if self.spec.get("kill_restart"):
                    log("kill -9 and start again on the same volume")
                    docker("kill", "-s", "KILL", self.container)
                    docker("start", self.container)
                    self.wait_ready()
            for a in self.spec.get("asserts", []):
                self.check(a)
        except Exception as e:  # noqa: BLE001
            self.results.append({"name": "run", "ok": False, "detail": f"{type(e).__name__}: {e}"})
            log(f"FAILED: {e}")
            (self.out / "harness-error.txt").write_text(traceback.format_exc(), encoding="utf-8")
        finally:
            try:
                self.collect()
            finally:
                self.report()
        return all(r["ok"] is not False for r in self.results)

    def report(self) -> None:
        ok = all(r["ok"] is not False for r in self.results)
        report = {"cell": self.name, "about": self.spec.get("about", ""), "image": self.image, "ok": ok,
                  "shape": self.shape() if self.spec else [], "results": self.results}
        (self.out / "report.json").write_text(json.dumps(report, indent=2, ensure_ascii=False), encoding="utf-8")
        mark = {True: "✅", False: "❌", None: "⏳"}
        lines = [f"### {mark[ok]} `{self.name}`: {self.spec.get('about', '')}", "",
                 f"`docker run {shlex.join(self.shape())}`", "", "| Assertion | Result |", "|---|---|"]
        for r in self.results:
            detail = str(r["detail"]).replace("|", "\\|").replace("\n", " ")[:300]
            lines.append(f"| {r['name']} | {mark[r['ok']]} {detail} |")
        (self.out / "summary.md").write_text("\n".join(lines) + "\n", encoding="utf-8")


def free_port() -> int:
    import socket

    with socket.socket() as so:
        so.bind(("127.0.0.1", 0))
        return int(so.getsockname()[1])


@dataclass
class ComposeCell(Cell):
    """A cell started from a NAS's compose file as the NAS starts it (story 59.09): the file
    unchanged, run by the Docker engine the NAS has (`compose.engine`, Container Manager's
    24.0.2 on DSM 7.2) in a docker:<engine>-dind container, with the file's own host paths
    made inside it (the data folder, its shares with the fixtures, DSM's folders) and the
    file's image the commit's. The API is reached through a relay in the engine's network
    namespace, so the server sees a local client as on the host network."""

    engine: dict[str, str] = field(default_factory=dict)
    dind_port: int = 0
    api_port: int = 0

    @property
    def file(self) -> Path:
        return HERE.parents[1] / self.spec["compose"]["file"]

    @property
    def dind(self) -> str:
        return f"nascell-{self.name}-dind"

    @property
    def container(self) -> str:
        return "maktaba"  # the file's container_name

    def compose(self, *args: str, **kw: Any) -> subprocess.CompletedProcess[str]:
        return docker("compose", "-p", "maktaba", "-f", str(self.file), *args, **kw)

    def shape(self) -> list[str]:
        return ["compose", "-f", self.spec.get("compose", {}).get("file", "?"), "up"]

    def prepare(self) -> None:
        engine = str(self.spec["compose"]["engine"])
        log(f"Docker {engine} in a dind container")
        self.teardown(quiet=True)
        self.dind_port, self.api_port = free_port(), free_port()
        docker("run", "-d", "--privileged", "--name", self.dind, "-e", "DOCKER_TLS_CERTDIR=",
               "-p", f"127.0.0.1:{self.dind_port}:2375", "-p", f"127.0.0.1:{self.api_port}:28088",
               f"docker:{engine}-dind", outer=True)
        TARGET.update({"DOCKER_HOST": f"tcp://127.0.0.1:{self.dind_port}",
                       "DOCKER_API_VERSION": str(self.spec["compose"].get("api", "1.43"))})

        def up() -> bool:
            return docker("version", "-f", "{{.Server.Version}}", check=False, timeout=20).returncode == 0

        wait("the dind engine", up, 120, every=2)
        self.engine = {
            "version": docker("version", "-f", "{{.Server.Version}}").stdout.strip(),
            "api": docker("version", "-f", "{{.Server.APIVersion}}").stdout.strip(),
            "compose": docker("compose", "version", "--short").stdout.strip(),
        }
        log(f"engine {self.engine}")
        # the file as Container Manager reads it, before anything else
        cfg = self.compose("config", check=False)
        (self.out / "compose-config.yaml").write_text((cfg.stdout or "") + (cfg.stderr or ""), encoding="utf-8")
        if cfg.returncode != 0:
            raise AssertionError(f"docker compose config: {cfg.stderr.strip()[-600:]}")
        # the commit's image under the file's name
        log("the image into the engine")
        save = subprocess.Popen(["docker", "save", self.image], stdout=subprocess.PIPE)
        load = subprocess.run(["docker", "load"], stdin=save.stdout, capture_output=True, text=True,
                              env={**os.environ, **TARGET}, timeout=900)
        save.wait(timeout=60)
        if save.returncode != 0 or load.returncode != 0:
            raise RuntimeError(f"docker save | load: {load.stderr[-400:]}")
        ref = re.search(r"^\s*image:\s*(\S+)", self.file.read_text(encoding="utf-8"), re.M)
        if not ref:
            raise AssertionError(f"no image in {self.file}")
        docker("tag", self.image, ref.group(1))
        # the file's host paths, inside the engine's container, as DSM has them
        docker("cp", f"{self.fixtures}/.", f"{self.dind}:/fixtures", outer=True)
        data = self.spec.get("data", {})
        media = self.spec.get("media", {})
        as_root(self.image,
                f"mkdir -p /h/docker/maktaba /h/Recordings && chown {data.get('owner', '0:100')} /h/docker/maktaba"
                f" && chmod {data.get('mode', '0770')} /h/docker/maktaba"
                f" && chown 0:100 /h/Recordings && chmod 0770 /h/Recordings"
                f" && mkdir -p /h/Movies && cp -a /src/. /h/Movies/ && {dsm_extras('/h/Movies')}"
                f" && chown -R {media.get('owner', '1024:100')} /h/Movies && chmod -R u=rwX,go=rX /h/Movies",
                "/volume1:/h", "/fixtures:/src:ro")
        docker("run", "--rm", "-i", "--entrypoint", "sh", "--user", "1026:100",
               "-v", f"/volume1/docker/maktaba:{VOLUME}", self.image, "-c",
               f"umask 077 && mkdir -p {CONFIG_DIR} && cat > {CONFIG_DIR}/server.toml",
               input=server_toml(self.quiet_window(), speech=bool(self.spec.get("speech"))))

    def setup(self) -> None:
        answers = {
            "admin": {"username": ADMIN, "password": self.password},
            "libraries": {"roots": self.roots, "path_map": []},
            "models": {"source": "huggingface", "hf_token": ""},
            "plex": {"action": "skip"},
        }
        log("setup --answers (docker compose run --rm maktaba setup)")
        t0 = time.monotonic()
        p = self.compose("run", "--rm", "-T", "maktaba", "setup", "--answers", "/dev/stdin", "--lang", "en",
                         *self.bundle(), input=json.dumps(answers), check=False, timeout=45 * 60)
        (self.out / "setup.log").write_text((p.stdout or "") + (p.stderr or ""), encoding="utf-8")
        self.timing("setup", t0)
        if p.returncode != 0:
            raise AssertionError(f"setup exited {p.returncode}: {(p.stdout + p.stderr).strip()[-600:]}")

    def start(self) -> None:
        log("docker compose up -d")
        self.compose("up", "-d", "--pull", "never")
        docker("run", "-d", "--name", "maktaba-relay", "--network", "host", "alpine/socat",
               "TCP-LISTEN:28088,fork,reuseaddr", "TCP:127.0.0.1:8088")
        self.api = API(f"http://127.0.0.1:{self.api_port}")

    def collect(self) -> None:
        super().collect()
        p = docker("logs", "-t", self.dind, check=False, outer=True, timeout=120)
        (self.out / "dind.log").write_text((p.stdout or "") + (p.stderr or ""), encoding="utf-8")
        (self.out / "engine.json").write_text(json.dumps(self.engine, indent=2), encoding="utf-8")

    def teardown(self, quiet: bool = False) -> None:
        docker("rm", "-f", "-v", self.dind, check=False, outer=True)
        TARGET.clear()


def urljoin(base: str, ref: str) -> str:
    from urllib.parse import urljoin as _j

    return _j(base, ref)


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--image", required=True)
    ap.add_argument("--cell", required=True)
    ap.add_argument("--fixtures", required=True, type=Path)
    ap.add_argument("--out", required=True, type=Path)
    ap.add_argument("--local", type=int, metavar="PORT", help="bridge network with PORT published (Docker Desktop)")
    ap.add_argument("--keep", action="store_true", help="leave the container and volumes")
    ap.add_argument("--plugin", type=Path, help="a third-party test plugin (.mkplugin) for the isolation cells (59.07)")
    args = ap.parse_args(argv)
    matrix = load_cells()
    spec = next((c for c in matrix["cells"] if c["name"] == args.cell), None)
    if spec is None:
        print(f"no cell {args.cell!r} in {CELLS}", file=sys.stderr)
        return 2
    kind = ComposeCell if spec.get("compose") else Cell
    cell = kind(spec=spec, image=args.image, fixtures=args.fixtures.resolve(), out=args.out.resolve(),
                local_port=args.local, landed=list(matrix.get("landed", [])),
                plugin=args.plugin.resolve() if args.plugin else None)
    try:
        ok = cell.run()
    finally:
        if not args.keep:
            cell.teardown(quiet=True)
    print((cell.out / "summary.md").read_text(encoding="utf-8"))
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())
