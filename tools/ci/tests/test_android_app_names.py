"""Every Android app's launcher name carries Maktaba, in English and in Arabic.

The Apple Remote's bare "Remote" was rejected by App Store Connect (ITMS-90129, 2026-11-02;
test_apple_app_names.py), and a bare word is another app's name on Google Play too. So every
Android app (apps/android/player, apps/android/remote, apps/tv/android/app) has a launcher
label, its manifest's <application android:label="@string/...">, that is defined in res/values
and in every res/values-* locale beside it (Arabic too), and each value starts with "Maktaba" or
holds the Arabic «مكتبة». A label set in build.gradle.kts (resValue) has no Arabic, so none is.
The Capacitor app (apps/mobile, com.hamzalabs.maktaba) generates its native projects; its name
is capacitor.config.ts's appName.
"""

from __future__ import annotations

import re
import xml.etree.ElementTree as ET
from pathlib import Path

import pytest

from test_apple_app_names import is_maktaba_name

ROOT = Path(__file__).resolve().parents[3]
ANDROID = "{http://schemas.android.com/apk/res/android}"
APPS = {
    "player": ROOT / "apps/android/player",
    "remote": ROOT / "apps/android/remote",
    "tv": ROOT / "apps/tv/android/app",
}


def test_every_app_is_found() -> None:
    """No Android app is left out: every module applying the application plugin is listed."""
    apps = {
        gradle.parent
        for base in (ROOT / "apps/android", ROOT / "apps/tv/android")
        for gradle in base.glob("*/build.gradle.kts")
        if "plugins.android.application" in gradle.read_text(encoding="utf-8")
    }
    assert apps == set(APPS.values())


def _label(app: Path) -> str:
    manifest = ET.parse(app / "src/main/AndroidManifest.xml").getroot()
    application = manifest.find("application")
    assert application is not None
    label = application.get(f"{ANDROID}label", "")
    match = re.fullmatch(r"@string/(\w+)", label)
    assert match, (
        f"{app.relative_to(ROOT)}: android:label {label!r} must be a @string resource"
    )
    return match.group(1)


def _values(res: Path, name: str) -> list[str]:
    """The string <name> as each strings file in one values* directory defines it."""
    found = []
    for xml in sorted(res.glob("*.xml")):
        for string in ET.parse(xml).getroot().iter("string"):
            if string.get("name") == name:
                found.append("".join(string.itertext()))
    return found


@pytest.mark.parametrize("app", sorted(APPS))
def test_launcher_names(app: str) -> None:
    module = APPS[app]
    name = _label(module)
    gradle = (module / "build.gradle.kts").read_text(encoding="utf-8")
    assert not re.search(rf'resValue\(\s*"string",\s*"{name}"', gradle), (
        f"{app}: the launcher name is set in build.gradle.kts, with no Arabic; "
        "put it in res/values*/strings.xml"
    )
    res = module / "src/main/res"
    locales = sorted(
        d for d in res.glob("values*") if d.is_dir() and list(d.glob("*.xml"))
    )
    assert res / "values" in locales and res / "values-ar" in locales
    for locale in locales:
        if locale.name != "values" and not re.fullmatch(
            r"values-[a-z]{2}(-r[A-Z]{2})?", locale.name
        ):
            continue  # a qualifier that is not a language (values-night, values-v31)
        values = _values(locale, name)
        assert len(values) == 1, (
            f"{app}: {locale.relative_to(ROOT)} defines {name} {len(values)} times"
        )
        assert is_maktaba_name(values[0]), (
            f"{app}: {locale.relative_to(ROOT)} {name} {values[0]!r} must start with Maktaba or "
            "hold «مكتبة»: a bare word is another app's name in the stores"
        )


def test_capacitor_app_name() -> None:
    config = (ROOT / "apps/mobile/capacitor.config.ts").read_text(encoding="utf-8")
    match = re.search(r"appName:\s*'([^']*)'", config)
    assert match, "apps/mobile/capacitor.config.ts: no appName"
    assert is_maktaba_name(match.group(1))
