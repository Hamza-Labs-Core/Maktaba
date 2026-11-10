#!/usr/bin/env python3
"""The NAS install files, checked (Epic 59 story 59.11).

    install_files.py            # the static checks (no Docker)
    install_files.py --docker   # and `docker compose config` of every variant, and each
                                # development image resolved in the registry

The published Docker compose (cloud/installers/docker/docker-compose.yml) is the NAS base:
its optional settings are commented blocks, each headed `# [name] ...` (memory, bridge, dbus,
dri, nvidia). `enable()` turns blocks on as a household (or maktaba.tv's install page,
web/packages/install) does: it removes the "# " in front of each block's settings, and the
bridge block also drops `network_mode: host`. T01: the file with no block, each block alone
and every block together is valid (`docker compose config`).

The Unraid template (deploy/nas/unraid/maktaba.xml) is read as Community Applications reads
it and rendered to the `docker run` arguments Unraid's dockerMan makes (T03), and checked
against the compose file so the two do not drift: the same image, the host network, the stop
timeout, the data folder, the render device, the environment.

The development compose (deploy/compose/docker-compose.yml) names only images release
publishes (release.yml's build-images matrix), and each resolves in the registry (T04).

Standard library only: cell.py imports it, and the plan job runs it before any install.
"""

from __future__ import annotations

import argparse
import re
import subprocess
import sys
import tempfile
import xml.etree.ElementTree as ET
from urllib.parse import urlparse
from dataclasses import dataclass, field
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
COMPOSE = ROOT / "cloud/installers/docker/docker-compose.yml"
UNRAID = ROOT / "deploy/nas/unraid/maktaba.xml"
DEV_COMPOSE = ROOT / "deploy/compose/docker-compose.yml"
RELEASE = ROOT / ".github/workflows/release.yml"

IMAGE = "ghcr.io/hamza-labs-core/maktaba-server"
DATA = "/var/lib/maktaba"
BLOCKS = ("memory", "bridge", "dbus", "dri", "nvidia")

HEADER = re.compile(r"^(\s*)# \[([a-z0-9-]+)\]")
# a commented setting: "# key:", "# key: value" or "#   - item", at any indent
SETTING = re.compile(r"^(\s*)# (\s*[A-Za-z_][A-Za-z0-9_.-]*:(?:\s|$)|\s*- )")


# --- the compose file's blocks ---------------------------------------------------------------


def blocks(text: str) -> dict[str, list[int]]:
    """Each block's commented setting lines (indexes into text.splitlines()). A block runs from
    its header through the comment lines after it, to the first line that is not a comment
    or the next header; a name may head more than one place (bridge: its ports and its
    environment line)."""
    out: dict[str, list[int]] = {}
    current = ""
    for i, line in enumerate(text.splitlines()):
        h = HEADER.match(line)
        if h:
            current = h.group(2)
            out.setdefault(current, [])
            continue
        if not line.lstrip().startswith("#"):
            current = ""
            continue
        if current and SETTING.match(line):
            out[current].append(i)
    return out


def enable(text: str, names: list[str] | tuple[str, ...]) -> str:
    """The compose text with these blocks turned on."""
    found = blocks(text)
    lines = text.splitlines()
    for n in names:
        if n not in found or not found[n]:
            raise ValueError(f"no block [{n}] with settings in the compose file")
        for i in found[n]:
            lines[i] = re.sub(r"^(\s*)# ", r"\1", lines[i], count=1)
    if "bridge" in names:
        lines = [ln for ln in lines if ln.strip() != "network_mode: host"]
    return "\n".join(lines) + "\n"


def setting(text: str, key: str) -> str:
    """The first active `key: value` of the file (quotes stripped)."""
    m = re.search(rf"^\s*{re.escape(key)}:\s*(.+?)\s*$", text, re.M)
    return m.group(1).strip("'\"") if m else ""


def check_compose(text: str) -> list[str]:
    """The file's shape: what every NAS file derived from it relies on."""
    errs = []
    found = blocks(text)
    for n in BLOCKS:
        if not found.get(n):
            errs.append(f"the compose file has no [{n}] block with settings")
    image = setting(text, "image")
    if image.rsplit(":", 1)[0] != IMAGE:
        errs.append(f"image {image!r} is not {IMAGE}")
    if setting(text, "network_mode") != "host":
        errs.append("network_mode is not host")
    if not re.fullmatch(r"\d+:\d+", setting(text, "user")):
        errs.append("user: is not a uid:gid")
    if setting(text, "stop_grace_period") != "90s":
        errs.append("stop_grace_period is not 90s")
    if setting(text, "MAKTABA_PLATFORM") != "docker":
        errs.append("MAKTABA_PLATFORM is not docker")
    if not re.search(rf"^\s*- \S+:{re.escape(DATA)}\s*$", text, re.M):
        errs.append(f"no volume at {DATA}")
    media = re.search(r"^\s*- (/[^:\s]+):(/[^:\s]+)(:ro)?\s*$", text, re.M)
    if not media or media.group(1) != media.group(2):
        errs.append("the media folder is not mounted at its own path")
    on = enable(text, BLOCKS)
    for want in ("mem_limit:", "/dev/dri:/dev/dri", "group_add:", "driver: nvidia",
                 "/var/run/dbus/system_bus_socket", "ports:", "MAKTABA_HOST_IP:"):
        if want not in on:
            errs.append(f"turning every block on gives no {want!r}")
    if "54329" in "\n".join(on.splitlines()[i] for i in found.get("bridge", [])):
        errs.append("the bridge block publishes Postgres's port (a bridge home has no nodes)")
    return errs


def variants() -> list[tuple[str, tuple[str, ...]]]:
    """T01: none, each block alone, every block."""
    return [("plain", ())] + [(n, (n,)) for n in BLOCKS] + [("all", BLOCKS)]


def compose_config(text: str, name: str) -> str:
    """`docker compose config -q` of this text: "" when valid, else the error."""
    with tempfile.TemporaryDirectory() as d:
        f = Path(d) / "docker-compose.yml"
        f.write_text(text, encoding="utf-8")
        p = subprocess.run(["docker", "compose", "-p", "nas-check", "-f", str(f), "config", "-q"],
                           capture_output=True, text=True)
        return "" if p.returncode == 0 else f"{name}: {(p.stderr or p.stdout).strip()}"


# --- the Unraid template -------------------------------------------------------------------


@dataclass
class Config:
    name: str
    target: str
    default: str
    value: str
    type: str
    mode: str = ""
    required: bool = False
    display: str = "always"
    description: str = ""


@dataclass
class Template:
    fields: dict[str, str]
    configs: list[Config] = field(default_factory=list)

    def by_type(self, t: str) -> list[Config]:
        return [c for c in self.configs if c.type == t]

    def env(self) -> dict[str, str]:
        return {c.target: c.value or c.default for c in self.by_type("Variable")}

    def user(self) -> str:
        m = re.search(r"--user[= ](\S+)", self.fields.get("ExtraParams", ""))
        return m.group(1) if m else ""


REQUIRED = ("Name", "Repository", "Registry", "Network", "Support", "Project", "Overview",
            "Category", "WebUI", "TemplateURL", "Icon", "ExtraParams")
TYPES = {"Path", "Port", "Variable", "Device", "Label"}


def read_template(path: Path = UNRAID) -> Template:
    root = ET.parse(path).getroot()
    if root.tag != "Container" or root.get("version") != "2":
        raise ValueError("the template's root is not <Container version=\"2\">")
    fields = {c.tag: (c.text or "").strip() for c in root if c.tag != "Config"}
    t = Template(fields=fields)
    for c in root.iter("Config"):
        t.configs.append(Config(name=c.get("Name", ""), target=c.get("Target", ""), default=c.get("Default", ""),
                                value=(c.text or "").strip(), type=c.get("Type", ""), mode=c.get("Mode", ""),
                                required=c.get("Required") == "true", display=c.get("Display", "always"),
                                description=c.get("Description", "")))
    return t


def check_template(t: Template) -> list[str]:
    errs = [f"no <{k}>" for k in REQUIRED if not t.fields.get(k)]
    for c in t.configs:
        if c.type not in TYPES:
            errs.append(f"Config {c.name!r}: Type {c.type!r}")
        if not c.name or not c.target:
            errs.append(f"a Config without Name or Target: {c}")
    if t.fields.get("Network") != "host":
        errs.append("Network is not host")
    if t.user() != "99:100":
        errs.append(f"ExtraParams' user is {t.user()!r}, not 99:100")
    if "--stop-timeout 90" not in t.fields.get("ExtraParams", ""):
        errs.append("ExtraParams has no --stop-timeout 90")
    if t.fields.get("Privileged", "false") != "false":
        errs.append("Privileged is not false")
    if t.fields.get("Repository") != f"{IMAGE}:latest":
        errs.append(f"Repository {t.fields.get('Repository')!r} is not {IMAGE}:latest")
    data = [c for c in t.by_type("Path") if c.target == DATA]
    if len(data) != 1:
        errs.append(f"no single Path at {DATA}")
    else:
        d = data[0]
        if not d.default.startswith("/mnt/cache/") or d.value != d.default:
            errs.append(f"the data folder's default {d.default!r} is not on the cache pool")
        if "cache pool" not in d.description:
            errs.append("the data folder's description does not say to keep it on a cache pool")
    media = [c for c in t.by_type("Path") if c.target != DATA]
    if not media:
        errs.append("no media Path")
    for m in media:
        if not m.target.startswith("/mnt/user/") or m.default != m.target:
            errs.append(f"media Path {m.name!r} is not mounted at its own /mnt/user path")
    if [c.target for c in t.by_type("Device")] != ["/dev/dri"] or t.by_type("Device")[0].required:
        errs.append("the render device /dev/dri is not the one optional Device")
    env = t.env()
    if env.get("MAKTABA_PLATFORM") != "unraid":
        errs.append("MAKTABA_PLATFORM is not unraid")
    if "TZ" not in env:
        errs.append("no TZ")
    support = urlparse(t.fields.get("Support", ""))
    if support.scheme != "https" or support.hostname != "maktaba.tv":
        errs.append("the support link is not maktaba.tv")
    return errs


def render(t: Template, *, paths: dict[str, tuple[str, str]] | None = None, devices: bool = True,
           image: str = "", name: str = "") -> list[str]:
    """The `docker run` arguments Unraid's dockerMan makes from the template: --net, each
    Variable as -e, each Path as -v host:target[:mode], each Device as --device, then the
    Extra Parameters, then the image. `paths` replaces a Path's host side and target (by the
    Config's Name), as a household edits them; `devices=False` leaves the devices out (a
    runner without /dev/dri)."""
    args = []
    if name:
        args += ["--name", name]
    args += ["--net", t.fields["Network"]]
    for k, v in t.env().items():
        args += ["-e", f"{k}={v}"]
    for c in t.by_type("Path"):
        host, target = (paths or {}).get(c.name, (c.value or c.default, c.target))
        mode = c.mode if c.mode in ("ro", "rw") else "rw"
        args += ["-v", f"{host}:{target}:{mode}"]
    if devices:
        for c in t.by_type("Device"):
            args += ["--device", c.value or c.default]
    args += t.fields.get("ExtraParams", "").split()
    return args + [image or t.fields["Repository"]]


def check_drift(compose: str, t: Template) -> list[str]:
    """The template says what the compose file says, where both say it."""
    errs = []
    if setting(compose, "image").rsplit(":", 1)[0] != t.fields.get("Repository", "").rsplit(":", 1)[0]:
        errs.append("the template's image is not the compose file's")
    if setting(compose, "network_mode") != t.fields.get("Network"):
        errs.append("the template's network is not the compose file's")
    grace = setting(compose, "stop_grace_period").rstrip("s")
    if f"--stop-timeout {grace}" not in t.fields.get("ExtraParams", ""):
        errs.append(f"the template's stop timeout is not the compose file's {grace}s")
    if not any(c.target == DATA for c in t.by_type("Path")):
        errs.append(f"the template has no {DATA}")
    on = enable(compose, ("dri",))
    if "/dev/dri:/dev/dri" not in on or "/dev/dri" not in [c.target for c in t.by_type("Device")]:
        errs.append("the render device differs")
    for key in ("MAKTABA_PLATFORM", "TZ"):
        if not setting(compose, key) or key not in t.env():
            errs.append(f"{key} is not in both")
    return errs


# --- the development compose ---------------------------------------------------------------


def published_services(release: Path = RELEASE) -> list[str]:
    """release.yml's build-images matrix: the images release pushes as maktaba-<svc>."""
    text = release.read_text(encoding="utf-8")
    job = text.split("\n  build-images:", 1)[1]
    m = re.search(r"^\s+svc: \[([^\]]+)\]", job, re.M)
    if not m:
        raise ValueError("release.yml's build-images has no svc matrix")
    return [s.strip() for s in m.group(1).split(",")]


def dev_images(path: Path = DEV_COMPOSE) -> dict[str, str]:
    """Each built service's image name (the tag left out)."""
    out, svc = {}, ""
    for line in path.read_text(encoding="utf-8").splitlines():
        m = re.match(r"^  ([a-z0-9_-]+):\s*$", line)
        if m:
            svc = m.group(1)
            continue
        m = re.match(r"^    image:\s*(\S+)", line)
        if m and svc:
            out[svc] = m.group(1)
    built = set()
    svc = ""
    for line in path.read_text(encoding="utf-8").splitlines():
        m = re.match(r"^  ([a-z0-9_-]+):\s*$", line)
        if m:
            svc = m.group(1)
        elif svc and re.match(r"^    build:", line):
            built.add(svc)
    return {s: re.sub(r"^([^:]*/[^:/]+):.*$", r"\1", out[s]) for s in sorted(built) if s in out}


def check_dev(path: Path = DEV_COMPOSE) -> list[str]:
    errs = []
    head = "\n".join(path.read_text(encoding="utf-8").splitlines()[:15])
    if "not for a NAS" not in head or "cloud/installers/docker/docker-compose.yml" not in head:
        errs.append("the development compose's header does not say it is not for a NAS, pointing to the Docker install")
    published = published_services()
    images = dev_images(path)
    if not images:
        errs.append("the development compose builds no image")
    for svc, image in images.items():
        want = f"ghcr.io/hamza-labs-core/maktaba-{svc}"
        if image != want or svc not in published:
            errs.append(f"{svc}: {image} is not a published image ({want}, release publishes {published})")
    return errs


def resolve(image: str) -> str:
    """`docker manifest inspect IMAGE`: "" when it resolves, else the error."""
    p = subprocess.run(["docker", "manifest", "inspect", image], capture_output=True, text=True)
    return "" if p.returncode == 0 else f"{image}: {(p.stderr or p.stdout).strip()[:300]}"


# --- main ------------------------------------------------------------------------------------


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--docker", action="store_true", help="also run docker compose config and resolve the images")
    args = ap.parse_args(argv)
    compose = COMPOSE.read_text(encoding="utf-8")
    t = read_template()
    errs = check_compose(compose) + check_template(t) + check_drift(compose, t) + check_dev()
    print("unraid: docker run " + " ".join(render(t)))
    if args.docker:
        for name, on in variants():
            e = compose_config(enable(compose, on), name)
            print(f"compose config [{name}]: {'ok' if not e else 'FAILED'}")
            if e:
                errs.append(e)
        for svc, image in dev_images().items():
            e = resolve(f"{image}:latest")
            print(f"{image}:latest: {'resolves' if not e else 'FAILED'}")
            if e:
                errs.append(e)
    for e in errs:
        print(f"::error title=nas-install-files::{e}")
    print("ok" if not errs else f"{len(errs)} problem(s)")
    return 0 if not errs else 1


if __name__ == "__main__":
    sys.exit(main())
