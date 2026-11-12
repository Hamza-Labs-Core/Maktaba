#!/usr/bin/env python3
"""A pull request that changes a reference image lists each one, with why (story 60.04).

maktaba.tv's visual references (web/packages/website/e2e/__screenshots__/) and its product
screenshots (web/packages/website/static/shots/, story 60.09) are what the site is compared
against and what it shows. A pull request that adds, changes, renames or removes one needs a
`Reference images:` section in its body listing every such file, each with one line on why;
the reviewing session opens each one before merging. Otherwise this fails, naming the
unlisted files.

    - web/packages/website/e2e/__screenshots__/home-en-360.png: the hero's new copy (60.02)

The body and the file list are read fresh from the API (pulls.get, pulls.listFiles,
paginated), not from the event payload, so a re-run reads the body as it is now; the
workflow (.github/workflows/reference-images.yml) runs on `edited` too, so fixing the body
re-runs it.

    reference_images.py                        in CI: the event's pull request number
                                               (GITHUB_EVENT_PATH), GITHUB_REPOSITORY, GH_TOKEN
    reference_images.py --body F --files F     a body, and the changed paths one a line

Exit 0 when every changed reference is listed with a reason (or none changed), 1 when one is
not, 2 when GitHub could not be asked (the check fails closed).
"""

from __future__ import annotations

import json
import os
import re
import sys
import urllib.error
import urllib.request
from collections.abc import Iterable

REFERENCE_DIRS = (
    "web/packages/website/e2e/__screenshots__/",
    "web/packages/website/static/shots/",
)

# the section's heading: "Reference images:" alone on its line, optionally a Markdown
# heading or bold ("## Reference images:", "**Reference images:**")
_HEADING = re.compile(r"^\s*(?:#{1,6}\s*)?(?:\*\*)?Reference images:?(?:\*\*)?:?\s*$", re.IGNORECASE)
_NEXT_SECTION = re.compile(r"^\s*(?:#{1,6}\s|\*\*[^*]+\*\*\s*$|[A-Z][A-Za-z ]{0,40}:\s*$)")
_ITEM = re.compile(r"^\s*[-*+]\s+(?P<rest>.+)$")
_SEP = re.compile(r"^\s*(?::|—|–|-{1,2}|\(|,)\s*")


def changed_references(files: Iterable[dict[str, str]]) -> list[str]:
    """The reference images among a pull request's files (the API's entries: filename,
    status, previous_filename): added, changed, renamed (both names) or removed."""
    out: list[str] = []
    for f in files:
        for name in (f.get("filename", ""), f.get("previous_filename", "")):
            if name and name.startswith(REFERENCE_DIRS) and name not in out:
                out.append(name)
    return out


def listed(body: str) -> dict[str, str]:
    """The files the body's `Reference images:` section lists, each with its reason ("" when
    it gives none). An item names the file by its repository path, in backticks or not."""
    out: dict[str, str] = {}
    inside = False
    for line in body.replace("\r\n", "\n").split("\n"):
        if _HEADING.match(line):
            inside = True
            continue
        if not inside:
            continue
        item = _ITEM.match(line)
        if not item:
            if line.strip() and _NEXT_SECTION.match(line):
                inside = False
            continue
        rest = item.group("rest").strip()
        m = re.match(r"`([^`]+)`(.*)$", rest) or re.match(r"(\S+?)(?=$|\s|:)(.*)$", rest)
        if not m:
            continue
        path, after = m.group(1).strip(), m.group(2)
        reason = _SEP.sub("", after, count=1).strip().rstrip(")").strip()
        out[path] = reason
    return out


def problems(changed: list[str], body: str) -> list[str]:
    """Why each changed reference fails: not listed, or listed with no reason."""
    have = listed(body)
    out = []
    for f in changed:
        if f not in have:
            out.append(f"{f}: not listed under `Reference images:`")
        elif not have[f]:
            out.append(f"{f}: listed with no reason (one line on why it changed)")
    return out


def _get(url: str, token: str) -> object:
    req = urllib.request.Request(
        url,
        headers={
            "Accept": "application/vnd.github+json",
            "Authorization": f"Bearer {token}",
            "X-GitHub-Api-Version": "2022-11-28",
        },
    )
    with urllib.request.urlopen(req, timeout=30) as resp:  # noqa: S310 - api.github.com
        return json.load(resp)


def github_pull(repo: str, number: int, token: str) -> tuple[str, list[dict[str, str]]]:
    """The pull request's body (pulls.get) and every file (pulls.listFiles, paginated)."""
    api = f"https://api.github.com/repos/{repo}/pulls/{number}"
    pr = _get(api, token)
    assert isinstance(pr, dict)
    files: list[dict[str, str]] = []
    page = 1
    while True:
        batch = _get(f"{api}/files?per_page=100&page={page}", token)
        assert isinstance(batch, list)
        files.extend(batch)
        if len(batch) < 100:
            break
        page += 1
    return pr.get("body") or "", files


def main(argv: list[str]) -> int:
    body_file = files_file = None
    i = 0
    while i < len(argv):
        if argv[i] == "--body" and i + 1 < len(argv):
            body_file = argv[i + 1]
            i += 2
        elif argv[i] == "--files" and i + 1 < len(argv):
            files_file = argv[i + 1]
            i += 2
        else:
            print(f"reference_images.py: unknown argument {argv[i]}", file=sys.stderr)
            return 2
    if body_file is not None or files_file is not None:
        if body_file is None or files_file is None:
            print("reference_images.py: --body and --files go together", file=sys.stderr)
            return 2
        with open(body_file, encoding="utf-8") as fh:
            body = fh.read()
        with open(files_file, encoding="utf-8") as fh:
            files = [{"filename": line.strip()} for line in fh if line.strip()]
    else:
        event_path = os.environ.get("GITHUB_EVENT_PATH", "")
        repo = os.environ.get("GITHUB_REPOSITORY", "")
        token = os.environ.get("GH_TOKEN", "")
        try:
            with open(event_path, encoding="utf-8") as fh:
                number = int(json.load(fh)["pull_request"]["number"])
        except (OSError, KeyError, TypeError, ValueError):
            print("reference_images.py: not a pull request event; nothing to check")
            return 0
        if not repo or not token:
            print("::error::reference_images.py needs GITHUB_REPOSITORY and GH_TOKEN")
            return 2
        try:
            body, files = github_pull(repo, number, token)
        except (urllib.error.URLError, OSError, ValueError, AssertionError) as exc:
            print(f"::error::could not read pull request #{number} from GitHub: {exc}")
            return 2
    changed = changed_references(files)
    if not changed:
        print("no reference image changed")
        return 0
    bad = problems(changed, body)
    for p in bad:
        print(f"::error::{p}")
    if bad:
        print(
            "Add a `Reference images:` section to the pull request's body, one line per changed "
            "file: `- <path>: <why it changed>`. The reviewer opens each one before merging."
        )
        return 1
    print(f"every changed reference image is listed with a reason ({len(changed)})")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
