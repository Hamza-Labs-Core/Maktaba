"""tools/ci/pr_closing_refs.py: a PR body must not close another open pull request."""

from __future__ import annotations

import importlib.util
import json
import sys
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[3]
_spec = importlib.util.spec_from_file_location("pr_closing_refs", ROOT / "tools/ci/pr_closing_refs.py")
assert _spec and _spec.loader
mod = importlib.util.module_from_spec(_spec)
sys.modules["pr_closing_refs"] = mod
_spec.loader.exec_module(mod)

REPO = "Hamza-Labs-Core/Maktaba"


@pytest.mark.parametrize(
    ("body", "want"),
    [
        ("Closes #709.", [709]),
        ("closes #709", [709]),
        ("Fixes: #12, fixed #13 and resolves #14", [12, 13, 14]),
        ("I resolved #795's merge with this fix", [795]),  # the incident's own words
        ("close #1 then CLOSED #2", [1, 2]),
        ("Closes Hamza-Labs-Core/Maktaba#709", [709]),
        ("fixes https://github.com/Hamza-Labs-Core/Maktaba/pull/795", [795]),
        ("resolves https://github.com/Hamza-Labs-Core/Maktaba/issues/710", [710]),
        ("Closes #709 and closes #709 again", [709]),
    ],
)
def test_closing_refs_are_found(body: str, want: list[int]) -> None:
    assert mod.closing_refs(body, REPO) == want


@pytest.mark.parametrize(
    "body",
    [
        "the merge for PR 795",
        "(#795)",
        "Part of #804",
        "see #795; it closes later",
        "disclosed #12",  # a word that only ends in close
        "fixtures #3",
        "closes other/repo#9",  # another repository's: not this check's
        "closes #",
    ],
)
def test_mentions_close_nothing(body: str) -> None:
    assert mod.closing_refs(body, REPO) == []


def test_only_open_pull_requests_fail() -> None:
    # 709 is an issue, 795 an open PR, 846 a merged PR
    state = {795: True, 709: False, 846: False}
    assert mod.open_pull_requests([709, 795, 846], lambda n: state[n]) == [795]


def test_main_fails_on_an_open_pr_and_passes_an_issue(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]
) -> None:
    monkeypatch.setenv("GITHUB_REPOSITORY", REPO)
    monkeypatch.setenv("GH_TOKEN", "t")
    monkeypatch.setattr(mod, "github_is_open_pr", lambda repo, token: lambda n: n == 795)

    def run(body: str, number: int = 900) -> int:
        event = tmp_path / "event.json"
        event.write_text(json.dumps({"pull_request": {"number": number, "body": body}}))
        monkeypatch.setenv("GITHUB_EVENT_PATH", str(event))
        return int(mod.main(["pr_closing_refs.py"]))

    assert run("I resolved #795's merge") == 1
    assert "#795, an open pull request" in capsys.readouterr().out
    assert run("Closes #709.") == 0
    assert run("the merge for PR 795 (#795)") == 0
    # a PR's own number is not another PR
    assert run("closes #795", number=795) == 0


def test_main_skips_other_events(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    event = tmp_path / "event.json"
    event.write_text(json.dumps({"push": {}}))
    monkeypatch.setenv("GITHUB_EVENT_PATH", str(event))
    assert mod.main(["pr_closing_refs.py"]) == 0


def test_main_fails_closed_without_a_token(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    event = tmp_path / "event.json"
    event.write_text(json.dumps({"pull_request": {"number": 1, "body": "closes #2"}}))
    monkeypatch.setenv("GITHUB_EVENT_PATH", str(event))
    monkeypatch.setenv("GITHUB_REPOSITORY", REPO)
    monkeypatch.delenv("GH_TOKEN", raising=False)
    monkeypatch.delenv("GITHUB_TOKEN", raising=False)
    assert mod.main(["pr_closing_refs.py"]) == 2
