#!/usr/bin/env python3
"""Maktaba's look in the Player's .msi, and its Arabic (Epic 13 story 13.14, decision D3).

compose builds the Player's .msi with jpackage, whose stock WiX dialogs carry WiX's bitmaps
and English only. compose passes jpackage a resource folder of its own and empties it before
every run (AbstractJPackageTask.prepareWorkingDir), so nothing reaches jpackage's WiX step; the
.msi is themed after it is built instead, through Windows Installer's own API (msi.dll),
before it is signed:

  1. the bitmaps: WixUI_Bmp_Dialog and WixUI_Bmp_Banner become apps/desktop-player/msi's
     dialog-en.bmp and banner-en.bmp (drawn by docs/design/desktop/installer-art.mjs);
  2. the words on them take Maktaba's text styles, light on ink (wixui.py: Maktaba_Bigger,
     Maktaba_Title, Maktaba_Light): every Text control inside a dialog's bitmap or banner;
  3. Arabic: a language transform, embedded as the storage "1025" and named in the package's
     Template (x64;1033,1025), so Windows Installer applies it itself where Windows' language
     is Arabic: WiX's Arabic words (wixui-strings.json) and jpackage's (below), right to left
     (each dialog's controls mirrored, read right to left), the mirrored bitmaps
     (dialog-ar.bmp, banner-ar.bmp), Arabic's code page (1256).

  theme_msi.py MSI            theme it in place (Windows: compose's packageMsi runs it)
  theme_msi.py --check MSI    fail unless MSI is themed: CI's Player job and the release legs

What changes is decided by plain functions over the tables (restyle_controls, arabic_rows,
mirror), tested on every OS (tools/ci/tests/test_installers.py); only reading and writing the
database needs Windows.
"""

from __future__ import annotations

import os
import shutil
import sys
import tempfile
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
import wixui  # noqa: E402

ROOT = HERE.parents[2]
ART = ROOT / "apps" / "desktop-player" / "msi"
ARABIC = 1025
ENGLISH = 1033
STORAGE = str(ARABIC)

# jpackage's own words (jdk.jpackage's MsiInstallerStrings_en.wxl), in Arabic
JPACKAGE_AR = {
    "Main Feature": "الميزة الرئيسية",
    "A higher version of [ProductName] is already installed. Downgrades disabled. Setup will now exit.":
        "إصدار أحدث من [ProductName] مثبّت بالفعل، والرجوع إلى إصدار أقدم غير مسموح. سيُغلق الإعداد الآن.",
    "A lower version of [ProductName] is already installed. Upgrades disabled. Setup will now exit.":
        "إصدار أقدم من [ProductName] مثبّت بالفعل، والترقية غير مسموحة. سيُغلق الإعداد الآن.",
    "[ProductName] Setup": "إعداد [ProductName]",
    "Shortcuts": "الاختصارات",
    "Select shortcuts to create.": "اختر الاختصارات التي تُنشأ.",
    "Create desktop shortcut(s)": "إنشاء اختصار على سطح المكتب",
    "Create start menu shortcut(s)": "إنشاء اختصار في قائمة ابدأ",
    "The folder [INSTALLDIR] already exist. Would you like to install to that folder anyway?":
        "المجلد [INSTALLDIR] موجود. أتريد التثبيت فيه على أي حال؟",
    "Open with [ProductName]": "فتح باستخدام [ProductName]",
    "[ProductName][ProductVersion] is not supported on this version of Windows":
        "لا يعمل [ProductName][ProductVersion] على هذا الإصدار من Windows",
}

# the tables and columns whose words the Arabic transform translates
TRANSLATED = [
    ("Control", "Text"),
    ("Dialog", "Title"),
    ("RadioButton", "Text"),
    ("UIText", "Text"),
    ("ActionText", "Description"),
    ("ActionText", "Template"),
    ("Error", "Message"),
    ("Feature", "Title"),
    ("LaunchCondition", "Description"),
    ("CustomAction", "Target"),
]
# Windows Installer's right-to-left bits: a dialog's, a control's (RTLRO, RightAligned, LeftScroll)
DIALOG_BIDI = 0x80 | 0x100 | 0x200
CONTROL_BIDI = 0x20 | 0x40 | 0x80
BITMAPS = {"WixUI_Bmp_Dialog": "dialog", "WixUI_Bmp_Banner": "banner"}


# --- what changes (plain functions over rows) ---------------------------------------------


def bitmap_boxes(controls: list[dict]) -> dict[str, list[tuple[str, tuple[int, int, int, int]]]]:
    """Per dialog, the boxes of its WiX bitmaps: [(kind, (x, y, w, h))]."""
    boxes: dict[str, list] = {}
    for c in controls:
        if c["Type"] == "Bitmap" and c["Text"] in BITMAPS:
            boxes.setdefault(c["Dialog_"], []).append((BITMAPS[c["Text"]], (c["X"], c["Y"], c["Width"], c["Height"])))
    return boxes


def restyle_controls(controls: list[dict]) -> dict[tuple[str, str], str]:
    """(dialog, control) -> its new Text: each Text control inside a bitmap's box, its words
    in Maktaba's style for that bitmap."""
    out = {}
    boxes = bitmap_boxes(controls)
    for c in controls:
        if c["Type"] != "Text" or not c["Text"]:
            continue
        for kind, (x, y, w, h) in boxes.get(c["Dialog_"], []):
            if c["X"] >= x and c["Y"] >= y and c["X"] + c["Width"] <= x + w and c["Y"] + c["Height"] <= y + h:
                new = wixui.restyle(c["Text"], kind)
                if new != c["Text"]:
                    out[(c["Dialog_"], c["Control"])] = new
                break
    return out


def translations(strings: dict) -> dict[str, str]:
    """English words -> Arabic: WiX's (every WixUI string, without its style) and jpackage's."""
    out = {}
    for words in strings.values():
        en, ar = words.get("en"), words.get("ar")
        if en and ar:
            out.setdefault(wixui.unstyled(en), wixui.unstyled(ar))
    out.update(JPACKAGE_AR)
    return out


def translate(text: str, table: dict[str, str]) -> str | None:
    """TEXT in Arabic, keeping its style prefix; None when there are no Arabic words for it."""
    if not text:
        return None
    m = wixui._STYLE.match(text)
    prefix, body = (text[: m.end()], text[m.end():]) if m else ("", text)
    ar = table.get(body)
    return None if ar is None else prefix + ar


def mirror(controls: list[dict], dialogs: dict[str, dict]) -> dict[tuple[str, str], tuple[int, int]]:
    """(dialog, control) -> (X, Attributes) right to left: each control at the mirrored place in
    its dialog, its words read right to left."""
    out = {}
    for c in controls:
        d = dialogs.get(c["Dialog_"])
        if not d:
            continue
        x = d["Width"] - c["X"] - c["Width"]
        attrs = c["Attributes"]
        if c["Type"] not in ("Bitmap", "Line", "ProgressBar", "Icon"):
            attrs |= CONTROL_BIDI
        out[(c["Dialog_"], c["Control"])] = (max(x, 0), attrs)
    return out


def template_with_arabic(template: str) -> str:
    """The Summary's Template ("x64;1033") with Arabic among its languages."""
    platform, _, langs = template.partition(";")
    ids = [x for x in langs.split(",") if x] or [str(ENGLISH)]
    if STORAGE not in ids:
        ids.append(STORAGE)
    return f"{platform};{','.join(ids)}"


# --- msi.dll ----------------------------------------------------------------------------------


class MsiError(RuntimeError):
    pass


class Msi:
    """The few calls of msi.dll this needs (Windows only)."""

    def __init__(self) -> None:
        import ctypes
        from ctypes import wintypes

        self.c = ctypes
        self.dll = ctypes.WinDLL("msi.dll")
        self.H = wintypes.DWORD

    def check(self, rc: int, what: str) -> None:
        if rc not in (0, 259):  # ERROR_SUCCESS, ERROR_NO_MORE_ITEMS
            raise MsiError(f"{what}: Windows Installer error {rc}")

    def open(self, path: Path, mode: int):
        h = self.H()
        # MSIDBOPEN_* are small integers passed where a path would be
        self.check(self.dll.MsiOpenDatabaseW(str(path), self.c.c_void_p(mode), self.c.byref(h)), f"open {path}")
        return h

    def close(self, h) -> None:
        self.dll.MsiCloseHandle(h)

    def commit(self, db) -> None:
        self.check(self.dll.MsiDatabaseCommit(db), "commit")

    def view(self, db, sql: str):
        v = self.H()
        self.check(self.dll.MsiDatabaseOpenViewW(db, sql, self.c.byref(v)), sql)
        self.check(self.dll.MsiViewExecute(v, 0), sql)
        return v

    def rows(self, db, sql: str, kinds: str):
        """Each row of SQL, its columns read as KINDS: s (string) or i (integer)."""
        v = self.view(db, sql)
        try:
            while True:
                r = self.H()
                rc = self.dll.MsiViewFetch(v, self.c.byref(r))
                if rc == 259:
                    return
                self.check(rc, sql)
                try:
                    yield [self.string(r, i + 1) if k == "s" else self.integer(r, i + 1) for i, k in enumerate(kinds)]
                finally:
                    self.close(r)
        finally:
            self.close(v)

    def string(self, rec, field: int) -> str:
        n = self.H(0)
        self.dll.MsiRecordGetStringW(rec, field, None, self.c.byref(n))
        buf = self.c.create_unicode_buffer(n.value + 1)
        n = self.H(n.value + 1)
        self.check(self.dll.MsiRecordGetStringW(rec, field, buf, self.c.byref(n)), "read a string")
        return buf.value

    def integer(self, rec, field: int) -> int:
        self.dll.MsiRecordGetInteger.restype = self.c.c_int
        return self.dll.MsiRecordGetInteger(rec, field)

    def execute(self, db, sql: str, *params) -> None:
        """SQL with ? parameters: str, int, or a Path (a stream from that file)."""
        v = self.H()
        self.check(self.dll.MsiDatabaseOpenViewW(db, sql, self.c.byref(v)), sql)
        rec = self.dll.MsiCreateRecord(len(params)) if params else 0
        try:
            for i, p in enumerate(params, 1):
                if isinstance(p, Path):
                    self.check(self.dll.MsiRecordSetStreamW(rec, i, str(p)), f"stream {p}")
                elif isinstance(p, int):
                    self.check(self.dll.MsiRecordSetInteger(rec, i, p), sql)
                else:
                    self.check(self.dll.MsiRecordSetStringW(rec, i, p), sql)
            self.check(self.dll.MsiViewExecute(v, rec), sql)
        finally:
            if rec:
                self.close(rec)
            self.close(v)

    def tables(self, db) -> set[str]:
        return {r[0] for r in self.rows(db, "SELECT `Name` FROM `_Tables`", "s")}

    def keys(self, db, table: str) -> list[str]:
        """The primary key columns of TABLE."""
        keys = self.H()
        self.check(self.dll.MsiDatabaseGetPrimaryKeysW(db, table, self.c.byref(keys)), f"the keys of {table}")
        try:
            return [self.string(keys, i) for i in range(1, self.dll.MsiRecordGetFieldCount(keys) + 1)]
        finally:
            self.close(keys)

    def kinds(self, db, sql: str) -> str:
        """SQL's columns as "s" (a string) or "i" (an integer), from the view's column types
        (MSICOLINFO_TYPES: s, l, g a string, i, j an integer, v a stream; capital when nullable)."""
        v = self.view(db, sql)
        rec = self.H()
        try:
            self.check(self.dll.MsiViewGetColumnInfo(v, 1, self.c.byref(rec)), f"the column types of {sql}")
            n = self.dll.MsiRecordGetFieldCount(rec)
            return "".join("i" if self.string(rec, i)[:1].lower() in "ij" else "s" for i in range(1, n + 1))
        finally:
            self.close(rec)
            self.close(v)

    def stream_bytes(self, db, table: str, key: str, value: str) -> bytes:
        v = self.H()
        sql = f"SELECT `Data` FROM `{table}` WHERE `{key}` = '{value}'"
        self.check(self.dll.MsiDatabaseOpenViewW(db, sql, self.c.byref(v)), sql)
        self.check(self.dll.MsiViewExecute(v, 0), sql)
        r = self.H()
        rc = self.dll.MsiViewFetch(v, self.c.byref(r))
        self.close(v)
        if rc == 259:
            raise MsiError(f"no {table} row {value}")
        out = bytearray()
        try:
            while True:
                buf = self.c.create_string_buffer(65536)
                n = self.H(65536)
                self.check(self.dll.MsiRecordReadStream(r, 1, buf, self.c.byref(n)), "read a stream")
                if n.value == 0:
                    return bytes(out)
                out += buf.raw[: n.value]
        finally:
            self.close(r)

    def summary(self, path_or_db, pid: int, value: str | None = None) -> str:
        si = self.H()
        db, path = (path_or_db, None) if not isinstance(path_or_db, Path) else (0, str(path_or_db))
        self.check(self.dll.MsiGetSummaryInformationW(db, path, 1 if value is not None else 0, self.c.byref(si)), "summary")
        try:
            if value is not None:
                self.check(self.dll.MsiSummaryInfoSetPropertyW(si, pid, 30, 0, None, value), "summary set")  # VT_LPSTR
                self.check(self.dll.MsiSummaryInfoPersist(si), "summary persist")
                return value
            kind, ival = self.H(), self.c.c_int()
            n = self.H(1024)
            buf = self.c.create_unicode_buffer(1024)
            self.check(self.dll.MsiSummaryInfoGetPropertyW(si, pid, self.c.byref(kind), self.c.byref(ival), None, buf, self.c.byref(n)), "summary get")
            return buf.value
        finally:
            self.close(si)


def extract_storage(package: Path, name: str, dest: Path) -> None:
    """Copy the substorage NAME of PACKAGE (an embedded transform) into the file DEST. Windows
    Installer reads a _Storages row's Data only as a whole storage (MsiRecordReadStream answers
    ERROR_INVALID_DATA), so this goes through OLE structured storage (ole32), as msidb -x does:
    the root storage, the substorage, CopyTo a new docfile, with the substorage's class (a
    transform's CLSID, which MsiDatabaseApplyTransform checks)."""
    import ctypes
    from ctypes import HRESULT, POINTER, WINFUNCTYPE, byref, c_ulong, c_void_p, c_wchar_p, wintypes

    ole32 = ctypes.OleDLL("ole32")
    STGM_READ, STGM_READWRITE, STGM_CREATE = 0x0, 0x2, 0x1000
    STGM_SHARE_DENY_WRITE, STGM_SHARE_EXCLUSIVE = 0x20, 0x10

    class STATSTG(ctypes.Structure):
        _fields_ = [
            ("pwcsName", c_void_p), ("type", wintypes.DWORD), ("cbSize", ctypes.c_ulonglong),
            ("mtime", ctypes.c_ulonglong), ("ctime", ctypes.c_ulonglong), ("atime", ctypes.c_ulonglong),
            ("grfMode", wintypes.DWORD), ("grfLocksSupported", wintypes.DWORD),
            ("clsid", ctypes.c_byte * 16), ("grfStateBits", wintypes.DWORD), ("reserved", wintypes.DWORD),
        ]

    def method(ptr, index, restype, *argtypes):
        vtbl = ctypes.cast(ptr, POINTER(POINTER(c_void_p))).contents
        return WINFUNCTYPE(restype, c_void_p, *argtypes)(vtbl[index])

    def release(ptr):
        if ptr:
            method(ptr, 2, c_ulong)(ptr)

    ole32.CoInitialize(None)
    root, sub, out = c_void_p(), c_void_p(), c_void_p()
    try:
        ole32.StgOpenStorage(c_wchar_p(str(package)), None, STGM_READ | STGM_SHARE_DENY_WRITE, None, 0, byref(root))
        # IStorage::OpenStorage (6), CopyTo (7), Commit (9), SetClass (15), Stat (17)
        method(root, 6, HRESULT, c_wchar_p, c_void_p, wintypes.DWORD, c_void_p, wintypes.DWORD, POINTER(c_void_p))(
            root, name, None, STGM_READ | STGM_SHARE_EXCLUSIVE, None, 0, byref(sub))
        ole32.StgCreateDocfile(c_wchar_p(str(dest)), STGM_CREATE | STGM_READWRITE | STGM_SHARE_EXCLUSIVE, 0, byref(out))
        method(sub, 7, HRESULT, wintypes.DWORD, c_void_p, c_void_p, c_void_p)(sub, 0, None, None, out)
        stat = STATSTG()
        method(sub, 17, HRESULT, POINTER(STATSTG), wintypes.DWORD)(sub, byref(stat), 1)  # STATFLAG_NONAME
        method(out, 15, HRESULT, c_void_p)(out, ctypes.addressof(stat.clsid))
        method(out, 9, HRESULT, wintypes.DWORD)(out, 0)
    finally:
        release(out)
        release(sub)
        release(root)


MSIDBOPEN_READONLY = 0
MSIDBOPEN_TRANSACT = 1
PID_TEMPLATE = 7


def _controls(msi: Msi, db) -> list[dict]:
    cols = ["Dialog_", "Control", "Type", "X", "Y", "Width", "Height", "Attributes", "Text"]
    sql = "SELECT `Dialog_`, `Control`, `Type`, `X`, `Y`, `Width`, `Height`, `Attributes`, `Text` FROM `Control`"
    return [dict(zip(cols, r)) for r in msi.rows(db, sql, "sssiiiiis")]


def _dialogs(msi: Msi, db) -> dict[str, dict]:
    sql = "SELECT `Dialog`, `Width`, `Height`, `Attributes` FROM `Dialog`"
    return {r[0]: {"Width": r[1], "Height": r[2], "Attributes": r[3]} for r in msi.rows(db, sql, "siii")}


def _bitmaps(msi: Msi, db, lang: str) -> None:
    for name, kind in BITMAPS.items():
        msi.execute(db, "UPDATE `Binary` SET `Data` = ? WHERE `Name` = ?", ART / f"{kind}-{lang}.bmp", name)


def _styles(msi: Msi, db) -> None:
    have = {r[0] for r in msi.rows(db, "SELECT `TextStyle` FROM `TextStyle`", "s")}
    for name, (face, size, rgb, bold) in wixui.TEXT_STYLES.items():
        if name not in have:
            msi.execute(
                db,
                "INSERT INTO `TextStyle` (`TextStyle`, `FaceName`, `Size`, `Color`, `StyleBits`) VALUES (?, ?, ?, ?, ?)",
                name, face, size, wixui.colorref(rgb), 1 if bold else 0,
            )
    for (dialog, control), text in restyle_controls(_controls(msi, db)).items():
        msi.execute(db, "UPDATE `Control` SET `Text` = ? WHERE `Dialog_` = ? AND `Control` = ?", text, dialog, control)


def arabic_rows(rows: list[tuple], table: dict[str, str]) -> dict:
    """For one column's (key, text) rows: key -> the Arabic text, for each with Arabic words."""
    out = {}
    for key, text in rows:
        ar = translate(text, table)
        if ar is not None and ar != text:
            out[key] = ar
    return out


def arabic_table(msi, db, name: str, column: str, table: dict[str, str]) -> int:
    """NAME's COLUMN in Arabic, each row found by its primary key, bound as the key's own type
    (the Error and RadioButton tables' keys are integers: a string never matches them). MSI is
    anything with keys(), kinds(), rows() and execute() (msi.dll's, or a test's). The rows
    changed."""
    keys = msi.keys(db, name)
    sql = "SELECT " + ", ".join(f"`{k}`" for k in keys) + f", `{column}` FROM `{name}`"
    kinds = msi.kinds(db, sql)
    rows = [(tuple(r[:-1]), r[-1]) for r in msi.rows(db, sql, kinds)]
    where = " AND ".join(f"`{k}` = ?" for k in keys)
    changed = arabic_rows(rows, table)
    for key, ar in changed.items():
        msi.execute(db, f"UPDATE `{name}` SET `{column}` = ? WHERE {where}", ar, *key)
    return len(changed)


def _arabic(msi: Msi, db) -> None:
    table = translations(wixui.load()["strings"])
    present = msi.tables(db)
    for name, column in TRANSLATED:
        if name not in present:
            continue
        arabic_table(msi, db, name, column, table)
    dialogs = _dialogs(msi, db)
    for (dialog, control), (x, attrs) in mirror(_controls(msi, db), dialogs).items():
        msi.execute(db, "UPDATE `Control` SET `X` = ?, `Attributes` = ? WHERE `Dialog_` = ? AND `Control` = ?", x, attrs, dialog, control)
    for dialog, d in dialogs.items():
        msi.execute(db, "UPDATE `Dialog` SET `Attributes` = ? WHERE `Dialog` = ?", d["Attributes"] | DIALOG_BIDI, dialog)
    msi.execute(db, "UPDATE `Property` SET `Value` = ? WHERE `Property` = 'ProductLanguage'", str(ARABIC))
    _bitmaps(msi, db, "ar")


def _codepage(msi: Msi, db, tmp: Path, codepage: int) -> None:
    (tmp / "_ForceCodepage.idt").write_bytes(f"\r\n\r\n{codepage}\t_ForceCodepage\r\n".encode("ascii"))
    msi.check(msi.dll.MsiDatabaseImportW(db, str(tmp), "_ForceCodepage.idt"), "code page")


def theme(path: Path) -> None:
    msi = Msi()
    with tempfile.TemporaryDirectory() as t:
        tmp = Path(t)
        db = msi.open(path, MSIDBOPEN_TRANSACT)
        try:
            _bitmaps(msi, db, "en")
            _styles(msi, db)
            msi.commit(db)
        finally:
            msi.close(db)
        # the Arabic package: a copy, in Arabic, against which the transform is made
        ar = tmp / "ar.msi"
        shutil.copy(path, ar)
        db = msi.open(ar, MSIDBOPEN_TRANSACT)
        try:
            _codepage(msi, db, tmp, 1256)
            _arabic(msi, db)
            msi.commit(db)
        finally:
            msi.close(db)
        mst = tmp / f"{STORAGE}.mst"
        dar, dbase = msi.open(ar, MSIDBOPEN_READONLY), msi.open(path, MSIDBOPEN_READONLY)
        try:
            msi.check(msi.dll.MsiDatabaseGenerateTransformW(dar, dbase, str(mst), 0, 0), "make the transform")
            msi.check(msi.dll.MsiCreateTransformSummaryInfoW(dar, dbase, str(mst), 0, 0), "the transform's summary")
        finally:
            msi.close(dar)
            msi.close(dbase)
        db = msi.open(path, MSIDBOPEN_TRANSACT)
        try:
            msi.execute(db, "DELETE FROM `_Storages` WHERE `Name` = ?", STORAGE)
            msi.execute(db, "INSERT INTO `_Storages` (`Name`, `Data`) VALUES (?, ?)", STORAGE, mst)
            template = msi.summary(db, PID_TEMPLATE)
            msi.summary(db, PID_TEMPLATE, template_with_arabic(template))
            msi.commit(db)
        finally:
            msi.close(db)


def check(path: Path) -> list[str]:
    """What is not themed in PATH, also with its Arabic transform applied."""
    msi = Msi()
    problems: list[str] = []
    storages: set[str] = set()
    with tempfile.TemporaryDirectory() as t:
        tmp = Path(t)
        db = msi.open(path, MSIDBOPEN_READONLY)
        try:
            for name, kind in BITMAPS.items():
                if msi.stream_bytes(db, "Binary", "Name", name) != (ART / f"{kind}-en.bmp").read_bytes():
                    problems.append(f"{name} is not {kind}-en.bmp")
            styles = {r[0] for r in msi.rows(db, "SELECT `TextStyle` FROM `TextStyle`", "s")}
            problems += [f"no text style {s}" for s in wixui.TEXT_STYLES if s not in styles]
            texts = [c for c in _controls(msi, db) if "{\\Maktaba_" in (c["Text"] or "")]
            if not any(c["Dialog_"] == "WelcomeDlg" and c["Control"] == "Title" for c in texts):
                problems.append("WelcomeDlg's title is not in Maktaba_Bigger")
            storages = {r[0] for r in msi.rows(db, "SELECT `Name` FROM `_Storages`", "s")}
            if STORAGE not in storages:
                problems.append(f"no embedded Arabic transform ({STORAGE})")
            template = msi.summary(path, PID_TEMPLATE)
            if STORAGE not in template.partition(";")[2].split(","):
                problems.append(f"the Template ({template}) does not name {STORAGE}")
        finally:
            msi.close(db)
        if STORAGE in storages:
            # the transform, applied to a copy, gives the Arabic dialogs
            mst = tmp / "ar.mst"
            extract_storage(path, STORAGE, mst)
            copy = tmp / "ar.msi"
            shutil.copy(path, copy)
            db = msi.open(copy, MSIDBOPEN_TRANSACT)
            try:
                msi.check(msi.dll.MsiDatabaseApplyTransformW(db, str(mst), 0), "apply the transform")
                controls = {(c["Dialog_"], c["Control"]): c for c in _controls(msi, db)}
                title = controls.get(("WelcomeDlg", "Title"), {}).get("Text", "")
                if not any("؀" <= ch <= "ۿ" for ch in title):
                    problems.append(f"the Arabic WelcomeDlg title is not Arabic: {title!r}")
                dialogs = _dialogs(msi, db)
                if dialogs.get("WelcomeDlg", {}).get("Attributes", 0) & DIALOG_BIDI != DIALOG_BIDI:
                    problems.append("the Arabic WelcomeDlg is not right to left")
                if msi.stream_bytes(db, "Binary", "Name", "WixUI_Bmp_Dialog") != (ART / "dialog-ar.bmp").read_bytes():
                    problems.append("the Arabic WixUI_Bmp_Dialog is not dialog-ar.bmp")
            finally:
                msi.close(db)
    return problems


def main(argv: list[str]) -> int:
    if os.name != "nt":
        print("theme_msi: Windows Installer's API is Windows' only", file=sys.stderr)
        return 2
    if len(argv) == 2 and argv[0] == "--check":
        problems = check(Path(argv[1]))
        for p in problems:
            print(f"::error::theme_msi: {argv[1]}: {p}")
        if not problems:
            print(f"theme_msi: {argv[1]}: Maktaba's bitmaps and text styles, and its Arabic transform")
        return 1 if problems else 0
    if len(argv) == 1:
        theme(Path(argv[0]))
        print(f"theme_msi: {argv[0]}: themed, with its Arabic transform")
        return 0
    print(__doc__, file=sys.stderr)
    return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
