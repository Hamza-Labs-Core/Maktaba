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
    # every key is a static string, a template or a plural
    keys = {k for k in ar if not k.startswith("@")}
    assert keys == set(out["strings"]) | set(out["templates"]) | set(out["plurals"])
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


def test_arabic_inside_an_english_string_is_shaped_left_to_right(tmp_path: Path) -> None:
    # a language's own name in the English catalog: Roku would draw it unjoined and reversed
    en = {"@k": {"widthChars": 30, "lines": 1}, "k": "Language: العربية", "@p": {"widthChars": 30}, "p": "Plain"}
    root = _catalog(tmp_path, en, {"k": "اللغة: العربية", "p": "عادي"})
    files, problems = shape_strings.build(root)
    assert problems == []
    out = json.loads(files[root / "locale" / "en_US" / "strings.json"])
    assert out["strings"]["k"] == "Language: " + visual.visual_text("العربية")
    assert out["strings"]["k"] == visual.visual_text("Language: العربية", 0, "ltr")
    assert out["strings"]["p"] == "Plain"  # English alone is left to the Label


def test_english_statics_equal_the_reference() -> None:
    en = json.loads((ROKU / "i18n" / "en.json").read_text(encoding="utf-8"))
    out = json.loads((ROKU / "locale" / "en_US" / "strings.json").read_text(encoding="utf-8"))
    assert out["rtl"] is False
    for key, text in out["strings"].items():
        if shape_strings.has_rtl(en[key]):
            meta = en["@" + key]
            assert text.split("\n") == visual.visual_lines(en[key], meta["widthChars"], meta["lines"], "ltr"), key
        else:
            assert text == en[key], key
    assert out["strings"]["account.language.ar"] == visual.visual_text("العربية")


def test_a_plural_keeps_each_languages_forms_in_logical_order(tmp_path: Path) -> None:
    en = {"@m": {"widthChars": 16, "lines": 1}, "m": {"one": "%1$d minute", "other": "%1$d minutes"}}
    ar_forms = {
        "zero": "%1$d دقيقة",
        "one": "دقيقة واحدة",
        "two": "دقيقتان",
        "few": "%1$d دقائق",
        "many": "%1$d دقيقة",
        "other": "%1$d دقيقة",
    }
    root = _catalog(tmp_path, en, {"m": ar_forms})
    files, problems = shape_strings.build(root)
    assert problems == []
    ar = json.loads(files[root / "locale" / "ar_SA" / "strings.json"])
    assert ar["plurals"]["m"] == {"forms": ar_forms, "widthChars": 16, "lines": 1}
    assert "m" not in ar["strings"] and "m" not in ar["templates"]
    out = json.loads(files[root / "locale" / "en_US" / "strings.json"])
    assert out["plurals"]["m"]["forms"] == en["m"]


def test_a_plural_form_too_long_for_its_label_fails_naming_the_form(tmp_path: Path) -> None:
    en = {"@m": {"widthChars": 10, "lines": 1}, "m": {"one": "%1$d minute", "other": "%1$d minutes"}}
    root = _catalog(tmp_path, en, {"m": {"one": "دقيقة", "other": "%1$d دقيقة"}})
    _, problems = shape_strings.build(root)
    # "000 minutes" is 11 characters: the number at its widest
    assert any("'m#other' needs 2 lines of 10" in p for p in problems), problems


def test_a_value_string_is_kept_logical_for_the_line_it_goes_into(tmp_path: Path) -> None:
    en = {"@u": {"widthChars": 20, "lines": 1, "value": True}, "u": "less than a minute"}
    root = _catalog(tmp_path, en, {"u": "أقل من دقيقة"})
    files, problems = shape_strings.build(root)
    assert problems == []
    ar = json.loads(files[root / "locale" / "ar_SA" / "strings.json"])
    assert ar["templates"]["u"] == {"text": "أقل من دقيقة", "widthChars": 20, "lines": 1}
    assert "u" not in ar["strings"]
