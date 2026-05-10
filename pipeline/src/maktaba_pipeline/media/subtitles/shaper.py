"""Segment → Cue shapers.

Two production shapers ship today:

- :class:`PassThroughShaper` — one cue per segment, no rewrapping.
  Used for languages whose tokens are already short or whose punctuation
  the ASR has already split sensibly.
- :class:`WrappingShaper` — greedy grapheme-aware line wrapping at
  ``max_line_chars`` per line and ``max_lines`` per cue. Long single
  tokens fall onto their own line rather than being mid-word split.

The protocol :class:`CueShaper` keeps the door open for richer
shapers (sentence-aware, punctuation-aware, CPS-bounded) in later
stories without changing the stage handler.
"""

from __future__ import annotations

from collections.abc import Iterable, Iterator
from typing import Protocol

import regex  # type: ignore[import-untyped]

from .cue import Cue, Segment

__all__ = [
    "CueShaper",
    "PassThroughShaper",
    "WrappingShaper",
    "default_shaper",
]


# ``\X`` matches one extended grapheme cluster — what humans count as a
# "character". Falls back to one code point on legacy Python regex
# engines; ``regex>=2024`` supports the full Unicode 15 set.
_GRAPHEME_RE = regex.compile(r"\X")


def _grapheme_count(s: str) -> int:
    """Count grapheme clusters (not code points) in ``s``."""
    return len(_GRAPHEME_RE.findall(s))


class CueShaper(Protocol):
    """Turn segments into cues. The stage handler calls ``shape`` once."""

    def shape(
        self,
        segments: Iterable[Segment],
        *,
        language: str,
    ) -> Iterator[Cue]: ...


class PassThroughShaper:
    """One segment in, one cue out. No wrapping, no splitting."""

    def shape(
        self,
        segments: Iterable[Segment],
        *,
        language: str,  # noqa: ARG002 — required by protocol
    ) -> Iterator[Cue]:
        for seg in segments:
            yield Cue(
                start_sec=seg.start_sec,
                end_sec=seg.end_sec,
                lines=(seg.text,),
                speaker=seg.speaker,
                cue_id=f"seq-{seg.seq}",
            )


class WrappingShaper:
    """Greedy word-wrap that respects grapheme counts.

    The wrap algorithm:

    1. Split the segment text on whitespace into tokens.
    2. Build lines greedily, appending a token only if the resulting
       line is ``<= max_line_chars`` graphemes wide.
    3. If a single token is already wider than ``max_line_chars``,
       place it on its own line (no mid-word break).
    4. Take the first ``max_lines`` lines; anything past that is
       dropped on the floor (the segment is likely too dense to
       display anyway).
    """

    def __init__(self, *, max_line_chars: int = 42, max_lines: int = 2) -> None:
        if max_line_chars < 1:
            raise ValueError("max_line_chars must be >= 1")
        if max_lines < 1:
            raise ValueError("max_lines must be >= 1")
        self.max_line_chars = max_line_chars
        self.max_lines = max_lines

    def _wrap(self, text: str) -> tuple[str, ...]:
        tokens = text.split()
        if not tokens:
            return ("",)

        lines: list[str] = []
        current = ""
        for tok in tokens:
            if current == "":
                current = tok
                continue
            candidate = f"{current} {tok}"
            if _grapheme_count(candidate) <= self.max_line_chars:
                current = candidate
            else:
                lines.append(current)
                current = tok
        if current:
            lines.append(current)

        # Truncate to max_lines. A future story may merge trailing
        # tokens into the last visible line; today we just clip.
        return tuple(lines[: self.max_lines])

    def shape(
        self,
        segments: Iterable[Segment],
        *,
        language: str,  # noqa: ARG002 — required by protocol
    ) -> Iterator[Cue]:
        for seg in segments:
            yield Cue(
                start_sec=seg.start_sec,
                end_sec=seg.end_sec,
                lines=self._wrap(seg.text),
                speaker=seg.speaker,
                cue_id=f"seq-{seg.seq}",
            )


def default_shaper(*, max_line_chars: int = 42, max_lines: int = 2) -> CueShaper:
    """Return the default production shaper.

    Currently :class:`WrappingShaper` — the parameters match the
    SDH-friendly defaults used by most consumer players (42 chars
    per line, 2 lines per cue).
    """
    return WrappingShaper(max_line_chars=max_line_chars, max_lines=max_lines)
