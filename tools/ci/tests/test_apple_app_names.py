"""Every Apple app's name carries Maktaba, in English and in Arabic.

App Store Connect rejects an upload whose bundle name or display name another app already
uses (ITMS-90129): the Remote's bare "Remote" was rejected on 2026-11-02. So every app
target of apps/apple/project.yml (each ships to App Store Connect, testflight.yml) has a
display name and a bundle name (CFBundleName, $(PRODUCT_NAME): the target's name) that
start with "Maktaba", and every localization of them in its InfoPlist.xcstrings starts with
"Maktaba" or holds the Arabic "مكتبة".

Every user-facing string ships in English and Arabic, the app's name too: each app has one
InfoPlist.xcstrings among its sources, and every key of its Info.plist that people read
(its display name, its bundle name on the Mac, where the menu bar shows it, and each
permission prompt) is in it, in English (the build setting's own words) and in Arabic. The
generated project file says the same (xcodegen was run).
"""

from __future__ import annotations

import fnmatch
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
ARABIC_NAME = "مكتبة"
# Info.plist keys people read, besides each permission prompt (*UsageDescription)
READ_KEYS = {"CFBundleDisplayName", "NSHumanReadableCopyright"}


def is_maktaba_name(name: str) -> bool:
    """A name no other App Store app has: Maktaba's own, in English or Arabic."""
    return name.startswith("Maktaba") or ARABIC_NAME in name


def is_read(key: str) -> bool:
    """An Info.plist key whose value the system shows people."""
    return key in READ_KEYS or key.endswith("UsageDescription")


def _merged(target: dict[str, Any]) -> dict[str, Any]:
    """A target with its templates' keys (type, platform, settings, info) under its own."""
    merged: dict[str, Any] = {}
    for name in target.get("templates", []):
        template = _merged(PROJECT["targetTemplates"][name])
        merged.update({k: v for k, v in template.items() if k not in ("settings", "info")})
        base = template.get("settings", {}).get("base", {})
        merged.setdefault("settings", {"base": {}})["base"].update(base)
        props = template.get("info", {}).get("properties", {})
        merged.setdefault("info", {}).setdefault("properties", {}).update(props)
    for key, value in target.items():
        if key == "settings":
            merged.setdefault("settings", {"base": {}})["base"].update(value.get("base", {}))
        elif key == "info":
            info = merged.setdefault("info", {})
            info.setdefault("properties", {}).update(value.get("properties", {}))
            info.update({k: v for k, v in value.items() if k != "properties"})
        else:
            merged[key] = value
    return merged


APPS = {
    name: target
    for name, target in ((n, _merged(t)) for n, t in PROJECT["targets"].items())
    if target.get("type") == "application"
}


def info_catalogs(app: dict[str, Any]) -> list[Path]:
    """The InfoPlist.xcstrings among an app's sources (its excludes left out)."""
    found = []
    for source in app.get("sources", []):
        folder = (APPLE / source["path"]).resolve()
        if not folder.is_dir():
            continue
        excludes = source.get("excludes", [])
        for catalog in sorted(folder.rglob("InfoPlist.xcstrings")):
            rel = catalog.relative_to(folder).as_posix()
            if not any(fnmatch.fnmatchcase(rel, pattern) for pattern in excludes):
                found.append(catalog)
    return found


def _units(loc: dict[str, Any]) -> list[dict[str, Any]]:
    units = [loc["stringUnit"]] if "stringUnit" in loc else []
    for kinds in loc.get("variations", {}).values():
        units += [v["stringUnit"] for v in kinds.values() if "stringUnit" in v]
    return units


def _catalog(path: Path) -> dict[str, Any]:
    strings: dict[str, Any] = json.loads(path.read_text(encoding="utf-8"))["strings"]
    return strings


def _strings(catalog: Path) -> list[tuple[str, str, str]]:
    """(key, language, value) of every name key in an InfoPlist catalog."""
    found = []
    strings = _catalog(catalog)
    for key in NAME_KEYS:
        for language, loc in strings.get(key, {}).get("localizations", {}).items():
            found += [(key, language, unit["value"]) for unit in _units(loc)]
    return found


def read_keys(app: dict[str, Any]) -> dict[str, str | None]:
    """Every Info.plist key of an app people read -> its English (None: $(PRODUCT_NAME))."""
    keys: dict[str, str | None] = {}
    plist = plistlib.loads((APPLE / app["info"]["path"]).resolve().read_bytes())
    for key, value in {**plist, **app["info"].get("properties", {})}.items():
        if is_read(key) and isinstance(value, str):
            keys[key] = value
    for setting, value in app["settings"]["base"].items():
        key = setting.removeprefix("INFOPLIST_KEY_")
        if setting.startswith("INFOPLIST_KEY_") and is_read(key):
            keys[key] = str(value)
    if app.get("platform") == "macOS":
        # the menu bar's app menu (About, Hide, Quit) and the Dock: the bundle name, which
        # is the target's ("Maktaba Player Mac") unless the catalog names the app
        keys["CFBundleName"] = keys.get("CFBundleDisplayName")
    return keys


@pytest.mark.parametrize(
    ("name", "ok"),
    [
        ("Maktaba", True),
        ("Maktaba Remote", True),
        ("ريموت مكتبة", True),
        ("مشغل مكتبة", True),
        ("Remote", False),
        ("ريموت", False),
        ("Player", False),
        ("TV", False),
        ("My Maktaba", False),
    ],
)
def test_the_rule(name: str, ok: bool) -> None:
    assert is_maktaba_name(name) is ok


@pytest.mark.parametrize(
    ("key", "read"),
    [
        ("CFBundleDisplayName", True),
        ("NSCameraUsageDescription", True),
        ("NSLocalNetworkUsageDescription", True),
        ("CFBundleIdentifier", False),
        ("NSBonjourServices", False),
        ("UIBackgroundModes", False),
    ],
)
def test_the_keys_people_read(key: str, read: bool) -> None:
    assert is_read(key) is read


def test_every_app_is_found() -> None:
    assert {
        "Maktaba Remote",
        "Maktaba Remote iPad",
        "Maktaba Player",
        "Maktaba Player iPad",
        "Maktaba Player Mac",
        "Maktaba TV",
    } <= set(APPS)


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

    # each localization (InfoPlist.xcstrings among the app's sources)
    for catalog in info_catalogs(app):
        for key, language, value in _strings(catalog):
            assert is_maktaba_name(value), (
                f"{catalog.relative_to(ROOT)}: {key} ({language}) {value!r} must start with "
                "Maktaba or hold «مكتبة»: a bare word is another app's name on the App Store"
            )


@pytest.mark.parametrize("target", sorted(APPS))
def test_one_info_catalog(target: str) -> None:
    """An app with no InfoPlist.xcstrings shows its English name on an Arabic device."""
    catalogs = info_catalogs(APPS[target])
    assert len(catalogs) == 1, (
        f"{target}: {len(catalogs)} InfoPlist.xcstrings among its sources "
        f"({[str(c.relative_to(ROOT)) for c in catalogs]}): it needs exactly one (exclude the "
        "others in project.yml)"
    )


@pytest.mark.parametrize("target", sorted(APPS))
def test_the_name_is_in_arabic(target: str) -> None:
    app = APPS[target]
    (catalog,) = info_catalogs(app)
    rel = catalog.relative_to(ROOT)
    display = app["settings"]["base"]["INFOPLIST_KEY_CFBundleDisplayName"]
    names = {(k, lang): v for k, lang, v in _strings(catalog) if k == "CFBundleDisplayName"}
    assert ("CFBundleDisplayName", "en") in names, f"{rel}: no English CFBundleDisplayName"
    assert names["CFBundleDisplayName", "en"] == display, (
        f"{rel}: the English name {names['CFBundleDisplayName', 'en']!r} is not the build "
        f"setting's {display!r}"
    )
    assert ("CFBundleDisplayName", "ar") in names, (
        f"{rel}: no Arabic CFBundleDisplayName: an Arabic device shows {display!r}"
    )
    assert ARABIC_NAME in names["CFBundleDisplayName", "ar"], (
        f"{rel}: the Arabic name {names['CFBundleDisplayName', 'ar']!r} must hold «مكتبة»"
    )


@pytest.mark.parametrize("target", sorted(APPS))
def test_every_key_people_read_is_in_english_and_arabic(target: str) -> None:
    app = APPS[target]
    (catalog,) = info_catalogs(app)
    rel = catalog.relative_to(ROOT)
    strings = _catalog(catalog)
    keys = read_keys(app)
    assert "CFBundleDisplayName" in keys
    for key, english in sorted(keys.items()):
        locs = strings.get(key, {}).get("localizations", {})
        for language in ("en", "ar"):
            units = _units(locs.get(language, {}))
            assert units, f"{rel}: {key} has no {language!r} localization ({target})"
            for unit in units:
                assert unit.get("state") == "translated" and unit.get("value"), (
                    f"{rel}: {key} ({language}) is not translated"
                )
        if english is not None and "$(" not in english:
            assert all(u["value"] == english for u in _units(locs["en"])), (
                f"{rel}: {key}'s English is not the build setting's {english!r} ({target})"
            )


def _objects(isa: str) -> dict[str, str]:
    """The project file's objects of one kind: their comment (a target's name) -> body."""
    pattern = r"\t\t\w+ /\* ([^\n]+?) \*/ = \{\n\t\t\tisa = " + isa + r";\n(.*?)\n\t\t\};"
    return {name.strip('"'): body for name, body in re.findall(pattern, PBXPROJ, re.S)}


def _resources(target: str) -> list[str]:
    """The file references a native target's Resources phase copies ("<id>:<name>")."""
    native = _objects("PBXNativeTarget").get(target)
    assert native, f"{target}: not in project.pbxproj"
    phase = re.search(r"(\w+) /\* Resources \*/,", native)
    assert phase, f"{target}: no Resources phase"
    body = re.search(
        phase.group(1) + r" /\* Resources \*/ = \{.*?files = \((.*?)\);", PBXPROJ, re.S
    )
    assert body, f"{target}: Resources phase {phase.group(1)} not found"
    refs = []
    for build_file, name in re.findall(r"(\w+) /\* (.+?) in Resources \*/", body.group(1)):
        ref = re.search(
            build_file + r" /\* .+? \*/ = \{isa = PBXBuildFile; fileRef = (\w+)", PBXPROJ
        )
        assert ref, f"{target}: build file {build_file} not found"
        refs.append(f"{ref.group(1)}:{name}")
    return refs


def test_the_project_file_is_generated() -> None:
    """Maktaba.xcodeproj says what project.yml says (cd apps/apple && xcodegen)."""
    found = re.findall(r"INFOPLIST_KEY_CFBundleDisplayName = (\"[^\"]*\"|[^;]*);", PBXPROJ)
    names = {n.strip('"') for n in found}
    wanted = {a["settings"]["base"]["INFOPLIST_KEY_CFBundleDisplayName"] for a in APPS.values()}
    assert names == wanted
    # each app copies one InfoPlist.xcstrings; the Mac's is not the iPhone's
    copied = {}
    for target in APPS:
        catalogs = [r for r in _resources(target) if r.endswith(":InfoPlist.xcstrings")]
        assert len(catalogs) == 1, f"{target} copies {len(catalogs)} InfoPlist.xcstrings"
        copied[target] = catalogs[0]
    assert copied["Maktaba Player"] == copied["Maktaba Player iPad"]
    assert copied["Maktaba Player Mac"] != copied["Maktaba Player"]
