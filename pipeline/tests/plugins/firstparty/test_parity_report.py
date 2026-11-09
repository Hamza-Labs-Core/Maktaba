"""Each N16 report belongs to one test. Two parity tests writing one report name replaced each
other's ``$MAKTABA_PARITY_REPORT_DIR/<name>.{txt,json}``: classify's wave 1 (two shows,
detect makes 29 plugin API calls) and wave 4 (one show, 19 calls) both wrote
``tv.maktaba.classify``, so detect's workload in the report looked like it depended on test
order. The harness now refuses a name a second test asks for, and no two parity tests name
one report."""

from __future__ import annotations

import ast
from pathlib import Path

import pytest

from tests.plugins.firstparty import harness
from tests.plugins.firstparty.conftest import ROOT


@pytest.fixture
def fresh(monkeypatch: pytest.MonkeyPatch) -> dict[str, str]:
    taken: dict[str, str] = {}
    monkeypatch.setattr(harness, "_REPORTED", taken)
    return taken


def test_a_test_may_write_its_own_report_again(fresh: dict[str, str]) -> None:
    harness.claim_report("tv.maktaba.toy", "t.py::test_a")
    harness.claim_report("tv.maktaba.toy", "t.py::test_a")
    assert fresh == {"tv.maktaba.toy": "t.py::test_a"}


def test_a_second_test_asking_for_a_taken_name_fails_naming_both(fresh: dict[str, str]) -> None:
    harness.claim_report("tv.maktaba.classify", "t.py::test_wave_1")
    with pytest.raises(AssertionError) as e:
        harness.claim_report("tv.maktaba.classify", "t.py::test_wave_4")
    assert "t.py::test_wave_1" in str(e.value) and "t.py::test_wave_4" in str(e.value)
    # the first test keeps its name; another name is free
    assert fresh["tv.maktaba.classify"] == "t.py::test_wave_1"
    harness.claim_report("tv.maktaba.classify.wave4", "t.py::test_wave_4")


def test_the_running_test_is_named_without_its_phase(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("PYTEST_CURRENT_TEST", "a/b.py::test_x[p (1)] (call)")
    assert harness._current_test() == "a/b.py::test_x[p (1)]"


def _report_names(path: Path) -> list[tuple[str, int]]:
    """The report names a test file passes to ``.report(...)``: string literals and
    module-level string constants (a name built at run time is left to claim_report)."""
    tree = ast.parse(path.read_text(encoding="utf-8"))
    consts = {
        t.id: n.value.value
        for n in tree.body
        if isinstance(n, ast.Assign)
        and isinstance(n.value, ast.Constant)
        and isinstance(n.value.value, str)
        for t in n.targets
        if isinstance(t, ast.Name)
    }
    out = []
    for n in ast.walk(tree):
        if not (
            isinstance(n, ast.Call)
            and isinstance(n.func, ast.Attribute)
            and n.func.attr == "report"
            and len(n.args) == 1
        ):
            continue
        arg = n.args[0]
        if isinstance(arg, ast.Constant) and isinstance(arg.value, str):
            out.append((arg.value, n.lineno))
        elif isinstance(arg, ast.Name) and arg.id in consts:
            out.append((consts[arg.id], n.lineno))
    return out


def test_no_two_parity_tests_name_one_report() -> None:
    files = sorted((ROOT / "plugins" / "first-party").glob("*/tests/test_*parity*.py"))
    assert len(files) >= 10, files  # the scan finds the plugins' parity tests
    seen: dict[str, str] = {}
    clashes = []
    for f in files:
        for name, line in _report_names(f):
            where = f"{f.relative_to(ROOT)}:{line}"
            if name in seen:
                clashes.append(f"{name}: {seen[name]} and {where}")
            seen.setdefault(name, where)
    assert "tv.maktaba.classify" in seen and "tv.maktaba.core-media" in seen, seen
    assert clashes == []
