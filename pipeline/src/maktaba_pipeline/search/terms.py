"""Keyword search's terms dictionary (Epic 49, story 49.8): a document's words.

A word's *key* is what search matches: the text folded as ``maktaba_normalize()`` folds it
(:mod:`.textnorm`), lower case, Latin accents removed. Its *shown* form is the word as the
transcript writes it, with harakat and tatweel removed: a suggestion shows مدرسة, whose key
is مدرسه. Go's ``api/internal/searchterms`` is the same, held equal by a test on shared
cases (``shared/testdata/searchterms.json``).

A word is a run of letters and digits (``str.isalnum``; Go: ``unicode.IsLetter`` or
``unicode.IsNumber``) of 2 to 40 characters, not digits only.
"""

from __future__ import annotations

import unicodedata
from collections import Counter

from .textnorm import normalize

__all__ = ["MAX_LEN", "MIN_LEN", "doc_terms", "key", "speaker_keys", "words"]

MIN_LEN = 2
MAX_LEN = 40

# harakat (U+064B-U+0652), superscript alef (U+0670), tatweel (U+0640): what a shown form drops
_MARKS = {c: None for c in range(0x064B, 0x0653)}
_MARKS[0x0670] = None
_MARKS[0x0640] = None


def _strip_accents(s: str) -> str:
    d = unicodedata.normalize("NFD", s)
    return unicodedata.normalize("NFC", "".join(c for c in d if unicodedata.category(c) != "Mn"))


def key(word: str) -> str:
    """The word's key: folded, lower case, without Latin accents."""
    return _strip_accents(normalize(unicodedata.normalize("NFC", word)).lower())


def words(text: str) -> list[str]:
    """The words of ``text`` as written (harakat and tatweel removed), in order."""
    s = unicodedata.normalize("NFC", text).translate(_MARKS)
    out: list[str] = []
    cur: list[str] = []
    for ch in s + " ":
        if ch.isalnum():
            cur.append(ch)
            continue
        if cur:
            w = "".join(cur)
            cur = []
            if MIN_LEN <= len(w) <= MAX_LEN and not w.isdigit():
                out.append(w)
    return out


def doc_terms(*texts: str) -> dict[str, str]:
    """Each key of the texts' words, with its most frequent shown form (ties: the first)."""
    seen: dict[str, Counter[str]] = {}
    for t in texts:
        for w in words(t):
            k = key(w)
            if MIN_LEN <= len(k) <= MAX_LEN:
                seen.setdefault(k, Counter())[w.lower()] += 1
    return {k: c.most_common(1)[0][0] for k, c in seen.items()}


def speaker_keys(speakers: str) -> set[tuple[str, str]]:
    """Each named speaker (one a line) by each word start of the name: (key, name), so
    الشيخ أحمد is found by الشي and by أحم (``search_speakers``)."""
    out: set[tuple[str, str]] = set()
    for line in speakers.splitlines():
        name = " ".join(line.split())
        parts = name.split(" ")
        for i in range(len(parts)):
            k = key(" ".join(parts[i:]))
            if k:
                out.add((k[:200], name))
    return out
