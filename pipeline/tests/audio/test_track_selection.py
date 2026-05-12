"""Story 2.2 — track-selection priority order, ties, and exclusions."""

from __future__ import annotations

from maktaba_pipeline.audio.probe import AudioTrack
from maktaba_pipeline.audio.track_selection import LibrarySettings, select_tracks


def _track(
    *,
    index: int = 0,
    language: str = "und",
    title: str | None = None,
    is_default: bool = False,
    channels: int | None = 2,
    disposition: dict[str, object] | None = None,
) -> AudioTrack:
    return AudioTrack(
        index=index,
        codec="aac",
        channels=channels,
        sample_rate=48000,
        language=language,
        title=title,
        is_default=is_default,
        disposition=disposition or {},
    )


def test_prefers_user_language_over_arabic() -> None:
    tracks = [_track(index=0, language="ara"), _track(index=1, language="eng")]
    out = select_tracks(tracks, LibrarySettings(preferred_audio_language="en"))
    assert [t.index for t in out] == [1]


def test_falls_back_to_arabic_when_no_preference() -> None:
    tracks = [_track(index=0, language="eng"), _track(index=1, language="ara")]
    out = select_tracks(tracks, LibrarySettings())
    assert [t.index for t in out] == [1]


def test_uses_default_disposition_when_no_arabic() -> None:
    tracks = [
        _track(index=0, language="eng", is_default=False),
        _track(index=1, language="fra", is_default=True),
    ]
    out = select_tracks(tracks, LibrarySettings())
    assert [t.index for t in out] == [1]


def test_falls_back_to_first_track_when_no_default() -> None:
    tracks = [
        _track(index=0, language="eng"),
        _track(index=1, language="fra"),
    ]
    out = select_tracks(tracks, LibrarySettings())
    assert [t.index for t in out] == [0]


def test_multi_audio_returns_all_non_commentary() -> None:
    tracks = [
        _track(index=0, language="ara"),
        _track(index=1, language="eng"),
        _track(index=2, language="eng", title="Director's commentary"),
    ]
    out = select_tracks(tracks, LibrarySettings(multi_audio=True))
    # commentary removed, the rest kept.
    assert [t.index for t in out] == [0, 1]


def test_excludes_commentary_disposition() -> None:
    tracks = [
        _track(index=0, language="ara", disposition={"commentary": 1}),
        _track(index=1, language="ara"),
    ]
    out = select_tracks(tracks, LibrarySettings())
    assert [t.index for t in out] == [1]


def test_und_track_still_picked_over_no_track() -> None:
    tracks = [_track(index=0, language="und", channels=1)]
    out = select_tracks(tracks, LibrarySettings(preferred_audio_language="ara"))
    assert [t.index for t in out] == [0]


def test_breaks_ties_by_channel_count() -> None:
    tracks = [
        _track(index=0, language="ara", channels=2, is_default=True),
        _track(index=1, language="ara", channels=6),
    ]
    out = select_tracks(tracks, LibrarySettings())
    # 5.1 wins over stereo even though stereo is the default.
    assert [t.index for t in out] == [1]


def test_iso_639_1_preference_normalises_to_639_3() -> None:
    tracks = [_track(index=0, language="eng"), _track(index=1, language="ara")]
    out = select_tracks(tracks, LibrarySettings(preferred_audio_language="ar"))
    assert [t.index for t in out] == [1]


def test_selection_deterministic_under_reorder() -> None:
    a = [_track(index=0, language="eng"), _track(index=1, language="ara")]
    b = list(reversed(a))
    out_a = select_tracks(a, LibrarySettings())
    out_b = select_tracks(b, LibrarySettings())
    assert out_a[0].index == out_b[0].index == 1
