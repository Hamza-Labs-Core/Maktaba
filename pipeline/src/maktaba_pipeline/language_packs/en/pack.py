"""The English pack (Epic 52, story 52.5): Hunspell ``en_US`` then ``en_GB`` (SCOWL, the
``lang.en`` asset), with English's own tokenizer rules.

- ``normalise``: NFC, curly apostrophes and quotes to ``'``.
- ``key``: casefolded, apostrophes at the ends stripped, diacritics folded (café → cafe).
- ``tokens``: words over Latin letters with inner apostrophes (``don't``) and hyphens
  (``well-known``, checked part by part); numbers, ordinals and times are numbers.
- ``known``: the dictionaries as written, then lower-cased; ``'s`` stripped; the shipped
  Arabic transliterations and acronyms; a capitalised word mid-sentence is ``UNSURE``.
- ``suggest``: the dictionaries', then ``wordfreq``'s words one edit away when installed.
"""

from __future__ import annotations

import unicodedata
from functools import lru_cache
from pathlib import Path
from typing import Literal

from ..base import Known, ScriptureRun, Token, strip_bidi
from ..hunspell import dictionary_files, load_dictionary
from ..hunspell_reader import Dictionary
from ..segment import split

__all__ = ["EN_VERSION", "EnglishPack"]

EN_VERSION = "1"

_HERE = Path(__file__).resolve().parent
_QUOTES = str.maketrans({"‘": "'", "’": "'", "“": '"', "”": '"'})


def _list(name: str) -> frozenset[str]:
    path = _HERE / name
    if not path.is_file():
        return frozenset()
    return frozenset(
        line.strip().casefold()
        for line in path.read_text(encoding="utf-8").splitlines()
        if line.strip() and not line.startswith("#")
    )


class EnglishPack:
    code = "en"
    scripts = frozenset({"Latn"})
    direction: Literal["rtl", "ltr"] = "ltr"
    pack_id = "en"
    tokenizer = "regex"

    def __init__(self, folder: Path | None) -> None:
        self.folder = folder
        self._dicts: list[Dictionary] = []
        if folder is not None:
            pairs = dictionary_files(folder, ("en_US", "en_GB"))
            self._dicts = [load_dictionary(a, d, d.stat().st_mtime) for a, d in pairs]
        self._translit = _list("transliterations.txt")
        self._acronyms = _list("acronyms.txt")
        self.dictionary = "hunspell" if self._dicts else "unavailable"
        self.version = f"en-{EN_VERSION}-{self.dictionary}"

    def normalise(self, text: str) -> str:
        return strip_bidi(text).translate(_QUOTES)

    def key(self, token: str) -> str:
        t = self.normalise(token).casefold().strip("'")
        t = unicodedata.normalize("NFD", t)
        return "".join(c for c in t if not unicodedata.combining(c))

    def tokens(self, text: str) -> list[Token]:
        return split(self.normalise_offsets(text), "'-")

    @staticmethod
    def normalise_offsets(text: str) -> str:
        """The text with curly apostrophes made straight, one for one (offsets kept)."""
        return text.translate(_QUOTES)

    def _in_dict(self, word: str) -> bool:
        return any(d.lookup(word) or d.lookup(word.lower()) for d in self._dicts)

    def known(self, token: str) -> tuple[Known, str | None]:
        return self.known_at(token, False)

    @lru_cache(maxsize=200_000)  # noqa: B019 - one pack per process and data version
    def known_at(self, token: str, mid_sentence: bool) -> tuple[Known, str | None]:
        word = self.normalise(token)
        folded = word.casefold()
        if folded in self._translit:
            return Known.KNOWN, "transliteration"
        if not self._dicts:
            return Known.UNSURE, None
        try:
            if "-" in word.strip("-"):
                parts = [p for p in word.split("-") if p]
                if parts and all(self.known_at(p, mid_sentence)[0] is Known.KNOWN for p in parts):
                    return Known.KNOWN, None
            if self._in_dict(word):
                return Known.KNOWN, None
            for tail in ("'s", "s'", "'"):
                if folded.endswith(tail) and self._in_dict(word[: -len(tail)]):
                    return Known.KNOWN, "possessive"
        except Exception:  # noqa: BLE001 - known() never raises
            return Known.UNSURE, None
        if 2 <= len(word) <= 6 and sum(c.isupper() for c in word) >= 2:
            return (Known.KNOWN if folded in self._acronyms else Known.UNSURE), "acronym"
        if mid_sentence and word[:1].isupper():
            return Known.UNSURE, "capitalised"
        return Known.UNKNOWN, None

    def suggest(self, token: str, n: int) -> list[str]:
        word = self.normalise(token)
        out: list[str] = []
        seen: set[str] = set()
        for d in self._dicts:
            for s in d.suggest(word, n):
                if self.key(s) not in seen and s != word:
                    seen.add(self.key(s))
                    out.append(s)
        return out[:n]

    def protected(self, tokens: list[Token]) -> list[bool]:
        return [False] * len(tokens)

    def scripture(self, tokens: list[Token]) -> list[ScriptureRun]:
        return []
