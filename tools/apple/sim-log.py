#!/usr/bin/env python3
"""Keeps a CI simulator's logs with the results, then deletes the simulator.

    tools/apple/sim-log.py UDID RESULTS NAME
    tools/apple/sim-log.py --self-test

Writes, under RESULTS:
- NAME-app.log: the app's own lines (subsystems com.hamzalabs.maktaba*), info and debug
  too, over the simulator's whole life: every launch.
- NAME-ends.log: what the system said of the app's processes ending (RunningBoard's exits,
  jetsam, watchdog, crashes). A test that failed with "the app is not running" says why
  here; the app's own log stops without a word when it is killed.
- NAME-crashes/: the host's crash reports of the Maktaba apps made since the simulator was
  (a simulator app's crash report is written on the host, ~/Library/Logs/DiagnosticReports).

A simulator that is no longer booted when its log is read had stopped under the tests (CI:
"device is not booted" after a shard's tests all passed, and the step then read no line of
the app at all). It is said, with what CoreSimulator logged on the host meanwhile
(NAME-coresimulator.log), and the simulator is booted again to read its log: the log is kept
on the simulator's disk, so every launch's lines are still there.
"""
from __future__ import annotations

import json
import os
import pathlib
import shutil
import subprocess
import sys
import time

APP = 'subsystem BEGINSWITH "com.hamzalabs.maktaba"'
# the system's word on an app process ending: RunningBoard's exit and termination lines,
# jetsam and the watchdog, and the crash reporter
ENDS = ('(eventMessage CONTAINS[c] "maktaba") AND (process == "runningboardd" OR process == "SpringBoard" '
        'OR process == "ReportCrash" OR process == "osanalyticshelper" OR process == "launchd") AND '
        '(eventMessage CONTAINS[c] "exit" OR eventMessage CONTAINS[c] "terminat" OR eventMessage CONTAINS[c] "jetsam" '
        'OR eventMessage CONTAINS[c] "watchdog" OR eventMessage CONTAINS[c] "crash" OR eventMessage CONTAINS[c] "kill")')


def state(devices: dict, udid: str) -> str | None:
    """The device's state in `simctl list devices -j` output, None when there is no such device."""
    for runtime in devices.get("devices", {}).values():
        for d in runtime:
            if d.get("udid") == udid:
                return d.get("state")
    return None


def crash_reports(folder: pathlib.Path, since: float) -> list[pathlib.Path]:
    """The Maktaba apps' crash reports in `folder` made at or after `since` (seconds)."""
    if not folder.is_dir():
        return []
    return sorted(p for p in folder.iterdir()
                  if p.is_file() and "maktaba" in p.name.lower() and p.suffix in (".ips", ".crash")
                  and p.stat().st_mtime >= since)


def run(*args: str, out: pathlib.Path | None = None) -> int:
    if out is None:
        return subprocess.run(args).returncode
    with out.open("w") as f:
        return subprocess.run(args, stdout=f).returncode


def made_at(udid: str) -> float:
    """When the simulator was made: its device folder's age (the job's start when unknown)."""
    folder = pathlib.Path.home() / "Library/Developer/CoreSimulator/Devices" / udid
    try:
        return folder.stat().st_birthtime
    except (OSError, AttributeError):
        return time.time() - 3 * 3600


def main(udid: str, results: pathlib.Path, name: str) -> int:
    results.mkdir(parents=True, exist_ok=True)
    since = made_at(udid)
    listed = subprocess.run(["xcrun", "simctl", "list", "devices", "-j"], capture_output=True, text=True)
    now = state(json.loads(listed.stdout or "{}"), udid) if listed.returncode == 0 else None
    if now is None:
        print(f"::warning::the simulator {udid} is gone: no log of the app to read")
        return 0
    if now != "Booted":
        minutes = max(1, int((time.time() - since) / 60) + 1)
        print(f"::warning::the simulator had stopped ({now}) before its log was read: it stopped under the "
              f"tests, CoreSimulator's lines are in {name}-coresimulator.log; booted again to read its log")
        run("log", "show", "--style", "compact", "--info", "--last", f"{minutes}m",
            "--predicate", 'subsystem BEGINSWITH "com.apple.CoreSimulator" OR process == "launchd_sim"',
            out=results / f"{name}-coresimulator.log")
        run("xcrun", "simctl", "bootstatus", udid, "-b")
    run("xcrun", "simctl", "spawn", udid, "log", "show", "--style", "compact", "--info", "--debug",
        "--predicate", APP, out=results / f"{name}-app.log")
    run("xcrun", "simctl", "spawn", udid, "log", "show", "--style", "compact", "--info",
        "--predicate", ENDS, out=results / f"{name}-ends.log")
    reports = crash_reports(pathlib.Path.home() / "Library/Logs/DiagnosticReports", since)
    if reports:
        crashes = results / f"{name}-crashes"
        crashes.mkdir(exist_ok=True)
        for p in reports:
            shutil.copy2(p, crashes / p.name)
        print(f"::warning::{len(reports)} crash report(s) of the app: {', '.join(p.name for p in reports)} "
              f"(kept in {name}-crashes)")
    run("xcrun", "simctl", "shutdown", udid)
    run("xcrun", "simctl", "delete", udid)
    return 0


def self_test() -> int:
    import tempfile
    devices = {"devices": {"iOS-27-2": [{"udid": "A", "state": "Booted"}, {"udid": "B", "state": "Shutdown"}]}}
    assert state(devices, "A") == "Booted"
    assert state(devices, "B") == "Shutdown"
    assert state(devices, "C") is None
    with tempfile.TemporaryDirectory() as d:
        folder = pathlib.Path(d)
        old = folder / "Maktaba Player-2026-11-19-010000.ips"
        new = folder / "Maktaba Player-2026-11-20-045012.ips"
        other = folder / "Safari-2026-11-20-045012.ips"
        text = folder / "Maktaba Player.txt"
        for p in (old, new, other, text):
            p.write_text("{}")
        os.utime(old, (1000, 1000))
        found = crash_reports(folder, 2000)
        assert found == [new], found
        assert crash_reports(folder / "none", 0) == []
    print("sim-log: self-test passed")
    return 0


if __name__ == "__main__":
    if sys.argv[1:] == ["--self-test"]:
        sys.exit(self_test())
    if len(sys.argv) != 4:
        print(__doc__, file=sys.stderr)
        sys.exit(2)
    sys.exit(main(sys.argv[1], pathlib.Path(sys.argv[2]), sys.argv[3]))
