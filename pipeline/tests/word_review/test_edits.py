"""A reviewer's fixes carried over to a new transcript (Epic 52, story 52.9)."""

from __future__ import annotations

from typing import Any
from uuid import uuid4

import pytest

from maktaba_pipeline.engine.sql import dumps
from maktaba_pipeline.engine.stages import transcribe as tr
from maktaba_pipeline.runtime import Database
from maktaba_pipeline.stt.settings import Word
from maktaba_pipeline.stt.word_data import decode, pack
from maktaba_pipeline.transcript.edits import carry_over, find_spans
from maktaba_pipeline.word_review.db import read_array
from tests.stages.world import make_show

pytestmark = pytest.mark.integration


def test_spans_are_whole_words_on_folded_spelling() -> None:
    assert find_spans("قال الصلوة خير", "الصلوه") == [(4, 6)]
    assert find_spans("قال الصلوة خير", "صلوة") == []
    assert find_spans("a b c b", "b") == [(2, 1), (6, 1)]


async def _transcript(db: Database, vid: Any, lines: list[str]) -> str:
    tid = str(uuid4())
    track = await tr._first_audio_track(db, vid)
    await db.execute(
        "INSERT INTO transcripts (id, video_id, audio_track_id, language, backend, model,"
        " word_level, diarized, is_active, metadata) VALUES ($1, $2, $3, 'ar', 'mlx-batched',"
        " 'm', true, false, false, '{}')",
        tid, str(vid), track,
    )  # fmt: skip
    for seq, text in enumerate(lines):
        start = seq * 10.0
        words = [
            Word(start + i * 0.5, start + i * 0.5 + 0.5, w, 0.9) for i, w in enumerate(text.split())
        ]
        await db.execute(
            "INSERT INTO transcript_segments (transcript_id, seq, start_sec, end_sec, text,"
            " confidence, metadata, word_data) VALUES ($1, $2, $3, $4, $5, 0.9, $6, $7)",
            tid, seq, start, start + 5.0, text, dumps({}), pack(text, words, start).data,
        )  # fmt: skip
    return tid


@pytest.mark.asyncio
async def test_four_fixes_carry_over_and_one_becomes_a_previous_fix_flag(db: Database) -> None:
    w = await make_show(db, episodes=1, loose=0)
    vid = w.episodes[0]
    old_lines = ["قال الصلوة خير", "ذهب الطالبه", "كتب المدرسه", "قرا الكتب", "سمع الخبر"]
    old = await _transcript(db, vid, old_lines)
    fixes = [(0, 4, 6, "الصلوة", "الصلاة"), (1, 4, 6, "الطالبه", "الطالبة"),
             (2, 4, 7, "المدرسه", "المدرسة"), (3, 0, 3, "قرا", "قرأ"),
             (4, 4, 5, "الخبر", "الأخبار")]  # fmt: skip
    for seq, cs, cl, before, after in fixes:
        await db.execute(
            "INSERT INTO transcript_edits (id, transcript_id, segment_seq, char_start, char_len,"
            " before, after) VALUES ($1, $2, $3, $4, $5, $6, $7)",
            str(uuid4()), old, seq, cs, cl, before, after,
        )  # fmt: skip
    await db.execute("UPDATE transcripts SET is_active = true WHERE id = $1", old)
    # the relisten heard the last sentence otherwise: its fix has no place
    new = await _transcript(db, vid, old_lines[:4] + ["سمع الحديث"])
    async with db.transaction():
        done = await carry_over(db, old, new)
    assert done.applied == 4 and done.flagged == 1
    rows = await db.fetch(
        "SELECT seq, text, word_data FROM transcript_segments"
        " WHERE transcript_id = $1 ORDER BY seq",
        new,
    )
    assert [r["text"] for r in rows][:4] == [
        "قال الصلاة خير",
        "ذهب الطالبة",
        "كتب المدرسة",
        "قرأ الكتب",
    ]
    slots = decode(bytes(rows[0]["word_data"]))
    assert [(s.char_start, s.char_len) for s in slots] == [(0, 3), (4, 6), (11, 3)]
    carried = await db.fetchval(
        "SELECT COUNT(*) FROM transcript_edits"
        " WHERE transcript_id = $1 AND carried_from IS NOT NULL",
        new,
    )
    assert int(carried) == 4
    assert int(await db.fetchval("SELECT revision FROM transcripts WHERE id = $1", new)) == 4
    flag = await db.fetchrow(
        "SELECT reasons, candidates, segment_seq FROM word_flags WHERE transcript_id = $1", new
    )
    assert flag is not None and read_array(flag["reasons"]) == ["previous_fix"]
    assert '"الأخبار"' in str(flag["candidates"]) and flag["segment_seq"] == 4
