"""Which Quran verse a title quotes (Story 27.11, display titles).

Lesson titles quote verses: «تفسير قوله تعالى: ﴿يا أيها الذين آمنوا كتب عليكم الصيام﴾»,
«{فَمَنْ يَعْمَلْ مِثْقَالَ ذَرَّةٍ خَيْرًا يَرَهُ}». :func:`find_quotes` finds the quoted spans
(``﴿…﴾``, ``{…}``, and brackets / quotes / bare words after «قوله تعالى»), and
:func:`locate` matches the quoted words against the Quran text to give the surah and
verse(s): «البقرة ١٨٣» / "Al-Baqara 2:183".

The text is the bundled Tanzil "Simple Clean" text (``data/quran``, CC BY 3.0, verbatim
with its notice). Matching is on folded words (no tashkeel, alef / ya / ta marbuta forms
unified): every word of the quote must match a run of the text, starting anywhere in a
verse and possibly running into the next ones; a quote that matches more than one place
gives no reference. The index (the text as one word stream, keyed by its first two words)
is built once, lazily.
"""

from __future__ import annotations

import json
import re
from dataclasses import dataclass
from functools import lru_cache
from pathlib import Path

from ..text.arabic import fold

__all__ = ["Quote", "VerseRef", "find_quotes", "locate", "surah_by_name"]

_DATA = Path(__file__).resolve().parent.parent / "data" / "quran"
_BASMALA = tuple(fold("بسم الله الرحمن الرحيم").split())
_AR_DIGITS = str.maketrans("0123456789", "٠١٢٣٤٥٦٧٨٩")


@dataclass(frozen=True, slots=True)
class VerseRef:
    surah: int
    first: int
    last: int
    name_ar: str
    name_en: str

    def label(self, arabic: bool = True) -> str:
        """«البقرة ١٨٣» / «البقرة ١٨٣–١٨٤»; "Al-Baqara 2:183" / "Al-Baqara 2:183-184"."""
        if arabic:
            ayas = str(self.first) if self.first == self.last else f"{self.first}–{self.last}"
            return f"{self.name_ar} {ayas.translate(_AR_DIGITS)}"
        ayas = str(self.first) if self.first == self.last else f"{self.first}-{self.last}"
        return f"{self.name_en} {self.surah}:{ayas}"


@dataclass(frozen=True, slots=True)
class _Index:
    words: tuple[str, ...]
    #: (surah, aya) of every word
    where: tuple[tuple[int, int], ...]
    by_two: dict[tuple[str, str], tuple[int, ...]]
    by_one: dict[str, tuple[int, ...]]
    names: tuple[tuple[str, str, int], ...]  # (ar, en, ayas) by surah - 1


@lru_cache(maxsize=1)
def _index() -> _Index:
    meta = json.loads((_DATA / "surahs.json").read_text(encoding="utf-8"))
    names = tuple((s["ar"], s["en"], int(s["ayas"])) for s in meta["surahs"])
    words: list[str] = []
    where: list[tuple[int, int]] = []
    for line in (_DATA / "quran-simple-clean.txt").read_text(encoding="utf-8").splitlines():
        if not line or line.startswith("#"):
            continue
        s, a, text = line.split("|", 2)
        sura, aya = int(s), int(a)
        toks = fold(text).split()
        if aya == 1 and sura != 1 and tuple(toks[:4]) == _BASMALA:
            toks = toks[4:]  # the text puts the basmala before each surah's first verse
        for t in toks:
            words.append(t)
            where.append((sura, aya))
    two: dict[tuple[str, str], list[int]] = {}
    one: dict[str, list[int]] = {}
    for i, w in enumerate(words):
        one.setdefault(w, []).append(i)
        if i + 1 < len(words):
            two.setdefault((w, words[i + 1]), []).append(i)
    return _Index(
        tuple(words),
        tuple(where),
        {k: tuple(v) for k, v in two.items()},
        {k: tuple(v) for k, v in one.items()},
        names,
    )


def _ref(ix: _Index, start: int, n: int) -> VerseRef:
    s, a = ix.where[start]
    _, b = ix.where[start + n - 1]
    ar, en, _ = ix.names[s - 1]
    return VerseRef(s, a, b, ar, en)


#: the fewest words a ``{…}`` span needs to count as a verse when nothing else says it is
#: one (no «قوله تعالى», no ``﴿…﴾``): most one- and two-word phrases occur exactly once in
#: the text, so a lesson topic in braces would otherwise get a verse reference
MIN_UNTRIGGERED_WORDS = 3


def _words(quote: str) -> list[str]:
    return [w for w in fold(re.sub(r"\.{2,}|…", " ", quote)).split() if w]


def locate(quote: str) -> VerseRef | None:
    """The verse(s) the quoted words are from, or None (not found, too short to tell, or
    found in more than one place)."""
    q = _words(quote)
    if not q:
        return None
    ix = _index()
    n = len(q)
    starts = ix.by_two.get((q[0], q[1]), ()) if n >= 2 else ix.by_one.get(q[0], ())
    hits: list[int] = []
    for i in starts:
        if i + n <= len(ix.words) and list(ix.words[i : i + n]) == q:
            hits.append(i)
            if len(hits) > 1:
                break
    if not hits and n >= 5:
        # a word or two spelt otherwise (Uthmani «الصلوة», «حسرة» for «حسرتى», a typo):
        # anchor on a later pair of words
        for off in (1, 2, 3):
            for j in ix.by_two.get((q[off], q[off + 1]), ()):
                i = j - off
                if i < 0 or i + n > len(ix.words):
                    continue
                bad = sum(1 for k in range(n) if ix.words[i + k] != q[k])
                if bad <= max(1, n // 4) and i not in hits:
                    hits.append(i)
            if hits:
                break
    if len(hits) != 1:
        return None
    return _ref(ix, hits[0], n)


@lru_cache(maxsize=1)
def _surah_names() -> dict[str, int]:
    out: dict[str, int] = {}
    for i, (ar, _en, _n) in enumerate(_index().names, 1):
        f = fold(ar)
        out[f] = i
        out[f.removeprefix("ال")] = out.get(f.removeprefix("ال"), i)
    return out


def surah_by_name(name: str) -> int | None:
    """The surah number of a name as titles write it («البقرة», «سورة آل عمران»)."""
    f = fold(name).removeprefix("سوره ").strip()
    return _surah_names().get(f)


# --------------------------------------------------------------------------- quotes

_TRIGGER = (
    r"(?:(?<!\w)|(?<=[وف]))(?:ب?قوله|ب?قول(?:\s+الله)?|قال(?:\s+الله)?)\s+(?:سبحانه\s+و\s*)?تعال[ىي]"
    r"(?:\s+في\s+سورة\s+\S+)?\s*[:：]?\s*"
    # «تفسير الآية ( ورفعنا لك ذكرك )»: «الآية» only before a bracket or quote
    r"|(?<!\w)(?:ال)?[آا]ي[ةه](?:\s+الكريم[ةه])?\s*[:：]?\s*(?=[﴿{(«\"“'])"
)
_BRACKETS = {"﴿": "﴾", "{": "}", "(": ")", "«": "»", '"': '"', "“": "”", "''": "''", "'": "'"}
_OPEN_RE = re.compile(r"﴿|\{|\(|«|\"|“|''|'")
_ARABIC_WORD = re.compile(r"[ء-ي]{2,}")


@dataclass(frozen=True, slots=True)
class Quote:
    """A quoted verse in a title: ``start:end`` is the whole span (with «قوله تعالى» and
    the brackets), ``text`` the quoted words, ``open`` / ``close`` its brackets ("" when
    bare), ``ref`` the verse(s) when found."""

    start: int
    end: int
    text: str
    open: str
    close: str
    triggered: bool
    ref: VerseRef | None


def _close_at(s: str, i: int, op: str) -> int:
    cl = _BRACKETS[op]
    j = s.find(cl, i + len(op))
    return -1 if j < 0 else j + len(cl)


def find_quotes(title: str) -> list[Quote]:
    """The verses a title quotes, in order. A ``﴿…﴾`` span is always a verse; ``{…}``
    and other brackets or quotes only after «قوله تعالى» or when the words are found in
    the Quran; bare words after «قوله تعالى» run to the next separator or «؟»."""
    out: list[Quote] = []
    taken: list[tuple[int, int]] = []

    def free(a: int, b: int) -> bool:
        return all(b <= x or a >= y for x, y in taken)

    for m in re.finditer(_TRIGGER, title):
        i = m.end()
        o = _OPEN_RE.match(title, i)
        if o:
            op = o.group(0)
            j = _close_at(title, i, op)
            if j > 0:
                inner = title[i + len(op) : j - len(_BRACKETS[op])]
                if _ARABIC_WORD.search(inner) and free(m.start(), j):
                    out.append(Quote(m.start(), j, inner.strip(), op, _BRACKETS[op], True,
                                     locate(inner)))  # fmt: skip
                    taken.append((m.start(), j))
                continue
        bare = re.match(r"[^|؟?!\-–—(){}﴿﴾«»\"]+", title[i:])
        if bare and len(_ARABIC_WORD.findall(bare.group(0))) >= 2:
            j = i + len(bare.group(0).rstrip())
            text = title[i:j].strip()
            if free(m.start(), j):
                out.append(Quote(m.start(), j, text, "", "", True, locate(text)))
                taken.append((m.start(), j))
    for m in re.finditer(r"﴿([^﴾]+)﴾|\{([^}]+)\}", title):
        inner = m.group(1) or m.group(2) or ""
        if not free(m.start(), m.end()) or not _ARABIC_WORD.search(inner):
            continue
        ref = locate(inner)
        if m.group(1) is None and (ref is None or len(_words(inner)) < MIN_UNTRIGGERED_WORDS):
            # «{ نظرية التطور }», «{15}»: braces, not a verse. Nor a short topic that
            # happens to occur once in the text: «{شهر رمضان}», «{الحج والعمرة}» and
            # «{اليوم الآخر}» are lesson subjects, not quotes of 2:185, 2:196 and 29:36
            continue
        op, cl = ("﴿", "﴾") if m.group(1) is not None else ("{", "}")
        out.append(Quote(m.start(), m.end(), inner.strip(), op, cl, False, ref))
        taken.append((m.start(), m.end()))
    return sorted(out, key=lambda q: q.start)
