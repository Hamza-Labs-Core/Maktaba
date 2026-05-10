"""Epic 4 — :mod:`maktaba_pipeline.subtitle.extractor` tests."""

from __future__ import annotations

from maktaba_pipeline.subtitle.extractor import (
    EmbeddedSubtitle,
    build_extract_args,
    filter_text_based,
    parse_subtitle_streams,
)
from maktaba_pipeline.subtitle.formats import SubtitleFormat


def test_parse_no_subtitle_streams_returns_empty() -> None:
    payload = {"streams": [{"codec_type": "video"}, {"codec_type": "audio"}]}
    assert parse_subtitle_streams(payload) == []


def test_parse_reads_language_default_forced() -> None:
    payload = {
        "streams": [
            {
                "codec_type": "subtitle",
                "codec_name": "subrip",
                "index": 2,
                "tags": {"language": "ara", "title": "Arabic"},
                "disposition": {"default": 1, "forced": 0},
            },
            {
                "codec_type": "subtitle",
                "codec_name": "mov_text",
                "index": 3,
                "tags": {"language": "eng"},
                "disposition": {"default": 0, "forced": 1},
            },
        ]
    }
    out = parse_subtitle_streams(payload)
    assert [t.language for t in out] == ["ara", "eng"]
    assert [t.is_default for t in out] == [True, False]
    assert [t.is_forced for t in out] == [False, True]
    # Subtitle rank is contiguous starting at 0.
    assert [t.subtitle_index for t in out] == [0, 1]
    # Global stream index comes from the source payload.
    assert [t.stream_index for t in out] == [2, 3]


def test_parse_flags_image_based_codecs() -> None:
    payload = {
        "streams": [
            {
                "codec_type": "subtitle",
                "codec_name": "hdmv_pgs_subtitle",
                "tags": {"language": "eng"},
                "disposition": {},
            },
            {
                "codec_type": "subtitle",
                "codec_name": "subrip",
                "tags": {"language": "eng"},
                "disposition": {},
            },
        ]
    }
    out = parse_subtitle_streams(payload)
    assert out[0].image_based is True
    assert out[1].image_based is False


def test_parse_missing_language_falls_back_to_und() -> None:
    payload = {
        "streams": [
            {
                "codec_type": "subtitle",
                "codec_name": "subrip",
                "tags": {},
                "disposition": {},
            },
        ]
    }
    assert parse_subtitle_streams(payload)[0].language == "und"


def test_filter_text_based_drops_image_streams() -> None:
    tracks = [
        EmbeddedSubtitle(0, 0, "subrip", "ara", None, True, False, False),
        EmbeddedSubtitle(1, 1, "hdmv_pgs_subtitle", "eng", None, False, False, True),
        EmbeddedSubtitle(2, 2, "mov_text", "fra", None, False, False, False),
    ]
    text_only = filter_text_based(tracks)
    assert [t.codec for t in text_only] == ["subrip", "mov_text"]


def test_build_extract_args_uses_subtitle_index_for_map() -> None:
    args = build_extract_args(
        "/tmp/in.mkv",
        subtitle_index=1,
        out_path="/tmp/out.vtt",
        fmt=SubtitleFormat.VTT,
    )
    assert "-map" in args
    map_idx = args.index("-map")
    assert args[map_idx + 1] == "0:s:1"
    # WebVTT target → webvtt codec.
    assert "webvtt" in args


def test_build_extract_args_srt_uses_srt_codec() -> None:
    args = build_extract_args(
        "/tmp/in.mkv",
        subtitle_index=0,
        out_path="/tmp/out.srt",
        fmt=SubtitleFormat.SRT,
    )
    assert "srt" in args
    assert "webvtt" not in args
