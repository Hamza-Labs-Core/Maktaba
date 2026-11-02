#!/usr/bin/env python3
"""The app's log a UI test job keeps is whole: info and debug lines, not errors alone.

    tools/apple/check-app-log.py build/player-results/iphone-1-app.log
    tools/apple/check-app-log.py --self-test

The Player UI jobs (.github/workflows/_apple-apps.yml) keep the app's own unified log
(MaktabaKit's `Log`, its bundle id the subsystem) next to each result bundle. The system
keeps a subsystem's info and debug lines only while `log config` asks it to (persist:debug);
without that, `log show --info --debug` finds errors and notices alone, and a failed wait
shows nothing of what the app did meanwhile. Every launch writes an info line
("launched <bundle id> <version> (<build>)") and a debug line ("launch arguments: N")
(`Log.launched`), so a kept log that lacks either was cut down to errors: this fails.

The log is `log show --style compact`: date, time, type (Df, I, Db, E, F), the process
(its name may hold spaces) with `[pid:tid]`, then `[subsystem:category]` and the message.
"""
from __future__ import annotations

import argparse
import collections
import re
import sys

LINE = re.compile(r"^\S+ \S+ (?P<type>[A-Za-z]+)\s+.+?\[\d+:[0-9a-fx]+\] \[(?P<subsystem>[^:\]]+):(?P<category>[^\]]+)\] (?P<message>.*)$")
INFO = "I"
DEBUG = "Db"
LAUNCHED = re.compile(r"^launched com\.hamzalabs\.maktaba\S* ")
ARGUMENTS = re.compile(r"^launch arguments: \d+$")


def problems(text: str) -> tuple[list[str], dict[str, int]]:
    """What the log `text` lacks, and how many of the app's lines it holds of each type."""
    types: collections.Counter[str] = collections.Counter()
    launched = arguments = False
    for line in text.splitlines():
        m = LINE.match(line)
        if not m or not m["subsystem"].startswith("com.hamzalabs.maktaba"):
            continue
        types[m["type"]] += 1
        if m["category"] == "session":
            launched |= m["type"] == INFO and bool(LAUNCHED.match(m["message"]))
            arguments |= m["type"] == DEBUG and bool(ARGUMENTS.match(m["message"]))
    found = []
    if not launched:
        found.append('no info line "launched <bundle id> <version>" (Log.launched): info lines were not kept')
    if not arguments:
        found.append('no debug line "launch arguments: N" (Log.launched): debug lines were not kept')
    return found, dict(types)


def self_test() -> None:
    head = "Timestamp               Ty Process[PID:TID]\n"
    info = "2026-11-02 14:35:45.165 I  Maktaba Player[812:1a2b] [com.hamzalabs.maktaba.player:session] launched com.hamzalabs.maktaba.player 1.4.0 (77)\n"
    debug = "2026-11-02 14:35:45.166 Db Maktaba Player[812:1a2b] [com.hamzalabs.maktaba.player:session] launch arguments: 4\n"
    error = "2026-11-02 14:35:47.001 E  Maktaba Player[812:1a2b] [com.hamzalabs.maktaba.player:ui] home failed: offline status=0 type=-\n"
    whole, types = problems(head + info + debug + error)
    assert whole == [] and types == {"I": 1, "Db": 1, "E": 1}, (whole, types)
    # error-only, as it was before persist:debug
    found, _ = problems(head + error)
    assert len(found) == 2 and "info" in found[0] and "debug" in found[1], found
    # info kept, debug not
    found, _ = problems(head + info + error)
    assert len(found) == 1 and "debug" in found[0], found
    # the launch line at the wrong level, or from another subsystem, does not count
    assert problems(info.replace(" I  ", " Df ") + debug)[0]
    assert problems(info.replace("[com.hamzalabs.maktaba.player:", "[com.apple.foo:") + debug)[0]
    # the iPad's and the Mac's bundle ids count
    assert problems(info.replace("player 1.4", "player.tablet 1.4") + debug)[0] == []
    # an empty log (log show failed, the app never ran)
    assert len(problems("")[0]) == 2
    print("check-app-log: self-test passed")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--self-test", action="store_true")
    parser.add_argument("logs", nargs="*")
    args = parser.parse_args()
    if args.self_test:
        self_test()
        return 0
    if not args.logs:
        parser.error("a log to check")
    status = 0
    for path in args.logs:
        try:
            with open(path, encoding="utf-8", errors="replace") as f:
                found, types = problems(f.read())
        except OSError as e:
            found, types = [f"cannot read it: {e}"], {}
        counts = ", ".join(f"{t} {n}" for t, n in sorted(types.items())) or "no lines of the app"
        if found:
            status = 1
            for p in found:
                print(f"::error file={path}::the app's log is not whole: {p} ({counts})", file=sys.stderr)
        else:
            print(f"{path}: whole ({counts})")
    return status


if __name__ == "__main__":
    sys.exit(main())
