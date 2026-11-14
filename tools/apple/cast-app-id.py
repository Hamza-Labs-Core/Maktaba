#!/usr/bin/env python3
"""The Apple Player's Cast receiver id, from shared/cast/app.json (Epic 58 story 58.06).

    tools/apple/cast-app-id.py            # write apps/apple/Config/Cast.xcconfig
    tools/apple/cast-app-id.py --check    # fail when it does not match app.json (CI)
    tools/apple/cast-app-id.py --self-test

The xcconfig carries `receiver_app_id` as MAKTABA_CAST_APP_ID (Info.plist
MaktabaCastReceiverAppID) and the Bonjour services the Cast SDK browses
(NSBonjourServices: `_googlecast._tcp` and `_<APPID>._googlecast._tcp`). Info.plist cannot
leave an entry out, so with no id both services read `_maktaba._tcp`, the entry the home
browser already lists: no Cast service is declared, and the app never starts the SDK.
A build can name another receiver without editing the file:
    xcodebuild MAKTABA_CAST_APP_ID=ABCD1234 MAKTABA_CAST_ENABLED=YES …
"""
import argparse
import json
import os
import re
import sys

ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))
APP = os.path.join(ROOT, "shared", "cast", "app.json")
OUT = os.path.join(ROOT, "apps", "apple", "Config", "Cast.xcconfig")
# a Cast console application id: letters and digits (Google's are 8 hex digits)
ID = re.compile(r"^[A-Za-z0-9]*$")


def render(app_id: str) -> str:
    if not ID.match(app_id):
        raise ValueError(f"receiver_app_id {app_id!r} is not a Cast application id")
    enabled = "YES" if app_id else "NO"
    return f"""// Generated from shared/cast/app.json by tools/apple/cast-app-id.py: do not edit by hand.
// The Cast receiver the Player casts to (Epic 58 story 58.06). Empty: no Cast button, the
// Google Cast SDK never started, no Cast Bonjour service declared.
MAKTABA_CAST_APP_ID ={" " + app_id if app_id else ""}
MAKTABA_CAST_ENABLED = {enabled}
MAKTABA_CAST_BONJOUR_NO = _maktaba._tcp
MAKTABA_CAST_BONJOUR_APP_NO = _maktaba._tcp
MAKTABA_CAST_BONJOUR_YES = _googlecast._tcp
MAKTABA_CAST_BONJOUR_APP_YES = _$(MAKTABA_CAST_APP_ID)._googlecast._tcp
MAKTABA_CAST_BONJOUR = $(MAKTABA_CAST_BONJOUR_$(MAKTABA_CAST_ENABLED))
MAKTABA_CAST_BONJOUR_APP = $(MAKTABA_CAST_BONJOUR_APP_$(MAKTABA_CAST_ENABLED))
"""


def wanted() -> str:
    with open(APP, encoding="utf-8") as fh:
        return render(str(json.load(fh).get("receiver_app_id", "")).strip())


def self_test() -> None:
    off = render("")
    assert "MAKTABA_CAST_APP_ID =\n" in off and "MAKTABA_CAST_ENABLED = NO" in off
    on = render("ABCD1234")
    assert "MAKTABA_CAST_APP_ID = ABCD1234" in on and "MAKTABA_CAST_ENABLED = YES" in on
    for bad in ("AB CD", "$(X)", "a/b"):
        try:
            render(bad)
        except ValueError:
            continue
        raise AssertionError(f"{bad!r} accepted")
    print("cast-app-id: self-test ok")


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--check", action="store_true")
    ap.add_argument("--self-test", action="store_true")
    a = ap.parse_args()
    if a.self_test:
        self_test()
        return 0
    text = wanted()
    if a.check:
        have = open(OUT, encoding="utf-8").read() if os.path.exists(OUT) else ""
        if have != text:
            print(f"{os.path.relpath(OUT, ROOT)} does not match shared/cast/app.json: run tools/apple/cast-app-id.py",
                  file=sys.stderr)
            return 1
        print("cast-app-id: Cast.xcconfig matches shared/cast/app.json")
        return 0
    with open(OUT, "w", encoding="utf-8") as fh:
        fh.write(text)
    print(f"wrote {os.path.relpath(OUT, ROOT)}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
