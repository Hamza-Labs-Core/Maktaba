"""The Arabic pack (Epic 52, story 52.4).

Arabic is where a word list fails: one stem takes hundreds of forms through clitics and
inflection («وسيكتبونها» is a correct word no plain list holds). So "known" is decided by
CAMeL Tools' MSA morphological analyser (the ``model.refine`` asset), with Ayaspell's
Hunspell dictionary (the ``lang.ar`` asset) for the nouns the analyser lacks and for
suggestions. Verse runs are checked against the Quran text (:mod:`.quran`, story 52.15).

- ``normalise``: NFC, bidi controls and tatweel stripped; harakat kept (a reviewer sees
  what was written).
- ``key``: :func:`~maktaba_pipeline.text.arabic.fold` (harakat gone, hamza forms, ة/ه,
  ى/ي, the Persian look-alikes unified, Arabic-Indic digits Latin).
- ``tokens``: runs of Arabic letters and their marks, clitics attached; digits are
  numbers; ﷺ ﷻ and the other ligatures of U+FDF0-U+FDFF are symbols, never words.
- ``known``: a number or one letter, the analyser, a dialect clitic peeled (``UNSURE``),
  Hunspell; else ``UNKNOWN``. Without the analyser nothing is ``UNKNOWN``.
"""

from __future__ import annotations

from collections.abc import Callable, Sequence
from functools import lru_cache
from pathlib import Path
from typing import Literal

from ...text.arabic import ARABIC_VERSION, fold
from ..base import Known, ScriptureRun, Token, strip_bidi
from ..hunspell import dictionary_files, load_dictionary
from ..hunspell_reader import Dictionary
from ..segment import split
from . import morphology, quran
from .confusions import one_swap
from .dialect import peeled

__all__ = ["AR_VERSION", "ArabicPack"]

AR_VERSION = "1"

_MARKS = frozenset(chr(c) for c in [*range(0x064B, 0x0653), 0x0670])
_LIGATURES = range(0xFDF0, 0xFE00)

#: an analyser: the analyses of an undiacritised word, as (lemma, pos, spelling, logprob)
Analyse = Callable[[str], Sequence[tuple[str, str, str, float]]]


def _dediac(word: str) -> str:
    return "".join(c for c in word if c not in _MARKS and c != "ـ")


#: forms that fold to the same letters, for restoring a spelling (hamza, ة, ى)
_RESTORE = {"ا": "أإآ", "ه": "ة", "ي": "ىئ", "و": "ؤ"}


def _respellings(word: str, limit: int = 64) -> list[str]:
    """Spellings of ``word`` that fold to the same key (the hamza, «ة» and «ى» forms
    restored), the closest first."""
    out = [word]
    for i, ch in enumerate(word):
        alts = _RESTORE.get(ch)
        if not alts:
            continue
        # «ة» and «ى» only end a word
        if ch in "هي" and i != len(word) - 1 and alts != "ىئ":
            continue
        grown = []
        for base in out:
            for a in alts:
                if a in "ةى" and i != len(word) - 1:
                    continue
                grown.append(base[:i] + a + base[i + 1 :])
        out.extend(grown)
        if len(out) >= limit:
            break
    return out[1:limit]


class ArabicPack:
    code = "ar"
    scripts = frozenset({"Arab"})
    direction: Literal["rtl", "ltr"] = "rtl"
    pack_id = "ar"
    tokenizer = "regex"

    def __init__(
        self,
        folder: Path | None,
        analyse: Analyse | None = None,
        quran_check: bool = True,
    ) -> None:
        self.folder = folder
        self._dicts: list[Dictionary] = []
        if folder is not None:
            pairs = dictionary_files(folder, ("ar",))
            self._dicts = [load_dictionary(a, d, d.stat().st_mtime) for a, d in pairs]
        if analyse is None and morphology.available():
            analyse = morphology.analyse
        self._analyse = analyse
        self.analyser = "camel" if analyse is not None else "unavailable"
        self.dictionary = "hunspell" if self._dicts else "unavailable"
        self.quran_check = quran_check
        self.version = (
            f"ar-{AR_VERSION}-fold{ARABIC_VERSION}-quran{quran.QURAN_CHECK_VERSION}"
            f"-{self.analyser}-{self.dictionary}"
        )

    # ---- text

    def normalise(self, text: str) -> str:
        return strip_bidi(text).replace("ـ", "")

    def key(self, token: str) -> str:
        return fold(token).replace(" ", "")

    def tokens(self, text: str) -> list[Token]:
        out: list[Token] = []
        for t in split(text):
            if t.kind == "word" and all(ord(c) in _LIGATURES for c in t.text):
                t = Token(t.text, t.start, t.end, "symbol", t.script)
            out.append(t)
        return out

    # ---- the dictionary

    def _hunspell(self, word: str) -> bool:
        return any(d.lookup(word) for d in self._dicts)

    def _analysis(self, word: str) -> str | None:
        """The top analysis as the reviewer reads it («كَتَبَ · verb»), or None."""
        if self._analyse is None:
            return None
        got = list(self._analyse(word))
        if not got:
            return None
        lemma, pos, _diac, _p = max(got, key=lambda a: (a[3], a[0], a[1]))
        return f"{lemma} · {pos}" if lemma or pos else ""

    def known(self, token: str) -> tuple[Known, str | None]:
        return self._known(_dediac(token))

    @lru_cache(maxsize=200_000)  # noqa: B019 - one pack per process and data version
    def _known(self, word: str) -> tuple[Known, str | None]:
        try:
            if not word or any(c.isdigit() for c in word) or len(word) == 1:
                return Known.KNOWN, None
            got = self._analysis(word)
            if got is not None:
                return Known.KNOWN, got
            if self._analyse is not None:
                for base in peeled(word):
                    if self._analysis(base) is not None:
                        return Known.UNSURE, "dialect"
            if self._hunspell(word):
                return Known.KNOWN, None
        except Exception:  # noqa: BLE001 - known() never raises: missing data is UNSURE
            return Known.UNSURE, None
        if self._analyse is None:
            return Known.UNSURE, None
        return Known.UNKNOWN, None

    def _accepted(self, word: str) -> bool:
        return self._known(word)[0] is Known.KNOWN and not word.isdigit()

    def suggest(self, token: str, n: int) -> list[str]:
        word = _dediac(token)
        out: list[str] = []
        seen: set[str] = set()

        def add(cand: str) -> bool:
            if cand != word and cand not in seen and self._accepted(cand):
                seen.add(cand)
                out.append(cand)
            return len(out) >= n

        for cand in _respellings(word):
            if add(cand):
                return out
        for d in self._dicts:
            for cand in d.suggest(word, n):
                if add(cand):
                    return out
        for cand in one_swap(word):
            if add(cand):
                return out
        return out

    # ---- verse runs (story 52.15)

    def scripture(self, tokens: list[Token]) -> list[ScriptureRun]:
        """The verse runs among ``tokens`` (their word tokens are aligned; op indices are
        indices into ``tokens``)."""
        idx = [i for i, t in enumerate(tokens) if t.kind == "word" and t.script == "Arab"]
        if len(idx) < quran.MIN_ANCHOR:
            return []
        runs = quran.align([tokens[i].text for i in idx])
        out: list[ScriptureRun] = []
        for r in runs:
            ops = tuple(
                type(o)(o.kind, idx[o.token] if o.token is not None else None, o.expected, o.ref)
                for o in r.ops
            )
            out.append(
                ScriptureRun(idx[r.start], idx[r.end - 1] + 1, r.ref, ops, rejected=r.rejected)
            )
        return out

    def protected(self, tokens: list[Token]) -> list[bool]:
        """The tokens of kept verse runs that equal the verse (or are a variant of it);
        with the Quran check off, every word of a run."""
        return self.protected_by(self.scripture(tokens), len(tokens))

    def protected_by(self, runs: list[ScriptureRun], n: int) -> list[bool]:
        out = [False] * n
        for run in runs:
            if run.rejected:
                continue
            if not self.quran_check:
                for i in range(run.start, run.end):
                    out[i] = True
                continue
            for op in run.ops:
                if op.token is not None and op.kind in ("eq", "var"):
                    out[op.token] = True
            # a word with a verse word missing before it is the check's, not protected
            for op in run.ops:
                if op.kind == "del" and op.token is not None:
                    out[op.token] = False
        return out
