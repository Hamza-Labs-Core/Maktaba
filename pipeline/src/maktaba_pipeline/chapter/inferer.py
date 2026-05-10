"""End-to-end chapter inferer: embeddings → row-ready chapter list.

Combines the boundary detector and the minimum-length merger into a
single :meth:`ChapterInferer.infer` entry point. Output rows always
start at second zero and end at the video duration; intermediate
chapter boundaries are emitted in seq order.
"""

from __future__ import annotations

from collections import Counter
from collections.abc import Sequence
from dataclasses import dataclass

from .boundary import detect_boundaries
from .merge import enforce_min_chapter_sec

__all__ = ["ChapterInferer", "ChapterRow"]


@dataclass(frozen=True, slots=True)
class ChapterRow:
    """One ``chapters`` row, ready to be inserted.

    ``seq`` is 0-based per video (chapter 0 is always the opening
    chapter starting at second zero). ``title`` is intentionally
    optional — title generation is a separate Story 5.7 follow-up
    and is left empty here.
    """

    video_id: str
    transcript_id: int
    seq: int
    start_sec: float
    end_sec: float
    lang: str | None
    confidence: float | None
    title: str | None = None
    source: str = "inferred"


def _detect_lang(units: Sequence[dict[str, object]]) -> str | None:
    """Pick a language tag from the first 3 units.

    Returns ``"mixed"`` when 2+ distinct languages appear in the
    sample; the first language otherwise. Returns ``None`` only when
    every sampled unit lacks a ``language`` value.
    """
    head = list(units[:3])
    langs: list[str] = []
    for unit in head:
        lang = unit.get("language")
        if isinstance(lang, str) and lang:
            langs.append(lang)
    if not langs:
        return None
    counts = Counter(langs)
    if len(counts) >= 2:
        return "mixed"
    return langs[0]


class ChapterInferer:
    """Builds the final chapter list from embeddings + unit metadata.

    The inferer is stateless; one instance can serve many transcripts
    concurrently. All knobs live on the instance so they can be
    swapped via configuration without changing the call site.
    """

    def __init__(
        self,
        *,
        threshold: float = 0.35,
        smoothing_window: int = 3,
        min_chapter_sec: float = 180.0,
    ) -> None:
        self._threshold = threshold
        self._smoothing_window = smoothing_window
        self._min_chapter_sec = min_chapter_sec

    def infer(
        self,
        *,
        video_id: str,
        transcript_id: int,
        units: Sequence[dict[str, object]],
        embeddings: Sequence[Sequence[float]],
        video_duration_sec: float,
    ) -> list[ChapterRow]:
        """Run boundary detect + min-length merge and emit rows.

        Returns a chronologically ordered list of :class:`ChapterRow`
        starting at second zero. When the inputs cannot be chaptered
        (no units, or no embeddings), a single row spanning the whole
        video is returned so consumers always have at least chapter 0.
        """
        if not units:
            return [
                ChapterRow(
                    video_id=video_id,
                    transcript_id=transcript_id,
                    seq=0,
                    start_sec=0.0,
                    end_sec=video_duration_sec,
                    lang=None,
                    confidence=1.0,
                )
            ]

        unit_starts: list[float] = []
        for u in units:
            raw = u.get("start_sec", 0.0)
            if isinstance(raw, (int, float)):
                unit_starts.append(float(raw))
            else:
                unit_starts.append(0.0)
        lang = _detect_lang(units)

        raw = detect_boundaries(
            embeddings,
            threshold=self._threshold,
            smoothing_window=self._smoothing_window,
        )
        merged = enforce_min_chapter_sec(
            raw,
            unit_starts,
            min_chapter_sec=self._min_chapter_sec,
        )

        chapter_starts: list[tuple[float, float | None]] = [(0.0, 1.0)]
        for idx, dist in merged:
            chapter_starts.append((unit_starts[idx], _confidence_from_distance(dist)))

        rows: list[ChapterRow] = []
        for i, (start, conf) in enumerate(chapter_starts):
            end = (
                chapter_starts[i + 1][0]
                if i + 1 < len(chapter_starts)
                else video_duration_sec
            )
            rows.append(
                ChapterRow(
                    video_id=video_id,
                    transcript_id=transcript_id,
                    seq=i,
                    start_sec=start,
                    end_sec=end,
                    lang=lang,
                    confidence=conf,
                )
            )
        return rows


def _confidence_from_distance(dist: float) -> float:
    """Map a cosine distance in [0, 2] to a confidence in [0, 1].

    A distance of 1.0 (orthogonal) maps to 0.5; the formula keeps
    monotonicity and clamps to [0, 1] so consumers can compare
    confidences across chapters meaningfully.
    """
    score = dist / 2.0
    if score < 0.0:
        return 0.0
    if score > 1.0:
        return 1.0
    return score
