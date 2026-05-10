"""Story 9.11 — speaker voiceprint matching, naming, and merge support.

Diarisation is opt-in per library (``settings.diarize: true``); when on,
voiceprints are matched against per-library `speakers` rows by cosine
similarity. This module owns the *match-or-create* decision called for
every committed segment and the unknown-id allocation.

Voiceprints are 512-dim float32 d-vectors stored as ``BYTEA`` (BLOB on
SQLite) — about 2 KiB per row. We don't compute them here (the
diarization model produces them in :mod:`stt.diarization`); we just
operate on the bytes.

The match-vs-create vocabulary:

- :class:`MatchAssignment` — voice matched an existing speaker; the
  caller inserts a `segment_speakers` row and *does not* update the
  speaker's voiceprint (avoid drift, AC-2).
- :class:`NewSpeaker` — distance > threshold for every candidate; the
  caller inserts a fresh `speakers` row with name=NULL and the next
  unknown index.
"""

from __future__ import annotations

import math
import struct
from collections.abc import Iterable
from dataclasses import dataclass

__all__ = [
    "DEFAULT_MATCH_THRESHOLD",
    "MatchAssignment",
    "NewSpeaker",
    "SpeakerCandidate",
    "Voiceprint",
    "best_match",
    "cosine_distance",
    "decide",
    "next_unknown_index",
    "pack_voiceprint",
    "unpack_voiceprint",
    "unknown_display_name",
]

#: AC-1 — voices farther than this distance from any candidate are
#: treated as unknown. Library-tunable via ``settings.speaker_match_threshold``
#: (Story 9.1).
DEFAULT_MATCH_THRESHOLD: float = 0.35

#: A 512-dim float32 voiceprint as a list of floats. The DB shape is
#: ``BYTEA`` via :func:`pack_voiceprint`.
Voiceprint = list[float]


def pack_voiceprint(vec: Iterable[float]) -> bytes:
    """Pack a float32 vector into compact DB bytes (little-endian).

    Mirrors numpy's ``np.asarray(vec, dtype=np.float32).tobytes()`` so a
    Python-only worker can read/write voiceprints without numpy as a
    hard dependency.
    """
    floats = list(vec)
    fmt = f"<{len(floats)}f"
    return struct.pack(fmt, *floats)


def unpack_voiceprint(buf: bytes) -> Voiceprint:
    """Reverse of :func:`pack_voiceprint`. Length is inferred from
    ``len(buf) // 4`` (float32)."""
    n = len(buf) // 4
    if n * 4 != len(buf):
        raise ValueError(f"voiceprint blob length {len(buf)} not a multiple of 4")
    fmt = f"<{n}f"
    return list(struct.unpack(fmt, buf))


def cosine_distance(a: Voiceprint, b: Voiceprint) -> float:
    """1 - cosine_similarity(a, b). Returns a value in [0, 2].

    Two parallel zero vectors produce a distance of 1 (the conservative
    "unknown" choice), so the speaker matcher refuses to confuse two
    silent embeddings as the same person.
    """
    if len(a) != len(b):
        raise ValueError(f"voiceprint dim mismatch: {len(a)} vs {len(b)}")
    dot = 0.0
    na = 0.0
    nb = 0.0
    for x, y in zip(a, b, strict=True):
        dot += x * y
        na += x * x
        nb += y * y
    denom = math.sqrt(na) * math.sqrt(nb)
    if denom == 0.0:
        return 1.0
    return 1.0 - (dot / denom)


@dataclass(slots=True, frozen=True)
class SpeakerCandidate:
    """A registered speaker we might match against.

    The ``unknown_index`` is non-NULL for the auto-assigned unknowns
    (``unknown-1``, ``unknown-2``, …). A renamed speaker keeps its
    voiceprint but loses its unknown_index — see Story 9.11 EC.
    """

    speaker_id: str
    voiceprint: Voiceprint
    name: str | None = None
    unknown_index: int | None = None


@dataclass(slots=True, frozen=True)
class MatchAssignment:
    """Voice matched an existing speaker."""

    speaker_id: str
    distance: float

    @property
    def confidence(self) -> float:
        """Map distance to a 0..1 confidence: 1 - distance, clamped."""
        return max(0.0, min(1.0, 1.0 - self.distance))


@dataclass(slots=True, frozen=True)
class NewSpeaker:
    """No candidate was within threshold; allocate a new unknown."""

    unknown_index: int


def best_match(
    voiceprint: Voiceprint,
    candidates: Iterable[SpeakerCandidate],
) -> tuple[SpeakerCandidate, float] | None:
    """Return the closest candidate + its distance, or ``None`` if the
    candidate set is empty. Iterates once; no sort needed."""
    best: tuple[SpeakerCandidate, float] | None = None
    for cand in candidates:
        d = cosine_distance(voiceprint, cand.voiceprint)
        if best is None or d < best[1]:
            best = (cand, d)
    return best


def decide(
    voiceprint: Voiceprint,
    candidates: Iterable[SpeakerCandidate],
    *,
    threshold: float = DEFAULT_MATCH_THRESHOLD,
) -> MatchAssignment | NewSpeaker:
    """Match-or-create decision per segment.

    Materialises ``candidates`` once so we can re-scan to compute the
    next unknown index for the new-speaker branch.
    """
    cands = list(candidates)
    match = best_match(voiceprint, cands)
    if match is not None:
        cand, dist = match
        if dist <= threshold:
            return MatchAssignment(speaker_id=cand.speaker_id, distance=dist)
    return NewSpeaker(unknown_index=next_unknown_index(cands))


def next_unknown_index(candidates: Iterable[SpeakerCandidate]) -> int:
    """Smallest positive integer not used as an existing
    ``unknown_index``. The EC notes that merges can free indices, so
    the next new unknown takes the lowest free slot rather than
    ``max + 1``.
    """
    used: set[int] = set()
    for c in candidates:
        if c.unknown_index is not None and c.unknown_index >= 1:
            used.add(c.unknown_index)
    n = 1
    while n in used:
        n += 1
    return n


def unknown_display_name(index: int) -> str:
    """Render the auto-assigned name (UI shape, AC-1)."""
    return f"unknown-{index}"
