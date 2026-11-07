"""N16's comparison (maktaba_plugin_sdk.parity.perf_check): p50 over the whole job, and p95
over the job's own run when the harness gives it, so a stall in the worker's code that both
sides share (seen on CI with the process's CPU flat) does not fail a plugin."""

from __future__ import annotations

from maktaba_plugin_sdk.parity import perf_check

MS = 0.001


def test_a_stall_outside_the_run_does_not_fail_p95() -> None:
    inproc = [15 * MS] * 20 + [16 * MS] * 2
    plugin = [23 * MS] * 20 + [95 * MS, 80 * MS]  # two jobs stalled before their run
    runs_a, runs_b = [7 * MS] * 22, [15 * MS] * 22
    assert perf_check("s", inproc, plugin, floor_s=25 * MS) != []  # the whole job's p95
    got = perf_check("s", inproc, plugin, floor_s=25 * MS, inproc_p95=runs_a, plugin_p95=runs_b)
    assert got == []


def test_a_slower_run_still_fails_p95_and_a_slower_job_p50() -> None:
    inproc, plugin = [10 * MS] * 20, [12 * MS] * 20
    got = perf_check("s", inproc, plugin, inproc_p95=[5 * MS] * 20, plugin_p95=[60 * MS] * 20)
    assert got == ["s p50: plugin 12.0 ms > 1.10 x in-process 10.0 ms",
                   "s p95 (its run): plugin 60.0 ms > 1.15 x in-process 5.0 ms"]  # fmt: skip


def test_without_run_samples_p95_is_the_whole_job() -> None:
    got = perf_check("s", [10 * MS] * 20, [10 * MS] * 18 + [90 * MS] * 2)
    assert got == ["s p95: plugin 90.0 ms > 1.15 x in-process 10.0 ms"]
