#!/usr/bin/env python3
"""Fails an Apple UI test job whose result bundle ran none, or not all, of its selected tests.

    xcresult_ran.py LABEL XCRESULT SELECTION     # SELECTION: the job's xcodebuild selection
    xcresult_ran.py LABEL --tests-json FILE SELECTION

SELECTION is a file holding the arguments the job gave xcodebuild to pick its tests, one a
line, exactly as passed: `-only-testing TARGET[/CLASS[/METHOD]]` pairs and an optional
`-test-iterations N`. The bundle's test tree comes from `xcrun xcresulttool get test-results
tests --path XCRESULT --format json` (or FILE, for the tests in tools/ci/tests).

xcodebuild can report `** TEST EXECUTE SUCCEEDED **` for a run that did not run its tests: when
the test runner exits unexpectedly (a crash, or a test over its time allowance), XCTest
restarts it, and the restarted runner may run nothing ("Executed 0 tests"). In CI run
43605618404 (#1312) a `-test-iterations 20` shard ran 1 iteration of 20 that way and the job
went green. This check reads what the bundle holds and fails, with an `::error::` naming the
job, when:

- no test ran (every test case is missing or skipped);
- a selected test, class or target has no test case in the bundle;
- with `-test-iterations N`, a selected test ran fewer than N times.

A test case "ran" when its result is Passed, Failed or Expected Failure; its runs are its
`Repetition` children (under `-test-iterations`), else one. A Swift Testing case's
`Arguments` children are one run of a parameterised test, not repetitions.
"""

from __future__ import annotations

import json
import re
import subprocess
import sys
from collections import Counter
from dataclasses import dataclass
from pathlib import Path
from typing import Any

RAN = {"Passed", "Failed", "Expected Failure"}
BUNDLES = {"UI test bundle", "Unit test bundle"}
# `Class/method()` or a parameterised `Class/method(_:)`: the selection names `Class/method`
ARGS = re.compile(r"\([^()]*\)$")


@dataclass(frozen=True)
class Case:
    bundle: str
    ident: str  # Class/method, without its parameter list
    result: str
    runs: int


@dataclass(frozen=True)
class Selection:
    only: list[str]  # TARGET[/CLASS[/METHOD]]
    iterations: int


def parse_selection(args: list[str]) -> Selection:
    only: list[str] = []
    iterations = 1
    it = iter(args)
    for a in it:
        if a == "-only-testing":
            only.append(next(it, ""))
        elif a == "-test-iterations":
            iterations = int(next(it, "1"))
        elif a:
            raise ValueError(f"an argument this check does not read: {a!r}")
    if not only or "" in only:
        raise ValueError("the selection names no test (no `-only-testing TARGET...`)")
    return Selection(only, iterations)


def cases(tree: dict[str, Any]) -> list[Case]:
    out: list[Case] = []

    def walk(node: dict[str, Any], bundle: str) -> None:
        kind = node.get("nodeType")
        if kind in BUNDLES:
            bundle = node.get("name", "")
        if kind == "Test Case":
            reps = [c for c in node.get("children", []) if c.get("nodeType") == "Repetition"]
            ident = ARGS.sub("", node.get("nodeIdentifier") or node.get("name", ""))
            ran = sum(1 for r in reps if r.get("result") in RAN) if reps else 1
            out.append(Case(bundle, ident, node.get("result", ""), ran))
            return
        for child in node.get("children", []):
            walk(child, bundle)

    for top in tree.get("testNodes", []):
        walk(top, "")
    return out


def matches(case: Case, selected: str) -> bool:
    target, _, rest = selected.partition("/")
    if case.bundle != target:
        return False
    return not rest or case.ident == rest or case.ident.startswith(rest + "/")


def check(label: str, tree: dict[str, Any], sel: Selection) -> tuple[list[str], list[str]]:
    """The lines to print and the errors, for the job LABEL's bundle TREE and selection SEL."""
    all_cases = cases(tree)
    ran = [c for c in all_cases if c.result in RAN]
    by_result = Counter(c.result for c in all_cases)
    runs = sum(c.runs for c in ran)
    lines = [
        f"{label}: {len(ran)} tests ran of {len(all_cases)} in the results "
        f"({by_result['Passed']} passed, {by_result['Failed']} failed, "
        f"{by_result['Expected Failure']} expected failures, {by_result['Skipped']} skipped), "
        f"{runs} runs; {len(sel.only)} selected" + (f", {sel.iterations} iterations each" if sel.iterations > 1 else "")
    ]
    errors: list[str] = []
    if not ran:
        errors.append(
            f"{label}: no test ran (the result bundle holds {len(all_cases)} test cases, none "
            "passed or failed): xcodebuild reported success for a run that tested nothing"
        )
    for s in sel.only:
        found = [c for c in all_cases if matches(c, s)]
        if not found:
            errors.append(f"{label}: {s} was selected but is not in the results (never ran)")
            continue
        if sel.iterations > 1:
            for c in found:
                if c.runs < sel.iterations:
                    errors.append(
                        f"{label}: {c.bundle}/{c.ident} ran {c.runs} of {sel.iterations} "
                        "iterations (the test runner restarted and dropped the rest)"
                    )
    return lines, errors


def load_tree(xcresult: str) -> dict[str, Any]:
    out = subprocess.run(
        ["xcrun", "xcresulttool", "get", "test-results", "tests", "--path", xcresult, "--format", "json"],
        check=True,
        capture_output=True,
        text=True,
    ).stdout
    tree: dict[str, Any] = json.loads(out)
    return tree


def main(argv: list[str]) -> int:
    if len(argv) == 4 and argv[1] == "--tests-json":
        label, source, selection, from_json = argv[0], argv[2], argv[3], True
    elif len(argv) == 3:
        label, source, selection, from_json = argv[0], argv[1], argv[2], False
    else:
        print(__doc__, file=sys.stderr)
        return 2
    try:
        sel = parse_selection(Path(selection).read_text(encoding="utf-8").splitlines())
        tree: dict[str, Any] = json.loads(Path(source).read_text(encoding="utf-8")) if from_json else load_tree(source)
    except (OSError, ValueError, subprocess.CalledProcessError) as e:
        detail = getattr(e, "stderr", "") or str(e)
        print(f"::error::{label}: the result bundle {source} could not be read: {detail.strip()}")
        return 1
    lines, errors = check(label, tree, sel)
    for line in lines:
        print(line)
    for err in errors:
        print(f"::error::{err}")
    return 1 if errors else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
