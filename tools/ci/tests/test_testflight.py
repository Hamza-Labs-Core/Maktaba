"""testflight.yml uploads the iPhone apps and the Apple TV app, each in its own leg.

Every `v*` tag archives, signs and uploads Maktaba Player, Maktaba Remote (iOS) and Maktaba TV
(tvOS) to App Store Connect. Each is its own matrix leg with fail-fast off, so one app's failed
upload (its App Store Connect record not made yet, say) leaves the others' alone. Each leg
archives for its scheme's own platform, from a scheme apps/apple/project.yml archives in
Release, and every uploaded app declares ITSAppUsesNonExemptEncryption NO (no
export-compliance question on every build). The Apple TV app has the layered icon, the App
Store icon and both Top Shelf images App Store Connect requires of a tvOS app.
"""

from __future__ import annotations

import json
from pathlib import Path
from typing import Any

import yaml

ROOT = Path(__file__).resolve().parents[3]
WORKFLOW = yaml.safe_load((ROOT / ".github/workflows/testflight.yml").read_text(encoding="utf-8"))
PROJECT = yaml.safe_load((ROOT / "apps/apple/project.yml").read_text(encoding="utf-8"))
PBXPROJ = (ROOT / "apps/apple/Maktaba.xcodeproj/project.pbxproj").read_text(encoding="utf-8")
UPLOAD = WORKFLOW["jobs"]["upload"]
LEGS: list[dict[str, str]] = UPLOAD["strategy"]["matrix"]["include"]
DESTINATIONS = {"iOS": "generic/platform=iOS", "tvOS": "generic/platform=tvOS"}
SDKS = {"iOS": "iphoneos", "tvOS": "appletvos"}
TV_ASSETS = ROOT / "apps/tv/tvos/MaktabaTV/Resources/Assets.xcassets"


def _settings(target: dict[str, Any]) -> dict[str, Any]:
    """A target's base build settings with its templates' under its own."""
    merged: dict[str, Any] = {}
    for name in target.get("templates", []):
        merged.update(_settings(PROJECT["targetTemplates"][name]))
    merged.update(target.get("settings", {}).get("base", {}))
    return merged


def _platform(target: dict[str, Any]) -> str:
    if "platform" in target:
        return str(target["platform"])
    for name in target.get("templates", []):
        found = _platform(PROJECT["targetTemplates"][name])
        if found:
            return found
    return ""


def test_every_app_is_in_the_matrix_once() -> None:
    schemes = [leg["scheme"] for leg in LEGS]
    assert sorted(schemes) == ["Maktaba Player", "Maktaba Remote", "Maktaba TV"]


def test_one_app_failing_leaves_the_others() -> None:
    assert UPLOAD["strategy"]["fail-fast"] is False


def test_each_leg_archives_for_its_schemes_platform() -> None:
    for leg in LEGS:
        scheme = PROJECT["schemes"][leg["scheme"]]
        assert scheme["archive"]["config"] == "Release", leg
        (target_name,) = scheme["build"]["targets"]
        platform = _platform(PROJECT["targets"][target_name])
        assert leg["destination"] == DESTINATIONS[platform], leg
        assert leg["sdk"] == SDKS[platform], leg


def test_the_archive_uses_the_legs_destination() -> None:
    archive = next(s for s in UPLOAD["steps"] if s.get("name") == "Archive")
    assert '-destination "$DESTINATION"' in archive["run"]
    assert UPLOAD["env"]["DESTINATION"] == "${{ matrix.destination }}"
    assert "-allowProvisioningUpdates" in next(
        s for s in UPLOAD["steps"] if s.get("name") == "Sign and upload to TestFlight"
    )["run"]


def test_every_uploaded_app_declares_standard_encryption_only() -> None:
    for leg in LEGS:
        (target_name,) = PROJECT["schemes"][leg["scheme"]]["build"]["targets"]
        settings = _settings(PROJECT["targets"][target_name])
        # YAML reads a bare NO as false
        assert settings.get("INFOPLIST_KEY_ITSAppUsesNonExemptEncryption") is False, target_name
    # xcodegen was run: the generated project carries it for every configuration of every app
    assert PBXPROJ.count("INFOPLIST_KEY_ITSAppUsesNonExemptEncryption = NO;") >= 2 * len(LEGS)


def test_the_tv_app_has_its_app_store_icons_and_top_shelf() -> None:
    tv = _settings(PROJECT["targets"]["Maktaba TV"])
    brand = TV_ASSETS / f"{tv['ASSETCATALOG_COMPILER_APPICON_NAME']}.brandassets"
    assets = json.loads((brand / "Contents.json").read_text(encoding="utf-8"))["assets"]
    have = {(a["role"], a["size"]) for a in assets}
    assert have >= {
        ("primary-app-icon", "400x240"),
        ("primary-app-icon", "1280x768"),
        ("top-shelf-image", "1920x720"),
        ("top-shelf-image-wide", "2320x720"),
    }
    for asset in assets:
        folder = brand / asset["filename"]
        assert folder.is_dir(), folder
        assert list(folder.rglob("*.png")), f"{folder} has no image"
