"""The release session's choice (tools/release/next_tag.py): the next patch tag, never on a
trail-only change, never twice on one commit, never on a commit older than a tagged one; and the
tag workflows its tag push starts: release.yml only on a push, each other one dispatchable on
its tag by hand."""

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


def test_trail_paths_are_the_trails_own_trail_only_list() -> None:
    # .trail.toml's [trail_only]: what `trail is-trail-only` (agent-trail) calls trail-only
    import tomllib

    conf = tomllib.loads((ROOT / ".trail.toml").read_text(encoding="utf-8"))
    assert tuple(conf["trail_only"]["paths"]) == nt.TRAIL_ONLY


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


# --- the green commits: main's runs of both workflows -------------------------------------------

SHA_A, SHA_B, SHA_C, SHA_D = ("a" * 40, "b" * 40, "c" * 40, "d" * 40)


def ci_run(sha: str, conclusion: str = "success") -> dict[str, str]:
    return {"headSha": sha, "event": "push", "conclusion": conclusion, "displayTitle": "Merge pull request"}


def main_rehearsal(rehearsed: str, head: str, conclusion: str = "success") -> dict[str, str]:
    # a workflow_run's headSha is main's head when it started: not the commit it rehearsed
    return {"headSha": head, "event": "workflow_run", "conclusion": conclusion, "displayTitle": nt.REHEARSAL_TITLE + rehearsed}


def test_main_rehearsal_counts_for_the_commit_in_its_name_not_its_head_sha() -> None:
    green = nt.green_commits([ci_run(SHA_A), ci_run(SHA_B)], [main_rehearsal(SHA_A, head=SHA_B)])
    assert green == {SHA_A}


def test_a_failed_or_not_run_rehearsal_is_not_green() -> None:
    skipped = {"headSha": SHA_B, "event": "workflow_run", "conclusion": "skipped", "displayTitle": "Update rehearsal not run (CI cancelled)"}
    runs = [main_rehearsal(SHA_A, head=SHA_B, conclusion="failure"), skipped]
    assert nt.green_commits([ci_run(SHA_A), ci_run(SHA_B)], runs) == set()


def test_a_push_rehearsal_counts_for_its_head_sha_and_other_events_never() -> None:
    rehearsals = [
        {"headSha": SHA_A, "event": "push", "displayTitle": "x"},  # main's, before 2026-11-07
        {"headSha": SHA_B, "event": "pull_request", "displayTitle": "x"},
        {"headSha": SHA_C, "event": "schedule", "displayTitle": "x"},
    ]
    ci = [ci_run(SHA_A), ci_run(SHA_B), ci_run(SHA_C), ci_run(SHA_D, "cancelled")]
    assert nt.green_commits(ci, rehearsals + [main_rehearsal(SHA_D, head=SHA_D)]) == {SHA_A}


def test_a_name_that_is_not_a_full_sha_is_no_commit() -> None:
    assert nt.rehearsed_commit({"event": "workflow_run", "headSha": SHA_A, "displayTitle": nt.REHEARSAL_TITLE + SHA_B[:12]}) is None
    assert nt.rehearsed_commit({"event": "workflow_run", "headSha": SHA_A, "displayTitle": nt.REHEARSAL_TITLE + SHA_B + "x"}) is None


def test_the_rehearsal_names_its_main_run_after_the_commit_it_rehearsed() -> None:
    doc = load("update-rehearsal.yml")
    name = doc["run-name"]
    assert f"format('{nt.REHEARSAL_TITLE}{{0}}', github.event.workflow_run.head_sha)" in name
    # and main's run is the one CI's passed push run starts
    trigger = on(doc)["workflow_run"]
    assert trigger == {"workflows": ["CI"], "types": ["completed"], "branches": ["main"]}
    assert load("ci.yml")["name"] == "CI"


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


def test_in_a_repository_from_both_run_lists(tmp_path: Path, monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]) -> None:
    import json

    repo = tmp_path / "r"
    repo.mkdir()
    run(repo, "init", "-q", "-b", "main")
    a = commit(repo, "api/main.go")
    run(repo, "tag", "-a", "v0.3.82", "-m", "v0.3.82", a)
    b = commit(repo, "web/app.ts")
    c = commit(repo, "web/app.ts")
    # CI passed on b and c; the rehearsal CI's run on b started (its headSha: c, main's head) passed
    (repo / ".ci.json").write_text(json.dumps([ci_run(c), ci_run(b)]))
    (repo / ".rh.json").write_text(json.dumps([main_rehearsal(b, head=c)]))
    monkeypatch.chdir(repo)
    assert nt.main(["--ci-runs", ".ci.json", "--rehearsal-runs", ".rh.json", "--main", "HEAD"]) == 0
    out = dict(line.split("=", 1) for line in capsys.readouterr().out.splitlines())
    assert (out["tag"], out["sha"]) == ("v0.3.83", b)
    with pytest.raises(SystemExit):
        nt.main(["--ci-runs", ".ci.json", "--main", "HEAD"])


# --- the workflows its tag push starts -----------------------------------------------------------


def tag_workflows() -> set[str]:
    out = set()
    for f in sorted(WORKFLOWS.glob("*.yml")):
        push = on(load(f.name)).get("push")
        if isinstance(push, dict) and push.get("tags"):
            out.add(f.name)
    return out


def test_release_is_never_dispatched() -> None:
    # shared/go/releaseverify pins BuildTrigger "push" (identity.go): homes refuse anything else,
    # so release.yml runs only from the release session's tag push
    assert "release.yml" in tag_workflows()
    assert "workflow_dispatch" not in on(load("release.yml"))
    identity = (ROOT / "shared/go/releaseverify/identity.go").read_text()
    assert 'BuildTrigger = "push"' in identity


@pytest.mark.parametrize("workflow", sorted(tag_workflows() - {"release.yml"}))
def test_each_other_tag_workflow_takes_a_tag_by_hand(workflow: str) -> None:
    inputs = (on(load(workflow)).get("workflow_dispatch") or {}).get("inputs") or {}
    assert "tag" in inputs, f"{workflow}: workflow_dispatch needs a `tag` input (a tag's run by hand)"


def test_the_guard_checks_a_dispatched_runs_tag() -> None:
    steps = load("_tag-guard.yml")["jobs"]["guard"]["steps"]
    assert "workflow_dispatch" in steps[0].get("if", ""), "the guard's first step checks a dispatched run's ref"
