"""The conformance suite every language pack passes (Epic 52, story 52.3).

Built-in packs run it in CI; Epic 54 will run it on third-party packs before its store
lists them. It checks the contract's rules over the pack's own sample sentences
(``samples.txt``) and over seeded random strings (the same strings every run, so a failure
reproduces): tokens cover the text, ``key`` is idempotent, ``known`` never raises, every
method answers the same twice (determinism), and ``protected`` / ``scripture`` keep to the
tokens they were given.
"""

from __future__ import annotations

import random
from collections.abc import Iterable
from dataclasses import dataclass

from .base import Known, LanguagePack, Token

__all__ = ["Failure", "check", "random_strings"]

_ALPHABETS = (
    "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZéèçß'-",
    "ابتثجحخدذرزسشصضطظعغفقكلمنهويءأإآةىؤئًٌٍَُِّْـ",
    '0123456789٠١٢٣٤٥٦٧٨٩.,:;!?،؛؟()«»"',
    "‎‏‫‬😀ﷺﷻٰۖ",
    "これはペンです中文Русскийעבריתไทย",
)


@dataclass(frozen=True, slots=True)
class Failure:
    rule: str
    text: str
    detail: str


def random_strings(n: int = 1000, seed: int = 52) -> list[str]:
    rng = random.Random(seed)
    out = []
    for _ in range(n):
        alpha = "".join(rng.sample(_ALPHABETS, rng.randint(1, 3)))
        words = [
            "".join(rng.choice(alpha) for _ in range(rng.randint(1, 9)))
            for _ in range(rng.randint(0, 6))
        ]
        out.append(" ".join(words))
    return out


def _covers(text: str, tokens: list[Token]) -> str | None:
    pos = 0
    for t in tokens:
        if t.start < pos or t.end <= t.start or t.end > len(text):
            return f"span {t.start}-{t.end} after {pos}"
        if text[t.start : t.end] != t.text and t.text != text[t.start : t.end].translate(
            {0x2018: "'", 0x2019: "'"}
        ):
            return f"token {t.text!r} is not text[{t.start}:{t.end}] {text[t.start : t.end]!r}"
        if text[pos : t.start].strip():
            return f"text {text[pos : t.start]!r} between tokens is not a space"
        pos = t.end
    if text[pos:].strip():
        return f"text {text[pos:]!r} after the last token is not a space"
    return None


def check(pack: LanguagePack, samples: Iterable[str] = (), n_random: int = 1000) -> list[Failure]:
    """Every rule the pack breaks, over ``samples`` and ``n_random`` random strings."""
    fails: list[Failure] = []
    texts = [*samples, *random_strings(n_random)]
    for text in texts:
        try:
            toks = pack.tokens(text)
        except Exception as exc:  # noqa: BLE001 - reported, not raised
            fails.append(Failure("tokens_raise", text, repr(exc)))
            continue
        why = _covers(text, toks)
        if why:
            fails.append(Failure("tokens_cover_text", text, why))
        if pack.tokens(text) != toks:
            fails.append(Failure("deterministic", text, "tokens differ on a second call"))
        for t in toks:
            k = pack.key(t.text)
            if pack.key(k) != k:
                fails.append(Failure("key_idempotent", t.text, f"{k!r} -> {pack.key(k)!r}"))
            if t.kind != "word":
                continue
            try:
                got = pack.known(t.text)
            except Exception as exc:  # noqa: BLE001
                fails.append(Failure("known_raises", t.text, repr(exc)))
                continue
            if not isinstance(got[0], Known):
                fails.append(Failure("known_type", t.text, repr(got)))
            if pack.known(t.text) != got:
                fails.append(Failure("deterministic", t.text, "known differs on a second call"))
        try:
            prot = pack.protected(toks)
            runs = pack.scripture(toks)
        except Exception as exc:  # noqa: BLE001
            fails.append(Failure("protected_raises", text, repr(exc)))
            continue
        if len(prot) != len(toks):
            fails.append(Failure("protected_length", text, f"{len(prot)} for {len(toks)}"))
        for r in runs:
            if not (0 <= r.start < r.end <= len(toks)):
                fails.append(Failure("scripture_span", text, f"{r.start}-{r.end}"))
            for op in r.ops:
                if op.token is not None and not (0 <= op.token < len(toks)):
                    fails.append(Failure("scripture_token", text, str(op.token)))
        if pack.normalise(text) != pack.normalise(text):
            fails.append(Failure("deterministic", text, "normalise differs"))
    for word in texts[:50]:
        for w in word.split()[:2]:
            try:
                first = pack.suggest(w, 3)
            except Exception as exc:  # noqa: BLE001
                fails.append(Failure("suggest_raises", w, repr(exc)))
                continue
            if pack.suggest(w, 3) != first or len(first) > 3:
                fails.append(Failure("suggest", w, repr(first)))
    return fails
