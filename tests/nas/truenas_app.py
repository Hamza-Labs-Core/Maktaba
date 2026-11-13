#!/usr/bin/env python3
"""The TrueNAS app under truenas/apps' own tooling (Epic 59 story 59.10, nas.yml's truenas-app).

    truenas_app.py lint   --catalog DIR --out OUT
    truenas_app.py deploy --catalog DIR --image IMAGE --out OUT

DIR is a clone of truenas/apps at the commit tests/nas/truenas/catalog.env pins. The app
(deploy/nas/truenas/ix-dev/community/maktaba) is copied into it as ix-dev/community/maktaba and
committed there, as a pull request to the catalog would add it.

lint (T01): the catalog's own checks, as its app-test-suite runs them:
  - .github/scripts/check_metadata.py: app.yaml and item.yaml are what the catalog's metadata
    generation makes of the app (capabilities, lib_version_hash, the README's warnings: a
    difference is printed, to be copied back into deploy/nas/truenas);
  - .github/scripts/port_validation.py: the defaults' ports clash with no other app's;
  - .github/scripts/ci.py --render-only for each of the app's test values (the render with the
    catalog's library and `docker compose config` of the result), then the shape story 59.10
    asks of each render (568, host network or the three ports, MAKTABA_PLATFORM, the 90 s stop,
    no D-Bus, nothing over /proc, media at its own path, the permissions step on an ixVolume
    only); then the same for tests/nas/truenas/*-values.yaml, which a GitHub runner cannot start
    (an Intel/AMD GPU, an NVIDIA card, host-path data);
  - `docker compose config` of deploy/nas/truenas/custom-app.yaml.

deploy (T06, T01's start): the image of this commit tagged as the app's pinned image, then
  - T06: the ixVolume render (basic-values.yaml) started with docker compose: the catalog's
    permissions step gives the volume to 568; Maktaba's preflight write check passes and it
    waits for setup, healthy, with the TrueNAS words; the setup run as the household runs it
    (the app's Shell: `docker exec ... maktaba-server container setup`), headless here; then
    ready (the healthcheck answering 200), every process as 568, GET /api/server/machine
    reading platform truenas (MAKTABA_PLATFORM) and the app's memory limit;
  - then .github/scripts/ci.py for each test values file, as the catalog's CI deploys it.

Each step's output is kept under OUT (the job's artifact), pass or fail; a failed step fails the
run after the rest ran.
"""

from __future__ import annotations

import argparse
import json
import secrets
import shutil
import subprocess
import sys
import time
import traceback
from pathlib import Path
from typing import Any, Callable

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[1]
sys.path.insert(0, str(HERE))

from cell import API, wait  # noqa: E402

APP_SRC = ROOT / "deploy" / "nas" / "truenas" / "ix-dev" / "community" / "maktaba"
CUSTOM_APP = ROOT / "deploy" / "nas" / "truenas" / "custom-app.yaml"
EXTRA_VALUES = sorted((HERE / "truenas").glob("*-values.yaml"))
APP_REL = Path("ix-dev") / "community" / "maktaba"
RENDERED = Path("templates") / "rendered" / "docker-compose.yaml"
TEST_VALUES = Path("templates") / "test_values"
TESTS_MNT = Path("/opt/tests/mnt")
DATA = "/var/lib/maktaba"


class Run:
    def __init__(self, catalog: Path, out: Path) -> None:
        self.catalog = catalog
        self.out = out
        self.app = catalog / APP_REL
        self.results: list[tuple[str, bool, str]] = []
        out.mkdir(parents=True, exist_ok=True)

    # --- helpers ---------------------------------------------------------------------------

    def sh(self, name: str, cmd: list[str], *, cwd: Path | None = None, input: str | None = None,
           timeout: float = 1800, check: bool = True) -> subprocess.CompletedProcess[str]:
        print(f"$ {' '.join(cmd)}", flush=True)
        p = subprocess.run(cmd, cwd=cwd or self.catalog, input=input, text=True, capture_output=True, timeout=timeout)
        log = self.out / f"{name}.log"
        with log.open("a", encoding="utf-8") as f:
            f.write(f"$ {' '.join(cmd)}\n--- stdout\n{p.stdout}\n--- stderr\n{p.stderr}\n--- exit {p.returncode}\n\n")
        if check and p.returncode != 0:
            raise AssertionError(f"{cmd[0]} {' '.join(cmd[1:3])} exited {p.returncode} (see {log.name}): "
                                 f"{(p.stdout + p.stderr).strip()[-1500:]}")
        return p

    def step(self, name: str, fn: Callable[[], str]) -> None:
        print(f"::group::{name}", flush=True)
        try:
            detail = fn() or "ok"
            self.results.append((name, True, detail))
            print(f"✅ {name}: {detail}", flush=True)
        except Exception as e:  # noqa: BLE001 - every step runs; the verdict is at the end
            self.results.append((name, False, str(e)))
            (self.out / "errors.log").open("a", encoding="utf-8").write(f"{name}\n{traceback.format_exc()}\n")
            print(f"❌ {name}: {e}", flush=True)
        finally:
            print("::endgroup::", flush=True)

    def ci_py(self, values: str, *extra: str, name: str = "") -> subprocess.CompletedProcess[str]:
        return self.sh(name or f"ci-{values}", ["python3", ".github/scripts/ci.py", "--train", "community",
                                                "--app", "maktaba", "--test-file", values, *extra])

    def rendered(self) -> dict[str, Any]:
        p = self.sh("compose-config", ["docker", "compose", "-p", "maktaba-render", "-f", str(self.app / RENDERED),
                                       "config", "--format", "json"])
        return json.loads(p.stdout)

    def summary(self) -> int:
        lines = ["### TrueNAS app (truenas/apps' tooling)", "", "| Step | Result |", "|---|---|"]
        for name, ok, detail in self.results:
            lines.append(f"| {name} | {'✅' if ok else '❌'} {detail.replace('|', '/')[:400]} |")
        text = "\n".join(lines) + "\n"
        (self.out / "summary.md").write_text(text, encoding="utf-8")
        print(text)
        return 0 if self.results and all(ok for _, ok, _ in self.results) else 1

    # --- the app in the catalog --------------------------------------------------------------

    def stage(self) -> None:
        """The app copied in and committed, as a pull request to the catalog adds it."""
        if self.app.exists():
            shutil.rmtree(self.app)
        shutil.copytree(APP_SRC, self.app)
        self.sh("stage", ["git", "add", str(APP_REL)])
        self.sh("stage", ["git", "-c", "user.name=maktaba-ci", "-c", "user.email=ci@maktaba.invalid",
                          "commit", "-q", "--allow-empty", "-m", "maktaba: the app from Hamza-Labs-Core/Maktaba"])

    # --- lint --------------------------------------------------------------------------------

    def lint(self) -> None:
        self.stage()
        self.step("check_metadata.py (app.yaml and item.yaml as the catalog generates them)", self.metadata)
        self.step("port_validation.py", lambda: self.sh("ports", ["python3", ".github/scripts/port_validation.py"]).stdout.strip()[-200:])
        for f in sorted((self.app / TEST_VALUES).glob("*.yaml")):
            self.step(f"render {f.name}", lambda f=f: self.render_and_check(f.name, f, skip_metadata=False))
        for f in EXTRA_VALUES:
            def extra(f: Path = f) -> str:
                dst = self.app / TEST_VALUES / f.name
                shutil.copy(f, dst)
                try:
                    return self.render_and_check(f.name, f, skip_metadata=True)
                finally:
                    dst.unlink()
            self.step(f"render {f.name} (ours: render only)", extra)
        self.step("custom-app.yaml: docker compose config", self.custom_app)

    def metadata(self) -> str:
        p = self.sh("metadata", ["python3", ".github/scripts/check_metadata.py", "--train", "community", "--app", "maktaba"],
                    check=False)
        if p.returncode != 0:
            diff = self.sh("metadata", ["git", "diff", "--", str(APP_REL)], check=False).stdout
            (self.out / "metadata.diff").write_text(diff, encoding="utf-8")
            raise AssertionError("the catalog generates other metadata: copy metadata.diff's changes into "
                                 "deploy/nas/truenas/ix-dev/community/maktaba\n" + diff[-3000:])
        return p.stdout.strip().splitlines()[-1] if p.stdout.strip() else "up to date"

    def render_and_check(self, name: str, values_path: Path, skip_metadata: bool) -> str:
        args = ["--render-only=true"] + (["--skip-metadata"] if skip_metadata else [])
        self.ci_py(name, *args)
        compose = self.rendered()
        (self.out / f"rendered-{name}.json").write_text(json.dumps(compose, indent=1), encoding="utf-8")
        values = _yaml(values_path.read_text(encoding="utf-8"))
        return check_shape(compose, values)

    def custom_app(self) -> str:
        self.sh("custom-app", ["docker", "compose", "-p", "maktaba-custom", "-f", str(CUSTOM_APP), "config", "-q"], cwd=ROOT)
        return "valid"

    # --- deploy ------------------------------------------------------------------------------

    def deploy(self, image: str) -> None:
        self.stage()
        pinned = pinned_image(self.app)
        self.sh("image", ["docker", "tag", image, pinned])
        self.step(f"T06: ixVolume, the permissions step, setup from the Shell, ready as 568 ({pinned})",
                  lambda: self.t06(image))
        for f in sorted((self.app / TEST_VALUES).glob("*.yaml")):
            self.clean(image)
            self.step(f"ci.py {f.name} (the catalog's deploy: up --wait, healthy)",
                      lambda f=f: (self.ci_py(f.name, "--skip-metadata"), "healthy")[1])
        self.clean(image)

    def clean(self, image: str) -> None:
        """The test mounts the renders made under /opt/tests/mnt, removed as root (the image
        itself as uid 0, its entrypoint bypassed: a runner's docker needs no sudo)."""
        if TESTS_MNT.exists():
            self.sh("clean", ["docker", "run", "--rm", "--user", "0:0", "--entrypoint", "sh", "-v",
                              f"{TESTS_MNT}:/m", image, "-c", "rm -rf /m/maktaba-*"], check=False)

    def t06(self, image: str) -> str:
        self.clean(image)
        self.ci_py("basic-values.yaml", "--render-only=true", "--skip-metadata", name="t06-render")
        rendered = self.app / RENDERED
        shutil.copy(rendered, self.out / "t06-rendered.yaml")
        values = _yaml((self.app / TEST_VALUES / "basic-values.yaml").read_text(encoding="utf-8"))
        data_host = Path(values["ix_volumes"][values["storage"]["data"]["ix_volume_config"]["dataset_name"]])
        compose = ["docker", "compose", "-p", "maktaba-t06", "-f", str(rendered)]
        password = "Nas-" + secrets.token_urlsafe(18)
        try:
            self.sh("t06", [*compose, "up", "-d", "--wait", "--wait-timeout", "900"], timeout=1200)
            perms = self.sh("t06", [*compose, "ps", "-a", "-q", "permissions"]).stdout.strip()
            code = self.sh("t06", ["docker", "inspect", "-f", "{{.State.ExitCode}}", perms]).stdout.strip()
            if code != "0":
                raise AssertionError(f"the permissions step exited {code}")
            st = data_host.stat()
            if (st.st_uid, st.st_gid) != (568, 568):
                raise AssertionError(f"{data_host} is {st.st_uid}:{st.st_gid} after the permissions step, not 568:568")
            ctr = self.sh("t06", [*compose, "ps", "-q", "maktaba"]).stdout.strip()

            def waiting() -> str:
                logs = self.sh("t06-logs", ["docker", "logs", ctr], check=False)
                text = logs.stdout + logs.stderr
                if "cannot write" in text or "لا يستطيع Maktaba الكتابة" in text:
                    raise AssertionError("the preflight's write check failed:\n" + text[-1500:])
                return "app's Shell" in text and "not set up yet" in text and "waiting"
            wait("Maktaba waiting for setup, in TrueNAS's words", waiting, 300, every=3)
            health = self.sh("t06", ["docker", "exec", ctr, "/opt/maktaba/server/bin/maktaba-server", "healthcheck"]).stdout
            if "waiting for setup" not in health:
                raise AssertionError(f"the healthcheck before setup: {health!r}")

            answers = {
                "admin": {"username": "nas-admin", "password": password},
                "libraries": {"roots": [m["path"] for m in values["storage"]["media"]], "path_map": []},
                "models": {"source": "huggingface", "hf_token": ""},
                "plex": {"action": "skip"},
            }
            self.sh("t06-setup", ["docker", "exec", "-i", ctr, "/opt/maktaba/server/bin/maktaba-server", "container",
                                  "setup", "--answers", "/dev/stdin", "--lang", "en", "--bundle", "core"],
                    input=json.dumps(answers), timeout=45 * 60)

            def ready() -> bool:
                p = self.sh("t06-health", ["docker", "exec", ctr, "/opt/maktaba/server/bin/maktaba-server", "healthcheck"],
                            check=False)
                return p.returncode == 0 and "answered 200" in p.stdout
            wait("the web app answering (the healthcheck)", ready, 20 * 60, every=10)

            # docker top needs the pid column
            rows = self.sh("t06", ["docker", "top", ctr, "-eo", "pid,uid,comm"]).stdout.split("\n")[1:]
            others = [r for r in rows if r.strip() and r.split()[1] != "568"]
            if others:
                raise AssertionError(f"processes not as 568: {others}")

            api = API("http://127.0.0.1:8088")
            api.credentials = {"username": "nas-admin", "password": password}
            wait("the admin's sign-in", lambda: api.login() and api.token, 10 * 60, every=10)
            s, d = api.get("/api/server/machine")
            (self.out / "t06-machine.json").write_text(json.dumps(d, indent=1, ensure_ascii=False), encoding="utf-8")
            if s != 200 or not isinstance(d, dict):
                raise AssertionError(f"GET /api/server/machine: {s} {d}")
            m = d.get("machine") or {}
            if m.get("platform") != "truenas":
                raise AssertionError(f"platform {m.get('platform')!r} ({m.get('platform_from')}), not truenas")
            limit = (m.get("memory") or {}).get("limit_bytes")
            want = int(values["resources"]["limits"]["memory"]) << 20
            if limit != want:
                raise AssertionError(f"the memory limit read is {limit}, not the app's {want}")
            return f"568:568 volume, setup from the Shell, ready; platform truenas, tier {d.get('tier')}, limit {limit >> 20} MiB"
        finally:
            self.sh("t06-logs", [*compose, "logs", "--no-color", "-t"], check=False)
            self.sh("t06-logs", [*compose, "ps", "-a"], check=False)
            self.sh("t06-down", [*compose, "down", "-v", "--remove-orphans"], check=False, timeout=300)


# --- the shape of a render --------------------------------------------------------------------


def _yaml(text: str) -> Any:
    import yaml  # the catalog's scripts need PyYAML too: nas.yml installs it

    return yaml.safe_load(text)


def pinned_image(app: Path) -> str:
    ix = _yaml((app / "ix_values.yaml").read_text(encoding="utf-8"))["images"]["image"]
    return f"{ix['repository']}:{ix['tag']}"


def check_shape(compose: dict[str, Any], values: dict[str, Any]) -> str:
    """What story 59.10 asks of a render of the app with these values (a short detail, or
    AssertionError)."""
    svcs = compose.get("services") or {}
    if "maktaba" not in svcs:
        raise AssertionError(f"no maktaba service: {sorted(svcs)}")
    s = svcs["maktaba"]
    errs: list[str] = []
    run_as = values.get("run_as", {})
    want_user = f"{run_as.get('user', 568)}:{run_as.get('group', 568)}"
    if s.get("user") != want_user:
        errs.append(f"user {s.get('user')!r}, not {want_user}")
    if s.get("privileged"):
        errs.append("privileged")
    env = s.get("environment") or {}
    if env.get("MAKTABA_PLATFORM") != "truenas":
        errs.append(f"MAKTABA_PLATFORM {env.get('MAKTABA_PLATFORM')!r}")
    if env.get("TZ") != values.get("TZ", "Etc/UTC"):
        errs.append(f"TZ {env.get('TZ')!r}")
    if "MAKTABA_UPDATE_DISABLE" in env:
        errs.append("MAKTABA_UPDATE_DISABLE is set: the self-update stays on")
    if s.get("stop_grace_period") not in ("1m30s", "90s"):
        errs.append(f"stop_grace_period {s.get('stop_grace_period')!r}")
    if "healthcheck" not in " ".join(((s.get("healthcheck") or {}).get("test")) or []):
        errs.append(f"healthcheck {s.get('healthcheck')}")

    net = values.get("network", {})
    if net.get("host_network", True):
        if s.get("network_mode") != "host":
            errs.append(f"network_mode {s.get('network_mode')!r}, not host")
        if s.get("ports"):
            errs.append(f"ports published on the host network: {s.get('ports')}")
    else:
        targets = sorted(int(p["target"]) for p in s.get("ports") or [])
        want = [8080, 8081, 8088]
        if not set(want) <= set(targets):
            errs.append(f"bridge ports {targets}, not {want}")
        if 54329 in targets:
            errs.append("the bundled Postgres's port published on a bridge")
        if net.get("host_ip") and env.get("MAKTABA_HOST_IP") != net["host_ip"]:
            errs.append(f"MAKTABA_HOST_IP {env.get('MAKTABA_HOST_IP')!r}, not {net['host_ip']}")
        # story 59.08: the published ports are the ones Maktaba hands out
        for key, var in (("web_port", "MAKTABA_PUBLIC_WEB_PORT"), ("api_port", "MAKTABA_PUBLIC_API_PORT"),
                         ("live_port", "MAKTABA_PUBLIC_STREAM_PORT")):
            p = net.get(key) or {}
            if p.get("bind_mode", "published") == "published" and p.get("port_number") and \
                    str(env.get(var)) != str(p["port_number"]):
                errs.append(f"{var} {env.get(var)!r}, not the published {p['port_number']}")

    vols = s.get("volumes") or []
    by_target = {v.get("target"): v for v in vols}
    for v in vols:
        t = str(v.get("target", ""))
        if "dbus" in t or "dbus" in str(v.get("source", "")):
            errs.append(f"a D-Bus mount: {v}")
        if t.startswith("/proc"):
            errs.append(f"a mount over /proc: {v}")
    for t in s.get("tmpfs") or []:
        if str(t).startswith("/proc"):
            errs.append(f"a tmpfs over /proc: {t}")
    if any("systempaths" in str(o) for o in s.get("security_opt") or []):
        errs.append("security_opt changes the masked paths")
    if DATA not in by_target:
        errs.append(f"no data mount at {DATA}")
    for m in values.get("storage", {}).get("media") or []:
        v = by_target.get(m["path"])
        if not v:
            errs.append(f"media {m['path']} not mounted at its own path")
            continue
        if v.get("source") != m["path"]:
            errs.append(f"media {m['path']} from {v.get('source')}")
        if bool(v.get("read_only")) != bool(m.get("read_only")):
            errs.append(f"media {m['path']} read_only {v.get('read_only')}, asked {m.get('read_only')}")

    data_type = values.get("storage", {}).get("data", {}).get("type")
    perms = svcs.get("permissions")
    if data_type == "ix_volume":
        if not perms:
            errs.append("an ixVolume without the catalog's permissions step")
        elif (s.get("depends_on") or {}).get("permissions", {}).get("condition") != "service_completed_successfully":
            errs.append(f"maktaba does not wait for the permissions step: {s.get('depends_on')}")
    elif perms:
        errs.append("a permissions step for host-path data: Maktaba's folders are never chowned")

    gpus = values.get("resources", {}).get("gpus", {})
    devices = [str(d.get("source", d)) if isinstance(d, dict) else str(d) for d in s.get("devices") or []]
    groups = {str(g) for g in s.get("group_add") or []}
    if gpus.get("use_all_gpus"):
        if not any("/dev/dri" in d for d in devices):
            errs.append(f"GPU allocated, no /dev/dri: {devices}")
        if not {"44", "107"} <= groups:
            errs.append(f"GPU allocated, groups {sorted(groups)} lack video (44) and render (107)")
    elif any("/dev/dri" in d for d in devices):
        errs.append("/dev/dri passed with no GPU allocated")
    nvidia = [g for g in (gpus.get("nvidia_gpu_selection") or {}).values() if g.get("use_gpu")]
    res = ((s.get("deploy") or {}).get("resources") or {})
    reserved = (res.get("reservations") or {}).get("devices") or []
    if nvidia:
        if not any(d.get("driver") == "nvidia" for d in reserved):
            errs.append(f"an NVIDIA card selected, no nvidia reservation: {reserved}")
        if env.get("NVIDIA_VISIBLE_DEVICES") in (None, "void"):
            errs.append(f"NVIDIA_VISIBLE_DEVICES {env.get('NVIDIA_VISIBLE_DEVICES')!r}")
    elif reserved:
        errs.append(f"a GPU reservation with no NVIDIA card selected: {reserved}")
    want_mem = int(values.get("resources", {}).get("limits", {}).get("memory", 0)) << 20
    got_mem = int((res.get("limits") or {}).get("memory") or 0)
    if want_mem and got_mem != want_mem:
        errs.append(f"memory limit {got_mem}, not {want_mem}")

    if errs:
        raise AssertionError("; ".join(errs))
    shape = "host network" if net.get("host_network", True) else "bridge"
    gpu = "Intel/AMD GPU" if gpus.get("use_all_gpus") else ("NVIDIA" if nvidia else "no GPU")
    return f"{want_user}, {shape}, {data_type} data, {len(values.get('storage', {}).get('media') or [])} media, {gpu}"


def main(argv: list[str]) -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("what", choices=["lint", "deploy"])
    ap.add_argument("--catalog", required=True, type=Path)
    ap.add_argument("--out", required=True, type=Path)
    ap.add_argument("--image", default="")
    a = ap.parse_args(argv)
    run = Run(a.catalog.resolve(), a.out.resolve())
    if a.what == "lint":
        run.lint()
    else:
        if not a.image:
            ap.error("deploy needs --image")
        run.deploy(a.image)
    return run.summary()


if __name__ == "__main__":
    t0 = time.monotonic()
    code = main(sys.argv[1:])
    print(f"({int(time.monotonic() - t0)} s)")
    sys.exit(code)
