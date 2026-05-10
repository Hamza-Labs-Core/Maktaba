"""Embedded subtitle extractor — codec gating tests only.

The actual ffmpeg invocation is exercised by integration tests in
Epic 20; this file only verifies the synchronous gating logic so
unit tests stay self-contained.
"""

from __future__ import annotations

from pathlib import Path

import pytest

from maktaba_pipeline.media.embedded_subs import (
    BITMAP_CODECS,
    TEXT_CODECS,
    EmbeddedExtractor,
    UnsupportedSubtitleCodec,
)


@pytest.mark.unit
def test_bitmap_codecs_are_disjoint_from_text_codecs() -> None:
    assert not (BITMAP_CODECS & TEXT_CODECS)


@pytest.mark.unit
def test_bitmap_codecs_include_pgs_and_dvd() -> None:
    assert "hdmv_pgs_subtitle" in BITMAP_CODECS
    assert "dvd_subtitle" in BITMAP_CODECS


@pytest.mark.unit
def test_text_codecs_include_subrip_and_mov_text() -> None:
    assert "subrip" in TEXT_CODECS
    assert "mov_text" in TEXT_CODECS


@pytest.mark.asyncio
async def test_extract_rejects_bitmap_codec(tmp_path: Path) -> None:
    extractor = EmbeddedExtractor()
    with pytest.raises(UnsupportedSubtitleCodec):
        await extractor.extract(
            video_path=tmp_path / "video.mkv",
            output_dir=tmp_path,
            content_hash="abc",
            stream_index=2,
            codec="hdmv_pgs_subtitle",
            language="en",
        )


@pytest.mark.asyncio
async def test_extract_rejects_unknown_codec(tmp_path: Path) -> None:
    extractor = EmbeddedExtractor()
    with pytest.raises(UnsupportedSubtitleCodec):
        await extractor.extract(
            video_path=tmp_path / "video.mkv",
            output_dir=tmp_path,
            content_hash="abc",
            stream_index=2,
            codec="nonexistent_codec",
            language="en",
        )


@pytest.mark.asyncio
async def test_extract_returns_cached_when_file_exists(tmp_path: Path) -> None:
    # Pre-create the expected output; the extractor must report cached
    # without invoking ffmpeg.
    out = tmp_path / "abc.en.s2.embedded.vtt"
    out.write_bytes(b"WEBVTT\n\n")
    extractor = EmbeddedExtractor(ffmpeg_path="/usr/bin/false")
    result = await extractor.extract(
        video_path=tmp_path / "video.mkv",
        output_dir=tmp_path,
        content_hash="abc",
        stream_index=2,
        codec="subrip",
        language="en",
    )
    assert result.cached is True
    assert result.path == out
    assert result.bytes == out.stat().st_size
