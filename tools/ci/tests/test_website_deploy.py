"""maktaba.tv's deploy (Epic 60 story 60.03, T08): website-deploy.yml goes live only when the
website's own token, the account and the go-live variable are all set; otherwise the job prints
a notice naming what is missing and passes. The gate's own script is run here with each mix.

Run: uv run --no-project --with pyyaml==6.0.3 --with pytest==8.4.2 pytest tools/ci/tests
"""

from __future__ import annotations

import os
import re
import subprocess
from pathlib import Path

import pytest
import yaml

ROOT = Path(__file__).resolve().parents[3]
WORKFLOW = ROOT / ".github/workflows/website-deploy.yml"
GATED = "steps.gate.outputs.ok == 'true'"
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


def test_the_gate_reads_the_websites_own_token_the_account_and_the_go_live_variable() -> None:
    gate = steps()[0]
    assert gate["id"] == "gate"
    assert gate["env"] == {
        "TOKEN": "${{ secrets.CLOUDFLARE_API_TOKEN_WEBSITE }}",
        "ACCOUNT": "${{ secrets.CLOUDFLARE_ACCOUNT_ID }}",
        "LIVE": "${{ vars.WEBSITE_LIVE }}",
    }


def test_the_shared_cloudflare_tokens_are_never_read() -> None:
    text = WORKFLOW.read_text()
    tokens = set(re.findall(r"secrets\.(CLOUDFLARE_API_TOKEN\w*)", text))
    assert tokens == {"CLOUDFLARE_API_TOKEN_WEBSITE"}


def test_every_step_after_the_gate_runs_only_behind_it() -> None:
    for step in steps()[1:]:
        cond = str(step.get("if", ""))
        name = step.get("name") or step.get("uses")
        assert GATED in cond or RETRIES.match(cond), f"{name}: runs without the gate ({cond!r})"
    # and nothing reaches Cloudflare without it: every step that reads a secret is gated
    for step in steps()[1:]:
        if "secrets." in yaml.safe_dump(step):
            assert GATED in str(step.get("if", "")), step.get("name")


def run_gate(tmp_path: Path, **env: str) -> tuple[str, str]:
    out = tmp_path / "out"
    out.write_text("")
    script = steps()[0]["run"]
    full = {"PATH": os.environ["PATH"], "GITHUB_OUTPUT": str(out), **env}
    r = subprocess.run(["bash", "-e", "-c", script], env=full, capture_output=True, text=True, check=True)
    return r.stdout, out.read_text()


@pytest.mark.parametrize(
    "env,missing",
    [
        ({}, ["CLOUDFLARE_API_TOKEN_WEBSITE", "CLOUDFLARE_ACCOUNT_ID", "WEBSITE_LIVE"]),
        # the account's secrets as they are today: still not live without the variable
        ({"TOKEN": "t", "ACCOUNT": "a"}, ["WEBSITE_LIVE"]),
        ({"TOKEN": "t", "ACCOUNT": "a", "LIVE": "false"}, ["WEBSITE_LIVE"]),
        ({"ACCOUNT": "a", "LIVE": "true"}, ["CLOUDFLARE_API_TOKEN_WEBSITE"]),
    ],
)
def test_without_any_of_the_three_the_job_passes_with_a_notice_naming_it(
    tmp_path: Path, env: dict[str, str], missing: list[str]
) -> None:
    stdout, out = run_gate(tmp_path, **env)
    assert out == "ok=false\n"
    assert stdout.startswith("::notice title=maktaba.tv not deployed::")
    for name in missing:
        assert name in stdout
    for name in {"CLOUDFLARE_API_TOKEN_WEBSITE", "CLOUDFLARE_ACCOUNT_ID", "WEBSITE_LIVE"} - set(missing):
        assert name not in stdout


def test_with_all_three_the_deploy_runs(tmp_path: Path) -> None:
    stdout, out = run_gate(tmp_path, TOKEN="t", ACCOUNT="acc", LIVE="true")
    assert out == "ok=true\n"
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
