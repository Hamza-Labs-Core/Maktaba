#!/usr/bin/env python3
"""The single node's rollout, rehearsed for real (cloud-rehearsal.yml, the single-node job).

On a throwaway machine (the CI runner), as root: setup.sh, ops/rollout.sh, ops/backup.sh and the
compose stack exactly as a node runs them, with this commit's cloud images in a registry on the
machine. Each way a deploy can fail is made to happen, and the node must be what it was before: the
images its roles run, .env, the compose file, the Caddyfile, the web app, the migrations, healthy
and ready. A reboot is the node's own: docker and the unit started again with another boot id.

    sudo REPO=$PWD python3 deploy/cloud-relay/tests/rollout_rehearsal.py

Never on a machine anyone uses: it installs Docker's settings, a firewall and a systemd unit.
"""
from __future__ import annotations

import hashlib
import io
import json
import os
import subprocess
import sys
import tarfile
import tempfile
import time
from pathlib import Path
from typing import Any, Callable

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


# --- the releases ---------------------------------------------------------------------------

MIGRATIONS = {
    # an expansion: a table, and a column with a default
    "expand": "-- +goose Up\nCREATE TABLE rehearsal_new (id INT);\nALTER TABLE users ADD COLUMN rehearsal_col INT NOT NULL DEFAULT 7;\n"
              "-- +goose Down\nALTER TABLE users DROP COLUMN rehearsal_col;\nDROP TABLE rehearsal_new;\n",
    "upfails": "-- +goose Up\nCREATE TABLE rehearsal_half (id INT);\nSELECT no_such_function();\n-- +goose Down\nDROP TABLE rehearsal_half;\n",
    "contract": "-- contract: nothing reads it\n-- +goose Up\nCREATE TABLE rehearsal_contract (id INT);\n-- +goose Down\nDROP TABLE rehearsal_contract;\n",
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
    for name, sql in MIGRATIONS.items():
        with tempfile.TemporaryDirectory() as d:
            sh(f"git -C {REPO} -c safe.directory={REPO} archive HEAD cloud shared tools/healthcheck | tar -x -C {d}")
            (Path(d) / f"cloud/migrations/9999000{list(MIGRATIONS).index(name) + 1}_rehearsal_{name}.sql").write_text(sql)
            build(name, d)
    IMAGES["gone"] = "gone@sha256:" + "0" * 64
    tag = next(line.split("=", 1)[1] for line in (REPO / "deploy/cloud-relay/.env.example").read_text().splitlines()
               if line.startswith("MAKTABA_CADDY_TAG="))
    ref = f"ghcr.io/hamza-labs-core/maktaba-caddy:{tag}"
    CADDY = f"{tag}@" + json.loads(sh(f"docker buildx imagetools inspect {ref} --format '{{{{json .Manifest}}}}' 2>/dev/null"))["digest"]


# --- a deploy, as the workflow does one -----------------------------------------------------

def tar_of(text: str, name: str = "index.html") -> bytes:
    buf = io.BytesIO()
    with tarfile.open(fileobj=buf, mode="w:gz") as t:
        info = tarfile.TarInfo(name)
        info.size = len(text)
        t.addfile(info, io.BytesIO(text.encode()))
    return buf.getvalue()


def source(edit: Callable[[Path], None] | None = None) -> None:
    sh(f"rm -rf {SRC} && mkdir -p {SRC} && cp -a {REPO}/deploy/cloud-relay/. {SRC}/")
    if edit:
        edit(SRC)


def env_for(release: str) -> dict[str, str]:
    return {**NAMES, "MAKTABA_CLOUD_IMAGE": REGISTRY, "MAKTABA_CLOUD_TAG": IMAGES[release], "MAKTABA_CADDY_TAG": CADDY, "WATCH_SECONDS": WATCH}


def begin(release: str, edit: Callable[[Path], None] | None = None, extra: dict[str, str] | None = None) -> tuple[int, str]:
    source(edit)
    return rc(f"bash {SRC}/ops/rollout.sh begin", env={**env_for(release), **(extra or {})})


def write_set(release: str) -> None:
    sh(f"cd {SRC} && SETUP_START=no bash setup.sh", env=env_for(release))


def deploy(release: str, web: str, edit: Callable[[Path], None] | None = None) -> tuple[int, str]:
    """begin, setup.sh, the web app, apply: up to the tests. (rc of apply, output)"""
    code, out = begin(release, edit)
    if code != 0:
        raise AssertionError(f"begin {release} exited {code}:\n{out[-3000:]}")
    write_set(release)
    sh(f"{ROLLOUT} web", stdin=tar_of(web))
    code, more = rc(f"{ROLLOUT} apply", env={"WATCH_SECONDS": WATCH})
    assert (code == 0) == ("__maktaba_deploy_complete__" in more), "the end marker and the exit code disagree"
    return code, out + more


def commit() -> None:
    sh(f"{ROLLOUT} commit")


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


def not_pending() -> None:
    assert not (STATE / "pending").exists() and not (STATE / "held").exists(), sh(f"{ROLLOUT} status")


def reboot(other_boot: bool = True) -> tuple[int, str]:
    """What a reboot does to the node: every container stopped and started by docker, then the
    unit, whose condition reads the boot id (another one, as a kernel gives each boot)."""
    if other_boot and (STATE / "pending").exists():
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
    code, out = rc(f"bash {SRC}/ops/rollout.sh begin", env={**env_for("b"), "MAKTABA_CLOUD_TAG": "b"})
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
    code, out = rc(f"{ROLLOUT} rollback", env={"WATCH_SECONDS": WATCH})
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
    code, out = rc(f"{ROLLOUT} rollback", env={"WATCH_SECONDS": WATCH})
    assert code == 0 and "KEPT" in out, out[-3000:]
    not_pending()
    ready()
    same(before, snapshot(), but=("migrations", "tables"))
    assert psql("SELECT count(*) FROM rehearsal_new") == "1", "the row is gone"
    # back to release a's own schema for the cases after this one
    psql("DROP TABLE rehearsal_new; ALTER TABLE users DROP COLUMN rehearsal_col; DELETE FROM goose_db_version WHERE version_id = 99990001")
    same(before, snapshot())
    return "the schema kept, the row kept, release a serves"


def bad_web_app() -> str:
    before = snapshot()
    code, out = begin("b")
    assert code == 0, out[-2000:]
    write_set("b")
    code, out = rc(f"{ROLLOUT} web", stdin=tar_of("no index", "other.html"))
    assert code != 0 and "index.html" in out, out
    code, _ = rc(f"{ROLLOUT} web", stdin=b"not a tar")
    assert code != 0
    assert page() == before["page"], "a broken app was served"
    code, out = rc(f"{ROLLOUT} rollback", env={"WATCH_SECONDS": WATCH})
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
    code, out = rc(f"{ROLLOUT} rollback", env={"WATCH_SECONDS": WATCH})
    assert code == 0, out[-2000:]
    not_pending()
    ready()
    same(before, snapshot())
    return "back, with nothing read from the broken set"


def reboot_during_the_tests() -> str:
    before = snapshot()
    code, out = deploy("expand", "app expand")
    assert code == 0, out[-3000:]
    # in the same boot the unit goes on with the deploy
    code, out = rc(f"{ROLLOUT} boot")
    assert code == 0 and (STATE / "pending").exists(), out
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
    code, out = rc(f"{ROLLOUT} rollback", env={"WATCH_SECONDS": WATCH})
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
    code, out = rc(f"{ROLLOUT} rollback", env={"WATCH_SECONDS": WATCH})
    assert code == 0, out
    same(before, snapshot())
    return "a changed cloud.toml recreated its roles alone"


def new_release() -> str:
    code, out = deploy("b", "app b")
    assert code == 0, out[-3000:]
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
    code, out = rc(f"{ROLLOUT} rollback", env={"WATCH_SECONDS": WATCH})
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
    sh("systemctl start maktaba-cloud-backup.service")
    out = sh(f"DEPLOY_DIR={DEPLOY} {DEPLOY}/ops/backup.sh --check")
    newest = sorted(Path("/var/backups/maktaba-cloud").glob("db-*.dump"))[-1]
    ts = newest.name[3:-5]
    folder = newest.parent
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
        SELECT id, 'h', 'h-' || left(md5(email), 6), 'x' FROM users WHERE email IN ('ci-e2e-12@example.com', 'someone@example.com')""")
    got = json.loads(sh(f"{DEPLOY}/ops/cloud-ops.py end-test-accounts"))
    assert got == {"ended": 2, "homes_removed": 1, "left_active": 0, "left_homes": 0}, got
    assert psql("SELECT string_agg(u.email, ' ' ORDER BY u.email) FROM account_deletions d JOIN users u ON u.id = d.user_id") == \
        "ci-e2e-11@example.com ci-e2e-12@example.com"
    assert psql("SELECT count(*) FROM servers s JOIN users u ON u.id = s.owner_user_id WHERE u.email = 'someone@example.com'") == "1"
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
    build_releases()
    case("a first deploy that fails", first_deploy_fails)
    case("the first deploy", first_deploy)
    case("log caps merged", log_caps)
    case("the same release again", redeploy)
    case("a failed pull, then a reboot", failed_pull_and_reboot)
    case("no room on the disk", no_room)
    case("images not pinned by digest", not_by_digest)
    case("a release that stops being ready", unhealthy)
    case("a migration that fails", migration_fails)
    case("its tests fail after it went live", tests_fail)
    case("rows written since the migration", rows_written_since)
    case("a broken web app", bad_web_app)
    case("setup stopped half way", setup_stops_half_way)
    case("a reboot during its tests", reboot_during_the_tests)
    case("a run cut off, then the next deploy", cut_off_then_the_next_deploy)
    case("a configuration change alone", config_only)
    case("backup, restored and spoiled", backup_and_check)
    case("the tests' accounts ended", test_accounts)
    case("a new release", new_release)
    case("a Down script that fails", lambda: held("downfails", "a Down script failed"))
    case("a contract migration", lambda: held("contract", "can't be undone"))
    (OUT / "results.json").write_text(json.dumps(results, indent=2))
    bad = [r["case"] for r in results if not r["ok"]]
    if os.environ.get("GITHUB_STEP_SUMMARY"):
        with open(os.environ["GITHUB_STEP_SUMMARY"], "a", encoding="utf-8") as f:
            f.write("| | case | time | |\n|---|---|---|---|\n" + "".join(
                f"| {'✅' if r['ok'] else '❌'} | {r['case']} | {r['sec']} s | {r['detail'].splitlines()[0][:200] if r['detail'] else ''} |\n" for r in results))
    print(f"\n{len(results) - len(bad)} of {len(results)} cases pass" + (f"; failed: {', '.join(bad)}" if bad else ""), flush=True)
    return 1 if bad or len(results) < 21 else 0


if __name__ == "__main__":
    sys.exit(main())
