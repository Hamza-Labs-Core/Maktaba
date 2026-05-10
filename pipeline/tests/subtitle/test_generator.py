"""Epic 4 — :mod:`maktaba_pipeline.subtitle.generator` tests."""

from __future__ import annotations

from maktaba_pipeline.subtitle.generator import (
    SubtitleCue,
    generate_srt,
    generate_vtt,
    segments_to_cues,
)


def test_segments_to_cues_drops_empty_text() -> None:
    rows = [
        {"start_sec": 0.0, "end_sec": 1.0, "text": "Hello"},
        {"start_sec": 1.0, "end_sec": 2.0, "text": "   "},  # whitespace → drop
        {"start_sec": 2.0, "end_sec": 3.0, "text": ""},     # empty → drop
        {"start_sec": 3.0, "end_sec": 4.0, "text": "World", "speaker": "Bob"},
    ]
    cues = segments_to_cues(rows)
    assert [c.text for c in cues] == ["Hello", "World"]
    assert cues[1].speaker == "Bob"


def test_generate_srt_indices_are_one_based_contiguous() -> None:
    cues = [
        SubtitleCue(0.0, 1.0, "a"),
        SubtitleCue(1.0, 2.0, "b"),
        SubtitleCue(2.0, 3.0, "c"),
    ]
    out = generate_srt(cues)
    lines = out.strip().splitlines()
    # First column of each cue header is the index.
    indices = [ln for ln in lines if ln.isdigit()]
    assert indices == ["1", "2", "3"]


def test_generate_srt_includes_speaker_as_prefix() -> None:
    cues = [SubtitleCue(0.0, 1.0, "hi", "Alice")]
    out = generate_srt(cues)
    assert "Alice: hi" in out


def test_generate_vtt_has_header() -> None:
    out = generate_vtt([])
    assert out.startswith("WEBVTT")


def test_generate_vtt_speaker_uses_voice_tag() -> None:
    cues = [SubtitleCue(0.0, 1.0, "hi", "Alice")]
    out = generate_vtt(cues)
    assert "<v Alice>hi" in out


def test_generate_vtt_escapes_text() -> None:
    cues = [SubtitleCue(0.0, 1.0, "a<b> & c")]
    out = generate_vtt(cues)
    assert "&lt;b&gt;" in out
    assert "&amp;" in out


def test_generate_clamps_inverted_endpoints() -> None:
    # end <= start would produce an invalid cue; the renderer should
    # nudge the end so the timing line is parseable.
    cues = [SubtitleCue(5.0, 5.0, "instant")]
    out = generate_srt(cues)
    # Each cue header line: HH:MM:SS,mmm --> HH:MM:SS,mmm
    timing = [ln for ln in out.splitlines() if "-->" in ln][0]
    start, _, end = timing.partition(" --> ")
    assert end > start


def test_generate_srt_accepts_raw_segment_dicts() -> None:
    # The renderer auto-converts dict rows so callers don't have to.
    rows = [{"start_sec": 0.0, "end_sec": 1.0, "text": "hi"}]
    out = generate_srt(rows)
    assert "hi" in out


def test_generate_empty_inputs() -> None:
    # Empty SRT is an empty string; empty VTT has just the header.
    assert generate_srt([]) == ""
    assert generate_vtt([]).startswith("WEBVTT")
