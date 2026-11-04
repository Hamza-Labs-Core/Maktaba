#!/usr/bin/env python3
"""Whether a push to a branch runs CI on the branch head (ci-branch.yml's first job).

ci.yml runs on a pull request's merge ref. When the pull request conflicts with its base,
GitHub cannot build that ref and starts no pull_request run, so the branch would get no CI
result until the base is merged in. ci-branch.yml runs on every push to a branch other
than main and release/**, and this decides whether that run tests the branch head:

- no open pull request from the branch into main or release/**: no (nothing to gate);
- every such pull request mergeable: no (its pull_request run tests the merge ref, and that
  run reports the required `ci-success` check);
- one that conflicts with its base (GitHub's `mergeable` is false): yes, with the
  merge-base of the branch head and that pull request's base, which ci.yml's path filters
  compare against (not the base's tip, which has moved on);
- GitHub still working out `mergeable` after the wait: yes (a duplicate of a pull_request
  run costs runner time; no result at all is what this is here to prevent).

    branch_head.py --repo OWNER/NAME --branch BRANCH --sha SHA

Prints why and writes `run=true|false`, `base=<merge-base sha>`, `pr=<number>` to
$GITHUB_OUTPUT when it is set. Stdlib and the gh CLI only: it runs before anything is
installed.
"""

from __future__ import annotations

import argparse
import json
import os
import re
import subprocess
import sys
import time
import urllib.parse
from collections.abc import Callable, Mapping, Sequence
from dataclasses import dataclass
from typing import Any

# the bases ci.yml's pull_request trigger runs on
_BASE = re.compile(r"^(main|release/.+)$")


@dataclass(frozen=True)
class Decision:
    run: bool
    why: str
    pr: int | None = None
    base_ref: str = ""


def gated(prs: Sequence[Mapping[str, Any]]) -> list[Mapping[str, Any]]:
    """The open pull requests whose base ci.yml's pull_request trigger covers."""
    return [p for p in prs if _BASE.match(str((p.get("base") or {}).get("ref", "")))]


def decide(prs: Sequence[Mapping[str, Any]]) -> Decision:
    """From each gated pull request's own record (with `mergeable`): run on the head or not."""
    if not prs:
        return Decision(False, "no open pull request from this branch into main or release/**")
    for p in prs:
        if p.get("mergeable") is False:
            return Decision(
                True,
                f"#{p['number']} conflicts with {p['base']['ref']}: no pull_request run, so CI runs on the branch head",
                int(p["number"]),
                str(p["base"]["ref"]),
            )
    unknown = [p for p in prs if p.get("mergeable") is None]
    if unknown:
        p = unknown[0]
        return Decision(
            True,
            f"GitHub has not worked out whether #{p['number']} merges: CI runs on the branch head anyway",
            int(p["number"]),
            str(p["base"]["ref"]),
        )
    numbers = ", ".join(f"#{p['number']}" for p in prs)
    return Decision(False, f"{numbers} merges cleanly: its pull_request run tests the merge ref")


def _gh(*args: str) -> Any:
    out = subprocess.run(["gh", "api", *args], capture_output=True, text=True, check=True)
    return json.loads(out.stdout)


def resolve(
    repo: str,
    branch: str,
    *,
    api: Callable[..., Any] = _gh,
    wait_s: float = 120,
    poll_s: float = 10,
    sleep: Callable[[float], None] = time.sleep,
) -> Decision:
    """Ask GitHub for the branch's pull requests, waiting while `mergeable` is being worked out."""
    owner = repo.split("/", 1)[0]
    listed = gated(api(f"repos/{repo}/pulls?state=open&head={owner}:{urllib.parse.quote(branch, safe='/')}&per_page=100"))
    deadline = time.monotonic() + wait_s
    while True:
        full = [api(f"repos/{repo}/pulls/{p['number']}") for p in listed]
        d = decide(full)
        if not any(p.get("mergeable") is None for p in full) or time.monotonic() >= deadline:
            return d
        sleep(poll_s)


def merge_base(repo: str, base_ref: str, sha: str, *, api: Callable[..., Any] = _gh) -> str:
    return str(api(f"repos/{repo}/compare/{base_ref}...{sha}")["merge_base_commit"]["sha"])


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--repo", required=True)
    ap.add_argument("--branch", required=True)
    ap.add_argument("--sha", required=True)
    a = ap.parse_args(argv)
    d = resolve(a.repo, a.branch)
    base = merge_base(a.repo, d.base_ref, a.sha) if d.run else ""
    print(f"branch head: {d.why}" + (f" (merge-base with {d.base_ref}: {base})" if base else ""))
    lines = [f"run={'true' if d.run else 'false'}", f"base={base}", f"pr={d.pr or ''}"]
    out = os.environ.get("GITHUB_OUTPUT")
    if out:
        with open(out, "a", encoding="utf-8") as f:
            f.write("\n".join(lines) + "\n")
    else:
        print("\n".join(lines))
    return 0


if __name__ == "__main__":
    sys.exit(main())
