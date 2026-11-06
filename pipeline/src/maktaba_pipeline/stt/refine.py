"""A second listener for the words: Whisper's transcript, corrected by Cohere's.

On lectures Cohere Transcribe Arabic hears the words better than Whisper turbo (a blind
listening test of 20 spots where they disagreed: Cohere right 17 times, turbo 0, the
other 3 Cohere's word misspelt), but it spells loosely (no hamza, «ه» for «ة») and
invents text over Quran recitation. So each stretch of the transcript (up to
:data:`CHUNK_SEC`, whole sentences) is heard again by Cohere and the two are aligned
word by word on folded spelling:

- where they agree, Whisper's word stays (its spelling and its timing);
- where they disagree, Cohere's words replace Whisper's, respelt the way Whisper spells
  the same word elsewhere in the file or, failing that, the way the Arabic dictionary
  (CAMeL Tools' MSA analyser) spells it; they share the replaced words' time;
- where Cohere heard nothing, Whisper's words stay: Cohere drops words at the edges of a
  stretch, so a gap in its text is no evidence that nothing was said;
- words Whisper heard as a run of the Quran (:func:`~maktaba_pipeline.classify.quran.verse_runs`)
  are never replaced;
- a stretch where the two barely agree (below :data:`MIN_AGREEMENT`) keeps Whisper's text:
  one of them went astray, and Whisper is the one with timings.

The module is pure Python: the recogniser and the dictionary are passed in, so the
stage owns the models and the tests need neither.
"""

from __future__ import annotations

import difflib
import re
from collections import Counter
from collections.abc import Callable, Sequence
from dataclasses import dataclass, field
from typing import Any, Protocol

from ..classify.quran import verse_runs
from ..text.arabic import fold
from .settings import HEARD_DIFFERENTLY, INSERTED, QURAN, SAMPLE_RATE, Sentence, Transcript, Word

__all__ = [
    "Alt",
    "CHUNK_SEC",
    "MIN_AGREEMENT",
    "Dictionary",
    "RefineStats",
    "Speller",
    "chunks",
    "merge_words",
    "refine",
]

#: the longest stretch Cohere hears at once (it decodes 30 s windows; whole sentences)
CHUNK_SEC = 28.0

#: below this word agreement a stretch keeps Whisper's text (Cohere invents text over
#: recitation and music, or Whisper hallucinated over silence: either way, keep the timed one)
MIN_AGREEMENT = 0.5

#: a word as heard: :attr:`Sentence.words` (indexes as ``(start, end, text)``)
TimedWord = Word


@dataclass(frozen=True, slots=True)
class Alt:
    """Where the second listen replaced words (Epic 52, story 52.1): ``n`` words from
    index ``i`` of the merged output, what Whisper heard there and what Cohere heard."""

    i: int
    n: int
    whisper: str
    cohere: str

_EDGE = re.compile(r"^([^\w]*)(.*?)([^\w]*)$", re.S)


def _key(token: str) -> str:
    """A token's folded core: what two spellings of one word share."""
    return fold(token).replace(" ", "")


class Dictionary(Protocol):
    def spell(self, word: str) -> str | None:
        """The dictionary's spelling of ``word`` (hamzas, «ة», «ى» restored), or None."""
        ...


class Speller:
    """Spells a word Cohere heard: as the library's vocabulary spells it (Epic 52, story
    52.10), else as Whisper spelt it in this file, else as the dictionary does, else as
    heard. Only hamza / «ة» / «ى» forms ever change: a candidate must fold to the same
    letters."""

    def __init__(
        self,
        whisper_words: Sequence[str],
        dictionary: Dictionary | None,
        vocabulary: Sequence[str] = (),
    ) -> None:
        self._vocab: dict[str, str] = {}
        for term in vocabulary:
            for part in term.split():
                core = _EDGE.match(part)
                word = core.group(2) if core else part
                if word:
                    self._vocab.setdefault(_key(word), word)
        self._known: dict[str, Counter[str]] = {}
        for token in whisper_words:
            core = _EDGE.match(token)
            word = core.group(2) if core else token
            if word:
                self._known.setdefault(_key(word), Counter())[word] += 1
        self._dictionary = dictionary

    def spell(self, token: str) -> str:
        m = _EDGE.match(token)
        if m is None or not m.group(2):
            return token
        lead, word, trail = m.groups()
        key = _key(word)
        meant = self._vocab.get(key)
        if meant:
            return lead + meant + trail
        seen = self._known.get(key)
        if seen:
            return lead + seen.most_common(1)[0][0] + trail
        if self._dictionary is not None:
            spelt = self._dictionary.spell(word)
            if spelt and _key(spelt) == key:
                return lead + spelt + trail
        return token


def _spread(
    start: float,
    end: float,
    words: list[str],
    probs: Sequence[float | None] = (),
    flags: int = 0,
) -> list[TimedWord]:
    """Words sharing ``start``..``end`` evenly (Cohere gives no timings). ``probs``: the
    probability each keeps (the Whisper word it replaced), ``flags`` their bits."""
    if not words:
        return []
    step = max(0.0, end - start) / len(words)
    return [
        Word(
            round(start + i * step, 3),
            round(start + (i + 1) * step, 3),
            w,
            probs[min(i, len(probs) - 1)] if probs else None,
            flags,
        )
        for i, w in enumerate(words)
    ]


def merge_words(
    whisper: Sequence[TimedWord],
    cohere_text: str,
    locked: Sequence[bool],
    speller: Speller,
    alts: list[Alt] | None = None,
) -> tuple[list[TimedWord], int]:
    """Whisper's words with Cohere's where they disagree, and how many words changed.

    ``locked[i]`` keeps Whisper's word ``i`` whatever Cohere heard (a verse; marked
    :data:`QURAN`). A replaced word keeps the probability of the Whisper word it replaced
    and is marked :data:`HEARD_DIFFERENTLY`; a word Whisper missed is :data:`INSERTED`,
    with no probability. ``alts`` collects each replaced span (Epic 52, story 52.1)."""
    heard = cohere_text.split()
    a = [_key(w[2]) for w in whisper]
    b = [_key(w) for w in heard]
    out: list[TimedWord] = []
    changed = 0
    sm = difflib.SequenceMatcher(None, a, b, autojunk=False)
    for op, i1, i2, j1, j2 in sm.get_opcodes():
        if any(locked[i1:i2]):
            out.extend(_mark(whisper[i1:i2], locked[i1:i2]))
            continue
        if op == "equal":
            out.extend(whisper[i1:i2])
            continue
        new = [speller.spell(w) for w in heard[j1:j2] if _key(w)]
        if not new:  # Cohere heard nothing here (or only punctuation differs)
            out.extend(whisper[i1:i2])
            continue
        if i2 > i1:
            start, end = whisper[i1][0], whisper[i2 - 1][1]
            probs = [_prob(w) for w in whisper[i1:i2]]
            bits = HEARD_DIFFERENTLY
        else:  # Whisper missed words here: they go in the gap between its neighbours
            start = whisper[i1 - 1][1] if i1 > 0 else (whisper[0][0] if whisper else 0.0)
            end = whisper[i1][0] if i1 < len(whisper) else start
            probs, bits = [], INSERTED
        if alts is not None:
            alts.append(
                Alt(
                    len(out),
                    len(new),
                    " ".join(w[2].strip() for w in whisper[i1:i2]),
                    " ".join(new),
                )
            )
        out.extend(_spread(start, end, new, probs, bits))
        changed += max(i2 - i1, len(new))
    return out, changed


def _prob(w: Any) -> float | None:
    return w.prob if isinstance(w, Word) else None


def _mark(words: Sequence[Any], locked: Sequence[bool]) -> list[TimedWord]:
    """``words`` as they are, the verse's ones marked :data:`QURAN`."""
    out: list[TimedWord] = []
    for w, lock in zip(words, locked, strict=True):
        word = w if isinstance(w, Word) else Word(w[0], w[1], w[2])
        out.append(word._replace(flags=word.flags | QURAN) if lock else word)
    return out


def chunks(sentences: Sequence[Sentence], limit: float = CHUNK_SEC) -> list[list[int]]:
    """Consecutive sentences grouped into stretches of at most ``limit`` seconds (a
    longer sentence is a stretch on its own). Sentences without words are left out:
    there is nothing to align them on."""
    out: list[list[int]] = []
    cur: list[int] = []
    for i, s in enumerate(sentences):
        if not s.words:
            if cur:
                out.append(cur)
                cur = []
            continue
        if cur and s.end - sentences[cur[0]].start > limit:
            out.append(cur)
            cur = []
        cur.append(i)
    if cur:
        out.append(cur)
    return out


@dataclass(slots=True)
class RefineStats:
    model: str
    stretches: int = 0
    #: stretches that kept Whisper's text: the two disagreed too much
    kept: int = 0
    words: int = 0
    changed: int = 0
    #: words never replaced because Whisper heard them as a run of the Quran
    quran_words: int = 0
    errors: list[str] = field(default_factory=list)

    def as_dict(self) -> dict[str, Any]:
        return {
            "model": self.model,
            "stretches": self.stretches,
            "kept": self.kept,
            "words": self.words,
            "changed": self.changed,
            "quran_words": self.quran_words,
            **({"errors": self.errors[:5]} if self.errors else {}),
        }


def _agreement(whisper: Sequence[TimedWord], cohere_text: str) -> float:
    a = [_key(w[2]) for w in whisper]
    b = [_key(w) for w in cohere_text.split()]
    if not a and not b:
        return 1.0
    return difflib.SequenceMatcher(None, a, b, autojunk=False).ratio()


def _sentence_text(words: Sequence[TimedWord]) -> str:
    return " ".join(w[2].strip() for w in words if w[2].strip())


#: stretches handed to the recogniser at once (it batches them on the GPU)
BATCH = 32


@dataclass(slots=True)
class _Stretch:
    group: list[int]
    whisper: list[TimedWord]
    locked: list[bool]
    audio: Any


def refine(
    result: Transcript,
    audio: Any,
    recognise: Callable[[list[Any]], list[str]],
    dictionary: Dictionary | None,
    model: str,
    vocabulary: Sequence[str] = (),
) -> RefineStats:
    """Correct ``result``'s words in place with what ``recognise`` hears in each
    stretch of ``audio`` (16 kHz mono, on the transcript's own timeline). ``recognise``
    hears a list of stretches and returns their texts in the same order."""
    stats = RefineStats(model=model)
    sentences = result.sentences
    all_words = [w for s in sentences for w in (s.words or [])]
    speller = Speller([w[2] for w in all_words], dictionary, vocabulary)
    locked_all = verse_runs([_key(w[2]) for w in all_words])
    # the verse's words are marked whether or not their stretch is heard again
    k = 0
    for s in sentences:
        if s.words:
            s.words = _mark(s.words, locked_all[k : k + len(s.words)])
            k += len(s.words)
    first: dict[int, int] = {}  # sentence -> index of its first word in all_words
    n = 0
    for i, s in enumerate(sentences):
        first[i] = n
        n += len(s.words or [])
    todo: list[_Stretch] = []
    for group in chunks(sentences):
        stats.stretches += 1
        whisper = [w for i in group for w in (sentences[i].words or [])]
        lo = first[group[0]]
        locked = locked_all[lo : lo + len(whisper)]
        stats.words += len(whisper)
        stats.quran_words += sum(locked)
        a = max(0, int(sentences[group[0]].start * SAMPLE_RATE))
        b = min(len(audio), int(sentences[group[-1]].end * SAMPLE_RATE) + SAMPLE_RATE // 4)
        if b - a >= SAMPLE_RATE // 2:
            todo.append(_Stretch(group, whisper, locked, audio[a:b]))
    for k in range(0, len(todo), BATCH):
        batch = todo[k : k + BATCH]
        try:
            heard = recognise([s.audio for s in batch])
        except Exception as exc:  # noqa: BLE001 - these stretches keep Whisper's words
            stats.errors.append(f"{type(exc).__name__}: {exc}")
            stats.kept += len(batch)
            continue
        for st, text in zip(batch, heard, strict=True):
            if _agreement(st.whisper, text) < MIN_AGREEMENT:
                stats.kept += 1
                continue
            alts: list[Alt] = []
            merged, changed = merge_words(st.whisper, text, st.locked, speller, alts)
            if changed:
                stats.changed += changed
                _redistribute(sentences, st.group, merged, alts)

    # no word is ever dropped, but a replacement's time can move a short sentence's only
    # word into its neighbour: the emptied sentence goes
    result.sentences = [s for s in sentences if s.text]
    return stats


def _redistribute(
    sentences: list[Sentence],
    group: list[int],
    merged: list[TimedWord],
    alts: Sequence[Alt] = (),
) -> None:
    """Give each sentence of the stretch back its words (by time), its text and where
    the second listen replaced words (:attr:`Sentence.alt`, index in the sentence)."""
    bounds = [(sentences[i].start, sentences[i].end) for i in group]
    per: list[list[TimedWord]] = [[] for _ in group]
    where: list[tuple[int, int]] = []  # merged index -> (sentence of the group, index in it)
    k = 0
    for w in merged:
        mid = (w[0] + w[1]) / 2
        while k + 1 < len(group) and mid >= bounds[k + 1][0]:
            k += 1
        where.append((k, len(per[k])))
        per[k].append(w)
    found: list[list[dict[str, Any]]] = [[] for _ in group]
    for a in alts:
        if a.i < len(where):
            g, i = where[a.i]
            found[g].append({"i": i, "n": a.n, "whisper": a.whisper, "cohere": a.cohere})
    for g, (idx, words) in enumerate(zip(group, per, strict=True)):
        s = sentences[idx]
        s.words = words
        s.text = _sentence_text(words)
        s.alt = found[g] or None
