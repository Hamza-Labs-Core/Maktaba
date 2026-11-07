#!/usr/bin/env python3
"""A pull request's body must not close another open pull request.

GitHub's closing keywords (close, closes, closed, fix, fixes, fixed, resolve, resolves,
resolved) before a reference to this repository's issue or pull request close it when
this pull request merges. Closing an issue is what they are for; but they close a pull
request too, attributed to whoever merged, with no trace of why. A body that wrote
"resolved" before an open PR's number closed it the moment its own PR merged (the trail's
d20261107-closing-keyword-closed-a-pr). So this check fails when a closing keyword names a
number that is an open pull request; issues stay allowed. To mention a PR, write "the
merge for PR 795" or "(#795)".

    pr_closing_refs.py              in CI: the event's pull request (GITHUB_EVENT_PATH),
                                    this repository (GITHUB_REPOSITORY), GH_TOKEN
    pr_closing_refs.py --body FILE  a body from a file (with GITHUB_REPOSITORY, GH_TOKEN)

Exit 0 when nothing closes an open PR (or not a pull request event), 1 when something
does, 2 when GitHub could not be asked (the check fails closed).
"""

from __future__ import annotations

import json
import os
import re
import sys
import urllib.error
import urllib.request
from collections.abc import Callable

KEYWORDS = r"close[sd]?|fix(?:e[sd])?|resolve[sd]?"


def closing_refs(body: str, repo: str) -> list[int]:
    """The numbers a closing keyword names in body, in this repository: #N, owner/repo#N
    and this repository's issue or pull request URLs. In order, each once."""
    owner_repo = re.escape(repo)
    target = (
        rf"(?:{owner_repo})?#(?P<n>\d+)"
        rf"|https://github\.com/{owner_repo}/(?:issues|pull)/(?P<u>\d+)"
    )
    pattern = re.compile(rf"\b(?:{KEYWORDS})\b:?\s+(?:{target})\b", re.IGNORECASE)
    out: list[int] = []
    for m in pattern.finditer(body):
        n = int(m.group("n") or m.group("u"))
        if n not in out:
            out.append(n)
    return out


def open_pull_requests(refs: list[int], is_open_pr: Callable[[int], bool]) -> list[int]:
    """The refs that are open pull requests (is_open_pr asks GitHub)."""
    return [n for n in refs if is_open_pr(n)]


def github_is_open_pr(repo: str, token: str) -> Callable[[int], bool]:
    """Asks GitHub whether #n is an open pull request: /pulls/n answers 404 for an issue."""

    def ask(n: int) -> bool:
        req = urllib.request.Request(
            f"https://api.github.com/repos/{repo}/pulls/{n}",
            headers={
                "Accept": "application/vnd.github+json",
                "Authorization": f"Bearer {token}",
                "X-GitHub-Api-Version": "2022-11-28",
            },
        )
        try:
            with urllib.request.urlopen(req, timeout=20) as resp:  # noqa: S310 - api.github.com
                pr = json.load(resp)
        except urllib.error.HTTPError as exc:
            if exc.code == 404:
                return False
            raise
        return bool(pr.get("state") == "open")

    return ask


def main(argv: list[str]) -> int:
    repo = os.environ.get("GITHUB_REPOSITORY", "")
    token = os.environ.get("GH_TOKEN") or os.environ.get("GITHUB_TOKEN") or ""
    if len(argv) == 3 and argv[1] == "--body":
        with open(argv[2], encoding="utf-8") as fh:
            body, own = fh.read(), 0
    else:
        with open(os.environ["GITHUB_EVENT_PATH"], encoding="utf-8") as fh:
            event = json.load(fh)
        pr = event.get("pull_request")
        if not pr:
            print("pr_closing_refs: not a pull request event: nothing to check")
            return 0
        body, own = pr.get("body") or "", int(pr.get("number") or 0)
    if not repo or not token:
        print("pr_closing_refs: GITHUB_REPOSITORY and GH_TOKEN are needed", file=sys.stderr)
        return 2
    refs = [n for n in closing_refs(body, repo) if n != own]
    try:
        bad = open_pull_requests(refs, github_is_open_pr(repo, token))
    except (urllib.error.URLError, OSError, ValueError) as exc:
        print(f"pr_closing_refs: could not ask GitHub: {exc}", file=sys.stderr)
        return 2
    if bad:
        for n in bad:
            print(
                f"::error::the body closes #{n}, an open pull request, when this merges "
                f'(a closing keyword before #{n}). To mention it, write "the merge for PR {n}" '
                f'or "(#{n})".'
            )
        return 1
    print(f"pr_closing_refs: no open pull request closed by this body ({len(refs)} closing refs)")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
