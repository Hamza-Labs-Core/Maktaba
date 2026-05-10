"""Story 3.7 — pause-decision logic + resume-prompt rebuild."""

from __future__ import annotations

import asyncio
from uuid import uuid4

from maktaba_pipeline.stt.pause_resume import (
    DEFAULT_RESUME_PROMPT_K,
    apply_resume_seek,
    build_resume_prompt,
    is_pause_due,
    load_resume_point,
)

from ..audio._fake_audio_db import FakeAudioDB, _TranscriptSegmentRow


def test_default_K_is_3() -> None:
    assert DEFAULT_RESUME_PROMPT_K == 3


def test_pause_decision_when_neither_flag_set() -> None:
    decision = is_pause_due({"pause_requested": False, "cancel_requested": False})
    assert decision.pause is False
    assert decision.cancel is False
    assert decision.reason is None


def test_pause_decision_user_request() -> None:
    decision = is_pause_due({"pause_requested": True, "cancel_requested": False})
    assert decision.pause is True and decision.cancel is False
    assert decision.reason == "user"


def test_cancel_takes_priority_over_pause() -> None:
    decision = is_pause_due({"pause_requested": True, "cancel_requested": True})
    assert decision.cancel is True and decision.pause is False
    assert decision.reason == "cancel"


def test_pause_decision_none_for_missing_row() -> None:
    decision = is_pause_due(None)
    assert decision.pause is False and decision.cancel is False


def test_resume_seek_subtracts_safety_margin_in_extract() -> None:
    # The prompt-seek doesn't subtract; the *extract* stage's
    # ``build_ffmpeg_args`` does. ``apply_resume_seek`` returns the
    # raw value clamped to >= 0.
    assert apply_resume_seek(123.4) == 123.4
    assert apply_resume_seek(-1.0) == 0.0


def test_build_resume_prompt_concatenates_with_spaces() -> None:
    out = build_resume_prompt(["hello world", "  ", "foo"])
    assert out == "hello world foo"


def test_build_resume_prompt_truncates_to_1024_chars() -> None:
    chunks = ["a" * 600, "b" * 600]
    out = build_resume_prompt(chunks)
    assert len(out) == 1024


def test_load_resume_point_reads_last_k_segments() -> None:
    db = FakeAudioDB()
    transcript_id = uuid4()

    for i in range(5):
        db.transcript_segments[i + 1] = _TranscriptSegmentRow(
            id=i + 1,
            transcript_id=transcript_id,
            seq=i,
            start_sec=float(i),
            end_sec=float(i + 1),
            text=f"seg{i}",
            speaker=None,
            confidence=None,
        )

    rp = asyncio.run(
        load_resume_point(
            db,
            transcript_id=transcript_id,
            last_segment_end_sec=5.0,
            pinned_language="ar",
            k=3,
        )
    )
    assert rp.transcript_id == transcript_id
    assert rp.last_segment_end_sec == 5.0
    # Most recent 3 segments are seq 2, 3, 4 — concatenated forward.
    assert rp.prompt == "seg2 seg3 seg4"
    assert rp.pinned_language == "ar"
