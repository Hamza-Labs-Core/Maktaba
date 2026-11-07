"""Writing a transcript's flags (Epic 52, story 52.6).

In one transaction: the transcript must still be active (else nothing is written). Each
flag the run produced is upserted on ``(transcript_id, segment_seq, char_start)``, with an
id derived from that place, so the same run writes the same rows. A flag a person resolved
(``resolved_by`` set: accepted, corrected, a name, the dictionary, skipped by hand) is
never touched, and its word is not flagged again in the same revision. An open flag this
run did not produce again goes, unless a person made it (``manual``, ``previous_fix``), or
its word is now in the vocabulary: then it is ``accepted`` on the vocabulary's word
(story 52.10).
"""

from __future__ import annotations

import uuid
from collections.abc import Sequence
from typing import Any

from ..engine.hashing import canonical_hash
from ..engine.sql import dumps, loads, uid
from .db import array_param, read_array
from .signals import Flag
from .vocabulary import Vocabulary

__all__ = ["KEEP_REASONS", "NAMESPACE", "flag_id", "flags_hash", "write"]

NAMESPACE = uuid.UUID("5f0c8f6e-52a0-4c52-9a52-000000000052")
#: open flags people made: a check run never removes them
KEEP_REASONS = frozenset({"manual", "previous_fix"})


def flag_id(transcript_id: Any, seq: int, char_start: int) -> str:
    return str(uuid.uuid5(NAMESPACE, f"{transcript_id}:{seq}:{char_start}"))


def _row(f: Flag) -> dict[str, Any]:
    return {
        "kind": f.kind,
        "seq": f.segment_seq,
        "wi": f.word_index,
        "wc": f.word_count,
        "cs": f.char_start,
        "cl": f.char_len,
        "a": f.start_sec,
        "b": f.end_sec,
        "orig": f.original,
        "ctx": f.context,
        "lang": f.language,
        "reasons": list(f.reasons),
        "scores": f.scores,
        "prio": f.priority,
        "cands": f.candidates,
        "key": f.key,
        "status": f.status,
        "pack": f.pack,
    }


def flags_hash(flags: Sequence[Flag]) -> str:
    return canonical_hash([_row(f) for f in flags])


async def write(
    db: Any,
    transcript: Any,
    flags: Sequence[Flag],
    check_version: int,
    vocabulary: Vocabulary | None = None,
) -> dict[str, int]:
    """Store ``flags`` for ``transcript`` (a row with id, video_id, library_id,
    audio_track_id, revision). Returns what changed, by kind of change."""
    tid = uid(transcript["id"])
    done = {"written": 0, "removed": 0, "accepted": 0, "kept": 0}
    async with db.transaction():
        still = await db.fetchval("SELECT 1 FROM transcripts WHERE id = $1 AND is_active", tid)
        if not still:
            return {**done, "inactive": 1}
        existing = {
            (int(r["segment_seq"]), int(r["char_start"])): r
            for r in await db.fetch(
                "SELECT id, segment_seq, char_start, status, resolved_by, reasons, key,"
                " language, scores FROM word_flags WHERE transcript_id = $1",
                tid,
            )
        }
        resolved = {k for k, r in existing.items() if r["resolved_by"] is not None}
        made: set[tuple[int, int]] = set()
        rows: list[tuple[Any, ...]] = []
        for f in flags:
            place = (f.segment_seq, f.char_start)
            if place in resolved:
                done["kept"] += 1
                continue
            made.add(place)
            rows.append(
                (
                    flag_id(tid, f.segment_seq, f.char_start),
                    f.kind,
                    tid,
                    uid(transcript["video_id"]),
                    uid(transcript["library_id"]),
                    transcript["audio_track_id"],
                    f.segment_seq,
                    f.word_index,
                    f.word_count,
                    f.char_start,
                    f.char_len,
                    f.start_sec,
                    f.end_sec,
                    f.original,
                    f.context,
                    f.language,
                    array_param(db, list(f.reasons)),
                    dumps(f.scores),
                    f.priority,
                    dumps(f.candidates),
                    f.key,
                    f.status,
                    f.pack,
                    check_version,
                )
            )
        # one batch, not a statement per flag: the transaction is held for one round trip
        # (a long transcript has thousands of flags; per-flag it held its row locks for
        # minutes on a busy home, and the next writer of the same transcript waited)
        if rows:
            await db.executemany(
                "INSERT INTO word_flags (id, kind, transcript_id, video_id, library_id,"
                " audio_track_id, segment_seq, word_index, word_count, char_start, char_len,"
                " start_sec, end_sec, original, context, language, reasons, scores, priority,"
                " candidates, key, status, pack, check_version)"
                " VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15,"
                " $16, $17, $18, $19, $20, $21, $22, $23, $24)"
                " ON CONFLICT (transcript_id, segment_seq, char_start) DO UPDATE SET"
                " kind = excluded.kind, word_index = excluded.word_index,"
                " word_count = excluded.word_count, char_len = excluded.char_len,"
                " start_sec = excluded.start_sec, end_sec = excluded.end_sec,"
                " original = excluded.original, context = excluded.context,"
                " language = excluded.language, reasons = excluded.reasons,"
                " scores = excluded.scores, priority = excluded.priority,"
                " candidates = excluded.candidates, key = excluded.key,"
                " status = excluded.status, pack = excluded.pack,"
                " check_version = excluded.check_version, audio_track_id = excluded.audio_track_id"
                " WHERE word_flags.resolved_by IS NULL",
                rows,
            )
        done["written"] += len(rows)
        for place, r in sorted(existing.items(), key=lambda kv: kv[0]):
            if place in made or place in resolved:
                continue
            if r["status"] not in ("open", "skipped", "stale"):
                continue
            if KEEP_REASONS & set(read_array(r["reasons"])):
                continue
            if vocabulary is not None and vocabulary.knows(str(r["language"]), str(r["key"])):
                scores = loads(r["scores"]) or {}
                scores = {**(scores if isinstance(scores, dict) else {}), "source": "vocabulary"}
                await db.execute(
                    "UPDATE word_flags SET status = 'accepted', scores = $2 WHERE id = $1",
                    r["id"],
                    dumps(scores),
                )
                done["accepted"] += 1
                continue
            await db.execute("DELETE FROM word_flags WHERE id = $1", r["id"])
            done["removed"] += 1
    return done
