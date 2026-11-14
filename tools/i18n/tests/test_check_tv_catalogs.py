"""Story 58.15 T01: the TV catalogs' check fails on a missing key or a placeholder mismatch,
naming the entry, and on Latin words in Arabic that are neither product names nor code."""

from __future__ import annotations

import json
import sys
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

import check_tv_catalogs as ctc  # noqa: E402


def write(root: Path, rel: str, en: object, ar: object) -> None:
    d = root / rel
    d.mkdir(parents=True)
    (d / "en.json").write_text(json.dumps(en, ensure_ascii=False), encoding="utf-8")
    (d / "ar.json").write_text(json.dumps(ar, ensure_ascii=False), encoding="utf-8")


def run(root: Path, *dirs: str) -> list[str]:
    problems, _ = ctc.check(dirs or ctc.CATALOGS, root)
    return problems


def test_a_matching_catalog_passes(tmp_path: Path) -> None:
    write(
        tmp_path,
        "apps/tv/web/i18n",
        {"home.title": "Home", "player.time": "{elapsed} of {total}", "code.hint": "Open maktaba.tv/code"},
        {"home.title": "الرئيسية", "player.time": "{elapsed} من {total}", "code.hint": "افتح maktaba.tv/code"},
    )
    assert run(tmp_path) == []


def test_a_key_missing_in_arabic_fails_naming_the_key(tmp_path: Path) -> None:
    write(
        tmp_path, "apps/tv/web/i18n", {"home.title": "Home", "home.empty": "Nothing here"}, {"home.title": "الرئيسية"}
    )
    problems = run(tmp_path)
    assert problems == ["apps/tv/web/i18n: 'home.empty' is in en.json and missing in ar.json"]


def test_a_key_missing_in_english_fails_naming_the_key(tmp_path: Path) -> None:
    write(tmp_path, "apps/tv/roku/i18n", {"a": "A"}, {"a": "أ", "b": "ب"})
    assert run(tmp_path) == ["apps/tv/roku/i18n: 'b' is in ar.json and missing in en.json"]


def test_an_empty_arabic_string_fails(tmp_path: Path) -> None:
    write(tmp_path, "apps/tv/web/i18n", {"a": "A"}, {"a": "  "})
    assert run(tmp_path) == ["apps/tv/web/i18n/ar.json: 'a' is empty"]


@pytest.mark.parametrize(
    ("en", "ar"),
    [
        ("Episode %1$s", "الحلقة"),  # missing in Arabic
        ("Episode", "الحلقة %1$s"),  # missing in English
        ("{count} items", "{n} عناصر"),  # renamed
        ("{name} and {name}", "{name}"),  # used twice in one, once in the other
        ("%s of %d", "%s من %s"),  # a different kind
    ],
)
def test_placeholders_that_differ_fail_naming_the_key(tmp_path: Path, en: str, ar: str) -> None:
    write(tmp_path, "web/packages/castreceiver/i18n", {"k": en}, {"k": ar})
    problems = run(tmp_path)
    assert len(problems) == 1
    assert problems[0].startswith("web/packages/castreceiver/i18n: 'k' placeholders differ")


def test_placeholders_in_another_order_or_spacing_pass(tmp_path: Path) -> None:
    write(tmp_path, "apps/tv/web/i18n", {"k": "{a} then {{ b }}"}, {"k": "{{b}} بعد {a}"})
    assert run(tmp_path) == []


def test_a_latin_word_in_arabic_fails(tmp_path: Path) -> None:
    write(tmp_path, "apps/tv/web/i18n", {"k": "Resume watching"}, {"k": "تابع Resume المشاهدة"})
    assert run(tmp_path) == ["apps/tv/web/i18n/ar.json: 'k' has Latin words that are not product names or code: Resume"]


@pytest.mark.parametrize(
    "ar",
    [
        "شاهد على Samsung و LG و Roku",
        "أرسل إلى Chromecast من Maktaba",
        "يدعم 4K و HDR و Dolby Vision",
        "الحلقة S2 E5 الساعة 12:34",
        "افتح `maktaba://code` أو https://maktaba.tv/code",
        "اكتب الرمز {code} على Fire TV",
        "شبكة Wi-Fi ورمز PIN",
        "عبر Maktaba Cloud",
    ],
)
def test_product_names_and_code_in_arabic_pass(tmp_path: Path, ar: str) -> None:
    write(tmp_path, "apps/tv/web/i18n", {"k": ar.replace("الحلقة", "Episode")}, {"k": ar})
    assert run(tmp_path) == []


def test_metadata_keys_are_skipped_and_nested_keys_are_dotted(tmp_path: Path) -> None:
    write(
        tmp_path,
        "apps/tv/roku/i18n",
        {"home": {"title": "Home"}, "@home.title": {"widthChars": 24}},
        {"home": {"title": "الرئيسية"}},
    )
    assert run(tmp_path) == []
    write(tmp_path, "apps/tv/web/i18n", {"home": {"title": "Home", "more": "More"}}, {"home": {"title": "الرئيسية"}})
    assert run(tmp_path, "apps/tv/web/i18n") == ["apps/tv/web/i18n: 'home.more' is in en.json and missing in ar.json"]


def test_a_catalog_not_there_yet_is_skipped(tmp_path: Path) -> None:
    problems, notes = ctc.check(ctc.CATALOGS, tmp_path)
    assert problems == []
    assert notes == [f"{d}: not there yet, skipped" for d in ctc.CATALOGS]


def test_a_catalog_without_its_arabic_file_fails(tmp_path: Path) -> None:
    d = tmp_path / "apps/tv/web/i18n"
    d.mkdir(parents=True)
    (d / "en.json").write_text('{"a": "A"}', encoding="utf-8")
    assert run(tmp_path) == ["apps/tv/web/i18n/ar.json: missing (a catalog has en.json and ar.json)"]


def test_broken_json_and_non_strings_fail(tmp_path: Path) -> None:
    write(tmp_path, "apps/tv/web/i18n", {"a": "A", "n": 3}, {"a": "أ", "n": 3})
    assert run(tmp_path) == [
        "apps/tv/web/i18n/en.json: 'n' is not a string",
        "apps/tv/web/i18n/ar.json: 'n' is not a string",
    ]
    (tmp_path / "apps/tv/web/i18n/ar.json").write_text("{", encoding="utf-8")
    assert run(tmp_path)[0].startswith("apps/tv/web/i18n/ar.json: not readable JSON")


def test_main_exits_non_zero_and_names_the_key(tmp_path: Path, capsys: pytest.CaptureFixture[str]) -> None:
    write(tmp_path, "apps/tv/web/i18n", {"home.new": "New"}, {})
    assert ctc.main(["--root", str(tmp_path)]) == 1
    err = capsys.readouterr().err
    assert "::error::apps/tv/web/i18n: 'home.new' is in en.json and missing in ar.json" in err
    write(tmp_path, "ok", {"a": "A"}, {"a": "أ"})
    assert ctc.main(["--root", str(tmp_path), "ok"]) == 0


def test_the_repositorys_tv_catalogs_pass() -> None:
    problems, _ = ctc.check(ctc.CATALOGS)
    assert problems == []


AR_MINUTES = {
    "zero": "%1$d دقيقة",
    "one": "دقيقة واحدة",
    "two": "دقيقتان",
    "few": "%1$d دقائق",
    "many": "%1$d دقيقة",
    "other": "%1$d دقيقة",
}
EN_MINUTES = {"one": "%1$d minute", "other": "%1$d minutes"}


def test_a_plural_with_each_languages_forms_passes(tmp_path: Path) -> None:
    write(tmp_path, "apps/tv/roku/i18n", {"t.minutes": EN_MINUTES}, {"t.minutes": AR_MINUTES})
    assert run(tmp_path, "apps/tv/roku/i18n") == []


def test_an_arabic_plural_missing_a_form_fails_naming_it(tmp_path: Path) -> None:
    ar = {k: v for k, v in AR_MINUTES.items() if k != "few"}
    write(tmp_path, "apps/tv/roku/i18n", {"t.minutes": EN_MINUTES}, {"t.minutes": ar})
    problems = run(tmp_path, "apps/tv/roku/i18n")
    assert len(problems) == 1
    assert "'t.minutes' has no 'few' form" in problems[0] and "ar.json" in problems[0]


def test_an_english_plural_with_a_form_english_does_not_use_fails(tmp_path: Path) -> None:
    write(tmp_path, "apps/tv/roku/i18n", {"t.minutes": {**EN_MINUTES, "two": "%1$d minutes"}}, {"t.minutes": AR_MINUTES})
    assert run(tmp_path, "apps/tv/roku/i18n") == [
        "apps/tv/roku/i18n/en.json: 't.minutes' has a 'two' form, which en does not use"
    ]


def test_a_plural_in_one_language_only_fails(tmp_path: Path) -> None:
    write(tmp_path, "apps/tv/roku/i18n", {"t.minutes": EN_MINUTES}, {"t.minutes": "%1$d دقيقة"})
    assert run(tmp_path, "apps/tv/roku/i18n") == [
        "apps/tv/roku/i18n: 't.minutes' is a plural in en.json and not in ar.json"
    ]


def test_plural_placeholders_compare_other_and_bound_each_form(tmp_path: Path) -> None:
    ar = {**AR_MINUTES, "other": "دقائق", "few": "%1$d %2$s دقائق"}
    write(tmp_path, "apps/tv/roku/i18n", {"t.minutes": EN_MINUTES}, {"t.minutes": ar})
    problems = run(tmp_path, "apps/tv/roku/i18n")
    assert any("'t.minutes' placeholders differ" in p for p in problems)
    assert any("form 'few' has placeholders its 'other' lacks" in p for p in problems)


def test_latin_words_in_an_arabic_plural_form_fail(tmp_path: Path) -> None:
    write(tmp_path, "apps/tv/roku/i18n", {"t.minutes": EN_MINUTES}, {"t.minutes": {**AR_MINUTES, "two": "two دقيقة"}})
    problems = run(tmp_path, "apps/tv/roku/i18n")
    assert problems == [
        "apps/tv/roku/i18n/ar.json: 't.minutes' form 'two' has Latin words that are not product names or code: two"
    ]


def test_a_nested_object_that_is_not_a_plural_is_still_dotted_keys(tmp_path: Path) -> None:
    write(tmp_path, "apps/tv/web/i18n", {"home": {"one": "One"}}, {"home": {"one": "واحد"}})
    assert run(tmp_path, "apps/tv/web/i18n") == []
