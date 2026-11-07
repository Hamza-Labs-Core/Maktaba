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


def _vs(ratio: float, base: int = 50_000) -> dict[str, object]:
    return {
        "base_p99_ns": base,
        "head_p99_ns": int(base * ratio),
        "base_p99_ns_rounds": [base],
        "head_p99_ns_rounds": [int(base * ratio)],
        "ratio": ratio,
    }


@pytest.mark.unit
@pytest.mark.parametrize(
    ("p99", "vs_ratio", "code", "says"),
    [
        # #806 on a 7763: over the budget, and so is its base on that runner: not the change
        (55_700, 1.01, 0, "the runner, not this change"),
        # over the budget and 20 % slower than its base: the change
        (55_700, 1.20, 1, "1.20 times it"),
        # over the budget with no base to compare with: the budget alone, as before
        (55_700, None, 1, "budget 55.3"),
        # within the budget but 20 % slower than the base: a warning
        (40_000, 1.20, 0, "slower than the base, within the budget"),
        (40_000, None, 0, ""),
    ],
)
def test_the_base_on_the_same_runner_tells_the_runner_from_the_change(
    monkeypatch: pytest.MonkeyPatch,
    capsys: pytest.CaptureFixture[str],
    p99: int,
    vs_ratio: float | None,
    code: int,
    says: str,
) -> None:
    lb = _tool()
    monkeypatch.setenv("LOGBENCH_PY_STRICT", "1")
    vs = None if vs_ratio is None else _vs(vs_ratio)
    assert lb.verdict(p99, 55_341, 2.77, vs) == code
    err = capsys.readouterr().err
    assert says in err if says else err == ""
    if code:
        assert err.startswith("::error::")


@pytest.mark.integration  # fresh processes, past the unit tier's cap
def test_compare_runs_the_base_logger_round_for_round() -> None:
    # this checkout's own logger as the base: the same code, so near one another
    lb = _tool()
    src = str(TOOL.parents[1] / "src")
    got = lb.compare(src, 2_000, rounds=2)
    assert len(got["base_p99_ns_rounds"]) == 2 and len(got["head_p99_ns_rounds"]) == 2
    assert got["base_p99_ns"] == min(got["base_p99_ns_rounds"])
    assert 0.2 < got["ratio"] < 5


@pytest.mark.integration  # fresh processes, past the unit tier's cap
def test_a_base_without_the_logger_is_refused(tmp_path: Path) -> None:
    # a base folder with no maktaba_pipeline imports the installed one: never compared
    lb = _tool()
    with pytest.raises(RuntimeError, match="not the logger in"):
        lb.compare(str(tmp_path), 500, rounds=1)
