"""Story 58.15 T02: the Roku build step (tools/shape_strings.py)."""

from __future__ import annotations

import json
import shutil
import xml.etree.ElementTree as ET
from pathlib import Path

import pytest

import shape_strings
import visual

ROKU = Path(__file__).resolve().parents[2]


def _catalog(tmp: Path, en: dict[str, object], ar: dict[str, object]) -> Path:
    (tmp / "i18n").mkdir(parents=True)
    (tmp / "i18n" / "en.json").write_text(json.dumps(en, ensure_ascii=False), encoding="utf-8")
    (tmp / "i18n" / "ar.json").write_text(json.dumps(ar, ensure_ascii=False), encoding="utf-8")
    return tmp


def test_the_committed_catalogs_are_current() -> None:
    assert shape_strings.main(["--check"]) == 0


def test_arabic_statics_equal_the_reference_line_by_line() -> None:
    en = json.loads((ROKU / "i18n" / "en.json").read_text(encoding="utf-8"))
    ar = json.loads((ROKU / "i18n" / "ar.json").read_text(encoding="utf-8"))
    out = json.loads((ROKU / "locale" / "ar_SA" / "strings.json").read_text(encoding="utf-8"))
    assert out["rtl"] is True
    for key, visual_text in out["strings"].items():
        meta = en["@" + key]
        want = visual.visual_lines(ar[key], meta["widthChars"], meta["lines"], "rtl")
        assert visual_text.split("\n") == want, key
    # every key is either a static string or a template
    keys = {k for k in ar if not k.startswith("@")}
    assert keys == set(out["strings"]) | set(out["templates"])
    assert "video.resumeFrom" in out["templates"]
    assert out["templates"]["video.resumeFrom"]["text"] == "المتابعة من %1$s"


def test_wrapped_lines_are_reordered_per_line(tmp_path: Path) -> None:
    text = "هذا الفيديو غير متاح الآن على هذا الجهاز"
    root = _catalog(tmp_path, {"@k": {"widthChars": 16, "lines": 3}, "k": "x"}, {"k": text})
    files, problems = shape_strings.build(root)
    assert problems == []
    lines = json.loads(files[root / "locale" / "ar_SA" / "strings.json"])["strings"]["k"].split("\n")
    assert len(lines) == 3
    # line 1 is the start of the sentence, its first word on the right
    assert lines[0].endswith(visual.visual_text("هذا"))
    assert lines[2].endswith(visual.visual_text("هذا"))  # هذا الجهاز: the last line
    assert lines == [visual.visual_text(line, 0, "rtl") for line in visual.wrap(text, 16)]


@pytest.mark.parametrize(
    ("en", "ar", "message"),
    [
        (
            {"@k": {"widthChars": 10, "lines": 1}, "k": "Short"},
            {"k": "نص طويل جدا لا يتسع"},
            "ar.json: 'k' needs 3 lines of 10",
        ),
        (
            {"@k": {"widthChars": 10, "lines": 1}, "k": "Much too long here"},
            {"k": "قصير"},
            "en.json: 'k' needs 2 lines",
        ),
        ({"@k": {"widthChars": 4, "lines": 3}, "k": "Unbreakable"}, {"k": "قصير"}, "a word longer than its label"),
        ({"k": "No size"}, {"k": "بلا حجم"}, "'k' has no label size"),
    ],
)
def test_a_string_too_long_for_its_label_fails_naming_the_key(
    tmp_path: Path, en: dict[str, object], ar: dict[str, object], message: str
) -> None:
    _, problems = shape_strings.build(_catalog(tmp_path, en, ar))
    assert any(message in p for p in problems), problems
    assert shape_strings.main(["--root", str(tmp_path)]) == 1


def test_translations_ts_is_roku_xml() -> None:
    root = ET.parse(ROKU / "locale" / "ar_SA" / "translations.ts").getroot()
    assert root.tag == "TS" and root.get("language") == "ar_SA"
    messages = {m.findtext("source"): m.findtext("translation") for m in root.iter("message")}
    out = json.loads((ROKU / "locale" / "ar_SA" / "strings.json").read_text(encoding="utf-8"))
    assert messages == out["strings"]


def test_check_finds_a_stale_file(tmp_path: Path) -> None:
    shutil.copytree(ROKU / "i18n", tmp_path / "i18n")
    shutil.copytree(ROKU / "locale", tmp_path / "locale")
    assert shape_strings.main(["--check", "--root", str(tmp_path)]) == 0
    (tmp_path / "locale" / "ar_SA" / "strings.json").write_text("{}", encoding="utf-8")
    assert shape_strings.main(["--check", "--root", str(tmp_path)]) == 1
