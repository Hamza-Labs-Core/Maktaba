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
from .settings import SAMPLE_RATE, Sentence, Transcript

__all__ = [
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

#: a word as (start, end, text): :attr:`Sentence.words`
TimedWord = tuple[float, float, str]

_EDGE = re.compile(r"^([^\w]*)(.*?)([^\w]*)$", re.S)


def _key(token: str) -> str:
    """A token's folded core: what two spellings of one word share."""
    return fold(token).replace(" ", "")


class Dictionary(Protocol):
    def spell(self, word: str) -> str | None:
        """The dictionary's spelling of ``word`` (hamzas, «ة», «ى» restored), or None."""
        ...


class Speller:
    """Spells a word Cohere heard: as Whisper spelt it in this file, else as the
    dictionary does, else as heard. Only hamza / «ة» / «ى» forms ever change: a candidate
    must fold to the same letters."""

    def __init__(self, whisper_words: Sequence[str], dictionary: Dictionary | None) -> None:
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
        seen = self._known.get(key)
        if seen:
            return lead + seen.most_common(1)[0][0] + trail
        if self._dictionary is not None:
            spelt = self._dictionary.spell(word)
            if spelt and _key(spelt) == key:
                return lead + spelt + trail
        return token


def _spread(start: float, end: float, words: list[str]) -> list[TimedWord]:
    """Words sharing ``start``..``end`` evenly (Cohere gives no timings)."""
    if not words:
        return []
    step = max(0.0, end - start) / len(words)
    return [
        (round(start + i * step, 3), round(start + (i + 1) * step, 3), w)
        for i, w in enumerate(words)
    ]


def merge_words(
    whisper: Sequence[TimedWord],
    cohere_text: str,
    locked: Sequence[bool],
    speller: Speller,
) -> tuple[list[TimedWord], int]:
    """Whisper's words with Cohere's where they disagree, and how many words changed.

    ``locked[i]`` keeps Whisper's word ``i`` whatever Cohere heard (a verse)."""
    heard = cohere_text.split()
    a = [_key(w[2]) for w in whisper]
    b = [_key(w) for w in heard]
    out: list[TimedWord] = []
    changed = 0
    sm = difflib.SequenceMatcher(None, a, b, autojunk=False)
    for op, i1, i2, j1, j2 in sm.get_opcodes():
        if op == "equal" or any(locked[i1:i2]):
            out.extend(whisper[i1:i2])
            continue
        new = [speller.spell(w) for w in heard[j1:j2] if _key(w)]
        if not new:  # Cohere heard nothing here (or only punctuation differs)
            out.extend(whisper[i1:i2])
            continue
        if i2 > i1:
            start, end = whisper[i1][0], whisper[i2 - 1][1]
        else:  # Whisper missed words here: they go in the gap between its neighbours
            start = whisper[i1 - 1][1] if i1 > 0 else (whisper[0][0] if whisper else 0.0)
            end = whisper[i1][0] if i1 < len(whisper) else start
        out.extend(_spread(start, end, new))
        changed += max(i2 - i1, len(new))
    return out, changed


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
) -> RefineStats:
    """Correct ``result``'s words in place with what ``recognise`` hears in each
    stretch of ``audio`` (16 kHz mono, on the transcript's own timeline). ``recognise``
    hears a list of stretches and returns their texts in the same order."""
    stats = RefineStats(model=model)
    sentences = result.sentences
    all_words = [w for s in sentences for w in (s.words or [])]
    speller = Speller([w[2] for w in all_words], dictionary)
    locked_all = verse_runs([_key(w[2]) for w in all_words])
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
            merged, changed = merge_words(st.whisper, text, st.locked, speller)
            if changed:
                stats.changed += changed
                _redistribute(sentences, st.group, merged)
    # no word is ever dropped, but a replacement's time can move a short sentence's only
    # word into its neighbour: the emptied sentence goes
    result.sentences = [s for s in sentences if s.text]
    return stats


def _redistribute(sentences: list[Sentence], group: list[int], merged: list[TimedWord]) -> None:
    """Give each sentence of the stretch back its words (by time), and its text."""
    bounds = [(sentences[i].start, sentences[i].end) for i in group]
    per: list[list[TimedWord]] = [[] for _ in group]
    k = 0
    for w in merged:
        mid = (w[0] + w[1]) / 2
        while k + 1 < len(group) and mid >= bounds[k + 1][0]:
            k += 1
        per[k].append(w)
    for idx, words in zip(group, per, strict=True):
        s = sentences[idx]
        s.words = words
        s.text = _sentence_text(words)
