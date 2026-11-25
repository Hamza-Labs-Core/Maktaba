#!/usr/bin/env python3
"""The single node's rollout on a machine that really reboots and really loses its power
(cloud-rehearsal.yml, the single-node-reboots job). rollout_rehearsal.py's other half.

A virtual machine (QEMU/KVM, the Ubuntu the servers run, from Ubuntu's own cloud image) is set up and
deployed to as the workflow does it: over SSH, with deploy.sh, as root, its images in a registry on
the runner. A reboot is `systemctl reboot`; a power cut is the machine's process killed, with
whatever it had not written to its disk. The node must come back to a known state by itself.

    sudo REPO=$PWD python3 deploy/cloud-relay/tests/reboot_rehearsal.py

    MUTATE=<name>       the node's scripts with one guarantee taken out (rollout_rehearsal.py), and
                        the cases here that must notice (NOTICE). Passes when one fails.
"""
from __future__ import annotations

import difflib
import io
import json
import os
import signal
import subprocess
import sys
import tarfile
import tempfile
import time
from pathlib import Path
from typing import Callable

sys.path.insert(0, str(Path(__file__).resolve().parent))
sys.dont_write_bytecode = True
import rollout_rehearsal as rr  # noqa: E402

REPO, OUT, MUTATE = rr.REPO, rr.OUT, rr.MUTATE
# one image, by its hash (a dated folder of Ubuntu's releases, which keeps it)
IMAGE_AT = "https://cloud-images.ubuntu.com/releases/26.04/release-20261108"
IMAGE = "ubuntu-26.04-server-cloudimg-amd64.img"
IMAGE_SHA256 = "7b90593f5532b49b7e3fe3d2743f0d1f0d822e5b07cebcd8e00c0df8069ecab9"
GUEST_REGISTRY = "10.0.2.2:5000/maktaba-cloud"  # the runner, as the machine's network names it
PORT = 2222
VM = Path("/var/tmp/reboot-rehearsal")
STATE, DEPLOY, SRC = rr.STATE, rr.DEPLOY, rr.SRC

# the cases, and what they must say: one that fails for another reason notices nothing
NOTICE = {"boots-a-failed-first": (["a first deploy's tests fail, then a reboot", "a first deploy's tests fail, then a power cut"], "a refused release"),
          "first-unsaved": (["the next deploy backs it up"], "begin took no backup"),
          "half-removed": (["a power cut with a container half removed"], "")}
# the cases a mutation's are run after (the ones that start from nothing run from the first)
AFTER = {"half-removed": ["the first deploy, then a power cut"]}


# --- the machine ----------------------------------------------------------------------------

qemu: subprocess.Popen[bytes] | None = None
SSH: list[str] = []


def make_machine() -> None:
    rr.sh("apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends qemu-system-x86 qemu-utils cloud-image-utils", timeout=600)
    assert Path("/dev/kvm").exists(), "no KVM on this runner"
    VM.mkdir(mode=0o700, parents=True, exist_ok=True)
    rr.sh(f"cd {VM} && curl -fsSL --retry 5 --retry-all-errors -o {IMAGE} {IMAGE_AT}/{IMAGE} && echo '{IMAGE_SHA256}  {IMAGE}' | sha256sum -c -", timeout=900)
    rr.sh(f"cd {VM} && rm -f id id.pub host host.pub disk.qcow2 && ssh-keygen -q -t ed25519 -N '' -f id && ssh-keygen -q -t ed25519 -N '' -f host && "
          f"qemu-img create -q -f qcow2 -F qcow2 -b {IMAGE} disk.qcow2 24G")
    host_key = "".join("    " + line + "\n" for line in (VM / "host").read_text().splitlines())
    (VM / "user-data").write_text(
        "#cloud-config\ndisable_root: false\nssh_deletekeys: true\nssh_keys:\n  ed25519_private: |\n" + host_key
        + f"  ed25519_public: {(VM / 'host.pub').read_text().strip()}\n"
        + f"users:\n  - name: root\n    ssh_authorized_keys:\n      - {(VM / 'id.pub').read_text().strip()}\n")
    (VM / "meta-data").write_text("instance-id: rehearsal\nlocal-hostname: rehearsal\n")
    rr.sh(f"cd {VM} && cloud-localds seed.img user-data meta-data")
    (VM / "known_hosts").write_text(f"[127.0.0.1]:{PORT} " + " ".join((VM / "host.pub").read_text().split()[:2]) + "\n")
    SSH[:] = ["ssh", "-p", str(PORT), "-i", str(VM / "id"), "-o", "IdentitiesOnly=yes", "-o", "BatchMode=yes", "-o", "ConnectTimeout=5",
              "-o", "ServerAliveInterval=5", "-o", "ServerAliveCountMax=2", "-o", "StrictHostKeyChecking=yes",
              "-o", f"UserKnownHostsFile={VM / 'known_hosts'}", "root@127.0.0.1"]


def power_on() -> None:
    global qemu
    log = open(OUT / "console.log", "ab")
    qemu = subprocess.Popen(
        ["qemu-system-x86_64", "-enable-kvm", "-cpu", "host", "-smp", "2", "-m", "4096", "-display", "none", "-serial", "stdio", "-monitor", "none",
         "-drive", f"file={VM}/disk.qcow2,if=virtio,format=qcow2,cache=none", "-drive", f"file={VM}/seed.img,if=virtio,format=raw,readonly=on",
         "-nic", f"user,model=virtio-net-pci,hostfwd=tcp:127.0.0.1:{PORT}-:22"],
        stdin=subprocess.DEVNULL, stdout=log, stderr=log)


def up(after: str = "") -> str:
    """The machine answers, in another boot than `after`, and has started what it starts."""
    end = time.monotonic() + 420
    while time.monotonic() < end:
        assert qemu and qemu.poll() is None, "the machine stopped"
        try:
            code, boot = on("cat /proc/sys/kernel/random/boot_id", check=False, timeout=20)
        except subprocess.TimeoutExpired:
            continue
        if code == 0 and boot.strip() and boot.strip() != after:
            break
        time.sleep(2)
    else:
        raise AssertionError("the machine does not answer")
    # the unit's condition restores first; what the boot starts has been started when its job is done
    end = time.monotonic() + 600
    while time.monotonic() < end:
        code, jobs = on("cloud-init status --wait >/dev/null 2>&1; systemctl list-jobs --no-legend | grep -E 'maktaba|docker|multi-user' || true", check=False, timeout=300)
        if code == 0 and not jobs.strip():
            return boot.strip()
        time.sleep(3)
    raise AssertionError(f"the boot does not end: {jobs}")


def boot_id() -> str:
    return on("cat /proc/sys/kernel/random/boot_id")[1].strip()


def reboot() -> None:
    was = boot_id()
    on("systemctl reboot", check=False)
    up(was)


def cut_the_power() -> None:
    assert qemu
    qemu.send_signal(signal.SIGKILL)
    qemu.wait()


def power_cut(was: str = "") -> None:
    was = was or boot_id()
    cut_the_power()
    power_on()
    up(was)


def on(cmd: str, stdin: bytes = b"", check: bool = True, timeout: float = 900) -> tuple[int, str]:
    p = subprocess.run([*SSH, cmd], input=stdin, capture_output=True, timeout=timeout)
    out = (p.stdout + p.stderr).decode("utf-8", "replace")
    if check and p.returncode != 0:
        raise AssertionError(f"`{cmd[:120]}` exited {p.returncode} on the machine:\n{out[-3000:]}")
    return p.returncode, out


# --- a deploy, as the workflow does one -----------------------------------------------------

deploys = 0
ID = ""


def q(v: str) -> str:
    return "'" + v.replace("'", "'\\''") + "'"


def env_words(release: str) -> str:
    env = {**rr.env_for(release), "MAKTABA_CLOUD_IMAGE": GUEST_REGISTRY, "DEPLOY_ID": ID, "WATCH_EVERY": "60"}
    return " ".join(f"{k}={q(v)}" for k, v in env.items())


def source() -> bytes:
    with tempfile.TemporaryDirectory() as d:
        src = Path(d) / "src"
        rr.sh(f"mkdir {src} && cp -a {REPO}/deploy/cloud-relay/. {src}/")
        rr.mutate(src)
        buf = io.BytesIO()
        with tarfile.open(fileobj=buf, mode="w:gz") as t:
            t.add(src, arcname=".")  # root's, as this is
        return buf.getvalue()


def dumps() -> set[str]:
    return set(on("ls /var/backups/maktaba-cloud 2>/dev/null | grep '\\.dump$' || true")[1].split())


def begin(release: str) -> str:
    global deploys, ID
    deploys += 1
    ID = f"reboots-{deploys}"
    on(f"rm -rf {SRC} && mkdir -p {SRC} && tar --no-same-owner -xzf - -C {SRC}", stdin=source())
    if deploys == 1:
        on(f"SETUP_DOCKER_ONLY=yes bash {SRC}/setup.sh", timeout=900)
    return on(f"{env_words(release)} bash {SRC}/ops/rollout.sh begin")[1]


def set_up(release: str) -> None:
    """The new set written: what the workflow does between begin and apply."""
    on(f"{env_words(release)} /usr/local/sbin/maktaba-rollout setup {ID}")
    on(f"/usr/local/sbin/maktaba-rollout web {ID}", stdin=rr.tar_of(f"app {release}"))


def deploy_sh(action: str) -> list[str]:
    return ["bash", str(REPO / "deploy/cloud-relay/deploy.sh"), action]


def deploy_env() -> dict[str, str]:
    return {**os.environ, "DEPLOY_ID": ID, "RELAY_SSH_HOST": "127.0.0.1", "RELAY_SSH_USER": "root", "RELAY_SSH_PORT": str(PORT),
            "RELAY_SSH_KEY": (VM / "id").read_text(), "RELAY_SSH_KNOWN_HOSTS": (VM / "known_hosts").read_text().strip(), "FOLLOW_SECONDS": "900"}


def follow(action: str) -> tuple[int, str]:
    p = subprocess.run(deploy_sh(action), env=deploy_env(), stdin=subprocess.DEVNULL, capture_output=True, timeout=1200)
    return p.returncode, (p.stdout + p.stderr).decode("utf-8", "replace")


def must(action: str) -> str:
    code, out = follow(action)
    assert code == 0, f"{action} exited {code}:\n{out[-3000:]}"
    return out


def live(release: str) -> None:
    begin(release)
    set_up(release)
    must("apply")


def started(action: str, said: str) -> subprocess.Popen[bytes]:
    """The action under way on the node, its output having said `said`."""
    p = subprocess.Popen(deploy_sh(action), env=deploy_env(), stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    end = time.monotonic() + 300
    while time.monotonic() < end:
        if said in on(f"cat {STATE}/jobs/{ID}.{action}/log 2>/dev/null", check=False)[1]:
            return p
        time.sleep(0.3)
    p.kill()
    raise AssertionError(f"{action} never said {said!r}")


# --- what the machine is --------------------------------------------------------------------

SNAPSHOT = f"""
cd {DEPLOY}
for s in maktaba-cloud api worker caddy postgres redis; do
  echo "$s $(docker inspect --format '{{{{.Config.Image}}}} {{{{.State.Running}}}}' $(docker compose --env-file .env ps -aq $s) 2>&1)"
done
grep -v '^#' .env | sed '/^$/d' | sort | sha256sum
sha256sum docker-compose.yml Caddyfile cloud.toml
find secrets -type f -exec sha256sum {{}} + | sort
docker compose --env-file .env exec -T caddy sha256sum /etc/caddy/Caddyfile </dev/null
curl -sk --max-time 10 --resolve {rr.APP}:443:127.0.0.1 https://{rr.APP}/
docker compose --env-file .env exec -T postgres psql -X -q -A -t -U maktaba -d maktaba -c "SELECT string_agg(version_id::text, ' ' ORDER BY id) FROM goose_db_version WHERE is_applied" </dev/null
docker compose --env-file .env exec -T postgres psql -X -q -A -t -U maktaba -d maktaba -c "SELECT string_agg(tablename, ' ' ORDER BY tablename) FROM pg_tables WHERE schemaname = 'public'" </dev/null
for p in maktaba-cloud:127.0.0.1:8443 api:172.30.77.10:8090; do
  for e in healthz readyz; do
    docker compose --env-file .env exec -T -e HEALTHCHECK_URL=http://${{p#*:}}/$e ${{p%%:*}} /usr/local/bin/healthcheck </dev/null >/dev/null 2>&1 && echo "${{p%%:*}} $e ok" || echo "${{p%%:*}} $e NO"
  done
done
"""


def snapshot() -> str:
    out = on(SNAPSHOT)[1]
    assert " NO\n" not in out and out.count(" ok\n") == 4, f"not healthy and ready:\n{out}"
    return out


def same(before: str) -> None:
    after = snapshot()
    diff = [line[:240] for line in difflib.unified_diff(before.splitlines(), after.splitlines(), lineterm="", n=0)][2:]
    assert before == after, "the node is not what it was:\n" + "\n".join(diff)[:3000]


def status() -> str:
    return on("/usr/local/sbin/maktaba-rollout status")[1].strip()


def settled() -> None:
    code, _ = on(f"[ ! -e {STATE}/pending ] && [ ! -e {STATE}/held ]", check=False)
    assert code == 0, status() + on("journalctl -b -u maktaba-relay -u maktaba-rollout-watch --no-pager -n 60", check=False)[1]


def nothing_serves() -> None:
    out = on("docker ps --filter label=com.docker.compose.project=maktaba-relay --format '{{.Label \"com.docker.compose.service\"}}'")[1].split()
    assert not set(out) & {"maktaba-cloud", "api", "worker", "caddy"}, f"a refused release runs: {out}"
    assert on(f"curl -sk --max-time 5 --resolve {rr.APP}:443:127.0.0.1 https://{rr.APP}/", check=False)[0] != 0, "a refused release answers"
    assert status() == "committed: none", status()


def psql(sql: str) -> str:
    return on(f"cd {DEPLOY} && docker compose --env-file .env up -d --wait postgres </dev/null >/dev/null 2>&1; "
              "docker compose --env-file .env exec -T postgres psql -X -q -A -t -v ON_ERROR_STOP=1 -U maktaba -d maktaba", stdin=sql.encode())[1].strip()


# --- the cases ------------------------------------------------------------------------------

def power_cut_while_the_database_is_made() -> str:
    """The very first start of Postgres on the node, cut: wherever it was, the next deploy begins."""
    begin("a")
    set_up("a")
    was = boot_id()
    p = subprocess.Popen(deploy_sh("apply"), env=deploy_env(), stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    end = time.monotonic() + 300
    while on("test -e /var/lib/docker/volumes/maktaba-relay_pg_data/_data/PG_VERSION", check=False)[0] != 0:
        assert time.monotonic() < end and p.poll() is None, "the database's folder was never made"
    p.kill()
    power_cut(was)
    settled()
    nothing_serves()
    out = begin("a")
    assert "a first deploy" in out, out[-2000:]
    how = "made afresh" if "made afresh" in out else "it had been made" if "the database is empty" in out else "nothing was there"
    set_up("a")
    must("apply")
    assert "first deploy failed" in must("rollback")
    settled()
    return how


def failed_first_then_a_reboot() -> str:
    out = begin("a")
    assert "a first deploy" in out, out[-1500:]
    set_up("a")
    must("apply")
    assert "app a" in on(f"curl -sk --max-time 10 --resolve {rr.APP}:443:127.0.0.1 https://{rr.APP}/")[1]
    assert "first deploy failed" in must("rollback")
    settled()
    nothing_serves()
    reboot()
    nothing_serves()
    assert on("systemctl is-enabled maktaba-relay")[1].strip() == "enabled", "the unit was not asked at all"
    return "down after `systemctl reboot`, the unit enabled"


def failed_first_then_a_power_cut() -> str:
    power_cut()
    settled()
    nothing_serves()
    return "down after its power was cut"


def next_deploy_backs_it_up() -> str:
    psql("INSERT INTO users (email, email_verified) VALUES ('first@example.com', true)")
    before = dumps()
    out = begin("a")
    assert "a first deploy" in out, out[-1500:]
    assert dumps() - before, "begin took no backup"
    newest = on("ls /var/backups/maktaba-cloud/db-*.counts | tail -n 1")[1].strip()
    assert "public.users 1\n" in on(f"cat {newest}")[1], "the account is not in the backup"
    return "a dump, with the account in it"


def power_cut_in_a_first_deploys_tests() -> str:
    set_up("a")
    must("apply")
    power_cut()
    settled()
    nothing_serves()
    out = must("rollback")  # the run, when it finds the node again
    assert "nothing to restore" in out, out[-1500:]
    nothing_serves()
    assert psql("SELECT count(*) FROM users") == "1", "the account went"
    return "stopped by the boot, and not started by it"


def first_deploy_then_a_power_cut() -> str:
    live("a")
    must("commit")
    was = on("cat /proc/sys/kernel/random/boot_id")[1].strip()
    power_cut(was)
    settled()
    snapshot()
    assert status() == f"committed: {rr.IMAGES['a']}", status()
    return "committed, and it serves"


def reboot_of_a_node_that_serves() -> str:
    before = snapshot()
    reboot()
    same(before)
    return "it serves"


def half_removed_container() -> str:
    """The power goes while a container is removed: its layer is gone, its folder is back. Docker lists it
    and knows nothing of it; compose can do nothing while it does."""
    before = snapshot()
    half = ("[ ${#c} = 64 ] && cp -a /var/lib/docker/containers/$c /root/half && docker rm -f $c >/dev/null && "
            "mv /root/half /var/lib/docker/containers/$c && sync && docker ps -aq --no-trunc")
    one_off = "docker compose --env-file .env run -d -T --no-deps maktaba-cloud migrate --config /etc/maktaba/cloud/cloud.toml up </dev/null 2>/dev/null"
    for deployed, which in ((False, "docker compose --env-file .env ps -q --no-trunc worker"),  # a role, on a node that serves
                            (True, f"docker inspect --format '{{{{.Id}}}}' $({one_off})")):  # the migrations' container, in a deploy
        if deployed:
            live("expand")
        on(f"cd {DEPLOY} && c=$({which}) && {half}")
        power_cut()
        settled()
        same(before)
        code, out = on("docker ps -aq --no-trunc | while read -r c; do docker inspect $c >/dev/null || echo half $c; done", check=False)
        assert "half" not in out, out
    return "taken out, and the node serves"


def reboot_in_the_tests() -> str:
    before = snapshot()
    live("expand")
    reboot()
    settled()
    same(before)
    return "the previous release"


def power_cut_in_the_tests() -> str:
    before = snapshot()
    live("expand")
    power_cut()
    settled()
    same(before)
    return "the previous release"


def power_cut_before_apply() -> str:
    before = snapshot()
    begin("expand")
    set_up("expand")
    power_cut()
    settled()
    same(before)
    return "the previous release, none of the new one's migrations"


def power_cut_in_apply() -> str:
    before = snapshot()
    begin("expand")
    set_up("expand")
    p = started("apply", "the stack")
    was = boot_id()
    p.kill()
    power_cut(was)
    settled()
    same(before)
    return "the previous release"


def power_cut_in_the_rollback() -> str:
    before = snapshot()
    live("slow")
    was = boot_id()
    p = started("rollback", "undoing the migrations")
    p.kill()
    power_cut(was)
    settled()
    same(before)
    return "the previous release"


def power_cut_late_in_the_rollback() -> str:
    before = snapshot()
    live("expand")
    was = boot_id()
    p = started("rollback", "undone by their Down scripts")
    p.kill()
    time.sleep(1)
    power_cut(was)
    settled()
    same(before)
    return "the previous release"


def reboot_in_the_rollback() -> str:
    before = snapshot()
    live("slow")
    was = boot_id()
    p = started("rollback", "undoing the migrations")
    p.kill()
    on("systemctl reboot", check=False)
    up(was)
    settled()
    same(before)
    return "the previous release"


def power_cut_after_a_commit() -> str:
    live("expand")
    must("commit")
    was = boot_id()
    cut_the_power()
    power_on()
    up(was)
    settled()
    snapshot()
    assert status() == f"committed: {rr.IMAGES['expand']}", status()
    assert "rehearsal_new" in psql("SELECT string_agg(tablename, ' ') FROM pg_tables"), "its schema went"
    return "the release that was committed"


def main() -> int:
    OUT.mkdir(parents=True, exist_ok=True)
    if os.geteuid() != 0 or not os.environ.get("CI"):
        sys.exit("only as root, on a CI runner")
    cases: list[tuple[str, Callable[[], str | None]]] = [
        ("a power cut while the database is made", power_cut_while_the_database_is_made),
        ("a first deploy's tests fail, then a reboot", failed_first_then_a_reboot),
        ("a first deploy's tests fail, then a power cut", failed_first_then_a_power_cut),
        ("the next deploy backs it up", next_deploy_backs_it_up),
        ("a power cut in a first deploy's tests", power_cut_in_a_first_deploys_tests),
        ("the first deploy, then a power cut", first_deploy_then_a_power_cut),
        ("a reboot of a node that serves", reboot_of_a_node_that_serves),
        ("a power cut with a container half removed", half_removed_container),
        ("a reboot in the tests", reboot_in_the_tests),
        ("a power cut in the tests", power_cut_in_the_tests),
        ("a power cut before apply", power_cut_before_apply),
        ("a power cut in apply", power_cut_in_apply),
        ("a power cut in the rollback", power_cut_in_the_rollback),
        ("a power cut late in the rollback", power_cut_late_in_the_rollback),
        ("a reboot in the rollback", reboot_in_the_rollback),
        ("a power cut after a commit", power_cut_after_a_commit),
    ]
    if MUTATE:
        keep = [*AFTER.get(MUTATE, ["a first deploy's tests fail, then a reboot", "a first deploy's tests fail, then a power cut"]), *NOTICE[MUTATE][0]]
        cases = [c for c in cases if c[0] in keep]
    rr.build_releases({"expand", "slow"})
    make_machine()
    power_on()
    up()
    on("mkdir -p /etc/docker && cat > /etc/docker/daemon.json", stdin=json.dumps({"insecure-registries": ["10.0.2.2:5000"]}).encode())
    print(on("uname -r; . /etc/os-release; echo $PRETTY_NAME")[1], flush=True)
    for name, fn in cases:
        rr.case(name, fn)
        if not rr.results[-1]["ok"]:
            with open(OUT / "journal.log", "a", encoding="utf-8") as f:
                f.write(f"=== {name}\n" + on("journalctl -b --no-pager -n 400", check=False)[1])
    if qemu:
        qemu.kill()
    results = rr.results
    (OUT / "results.json").write_text(json.dumps(results, indent=2))
    bad = [r["case"] for r in results if not r["ok"]]
    if os.environ.get("GITHUB_STEP_SUMMARY"):
        with open(os.environ["GITHUB_STEP_SUMMARY"], "a", encoding="utf-8") as f:
            f.write("| | case | time | |\n|---|---|---|---|\n" + "".join(
                f"| {'✅' if r['ok'] else '❌'} | {r['case']} | {r['sec']} s | {r['detail'].splitlines()[0][:200] if r['detail'] else ''} |\n" for r in results))
    print(f"\n{len(results) - len(bad)} of {len(results)} cases pass" + (f"; failed: {', '.join(bad)}" if bad else ""), flush=True)
    if MUTATE:
        names, says = NOTICE[MUTATE]
        noticed = {r["case"] for r in results if not r["ok"] and r["case"] in names and says in r["detail"]}
        if not set(AFTER.get(MUTATE, [])).isdisjoint(bad):
            noticed = set()  # not on a node that was not set up
        print(f"{MUTATE}: {'noticed by ' + ', '.join(sorted(noticed)) if noticed else 'NOT NOTICED'}", flush=True)
        return 0 if noticed else 1
    return 1 if bad else 0


if __name__ == "__main__":
    sys.exit(main())
