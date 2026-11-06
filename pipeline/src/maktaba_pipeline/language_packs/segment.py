"""Splitting a sentence into tokens with code-point offsets (Epic 52, story 52.3).

One deterministic splitter the packs share: a word is a run of letters and the combining
marks on them (harakat stay with their letter), a number a run of digits (``10:30``,
``3.5``, ``3rd``), anything else that is not a space one punctuation or symbol token. A
pack may join more characters into words (``joiners``: an apostrophe or hyphen between
letters, Hunspell's ``WORDCHARS``). The tokens and the spaces between them give back the
text exactly. PyICU's word break (UAX #29) is used for scripts written without spaces when
it is installed (:func:`icu_tokens`).
"""

from __future__ import annotations

import importlib.util
import unicodedata

from .base import Token, TokenKind
from .scripts import COMMON, INHERITED, script_of

__all__ = ["HAS_ICU", "icu_tokens", "split"]

HAS_ICU = importlib.util.find_spec("icu") is not None

#: tatweel and the zero-width joiners stay inside a word
_IN_WORD = frozenset("ـ‌‍")
#: between two digits, still one number
_IN_NUMBER = frozenset(".,:٫٬/")


def _is_letter(ch: str) -> bool:
    cat = unicodedata.category(ch)
    return cat.startswith("L") or cat in ("Mn", "Mc", "Me") or ch in _IN_WORD


def _is_digit(ch: str) -> bool:
    return unicodedata.category(ch) == "Nd"


def _script(text: str) -> str:
    for ch in text:
        s = script_of(ch)
        if s not in (COMMON, INHERITED):
            return s
    return COMMON


def split(text: str, joiners: str = "", offset: int = 0) -> list[Token]:
    """``text``'s tokens (offsets plus ``offset``: a run's place in its sentence)."""
    out: list[Token] = []
    i, n = 0, len(text)
    join = frozenset(joiners)
    while i < n:
        ch = text[i]
        if ch.isspace():
            i += 1
            continue
        j = i
        kind: TokenKind
        if _is_digit(ch):
            j += 1
            while j < n and (
                _is_digit(text[j])
                or (text[j] in _IN_NUMBER and j + 1 < n and _is_digit(text[j + 1]))
            ):
                j += 1
            # an ordinal or unit glued on (3rd, 10th, 5kg) stays the number's
            while j < n and _is_letter(text[j]) and not _is_letter_of_other_script(text, i, j):
                j += 1
            kind = "number"
        elif _is_letter(ch):
            first = script_of(ch)
            j += 1
            while j < n and (
                (_is_letter(text[j]) and _same_script(first, text[j]))
                or (text[j] in join and j + 1 < n and _is_letter(text[j + 1]))
            ):
                j += 1
            kind = "word"
        else:
            j += 1
            while j < n and unicodedata.category(text[j]) in ("Mn", "Me"):
                j += 1  # a mark on a symbol stays with it
            kind = "symbol" if unicodedata.category(ch).startswith("S") else "punct"
        piece = text[i:j]
        out.append(Token(piece, offset + i, offset + j, kind, _script(piece)))
        i = j
    return out


def _same_script(first: str, ch: str) -> bool:
    """A word never crosses from one script into another (Japanese kana into kanji, an
    Arabic word glued to a Latin one): without ICU that is the best word break there is."""
    s = script_of(ch)
    return s in (INHERITED, COMMON) or s == first or first in (INHERITED, COMMON)


def _is_letter_of_other_script(text: str, start: int, j: int) -> bool:
    """A letter after digits is part of the number only in a Latin ordinal or unit."""
    return script_of(text[j]) not in ("Latn", INHERITED)


def icu_tokens(text: str, locale: str, offset: int = 0) -> list[Token] | None:
    """UAX #29 word breaks through PyICU (offsets in code points), or None without it."""
    if not HAS_ICU:
        return None
    import icu  # type: ignore[import-not-found,import-untyped,unused-ignore]

    bi = icu.BreakIterator.createWordInstance(icu.Locale(locale))
    bi.setText(text)
    # ICU speaks UTF-16: map its offsets back to code points
    u16: list[int] = []
    for k, ch in enumerate(text):
        u16.extend([k] * (2 if ord(ch) > 0xFFFF else 1))
    u16.append(len(text))
    out: list[Token] = []
    prev = 0
    for b in bi:
        a, z = u16[prev], u16[int(b)]
        prev = int(b)
        piece = text[a:z]
        if not piece or piece.isspace():
            continue
        if any(_is_digit(c) for c in piece) and not any(_is_letter(c) for c in piece):
            kind: TokenKind = "number"
        elif any(_is_letter(c) for c in piece):
            kind = "word"
        else:
            kind = "symbol" if unicodedata.category(piece[0]).startswith("S") else "punct"
        out.append(Token(piece, offset + a, offset + z, kind, _script(piece)))
    return out
