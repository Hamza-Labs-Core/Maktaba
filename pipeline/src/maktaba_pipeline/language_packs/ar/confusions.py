"""Letters Whisper confuses in Arabic speech (Epic 52, story 52.4): a dictionary word one
of these swaps away is a likely spelling."""

from __future__ import annotations

__all__ = ["CONFUSABLE", "one_swap"]

_GROUPS = ("ذزظ", "ثسص", "ضدظ", "قكء", "حه", "عءا", "تط")

CONFUSABLE: dict[str, str] = {}
for g in _GROUPS:
    for ch in g:
        CONFUSABLE[ch] = CONFUSABLE.get(ch, "") + "".join(c for c in g if c != ch)


def one_swap(word: str) -> list[str]:
    """Every spelling of ``word`` with one confusable letter swapped, in a fixed order."""
    out: list[str] = []
    for i, ch in enumerate(word):
        for other in CONFUSABLE.get(ch, ""):
            cand = word[:i] + other + word[i + 1 :]
            if cand not in out:
                out.append(cand)
    return out
