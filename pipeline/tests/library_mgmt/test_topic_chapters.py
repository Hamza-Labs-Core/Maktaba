"""Story 9.18 — chapters at relative topic shifts (what the engine chapter_infer runs)."""

from __future__ import annotations

import math
import random

import pytest

pytest.importorskip("numpy")

from maktaba_pipeline.library_mgmt.chapter import (
    InferredChapter,
    Segment,
    extractive_title,
    infer_topic_chapters,
)

DIM = 16


def _topic(i: int) -> list[float]:
    """A unit vector per topic; every topic shares a strong common component, as e5's
    vectors do (adjacent windows are all > 0.95 similar)."""
    v = [1.0] + [0.0] * (DIM - 1)
    v[1 + i] = 0.35
    n = math.sqrt(sum(x * x for x in v))
    return [x / n for x in v]


def _lecture(
    topics: list[int],
    per_topic: int,
    *,
    step: float = 8.0,
    pause_at: dict[int, float] | None = None,
) -> list[Segment]:
    """``per_topic`` segments per topic in order; ``pause_at[i]`` silence before segment i."""
    rng = random.Random(7)
    segs: list[Segment] = []
    t = 0.0
    k = 0
    for topic in topics:
        base = _topic(topic)
        for _ in range(per_topic):
            t += (pause_at or {}).get(k, 0.3)
            vec = [x + rng.gauss(0.0, 0.01) for x in base]
            segs.append(Segment(t, t + step, vec, f"كلام في الموضوع {topic} رقم {k}"))
            t += step
            k += 1
    return segs


@pytest.mark.unit
def test_boundaries_land_on_the_topic_changes() -> None:
    segs = _lecture([0, 1, 2], per_topic=60)  # three ~8 minute subjects
    chapters = infer_topic_chapters(segs)
    assert len(chapters) == 3
    starts = [c.start_sec for c in chapters]
    assert starts[0] == segs[0].start_sec
    assert abs(starts[1] - segs[60].start_sec) <= 3 * 8.3
    assert abs(starts[2] - segs[120].start_sec) <= 3 * 8.3
    assert chapters[-1].end_sec == segs[-1].end_sec
    # contiguous spans, in order
    assert all(a.end_sec == b.start_sec for a, b in zip(chapters, chapters[1:], strict=False))
    assert [c.seq for c in chapters] == [0, 1, 2]


@pytest.mark.unit
def test_a_boundary_moves_to_the_nearest_long_pause() -> None:
    segs = _lecture([0, 1], per_topic=60, pause_at={62: 4.0})
    chapters = infer_topic_chapters(segs)
    assert len(chapters) == 2
    assert chapters[1].start_sec == segs[62].start_sec


@pytest.mark.unit
def test_one_subject_or_a_short_video_is_one_chapter() -> None:
    same = _lecture([0], per_topic=120)
    assert len(infer_topic_chapters(same)) == 1
    short = _lecture([0, 1], per_topic=8)
    only = infer_topic_chapters(short)
    assert len(only) == 1 and only[0].start_sec == short[0].start_sec
    assert infer_topic_chapters([]) == []


@pytest.mark.unit
def test_chapters_are_never_shorter_than_the_minimum() -> None:
    # the subject changes every 2 minutes; with a low bar every change is a candidate,
    # but chapters stay >= 4 minutes apart
    segs = _lecture([0, 1, 2, 3, 4, 5, 6, 7], per_topic=15)
    chapters = infer_topic_chapters(segs, window_segments=5, z=0.5, min_chapter_sec=240.0)
    assert len(chapters) >= 2
    assert all(c.end_sec - c.start_sec >= 240.0 for c in chapters)
    # an even rhythm of changes has no change that stands out: no chapters by default
    assert len(infer_topic_chapters(segs)) == 1


@pytest.mark.unit
def test_dimension_mismatch_is_an_error() -> None:
    segs = _lecture([0, 1], per_topic=30)
    segs[5] = Segment(segs[5].start_sec, segs[5].end_sec, [1.0, 0.0], "x")
    with pytest.raises(ValueError, match="dim mismatch"):
        infer_topic_chapters(segs)


@pytest.mark.unit
def test_title_is_the_leading_sentence_closest_to_the_chapter() -> None:
    v = _topic(0)
    off = _topic(5)
    segs = [
        Segment(0.0, 4.0, off, "نعم"),  # too short
        Segment(4.0, 9.0, off, "سؤال من الأخ عن شيء آخر تماما"),
        Segment(9.0, 15.0, v, "المسح على الجبيرة وأحكامه"),
        Segment(15.0, 20.0, v, "الجبيرة ما يوضع على الكسر"),
        Segment(20.0, 25.0, v, "ويمسح عليها في الحدثين"),
    ]
    chapter = InferredChapter(seq=0, start_sec=0.0, end_sec=25.0)
    assert extractive_title(chapter, segs) in {
        "المسح على الجبيرة وأحكامه",
        "الجبيرة ما يوضع على الكسر",
        "ويمسح عليها في الحدثين",
    }


@pytest.mark.unit
def test_title_is_cut_at_a_word_and_none_without_a_sentence() -> None:
    v = _topic(0)
    long = "كلمة " * 40
    segs = [Segment(0.0, 5.0, v, long)]
    chapter = InferredChapter(seq=0, start_sec=0.0, end_sec=5.0)
    title = extractive_title(chapter, segs, max_chars=30)
    assert title is not None and title.endswith("…") and len(title) <= 30
    assert "  " not in title and not title.startswith(" ")
    assert extractive_title(chapter, [Segment(0.0, 5.0, v, "نعم")]) is None
    assert extractive_title(InferredChapter(seq=1, start_sec=50.0, end_sec=60.0), segs) is None


def _reference(
    segments: list[Segment], *, w: int = 10, z: float = 2.5, min_sec: float = 240.0, radius: int = 3
) -> list[tuple[float, float]]:
    """The plain-Python statement of infer_topic_chapters (the first build), to hold the
    numpy version to the same boundaries."""

    def cos(a: list[float], b: list[float]) -> float:
        na = math.sqrt(sum(x * x for x in a))
        nb = math.sqrt(sum(x * x for x in b))
        if na == 0 or nb == 0:
            return 0.0
        return sum(x * y for x, y in zip(a, b, strict=True)) / (na * nb)

    def wsum(lo: int, hi: int) -> list[float]:
        return [sum(col) for col in zip(*(s.embedding for s in segments[lo:hi]), strict=True)]

    n = len(segments)
    pos = list(range(w, n - w + 1))
    drops = [1.0 - cos(wsum(i - w, i), wsum(i, i + w)) for i in pos]
    mean = sum(drops) / len(drops)
    std = math.sqrt(sum((d - mean) ** 2 for d in drops) / len(drops))
    peaks = sorted(
        (
            (drops[k], pos[k])
            for k in range(len(drops))
            if drops[k] >= mean + z * std
            and (k == 0 or drops[k] >= drops[k - 1])
            and (k == len(drops) - 1 or drops[k] >= drops[k + 1])
        ),
        key=lambda p: (-p[0], p[1]),
    )
    first, last = segments[0].start_sec, segments[-1].end_sec
    chosen: list[int] = []
    for _, i in peaks:
        lo, hi = max(1, i - radius), min(n - 1, i + radius)
        i = max(
            range(lo, hi + 1),
            key=lambda j: (segments[j].start_sec - segments[j - 1].end_sec, -abs(j - i)),
        )
        t = segments[i].start_sec
        if t - first < min_sec or last - t < min_sec:
            continue
        if all(abs(t - segments[j].start_sec) >= min_sec for j in chosen):
            chosen.append(i)
    cuts = [segments[i].start_sec for i in sorted(chosen)]
    edges = [first, *cuts, last]
    return list(zip(edges, edges[1:], strict=False))


@pytest.mark.unit
@pytest.mark.parametrize("seed", [1, 2, 3, 4, 5])
def test_numpy_version_matches_the_reference(seed: int) -> None:
    rng = random.Random(seed)
    topics = [rng.randrange(6) for _ in range(rng.randrange(3, 7))]
    pauses = {rng.randrange(40, 200): rng.uniform(1.0, 4.0) for _ in range(8)}
    segs = _lecture(topics, per_topic=rng.randrange(35, 70), pause_at=pauses)
    got = [(c.start_sec, c.end_sec) for c in infer_topic_chapters(segs)]
    assert got == _reference(segs)
