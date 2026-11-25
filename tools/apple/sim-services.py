#!/usr/bin/env python3
"""Turns services of a booted simulator off, or starts them, and sees that it took.

    tools/apple/sim-services.py UDID --off com.apple.apsd [--off ...] [--budget SECONDS]
    tools/apple/sim-services.py UDID --start system/com.apple.TextInput.kbd [--budget SECONDS]
    tools/apple/sim-services.py --self-test

Each `simctl spawn` starts a process in the simulator, and just after a boot on a CI runner that
is slow: one shard's calls took 97 to 410 s together (run 45546321917, 14 shards). A 30 s limit
on each cut calls on all 14 shards of the next run and left apsd running on 5 (45553400654). So
the simulator's launchd is asked for its services first (until it answers, nothing is done);
all are disabled and booted out; and those a list still shows are again, until a list shows
none of them or BUDGET is gone. A service to start is kicked off until launchd says it runs
(a process only spawned can be held for long before it does: job 139001674314). One call
may take CALL seconds at most, so one that never answers leaves time to ask again. Each call is
printed with the time it took. A service not seen to go, or to run, is a warning, and the exit
status is 0: the tests run anyway.
"""
from __future__ import annotations

import argparse
import subprocess
import sys
import time
from collections.abc import Callable

BUDGET = 600.0  # seconds, all calls together
CALL = 200.0  # seconds, one call at most
PAUSE = 5.0  # seconds between two rounds
DOMAIN = "user/foreground"

# a launchctl call: its arguments and its time limit -> its output, None when cut or failed
Call = Callable[[list[str], float], "str | None"]


def launchctl(udid: str) -> Call:
    def call(args: list[str], limit: float) -> str | None:
        start = time.monotonic()
        try:
            done = subprocess.run(["xcrun", "simctl", "spawn", udid, "launchctl", *args],
                                  capture_output=True, text=True, errors="replace", timeout=limit, check=False)
            out, how = (done.stdout if done.returncode == 0 else None), f"exit {done.returncode}"
        except subprocess.TimeoutExpired:
            out, how = None, "cut"
        print(f"launchctl {' '.join(args)}: {how}, {time.monotonic() - start:.1f}s", flush=True)
        return out
    return call


def listed(out: str | None) -> set[str] | None:
    """The labels `launchctl list` shows; None when it showed nothing (cut, failed or empty)."""
    if not out:
        return None
    labels = {parts[2] for parts in (line.split("\t") for line in out.splitlines()[1:]) if len(parts) == 3}
    return labels or None


def turn_off(services: list[str], call: Call, budget: float = BUDGET, *,
             clock: Callable[[], float] = time.monotonic, sleep: Callable[[float], None] = time.sleep) -> str | None:
    """None once a list shows none of `services`, else what to warn of."""
    end = clock() + budget
    last: set[str] | None = None  # what the last list that answered showed

    def ask(*args: str) -> str | None:
        left = end - clock()
        return call(list(args), min(CALL, left)) if left > 0 else None

    acted = False  # a service not loaded yet is not in the first list, and is not gone
    first = True
    while first or clock() < end:
        if not first:
            sleep(min(PAUSE, max(0.0, end - clock())))
        first = False
        labels = listed(ask("list"))
        if labels is None:
            continue
        last = labels
        there = [s for s in services if s in labels] if acted else services
        if not there:
            return None
        acted = True
        for service in there:
            ask("disable", f"{DOMAIN}/{service}")
            ask("bootout", f"{DOMAIN}/{service}")
    if last is None:
        return f"the simulator did not list its services: {', '.join(services)} may still run"
    return f"not seen to go in {budget:.0f}s: {', '.join(s for s in services if s in last)}"


def running(out: str | None) -> bool:
    """Whether `launchctl print` of a service says that it runs."""
    return any(line.strip() == "state = running" for line in (out or "").splitlines())


def start(service: str, call: Call, budget: float = BUDGET, *,
          clock: Callable[[], float] = time.monotonic, sleep: Callable[[float], None] = time.sleep) -> str | None:
    """None once launchd says `service` (its domain with it) runs, else what to warn of."""
    end = clock() + budget

    def ask(*args: str) -> str | None:
        left = end - clock()
        return call(list(args), min(CALL, left)) if left > 0 else None

    while True:
        ask("kickstart", service)
        if running(ask("print", service)):
            return None
        if clock() >= end:
            return f"not seen to run in {budget:.0f}s: {service}"
        sleep(min(PAUSE, end - clock()))


class Fake:
    """A simulator for the self-test: each call takes `takes` seconds (more than its limit: cut)."""

    def __init__(self, services: list[str], takes: Callable[[int], float] = lambda n: 1.0, stuck: bool = False):
        self.there, self.takes, self.stuck = list(services), takes, stuck
        self.runs_at = float("inf")  # a kickstarted service runs from then on
        self.now, self.calls = 0.0, []

    def sleep(self, seconds: float) -> None:
        self.now += seconds

    def call(self, args: list[str], limit: float) -> str | None:
        assert 0 < limit <= CALL, limit
        takes = self.takes(len(self.calls))
        self.calls.append(" ".join(args))
        self.now += min(takes, limit)
        if takes > limit:
            return None
        if args[0] == "list":
            return "PID\tStatus\tLabel\n" + "".join(f"12\t0\t{s}\n" for s in [*self.there, "com.apple.other"])
        if args[0] == "print":
            return f"x = {{\n\tstate = {'running' if self.now >= self.runs_at else 'xpcproxy'}\n}}"
        name = args[1].removeprefix(DOMAIN + "/")
        if args[0] == "bootout" and not self.stuck and name in self.there:
            self.there.remove(name)
        return ""

    def off(self, budget: float = BUDGET) -> str | None:
        return turn_off(["a", "b"], self.call, budget, clock=lambda: self.now, sleep=self.sleep)


def self_test() -> None:
    assert listed("PID\tStatus\tLabel\n-\t0\tcom.apple.x\n9\t0\tcom.apple.y\n") == {"com.apple.x", "com.apple.y"}
    assert listed(None) is None and listed("") is None and listed("PID\tStatus\tLabel\n") is None

    sim = Fake(["a", "b"])
    assert sim.off() is None
    assert sim.calls == ["list", "disable user/foreground/a", "bootout user/foreground/a",
                         "disable user/foreground/b", "bootout user/foreground/b", "list"], sim.calls
    late = Fake([])  # not loaded yet: disabled all the same
    assert late.off() is None and len(late.calls) == 6, late.calls

    # every call slow, as on a runner just after a boot
    slow = Fake(["a", "b"], lambda n: 70.0)
    assert slow.off() is None and slow.now < BUDGET, slow.now
    # the first calls cut: asked again
    cut = Fake(["a", "b"], lambda n: 999.0 if n < 2 else 1.0)
    assert cut.off() is None and cut.calls[:3] == ["list"] * 3, cut.calls
    # a bootout cut: the list shows its service, and it is booted out again, alone
    again = Fake(["a", "b"], lambda n: 999.0 if n == 2 else 1.0)
    assert again.off() is None
    assert again.calls[5:] == ["list", "disable user/foreground/a", "bootout user/foreground/a", "list"], again.calls

    # never gone, never answering: the budget ends it, and says which
    stuck = Fake(["b"], stuck=True)
    assert stuck.off(60.0) == "not seen to go in 60s: b"
    assert 60 <= stuck.now < 62, stuck.now
    dead = Fake(["a", "b"], lambda n: 999.0)
    assert dead.off() == "the simulator did not list its services: a, b may still run"
    assert dead.now == BUDGET, dead.now

    assert running("s = {\n\tstate = running\n\tjob state = exited\n}") and not running("\tjob state = running\n")
    assert not running("\tstate = xpcproxy\n") and not running(None)
    kbd = Fake([])

    def kick(budget: float = BUDGET) -> str | None:
        return start("system/k", kbd.call, budget, clock=lambda: kbd.now, sleep=kbd.sleep)

    kbd.runs_at = 0.0
    assert kick() is None and kbd.calls == ["kickstart system/k", "print system/k"], kbd.calls
    kbd = Fake([])
    kbd.runs_at = 20.0  # spawned, and held
    assert kick() is None and 20 <= kbd.now < 30, kbd.now
    kbd = Fake([])
    assert kick(60.0) == "not seen to run in 60s: system/k" and 60 <= kbd.now < 62, kbd.now
    kbd = Fake([], lambda n: 999.0)
    assert kick() == "not seen to run in 600s: system/k" and kbd.now == BUDGET, kbd.now
    print("sim-services.py: self-test passed")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("udid", nargs="?")
    parser.add_argument("--off", action="append", default=[], metavar="SERVICE")
    parser.add_argument("--start", metavar="DOMAIN/SERVICE")
    parser.add_argument("--budget", type=float, default=BUDGET)
    parser.add_argument("--self-test", action="store_true")
    args = parser.parse_args()
    if args.self_test:
        self_test()
        return 0
    if not args.udid or bool(args.off) == bool(args.start):
        parser.error("UDID, and --off or --start, are needed")
    began = time.monotonic()
    call = launchctl(args.udid)
    if args.off:
        wrong, why = turn_off(args.off, call, args.budget), "its load can fail a test"
        done = f"off: {', '.join(args.off)}"
    else:
        wrong, why = start(args.start, call, args.budget), "a test that needs it may wait for it"
        done = f"running: {args.start}"
    print(f"::warning::{wrong}; {why} (#1678)" if wrong else f"{done} ({time.monotonic() - began:.0f}s)", flush=True)
    return 0


if __name__ == "__main__":
    sys.exit(main())
