"""One Go cache entry per runner OS, arch and Go version, each written by one job on main.

Each Go job used to key its cache on its own go.sum files and its runner image, and one
push to main wrote some 20 setup-go entries of 0.2-0.8 GB (7.9 GB of the repository's
10 GB), so small pinned tools were evicted within the hour. Now every job restores through
.github/actions/go-cache, keyed on every go.sum of the checkout, and only the writers below
save, after tools/ci/go-cache-fill.sh puts every module of the checkout in the cache.
"""

from __future__ import annotations

import os
import re
import stat
import subprocess
from pathlib import Path
from typing import Any

import yaml

ROOT = Path(__file__).resolve().parents[3]
WORKFLOWS = sorted((ROOT / ".github/workflows").glob("*.yml"))
ACTION = ROOT / ".github/actions/go-cache/action.yml"
FILL = ROOT / "tools/ci/go-cache-fill.sh"
MAIN = "github.event_name == 'push' && github.ref == 'refs/heads/main'"

# (workflow, job) -> the runner it writes for, as an extra condition on its matrix
WRITERS = {
    ("_unit.yml", "unit"): "",  # Linux x64, Go 1.26 (api)
    ("_launcher.yml", "launcher"): " && runner.os == 'Linux'",  # Linux x64, Go 1.25.8
    ("_linux-app.yml", "payload"): " && runner.arch == 'ARM64'",  # Linux arm64
    ("_win-app.yml", "win-app"): "",  # Windows x64
    ("_mac-app.yml", "mac-app"): "",  # macOS arm64
}


def _jobs() -> list[tuple[str, str, list[dict[str, Any]]]]:
    found = []
    for path in WORKFLOWS:
        doc = yaml.safe_load(path.read_text(encoding="utf-8")) or {}
        for name, job in (doc.get("jobs") or {}).items():
            found.append((path.name, name, job.get("steps") or []))
    return found


def test_setup_go_never_caches_itself() -> None:
    for workflow, job, steps in _jobs():
        for s in steps:
            if str(s.get("uses", "")).startswith("actions/setup-go@"):
                w = s.get("with") or {}
                assert w.get("cache") is False, f"{workflow} {job}: setup-go without cache: false"
                assert "cache-dependency-path" not in w, f"{workflow} {job}: setup-go cache-dependency-path"


def test_the_key_is_the_runner_and_go_version_and_every_go_sum() -> None:
    action = yaml.safe_load(ACTION.read_text(encoding="utf-8"))
    assert "inputs" not in action, "go-cache takes no per-job go.sum list"
    key_step = action["runs"]["steps"][0]
    assert key_step["env"]["HASH"] == "${{ hashFiles('**/go.sum', '!**/node_modules/**') }}"
    run = key_step["run"]
    assert 'key=setup-go-${RUNNER_OS}-${arch}-go-${version#go}-${HASH}' in run
    assert "ImageOS" not in run, "the runner image is not in the key"
    restore = action["runs"]["steps"][1]
    assert restore["uses"] == "actions/cache/restore@v4"
    assert "restore-keys" in restore["with"]


def test_only_the_writers_save_and_they_fill_first() -> None:
    savers = set()
    for workflow, job, steps in _jobs():
        names = [s.get("name") for s in steps]
        save = [s for s in steps if s.get("name") == "Save the Go caches"]
        if (workflow, job) not in WRITERS:
            assert not save, f"{workflow} {job}: saves a Go cache entry but is not one of the writers"
            assert "Every module in the Go cache" not in names
            continue
        savers.add((workflow, job))
        assert len(save) == 1, f"{workflow} {job}: one save step"
        i = names.index("Save the Go caches")
        fill = steps[i - 1]
        assert fill.get("name") == "Every module in the Go cache", f"{workflow} {job}: fill before the save"
        assert fill["run"] == "tools/ci/go-cache-fill.sh"
        cond = (
            f"{MAIN}{WRITERS[(workflow, job)]} && steps.go-cache.outputs.key != '' "
            "&& steps.go-cache.outputs.cache-hit != 'true'"
        )
        assert fill["if"] == cond and save[0]["if"] == cond, f"{workflow} {job}: {save[0]['if']}"
        assert save[0]["with"] == {
            "path": "${{ steps.go-cache.outputs.path }}",
            "key": "${{ steps.go-cache.outputs.key }}",
        }
    assert savers == set(WRITERS)


def test_every_go_cache_step_is_the_shared_action_with_no_inputs() -> None:
    n = 0
    for workflow, job, steps in _jobs():
        for s in steps:
            if s.get("uses") == "./.github/actions/go-cache":
                n += 1
                assert "with" not in s, f"{workflow} {job}: go-cache takes no inputs"
                if (workflow, job) in WRITERS:
                    assert s.get("id") == "go-cache", f"{workflow} {job}: the step id the save reads"
    assert n >= 40


# --- tools/ci/go-cache-fill.sh against a fake go -------------------------------------------


def _fake_go(tmp: Path, version: str, fail: str = "") -> Path:
    """A `go` that reports VERSION, logs `go mod download` per directory, and fails in FAIL."""
    bin_dir = tmp / "bin"
    bin_dir.mkdir()
    go = bin_dir / "go"
    go.write_text(
        "#!/usr/bin/env bash\n"
        f'if [ "$1 $2" = "env GOVERSION" ]; then echo go{version}; exit 0; fi\n'
        'if [ "$1 $2" = "mod download" ]; then\n'
        '  echo "$(basename "$PWD") $GOPROXY $GOTOOLCHAIN" >> "$FAKE_GO_LOG"\n'
        f'  [ "$(basename "$PWD")" != "{fail}" ] || exit 1\n'
        "  exit 0\n"
        "fi\n"
        "exit 2\n",
        encoding="utf-8",
    )
    go.chmod(go.stat().st_mode | stat.S_IXUSR)
    return bin_dir


def _repo(tmp: Path, modules: dict[str, str]) -> Path:
    repo = tmp / "repo"
    repo.mkdir()
    subprocess.run(["git", "init", "-q"], cwd=repo, check=True)
    for name, version in modules.items():
        (repo / name).mkdir(parents=True)
        (repo / name / "go.mod").write_text(f"module example.com/{name}\n\ngo {version}\n", encoding="utf-8")
    subprocess.run(["git", "add", "-A"], cwd=repo, check=True)
    return repo


def _fill(tmp: Path, repo: Path, version: str, fail: str = "", goproxy: str = "off") -> tuple[str, list[str]]:
    log = tmp / "go.log"
    env = dict(os.environ)
    env.update(
        PATH=f"{_fake_go(tmp, version, fail)}{os.pathsep}{env['PATH']}",
        FAKE_GO_LOG=str(log),
        GO_CACHE_FILL_DELAYS="0 0",
        GOPROXY=goproxy,
    )
    r = subprocess.run(["bash", str(FILL), str(repo)], env=env, capture_output=True, text=True, check=True)
    calls = log.read_text(encoding="utf-8").splitlines() if log.exists() else []
    return r.stdout, calls


def test_fill_downloads_every_module_online_with_the_local_go(tmp_path: Path) -> None:
    repo = _repo(tmp_path, {"api": "1.26.0", "server": "1.25.8", "old": "1.23"})
    out, calls = _fill(tmp_path, repo, "1.26.0")
    assert sorted(calls) == [f"{m} https://proxy.golang.org,direct local" for m in ("api", "old", "server")]
    assert "3 modules in the cache, 0 for a newer Go, 0 failed (Go 1.26.0)" in out


def test_fill_skips_a_module_that_wants_a_newer_go(tmp_path: Path) -> None:
    repo = _repo(tmp_path, {"api": "1.26.0", "server": "1.25.8", "tools": "1.25"})
    out, calls = _fill(tmp_path, repo, "1.25.8")
    assert sorted(c.split()[0] for c in calls) == ["server", "tools"]
    assert "api wants Go 1.26.0 (running 1.25.8), skipped" in out


def test_fill_keeps_a_set_goproxy(tmp_path: Path) -> None:
    repo = _repo(tmp_path, {"api": "1.26.0"})
    _, calls = _fill(tmp_path, repo, "1.26.0", goproxy="https://proxy.example")
    assert calls == ["api https://proxy.example local"]


def test_fill_retries_then_warns_and_goes_on(tmp_path: Path) -> None:
    repo = _repo(tmp_path, {"api": "1.26.0", "server": "1.25.8"})
    out, calls = _fill(tmp_path, repo, "1.26.0", fail="api")
    assert [c.split()[0] for c in calls].count("api") == 3, "three tries"
    assert "server" in [c.split()[0] for c in calls]
    assert re.search(r"::warning::go-cache-fill: the modules of api did not download", out)
    assert "1 modules in the cache, 0 for a newer Go, 1 failed" in out
