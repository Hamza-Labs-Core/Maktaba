# ruff: noqa: E501  (long SQL string literals read better unwrapped)
"""``fp_gate``: do a season's samples share a signature, so reading candidates is worth it?

Step 2 of the two-pass fingerprint flow (plan 06-11 D12 and section 3.4). ``detect``
requested fingerprints of ``PER_SEASON`` sample episodes plus this gate. The gate:

* waits (``waiting_inputs``, output unchanged) while any sample still has a live
  ``fingerprint`` job - the last sample to finish triggers it again;
* otherwise applies ``has_signature`` (at least half of the sample pairs share an intro,
  an end song or frames) and stores ``signature`` in ``series_seasons.metadata``:
  ``shared`` / ``none`` / ``incomplete`` (fewer than two samples carry a fingerprint);
* on ``shared`` requests the candidates' fingerprints. A season of talks without a fixed
  intro has nothing a fingerprint could match, so its candidates are never read.

Termination is argued in the spec (section 3.4): each video's fingerprint output changes at
most once without an external change, and no request leads back to the requesting stage.
"""

from __future__ import annotations

from collections.abc import Sequence
from typing import Any
from uuid import UUID

from ...audio.chromaprint import MATCH, decode, similarity
from ...db.jobs import Job
from ...thumbnail.dhash import hamming
from ..context import StageContext
from ..graph import SIG_VERSION, Request, StageResult, Subject
from ..hashing import canonical_hash
from ..sql import dumps, loads, now, to_ts, uid
from .registry import StageImpl

__all__ = ["IMPL", "decide_signature", "pair_matches"]

SUCCESS = "('changed','unchanged','skipped_unchanged','imported')"
Signal = tuple[str | None, str | None, str | None]  # audio_head, audio_tail, frame_hashes


def _hashes(s: str | None) -> list[int]:
    return [int(h, 16) for h in (s or "").split(",") if h]


def pair_matches(a: Signal, b: Signal) -> bool:
    """Do two fingerprinted videos share an intro, end song or frames (``signatures._same``)?"""
    ah, at, bh, bt = decode(a[0]), decode(a[1]), decode(b[0]), decode(b[1])
    for x, y in ((ah, bh), (at, bt), (ah, bt), (at, bh)):
        if x and y and similarity(x, y, step=4) >= MATCH:
            return True
    af, bf = _hashes(a[2]), _hashes(b[2])
    return bool(af and bf) and sum(1 for f in af if min(hamming(f, g) for g in bf) <= 6) >= 2


def decide_signature(signals: Sequence[Signal]) -> str:
    """``shared`` when at least half the pairs match, ``none`` when they do not,
    ``incomplete`` when fewer than two samples carry any fingerprint."""
    have = [s for s in signals if s[0] or s[1] or s[2]]
    pairs = [(i, j) for i in range(len(have)) for j in range(i + 1, len(have))]
    if not pairs:
        return "incomplete"
    same = sum(pair_matches(have[i], have[j]) for i, j in pairs)
    return "shared" if 2 * same >= len(pairs) else "none"


async def _season(db: Any, season_id: UUID) -> tuple[dict[str, Any], str | None] | None:
    row = await db.fetchrow(
        "SELECT metadata, retired_at, library_id FROM series_seasons WHERE id = $1", uid(season_id)
    )
    if row is None or row["retired_at"] is not None:
        return None
    return loads(row["metadata"]) or {}, None if row["library_id"] is None else str(
        row["library_id"]
    )


async def input_fingerprint(db: Any, subject: Subject) -> str | None:
    s = await _season(db, subject.id)
    if s is None:
        return canonical_hash("gone")
    plan = s[0].get("fp_plan") or {}
    samples = list(plan.get("samples") or [])
    outs: list[list[Any]] = []
    for v in samples:
        r = await db.fetchrow(
            "SELECT output_hash FROM processing_jobs WHERE subject_kind = 'video' AND subject_id = $1"
            f" AND stage = 'fingerprint' AND state = 'done' AND outcome IN {SUCCESS}"
            " ORDER BY id DESC LIMIT 1",
            v,
        )
        outs.append([v, None if r is None else r["output_hash"]])
    return canonical_hash(
        {"samples": outs, "candidates": sorted(plan.get("candidates") or []), "v": SIG_VERSION}
    )


async def _live_fingerprint_jobs(db: Any, videos: Sequence[str]) -> bool:
    for v in videos:
        r = await db.fetchrow(
            "SELECT 1 AS x FROM processing_jobs WHERE subject_kind = 'video' AND subject_id = $1"
            " AND stage = 'fingerprint'"
            " AND state IN ('pending','claimed','running','resuming','paused') LIMIT 1",
            v,
        )
        if r is not None:
            return True
    return False


async def _signal(db: Any, video_id: str) -> Signal:
    r = await db.fetchrow(
        "SELECT audio_head, audio_tail, frame_hashes FROM video_signals WHERE video_id = $1",
        video_id,
    )
    return (
        (None, None, None) if r is None else (r["audio_head"], r["audio_tail"], r["frame_hashes"])
    )


async def _fingerprinted(db: Any, video_id: str) -> bool:
    r = await db.fetchrow(
        "SELECT fp_version, fp_partial, audio_head, audio_tail, frame_hashes FROM video_signals"
        " WHERE video_id = $1",
        video_id,
    )
    return bool(
        r is not None
        and r["fp_version"] == SIG_VERSION
        and not r["fp_partial"]
        and (r["audio_head"] or r["audio_tail"] or r["frame_hashes"])
    )


async def run(db: Any, job: Job, ctx: StageContext) -> StageResult:
    season_id = job.subject.id
    s = await _season(db, season_id)
    if s is None or not (s[0].get("fp_plan") or {}).get("candidates"):
        return StageResult(
            output_hash=canonical_hash({"signature": None}), summary={"gate": "idle"}
        )
    meta, _lib = s
    plan = meta["fp_plan"]
    samples: list[str] = list(plan["samples"])
    if await _live_fingerprint_jobs(db, samples):
        return StageResult(
            output_hash=canonical_hash({"signature": meta.get("signature")}),
            outcome="waiting_inputs",
            summary={"gate": "waiting for sample fingerprints"},
        )
    signature = decide_signature([await _signal(db, v) for v in samples])
    async with db.transaction():
        row = await db.fetchrow("SELECT metadata FROM series_seasons WHERE id = $1", uid(season_id))
        fresh = loads(row["metadata"]) or {}
        fresh["signature"] = signature
        await db.execute(
            "UPDATE series_seasons SET metadata = $2, updated_at = $3 WHERE id = $1",
            uid(season_id),
            dumps(fresh),
            to_ts(db, now()),
        )
    requests: list[Request] = []
    if signature == "shared":
        for c in plan["candidates"]:
            if not await _fingerprinted(db, c):
                requests.append(
                    Request(
                        "fingerprint",
                        "video",
                        UUID(c),
                        "candidate for a season with a shared signature",
                        payload={"role": "candidate", "seasons": [str(season_id)]},
                    )
                )
    return StageResult(
        output_hash=canonical_hash({"signature": signature}),
        requests=tuple(requests),
        summary={"signature": signature, "candidates_requested": len(requests)},
    )


IMPL = StageImpl(run=run, input_fingerprint=input_fingerprint)
