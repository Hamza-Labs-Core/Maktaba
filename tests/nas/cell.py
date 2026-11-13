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

Two kinds of cell start the published install files instead of a `docker run` of their own
(story 59.11): `"compose": {"blocks": [...]}` fills in cloud/installers/docker/docker-compose.yml
as a household does (those blocks on, the cell's image, user and volumes in place of the
file's) and runs it with `docker compose` (setup through `docker compose run --rm -T maktaba
setup`, then `up -d`); `"template": "unraid"` renders deploy/nas/unraid/maktaba.xml to the
`docker run` arguments Unraid makes (its user, environment, stop timeout and paths, the data
and media paths pointed at the cell's volumes; /dev/dri only where the runner has one). A
compose cell needs the host network (no --local).

An assertion or a cell whose `needs` are not all in cells.json's `landed` is skipped and
reported as pending. Exit 0 when every assertion that ran passed, 1 otherwise, 2 on a
usage error.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import posixpath
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
sys.path.insert(0, str(HERE))

import install_files  # noqa: E402  (tests/nas: the published compose's blocks, the Unraid template)

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

#: Story 59.08: the NAS's own mDNS responder, in a sibling container on the host's network
AVAHI_IMAGE = "debian:12-slim"
AVAHI_SCRIPT = """set -e
apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends \
  avahi-daemon avahi-utils dbus >/dev/null
# the NAS knows the user Maktaba's container runs as (a NAS account): the system bus looks
# up a caller's groups and drops a uid it does not know
getent passwd "$NAS_UID" >/dev/null || useradd -u "$NAS_UID" -M -s /usr/sbin/nologin maktaba-nas
mkdir -p /run/dbus && rm -f /run/dbus/pid /run/dbus/system_bus_socket
dbus-daemon --system --fork
avahi-daemon --no-chroot --no-rlimits -D
for i in $(seq 1 60); do avahi-daemon -c && break; sleep 1; done
# ready once Avahi owns its name on the bus (Maktaba asks the bus that, once, at its start)
for i in $(seq 1 60); do
  dbus-send --system --print-reply --dest=org.freedesktop.DBus / org.freedesktop.DBus.GetNameOwner \
    string:org.freedesktop.Avahi >/dev/null 2>&1 && break
  sleep 1
done
avahi-publish-service "NAS share" _smb._tcp 445 >/tmp/publish.log 2>&1 &
touch /tmp/avahi-ready
exec sleep infinity
"""


def host_ip() -> str:
    """The runner's LAN address, where a bridge cell's published ports answer."""
    import socket

    with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as s:
        s.connect(("192.0.2.1", 9))
        return str(s.getsockname()[0])


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
        self.retry_after = 5.0  # the last 429's Retry-After, in seconds

    def login(self) -> tuple[int, Any]:
        assert self.credentials is not None
        self.token = ""
        s, d = self.call("POST", "/api/auth/login", self.credentials, retry=False)
        if s == 200 and isinstance(d, dict) and d.get("access_token"):
            self.token = d["access_token"]
        return s, d

    def call(self, method: str, path: str, body: Any = None, raw: bool = False,
             timeout: float = 30, retry: bool = True) -> tuple[int, Any]:
        s, d = self._paced(method, path, body, raw, timeout)
        if s == 401 and retry and self.token and self.credentials:
            self.login()  # the access token lasts minutes; a cell's waits are longer
            s, d = self._paced(method, path, body, raw, timeout)
        return s, d

    #: how long the harness waits out the server's per-user rate limit (429) in all
    RATE_WAIT_SEC = 180.0

    def _paced(self, method: str, path: str, body: Any, raw: bool, timeout: float) -> tuple[int, Any]:
        """One call, waiting out a 429: the harness's own reads (paging 20,000 videos, a
        poll loop) can pass the per-user limit a person never reaches; the server says how
        long to wait (Retry-After), else 5 s, for at most :attr:`RATE_WAIT_SEC` in all."""
        waited = 0.0
        while True:
            s, d = self._call(method, path, body, raw, timeout)
            if s != 429 or waited >= self.RATE_WAIT_SEC:
                return s, d
            pause = min(max(self.retry_after, 1.0), 30.0, self.RATE_WAIT_SEC - waited)
            time.sleep(pause)
            waited += pause

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
            if e.code == 429:
                try:
                    self.retry_after = float(e.headers.get("Retry-After") or 5)
                except ValueError:  # an HTTP date: the default wait
                    self.retry_after = 5.0
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
        if self.spec.get("user"):
            return self.spec["user"]
        if self.spec.get("template") == "unraid":
            return install_files.read_template().user()
        return ""

    @property
    def compose(self) -> dict[str, Any] | None:
        return self.spec.get("compose")

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
        if self.spec.get("template") == "unraid":
            return self.unraid_shape()
        a: list[str] = []
        if self.user:
            a += ["--user", self.user]
        if self.spec.get("seccomp"):
            a += ["--security-opt", f"seccomp={HERE / self.spec['seccomp']}"]
        for k, v in self.spec.get("env", {}).items():
            a += ["-e", f"{k}={v}"]
        if self.bridged:
            a += ["-e", f"MAKTABA_HOST_IP={host_ip()}"]
            # a published port that differs from the container's is named to it (story 59.08)
            for inner, var in ((8088, "MAKTABA_PUBLIC_WEB_PORT"), (8080, "MAKTABA_PUBLIC_API_PORT"),
                               (8081, "MAKTABA_PUBLIC_STREAM_PORT")):
                if self.published().get(inner, inner) != inner:
                    a += ["-e", f"{var}={self.published()[inner]}"]
        if self.spec.get("avahi") == "dbus":
            # the NAS's system bus (the sibling's), as a household mounts the host's (59.08)
            a += ["-v", f"{self.vol('dbus')}:/run/dbus"]
        a += list(self.spec.get("docker", []))
        return a + self.mounts()

    @property
    def bridged(self) -> bool:
        """The cell runs on Docker's default bridge (story 59.08), its ports published."""
        return self.spec.get("network") == "bridge"

    def unraid_shape(self) -> list[str]:
        """The Unraid template as dockerMan runs it (59.11), its paths on the cell's volumes and
        its network left to network(); the cell's own user must be the template's."""
        t = install_files.read_template()
        if self.spec.get("user") and self.spec["user"] != t.user():
            raise AssertionError(f"the cell's user {self.spec['user']} is not the template's {t.user()}")
        media = [c.name for c in t.by_type("Path") if c.target != VOLUME]
        paths = {c.name: (self.vol("data"), VOLUME) for c in t.by_type("Path") if c.target == VOLUME}
        paths[media[0]] = (self.vol("media"), MEDIA)
        args = install_files.render(t, paths=paths, devices=Path("/dev/dri").is_dir(), image="-")[:-1]
        i = args.index("--net")
        return args[:i] + args[i + 2:]

    def compose_text(self) -> str:
        """The published compose file filled in for this cell, as maktaba.tv's install page
        fills it in: the cell's blocks on, its image, user and volumes in the file's places."""
        if self.local_port:
            raise AssertionError("a compose cell runs on the host network (no --local)")
        for k in ("seccomp", "docker", "env", "noaccess_root"):
            if self.spec.get(k):
                raise AssertionError(f"a compose cell takes no {k!r}")
        text = install_files.enable(install_files.COMPOSE.read_text(encoding="utf-8"),
                                    tuple((self.compose or {}).get("blocks", [])))
        media = self.spec.get("media", {})
        subs = [
            ("image: ghcr.io/hamza-labs-core/maktaba-server:latest", f"image: {self.image}"),
            ("container_name: maktaba", f"container_name: {self.container}"),
            ("- maktaba:/var/lib/maktaba", f"- data:{VOLUME}"),
            ("- /srv/media:/srv/media", f"- media:{MEDIA}" + (":ro" if media.get("ro") else "")),
            ("\nvolumes:\n  maktaba:\n", "\nvolumes:\n"
             f"  data:\n    external: true\n    name: {self.vol('data')}\n"
             f"  media:\n    external: true\n    name: {self.vol('media')}\n"),
        ]
        if self.user:
            subs.append(('user: "1000:1000"', f'user: "{self.user}"'))
        for old, new in subs:
            if text.count(old) != 1:
                raise AssertionError(f"the published compose file no longer has {old.strip()!r} once")
            text = text.replace(old, new)
        return text

    def compose_cmd(self, *args: str, **kw: Any) -> subprocess.CompletedProcess[str]:
        f = self.out / "docker-compose.yml"
        return docker("compose", "-p", self.container, "-f", str(f), *args, **kw)

    @property
    def web_port(self) -> int:
        """The web port the cell reaches the server at: the cell's `ports.web` (the port it sets
        in the container, story 59.08), its bridge's published web port, else 8088."""
        published = self.published()
        if published:
            return published[8088]
        return int(self.spec.get("ports", {}).get("web", 8088))

    def published(self) -> dict[int, int]:
        """A bridge cell's published ports, container port -> the host's: 8088, 8080 and 8081
        as themselves, or the cell's `publish` (story 59.08: a NAS that publishes 19088:8088)."""
        if not self.bridged:
            return {}
        out = {8088: 8088, 8080: 8080, 8081: 8081}
        out.update({int(k): int(v) for k, v in self.spec.get("publish", {}).items()})
        return out

    def network(self) -> list[str]:
        if self.local_port:
            return ["-p", f"127.0.0.1:{self.local_port}:28088"]
        if self.bridged:
            # the bridge variant of the compose files: 8088, 8080 and 8081, never 54329
            return [a for inner, outer in self.published().items() for a in ("-p", f"{outer}:{inner}")]
        return ["--network", "host"]

    def setup_network(self) -> list[str]:
        return [] if self.local_port or self.bridged else ["--network", "host"]

    # -- steps

    def prepare(self) -> None:
        log(f"volumes for {self.name}")
        self.teardown(quiet=True)
        data = self.spec.get("data", {})
        owner = data.get("owner", "1000:1000")
        mode = data.get("mode", "0750")
        for v in ("data", "media", "noaccess", "dbus"):
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
        if self.compose is not None:
            (self.out / "docker-compose.yml").write_text(self.compose_text(), encoding="utf-8")
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
        if self.compose is not None:
            p = self.compose_cmd("run", "--rm", "-T", "maktaba", "setup", "--answers", "/dev/stdin", "--lang", "en",
                                 *self.bundle(), input=json.dumps(answers), check=False, timeout=45 * 60)
        else:
            p = docker("run", "--rm", "-i", *self.shape(), *self.setup_network(),
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
        if self.spec.get("avahi"):
            self.start_avahi()
        log("start the server container")
        if self.compose is not None:
            self.compose_cmd("up", "-d", "--pull", "never")
            self.api = API("http://127.0.0.1:8088")
            return
        docker("run", "-d", "--name", self.container, *self.shape(), *self.network(), self.image)
        if self.local_port:
            docker("run", "-d", "--name", self.container + "-relay", "--network", f"container:{self.container}",
                   "alpine/socat", "TCP-LISTEN:28088,fork,reuseaddr", f"TCP:127.0.0.1:{self.web_port}")
        self.api = API(f"http://127.0.0.1:{self.local_port or self.web_port}")

    # -- story 59.08: the NAS's own mDNS responder beside Maktaba

    @property
    def avahi(self) -> str:
        return self.container + "-avahi"

    def start_avahi(self) -> None:
        """The NAS's avahi-daemon in a sibling container on the host's network: its own
        system bus (shared with Maktaba in the dbus cell), avahi-daemon dropping root to its
        own `avahi` user (another uid than Maktaba's), and a share it publishes, as a NAS
        publishes its SMB service."""
        log("the NAS's avahi-daemon, beside Maktaba")
        mounts = ["-v", f"{self.vol('dbus')}:/run/dbus"]
        uid = (self.user or "1000").split(":")[0]
        docker("run", "-d", "--name", self.avahi, "--network", "host", "-e", f"NAS_UID={uid}", *mounts, AVAHI_IMAGE,
               "sh", "-c", AVAHI_SCRIPT)

        def up() -> bool:
            st = docker("inspect", "-f", "{{.State.Running}}", self.avahi, check=False).stdout.strip()
            if st != "true":
                raise AssertionError("the avahi container stopped: " + docker("logs", self.avahi, check=False).stderr[-600:])
            return docker("exec", self.avahi, "test", "-e", "/tmp/avahi-ready", check=False).returncode == 0

        wait("avahi-daemon in its container", up, 10 * 60)

    def avahi_sh(self, script: str, timeout: int = 120) -> str:
        p = docker("exec", self.avahi, "sh", "-c", script, check=False, timeout=timeout)
        return (p.stdout or "") + (p.stderr or "")

    def browse(self) -> list[list[str]]:
        """avahi-browse's resolved services (=;iface;proto;name;type;domain;host;addr;port;txt)."""
        out = self.avahi_sh("timeout 20 avahi-browse -a -r -t -p -k 2>/dev/null || true")
        return [line.split(";") for line in out.splitlines() if line.startswith("=;")]

    def host_name(self) -> str:
        return self.avahi_sh("hostname").strip().split(".")[0]

    def maktaba_seen(self) -> list[str]:
        """Waits for avahi-browse to list the NAS's share and Maktaba; Maktaba's record."""
        found: dict[str, list[str]] = {}

        def both() -> bool:
            for rec in self.browse():
                if len(rec) > 6 and rec[4] in ("_maktaba._tcp", "_smb._tcp"):
                    found[rec[4]] = rec
            return "_maktaba._tcp" in found and "_smb._tcp" in found

        try:
            wait("the NAS's share and Maktaba in avahi-browse", both, 5 * 60, every=10)
        finally:
            (self.out / "avahi-browse.txt").write_text(self.avahi_sh("timeout 20 avahi-browse -a -r -t -p -k || true"),
                                                       encoding="utf-8")
        return found["_maktaba._tcp"]

    def nas_name_intact(self) -> str:
        host = self.host_name()
        out = self.avahi_sh(f"avahi-resolve -4 -n {host}.local")
        if host not in out or "Failed" in out:
            raise AssertionError(f"the NAS's own name does not resolve: {out.strip()}")
        log_text = docker("logs", self.avahi, check=False)
        if "Host name conflict" in (log_text.stdout or "") + (log_text.stderr or ""):
            raise AssertionError("avahi-daemon renamed the NAS (a host name conflict)")
        return host

    def assert_mdns_own(self) -> str:
        """T02: no D-Bus mount: Maktaba's own responder answers beside the NAS's avahi-daemon,
        under <host>-maktaba; avahi-browse shows both, and the NAS keeps its name."""
        rec = self.maktaba_seen()
        host = self.nas_name_intact()
        if rec[6].lower() != f"{host}-maktaba.local".lower():
            raise AssertionError(f"Maktaba announced at {rec[6]}, not {host}-maktaba.local")
        own = self.avahi_sh(f"avahi-resolve -4 -n {host}-maktaba.local")
        if "Failed" in own or not own.strip():
            raise AssertionError(f"{host}-maktaba.local does not resolve: {own.strip()}")
        top = docker("top", self.container, "-eo", "pid,comm").stdout
        if "avahi-publish" in top:
            raise AssertionError("avahi-publish-service runs without a D-Bus mount")
        return f"{rec[3]} at {rec[6]}:{rec[8]} beside the NAS's _smb._tcp; {host}.local intact"

    def assert_mdns_dbus(self) -> str:
        """T08: the system bus mounted: Maktaba publishes through the NAS's Avahi, at the
        NAS's own name."""
        rec = self.maktaba_seen()
        host = self.nas_name_intact()
        if rec[6].lower() != f"{host}.local".lower():
            clog = docker("logs", self.container, check=False)
            said = [ln for ln in ((clog.stdout or "") + (clog.stderr or "")).splitlines() if "advertising _maktaba" in ln]
            raise AssertionError(f"Maktaba announced at {rec[6]}, not through Avahi at {host}.local"
                                 f" ({said[-1].strip() if said else 'no advertising line'})")
        top = docker("top", self.container, "-eo", "pid,comm").stdout
        if "avahi-publish" not in top:
            raise AssertionError("no avahi-publish-service in the container")
        return f"{rec[3]} at {rec[6]}:{rec[8]} through the NAS's Avahi"

    def assert_bridge(self) -> str:
        """T04: on a bridge mDNS and SSDP are off, setup and the log say how apps connect,
        public_url comes from MAKTABA_HOST_IP, and an app connects by the address."""
        ip = host_ip()
        url = f"http://{ip}:{self.web_port}"
        m = self.machine()
        net = (m.get("machine") or {}).get("network") or {}
        if net.get("mode") != "bridge":
            raise AssertionError(f"network read as {net}")
        if m.get("web_url") != url:
            raise AssertionError(f"web_url {m.get('web_url')!r}, want {url}")
        text = "This container is on a bridge network"
        setup_log = (self.out / "setup.log").read_text(encoding="utf-8")
        if text not in setup_log or url not in setup_log:
            raise AssertionError("setup did not say the bridge text with the address")
        clog = docker("logs", self.container, check=False)
        clog_text = (clog.stdout or "") + (clog.stderr or "")
        for want in ("network: bridge", "mDNS and SSDP off", text, "شبكة جسرية"):
            if want not in clog_text:
                raise AssertionError(f"the container log lacks {want!r}")
        if "advertising _maktaba._tcp" in clog_text:
            raise AssertionError("mDNS announced on a bridge")
        # an app on the LAN: the host's address, the published port
        s, _ = API(url).get("/api/system/health", timeout=10)
        if s != 200:
            raise AssertionError(f"{url}/api/system/health: {s}")
        return f"{net.get('interface')} ({net.get('driver')}), {url} answers"

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

    def assert_healthcheck(self) -> str:
        """59.10: the image's own healthcheck (what the TrueNAS app's compose runs, by `docker
        exec` as the container's user) says healthy once the web app answers."""
        p = docker("exec", self.container, "/opt/maktaba/server/bin/maktaba-server", "healthcheck", check=False)
        if p.returncode != 0 or "answered 200" not in p.stdout:
            raise AssertionError(f"healthcheck exited {p.returncode}: {(p.stdout + p.stderr).strip()[-400:]}")
        return p.stdout.strip()

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

    def local_form(self, path: str) -> str:
        """A stored path in the home's own form: smb://<nas>/Movies/en/x.mp4, as a NAS home
        stores its own folders once converted (59.13), is /volume1/Movies/en/x.mp4 (the
        share's folder beside the cell's media folder); any other path as it is."""
        if not path.startswith("smb://"):
            return path
        rest = path[len("smb://"):].split("/", 1)
        return f"{posixpath.dirname(self.media.rstrip('/'))}/{rest[1] if len(rest) > 1 else ''}"

    def under(self, root: str) -> list[dict[str, Any]]:
        prefix = root.rstrip("/") + "/"
        return [v for v in self.videos() if self.local_form(v.get("path") or "").startswith(prefix)]

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
        share = self.media.rstrip("/").rsplit("/", 1)[-1]

        def under(r: str) -> bool:
            # a NAS home converts its own folders to their share paths (59.13):
            # /volume1/Movies/en → smb://<nas>/Movies/en
            return r.startswith(self.media + "/") or (r.startswith("smb://") and self.share(r) == share)

        libs = [lib for lib in self.libraries() if any(under(r) for r in lib.get("roots") or [])]
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

    def assert_platform(self) -> str:
        """GET /api/server/machine names the platform the install file set (MAKTABA_PLATFORM)."""
        m = self.machine().get("machine") or {}
        want = self.spec.get("platform", "")
        if m.get("platform") != want or m.get("platform_from") != "env":
            raise AssertionError(f"platform {m.get('platform')!r} ({m.get('platform_from')!r}), not {want!r} from the environment")
        return f"{want} (MAKTABA_PLATFORM)"

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
        """/volume1/Movies/en → Movies; smb://<nas>/Movies/en (the home converts its own
        folders to their shares since 59.13) → Movies."""
        if path.startswith("smb://"):
            return path[len("smb://"):].split("/")[1]
        return path.strip("/").split("/")[1]

    @staticmethod
    def on_dsm_share(root: str) -> bool:
        """A library root on one of the NAS's shares: its own path, or the share path."""
        return root.startswith(("/volume", "smb://"))

    def assert_fix_dsm(self) -> str:
        """Each root that needs one (Read only, No access) has DSM's fix (library.fix.dsm),
        naming its shared folder."""
        seen = []
        libs = [lib for lib in self.libraries() if any(self.on_dsm_share(r) for r in lib.get("roots") or [])]
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
        lib = next((x for x in self.libraries()
                    if any(r == root or r.endswith("/Recordings") for r in x.get("roots") or [])), None)
        if lib is None:
            s, d = self.api.call("POST", "/api/libraries", {"name": "Recordings", "roots": [root]})
            if s not in (200, 201) or not isinstance(d, dict):
                raise AssertionError(f"POST /api/libraries {root}: {s} {d}")
            lib = d
        rep = self.check_access(lib)
        if rep.get("access") != "rw":
            raise AssertionError(f"{root}: access {rep.get('access')!r}, not rw: {rep}")
        return f"{ro}; Recordings: read and write"

    # -- story 59.13: a NAS as the cluster's home

    def assert_nas_home_shares(self) -> str:
        """The home keeps a record of the NAS itself (GET /api/cluster/storage/home-shares):
        its own folder /volume1/Movies is the share Movies by DSM's layout, at the NAS's own
        path, so a node's /Volumes/Movies matches it with no path map; and with every shared
        folder read-only (access_ro, access_none before this) the join notice
        no_writable_share is there. The pipeline completes the record at its storage check
        (at start, then every five minutes)."""
        assert self.api is not None

        def ready() -> dict[str, Any] | None:
            s, d = self.api.get("/api/cluster/storage/home-shares")  # type: ignore[union-attr]
            if s != 200 or not isinstance(d, dict):
                raise AssertionError(f"GET /api/cluster/storage/home-shares: {s} {d}")
            movies = [x for x in d.get("shares") or [] if x.get("path") == "/volume1/Movies"]
            return d if d.get("nas") and movies else None

        d = wait("the NAS's own Movies share", ready, 420, every=10)
        movies = next(x for x in d["shares"] if x["path"] == "/volume1/Movies")
        if movies.get("share") != "Movies" or movies.get("rule") != "Movies" or not movies.get("host_path"):
            raise AssertionError(f"/volume1/Movies: {movies}, want the share Movies at the NAS's own path")
        if d.get("platform") != "dsm" or not d.get("server"):
            raise AssertionError(f"home {d.get('server')!r} on {d.get('platform')!r}, want a named dsm home")
        if "no_writable_share" not in (d.get("notices") or []):
            raise AssertionError(f"every share is read-only, yet no no_writable_share notice: {d}")
        return f"smb://{d['server']}/Movies ← /volume1/Movies; notices {d['notices']}"

    # The behaviour change 59.13 brings to an existing NAS home: once the home keeps its own
    # record, the automatic conversion (story 31.3) rewrites its stored paths from
    # /volume1/Movies/... to smb://<nas>/Movies/... . These prove the home still works on
    # what it converted, and that a home with no NAS record is left alone.

    def media_paths(self) -> dict[str, str]:
        """Every video of the cell's media (not the no-access library): id → stored path."""
        out: dict[str, str] = {}
        for v in self.videos():
            p = v.get("path") or ""
            if self.local_form(p).startswith(self.media.rstrip("/") + "/"):
                if v["id"] in out:
                    raise AssertionError(f"video {v['id']} listed twice")
                out[v["id"]] = p
        return out

    def canon(self) -> dict[str, Any]:
        """GET /api/cluster/storage/canonicalize: the automatic conversion's state."""
        assert self.api is not None
        s, d = self.api.get("/api/cluster/storage/canonicalize")
        if s != 200 or not isinstance(d, dict):
            raise AssertionError(f"GET /api/cluster/storage/canonicalize: {s} {d}")
        st = d.get("state")
        return st if isinstance(st, dict) else {}

    def ask_canon_check(self) -> None:
        """Check now (the Machines page's button): the home looks at once; the share map's
        settle (storage.map_settle_minutes) still holds a conversion back."""
        assert self.api is not None
        s, d = self.api.call("POST", "/api/cluster/storage/canonicalize", {"action": "run"})
        if s not in (200, 202, 204):
            raise AssertionError(f"POST /api/cluster/storage/canonicalize run: {s} {d}")

    def rescan_settled(self) -> str:
        """Scan every library again and wait until the scan queue is idle with the new
        scans done."""
        assert self.api is not None
        api = self.api

        def scan_counts() -> dict[str, int]:
            s, d = api.get("/api/queues")
            if s != 200 or not isinstance(d, dict):
                raise AssertionError(f"GET /api/queues: {s} {d}")
            q = next((q for q in d.get("queues") or [] if q.get("stage") == "scan"), None)
            return dict((q or {}).get("counts") or {})

        before = scan_counts().get("done_24h", 0)
        self.scan_all()
        wanted = len([lib for lib in self.libraries() if NOACCESS not in (lib.get("roots") or [])])

        def idle() -> str:
            c = scan_counts()
            busy = c.get("pending", 0) + c.get("ready", 0) + c.get("running", 0)
            # a scan asked while one waits is merged into it: at least one more done
            if busy == 0 and c.get("done_24h", 0) >= before + min(wanted, 1):
                return f"{c.get('done_24h', 0) - before} scan(s) done"
            if c.get("failed_open", 0):
                raise AssertionError(f"a scan failed: {c}")
            return ""

        done = wait("the rescan of the converted libraries", idle, 10 * 60, every=10)
        time.sleep(20)  # the scan's last inserts commit with its job; a margin for the listing
        return done

    def subtitle_served(self, video_id: str, lang: str, words: str) -> str:
        """A transcode session of the video lists its subtitle in lang as an HLS subtitle
        rendition (EXT-X-MEDIA TYPE=SUBTITLES, URI subs-<lang>/index.m3u8), and the
        rendition's first file has the subtitle's words: the streaming service found it (a
        side-car beside the file at its stored path, else the database's copy a read-only
        library keeps)."""
        assert self.api is not None
        api = self.api
        s, d = api.call("POST", "/api/stream/sessions",
                        {"video_id": video_id, "force_transcode": True, "force_software": True})
        if s not in (200, 201) or not isinstance(d, dict) or not d.get("manifest_url"):
            raise AssertionError(f"POST /api/stream/sessions: {s} {d}")
        master = urljoin(api.base, d["manifest_url"])
        try:
            def fetch(url: str) -> str:
                s, b = api.get(url, raw=True, timeout=60)
                if s != 200:
                    raise AssertionError(f"GET {url}: {s} {b[:200]!r}")
                return b.decode("utf-8", "replace")

            def subs_uri() -> str:
                for line in fetch(master).splitlines():
                    if line.startswith("#EXT-X-MEDIA:TYPE=SUBTITLES"):
                        m = re.search(r'URI="([^"]+)"', line)
                        if m and f"subs-{lang}/" in m.group(1):
                            return m.group(1)
                return ""

            index = urljoin(master, wait(f"the {lang} subtitle rendition in the master playlist", subs_uri, 180, every=3))

            def first(url: str) -> str:
                return next((ln.strip() for ln in fetch(url).splitlines() if ln and not ln.startswith("#")), "")

            vtt = urljoin(index, wait("a file in the subtitle playlist", lambda: first(index), 120, every=3))
            text = fetch(vtt)
            if words not in text:
                raise AssertionError(f"the subtitle has no {words!r}: {text[:300]!r}")
            return f"{len(text)} bytes with {words!r}"
        finally:
            api.call("DELETE", f"/api/stream/sessions/{d.get('session_id')}")

    #: runs one statement with the pipeline process's own database address and the venv's asyncpg;
    #: prints the rows as JSON (argv: the statement, its arguments as a JSON list)
    SQL_SCRIPT = """
import asyncio, glob, json, os, sys
import asyncpg
dsn = None
for env in glob.glob("/proc/[0-9]*/environ"):
    try:
        cmd = open(env[:-len("environ")] + "cmdline", "rb").read()
        kvs = open(env, "rb").read().split(b"\\0")
    except OSError:
        continue
    if b"maktaba_pipeline" not in cmd:
        continue
    dsn = next((kv.split(b"=", 1)[1].decode() for kv in kvs
                if kv.startswith((b"MAKTABA_DATABASE_URL=", b"DATABASE_URL="))), None)
    if dsn:
        # the address never carries the password: the process's passfile does (PGPASSFILE
        # and the other PG* variables the supervisor gives it), and asyncpg reads them too
        for kv in kvs:
            k, _, v = kv.partition(b"=")
            if k.startswith(b"PG"):
                os.environ[k.decode()] = v.decode()
        break
if not dsn:
    sys.exit("no pipeline process with MAKTABA_DATABASE_URL or DATABASE_URL")
async def main():
    c = await asyncpg.connect(dsn)
    try:
        rows = await c.fetch(sys.argv[1], *json.loads(sys.argv[2]))
    finally:
        await c.close()
    print(json.dumps([dict(r) for r in rows], default=str, ensure_ascii=False))
asyncio.run(main())
"""

    def sql(self, statement: str, *args: Any) -> list[dict[str, Any]]:
        """One statement in the home's database, as the pipeline connects to it."""
        user = ["-u", self.user] if self.user else []
        p = docker("exec", "-i", *user, self.container, self.venv_python(), "-", statement,
                   json.dumps(list(args), ensure_ascii=False), input=self.SQL_SCRIPT, check=False, timeout=120)
        if p.returncode != 0:
            raise AssertionError(f"SQL {statement[:80]!r}: {(p.stdout + p.stderr).strip()[-500:]}")
        return list(json.loads(p.stdout.strip().splitlines()[-1]))

    #: the database copy of a generated subtitle, as the subtitles stage keeps it for a
    #: library read-only for Maktaba (story 59.03); seeded: this cell runs no transcription
    STORED_SUB_WORDS = "The library opens at nine in the morning."

    def seed_stored_subtitle(self, video_id: str, path: str) -> None:
        body = f"WEBVTT\n\n00:00:00.000 --> 00:00:03.000\n{self.STORED_SUB_WORDS}\n"
        self.sql("INSERT INTO subtitle_files (video_id, language, format, source, path, byte_size, sha256,"
                 " is_external, metadata, content) VALUES ($1::uuid, 'en', 'vtt', 'generated', $2, $3, $4,"
                 " false, '{\"stored\": \"database\", \"seeded_by\": \"tests/nas nas_converted\"}'::jsonb, $5)",
                 video_id, path, len(body.encode()), hashlib.sha256(body.encode()).hexdigest(), body)

    def stored_subtitle_rows(self, video_id: str) -> list[dict[str, Any]]:
        return self.sql("SELECT path, deleted_at, content IS NOT NULL AS in_db FROM subtitle_files"
                        " WHERE video_id = $1::uuid AND language = 'en' ORDER BY id", video_id)

    def assert_nas_converted(self) -> str:
        """After the share map's settle the home converts its own folders' paths
        (smb://<nas>/Movies/..., same video ids, nothing lost or added), and keeps working
        on them: a rescan of the converted libraries finds the same files (no duplicate, no
        removal), a clip added afterwards is stored in the share form and gets its poster
        from that path (the thumbnail stage runs ffmpeg on the file), a transcode plays, the read-only library's stored subtitle
        (subtitle_files.content, seeded as the subtitles stage keeps it: this cell
        transcribes nothing) has its path converted, stays the one live row and is served,
        the Arabic side-car beside a converted file reaches the player, and Check access
        still reads the converted roots."""
        assert self.api is not None
        api = self.api
        before = self.media_paths()
        server = self.home_server()
        early = [p for p in before.values() if p.startswith("smb://")]
        if early:
            raise AssertionError(f"{len(early)} paths converted before the check started (first {early[0]}); "
                                 "the settle should have held them")
        lesson = next((i for i, p in before.items() if p.endswith("/ar/درس 01.mp4")), None)
        stored = next((i for i, p in before.items() if p.endswith("/en/Lesson 02.mp4")), None)
        if lesson is None or stored is None:
            raise AssertionError("درس 01 or Lesson 02 is not in the library")
        # the read-only library's database copy of a subtitle (subtitle_files.content), as
        # the subtitles stage keeps it: served before the conversion
        sub_path = f"{self.media.rstrip('/')}/en/Lesson 02.en.vtt"
        self.seed_stored_subtitle(stored, sub_path)
        s, subs0 = api.get(f"/api/videos/{stored}/subtitles")
        langs0 = sorted(x.get("language") for x in (subs0 or {}).get("items") or [])
        if s != 200 or langs0 != ["en"]:
            raise AssertionError(f"Lesson 02's subtitles before: {s} {subs0}")
        self.subtitle_served(stored, "en", self.STORED_SUB_WORDS)

        t0 = time.monotonic()
        self.ask_canon_check()

        def converted() -> dict[str, Any] | None:
            st = self.canon()
            if st.get("phase") == "failed":
                raise AssertionError(f"the conversion failed: {st.get('problems')}")
            last = st.get("last") or {}
            return st if st.get("phase") == "done" and last.get("changes") else None

        st = wait("the home's conversion of its own paths (after the share map's settle)", converted, 30 * 60, every=15)
        self.canon_batch = (st.get("last") or {}).get("batch")
        took = round((time.monotonic() - t0) / 60, 1)

        def expected(p: str) -> str:
            rel = p[len(posixpath.dirname(self.media.rstrip("/"))) + 1:]
            return f"smb://{server}/{rel}"

        after = self.media_paths()
        if set(after) != set(before):
            raise AssertionError(f"ids changed: lost {sorted(set(before) - set(after))[:3]}, new {sorted(set(after) - set(before))[:3]}")
        wrong = [(before[i], after[i]) for i in before if after[i] != expected(before[i])]
        if wrong:
            raise AssertionError(f"{len(wrong)} paths not in the share form, first {wrong[0]}")

        scans = self.rescan_settled()
        again = self.media_paths()
        if again != after:
            lost = sorted(set(after) - set(again))
            new = sorted(set(again) - set(after))
            moved = [i for i in after if i in again and again[i] != after[i]]
            raise AssertionError(f"the rescan changed the library: lost {len(lost)}, new {len(new)} "
                                 f"(first {[again[i] for i in new[:2]]}), moved {len(moved)}")
        dups = len(again) - len(set(again.values()))
        if dups:
            raise AssertionError(f"{dups} duplicate paths after the rescan")

        # a clip added to the converted library (from the host: the container's mount is
        # read-only): the rescan stores it in the share form, and the pipeline's own stages
        # (probe, hash, thumbnail: ffmpeg on the file) make its poster from that path
        name = "Lesson 21 (after the conversion).mp4"
        m_owner = self.spec.get("media", {}).get("owner", "1000:1000")
        as_root(self.image,
                f"cp '/d/en/Lesson 01.mp4' '/d/en/{name}' && printf 'maktaba 59.13' >> '/d/en/{name}'"
                f" && chown {m_owner} '/d/en/{name}' && chmod 644 '/d/en/{name}'",
                f"{self.vol('media')}:/d")
        self.rescan_settled()
        grown = self.media_paths()
        added = {i: p for i, p in grown.items() if i not in again}
        want_new = f"smb://{server}/{posixpath.basename(self.media.rstrip('/'))}/en/{name}"
        if list(added.values()) != [want_new] or any(grown[i] != again[i] for i in again):
            raise AssertionError(f"the rescan after a new clip: added {added}, want only {want_new}")
        new_id = next(iter(added))

        def poster() -> int:
            st_, b_ = api.get(f"/api/videos/{new_id}/poster?size=small", raw=True)
            return len(b_) if st_ == 200 and isinstance(b_, bytes) and len(b_) > 100 else 0

        poster_bytes = wait("the new clip's poster (made from its share path)", poster, 15 * 60, every=10)
        thumb = f"new clip at {want_new}: poster {poster_bytes} bytes"
        trans = self.transcode(force_software=True)

        # the stored subtitle: its path converted with the rest, still the one live row (the
        # rescan neither deleted nor doubled it), listed and served as before
        rows = self.stored_subtitle_rows(stored)
        want_sub = f"smb://{server}/{sub_path[len(posixpath.dirname(self.media.rstrip('/'))) + 1:]}"
        live = [r for r in rows if r.get("deleted_at") in (None, "None")]
        if len(live) != 1 or live[0].get("path") != want_sub or not live[0].get("in_db"):
            raise AssertionError(f"Lesson 02's subtitle rows after: {rows}, want one live at {want_sub}")
        s, subs1 = api.get(f"/api/videos/{stored}/subtitles")
        langs1 = sorted(x.get("language") for x in (subs1 or {}).get("items") or [])
        if s != 200 or langs1 != langs0:
            raise AssertionError(f"Lesson 02's subtitles after: {langs1}, before {langs0}")
        sub = self.subtitle_served(stored, "en", self.STORED_SUB_WORDS)
        # the side-car beside a converted file: found through the converted path
        side = self.subtitle_served(lesson, "ar", "تفتح المكتبة")
        access = self.assert_access_ro()  # Check access on the converted roots, through the API's share map
        return (f"{len(after)} paths → smb://{server}/… in {took} min (batch {self.canon_batch}); rescan: {scans}, "
                f"same {len(again)} videos; {thumb}; {trans}; "
                f"stored subtitle at {want_sub}: {sub}; side-car: {side}; access: {access}")

    def home_server(self) -> str:
        assert self.api is not None
        s, d = self.api.get("/api/cluster/storage/home-shares")
        if s != 200 or not isinstance(d, dict) or not d.get("server"):
            raise AssertionError(f"GET /api/cluster/storage/home-shares: {s} {d}")
        return str(d["server"])

    def storage_servers(self) -> list[dict[str, Any]]:
        assert self.api is not None
        s, d = self.api.get("/api/cluster/storage-servers")
        if s != 200 or not isinstance(d, dict):
            raise AssertionError(f"GET /api/cluster/storage-servers: {s} {d}")
        return list(d.get("servers") or [])

    def assert_nas_restart_idempotent(self) -> str:
        """After nas_converted, the container restarted on the same volume: the home
        checks again and converts nothing (the same last batch, no new run), every path and
        id is as it was, and the NAS keeps one record of itself with the same shares."""
        assert self.api is not None
        batch = getattr(self, "canon_batch", None)
        if not batch:
            raise AssertionError("run after nas_converted")
        paths = self.media_paths()
        servers = {x.get("name"): x.get("exports") for x in self.storage_servers()}
        checked = self.canon().get("updated_at")
        log("restart the container on the same volume")
        docker("restart", "-t", "60", self.container)
        self.wait_ready()
        self.ask_canon_check()

        def looked() -> dict[str, Any] | None:
            st = self.canon()
            return st if st.get("updated_at") not in (None, checked) and st.get("phase") not in (
                "backing_up", "pausing", "applying", "verifying") else None

        st = wait("the home's conversion check after the restart", looked, 15 * 60, every=10)
        last = st.get("last") or {}
        if st.get("phase") != "done" or last.get("batch") != batch or st.get("run"):
            raise AssertionError(f"after the restart: phase {st.get('phase')}, last batch {last.get('batch')} "
                                 f"(was {batch}), run {st.get('run')}, problems {st.get('problems')}")
        if self.media_paths() != paths:
            raise AssertionError("the paths changed across the restart")
        stored = next((i for i, p in paths.items() if p.endswith("/en/Lesson 02.mp4")), None)
        if stored is not None:
            rows = [r for r in self.stored_subtitle_rows(stored) if r.get("deleted_at") in (None, "None")]
            if len(rows) != 1 or not str(rows[0].get("path", "")).startswith("smb://"):
                raise AssertionError(f"Lesson 02's stored subtitle across the restart: {rows}")
        now = {x.get("name"): x.get("exports") for x in self.storage_servers()}
        if now != servers:
            raise AssertionError(f"the storage servers changed across the restart: {servers} → {now}")
        return f"same batch {batch}, {len(paths)} paths unchanged, servers {sorted(now)}"

    def assert_plain_home_untouched(self) -> str:
        """A home that is not a NAS (no NAS platform: the image's plain install) keeps no
        record of itself and its paths are never converted: no storage server, home-shares
        says no NAS, and after the home's conversion check (asked now) every path is still
        its local one under the media folder."""
        assert self.api is not None
        s, hs = self.api.get("/api/cluster/storage/home-shares")
        if s != 200 or not isinstance(hs, dict) or hs.get("nas"):
            raise AssertionError(f"home-shares on a plain home: {s} {hs}")
        before = self.media_paths()
        if not before:
            raise AssertionError("run after scan")
        checked = self.canon().get("updated_at")
        self.ask_canon_check()

        def looked() -> dict[str, Any] | None:
            st = self.canon()
            return st if st.get("updated_at") not in (None, checked) else None

        st = wait("the home's conversion check", looked, 10 * 60, every=10)
        if (st.get("last") or {}).get("changes"):
            raise AssertionError(f"a plain home converted paths: {st}")
        servers = self.storage_servers()
        if servers:
            raise AssertionError(f"a plain home made storage server records: {servers}")
        after = self.media_paths()
        moved = [p for p in after.values() if not p.startswith(self.media.rstrip("/") + "/")]
        if after != before or moved:
            raise AssertionError(f"paths changed on a plain home: {moved[:2]}")
        return f"no record, phase {st.get('phase')}, {len(after)} paths local"

    def assert_compose_engine(self) -> str:
        """The compose file was taken unchanged by Docker 24.0.x (`docker compose config`, then
        `up`): what Container Manager runs on DSM 7.2."""
        info = getattr(self, "engine", {})
        if not info:
            raise AssertionError("not a compose cell")
        want = str(self.spec["compose_file"]["engine"])
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

    # -- story 59.08: the ports a household chooses

    @property
    def holder(self) -> str:
        return self.container + "-holder"

    def expect_port_taken(self) -> None:
        """Setup with the port free, then another program takes the port the cell sets
        (`hold_port`) on the host's network, then the server starts: it must stop on it."""
        self.setup()
        port = int(self.spec["hold_port"])
        log(f"another program holds port {port}")
        docker("run", "-d", "--name", self.holder, "--network", "host", "alpine/socat",
               f"TCP-LISTEN:{port},fork,reuseaddr", "SYSTEM:echo busy")
        self.start()

    def container_log(self) -> str:
        p = docker("logs", self.container, check=False)
        return (p.stdout or "") + (p.stderr or "")

    def assert_port_taken_message(self) -> str:
        """The port set in the container is held by another program: it is not moved, the
        server never answers, and the log names the port and the variable in both languages."""
        port = int(self.spec["hold_port"])
        var = next(k for k, v in self.spec.get("env", {}).items() if str(v) == str(port))
        en = f"Port {port} is used by another program on this machine, so Maktaba cannot use it. Set {var} to a free port"
        ar = f"المنفذ {port} يستخدمه برنامج آخر على هذا الجهاز"

        def said() -> bool:
            text = self.container_log()
            return en in text and ar in text and f"اضبط {var}" in text

        wait("the taken-port message in both languages", said, 15 * 60, every=10)
        text = self.container_log()
        for moved in ("port: web_listen", "8780"):
            if moved in text:
                raise AssertionError(f"the port was moved ({moved!r} in the log)")
        return f"port {port}, {var}, English and Arabic"

    @staticmethod
    def answers_on(port: int) -> bool:
        """Whether something accepts a connection on 127.0.0.1:port (the host's network)."""
        import socket

        with socket.socket() as so:
            so.settimeout(3)
            return so.connect_ex(("127.0.0.1", port)) == 0

    def assert_custom_ports(self) -> str:
        """Every port set in the container (story 59.08): the web app loads on its port, the API
        and live TV answer on theirs, Postgres listens on its, nothing on the defaults, and the
        addresses Maktaba hands out name the ports."""
        p = {k: int(v) for k, v in self.spec["ports"].items()}
        s, page = API(f"http://127.0.0.1:{p['web']}").call("GET", "/", raw=True, timeout=15)
        if s != 200 or b"<html" not in (page if isinstance(page, bytes) else str(page).encode()).lower():
            raise AssertionError(f"the web app on {p['web']}: {s}")
        for what, port in (("the API", p["api"]), ("live TV", p["stream"])):
            s, d = API(f"http://127.0.0.1:{port}").get("/healthz", timeout=10)
            if s != 200 or not isinstance(d, dict) or d.get("status") != "ok":
                raise AssertionError(f"{what} on {port}/healthz: {s} {d}")
        if not self.answers_on(p["postgres"]):
            raise AssertionError(f"nothing listens on the Postgres port {p['postgres']}")
        on_default = [d for d in (8088, 8080, 8081, 54329) if self.answers_on(d)]
        if on_default:
            raise AssertionError(f"something listens on the default port(s) {on_default}")
        m = self.machine()
        if not str(m.get("web_url", "")).endswith(f":{p['web']}"):
            raise AssertionError(f"web_url {m.get('web_url')!r} does not name port {p['web']}")
        return f"web {p['web']}, API {p['api']}, live TV {p['stream']}, Postgres {p['postgres']}; defaults free"

    def assert_live_tv_port(self) -> str:
        """Live TV started on the port set in the container: the streaming role runs, the TV
        tuner and live TV (off on a core-bundle home) are turned on as Settings and the Live TV page do,
        and it answers there naming that port in its own address."""
        port = int(self.spec["ports"]["stream"])
        assert self.api is not None
        s, d = self.api.get("/api/system/processes")
        running = {p.get("role") for p in (d or {}).get("processes", []) if p.get("state") == "running"}
        if "streaming" not in running:
            raise AssertionError(f"streaming not running: {running}")
        # live TV, which the cell's core bundle leaves off (the tuner requires it), then the
        # Live TV page's tuner switch: the device row and the livetv.hdhomerun component
        s, d = self.api.call("POST", "/api/components/apply", {"with_conflicts": True, "changes": [
            {"id": "livetv", "want": "enabled"}, {"id": "livetv.hdhomerun", "want": "enabled"}]})
        if s != 200:
            raise AssertionError(f"turning live TV on: {s} {d}")
        s, d = self.api.call("PATCH", "/api/tv/hdhr", {"enabled": True})
        if s != 200:
            raise AssertionError(f"turning the tuner on: {s} {d}")
        tuner = API(f"http://127.0.0.1:{port}")

        seen: list[str] = []

        def discover() -> Any:
            s, d = tuner.get("/discover.json", timeout=10)
            seen[:] = [f"{s} {str(d)[:200]}"]
            return d if s == 200 and isinstance(d, dict) else None

        try:
            d = wait(f"the tuner on {port}", discover, 5 * 60, every=5)
        except AssertionError as e:
            raise AssertionError(f"{e}; its last answer: {seen[0] if seen else 'none'}") from None
        base = str(d.get("BaseURL", ""))
        if not base.endswith(f":{port}"):
            raise AssertionError(f"the tuner's BaseURL {base!r} does not name port {port}")
        return f"tuner on {port}: {base}"

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
        if self.spec.get("avahi"):
            keep("avahi.log", "logs", "-t", self.avahi)
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
        if self.compose is not None and (self.out / "docker-compose.yml").is_file():
            self.compose_cmd("down", "--remove-orphans", check=False)
        for c in (self.container + "-relay", self.container, self.avahi, self.holder):
            docker("rm", "-f", c, check=False)
        for v in ("data", "media", "noaccess", "dbus"):
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
            elif self.spec.get("hold_port"):
                self.expect_port_taken()
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

    def shape_or_error(self) -> list[str]:
        try:
            return self.shape()
        except Exception as e:  # noqa: BLE001 - the report is written whatever went wrong
            return [f"(no shape: {e})"]

    def how(self) -> str:
        """How the cell started its container, for the summary."""
        if self.compose is not None:
            blocks = ", ".join(self.compose.get("blocks", [])) or "none"
            return f"`docker compose up` of the published compose file (blocks on: {blocks}; kept as docker-compose.yml)"
        return f"`docker run {shlex.join(self.shape_or_error())}`"

    def report(self) -> None:
        ok = all(r["ok"] is not False for r in self.results)
        report = {"cell": self.name, "about": self.spec.get("about", ""), "image": self.image, "ok": ok,
                  "shape": self.shape_or_error() if self.compose is None else [], "compose": self.compose,
                  "results": self.results}
        (self.out / "report.json").write_text(json.dumps(report, indent=2, ensure_ascii=False), encoding="utf-8")
        mark = {True: "✅", False: "❌", None: "⏳"}
        lines = [f"### {mark[ok]} `{self.name}`: {self.spec.get('about', '')}", "",
                 self.how(), "", "| Assertion | Result |", "|---|---|"]
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
class DindComposeCell(Cell):
    """A cell started from a NAS's compose file as the NAS starts it (story 59.09): the file
    unchanged, run by the Docker engine the NAS has (`compose_file.engine`, Container Manager's
    24.0.2 on DSM 7.2) in a docker:<engine>-dind container, with the file's own host paths
    made inside it (the data folder, its shares with the fixtures, DSM's folders) and the
    file's image the commit's. The API is reached through a relay in the engine's network
    namespace, so the server sees a local client as on the host network."""

    engine: dict[str, str] = field(default_factory=dict)
    dind_port: int = 0
    api_port: int = 0

    @property
    def file(self) -> Path:
        return HERE.parents[1] / self.spec["compose_file"]["file"]

    @property
    def dind(self) -> str:
        return f"nascell-{self.name}-dind"

    @property
    def container(self) -> str:
        return "maktaba"  # the file's container_name

    def dind_compose(self, *args: str, **kw: Any) -> subprocess.CompletedProcess[str]:
        return docker("compose", "-p", "maktaba", "-f", str(self.file), *args, **kw)

    def shape(self) -> list[str]:
        return ["compose", "-f", self.spec.get("compose_file", {}).get("file", "?"), "up"]

    def prepare(self) -> None:
        engine = str(self.spec["compose_file"]["engine"])
        log(f"Docker {engine} in a dind container")
        self.teardown(quiet=True)
        self.dind_port, self.api_port = free_port(), free_port()
        docker("run", "-d", "--privileged", "--name", self.dind, "-e", "DOCKER_TLS_CERTDIR=",
               "-p", f"127.0.0.1:{self.dind_port}:2375", "-p", f"127.0.0.1:{self.api_port}:28088",
               f"docker:{engine}-dind", outer=True)
        TARGET.update({"DOCKER_HOST": f"tcp://127.0.0.1:{self.dind_port}",
                       "DOCKER_API_VERSION": str(self.spec["compose_file"].get("api", "1.43"))})

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
        cfg = self.dind_compose("config", check=False)
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
        p = self.dind_compose("run", "--rm", "-T", "maktaba", "setup", "--answers", "/dev/stdin", "--lang", "en",
                         *self.bundle(), input=json.dumps(answers), check=False, timeout=45 * 60)
        (self.out / "setup.log").write_text((p.stdout or "") + (p.stderr or ""), encoding="utf-8")
        self.timing("setup", t0)
        if p.returncode != 0:
            raise AssertionError(f"setup exited {p.returncode}: {(p.stdout + p.stderr).strip()[-600:]}")

    def start(self) -> None:
        log("docker compose up -d")
        self.dind_compose("up", "-d", "--pull", "never")
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
    kind = DindComposeCell if spec.get("compose_file") else Cell
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
