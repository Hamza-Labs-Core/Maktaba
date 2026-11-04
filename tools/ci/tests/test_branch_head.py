"""ci-branch.yml's guard (tools/ci/branch_head.py): CI runs on a branch head only for a pull
request that conflicts with its base, against the merge-base."""

from __future__ import annotations

import sys
from pathlib import Path
from typing import Any

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

import branch_head as bh  # noqa: E402


def pr(number: int, base: str = "main", mergeable: bool | None = True) -> dict[str, Any]:
    return {"number": number, "base": {"ref": base}, "mergeable": mergeable}


def test_no_pull_request_runs_nothing() -> None:
    assert bh.decide([]) == bh.Decision(False, bh.decide([]).why)


def test_a_mergeable_pull_request_is_left_to_its_pull_request_run() -> None:
    d = bh.decide([pr(7)])
    assert not d.run and "#7 merges cleanly" in d.why


def test_a_conflicting_pull_request_runs_on_the_head_against_its_base() -> None:
    d = bh.decide([pr(7, mergeable=True), pr(608, base="release/0.3", mergeable=False)])
    assert (d.run, d.pr, d.base_ref) == (True, 608, "release/0.3")


def test_mergeability_still_unknown_runs_rather_than_leaving_no_result() -> None:
    d = bh.decide([pr(9, mergeable=None)])
    assert (d.run, d.pr, d.base_ref) == (True, 9, "main")


def test_only_bases_ci_yml_runs_on_are_gated() -> None:
    prs = [pr(1, base="main"), pr(2, base="release/0.3"), pr(3, base="feat/other"), pr(4, base="releases")]
    assert [p["number"] for p in bh.gated(prs)] == [1, 2]


class FakeApi:
    def __init__(self, listed: list[dict[str, Any]], states: dict[int, list[bool | None]]) -> None:
        self.listed, self.states = listed, states
        self.calls: list[str] = []

    def __call__(self, path: str) -> Any:
        self.calls.append(path)
        if "/pulls?" in path:
            return self.listed
        if "/compare/" in path:
            return {"merge_base_commit": {"sha": "b" * 40}}
        n = int(path.rsplit("/", 1)[1])
        seq = self.states[n]
        return pr(n, mergeable=seq.pop(0) if len(seq) > 1 else seq[0])


def test_resolve_waits_while_github_works_out_mergeability() -> None:
    api = FakeApi([pr(5)], {5: [None, None, False]})
    slept: list[float] = []
    d = bh.resolve("o/r", "feat/x", api=api, sleep=slept.append, wait_s=1000, poll_s=10)
    assert d.run and d.pr == 5
    assert slept == [10, 10]
    assert api.calls[0] == "repos/o/r/pulls?state=open&head=o:feat/x&per_page=100"


def test_resolve_gives_up_waiting_and_runs() -> None:
    api = FakeApi([pr(5)], {5: [None]})
    d = bh.resolve("o/r", "feat/x", api=api, sleep=lambda s: None, wait_s=0)
    assert d.run and "has not worked out" in d.why


def test_resolve_skips_a_branch_with_no_gated_pull_request() -> None:
    api = FakeApi([pr(3, base="feat/other")], {})
    assert not bh.resolve("o/r", "feat/x", api=api, sleep=lambda s: None).run
    assert len(api.calls) == 1


def test_the_merge_base_comes_from_comparing_the_base_with_the_head() -> None:
    api = FakeApi([], {})
    assert bh.merge_base("o/r", "main", "a" * 40, api=api) == "b" * 40
    assert api.calls == [f"repos/o/r/compare/main...{'a' * 40}"]


def test_main_writes_the_outputs(tmp_path: Path, monkeypatch: Any) -> None:
    out = tmp_path / "out"
    monkeypatch.setenv("GITHUB_OUTPUT", str(out))
    monkeypatch.setattr(bh, "resolve", lambda repo, branch: bh.decide([pr(608, mergeable=False)]))
    monkeypatch.setattr(bh, "merge_base", lambda repo, ref, sha: "c" * 40)
    assert bh.main(["--repo", "o/r", "--branch", "feat/x", "--sha", "a" * 40]) == 0
    assert out.read_text().splitlines() == ["run=true", f"base={'c' * 40}", "pr=608"]
