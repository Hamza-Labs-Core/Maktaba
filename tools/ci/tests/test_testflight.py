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
import subprocess
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


# Crash symbols (Epic 53 story 53.16): each leg uploads its archive's symbol tables with the
# MAKTABA_SYMBOLS_TOKEN secret, after its TestFlight upload. With no secret it logs one line and
# runs nothing else; with one, a failed upload warns and keeps the tables, never failing the leg.
SYMBOL_STEPS = ["Symbols token", "Go modules", "Upload crash symbols", "Keep the symbol tables"]


def _step_index(name: str) -> int:
    return next(i for i, s in enumerate(UPLOAD["steps"]) if s.get("name") == name)


def test_symbols_upload_after_testflight() -> None:
    names = [s.get("name") for s in UPLOAD["steps"]]
    for name in SYMBOL_STEPS:
        assert name in names, name
    assert _step_index("Sign and upload to TestFlight") < _step_index("Symbols token")
    assert _step_index("Keep the symbol tables") < _step_index("Remove the API key")


def test_no_token_is_one_line_and_nothing_else() -> None:
    gate = STEPS["Symbols token"]
    assert gate["env"]["MAKTABA_SYMBOLS_TOKEN"] == "${{ secrets.MAKTABA_SYMBOLS_TOKEN }}"
    assert "set -e" not in gate["run"]
    assert 'echo "set=false"' in gate["run"] and "no MAKTABA_SYMBOLS_TOKEN secret is set" in gate["run"]
    # every step after the gate that does work runs only with the token
    start = _step_index("Symbols token")
    for s in UPLOAD["steps"][start + 1 : _step_index("Remove the API key")]:
        cond = str(s.get("if", ""))
        assert "steps.symbols.outputs.set == 'true'" in cond or "steps.symbols_upload.outputs.kept" in cond, s
        # and nothing it does can fail the leg: the upload step exits 0 on its own
        assert s.get("continue-on-error") is True or s.get("name") == "Upload crash symbols", s


def test_a_failed_upload_warns_and_keeps_the_tables() -> None:
    run = STEPS["Upload crash symbols"]["run"]
    assert "set -e" not in run.replace("set -uo", "")
    assert "app.xcarchive/dSYMs/*.dSYM" in run
    assert 'upload -version "$TAG"' in run
    # every exit is 0: nothing the symbol service does fails a release
    assert "exit 1" not in run
    assert "::warning::" in run and 'echo "kept=true"' in run
    keep = STEPS["Keep the symbol tables"]
    assert keep["uses"].startswith("actions/upload-artifact@")
    assert keep["with"]["path"].endswith("/symbols/*.symbols")
    assert STEPS["Upload crash symbols"]["env"]["TAG"] == "${{ steps.version.outputs.tag }}"
    assert 'echo "tag=$tag"' in STEPS["Versions"]["run"]


def _bash_ok(script: str, env: dict[str, str]) -> str:
    out = subprocess.run(["bash", "-c", script], env={"PATH": "/usr/bin:/bin", **env},
                         capture_output=True, text=True, check=False)
    assert out.returncode == 0, out.stderr
    return out.stdout


def test_the_gate_runs_without_and_with_a_token(tmp_path: Path) -> None:
    gate = STEPS["Symbols token"]["run"]
    for token, want in (("", "set=false"), ("tok", "set=true")):
        out = tmp_path / "out"
        out.write_text("")
        printed = _bash_ok(gate, {"MAKTABA_SYMBOLS_TOKEN": token, "GITHUB_OUTPUT": str(out), "SCHEME": "Maktaba TV"})
        assert out.read_text().strip() == want
        assert (printed.count("\n") == 1) == (token == ""), printed


# The cloud's end: relay.yml's Phase 1 deploy hands deploy.sh the same secret, which writes
# the file the api reads (MAKTABA_CLOUD_SYMBOLS_TOKEN_FILE, cloud/cmd/maktaba-cloud/logs.go).
def test_the_deploy_provisions_the_cloud_token() -> None:
    relay = yaml.safe_load((ROOT / ".github/workflows/relay.yml").read_text(encoding="utf-8"))
    ha = next(s for j in relay["jobs"].values() for s in j.get("steps", [])
              if s.get("name") == "Deploy (the Phase 1 pair)")
    assert ha["env"]["MAKTABA_SYMBOLS_TOKEN"] == "${{ secrets.MAKTABA_SYMBOLS_TOKEN }}"
    deploy = (ROOT / "deploy/cloud-ha/deploy.sh").read_text(encoding="utf-8")
    assert 'if [ -n "${MAKTABA_SYMBOLS_TOKEN:-}" ]; then' in deploy
    assert "t=/etc/maktaba/symbols.token" in deploy
    assert "MAKTABA_CLOUD_SYMBOLS_TOKEN_FILE=\\$t" in deploy
    # the token goes over stdin, never on a command line
    assert 'printf \'%s\' "${MAKTABA_SYMBOLS_TOKEN}" | on' in deploy
    logs = (ROOT / "cloud/cmd/maktaba-cloud/logs.go").read_text(encoding="utf-8")
    assert 'EnvSymbolsTokenFile = "MAKTABA_CLOUD_SYMBOLS_TOKEN_FILE"' in logs
    # step 6 (app-update restarts the leader's api) comes after the token is written
    assert deploy.index("symbols.token") < deploy.index("app-update ${tag}")
