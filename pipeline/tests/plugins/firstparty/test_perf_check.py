"""N16's comparison (maktaba_plugin_sdk.parity.perf_check): the CPU each job cost (p50 and
p95), which a shared machine's stalls do not move, and the wall time paired round by round
(the median difference), within 10% plus the plugin path's fixed cost of a job and the hop
each of its statements pays."""

from __future__ import annotations

from maktaba_plugin_sdk.parity import STATEMENT_HOP_S, JobTiming, perf_check

MS = 0.001
FLOOR = 25 * MS


def rounds(
    inproc: list[tuple[float, float]], plugin: list[tuple[float, float]], calls: int, hop: float
) -> tuple[list[JobTiming], list[JobTiming]]:
    """(wall ms, cpu ms) per round, each side."""
    a = [JobTiming(wall=w * MS, cpu=c * MS) for w, c in inproc]
    b = [JobTiming(wall=w * MS, cpu=c * MS, calls=calls, hop=hop * MS) for w, c in plugin]
    return a, b


def test_the_failure_seen_on_ci_is_the_platform_cost_and_passes() -> None:
    # PR #1021's run: detect made 29 plugin API calls on a slow runner, 26 ms over the
    # in-process wall time; its CPU was 4 ms over (the API's 16 ms is N18's)
    a, b = rounds([(25.2, 15.5)] * 11, [(51.4, 19.6)] * 11, calls=29, hop=0.4)
    assert perf_check("detect", a, b, floor_s=FLOOR) == []


def test_stalls_on_either_side_do_not_fail() -> None:
    base_a, base_b = [(15.0, 8.0)] * 11, [(30.0, 10.0)] * 11
    # a stall (wall only, the CPU flat) on the plugin's turn in four rounds, and on the
    # in-process side's in two
    for i in (1, 4, 7, 9):
        base_b[i] = (30.0 + 120.0, 10.0)
    for i in (2, 5):
        base_a[i] = (15.0 + 200.0, 8.0)
    a, b = rounds(base_a, base_b, calls=10, hop=0.5)
    assert perf_check("s", a, b, floor_s=FLOOR) == []


def test_a_machine_slow_for_a_while_slows_both_sides_of_its_rounds() -> None:
    # rounds 0-5 at twice the speed of rounds 6-10: unpaired, the plugin's p50 would be a slow
    # round and the in-process one a fast one
    a, b = rounds(
        [(10.0, 5.0)] * 6 + [(40.0, 5.0)] * 5,
        [(20.0, 6.0)] * 6 + [(60.0, 6.0)] * 5,
        calls=5,
        hop=0.5,
    )
    assert perf_check("s", a, b, floor_s=FLOOR) == []


def test_twice_the_cpu_fails_p50_and_p95() -> None:
    a, b = rounds([(50.0, 30.0)] * 11, [(90.0, 60.0)] * 11, calls=5, hop=0.5)
    got = perf_check("s", a, b, floor_s=FLOOR)
    assert got[:2] == [
        "s p50 (CPU): plugin 60.0 ms > 1.10 x in-process 30.0 ms",
        "s p95 (CPU): plugin 60.0 ms > 1.15 x in-process 30.0 ms",
    ]


def test_a_plugin_that_waits_fails_the_paired_wall_time() -> None:
    # the same CPU, 40 ms more wall a job: a sleep, a lock, round trips to Postgres
    a, b = rounds([(20.0, 10.0)] * 11, [(60.0, 10.5)] * 11, calls=4, hop=0.5)
    assert perf_check("s", a, b, floor_s=FLOOR) == [
        "s p50 (wall, paired): plugin slower by 40.0 ms > allowed 29.0 ms (10% of in-process"
        " 20.0 ms + 25.0 ms a job + 4 calls x 0.50 ms)"
    ]


def test_a_statement_hop_over_n18_counts() -> None:
    # 20 calls whose cheapest takes 3 ms: only N18's 2 ms a statement is allowed
    slow_hop = 3.0
    a, b = rounds([(20.0, 10.0)] * 11, [(20.0 + 25 + 20 * slow_hop, 10.0)] * 11, 20, slow_hop)
    got = perf_check("s", a, b, floor_s=FLOOR)
    assert len(got) == 1 and f"20 calls x {STATEMENT_HOP_S * 1000:.2f} ms" in got[0], got


def test_a_difference_under_the_floor_does_not_count() -> None:
    a, b = rounds([(5.0, 3.0)] * 11, [(25.0, 15.0)] * 11, calls=0, hop=0.0)
    assert perf_check("s", a, b, floor_s=FLOOR) == []
    assert perf_check("s", [], b, floor_s=FLOOR) == []
