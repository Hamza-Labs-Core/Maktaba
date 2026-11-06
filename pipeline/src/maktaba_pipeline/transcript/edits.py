"""A reviewer's word fixes, carried over to a new transcript (Epic 52, story 52.9).

The Go API owns the edit itself (``api/internal/transcript``: one transaction, the
sentence's text, its ``word_data``, a ``transcript_edits`` row, the revision). When a video
is transcribed again (a relisten, a new model), the new transcript would lose them, so
``transcribe`` and ``second_listen`` call :func:`carry_over` inside the transaction that
stores it:

- each live (not undone) edit of the old transcript is looked for in the new one, within
  ±:data:`WINDOW_S` of the old sentence's start: a run of words whose folded spelling
  equals the folded ``before``;
- found exactly once: applied there, with the same semantics as Go (the shared fixtures
  ``shared/testdata/word_data/edits``), and recorded with ``carried_from``;
- the new transcript already reads ``after`` there: nothing to do;
- else a ``previous_fix`` flag at the closest words, the old fix its first candidate.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any
from uuid import uuid4

from ..engine.sql import dumps, uid
from ..stt.word_data import edit as edit_word_data
from ..text.arabic import fold
from ..word_review.db import array_param

__all__ = ["WINDOW_S", "Carried", "apply_text_edit", "carry_over", "find_spans"]

#: how far from the old sentence's start a fix is looked for (s)
WINDOW_S = 2.0


@dataclass(slots=True)
class Carried:
    applied: int = 0
    flagged: int = 0
    already: int = 0


def _fkey(text: str) -> str:
    return fold(text).replace(" ", "")


def _word_runs(text: str) -> list[tuple[int, int]]:
    out: list[tuple[int, int]] = []
    i = 0
    while i < len(text):
        if text[i].isspace():
            i += 1
            continue
        j = i
        while j < len(text) and not text[j].isspace():
            j += 1
        out.append((i, j))
        i = j
    return out


def find_spans(text: str, wanted: str) -> list[tuple[int, int]]:
    """Every run of whole words of ``text`` whose folded spelling equals ``wanted``'s,
    as (char_start, char_len) code points."""
    key = _fkey(wanted)
    n = max(1, len(wanted.split()))
    if not key:
        return []
    runs = _word_runs(text)
    out: list[tuple[int, int]] = []
    for i in range(len(runs) - n + 1):
        a, b = runs[i][0], runs[i + n - 1][1]
        if _fkey(text[a:b]) == key:
            out.append((a, b - a))
    return out


def apply_text_edit(
    text: str, data: bytes | None, char_start: int, char_len: int, after: str
) -> tuple[str, bytes | None]:
    """The sentence after the edit: the same semantics as Go's ``worddata.Edit``."""
    return edit_word_data(text, data, char_start, char_len, after)


def _distance(a: str, b: str) -> int:
    prev = list(range(len(b) + 1))
    for i, ca in enumerate(a, 1):
        cur = [i]
        for j, cb in enumerate(b, 1):
            cur.append(min(prev[j] + 1, cur[j - 1] + 1, prev[j - 1] + (ca != cb)))
        prev = cur
    return prev[-1]


def _closest(text: str, wanted: str) -> tuple[int, int]:
    """The run of words of ``text`` closest in spelling to ``wanted`` (the place a fix that
    cannot be carried over is shown)."""
    n = max(1, len(wanted.split()))
    runs = _word_runs(text)
    if not runs:
        return (0, 0)
    best: tuple[int, int, int] | None = None
    key = _fkey(wanted)
    for i in range(max(1, len(runs) - n + 1)):
        a, b = runs[i][0], runs[min(len(runs), i + n) - 1][1]
        d = _distance(_fkey(text[a:b]), key)
        if best is None or d < best[0]:
            best = (d, a, b - a)
    assert best is not None
    return best[1], best[2]


async def carry_over(db: Any, old_tid: Any, new_tid: Any) -> Carried:
    """The old transcript's live edits, applied again to ``new_tid`` (whose segments are
    stored, inside the caller's transaction)."""
    done = Carried()
    edits = list(
        await db.fetch(
            "SELECT e.id, e.segment_seq, e.before, e.after, e.user_id, s.start_sec"
            " FROM transcript_edits e JOIN transcript_segments s"
            "   ON s.transcript_id = e.transcript_id AND s.seq = e.segment_seq"
            " WHERE e.transcript_id = $1 AND e.undone_at IS NULL"
            " ORDER BY e.created_at, e.id",
            uid(old_tid),
        )
    )
    if not edits:
        return done
    tr = await db.fetchrow(
        "SELECT t.video_id, t.audio_track_id, v.library_id FROM transcripts t"
        " JOIN videos v ON v.id = t.video_id WHERE t.id = $1",
        uid(new_tid),
    )
    for e in edits:
        t0 = float(e["start_sec"])
        rows = list(
            await db.fetch(
                "SELECT seq, start_sec, end_sec, text, word_data FROM transcript_segments"
                " WHERE transcript_id = $1 AND end_sec >= $2 AND start_sec <= $3 ORDER BY seq",
                uid(new_tid),
                t0 - WINDOW_S,
                t0 + WINDOW_S,
            )
        )
        before, after = str(e["before"]), str(e["after"])
        found = [(r, span) for r in rows for span in find_spans(str(r["text"] or ""), before)]
        if len(found) == 1:
            r, (cs, cl) = found[0]
            text, data = apply_text_edit(
                str(r["text"]), bytes(r["word_data"]) if r["word_data"] else None, cs, cl, after
            )
            await db.execute(
                "UPDATE transcript_segments SET text = $3, word_data = $4"
                " WHERE transcript_id = $1 AND seq = $2",
                uid(new_tid),
                r["seq"],
                text,
                data,
            )
            await db.execute(
                "INSERT INTO transcript_edits (id, transcript_id, segment_seq, char_start,"
                " char_len, before, after, user_id, carried_from)"
                " VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)",
                uid(uuid4()),
                uid(new_tid),
                r["seq"],
                cs,
                cl,
                str(r["text"])[cs : cs + cl],
                after,
                uid(e["user_id"]) if e["user_id"] else None,
                uid(e["id"]),
            )
            await db.execute(
                "UPDATE transcripts SET revision = revision + 1 WHERE id = $1", uid(new_tid)
            )
            done.applied += 1
            continue
        if after and any(find_spans(str(r["text"] or ""), after) for r in rows):
            done.already += 1
            continue
        if not rows or tr is None:
            continue
        near = min(rows, key=lambda r: abs(float(r["start_sec"]) - t0))
        text = str(near["text"] or "")
        cs, cl = _closest(text, before)
        await db.execute(
            "INSERT INTO word_flags (id, kind, transcript_id, video_id, library_id,"
            " audio_track_id, segment_seq, char_start, char_len, start_sec, end_sec,"
            " original, context, reasons, scores, priority, candidates, key, status)"
            " VALUES ($1, 'word', $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14,"
            " $15, $16, $17, 'open')"
            " ON CONFLICT (transcript_id, segment_seq, char_start) DO NOTHING",
            uid(uuid4()),
            uid(new_tid),
            uid(tr["video_id"]),
            uid(tr["library_id"]),
            tr["audio_track_id"],
            near["seq"],
            cs,
            cl,
            float(near["start_sec"]),
            float(near["end_sec"]),
            text[cs : cs + cl],
            text,
            array_param(db, ["previous_fix"]),
            dumps({"previous_fix": {"edit": str(e["id"]), "before": before},
                   "time_estimated": True}),
            0.9,
            dumps([{"text": after, "source": "previous_fix", "score": 1.0}]),
            _fkey(text[cs : cs + cl]),
        )  # fmt: skip
        done.flagged += 1
    return done
