"""Every Go job downloads its modules first, with retries, then reads the module cache only.

One proxy.golang.org stream error failed an integration job at setup before any test ran
(#628). Every job that sets up Go runs .github/actions/go-mod-download before its first
build, test, lint or `make`: three attempts per module directory, then an `::error::` naming
the module, then GOPROXY=off for the rest of the job (unless it is one of the few listed
below that must stay online). GOFLAGS is left alone.
"""

from __future__ import annotations

import re
from pathlib import Path
from typing import Any

import yaml

ROOT = Path(__file__).resolve().parents[3]
WORKFLOWS = sorted((ROOT / ".github/workflows").glob("*.yml"))
ACTION_PATH = ROOT / ".github/actions/go-mod-download/action.yml"
ACTION = "./.github/actions/go-mod-download"

# jobs that set up Go but build no module of this checkout
EXEMPT = {
    ("_tag-guard.yml", "guard"): "release-order builds from the checkout alone, with GOPROXY=off already",
    ("_node-packaging.yml", "installer-tests"): "tag-guard-test.sh runs offline on an empty module cache",
    ("release.yml", "package-nfpm"): "Go only to install nfpm",
}

# jobs that stay online after the download: tag, release and scheduled runs no pull
# request proves offline yet, and the jobs that build an earlier release's tree
ONLINE = {
    ("_integration.yml", "compat-prev"),
    ("relay.yml", "gate"),
    ("desktop-release.yml", "desktop"),
    ("desktop-release.yml", "publish"),
    ("packages.yml", "gate"),
    ("packages.yml", "homebrew"),
    ("release.yml", "build-artifacts"),
    ("release.yml", "build-server"),
    ("release.yml", "package-packs"),
    ("release.yml", "package-linux-payload"),
    ("release.yml", "sign-and-release"),
    ("release.yml", "package-linux-full"),
    ("trusted-root.yml", "refresh"),
    ("update-rehearsal.yml", "kit"),
}

USES_GO = re.compile(r"\bgo (build|test|run|vet|generate|list)\b|\bmake \w")


def _go_jobs() -> list[tuple[str, str, list[dict[str, Any]]]]:
    found = []
    for path in WORKFLOWS:
        doc = yaml.safe_load(path.read_text(encoding="utf-8")) or {}
        for name, job in (doc.get("jobs") or {}).items():
            steps = job.get("steps") or []
            if any(str(s.get("uses", "")).startswith("actions/setup-go@") for s in steps):
                found.append((path.name, name, steps))
    return found


def _download(steps: list[dict[str, Any]]) -> tuple[int, dict[str, Any]] | None:
    for i, s in enumerate(steps):
        if s.get("uses") == ACTION:
            return i, s
    return None


def test_there_are_go_jobs() -> None:
    assert len(_go_jobs()) >= 40


def test_exemptions_are_real_jobs() -> None:
    jobs = {(w, j) for w, j, _ in _go_jobs()}
    assert set(EXEMPT) <= jobs and ONLINE <= jobs


def test_every_go_job_downloads_its_modules_first() -> None:
    for workflow, job, steps in _go_jobs():
        where = f"{workflow} {job}"
        found = _download(steps)
        if (workflow, job) in EXEMPT:
            continue
        assert found, f"{where}: sets up Go but has no {ACTION} step"
        i, step = found
        setup = next(n for n, s in enumerate(steps) if str(s.get("uses", "")).startswith("actions/setup-go@"))
        assert setup < i, f"{where}: the download runs before setup-go"
        for s in steps[setup + 1 : i]:
            run = str(s.get("run", ""))
            building = USES_GO.search(run) or "golangci" in str(s.get("uses", ""))
            assert not building or re.search(r"go install \S+@", run), (
                f"{where}: {s.get('name') or run[:40]!r} builds before the modules downloaded"
            )


def test_the_modules_are_modules() -> None:
    for workflow, job, steps in _go_jobs():
        found = _download(steps)
        if not found:
            continue
        mods = str(found[1]["with"]["modules"]).split()
        assert mods, f"{workflow} {job}: no modules"
        if mods == ["all"]:
            continue
        for m in mods:
            assert (ROOT / m / "go.mod").is_file(), f"{workflow} {job}: {m} has no go.mod"
        # every go.sum the job's cache key names is a module it downloads
        for s in steps:
            if s.get("uses") == "./.github/actions/go-cache":
                for gosum in re.findall(r"'([^']+)/go\.sum'", s["with"]["go-sum-hash"]):
                    assert gosum in mods, f"{workflow} {job}: caches {gosum} but does not download it"


def test_offline_unless_listed() -> None:
    for workflow, job, steps in _go_jobs():
        found = _download(steps)
        if not found:
            continue
        i, step = found
        offline = str(step["with"].get("offline", "true")) == "true"
        assert offline == ((workflow, job) not in ONLINE), f"{workflow} {job}: offline is {offline}"
        if offline:
            for s in steps[i + 1 :]:
                assert not re.search(r"go install \S+@", str(s.get("run", ""))), (
                    f"{workflow} {job}: installs a tool after going offline"
                )


def test_the_action_retries_names_the_module_and_goes_offline() -> None:
    action = yaml.safe_load(ACTION_PATH.read_text(encoding="utf-8"))
    (step,) = action["runs"]["steps"]
    run = step["run"]
    assert "go mod download" in run
    assert '"${n}" -ge 3' in run and "sleep" in run, "three attempts with a wait between"
    assert "::error::" in run and "never downloaded after 3 attempts" in run and "${mod:+" in run
    assert 'echo "GOPROXY=off" >> "${GITHUB_ENV}"' in run
    assert "GOFLAGS" not in run
    assert action["inputs"]["offline"]["default"] == "true"
