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
    assert len(names) >= 39 and noticed <= cases, noticed - cases
    run = yaml.safe_load((ROOT / ".github/workflows/cloud-rehearsal.yml").read_text())["jobs"]
    assert run["single-node"]["strategy"]["matrix"]["part"] == ["1", "2", "3"]
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
