"""The language pack contract (Epic 52, story 52.3).

A pack knows one language's spelling: how to normalise and fold its words, split a
sentence into tokens (with code-point offsets into the sentence), say whether a word is
known, suggest spellings, and which tokens must never be flagged. Every answer is
deterministic: the same text, data files and :attr:`LanguagePack.version` give the same
answer. No pack samples a model, calls an LLM or reaches the network; its data comes only
from its Epic 51 asset folder (``components.packs.pack_dir``).
"""

from __future__ import annotations

import unicodedata
from dataclasses import dataclass, field
from enum import Enum
from typing import Any, Literal, Protocol, runtime_checkable

__all__ = [
    "BIDI",
    "Known",
    "LanguagePack",
    "ScriptureOp",
    "ScriptureRun",
    "Token",
    "TokenKind",
    "strip_bidi",
]

TokenKind = Literal["word", "number", "punct", "symbol"]


@dataclass(frozen=True, slots=True)
class Token:
    """A token of a sentence: ``text[start:end]``, in code points."""

    text: str
    start: int
    end: int
    kind: TokenKind
    #: ISO 15924 (``Arab``, ``Latn``, ...); ``Zyyy`` (common) for digits and marks alone
    script: str = "Zyyy"


class Known(Enum):
    KNOWN = "known"
    UNKNOWN = "unknown"
    UNSURE = "unsure"


@dataclass(frozen=True, slots=True)
class ScriptureOp:
    """One aligned position of a scripture run (story 52.15): ``token`` is the index into
    the tokens the pack was given (None for a verse word the transcript lacks);
    ``kind`` ``eq`` (equal), ``var`` (an orthographic or reading variant), ``sub``,
    ``ins`` (a transcript word not in the verse) or ``del`` (a verse word missing)."""

    kind: Literal["eq", "var", "sub", "ins", "del"]
    token: int | None
    expected: str | None = None
    ref: dict[str, Any] = field(default_factory=dict)


@dataclass(frozen=True, slots=True)
class ScriptureRun:
    """A run of tokens checked against a canonical text (Arabic: a Quran verse run)."""

    start: int  # first token index
    end: int  # one past the last token index
    ref: dict[str, Any]
    ops: tuple[ScriptureOp, ...] = ()
    #: too many mismatches to be a quotation: its words go back to the ordinary signals
    rejected: bool = False


@runtime_checkable
class LanguagePack(Protocol):
    code: str
    version: str
    scripts: frozenset[str]
    direction: Literal["rtl", "ltr"]

    def normalise(self, text: str) -> str: ...

    def key(self, token: str) -> str: ...

    def tokens(self, text: str) -> list[Token]: ...

    def known(self, token: str) -> tuple[Known, str | None]: ...

    def suggest(self, token: str, n: int) -> list[str]: ...

    def protected(self, tokens: list[Token]) -> list[bool]: ...

    def scripture(self, tokens: list[Token]) -> list[ScriptureRun]: ...


#: bidi controls a transcript never needs (LRM, RLM, embeddings, isolates)
BIDI = frozenset("‎‏‪‫‬‭‮⁦⁧⁨⁩")


def strip_bidi(text: str) -> str:
    return "".join(c for c in unicodedata.normalize("NFC", text) if c not in BIDI)
