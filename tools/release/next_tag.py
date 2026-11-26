#!/usr/bin/env python3
"""Which commit the release session tags, and with which tag.

The release session (a scheduled agent session, not a workflow: docs/updates.md, "How a release
is made") runs this. It hands this script main's green runs of both workflows, as `gh run list
--json headSha,event,displayTitle` writes them (`--ci-runs FILE --rehearsal-runs FILE`), or the
commits both passed on (`--green FILE`, one SHA a line), with broadcast-rehearsal.yml's runs
(`--broadcast-runs FILE`; without it, it fetches them itself with gh: fetch_broadcast_runs),
and it prints (key=value lines, as for
$GITHUB_OUTPUT):

    tag=vX.Y.Z      the next patch tag ('' when nothing is tagged)
    sha=<commit>    the commit it goes on
    reason=<text>   why nothing is tagged (or what is)

A commit is green when a ci.yml push run passed on it and an update-rehearsal.yml run of main
passed on it. Since 2026-11-07 main's rehearsal is a workflow_run started by CI's green push
run, and such a run's headSha is main's head when it started, not the commit it rehearsed: it
is named "Update rehearsal of <sha>" (REHEARSAL_TITLE), and that sha is its commit. A rehearsal
push run (main's before 2026-11-07) counts for its headSha.

A green commit is tag-eligible only when:
  - the broadcast rehearsal's nightly passed on its broadcast code (story 50.29 §4,
    broadcast_verdict; the runs from `--broadcast-runs FILE`, or fetched with gh:
    fetch_broadcast_runs, no tag when that fails): among the nightly runs of
    broadcast-rehearsal.yml on a commit whose broadcast code (BROADCAST_CODE, the workflow's
    pull-request paths) is the same as this commit's, the newest one that finished passed. A
    commit with no such run is not eligible (fail closed); one whose broadcast code has not
    changed since a green nightly is.
  - the newest finished nightly CVE gate run (cve-gate-nightly.yml, story 23.7 follow-up
    #1472; `--cve-runs FILE`, or fetched with gh: fetch_cve_runs, no tag when that fails) on
    this commit — or on a main commit that differs from it only in trail files — did not fail.
    A newly merged commit with no nightly CVE gate run yet stays eligible because its CI push
    run already passed `cve-gate`; if a later nightly fails on an unchanged main, the commit is
    held back until main fixes or suppresses the advisory.

The commit is the newest green and eligible one on main (first-parent order: main's own
commits, newest first). It is tagged only when:
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
import json
import re
import subprocess
import sys
from collections.abc import Callable, Iterable, Mapping, Sequence
from dataclasses import dataclass
from typing import Any

# The paths a push to main may change without a CI run or a rehearsal: ci.yml's and
# update-rehearsal.yml's push paths-ignore and .trail.toml's [trail_only] paths, which `trail
# is-trail-only` (agent-trail) reads (tools/ci/tests/test_next_tag.py keeps the four the same).
TRAIL_ONLY: tuple[str, ...] = ("docs/claude-trail.md", "docs/trail/**")

STABLE = re.compile(r"^v(\d+)\.(\d+)\.(\d+)$")

# update-rehearsal.yml's run-name for main's run: the commit it rehearsed (a workflow_run's own
# headSha is main's head when it started). test_next_tag.py keeps the workflow's the same.
REHEARSAL_TITLE = "Update rehearsal of "
_TITLED = re.compile("^" + re.escape(REHEARSAL_TITLE) + r"([0-9a-f]{40})$")


# broadcast-rehearsal.yml's pull-request paths: the broadcast code a nightly run rehearses
# (test_next_tag.py keeps the two the same). A trailing /** is the folder and everything in it.
BROADCAST_CODE: tuple[str, ...] = (
    "web/packages/broadcast-edge/**",
    "web/packages/tvsite/src/sign.ts",
    "cloud/internal/broadcast/**",
    "streaming/internal/broadcast/**",
    "api/internal/broadcast/**",
    "streaming/internal/relay/**",
    "streaming/cmd/rehearsal-broadcast-home/**",
    "streaming/go.mod",
    "streaming/go.sum",
    "tools/broadcast-viewers/**",
    "tools/rehearsal-cloud/broadcast/**",
    # broadcast-observe (50.28): the cloud's monitor, the alert rules and routes, the canary
    # probes, status-export and the status page's fold
    "cloud/cmd/rehearsal-broadcast-monitor/**",
    "deploy/cloud-ha/monitoring/alerts.yml",
    "deploy/cloud-ha/monitoring/alertmanager.yml",
    "deploy/cloud-ha/monitoring/broadcast-probe.sh",
    "deploy/cloud-ha/monitoring/probe.sh",
    "deploy/cloud-ha/bin/status-export",
    "status/lib.mjs",
    "status/update.mjs",
    "status/config.json",
    ".github/workflows/broadcast-rehearsal.yml",
)

# broadcast-rehearsal.yml's run-name for a nightly-set run (the schedule's, or a hand run with
# `set: nightly`): the commit it rehearsed. test_next_tag.py keeps the workflow's the same.
BROADCAST_NIGHTLY_TITLE = "Broadcast rehearsal nightly of "
_NIGHTLY_TITLED = re.compile("^" + re.escape(BROADCAST_NIGHTLY_TITLE) + r"([0-9a-f]{40})$")

# A run that ended so is no result either way (stopped, or never ran): it is passed over.
NO_RESULT = frozenset({"cancelled", "skipped", "neutral", "stale", ""})


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


def rehearsed_commit(run: Mapping[str, Any]) -> str | None:
    """The commit an update-rehearsal.yml run of main rehearsed: a workflow_run's is in its
    name; a push run's is its headSha; any other run (a pull request's, the nightly one, a hand
    run) is not main's."""
    event = run.get("event")
    if event == "workflow_run":
        m = _TITLED.match(str(run.get("displayTitle", "")))
        return m[1] if m else None
    if event == "push":
        return str(run["headSha"])
    return None


def green_commits(ci_runs: Iterable[Mapping[str, Any]], rehearsal_runs: Iterable[Mapping[str, Any]]) -> set[str]:
    """The commits a passed ci.yml push run and a passed rehearsal of main share (runs as `gh
    run list --json headSha,event,conclusion,displayTitle` gives them; a run with no conclusion
    field counts as passed: the list is of `--status success` runs)."""

    def passed(run: Mapping[str, Any]) -> bool:
        return run.get("conclusion", "success") == "success"

    ci = {str(r["headSha"]) for r in ci_runs if passed(r) and r.get("event", "push") == "push"}
    rehearsed = {c for r in rehearsal_runs if passed(r) and (c := rehearsed_commit(r))}
    return ci & rehearsed


def nightly_commit(run: Mapping[str, Any]) -> str | None:
    """The commit a broadcast-rehearsal.yml run rehearsed with the nightly set: the one in its
    name, or a scheduled run's headSha (a schedule runs on main's head; runs before the
    run-name have no name). Any other run (a pull request's or a push's short set, or a hand
    run on another branch) is none."""
    # Only a schedule's run or a hand run counts: a pull request's run (a fork's too, whose
    # branch may be called main) runs the PR's own workflow file and so can carry any name. A
    # dispatch needs write access, and its name must be its own headSha (both run on the ref's
    # head, github.sha).
    if run.get("headBranch", "main") != "main":
        return None
    event = run.get("event")
    head = str(run.get("headSha", ""))
    if event == "schedule":
        return head
    if event == "workflow_dispatch":
        m = _NIGHTLY_TITLED.match(str(run.get("displayTitle", "")))
        return m[1] if m and m[1] == head else None
    return None


def nightly_results(runs: Iterable[Mapping[str, Any]]) -> list[tuple[str, bool]]:
    """The nightly runs that finished with a result, newest first, as (commit, passed). runs is
    `gh run list --json headSha,headBranch,event,status,conclusion,displayTitle,createdAt` (every status:
    a red run counts). A run still going, or one cancelled or skipped, is passed over."""
    done: list[tuple[str, str, bool]] = []
    for r in runs:
        commit = nightly_commit(r)
        if commit is None or r.get("status", "completed") != "completed":
            continue
        conclusion = str(r.get("conclusion") or "")
        if conclusion in NO_RESULT:
            continue
        done.append((str(r.get("createdAt", "")), commit, conclusion == "success"))
    # newest first; sorted() is stable, so runs without createdAt keep gh's own (newest-first) order
    done = sorted(done, key=lambda d: d[0], reverse=True)
    return [(c, ok) for _, c, ok in done]


def broadcast_verdict(sha: str, nightlies: Sequence[tuple[str, bool]], code: Callable[[str], str | None]) -> str | None:
    """Why sha is not tag-eligible on the broadcast rehearsal, or None when it is.

    nightlies: nightly_results(), newest first. code(c): c's broadcast code (any value equal for
    two commits whose BROADCAST_CODE files are the same), None when c is not in the checkout.
    The verdict is the newest nightly on a commit with sha's broadcast code: passed, eligible;
    failed, not. No such nightly: not eligible.
    """
    mine = code(sha)
    if mine is None:
        return f"{sha}'s broadcast code is not in the checkout"
    for commit, passed in nightlies:
        if code(commit) != mine:
            continue
        if passed:
            return None
        return f"the newest broadcast rehearsal nightly on {sha}'s broadcast code (run on {commit}) failed"
    return f"no broadcast rehearsal nightly ran on {sha}'s broadcast code"


def cve_nightly_results(runs: Iterable[Mapping[str, Any]]) -> list[tuple[str, bool]]:
    """The finished nightly CVE gate runs on main (schedule or workflow_dispatch), newest first,
    as (commit, passed). A run still going, or one cancelled or skipped, is passed over."""
    done: list[tuple[str, str, bool]] = []
    for r in runs:
        if r.get("headBranch", "main") != "main":
            continue
        if r.get("event") not in CVE_EVENTS:
            continue
        if r.get("status", "completed") != "completed":
            continue
        conclusion = str(r.get("conclusion") or "")
        if conclusion in NO_RESULT:
            continue
        commit = str(r.get("headSha", ""))
        if not commit:
            continue
        done.append((str(r.get("createdAt", "")), commit, conclusion == "success"))
    done = sorted(done, key=lambda d: d[0], reverse=True)
    return [(c, ok) for _, c, ok in done]


def cve_verdict(
    sha: str,
    nightlies: Sequence[tuple[str, bool]],
    same_non_trail: Callable[[str, str], bool],
) -> str | None:
    """Why sha is not tag-eligible on the nightly CVE gate, or None when it is.

    Checks the newest finished nightly CVE gate run on sha or on a commit that differs from sha
    only in trail files. If none has run yet, sha stays eligible (its CI push run already
    passed cve-gate); if the newest one failed, sha is held back until fixed or re-run green.
    """
    for commit, passed in nightlies:
        if commit != sha and not same_non_trail(sha, commit):
            continue
        if passed:
            return None
        return f"the newest nightly CVE gate run on {sha} (run on {commit}) failed"
    return None


def newest_eligible(main_order: Sequence[str], green: Iterable[str], verdict: Callable[[str], str | None]) -> tuple[str | None, list[str]]:
    """The first commit of main_order (newest first) that is green and whose verdict is None,
    and why each newer green commit was passed over."""
    ok = set(green)
    passed_over: list[str] = []
    for c in main_order:
        if c not in ok:
            continue
        why = verdict(c)
        if why is None:
            return c, passed_over
        passed_over.append(why)
    return None, passed_over


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


def _broadcast_pathspecs() -> list[str]:
    return [p[:-2] if p.endswith("/**") else p for p in BROADCAST_CODE]


def git_broadcast_code(sha: str) -> str | None:
    """sha's BROADCAST_CODE files and their blobs (equal for two commits with the same code)."""
    r = subprocess.run(["git", "ls-tree", "-r", sha, "--", *_broadcast_pathspecs()], check=False, capture_output=True, text=True)
    return r.stdout if r.returncode == 0 else None


def git_same_non_trail(a: str, b: str) -> bool:
    """Whether commits a and b differ only in trail-only files (or not at all)."""
    if a == b:
        return True
    r = subprocess.run(["git", "diff", "--name-only", "--no-renames", a, b], check=False, capture_output=True, text=True)
    if r.returncode != 0:
        return False
    return all(trail_only(p) for p in r.stdout.splitlines() if p)


# The broadcast rehearsal's runs the gate reads, when no --broadcast-runs file is given: only a
# schedule's or a hand run counts (nightly_commit), so those two events are asked for, each
# with a month and more of history (a push run on every merge would crowd a mixed list out).
BROADCAST_EVENTS = ("schedule", "workflow_dispatch")
BROADCAST_LIMIT = 60
BROADCAST_FIELDS = "headSha,headBranch,event,status,conclusion,displayTitle,createdAt"

CVE_WORKFLOW = "cve-gate-nightly.yml"
CVE_EVENTS = ("schedule", "workflow_dispatch")
CVE_LIMIT = 30
CVE_FIELDS = "headSha,headBranch,event,status,conclusion,createdAt"


class FetchError(Exception):
    """A workflow's runs could not be had: no tag (fail closed)."""


Runner = Callable[[list[str]], "subprocess.CompletedProcess[str]"]


def _run(cmd: list[str]) -> subprocess.CompletedProcess[str]:
    return subprocess.run(cmd, check=False, capture_output=True, text=True, timeout=120)


def _fetch_workflow_runs(
    workflow: str,
    events: Sequence[str],
    limit: int,
    fields: str,
    run: Runner = _run,
) -> list[dict[str, Any]]:
    runs: list[dict[str, Any]] = []
    for event in events:
        cmd = [
            "gh", "run", "list", "--workflow", workflow, "--branch", "main",
            "--event", event, "--limit", str(limit), "--json", fields,
        ]  # fmt: skip
        try:
            r = run(cmd)
        except (OSError, subprocess.SubprocessError) as e:
            raise FetchError(f"gh could not run ({e})") from e
        if r.returncode != 0:
            detail = (r.stderr or r.stdout or "").strip().splitlines()
            raise FetchError(f"gh run list failed (exit {r.returncode}{': ' + detail[-1] if detail else ''})")
        try:
            got = json.loads(r.stdout)
        except ValueError as e:
            raise FetchError(f"gh run list printed no JSON ({e})") from e
        if not isinstance(got, list) or not all(isinstance(x, dict) for x in got):
            raise FetchError("gh run list printed JSON that is not a list of runs")
        runs.extend(got)
    return runs


def fetch_broadcast_runs(run: Runner = _run) -> list[dict[str, Any]]:
    """broadcast-rehearsal.yml's runs on main, every status, as `gh run list --json` gives
    them. Raises FetchError when gh is missing, fails, or prints something that is not a list."""
    return _fetch_workflow_runs("broadcast-rehearsal.yml", BROADCAST_EVENTS, BROADCAST_LIMIT, BROADCAST_FIELDS, run)


def fetch_cve_runs(run: Runner = _run) -> list[dict[str, Any]]:
    """cve-gate-nightly.yml's runs on main, every status, as `gh run list --json` gives
    them. Raises FetchError when gh is missing, fails, or prints something that is not a list."""
    return _fetch_workflow_runs(CVE_WORKFLOW, CVE_EVENTS, CVE_LIMIT, CVE_FIELDS, run)


def load_broadcast_runs(path: str) -> list[dict[str, Any]]:
    try:
        with open(path, encoding="utf-8") as f:
            got = json.load(f)
    except (OSError, ValueError) as e:
        raise FetchError(f"{path}: {e}") from e
    if not isinstance(got, list) or not all(isinstance(x, dict) for x in got):
        raise FetchError(f"{path}: not a list of runs")
    return got


def main(argv: Sequence[str] | None = None, run: Runner = _run) -> int:
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--green", help="a file: the commits CI and the rehearsal both passed on")
    ap.add_argument("--ci-runs", help="a file: ci.yml's passed push runs on main (gh run list --json)")
    ap.add_argument("--rehearsal-runs", help="a file: update-rehearsal.yml's passed runs of main (gh run list --json)")
    ap.add_argument(
        "--broadcast-runs",
        help="a file: broadcast-rehearsal.yml's runs, every status (gh run list --json); "
        "without it they are fetched with gh. A commit is eligible only on a green nightly",
    )
    ap.add_argument(
        "--cve-runs",
        help="a file: cve-gate-nightly.yml's runs, every status (gh run list --json); "
        "without it they are fetched with gh. A failed newest nightly on the commit blocks the tag",
    )
    ap.add_argument("--main", default="origin/main", help="main's ref (default origin/main)")
    ap.add_argument("--depth", type=int, default=200, help="how many of main's commits to look through")
    args = ap.parse_args(argv)

    if args.green and not (args.ci_runs or args.rehearsal_runs):
        with open(args.green, encoding="utf-8") as f:
            green: Iterable[str] = [line.strip() for line in f if line.strip()]
    elif args.ci_runs and args.rehearsal_runs and not args.green:
        with open(args.ci_runs, encoding="utf-8") as f:
            ci_runs = json.load(f)
        with open(args.rehearsal_runs, encoding="utf-8") as f:
            rehearsal_runs = json.load(f)
        green = green_commits(ci_runs, rehearsal_runs)
    else:
        ap.error("give --green, or both --ci-runs and --rehearsal-runs")
    order = git("rev-list", "--first-parent", f"--max-count={args.depth}", args.main).split()
    try:
        runs = load_broadcast_runs(args.broadcast_runs) if args.broadcast_runs else fetch_broadcast_runs(run)
    except FetchError as e:
        print("tag=")
        print("sha=")
        print(f"reason=no tag: the broadcast rehearsal's runs could not be read: {e}")
        return 0
    try:
        cve_runs = load_broadcast_runs(args.cve_runs) if args.cve_runs else fetch_cve_runs(run)
    except FetchError as e:
        print("tag=")
        print("sha=")
        print(f"reason=no tag: the nightly CVE gate's runs could not be read: {e}")
        return 0
    nightlies = nightly_results(runs)
    cve_nightlies = cve_nightly_results(cve_runs)
    codes: dict[str, str | None] = {}

    def code(c: str) -> str | None:
        if c not in codes:
            codes[c] = git_broadcast_code(c)
        return codes[c]

    def verdict(c: str) -> str | None:
        return broadcast_verdict(c, nightlies, code) or cve_verdict(c, cve_nightlies, git_same_non_trail)

    sha, passed_over = newest_eligible(order, green, verdict)
    if sha is None:
        why = f"no commit of {args.main}'s newest {args.depth} passed CI, the update rehearsal, the broadcast rehearsal and the nightly CVE gate"
        d = Decision("", "", "; ".join([why, *passed_over]))
    else:
        d = decide(sha, git_tags(), git_is_ancestor, git_changed)
        if passed_over:
            d = Decision(d.tag, d.sha, f"{d.reason} (newer green commits passed over: {'; '.join(passed_over)})")
    print(f"tag={d.tag}")
    print(f"sha={d.sha}")
    print(f"reason={d.reason}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
