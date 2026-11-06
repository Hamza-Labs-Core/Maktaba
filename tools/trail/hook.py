#!/usr/bin/env python3
"""Claude Code hook: did this session write a trail event for each pull request it merged?

Wired in .claude/settings.json for Stop, PreCompact, SessionStart and SessionEnd. It reads
the hook's JSON on stdin (session_id, transcript_path, hook_event_name, stop_hook_active,
source), finds `gh pr merge N` in the session's transcript and its subagents' transcripts,
keeps the N that git shows merged into origin/main, and looks for an event for each one:
a docs/trail/events/NNNN-*.md file (or one listing N in prs:) on any local branch or in any
worktree of this repository.

  Stop          blocks once (stop_hook_active) with the list, so the session writes them now
  PreCompact    tells the user, before the details are compacted away
  SessionStart  after a compact or resume: reminds the session; at startup: lists what
                earlier sessions left unwritten (from the log SessionEnd keeps)
  SessionEnd    tells the user and logs what is still unwritten

Never fails the session: any error prints nothing and exits 0.
"""

from __future__ import annotations

import json
import os
import re
import subprocess
import sys
import time
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
from trail import EVENTS, FILE_RE, MERGE_RE, MISSING_AFTER  # noqa: E402

MERGE_CMD_RE = re.compile(r"gh pr merge (\d+)")
STATE_DIR = Path(os.environ.get("MAKTABA_TRAIL_HOOK_DIR", Path.home() / ".claude" / "trail-hook"))
FETCH_EVERY = 600  # seconds between `git fetch origin main` runs from this hook


def git(repo: Path, *args: str, timeout: float = 20) -> str:
    r = subprocess.run(["git", *args], cwd=repo, capture_output=True, text=True, timeout=timeout)
    return r.stdout if r.returncode == 0 else ""


def transcripts(transcript: Path) -> list[Path]:
    out = [transcript] if transcript.is_file() else []
    sub = transcript.with_suffix("") / "subagents"
    if sub.is_dir():
        out += sorted(sub.glob("*.jsonl"))
    return out


def merge_commands(paths: list[Path]) -> set[int]:
    """Pull request numbers the session (or its subagents) ran `gh pr merge` on."""
    found: set[int] = set()
    for p in paths:
        try:
            with p.open(encoding="utf-8", errors="replace") as f:
                for line in f:
                    if "gh pr merge" not in line or '"tool_use"' not in line:
                        continue
                    try:
                        rec = json.loads(line)
                    except ValueError:
                        continue
                    for block in (rec.get("message") or {}).get("content") or []:
                        if isinstance(block, dict) and block.get("type") == "tool_use":
                            cmd = str((block.get("input") or {}).get("command", ""))
                            found |= {int(n) for n in MERGE_CMD_RE.findall(cmd)}
        except OSError:
            continue
    return found


def fetch_main(repo: Path) -> None:
    STATE_DIR.mkdir(parents=True, exist_ok=True)
    stamp = STATE_DIR / "last-fetch"
    try:
        if time.time() - stamp.stat().st_mtime < FETCH_EVERY:
            return
    except OSError:
        pass
    git(repo, "fetch", "-q", "origin", "main", timeout=15)
    stamp.touch()


def merged_on_main(repo: Path) -> set[int]:
    log = git(repo, "log", "--first-parent", "--merges", "--format=%s", "origin/main")
    return {int(m.group(1)) for line in log.splitlines() if (m := MERGE_RE.match(line))}


def covered(repo: Path) -> set[int]:
    """Pull requests with an event on any local branch, remote branch or worktree."""
    out: set[int] = set()

    def add(name: str, text: str | None = None) -> None:
        m = FILE_RE.match(Path(name).name)
        if m and m.group("pr"):
            out.add(int(m.group("pr")))
        if text:
            pm = re.search(r"^prs:\s*\[([^\]]*)\]", text, re.M)
            if pm:
                out.update(int(x) for x in re.findall(r"\d+", pm.group(1)))

    for name in git(repo, "log", "--all", "--format=", "--name-only", "--", EVENTS).split():
        add(name)
    for line in git(repo, "worktree", "list", "--porcelain").splitlines():
        if line.startswith("worktree "):
            d = Path(line[9:]) / EVENTS
            if d.is_dir():
                for p in d.glob("*.md"):
                    try:
                        add(p.name, p.read_text(encoding="utf-8"))
                    except OSError:
                        pass
    return out


def unwritten(repo: Path, transcript: Path) -> list[int]:
    asked = {n for n in merge_commands(transcripts(transcript)) if n > MISSING_AFTER}
    if not asked:
        return []
    fetch_main(repo)
    return sorted((asked & merged_on_main(repo)) - covered(repo))


def describe(prs: list[int]) -> str:
    names = ", ".join(f"#{n}" for n in prs)
    return (
        f"Trail: this session merged {names} with no trail event yet. Write one per pull request "
        f"now, while you remember what happened (the `trail` skill: {EVENTS}/NNNN-slug.md, "
        "in the code PR or a trail-only PR)."
    )


def log_path() -> Path:
    return STATE_DIR / "unwritten.jsonl"


def remember(session: str, prs: list[int]) -> None:
    STATE_DIR.mkdir(parents=True, exist_ok=True)
    with log_path().open("a", encoding="utf-8") as f:
        f.write(json.dumps({"session": session, "prs": prs, "at": time.strftime("%Y-%m-%dT%H:%M:%S")}) + "\n")


def earlier(repo: Path) -> list[tuple[str, list[int]]]:
    try:
        lines = log_path().read_text(encoding="utf-8").splitlines()
    except OSError:
        return []
    have = covered(repo)
    out = []
    for line in lines[-50:]:
        try:
            rec = json.loads(line)
        except ValueError:
            continue
        left = [n for n in rec.get("prs", []) if n not in have]
        if left:
            out.append((str(rec.get("session", "?")), left))
    return out


def handle(data: dict[str, object], repo: Path) -> dict[str, object] | None:
    event = str(data.get("hook_event_name", ""))
    transcript = Path(str(data.get("transcript_path", "")))
    session = str(data.get("session_id", ""))
    if event == "SessionStart" and data.get("source") == "startup":
        left = earlier(repo)
        if not left:
            return None
        rows = "; ".join(f"{', '.join('#' + str(n) for n in prs)} (session {s})" for s, prs in left)
        return {
            "hookSpecificOutput": {
                "hookEventName": "SessionStart",
                "additionalContext": f"Trail: earlier sessions merged pull requests with no trail event: {rows}. "
                "Write their events from the PRs and their diffs if this session has the context.",
            }
        }
    prs = unwritten(repo, transcript)
    if not prs:
        return None
    msg = describe(prs)
    if event == "Stop":
        return None if data.get("stop_hook_active") else {"decision": "block", "reason": msg}
    if event == "SessionStart":
        return {"hookSpecificOutput": {"hookEventName": "SessionStart", "additionalContext": msg}}
    if event == "SessionEnd":
        remember(session, prs)
    return {"systemMessage": msg}


def main() -> int:
    try:
        data = json.load(sys.stdin)
        repo = Path(os.environ.get("CLAUDE_PROJECT_DIR") or str(data.get("cwd") or "."))
        out = handle(data, repo)
        if out:
            print(json.dumps(out))
    except Exception:  # a hook never breaks the session
        return 0
    return 0


if __name__ == "__main__":
    sys.exit(main())
