"""The single cloud node's deploy (.github/workflows/cloud-deploy.yml, deploy/cloud-relay):
production only from main and only what QA passed, the key gone before another action runs, DNS
records judged before any is written, media taken from the relay alone. The rollout itself is
rehearsed for real in cloud-rehearsal.yml's single-node job."""

from __future__ import annotations

import importlib.util
import json
import os
import re
import subprocess
import sys
import threading
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path
from typing import Any
from urllib.parse import parse_qs, urlsplit

import pytest
import yaml

ROOT = Path(__file__).resolve().parents[3]
WF = ROOT / ".github/workflows/cloud-deploy.yml"
NODE = ROOT / "deploy/cloud-relay"


def wf() -> dict[str, Any]:
    return yaml.safe_load(WF.read_text())


def steps(job: str) -> list[dict[str, Any]]:
    return wf()["jobs"][job]["steps"]


def named(job: str, name: str) -> dict[str, Any]:
    return next(s for s in steps(job) if s.get("name", "").startswith(name))


# --- production


@pytest.mark.parametrize("job", ["resolve", "deploy"])
@pytest.mark.parametrize(
    ("target", "event", "ref", "ok"),
    [
        ("prod", "workflow_dispatch", "refs/heads/main", True),
        ("prod", "workflow_dispatch", "refs/heads/other", False),
        ("prod", "workflow_dispatch", "refs/tags/v1.2.3", False),
        ("prod", "push", "refs/heads/main", False),
        ("", "push", "refs/heads/scratch", False),
        ("production", "workflow_dispatch", "refs/heads/main", False),
    ],
)
def test_production_is_refused_anywhere_but_main(job: str, target: str, event: str, ref: str, ok: bool) -> None:
    assert _refusal(job, target, event, ref) is ok


def _refusal(job: str, target: str, event: str, ref: str, gate: str = "2", attempt: str = "2") -> bool:
    first = steps(job)[0]
    assert first["if"] == "inputs.target != 'qa'", "the refusal is the job's first step, for anything but qa"
    p = subprocess.run(
        ["bash", "-c", first["run"]],
        env={"PATH": os.environ["PATH"], "TARGET_INPUT": target, "GITHUB_EVENT_NAME": event, "GITHUB_REF": ref,
             "GATE_ATTEMPT": gate, "GITHUB_RUN_ATTEMPT": attempt},
        capture_output=True,
    )
    return p.returncode == 0


def test_a_deploy_job_run_again_on_its_own_asks_the_gate_again() -> None:
    assert _refusal("deploy", "prod", "workflow_dispatch", "refs/heads/main", gate="1", attempt="1")
    assert not _refusal("deploy", "prod", "workflow_dispatch", "refs/heads/main", gate="1", attempt="2")
    assert not _refusal("deploy", "prod", "workflow_dispatch", "refs/heads/main", gate="", attempt="1")
    assert steps("deploy")[0]["env"]["GATE_ATTEMPT"] == "${{ needs.resolve.outputs.attempt }}"
    assert wf()["jobs"]["resolve"]["outputs"]["attempt"] == "${{ github.run_attempt }}"


def test_the_deploy_job_itself_needs_main_for_production() -> None:
    assert "(inputs.target != 'prod' || github.ref == 'refs/heads/main')" in wf()["jobs"]["deploy"]["if"]
    assert wf()["jobs"]["deploy"]["environment"]["name"] == "${{ inputs.target || 'qa' }}"
    assert list(wf()[True]) == ["workflow_dispatch"], "dispatched by hand only"


def test_what_qa_records_is_what_production_compares() -> None:
    record = re.search(r"'\{(tag:.*?)\}'", named("record", "What QA passed")["run"], re.S)
    assert record
    keys = set(re.findall(r"(\w+): \$", record[1])) - {"tag", "run", "workflow_sha"}
    gate = named("resolve", "QA passed this release")["run"]
    assert keys - {"web_sha256"} == set(re.findall(r"\.(\w+) == \$", gate)) - {"run"} == {
        "cloud_ref", "server_ref", "caddy_ref", "postgres_ref", "redis_ref", "files"}
    assert "branch=main&event=workflow_dispatch" in gate and '.head_branch == "main"' in gate
    # by its commit too: a tag can be named main
    assert 'merge-base --is-ancestor "$sha" origin/main' in gate
    # the web bundle by what it is, recorded and checked
    assert "web_sha256" in keys and "web_sha256=$(jq -r .web_sha256" in gate
    web = named("deploy", "The cloud web app's bundle")
    assert "cloud-web-$WEB_SHA256" in web["run"] and "sha256sum -c" in web["run"]
    assert "needs.resolve.outputs.web_sha256" in web["env"]["WEB_SHA256"]
    # and main still has the files: an old run started again puts none back
    assert 'git rev-parse "origin/main:$f"' in named("resolve", "The deploy's files")["run"]
    files = named("resolve", "The deploy's files")["run"]
    for f in (".github/workflows/cloud-deploy.yml", "tools/release/cloud-guard.sh", "deploy/cloud-relay"):
        assert f in files and (ROOT / f).exists()
    assert "GITHUB_WORKFLOW_SHA" in files
    assert (NODE / "tests/e2e.py").exists(), "the test is under the hashed folder"


def test_every_image_of_the_stack_is_pinned_or_a_release_by_digest() -> None:
    compose = yaml.safe_load((NODE / "docker-compose.yml").read_text())
    for name, svc in compose["services"].items():
        image = svc["image"]
        if name in ("postgres", "redis"):
            assert re.fullmatch(r"\w+:[\w.-]+@sha256:[0-9a-f]{64}", image), image
        else:
            assert re.fullmatch(r"\$\{MAKTABA_(CLOUD|CADDY)_IMAGE:-ghcr\.io/hamza-labs-core/[\w-]+\}:\$\{MAKTABA_(CLOUD|CADDY)_TAG:-[\w.-]+\}", image), image


def test_actions_are_pinned_to_commits() -> None:
    for line in WF.read_text().splitlines():
        m = re.search(r"uses:\s*(\S+)@(\S+)(.*)$", line)
        if m:
            assert re.fullmatch(r"[0-9a-f]{40}", m[2]) and re.search(r"#\s*v\d", m[3]), line


def test_the_key_is_gone_before_another_action_runs() -> None:
    names = [s.get("name") or s.get("uses", "") for s in steps("deploy")]
    forget = names.index("Forget the key")
    after_ssh = [s for s in steps("deploy")[names.index("SSH to the node (the pinned host key)") :]]
    for i, s in enumerate(steps("deploy")):
        if "uses" in s and s in after_ssh:
            assert i > forget, f"{s['uses']} runs while the key is on the runner"
    assert steps("deploy")[forget]["if"] == "always()"


def test_a_failure_after_begin_rolls_back_and_a_pass_commits() -> None:
    names = [s.get("name", "") for s in steps("deploy")]
    order = ["Begin (ops/rollout.sh)", "Set up the node (setup.sh, idempotent)", "The cloud web app's bundle (QA's build)",
             "Deploy (deploy.sh)", "Test (deploy/cloud-relay/tests/e2e.py)", "End the test accounts",
             "Commit (the node's set is this one)", "Roll back (what was before this deploy)", "Forget the key"]
    assert [names.index(n) for n in order] == sorted(names.index(n) for n in order)
    for name, action in (("Deploy (deploy.sh)", "apply"), ("Commit", "commit"), ("Roll back", "rollback")):
        assert f"bash deploy/cloud-relay/deploy.sh {action}" in named("deploy", name)["run"], name
    assert wf()["jobs"]["deploy"]["env"]["DEPLOY_ID"] == "${{ github.run_id }}-${{ github.run_attempt }}"
    for name in ("Set up the node", "The cloud web app's bundle"):
        assert "'$DEPLOY_ID'" in named("deploy", name)["run"], name
    back = named("deploy", "Roll back")
    assert back["if"] == "(failure() || cancelled()) && steps.begin.outcome != 'skipped' && steps.commit.outcome != 'success'"
    assert "if" not in named("deploy", "Commit"), "only after everything before it passed"
    assert named("deploy", "End the test accounts")["if"] == "always() && steps.begin.outcome == 'success'"


def test_no_other_workflow_deploys_the_single_node() -> None:
    for f in (ROOT / ".github/workflows").glob("*.yml"):
        if f.name == "cloud-deploy.yml":
            continue
        runs = [s.get("run", "") for j in (yaml.safe_load(f.read_text()).get("jobs") or {}).values() for s in (j.get("steps") or [])]
        assert not [r for r in runs if re.search(r"(?m)\bbash\s+deploy/cloud-relay/deploy\.sh|^\s*(\./)?deploy/cloud-relay/deploy\.sh|make\s+relay-deploy", r)], f.name
    relay = yaml.safe_load((ROOT / ".github/workflows/relay.yml").read_text())
    for job in ("gate", "deploy"):
        assert "vars.CLOUD_HA == 'true'" in relay["jobs"][job]["if"], job
    assert "refs/tags/v" in relay["jobs"]["deploy"]["if"]
    assert "relay-deploy" not in (ROOT / "Makefile").read_text()


# --- the node's DNS records


class FakeCloudflare(BaseHTTPRequestHandler):
    records: list[dict[str, Any]] = []
    writes: list[tuple[str, str, dict[str, Any]]] = []

    def _send(self, result: Any) -> None:
        b = json.dumps({"success": True, "result": result}).encode()
        self.send_response(200)
        self.end_headers()
        self.wfile.write(b)

    def do_GET(self) -> None:
        u = urlsplit(self.path)
        q = parse_qs(u.query)
        if u.path.endswith("/dns_records"):
            self._send([r for r in self.records if r["name"] == q["name"][0]])
        else:
            self._send([{"id": "z1"}])

    def _write(self) -> None:
        body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        self.writes.append((self.command, self.path, body))
        self._send(body)

    do_POST = do_PATCH = _write

    def log_message(self, *a: Any) -> None:
        pass


def dns(records: list[dict[str, Any]], *names: str) -> tuple[int, str, list[tuple[str, str, dict[str, Any]]]]:
    FakeCloudflare.records, FakeCloudflare.writes = records, []
    srv = HTTPServer(("127.0.0.1", 0), FakeCloudflare)
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    try:
        p = subprocess.run(
            [sys.executable, str(NODE / "ops/dns-records.py"), "example.test", "192.0.2.9", *names],
            env={"PATH": os.environ["PATH"], "CF_API_TOKEN": "t0ken", "CF_API_BASE": f"http://127.0.0.1:{srv.server_port}"},
            capture_output=True, text=True,
        )
    finally:
        srv.shutdown()
        srv.server_close()
    assert "t0ken" not in p.stdout + p.stderr
    return p.returncode, p.stdout + p.stderr, FakeCloudflare.writes


def a(name: str, ip: str = "192.0.2.9", **kw: Any) -> dict[str, Any]:
    return {"id": "r-" + name, "type": "A", "name": name, "content": ip, "ttl": 300, "proxied": False, **kw}


def test_dns_writes_and_leaves_alone() -> None:
    code, out, writes = dns(
        [a("same.example.test"), a("moved.example.test", "192.0.2.1"), a("proxied.example.test", proxied=True),
         {"id": "t", "type": "TXT", "name": "new.example.test", "content": "x"}],
        "same.example.test", "moved.example.test", "proxied.example.test", "new.example.test")
    assert code == 0, out
    assert [(m, b["name"], b["content"], b["proxied"]) for m, _, b in writes] == [
        ("PATCH", "moved.example.test", "192.0.2.9", False), ("PATCH", "proxied.example.test", "192.0.2.9", False),
        ("POST", "new.example.test", "192.0.2.9", False)]


@pytest.mark.parametrize("kind", ["AAAA", "CNAME", "HTTPS", "SVCB", "NS"])
def test_dns_refuses_a_record_that_sends_clients_elsewhere_before_any_write(kind: str) -> None:
    code, out, writes = dns([{"id": "x", "type": kind, "name": "last.example.test", "content": "y"}],
                            "new.example.test", "last.example.test")
    assert code != 0 and kind in out and "last.example.test" in out
    assert writes == []


def test_dns_refuses_two_addresses_and_another_zone_before_any_write() -> None:
    assert dns([a("two.example.test"), a("two.example.test", "192.0.2.1")], "new.example.test", "two.example.test")[::2] == (1, [])
    assert dns([], "new.example.test", "elsewhere.test")[::2] == (1, [])


# --- the media test


@pytest.fixture
def e2e(monkeypatch: pytest.MonkeyPatch) -> Any:
    for k, v in {"RELAY_DOMAIN": "relay.example.test", "API_DOMAIN": "api.example.test", "APP_DOMAIN": "app.example.test",
                 "NODE_IP": "192.0.2.9", "HOME_IMAGE": "x", "NODE_SSH": "true", "RUN_ID": "42"}.items():
        monkeypatch.setenv(k, v)
    spec = importlib.util.spec_from_file_location("e2e", NODE / "tests/e2e.py")
    assert spec and spec.loader
    m = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(m)
    return m


def test_media_is_taken_from_the_relay_alone(e2e: Any) -> None:
    home = "https://e2e-42.relay.example.test"
    assert e2e.media_url("/stream/1/direct?sig=x") == home + "/stream/1/direct?sig=x"
    assert e2e.media_url(home + "/stream/1") == home + "/stream/1"
    for bad in ("https://cdn.example.test/x", "//cdn.example.test/x", "http://e2e-42.relay.example.test/x",
                "https://e2e-42.relay.example.test.evil.test/x", "https://e2e-42.relay.example.test@evil.test/x",
                "https://e2e-42.relay.example.test:444/x", "https://other.relay.example.test/x"):
        with pytest.raises(AssertionError, match="leaves the relay"):
            e2e.media_url(bad)
    with pytest.raises(AssertionError, match="redirects"):
        e2e.SameOrigin().redirect_request(None, None, 302, "", {}, "https://cdn.example.test/x")


def test_a_run_that_leaves_something_behind_fails(e2e: Any, monkeypatch: pytest.MonkeyPatch) -> None:
    acct = e2e.Account()
    monkeypatch.setattr(e2e, "http", lambda *a, **k: (404, None))
    for left, ok in (({"left_active": 0, "left_homes": 0}, True), ({"left_active": 1, "left_homes": 0}, False),
                     ({"left_active": 0, "left_homes": 1}, False)):
        monkeypatch.setattr(e2e, "node", lambda cmd, **k: json.dumps({"ended": 1, "homes_removed": 1, **left}))
        e2e.results.clear()
        assert e2e.check("ended", lambda: e2e.check_ended(acct)) is ok
    # the owner's own deletion failed: the node ended the account, and the run still fails
    monkeypatch.setattr(e2e, "node", lambda cmd, **k: json.dumps({"ended": 1, "homes_removed": 1, "left_active": 0, "left_homes": 0}))
    acct.token = "t"
    e2e.results.clear()
    assert e2e.check("ended", lambda: e2e.check_ended(acct)) is False
    assert "deletion failed" in e2e.results[0]["detail"] or "sign-in" in e2e.results[0]["detail"]


# --- the rehearsal


def test_every_mutation_is_run_and_names_cases_that_exist() -> None:
    text = (NODE / "tests/rollout_rehearsal.py").read_text()
    table = text[text.index("MUTATIONS: dict") : text.index("def mutate(")]
    names = re.findall(r'^    "([\w-]+)": \("ops/', table, re.M)
    cases = set(re.findall(r'^        \("([^"]+)", ', text, re.M))
    noticed = {c for group in re.findall(r"\[((?:\"[^\"]+\"(?:, )?)+)\]\)", table) for c in re.findall(r'"([^"]+)"', group)}
    assert len(names) >= 45 and noticed <= cases, noticed - cases
    run = yaml.safe_load((ROOT / ".github/workflows/cloud-rehearsal.yml").read_text())["jobs"]
    assert run["single-node"]["strategy"]["matrix"]["part"] == ["1", "2", "3", "4"]
    assert {"single-node", "single-node-reboots"} <= set(run["cloud-rehearsal"]["needs"])
    assert 'for j in image scenario single-node single-node-reboots; do' in run["cloud-rehearsal"]["steps"][0]["run"]
    # the mutations are no part of the workflow a deploy waits for
    assert "MUTATE" not in (ROOT / ".github/workflows/cloud-rehearsal.yml").read_text()
    mutations = yaml.safe_load((ROOT / ".github/workflows/cloud-rehearsal-mutations.yml").read_text())["jobs"]
    # (a mutation that names no case here is the machine's: reboot_rehearsal.py)
    machine = re.findall(r'^    "([\w-]+)": \("ops/.*, \[\]\),$', table, re.M)
    assert mutations["mutation"]["strategy"]["matrix"]["mutation"] == [n for n in names if n not in machine]
    run_there = mutations["reboot-mutation"]["strategy"]["matrix"]["mutation"]
    assert set(machine) <= set(run_there) <= set(names)
    notice = (NODE / "tests/reboot_rehearsal.py").read_text()
    assert all(f'"{n}": ([' in notice for n in run_there)


ADDRESS, ADDRESS_KEY = "192.0.2.77", "AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl"


def run_step(step: dict[str, Any], tmp: Path, **env: str) -> tuple[int, str, str]:
    """A step's script as the runner runs it: (exit, output, what it wrote to GITHUB_ENV)."""
    (tmp / "github_env").write_text("")
    home = tmp / "home"
    home.mkdir(exist_ok=True)
    p = subprocess.run(["bash", "-e", "-c", step["run"]], cwd=ROOT, capture_output=True, text=True,
                       env={"PATH": os.environ["PATH"], "HOME": str(home), "GITHUB_ENV": str(tmp / "github_env"), "RUNNER_TEMP": str(tmp), **env})
    return p.returncode, p.stdout + p.stderr, (tmp / "github_env").read_text()


def test_the_target_step_needs_both_secrets_and_prints_neither(tmp_path: Path) -> None:
    # a step's env: is printed before the step runs, so a mask of its own comes too late: the address and
    # the host key's line are the target environment's secrets, masked everywhere
    step = named("deploy", "The target")
    assert step["env"]["NODE_HOST"] == "${{ secrets.NODE_HOST }}"
    assert step["env"]["NODE_KNOWN_HOSTS"] == "${{ secrets.NODE_KNOWN_HOSTS }}"
    both = {"NODE_HOST": ADDRESS, "NODE_KNOWN_HOSTS": f"{ADDRESS} ssh-ed25519 {ADDRESS_KEY}"}
    rest = {"TARGET": "qa", "HAS_DNS_TOKEN": "true", "CLOUD_TLS": ""}
    code, out, env = run_step(step, tmp_path, **both, **rest)
    assert code == 0 and out == "node: qa, certificates: dns\n", out
    assert f"NODE_IP={ADDRESS}\n" in env and (tmp_path / "home/.ssh/known_hosts_node").read_text().strip() == both["NODE_KNOWN_HOSTS"]
    for missing in ("NODE_HOST", "NODE_KNOWN_HOSTS"):
        code, out, env = run_step(step, tmp_path, **{**both, missing: ""}, **rest)
        assert code != 0 and "the qa environment's" in out and "NODE_IP" not in env, (missing, out)


def test_the_address_is_read_in_one_place() -> None:
    text = (ROOT / ".github/workflows/cloud-deploy.yml").read_text()
    assert text.count("secrets.NODE_HOST ") == 1 and text.count("secrets.NODE_KNOWN_HOSTS ") == 1
    assert set(re.findall(r"secrets\.(NODE_\w+)", text)) == {"NODE_HOST", "NODE_KNOWN_HOSTS"}
    # no repository variable but the one that says how certificates are got: a variable is printed
    assert set(re.findall(r"vars\.(\w+)", text)) == {"CLOUD_TLS"} and "vars[" not in text and "toJSON(" not in text
    for name, job in wf()["jobs"].items():
        assert not any("NODE_" in k or "NODE_" in str(v) for k, v in (job.get("env") or {}).items()), name
    uses = {s.get("name") for s in steps("deploy") if "NODE_IP" in str(s)}
    assert uses == {"The target", "SSH to the node (the pinned host key)", "The node's DNS records (with the DNS token)",
                    "Deploy (deploy.sh)", "Commit (the node's set is this one)", "Roll back (what was before this deploy)",
                    "The node's address out of what is uploaded"}
    assert "set -x" not in text


def test_no_uploaded_file_names_the_node(tmp_path: Path) -> None:
    step = named("deploy", "The node's address out of what is uploaded")
    order = [s.get("name") or s.get("uses", "").split("@")[0] for s in steps("deploy")]
    upload = next(s for s in steps("deploy") if s.get("with", {}).get("path") == "${{ runner.temp }}/e2e")
    assert order.index(step["name"]) + 1 == steps("deploy").index(upload)
    assert step["if"] == "always()" and step["id"] == "cleaned"
    assert upload["if"] == "always() && steps.cleaned.outcome == 'success'"
    ssh = tmp_path / "home/.ssh"
    ssh.mkdir(parents=True)
    (ssh / "known_hosts_node").write_text(f"{ADDRESS} ssh-ed25519 {ADDRESS_KEY}\n")
    finger = subprocess.run(["ssh-keygen", "-l", "-f", str(ssh / "known_hosts_node")], capture_output=True, text=True, check=True).stdout.split()[1]
    e2e = tmp_path / "e2e"
    (e2e / "deep").mkdir(parents=True)
    said = f"ssh: connect to host {ADDRESS} port 22: refused\nthe fingerprint is {finger}.\nkey {ADDRESS_KEY}\nkept 198.51.100.4\n"
    (e2e / "node-compose.log").write_text(said)
    (e2e / "deep/results.json").write_bytes(said.encode() + b"\x00\xff")
    code, out, _ = run_step(step, tmp_path, NODE_IP=ADDRESS)
    assert code == 0, out
    for f in ("node-compose.log", "deep/results.json"):
        got = (e2e / f).read_bytes()
        assert not any(w.encode() in got for w in (ADDRESS, ADDRESS_KEY, finger)) and b"kept 198.51.100.4" in got, got
    assert (e2e / "deep/results.json").read_bytes().endswith(b"\x00\xff")
    # nothing to take out: the run ended before it knew the node
    (ssh / "known_hosts_node").unlink()
    code, out, _ = run_step(step, tmp_path)
    assert code == 0 and b"kept" in (e2e / "node-compose.log").read_bytes(), out


def test_every_mutation_still_finds_what_it_takes_out(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("REPO", str(ROOT))
    spec = importlib.util.spec_from_file_location("rollout_rehearsal", NODE / "tests/rollout_rehearsal.py")
    assert spec and spec.loader
    m = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(m)
    for name in m.MUTATIONS:
        src = tmp_path / name
        (src / "ops").mkdir(parents=True)
        for f in ("rollout.sh", "backup.sh"):
            (src / "ops" / f).write_text((NODE / "ops" / f).read_text())
        m.MUTATE = name
        m.mutate(src)  # it fails when nothing is taken out, or what is left does not parse
        changed = m.MUTATIONS[name][0]
        assert (src / changed).read_text() != (NODE / changed).read_text(), name


# --- mail


def target_env(name: str) -> dict[str, str]:
    return dict(line.split("=", 1) for line in (NODE / f"targets/{name}.env").read_text().splitlines() if re.match(r"[A-Z_]+=", line))


def mail_block(tmp: Path, env: dict[str, str], dotenv: str = "", drivers: str = "log\\nresend",
               siteverify: str = """echo '{"success":false,"error-codes":["invalid-input-response"]}'""") -> tuple[int, str, dict[str, Any]]:
    """setup.sh's mail block, then its cloud.toml: (exit, what it said, [mail]). The release's image
    says drivers; "" is one from before the command (it exits 2)."""
    import tomllib

    text = (NODE / "setup.sh").read_text()
    block = text[text.index("# --- mail: begin") : text.index("# --- mail: end")]
    helpers = text[text.index("# get_env KEY") : text.index("# is_placeholder VALUE")]
    loop = re.search(r"^for k in RELAY_DOMAIN .*?^done$.*?^for k in MAIL_ALLOW .*?^done$", text, re.S | re.M)
    (tmp / "bin").mkdir(exist_ok=True)
    answer = drivers if drivers.startswith("exit") else f'printf "%b\\n" "{drivers}"' if drivers else "echo '--role required' >&2; exit 2"
    (tmp / "bin/docker").write_text(f'#!/bin/sh\necho "$*" >> {tmp}/docker.said\n[ "$1" = run ] || exit 0\n{answer}\n')
    # siteverify: what it is given on stdin is kept, with the command line it must not be on
    (tmp / "bin/curl").write_text(f'#!/bin/sh\necho "$*" > {tmp}/curl.args\ncat > {tmp}/curl.stdin\n{siteverify}\n')
    (tmp / "bin/timeout").write_text('#!/bin/sh\nshift 3\nexec "$@"\n')
    for f in ("docker", "timeout", "curl"):
        (tmp / "bin" / f).chmod(0o755)
    render = re.search(r"^sed -e .*?cloud\.toml\.new\"$", text, re.S | re.M)
    assert loop and render
    (tmp / "secrets").mkdir(exist_ok=True)
    (tmp / "cloud.toml.template").write_text((NODE / "cloud.toml.template").read_text())
    if dotenv or not (tmp / ".env").exists():
        (tmp / ".env").write_text(dotenv)
    script = "\n".join([
        "set -euo pipefail", f"DEPLOY_DIR={tmp}", 'ENV_FILE="$DEPLOY_DIR/.env"',
        'log() { echo "$*"; }', 'warn() { echo "!! $*" >&2; }', 'die() { echo "xx $*" >&2; exit 1; }',
        helpers, loop[0], block, 'RELAY_PLANS_ENFORCE_V=false', render[0], 'mv "$DEPLOY_DIR/cloud.toml.new" "$DEPLOY_DIR/cloud.toml"'])
    p = subprocess.run(["bash", "-c", script], env={"PATH": f"{tmp}/bin:{os.environ['PATH']}", **env}, capture_output=True, text=True)
    said = p.stdout + p.stderr
    toml = tomllib.loads((tmp / "cloud.toml").read_text())["mail"] if p.returncode == 0 else {}
    return p.returncode, said, toml


KEY = "re_9f_a-key.that/is+none"


def test_a_node_with_the_key_sends_mail_to_whom_its_target_says(tmp_path: Path) -> None:
    for target, allow, prefix in (("qa", ["@maktaba.tv", "delivered@resend.dev", "bounced@resend.dev"], "[QA]"), ("prod", [], "")):
        tmp = tmp_path / target
        tmp.mkdir()
        t = target_env(target)
        mail_env = {k: v for k, v in t.items() if k.startswith("MAIL_")}
        code, said, toml = mail_block(tmp, {"RESEND_API_KEY": KEY, **mail_env})
        assert code == 0, said
        assert toml["driver"] == "resend" and toml["from"] == "Maktaba <no-reply@maktaba.tv>" and toml["reply_to"] == "support@maktaba.tv"
        assert toml["allow_recipients"] == allow and toml["subject_prefix"] == prefix
        key = tmp / "secrets/resend.key"
        assert key.read_text() == KEY and key.stat().st_mode & 0o777 == 0o600
        # nowhere else, and never said
        assert KEY not in said + (tmp / ".env").read_text() + (tmp / "cloud.toml").read_text()
        assert "resend" in said and ("everyone" in said) == (target == "prod")
        # a setup that names no key keeps the node's; one that says there is none takes it away
        assert mail_block(tmp, {})[2]["driver"] == "resend"
        code, said, toml = mail_block(tmp, {"RESEND_API_KEY": ""})
        assert code == 0 and toml["driver"] == "log" and not key.exists() and "no RESEND_API_KEY" in said


def test_the_driver_is_for_a_release_that_knows_it(tmp_path: Path) -> None:
    given = {"RESEND_API_KEY": KEY, "MAIL_ALLOW": "everyone", "MAKTABA_CLOUD_TAG": "v9.9.9@sha256:abc"}
    for drivers in ("", "log", "log\\nresender"):
        code, said, toml = mail_block(tmp_path, given, drivers=drivers)
        assert code == 0 and toml["driver"] == "log" and "no resend driver" in said, drivers
    # an image that could not be asked is no answer: nothing goes on, and what it said is told
    for rc in (1, 124, 125, 127):
        code, said, _ = mail_block(tmp_path, given, drivers=f"exit {rc}")
        assert code != 0 and f"could not be asked for its mail drivers ({rc})" in said and KEY not in said, rc
    # and is not asked while there is no key or no list
    assert mail_block(tmp_path, {"RESEND_API_KEY": "", "MAIL_ALLOW": "everyone"}, drivers="exit 1")[0] == 0
    # the key is there for the release that does
    code, said, toml = mail_block(tmp_path, {"RESEND_API_KEY": KEY})
    assert code == 0 and toml["driver"] == "resend"
    asked = (tmp_path / "docker.said").read_text().splitlines()[-1]
    assert asked == "run --rm --pull never --network none ghcr.io/hamza-labs-core/maktaba-cloud:v9.9.9@sha256:abc mail drivers"


def test_the_bot_check_is_on_with_both_its_keys_and_says_so(tmp_path: Path) -> None:
    import tomllib

    both = {"TURNSTILE_SITE_KEY": "0x4AAAAAAA_site-key", "TURNSTILE_SECRET_KEY": "0x4AAAAAAA_the-secret"}
    for env, drivers, on in ((both, "log\\nresend", True), ({**both, "TURNSTILE_SECRET_KEY": ""}, "log\\nresend", False),
                             ({**both, "TURNSTILE_SITE_KEY": ""}, "log\\nresend", False), ({}, "log\\nresend", False), (both, "", False)):
        tmp = tmp_path / str(len(list(tmp_path.iterdir())))
        tmp.mkdir()
        code, said, _ = mail_block(tmp, env, drivers=drivers)
        assert code == 0, said
        toml = tomllib.loads((tmp / "cloud.toml").read_text())
        assert toml["turnstile"]["enabled"] is on and toml["turnstile"]["staff"] is False, env
        assert ("bot check: on" in said) is on and ("it is off" in said) is not on
        # the secret is .env's, which compose hands the api alone; never said, never in cloud.toml
        assert "the-secret" not in said + (tmp / "cloud.toml").read_text()
    compose = yaml.safe_load((NODE / "docker-compose.yml").read_text())["services"]
    assert [n for n, svc in compose.items() if "MAKTABA_CLOUD_TURNSTILE_SECRET" in (svc.get("environment") or {})] == ["api"]
    # the secret is asked of Cloudflare once, on stdin; a bad one fails the deploy, saying which
    tmp = tmp_path / "asked"
    tmp.mkdir()
    bad_secret = """echo '{"success":false,"error-codes":["invalid-input-secret"]}'"""
    for siteverify, ok, words in ((bad_secret, False, "is not a secret Cloudflare knows"), ("echo '{\"success\":true}'", True, "bot check: on"),
                                  ("exit 28", True, "could not be asked"), ("echo '<html>'", True, "could not be asked")):
        code, said, _ = mail_block(tmp, both, siteverify=siteverify)
        assert (code == 0) is ok and words in said and "the-secret" not in said, siteverify
        assert "the-secret" in (tmp / "curl.stdin").read_text() and "the-secret" not in (tmp / "curl.args").read_text()
    (tmp / "curl.args").unlink()
    mail_block(tmp, {"TURNSTILE_SITE_KEY": "", "TURNSTILE_SECRET_KEY": ""})
    assert not (tmp / "curl.args").exists(), "not asked without the keys"
    # taken out of the environment, they are taken off the node
    tmp = tmp_path / "off"
    tmp.mkdir()
    mail_block(tmp, both)
    mail_block(tmp, {"TURNSTILE_SITE_KEY": "", "TURNSTILE_SECRET_KEY": ""})
    assert "the-secret" not in (tmp / ".env").read_text()
    for bad in ({**both, "TURNSTILE_SITE_KEY": 'x"'}, {**both, "TURNSTILE_SECRET_KEY": "a|b"}):
        assert mail_block(tmp, bad)[0] != 0


def test_a_mail_setting_taken_out_of_the_target_does_not_stay(tmp_path: Path) -> None:
    given = {"RESEND_API_KEY": KEY, "MAIL_ALLOW": "@maktaba.tv", "MAIL_SUBJECT_PREFIX": "[QA]", "MAIL_DAILY_LIMIT": "10", "MAIL_MONTHLY_LIMIT": "200"}
    assert mail_block(tmp_path, given)[2]["subject_prefix"] == "[QA]"
    code, _, toml = mail_block(tmp_path, {"MAIL_ALLOW": "@maktaba.tv", "MAIL_SUBJECT_PREFIX": "", "MAIL_DAILY_LIMIT": "", "MAIL_MONTHLY_LIMIT": ""})
    assert code == 0 and toml["subject_prefix"] == "" and toml["daily_limit"] == 80 and toml["driver"] == "resend"
    code, said, toml = mail_block(tmp_path, {"MAIL_ALLOW": ""})
    assert code == 0 and toml["driver"] == "log" and "MAIL_ALLOW is not set" in said


def test_a_node_without_the_key_or_a_list_keeps_the_log_and_says_so(tmp_path: Path) -> None:
    code, said, toml = mail_block(tmp_path, {"MAIL_ALLOW": "everyone"})
    assert code == 0 and toml["driver"] == "log" and "driver is log" in said
    assert toml["daily_limit"] > 0 and toml["monthly_limit"] > 0, "the template reads without any MAIL_ setting"
    (tmp_path / ".env").write_text("")
    code, said, toml = mail_block(tmp_path, {"RESEND_API_KEY": KEY})
    assert code == 0 and toml["driver"] == "log" and "MAIL_ALLOW is not set" in said


@pytest.mark.parametrize("bad", [{"MAIL_ALLOW": 'a@b.test"] #'}, {"MAIL_ALLOW": "a|b"}, {"MAIL_ALLOW": ","}, {"MAIL_ALLOW": "@maktaba.tv,"}, {"MAIL_ALLOW": "@maktaba.tv,everyone"},
                                 {"MAIL_ALLOW": "@"}, {"MAIL_ALLOW": "*"}, {"RESEND_API_KEY": "sk_live_1"}, {"MAIL_SUBJECT_PREFIX": "x|y"},
                                 {"MAIL_SUBJECT_PREFIX": "a&b"}, {"MAIL_DAILY_LIMIT": "0"}, {"MAIL_MONTHLY_LIMIT": "lots"}])
def test_a_setting_that_is_none_is_refused(tmp_path: Path, bad: dict[str, str]) -> None:
    code, said, _ = mail_block(tmp_path, {"RESEND_API_KEY": KEY, "MAIL_ALLOW": "everyone", **bad})
    assert code != 0 and re.search("MAIL_|RESEND_API_KEY is not", said) and KEY not in said and "sk_live" not in said


def test_qa_mails_no_stranger_and_both_targets_fit_the_plan() -> None:
    qa, prod = target_env("qa"), target_env("prod")
    assert qa["MAIL_ALLOW"] and "everyone" not in qa["MAIL_ALLOW"]
    for e in qa["MAIL_ALLOW"].split(","):
        assert e.endswith(("@maktaba.tv", "@resend.dev")) and e != "@resend.dev", e
    assert prod["MAIL_ALLOW"] == "everyone"
    # one provider account: 100 mails a day, 3,000 a month (docs/cloud-mail.md)
    assert int(qa["MAIL_DAILY_LIMIT"]) + int(prod["MAIL_DAILY_LIMIT"]) <= 95, "room for the dashboard's own"
    assert int(qa["MAIL_MONTHLY_LIMIT"]) + int(prod["MAIL_MONTHLY_LIMIT"]) <= 2900


def test_the_mail_key_is_the_environments_and_goes_on_stdin_only() -> None:
    users = []
    for name, job in wf()["jobs"].items():
        for s in job["steps"]:
            if "secrets.RESEND_API_KEY" in json.dumps(s):
                users.append((name, s["name"], job.get("environment")))
    assert [u[:2] for u in users] == [("deploy", "Set up the node (setup.sh, idempotent)")]
    assert users[0][2], "the job is in the target's environment, whose own secret it is"
    step = named("deploy", "Set up the node")
    assert step["env"]["RESEND_API_KEY"] == "${{ secrets.RESEND_API_KEY }}"
    run = step["run"]
    assert run.count("RESEND_API_KEY:-") == 2
    for k in ("RESEND_API_KEY", "TURNSTILE_SECRET_KEY", "TURNSTILE_SITE_KEY"):
        assert step["env"][k] == "${{ secrets.%s }}" % k and f'"${{{k}:-}}"' in run[: run.index("ssh node")], k
    # on the node it is read from stdin: the remote command holds only the variable's name
    remote = run[run.index('ssh node "') :]
    assert "$RESEND_API_KEY" not in remote and "$TURNSTILE_SECRET_KEY" not in remote and 'export RESEND_API_KEY=\\"\\$m\\"' in remote
    for k in ("MAIL_ALLOW", "MAIL_SUBJECT_PREFIX", "MAIL_DAILY_LIMIT", "MAIL_MONTHLY_LIMIT"):
        assert k in remote, k


def test_the_remote_setup_hands_the_key_over_whole(tmp_path: Path) -> None:
    """The step itself, with an ssh that runs the remote command here and a setup that says what it got."""
    (tmp_path / "ssh").write_text('#!/bin/sh\nshift\nexec bash -c "$1"\n')
    (tmp_path / "ssh").chmod(0o755)
    (tmp_path / "rollout").write_text('#!/bin/sh\nprintf "%s|" "$RESEND_API_KEY" "${CF_API_TOKEN-unset}" "$MAIL_ALLOW" "$MAIL_SUBJECT_PREFIX" "$MAIL_DAILY_LIMIT" "$TURNSTILE_SECRET_KEY" "$TURNSTILE_SITE_KEY"\n')
    (tmp_path / "rollout").chmod(0o755)
    run = named("deploy", "Set up the node")["run"].replace("/usr/local/sbin/maktaba-rollout", str(tmp_path / "rollout"))
    for target in ("qa", "prod"):
        t = target_env(target)
        for key in (KEY, ""):
            p = subprocess.run(["bash", "-c", run], capture_output=True, text=True, env={
                "PATH": f"{tmp_path}:{os.environ['PATH']}", "TARGET": target, "DEPLOY_ID": "1", "RELAY_TLS": "dns", "CLOUD_REF": "c", "CADDY_REF": "c",
                "RESEND_API_KEY": key, "TURNSTILE_SECRET_KEY": key and "0xsecret", "TURNSTILE_SITE_KEY": "0xsite", **t})
            assert p.returncode == 0, p.stderr
            said = p.stdout.splitlines()[-1]
            assert said.endswith(f"{key}|unset|{t['MAIL_ALLOW']}|{t.get('MAIL_SUBJECT_PREFIX', '')}|{t['MAIL_DAILY_LIMIT']}|{key and '0xsecret'}|0xsite|"), said
            level = "::warning::" if target == "prod" else "::notice::"
            for missing in ("has no RESEND_API_KEY", "has not both TURNSTILE_SECRET_KEY and TURNSTILE_SITE_KEY"):
                assert (f"{level}the {target} environment {missing}" in p.stdout) == (key == ""), missing


def test_e2e_refuses_a_qa_that_could_mail_a_stranger(e2e: Any) -> None:
    sends = {"driver": "resend", "delivers": True}
    assert "listed" in e2e.mail_status({"mail": {**sends, "recipients": "listed"}}, True)
    assert "not delivered" in e2e.mail_status({"mail": {"driver": "log", "delivers": False, "recipients": "all"}}, True)
    assert e2e.mail_status({"status": "ready"}, True) == "mail not stated"
    assert "all" in e2e.mail_status({"mail": {**sends, "recipients": "all"}}, False)
    with pytest.raises(AssertionError, match="stranger"):
        e2e.mail_status({"mail": {**sends, "recipients": "all"}}, True)


# --- the operator's reset link


def ops_tool(tmp: Path, args: list[str], stdin: str, tty: bool) -> tuple[int, str, str]:
    """cloud-ops.py with a docker that keeps what psql is given: (exit, what it printed, the SQL)."""
    import pty

    (tmp / "bin").mkdir(exist_ok=True)
    (tmp / ".env").write_text("APP_DOMAIN=app.example.test\n")
    (tmp / "bin/docker").write_text(f'#!/bin/sh\necho "$*" >> {tmp}/docker.args\ncat > {tmp}/sql\n[ -e {tmp}/none ] || echo some-id\n')
    (tmp / "bin/docker").chmod(0o755)
    script = f"import os, runpy, sys; os.geteuid = lambda: 0; sys.argv = {['cloud-ops.py', *args]!r}; runpy.run_path({str(NODE / 'ops/cloud-ops.py')!r}, run_name='__main__')"
    env = {"PATH": f"{tmp}/bin:{os.environ['PATH']}", "DEPLOY_DIR": str(tmp)}
    master, slave = pty.openpty()
    p = subprocess.run([sys.executable, "-c", script], input=stdin.encode(), env=env, stdout=slave if tty else subprocess.PIPE, stderr=subprocess.PIPE)
    # read while the slave is open: closing it may drop what the terminal holds
    said = os.read(master, 4096).decode() if tty and p.returncode == 0 else (p.stdout or b"").decode()
    os.close(slave)
    os.close(master)
    return p.returncode, said + p.stderr.decode(), (tmp / "sql").read_text() if (tmp / "sql").exists() else ""


def test_the_operators_reset_link_is_shown_once_on_a_terminal_and_audited(tmp_path: Path) -> None:
    import hashlib

    code, said, sql = ops_tool(tmp_path, ["reset-link"], "owner@example.test\n", tty=True)
    assert code == 0, said
    link = re.search(r"https://app\.example\.test/reset-password\?token=([A-Za-z0-9_-]{43})", said)
    assert link
    # only its SHA-256 goes to the database, as the cloud keeps one, with its audit row in the same transaction
    assert hashlib.sha256(link[1].encode()).hexdigest() in sql and link[1] not in sql + (tmp_path / "docker.args").read_text()
    assert sql.index("BEGIN;") < sql.index("email_verifications") < sql.index("audit_log") < sql.index("COMMIT;")
    assert "'auth.password_reset_link'" in sql and "status = 'active'" in sql and "'1 hour'" in sql
    assert "owner@example.test" not in (tmp_path / "docker.args").read_text()
    # a pipe, a file or a CI run gets none, and nothing is made
    (tmp_path / "sql").unlink()
    code, said, sql = ops_tool(tmp_path, ["reset-link"], "owner@example.test\n", tty=False)
    assert code != 0 and "terminal only" in said and "token=" not in said and not sql
    # no such account: no link
    (tmp_path / "none").touch()
    code, said, _ = ops_tool(tmp_path, ["reset-link"], "nobody@example.test\n", tty=True)
    assert code != 0 and "no active account" in said and "token=" not in said


def test_the_reset_links_hash_is_the_clouds() -> None:
    go = (ROOT / "cloud/internal/stores/tokens.go").read_text()
    assert "sha256.Sum256([]byte(raw))" in go and "hex.EncodeToString(sum[:])" in go
    assert re.search(r'PurposePasswordReset\s+= "password_reset"', go)
