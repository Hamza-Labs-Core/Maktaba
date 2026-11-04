"""testflight.yml uploads the iPhone, iPad, Mac and Apple TV apps, each in its own leg.

Every `v*` tag archives, signs and uploads Maktaba Player (iPhone), Maktaba Player iPad,
Maktaba Remote (iPhone), Maktaba Remote iPad, Maktaba Player Mac (macOS) and Maktaba TV (tvOS)
to App Store Connect.
Each is its own matrix leg with fail-fast off, so one app's failed upload (its App Store
Connect record not made yet, say) leaves the others' alone. Each leg archives for its scheme's
own platform, from a scheme apps/apple/project.yml archives in Release, and every uploaded app
declares ITSAppUsesNonExemptEncryption NO (no export-compliance question on every build).
The Mac app's entitlements (its sandbox, which the Mac App Store requires) are signed into the
unsigned archive for the export to keep, it has an App Store category, and its app icon has
every Mac size. The Apple TV app has the layered icon, the App Store icon and both Top Shelf
images App Store Connect requires of a tvOS app. Every uploaded app is also compiled on pull
requests by .github/workflows/_apple-apps.yml, so a tag never archives an app CI never built.
"""

from __future__ import annotations

import json
import struct
from pathlib import Path
from typing import Any

import yaml

ROOT = Path(__file__).resolve().parents[3]
WORKFLOW = yaml.safe_load((ROOT / ".github/workflows/testflight.yml").read_text(encoding="utf-8"))
PROJECT = yaml.safe_load((ROOT / "apps/apple/project.yml").read_text(encoding="utf-8"))
PBXPROJ = (ROOT / "apps/apple/Maktaba.xcodeproj/project.pbxproj").read_text(encoding="utf-8")
UPLOAD = WORKFLOW["jobs"]["upload"]
LEGS: list[dict[str, str]] = UPLOAD["strategy"]["matrix"]["include"]
DESTINATIONS = {
    "iOS": "generic/platform=iOS",
    "macOS": "generic/platform=macOS",
    "tvOS": "generic/platform=tvOS",
}
SDKS = {"iOS": "iphoneos", "macOS": "macosx", "tvOS": "appletvos"}
TV_ASSETS = ROOT / "apps/tv/tvos/MaktabaTV/Resources/Assets.xcassets"
APPLE = ROOT / "apps/apple"
STEPS = {s.get("name"): s for s in UPLOAD["steps"]}
APPLE_CI = yaml.safe_load(
    (ROOT / ".github/workflows/_apple-apps.yml").read_text(encoding="utf-8")
)["jobs"]


def _settings(target: dict[str, Any]) -> dict[str, Any]:
    """A target's base build settings with its templates' under its own."""
    merged: dict[str, Any] = {}
    for name in target.get("templates", []):
        merged.update(_settings(PROJECT["targetTemplates"][name]))
    merged.update(target.get("settings", {}).get("base", {}))
    return merged


def _target(leg: dict[str, str]) -> dict[str, Any]:
    (target_name,) = PROJECT["schemes"][leg["scheme"]]["build"]["targets"]
    target: dict[str, Any] = PROJECT["targets"][target_name]
    return target


def _png_size(path: Path) -> tuple[int, int]:
    head = path.read_bytes()[:24]
    assert head[:8] == b"\x89PNG\r\n\x1a\n", path
    width, height = struct.unpack(">II", head[16:24])
    return width, height


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
    assert sorted(schemes) == [
        "Maktaba Player",
        "Maktaba Player Mac",
        "Maktaba Player iPad",
        "Maktaba Remote",
        "Maktaba Remote iPad",
        "Maktaba TV",
    ]


def test_every_uploaded_app_is_built_on_pull_requests() -> None:
    # the Apple apps job's plain builds, and the schemes the Player's UI tests build
    run = next(
        s["run"] for s in APPLE_CI["apple-apps"]["steps"] if s.get("name") == "Build the apps"
    )
    built = {line.split('"')[1] for line in run.splitlines() if line.strip().startswith('build "')}
    ui_legs = APPLE_CI["player-ui-build"]["strategy"]["matrix"]["include"]
    built |= {leg["scheme"] for leg in ui_legs}
    for leg in LEGS:
        assert leg["scheme"] in built, leg


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


def test_the_mac_apps_entitlements_are_signed_into_the_archive() -> None:
    # an unsigned archive has no entitlements, and the export signs with the archived app's:
    # without this the Mac app would ship unsandboxed (the Mac App Store rejects it)
    for leg in LEGS:
        target = _target(leg)
        if _platform(target) == "macOS":
            path = target["entitlements"]["path"]
            assert leg.get("entitlements") == f"apps/apple/{path}", leg
            assert target["entitlements"]["properties"]["com.apple.security.app-sandbox"] is True
            assert (ROOT / leg["entitlements"]).is_file(), leg
        else:
            assert "entitlements" not in leg, leg
    assert UPLOAD["env"]["ENTITLEMENTS"] == "${{ matrix.entitlements }}"
    step = STEPS["Entitlements into the archive"]
    assert step["if"] == "matrix.entitlements != ''"
    names = list(STEPS)
    assert names.index("Archive") < names.index("Entitlements into the archive")
    assert names.index("Entitlements into the archive") < names.index(
        "Sign and upload to TestFlight"
    )
    run = step["run"]
    assert "$(AppIdentifierPrefix)" in run.replace("\\$", "$")
    assert "codesign --force --sign - --options runtime --entitlements" in run
    assert "com.apple.security.app-sandbox" in run


def test_the_mac_app_has_an_app_store_category() -> None:
    # the Mac App Store rejects an upload without LSApplicationCategoryType
    for leg in LEGS:
        target = _target(leg)
        if _platform(target) == "macOS":
            category = _settings(target).get("INFOPLIST_KEY_LSApplicationCategoryType")
            assert category == "public.app-category.entertainment", leg


def test_the_mac_app_icon_has_every_mac_size() -> None:
    target = PROJECT["targets"]["Maktaba Player Mac"]
    icon = {**PROJECT["settings"]["base"], **_settings(target)}["ASSETCATALOG_COMPILER_APPICON_NAME"]
    folder = APPLE / f"Shared/Assets.xcassets/{icon}.appiconset"
    images = json.loads((folder / "Contents.json").read_text(encoding="utf-8"))["images"]
    mac = {(i["size"], i["scale"]): i["filename"] for i in images if i["idiom"] == "mac"}
    want = {(f"{n}x{n}", scale) for n in (16, 32, 128, 256, 512) for scale in ("1x", "2x")}
    assert set(mac) == want
    for (size, scale), filename in mac.items():
        points = int(size.split("x")[0])
        pixels = points * int(scale[0])
        assert _png_size(folder / filename) == (pixels, pixels), filename
    # the iPhone and iPad keep their single-size icon
    assert any(i["idiom"] == "universal" and i.get("platform") == "ios" for i in images)
