"""A small, deterministic Hunspell dictionary reader (Epic 52, story 52.3).

Reads a LibreOffice ``.aff`` / ``.dic`` pair and answers whether a word is in it, the way
Hunspell does at lookup time: the word as a stem, or with one prefix and/or one suffix
stripped back to a stem that carries that affix's flag (cross products where both allow
it). Suggestions are the dictionary's ``REP`` replacements and every word one edit away
(``TRY`` letters), known ones only, in a fixed order.

What it reads: ``SET``, ``FLAG`` (single characters, ``long``, ``num``, ``UTF-8``),
``TRY``, ``WORDCHARS``, ``ICONV``, ``REP``, ``PFX`` / ``SFX`` (strip, add, condition),
``NEEDAFFIX``, ``FORBIDDENWORD``, ``KEEPCASE``. Compounding and second-level affixes are
not, so a word only a compound rule allows is unknown (word review then says UNKNOWN or
UNSURE, never a crash). Pure Python, no dependency; the same files always give the same
answers.
"""

from __future__ import annotations

import re
from collections.abc import Iterable
from dataclasses import dataclass, field
from pathlib import Path

__all__ = ["Affix", "Dictionary", "load"]


@dataclass(frozen=True, slots=True)
class Affix:
    flag: str
    cross: bool
    strip: str
    add: str
    condition: re.Pattern[str] | None
    flags: frozenset[str] = frozenset()


@dataclass(slots=True)
class Dictionary:
    words: dict[str, frozenset[str]] = field(default_factory=dict)
    prefixes: list[Affix] = field(default_factory=list)
    suffixes: list[Affix] = field(default_factory=list)
    try_chars: str = ""
    wordchars: str = ""
    iconv: list[tuple[str, str]] = field(default_factory=list)
    rep: list[tuple[str, str]] = field(default_factory=list)
    needaffix: str | None = None
    forbidden: str | None = None
    keepcase: str | None = None
    #: lookups answered so far (cleared whole past 200 k entries)
    memo: dict[str, bool] = field(default_factory=dict)
    #: the affixes by the last (suffix) or first (prefix) letter they add ("" for none)
    by_end: dict[str, list[Affix]] = field(default_factory=dict)
    by_start: dict[str, list[Affix]] = field(default_factory=dict)

    def index(self) -> None:
        self.by_end, self.by_start = {}, {}
        for s in self.suffixes:
            self.by_end.setdefault(s.add[-1:] if s.add else "", []).append(s)
        for p in self.prefixes:
            self.by_start.setdefault(p.add[:1] if p.add else "", []).append(p)

    def convert(self, word: str) -> str:
        for a, b in self.iconv:
            word = word.replace(a, b)
        return word

    def _stem_ok(self, stem: str, need: str | None, extra: str | None = None) -> bool:
        flags = self.words.get(stem)
        if flags is None:
            return False
        if self.forbidden and self.forbidden in flags:
            return False
        if need is not None and need not in flags:
            return False
        if extra is not None and extra not in flags:
            return False
        return not (need is None and extra is None and self.needaffix and self.needaffix in flags)

    def _suffixed(self, word: str, need_cross: bool = False) -> list[tuple[str, Affix]]:
        out: list[tuple[str, Affix]] = []
        for s in [*self.by_end.get("", ()), *self.by_end.get(word[-1:], ())]:
            if need_cross and not s.cross:
                continue
            if s.add and not word.endswith(s.add):
                continue
            stem = (word[: len(word) - len(s.add)] if s.add else word) + s.strip
            if not stem:
                continue
            if s.condition is not None and not s.condition.search(stem):
                continue
            out.append((stem, s))
        return out

    def _prefixed(self, word: str) -> list[tuple[str, Affix]]:
        out: list[tuple[str, Affix]] = []
        for p in [*self.by_start.get("", ()), *self.by_start.get(word[:1], ())]:
            if p.add and not word.startswith(p.add):
                continue
            stem = p.strip + (word[len(p.add) :] if p.add else word)
            if not stem:
                continue
            if p.condition is not None and not p.condition.match(stem):
                continue
            out.append((stem, p))
        return out

    def lookup(self, word: str) -> bool:
        return _lookup(self, word)

    def suggest(self, word: str, n: int) -> list[str]:
        out: list[str] = []
        seen = {word}
        for cand in self._candidates(word):
            if cand in seen:
                continue
            seen.add(cand)
            if self.lookup(cand):
                out.append(cand)
                if len(out) >= n:
                    break
        return out

    def _candidates(self, word: str) -> Iterable[str]:
        for a, b in self.rep:
            i = word.find(a)
            while i >= 0:
                yield word[:i] + b + word[i + len(a) :]
                i = word.find(a, i + 1)
        letters = self.try_chars or "".join(sorted({c for w in self.words for c in w}))
        for i in range(len(word) - 1):  # swapped neighbours
            yield word[:i] + word[i + 1] + word[i] + word[i + 2 :]
        for i in range(len(word)):  # one letter changed
            for c in letters:
                if c != word[i]:
                    yield word[:i] + c + word[i + 1 :]
        for i in range(len(word)):  # one letter dropped
            yield word[:i] + word[i + 1 :]
        for i in range(len(word) + 1):  # one letter added
            for c in letters:
                yield word[:i] + c + word[i:]


def _lookup(d: Dictionary, word: str) -> bool:
    got = d.memo.get(word)
    if got is None:
        got = _uncached(d, word)
        if len(d.memo) > 200_000:
            d.memo.clear()
        d.memo[word] = got
    return got


def _uncached(d: Dictionary, word: str) -> bool:
    if not word:
        return False
    if d._stem_ok(word, None):
        return True
    for stem, s in d._suffixed(word):
        if d._stem_ok(stem, s.flag):
            return True
    for stem, p in d._prefixed(word):
        if d._stem_ok(stem, p.flag):
            return True
        if p.cross:
            for stem2, s in d._suffixed(stem, need_cross=True):
                if d._stem_ok(stem2, p.flag, s.flag):
                    return True
    return False


def _split_flags(raw: str, mode: str) -> list[str]:
    if not raw:
        return []
    if mode == "long":
        return [raw[i : i + 2] for i in range(0, len(raw), 2)]
    if mode == "num":
        return [f for f in raw.split(",") if f]
    return list(raw)


def _condition(cond: str, suffix: bool) -> re.Pattern[str] | None:
    if cond in ("", "."):
        return None
    return re.compile(("" if suffix else "^") + cond + ("$" if suffix else ""))


def _read(path: Path, encoding: str = "utf-8") -> list[str]:
    raw = path.read_bytes()
    text = raw.decode(encoding, errors="replace")
    if text.startswith("﻿"):
        text = text[1:]
    return text.splitlines()


def load(aff: Path, dic: Path) -> Dictionary:
    """The dictionary of an ``.aff`` / ``.dic`` pair."""
    lines = _read(aff, "latin-1")
    encoding = "utf-8"
    for line in lines:
        if line.startswith("SET "):
            encoding = line.split()[1].strip().lower()
            break
    if encoding not in ("latin-1", "utf-8"):
        encoding = {"iso8859-1": "latin-1", "iso-8859-1": "latin-1"}.get(encoding, encoding)
    lines = _read(aff, encoding)
    d = Dictionary()
    mode = "char"
    pending: dict[tuple[str, str], tuple[bool, int]] = {}
    for line in lines:
        parts = line.split()
        if not parts or parts[0].startswith("#"):
            continue
        head = parts[0]
        if head == "FLAG" and len(parts) > 1:
            mode = {"long": "long", "num": "num"}.get(parts[1].lower(), "char")
        elif head == "TRY" and len(parts) > 1:
            d.try_chars = parts[1]
        elif head == "WORDCHARS" and len(parts) > 1:
            d.wordchars = parts[1]
        elif head == "NEEDAFFIX" and len(parts) > 1:
            d.needaffix = parts[1]
        elif head == "FORBIDDENWORD" and len(parts) > 1:
            d.forbidden = parts[1]
        elif head == "KEEPCASE" and len(parts) > 1:
            d.keepcase = parts[1]
        elif head in ("ICONV", "REP") and len(parts) == 3:
            (d.iconv if head == "ICONV" else d.rep).append((parts[1], parts[2].replace("_", " ")))
        elif head in ("PFX", "SFX") and len(parts) >= 4:
            flag = parts[1]
            if (head, flag) not in pending and parts[2] in ("Y", "N") and parts[3].isdigit():
                pending[(head, flag)] = (parts[2] == "Y", int(parts[3]))
                continue
            if len(parts) < 5 or (head, flag) not in pending:
                continue
            cross = pending[(head, flag)][0]
            strip = "" if parts[2] == "0" else parts[2]
            add, _, cont = parts[3].partition("/")
            add = "" if add == "0" else add
            aff_ = Affix(
                flag,
                cross,
                strip,
                add,
                _condition(parts[4], head == "SFX"),
                frozenset(_split_flags(cont, mode)),
            )
            (d.prefixes if head == "PFX" else d.suffixes).append(aff_)
    for i, line in enumerate(_read(dic, encoding)):
        if i == 0 and line.strip().isdigit():
            continue
        line = line.split("\t", 1)[0].strip()
        if not line:
            continue
        word, _, raw = line.partition("/")
        word = word.split(" ", 1)[0] if not raw else word
        raw = raw.split(" ", 1)[0]
        flags = frozenset(_split_flags(raw, mode))
        d.words[word] = d.words.get(word, frozenset()) | flags
    d.index()
    return d
