"""Story 9.18 — chapter inference from transcript topic shifts.

Architecture §4.6 calls for "inferred chapters from transcript-level
topic shifts (cosine drop between adjacent segment embeddings >
threshold)". This module is the inference algorithm; the pipeline-stage
wiring (`processing_jobs.stage='chapter_infer'`) lives in the
orchestrator and the chapter-row writer lives in the DB module.

The algorithm processes a video's transcript-segment embeddings
sequentially, computing cosine similarity between adjacent
``window_segments``-sized windows, and emits a chapter boundary wherever
the cosine drop exceeds ``drop_threshold``, subject to a minimum
chapter duration ``min_chapter_sec``.

Suppression vs override (AC-4) is the responsibility of the writer:
this module just emits boundary candidates as ``InferredChapter``
records. The writer merges them with `embedded` and `manual` rows per
Epic 8 Story 8.12's priority rule.
"""

from __future__ import annotations

import math
from collections.abc import Sequence
from dataclasses import dataclass
from typing import Protocol


class _LabelResolver(Protocol):
    """Tiny callable protocol for the labeller — plain ``Callable``
    works but the named alias makes the type signatures clearer."""

    def __call__(self, texts: Sequence[str]) -> str: ...

__all__ = [
    "DEFAULT_DROP_THRESHOLD",
    "DEFAULT_MIN_CHAPTER_SEC",
    "DEFAULT_TITLE_FALLBACK",
    "DEFAULT_WINDOW_SEGMENTS",
    "InferredChapter",
    "Segment",
    "infer_chapters",
    "title_for_chapter",
]


#: AC defaults — overrideable via library/global settings.
DEFAULT_WINDOW_SEGMENTS: int = 5
DEFAULT_DROP_THRESHOLD: float = 0.35
DEFAULT_MIN_CHAPTER_SEC: float = 60.0
DEFAULT_TITLE_FALLBACK: str = "Chapter"


@dataclass(slots=True, frozen=True)
class Segment:
    """One transcript segment as the inference algorithm sees it.

    ``embedding`` is the per-segment vector produced by Epic 5's
    indexer; ``text`` is kept around so the labeller can reach into the
    closest-to-centroid segments without a second DB round-trip.
    """

    start_sec: float
    end_sec: float
    embedding: Sequence[float]
    text: str = ""


@dataclass(slots=True, frozen=True)
class InferredChapter:
    """One inferred chapter span (AC-2 row in `chapters` with
    ``source='inferred'``)."""

    seq: int
    start_sec: float
    end_sec: float
    title: str | None = None


def infer_chapters(
    segments: Sequence[Segment],
    *,
    window_segments: int = DEFAULT_WINDOW_SEGMENTS,
    drop_threshold: float = DEFAULT_DROP_THRESHOLD,
    min_chapter_sec: float = DEFAULT_MIN_CHAPTER_SEC,
) -> list[InferredChapter]:
    """Emit inferred chapter spans for one video.

    Returns the *full* list of N+1 chapter spans (where N is the number
    of detected boundaries) — including the "everything else" trailing
    chapter. Title is left as ``None`` here; :func:`title_for_chapter`
    fills it in once the embedder has answered with the closest token.

    EC: a transcript with fewer than ``2 * window_segments`` segments
    produces exactly one chapter spanning the video; no boundaries.
    """
    if not segments:
        return []

    if len(segments) < 2 * window_segments:
        return [
            InferredChapter(
                seq=0,
                start_sec=segments[0].start_sec,
                end_sec=segments[-1].end_sec,
            )
        ]

    boundaries: list[int] = []
    last_boundary_time = segments[0].start_sec
    for i in range(window_segments, len(segments) - window_segments):
        left = _mean_window(segments, i - window_segments, i)
        right = _mean_window(segments, i, i + window_segments)
        sim = _cosine(left, right)
        drop = 1.0 - sim
        if drop < drop_threshold:
            continue
        boundary_time = segments[i].start_sec
        if boundary_time - last_boundary_time < min_chapter_sec:
            continue
        boundaries.append(i)
        last_boundary_time = boundary_time

    chapters: list[InferredChapter] = []
    start = segments[0].start_sec
    seq = 0
    last_idx = 0
    for b in boundaries:
        end = segments[b].start_sec
        chapters.append(InferredChapter(seq=seq, start_sec=start, end_sec=end))
        seq += 1
        start = end
        last_idx = b
    chapters.append(
        InferredChapter(
            seq=seq,
            start_sec=start,
            end_sec=segments[-1].end_sec,
        )
    )
    _ = last_idx  # kept for symmetry with future title-from-window code
    return chapters


def title_for_chapter(
    chapter: InferredChapter,
    segments: Sequence[Segment],
    label_resolver: _LabelResolver | None = None,
    *,
    max_chars: int = 80,
) -> str:
    """AC-3 — produce a label for the chapter window.

    The labeller is the embedder's nearest-token API (passed in via
    ``label_resolver``); when unavailable we fall back to
    ``"Chapter N"`` so the writer always has something to insert.
    """
    in_window = [
        s for s in segments if s.start_sec >= chapter.start_sec and s.start_sec < chapter.end_sec
    ]
    if not in_window or label_resolver is None:
        return f"{DEFAULT_TITLE_FALLBACK} {chapter.seq + 1}"
    # Pick the top 3 segments closest to the window centroid.
    centroid = _mean_window(in_window, 0, len(in_window))
    scored = sorted(
        ((s, _cosine(centroid, s.embedding)) for s in in_window),
        key=lambda t: t[1],
        reverse=True,
    )
    pick = scored[:3]
    label = label_resolver([s.text for s, _ in pick])
    label = label.strip()
    if not label:
        return f"{DEFAULT_TITLE_FALLBACK} {chapter.seq + 1}"
    return label[:max_chars]


# ---------------------------------------------------------------------------
# Internals
# ---------------------------------------------------------------------------


def _cosine(a: Sequence[float], b: Sequence[float]) -> float:
    if not a or not b:
        return 0.0
    if len(a) != len(b):
        raise ValueError(f"vector dim mismatch: {len(a)} vs {len(b)}")
    dot = sum(x * y for x, y in zip(a, b, strict=True))
    na = math.sqrt(sum(x * x for x in a))
    nb = math.sqrt(sum(x * x for x in b))
    if na == 0.0 or nb == 0.0:
        return 0.0
    return dot / (na * nb)


def _mean_window(segments: Sequence[Segment], lo: int, hi: int) -> list[float]:
    """Mean of segment embeddings in [lo, hi). All vectors must agree
    on dimension."""
    if hi <= lo:
        return []
    dim = len(segments[lo].embedding)
    out = [0.0] * dim
    for k in range(lo, hi):
        emb = segments[k].embedding
        if len(emb) != dim:
            raise ValueError(
                f"embedding dim mismatch at index {k}: {len(emb)} vs {dim}"
            )
        for j, v in enumerate(emb):
            out[j] += v
    n = hi - lo
    return [v / n for v in out]
