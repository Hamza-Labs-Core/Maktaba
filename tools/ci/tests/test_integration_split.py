"""The integration tier runs on three runners.

On one 4-vCPU runner the api module's tests (422-451 s of package time), cmd/maktaba-server's
(365-395 s), the other Go modules and the pipeline's `pytest -m integration` (120-147 s of a
220-268 s session collecting, CPU-starved) all ran at once, and the tier failed PRs on time
alone with every test passing. So CI's integration job runs the Go modules in two shards
(`make test-integration-go INTEGRATION_GO_SHARD=api|rest`, a matrix) and the pipeline job
runs the pytest half (`make test-integration-py`), each under a budget. `make
test-integration` still runs everything, for a local run.
"""

from __future__ import annotations

import os
import re
import subprocess
from pathlib import Path
from typing import Any

import yaml

ROOT = Path(__file__).resolve().parents[3]
WORKFLOW = ROOT / ".github/workflows/_integration.yml"


def _jobs() -> dict[str, Any]:
    doc = yaml.safe_load(WORKFLOW.read_text(encoding="utf-8"))
    jobs: dict[str, Any] = doc["jobs"]
    return jobs


def _runs(job: dict[str, Any]) -> list[str]:
    return [str(s.get("run", "")) for s in job.get("steps") or []]


def _dry_run(target: str, *args: str) -> str:
    env = {**os.environ, "MAKEFLAGS": ""}
    out = subprocess.run(
        ["make", "-n", "--no-print-directory", target, *args],
        cwd=ROOT,
        capture_output=True,
        text=True,
        env=env,
        check=True,
    )
    return out.stdout


def test_the_integration_job_runs_the_go_shards_and_no_pytest() -> None:
    job = _jobs()["integration"]
    assert job["strategy"]["matrix"]["shard"] == ["api", "rest"]
    assert job["strategy"]["fail-fast"] is False
    runs = _runs(job)
    assert any(
        r.startswith("make test-integration-go ")
        and 'INTEGRATION_GO_SHARD="$SHARD"' in r
        for r in runs
    )
    assert not any("pytest" in r or "make test-integration-py" in r for r in runs)
    assert not any(re.search(r"make test-integration(\s|$)", r) for r in runs)


def test_the_pipeline_job_runs_the_pytest_half_on_the_same_trigger() -> None:
    jobs = _jobs()
    pipeline = jobs["pipeline"]
    assert pipeline["if"] == jobs["integration"]["if"] == "inputs.integration"
    runs = _runs(pipeline)
    assert "make test-integration-py" in runs
    # the steps that moved with it: the ffmpeg file tests, the unmarked tests, the relay
    joined = "\n".join(runs)
    for part in (
        "tests/library_mgmt",
        "not unit and not integration and not e2e",
        "test_relay_two_process.py",
    ):
        assert part in joined
    # ffmpeg comes only after the tier, which runs without it
    names = [str(s.get("name", "")) for s in pipeline["steps"]]
    assert names.index("Run integration tier (pipeline)") < names.index(
        "Install ffmpeg"
    )


def _modules(shard: str) -> list[str]:
    out = _dry_run("test-integration-go-inner", f"INTEGRATION_GO_SHARD={shard}")
    return out.split("integration-go.sh", 1)[1].split()


def test_the_shards_cover_every_module_once() -> None:
    every = _modules("all")
    api, rest = _modules("api"), _modules("rest")
    assert api == ["api"]
    assert sorted(api + rest) == sorted(every)
    assert "cmd/maktaba-server" in rest and "streaming" in rest


def test_an_unknown_shard_fails() -> None:
    out = subprocess.run(
        [
            "make",
            "-n",
            "--no-print-directory",
            "test-integration-go-inner",
            "INTEGRATION_GO_SHARD=nope",
        ],
        cwd=ROOT,
        capture_output=True,
        text=True,
        env={**os.environ, "MAKEFLAGS": ""},
        check=False,
    )
    assert out.returncode != 0 and "INTEGRATION_GO_SHARD=nope" in out.stderr


def test_each_part_has_its_own_budget() -> None:
    go = _dry_run("test-integration-go", "INTEGRATION_GO_SHARD=api")
    py = _dry_run("test-integration-py")
    assert re.search(r"test-budget\.sh wall integration-go-api \S+ --", go)
    assert re.search(r"test-budget\.sh wall integration-py \S+ --", py)
    assert "test-integration-go-inner" in go and "test-integration-py-inner" in py


def test_the_pytest_half_lists_its_slowest_tests() -> None:
    py = _dry_run("test-integration-py-inner")
    assert "pytest -m integration --durations=25" in py
    # no tests matching the marker (exit 5) is no failure
    assert "[ $? -eq 5 ]" in py


def test_a_local_run_still_runs_both_halves() -> None:
    both = _dry_run("test-integration-inner")
    assert "test-integration-py-inner" in both and "test-integration-go-inner" in both
