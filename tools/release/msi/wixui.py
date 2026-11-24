#!/usr/bin/env python3
"""Windows Installer's own words on Maktaba's installer art (Epic 13 story 13.14).

Both .msi installers draw WiX's stock dialogs (WixUI) over Maktaba's bitmaps: the dialog
bitmap (a dark brand column and page) behind the welcome and closing dialogs' words, the
banner (dark, the arch at the end) behind each inner dialog's title. WiX writes those words
in black; on the dark art they take Maktaba's text styles instead, light on ink:

  Maktaba_Bigger  the dialog bitmap's titles (WiX's WixUI_Font_Bigger)
  Maktaba_Title   the banner's titles (WiX's WixUI_Font_Title)
  Maktaba_Light   the descriptions under either

WiX's words in English and Arabic come from WiX 3.11.2 itself (WixUI_en-us.wxl and
WixUI_ar-SA.wxl, Microsoft Reciprocal License), pinned below and kept in wixui-strings.json:

  wixui.py update   fetch both (tools/release/fetch-pinned.sh) and rewrite wixui-strings.json
  wixui.py write    write the server app's WiX files from it (tauri's .msi:
                    apps/desktop/src-tauri/windows/wix/en-US.wxl, ar-SA.wxl, maktaba-ui.wxs)
  wixui.py check    fail when those files differ from what `write` makes

The Player's .msi (jpackage's, through compose) takes the same styles and words after it is
built: theme_msi.py.
"""

from __future__ import annotations

import json
import re
import subprocess
import sys
import tempfile
import xml.etree.ElementTree as ET
from pathlib import Path
from xml.sax.saxutils import escape

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[2]
STRINGS = HERE / "wixui-strings.json"
TAURI_WIX = ROOT / "apps" / "desktop" / "src-tauri" / "windows" / "wix"

_BASE = "https://raw.githubusercontent.com/wixtoolset/wix3/wix3112rtm/src/ext/UIExtension/wixlib/"
SOURCES = {
    "en": (_BASE + "WixUI_en-us.wxl", "63384a277e545c7798dece2917393de58c4a5fd39e539dc30b6a29015be1c968"),
    "ar": (_BASE + "WixUI_ar-SA.wxl", "ec560fe535b04cbcacafbb76e72113fad10385b962c287ba2c94ffd98e74ab8b"),
}
LICENCE = (
    "WiX Toolset 3.11.2's WixUI strings (src/ext/UIExtension/wixlib, tag wix3112rtm), "
    "Copyright (c) .NET Foundation and contributors, under the Microsoft Reciprocal License "
    "(MS-RL); written here by tools/release/msi/wixui.py update"
)

# The text styles: face, size (points), colour (the desktop tokens), bold. Windows Installer
# keeps a colour as 0x00BBGGRR.
TEXT_STYLES = {
    "Maktaba_Bigger": ("Segoe UI", 13, "E8ECF3", True),  # semantic.fg
    "Maktaba_Title": ("Segoe UI", 9, "E8ECF3", True),  # semantic.fg
    "Maktaba_Light": ("Segoe UI", 8, "A9B3C4", False),  # semantic.muted
}

# WixUI's words drawn on the art, from WiX 3.11.2's dialogs (each Text control inside the
# dialog bitmap's or the banner's box): the dialog bitmap's
ON_DIALOG = [
    "ExitDialogDescription", "ExitDialogTitle", "FatalErrorDescription1", "FatalErrorTitle",
    "MaintenanceWelcomeDlgDescription", "MaintenanceWelcomeDlgTitle", "PrepareDlgDescription",
    "PrepareDlgTitle", "ResumeDlgDescription", "ResumeDlgTitle", "UserExitDescription1",
    "UserExitTitle", "WelcomeDlgDescription", "WelcomeDlgTitle", "WelcomeEulaDlgTitle",
    "WelcomeUpdateDlgDescriptionUpdate",
]
# and the banner's
ON_BANNER = [
    "AdvancedWelcomeEulaDlgTitle", "BrowseDlgDescription", "BrowseDlgTitle",
    "CustomizeDlgDescription", "CustomizeDlgTitle", "DiskCostDlgDescription", "DiskCostDlgTitle",
    "FeaturesDlgDescription", "FeaturesDlgTitle", "FilesInUseDescription", "FilesInUseTitle",
    "InstallDirDlgDescription", "InstallDirDlgTitle", "InstallScopeDlgDescription",
    "InstallScopeDlgTitle", "LicenseAgreementDlgDescription", "LicenseAgreementDlgTitle",
    "MaintenanceTypeDlgDescription", "MaintenanceTypeDlgTitle", "MsiRMFilesInUseDescription",
    "MsiRMFilesInUseTitle", "OutOfDiskDlgDescription", "OutOfDiskDlgTitle",
    "OutOfRbDiskDlgDescription", "OutOfRbDiskDlgTitle", "ProgressDlgTitleChanging",
    "ProgressDlgTitleInstalling", "ProgressDlgTitleRemoving", "ProgressDlgTitleRepairing",
    "ProgressDlgTitleUpdating", "SetupTypeDlgDescription", "SetupTypeDlgTitle",
    "VerifyReadyDlgChangeTitle", "VerifyReadyDlgInstallTitle", "VerifyReadyDlgRemoveTitle",
    "VerifyReadyDlgRepairTitle", "VerifyReadyDlgUpdateTitle",
]

CULTURES = {"en": ("en-US", 1252), "ar": ("ar-SA", 1256)}

_STYLE = re.compile(r"^\{[\\&]([A-Za-z0-9_]+)\}")


def restyle(text: str, on: str) -> str:
    """TEXT as drawn on the art (ON: "dialog" or "banner"): WiX's title styles become
    Maktaba's; plain words become Maktaba_Light; any other style stays."""
    m = _STYLE.match(text)
    if m:
        style = m.group(1)
        new = {"WixUI_Font_Bigger": "Maktaba_Bigger", "WixUI_Font_Title": "Maktaba_Title"}.get(style)
        if new is None:
            return text
        return "{\\" + new + "}" + text[m.end():]
    return "{\\Maktaba_Light}" + text


def unstyled(text: str) -> str:
    return _STYLE.sub("", text, count=1)


def colorref(rgb: str) -> int:
    r, g, b = (int(rgb[i : i + 2], 16) for i in (0, 2, 4))
    return r | g << 8 | b << 16


def parse_wxl(data: bytes) -> dict[str, str]:
    ns = "{http://schemas.microsoft.com/wix/2006/localization}"
    root = ET.fromstring(data)
    return {s.get("Id"): s.text or "" for s in root.iter(ns + "String")}


def load() -> dict:
    return json.loads(STRINGS.read_text(encoding="utf-8"))


def update() -> None:
    fetch = ROOT / "tools" / "release" / "fetch-pinned.sh"
    with tempfile.TemporaryDirectory() as tmp:
        words: dict[str, dict[str, str]] = {}
        for lang, (url, sha) in SOURCES.items():
            dest = Path(tmp) / url.rsplit("/", 1)[1]
            subprocess.run([str(fetch), url, sha, str(dest)], check=True, stdout=subprocess.DEVNULL)
            for key, text in parse_wxl(dest.read_bytes()).items():
                words.setdefault(key, {})[lang] = text
    doc = {
        "licence": LICENCE,
        "sources": {lang: {"url": url, "sha256": sha} for lang, (url, sha) in SOURCES.items()},
        "strings": {k: words[k] for k in sorted(words)},
    }
    STRINGS.write_text(json.dumps(doc, ensure_ascii=False, indent=1) + "\n", encoding="utf-8")


def tauri_wxl(lang: str, strings: dict) -> str:
    """The server app's .msi words for LANG (tauri's bundle.windows.wix.language localePath):
    WixUI's strings drawn on the art, restyled; tauri adds its own strings to it."""
    culture, codepage = CULTURES[lang]
    lines = [
        '<?xml version="1.0" encoding="utf-8"?>',
        "<!-- Written by tools/release/msi/wixui.py write (story 13.14): WixUI's words drawn on the",
        "     installer art, in Maktaba's text styles (windows/wix/maktaba-ui.wxs). The words are",
        "     WiX 3.11.2's (Microsoft Reciprocal License). -->",
        f'<WixLocalization Culture="{culture.lower()}" Codepage="{codepage}" '
        'xmlns="http://schemas.microsoft.com/wix/2006/localization">',
    ]
    for key in sorted(ON_DIALOG + ON_BANNER):
        text = strings[key][lang]
        text = restyle(text, "dialog" if key in ON_DIALOG else "banner")
        lines.append(f'  <String Id="{key}" Overridable="yes">{escape(text)}</String>')
    lines.append("</WixLocalization>")
    return "\n".join(lines) + "\n"


def tauri_wxs() -> str:
    """The text styles, as a fragment tauri links (bundle.windows.wix.fragmentPaths, pulled in
    by its empty component group, componentGroupRefs)."""
    lines = [
        '<?xml version="1.0" encoding="utf-8"?>',
        "<!-- Written by tools/release/msi/wixui.py write (story 13.14): Maktaba's text styles for",
        "     WixUI's words on the installer art, light on ink (the desktop tokens). -->",
        '<Wix xmlns="http://schemas.microsoft.com/wix/2006/wi">',
        "  <Fragment>",
        '    <ComponentGroup Id="MaktabaUiStyles" />',
        "    <UI>",
    ]
    for name, (face, size, rgb, bold) in TEXT_STYLES.items():
        r, g, b = (int(rgb[i : i + 2], 16) for i in (0, 2, 4))
        extra = ' Bold="yes"' if bold else ""
        lines.append(
            f'      <TextStyle Id="{name}" FaceName="{face}" Size="{size}" Red="{r}" Green="{g}" Blue="{b}"{extra} />'
        )
    lines += ["    </UI>", "  </Fragment>", "</Wix>"]
    return "\n".join(lines) + "\n"


def tauri_files() -> dict[Path, str]:
    strings = load()["strings"]
    return {
        TAURI_WIX / "en-US.wxl": tauri_wxl("en", strings),
        TAURI_WIX / "ar-SA.wxl": tauri_wxl("ar", strings),
        TAURI_WIX / "maktaba-ui.wxs": tauri_wxs(),
    }


def main(argv: list[str]) -> int:
    cmd = argv[0] if argv else ""
    if cmd == "update":
        update()
        return 0
    if cmd == "write":
        for path, text in tauri_files().items():
            path.write_text(text, encoding="utf-8")
        return 0
    if cmd == "check":
        stale = [p for p, t in tauri_files().items() if not p.exists() or p.read_text(encoding="utf-8") != t]
        for p in stale:
            print(f"wixui: {p.relative_to(ROOT)} is stale: run tools/release/msi/wixui.py write", file=sys.stderr)
        return 1 if stale else 0
    print(__doc__, file=sys.stderr)
    return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
