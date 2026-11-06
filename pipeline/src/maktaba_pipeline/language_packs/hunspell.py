"""The generic Hunspell pack (Epic 52, story 52.3): about 100 languages from LibreOffice's
dictionaries, one Epic 51 asset each (``lang.hunspell.<code>``), no new code per language.

- ``normalise``: NFC, bidi controls stripped, the dictionary's ``ICONV`` table.
- ``tokens``: the shared splitter (UAX #29 through PyICU where installed), with the
  dictionary's ``WORDCHARS`` joined into words.
- ``known``: the word as written, then lower-cased: ``KNOWN``; else ``UNKNOWN``, except a
  capitalised word that is not the sentence's first, which is ``UNSURE`` (a likely name).
- ``suggest``: the dictionary's (``REP``, then one edit away).
"""

from __future__ import annotations

from functools import lru_cache
from pathlib import Path
from typing import Literal

from .base import Known, ScriptureRun, Token, strip_bidi
from .hunspell_reader import Dictionary, load
from .segment import HAS_ICU, icu_tokens, split

__all__ = ["HUNSPELL_VERSION", "HunspellPack", "dictionary_files", "load_dictionary"]

HUNSPELL_VERSION = "1"

#: languages written right to left that a Hunspell dictionary may cover
_RTL = frozenset({"ar", "fa", "he", "ur", "yi", "ps", "ckb"})


def dictionary_files(folder: Path, prefer: tuple[str, ...] = ()) -> list[tuple[Path, Path]]:
    """The ``.aff`` / ``.dic`` pairs in ``folder``, the ``prefer`` stems first."""
    pairs = []
    for aff in sorted(folder.glob("*.aff")):
        dic = aff.with_suffix(".dic")
        if dic.is_file():
            pairs.append((aff, dic))
    rank = {stem: i for i, stem in enumerate(prefer)}
    return sorted(pairs, key=lambda p: (rank.get(p[0].stem, len(rank)), p[0].stem))


@lru_cache(maxsize=32)
def load_dictionary(aff: Path, dic: Path, mtime: float) -> Dictionary:
    """One loaded dictionary per file pair and version of the files (``mtime``)."""
    return load(aff, dic)


class HunspellPack:
    """A language whose dictionary is a Hunspell pair in ``folder``."""

    direction: Literal["rtl", "ltr"]

    def __init__(self, code: str, folder: Path, scripts: frozenset[str] = frozenset({"Latn"})):
        self.code = code
        self.folder = folder
        self.scripts = scripts
        self.direction = "rtl" if code.split("-")[0] in _RTL else "ltr"
        self.tokenizer = "icu" if HAS_ICU else "regex"
        self.version = f"hunspell-{HUNSPELL_VERSION}-{self.tokenizer}"
        self.pack_id = "hunspell"
        pairs = dictionary_files(folder, (code.replace("-", "_"),))
        if not pairs:
            raise FileNotFoundError(f"no Hunspell dictionary in {folder}")
        self._dicts = [load_dictionary(a, d, d.stat().st_mtime) for a, d in pairs]
        self._wordchars = "".join(sorted({c for d in self._dicts for c in d.wordchars}))

    def normalise(self, text: str) -> str:
        out = strip_bidi(text)
        for d in self._dicts:
            out = d.convert(out)
        return out

    def key(self, token: str) -> str:
        return self.normalise(token).casefold()

    def tokens(self, text: str) -> list[Token]:
        got = icu_tokens(text, self.code) if HAS_ICU else None
        return got if got is not None else split(text, self._wordchars + "'’")

    def _lookup(self, word: str) -> bool:
        w = self.normalise(word)
        return any(d.lookup(w) or d.lookup(w.lower()) for d in self._dicts)

    def known(self, token: str) -> tuple[Known, str | None]:
        return self.known_at(token, False)

    def known_at(self, token: str, mid_sentence: bool) -> tuple[Known, str | None]:
        try:
            if self._lookup(token):
                return Known.KNOWN, None
        except Exception:  # noqa: BLE001 - known() never raises
            return Known.UNSURE, None
        if mid_sentence and token[:1].isupper():
            return Known.UNSURE, "capitalised"
        return Known.UNKNOWN, None

    def suggest(self, token: str, n: int) -> list[str]:
        out: list[str] = []
        seen: set[str] = set()
        for d in self._dicts:
            for s in d.suggest(self.normalise(token), n):
                if self.key(s) not in seen and s != token:
                    seen.add(self.key(s))
                    out.append(s)
        return out[:n]

    def protected(self, tokens: list[Token]) -> list[bool]:
        return [False] * len(tokens)

    def scripture(self, tokens: list[Token]) -> list[ScriptureRun]:
        return []
