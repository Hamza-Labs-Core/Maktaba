"""Story 2.1 — :mod:`maktaba_pipeline.audio.probe` tests."""

from __future__ import annotations

import asyncio

from maktaba_pipeline.audio.probe import (
    AudioTrack,
    MediaInfo,
    ProbeResult,
    commit_probe,
    parse_ffprobe_json,
)

from ._fake_audio_db import FakeAudioDB

# --- parse_ffprobe_json ----------------------------------------------------


def test_parse_writes_media_info_fields() -> None:
    payload = {
        "format": {"format_name": "mov,mp4,m4a,3gp", "bit_rate": "5000000"},
        "streams": [
            {
                "codec_type": "video",
                "codec_name": "h264",
                "width": 1920,
                "height": 1080,
                "avg_frame_rate": "30000/1001",
            },
            {
                "codec_type": "subtitle",
                "codec_name": "subrip",
            },
        ],
    }
    result = parse_ffprobe_json(payload)
    assert result.media.container == "mov,mp4,m4a,3gp"
    assert result.media.video_codec == "h264"
    assert result.media.width == 1920
    assert result.media.height == 1080
    assert result.media.bitrate_kbps == 5000
    assert result.media.has_subtitles is True
    assert 29.5 < (result.media.fps or 0) < 30.5


def test_parse_one_audio_row_per_track_with_default_flag() -> None:
    payload = {
        "format": {"format_name": "matroska,webm"},
        "streams": [
            {
                "codec_type": "audio",
                "codec_name": "aac",
                "channels": 2,
                "sample_rate": "48000",
                "tags": {"language": "ara", "title": "Arabic"},
                "disposition": {"default": 1, "commentary": 0},
            },
            {
                "codec_type": "audio",
                "codec_name": "aac",
                "channels": 2,
                "sample_rate": "48000",
                "tags": {"language": "eng"},
                "disposition": {"default": 0},
            },
            {
                "codec_type": "audio",
                "codec_name": "aac",
                "channels": 2,
                "sample_rate": "48000",
                "tags": {"language": "fra"},
                "disposition": {},
            },
        ],
    }
    result = parse_ffprobe_json(payload)
    assert [t.language for t in result.audio] == ["ara", "eng", "fra"]
    assert [t.is_default for t in result.audio] == [True, False, False]
    assert [t.index for t in result.audio] == [0, 1, 2]


def test_parse_undefined_language_becomes_und() -> None:
    payload = {
        "format": {},
        "streams": [
            {
                "codec_type": "audio",
                "codec_name": "pcm_s16le",
                "channels": 1,
                "sample_rate": "16000",
                "tags": {},
                "disposition": {},
            },
        ],
    }
    result = parse_ffprobe_json(payload)
    assert result.audio[0].language == "und"


def test_parse_audioless_video_returns_empty_audio_list() -> None:
    payload = {
        "format": {"format_name": "mov,mp4"},
        "streams": [{"codec_type": "video", "codec_name": "h264"}],
    }
    result = parse_ffprobe_json(payload)
    assert result.audio == []


# --- commit_probe ---------------------------------------------------------


def _make_result(audio: list[AudioTrack]) -> ProbeResult:
    return ProbeResult(
        media=MediaInfo(
            container="matroska,webm",
            video_codec="h264",
            width=1920,
            height=1080,
            fps=24.0,
            bitrate_kbps=4000,
            has_subtitles=False,
        ),
        audio=audio,
    )


def test_commit_probe_advances_to_probed_and_enqueues_extract() -> None:
    db = FakeAudioDB()
    vid = db.add_video()
    tracks = [
        AudioTrack(
            index=0,
            codec="aac",
            channels=2,
            sample_rate=48000,
            language="ara",
            title=None,
            is_default=True,
            disposition={"default": 1},
        ),
    ]

    asyncio.run(commit_probe(db, video_id=vid, result=_make_result(tracks)))

    assert db.videos[vid].state == "probed"
    assert any(j.stage == "extract" and j.state == "pending" for j in db.processing_jobs.values())
    assert len(db.audio_tracks) == 1
    assert next(iter(db.audio_tracks.values())).language == "ara"


def test_commit_probe_no_audio_advances_to_ready_no_audio_and_skips_extract() -> None:
    db = FakeAudioDB()
    vid = db.add_video()

    asyncio.run(commit_probe(db, video_id=vid, result=_make_result([])))

    assert db.videos[vid].state == "ready_no_audio"
    assert all(j.stage != "extract" for j in db.processing_jobs.values())


def test_commit_probe_idempotent_on_replay() -> None:
    db = FakeAudioDB()
    vid = db.add_video()
    tracks = [
        AudioTrack(
            index=0,
            codec="aac",
            channels=2,
            sample_rate=48000,
            language="ara",
            title=None,
            is_default=True,
        ),
    ]

    asyncio.run(commit_probe(db, video_id=vid, result=_make_result(tracks)))
    # Re-run: late_stage_finish guard should make this a no-op.
    asyncio.run(commit_probe(db, video_id=vid, result=_make_result(tracks)))

    assert db.videos[vid].state == "probed"
    assert len(db.audio_tracks) == 1  # ON CONFLICT DO NOTHING swallowed the duplicate
