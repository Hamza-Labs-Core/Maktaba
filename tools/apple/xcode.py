#!/usr/bin/env python3
"""The Xcode, the simulators and the SDK check of the Apple jobs (_apple-apps.yml, testflight.yml).

    tools/apple/xcode.py select [--min 27] [--sdk iphoneos]
    tools/apple/xcode.py simulator {iphone,ipad,appletv} NAME
    tools/apple/xcode.py check-sdk [--min 27] ARCHIVE.xcarchive
    tools/apple/xcode.py --self-test

`select` switches to the newest released Xcode on the runner (`/Applications/Xcode_<version>.app`;
a beta's folder, `Xcode_27_beta_3.app`, is never picked: App Store Connect refuses its builds)
and fails, naming what it found, when that Xcode is older than --min. App Store Connect wants
the iOS 27 SDK from April 2027 (ITMS-90725), so every Apple job asks for 27. With --sdk it also
fails when that Xcode has no such SDK (`xcodebuild -showsdks`).

`simulator` makes a throwaway simulator and prints its UDID. It tries the pairs in order and
keeps the first that `simctl create` accepts (docs/testflight.md, "CI's simulators"): runtimes
newest first, each with the device types it lists as supported. Xcode 27's newest runtimes do not
take every device type (iOS 27.1 is the iPhone Duo's alone; tvOS 27 has no first Apple TV 4K),
so the newest runtime and the newest device type are not always a pair. iphone: the
highest-numbered plain "iPhone N"; ipad: the highest "iPad Pro 11-inch (MN)"; appletv: the
newest generation of 4K Apple TV (not its 1080p form). No pair: it fails with what it tried.

`check-sdk` reads each app's Info.plist in an archive and fails unless it was built with a
release Xcode of --min or later and its platform SDK is --min or later (`DTXcode`, `DTSDKName`,
`DTPlatformVersion`): the upload's guard against an Xcode older than App Store Connect takes.
"""

from __future__ import annotations

import argparse
import contextlib
import io
import json
import os
import plistlib
import re
import subprocess
import sys
import tempfile
from collections.abc import Callable, Iterable
from pathlib import Path

MIN = 27
APPS = Path("/Applications")
RELEASE = re.compile(r"^Xcode_(\d+(?:\.\d+)*)\.app$")
KINDS = {
    "iphone": ("iOS", re.compile(r"^iPhone (\d+)$")),
    "ipad": ("iOS", re.compile(r"^iPad Pro 11-inch \(M(\d+)\)$")),
    # "Apple TV 4K" (the first) or "Apple TV 4K (3rd generation)"; never "(at 1080p)"
    "appletv": ("tvOS", re.compile(r"^Apple TV 4K(?: \((\d+)(?:st|nd|rd|th) generation\))?$")),
}


def version(text: str) -> tuple[int, ...]:
    return tuple(int(p) for p in text.split("."))


# ---- select


def newest_xcode(names: Iterable[str], real: Callable[[str], str] = lambda n: n) -> tuple[str, tuple[int, ...]] | None:
    """The newest released Xcode among /Applications' entries: (name, version), or None.

    `real` resolves a name to the app it is (the image's `Xcode_27.2.0.app` is a link to
    `Xcode_27.2.app`); two names for one app count once, under the first seen."""
    best: tuple[str, tuple[int, ...]] | None = None
    seen: set[str] = set()
    for name in sorted(names):
        m = RELEASE.match(name)
        if not m:
            continue
        target = real(name)
        if target in seen:
            continue
        seen.add(target)
        v = version(m.group(1))
        if best is None or v > best[1]:
            best = (name, v)
    return best


def xcodebuild_major(output: str) -> int:
    """The major version in `xcodebuild -version`'s first line ("Xcode 27.2")."""
    m = re.match(r"Xcode (\d+)", output)
    if not m:
        raise ValueError(f"not an Xcode version: {output.splitlines()[0] if output else output!r}")
    return int(m.group(1))


def select(minimum: int, sdk: str | None) -> int:
    names = os.listdir(APPS)
    found = newest_xcode(names, lambda n: os.path.realpath(APPS / n))
    listed = ", ".join(sorted(n for n in names if n.startswith("Xcode"))) or "none"
    if found is None or found[1][0] < minimum:
        print(f"::error::no released Xcode {minimum} or later on this runner (it has: {listed}). "
              f"App Store Connect wants the {minimum} SDKs; run the job on an image with Xcode {minimum}.")
        return 1
    path = APPS / found[0]
    subprocess.run(["sudo", "xcode-select", "-s", str(path / "Contents/Developer")], check=True)
    out = subprocess.run(["xcodebuild", "-version"], check=True, capture_output=True, text=True).stdout
    print(out, end="")
    if xcodebuild_major(out) < minimum:
        print(f"::error::{path} says {out.splitlines()[0]}, older than Xcode {minimum}")
        return 1
    if sdk:
        sdks = subprocess.run(["xcodebuild", "-showsdks"], check=True, capture_output=True, text=True).stdout
        got = sdk_versions(sdks, sdk)
        print(f"{sdk} SDKs: {', '.join('.'.join(map(str, v)) for v in got) or 'none'}")
        if not got or max(got)[0] < minimum:
            print(f"::error::no {sdk} SDK {minimum} or later in {path}")
            return 1
    return 0


def sdk_versions(showsdks: str, sdk: str) -> list[tuple[int, ...]]:
    """The versions of an SDK family in `xcodebuild -showsdks` (`-sdk iphoneos27.2`)."""
    return [version(m) for m in re.findall(rf"-sdk {re.escape(sdk)}(\d+(?:\.\d+)*)\b", showsdks)]


# ---- simulator


def candidates(kind: str, listing: dict) -> list[tuple[str, str, str]]:
    """(runtime id, device type id, label) pairs to try, best first, from `simctl list -j`."""
    platform, pattern = KINDS[kind]
    runtimes = [r for r in listing["runtimes"] if r.get("platform") == platform and r.get("isAvailable")]
    runtimes.sort(key=lambda r: version(r["version"]), reverse=True)
    pairs: list[tuple[str, str, str]] = []
    for r in runtimes:
        ranked = []
        for d in r.get("supportedDeviceTypes", []):
            m = pattern.match(d["name"])
            if m:
                ranked.append((int(m.group(1) or 1), d["identifier"], d["name"]))
        for _, ident, name in sorted(ranked, reverse=True):
            pairs.append((r["identifier"], ident, f"{name} on {r['name']}"))
    return pairs


def make_simulator(kind: str, name: str, create: Callable[[str, str, str], str | None], listing: dict) -> str | None:
    tried = []
    for runtime, devtype, label in candidates(kind, listing):
        udid = create(name, devtype, runtime)
        if udid:
            print(f"{kind}: {label} ({devtype}, {runtime})", file=sys.stderr)
            return udid
        tried.append(label)
    # stderr: the workflow reads stdout into udid=$(...), and that is lost when the step fails
    print(f"::error::no {kind} simulator could be made; tried: {', '.join(tried) or 'no matching device type in any runtime'}",
          file=sys.stderr)
    return None


def simctl_create(name: str, devtype: str, runtime: str) -> str | None:
    # exits non-zero and makes nothing when it refuses the pair ("Incompatible device")
    p = subprocess.run(["xcrun", "simctl", "create", name, devtype, runtime], capture_output=True, text=True)
    if p.returncode != 0:
        print(f"refused: {devtype} on {runtime}: {p.stderr.strip().splitlines()[-1] if p.stderr.strip() else p.returncode}",
              file=sys.stderr)
        return None
    return p.stdout.strip()


def simulator(kind: str, name: str) -> int:
    listing = json.loads(subprocess.run(["xcrun", "simctl", "list", "runtimes", "-j"], check=True,
                                        capture_output=True, text=True).stdout)
    udid = make_simulator(kind, name, simctl_create, listing)
    if not udid:
        return 1
    print(udid)
    return 0


# ---- check-sdk


def sdk_problems(info: dict, minimum: int) -> list[str]:
    """Why an app's Info.plist shows it built with an SDK or Xcode older than `minimum`."""
    problems = []
    sdk_name = str(info.get("DTSDKName", ""))
    m = re.match(r"^[a-z]+?(\d+)(?:\.\d+)*", sdk_name)
    if not m:
        problems.append(f"DTSDKName {sdk_name or 'missing'}")
    elif int(m.group(1)) < minimum:
        problems.append(f"DTSDKName {sdk_name} (SDK {m.group(1)} < {minimum})")
    platform = str(info.get("DTPlatformVersion", ""))
    if not re.match(r"^\d+(?:\.\d+)*$", platform):
        problems.append(f"DTPlatformVersion {platform or 'missing'}")
    elif version(platform)[0] < minimum:
        problems.append(f"DTPlatformVersion {platform} (< {minimum})")
    # DTXcode: "2720" for Xcode 27.2
    xcode = str(info.get("DTXcode", ""))
    if not xcode.isdigit():
        problems.append(f"DTXcode {xcode or 'missing'}")
    elif int(xcode) < minimum * 100:
        problems.append(f"DTXcode {xcode} (Xcode older than {minimum})")
    return problems


def check_sdk(archive: Path, minimum: int) -> int:
    apps = sorted((archive / "Products/Applications").glob("*.app"))
    if not apps:
        print(f"::error::no app in {archive}/Products/Applications")
        return 1
    status = 0
    for app in apps:
        plist = app / "Contents/Info.plist" if (app / "Contents/Info.plist").exists() else app / "Info.plist"
        info = plistlib.loads(plist.read_bytes())
        problems = sdk_problems(info, minimum)
        built = f"{info.get('DTSDKName')}, platform {info.get('DTPlatformVersion')}, Xcode {info.get('DTXcode')} ({info.get('DTXcodeBuild')})"
        if problems:
            print(f"::error::{app.name} was built with an SDK App Store Connect will refuse ({built}): {'; '.join(problems)}")
            status = 1
        else:
            print(f"{app.name}: {built}")
    return status


# ---- self-test


def self_test() -> int:
    # the cases below print ::error:: lines on purpose: kept out of the log, where GitHub
    # would show them as the job's errors
    with contextlib.redirect_stdout(io.StringIO()), contextlib.redirect_stderr(io.StringIO()):
        _self_test()
    print("xcode.py self-test: ok")
    return 0


def _self_test() -> None:
    links = {"Xcode.app": "Xcode_27.1.app", "Xcode_27.2.0.app": "Xcode_27.2.app", "Xcode_27.0.app": "Xcode_27.app",
             "Xcode_27.0.0.app": "Xcode_27.app"}
    names = ["Xcode.app", "Xcode_27.app", "Xcode_27.0.app", "Xcode_27.0.0.app", "Xcode_27.1.app",
             "Xcode_27.2.app", "Xcode_27.2.0.app", "Xcode_28_beta_1.app", "Xcode_26.6.app"]
    # Xcode_27.2.0.app and Xcode_27.2.app are one app; the beta and Xcode.app never count
    assert newest_xcode(names, lambda n: links.get(n, n)) == ("Xcode_27.2.0.app", (27, 2, 0))
    assert newest_xcode(["Xcode_26.6.app", "Xcode_27.app"]) == ("Xcode_27.app", (27,))
    # a beta is never picked, however new
    assert newest_xcode(["Xcode_27_beta_3.app", "Xcode_26.6.app"]) == ("Xcode_26.6.app", (26, 6))
    assert newest_xcode(["Xcode_27_beta_3.app", "Xcode.app"]) is None
    assert newest_xcode(["Xcode_26.10.app", "Xcode_26.9.app"]) == ("Xcode_26.10.app", (26, 10))
    assert xcodebuild_major("Xcode 27.2\nBuild version 27B52\n") == 27
    shows = "iOS SDKs:\n\tiOS 27.2 -sdk iphoneos27.2\n\niOS Simulator SDKs:\n\tSimulator - iOS 27.2 -sdk iphonesimulator27.2\n"
    assert sdk_versions(shows, "iphoneos") == [(27, 2)]
    assert sdk_versions(shows, "appletvos") == []

    def rt(platform: str, ver: str, types: list[str]) -> dict:
        ident = f"com.apple.CoreSimulator.SimRuntime.{platform}-{ver.replace('.', '-')}"
        return {"platform": platform, "version": ver, "isAvailable": True, "identifier": ident,
                "name": f"{platform} {ver}",
                "supportedDeviceTypes": [{"name": t, "identifier": "type." + t.replace(" ", "-")} for t in types]}

    # what Xcode 27.1 answers on a Mac (docs/testflight.md): iOS 27.1 is the Duo's alone
    listing = {"runtimes": [
        rt("iOS", "27.1", ["iPhone Duo"]),
        rt("iOS", "27.0", ["iPhone 16", "iPhone 17", "iPhone 17 Pro", "iPhone 18 Pro", "iPhone 17e",
                           "iPad Pro 11-inch (M4)", "iPad Pro 11-inch (M5)", "iPad Pro 13-inch (M5)"]),
        rt("iOS", "26.4", ["iPhone 17", "iPad Pro 11-inch (M5)"]),
        rt("tvOS", "27.0", ["Apple TV 4K (2nd generation)", "Apple TV 4K (3rd generation)",
                            "Apple TV 4K (3rd generation) (at 1080p)"]),
        rt("tvOS", "26.4", ["Apple TV", "Apple TV 4K", "Apple TV 4K (3rd generation)"]),
        {**rt("tvOS", "27.2", ["Apple TV 4K (4th generation)"]), "isAvailable": False},
    ]}
    assert candidates("iphone", listing)[0][:2] == ("com.apple.CoreSimulator.SimRuntime.iOS-27-0", "type.iPhone-17")
    assert candidates("iphone", listing)[1][0].endswith("iOS-27-0") and candidates("iphone", listing)[1][1] == "type.iPhone-16"
    assert candidates("ipad", listing)[0][1] == "type.iPad-Pro-11-inch-(M5)"
    tv = candidates("appletv", listing)
    assert tv[0][:2] == ("com.apple.CoreSimulator.SimRuntime.tvOS-27-0", "type.Apple-TV-4K-(3rd-generation)"), tv
    # the first Apple TV 4K ranks last on a runtime that still takes it; 1080p and plain Apple TV never
    assert [t[1] for t in tv if t[0].endswith("tvOS-26-4")] == ["type.Apple-TV-4K-(3rd-generation)", "type.Apple-TV-4K"]
    assert not any("1080p" in t[1] for t in tv)

    # a refused pair moves on to the next; none accepted fails
    refused = {("type.iPhone-17", "com.apple.CoreSimulator.SimRuntime.iOS-27-0")}
    made: list[tuple[str, str]] = []

    def create(_name: str, devtype: str, runtime: str) -> str | None:
        made.append((devtype, runtime))
        return None if (devtype, runtime) in refused else "UDID-1"

    assert make_simulator("iphone", "t", create, listing) == "UDID-1"
    assert made == [("type.iPhone-17", "com.apple.CoreSimulator.SimRuntime.iOS-27-0"),
                    ("type.iPhone-16", "com.apple.CoreSimulator.SimRuntime.iOS-27-0")]
    assert make_simulator("ipad", "t", lambda *_: None, listing) is None
    assert make_simulator("appletv", "t", create, {"runtimes": []}) is None

    good = {"DTSDKName": "iphoneos27.2", "DTPlatformVersion": "27.2", "DTXcode": "2720", "DTXcodeBuild": "27B52"}
    assert sdk_problems(good, 27) == []
    assert sdk_problems({**good, "DTSDKName": "macosx27.0", "DTPlatformVersion": "27.0"}, 27) == []
    assert sdk_problems({**good, "DTSDKName": "appletvos27.0"}, 27) == []
    old = {"DTSDKName": "iphoneos26.5", "DTPlatformVersion": "26.5", "DTXcode": "2650"}
    assert len(sdk_problems(old, 27)) == 3, sdk_problems(old, 27)
    assert sdk_problems({}, 27) == ["DTSDKName missing", "DTPlatformVersion missing", "DTXcode missing"]

    with tempfile.TemporaryDirectory() as tmp:
        archive = Path(tmp) / "app.xcarchive"
        ios = archive / "Products/Applications/Player.app"
        ios.mkdir(parents=True)
        (ios / "Info.plist").write_bytes(plistlib.dumps(good))
        assert check_sdk(archive, 27) == 0
        mac = archive / "Products/Applications/Mac.app/Contents"
        mac.mkdir(parents=True)
        (mac / "Info.plist").write_bytes(plistlib.dumps(old))
        assert check_sdk(archive, 27) == 1
        assert check_sdk(Path(tmp) / "none.xcarchive", 27) == 1


def main(argv: list[str]) -> int:
    if argv == ["--self-test"]:
        return self_test()
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    sub = ap.add_subparsers(dest="cmd", required=True)
    s = sub.add_parser("select")
    s.add_argument("--min", type=int, default=MIN)
    s.add_argument("--sdk")
    m = sub.add_parser("simulator")
    m.add_argument("kind", choices=sorted(KINDS))
    m.add_argument("name")
    c = sub.add_parser("check-sdk")
    c.add_argument("--min", type=int, default=MIN)
    c.add_argument("archive", type=Path)
    a = ap.parse_args(argv)
    if a.cmd == "select":
        return select(a.min, a.sdk)
    if a.cmd == "simulator":
        return simulator(a.kind, a.name)
    return check_sdk(a.archive, a.min)


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
