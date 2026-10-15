"""Audio (intro / end song) and frame signatures as "same show" evidence."""

from __future__ import annotations

import random
from datetime import date, timedelta

import pytest

from maktaba_pipeline.audio.chromaprint import decode, encode, parse_raw, similarity, windows
from maktaba_pipeline.classify.series_detector import PlaylistFacts, VideoFacts, detect
from maktaba_pipeline.thumbnail.dhash import dhashes_from_frames, frame_times

pytestmark = pytest.mark.unit

rng = random.Random(7)
THEME = tuple(rng.getrandbits(32) for _ in range(700))  # the season's intro music


def _noise(n: int = 700) -> tuple[int, ...]:
    return tuple(rng.getrandbits(32) for _ in range(n))


def _shifted(fp: tuple[int, ...], by: int, flip_bits: int = 3) -> tuple[int, ...]:
    """The same music starting ``by`` items later, with a little encoding noise."""
    out = list(_noise(by)) + list(fp[: len(fp) - by])
    return tuple(x ^ (rng.getrandbits(32) & ((1 << flip_bits) - 1)) for x in out)


def test_same_music_matches_across_an_offset_and_noise_does_not() -> None:
    assert similarity(THEME, _shifted(THEME, 40)) > 0.85
    assert 0.4 < similarity(THEME, _noise()) < 0.6
    assert similarity((), THEME) == 0.0


def test_encode_roundtrip_and_windows() -> None:
    assert decode(encode(THEME[:10])) == list(THEME[:10])
    assert parse_raw(bytes([1, 0, 0, 0, 2])) == [1]
    assert [w[0] for w in windows(30 * 60)] == ["head", "tail"]
    assert [w[0] for w in windows(150)] == ["head"]  # too short for a separate ending


def test_frames_drop_flat_images_and_stay_inside_the_video() -> None:
    black = bytes(72)
    ramp = bytes((x * 25) % 256 for x in range(72))
    assert dhashes_from_frames(black + ramp) == dhashes_from_frames(ramp)
    assert frame_times(40) == [15.0, 10.0, 20.0, 30.0]


def _season(n: int) -> list[VideoFacts]:
    return [
        VideoFacts(
            f"e{i}",
            f"برنامج النور | الحلقة {i}",
            1500 + i,
            date(2020, 5, 1) + timedelta(i),
            True,
            audio_head=_shifted(THEME, i % 5),
        )  # fmt: skip
        for i in range(1, n + 1)
    ]


def test_unlisted_episode_with_the_season_intro_but_a_bare_title_is_attached() -> None:
    eps = _season(10)
    bare = VideoFacts("x", "لماذا خلقنا؟", 1520, date(2020, 5, 20), True,
                      audio_head=_shifted(THEME, 25))  # fmt: skip
    cut = VideoFacts("y", "لماذا خلقنا؟ ج2", 1520, date(2020, 5, 21), True)  # intro cut, no title
    det = detect(
        eps + [bare, cut],
        [PlaylistFacts("PL1", "برنامج النور", tuple((v.key, i) for i, v in enumerate(eps, 1)))],
    )
    se = det.series[0].seasons[0]
    assert "x" in se.episodes  # the music says it is this show
    assert "y" in det.unassigned  # no music, no title evidence: unknown, not "different"
