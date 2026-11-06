"""Hypothesis words against a person's reference (Epic 52, story 52.2).

Words are aligned by Levenshtein over the pack's ``key()``, so a difference only in hamza,
«ة», «ى» or harakat aligns as the same word, labelled ``spelling``; another word is a
``substitution``; a hypothesis word with no reference word an ``insertion``. Reference
words the hypothesis lacks are deletions: they cannot be flagged at a word, so they count
against recall through the nearest hypothesis word (within one word).
"""

from __future__ import annotations

from collections.abc import Callable, Sequence
from dataclasses import dataclass

__all__ = ["Aligned", "align"]

LABELS = ("ok", "spelling", "substitution", "insertion")


@dataclass(frozen=True, slots=True)
class Aligned:
    #: one label per hypothesis word
    labels: tuple[str, ...]
    #: per hypothesis word: deletions next to it (reference words missing beside it)
    deletions_near: tuple[int, ...]
    deletions: int


def align(hyp: Sequence[str], ref: Sequence[str], key: Callable[[str], str]) -> Aligned:
    a = [key(w) for w in hyp]
    b = [key(w) for w in ref]
    n, m = len(a), len(b)
    # cost table: substitution of unequal keys 1, equal 0; insert / delete 1
    d = [[0] * (m + 1) for _ in range(n + 1)]
    for i in range(n + 1):
        d[i][0] = i
    for j in range(m + 1):
        d[0][j] = j
    for i in range(1, n + 1):
        for j in range(1, m + 1):
            d[i][j] = min(
                d[i - 1][j] + 1,
                d[i][j - 1] + 1,
                d[i - 1][j - 1] + (0 if a[i - 1] == b[j - 1] else 1),
            )
    labels = ["ok"] * n
    near = [0] * n
    dels = 0
    i, j = n, m
    while i > 0 or j > 0:
        if i > 0 and j > 0 and d[i][j] == d[i - 1][j - 1] + (0 if a[i - 1] == b[j - 1] else 1):
            if a[i - 1] != b[j - 1]:
                labels[i - 1] = "substitution"
            elif hyp[i - 1] != ref[j - 1]:
                labels[i - 1] = "spelling"
            i, j = i - 1, j - 1
        elif i > 0 and d[i][j] == d[i - 1][j] + 1:
            labels[i - 1] = "insertion"
            i -= 1
        else:
            dels += 1
            if n:
                near[min(max(i - 1, 0), n - 1)] += 1
            j -= 1
    return Aligned(tuple(labels), tuple(near), dels)
