"""The Quran check (Epic 52, story 52.15): verse runs against the Quran text.

The text is the bundled Tanzil "Simple Clean" v1.1 (``data/quran``, CC BY 3.0, used
verbatim: every normalisation here is done in memory, never written back). A transcript's
Arabic words are compared with it on two keys:

- :func:`match_key`: the Arabic pack's key (harakat, Quranic marks U+06D6-U+06ED, the
  dagger alef, tatweel gone; alef wasla and hamza forms, ة/ه, ى/ي unified). Equal keys are
  the same word.
- :func:`rasm_key`: the match key without its dots and long alefs (and the Uthmani waw
  written for an alef). Unequal words with equal rasm keys are an orthographic or reading
  variant (الصلوة / الصلاة, ملك / مالك, يعلمون / تعلمون): not an error of hearing.

:func:`align` finds the runs: anchored where at least :data:`MIN_ANCHOR` words in a row
equal the text (the index of :mod:`maktaba_pipeline.classify.quran`), extended both ways
word by word, tolerating about one wrong word in five. A run is kept when it has at least
4 words and at least :data:`MIN_GOOD` of them equal or variant. An anchor found equally
well in more than one place leaves its run unchecked: no flag, no protection. The
isti'adha, the basmala and «صدق الله العظيم» are their own phrases, never flagged.
"""

from __future__ import annotations

from dataclasses import dataclass
from functools import lru_cache
from typing import Any

from ...classify import quran as q
from ...text.arabic import fold
from ..base import ScriptureOp, ScriptureRun

__all__ = [
    "MIN_ANCHOR",
    "MIN_GOOD",
    "PHRASES",
    "QURAN_CHECK_VERSION",
    "align",
    "match_key",
    "rasm_key",
    "verse_word",
]

#: bumps re-check every transcript the Arabic pack touched (it is in the pack's version)
QURAN_CHECK_VERSION = 1
MIN_ANCHOR = q.MIN_RUN_WORDS
MIN_GOOD = 0.8
#: the window the stop rule reads, and the cost it allows in it
WINDOW = 5
WINDOW_COST = 1.0
VARIANT_COST = 0.3

#: phrases said around a recitation, never flagged and never part of a run
PHRASES: dict[str, tuple[str, ...]] = {
    "istiadha": tuple(fold("أعوذ بالله من الشيطان الرجيم").split()),
    "basmala": tuple(fold("بسم الله الرحمن الرحيم").split()),
    "sadaqa": tuple(fold("صدق الله العظيم").split()),
}

_UNDOT = str.maketrans(
    {
        "ت": "ب", "ث": "ب", "ن": "ب", "ي": "ب", "ئ": "ب",
        "ج": "ح", "خ": "ح",
        "ذ": "د",
        "ز": "ر",
        "ش": "س",
        "ض": "ص",
        "ظ": "ط",
        "غ": "ع",
        "ق": "ف",
        "ة": "ه",
        "ا": None,
    }
)  # fmt: skip


def match_key(word: str) -> str:
    return fold(word).replace(" ", "")


def rasm_key(word: str) -> str:
    k = match_key(word)
    # the Uthmani waw written for an alef: الصلوة, الزكوة, الربوا
    if k.endswith("وه"):
        k = k[:-2] + "ه"
    elif k.endswith("وا") and len(k) > 3:
        k = k[:-2]
    return k.translate(_UNDOT)


def _variant(a: str, b: str) -> bool:
    return a != b and rasm_key(a) == rasm_key(b)


@dataclass(slots=True)
class _Op:
    kind: str  # eq var sub ins del
    t: int | None  # transcript index
    qi: int | None  # quran word index


@lru_cache(maxsize=1)
def _aya_starts() -> tuple[int, ...]:
    """For each word of the text, the index of its verse's first word."""
    ix = q._index()
    out: list[int] = []
    start = 0
    for i, w in enumerate(ix.where):
        if i == 0 or w != ix.where[i - 1]:
            start = i
        out.append(start)
    return tuple(out)


@lru_cache(maxsize=1)
def _originals() -> tuple[str, ...]:
    """Each word of the index as Tanzil writes it (with its hamzas), for display; the
    folded word where the two cannot be lined up."""
    ix = q._index()
    out: list[str] = []
    for line in (q._DATA / "quran-simple-clean.txt").read_text(encoding="utf-8").splitlines():
        if not line or line.startswith("#"):
            continue
        sura, aya, verse = line.split("|", 2)
        toks = [t for t in verse.split() if fold(t)]
        if aya == "1" and sura != "1" and tuple(fold(" ".join(toks[:4])).split()) == q._BASMALA:
            toks = toks[4:]
        out.extend(toks)
    if len(out) != len(ix.words) or any(fold(a) != b for a, b in zip(out, ix.words, strict=True)):
        return ix.words
    return tuple(out)


def verse_word(qi: int) -> str:
    return _originals()[qi]


def _verse_back(qi: int, verses: int = 2) -> int:
    """The first word of the verse ``verses`` before the one ``qi`` is in."""
    starts = _aya_starts()
    s = starts[qi]
    for _ in range(verses):
        if s == 0:
            break
        s = starts[s - 1]
    return s


def _extend(keys: list[str], t: int, qi: int, limit: int, step: int) -> tuple[list[_Op], float]:
    """Ops from transcript word ``t`` and text word ``qi`` on, in direction ``step`` (+1
    forward, -1 backward), up to transcript index ``limit`` (exclusive going forward,
    inclusive going back). Stops when the last :data:`WINDOW` transcript words cost more
    than :data:`WINDOW_COST`; the trailing non-equal ops are dropped."""
    text = q._index().words
    ops: list[_Op] = []
    cost: list[float] = []  # per transcript word, in order of ops that consume one

    def inside(ti: int) -> bool:
        return ti < limit if step > 0 else ti >= limit

    def tk(ti: int) -> str | None:
        return keys[ti] if 0 <= ti < len(keys) and inside(ti) else None

    def qk(i: int) -> str | None:
        return text[i] if 0 <= i < len(text) else None

    while inside(t) and qk(qi) is not None:
        a, b = keys[t], text[qi]
        if a == b:
            ops.append(_Op("eq", t, qi))
            cost.append(0.0)
            t, qi = t + step, qi + step
        elif _variant(a, b):
            ops.append(_Op("var", t, qi))
            cost.append(VARIANT_COST)
            t, qi = t + step, qi + step
        else:
            n1t, n1q = tk(t + step), qk(qi + step)
            if step > 0 and n1t is not None and qi > 0:
                # a reciter going back to repeat a phrase: no penalty
                back = _verse_back(qi)
                hit = next(
                    (j for j in range(qi - 1, back - 1, -1) if text[j] == a and qk(j + 1) == n1t),
                    None,
                )
                if hit is not None:
                    qi = hit
                    continue
            if n1t is not None and n1q is not None and n1t == n1q:
                ops.append(_Op("sub", t, qi))
                cost.append(1.0)
                t, qi = t + step, qi + step
            elif n1q is not None and a == n1q:
                ops.append(_Op("del", None, qi))
                if cost:
                    cost[-1] += 1.0
                else:
                    cost.append(1.0)
                qi += step
            elif n1t is not None and n1t == b:
                ops.append(_Op("ins", t, None))
                cost.append(1.0)
                t += step
            else:
                ops.append(_Op("sub", t, qi))
                cost.append(1.0)
                t, qi = t + step, qi + step
        if sum(cost[-WINDOW:]) > WINDOW_COST:
            break
    while ops and ops[-1].kind not in ("eq", "var"):
        ops.pop()
    total = sum(0.0 if o.kind == "eq" else VARIANT_COST if o.kind == "var" else 1.0 for o in ops)
    return ops, total


def _phrase_at(keys: list[str], i: int) -> tuple[str, int] | None:
    for name, words in PHRASES.items():
        if tuple(keys[i : i + len(words)]) == words:
            return name, len(words)
    return None


def _anchors(keys: list[str], i: int) -> tuple[int, list[int]]:
    """The longest run of equal words from transcript word ``i``, and every place in the
    text it starts (empty under :data:`MIN_ANCHOR`)."""
    if i + 1 >= len(keys):
        return 0, []
    ix = q._index()
    best, places = 0, []
    for start in ix.by_two.get((keys[i], keys[i + 1]), ()):
        n = 2
        while (
            i + n < len(keys) and start + n < len(ix.words) and ix.words[start + n] == keys[i + n]
        ):
            n += 1
        if n > best:
            best, places = n, [start]
        elif n == best:
            places.append(start)
    return (best, places) if best >= MIN_ANCHOR else (0, [])


def _ref(qi: int) -> dict[str, Any]:
    ix = q._index()
    s, a = ix.where[qi]
    return {"surah": s, "aya": a, "word_index_in_aya": qi - _aya_starts()[qi]}


def _run_ref(qs: list[int]) -> dict[str, Any]:
    ix = q._index()
    first, last = ix.where[min(qs)], ix.where[max(qs)]
    ar, en, _ = ix.names[first[0] - 1]
    if first[0] == last[0]:
        ref = q.VerseRef(first[0], first[1], last[1], ar, en)
    else:
        ref = q.VerseRef(first[0], first[1], first[1], ar, en)
    return {
        "surah": first[0],
        "first": first[1],
        "last": last[1] if first[0] == last[0] else first[1],
        "label_ar": ref.label(arabic=True),
        "label_en": ref.label(arabic=False),
    }


def align(words: list[str]) -> list[ScriptureRun]:
    """The verse runs of a transcript's Arabic words (as written; keyed here)."""
    keys = [match_key(w) for w in words]
    runs: list[ScriptureRun] = []
    i = 0
    done = 0  # transcript words before this are another run's
    while i < len(keys):
        phrase = _phrase_at(keys, i)
        if phrase is not None:
            name, n = phrase
            runs.append(
                ScriptureRun(
                    i, i + n, {"phrase": name},
                    tuple(ScriptureOp("eq", k, None, {"phrase": name}) for k in range(i, i + n)),
                )
            )  # fmt: skip
            i = done = i + n
            continue
        n, places = _anchors(keys, i)
        if not places:
            i += 1
            continue
        scored: list[tuple[float, list[_Op]]] = []
        for start in places[:64]:
            fwd, fc = _extend(keys, i + n, start + n, len(keys), 1)
            back, bc = _extend(keys, i - 1, start - 1, done, -1)
            mid = [_Op("eq", i + k, start + k) for k in range(n)]
            ops = list(reversed(back)) + mid + fwd
            good = sum(1 for o in ops if o.kind == "eq") + 0.7 * sum(
                1 for o in ops if o.kind == "var"
            )
            scored.append((good - fc - bc, ops))
        scored.sort(key=lambda x: -x[0])
        if len(scored) > 1 and scored[0][0] == scored[1][0]:
            # the same words in more than one place: left unchecked (no flag, no protection)
            i = done = i + n
            continue
        ops = scored[0][1]
        ts = [o.t for o in ops if o.t is not None]
        lo, hi = min(ts), max(ts) + 1
        heard = hi - lo
        good = sum(1 for o in ops if o.kind in ("eq", "var"))
        qs = [o.qi for o in ops if o.qi is not None]
        kept = heard >= MIN_ANCHOR and good / max(1, heard) >= MIN_GOOD
        out: list[ScriptureOp] = []
        pending_missing: list[_Op] = []
        for o in ops:
            if o.kind == "del":
                pending_missing.append(o)
                continue
            assert o.t is not None
            if pending_missing:
                for m in pending_missing:
                    assert m.qi is not None
                    out.append(ScriptureOp("del", o.t, verse_word(m.qi), _ref(m.qi)))
                pending_missing = []
            if o.kind == "ins":
                out.append(ScriptureOp("ins", o.t, None, {}))
            else:
                assert o.qi is not None
                out.append(ScriptureOp(o.kind, o.t, verse_word(o.qi), _ref(o.qi)))  # type: ignore[arg-type]
        runs.append(ScriptureRun(lo, hi, _run_ref(qs), tuple(out), rejected=not kept))
        i = done = hi
    return runs
