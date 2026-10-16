"""Arabic folding, yt-dlp filename sanitising, sidecar parsing, dHash."""

from __future__ import annotations

import json
from datetime import date

import pytest

from maktaba_pipeline.discovery.ytdlp_sidecars import (
    PlaylistInfo,
    VideoInfo,
    is_real_playlist,
    parse_archive,
    parse_info_json,
)
from maktaba_pipeline.text.arabic import fold, light, ytdlp_filename
from maktaba_pipeline.thumbnail.dhash import dhash_from_gray, hamming, similarity

pytestmark = pytest.mark.unit

# yt-dlp takes ~0.4 s to import: do it at collection, not inside the timed unit test
try:
    from yt_dlp import utils as _yt_utils
except ImportError:  # optional extra
    _yt_utils = None


def test_fold_variants_equal() -> None:
    assert fold("فَائِدَة لُغَوِية") == fold("فـائـدة لغوية") == fold("فائدة  لغوية")
    assert fold("#سلسلة_الحبيب ｜ زاهر") == "سلسله الحبيب زاهر"
    assert fold("الحلقة ٢٥") == "الحلقه 25"
    # light keeps letter variants: prefix matching must not fold hamza
    assert light("فأين") != light("فائ")


@pytest.mark.parametrize(
    ("title", "stem"),
    [
        (
            "الحبيب | بل الرفيق الأعلى | الحلقة الأخيرة",
            "الحبيب ｜ بل الرفيق الأعلى ｜ الحلقة الأخيرة",
        ),
        ("هل تتمنى؟ / حقق حلمك: الآن?", "هل تتمنى؟ ⧸ حقق حلمك： الآن？"),
        ('نشيد "هزني الشوق"', "نشيد ＂هزني الشوق＂"),
        ("-2022-لكل من مات له محبوب", "-2022-لكل من مات له محبوب"),
    ],
)
def test_ytdlp_filename(title: str, stem: str) -> None:
    assert ytdlp_filename(title) == stem


def test_ytdlp_filename_matches_yt_dlp_when_installed() -> None:
    if _yt_utils is None:
        pytest.skip("yt-dlp not installed")
    yt_utils = _yt_utils
    for t in ["الحبيب | بل الرفيق", 'a/b:c*d?e<f>g|h"i', "  padded  ", "-dash"]:
        assert ytdlp_filename(t) == yt_utils.sanitize_filename(t, restricted=False)


def test_parse_video_info_json() -> None:
    v = parse_info_json(
        json.dumps(
            {
                "id": "JekM9-soyJQ", "title": "اعتزل", "upload_date": "20220726", "duration": 55,
                "playlist_title": "قصص الأنبياء رمضان 2024", "playlist_id": "PLx",
                "playlist_index": 8,
            }
        )
    )  # fmt: skip
    assert isinstance(v, VideoInfo)
    assert v.upload_date == date(2022, 7, 26)
    assert (v.playlist_id, v.playlist_index) == ("PLx", 8)


def test_parse_playlist_info_json() -> None:
    p = parse_info_json(json.dumps({"_type": "playlist", "id": "UCabc", "title": "X - Videos"}))
    assert isinstance(p, PlaylistInfo)
    assert not p.is_real
    assert (
        is_real_playlist("PLxpAkjl")
        and is_real_playlist("OLAK5uy")
        and not is_real_playlist("UUabc")
    )
    assert parse_info_json("not json") is None


def test_parse_archive() -> None:
    assert parse_archive("youtube a\nyoutube b\nyoutube a\nbad line\n") == ["a", "b"]


def test_dhash() -> None:
    flat = bytes([128] * 72)
    ramp = bytes(x * 10 for _ in range(8) for x in range(9, 0, -1))
    assert dhash_from_gray(flat) == 0
    assert dhash_from_gray(ramp) == (1 << 64) - 1
    assert hamming(0, (1 << 64) - 1) == 64
    assert similarity(5, 5) == 1.0
    with pytest.raises(ValueError):
        dhash_from_gray(b"short")
