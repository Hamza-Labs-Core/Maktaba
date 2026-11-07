"""auto-tag.yml's choice (tools/release/next_tag.py): the next patch tag, never on a trail-only
change, never twice on one commit, never on a commit older than a tagged one; and the workflows
it dispatches are every tag workflow, each dispatchable on its tag."""

from __future__ import annotations

import os
import subprocess
import sys
from pathlib import Path
from typing import Any

import pytest
import yaml

ROOT = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(ROOT / "tools" / "release"))

import next_tag as nt  # noqa: E402

WORKFLOWS = ROOT / ".github" / "workflows"


def load(name: str) -> dict[Any, Any]:
    return yaml.safe_load((WORKFLOWS / name).read_text(encoding="utf-8"))


def on(doc: dict[Any, Any]) -> dict[str, Any]:
    # PyYAML reads the bare key `on` as True
    return doc.get("on", doc.get(True)) or {}


# --- the version ------------------------------------------------------------------------------


def test_next_patch_is_the_highest_plus_one() -> None:
    assert nt.next_patch(["v0.3.9", "v0.3.82", "v0.3.10", "v0.2.99"]) == "v0.3.83"


def test_next_patch_counts_no_pre_release() -> None:
    assert nt.next_patch(["v0.3.0-rc.7", "v0.2.4", "v0.3.0-rc.9", "vnext"]) == "v0.2.5"


def test_next_patch_needs_a_tag() -> None:
    with pytest.raises(ValueError):
        nt.next_patch(["v1.0.0-rc.1"])


# --- the trail --------------------------------------------------------------------------------


@pytest.mark.parametrize(
    ("path", "only"),
    [
        ("docs/claude-trail.md", True),
        ("docs/trail/events/0001-x.md", True),
        ("docs/trail/current.md", True),
        ("docs/trailer.md", False),
        ("docs/updates.md", False),
        ("docs/claude-trail.md.bak", False),
        ("api/main.go", False),
    ],
)
def test_trail_only(path: str, only: bool) -> None:
    assert nt.trail_only(path) is only


@pytest.mark.parametrize("workflow", ["ci.yml", "update-rehearsal.yml"])
def test_trail_paths_are_the_workflows_paths_ignore(workflow: str) -> None:
    assert tuple(on(load(workflow))["push"]["paths-ignore"]) == nt.TRAIL_ONLY


# --- the decision (a linear main: a <- b <- c <- d) --------------------------------------------

ORDER = ["a", "b", "c", "d"]


def ancestor(x: str, y: str) -> bool:
    return ORDER.index(x) <= ORDER.index(y)


def changes(paths: dict[tuple[str, str], list[str]]):
    return lambda x, y: paths.get((x, y), ["api/main.go"])


def test_the_next_patch_on_a_code_change() -> None:
    d = nt.decide("c", {"v0.3.82": "a", "v0.3.0-rc.1": "a"}, ancestor, changes({}))
    assert (d.tag, d.sha) == ("v0.3.83", "c")


def test_a_trail_only_change_is_not_tagged() -> None:
    trail = {("a", "c"): ["docs/trail/events/0700-x.md", "docs/claude-trail.md"]}
    d = nt.decide("c", {"v0.3.82": "a"}, ancestor, changes(trail))
    assert d.tag == "" and "trail" in d.reason


def test_trail_and_code_is_tagged() -> None:
    mixed = {("a", "c"): ["docs/trail/events/0700-x.md", "web/src/app.ts"]}
    assert nt.decide("c", {"v0.3.82": "a"}, ancestor, changes(mixed)).tag == "v0.3.83"


def test_no_change_is_not_tagged() -> None:
    assert nt.decide("c", {"v0.3.82": "a"}, ancestor, changes({("a", "c"): []})).tag == ""


def test_a_tagged_commit_is_not_tagged_again() -> None:
    d = nt.decide("c", {"v0.3.82": "a", "v0.3.83": "c"}, ancestor, changes({}))
    assert d.tag == "" and "tagged already" in d.reason


def test_an_older_commit_is_not_tagged_after_a_newer_one() -> None:
    d = nt.decide("b", {"v0.3.82": "a", "v0.3.83": "c"}, ancestor, changes({}))
    assert d.tag == "" and "newer" in d.reason


def test_a_newer_pre_release_also_stops_an_older_commit() -> None:
    assert nt.decide("b", {"v0.3.82": "a", "v0.3.83-rc.1": "c"}, ancestor, changes({})).tag == ""


def test_a_commit_without_the_highest_tag_is_not_tagged() -> None:
    def beside(x: str, y: str) -> bool:  # "z" is on a side line: neither has the other
        return x == y or ("z" not in (x, y) and ancestor(x, y))

    d = nt.decide("z", {"v0.3.82": "c"}, beside, changes({}))
    assert d.tag == "" and "does not contain" in d.reason


def test_newest_green_is_main_order() -> None:
    newest_first = list(reversed(ORDER))
    assert nt.newest_green(newest_first, {"b", "c"}) == "c"
    assert nt.newest_green(newest_first, {"x"}) is None


# --- against a real repository ----------------------------------------------------------------


def run(cwd: Path, *args: str) -> str:
    env = {
        **os.environ,
        "GIT_AUTHOR_NAME": "t",
        "GIT_AUTHOR_EMAIL": "t@example.com",
        "GIT_COMMITTER_NAME": "t",
        "GIT_COMMITTER_EMAIL": "t@example.com",
        "GIT_CONFIG_GLOBAL": os.devnull,
        "GIT_CONFIG_NOSYSTEM": "1",
    }
    return subprocess.run(["git", *args], cwd=cwd, env=env, check=True, capture_output=True, text=True).stdout.strip()


def commit(repo: Path, path: str) -> str:
    f = repo / path
    f.parent.mkdir(parents=True, exist_ok=True)
    f.write_text(f.read_text() + "x\n" if f.exists() else "x\n")
    run(repo, "add", "-A")
    run(repo, "commit", "-q", "-m", path)
    return run(repo, "rev-parse", "HEAD")


def choose(repo: Path, green: list[str], monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]) -> dict[str, str]:
    (repo / ".green").write_text("\n".join(green) + "\n")
    monkeypatch.chdir(repo)
    assert nt.main(["--green", str(repo / ".green"), "--main", "HEAD"]) == 0
    return dict(line.split("=", 1) for line in capsys.readouterr().out.splitlines())


def test_in_a_repository(tmp_path: Path, monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]) -> None:
    repo = tmp_path / "r"
    repo.mkdir()
    run(repo, "init", "-q", "-b", "main")
    a = commit(repo, "api/main.go")
    run(repo, "tag", "-a", "v0.3.82", "-m", "v0.3.82", a)
    run(repo, "tag", "v0.3.0", a)  # a lightweight tag counts too
    t = commit(repo, "docs/trail/events/0001-x.md")
    assert choose(repo, [t], monkeypatch, capsys)["tag"] == ""

    c = commit(repo, "web/app.ts")
    out = choose(repo, [a, t, c], monkeypatch, capsys)
    assert (out["tag"], out["sha"]) == ("v0.3.83", c)

    run(repo, "tag", "-a", "v0.3.83", "-m", "v0.3.83", c)
    assert choose(repo, [c], monkeypatch, capsys)["tag"] == ""
    assert choose(repo, [], monkeypatch, capsys)["tag"] == ""


# --- the workflows it dispatches --------------------------------------------------------------


def tag_workflows() -> set[str]:
    out = set()
    for f in sorted(WORKFLOWS.glob("*.yml")):
        push = on(load(f.name)).get("push")
        if isinstance(push, dict) and push.get("tags"):
            out.add(f.name)
    return out


def dispatched() -> set[str]:
    env = load("auto-tag.yml")["jobs"]["tag"]["env"]
    return set(env["RELEASE_WORKFLOWS"].split())


def test_auto_tag_dispatches_every_tag_workflow() -> None:
    assert dispatched() == tag_workflows()
    assert "release.yml" in dispatched()


@pytest.mark.parametrize("workflow", sorted(tag_workflows()))
def test_each_tag_workflow_takes_a_tag_by_dispatch(workflow: str) -> None:
    inputs = (on(load(workflow)).get("workflow_dispatch") or {}).get("inputs") or {}
    assert "tag" in inputs, f"{workflow}: workflow_dispatch needs a `tag` input (auto-tag.yml passes it)"


def test_auto_tag_follows_ci_and_the_rehearsal_on_main() -> None:
    doc = load("auto-tag.yml")
    run_on = on(doc)["workflow_run"]
    names = {load("ci.yml")["name"], load("update-rehearsal.yml")["name"]}
    assert set(run_on["workflows"]) == names
    assert run_on["branches"] == ["main"]
    assert doc["concurrency"]["cancel-in-progress"] is False


def test_the_guard_checks_a_dispatched_runs_tag() -> None:
    steps = load("_tag-guard.yml")["jobs"]["guard"]["steps"]
    assert "workflow_dispatch" in steps[0].get("if", ""), "the guard's first step checks a dispatched run's ref"
