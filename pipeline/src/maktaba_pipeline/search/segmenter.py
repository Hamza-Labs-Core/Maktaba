"""Sentence segmentation that preserves segment-id provenance.

Concatenates segment texts with a single space separator while
tracking a character → segment-id map; splits the joined text on
sentence-end punctuation (``.``, ``!``, ``?``, Arabic ``؟``, Devanagari
``।``) and ``\\n+``; for each sentence reports start/end seconds and
the unique source segment ids that contributed at least one char.

Punctuation is kept with the preceding sentence so downstream
snippet rendering reads naturally.
"""

from __future__ import annotations

from collections.abc import Sequence

import regex  # type: ignore[import-untyped]

from .models import SegmentRow, Sentence

__all__ = ["SENTENCE_END_RE", "split_into_sentences"]


# Multi-script sentence terminators. Arabic uses U+061F (reversed ?),
# Devanagari uses U+0964. The class also matches LF runs because raw
# ASR output sometimes contains line breaks between utterances.
SENTENCE_END_RE = regex.compile(r"[.!?؟।]+|\n+")


def split_into_sentences(segments: Sequence[SegmentRow]) -> list[Sentence]:
    """Split ``segments`` into sentence-bounded :class:`Sentence`\\ s.

    Algorithm:

    1. Walk segments in input order, appending each segment's text to a
       single buffer separated by ``" "``. Record ``(start, end)`` char
       offsets per segment so we can map a sentence span back to the
       set of contributing segments.
    2. Apply :data:`SENTENCE_END_RE` to the joined buffer; the matched
       punctuation stays with the *preceding* sentence (closed at
       ``match.end()``).
    3. For each sentence span, collect every segment whose char range
       overlaps the span. Sentence start_sec = first segment's
       start_sec; end_sec = last overlapping segment's end_sec.
    4. Drop sentences whose text is whitespace-only.

    Empty input → empty output. Single-sentence input (no terminator)
    yields one :class:`Sentence` covering the whole input.
    """
    if not segments:
        return []

    parts: list[str] = []
    # spans[i] is (char_start, char_end) for segments[i] in the joined
    # buffer. End is exclusive.
    spans: list[tuple[int, int]] = []
    cursor = 0
    for idx, seg in enumerate(segments):
        if idx > 0:
            parts.append(" ")
            cursor += 1
        start = cursor
        parts.append(seg.text)
        cursor += len(seg.text)
        spans.append((start, cursor))

    joined = "".join(parts)

    # Build sentence boundaries from the regex matches. A sentence
    # spans from the previous boundary (or 0) up to and including the
    # matched terminator.
    boundaries: list[tuple[int, int]] = []
    prev = 0
    for m in SENTENCE_END_RE.finditer(joined):
        end = m.end()
        boundaries.append((prev, end))
        prev = end
    if prev < len(joined):
        boundaries.append((prev, len(joined)))

    out: list[Sentence] = []
    for span_start, span_end in boundaries:
        raw = joined[span_start:span_end].strip()
        if not raw:
            continue

        # Collect contributing segments (overlap test). Preserves the
        # order they appear in `segments`, then dedupes by id while
        # keeping first-seen order.
        contributing: list[SegmentRow] = []
        seen: set[int] = set()
        for seg, (s_a, s_b) in zip(segments, spans, strict=True):
            if s_a >= span_end or s_b <= span_start:
                continue
            if seg.id in seen:
                continue
            seen.add(seg.id)
            contributing.append(seg)
        if not contributing:
            # Defensive — should not happen given the overlap math,
            # but if a sentence ends up made of pure separator chars
            # we drop it rather than emit a phantom row.
            continue

        out.append(
            Sentence(
                text=raw,
                start_sec=contributing[0].start_sec,
                end_sec=contributing[-1].end_sec,
                segment_ids=tuple(seg.id for seg in contributing),
            )
        )
    return out
