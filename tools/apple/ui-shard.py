#!/usr/bin/env python3
"""Splits the Player's UI tests over CI shards by how long each test takes
(.github/workflows/_apple-apps.yml).

    ui-shard.py DEVICE SHARD SHARDS FILE...     # this shard's tests, `Class/testMethod` a line
    ui-shard.py --measure LOG...                # durations from xcodebuild logs, as JSON
    ui-shard.py --self-test

Each file is a test class named after it; each of its `func testX()` is a test. The tests go,
longest first, to the shard with the least time so far (by `ui-durations.json` beside this
script: seconds per `Class/testMethod`, per device, as CI measured them), so the shards end
together rather than hold the same number of tests: one test of the iPhone's took 4 s and
another 16 min. A test not measured yet counts as the device's median. Every test is in
exactly one shard. To measure again, download the shards' logs of a CI run (`gh api
repos/OWNER/REPO/actions/jobs/ID/logs`) and run `--measure` over them into the JSON.
"""
from __future__ import annotations

import json
import os
import re
import statistics
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
DURATIONS = os.path.join(HERE, "ui-durations.json")
TEST = re.compile(r"^\s*(?:@\w+\s+)*func (test\w*)\s*\(\s*\)", re.M)
# xcodebuild's line for a finished test. Its module is the same on both devices (one app for
# iPhone and iPad), so the job's name, near the top of its log, says which one it ran on
CASE = re.compile(r"Test Case '-\[Maktaba_Player_UITests\.(\w+) (\w+)\]' (?:passed|failed) \(([\d.]+) seconds\)")
JOB = re.compile(r"Complete job name: .*\bPlayer UI tests \((iphone|ipad) \w+/\d+\)")


def tests_in(files: list[str]) -> list[str]:
    out = []
    for f in files:
        name = os.path.basename(f).removesuffix(".swift")
        with open(f, encoding="utf-8") as fh:
            out += [f"{name}/{m}" for m in TEST.findall(fh.read())]
    return out


def shards(tests: list[str], durations: dict[str, float], count: int) -> list[list[str]]:
    known = [durations[t] for t in tests if t in durations]
    default = statistics.median(known) if known else 60.0
    weight = {t: durations.get(t, default) for t in tests}
    load, out = [0.0] * count, [[] for _ in range(count)]
    for t in sorted(weight, key=lambda n: (-weight[n], n)):
        i = min(range(count), key=lambda k: (load[k], k))
        load[i] += weight[t]
        out[i].append(t)
    return out


def measure(logs: list[str]) -> dict[str, dict[str, float]]:
    out: dict[str, dict[str, float]] = {"iphone": {}, "ipad": {}}
    for log in logs:
        with open(log, encoding="utf-8", errors="replace") as fh:
            text = fh.read()
        devices = set(JOB.findall(text))
        if len(devices) != 1:
            raise SystemExit(f"{log}: not the log of one 'Player UI tests (iphone|ipad N/M)' job: download each job's "
                             "log on its own")
        device = devices.pop()
        for m in CASE.finditer(text):
            out[device][f"{m.group(1)}/{m.group(2)}"] = round(float(m.group(3)), 1)
    return {d: dict(sorted(v.items())) for d, v in out.items()}


def self_test() -> None:
    import tempfile

    with tempfile.TemporaryDirectory() as d:
        a, b = os.path.join(d, "PlayerAUITests.swift"), os.path.join(d, "PlayerBUITests.swift")
        with open(a, "w") as fh:
            fh.write("func testOne() {}\n    @MainActor func testTwo() throws {}\nfunc testHelper(_ x: Int) {}\nfunc helper() {}\n")
        with open(b, "w") as fh:
            fh.write("    func testLong() async throws {}\n    func testNew() {}\n")
        tests = tests_in([a, b])
        assert tests == ["PlayerAUITests/testOne", "PlayerAUITests/testTwo", "PlayerBUITests/testLong", "PlayerBUITests/testNew"], tests
        durations = {"PlayerAUITests/testOne": 10, "PlayerAUITests/testTwo": 20, "PlayerBUITests/testLong": 100}
        got = shards(tests, durations, 2)
        # the long test alone; the rest (the new one at the median, 20 s) on the other shard
        assert got == [["PlayerBUITests/testLong"], ["PlayerAUITests/testTwo", "PlayerBUITests/testNew", "PlayerAUITests/testOne"]], got
        assert sorted(sum(shards(tests, {}, 3), [])) == sorted(tests), "every test in one shard"
        # the same module on both devices: the job says which device a log is of
        def log(name: str, job: str, *cases: str) -> str:
            path = os.path.join(d, name)
            with open(path, "w") as fh:
                fh.write(f"2026Z Complete job name: {job}\n" if job else "")
                fh.writelines(f"2026Z Test Case '-[Maktaba_Player_UITests.{c} seconds).\n" for c in cases)
            return path

        ipad = log("a.log", "apple-apps / Player UI tests (ipad 2/6)", "PlayerAUITests testOne]' passed (12.345")
        phone = log("b.log", "ci / apple-apps / Player UI tests (iphone 2/6)",
                    "PlayerAUITests testOne]' passed (5.0", "PlayerBUITests testLong]' failed (99.0")
        shots = log("c.log", "apple-apps / Player UI tests (ipad shots/6)", "PlayerDemoShots testShots]' passed (300.0")
        assert measure([ipad, phone, shots]) == {
            "iphone": {"PlayerAUITests/testOne": 5.0, "PlayerBUITests/testLong": 99.0},
            "ipad": {"PlayerAUITests/testOne": 12.3, "PlayerDemoShots/testShots": 300.0},
        }
        # a log that names no device, or another job's, is refused rather than counted as an iPhone's
        for other in (log("d.log", "", "PlayerAUITests testOne]' passed (5.0"),
                      log("e.log", "apple-apps / Player UI tests (mac)", "PlayerAUITests testOne]' passed (5.0")):
            try:
                measure([other])
            except SystemExit:
                continue
            raise AssertionError(f"{other} measured")
    print("ui-shard.py: self-test passed")


if __name__ == "__main__":
    if sys.argv[1] == "--self-test":
        self_test()
    elif sys.argv[1] == "--measure":
        print(json.dumps(measure(sys.argv[2:]), indent=2, ensure_ascii=False))
    else:
        device, shard, count = sys.argv[1], int(sys.argv[2]), int(sys.argv[3])
        with open(DURATIONS, encoding="utf-8") as fh:
            durations = json.load(fh)[device]
        tests = tests_in(sys.argv[4:])
        mine = shards(tests, durations, count)[shard - 1]
        known = [durations[t] for t in tests if t in durations]
        default = statistics.median(known) if known else 60.0
        print(f"shard {shard}/{count} ({device}): {len(mine)} tests, about "
              f"{sum(durations.get(t, default) for t in mine) / 60:.0f} min", file=sys.stderr)
        print("\n".join(mine))
