"""tools/logbench/compare.py: the nightly run's regression check (Epic 53 story 53.13 §2)."""

import json
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

import compare  # noqa: E402


def run(f=1.0, **benches):
    return {"_runner": {"factor": f}, **benches}


def test_a_cost_over_20_percent_is_a_regression_and_a_count_is_not():
    prev = run(logger_overhead_go={"calls": 100, "p99_ns": 1000}, dashboard_30d={"p95_ms": 50})
    cur = run(logger_overhead_go={"calls": 300, "p99_ns": 1300}, dashboard_30d={"p95_ms": 55})
    found = compare.regressions(prev, cur)
    assert [(b, m) for b, m, *_ in found] == [("logger_overhead_go", "p99_ns")]


def test_a_slower_runner_is_taken_out():
    prev = run(1.0, capture_burst={"cpu_ns_per_line": 10000})
    cur = run(2.0, capture_burst={"cpu_ns_per_line": 19000})
    assert compare.regressions(prev, cur) == []


def test_a_new_bench_or_a_missing_metric_is_not_compared():
    prev = run(disk={"cap_bytes": 4, "worst_bytes": 3})
    cur = run(disk={"cap_bytes": 8, "worst_bytes": 3}, logger_overhead_python={"p99_ns": 9})
    assert compare.regressions(prev, cur) == []


def test_the_cli_writes_the_issue_body_and_fails(tmp_path):
    p, c, md = tmp_path / "p.json", tmp_path / "c.json", tmp_path / "r.md"
    p.write_text(json.dumps(run(index_and_search={"filters_p95_ms": 10})))
    c.write_text(json.dumps(run(index_and_search={"filters_p95_ms": 20})))
    assert compare.main([str(p), str(c), "--markdown", str(md)]) == 1
    assert "filters_p95_ms" in md.read_text() and "+100%" in md.read_text()
    c.write_text(json.dumps(run(index_and_search={"filters_p95_ms": 11})))
    assert compare.main([str(p), str(c), "--markdown", str(md)]) == 0
    assert md.read_text() == ""
