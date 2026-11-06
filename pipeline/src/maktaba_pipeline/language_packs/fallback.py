"""The fallback pack (Epic 52, story 52.3): any language without a full pack.

UAX #29 word breaks through PyICU when it is installed (languages without spaces get real
words), else the shared splitter (:func:`.segment.split`, which at least breaks where the
script changes). Known-ness comes from ``wordfreq``'s Zipf frequency when it is installed:
at or above :data:`ZIPF_FLOOR` ``KNOWN``, below ``UNSURE``. Never ``UNKNOWN``, so the
fallback never adds ``not_in_dictionary``: a language with no dictionary gets the
confidence and listener signals only.
"""

from __future__ import annotations

import importlib.util
from functools import lru_cache
from typing import Any, Literal

from .base import Known, ScriptureRun, Token, strip_bidi
from .segment import HAS_ICU, icu_tokens, split

__all__ = ["FALLBACK_VERSION", "ZIPF_FLOOR", "FallbackPack"]

FALLBACK_VERSION = "1"
ZIPF_FLOOR = 2.5

HAS_WORDFREQ = importlib.util.find_spec("wordfreq") is not None


class FallbackPack:
    """The fallback for ``code`` (a BCP 47 language Whisper heard)."""

    direction: Literal["rtl", "ltr"]

    def __init__(self, code: str = "und", direction: Literal["rtl", "ltr"] = "ltr") -> None:
        self.code = code
        self.scripts: frozenset[str] = frozenset()  # any
        self.direction = direction
        self.tokenizer = "icu" if HAS_ICU else "regex"
        self.version = (
            f"fallback-{FALLBACK_VERSION}-{self.tokenizer}-{'wf' if HAS_WORDFREQ else 'nf'}"
        )
        self.pack_id = "fallback"

    def normalise(self, text: str) -> str:
        return strip_bidi(text)

    def key(self, token: str) -> str:
        return strip_bidi(token).casefold()

    def tokens(self, text: str) -> list[Token]:
        got = icu_tokens(text, self.code) if HAS_ICU else None
        return got if got is not None else split(text, "'’-")

    def known(self, token: str) -> tuple[Known, str | None]:
        try:
            z = _zipf(self.key(token), self.code)
        except Exception:  # noqa: BLE001 - known() never raises: missing data is UNSURE
            return Known.UNSURE, None
        if z is None:
            return Known.UNSURE, None
        return (Known.KNOWN if z >= ZIPF_FLOOR else Known.UNSURE), f"zipf {z:.1f}"

    def suggest(self, token: str, n: int) -> list[str]:
        return []

    def protected(self, tokens: list[Token]) -> list[bool]:
        return [False] * len(tokens)

    def scripture(self, tokens: list[Token]) -> list[ScriptureRun]:
        return []


@lru_cache(maxsize=200_000)
def _zipf(word: str, lang: str) -> float | None:
    if not HAS_WORDFREQ or not word:
        return None
    import wordfreq  # type: ignore[import-not-found,import-untyped,unused-ignore]

    got: Any = wordfreq.zipf_frequency(word, lang)
    return float(got)
