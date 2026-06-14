"""Story 26.1 — title parser unit tests.

Covers every test case + edge case called out in
``specs/epics/26-content-intelligence/story-26-01-title-parser.md`` plus a
checked-in fixture-corpus regression guard.
"""

from __future__ import annotations

import json
from datetime import date
from pathlib import Path

import pytest

from maktaba_pipeline.classify.title_parser import PARSER_VERSION, parse

pytestmark = pytest.mark.unit

_CORPUS = Path(__file__).resolve().parents[1] / "fixtures" / "filenames.jsonl"


def test_scene_episode() -> None:
    p = parse("Show.Name.S01E02.720p.x265-GRP.mkv")
    assert p.show_name == "Show Name"
    assert (p.season, p.episode) == (1, 2)
    assert p.resolution == "720p"
    assert p.video_codec == "x265"
    assert p.release_group == "GRP"
    assert p.kind == "episode"
    assert p.confidence >= 0.9


def test_alt_episode_with_title() -> None:
    p = parse("Show - 01x02 - The Title.mp4")
    assert (p.season, p.episode) == (1, 2)
    assert p.episode_title == "The Title"
    assert p.kind == "episode"


def test_movie_paren_year() -> None:
    p = parse("Movie Name (2024).mkv")
    assert p.year == 2024
    assert p.kind == "movie"
    assert p.title == "Movie Name"
    assert p.confidence >= 0.75


def test_movie_dotted_year() -> None:
    p = parse("Movie.Name.2024.1080p.mkv")
    assert p.year == 2024
    assert p.kind == "movie"
    assert p.resolution == "1080p"
    assert p.title == "Movie Name"


def test_date_based() -> None:
    p = parse("Show.2024.03.14.Topic.mp4")
    assert p.airdate == date(2024, 3, 14)
    assert p.kind == "episode"
    assert p.show_name == "Show"


def test_multi_episode_range() -> None:
    p = parse("Show.S01E01-E03.mkv")
    assert p.episode == 1
    assert p.episode_end == 3


def test_multi_episode_consecutive() -> None:
    p = parse("Show.S01E01E02.mkv")
    assert p.episode == 1
    assert p.episode_end == 2


def test_arabic_episode_arabic_digits() -> None:
    p = parse("اسم - الحلقة ٥.mp4")
    assert p.episode == 5
    assert p.show_name == "اسم"
    assert p.kind == "episode"


def test_arabic_season_episode() -> None:
    p = parse("برنامج الموسم 1 الحلقة 12.mkv")
    assert p.season == 1
    assert p.episode == 12


def test_arabic_film() -> None:
    p = parse("اسم الفيلم (2019).mkv")
    assert p.year == 2019
    assert p.kind == "movie"
    assert "اسم الفيلم" in p.title


def test_dir_fallback() -> None:
    p = parse("02.mkv", dirnames=["Show Name", "Season 01"])
    assert p.show_name == "Show Name"
    assert p.season == 1
    assert p.episode == 2


def test_unparseable_is_low_confidence() -> None:
    p = parse("IMG_4471.mkv")
    assert p.kind == "unknown"
    assert p.confidence <= 0.25
    assert p.title  # present, no raise


def test_determinism() -> None:
    name = "Show.Name.S01E02.720p.x265-GRP.mkv"
    assert parse(name) == parse(name)


def test_no_metadata_bleed_into_title() -> None:
    p = parse("Show.S01E02.1080p.BluRay.x264-GRP.mkv")
    assert p.show_name == "Show"
    for residue in ("1080p", "BluRay", "x264", "GRP"):
        assert residue.lower() not in (p.show_name or "").lower()


# --- edge cases ---------------------------------------------------------


def test_resolution_not_year() -> None:
    p = parse("Movie.Name.2160p.mkv")
    assert p.resolution == "2160p"
    assert p.year is None


def test_show_name_is_a_number() -> None:
    p = parse("1923.S01E01.mkv")
    assert p.show_name == "1923"
    assert p.year is None
    assert p.kind == "episode"


def test_mixed_script_name() -> None:
    p = parse("Naruto الحلقة 12.mkv")
    assert p.show_name == "Naruto"
    assert p.episode == 12


def test_garbage_release_tags_stripped() -> None:
    p = parse("[www.site.com]Show.S01E02.720p-GRP.mkv")
    assert "www" not in (p.show_name or "").lower()
    assert p.show_name == "Show"


def test_sample_flagged() -> None:
    p = parse("Show.S01E02.sample.mkv")
    assert p.metadata.get("is_sample") is True
    assert (p.season, p.episode) == (1, 2)


def test_absurdly_long_name_truncated() -> None:
    # Marker is early; the 5000-char tail is truncated for processing but
    # the parser still returns (within budget) and recovers the S/E.
    p = parse("Show.S01E02." + "x" * 5000 + ".mkv")
    assert p.episode == 2  # still extracts; returns without raising


def test_parser_version_stamped() -> None:
    assert parse("anything.mkv").parser_version == PARSER_VERSION


def test_never_raises_on_garbage() -> None:
    for junk in ("", ".", "...", "-", "()", "[]", "S.E", "él.mkv"):
        parse(junk)  # must not raise


# --- corpus regression --------------------------------------------------


def test_corpus_regression() -> None:
    """Run the parser over the checked-in corpus and assert ≥95 %
    exact-field accuracy. The corpus is the regression guard for parser
    changes (AC ``test_corpus_regression``)."""
    assert _CORPUS.exists(), f"missing corpus: {_CORPUS}"
    records = [json.loads(line) for line in _CORPUS.read_text("utf-8").splitlines() if line.strip()]
    assert len(records) >= 300, f"corpus too small: {len(records)}"

    total = 0
    correct = 0
    failures: list[str] = []
    for rec in records:
        got = parse(rec["filename"])
        for field_name, expected in rec["expect"].items():
            total += 1
            actual = getattr(got, field_name)
            if field_name == "airdate" and actual is not None:
                actual = actual.isoformat()
            if actual == expected:
                correct += 1
            elif len(failures) < 20:
                failures.append(f"{rec['filename']}: {field_name}={actual!r} != {expected!r}")

    accuracy = correct / total if total else 0.0
    assert accuracy >= 0.95, f"accuracy {accuracy:.3f} ({correct}/{total})\n" + "\n".join(failures)
