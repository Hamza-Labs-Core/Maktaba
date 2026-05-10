"""Filename parsing for external sidecars."""

from __future__ import annotations

from pathlib import Path

import pytest

from maktaba_pipeline.media.subtitles.filename import (
    compile_subtitle_regex,
    normalize_lang,
    parse_filename,
)


@pytest.mark.unit
def test_parse_plain_lang_srt() -> None:
    parsed = parse_filename(Path("Lecture.ar.srt"), "Lecture")
    assert parsed is not None
    assert parsed.lang == "ar"
    assert parsed.ext == "srt"
    assert parsed.flag is None


@pytest.mark.unit
def test_parse_with_flag() -> None:
    parsed = parse_filename(Path("Lecture.en.forced.vtt"), "Lecture")
    assert parsed is not None
    assert parsed.lang == "en"
    assert parsed.flag == "forced"
    assert parsed.ext == "vtt"


@pytest.mark.unit
def test_parse_lowercase_ext() -> None:
    parsed = parse_filename(Path("Lecture.AR.SRT"), "Lecture")
    assert parsed is not None
    assert parsed.lang == "ar"
    assert parsed.ext == "srt"


@pytest.mark.unit
def test_parse_no_lang_just_ext() -> None:
    parsed = parse_filename(Path("Lecture.srt"), "Lecture")
    assert parsed is not None
    assert parsed.lang == "und"  # no lang tag → undetermined
    assert parsed.ext == "srt"


@pytest.mark.unit
def test_parse_mismatched_stem_returns_none() -> None:
    assert parse_filename(Path("Other.ar.srt"), "Lecture") is None


@pytest.mark.unit
def test_parse_non_subtitle_ext_returns_none() -> None:
    assert parse_filename(Path("Lecture.ar.txt"), "Lecture") is None


@pytest.mark.unit
def test_normalize_known_lang() -> None:
    assert normalize_lang("ar") == ("ar", None)
    assert normalize_lang("EN") == ("en", None)


@pytest.mark.unit
def test_normalize_unknown_lang_returns_und_with_raw() -> None:
    assert normalize_lang("xyz") == ("und", "xyz")


@pytest.mark.unit
def test_normalize_none() -> None:
    assert normalize_lang(None) == ("und", None)


@pytest.mark.unit
def test_compile_subtitle_regex_escapes_special_chars() -> None:
    # Stems can contain dots and brackets; ensure they are escaped.
    matcher = compile_subtitle_regex("Talk.S01[E02]")
    assert matcher.match("Talk.S01[E02].en.srt") is not None
    assert matcher.match("TalkXS01XE02X.en.srt") is None


@pytest.mark.unit
def test_parse_unknown_lang_carries_raw_in_field() -> None:
    parsed = parse_filename(Path("Lecture.xyz.srt"), "Lecture")
    assert parsed is not None
    assert parsed.lang == "und"
    assert parsed.raw_lang == "xyz"
