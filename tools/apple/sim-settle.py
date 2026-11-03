#!/usr/bin/env python3
"""Waits until a booted simulator's own processes are quiet: its first-boot work done.

    tools/apple/sim-settle.py UDID LOG [--max SECONDS]
    tools/apple/sim-settle.py --self-test

A simulator made for a CI run is busy for minutes after it boots. A UI test in that time
ran slowly enough to fail on time (an accessibility audit "failed to complete in time"
eight minutes after the simulator was made; a launch took 31 s to idle). The runner as a
whole is no measure: its own daemons (diagnosticd, apsd, Spotlight) kept it under 30% idle
for twelve minutes on every shard. So this sums the CPU of the simulator's processes alone
(the ones launched from its runtime, `/RuntimeRoot/`, or its device folder) on each 5-second
look, and returns once three looks running are under QUIET percent of one core. Each look,
with the simulator's busiest processes, the runner's idle share and the runner's own busiest
processes, goes to LOG. Not quiet
within --max seconds: it says so and returns 0 (the tests run anyway; their results decide).
"""
from __future__ import annotations

import argparse
import re
import subprocess
import sys
import time

QUIET = 40.0  # percent of one core, the simulator's processes together
LOOKS = 3


def sim_pids(ps: str, udid: str) -> dict[int, str]:
    """The simulator's processes in `ps -Ao pid=,command=` output: pid -> short name."""
    found: dict[int, str] = {}
    for line in ps.splitlines():
        parts = line.strip().split(None, 1)
        if len(parts) != 2 or not parts[0].isdigit():
            continue
        pid, command = int(parts[0]), parts[1]
        if "/RuntimeRoot/" in command or f"/Devices/{udid}/" in command:
            # the executable's name (its path may hold spaces; its arguments start with -)
            found[pid] = command.split(" -", 1)[0].rsplit("/", 1)[-1]
    return found


def runner_busiest(ps: str, cpu: dict[int, float], sim: dict[int, str], most: int = 3) -> list[tuple[str, float]]:
    """The runner's own busiest processes (not the simulator's): what else holds its CPU."""
    names: dict[int, str] = {}
    for line in ps.splitlines():
        parts = line.strip().split(None, 1)
        if len(parts) == 2 and parts[0].isdigit():
            names[int(parts[0])] = parts[1].split(" -", 1)[0].rsplit("/", 1)[-1]
    rest = [(names.get(p, str(p)), c) for p, c in cpu.items() if p not in sim and c > 0]
    return sorted(rest, key=lambda x: -x[1])[:most]


def last_sample(top: str) -> tuple[float | None, dict[int, float]]:
    """The runner's idle share and each pid's CPU in the last sample of `top -l 2 -stats pid,cpu`."""
    samples = re.split(r"(?m)^Processes:", top)
    last = samples[-1]
    idle_m = re.search(r"CPU usage:.*?([\d.]+)% idle", last)
    cpu: dict[int, float] = {}
    for line in last.splitlines():
        m = re.match(r"^\s*(\d+)\s+([\d.]+)\s*$", line)
        if m:
            cpu[int(m.group(1))] = float(m.group(2))
    return (float(idle_m.group(1)) if idle_m else None), cpu


def look(udid: str) -> tuple[float, float | None, list[tuple[str, float]], list[tuple[str, float]]]:
    ps = subprocess.run(["ps", "-Ao", "pid=,command="], capture_output=True, text=True, check=False).stdout
    top = subprocess.run(["top", "-l", "2", "-s", "5", "-n", "500", "-stats", "pid,cpu"], capture_output=True, text=True, check=False).stdout
    names = sim_pids(ps, udid)
    idle, cpu = last_sample(top)
    busy = sorted(((names[p], c) for p, c in cpu.items() if p in names), key=lambda x: -x[1])
    return sum(c for _, c in busy), idle, busy, runner_busiest(ps, cpu, names)


def settle(udid: str, log: str, most: int) -> None:
    start = time.monotonic()
    quiet = 0
    with open(log, "a", encoding="utf-8") as out:
        while True:
            total, idle, busy, runner = look(udid)
            took = int(time.monotonic() - start)
            top3 = "; ".join(f"{n} {c:.0f}" for n, c in busy[:3])
            # the runner's own busiest too: a shard ran at 0% idle all through with the
            # simulator at half a core (CI run 41542134785, iPhone 2/6), its audits timing out
            others = "; ".join(f"{n} {c:.0f}" for n, c in runner)
            line = (f"t={took}s simulator={total:.0f}% runner-idle={idle if idle is not None else '?'}% {top3}"
                    f" | runner: {others}")
            print(line, flush=True)
            out.write(line + "\n")
            quiet = quiet + 1 if total < QUIET else 0
            end = None
            if quiet >= LOOKS:
                end = f"settled at {took}s"
            elif took >= most:
                end = f"not settled at {took}s: running the tests"
            if end:
                print(end, flush=True)
                out.write(end + "\n")
                return


def self_test() -> None:
    udid = "41C04E66-7255-421E-96DA-B0F90B1F56D4"
    ps = "\n".join([
        "  101 /sbin/launchd",
        "  202 /Library/Developer/CoreSimulator/Volumes/iOS_23E244/Library/Developer/CoreSimulator/Profiles/Runtimes/iOS 26.4.simruntime/Contents/Resources/RuntimeRoot/usr/libexec/diagnosticd",
        f"  303 /Users/runner/Library/Developer/CoreSimulator/Devices/{udid}/data/Containers/Bundle/Application/X/Maktaba Player.app/Maktaba Player -flag",
        "  404 /usr/libexec/diagnosticd",
        "garbage",
    ])
    assert sim_pids(ps, udid) == {202: "diagnosticd", 303: "Maktaba Player"}, sim_pids(ps, udid)
    top = "Processes: 500 total\nCPU usage: 50.0% user, 20.0% sys, 30.0% idle\nPID    %CPU\n202    99.0\n404    1.0\nProcesses: 500 total\nCPU usage: 10.0% user, 5.0% sys, 85.0% idle\nPID    %CPU\n202    12.5\n303    3.0\n404    40.0"
    idle, cpu = last_sample(top)
    assert idle == 85.0, idle
    assert cpu == {202: 12.5, 303: 3.0, 404: 40.0}, cpu
    assert runner_busiest(ps, cpu, sim_pids(ps, udid)) == [("diagnosticd", 40.0)], runner_busiest(ps, cpu, sim_pids(ps, udid))
    print("sim-settle.py: self-test passed")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("udid", nargs="?")
    parser.add_argument("log", nargs="?")
    parser.add_argument("--max", type=int, default=480)
    parser.add_argument("--self-test", action="store_true")
    args = parser.parse_args()
    if args.self_test:
        self_test()
        return 0
    if not args.udid or not args.log:
        parser.error("UDID and LOG are needed")
    settle(args.udid, args.log, args.max)
    return 0


if __name__ == "__main__":
    sys.exit(main())
