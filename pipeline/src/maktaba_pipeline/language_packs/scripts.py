"""A sentence split into script runs (Epic 52, story 52.3): Latin inside Arabic goes to
the Latin pack, Arabic to the Arabic one.

Python's :mod:`unicodedata` has no Script property, so the scripts word review meets are
named by code-point blocks (ISO 15924 codes); every other letter is ``Zzzz`` (unknown,
the fallback's). Common characters (digits, punctuation, spaces) and Inherited ones
(combining marks: harakat) join the run around them, so offsets stay those of the whole
sentence.
"""

from __future__ import annotations

import unicodedata
from dataclasses import dataclass

__all__ = ["COMMON", "INHERITED", "Run", "runs", "script_of"]

COMMON = "Zyyy"
INHERITED = "Zinh"

#: (first, last, script): the blocks of the languages Whisper hears most
_BLOCKS: tuple[tuple[int, int, str], ...] = (
    (0x0041, 0x005A, "Latn"),
    (0x0061, 0x007A, "Latn"),
    (0x00C0, 0x024F, "Latn"),
    (0x1E00, 0x1EFF, "Latn"),
    (0x0370, 0x03FF, "Grek"),
    (0x0400, 0x052F, "Cyrl"),
    (0x0590, 0x05FF, "Hebr"),
    (0x0600, 0x06FF, "Arab"),
    (0x0750, 0x077F, "Arab"),
    (0x08A0, 0x08FF, "Arab"),
    (0xFB50, 0xFDFF, "Arab"),
    (0xFE70, 0xFEFF, "Arab"),
    (0x0900, 0x097F, "Deva"),
    (0x0980, 0x09FF, "Beng"),
    (0x0E00, 0x0E7F, "Thai"),
    (0x10A0, 0x10FF, "Geor"),
    (0x0530, 0x058F, "Armn"),
    (0x1100, 0x11FF, "Hang"),
    (0xAC00, 0xD7AF, "Hang"),
    (0x3040, 0x309F, "Hira"),
    (0x30A0, 0x30FF, "Kana"),
    (0x4E00, 0x9FFF, "Hani"),
    (0x3400, 0x4DBF, "Hani"),
)


def script_of(ch: str) -> str:
    cat = unicodedata.category(ch)
    if cat in ("Mn", "Me"):
        return INHERITED
    cp = ord(ch)
    if not cat.startswith("L"):
        # Arabic punctuation and digits are Common too (، ؛ ؟ ٠-٩)
        if 0x0600 <= cp <= 0x06FF and cat.startswith("S"):
            return "Arab"  # ﷺ-like signs of the block stay with it
        if 0xFDF0 <= cp <= 0xFDFF:
            return "Arab"  # the honorific ligatures ﷺ ﷻ
        return COMMON
    for a, b, script in _BLOCKS:
        if a <= cp <= b:
            return script
    return "Zzzz"


@dataclass(frozen=True, slots=True)
class Run:
    script: str
    start: int
    end: int


def runs(text: str) -> list[Run]:
    """``text`` as maximal runs of one script, Common and Inherited characters joined to
    the run before them (or after, at the start). A text of only Common characters is one
    ``Zyyy`` run."""
    out: list[Run] = []
    cur: str | None = None
    start = 0
    lead = 0  # Common characters before the first script
    for i, ch in enumerate(text):
        s = script_of(ch)
        if s in (COMMON, INHERITED):
            continue
        if cur is None:
            cur, start = s, lead
            continue
        if s != cur:
            # punctuation after a word stays with its run; the spaces before the next
            # script's first letter go with that run
            cut = i
            while cut > start and script_of(text[cut - 1]) == COMMON and text[cut - 1].isspace():
                cut -= 1
            out.append(Run(cur, start, cut))
            cur, start = s, cut
    if cur is None:
        return [Run(COMMON, 0, len(text))] if text else []
    out.append(Run(cur, start, len(text)))
    return out
