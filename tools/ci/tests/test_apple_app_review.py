"""What App Store review and App Store Connect check in every Apple app's bundle.

docs/apple-app-review.md is the audit; this keeps its "done" rows true:

- **A privacy manifest** (PrivacyInfo.xcprivacy) among each app's sources, in its Resources
  (xcodegen was run): App Store Connect refuses an upload whose code calls a required-reason
  API its manifest does not explain. Each category the app's own code (its sources and
  MaktabaKit's) calls is declared with one of Apple's reasons for it; nothing tracks; and
  every data type it says the app collects is in docs/app-privacy.md's App Store table.
- **Export compliance** answered in the bundle (ITSAppUsesNonExemptEncryption NO: only the
  system's HTTPS and CryptoKit hashing), so no build waits on the question.
- **App Transport Security** is NSAllowsLocalNetworking alone: no NSAllowsArbitraryLoads,
  which review asks to justify and which iOS ignores beside NSAllowsLocalNetworking anyway.
- **Local network**: the Bonjour type the apps browse is declared, with the prompt's words.
- **App icons** with no alpha channel (App Store Connect refuses a transparent large icon).
"""

from __future__ import annotations

import fnmatch
import plistlib
import re
import struct
from pathlib import Path
from typing import Any

import pytest
from test_apple_app_names import APPLE, APPS, PBXPROJ, ROOT

KIT = APPLE / "MaktabaKit/Sources"
PRIVACY_DOC = (ROOT / "docs/app-privacy.md").read_text(encoding="utf-8")

# Apple's required-reason API categories: what in Swift calls one, and the reasons that fit
# this code (https://developer.apple.com/documentation/bundleresources/describing-use-of-required-reason-api)
REQUIRED_REASON = {
    "UserDefaults": (r"\bUserDefaults\b|@AppStorage", {"CA92.1", "1C8F.1"}),
    "FileTimestamp": (
        r"\.(creationDate|modificationDate)\b|contentModificationDateKey|creationDateKey|"
        r"attributesOfItem|\bgetattrlist\b|\bfstat\(|\blstat\(",
        {"C617.1", "DDA9.1", "3B52.1", "0A2A.1"},
    ),
    "SystemBootTime": (r"\bsystemUptime\b|\bmach_absolute_time\b", {"35F9.1", "8FFB.1", "3D61.1"}),
    "DiskSpace": (
        r"volumeAvailableCapacity|volumeTotalCapacity|systemFreeSize|systemSize\b|\bstatfs\(",
        {"85F4.1", "E174.1", "7D9E.1", "B728.1"},
    ),
    "ActiveKeyboards": (r"\bactiveInputModes\b", {"3EC4.1", "54BD.1"}),
}

# a manifest's data type -> its name in App Store Connect's App Privacy (docs/app-privacy.md)
DATA_TYPE_NAMES = {
    "EmailAddress": "Email Address",
    "UserID": "User ID",
    "DeviceID": "Device ID",
    "CrashData": "Crash Data",
    "OtherDiagnosticData": "Other Diagnostic Data",
    "CustomerSupport": "Customer Support",
    "ProductInteraction": "Product Interaction",
}


def sources(app: dict[str, Any], pattern: str) -> list[Path]:
    """The files of an app's sources matching pattern (its excludes left out)."""
    found = []
    for source in app.get("sources", []):
        folder = (APPLE / source["path"]).resolve()
        if not folder.is_dir():
            continue
        excludes = source.get("excludes", [])
        for path in sorted(folder.rglob(pattern)):
            rel = path.relative_to(folder).as_posix()
            if not any(fnmatch.fnmatchcase(rel, p) for p in excludes):
                found.append(path)
    return found


def manifest(target: str) -> dict[str, Any]:
    found = sources(APPS[target], "PrivacyInfo.xcprivacy")
    assert len(found) == 1, f"{target}: {len(found)} PrivacyInfo.xcprivacy among its sources, want 1"
    data: dict[str, Any] = plistlib.loads(found[0].read_bytes())
    return data


def calls(target: str) -> set[str]:
    """The required-reason categories the app's own Swift (and MaktabaKit's) calls."""
    files = sources(APPS[target], "*.swift") + sorted(KIT.rglob("*.swift"))
    text = "\n".join(f.read_text(encoding="utf-8") for f in files)
    # comments say what a call does; only code counts
    code = re.sub(r"//[^\n]*", "", text)
    return {cat for cat, (pattern, _) in REQUIRED_REASON.items() if re.search(pattern, code)}


def setting(target: str, key: str) -> Any:
    return APPS[target]["settings"]["base"].get(key)


def info(target: str) -> dict[str, Any]:
    app = APPS[target]
    plist: dict[str, Any] = plistlib.loads((APPLE / app["info"]["path"]).resolve().read_bytes())
    return plist


@pytest.mark.parametrize("target", sorted(APPS))
def test_privacy_manifest(target: str) -> None:
    m = manifest(target)
    assert m.get("NSPrivacyTracking") is False, f"{target}: NSPrivacyTracking must be false"
    assert m.get("NSPrivacyTrackingDomains", []) == [], f"{target}: no tracking domains"

    declared = {
        e["NSPrivacyAccessedAPIType"].removeprefix("NSPrivacyAccessedAPICategory"): set(
            e["NSPrivacyAccessedAPITypeReasons"]
        )
        for e in m.get("NSPrivacyAccessedAPITypes", [])
    }
    for category in calls(target):
        assert category in declared, (
            f"{target}: its code calls a {category} API (required reason) that its "
            "PrivacyInfo.xcprivacy does not declare: App Store Connect refuses the upload"
        )
        allowed = REQUIRED_REASON[category][1]
        assert declared[category] and declared[category] <= allowed, (
            f"{target}: {category} reasons {sorted(declared[category])}, want some of {sorted(allowed)}"
        )

    for entry in m.get("NSPrivacyCollectedDataTypes", []):
        kind = entry["NSPrivacyCollectedDataType"].removeprefix("NSPrivacyCollectedDataType")
        assert entry["NSPrivacyCollectedDataTypeTracking"] is False, f"{target}: {kind} tracks"
        assert kind in DATA_TYPE_NAMES, f"{target}: {kind}: add its App Privacy name here"
        assert f"**{DATA_TYPE_NAMES[kind]}**" in PRIVACY_DOC, (
            f"{target}: the manifest says it collects {kind}; docs/app-privacy.md's App Store "
            f"table must list **{DATA_TYPE_NAMES[kind]}** (the label and the code agree)"
        )


def test_every_manifest_is_bundled() -> None:
    """xcodegen put each app's manifest in its Resources phase (one per app target)."""
    assert PBXPROJ.count("/* PrivacyInfo.xcprivacy in Resources */ = {isa = PBXBuildFile") == len(APPS)


@pytest.mark.parametrize("target", sorted(APPS))
def test_export_compliance(target: str) -> None:
    assert setting(target, "INFOPLIST_KEY_ITSAppUsesNonExemptEncryption") in (False, "NO"), (
        f"{target}: ITSAppUsesNonExemptEncryption NO (only the system's HTTPS) answers export "
        "compliance in the bundle"
    )


@pytest.mark.parametrize("target", sorted(APPS))
def test_transport_security(target: str) -> None:
    assert info(target).get("NSAppTransportSecurity") == {"NSAllowsLocalNetworking": True}, (
        f"{target}: ATS is NSAllowsLocalNetworking alone (docs/apple-app-review.md)"
    )


@pytest.mark.parametrize("target", sorted(APPS))
def test_local_network(target: str) -> None:
    assert "_maktaba._tcp" in info(target).get("NSBonjourServices", [])
    assert setting(target, "INFOPLIST_KEY_NSLocalNetworkUsageDescription")


def _has_alpha(png: Path) -> bool:
    """A PNG's colour type carries alpha (4: grey+alpha, 6: RGBA) or a tRNS chunk."""
    data = png.read_bytes()
    assert data[:8] == b"\x89PNG\r\n\x1a\n", png
    colour_type = data[25]
    return colour_type in (4, 6) or b"tRNS" in data[: data.find(b"IDAT")]


ICONS = sorted((APPLE / "Shared/Assets.xcassets").glob("*.appiconset/icon-1024.png"))


@pytest.mark.parametrize("icon", ICONS, ids=lambda p: p.parent.name)
def test_large_icon_has_no_alpha(icon: Path) -> None:
    assert not _has_alpha(icon), f"{icon.relative_to(ROOT)}: the App Store icon may not have an alpha channel"


def test_icons_are_found() -> None:
    assert {p.parent.name for p in ICONS} >= {"AppIcon.appiconset", "RemoteIcon.appiconset"}
    assert struct.unpack(">II", ICONS[0].read_bytes()[16:24]) == (1024, 1024)


# ---- the App Store Connect drafts (apps/apple/AppStore/<app>/metadata, fastlane deliver's layout)

STORE = APPLE / "AppStore"
# App Store Connect's limits: characters, or bytes for the keywords and the review notes
# (https://developer.apple.com/help/app-store-connect/reference/app-information/platform-version-information)
LIMITS = {
    "name.txt": (2, 30, "chars"),
    "subtitle.txt": (0, 30, "chars"),
    "promotional_text.txt": (0, 170, "chars"),
    "description.txt": (10, 4000, "chars"),
    "keywords.txt": (1, 100, "bytes"),
    "notes.txt": (1, 4000, "bytes"),
}
DRAFTS = sorted(p for p in STORE.glob("*/metadata") if p.is_dir())


def test_every_app_has_drafts() -> None:
    """One apps/apple/AppStore/<app>/ per App Store app, its scheme.txt naming the target. The
    retired iPad-only targets (bundle ids ending .tablet, #1616: one universal app instead)
    are never submitted and need none."""
    schemes = {(p.parent / "scheme.txt").read_text(encoding="utf-8").strip() for p in DRAFTS}
    store = {
        name
        for name, app in APPS.items()
        if not str(app["settings"]["base"].get("PRODUCT_BUNDLE_IDENTIFIER", "")).endswith(".tablet")
    }
    assert schemes <= set(APPS), f"drafts for schemes that are no app target: {sorted(schemes - set(APPS))}"
    assert schemes == store, f"an App Store app without drafts: {sorted(store - schemes)}"


@pytest.mark.parametrize("folder", DRAFTS, ids=lambda p: p.parent.name)
def test_drafts_fit_app_store_connect(folder: Path) -> None:
    for locale in ("en-US", "ar-SA"):
        for name in ("name.txt", "subtitle.txt", "description.txt", "keywords.txt", "promotional_text.txt"):
            assert (folder / locale / name).is_file(), f"{folder.parent.name}: no {locale}/{name}"
        for url in ("support_url.txt", "privacy_url.txt"):
            assert (folder / locale / url).read_text(encoding="utf-8").startswith("https://maktaba.tv/")
    for path in folder.rglob("*.txt"):
        if path.name not in LIMITS:
            continue
        low, high, unit = LIMITS[path.name]
        text = path.read_text(encoding="utf-8").rstrip("\n")
        size = len(text.encode()) if unit == "bytes" else len(text)
        assert low <= size <= high, f"{path.relative_to(ROOT)}: {size} {unit}, App Store Connect takes {low}-{high}"
    assert not (folder / "review_information" / "demo_password.txt").exists(), (
        "no demo password file: the notes name the release secret, filled in at submission"
    )
