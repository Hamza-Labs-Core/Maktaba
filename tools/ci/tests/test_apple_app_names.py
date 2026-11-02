"""Every Apple app's name carries Maktaba, in English and in Arabic.

App Store Connect rejects an upload whose bundle name or display name another app already
uses (ITMS-90129): the Remote's bare "Remote" was rejected on 2026-11-02. So every app
target of apps/apple/project.yml (each ships to App Store Connect, testflight.yml) has a
display name and a bundle name (CFBundleName, $(PRODUCT_NAME): the target's name) that
start with "Maktaba", and every localization of them in its InfoPlist.xcstrings starts with
"Maktaba" or holds the Arabic "مكتبة". The generated project file says the same (xcodegen
was run).
"""

from __future__ import annotations

import json
import plistlib
import re
from pathlib import Path
from typing import Any

import pytest
import yaml

ROOT = Path(__file__).resolve().parents[3]
APPLE = ROOT / "apps/apple"
PROJECT = yaml.safe_load((APPLE / "project.yml").read_text(encoding="utf-8"))
PBXPROJ = (APPLE / "Maktaba.xcodeproj/project.pbxproj").read_text(encoding="utf-8")
NAME_KEYS = ("CFBundleDisplayName", "CFBundleName")


def is_maktaba_name(name: str) -> bool:
    """A name no other App Store app has: Maktaba's own, in English or Arabic."""
    return name.startswith("Maktaba") or "مكتبة" in name


def _merged(target: dict[str, Any]) -> dict[str, Any]:
    """A target with its templates' keys (type, platform, settings, info) under its own."""
    merged: dict[str, Any] = {}
    for name in target.get("templates", []):
        template = _merged(PROJECT["targetTemplates"][name])
        merged.update({k: v for k, v in template.items() if k != "settings"})
        base = template.get("settings", {}).get("base", {})
        merged.setdefault("settings", {"base": {}})["base"].update(base)
    for key, value in target.items():
        if key == "settings":
            merged.setdefault("settings", {"base": {}})["base"].update(value.get("base", {}))
        else:
            merged[key] = value
    return merged


APPS = {
    name: target
    for name, target in ((n, _merged(t)) for n, t in PROJECT["targets"].items())
    if target.get("type") == "application"
}


def _strings(catalog: Path) -> list[tuple[str, str, str]]:
    """(key, language, value) of every name key in an InfoPlist catalog."""
    found = []
    strings = json.loads(catalog.read_text(encoding="utf-8"))["strings"]
    for key in NAME_KEYS:
        for language, loc in strings.get(key, {}).get("localizations", {}).items():
            units = [loc["stringUnit"]] if "stringUnit" in loc else []
            for kinds in loc.get("variations", {}).values():
                units += [v["stringUnit"] for v in kinds.values() if "stringUnit" in v]
            found += [(key, language, unit["value"]) for unit in units]
    return found


@pytest.mark.parametrize(
    ("name", "ok"),
    [
        ("Maktaba", True),
        ("Maktaba Remote", True),
        ("ريموت مكتبة", True),
        ("Remote", False),
        ("ريموت", False),
        ("Player", False),
        ("TV", False),
        ("My Maktaba", False),
    ],
)
def test_the_rule(name: str, ok: bool) -> None:
    assert is_maktaba_name(name) is ok


def test_every_app_is_found() -> None:
    assert {"Maktaba Remote", "Maktaba Remote iPad", "Maktaba Player", "Maktaba TV"} <= set(APPS)


@pytest.mark.parametrize("target", sorted(APPS))
def test_app_names(target: str) -> None:
    app = APPS[target]
    settings = app["settings"]["base"]
    display = settings.get("INFOPLIST_KEY_CFBundleDisplayName")
    assert display, f"{target}: no INFOPLIST_KEY_CFBundleDisplayName (the home-screen name)"
    assert is_maktaba_name(display), f"{target}: display name {display!r} must start with Maktaba"
    product = settings.get("PRODUCT_NAME", target)
    assert is_maktaba_name(product), f"{target}: bundle name {product!r} must start with Maktaba"

    # the Info.plist's own names, when it spells one out instead of the build settings'
    info = (APPLE / app["info"]["path"]).resolve()
    plist = plistlib.loads(info.read_bytes())
    for key in NAME_KEYS:
        value = plist.get(key, "")
        if value and not value.startswith("$("):
            assert is_maktaba_name(value), f"{info.relative_to(ROOT)}: {key} {value!r}"

    # each localization (InfoPlist.xcstrings beside the app's sources)
    for source in app.get("sources", []):
        catalog = (APPLE / source["path"] / "InfoPlist.xcstrings").resolve()
        if catalog.is_file():
            for key, language, value in _strings(catalog):
                assert is_maktaba_name(value), (
                    f"{catalog.relative_to(ROOT)}: {key} ({language}) {value!r} must start with "
                    "Maktaba or hold «مكتبة»: a bare word is another app's name on the App Store"
                )


def test_the_project_file_is_generated() -> None:
    """Maktaba.xcodeproj says what project.yml says (cd apps/apple && xcodegen)."""
    found = re.findall(r"INFOPLIST_KEY_CFBundleDisplayName = (\"[^\"]*\"|[^;]*);", PBXPROJ)
    names = {n.strip('"') for n in found}
    wanted = {a["settings"]["base"]["INFOPLIST_KEY_CFBundleDisplayName"] for a in APPS.values()}
    assert names == wanted
