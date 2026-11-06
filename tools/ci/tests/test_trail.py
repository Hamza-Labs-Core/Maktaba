"""tools/trail: the Claude trail's checker (events, snapshots, Missing) and its session hook."""

from __future__ import annotations

import json
import subprocess
import sys
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(ROOT / "tools" / "trail"))

import hook  # noqa: E402
import trail  # noqa: E402

GOOD = """---
schema: 1
kind: pr
pr: 700
title: A change
date: 2026-11-06
session: local_abc
---

## What happened
Built the thing; the first guess (a race) was wrong, the cause was a stale cache.

## What we learned
- The cache is keyed by path, not by id.
"""


def write(repo: Path, rel: str, text: str) -> None:
    p = repo / rel
    p.parent.mkdir(parents=True, exist_ok=True)
    p.write_text(text, encoding="utf-8")


def g(repo: Path, *args: str) -> str:
    return subprocess.run(["git", *args], cwd=repo, check=True, capture_output=True, text=True).stdout.strip()


@pytest.fixture
def repo(tmp_path: Path) -> Path:
    g(tmp_path, "init", "-q", "-b", "main")
    g(tmp_path, "config", "user.email", "t@example.invalid")
    g(tmp_path, "config", "user.name", "t")
    write(tmp_path, "README.md", "x\n")
    g(tmp_path, "add", "-A")
    g(tmp_path, "commit", "-qm", "root")
    return tmp_path


def merge_pr(repo: Path, n: int, files: dict[str, str], title: str = "Some change") -> str:
    g(repo, "checkout", "-q", "-b", f"pr{n}")
    for rel, text in files.items():
        write(repo, rel, text)
    g(repo, "add", "-A")
    g(repo, "commit", "-qm", f"work {n}")
    g(repo, "checkout", "-q", "main")
    g(repo, "merge", "-q", "--no-ff", f"pr{n}", "-m", f"Merge pull request #{n} from org/pr{n}\n\n{title}")
    return g(repo, "rev-parse", "HEAD")


# ---- events ---------------------------------------------------------------------------


def test_a_good_event_checks() -> None:
    ev, errs = trail.event_from("docs/trail/events/0700-a-change.md", GOOD)
    assert errs == []
    assert ev is not None and ev.pr == 700 and "#700" in ev.cites


@pytest.mark.parametrize(
    ("name", "text", "needle"),
    [
        ("0701-a-change.md", GOOD, "named for pull request 701"),
        ("700-a-change.md", GOOD, "an event is named"),
        ("0700-A_Change.md", GOOD, "an event is named"),
        ("0700-a.md", GOOD.replace("kind: pr", "kind: chat"), "kind must be one of"),
        ("0700-a.md", GOOD.replace("date: 2026-11-06", "date: 6 Nov"), "date must be"),
        ("0700-a.md", GOOD.replace("session: local_abc\n", ""), "lacks ['session']"),
        ("0700-a.md", GOOD.replace("## What happened\nBuilt", "## What happened\n\n## Notes\nBuilt"), "unknown sections"),
        ("0700-a.md", GOOD + "\nThe token is ghp_" + "a" * 36 + "\n", "a GitHub token"),
        ("0700-a.md", GOOD + "\npassword: hunter2hunter2\n", "a credential value"),
        ("0700-a.md", GOOD + "\nDone this way (owner, 2026-11-05).\n", "owner"),
        ("0700-a.md", GOOD.replace("kind: pr", "kind: correction"), "corrects"),
        ("d20261106-x.md", GOOD.replace("pr: 700\n", "").replace("kind: pr", "kind: decision").replace("2026-11-06", "2026-11-07"), "named for 20261106"),
    ],
)
def test_bad_events_are_refused_with_a_reason(name: str, text: str, needle: str | None) -> None:
    _, errs = trail.event_from(f"docs/trail/events/{name}", text)
    if needle is None:  # still has text: not empty
        return
    assert any(needle in e for e in errs), errs


def test_what_happened_is_required() -> None:
    text = GOOD.split("## What happened")[0] + "## What we learned\n- x\n"
    _, errs = trail.event_from("docs/trail/events/0700-a.md", text)
    assert any("What happened" in e for e in errs)


def test_sections_must_keep_their_order() -> None:
    text = GOOD.replace("## What happened", "## Keep doing\n- x\n\n## What happened")
    _, errs = trail.event_from("docs/trail/events/0700-a.md", text)
    assert any("order" in e for e in errs)


def test_issue_and_day_events_cite_by_their_names() -> None:
    issue = GOOD.replace("kind: pr\npr: 700", "kind: incident\nissue: 649")
    ev, errs = trail.event_from("docs/trail/events/i0649-shards.md", issue)
    assert errs == [] and ev is not None and "i649" in ev.cites
    day = GOOD.replace("kind: pr\npr: 700\n", "kind: decision\nprs: [608, 635]\n")
    ev, errs = trail.event_from("docs/trail/events/d20261106-split.md", day)
    assert errs == [] and ev is not None and {"d20261106-split", "#608", "#635"} <= ev.cites


def test_a_correction_must_point_at_an_event(repo: Path) -> None:
    write(repo, "docs/trail/events/0700-a.md", GOOD)
    fix = GOOD.replace("kind: pr", "kind: correction\ncorrects: 0699-gone").replace("pr: 700", "pr: 701")
    write(repo, "docs/trail/events/0701-fix.md", fix)
    _, errs = trail.load_events(None, repo)
    assert any("corrects 0699-gone" in e for e in errs)


# ---- missing and snapshots ------------------------------------------------------------


def test_missing_lists_merged_prs_with_no_event_and_skips_trail_only_ones(repo: Path) -> None:
    merge_pr(repo, 700, {"a.txt": "a"}, "Feature A")
    merge_pr(repo, 701, {"b.txt": "b", "docs/trail/events/0701-b.md": GOOD.replace("700", "701")})
    merge_pr(repo, 702, {"docs/trail/events/0700-a.md": GOOD})  # a trail-only PR: never Missing
    merge_pr(repo, 600, {"c.txt": "c"})  # before trail v2: covered by the frozen trail
    events, errs = trail.load_events("HEAD", repo)
    assert errs == []
    assert trail.missing("HEAD", events, repo) == []  # 700 got its event in 702
    merge_pr(repo, 703, {"d.txt": "d"}, "Feature D")
    events, _ = trail.load_events("HEAD", repo)
    rows = trail.missing("HEAD", events, repo)
    assert [r[0] for r in rows] == [703] and rows[0][2] == "Feature D"


def snapshot(repo: Path, tag: str, body: dict[str, str], built: str = "full") -> str:
    sha = g(repo, "rev-parse", f"{tag}^{{commit}}")
    events, _ = trail.load_events(sha, repo)
    sections = {
        "Keep doing": body.get("Keep doing", ""),
        "Stop doing": body.get("Stop doing", ""),
        "What we learned": body.get("What we learned", ""),
        "Open": body.get("Open", ""),
        "Missing events": trail.missing_table(trail.missing(sha, events, repo)),
        "Index": trail.index_table(events),
    }
    text = f"---\nschema: 1\ntag: {tag}\nup_to_commit: {sha[:9]}\nbuilt_from: {built}\n---\n\n# Trail snapshot: {tag}\n\n"
    text += "".join(f"## {k}\n\n{v}\n\n" for k, v in sections.items())
    return text


def test_a_snapshot_checks_when_its_citations_resolve(repo: Path) -> None:
    merge_pr(repo, 700, {"a.txt": "a", "docs/trail/events/0700-a.md": GOOD})
    g(repo, "tag", "v0.4.0")
    text = snapshot(repo, "v0.4.0", {"What we learned": "- The cache is keyed by path. (#700)"})
    write(repo, "docs/trail/snapshots/v0.4.0.md", text)
    write(repo, "docs/trail/current.md", text)
    assert trail.check(repo) == []


def test_a_snapshot_refuses_uncited_or_unknown_citations_and_hand_edited_tables(repo: Path) -> None:
    merge_pr(repo, 700, {"a.txt": "a", "docs/trail/events/0700-a.md": GOOD})
    g(repo, "tag", "v0.4.0")
    text = snapshot(repo, "v0.4.0", {"Keep doing": "- Something with no source.\n- Another. (#999)"})
    text = text.replace("| 0700-a |", "| 0700-a-edited |")
    errs = trail.check_snapshot("docs/trail/snapshots/v0.4.0.md", text, repo)
    assert any("cites no event" in e for e in errs)
    assert any("cites #999" in e for e in errs)
    assert any("`## Index` is not" in e for e in errs)


def test_a_snapshot_must_sit_on_its_tags_commit_and_current_must_copy_the_newest(repo: Path) -> None:
    merge_pr(repo, 700, {"a.txt": "a", "docs/trail/events/0700-a.md": GOOD})
    g(repo, "tag", "v0.4.0")
    merge_pr(repo, 701, {"b.txt": "b", "docs/trail/events/0701-b.md": GOOD.replace("700", "701")})
    g(repo, "tag", "v0.4.1")
    old = snapshot(repo, "v0.4.0", {})
    new = snapshot(repo, "v0.4.1", {}, built="v0.4.0")
    write(repo, "docs/trail/snapshots/v0.4.0.md", old)
    write(repo, "docs/trail/snapshots/v0.4.1.md", new.replace(g(repo, "rev-parse", "v0.4.1")[:9], g(repo, "rev-parse", "v0.4.0")[:9]))
    write(repo, "docs/trail/current.md", old)
    errs = trail.check(repo)
    assert any("is not v0.4.1's commit" in e for e in errs)
    assert any("not a copy of the newest snapshot" in e for e in errs)


def test_the_skeleton_carries_the_last_snapshot_forward_and_lists_new_events(repo: Path) -> None:
    merge_pr(repo, 700, {"a.txt": "a", "docs/trail/events/0700-a.md": GOOD})
    g(repo, "tag", "v0.4.0")
    first = snapshot(repo, "v0.4.0", {"Keep doing": "- Key caches by id. (#700)"})
    write(repo, "docs/trail/snapshots/v0.4.0.md", first)
    write(repo, "docs/trail/current.md", first)
    merge_pr(repo, 701, {"b.txt": "b", "docs/trail/events/0701-b.md": GOOD.replace("700", "701")})
    g(repo, "tag", "v0.4.1")
    sk = trail.skeleton("v0.4.1", repo)
    assert "built_from: v0.4.0" in sk
    assert "- Key caches by id. (#700)" in sk
    assert "new: docs/trail/events/0701-b.md" in sk and "new: docs/trail/events/0700-a.md" not in sk


def test_the_repositorys_own_trail_checks() -> None:
    # without git verification here (a shallow test checkout may lack tags); CI's lint step
    # runs the full check with tags fetched
    assert trail.check(ROOT, verify_git=False) == []


# ---- the hook -------------------------------------------------------------------------


def transcript(path: Path, commands: list[str]) -> Path:
    lines = []
    for c in commands:
        lines.append(json.dumps({"message": {"content": [{"type": "tool_use", "name": "Bash", "input": {"command": c}}]}}))
    path.write_text("\n".join(lines) + "\n", encoding="utf-8")
    return path


def hook_repo(repo: Path) -> None:
    # the hook reads origin/main: point it at the repo itself
    g(repo, "remote", "add", "origin", str(repo))
    g(repo, "fetch", "-q", "origin")


def test_the_hook_blocks_a_stop_once_for_a_merged_pr_with_no_event(repo: Path, tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(hook, "STATE_DIR", tmp_path / "state")
    merge_pr(repo, 700, {"a.txt": "a"})
    hook_repo(repo)
    t = transcript(tmp_path / "s.jsonl", ["gh pr merge 700 --merge", "gh pr merge 12 --merge", "echo gh pr merge 701"])
    out = hook.handle({"hook_event_name": "Stop", "transcript_path": str(t), "session_id": "s"}, repo)
    assert out is not None and out["decision"] == "block" and "#700" in str(out["reason"])
    assert "#12" not in str(out["reason"])  # before trail v2
    again = hook.handle({"hook_event_name": "Stop", "transcript_path": str(t), "stop_hook_active": True}, repo)
    assert again is None


def test_the_hook_is_quiet_once_an_event_exists_on_any_branch(repo: Path, tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(hook, "STATE_DIR", tmp_path / "state")
    merge_pr(repo, 700, {"a.txt": "a"})
    hook_repo(repo)
    g(repo, "checkout", "-q", "-b", "trail-700")
    write(repo, "docs/trail/events/0700-a.md", GOOD)
    g(repo, "add", "-A")
    g(repo, "commit", "-qm", "event")
    g(repo, "checkout", "-q", "main")
    t = transcript(tmp_path / "s.jsonl", ["gh pr merge 700 --merge"])
    assert hook.handle({"hook_event_name": "Stop", "transcript_path": str(t)}, repo) is None


def test_the_hook_reads_subagent_transcripts_and_logs_at_session_end(repo: Path, tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(hook, "STATE_DIR", tmp_path / "state")
    merge_pr(repo, 700, {"a.txt": "a"})
    hook_repo(repo)
    t = transcript(tmp_path / "s.jsonl", [])
    (tmp_path / "s" / "subagents").mkdir(parents=True)
    transcript(tmp_path / "s" / "subagents" / "agent-1.jsonl", ["gh pr merge 700 --merge"])
    end = hook.handle({"hook_event_name": "SessionEnd", "transcript_path": str(t), "session_id": "s"}, repo)
    assert end is not None and "#700" in str(end["systemMessage"])
    start = hook.handle({"hook_event_name": "SessionStart", "source": "startup", "transcript_path": "x"}, repo)
    assert start is not None and "#700" in str(start["hookSpecificOutput"])


def test_the_hook_never_fails_the_session(monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]) -> None:
    monkeypatch.setattr(sys, "stdin", __import__("io").StringIO("not json"))
    assert hook.main() == 0
    assert capsys.readouterr().out == ""
