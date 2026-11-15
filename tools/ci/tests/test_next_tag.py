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


# --- the broadcast rehearsal's nightly (story 50.29 §4) -----------------------------------------


def nightly(sha: str, conclusion: str = "success", event: str = "workflow_dispatch", created: str = "", **extra: str) -> dict[str, str]:
    run = {"headSha": sha, "headBranch": "main", "event": event, "status": "completed", "conclusion": conclusion, "createdAt": created}
    run["displayTitle"] = nt.BROADCAST_NIGHTLY_TITLE + sha if event != "schedule" else "Broadcast rehearsal"
    return {**run, **extra}


def test_the_broadcast_code_is_the_rehearsals_pull_request_paths() -> None:
    assert tuple(on(load("broadcast-rehearsal.yml"))["pull_request"]["paths"]) == nt.BROADCAST_CODE


def test_the_broadcast_rehearsal_names_a_nightly_run_after_its_commit() -> None:
    doc = load("broadcast-rehearsal.yml")
    name = doc["run-name"]
    assert f"format('{nt.BROADCAST_NIGHTLY_TITLE}{{0}}', github.sha)" in name
    assert "github.event_name == 'schedule'" in name and "inputs.set == 'nightly'" in name
    # the run-name's nightly is the set the jobs run
    assert "github.event_name == 'schedule' && 'nightly'" in doc["env"]["SET"]
    assert "schedule" in on(doc)


def test_nightly_runs_are_the_nightly_set_on_main_only() -> None:
    runs = [
        nightly(SHA_A, event="schedule"),  # a scheduled run before the run-name: its headSha
        nightly(SHA_B),  # a hand run with set: nightly
        {"headSha": SHA_C, "headBranch": "main", "event": "push", "status": "completed", "conclusion": "success", "displayTitle": "Broadcast rehearsal"},
        {"headSha": SHA_C, "headBranch": "feat/x", "event": "pull_request", "status": "completed", "conclusion": "success", "displayTitle": "Broadcast rehearsal"},
        nightly(SHA_D, headBranch="feat/x"),  # a nightly-set hand run on another branch
        # a pull request's run (a fork's branch called main) can name itself anything
        nightly(SHA_C, event="pull_request"),
        nightly(SHA_C, event="push"),
        # a hand run whose name is not its own commit
        {**nightly(SHA_D), "displayTitle": nt.BROADCAST_NIGHTLY_TITLE + SHA_C},
    ]
    assert [c for c, _ in nt.nightly_results(runs)] == [SHA_A, SHA_B]


def test_nightly_results_skip_runs_with_no_result_and_sort_newest_first() -> None:
    runs = [
        nightly(SHA_A, "failure", created="2026-11-12T03:17:00Z"),
        nightly(SHA_B, "success", created="2026-11-14T03:17:00Z"),
        nightly(SHA_C, "cancelled", created="2026-11-15T03:17:00Z"),
        {**nightly(SHA_D, created="2026-11-16T03:17:00Z"), "status": "in_progress", "conclusion": ""},
        nightly(SHA_D, "timed_out", created="2026-11-13T03:17:00Z"),
    ]
    assert nt.nightly_results(runs) == [(SHA_B, True), (SHA_D, False), (SHA_A, False)]


# a linear main a <- b <- c <- d; the broadcast code changed in c (b and a share theirs)
CODE = {"a": "edge-1", "b": "edge-1", "c": "edge-2", "d": "edge-2"}


def verdict(sha: str, nightlies: list[tuple[str, bool]]) -> str | None:
    return nt.broadcast_verdict(sha, nightlies, CODE.get)


def test_eligible_on_a_green_nightly_of_its_own_commit() -> None:
    assert verdict("d", [("d", True)]) is None


def test_eligible_when_its_broadcast_code_has_not_changed_since_a_green_nightly() -> None:
    assert verdict("d", [("c", True)]) is None  # d changed no broadcast file since c
    assert verdict("b", [("d", True), ("a", True)]) is None  # a newer nightly on other code is not b's


def test_not_eligible_when_the_newest_nightly_on_its_code_failed() -> None:
    why = verdict("d", [("d", False), ("c", True)])
    assert why is not None and "failed" in why


def test_not_eligible_without_a_nightly_on_its_code() -> None:
    why = verdict("c", [("b", True), ("a", True)])  # c changed the broadcast code: no nightly since
    assert why is not None and "no broadcast rehearsal nightly" in why
    assert verdict("c", []) is not None


def test_not_eligible_when_its_code_is_not_in_the_checkout() -> None:
    assert verdict("z", [("z", True)]) is not None


def test_the_newest_eligible_commit_when_the_newest_green_one_is_not() -> None:
    newest_first = list(reversed(ORDER))
    nightlies = [("d", False), ("b", True)]  # d's code (c's too) failed its nightly; a and b's passed
    sha, passed_over = nt.newest_eligible(newest_first, {"a", "b", "c", "d"}, lambda c: verdict(c, nightlies))
    assert sha == "b"
    assert len(passed_over) == 2 and all("failed" in w for w in passed_over)


def test_everything_green_chooses_the_newest() -> None:
    newest_first = list(reversed(ORDER))
    sha, passed_over = nt.newest_eligible(newest_first, {"a", "b", "c", "d"}, lambda c: verdict(c, [("d", True)]))
    assert (sha, passed_over) == ("d", [])


def test_no_eligible_commit() -> None:
    newest_first = list(reversed(ORDER))
    sha, passed_over = nt.newest_eligible(newest_first, {"c", "d"}, lambda c: verdict(c, []))
    assert sha is None and len(passed_over) == 2


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


def choose(
    repo: Path,
    green: list[str],
    monkeypatch: pytest.MonkeyPatch,
    capsys: pytest.CaptureFixture[str],
    nightlies: list[dict[str, str]] | None = None,
) -> dict[str, str]:
    import json

    (repo / ".green").write_text("\n".join(green) + "\n")
    # by default a green nightly on every green commit: these cases are about the other rules
    runs = [nightly(c) for c in green] if nightlies is None else nightlies
    (repo / ".bc.json").write_text(json.dumps(runs))
    monkeypatch.chdir(repo)
    assert nt.main(["--green", str(repo / ".green"), "--broadcast-runs", str(repo / ".bc.json"), "--main", "HEAD"]) == 0
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


def test_in_a_repository_the_broadcast_rehearsal_gates_the_tag(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]
) -> None:
    repo = tmp_path / "r"
    repo.mkdir()
    run(repo, "init", "-q", "-b", "main")
    a = commit(repo, "api/main.go")
    run(repo, "tag", "-a", "v0.3.82", "-m", "v0.3.82", a)
    b = commit(repo, "web/packages/broadcast-edge/src/index.ts")  # the nightly ran here
    c = commit(repo, "web/app.ts")  # no broadcast file changed since b
    d = commit(repo, "streaming/internal/broadcast/up.go")  # a broadcast change, no nightly since

    # everything green: d has no nightly on its code, so c (b's code, green nightly) is tagged
    out = choose(repo, [b, c, d], monkeypatch, capsys, nightlies=[nightly(b)])
    assert (out["tag"], out["sha"]) == ("v0.3.83", c)
    assert "passed over" in out["reason"] and d in out["reason"]

    # a green nightly on d: d is tagged
    out = choose(repo, [b, c, d], monkeypatch, capsys, nightlies=[nightly(d, created="2"), nightly(b, created="1")])
    assert (out["tag"], out["sha"]) == ("v0.3.83", d)

    # the newest nightly on b's code failed: neither b nor c is eligible
    red = [nightly(b, "failure", created="2"), nightly(b, created="1")]
    out = choose(repo, [b, c], monkeypatch, capsys, nightlies=red)
    assert (out["tag"], out["sha"]) == ("", "")
    assert "failed" in out["reason"]

    # no nightly at all: nothing is tagged (fail closed)
    assert choose(repo, [b, c, d], monkeypatch, capsys, nightlies=[])["tag"] == ""


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
    (repo / ".bc.json").write_text(json.dumps([nightly(c, event="schedule")]))
    monkeypatch.chdir(repo)
    assert nt.main(["--ci-runs", ".ci.json", "--rehearsal-runs", ".rh.json", "--broadcast-runs", ".bc.json", "--main", "HEAD"]) == 0
    out = dict(line.split("=", 1) for line in capsys.readouterr().out.splitlines())
    assert (out["tag"], out["sha"]) == ("v0.3.83", b)
    with pytest.raises(SystemExit):
        nt.main(["--ci-runs", ".ci.json", "--broadcast-runs", ".bc.json", "--main", "HEAD"])
    with pytest.raises(SystemExit):  # the broadcast rehearsal's runs are never optional
        nt.main(["--ci-runs", ".ci.json", "--rehearsal-runs", ".rh.json", "--main", "HEAD"])


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
