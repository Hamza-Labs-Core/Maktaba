"""Story 9.5 — built-in + user ignore globs and extension filtering."""

from __future__ import annotations

import pytest

from maktaba_pipeline.library_mgmt.ignore import (
    BUILT_IN_IGNORE_GLOBS,
    DEFAULT_SUPPORTED_EXTS,
    IgnoreFilter,
    is_supported_extension,
)


@pytest.mark.unit
def test_built_in_globs_match_partials_and_hidden() -> None:
    f = IgnoreFilter(case_insensitive=False)
    assert f.is_ignored("/library/.maktaba/cache/x.json")
    assert f.is_ignored("/library/movie.mp4.part")
    assert f.is_ignored("/library/movie.mp4.crdownload")
    assert f.is_ignored("/library/.DS_Store")
    assert f.is_ignored("/library/Thumbs.db")
    assert f.is_ignored("/library/.hidden")
    assert not f.is_ignored("/library/movie.mp4")


@pytest.mark.unit
def test_user_globs_extend_built_ins() -> None:
    f = IgnoreFilter(user_globs=("**/raw/**", "**/*.tmp.mp4"), case_insensitive=False)
    assert f.is_ignored("/library/raw/source.mp4")
    assert f.is_ignored("/library/clip.tmp.mp4")
    assert not f.is_ignored("/library/clip.mp4")


@pytest.mark.unit
def test_supported_extension_check_is_case_insensitive() -> None:
    assert is_supported_extension("Foo.MP4")
    assert is_supported_extension("bar.mkv")
    assert not is_supported_extension("note.txt")


@pytest.mark.unit
def test_is_acceptable_combines_ignore_and_extension() -> None:
    f = IgnoreFilter(case_insensitive=False)
    assert f.is_acceptable("/library/movie.mp4")
    assert not f.is_acceptable("/library/movie.txt")  # bad extension
    assert not f.is_acceptable("/library/.maktaba/x.mp4")  # ignored dir


@pytest.mark.unit
def test_default_supported_exts_match_architecture() -> None:
    # Smoke check of a few critical entries from architecture §3.1.
    for ext in (".mp4", ".mkv", ".webm", ".m4v"):
        assert ext in DEFAULT_SUPPORTED_EXTS


@pytest.mark.unit
def test_built_in_globs_are_documented_constants() -> None:
    # BUILT_IN_IGNORE_GLOBS is what the AC-1 test fixture references; pin
    # the count to catch accidental additions.
    assert len(BUILT_IN_IGNORE_GLOBS) == 6
