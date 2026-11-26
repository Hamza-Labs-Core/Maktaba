"""No macOS or Windows runner is held waiting for another job.

The Player's iPhone and iPad UI test shards used to start beside their build and ask the API
for its artifact for up to 30 minutes, each on a Mac. With many pull requests queued they had
the runners their own builds were queued for, and failed ("no build in 30 minutes") with no
test run. A job that uses another job's output `needs:` it: GitHub queues it once that job is
done, and a queued job holds no runner.
"""

from __future__ import annotations

import json
import re
import sys
from pathlib import Path
from typing import Any

import pytest
import yaml

ROOT = Path(__file__).resolve().parents[3]
WORKFLOWS = ROOT / ".github/workflows"
sys.path.insert(0, str(ROOT / "tools/ci"))
import ci_paths

DOCS: dict[str, dict[str, Any]] = {
    p.name: yaml.safe_load(p.read_text(encoding="utf-8")) for p in sorted(WORKFLOWS.glob("*.yml"))
}
APPLE = DOCS["_apple-apps.yml"]["jobs"]
DEVICES = ("iphone", "ipad")

# a runner there are few of: GitHub's macOS and Windows images, and the Xcode 27 one
SCARCE = re.compile(r"""["'](?:macos|windows|xcode)(?:-[^"']+)?["']""", re.IGNORECASE)
# what asks GitHub about runs, jobs, artifacts or checks, or about a release another job
# publishes; asking in a loop, or with a sleep, is waiting (asked once, for what an earlier run
# left, it holds nothing: nightly-perf.yml's last night's numbers)
ASKS = re.compile(
    r"/actions/(?:runs|jobs|artifacts|workflows)\b"
    r"|gh run (?:watch|view|list|download)\b"
    r"|gh pr checks\b"
    r"|/(?:check-runs|check-suites)\b"
    r"|gh release (?:view|list)\b"
    r"|github\.rest\.(?:actions|checks)\b"
)
AGAIN = re.compile(r"\b(?:while|until|for|sleep|setTimeout|Start-Sleep|do)\b|gh (?:run watch|pr checks .*--watch)")
# what waits for a check or an artifact by itself
WAITING_ACTION = re.compile(r"wait-(?:on|for)-(?:check|artifact|workflow|github)|wait-for-it", re.IGNORECASE)
SCRIPT_PATH = r"[\w./-]+\.(?:sh|ps1)"
SCRIPT = re.compile(rf"(?:tools|tests|deploy|web)/{SCRIPT_PATH}")
SCRIPT_RELATIVE = re.compile(rf"""(?:^|[\s"'=/])(\.\.?/{SCRIPT_PATH})""")
SCRIPT_SIBLING = re.compile(rf"(?:\$\{{?\w+\}}?|\$\(dirname\s+[^)]*\))/({SCRIPT_PATH})")


def _scarce(job: dict[str, Any]) -> bool:
    runs_on = json.dumps(job.get("runs-on", ""))
    if "matrix." in runs_on:
        runs_on += json.dumps((job.get("strategy") or {}).get("matrix"))
    return bool(SCARCE.search(runs_on))


def _scripts(text: str) -> list[str]:
    """What a step runs: its own text, and each of the repository's shell and PowerShell scripts
    it names (one run only to test itself asks nothing)."""
    root = ROOT.resolve()
    found = [(text, root)]
    visited: set[Path] = set()
    for source, base_dir in found:
        for line in source.splitlines():
            if "--self-test" in line or line.strip().startswith("#"):
                continue
            paths = [root / name for name in SCRIPT.findall(line)]
            paths.extend(base_dir / name for name in SCRIPT_RELATIVE.findall(line))
            paths.extend(base_dir / name for name in SCRIPT_SIBLING.findall(line))
            for candidate in paths:
                path = candidate.resolve()
                if path not in visited and path.is_relative_to(root) and path.is_file():
                    visited.add(path)
                    found.append((path.read_text(encoding="utf-8"), path.parent))
    return [source for source, _ in found]


def _asks(text: str) -> list[str]:
    """The lines of `text` (comments aside) that ask GitHub about another job, when it may ask
    more than once."""
    lines = [line.strip() for line in text.splitlines()]
    code = [line for line in lines if not line.startswith(("#", "//"))]
    if not any(AGAIN.search(line) for line in code):
        return []
    return [line for line in code if ASKS.search(line)]


def _waits(steps: list[dict[str, Any]]) -> list[str]:
    found = []
    for step in steps:
        uses = str(step.get("uses", ""))
        if step.get("if") == "runner.os == 'Linux'":
            continue  # a matrix's Linux leg alone
        if WAITING_ACTION.search(uses):
            found.append(uses)
        if uses.startswith("./.github/actions/"):
            action = yaml.safe_load((ROOT / uses / "action.yml").read_text(encoding="utf-8"))
            found += _waits(action["runs"].get("steps") or [])
        if uses.startswith("actions/github-script@"):
            found += _asks(str((step.get("with") or {}).get("script", "")))
        for text in _scripts(str(step.get("run", ""))):
            found += _asks(text)
    return found


SCARCE_JOBS = [
    pytest.param(job.get("steps") or [], id=f"{name}:{job_name}")
    for name, doc in DOCS.items()
    for job_name, job in (doc.get("jobs") or {}).items()
    if _scarce(job)
]


def test_the_scarce_jobs_are_found() -> None:
    ids = {p.id for p in SCARCE_JOBS}
    assert {"_apple-apps.yml:player-ui-tests-ipad", "testflight.yml:upload", "nightly-perf.yml:log-bench"} <= ids
    assert len(ids) >= 25
    assert all(_scarce(job) for job in APPLE.values())
    # by a matrix too
    assert any(i and i.startswith("desktop-release.yml:") for i in ids)
    assert not any(i and i.startswith("_tag-guard.yml:") for i in ids)  # it waits for CI, on Linux


@pytest.mark.parametrize("steps", SCARCE_JOBS)
def test_no_scarce_runner_waits_for_another_job(steps: list[dict[str, Any]]) -> None:
    assert _waits(steps) == []


def test_the_old_wait_is_caught() -> None:
    step = {
        "run": 'while :; do\n  found=$(gh api "repos/$GITHUB_REPOSITORY/actions/runs/$GITHUB_RUN_ID/artifacts?name=$name")\n'
        "  sleep 15\ndone\n"
    }
    assert _waits([step])
    assert _waits([{"run": "bash tools/release/tag-guard.sh"}])  # inside a script
    assert not _waits([{"run": "# gh run watch\npython3 tools/apple/snapshots.py --self-test"}])


@pytest.mark.parametrize("label", ["macos", "MacOS", "windows", "WINDOWS"])
def test_plain_self_hosted_labels_are_scarce(label: str) -> None:
    assert _scarce({"runs-on": ["self-hosted", label]})


def test_an_expression_can_select_a_scarce_label() -> None:
    assert _scarce({"runs-on": "${{ inputs.fast && 'macos-15' || 'ubuntu-latest' }}"})


@pytest.mark.parametrize(("wrapper_call", "inner_call"), [
    ("bash tools/release/sub/inner.sh", "bash tools/release/tag-guard.sh"),
    ('bash "$(dirname "$0")/sub/inner.sh"', "bash ../tag-guard.sh"),
    ("bash ./sub/inner.sh", "bash ../tag-guard.sh"),
    ('bash "${SCRIPT_DIR}/sub/inner.sh"', 'bash "$SCRIPT_DIR/../tag-guard.sh"'),
    ('bash "$here/sub/inner.sh"', '. "$(dirname "${BASH_SOURCE[0]}")/../tag-guard.sh"'),
])
def test_nested_scripts_are_scanned_once(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, wrapper_call: str, inner_call: str
) -> None:
    guard = ROOT / "tools/release/tag-guard.sh"
    release = tmp_path / "tools/release"
    (release / "sub").mkdir(parents=True)
    (release / guard.name).write_bytes(guard.read_bytes())
    (release / "wrapper.sh").write_text(f"{wrapper_call}\n", encoding="utf-8")
    (release / "sub/inner.sh").write_text(f"bash ../wrapper.sh\n{inner_call}\n", encoding="utf-8")
    alias = tmp_path.parent / f"{tmp_path.name}-alias"
    alias.symlink_to(tmp_path, target_is_directory=True)
    monkeypatch.setattr(sys.modules[__name__], "ROOT", alias)
    direct = _waits([{"run": "bash tools/release/tag-guard.sh"}])
    assert direct
    assert _waits([{"run": "bash tools/release/wrapper.sh"}]) == direct


# ---- the Player's UI tests: each device's shards after its own build ----


def _build_artifact(device: str) -> dict[str, Any]:
    (upload,) = [
        s for s in APPLE[f"player-ui-build-{device}"]["steps"] if str(s.get("uses", "")).startswith("actions/upload-artifact@")
    ]
    given: dict[str, Any] = upload["with"]
    return given


@pytest.mark.parametrize("device", DEVICES)
def test_a_shard_is_queued_once_its_own_devices_build_is_done(device: str) -> None:
    build, tests = APPLE[f"player-ui-build-{device}"], APPLE[f"player-ui-tests-{device}"]
    # its own device's build alone: a failed iPad build never skips an iPhone shard
    assert tests["needs"] == [f"player-ui-build-{device}"]
    assert [leg["device"] for leg in build["strategy"]["matrix"]["include"]] == [device]
    assert tests["strategy"]["matrix"]["device"] == [device]
    # no status function: a shard is not queued at all when its build did not pass
    assert build["if"] == tests["if"] == "inputs.player_ui"
    assert "needs" not in build
    assert "continue-on-error" not in build and "continue-on-error" not in tests


def test_both_devices_run_the_same_steps_and_shards() -> None:
    iphone, ipad = (APPLE[f"player-ui-tests-{d}"] for d in DEVICES)
    for key in ("name", "runs-on", "timeout-minutes", "env", "steps"):
        assert iphone[key] == ipad[key], key
    assert APPLE["player-ui-build-iphone"]["steps"] == APPLE["player-ui-build-ipad"]["steps"]
    for job in (iphone, ipad):
        assert job["strategy"]["fail-fast"] is False
        matrix = job["strategy"]["matrix"]
        assert "[\"1\", \"2\", \"3\", \"4\", \"5\", \"6\", \"shots\"]" in matrix["shard"]
        assert matrix["shards"] == [6]
    assert iphone["strategy"]["matrix"]["shard"] == ipad["strategy"]["matrix"]["shard"]


@pytest.mark.parametrize("device", DEVICES)
def test_a_shard_run_again_finds_the_build(device: str) -> None:
    # by its name in the run (not the attempt's), kept longer than a failure waits for a re-run
    given = _build_artifact(device)
    assert given["name"] == "player-ui-build-${{ matrix.device }}"
    assert given["if-no-files-found"] == "error"
    assert given["retention-days"] >= 7
    downloads = [
        s["with"] for s in APPLE[f"player-ui-tests-{device}"]["steps"] if str(s.get("uses", "")).startswith("actions/download-artifact@")
    ]
    assert len(downloads) == 3  # the first try and two more (test_artifact_download.py)
    assert all(d == {"name": given["name"], "path": "${{ runner.temp }}"} for d in downloads)


@pytest.mark.parametrize("device", DEVICES)
def test_the_job_names_are_the_ones_others_find_them_by(device: str) -> None:
    def named(job: str, **matrix: str) -> str:
        return re.sub(r"\$\{\{ matrix\.(\w+) \}\}", lambda m: matrix[m[1]], str(APPLE[job]["name"]))

    shard = named(f"player-ui-tests-{device}", device=device, shard="1", shards="6")
    build = named(f"player-ui-build-{device}", device=device)
    assert shard == f"Player UI tests ({device} 1/6)"
    assert build == f"Player UI tests build ({device})"
    # the tag guard lets a failed shard through, never a failed build
    guard = (ROOT / "tools/release/tag-guard.sh").read_text(encoding="utf-8")
    ignored = re.search(r"^IGNORED_CI_JOBS='(.*)'$", guard, re.MULTILINE)
    assert ignored
    assert re.search(ignored[1], f"apple-apps / {shard}")
    assert not re.search(ignored[1], f"apple-apps / {build}")
    # the website's screenshots read the shards' results
    shots = json.loads((ROOT / "web/packages/website/shots.config.json").read_text(encoding="utf-8"))
    assert any(f"Player UI tests ({device}" in json.dumps(app.get("job", ""), ensure_ascii=False) for app in shots["sources"].values())


# ---- a pull request's superseded run lets its runners go ----


def _reaches_scarce(name: str) -> bool:
    for job in (DOCS[name].get("jobs") or {}).values():
        uses = str(job.get("uses", ""))
        if _scarce(job) or (uses.startswith("./.github/workflows/") and _reaches_scarce(uses.rsplit("/", 1)[1])):
            return True
    return False


def test_a_new_push_cancels_a_pull_requests_older_run_on_scarce_runners() -> None:
    names = [n for n, doc in DOCS.items() if "pull_request" in (doc.get("on") or doc.get(True) or {}) and _reaches_scarce(n)]
    assert "ci.yml" in names
    event = {"github": {"event_name": "pull_request", "event": {"pull_request": {"number": 12}}}}
    for name in names:
        cancel = DOCS[name]["concurrency"]["cancel-in-progress"]
        assert (cancel if isinstance(cancel, bool) else ci_paths.evaluate(cancel, event)) is True, name
