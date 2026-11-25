"""The installers' look (Epic 13 story 13.14, decision D3), on every pull request.

What a Windows or a Mac build would show, checked from the files that make it:

- the art (docs/design/desktop/installer-art.mjs's output): every file there, in the format and
  at the size its tool reads (24-bit uncompressed BMPs; the .dmg's TIFF at 1x and 2x);
- the -setup.exe's template (apps/desktop/src-tauri/windows/installer.nsi), rendered as tauri
  renders it (tools/release/win-app/nsis_template.py) for a server and a client build: its
  two pages in place of MUI's, every word in English and Arabic, every bitmap it embeds, and
  tauri's own sections and functions kept (the template is taken from one tauri-bundler);
- tauri.conf.json: the template, the languages, the .dmg window (tauri's own .msi is left
  as it was: the server app ships none, so nothing would build it);
- the WiX words (tools/release/msi/wixui.py) and the Player .msi's theming decisions
  (theme_msi.py's plain functions; the database work is Windows', checked by CI's Player job);
- the colours: the desktop tokens, and the .dmg shelf's names readable in light and dark.

The real builds are CI's: win-app (tauri compiles the template), Desktop Player (packageMsi
themes the .msi, theme_msi.py --check reads it back), mac-app's bundle-size job (the Core
.dmg, laid out and checked by check-dmg.sh).
"""

from __future__ import annotations

import json
import re
import struct
import sys
import tempfile
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[3]
SRC_TAURI = ROOT / "apps" / "desktop" / "src-tauri"
NSIS_ART = SRC_TAURI / "windows" / "installer"
PLAYER_ART = ROOT / "apps" / "desktop-player" / "msi"
CONF = json.loads((SRC_TAURI / "tauri.conf.json").read_text(encoding="utf-8"))
TEMPLATE = (SRC_TAURI / "windows" / "installer.nsi").read_text(encoding="utf-8")

for sub in ("tools/release/win-app", "tools/release/msi", "tools/release/mac-app"):
    sys.path.insert(0, str(ROOT / sub))
import dmg_layout  # noqa: E402
import nsis_template  # noqa: E402
import theme_msi  # noqa: E402
import wixui  # noqa: E402


# --- the art --------------------------------------------------------------------------------


def bmp(path: Path) -> tuple[int, int]:
    """A BMP's width and height, failing unless it is 24-bit, uncompressed and whole."""
    data = path.read_bytes()
    assert data[:2] == b"BM", f"{path.name}: not a BMP"
    size, offset, header, w, h, planes, bits, compression = struct.unpack_from("<I4xIIiiHHI", data, 2)
    assert size == len(data), f"{path.name}: says {size} bytes, has {len(data)}"
    assert header == 40 and planes == 1, f"{path.name}: not a BITMAPINFOHEADER BMP"
    assert bits == 24 and compression == 0, f"{path.name}: {bits}-bit, compression {compression}: NSIS and WiX want 24-bit BI_RGB"
    assert h > 0, f"{path.name}: top-down; Windows Installer reads bottom-up BMPs"
    assert offset + ((w * 3 + 3) // 4 * 4) * h == len(data)
    return w, h


SCALES = {100: 1, 150: 1.5, 200: 2}
NSIS_PIECES = {"brand": (164, 314), "title-welcome": (282, 40), "title-progress": (282, 40)}


@pytest.mark.parametrize("lang", ["en", "ar"])
@pytest.mark.parametrize("pct", SCALES)
@pytest.mark.parametrize("piece", NSIS_PIECES)
def test_the_setup_exes_bitmaps_are_there_at_each_scale(piece: str, pct: int, lang: str) -> None:
    w, h = NSIS_PIECES[piece]
    assert bmp(NSIS_ART / f"{piece}-{lang}-{pct}.bmp") == (round(w * SCALES[pct]), round(h * SCALES[pct]))


@pytest.mark.parametrize(
    "path",
    [PLAYER_ART / "dialog-en.bmp", PLAYER_ART / "dialog-ar.bmp"],
    ids=lambda p: str(p.relative_to(ROOT)),
)
def test_the_msi_dialog_bitmaps_are_wixs_size(path: Path) -> None:
    assert bmp(path) == (493, 312)


@pytest.mark.parametrize(
    "path",
    [PLAYER_ART / "banner-en.bmp", PLAYER_ART / "banner-ar.bmp"],
    ids=lambda p: str(p.relative_to(ROOT)),
)
def test_the_msi_banners_are_wixs_size(path: Path) -> None:
    assert bmp(path) == (493, 58)


def test_the_arabic_msi_art_is_mirrored_not_copied() -> None:
    assert (PLAYER_ART / "dialog-ar.bmp").read_bytes() != (PLAYER_ART / "dialog-en.bmp").read_bytes()
    assert (PLAYER_ART / "banner-ar.bmp").read_bytes() != (PLAYER_ART / "banner-en.bmp").read_bytes()


def tiff_sizes(path: Path) -> list[tuple[int, int]]:
    data = path.read_bytes()
    end = {b"II": "<", b"MM": ">"}[data[:2]]
    (ifd,) = struct.unpack_from(end + "I", data, 4)
    sizes = []
    while ifd:
        (n,) = struct.unpack_from(end + "H", data, ifd)
        tags = {}
        for i in range(n):
            tag, kind, _count, value = struct.unpack_from(end + "HHI4s", data, ifd + 2 + 12 * i)
            tags[tag] = struct.unpack_from(end + ("H" if kind == 3 else "I"), value)[0]
        sizes.append((tags[256], tags[257]))
        (ifd,) = struct.unpack_from(end + "I", data, ifd + 2 + 12 * n)
    return sizes


def test_the_dmg_background_holds_1x_and_2x() -> None:
    dmg = CONF["bundle"]["macOS"]["dmg"]
    w, h = dmg["windowSize"]["width"], dmg["windowSize"]["height"]
    path = SRC_TAURI / dmg["background"]
    assert sorted(tiff_sizes(path)) == [(w, h), (w * 2, h * 2)]
    assert path.stat().st_size < 1024 * 1024


def luminance(hex_: str) -> float:
    c = [int(hex_[i : i + 2], 16) / 255 for i in (0, 2, 4)]
    r, g, b = [v / 12.92 if v <= 0.03928 else ((v + 0.055) / 1.055) ** 2.4 for v in c]
    return 0.2126 * r + 0.7152 * g + 0.0722 * b


def contrast(a: str, b: str) -> float:
    la, lb = sorted([luminance(a), luminance(b)], reverse=True)
    return (la + 0.05) / (lb + 0.05)


def test_the_dmg_shelf_keeps_finders_names_readable_light_and_dark() -> None:
    art = (ROOT / "docs/design/desktop/installer-art/art.html").read_text(encoding="utf-8")
    shelf = re.search(r"\.dmg \.shelf \{(.*?)\}", art, re.S).group(1)
    colour = re.search(r"background: #([0-9a-fA-F]{6});", shelf).group(1)
    top = int(re.search(r"inset-block-start: (\d+)px", shelf).group(1))
    height = int(re.search(r"block-size: (\d+)px", shelf).group(1))
    # Finder writes the names black (light appearance) or white (dark), on the shelf
    assert contrast(colour, "000000") >= 4.5
    assert contrast(colour, "FFFFFF") >= 4.5
    # under each icon (128 points, the name's line below it) and inside the shelf
    dmg = CONF["bundle"]["macOS"]["dmg"]
    for pos in (dmg["appPosition"], dmg["applicationFolderPosition"]):
        name_top = pos["y"] + int(dmg_layout.ICON_SIZE) // 2 + 2
        assert top <= name_top and name_top + 20 <= top + height, pos


# --- the colours ---------------------------------------------------------------------------


def tokens() -> dict[str, str]:
    """The site's dark tokens (the installers are dark), every var() resolved: --name -> RRGGBB."""
    css = (ROOT / "web/packages/website/design/site-tokens.css").read_text(encoding="utf-8")
    css = css.split("@media", 1)[0]
    raw = dict(re.findall(r"--([\w-]+):\s*([^;]+);", css))

    def value(name: str, depth: int = 0) -> str:
        v = raw[name].strip()
        m = re.fullmatch(r"var\(--([\w-]+)\)", v)
        return value(m.group(1), depth + 1) if m else v

    return {k: value(k).lstrip("#").upper() for k in raw if re.fullmatch(r"#?[0-9A-Fa-f]{6}|var\(.*\)", raw[k].strip())}


TEMPLATE_TOKENS = {
    "MK_WINDOW": "color-desktop-window",
    "MK_PANE": "color-desktop-pane",
    "MK_CHROME": "color-desktop-chrome",
    "MK_SUBTLE": "color-desktop-subtle",
    "MK_FG": "color-semantic-fg",
    "MK_MUTED": "color-semantic-muted",
    "MK_BRASS": "color-brand-500",
    "MK_OK": "color-semantic-success",
    "MK_CONTROL": "color-desktop-control",
}


@pytest.mark.parametrize("define", TEMPLATE_TOKENS)
def test_the_setup_exes_colours_are_the_tokens(define: str) -> None:
    got = re.search(rf'^!define {define} "([0-9A-F]{{6}})"', TEMPLATE, re.M).group(1)
    assert got == tokens()[TEMPLATE_TOKENS[define]]


def test_the_colorrefs_are_the_same_colours() -> None:
    for define, rgb in (("MK_BRASS_REF", "MK_BRASS"), ("MK_SUBTLE_REF", "MK_SUBTLE")):
        ref = int(re.search(rf"^!define {define} (0x[0-9A-F]+)", TEMPLATE, re.M).group(1), 16)
        hex_ = re.search(rf'^!define {rgb} "([0-9A-F]{{6}})"', TEMPLATE, re.M).group(1)
        assert ref == wixui.colorref(hex_)


def test_the_msi_text_styles_are_the_tokens() -> None:
    t = tokens()
    assert wixui.TEXT_STYLES["Maktaba_Bigger"][2] == t["color-semantic-fg"]
    assert wixui.TEXT_STYLES["Maktaba_Title"][2] == t["color-semantic-fg"]
    assert wixui.TEXT_STYLES["Maktaba_Light"][2] == t["color-semantic-muted"]
    # light words on the dark art: the dialog bitmap's page and the banner
    for style in wixui.TEXT_STYLES.values():
        assert contrast(style[2], t["color-desktop-window"]) >= 4.5
        assert contrast(style[2], t["color-desktop-chrome"]) >= 4.5


# --- the -setup.exe's template ---------------------------------------------------------------

# tauri-bundler 2.10.3's installer.nsi: what installs, updates and uninstalls stays tauri's
UPSTREAM = [
    "Function PageReinstall", "Function PageReinstallUpdateSelection", "Function PageLeaveReinstall",
    "Function RunMainBinary", "Function un.ConfirmShow", "Function un.ConfirmLeave", "Function .onInit",
    "Section WebView2", "Section Install", "Function .onInstSuccess", "Function un.onInit",
    "Section Uninstall", "Function RestorePreviousInstallLocation", "Function Skip",
    "Function SkipIfPassive", "Function un.SkipIfPassive", "Function CreateOrUpdateStartMenuShortcut",
    "Function CreateOrUpdateDesktopShortcut",
]


def test_the_template_is_the_tauri_clis_own() -> None:
    lock = (ROOT / "apps/desktop/pnpm-lock.yaml").read_text(encoding="utf-8")
    cli = re.search(r"^  '@tauri-apps/cli@([\d.]+)':", lock, re.M).group(1)
    assert f"@tauri-apps/cli {cli}" in TEMPLATE, (
        f"apps/desktop runs @tauri-apps/cli {cli}: take that CLI's tauri-bundler installer.nsi again, "
        "put Maktaba's marked parts back into it, and name it in the template's header"
    )


@pytest.mark.parametrize("name", UPSTREAM)
def test_the_template_keeps_tauris_sections_and_functions(name: str) -> None:
    assert re.search(rf"^{re.escape(name)}(\s|$)", TEMPLATE, re.M), f"{name} is gone from installer.nsi"


@pytest.fixture(scope="module", params=["server", "client"])
def script(request) -> str:
    with tempfile.TemporaryDirectory() as tmp:
        return nsis_template.render(request.param, Path(tmp))


def test_the_script_renders_whole(script: str) -> None:
    assert "{{" not in script and "}}" not in script


def test_two_pages_in_place_of_muis(script: str) -> None:
    pages = re.findall(r"^\s*(Page custom \w+(?: \w+)?|!insertmacro MUI_PAGE_\w+)", script, re.M)
    assert "Page custom MaktabaWelcome MaktabaWelcomeLeave" in pages
    assert "!insertmacro MUI_PAGE_INSTFILES" in pages
    for stock in ("MUI_PAGE_WELCOME", "MUI_PAGE_DIRECTORY", "MUI_PAGE_FINISH"):
        assert f"!insertmacro {stock}" not in pages
    # the installing page is drawn by Maktaba's function
    i = script.index("!insertmacro MUI_PAGE_INSTFILES")
    assert "!define MUI_PAGE_CUSTOMFUNCTION_SHOW MaktabaInstallingShow" in script[i - 300 : i]
    # it holds the folder, so it stands where tauri's directory page did: after the reinstall
    # page (an earlier install's uninstall) and the install mode page, before the Start menu's
    assert pages.index("Page custom PageReinstall PageLeaveReinstall") < pages.index("Page custom MaktabaWelcome MaktabaWelcomeLeave")
    assert pages.index("Page custom MaktabaWelcome MaktabaWelcomeLeave") < pages.index("!insertmacro MUI_PAGE_STARTMENU")
    if "!insertmacro MULTIUSER_PAGE_INSTALLMODE" in pages:
        assert pages.index("!insertmacro MULTIUSER_PAGE_INSTALLMODE") < pages.index("Page custom MaktabaWelcome MaktabaWelcomeLeave")


def function(script: str, name: str) -> str:
    m = re.search(rf"^Function {re.escape(name)}\b.*?^FunctionEnd", script, re.M | re.S)
    assert m, f"no Function {name}"
    return m.group(0)


def blocks_at(code: str, pos: int) -> list[str]:
    """The LogicLib ${If} (or ${Else}...) branches open at POS of CODE, outermost first."""
    open_: list[str] = []
    for raw in code[:pos].splitlines():
        line = raw.strip()
        if line.startswith(("${If} ", "${IfNot} ")):
            open_.append(line)
        elif line.startswith(("${ElseIf} ", "${Else}")):
            open_[-1] = f"not ({open_[-1]}) {line}"
        elif line.startswith(("${OrIf} ", "${AndIf} ")):
            open_[-1] += " " + line
        elif line.startswith("${EndIf}"):
            open_.pop()
    return open_


def test_an_upgrade_keeps_the_desktop_shortcut(script: str) -> None:
    # the reinstall page's default runs the old uninstaller, which deletes the shortcut; so does
    # the WiX .msi's: the welcome page's desktop box starts ticked when either is there
    init = function(script, ".onInit")
    assert "StrCpy $MkDesktop 0" in init
    assert init.index("StrCpy $MkDesktop 0") < init.index("Call MaktabaPriorDesktop")
    assert init.index("!insertmacro SetContext") < init.index("Call MaktabaPriorDesktop")
    prior = function(script, "MaktabaPriorDesktop")
    assert '${FileExists} "$DESKTOP\\${PRODUCTNAME}.lnk"' in prior
    assert "EnumRegKey $1 HKLM \"SOFTWARE\\Microsoft\\Windows\\CurrentVersion\\Uninstall\"" in prior
    assert '"msiexec"' in prior and prior.count("StrCpy $MkDesktop 1") == 2
    # and a new one starts without: nothing else turns it on
    assert script.count("StrCpy $MkDesktop 1") == 2


def install_section(script: str) -> str:
    return re.search(r"^Section Install\b.*?^SectionEnd", script, re.M | re.S).group(0)


def test_every_mode_makes_the_desktop_shortcut_by_the_one_rule(script: str) -> None:
    # silent and passive runs have no welcome page: what it would start with decides for them
    # (none on a new install, kept when this one replaces an install that has it)
    install = install_section(script)
    assert install.count("Call CreateOrUpdateDesktopShortcut") == 1
    call = install.index("Call CreateOrUpdateDesktopShortcut")
    assert install[:call].rstrip().endswith("${If} $MkDesktop = ${BST_CHECKED}")
    assert blocks_at(install, call) == ["${If} $MkDesktop = ${BST_CHECKED}"]
    # the old main binary's shortcut is moved to the new one before anything is skipped, an
    # update (and /NS) makes none, the WiX .msi's is always made again: tauri's, as it was
    fn = function(script, "CreateOrUpdateDesktopShortcut")
    moved = fn.index('!insertmacro IsShortcutTarget "$DESKTOP\\${PRODUCTNAME}.lnk" "$INSTDIR\\$OldMainBinaryName"')
    skipped = fn.index("${If} $WixMode = 0")
    update = fn.index("${If} $UpdateMode = 1")
    assert moved < skipped < update < fn.index("${OrIf} $NoShortcutMode = 1") < fn.index('CreateShortcut "$DESKTOP\\${PRODUCTNAME}.lnk"')


def test_no_copyright_line_under_any_page(script: str) -> None:
    assert re.findall(r"^BrandingText (.*)$", script, re.M) == ['" "']
    # the exe's version info keeps tauri's; nothing else shows it
    assert [l.strip() for l in script.splitlines() if "${COPYRIGHT}" in l] == ['VIAddVersionKey "LegalCopyright" "${COPYRIGHT}"']
    assert "©" not in re.sub(r'^!define COPYRIGHT .*$', "", script, flags=re.M)


def test_a_failed_install_keeps_its_details_on_screen(script: str) -> None:
    i = script.index("!insertmacro MUI_PAGE_INSTFILES")
    assert "!define MUI_PAGE_CUSTOMFUNCTION_LEAVE MaktabaInstallingLeave" in script[i - 400 : i]
    leave = function(script, "MaktabaInstallingLeave")
    assert leave.splitlines()[1].strip() == "IfAbort +2" and leave.splitlines()[2].strip() == "Return"
    for shown in ("SetDetailsView show", "GetDlgItem $0 $MkPage 1016", "GetDlgItem $0 $MkPage 1027", "GetDlgItem $0 $MkPage 1006", "$(mkFailed)"):
        assert shown in leave, shown
    assert "Function .onInstFailed" not in script
    # the window closes by itself only after the install succeeded (the end of Section Install)
    install = re.search(r"^Section Install.*?^SectionEnd", script, re.M | re.S).group(0)
    assert install.rstrip().splitlines()[-2].strip() == "SetAutoClose true"


# --- the room on the disk: looked at before anything is stopped or deleted ---------------------


def test_nothing_asks_the_directory_page_that_is_not_there(script: str) -> None:
    # NSIS answers GetInstDirError only for its own directory page (Ui.c): 0 anywhere else
    code = "\n".join(l for l in script.splitlines() if not l.lstrip().startswith(";"))
    assert "GetInstDirError" not in code


def test_the_room_is_looked_at_before_the_hooks_in_every_mode(script: str) -> None:
    install = install_section(script)
    look = install.index("Call MaktabaRoom")
    # no condition around it: silent, passive, an update and a person at the window all pass it
    assert blocks_at(install, look) == []
    hook = install.index("!insertmacro NSIS_HOOK_PREINSTALL")
    assert look < hook
    # before the section writes anything too, and it ends the run when there is none
    assert look < install.index("SetOutPath $INSTDIR")
    refused = install[look : install.index("SetOutPath $INSTDIR")]
    assert "${If} $MkRoomShort = 1" in refused
    for mode in ("${If} ${Silent}", "${OrIf} $PassiveMode = 1", "${OrIf} $UpdateMode = 1"):
        assert mode in refused
    assert "Call MaktabaNoRoom\n      Quit" in refused
    assert "SetErrorLevel ${MK_ROOM_EXIT}" in refused and 'Abort "$MkRoomText"' in refused


def test_a_run_with_no_pages_is_refused_as_it_starts(script: str) -> None:
    init = function(script, ".onInit")
    look = init.index("Call MaktabaRoom")
    assert blocks_at(init, look) == ["${If} ${Silent} ${OrIf} $PassiveMode = 1 ${OrIf} $UpdateMode = 1"]
    # once the folder is known (/D=, the earlier install's, the default)
    assert init.index("Call RestorePreviousInstallLocation") < look
    assert init[look:].split("\n", 1)[1].strip().startswith("${If} $MkRoomShort = 1\n      Call MaktabaNoRoom\n      Abort")
    # and that is before the reinstall page's silent uninstall and the WebView2 download
    assert script.index("Function .onInit") < script.index("Section WebView2")


def test_the_pages_refuse_before_the_uninstall_and_before_the_install(script: str) -> None:
    # the reinstall page's leave runs the old uninstaller: the room first
    leave = function(script, "PageLeaveReinstall")
    look = leave.index("Call MaktabaRoom")
    assert leave.index("reinst_uninstall:") < look < leave.index("HideWindow") < leave.index("ExecWait")
    assert blocks_at(leave, look) == ["${If} $PassiveMode <> 1", "${If} $R0 <> 0 ${OrIf} $R1 = 1"]
    # the welcome page's: the person stays on it, the folder still to choose
    welcome = function(script, "MaktabaWelcomeLeave")
    look = welcome.index("Call MaktabaRoom")
    assert blocks_at(welcome, look) == []
    assert look < welcome.index("Call MaktabaNormalWindow")
    for fn, words in ((leave[leave.index("Call MaktabaRoom") :], "$(mkNoRoomAgain)"), (welcome, "$(mkNoRoomFolder)")):
        said = fn.index("MessageBox")
        assert fn.index("${If} $MkRoomShort = 1") < said < fn.index(words) < fn.index("Abort")


def test_the_room_counts_the_payload_twice_when_the_temp_folder_shares_its_drive(script: str) -> None:
    # tauri's default compression is solid: the installer unpacks all it holds into %TEMP%
    assert 'SetCompressor /SOLID "lzma"' in script and "!define MK_SOLID" in script
    assert int(re.search(r"^!define MK_ROOM_MARGIN (\d+)$", script, re.M).group(1)) == 64 * 1024
    room = function(script, "MaktabaRoom")
    assert room.index("StrCpy $0 $INSTDIR") < room.index("StrCpy $0 $TEMP")
    assert room.count("IntOp $0 ${ESTIMATEDSIZE} + ${MK_ROOM_MARGIN}") == 2
    assert "${AndIf} $9 == $3\n      IntOp $0 $0 + ${ESTIMATEDSIZE}" in room
    # every register it uses is put back: it is called from pages and sections that hold theirs
    pushed = re.findall(r"^  Push (\$\S+)$", room.split("StrCpy $MkRoomShort 0")[0], re.M)
    popped = re.findall(r"^  Pop (\$\S+)$", room, re.M)
    assert pushed == popped[::-1] == ["$0", "$1", "$2", "$3", "$4", "$5", "$6", "$7", "$8", "$9", "$R2"]


def test_the_old_payload_the_hooks_delete_counts_as_room(script: str) -> None:
    # an update deletes the old server\\ before it copies the new one: a home that has room for
    # it must not be refused
    room = function(script, "MaktabaRoom")
    old = room.index("Call MaktabaOld")
    last = room.rindex("Call MaktabaShort")
    assert blocks_at(room, old) == ["${If} $MkRoomShort = 0"]
    assert room[old:last].splitlines()[1].strip() == "IntOp $0 $0 - $1"
    # never in %TEMP%, when that is another drive: the old payload frees nothing there
    assert room.index("Call MaktabaShort") < old or room.count("Call MaktabaShort") == 1
    # and never below the margin
    assert "${If} $0 < ${MK_ROOM_MARGIN}\n      StrCpy $0 ${MK_ROOM_MARGIN}" in room[old:last]
    # the install drive's numbers, not %TEMP%'s
    assert "StrCpy $1 $7\n    StrCpy $2 $8\n    Call MaktabaShort" in room[old:]
    fn = function(script, "MaktabaOld")
    # the app is written over in every build; $0, the caller's need, is put back
    assert fn.splitlines()[1].strip() == "Push $0" and fn.rstrip().splitlines()[-2].strip() == "Pop $0"
    assert fn.index("StrCpy $MkRoomOld 0") < fn.index('Push "$INSTDIR\\${MAINBINARYNAME}.exe"\n  Call MaktabaCount')
    assert "System::Int64Op $MkRoomOld / 1024\n  Pop $1" in fn
    hooks = (SRC_TAURI / "windows" / "installer-hooks.nsh").read_text(encoding="utf-8")
    pre = re.search(r"^!macro NSIS_HOOK_PREINSTALL.*?^!macroend", hooks, re.M | re.S).group(0)
    # its conditions are the hook's own
    for what in ("!insertmacro MAKTABA_INSTALLED_TAKES_FLAGS", "${If} $R8 = 1", '${FileExists} "$INSTDIR\\server\\payload.json"'):
        assert what in fn and what in pre, what
    assert 'RMDir /r "$INSTDIR\\server"' in pre
    take = fn.index('Push "$INSTDIR\\server"\n        Call MaktabaTake')
    over = fn.index("Call MaktabaOver")
    assert blocks_at(fn, take)[-1] == '${If} $R8 = 1 ${AndIf} ${FileExists} "$INSTDIR\\server\\payload.json"'
    # a server\\ that is a link: the hook removes the link, and nothing of it is freed here
    for at in (take, over):
        assert "${If} $0 <> -1 ${AndIf} $1 = 0" in blocks_at(fn, at)
    assert "IntOp $1 $0 & 0x400" in fn


def test_a_retry_of_an_upgrade_that_stopped_half_way_counts_what_it_writes_over(script: str) -> None:
    # no record of the new Maktaba.exe yet, or no payload.json: the hook deletes nothing, and the
    # new release's own files there are written over in place. Two payloads would be asked of a
    # home that has no server
    over = function(script, "MaktabaOver")
    counted = re.findall(r'^\s*Push "\$INSTDIR\\\\?(.+)"\n\s*Call MaktabaCount$', over, re.M)
    written = re.findall(r'^\s*File /a "/oname=(.+?)" ', install_section(script), re.M)
    assert bool(counted) == ("server" in "".join(written))
    assert [c.replace("\\\\", "\\") for c in counted] == [w.replace("\\\\", "\\") for w in written]
    fn = function(script, "MaktabaOld")
    assert "${Else}" in fn[: fn.index("Call MaktabaOver")].splitlines()[-2]


def test_what_goes_is_what_it_takes_on_the_disk(script: str) -> None:
    count = function(script, "MaktabaCount")
    # a link's target is not freed; a file past NSIS's numbers is not counted
    assert "IntOp $2 $1 & 0x410" in count and "${If} $1 <> -1\n  ${AndIf} $2 = 0" in count
    assert "GetCompressedFileSizeW(w r0, *i .r2) i .r1" in count
    assert "${If} $1 > 0\n    ${AndIf} $2 = 0\n      System::Int64Op $MkRoomOld + $1\n      Pop $MkRoomOld" in count
    take = function(script, "MaktabaTake")
    link = take.index("IntOp $4 $3 & 0x400")
    assert link < take.index("Call MaktabaTake") and "${If} $4 = 0" in blocks_at(take, take.index("Call MaktabaTake"))
    for fn, regs in ((count, ["$1", "$2"]), (take, ["$1", "$2", "$3", "$4"])):
        assert re.findall(r"^  Push (\$\d)$", fn, re.M) == regs
        assert re.findall(r"^  Pop (\$\d)$", fn, re.M) == regs[::-1] + ["$0"]


def test_the_app_can_read_what_the_installer_unpacks(script: str) -> None:
    # its version information, which is not compressed: room.rs looks for the key in the file
    key = re.search(r'pub const UNPACKED_KEY: &str = "(\w+)";', (SRC_TAURI / "src" / "room.rs").read_text(encoding="utf-8")).group(1)
    assert f'VIAddVersionKey "{key}" "${{ESTIMATEDSIZE}}"' in script
    margin = int(re.search(r"^!define MK_ROOM_MARGIN (\d+)$", script, re.M).group(1))
    room = (SRC_TAURI / "src" / "room.rs").read_text(encoding="utf-8")
    assert int(re.search(r"const MARGIN_KB: u64 = ([\d_]+);", room).group(1).replace("_", "")) == margin


def test_the_free_space_is_counted_past_32_bits_and_never_guessed(script: str) -> None:
    free = function(script, "MaktabaFree")
    assert "GetDiskFreeSpaceExW(w r6, *l .r1, *l .r5, p 0)" in free
    assert "System::Int64Op $1 / 1024" in free
    # a folder not there yet: its nearest parent that is
    assert "${GetParent} $0 $0" in free and free.index("${Do}") < free.index("${GetParent} $0 $0") < free.index("${Loop}")
    # past the root nothing is asked (a share that does not answer ends as "\\", the root of
    # whatever drive is current): the slash goes first, then nothing left ends it
    assert free.index("StrCpy $0 $0 -1") < free.index('${If} $0 == ""\n      Return') < free.index('StrCpy $6 "$0\\"')
    # a drive that does not answer refuses nothing
    assert function(script, "MaktabaShort").splitlines()[1:4] == ['  ${If} $1 == ""', "    Return", "  ${EndIf}"]
    assert "System::Int64Op $1 < $0" in function(script, "MaktabaShort")


def test_a_refused_update_starts_the_home_and_the_app_again(script: str) -> None:
    refused = function(script, "MaktabaNoRoom")
    assert refused.splitlines()[1].strip() == "SetErrorLevel ${MK_ROOM_EXIT}"
    # the app stopped the server for its own update and closed
    start = refused.index("!insertmacro MAKTABA_START_AGAIN")
    assert "${If} $UpdateMode = 1" in blocks_at(refused, start)
    app = refused.index("nsis_tauri_utils::RunAsUser")
    assert refused.index('${GetOptions} $CMDLINE "/R" $R0') < app
    # what the agent and the app read as they start is there before either starts
    name = re.search(r'pub const REFUSED_FILE: &str = "([\w-]+)";', (SRC_TAURI / "src" / "attempts.rs").read_text(encoding="utf-8")).group(1)
    left = refused.index(f'FileOpen $R1 "$R0\\Maktaba\\updates\\{name}" w')
    assert refused.index('ReadEnvStr $R0 "LOCALAPPDATA"') < left < refused.index('FileWrite $R1 "${VERSION}"') < start < app
    assert "${If} $UpdateMode = 1" in blocks_at(refused, left)
    # both before a window that waits for a person, and none where nobody is (the agent's /P /UPDATE)
    box = refused.index("MessageBox")
    assert start < app < box
    # in front of the app just reopened: it has no window of its own to belong to
    assert "MB_ICONSTOP|MB_TOPMOST|MB_SETFOREGROUND" in refused[box:].splitlines()[0]
    assert blocks_at(refused, box) == ["not (${If} ${Silent}) ${ElseIf} $PassiveMode <> 1 ${OrIf} $UpdateMode <> 1 ${OrIf} $R1 = 1"]
    hooks = (SRC_TAURI / "windows" / "installer-hooks.nsh").read_text(encoding="utf-8")
    again = re.search(r"^!macro MAKTABA_START_AGAIN.*?^!macroend", hooks, re.M | re.S).group(0)
    assert "--server-start" in again and "--server-stop" not in again and "RMDir" not in again


def test_the_app_reads_the_installers_own_exit_code(script: str) -> None:
    code = int(re.search(r"^!define MK_ROOM_EXIT (\d+)$", script, re.M).group(1))
    rust = (SRC_TAURI / "src" / "attempts.rs").read_text(encoding="utf-8")
    assert int(re.search(r"pub const INSTALLER_NO_ROOM: i32 = (\d+);", rust).group(1)) == code
    # NSIS's own: 0 done, 1 cancelled, 2 stopped by the script
    assert code not in (0, 1, 2)
    check = (ROOT / "tools/release/win-app/check.sh").read_text(encoding="utf-8")
    assert f"NO_ROOM_EXIT={code}" in check


def test_the_room_words_say_where_and_how_much(script: str) -> None:
    for lang in ("ENGLISH", "ARABIC"):
        for name in ("mkNoRoom", "mkNoRoomTemp"):
            text = re.search(rf'^\s*LangString {name} \$\{{LANG_{lang}\}} "(.*)"$', script, re.M).group(1)
            for what in ("$MkRoomOn", "$MkRoomNeed", "$MkRoomFree"):
                assert what in text, (name, lang, what)
        for name in ("mkNoRoomFolder", "mkNoRoomAgain"):
            assert re.search(rf'^\s*LangString {name} \$\{{LANG_{lang}\}} "', script, re.M), (name, lang)


def test_a_cleared_start_menu_box_holds_in_every_mode(script: str) -> None:
    fn = function(script, "CreateOrUpdateStartMenuShortcut")
    box = fn.index("${If} $MkStartMenu <> ${BST_CHECKED}")
    assert box < fn.index("${If} $WixMode = 0"), "the box is read only outside WiX mode"
    assert fn[box:].splitlines()[1].strip() == "Return"


def test_the_welcome_page_goes_back_only_to_the_reinstall_page(script: str) -> None:
    assert "StrCpy $MkReinstallShown 1" in function(script, "PageReinstall")
    assert "Call MaktabaNormalWindow" in function(script, "PageReinstall")
    welcome = function(script, "MaktabaWelcome")
    assert "${If} $MkReinstallShown <> 1" in welcome and "EnableWindow $0 0" in welcome


def test_english_and_arabic(script: str) -> None:
    assert re.findall(r'^!insertmacro MUI_LANGUAGE "(\w+)"', script, re.M) == ["English", "Arabic"]
    assert CONF["bundle"]["windows"]["nsis"]["languages"] == ["English", "Arabic"]
    assert CONF["bundle"]["windows"]["nsis"]["displayLanguageSelector"] is False


def test_every_word_is_in_both_languages(script: str) -> None:
    defined = {}
    for name, lang in re.findall(r"^\s*LangString (mk\w+) \$\{LANG_(\w+)\}", script, re.M):
        defined.setdefault(name, set()).add(lang)
    used = set(re.findall(r"\$\((mk\w+)\)", script))
    assert used, "the pages use no words of their own"
    for name in used:
        assert defined.get(name) == {"ENGLISH", "ARABIC"}, f"$({name}): {sorted(defined.get(name, []))}"
    assert set(defined) <= used | {"mkLead"}, f"words no page uses: {sorted(set(defined) - used)}"


def test_every_arabic_word_is_arabic(script: str) -> None:
    for name, text in re.findall(r'^\s*LangString (mk\w+) \$\{LANG_ARABIC\} "(.*)"$', script, re.M):
        assert re.search(r"[؀-ۿ]", text), f"{name} has no Arabic: {text}"


def test_every_bitmap_the_script_embeds_is_there() -> None:
    names = set(re.findall(r'!insertmacro MK_ART ([\w-]+)', TEMPLATE))
    assert names == {"brand", "title-welcome", "title-progress"}
    for name in names:
        for lang in ("en", "ar"):
            for pct in SCALES:
                assert (NSIS_ART / f"{name}-{lang}-{pct}.bmp").is_file()


def test_both_hooks_files_say_where_the_art_is() -> None:
    ui = (SRC_TAURI / "windows" / "installer-ui.nsh").read_text(encoding="utf-8")
    assert '!define MAKTABA_ART "${__FILEDIR__}\\installer"' in ui
    hooks = (SRC_TAURI / "windows" / "installer-hooks.nsh").read_text(encoding="utf-8")
    assert '!include "${MAKTABA_HOOKS_DIR}\\installer-ui.nsh"' in hooks
    nsis = CONF["bundle"]["windows"]["nsis"]
    assert nsis["template"] == "windows/installer.nsi"
    assert nsis["installerHooks"] == "windows/installer-ui.nsh"
    # the server build's overlay names the hooks, which include installer-ui.nsh
    payload = (ROOT / "tools/release/win-app/payload.sh").read_text(encoding="utf-8")
    assert "installer-hooks.nsh" in payload


def test_no_comment_swallows_the_next_line() -> None:
    # makensis reads a trailing backslash as a line continuation, in a comment too
    for n, line in enumerate(TEMPLATE.splitlines(), 1):
        if line.lstrip().startswith(";"):
            assert not line.rstrip().endswith("\\"), f"installer.nsi:{n}: {line}"


# --- the .msi files ------------------------------------------------------------------------


def test_tauris_own_msi_is_left_as_it_was() -> None:
    # the server app ships no .msi (desktop-release.yml builds its NSIS installer only), so
    # nothing would build a themed one: its settings stay tauri's
    assert CONF["bundle"]["windows"]["wix"] == {"language": ["en-US"]}
    assert not (SRC_TAURI / "windows" / "wix").exists()


def test_the_wix_words_are_there_in_both_languages() -> None:
    strings = wixui.load()["strings"]
    for key in ("WelcomeDlgTitle", "WelcomeDlgDescription", "InstallDirDlgTitle", "ProgressDlgTitleInstalling", "ExitDialogTitle", "WixUINext"):
        assert strings[key]["en"] and re.search(r"[\u0600-\u06ff]", strings[key]["ar"]), key


@pytest.mark.parametrize(
    "text, on, want",
    [
        ("{\\WixUI_Font_Bigger}Welcome to the [ProductName] Setup Wizard", "dialog", "{\\Maktaba_Bigger}Welcome to the [ProductName] Setup Wizard"),
        ("{\\WixUI_Font_Title}Destination Folder", "banner", "{\\Maktaba_Title}Destination Folder"),
        ("Click Next to install", "banner", "{\\Maktaba_Light}Click Next to install"),
        ("{\\WixUI_Font_Emphasized}Install just for you", "banner", "{\\WixUI_Font_Emphasized}Install just for you"),
    ],
)
def test_restyle(text: str, on: str, want: str) -> None:
    assert wixui.restyle(text, on) == want


def control(dialog, name, kind, x, y, w, h, text="", attrs=3):
    return {"Dialog_": dialog, "Control": name, "Type": kind, "X": x, "Y": y, "Width": w, "Height": h, "Attributes": attrs, "Text": text}


CONTROLS = [
    control("WelcomeDlg", "Bitmap", "Bitmap", 0, 0, 370, 234, "WixUI_Bmp_Dialog", 1),
    control("WelcomeDlg", "Title", "Text", 135, 20, 220, 60, "{\\WixUI_Font_Bigger}Welcome to the [ProductName] Setup Wizard"),
    control("WelcomeDlg", "Description", "Text", 135, 80, 220, 60, "The Setup Wizard will install [ProductName] on your computer. Click Next to continue or Cancel to exit the Setup Wizard."),
    control("WelcomeDlg", "Next", "PushButton", 236, 243, 56, 17, "&Next"),
    control("InstallDirDlg", "BannerBitmap", "Bitmap", 0, 0, 370, 44, "WixUI_Bmp_Banner", 1),
    control("InstallDirDlg", "Title", "Text", 15, 6, 200, 15, "{\\WixUI_Font_Title}Destination Folder"),
    control("InstallDirDlg", "Description", "Text", 25, 23, 280, 15, "Click Next to install to the default folder or click Change to choose another."),
    control("InstallDirDlg", "FolderLabel", "Text", 20, 60, 290, 30, "Install [ProductName] to:"),
]


def test_the_player_msi_restyles_only_the_words_on_its_art() -> None:
    out = theme_msi.restyle_controls(CONTROLS)
    assert out[("WelcomeDlg", "Title")].startswith("{\\Maktaba_Bigger}")
    assert out[("WelcomeDlg", "Description")].startswith("{\\Maktaba_Light}")
    assert out[("InstallDirDlg", "Title")].startswith("{\\Maktaba_Title}")
    assert out[("InstallDirDlg", "Description")].startswith("{\\Maktaba_Light}")
    # below the banner, on Windows' own dialog colour: as WiX wrote it
    assert ("InstallDirDlg", "FolderLabel") not in out
    assert ("WelcomeDlg", "Next") not in out


def test_the_arabic_transform_translates_wixs_and_jpackages_words() -> None:
    table = theme_msi.translations(wixui.load()["strings"])
    ar = theme_msi.translate("{\\Maktaba_Bigger}Welcome to the [ProductName] Setup Wizard", table)
    assert ar.startswith("{\\Maktaba_Bigger}") and "[ProductName]" in ar and re.search(r"[؀-ۿ]", ar)
    assert theme_msi.translate("&Next", table) and re.search(r"[؀-ۿ]", theme_msi.translate("&Next", table))
    assert theme_msi.translate("[ProductName] Setup", table) == "إعداد [ProductName]"
    assert theme_msi.translate("Select shortcuts to create.", table)
    assert theme_msi.translate("no such words", table) is None
    rows = theme_msi.arabic_rows([("WelcomeDlg\x1fTitle", CONTROLS[1]["Text"]), ("x", "unknown")], table)
    assert list(rows) == ["WelcomeDlg\x1fTitle"]


class FakeMsi:
    """theme_msi's database calls over in-memory tables: (key columns, column kinds, rows)."""

    def __init__(self, tables: dict) -> None:
        self.tables = tables
        self.bound: list[tuple] = []

    def keys(self, db, name):
        return self.tables[name][0]

    def kinds(self, db, sql):
        name = re.search(r"FROM `(\w+)`", sql).group(1)
        cols = re.findall(r"`(\w+)`", sql.split(" FROM ")[0])
        return "".join(self.tables[name][1][c] for c in cols)

    def rows(self, db, sql, kinds):
        name = re.search(r"FROM `(\w+)`", sql).group(1)
        cols = re.findall(r"`(\w+)`", sql.split(" FROM ")[0])
        for row in self.tables[name][2]:
            got = [row[c] for c in cols]
            for value, kind in zip(got, kinds):
                assert isinstance(value, int if kind == "i" else str)
            yield got

    def execute(self, db, sql, *params):
        self.bound.append((sql, params))


def test_integer_keys_are_bound_as_integers() -> None:
    table = theme_msi.translations(wixui.load()["strings"])
    msi = FakeMsi({
        # the Error table: Error (an integer key), Message
        "Error": (["Error"], {"Error": "i", "Message": "s"}, [{"Error": 1, "Message": "Cancel"}, {"Error": 2, "Message": "no Arabic for these words"}]),
        # RadioButton: Property (a string) and Order (an integer)
        "RadioButton": (["Property", "Order"], {"Property": "s", "Order": "i", "Text": "s"}, [{"Property": "WixUI_InstallMode", "Order": 1, "Text": "&Next"}]),
        "Dialog": (["Dialog"], {"Dialog": "s", "Title": "s"}, [{"Dialog": "WelcomeDlg", "Title": "[ProductName] Setup"}]),
    })
    assert theme_msi.arabic_table(msi, 0, "Error", "Message", {"Cancel": "إلغاء الأمر"}) == 1
    assert theme_msi.arabic_table(msi, 0, "RadioButton", "Text", table) == 1
    assert theme_msi.arabic_table(msi, 0, "Dialog", "Title", table) == 1
    (sql, params), (sql2, params2), (sql3, params3) = msi.bound
    assert sql == "UPDATE `Error` SET `Message` = ? WHERE `Error` = ?" and params == ("إلغاء الأمر", 1)
    assert type(params[1]) is int
    assert sql2.endswith("WHERE `Property` = ? AND `Order` = ?") and params2[1:] == ("WixUI_InstallMode", 1)
    assert type(params2[2]) is int
    assert params3[1:] == ("WelcomeDlg",) and type(params3[1]) is str


def test_jpackages_words_keep_their_placeholders() -> None:
    for en, ar in theme_msi.JPACKAGE_AR.items():
        assert set(re.findall(r"\[\w+\]", en)) == set(re.findall(r"\[\w+\]", ar)), en


def test_the_arabic_dialogs_mirror() -> None:
    out = theme_msi.mirror(CONTROLS, {"WelcomeDlg": {"Width": 370, "Height": 270, "Attributes": 39}, "InstallDirDlg": {"Width": 370, "Height": 270, "Attributes": 39}})
    x, attrs = out[("WelcomeDlg", "Title")]
    assert x == 370 - 135 - 220 and attrs & theme_msi.CONTROL_BIDI == theme_msi.CONTROL_BIDI
    assert out[("WelcomeDlg", "Next")][0] == 370 - 236 - 56
    # the bitmap spans the dialog, and draws no words
    assert out[("WelcomeDlg", "Bitmap")] == (0, 1)


def test_the_template_names_arabic() -> None:
    assert theme_msi.template_with_arabic("x64;1033") == "x64;1033,1025"
    assert theme_msi.template_with_arabic("x64;1033,1025") == "x64;1033,1025"
    assert theme_msi.template_with_arabic("x64;") == "x64;1033,1025"


def test_the_player_build_themes_its_msi() -> None:
    gradle = (ROOT / "apps/desktop-player/build.gradle.kts").read_text(encoding="utf-8")
    assert 'it.name == "packageMsi"' in gradle and "tools/release/msi/theme_msi.py" in gradle
    for wf in (".github/workflows/_desktop-player.yml", ".github/workflows/desktop-release.yml"):
        assert "tools/release/msi/theme_msi.py --check" in (ROOT / wf).read_text(encoding="utf-8"), wf


def test_the_arabic_msi_is_installed_by_windows_installer() -> None:
    # MsiDatabaseApplyTransform (theme_msi.py --check) is not msiexec: CI's Player job installs it
    wf = (ROOT / ".github/workflows/_desktop-player.yml").read_text(encoding="utf-8")
    assert wf.index("theme_msi.py --check") < wf.index("tools/release/msi/install-arabic.ps1")
    script = (ROOT / "tools/release/msi/install-arabic.ps1").read_text(encoding="utf-8")
    for want in ("TRANSFORMS=:1025", "/qn", "/l*v", "'/x'", "ProductLanguage = 1025"):
        assert want in script, want


# --- the .dmg ------------------------------------------------------------------------------


def test_the_dmg_window_comes_from_the_config() -> None:
    want = dmg_layout.layout(str(SRC_TAURI / "tauri.conf.json"))
    assert want["background"] == "dmg-background.tiff"
    w, h = want["size"]
    for name, (x, y) in want["icons"].items():
        assert 64 <= x <= w - 64 and 64 <= y <= h - 64, name
    assert set(want["icons"]) == {"Maktaba.app", "Applications"}


class _Rec:
    def __init__(self, **kw):
        self.__dict__.update(kw)


def test_the_dmg_check_resolves_the_backgrounds_alias(tmp_path: Path) -> None:
    want = dmg_layout.layout(str(SRC_TAURI / "tauri.conf.json"))
    bg = tmp_path / ".background" / want["background"]
    bg.parent.mkdir()
    bg.write_bytes(b"tiff")

    def alias(name="Maktaba", path=f"/.background/{want['background']}", cnid=None, folder=None):
        return _Rec(
            volume=_Rec(name=name),
            target=_Rec(posix_path=path, cnid=bg.stat().st_ino if cnid is None else cnid,
                        folder_cnid=bg.parent.stat().st_ino if folder is None else folder),
        )

    assert dmg_layout.alias_problems(alias(), str(tmp_path), want) == []
    # what a mount through a link (/var/folders for /private/var/folders) recorded: a path out
    # of the volume
    assert dmg_layout.alias_problems(alias(path="/../../var/folders/x/mnt/.background/" + want["background"]), str(tmp_path), want)
    assert dmg_layout.alias_problems(alias(name="Other"), str(tmp_path), want)
    assert dmg_layout.alias_problems(alias(cnid=1), str(tmp_path), want)
    assert dmg_layout.alias_problems(alias(folder=1), str(tmp_path), want)


def test_the_dmg_layout_mounts_at_the_real_path_and_waits_out_a_busy_image() -> None:
    layout = (ROOT / "tools/release/mac-app/dmg-layout.sh").read_text(encoding="utf-8")
    assert 'SCRATCH="$(cd "$(mktemp -d' in layout and '&& pwd -P)"' in layout
    assert "grep -q 'Resource busy'" in layout
    # every hdiutil step goes through the retry, but the exit trap's best-effort detach
    plain = [l.strip() for l in layout.splitlines() if re.match(r"\s*hdiutil (attach|convert|detach)", l) and not l.rstrip().endswith("|| true")]
    assert plain == [], plain
    for step in ("hdi attach", "hdi convert", "hdi detach"):
        assert step in layout, step


def test_every_mac_build_lays_its_dmg_out_and_checks_it() -> None:
    build = (ROOT / "tools/release/mac-app/tauri-build.sh").read_text(encoding="utf-8")
    assert "lay_out_dmgs" in build and "dmg-layout.sh" in build
    check = (ROOT / "tools/release/mac-app/check-dmg.sh").read_text(encoding="utf-8")
    assert "dmg-layout.sh\" --check" in check
    layout = (ROOT / "tools/release/mac-app/dmg-layout.sh").read_text(encoding="utf-8")
    assert "fetch-pinned.sh" in layout
    assert re.search(r"^DS_STORE_SHA256=[0-9a-f]{64}$", layout, re.M)
    assert re.search(r"^MAC_ALIAS_SHA256=[0-9a-f]{64}$", layout, re.M)
