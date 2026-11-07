#!/usr/bin/env python3
"""Which commit the release session tags, and with which tag.

The release session (a scheduled agent session, not a workflow: docs/updates.md, "How a release
is made") runs this. It hands this script the commits on main whose ci.yml and
update-rehearsal.yml push runs both succeeded (`--green FILE`, one SHA a line) and it prints
(key=value lines, as for $GITHUB_OUTPUT):

    tag=vX.Y.Z      the next patch tag ('' when nothing is tagged)
    sha=<commit>    the commit it goes on
    reason=<text>   why nothing is tagged (or what is)

The commit is the newest green one on main (first-parent order: main's own commits, newest
first). It is tagged only when:
  - it has no v* tag of its own (a session, or another run, tagged it already);
  - no newer commit has a v* tag (an older commit is never tagged after a newer one: a tag
    below a released one would be refused by the tag guard, and would skip nothing anyway);
  - it contains the highest vX.Y.Z tag's commit (the guard's history rule);
  - its change since that tag is not only the Claude trail (the paths ci.yml and
    update-rehearsal.yml ignore on a push: such a commit is no release).
The tag is the highest vX.Y.Z (pre-releases are not counted) plus one patch.

Run from a checkout with main and the tags fetched (`--main REF`, default origin/main).
"""

from __future__ import annotations

import argparse
import re
import subprocess
import sys
from collections.abc import Callable, Iterable, Sequence
from dataclasses import dataclass

# The paths a push to main may change without a CI run or a rehearsal: ci.yml's and
# update-rehearsal.yml's push paths-ignore and .trail.toml's [trail_only] paths, which `trail
# is-trail-only` (agent-trail) reads (tools/ci/tests/test_next_tag.py keeps the four the same).
TRAIL_ONLY: tuple[str, ...] = ("docs/claude-trail.md", "docs/trail/**")

STABLE = re.compile(r"^v(\d+)\.(\d+)\.(\d+)$")


def trail_only(path: str) -> bool:
    """path is one of TRAIL_ONLY's (a trailing /** is the folder and everything in it)."""
    for pattern in TRAIL_ONLY:
        if pattern.endswith("/**"):
            if path.startswith(pattern[:-2]):
                return True
        elif path == pattern:
            return True
    return False


def version(tag: str) -> tuple[int, int, int] | None:
    m = STABLE.match(tag)
    return (int(m[1]), int(m[2]), int(m[3])) if m else None


def next_patch(tags: Iterable[str]) -> str:
    """The highest vX.Y.Z plus one patch."""
    versions = [v for v in (version(t) for t in tags) if v]
    if not versions:
        raise ValueError("no vX.Y.Z tag to count from")
    x, y, z = max(versions)
    return f"v{x}.{y}.{z + 1}"


@dataclass(frozen=True)
class Decision:
    tag: str  # '' when nothing is tagged
    sha: str
    reason: str


def newest_green(main_order: Sequence[str], green: Iterable[str]) -> str | None:
    """The first commit of main_order (newest first) that is green."""
    ok = set(green)
    return next((c for c in main_order if c in ok), None)


def decide(
    sha: str,
    tags: dict[str, str],
    is_ancestor: Callable[[str, str], bool],
    changed: Callable[[str, str], list[str]],
) -> Decision:
    """Whether sha gets the next patch tag.

    tags: every v* tag's name to its commit. is_ancestor(a, b): a is in b's history (a == b
    counts). changed(a, b): the paths that differ between a and b.
    """
    own = sorted(t for t, c in tags.items() if c == sha)
    if own:
        return Decision("", sha, f"{sha} is tagged already ({', '.join(own)})")
    newer = sorted(t for t, c in tags.items() if is_ancestor(sha, c))
    if newer:
        return Decision("", sha, f"a newer commit is tagged ({', '.join(newer)}): {sha} is not tagged after it")
    stable = {t: c for t, c in tags.items() if version(t)}
    if not stable:
        raise ValueError("no vX.Y.Z tag to count from")
    highest = max(stable, key=lambda t: version(t) or (0, 0, 0))
    last = stable[highest]
    if not is_ancestor(last, sha):
        return Decision("", sha, f"{sha} does not contain {highest} ({last}), the highest tag")
    paths = changed(last, sha)
    if not paths or all(trail_only(p) for p in paths):
        return Decision("", sha, f"{sha} changes only the Claude trail since {highest}: no release")
    tag = next_patch(tags)
    if tag in tags:
        raise ValueError(f"{tag} exists already, on {tags[tag]}")
    return Decision(tag, sha, f"{tag} on {sha} ({len(paths)} path(s) changed since {highest})")


# --- git --------------------------------------------------------------------------------------


def git(*args: str) -> str:
    return subprocess.run(["git", *args], check=True, capture_output=True, text=True).stdout


def git_tags() -> dict[str, str]:
    out = git("for-each-ref", "--format=%(refname:short) %(*objectname) %(objectname)", "refs/tags/v*")
    tags: dict[str, str] = {}
    for line in out.splitlines():
        parts = line.split()
        # an annotated tag's commit is %(*objectname); a lightweight one's is %(objectname)
        name, commit = parts[0], parts[1] if len(parts) == 3 else parts[-1]
        tags[name] = commit
    return tags


def git_is_ancestor(a: str, b: str) -> bool:
    return subprocess.run(["git", "merge-base", "--is-ancestor", a, b], check=False, capture_output=True).returncode == 0


def git_changed(a: str, b: str) -> list[str]:
    return [p for p in git("diff", "--name-only", "--no-renames", a, b).splitlines() if p]


def main(argv: Sequence[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--green", required=True, help="a file: the commits CI and the rehearsal both passed on")
    ap.add_argument("--main", default="origin/main", help="main's ref (default origin/main)")
    ap.add_argument("--depth", type=int, default=200, help="how many of main's commits to look through")
    args = ap.parse_args(argv)

    with open(args.green, encoding="utf-8") as f:
        green = [line.strip() for line in f if line.strip()]
    order = git("rev-list", "--first-parent", f"--max-count={args.depth}", args.main).split()
    sha = newest_green(order, green)
    if sha is None:
        d = Decision("", "", f"no commit of {args.main}'s newest {args.depth} passed both CI and the rehearsal")
    else:
        d = decide(sha, git_tags(), git_is_ancestor, git_changed)
    print(f"tag={d.tag}")
    print(f"sha={d.sha}")
    print(f"reason={d.reason}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
