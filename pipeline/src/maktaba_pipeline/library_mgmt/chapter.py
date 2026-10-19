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

import importlib
import math
from collections.abc import Sequence
from dataclasses import dataclass
from typing import Any, Protocol


class _LabelResolver(Protocol):
    """Tiny callable protocol for the labeller — plain ``Callable``
    works but the named alias makes the type signatures clearer."""

    def __call__(self, texts: Sequence[str]) -> str: ...


__all__ = [
    "DEFAULT_DROP_THRESHOLD",
    "DEFAULT_MIN_CHAPTER_SEC",
    "DEFAULT_SHIFT_MIN_SEC",
    "DEFAULT_SHIFT_WINDOW",
    "DEFAULT_SHIFT_Z",
    "DEFAULT_SNAP_RADIUS",
    "DEFAULT_TITLE_FALLBACK",
    "DEFAULT_WINDOW_SEGMENTS",
    "InferredChapter",
    "Segment",
    "extractive_title",
    "infer_chapters",
    "infer_topic_chapters",
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
# Relative topic shifts (what the engine ``chapter_infer`` stage runs)
# ---------------------------------------------------------------------------

#: Defaults calibrated on the library's own transcripts with multilingual-e5-small
#: (25 lectures, 15 min to 2 h): the drop between two 10-segment windows has a median
#: of 0.019 and a 99.9th percentile of 0.046, so the absolute 0.35 of
#: :func:`infer_chapters` (chosen for e5-large) never fires. A drop that stands
#: ``DEFAULT_SHIFT_Z`` standard deviations above the video's own mean does, at about
#: one chapter per 10 minutes, and the cuts land where the subject changes.
DEFAULT_SHIFT_WINDOW: int = 10
DEFAULT_SHIFT_Z: float = 2.5
DEFAULT_SHIFT_MIN_SEC: float = 240.0
#: a boundary moves to the longest pause within this many segments of the peak
DEFAULT_SNAP_RADIUS: int = 3


def infer_topic_chapters(
    segments: Sequence[Segment],
    *,
    window_segments: int = DEFAULT_SHIFT_WINDOW,
    z: float = DEFAULT_SHIFT_Z,
    min_chapter_sec: float = DEFAULT_SHIFT_MIN_SEC,
    snap_radius: int = DEFAULT_SNAP_RADIUS,
) -> list[InferredChapter]:
    """Chapters at the video's strongest topic shifts, whatever the embedding model.

    For every position the cosine drop between the mean of the ``window_segments``
    before and after it is computed; a local peak counts when it is at least ``z``
    standard deviations above the mean drop of this video. Peaks are taken strongest
    first, at least ``min_chapter_sec`` from the video's ends and from each other, and
    each moves to the longest silence within ``snap_radius`` segments (people pause
    between subjects, not mid-sentence). Returns the full span list, as
    :func:`infer_chapters` does: one chapter when the video is too short to split.
    """
    if not segments:
        return []
    n = len(segments)
    whole = [InferredChapter(seq=0, start_sec=segments[0].start_sec, end_sec=segments[-1].end_sec)]
    w = window_segments
    if w < 1 or n < 2 * w + 1:
        return whole
    # numpy (the search extra brings it, and the stage needs that extra anyway): a 4-hour
    # transcript is ~4 000 x 384 numbers, which pure Python holds the GIL over for seconds
    np: Any = importlib.import_module("numpy")
    x = _matrix(np, segments)
    # prefix sums of the vectors: every window sum in O(dim)
    prefix = np.vstack([np.zeros((1, x.shape[1])), np.cumsum(x, axis=0)])
    positions = np.arange(w, n - w + 1)
    left = prefix[positions] - prefix[positions - w]
    right = prefix[positions + w] - prefix[positions]
    norms = np.linalg.norm(left, axis=1) * np.linalg.norm(right, axis=1)
    dots = np.einsum("ij,ij->i", left, right)
    cos = np.divide(dots, norms, out=np.zeros_like(dots), where=norms != 0.0)
    drops = 1.0 - cos
    std = float(drops.std())
    if std == 0.0:
        return whole
    floor = float(drops.mean()) + z * std
    prev = np.concatenate(([-np.inf], drops[:-1]))
    nxt = np.concatenate((drops[1:], [-np.inf]))
    mask = (drops >= floor) & (drops >= prev) & (drops >= nxt)
    peaks = [(float(drops[k]), int(positions[k])) for k in np.flatnonzero(mask)]
    peaks.sort(key=lambda p: (-p[0], p[1]))
    first, last = segments[0].start_sec, segments[-1].end_sec
    chosen: list[int] = []
    for _, i in peaks:
        i = _snap_to_pause(segments, i, snap_radius)
        t = segments[i].start_sec
        if t - first < min_chapter_sec or last - t < min_chapter_sec:
            continue
        if all(abs(t - segments[j].start_sec) >= min_chapter_sec for j in chosen):
            chosen.append(i)
    chosen.sort()
    chapters: list[InferredChapter] = []
    start = first
    for seq, i in enumerate(chosen):
        end = segments[i].start_sec
        chapters.append(InferredChapter(seq=seq, start_sec=start, end_sec=end))
        start = end
    chapters.append(InferredChapter(seq=len(chosen), start_sec=start, end_sec=last))
    return chapters


def extractive_title(
    chapter: InferredChapter,
    segments: Sequence[Segment],
    *,
    lead_segments: int = 6,
    min_words: int = 3,
    max_chars: int = 60,
) -> str | None:
    """The chapter's own words as its title, or None when it has none worth showing.

    Among the chapter's first ``lead_segments`` sentences (a new subject is usually
    announced as it starts), the one closest to the chapter's mean vector, with at least
    ``min_words`` words; cut at a word boundary to ``max_chars`` with an ellipsis.
    """
    inside = [s for s in segments if chapter.start_sec <= s.start_sec < chapter.end_sec]
    if not inside:
        return None
    lead = [s for s in inside[:lead_segments] if len(s.text.split()) >= min_words]
    if not lead:
        return None
    np: Any = importlib.import_module("numpy")
    centroid = _matrix(np, inside).mean(axis=0)
    cand = _matrix(np, lead)
    norms = np.linalg.norm(cand, axis=1) * float(np.linalg.norm(centroid))
    sims = np.divide(cand @ centroid, norms, out=np.zeros(len(lead)), where=norms != 0.0)
    best = lead[int(sims.argmax())]  # the first of equals, as max() picks
    text = " ".join(best.text.split())
    if len(text) <= max_chars:
        return text
    cut = text[: max_chars - 1].rsplit(" ", 1)[0] or text[: max_chars - 1]
    return cut.rstrip(" ،,.؛:") + "…"


def _matrix(np: Any, segments: Sequence[Segment]) -> Any:
    """The segments' vectors as one float64 matrix (all of one dimension)."""
    dim = len(segments[0].embedding)
    for k, s in enumerate(segments):
        if len(s.embedding) != dim:
            raise ValueError(f"embedding dim mismatch at index {k}: {len(s.embedding)} vs {dim}")
    return np.asarray([s.embedding for s in segments], dtype=np.float64)


def _snap_to_pause(segments: Sequence[Segment], i: int, radius: int) -> int:
    """The segment within ``radius`` of ``i`` that follows the longest silence."""
    lo, hi = max(1, i - radius), min(len(segments) - 1, i + radius)
    if lo > hi:
        return i
    return max(
        range(lo, hi + 1),
        key=lambda j: (segments[j].start_sec - segments[j - 1].end_sec, -abs(j - i)),
    )


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
            raise ValueError(f"embedding dim mismatch at index {k}: {len(emb)} vs {dim}")
        for j, v in enumerate(emb):
            out[j] += v
    n = hi - lo
    return [v / n for v in out]
