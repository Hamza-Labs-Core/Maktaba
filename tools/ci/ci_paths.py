#!/usr/bin/env python3
"""CI runs what a change touches: the guard over ci.yml's path filters, and a simulator.

.github/ci-paths.yml holds the path filters ci.yml's `changes` job evaluates on a pull
request; each job's `if:` (and the inputs it hands its reusable workflow) reads them as
`needs.changes.outputs.<filter>`. On a push every filter is true: main gets the full run.

    ci_paths.py guard               # fails on a job that could silently never run
    ci_paths.py which FILE...       # the jobs a pull request changing FILEs runs
    ci_paths.py which --push        # the jobs a push runs

The guard (run in the lint job, on every pull request) checks that:

- every filter's globs are ones paths-filter and this script read alike;
- the changes job has one output per filter, true on a push;
- every job is gated by filters (and runs when any one of them alone is true, the jobs it
  needs skipped or not), or is marked in ci.yml with a `# ci-paths:` comment in its block:
  `always-run` (runs on a pull request that matches no filter), `push-only`, `pr-only`
  (not on a push), `not-a-gate` (not in ci-success's needs);
- every filter is some job's; every job but the not-a-gate ones is in ci-success's needs;
- on a push every job runs but the pr-only ones;
- every tracked file is matched by a filter or listed in NOT_IN_CI below.

The simulator evaluates the jobs' real `if:` and `with:` expressions (a subset of GitHub's
expression language: `||`, `&&`, `!`, `==`, `!=`, parentheses, strings, contexts and the
status functions), each job assumed to pass when it runs.
"""

from __future__ import annotations

import argparse
import re
import subprocess
import sys
from collections.abc import Iterable, Mapping, Sequence
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

import yaml

ROOT = Path(__file__).resolve().parents[2]
CI_YML = ROOT / ".github" / "workflows" / "ci.yml"
FILTERS_YML = ROOT / ".github" / "ci-paths.yml"
FILTERS_REF = ".github/ci-paths.yml"

# Tracked files no CI job builds or tests, or that only an always-run job reads. A file in
# neither a filter nor here fails the guard, so a new folder is given its jobs.
NOT_IN_CI: dict[str, str] = {
    "docs/**": "documentation (the docs tests read are in the pipeline's filter)",
    "specs/**": "specifications (likewise)",
    "*.md": "the repository's own README, CHANGELOG, CLAUDE, CONTRIBUTING",
    "LICENSE": "the licence",
    ".claude/**": "Claude Code's project settings",
    ".editorconfig": "editor settings",
    "**/.gitignore": "git (a folder's own is matched by its folder's filters too)",
    ".env.example": "an example for developers",
    ".envrc.example": "an example for developers",
    ".pre-commit-config.yaml": "local hooks",
    "apps/README.md": "documentation",
    "apps/tv/README.md": "documentation",
    "apps/mobile/**": "the Capacitor shell: built by mobile-release.yml on a tag",
    ".github/CODEOWNERS": "review routing",
    ".github/PULL_REQUEST_TEMPLATE.md": "the template (pr-body-check reads the body itself)",
    ".github/labeler.yml": "labeler.yml's own config",
    ".github/workflows/labeler.yml": "its own workflow",
    ".github/workflows/release.yml": "runs on a tag",
    ".github/workflows/mobile-release.yml": "runs on a tag",
    ".github/workflows/tv-release.yml": "runs on a tag",
    ".github/workflows/web-release.yml": "runs on a tag",
    ".github/workflows/testflight.yml": "runs on a tag",
    ".github/workflows/packages.yml": "runs after a release",
    ".github/workflows/relay.yml": "its own workflow (cloud relay)",
    ".github/workflows/trusted-root.yml": "its own scheduled workflow",
    ".github/workflows/update-rehearsal.yml": "its own workflow and path filter",
    ".github/workflows/_tag-guard.yml": "the tag workflows' first job",
    ".github/workflows/_pr-body-check.yml": "pr-body-check runs on every pull request",
    ".github/workflows/coverage-map.yml": "runs after CI on main (the affected-tests map)",
    ".githooks/**": "the pre-push hook: the affected-tests job shellchecks it on every pull request",
    "tools/ci/ci_paths.py": "this guard: the lint job runs it on every pull request",
    "tools/ci/apple_player_scope.py": "run by the changes job on every pull request",
    "tools/ci/tests/**": "the guard's tests: the lint job runs them on every pull request",
    "deploy/cloud-relay/**": "relay.yml",
    "deploy/github/**": "branch protection as Terraform, applied by hand",
    "deploy/observability/**": "Prometheus config, not built",
    "deploy/server.toml.example": "an example config",
    "tools/rehearsal/**": "update-rehearsal.yml",
    "tools/dev/**": "the dev stack's helpers",
    "tools/icons/**": "makes the icons by hand",
    "tools/tv/**": "a spike's notes",
    "tools/check-prereqs.sh": "make prereqs, run by hand",
    "tools/sbom.sh": "make sbom, run by hand",
    "tools/verify-reproducibility.sh": "make verify-reproducibility, run by hand",
    "tools/image-size-guard.sh": "make image-size-guard, run by hand",
}

MARKERS = {"always-run", "push-only", "pr-only", "not-a-gate"}
# The Claude trail: a pull request that changes only this file runs no job (the owner,
# 2026-11-02: "Update trail on its own PRs, no ci no test"). The changes job's `repo` output
# (its own paths-filter step, where every glob must match) is true when any other file
# changed, and an always-run job is gated by it. No filter may match the trail, so `repo` is
# true whenever a filter is.
TRAIL = "docs/claude-trail.md"
REPO = "repo"
REPO_FILTERS = {REPO: [f"!{TRAIL}"]}
GATE = "ci-success"
CHANGES = "changes"
# a pattern paths-filter (picomatch) and _glob read alike
_SAFE_GLOB = re.compile(r"[A-Za-z0-9_.*/ @+-]+")
_OUTPUT_REF = re.compile(r"needs\.changes\.outputs\.([A-Za-z0-9_-]+)")


# ---- the filters ------------------------------------------------------------------------


def _flatten(v: Any) -> list[str]:
    if isinstance(v, list):
        return [p for x in v for p in _flatten(x)]
    if isinstance(v, str):
        return [v]
    raise ValueError(f"a filter entry that is not a glob: {v!r}")


def load_filters(text: str | None = None) -> dict[str, list[str]]:
    """The filters of ci-paths.yml, each its globs (the groups' anchors flattened)."""
    raw = yaml.safe_load(FILTERS_YML.read_text(encoding="utf-8") if text is None else text)
    if not isinstance(raw, dict):
        raise ValueError("ci-paths.yml is not a mapping of filters")
    return {str(k): _flatten(v) for k, v in raw.items()}


def job_filters(filters: Mapping[str, list[str]]) -> dict[str, list[str]]:
    return {k: v for k, v in filters.items() if not k.startswith("_")}


def _glob(pattern: str) -> re.Pattern[str]:
    rx, i = "", 0
    while i < len(pattern):
        if pattern.startswith("**/", i):
            rx, i = rx + "(?:.*/)?", i + 3
        elif pattern.startswith("**", i):
            rx, i = rx + ".*", i + 2
        elif pattern[i] == "*":
            rx, i = rx + "[^/]*", i + 1
        elif pattern[i] == "?":
            rx, i = rx + "[^/]", i + 1
        else:
            rx, i = rx + re.escape(pattern[i]), i + 1
    return re.compile(rx)


def matches(pattern: str, path: str) -> bool:
    return _glob(pattern).fullmatch(path) is not None


def matched(filters: Mapping[str, list[str]], files: Iterable[str]) -> set[str]:
    """The filters a change of files makes true."""
    files = list(files)
    return {name for name, pats in filters.items() if any(matches(p, f) for p in pats for f in files)}


# ---- GitHub expressions -----------------------------------------------------------------

_TOKEN = re.compile(
    r"\s*(?:(?P<op>\|\||&&|==|!=|!|\(|\)|,|\.|\[|\])|'(?P<str>(?:[^']|'')*)'|(?P<num>-?\d+(?:\.\d+)?)"
    r"|(?P<id>[A-Za-z_][A-Za-z0-9_-]*))"
)


class ExprError(ValueError):
    pass


def _tokens(src: str) -> list[tuple[str, str]]:
    out, i = [], 0
    src = src.strip()
    while i < len(src):
        m = _TOKEN.match(src, i)
        if not m or m.end() == i:
            raise ExprError(f"cannot read {src[i:]!r} in {src!r}")
        i = m.end()
        for kind in ("op", "str", "num", "id"):
            if m.group(kind) is not None:
                val = m.group(kind)
                out.append((kind, val.replace("''", "'") if kind == "str" else val))
                break
    return out


_STATUS = {"always", "cancelled", "success", "failure"}


@dataclass
class _Eval:
    tokens: list[tuple[str, str]]
    ctx: Mapping[str, Any]
    needs_ok: bool
    needs_failed: bool
    pos: int = 0

    def peek(self) -> tuple[str, str] | None:
        return self.tokens[self.pos] if self.pos < len(self.tokens) else None

    def take(self, val: str | None = None) -> tuple[str, str]:
        t = self.peek()
        if t is None or (val is not None and t[1] != val):
            raise ExprError(f"expected {val!r}, got {t!r}")
        self.pos += 1
        return t

    def run(self) -> Any:
        v = self.or_()
        if self.peek() is not None:
            raise ExprError(f"left over: {self.tokens[self.pos:]}")
        return v

    def or_(self) -> Any:
        v = self.and_()
        while self.peek() == ("op", "||"):
            self.take()
            r = self.and_()
            v = v if _truthy(v) else r
        return v

    def and_(self) -> Any:
        v = self.not_()
        while self.peek() == ("op", "&&"):
            self.take()
            r = self.not_()
            v = r if _truthy(v) else v
        return v

    def not_(self) -> Any:
        if self.peek() == ("op", "!"):
            self.take()
            return not _truthy(self.not_())
        return self.cmp()

    def cmp(self) -> Any:
        v = self.primary()
        t = self.peek()
        if t in (("op", "=="), ("op", "!=")):
            self.take()
            r = self.primary()
            eq = _loose_eq(v, r)
            return eq if t[1] == "==" else not eq
        return v

    def primary(self) -> Any:
        t = self.take()
        kind, val = t
        if t == ("op", "("):
            v = self.or_()
            self.take(")")
            return v
        if kind == "str":
            return val
        if kind == "num":
            return float(val)
        if kind != "id":
            raise ExprError(f"unexpected {val!r}")
        if val in ("true", "false"):
            return val == "true"
        if val == "null":
            return None
        if self.peek() == ("op", "("):
            self.take()
            args = []
            while self.peek() != ("op", ")"):
                args.append(self.or_())
                if self.peek() == ("op", ","):
                    self.take()
            self.take(")")
            return self._call(val, args)
        cur: Any = self.ctx.get(val)
        while self.peek() in (("op", "."), ("op", "[")):
            if self.take()[1] == ".":
                key = self.take()[1]
            else:
                key = str(self.or_())
                self.take("]")
            cur = cur.get(key) if isinstance(cur, Mapping) else None
        return cur

    def _call(self, name: str, args: list[Any]) -> Any:
        if name == "always":
            return True
        if name == "cancelled":
            return False
        if name == "success":
            return self.needs_ok
        if name == "failure":
            return self.needs_failed
        if name == "format":
            s = str(args[0])
            for i, a in enumerate(args[1:]):
                s = s.replace("{%d}" % i, str(a))
            return s
        if name == "contains":
            hay, needle = args
            return needle in hay if isinstance(hay, (list, str)) else False
        if name == "toJSON":
            return repr(args[0])
        raise ExprError(f"no function {name}() in this simulator")


def _truthy(v: Any) -> bool:
    return v not in (None, False, "", 0, 0.0)


def _loose_eq(a: Any, b: Any) -> bool:
    """GitHub's ==: strings ignore case; values of different types are not equal here
    (GitHub would coerce them to numbers; ci.yml compares like with like)."""
    if isinstance(a, str) and isinstance(b, str):
        return a.lower() == b.lower()
    if type(a) is type(b):
        return bool(a == b)
    return False


def _strip(expr: str) -> str:
    e = expr.strip()
    if e.startswith("${{") and e.endswith("}}"):
        e = e[3:-2]
    return e


def evaluate(
    expr: str, ctx: Mapping[str, Any], *, needs_ok: bool = True, needs_failed: bool = False
) -> Any:
    return _Eval(_tokens(_strip(expr)), ctx, needs_ok, needs_failed).run()


def uses_status(expr: str) -> bool:
    return any(k == "id" and v in _STATUS for k, v in _tokens(_strip(expr)))


# ---- ci.yml -----------------------------------------------------------------------------


@dataclass
class Workflow:
    jobs: dict[str, dict[str, Any]]
    markers: dict[str, set[str]] = field(default_factory=dict)


def load_workflow(text: str | None = None) -> Workflow:
    text = CI_YML.read_text(encoding="utf-8") if text is None else text
    data = yaml.safe_load(text)
    jobs = data["jobs"]
    markers: dict[str, set[str]] = {}
    current = None
    in_jobs = False
    for line in text.splitlines():
        if line.startswith("jobs:"):
            in_jobs = True
            continue
        if not in_jobs:
            continue
        m = re.match(r"^  ([A-Za-z0-9_-]+):\s*$", line)
        if m:
            current = m.group(1)
            markers.setdefault(current, set())
            continue
        m = re.match(r"^\s+# ci-paths:\s*(.+?)\s*$", line)
        if m and current:
            markers[current] |= {x.strip() for x in m.group(1).split(",")}
    return Workflow(jobs=jobs, markers=markers)


def _needs(job: Mapping[str, Any]) -> list[str]:
    n = job.get("needs", [])
    return [n] if isinstance(n, str) else list(n)


def _order(jobs: Mapping[str, Mapping[str, Any]]) -> list[str]:
    done: list[str] = []

    def visit(j: str, path: tuple[str, ...]) -> None:
        if j in done:
            return
        if j in path:
            raise ValueError(f"a cycle in needs: {path + (j,)}")
        for n in _needs(jobs[j]):
            visit(n, path + (j,))
        done.append(j)

    for j in jobs:
        visit(j, ())
    return done


@dataclass
class Run:
    """A job that runs, and the inputs it hands its reusable workflow."""

    inputs: dict[str, Any]


def simulate(
    wf: Workflow,
    true_filters: Iterable[str],
    *,
    event: str = "pull_request",
    fork: bool = False,
    apple_scope: bool = False,
    repo: bool | None = None,
) -> dict[str, Run | None]:
    """Which jobs run (each assumed to pass) when the given filters are true. `repo`: a file
    other than the trail changed (by default, when a filter is true)."""
    true_filters = set(true_filters)
    repo = bool(true_filters) if repo is None else repo
    changes = wf.jobs[CHANGES]
    step_outputs: dict[str, Any] = {
        "filter": {"outputs": {}},
        REPO: {"outputs": {}},
        "apple": {"outputs": {"player_ui": "true" if apple_scope else "false"}},
    }
    base_ctx: dict[str, Any] = {
        "github": {
            "event_name": event,
            "event": {"pull_request": {"head": {"repo": {"fork": fork}}}} if event == "pull_request" else {},
        },
    }
    if event == "pull_request":
        step_outputs["filter"]["outputs"] = {name: "true" for name in true_filters}
        step_outputs[REPO]["outputs"] = {REPO: "true" if repo else "false"}
    outs = {
        k: _as_output(evaluate(v, {**base_ctx, "steps": step_outputs}))
        for k, v in (changes.get("outputs") or {}).items()
    }
    results: dict[str, str] = {}
    runs: dict[str, Run | None] = {}
    for j in _order(wf.jobs):
        job = wf.jobs[j]
        if j == CHANGES:
            results[j], runs[j] = "success", Run({})
            continue
        needs = _needs(job)
        ctx = {
            **base_ctx,
            "needs": {
                n: {"result": results[n], "outputs": outs if n == CHANGES and results[n] == "success" else {}}
                for n in needs
            },
        }
        cond = job.get("if")
        ok = all(results[n] == "success" for n in needs)
        failed = any(results[n] == "failure" for n in needs)
        if cond is None:
            go = ok
        else:
            go = _truthy(evaluate(str(cond), ctx, needs_ok=ok, needs_failed=failed))
            if not uses_status(str(cond)):
                go = go and ok
        if go:
            inputs = {
                k: evaluate(v, ctx) if isinstance(v, str) and "${{" in v else v for k, v in (job.get("with") or {}).items()
            }
            results[j], runs[j] = "success", Run(inputs)
        else:
            results[j], runs[j] = "skipped", None
    return runs


def _as_output(v: Any) -> str:
    """A job output is a string: GitHub writes a true expression as 'true'."""
    if isinstance(v, bool):
        return "true" if v else "false"
    return "" if v is None else str(v)


def which(files: Sequence[str], **kw: Any) -> dict[str, Run | None]:
    wf = load_workflow()
    return simulate(wf, matched(job_filters(load_filters()), files), repo=any(f != TRAIL for f in files), **kw)


# ---- the guard --------------------------------------------------------------------------


def _refs(v: Any) -> set[str]:
    if isinstance(v, str):
        return set(_OUTPUT_REF.findall(v))
    if isinstance(v, Mapping):
        return {r for x in v.values() for r in _refs(x)}
    if isinstance(v, list):
        return {r for x in v for r in _refs(x)}
    return set()


def tracked_files() -> list[str]:
    out = subprocess.run(["git", "ls-files"], cwd=ROOT, capture_output=True, text=True, check=True)
    return [f for f in out.stdout.splitlines() if f]


def guard(
    wf: Workflow | None = None,
    filters: Mapping[str, list[str]] | None = None,
    files: Sequence[str] | None = None,
) -> list[str]:
    """What is wrong with ci.yml and ci-paths.yml: an empty list when nothing is."""
    wf = wf or load_workflow()
    filters = dict(filters if filters is not None else load_filters())
    named = job_filters(filters)
    errs: list[str] = []

    for name, pats in filters.items():
        if not pats:
            errs.append(f"filter {name} has no globs")
        for p in pats:
            if not _SAFE_GLOB.fullmatch(p) or p.startswith(("/", "./")):
                errs.append(f"filter {name}: {p!r} is not a plain glob (no !, braces, brackets, leading /)")

    changes = wf.jobs.get(CHANGES)
    if not changes:
        return errs + ["ci.yml has no changes job"]
    step = next((s for s in changes.get("steps", []) if s.get("id") == "filter"), None)
    if not step or (step.get("with") or {}).get("filters") != FILTERS_REF:
        errs.append(f"the changes job's `filter` step must read filters: {FILTERS_REF}")
    repo_step = next((s for s in changes.get("steps", []) if s.get("id") == REPO), None)
    repo_with = (repo_step or {}).get("with") or {}
    try:
        repo_filters = yaml.safe_load(str(repo_with.get("filters", "")))
    except yaml.YAMLError:
        repo_filters = None
    if repo_with.get("predicate-quantifier") != "every" or repo_filters != REPO_FILTERS:
        errs.append(
            f"the changes job needs a `{REPO}` paths-filter step with predicate-quantifier: every and "
            f"filters {REPO}: ['!{TRAIL}'] (true when a file other than the trail changed)"
        )
    for name, pats in named.items():
        if any(matches(p, TRAIL) for p in pats):
            errs.append(f"filter {name} matches {TRAIL}: a pull request changing only the trail runs nothing")
    outputs = changes.get("outputs") or {}
    if set(outputs) != set(named) | {REPO}:
        missing = sorted((set(named) | {REPO}) - set(outputs))
        extra = sorted(set(outputs) - set(named) - {REPO})
        errs.append(f"the changes job's outputs are not the filters (missing {missing}, not filters {extra})")
    for k, v in outputs.items():
        e = _strip(str(v)).strip()
        step_id = REPO if k == REPO else "filter"
        if not e.startswith("github.event_name != 'pull_request' ||") or f"steps.{step_id}.outputs.{k} == 'true'" not in e:
            errs.append(
                f"changes output {k} must be `github.event_name != 'pull_request' || "
                f"steps.{step_id}.outputs.{k} == 'true'` (true on a push)"
            )

    used: set[str] = set()
    needs_of_gate = set(_needs(wf.jobs.get(GATE, {})))
    if GATE not in wf.jobs:
        errs.append("ci.yml has no ci-success job")
    elif str(wf.jobs[GATE].get("if", "")).strip() != "always()":
        errs.append("ci-success must run always()")

    # a pull request no filter matches (docs, say), and one that changes only the trail
    nothing = simulate(wf, set(), repo=True)
    trail_only = simulate(wf, set(), repo=False)
    on_push = simulate(wf, set(), event="push")
    for j, job in wf.jobs.items():
        if j in (CHANGES, GATE):
            continue
        marks = wf.markers.get(j, set())
        if marks - MARKERS:
            errs.append(f"job {j}: unknown ci-paths marker(s) {sorted(marks - MARKERS)}")
        # `repo` is no filter: a job gated by it alone is an always-run job
        gated_on_repo = REPO in _refs(job.get("if", ""))
        if_refs = _refs(job.get("if", "")) - {REPO}
        with_refs = _refs(job.get("with") or {})
        for r in sorted((if_refs | with_refs) - set(named)):
            errs.append(f"job {j} reads needs.changes.outputs.{r}, which is no filter")
        used |= if_refs | with_refs
        if trail_only.get(j):
            errs.append(
                f"job {j} runs on a pull request that changes only {TRAIL}: "
                f"gate it on needs.changes.outputs.{REPO} or a filter"
            )
        # `repo` is true whenever a filter is, so a job gated by it may hand on any filter
        if if_refs and with_refs - if_refs and not gated_on_repo:
            errs.append(f"job {j} hands its workflow {sorted(with_refs - if_refs)} but its if: does not run on them")
        if if_refs:
            if marks & {"always-run", "push-only"}:
                errs.append(f"job {j} is gated by filters and marked {sorted(marks & {'always-run', 'push-only'})}")
            if CHANGES not in _needs(job):
                errs.append(f"job {j} reads the filters but does not need changes")
            for r in sorted(if_refs & set(named)):
                alone = [simulate(wf, {r}, fork=f, apple_scope=False).get(j) for f in (False, True)]
                if not any(alone):
                    errs.append(
                        f"job {j} does not run when {r} alone is true (the jobs it needs skipped): "
                        "a job that needs another than changes must say !cancelled()"
                    )
            if nothing.get(j) and "always-run" not in marks:
                errs.append(f"job {j} runs when no filter is true: mark it always-run or fix its if:")
        elif "always-run" in marks:
            if not nothing.get(j):
                errs.append(f"job {j} is marked always-run but does not run on a pull request no filter matches")
        elif "push-only" in marks:
            if nothing.get(j):
                errs.append(f"job {j} is marked push-only but runs on a pull request")
        else:
            errs.append(f"job {j} is gated by no filter and not marked always-run (`# ci-paths: always-run`)")
        if not on_push.get(j) and "pr-only" not in marks:
            errs.append(f"job {j} does not run on a push to main (mark it pr-only if that is meant)")
        if j not in needs_of_gate and "not-a-gate" not in marks:
            errs.append(f"job {j} is not in ci-success's needs (mark it not-a-gate if that is meant)")
    for name in sorted(set(named) - used):
        errs.append(f"filter {name} is no job's")

    files = tracked_files() if files is None else files
    uncovered = [
        f
        for f in files
        if not any(matches(p, f) for pats in named.values() for p in pats)
        and not any(matches(p, f) for p in NOT_IN_CI)
    ]
    if uncovered:
        shown = ", ".join(uncovered[:20]) + (" …" if len(uncovered) > 20 else "")
        errs.append(
            f"{len(uncovered)} tracked file(s) no filter in {FILTERS_REF} matches and NOT_IN_CI does not "
            f"list: {shown}"
        )
    return errs


# ---- the command line -------------------------------------------------------------------


def _show(runs: Mapping[str, Run | None]) -> str:
    lines = []
    for j, r in runs.items():
        if r is None or j in (CHANGES,):
            continue
        parts = sorted(k for k, v in r.inputs.items() if v is True)
        off = sorted(k for k, v in r.inputs.items() if v is False)
        extra = f"  [{', '.join(parts)}]" if parts else ""
        extra += f"  (not: {', '.join(off)})" if off else ""
        lines.append(f"{j}{extra}")
    return "\n".join(lines)


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description="CI path filters: guard and simulator")
    sub = ap.add_subparsers(dest="cmd", required=True)
    sub.add_parser("guard")
    w = sub.add_parser("which")
    w.add_argument("files", nargs="*")
    w.add_argument("--push", action="store_true")
    w.add_argument("--apple-player-scope", action="store_true", help="the project file has a Player change")
    a = ap.parse_args(argv)
    if a.cmd == "guard":
        errs = guard()
        for e in errs:
            print(f"::error::ci-paths: {e}")
        if not errs:
            print("ci-paths: every job is gated by a filter or marked, every filter used, every file covered")
        return 1 if errs else 0
    runs = which(a.files, event="push" if a.push else "pull_request", apple_scope=a.apple_player_scope)
    print(_show(runs))
    return 0


if __name__ == "__main__":
    sys.exit(main())
