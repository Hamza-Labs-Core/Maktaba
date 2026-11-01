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
    assert len(db.audio_tracks) == 1  # ON CONFLICT updated the row, no duplicate


def test_commit_probe_skips_extract_when_already_transcribed() -> None:
    """An imported (already transcribed) video is probed for streaming only."""
    db = FakeAudioDB()
    vid = db.add_video()
    db.seed_transcript(video_id=vid)
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

    assert db.videos[vid].state == "probed"
    assert all(j.stage != "extract" for j in db.processing_jobs.values())
    assert len(db.audio_tracks) == 1  # media facts are still recorded


def test_commit_probe_inactive_transcript_still_extracts() -> None:
    """Only an active transcript counts: a superseded one does not stop extract."""
    db = FakeAudioDB()
    vid = db.add_video()
    db.seed_transcript(video_id=vid, is_active=False)
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

    assert any(j.stage == "extract" for j in db.processing_jobs.values())


def test_commit_probe_skips_extract_when_a_transcribe_job_is_queued() -> None:
    """The engine's transcribe decodes its own audio: extract would only cache a WAV."""
    from tests.audio._fake_audio_db import _ProcessingJobRow

    db = FakeAudioDB()
    vid = db.add_video()
    db.processing_jobs[9999] = _ProcessingJobRow(id=9999, video_id=vid, stage="transcribe")
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

    assert db.videos[vid].state == "probed"
    assert all(j.stage != "extract" for j in db.processing_jobs.values())


# --- the probe is the authority on a track's facts ----------------------


def _opus_stereo(index: int = 0, *, default: bool = True) -> AudioTrack:
    return AudioTrack(
        index=index,
        codec="opus",
        channels=2,
        sample_rate=48000,
        language="ara",
        title="Arabic",
        is_default=default,
        disposition={"default": 1 if default else 0},
    )


def test_commit_probe_fills_the_bare_row_transcribe_wrote_first() -> None:
    """The order behind the silent streams: transcribe recorded track 0 with no codec
    before the probe ran; the probe's facts still land on that row (same id, so a
    transcript naming it keeps it)."""
    db = FakeAudioDB()
    vid = db.add_video()
    bare = db.add_bare_audio_track(video_id=vid)
    db.seed_transcript(video_id=vid, audio_track_id=bare)

    asyncio.run(commit_probe(db, video_id=vid, result=_make_result([_opus_stereo()])))

    assert list(db.audio_tracks) == [bare]
    row = db.audio_tracks[bare]
    assert (row.codec, row.channels, row.sample_rate) == ("opus", 2, 48000)
    assert (row.language, row.title, row.is_default) == ("ara", "Arabic", True)
    assert row.disposition == '{"default": 1}'


def test_commit_probe_default_is_the_probe_s_choice_across_tracks() -> None:
    """Transcribe marked track 0 the default (the video had none); the file's default
    is track 1: after the probe exactly track 1 is the default."""
    db = FakeAudioDB()
    vid = db.add_video()
    db.add_bare_audio_track(video_id=vid, is_default=True)
    tracks = [_opus_stereo(0, default=False), _opus_stereo(1, default=True)]

    asyncio.run(commit_probe(db, video_id=vid, result=_make_result(tracks)))

    got = sorted((r.track_index, r.is_default) for r in db.audio_tracks.values())
    assert got == [(0, False), (1, True)]


def test_commit_probe_rows_past_the_file_s_streams_lose_the_default() -> None:
    """A file replaced by one with fewer audio streams: the old row stays (a transcript
    may name it) but is no longer the video's default."""
    db = FakeAudioDB()
    vid = db.add_video()
    db.add_bare_audio_track(video_id=vid, track_index=0, is_default=False)
    db.add_bare_audio_track(video_id=vid, track_index=1, is_default=True)

    asyncio.run(commit_probe(db, video_id=vid, result=_make_result([_opus_stereo(0)])))

    got = sorted((r.track_index, r.codec, r.is_default) for r in db.audio_tracks.values())
    assert got == [(0, "opus", True), (1, None, False)]


def test_commit_probe_corrects_convert_mp4_s_codec() -> None:
    """convert_mp4 sets codec 'aac' and asks for a probe: the probe's facts stand."""
    db = FakeAudioDB()
    vid = db.add_video(state="probed")
    tid = db.add_bare_audio_track(video_id=vid)
    db.audio_tracks[tid].codec = "aac"
    aac = AudioTrack(
        index=0, codec="aac", channels=6, sample_rate=44100, language="eng", title=None,
        is_default=True, disposition={"default": 1},
    )  # fmt: skip

    asyncio.run(commit_probe(db, video_id=vid, result=_make_result([aac])))

    row = db.audio_tracks[tid]
    assert (row.codec, row.channels, row.sample_rate, row.language) == ("aac", 6, 44100, "eng")
