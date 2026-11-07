"""``tools/log_bench.py``'s runner factor (issues #744, #807): the slowest of its loops
against the reference, clamped to 1–4, with the loops' times in the report."""

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
    monkeypatch.setattr(lb, "_tail_p99_ns", lambda: lb.REFERENCE_TAIL_P99_NS)
    monkeypatch.setattr(lb, "_stock_p99_ns", lambda: (6_000, lb.REFERENCE_STOCK_P99_NS))
    got, raw = lb.calibrate()
    assert got == pytest.approx(factor)
    assert set(raw) == {"hash_s", "lines_s", "tail_p99_ns", "stock_p50_ns", "stock_p99_ns"}


@pytest.mark.unit
def test_a_heavy_tail_raises_the_factor(monkeypatch: pytest.MonkeyPatch) -> None:
    # a runner as fast as the reference on the whole but 3.3 times slower at the tail: the
    # budget, on a p99, follows the tail
    lb = _tool()
    times = iter([lb.REFERENCE_HASH_S * 2.7, lb.REFERENCE_LINES_S * 2.7])
    monkeypatch.setattr(lb, "_best_of_three", lambda _work: next(times))
    monkeypatch.setattr(lb, "_tail_p99_ns", lambda: int(lb.REFERENCE_TAIL_P99_NS * 3.3))
    monkeypatch.setattr(lb, "_stock_p99_ns", lambda: (6_000, lb.REFERENCE_STOCK_P99_NS))
    got, _ = lb.calibrate()
    assert got == pytest.approx(3.3, rel=0.01)


@pytest.mark.unit
def test_structlogs_own_tail_raises_the_factor(monkeypatch: pytest.MonkeyPatch) -> None:
    # CI's AMD EPYC 7763: the C loops 2.7 times the reference, the tail 3.4, a call through
    # structlog's stock pipeline 3.6 at p99, as the logger is: the factor follows structlog
    lb = _tool()
    times = iter([lb.REFERENCE_HASH_S * 2.7, lb.REFERENCE_LINES_S * 2.5])
    monkeypatch.setattr(lb, "_best_of_three", lambda _work: next(times))
    monkeypatch.setattr(lb, "_tail_p99_ns", lambda: int(lb.REFERENCE_TAIL_P99_NS * 3.4))
    monkeypatch.setattr(lb, "_stock_p99_ns", lambda: (14_600, int(lb.REFERENCE_STOCK_P99_NS * 3.6)))
    got, raw = lb.calibrate()
    assert got == pytest.approx(3.6, rel=0.01)
    assert raw["stock_p50_ns"] == 14_600


@pytest.mark.unit
def test_the_loops_run() -> None:
    lb = _tool()
    lb._lines_work()
    assert 0 < lb._best_of_three(lambda: None) < 1
    assert lb._tail_p99_ns(200) > 0
    p50, p99 = lb._stock_p99_ns(200)
    assert 0 < p50 <= p99


@pytest.mark.unit
def test_the_best_of_its_rounds_is_judged(monkeypatch: pytest.MonkeyPatch) -> None:
    # a shared runner's other tenants only add time: the round with the lowest p99 is the
    # logger's own cost, and every round is in the report
    lb = _tool()
    got = lb.run(400, rounds=3)
    assert got["rounds"] == 3 and len(got["p99_ns_rounds"]) == 3
    assert got["p99_ns"] == min(got["p99_ns_rounds"])
    assert 0 < got["p50_ns"] <= got["p99_ns"]
