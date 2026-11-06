"""What every fix teaches (Epic 52, story 52.10).

A library's vocabulary (and the home's) is loaded once per ``word_check`` run: its keys
per language are known words, a word one edit from a term is ``near_vocabulary``, and a
``vocab_pairs`` hit ("heard X, meant Y") puts Y first among the candidates. New
transcriptions get a *derived prompt*: the library's own ``stt.initial_prompt`` first, then
the most used names and terms, cut to Whisper's 224-token prompt limit. The derived part is
never part of ``transcribe``'s input fingerprint, so learning a word never queues the
library for transcription again.
"""

from __future__ import annotations

import hashlib
from collections.abc import Callable
from dataclasses import dataclass, field
from typing import Any

from ..engine.sql import uid
from ..log import get_logger
from .db import read_array

__all__ = [
    "PROMPT_TOKENS",
    "Vocabulary",
    "damerau1",
    "derived_prompt",
    "load",
    "prompt_terms",
    "version",
]

_log = get_logger()

#: Whisper's prompt limit (tokens)
PROMPT_TOKENS = 224
#: the fewest letters a key needs for "one edit away" to mean anything
NEAR_MIN = 4


def damerau1(a: str, b: str) -> bool:
    """``a`` and ``b`` are exactly one edit apart (a change, an insertion, a deletion, or
    two neighbours swapped)."""
    if a == b:
        return False
    la, lb = len(a), len(b)
    if abs(la - lb) > 1:
        return False
    if la == lb:
        diff = [i for i in range(la) if a[i] != b[i]]
        if len(diff) == 1:
            return True
        return (
            len(diff) == 2
            and diff[1] == diff[0] + 1
            and a[diff[0]] == b[diff[1]]
            and a[diff[1]] == b[diff[0]]
        )
    if la > lb:
        a, b = b, a
    return any(b[:i] + b[i + 1 :] == a for i in range(len(b)))


@dataclass(slots=True)
class Vocabulary:
    #: language -> key -> term
    terms: dict[str, dict[str, str]] = field(default_factory=dict)
    #: language -> heard key -> meant
    pairs: dict[str, dict[str, str]] = field(default_factory=dict)
    #: language -> names (for the gazetteer)
    names: dict[str, list[str]] = field(default_factory=dict)

    def knows(self, language: str, key: str) -> bool:
        return key in self.terms.get(language, {})

    def near(self, language: str, key: str) -> str | None:
        if len(key) < NEAR_MIN:
            return None
        terms = self.terms.get(language, {})
        for k in sorted(terms):
            if len(k) >= NEAR_MIN and damerau1(k, key):
                return terms[k]
        return None

    def meant(self, language: str, key: str) -> str | None:
        return self.pairs.get(language, {}).get(key)

    def all_names(self) -> list[str]:
        return [n for ns in self.names.values() for n in ns]


async def load(db: Any, library_id: Any) -> Vocabulary:
    """The library's and the home's vocabulary, and the library's pairs (a database
    without the tables gives an empty vocabulary)."""
    v = Vocabulary()
    try:
        rows = await db.fetch(
            "SELECT language, term, key, kind, variants FROM vocabulary"
            " WHERE scope = 'home' OR library_id = $1 ORDER BY scope, term",
            uid(library_id),
        )
        for r in rows:
            lang, key, term = str(r["language"]), str(r["key"]), str(r["term"])
            v.terms.setdefault(lang, {}).setdefault(key, term)
            for var in read_array(r["variants"]):
                v.terms[lang].setdefault(var, term)
            if r["kind"] == "name":
                v.names.setdefault(lang, []).append(term)
        pairs = await db.fetch(
            "SELECT language, heard_key, meant, uses FROM vocab_pairs"
            " WHERE library_id = $1 OR library_id IS NULL ORDER BY uses DESC, meant",
            uid(library_id),
        )
        for r in pairs:
            v.pairs.setdefault(str(r["language"]), {}).setdefault(
                str(r["heard_key"]), str(r["meant"])
            )
    except Exception as exc:  # noqa: BLE001 - no vocabulary is an empty one
        _log.debug("vocabulary_unavailable", error=f"{type(exc).__name__}"[:80])
    return v


async def version(db: Any, library_id: Any) -> str:
    """A digest of what the library's checks read from the vocabulary: part of
    ``word_check``'s fingerprint (a change re-checks only where 52.10 queues it)."""
    v = await load(db, library_id)
    h = hashlib.sha256()
    for lang in sorted(v.terms):
        for k in sorted(v.terms[lang]):
            h.update(f"t|{lang}|{k}|{v.terms[lang][k]}\n".encode())
    for lang in sorted(v.pairs):
        for k in sorted(v.pairs[lang]):
            h.update(f"p|{lang}|{k}|{v.pairs[lang][k]}\n".encode())
    return h.hexdigest()[:16]


async def prompt_terms(db: Any, library_id: Any, limit: int = 400) -> list[str]:
    """The library's (and home's) names and terms, most used first."""
    try:
        rows = await db.fetch(
            "SELECT term FROM vocabulary WHERE kind IN ('name', 'term')"
            " AND (scope = 'home' OR library_id = $1) ORDER BY uses DESC, term LIMIT $2",
            uid(library_id),
            limit,
        )
    except Exception:  # noqa: BLE001 - no vocabulary yet
        return []
    seen: set[str] = set()
    out = []
    for r in rows:
        t = str(r["term"]).strip()
        if t and t not in seen:
            seen.add(t)
            out.append(t)
    return out


def derived_prompt(
    owner: str | None,
    terms: list[str],
    count_tokens: Callable[[str], int] | None = None,
    limit: int = PROMPT_TOKENS,
) -> str | None:
    """The owner's prompt (always first, never cut), then terms while the whole stays
    within ``limit`` tokens (``count_tokens``: the model's tokenizer; without one, a
    conservative estimate of a token per 2 characters)."""
    count = count_tokens or (lambda s: (len(s) + 1) // 2)
    base = (owner or "").strip()
    out = base
    for t in terms:
        cand = f"{out}, {t}" if out else t
        if count(cand) > limit:
            break
        out = cand
    return out or None
