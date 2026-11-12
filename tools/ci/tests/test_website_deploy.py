"""maktaba.tv's deploy (Epic 60 story 60.03, T08): website-deploy.yml goes live only when a
token (the website's own, else the shared CLOUDFLARE_API_TOKEN), the account and the go-live
variable are all set; otherwise the job prints a notice naming what is missing and passes.
WEBSITE_LIVE is the only switch, so nothing that writes may run without it; the read-only token
check runs as soon as a token and the account are set. The gate's own script is run here with
each mix.

Run: uv run --no-project --with pyyaml==6.0.3 --with pytest==8.4.2 pytest tools/ci/tests
"""

from __future__ import annotations

import json
import os
import re
import subprocess
from pathlib import Path

import pytest
import yaml

ROOT = Path(__file__).resolve().parents[3]
WORKFLOW = ROOT / ".github/workflows/website-deploy.yml"
GATED = "steps.gate.outputs.ok == 'true'"
CHECK = "steps.gate.outputs.check == 'true'"
TOKEN = "${{ secrets.CLOUDFLARE_API_TOKEN_WEBSITE || secrets.CLOUDFLARE_API_TOKEN }}"
# the steps that may run before the site is live: they read and write nothing on Cloudflare
READ_ONLY = {"actions/checkout@v6.0.2", "actions/setup-node@v6.4.0", "Token check", "Token check report"}
# the retries of the artifact download run only when the gated first attempt failed
RETRIES = re.compile(r"^steps\.artifact-website-site(-\d)?\.outcome == 'failure'$")


def workflow() -> dict:
    return yaml.safe_load(WORKFLOW.read_text())


def steps() -> list[dict]:
    return workflow()["jobs"]["deploy"]["steps"]


def test_no_pull_request_ever_deploys() -> None:
    on = workflow().get("on", workflow().get(True))
    assert set(on) == {"push", "workflow_dispatch", "schedule"}
    assert on["push"]["branches"] == ["main"]


def test_the_gate_reads_a_token_the_account_and_the_go_live_variable() -> None:
    gate = steps()[0]
    assert gate["id"] == "gate"
    assert gate["env"] == {
        "TOKEN": TOKEN,
        "ACCOUNT": "${{ secrets.CLOUDFLARE_ACCOUNT_ID }}",
        "LIVE": "${{ vars.WEBSITE_LIVE }}",
    }


def test_every_token_read_is_the_websites_own_else_the_shared_one() -> None:
    text = WORKFLOW.read_text()
    reads = re.findall(r"\$\{\{ secrets\.CLOUDFLARE_API_TOKEN[^}]*\}\}", text)
    assert reads and set(reads) == {TOKEN}


def name_of(step: dict) -> str:
    return step.get("name") or step.get("uses")


def test_nothing_that_writes_runs_without_the_go_live_variable() -> None:
    """WEBSITE_LIVE is the only switch: every step but the read-only ones (checkout, Node, the
    token check and its report) needs ok, which only WEBSITE_LIVE = true sets."""
    for step in steps()[1:]:
        cond = str(step.get("if", ""))
        name = name_of(step)
        if name in READ_ONLY:
            assert CHECK in cond, f"{name}: should run once a token and the account are set ({cond!r})"
            continue
        assert GATED in cond or RETRIES.match(cond), f"{name}: runs without WEBSITE_LIVE ({cond!r})"
    # every step that reads a secret is either the read-only token check or behind ok
    for step in steps()[1:]:
        if "secrets." in yaml.safe_dump(step):
            cond = str(step.get("if", ""))
            assert GATED in cond or name_of(step) == "Token check", name_of(step)


def test_the_token_check_is_read_only_and_does_not_fail_the_run_before_go_live() -> None:
    token = next(s for s in steps() if s.get("id") == "token")
    assert token["run"].count("token-check.mjs --pieces workers,dns --zone maktaba.tv") == 1
    assert "wrangler" not in token["run"] and "website-deploy.mjs" not in token["run"]
    assert token["continue-on-error"] == "${{ steps.gate.outputs.ok != 'true' }}"
    # the check itself only reads: every Cloudflare call in it is a GET
    src = (ROOT / "tools/cloudflare/token-check.mjs").read_text()
    calls = re.findall(r"\bcf\.(\w+)\(", src)
    assert calls and set(calls) <= {"get", "zoneId"}, calls


def run_gate(tmp_path: Path, **env: str) -> tuple[str, str]:
    out = tmp_path / "out"
    out.write_text("")
    script = steps()[0]["run"]
    full = {"PATH": os.environ["PATH"], "GITHUB_OUTPUT": str(out), **env}
    r = subprocess.run(["bash", "-e", "-c", script], env=full, capture_output=True, text=True, check=True)
    return r.stdout, out.read_text()


@pytest.mark.parametrize(
    "env,missing,check",
    [
        ({}, ["CLOUDFLARE_API_TOKEN", "CLOUDFLARE_ACCOUNT_ID", "WEBSITE_LIVE"], "false"),
        # the account's secrets as they are today: the token check runs, nothing is deployed
        ({"TOKEN": "t", "ACCOUNT": "a"}, ["WEBSITE_LIVE"], "true"),
        ({"TOKEN": "t", "ACCOUNT": "a", "LIVE": "false"}, ["WEBSITE_LIVE"], "true"),
        ({"TOKEN": "t", "ACCOUNT": "a", "LIVE": "TRUE"}, ["WEBSITE_LIVE"], "true"),
        ({"ACCOUNT": "a", "LIVE": "true"}, ["CLOUDFLARE_API_TOKEN"], "false"),
        ({"TOKEN": "t", "LIVE": "true"}, ["CLOUDFLARE_ACCOUNT_ID"], "false"),
    ],
)
def test_without_any_of_the_three_the_job_passes_with_a_notice_naming_it(
    tmp_path: Path, env: dict[str, str], missing: list[str], check: str
) -> None:
    stdout, out = run_gate(tmp_path, **env)
    assert out == f"check={check}\nok=false\n"
    assert "::notice title=maktaba.tv not deployed::" in stdout
    for name in missing:
        assert name in stdout
    for name in {"CLOUDFLARE_ACCOUNT_ID", "WEBSITE_LIVE"} - set(missing):
        assert name not in stdout.split("::notice", 1)[1]


def test_with_all_three_the_deploy_runs(tmp_path: Path) -> None:
    stdout, out = run_gate(tmp_path, TOKEN="t", ACCOUNT="acc", LIVE="true")
    assert out == "check=true\nok=true\n"
    assert "::add-mask::acc" in stdout
    assert "notice" not in stdout


def test_a_rollback_builds_nothing_and_still_passes_the_gate() -> None:
    jobs = workflow()["jobs"]
    assert jobs["build"]["if"] == "${{ !inputs.rollback }}"
    assert "inputs.rollback && needs.build.result == 'skipped'" in jobs["deploy"]["if"]
    names = {s.get("id"): s for s in steps()}
    assert "inputs.rollback" in names["rollback"]["if"]
    assert "!inputs.rollback" in names["deploy"]["if"]


def test_one_deploy_at_a_time_never_cancelled() -> None:
    assert workflow()["concurrency"] == {"group": "website-deploy", "cancel-in-progress": False}


# ---- story 60.04: a red nightly sweep blocks the deploy -----------------------------------

NIGHTLY = ROOT / ".github/workflows/website-nightly.yml"
STATE_SCRIPT = ROOT / "tools/ci/website_nightly_state.sh"


def nightly_gate() -> dict:
    return next(s for s in steps() if s.get("name") == "Nightly sweep gate")


def run_nightly_gate(state: str, failed: str = "[]") -> subprocess.CompletedProcess[str]:
    env = {"PATH": os.environ["PATH"], "STATE": state, "FAILED": failed, "RUN": "https://run/1"}
    return subprocess.run(["bash", "-e", "-c", nightly_gate()["run"]], env=env, capture_output=True, text=True)


def test_the_nightly_gate_sits_after_the_token_check_and_before_any_upload() -> None:
    names = [s.get("name") or s.get("uses") for s in steps()]
    gate = names.index("Nightly sweep gate")
    assert names.index("Token check") < gate < names.index("Download website-site")
    step = nightly_gate()
    assert GATED in step["if"] and "!inputs.rollback" in step["if"], "a rollback is never held back"
    assert step["env"]["STATE"] == "${{ needs.nightly.outputs.state }}"


@pytest.mark.parametrize("state", ["green", "none", "red"])
def test_the_gate_lets_a_green_absent_or_remeasured_sweep_through(state: str) -> None:
    r = run_nightly_gate(state, '["/install/qnap/"]' if state == "red" else "[]")
    assert r.returncode == 0, r.stdout + r.stderr
    if state == "red":
        assert "measured those pages again and they pass" in r.stdout


@pytest.mark.parametrize("state", ["unreadable", "", "something-else"])
def test_the_gate_refuses_a_red_sweep_it_cannot_read(state: str) -> None:
    r = run_nightly_gate(state)
    assert r.returncode == 1
    assert "::error title=maktaba.tv not deployed::" in r.stdout


def test_the_build_measures_the_pages_the_last_sweep_failed_and_the_deploy_needs_it() -> None:
    jobs = workflow()["jobs"]
    assert jobs["nightly"]["permissions"] == {"contents": "read", "actions": "read"}
    assert jobs["nightly"]["if"] == "${{ !inputs.rollback }}"
    assert jobs["build"]["needs"] == ["nightly"]
    assert jobs["build"]["with"] == {"lighthouse_extra": "${{ needs.nightly.outputs.failed }}"}
    assert jobs["deploy"]["needs"] == ["nightly", "build"]
    # the deploy goes on only when that build (with those pages) passed
    assert "needs.build.result == 'success'" in jobs["deploy"]["if"]


def fake_gh(tmp_path: Path, runs: dict | None, failures: str | None, api_status: int = 0) -> Path:
    """A `gh` that answers the runs API with `runs` (or an HTTP error) and `run download` with a
    failures.json of `failures` (None: the artifact is missing)."""
    bin_dir = tmp_path / "bin"
    bin_dir.mkdir()
    (tmp_path / "runs.json").write_text(json.dumps(runs or {}))
    if failures is not None:
        (tmp_path / "failures.json").write_text(failures)
    script = f"""#!/usr/bin/env bash
if [ "$1" = api ]; then
  if [ {api_status} = 404 ]; then echo "gh: Not Found (HTTP 404)" >&2; exit 1; fi
  if [ {api_status} != 0 ]; then echo "gh: Server Error (HTTP 500)" >&2; exit 1; fi
  cat "{tmp_path}/runs.json"; exit 0
fi
if [ "$1" = run ] && [ "$2" = download ]; then
  dir=""; while [ $# -gt 0 ]; do [ "$1" = -D ] && dir="$2"; shift; done
  [ -f "{tmp_path}/failures.json" ] || exit 1
  cp "{tmp_path}/failures.json" "$dir/failures.json"; exit 0
fi
exit 2
"""
    gh = bin_dir / "gh"
    gh.write_text(script)
    gh.chmod(0o755)
    return bin_dir


def run_state(tmp_path: Path, bin_dir: Path) -> tuple[int, dict[str, str], str]:
    out = tmp_path / "out"
    out.write_text("")
    env = {
        "PATH": f"{bin_dir}:{os.environ['PATH']}",
        "REPO": "o/r",
        "GITHUB_OUTPUT": str(out),
        "TMPDIR": str(tmp_path),
    }
    r = subprocess.run(["bash", str(STATE_SCRIPT)], env=env, capture_output=True, text=True)
    outputs = dict(line.split("=", 1) for line in out.read_text().splitlines() if "=" in line)
    return r.returncode, outputs, r.stdout + r.stderr


def runs_of(*conclusions: str) -> dict:
    return {
        "workflow_runs": [
            {"id": i + 1, "conclusion": c, "html_url": f"https://run/{i + 1}"} for i, c in enumerate(conclusions)
        ]
    }


def test_state_green_skips_cancelled_runs(tmp_path: Path) -> None:
    code, out, _ = run_state(tmp_path, fake_gh(tmp_path, runs_of("cancelled", "success", "failure"), None))
    assert code == 0
    assert out["state"] == "green" and out["run"] == "https://run/2"


def test_state_none_before_any_sweep_or_before_the_workflow_exists(tmp_path: Path) -> None:
    code, out, _ = run_state(tmp_path, fake_gh(tmp_path, runs_of("cancelled"), None))
    assert (code, out["state"]) == (0, "none")
    (tmp_path / "b").mkdir()
    code, out, _ = run_state(tmp_path / "b", fake_gh(tmp_path / "b", None, None, api_status=404))
    assert (code, out["state"]) == (0, "none")


def test_state_red_carries_the_failing_pages(tmp_path: Path) -> None:
    gh = fake_gh(tmp_path, runs_of("failure"), '["/install/qnap/", "/ar/"]')
    code, out, log = run_state(tmp_path, gh)
    assert code == 0
    assert out["state"] == "red"
    assert json.loads(out["failed"]) == ["/install/qnap/", "/ar/"]
    assert "/install/qnap/" in log


@pytest.mark.parametrize("failures", [None, "[]", "not json", '[{"x": 1}]', '["no-slash"]'])
def test_state_unreadable_when_a_red_sweep_has_no_readable_list(tmp_path: Path, failures: str | None) -> None:
    code, out, log = run_state(tmp_path, fake_gh(tmp_path, runs_of("timed_out"), failures))
    assert code == 0
    assert out["state"] == "unreadable" and out["failed"] == "[]"
    assert "::error" in log


def test_state_fails_on_an_api_error(tmp_path: Path) -> None:
    code, out, _ = run_state(tmp_path, fake_gh(tmp_path, None, None, api_status=500))
    assert code == 1 and "state" not in out


def test_the_nightly_sweeps_every_page_in_sixteen_bounded_shards() -> None:
    wf = yaml.safe_load(NIGHTLY.read_text())
    on = wf.get("on", wf.get(True))
    assert set(on) == {"schedule", "workflow_dispatch"}
    jobs = wf["jobs"]
    assert jobs["build"]["with"] == {"quality": False}
    sweep = jobs["sweep"]
    assert sweep["strategy"]["matrix"]["shard"] == list(range(1, 17))
    assert sweep["strategy"]["fail-fast"] is False
    assert sweep["timeout-minutes"] == 15
    run = next(s["run"] for s in sweep["steps"] if "lighthouse.mjs run" in str(s.get("run")))
    assert "--set all" in run and '--shard "$SHARD/16"' in run and "--remeasure 3" in run
    # the whole within 60 minutes: the build's 20, a shard's 15, the verdict's 5
    assert jobs["verdict"]["timeout-minutes"] <= 5
    uploads = [s["with"]["name"] for s in jobs["verdict"]["steps"] if "upload-artifact" in str(s.get("uses"))]
    assert uploads == ["website-nightly-failures"]
