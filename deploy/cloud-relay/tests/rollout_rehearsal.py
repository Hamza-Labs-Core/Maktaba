#!/usr/bin/env python3
"""The single node's rollout, rehearsed for real (cloud-rehearsal.yml, the single-node job).

On a throwaway machine (the CI runner), as root: setup.sh, ops/rollout.sh, ops/backup.sh and the
compose stack exactly as a node runs them, with this commit's cloud images in a registry on the
machine. Each way a deploy can fail is made to happen, and the node must be what it was before: the
images its roles run, .env, the compose file, the Caddyfile, the web app, the migrations, healthy
and ready. A reboot is the node's own: docker and the unit started again with another boot id.

    sudo REPO=$PWD python3 deploy/cloud-relay/tests/rollout_rehearsal.py

    PART=1|2            half of the cases (the CI's two jobs)
    MUTATE=<name>       the rehearsal rehearsed: the node's scripts with one guarantee taken out
                        (MUTATIONS below), and the cases that must notice. Passes when one fails.

Never on a machine anyone uses: it installs Docker's settings, a firewall and a systemd unit.
"""
from __future__ import annotations

import hashlib
import io
import json
import os
import re
import signal
import subprocess
import sys
import tarfile
import tempfile
import threading
import time
from contextlib import contextmanager
from pathlib import Path
from typing import Any, Callable, Iterator

REPO = Path(os.environ["REPO"])
OUT = Path(os.environ.get("OUT", "/tmp/rollout-rehearsal"))
DEPLOY = Path("/opt/maktaba-relay")
SRC = Path("/opt/maktaba-relay-src")
STATE = Path("/var/lib/maktaba-cloud-deploy")
ROLLOUT = "/usr/local/sbin/maktaba-rollout"
REGISTRY = "localhost:5000/maktaba-cloud"
APP = "app.rehearsal.test"
NAMES = {"RELAY_DOMAIN": "relay.rehearsal.test", "API_DOMAIN": "api.rehearsal.test", "APP_DOMAIN": APP,
         "ACME_EMAIL": "rehearsal@rehearsal.test", "RELAY_TLS": "internal", "RELAY_PLANS_ENFORCE": "false"}
WATCH = "30"
MUTATE = os.environ.get("MUTATE", "")

IMAGES: dict[str, str] = {}
CADDY = ""
results: list[dict[str, Any]] = []


def sh(cmd: str, check: bool = True, stdin: bytes | None = None, env: dict[str, str] | None = None, timeout: float = 900) -> str:
    p = subprocess.run(["bash", "-c", cmd], input=stdin if stdin is not None else b"", capture_output=True,
                       timeout=timeout, env={**os.environ, **(env or {})})
    out = (p.stdout + p.stderr).decode("utf-8", "replace")
    if check and p.returncode != 0:
        raise AssertionError(f"`{cmd[:100]}` exited {p.returncode}:\n{out[-3000:]}")
    return out


def rc(cmd: str, stdin: bytes | None = None, env: dict[str, str] | None = None) -> tuple[int, str]:
    p = subprocess.run(["bash", "-c", cmd], input=stdin if stdin is not None else b"", capture_output=True,
                       timeout=900, env={**os.environ, **(env or {})})
    return p.returncode, (p.stdout + p.stderr).decode("utf-8", "replace")


def dc(args: str) -> str:
    return sh(f"cd {DEPLOY} && docker compose --env-file .env {args}")


def psql(sql: str) -> str:
    return sh(f"cd {DEPLOY} && docker compose --env-file .env exec -T postgres psql -X -q -A -t -v ON_ERROR_STOP=1 -U maktaba -d maktaba",
              stdin=sql.encode()).strip()


# --- one guarantee at a time, taken out (MUTATE) ----------------------------------------------

# name: (the file, what it says, what it says instead, the cases that must notice)
MUTATIONS: dict[str, tuple[str, str, str, list[str]]] = {
    "no-backup": ("ops/rollout.sh", r"DEPLOY_DIR=\"\$DEPLOY_DIR\" ROLLOUT_LOCKED=1 bash \"\$src/ops/backup\.sh\"", "true",
                  ["its tests fail after it went live"]),
    "no-lock": ("ops/rollout.sh", r"flock (-w [^\n]*?|-n) 9", "true", ["one command at a time"]),
    "drop-in-place": ("ops/rollout.sh", r"mv -T \"\$STATE/pending\" \"\$STATE/dropped/\$\(date \+%s\)\.\$\$\" \|\| return 1",
                      'rm -rf "$STATE/pending" || true', ["the saved set can't be removed"]),
    "web-not-atomic": ("ops/rollout.sh", r"ln -sfn \"\$1\" \"\$DEPLOY_DIR/web/current\.new\" \|\| return 1",
                       'rm -f "$DEPLOY_DIR/web/current"; sleep 3; ln -sfn "$1" "$DEPLOY_DIR/web/current.new"', ["a new release"]),
    "never-settled": ("ops/rollout.sh", r"touch \"\$STATE/pending/settled\"", "true", ["a crash while restoring"]),
    "keeps-no-backup": ("ops/backup.sh", r"-mtime \+\"\$KEEP_DAYS\" -delete", '! -name "*$ts*" -delete', ["backup, restored and spoiled"]),
    "in-the-session": ("ops/rollout.sh", r"systemd-run --quiet .*?\n.*?\n\s*--setenv=HOME=\"\$\{HOME:-/root\}\" ", "", ["a cut session"]),
    "no-watch": ("ops/rollout.sh", r"ExecStart=\$SELF watch", "ExecStart=/bin/true", ["a lost runner"]),
    "boot-starts-anyway": ("ops/rollout.sh", r"flock -n 9 \|\| exit 1", "flock -n 9 || exit 0", ["a boot during a rollback"]),
    "hold-by-compose": ("ops/rollout.sh", r"stop_all \"\$\{APP\[@\]\}\" \|\| stopped=", 'dc stop "${APP[@]}" >/dev/null 2>&1 || stopped=', ["a hold with a broken compose file"]),
    "first-by-what-runs": ("ops/rollout.sh", r"! docker volume inspect [^\n]*", "! running maktaba-cloud", ["a node nobody committed"]),
    "truncated-dump": ("ops/backup.sh", r"\[ \"\$\(sha256sum [^\n]*\n\tpg pg_restore -f /dev/null [^\n]*", "pg pg_restore --list <\"$tmp/dump\" >/dev/null",
                       ["backup, restored and spoiled"]),
    "no-reserve": ("ops/rollout.sh", r"if ! fallocate [^\n]*", "true", ["the disk fills after begin"]),
    "downgrades": ("ops/rollout.sh", r"\[ \"\$now\" -lt \"\$was\" \]", "false", ["an older release"]),
}


def mutate(src: Path) -> None:
    if not MUTATE:
        return
    file, pattern, instead, _ = MUTATIONS[MUTATE]
    text = (src / file).read_text()
    out, n = re.subn(pattern, lambda _m: instead, text, flags=re.S if "\\n" in pattern and ".*?" in pattern else 0)
    assert n >= 1, f"{MUTATE}: nothing to take out of {file}"
    (src / file).write_text(out)
    sh(f"bash -n {src / file}")


# --- the releases ---------------------------------------------------------------------------

MIGRATIONS = {
    # an expansion: a table, and a column with a default
    "expand": "-- +goose Up\nCREATE TABLE rehearsal_new (id INT);\nALTER TABLE users ADD COLUMN rehearsal_col INT NOT NULL DEFAULT 7;\n"
              "-- +goose Down\nALTER TABLE users DROP COLUMN rehearsal_col;\nDROP TABLE rehearsal_new;\n",
    "upfails": "-- +goose Up\nCREATE TABLE rehearsal_half (id INT);\nSELECT no_such_function();\n-- +goose Down\nDROP TABLE rehearsal_half;\n",
    "contract": "-- contract: nothing reads it\n-- +goose Up\nCREATE TABLE rehearsal_contract (id INT);\n-- +goose Down\nDROP TABLE rehearsal_contract;\n",
    "both-contract": "-- contract: nothing reads it\n-- +goose Up\nALTER TABLE users DROP COLUMN display_name;\n-- +goose Down\nALTER TABLE users ADD COLUMN display_name TEXT;\n",
    "both-seeded": "-- +goose Up\nCREATE TABLE rehearsal_seeded (id INT);\nINSERT INTO rehearsal_seeded VALUES (1);\n-- +goose Down\nDROP TABLE rehearsal_seeded;\n",
    "downfails": "-- +goose Up\nCREATE TABLE rehearsal_down (id INT);\n-- +goose Down\nSELECT no_such_function();\n",
}


def build_releases() -> None:
    global CADDY
    sh("docker rm -f rehearsal-registry >/dev/null 2>&1; docker run -d --name rehearsal-registry --restart always -p 127.0.0.1:5000:5000 "
       "registry:2.8.3@sha256:a3d8aaa63ed8681a604f1dea0aa03f100d5895b6a58ace528858a7b332415373")
    time.sleep(3)

    def build(name: str, context: str, args: str = "") -> None:
        sh(f"docker build -q -f {REPO}/tools/rehearsal-cloud/Dockerfile --build-arg VERSION={name} {args} -t {REGISTRY}:{name} {context}", timeout=1800)
        sh(f"docker push -q {REGISTRY}:{name}")
        digest = sh(f"docker image inspect --format '{{{{index .RepoDigests 0}}}}' {REGISTRY}:{name}").strip().split("@")[1]
        IMAGES[name] = f"{name}@{digest}"
        print(f"release {name}: {IMAGES[name]}", flush=True)

    build("a", str(REPO))
    build("b", str(REPO))  # another version, no migration
    build("bad", str(REPO), "--build-arg BUILD_TAGS=rehearsalbad")  # its readiness fails 20 s after it starts
    numbers = {name: f"9999000{i + 1}" for i, name in enumerate(MIGRATIONS)}
    for name, migrations in {"expand": ["expand"], "upfails": ["upfails"], "contract": ["contract"],
                             "both": ["both-contract", "both-seeded"], "downfails": ["downfails"]}.items():
        if MUTATE and name not in ("expand",):
            continue  # the cases that notice a mutation need no more
        with tempfile.TemporaryDirectory() as d:
            sh(f"git -C {REPO} -c safe.directory={REPO} archive HEAD cloud shared tools/healthcheck | tar -x -C {d}")
            for m in migrations:
                (Path(d) / f"cloud/migrations/{numbers[m]}_rehearsal_{m.replace('-', '_')}.sql").write_text(MIGRATIONS[m])
            build(name, d)
    IMAGES["gone"] = "gone@sha256:" + "0" * 64
    tag = next(line.split("=", 1)[1] for line in (REPO / "deploy/cloud-relay/.env.example").read_text().splitlines()
               if line.startswith("MAKTABA_CADDY_TAG="))
    ref = f"ghcr.io/hamza-labs-core/maktaba-caddy:{tag}"
    CADDY = f"{tag}@" + json.loads(sh(f"docker buildx imagetools inspect {ref} --format '{{{{json .Manifest}}}}' 2>/dev/null"))["digest"]


# --- a deploy, as the workflow does one -----------------------------------------------------

LAST_TAR = b""


def tar_of(text: str, name: str = "index.html") -> bytes:
    global LAST_TAR
    buf = io.BytesIO()
    with tarfile.open(fileobj=buf, mode="w:gz") as t:
        info = tarfile.TarInfo(name)
        info.size = len(text)
        t.addfile(info, io.BytesIO(text.encode()))
    LAST_TAR = buf.getvalue()
    return LAST_TAR


def source(edit: Callable[[Path], None] | None = None) -> None:
    sh(f"rm -rf {SRC} && mkdir -p {SRC} && cp -a {REPO}/deploy/cloud-relay/. {SRC}/")
    if edit:
        edit(SRC)
    mutate(SRC)


def env_for(release: str) -> dict[str, str]:
    return {**NAMES, "MAKTABA_CLOUD_IMAGE": REGISTRY, "MAKTABA_CLOUD_TAG": IMAGES[release], "MAKTABA_CADDY_TAG": CADDY, **JOB_ENV}


# what begin's and the jobs' environment says; the watch looks every 15 s here
JOB_ENV = {"WATCH_SECONDS": WATCH, "WATCH_EVERY": "15"}
deploys = 0
ID = ""


def dumps() -> set[str]:
    return {p.name for p in Path("/var/backups/maktaba-cloud").glob("db-*.dump")}


def begin(release: str, edit: Callable[[Path], None] | None = None, extra: dict[str, str] | None = None) -> tuple[int, str]:
    global deploys, ID
    deploys += 1
    ID = f"{os.environ.get('GITHUB_RUN_ID', '1')}-{deploys}"
    source(edit)
    before = dumps()
    code, out = rc(f"bash {SRC}/ops/rollout.sh begin", env={**env_for(release), "DEPLOY_ID": ID, **(extra or {})})
    if code == 0 and not (STATE / "pending/first").exists():
        assert dumps() - before, "begin took no backup"
    return code, out


def write_set(release: str) -> None:
    sh(f"{ROLLOUT} setup {ID}", env=env_for(release))


def job(cmd: str, env: dict[str, str] | None = None, deploy: str = "") -> tuple[int, str]:
    return rc(f"{ROLLOUT} job {deploy or ID} {cmd} 0", env={**JOB_ENV, **(env or {})})


def prepare(release: str, web: str, edit: Callable[[Path], None] | None = None, extra: dict[str, str] | None = None) -> str:
    """begin, setup.sh and the web app: up to apply."""
    code, out = begin(release, edit, extra)
    if code != 0:
        raise AssertionError(f"begin {release} exited {code}:\n{out[-3000:]}")
    write_set(release)
    sh(f"{ROLLOUT} web {ID}", stdin=tar_of(web))
    return out


def deploy(release: str, web: str, edit: Callable[[Path], None] | None = None, extra: dict[str, str] | None = None) -> tuple[int, str]:
    """Up to the tests. (rc of apply, output)"""
    out = prepare(release, web, edit, extra)
    code, more = job("apply", extra)
    assert (code == 0) == ("__maktaba_deploy_complete__" in more), "the end marker and the exit code disagree"
    return code, out + more


def rollback() -> tuple[int, str]:
    return job("rollback")


def commit() -> None:
    code, out = job("commit")
    assert code == 0, out


# --- what a node is -------------------------------------------------------------------------

def page() -> str:
    return sh(f"curl -sk --max-time 10 --resolve {APP}:443:127.0.0.1 https://{APP}/").strip()


def env_lines() -> str:
    return "\n".join(sorted(line for line in (DEPLOY / ".env").read_text().splitlines() if line and not line.startswith("#")))


def snapshot() -> dict[str, str]:
    """The node as a whole: anything that differs after a failed deploy is a failed rollback."""
    images = {}
    for svc in ("maktaba-cloud", "api", "worker", "caddy", "postgres", "redis"):
        images[svc] = sh(f"cd {DEPLOY} && docker inspect --format '{{{{.Config.Image}}}} {{{{.State.Running}}}}' $(docker compose --env-file .env ps -aq {svc})").strip()
    return {
        "images": json.dumps(images), "env": env_lines(),
        "compose": hashlib.sha256((DEPLOY / "docker-compose.yml").read_bytes()).hexdigest(),
        "caddyfile": hashlib.sha256((DEPLOY / "Caddyfile").read_bytes()).hexdigest(),
        # the Caddyfile the running Caddy mounted (a bind mount holds the file it was started with)
        "caddy_runs": sh(f"cd {DEPLOY} && docker compose --env-file .env exec -T caddy sha256sum /etc/caddy/Caddyfile").split()[0],
        "toml": hashlib.sha256((DEPLOY / "cloud.toml").read_bytes()).hexdigest(),
        "secrets": sh(f"cd {DEPLOY} && find secrets -type f -exec sha256sum {{}} + | sort"),
        "page": page(), "migrations": psql("SELECT string_agg(version_id::text, ' ' ORDER BY id) FROM goose_db_version WHERE is_applied"),
        "tables": psql("SELECT string_agg(tablename, ' ' ORDER BY tablename) FROM pg_tables WHERE schemaname = 'public'"),
    }


def ready() -> None:
    for svc, addr in (("maktaba-cloud", "127.0.0.1:8443"), ("api", "172.30.77.10:8090")):
        for path in ("healthz", "readyz"):
            dc(f"exec -T -e HEALTHCHECK_URL=http://{addr}/{path} {svc} /usr/local/bin/healthcheck </dev/null")


def same(before: dict[str, str], after: dict[str, str], but: tuple[str, ...] = ()) -> None:
    diff = {k: (before[k][-400:], after[k][-400:]) for k in before if before[k] != after[k] and k not in but}
    assert not diff, f"the node is not what it was: {json.dumps(diff, indent=1)}"


def wait_for(what: str, ok: Callable[[], bool], seconds: float) -> float:
    t0 = time.monotonic()
    while not ok():
        if time.monotonic() - t0 > seconds:
            raise AssertionError(f"{what}: not after {seconds:.0f} s; {sh(f'{ROLLOUT} status')}")
        time.sleep(0.2)
    return time.monotonic() - t0


def pending() -> bool:
    return (STATE / "pending").exists()


def app_running() -> set[str]:
    out = sh("docker ps --filter label=com.docker.compose.project=maktaba-relay --format '{{.Label \"com.docker.compose.service\"}}'")
    return set(out.split()) & {"maktaba-cloud", "api", "worker"}


@contextmanager
def served_only(*pages: str) -> Iterator[None]:
    """While it is open, every answer of the web app is one of pages, whole, every 100 ms."""
    stop = threading.Event()
    seen: list[str] = []

    def look() -> None:
        while not stop.is_set():
            p = subprocess.run(["curl", "-sk", "--max-time", "5", "-w", " %{http_code}", "--resolve", f"{APP}:443:127.0.0.1", f"https://{APP}/"],
                               capture_output=True, text=True)
            seen.append(p.stdout.strip() if p.returncode == 0 else f"curl {p.returncode}")
            time.sleep(0.1)

    t = threading.Thread(target=look)
    t.start()
    try:
        yield
    finally:
        stop.set()
        t.join()
    bad = sorted({x for x in seen if x not in [f"{p} 200" for p in pages]})
    assert not bad and len(seen) > 20, f"{len(seen)} answers, among them {bad[:5]}"


def not_pending() -> None:
    assert not (STATE / "pending").exists() and not (STATE / "held").exists(), sh(f"{ROLLOUT} status")


def reboot(other_boot: bool = True) -> tuple[int, str]:
    """What a reboot does to the node: every container stopped and started by docker, then the
    unit, whose condition reads the boot id (another one, as a kernel gives each boot)."""
    if other_boot and pending():
        (STATE / "pending/boot_id").write_text("00000000-0000-0000-0000-000000000000\n")
    sh("systemctl stop maktaba-relay || true; systemctl restart docker; systemctl reset-failed docker maktaba-relay || true")
    for _ in range(30):
        if rc("curl -sf http://localhost:5000/v2/")[0] == 0:
            break
        time.sleep(2)
    return rc("systemctl start maktaba-relay; systemctl status --no-pager -n 60 maktaba-relay", env={})


# --- the cases ------------------------------------------------------------------------------

def case(name: str, fn: Callable[[], str | None]) -> None:
    t0 = time.monotonic()
    print(f"\n=== {name}", flush=True)
    try:
        detail = fn() or ""
        ok = True
    except Exception as e:  # noqa: BLE001 - every case reports; a broken node fails the rest too
        detail, ok = str(e)[-3500:], False
    results.append({"case": name, "ok": ok, "sec": round(time.monotonic() - t0), "detail": detail})
    print(f"{'ok  ' if ok else 'FAIL'} {name} ({results[-1]['sec']} s) {detail}", flush=True)


def first_deploy_cut_off() -> str:
    """A first deploy whose run stopped: the next one undoes it and goes on."""
    code, out = begin("a")
    assert code == 0 and "a first deploy" in out, out[-2000:]
    write_set("a")
    code, out = begin("a")
    assert code == 0 and "cut off" in out, out[-3000:]
    code, out = rollback()
    assert code == 0 and "first deploy failed" in out, out[-2000:]
    not_pending()
    return "undone, and the next one began"


def first_deploy_fails() -> str:
    code, out = deploy("bad", "bad app")
    assert code != 0 and "first deploy failed" in out, out[-2000:]
    not_pending()
    assert not sh(f"cd {DEPLOY} && docker compose --env-file .env ps --status running --services").split().count("api"), "the api still runs"
    return "stopped, nothing to go back to"


def first_deploy() -> str:
    code, out = deploy("a", "app a")
    assert code == 0, out[-3000:]
    ready()
    assert "app a" in page()
    commit()
    not_pending()
    return "release a serves"


def redeploy() -> str:
    before = snapshot()
    ids = sh(f"cd {DEPLOY} && docker compose --env-file .env ps -q | sort")
    code, out = deploy("a", "app a")
    assert code == 0, out[-3000:]
    commit()
    same(before, snapshot())
    assert ids == sh(f"cd {DEPLOY} && docker compose --env-file .env ps -q | sort"), "an unchanged deploy recreated a container"
    return "nothing changed, no container recreated"


def failed_pull_and_reboot() -> str:
    before = snapshot()
    code, out = begin("gone")
    assert code != 0 and "nothing was changed" in out, out[-2000:]
    not_pending()
    same(before, snapshot())
    code, out = reboot()
    assert code == 0, out
    ready()
    same(before, snapshot())
    return "release a before and after the reboot"


def no_room() -> str:
    before = snapshot()
    code, out = begin("b", extra={"MIN_FREE_MB": "999999999"})
    assert code != 0 and "MB free" in out, out[-1500:]
    not_pending()
    same(before, snapshot())
    return "refused, unchanged"


def not_by_digest() -> str:
    before = snapshot()
    source()
    code, out = rc(f"bash {SRC}/ops/rollout.sh begin", env={**env_for("b"), "DEPLOY_ID": "1-tag", "MAKTABA_CLOUD_TAG": "b"})
    assert code != 0 and "by digest" in out, out[-1500:]

    def unpin(src: Path) -> None:
        f = src / "docker-compose.yml"
        f.write_text("\n".join(line.split("@sha256:")[0] if "image: postgres:" in line else line for line in f.read_text().split("\n")))

    code, out = begin("b", unpin)
    assert code != 0 and "not pinned by digest" in out, out[-1500:]
    not_pending()
    same(before, snapshot())
    return "a tag, and an unpinned Postgres, refused"


def unhealthy() -> str:
    before = snapshot()
    code, out = deploy("bad", "app bad")
    assert code != 0 and "restored" in out, out[-3000:]
    not_pending()
    ready()
    same(before, snapshot())
    return "readiness lost 20 s in; release a serves again"


def migration_fails() -> str:
    before = snapshot()
    code, out = deploy("upfails", "app upfails")
    assert code != 0 and "a migration failed" in out and "restored" in out, out[-3000:]
    not_pending()
    ready()
    same(before, snapshot())
    return "release a serves again, its migrations as they were"


def add_header(src: Path) -> None:
    f = src / "Caddyfile"
    f.write_text(f.read_text().replace("X-Frame-Options DENY", 'X-Frame-Options DENY\n\t\tX-Rehearsal "new"', 1))


def tests_fail() -> str:
    before = snapshot()
    code, out = deploy("expand", "app expand", add_header)
    assert code == 0, out[-3000:]
    assert "app expand" in page() and "rehearsal_new" in snapshot()["tables"]
    head = sh(f"curl -skI --resolve {APP}:443:127.0.0.1 https://{APP}/")
    assert "x-rehearsal: new" in head.lower(), "the changed Caddyfile is not what Caddy runs: " + head
    code, out = rollback()
    assert code == 0 and "undone by their Down scripts" in out, out[-3000:]
    not_pending()
    ready()
    same(before, snapshot())
    return "Down scripts, files, web app and images back"


def rows_written_since() -> str:
    before = snapshot()
    code, out = deploy("expand", "app expand")
    assert code == 0, out[-3000:]
    psql("INSERT INTO rehearsal_new VALUES (1)")
    code, out = rollback()
    assert code == 0 and "KEPT" in out, out[-3000:]
    not_pending()
    ready()
    same(before, snapshot(), but=("migrations", "tables"))
    assert psql("SELECT count(*) FROM rehearsal_new") == "1", "the row is gone"
    # back to release a's own schema for the cases after this one
    forget_expand()
    same(before, snapshot())
    return "the schema kept, the row kept, release a serves"


def bad_web_app() -> str:
    before = snapshot()
    code, out = begin("b")
    assert code == 0, out[-2000:]
    write_set("b")
    code, out = rc(f"{ROLLOUT} web {ID}", stdin=tar_of("no index", "other.html"))
    assert code != 0 and "index.html" in out, out
    code, _ = rc(f"{ROLLOUT} web {ID}", stdin=b"not a tar")
    assert code != 0
    assert page() == before["page"], "a broken app was served"
    code, out = rollback()
    assert code == 0, out[-2000:]
    not_pending()
    same(before, snapshot())
    return "the old app served throughout"


def setup_stops_half_way() -> str:
    before = snapshot()
    code, out = begin("b")
    assert code == 0, out[-2000:]
    write_set("b")
    with (DEPLOY / ".env").open("a") as f:
        f.write("\nPOSTGRES_DB=elsewhere\n")  # a .env half written: not what the database has
    (DEPLOY / "docker-compose.yml").write_text("not: [a compose file\n")
    code, out = rollback()
    assert code == 0, out[-2000:]
    not_pending()
    ready()
    same(before, snapshot())
    return "back, with nothing read from the broken set"


def migrated_outside_apply() -> str:
    """A role that starts migrates by itself: docker restarted in the middle of a deploy, or a
    person. Its migrations go back too, though apply never ran them."""
    before = snapshot()
    code, out = begin("expand")
    assert code == 0, out[-2000:]
    write_set("expand")
    dc("up -d --wait </dev/null")
    assert "rehearsal_new" in snapshot()["tables"]
    code, out = rollback()
    assert code == 0 and "undone by their Down scripts" in out, out[-3000:]
    not_pending()
    ready()
    same(before, snapshot())
    return "undone as apply's own are"


def a_crash_while_restoring() -> str:
    """The rollback's unit killed after it settled the migrations, with the files half put back: the
    watch restores again, and does not judge the migrations again."""
    before = snapshot()
    code, out = deploy("expand", "app expand", add_header)
    assert code == 0, out[-3000:]
    psql("INSERT INTO rehearsal_new VALUES (1)")  # the schema is kept
    subprocess.Popen(["bash", "-c", f"{ROLLOUT} job {ID} rollback 0 >/dev/null 2>&1"])
    wait_for("the rollback settles the migrations", lambda: (STATE / "pending/settled").exists(), 120)
    sh("systemctl stop maktaba-rollout-watch.timer")
    sh(f"systemctl kill -s KILL maktaba-rollout-rollback-{ID}")
    sh(f"rm -rf {DEPLOY}/.env {DEPLOY}/Caddyfile && mkdir -p {DEPLOY}/secrets.restore")
    sh("systemctl start maktaba-rollout-watch.timer")
    wait_for("the watch restores", lambda: not pending(), 300)
    said = sh("journalctl -u maktaba-rollout-watch --since -10min --no-pager -o cat")
    assert "stopped part way" in said and "serves again" in said and "undoing the migrations" not in said, said[-3000:]
    not_pending()
    ready()
    same(before, snapshot(), but=("migrations", "tables"))
    assert psql("SELECT count(*) FROM rehearsal_new") == "1", "the row is gone"
    forget_expand()
    same(before, snapshot())
    return "restored again, the kept schema not judged twice"


def forget_expand() -> None:
    psql("DROP TABLE rehearsal_new; ALTER TABLE users DROP COLUMN rehearsal_col; DELETE FROM goose_db_version WHERE version_id = 99990001")


def the_saved_set_cannot_be_removed() -> str:
    """Something in the saved set can't be removed (a crash half way through looks the same): it was
    renamed first, so it is nobody's, and nothing restores it later."""
    before = snapshot()
    code, out = deploy("expand", "app expand")
    assert code == 0, out[-3000:]
    stuck = STATE / "pending/set/ops"
    sh(f"chattr +i {stuck}/backup.sh")
    try:
        code, out = rollback()
        assert code == 0, out[-3000:]
        not_pending()
        same(before, snapshot())
        code, out = reboot()
        assert code == 0, out[-2000:]
        same(before, snapshot())
    finally:
        sh(f"chattr -R -i {STATE} || true")
    # and a commit's
    code, out = deploy("a", "app a")
    assert code == 0, out[-3000:]
    sh(f"chattr +i {stuck}/backup.sh")
    try:
        commit()
        not_pending()
        sh("systemctl start maktaba-rollout-watch.service")
        same(before, snapshot())
    finally:
        sh(f"chattr -R -i {STATE} || true; rm -rf {STATE}/dropped")
    return "out of the way in one rename"


def two_failed_in_a_row_with_a_kept_schema() -> str:
    before = snapshot()
    for _ in range(2):
        code, out = deploy("expand", "app expand")
        assert code == 0, out[-3000:]
        psql("INSERT INTO rehearsal_new VALUES (1)")
        code, out = rollback()
        assert code == 0, out[-3000:]
        not_pending()
        ready()
        same(before, snapshot(), but=("migrations", "tables"))
    assert psql("SELECT count(*) FROM rehearsal_new") == "2", "a row is gone"
    # the second one applied nothing, so it had nothing to undo; the third is kept
    code, out = deploy("expand", "app expand")
    assert code == 0, out[-3000:]
    commit()
    ready()
    code, out = deploy("a", "app a")
    assert code == 0, out[-3000:]
    commit()
    forget_expand()
    same(before, snapshot())
    return "the row of each kept, and the release then deployed"


def older_release() -> str:
    """An old run started again: its release is refused, its commands do nothing to this deploy."""
    before = snapshot()
    was = (STATE / "committed").read_text()
    digest = IMAGES["b"].split("@")[1]
    (STATE / "committed").write_text(f"v9.10.0@{digest}\n")
    try:
        for old in ("v9.9.9", "v8.99.0", "v0.3.162"):
            code, out = begin("b", extra={"MAKTABA_CLOUD_TAG": f"{old}@{digest}"})
            assert code != 0 and "older than the node's release" in out, out[-1500:]
            not_pending()
        for fine in ("v9.10.0", "v9.10.1", "v10.0.0"):
            code, out = begin("b", extra={"MAKTABA_CLOUD_TAG": f"{fine}@{digest}"})
            assert code == 0, out[-1500:]
            for cmd in ("setup", "web", "apply", "commit", "renew"):
                code, out = rc(f"{ROLLOUT} {cmd} 1-0", stdin=b"")
                assert code != 0 and "not 1-0" in out, f"{cmd}: {out}"
            code, out = job("rollback", deploy="1-0")
            assert code == 0 and "nothing to restore" in out and pending(), out
            sh(f"rm -rf {STATE}/jobs/1-0.rollback")
            code, out = rollback()
            assert code == 0, out
            not_pending()
    finally:
        (STATE / "committed").write_text(was)
    same(before, snapshot())
    return "refused; another deploy's commands do nothing"


def one_at_a_time() -> str:
    before = snapshot()
    held_by = subprocess.Popen(["flock", str(STATE / "lock"), "sleep", "600"], start_new_session=True)
    try:
        time.sleep(1)
        code, out = begin("b", extra={"LOCK_WAIT": "2"})
        assert code != 0 and "at work" in out, out[-1500:]
        not_pending()
        code, out = rc(f"{ROLLOUT} boot")
        assert code == 1, f"a boot while a command is at work: {code} {out}"
    finally:
        os.killpg(held_by.pid, signal.SIGKILL)
        held_by.wait()
    prepare("b", "app b")
    p = subprocess.Popen(["bash", "-c", f"{ROLLOUT} job {ID} apply 0 >/dev/null 2>&1"], env={**os.environ, **JOB_ENV})
    wait_for("apply is at work", lambda: (STATE / "pending/applying").exists(), 60)
    assert rc(f"flock -n {STATE}/lock true")[0] != 0, "apply holds no lock"
    code, out = rc(f"{ROLLOUT} commit {ID}", env={"LOCK_WAIT": "1"})
    assert code != 0 and "at work" in out, out
    p.wait()
    code, out = rollback()
    assert code == 0, out[-2000:]
    same(before, snapshot())
    return "each refused while another is at work"


def boot_during_a_rollback() -> str:
    """The stack's unit started (another boot id) again and again while a rollback is at work: the
    rollback's is the only start."""
    before = snapshot()
    code, out = deploy("expand", "app expand")
    assert code == 0, out[-3000:]
    (STATE / "pending/boot_id").write_text("00000000-0000-0000-0000-000000000000\n")
    sh("systemctl stop maktaba-relay || true") if rc("systemctl is-active maktaba-relay")[0] == 0 else None
    p = subprocess.Popen(["bash", "-c", f"{ROLLOUT} job {ID} rollback 0 >/dev/null 2>&1"], env={**os.environ, **JOB_ENV})
    wait_for("the rollback is at work", lambda: (STATE / "pending/restoring").exists(), 60)
    asked = 0
    while p.poll() is None:
        code, out = rc(f"{ROLLOUT} boot")
        if pending():  # the rollback was still at work when boot answered
            assert code == 1, f"boot would start the stack in the middle of a rollback: {code} {out}"
            asked += 1
        rc("systemctl reset-failed maktaba-relay; timeout 5 systemctl start maktaba-relay")
        time.sleep(0.3)
    assert asked >= 3, f"boot was asked {asked} times"
    code, out = rollback()
    assert code == 0 and "undone by their Down scripts" in out and "HELD" not in out, out[-3000:]
    not_pending()
    ready()
    same(before, snapshot())
    return "nothing else started it"


def hold_with_a_broken_compose_file() -> str:
    """The database can't come up and the new compose file can't be read: held means stopped."""
    before = snapshot()
    keep = Path("/root/rehearsal-set")
    sh(f"rm -rf {keep} && cp -a {DEPLOY} {keep}")
    code, out = deploy("b", "app b")
    assert code == 0, out[-3000:]
    (DEPLOY / "docker-compose.yml").write_text("not: [a compose file\n")
    (STATE / "pending/set/docker-compose.yml").write_text("not: [a compose file either\n")
    code, out = rollback()
    assert code != 0 and "HELD" in out and "are stopped" in out, out[-3000:]
    assert not app_running(), f"still running: {app_running()}"
    code, out = reboot()
    assert not app_running(), "a held node started at boot"
    sh(f"{ROLLOUT} unhold")
    # a person sets it right: the folder as it was, and the same release again
    sh(f"cd {keep} && cp -a .env docker-compose.yml Caddyfile cloud.toml secrets {DEPLOY}/")
    code, out = deploy("a", "app a")
    assert code == 0, out[-3000:]
    commit()
    sh(f"rm -rf {keep} {STATE}/held-set")
    ready()
    same(before, snapshot())
    return "stopped whatever compose could say"


def node_nobody_committed() -> str:
    """No record of a committed deploy (set up by hand, or held and unheld), its relay not running at
    this moment: it has a database, so it is no first deploy, and it is saved and backed up."""
    before = snapshot()
    was = (STATE / "committed").read_text()
    (STATE / "committed").unlink()
    dc("stop maktaba-cloud")
    code, out = begin("b")
    assert code == 0 and "first deploy" not in out, out[-2000:]
    assert (STATE / "pending/set/.env").exists() and not (STATE / "pending/first").exists()
    code, out = rollback()
    assert code == 0, out[-2000:]
    not_pending()
    ready()
    (STATE / "committed").write_text(was)
    same(before, snapshot())
    return "saved and backed up"


def disk_fills() -> str:
    """The deploy folder's disk fills between begin and setup.sh: it goes back all the same."""
    before = snapshot()
    def larger(src: Path) -> None:
        with (src / "Caddyfile").open("a") as f:
            f.write("# a larger file than before\n" * 100_000)

    code, out = begin("expand", larger)
    assert code == 0, out[-2000:]
    sh(f"dd if=/dev/zero of={DEPLOY}/fill bs=1M 2>/dev/null; dd if=/dev/zero of={DEPLOY}/fill2 bs=1k 2>/dev/null; true")
    code, out = rc(f"{ROLLOUT} setup {ID}", env=env_for("expand"))
    assert code != 0, "setup.sh wrote a new set on a full disk"
    code, out = rollback()
    sh(f"rm -f {DEPLOY}/fill {DEPLOY}/fill2")
    assert code == 0 and "HELD" not in out, out[-3000:]
    not_pending()
    ready()
    same(before, snapshot())
    return "back, from the room begin kept"


def backups_during_a_deploy() -> str:
    """The nightly backup's unit, again and again while a deploy goes live and goes back: each one
    ends well, and what it took restores."""
    before = snapshot()
    stop = threading.Event()
    failed: list[str] = []
    ran = [0]

    def loop() -> None:
        while not stop.is_set():
            code, out = rc("systemctl start maktaba-cloud-backup.service || journalctl -u maktaba-cloud-backup -n 20 --no-pager")
            ran[0] += 1
            if code != 0 or "failed" in out:
                failed.append(out[-800:])
            time.sleep(1)

    t = threading.Thread(target=loop)
    t.start()
    try:
        code, out = deploy("expand", "app expand", add_header)
        assert code == 0, out[-3000:]
        code, out = rollback()
        assert code == 0, out[-3000:]
        wait_for("a backup after it", lambda: ran[0] >= 2, 120)
    finally:
        stop.set()
        t.join()
    assert not failed, failed[0]
    sh(f"DEPLOY_DIR={DEPLOY} {DEPLOY}/ops/backup.sh --check")
    same(before, snapshot())
    return f"{ran[0]} backups alongside, the newest restored"


# --- a cut session, a cancelled run, a lost runner ----------------------------------------------

def ssh_env() -> dict[str, str]:
    key = Path("/root/rehearsal_ssh")
    if not key.exists():
        sh("DEBIAN_FRONTEND=noninteractive apt-get install -y -q openssh-server >/dev/null 2>&1; mkdir -p /root/.ssh /run/sshd; chmod 700 /root/.ssh; "
           f"ssh-keygen -q -t ed25519 -N '' -f {key}; cat {key}.pub >> /root/.ssh/authorized_keys; chmod 600 /root/.ssh/authorized_keys; "
           "ssh-keygen -A >/dev/null; systemctl start ssh")
    host = Path("/etc/ssh/ssh_host_ed25519_key.pub").read_text().split()
    return {"RELAY_SSH_HOST": "127.0.0.1", "RELAY_SSH_USER": "root", "RELAY_SSH_KEY": key.read_text(),
            "RELAY_SSH_KNOWN_HOSTS": f"127.0.0.1 {host[0]} {host[1]}", "DEPLOY_ID": ID, "RETRY_SECONDS": "2"}


def remote(action: str, log: Path) -> subprocess.Popen[bytes]:
    """deploy.sh as the workflow runs it, over a real SSH session, in a process group of its own."""
    return subprocess.Popen(["bash", str(REPO / "deploy/cloud-relay/deploy.sh"), action], stdin=subprocess.DEVNULL,
                            stdout=log.open("wb"), stderr=subprocess.STDOUT, env={**os.environ, **JOB_ENV, **ssh_env()}, start_new_session=True)


def said(log: Path, text: str) -> Callable[[], bool]:
    return lambda: text in log.read_text(errors="replace")


def cut_it(at_the_node: bool) -> None:
    """The session ended under it: its client killed, or the node's end of it (a dropped network)."""
    pattern = "sshd.*root@notty" if at_the_node else "^ssh .*maktaba-rollout job"
    # no shell: its own command line would match
    code = subprocess.run(["pkill", "-9", "-f", pattern], check=False).returncode
    assert code == 0, f"no session to cut ({pattern})"


def cut_session() -> str:
    """The SSH session killed in the middle of apply, then of rollback: the node's unit goes on, and
    deploy.sh picks it up where it was."""
    before = snapshot()
    prepare("expand", "app expand", add_header)
    log = OUT / "cut-apply.log"
    p = remote("apply", log)
    for text in ("the migrations", "its health"):
        wait_for(text, said(log, text), 120)
        cut_it(text in ("its health", "undone by their Down scripts"))
    assert p.wait(300) == 0, log.read_text()[-3000:]
    text = log.read_text()
    assert text.count("the session was cut") >= 2 and text.count("[rollout] the migrations") == 1, text[-3000:]
    log = OUT / "cut-rollback.log"
    p = remote("rollback", log)
    for text in ("restoring the set", "undone by their Down scripts"):
        wait_for(text, said(log, text), 120)
        cut_it(text in ("its health", "undone by their Down scripts"))
    assert p.wait(400) == 0, log.read_text()[-3000:]
    assert "serves again" in log.read_text() and "HELD" not in log.read_text()
    not_pending()
    ready()
    same(before, snapshot())
    return "both went on and were followed to their ends"


def runner_gone_in_the_rollback() -> str:
    """The runner killed outright in the middle of the rollback: nobody follows it, and it ends."""
    before = snapshot()
    code, out = deploy("expand", "app expand", add_header)
    assert code == 0, out[-3000:]
    for delay in (0.5, 4):
        log = OUT / f"gone-rollback-{delay}.log"
        p = remote("rollback", log)
        wait_for("the rollback began", lambda: (STATE / "pending/restoring").exists() or not pending(), 60)
        time.sleep(delay)
        os.killpg(p.pid, signal.SIGKILL)
    wait_for("the rollback ends on the node", lambda: not pending(), 300)
    not_pending()
    ready()
    same(before, snapshot())
    return "back with nobody there"


def cancelled_in_the_deploy_step() -> str:
    """The run cancelled in the middle of apply: the rollback step waits for the node's apply to end,
    then goes back; the runner is gone before it does."""
    before = snapshot()
    prepare("expand", "app expand", add_header)
    log = OUT / "cancel-apply.log"
    p = remote("apply", log)
    wait_for("apply is at the stack", said(log, "the stack"), 120)
    os.killpg(p.pid, signal.SIGTERM)
    p.wait(30)
    log = OUT / "cancel-rollback.log"
    p = remote("rollback", log)
    time.sleep(5)
    os.killpg(p.pid, signal.SIGKILL)
    wait_for("the node is back", lambda: not pending(), 400)
    not_pending()
    ready()
    same(before, snapshot())
    return "back, after the apply it waited for"


def lost_runner() -> str:
    """No word from the workflow: renewed, the deploy stays; not renewed, the node goes back."""
    before = snapshot()
    lease = {"LEASE_SECONDS": "40"}
    code, out = deploy("expand", "app expand", extra=lease)
    assert code == 0, out[-3000:]
    for _ in range(12):
        sh(f"{ROLLOUT} renew {ID}", env=lease)
        time.sleep(6)
    assert pending(), "a renewed deploy went back"
    took = wait_for("the watch brings it back", lambda: not pending(), 240)
    not_pending()
    ready()
    same(before, snapshot())
    code, out = job("commit")
    assert code != 0, "a deploy that went back was committed"
    return f"renewed for 72 s it stayed; silent, it was back after {took:.0f} s"


def killed_in_the_middle_of_apply() -> str:
    """apply's own unit killed (out of memory, say): whoever follows hears it, and the watch restores."""
    before = snapshot()
    prepare("expand", "app expand", add_header)
    p = subprocess.Popen(["bash", "-c", f"{ROLLOUT} job {ID} apply 0"], stdout=subprocess.PIPE, stderr=subprocess.STDOUT, env={**os.environ, **JOB_ENV})
    wait_for("apply is at work", lambda: (STATE / "pending/applying").exists(), 60)
    time.sleep(6)
    sh(f"systemctl kill -s KILL maktaba-rollout-apply-{ID}")
    out = p.communicate(timeout=120)[0].decode()
    assert p.returncode == 70 and "stopped part way" in out, f"{p.returncode} {out[-1500:]}"
    wait_for("the watch restores", lambda: not pending(), 300)
    not_pending()
    ready()
    same(before, snapshot())
    return "the watch restored it"


def reboot_during_the_tests() -> str:
    before = snapshot()
    code, out = deploy("expand", "app expand")
    assert code == 0, out[-3000:]
    # in the same boot the unit goes on with the deploy
    code, out = rc(f"{ROLLOUT} boot")
    assert code == 0 and pending(), out
    code, out = reboot()
    assert code == 0, out[-3000:]
    not_pending()
    ready()
    same(before, snapshot())
    return "the node went back by itself"


def cut_off_then_the_next_deploy() -> str:
    before = snapshot()
    code, out = deploy("expand", "app expand")
    assert code == 0, out[-3000:]
    # the run stopped here; the next deploy begins
    code, out = begin("b")
    assert code == 0 and "cut off" in out, out[-3000:]
    same(before, snapshot())
    code, out = rollback()
    assert code == 0, out
    not_pending()
    same(before, snapshot())
    return "the cut-off deploy was undone before the next one began"


def config_only() -> str:
    def edit(src: Path) -> None:
        f = src / "cloud.toml.template"
        f.write_text(f.read_text().replace('log_level  = "info"', 'log_level  = "debug"', 1))

    ids = {s: sh(f"cd {DEPLOY} && docker compose --env-file .env ps -q {s}").strip() for s in ("maktaba-cloud", "api", "worker", "caddy", "postgres")}
    before = snapshot()
    code, out = deploy("a", "app a", edit)
    assert code == 0, out[-3000:]
    now = {s: sh(f"cd {DEPLOY} && docker compose --env-file .env ps -q {s}").strip() for s in ids}
    changed = sorted(s for s in ids if ids[s] != now[s])
    assert changed == ["api", "maktaba-cloud", "worker"], f"recreated: {changed}"
    code, out = rollback()
    assert code == 0, out
    same(before, snapshot())
    return "a changed cloud.toml recreated its roles alone"


def new_release() -> str:
    with served_only("app a", "app b"):
        code, out = deploy("b", "app b")
        assert code == 0, out[-3000:]
        # the same bundle again, the one being served: never removed
        sh(f"{ROLLOUT} web {ID}", stdin=LAST_TAR)
        commit()
    not_pending()
    ready()
    assert "app b" in page() and IMAGES["b"] in env_lines()
    left = sh("docker image ls --digests --format '{{.Repository}}@{{.Digest}}' | grep maktaba-cloud || true")
    assert IMAGES["a"].split("@")[1] not in left, "release a's image is still on the disk"
    assert len(list((DEPLOY / "web/releases").iterdir())) == 1
    return "release b serves; the last one's image and app are gone"


def held(release: str, says: str) -> str:
    code, out = deploy(release, "app " + release)
    assert code == 0, out[-3000:]
    code, out = rollback()
    assert code != 0 and says in out and "HELD" in out, out[-3000:]
    running = sh(f"cd {DEPLOY} && docker compose --env-file .env ps --status running --services").split()
    assert not {"maktaba-cloud", "api", "worker"} & set(running), f"still running: {running}"
    assert "postgres" in running
    assert (STATE / "pending/set/.env").exists(), "the saved set is gone"
    code, out = begin("b")
    assert code != 0 and "held" in out, out[-1500:]
    reboot()
    assert not {"maktaba-cloud", "api", "worker"} & set(sh(f"cd {DEPLOY} && docker compose --env-file .env ps --status running --services").split()), \
        "a held node started at boot"
    # a person decides
    sh(f"{ROLLOUT} unhold")
    code, out = deploy("b", "app b")
    assert code == 0, out[-3000:]
    commit()
    ready()
    return "stopped, said why, refused deploys and boots until a person decided"


def backup_and_check() -> str:
    folder = Path("/var/backups/maktaba-cloud")
    aged = {}
    for days in (3, 13, 16, 40):
        for name in (f"db-2020010{days % 10}T0000{days:02d}Z.dump", f"keys-2020010{days % 10}T0000{days:02d}Z.tar.gz"):
            (folder / name).write_bytes(b"old")
            os.utime(folder / name, (time.time() - days * 86400,) * 2)
            aged[name] = days
    sh("systemctl start maktaba-cloud-backup.service")
    for name, days in aged.items():
        assert (folder / name).exists() == (days < 14), f"{name}, {days} days old: {'kept' if (folder / name).exists() else 'gone'}"
        (folder / name).unlink(missing_ok=True)
    # a dump cut short on its way out of the container is not kept
    shim = Path("/root/rehearsal-shim")
    shim.mkdir(exist_ok=True)
    whole = max((folder / d).stat().st_size for d in dumps())
    docker = sh("command -v docker").strip()
    # its table of contents whole, and rows missing at its end
    (shim / "docker").write_text(f'#!/bin/bash\ncase "$*" in *"cat /tmp/backup-"*) {docker} "$@" | head -c {whole * 9 // 10}; exit 0 ;; esac\nexec {docker} "$@"\n')
    (shim / "docker").chmod(0o755)
    had = dumps()
    code, said_ = rc(f"PATH={shim}:$PATH DEPLOY_DIR={DEPLOY} {DEPLOY}/ops/backup.sh")
    assert code != 0 and dumps() == had and "cut short" in said_, f"a dump without its end: {code} {said_}"
    sh("systemctl start maktaba-cloud-backup.service")
    out = sh(f"DEPLOY_DIR={DEPLOY} {DEPLOY}/ops/backup.sh --check")
    newest = sorted(Path("/var/backups/maktaba-cloud").glob("db-*.dump"))[-1]
    ts = newest.name[3:-5]
    spoiled = {
        "a count": (folder / f"db-{ts}.counts", lambda b: b.replace(b"public.users ", b"public.users 1", 1)),
        "a dump of another database": (newest, lambda b: _empty_dump()),
        "a key": (folder / f"keys-{ts}.tar.gz", lambda b: _keys_with_another_seed(b)),
        "no keys": (folder / f"keys-{ts}.tar.gz", None),
        "no counts": (folder / f"db-{ts}.counts", None),
    }
    for name, (path, change) in spoiled.items():
        good = path.read_bytes()
        if change is None:
            path.unlink()
        else:
            path.write_bytes(change(good))
        code, said = rc(f"DEPLOY_DIR={DEPLOY} {DEPLOY}/ops/backup.sh --check")
        path.write_bytes(good)
        assert code != 0, f"{name} spoiled, and the check passed: {said}"
    sh(f"DEPLOY_DIR={DEPLOY} {DEPLOY}/ops/backup.sh --check")
    assert psql("SELECT count(*) FROM pg_database WHERE datname LIKE 'restore_check%'") == "0"
    return out.strip().splitlines()[-1]


def _empty_dump() -> bytes:
    psql("CREATE DATABASE rehearsal_empty")
    try:
        return subprocess.run(["bash", "-c", f"cd {DEPLOY} && docker compose --env-file .env exec -T postgres pg_dump -U maktaba -d rehearsal_empty -Fc"],
                              stdin=subprocess.DEVNULL, capture_output=True, check=True).stdout
    finally:
        psql("DROP DATABASE rehearsal_empty")


def _keys_with_another_seed(good: bytes) -> bytes:
    out = io.BytesIO()
    with tarfile.open(fileobj=io.BytesIO(good)) as a, tarfile.open(fileobj=out, mode="w:gz") as b:
        for m in a.getmembers():
            data = a.extractfile(m).read() if m.isfile() else None  # type: ignore[union-attr]
            if m.name.endswith("entitlement.key"):
                data = b"x" * len(data or b"")
            b.addfile(m, io.BytesIO(data) if data is not None else None)
    return out.getvalue()


def test_accounts() -> str:
    psql("""INSERT INTO users (email, email_verified) VALUES ('ci-e2e-11@example.com', false), ('ci-e2e-12@example.com', true),
        ('someone@example.com', true), ('ci-e2e-x@example.com', true);
        INSERT INTO servers (owner_user_id, name, slug, server_secret_hash)
        SELECT id, 'h', 'h-' || left(md5(email), 6), 'x' FROM users WHERE email IN ('ci-e2e-12@example.com', 'someone@example.com');
        INSERT INTO server_claims (token_hash, code, user_id, expires_at)
        SELECT md5(email), left(md5(email), 8), id, now() + INTERVAL '9 minutes' FROM users WHERE email LIKE '%@example.com'""")
    got = json.loads(sh(f"{DEPLOY}/ops/cloud-ops.py end-test-accounts"))
    assert got == {"ended": 2, "homes_removed": 1, "left_active": 0, "left_homes": 0}, got
    assert psql("SELECT string_agg(u.email, ' ' ORDER BY u.email) FROM account_deletions d JOIN users u ON u.id = d.user_id") == \
        "ci-e2e-11@example.com ci-e2e-12@example.com"
    assert psql("SELECT count(*) FROM servers s JOIN users u ON u.id = s.owner_user_id WHERE u.email = 'someone@example.com'") == "1"
    # a home that has not redeemed its code yet can't any more; nobody else's code is touched
    assert psql("SELECT string_agg(u.email, ' ' ORDER BY u.email) FROM server_claims c JOIN users u ON u.id = c.user_id") == \
        "ci-e2e-x@example.com someone@example.com"
    again = json.loads(sh(f"{DEPLOY}/ops/cloud-ops.py end-test-accounts"))
    assert again["ended"] == 0 and again["homes_removed"] == 0, again
    return "the tests' accounts alone"


def log_caps() -> str:
    d = json.loads(Path("/etc/docker/daemon.json").read_text())
    assert d["log-opts"]["max-size"] and d["log-opts"]["max-file"], d
    assert d.get("rehearsal-keep", None) is None
    assert d.get("exec-opts") == ["native.cgroupdriver=systemd"] or "exec-opts" not in d, "the file's own settings went"
    return json.dumps(d)


def main() -> int:
    OUT.mkdir(parents=True, exist_ok=True)
    if os.geteuid() != 0 or not os.environ.get("CI"):
        sys.exit("only as root, on a CI runner")
    # a daemon.json with settings of its own: the caps are merged into it
    dj = Path("/etc/docker/daemon.json")
    have = json.loads(dj.read_text()) if dj.exists() else {}
    have.pop("log-opts", None)
    have["exec-opts"] = ["native.cgroupdriver=systemd"]
    dj.parent.mkdir(exist_ok=True)
    dj.write_text(json.dumps(have))
    # the deploy folder on a small disk of its own, so that it can be filled
    sh(f"mkdir -p {DEPLOY} && truncate -s 160M /root/rehearsal-disk && mkfs.ext4 -q -F /root/rehearsal-disk && mount -o loop /root/rehearsal-disk {DEPLOY} && rmdir {DEPLOY}/lost+found")
    build_releases()
    first: list[tuple[str, Callable[[], str | None]]] = [
        ("a first deploy cut off", first_deploy_cut_off),
        ("a first deploy that fails", first_deploy_fails),
        ("the first deploy", first_deploy),
        ("log caps merged", log_caps),
    ]
    part1: list[tuple[str, Callable[[], str | None]]] = [
        ("the same release again", redeploy),
        ("a failed pull, then a reboot", failed_pull_and_reboot),
        ("no room on the disk", no_room),
        ("images not pinned by digest", not_by_digest),
        ("an older release", older_release),
        ("a release that stops being ready", unhealthy),
        ("a migration that fails", migration_fails),
        ("its tests fail after it went live", tests_fail),
        ("rows written since the migration", rows_written_since),
        ("two failed in a row, the schema kept", two_failed_in_a_row_with_a_kept_schema),
        ("a broken web app", bad_web_app),
        ("setup stopped half way", setup_stops_half_way),
        ("the disk fills after begin", disk_fills),
        ("migrated outside apply", migrated_outside_apply),
        ("a configuration change alone", config_only),
        ("a node nobody committed", node_nobody_committed),
        ("backup, restored and spoiled", backup_and_check),
        ("backups during a deploy", backups_during_a_deploy),
        ("the tests' accounts ended", test_accounts),
        ("a new release", new_release),
    ]
    part2: list[tuple[str, Callable[[], str | None]]] = [
        ("one command at a time", one_at_a_time),
        ("a reboot during its tests", reboot_during_the_tests),
        ("a boot during a rollback", boot_during_a_rollback),
        ("a crash while restoring", a_crash_while_restoring),
        ("the saved set can't be removed", the_saved_set_cannot_be_removed),
        ("a run cut off, then the next deploy", cut_off_then_the_next_deploy),
        ("a cut session", cut_session),
        ("the runner gone in the rollback", runner_gone_in_the_rollback),
        ("cancelled in the deploy step", cancelled_in_the_deploy_step),
        ("a lost runner", lost_runner),
        ("apply killed part way", killed_in_the_middle_of_apply),
        ("a hold with a broken compose file", hold_with_a_broken_compose_file),
        ("a Down script that fails", lambda: held("downfails", "a Down script failed")),
        ("a contract migration", lambda: held("contract", "can't be undone")),
        ("a contract hidden behind a kept one", lambda: held("both", "can't be undone")),
    ]
    every = part1 + part2
    todo = {"1": part1, "2": part2}.get(os.environ.get("PART", ""), every)
    if MUTATE:
        names = MUTATIONS[MUTATE][3]
        assert set(names) <= {n for n, _ in every}, names
        first, todo = first[2:3], [c for c in every if c[0] in names]
    for name, fn in first + todo:
        case(name, fn)
    (OUT / "results.json").write_text(json.dumps(results, indent=2))
    bad = [r["case"] for r in results if not r["ok"]]
    if os.environ.get("GITHUB_STEP_SUMMARY"):
        with open(os.environ["GITHUB_STEP_SUMMARY"], "a", encoding="utf-8") as f:
            f.write("| | case | time | |\n|---|---|---|---|\n" + "".join(
                f"| {'✅' if r['ok'] else '❌'} | {r['case']} | {r['sec']} s | {r['detail'].splitlines()[0][:200] if r['detail'] else ''} |\n" for r in results))
    print(f"\n{len(results) - len(bad)} of {len(results)} cases pass" + (f"; failed: {', '.join(bad)}" if bad else ""), flush=True)
    if MUTATE:
        noticed = set(bad)
        print(f"{MUTATE}: {'noticed by ' + ', '.join(sorted(noticed)) if noticed else 'NOT NOTICED'}", flush=True)
        return 0 if noticed else 1
    return 1 if bad or len(results) != len(first) + len(todo) else 0


if __name__ == "__main__":
    sys.exit(main())
