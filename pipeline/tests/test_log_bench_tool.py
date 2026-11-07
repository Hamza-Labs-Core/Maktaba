"""``tools/log_bench.py``'s runner factor (issue #744): the slower of its two loops against
the reference, clamped to 1–4, with the loops' times in the report."""

from __future__ import annotations

import importlib.util
from pathlib import Path
from types import ModuleType

import pytest

TOOL = Path(__file__).resolve().parents[1] / "tools" / "log_bench.py"


def _tool() -> ModuleType:
    spec = importlib.util.spec_from_file_location("log_bench_tool", TOOL)
    assert spec and spec.loader
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


@pytest.mark.unit
@pytest.mark.parametrize(
    ("hash_x", "lines_x", "factor"),
    [(0.5, 0.9, 1.0), (2.8, 1.5, 2.8), (1.2, 2.5, 2.5), (9.0, 1.0, 4.0)],
)
def test_the_factor_is_the_slower_loop_clamped(
    monkeypatch: pytest.MonkeyPatch, hash_x: float, lines_x: float, factor: float
) -> None:
    lb = _tool()
    times = iter([lb.REFERENCE_HASH_S * hash_x, lb.REFERENCE_LINES_S * lines_x])
    monkeypatch.setattr(lb, "_best_of_three", lambda _work: next(times))
    got, raw = lb.calibrate()
    assert got == pytest.approx(factor)
    assert set(raw) == {"hash_s", "lines_s"}


@pytest.mark.unit
def test_the_loops_run() -> None:
    lb = _tool()
    lb._lines_work()
    assert 0 < lb._best_of_three(lambda: None) < 1
