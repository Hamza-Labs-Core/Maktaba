"""From a transcript's sentences to its suspect words (Epic 52, stories 52.6 and 52.15).

Pure and deterministic: the same sentences, packs, vocabulary, names and settings give the
same flags, in the same order, byte for byte. The stage (``engine/stages/word_check.py``)
reads the inputs and writes the output; this module decides.

Per transcript:

1. Each sentence splits into script runs; each run goes to its pack (the registry's
   choice for that script and the transcript's language) for tokens.
2. Scripture first: a pack with a canonical text (Arabic: the Quran) aligns its words with
   it. Words equal to the verse (or a variant of it) are protected; words that differ get
   ``quran_mismatch`` alone. With that reason off, whole verse runs are protected.
3. Every other word token gets its signals: Whisper's confidence through the calibration
   curve, the second listen's marks, the dictionary, the vocabulary; a name match halves
   the total. Flagged at the library's threshold.
4. Adjacent flagged words merge (at most :data:`~.scoring.MAX_SPAN`); the budget keeps the
   top ones open and the rest skipped; a transcript with too many gets one more flag.
"""

from __future__ import annotations

from collections.abc import Callable, Mapping, Sequence
from dataclasses import dataclass, field
from typing import Any

from ..language_packs.base import Known, LanguagePack, ScriptureRun, Token
from ..language_packs.scripts import runs as script_runs
from ..stt.settings import HEARD_DIFFERENTLY, INSERTED, Word
from . import scoring
from .gazetteer import Gazetteer
from .settings import Effective
from .vocabulary import Vocabulary

__all__ = ["Flag", "Inputs", "SentenceIn", "check", "words_of"]

#: candidates kept per flag
MAX_CANDIDATES = 5


@dataclass(frozen=True, slots=True)
class SentenceIn:
    seq: int
    start: float
    end: float
    text: str
    words: list[Word] | None = None
    alt: list[dict[str, Any]] | None = None
    no_speech: float = 0.0


@dataclass(slots=True)
class Flag:
    kind: str
    segment_seq: int
    word_index: int
    word_count: int
    char_start: int
    char_len: int
    start_sec: float | None
    end_sec: float | None
    original: str
    context: str
    language: str
    reasons: list[str]
    scores: dict[str, Any]
    priority: float
    candidates: list[dict[str, Any]]
    key: str
    pack: str
    status: str = "open"


@dataclass(slots=True)
class Inputs:
    sentences: list[SentenceIn]
    language: str
    settings: Effective
    #: (script, language) -> pack: the registry's choice
    pack_for: Callable[[str, str], LanguagePack]
    model: str = ""
    audio_sec: float = 0.0
    names: Gazetteer = field(default_factory=Gazetteer)
    vocabulary: Vocabulary = field(default_factory=Vocabulary)
    #: (pack code, model) -> calibration curve
    curve_for: Callable[[str, str], scoring.Curve] = scoring.curve_for


@dataclass(slots=True)
class _Tok:
    seq: int
    tok: Token
    pack: LanguagePack
    index: int  # word index in its sentence (its pack's word tokens)
    protected: bool = False
    quran: dict[str, Any] | None = None
    quran_candidate: str | None = None


def _round(x: float) -> float:
    return round(float(x), 4)


def _tokens(inp: Inputs) -> list[_Tok]:
    out: list[_Tok] = []
    for s in inp.sentences:
        n = 0
        for run in script_runs(s.text):
            pack = inp.pack_for(run.script, inp.language)
            for t in pack.tokens(s.text[run.start : run.end]):
                tok = Token(t.text, t.start + run.start, t.end + run.start, t.kind, t.script)
                out.append(_Tok(s.seq, tok, pack, n))
                if tok.kind == "word":
                    n += 1
    return out


def _scripture(toks: list[_Tok], quran_on: bool) -> None:
    """Protect verse runs' words, and mark the mismatches (in place)."""
    by_pack: dict[int, list[int]] = {}
    for i, t in enumerate(toks):
        by_pack.setdefault(id(t.pack), []).append(i)
    for idx in by_pack.values():
        pack = toks[idx[0]].pack
        tokens = [toks[i].tok for i in idx]
        try:
            runs: list[ScriptureRun] = pack.scripture(tokens)
        except Exception:  # noqa: BLE001 - a pack without a working check checks nothing
            runs = []
        for run in runs:
            if run.rejected:
                continue
            if not quran_on or "phrase" in run.ref:
                for k in range(run.start, run.end):
                    toks[idx[k]].protected = True
                continue
            for op in run.ops:
                if op.token is None:
                    continue
                t = toks[idx[op.token]]
                if op.kind in ("eq", "var") and t.quran is None:
                    t.protected = True  # a variant is no error of hearing either
                if op.kind in ("sub", "ins", "del"):
                    t.protected = False
                    q = {**run.ref, **op.ref, "kind": op.kind}
                    if op.kind == "del":
                        q["missing"] = op.expected
                    elif op.expected:
                        q["expected"] = op.expected
                    t.quran = q
                    t.quran_candidate = op.expected


def _whisper_of(s: SentenceIn, tok: Token) -> list[Word]:
    """The Whisper words that overlap the token (by code points)."""
    if not s.words:
        return []
    out = []
    pos = 0
    for w in s.words:
        text = w.text.strip()
        at = s.text.find(text, pos) if text else -1
        if at < 0:
            continue
        pos = at + len(text)
        if at < tok.end and at + len(text) > tok.start:
            out.append(w)
    return out


def _alt_of(s: SentenceIn, tok: Token) -> list[tuple[str, str]]:
    """(whisper, cohere) of the second-listen replacements covering the token."""
    if not s.alt or not s.words:
        return []
    spans = []
    pos = 0
    starts: list[tuple[int, int]] = []
    for w in s.words:
        text = w.text.strip()
        at = s.text.find(text, pos) if text else -1
        if at < 0:
            starts.append((-1, -1))
            continue
        pos = at + len(text)
        starts.append((at, at + len(text)))
    for a in s.alt:
        try:
            i, n = int(a["i"]), int(a.get("n", 1))
        except (KeyError, TypeError, ValueError):
            continue
        covered = [starts[k] for k in range(i, min(len(starts), i + n)) if starts[k][0] >= 0]
        if any(x < tok.end and y > tok.start for x, y in covered):
            spans.append((str(a.get("whisper") or ""), str(a.get("cohere") or "")))
    return spans


def _times(s: SentenceIn, a: int, b: int, words: list[Word]) -> tuple[float, float, bool]:
    if words:
        return _round(min(w.start for w in words)), _round(max(w.end for w in words)), False
    n = max(1, len(s.text))
    span = max(0.0, s.end - s.start)
    return _round(s.start + span * a / n), _round(s.start + span * b / n), True


def _known(pack: LanguagePack, tok: _Tok, mid: bool) -> tuple[Known, str | None]:
    at = getattr(pack, "known_at", None)
    try:
        if at is not None:
            got: tuple[Known, str | None] = at(tok.tok.text, mid)
            return got
        return pack.known(tok.tok.text)
    except Exception:  # noqa: BLE001 - a pack's failure is no evidence
        return Known.UNSURE, None


def _add(cands: list[dict[str, Any]], text: str, source: str, score: float, original: str) -> None:
    text = text.strip()
    if not text or any(c["text"] == text for c in cands):
        return
    if text == original and source not in ("second_listen",):
        return
    cands.append({"text": text, "source": source, "score": round(score, 3)})


def _word_flag(inp: Inputs, s: SentenceIn, t: _Tok, first_word: bool) -> Flag | None:
    tok, pack = t.tok, t.pack
    original = s.text[tok.start : tok.end]
    words = _whisper_of(s, tok)
    text_only = not s.words
    reasons = inp.settings.reasons
    sig: dict[str, float] = {}
    scores: dict[str, Any] = {}
    cands: list[dict[str, Any]] = []
    key = pack.key(original)
    if t.quran is not None:
        if "quran_mismatch" not in reasons:
            return None
        sig["quran_mismatch"] = scoring.P["quran_mismatch"]
        scores["quran"] = t.quran
        if t.quran_candidate:
            kind = t.quran.get("kind")
            _add(cands, t.quran_candidate, "quran", 0.9, "" if kind == "del" else original)
    else:
        probs = [w.prob for w in words if w.prob is not None]
        bits = 0
        for w in words:
            bits |= w.flags
        if probs and "low_confidence" in reasons:
            prob = min(probs)
            curve = inp.curve_for(pack.code, inp.model)
            p_cal = curve(prob)
            scores["prob"] = _round(prob)
            scores["prob_cal"] = _round(p_cal)
            if (curve.calibrated and p_cal >= 0.5) or (
                not curve.calibrated and prob < scoring.RAW_FLOOR
            ):
                sig["low_confidence"] = p_cal
        if bits & (HEARD_DIFFERENTLY | INSERTED) and "listeners_disagree" in reasons:
            sig["listeners_disagree"] = scoring.P["listeners_disagree"]
            scores["listen"] = "inserted" if bits & INSERTED else "replaced"
            for whisper, cohere in _alt_of(s, tok):
                _add(cands, cohere, "second_listen", 0.8, "")
                _add(cands, whisper, "whisper", 0.6, original)
        vocab_known = inp.vocabulary.knows(pack.code, key)
        state, analysis = (
            (Known.KNOWN, "vocabulary") if vocab_known else _known(pack, t, not first_word)
        )
        scores["oov"] = state.value
        if analysis:
            scores["analysis"] = analysis
        if state is Known.UNKNOWN and "not_in_dictionary" in reasons:
            sig["not_in_dictionary"] = scoring.P["not_in_dictionary"]
        elif state is Known.UNSURE and "not_in_dictionary" in reasons:
            sig["unsure"] = scoring.P["unsure"]
        if not vocab_known and state is not Known.KNOWN and "near_vocabulary" in reasons:
            near = inp.vocabulary.near(pack.code, key)
            if near is not None:
                sig["near_vocabulary"] = scoring.P["near_vocabulary"]
                _add(cands, near, "vocabulary", 0.7, original)
        meant = inp.vocabulary.meant(pack.code, key)
        if meant:
            cands.insert(0, {"text": meant, "source": "vocabulary", "score": 0.95})
    if not sig or set(sig) == {"unsure"}:
        return None
    priority = scoring.combine(sig.values())
    name = inp.names.match(pack.code, key) if "named_entity" in reasons else None
    if name is not None and t.quran is None:
        priority *= scoring.NAME_FACTOR
        scores["entity"] = name
        _add(cands, name, "gazetteer", 0.7, original)
    if priority < scoring.threshold(inp.settings.sensitivity, text_only):
        return None
    if t.quran is None:
        try:
            for sug in pack.suggest(original, 3):
                _add(cands, sug, "dictionary", 0.5, original)
        except Exception:  # noqa: BLE001 - no suggestions, still a flag
            pass
    reason_list = [r for r in scoring_order(sig)]
    if name is not None and t.quran is None:
        reason_list.append("named_entity")
    a, b, estimated = _times(s, tok.start, tok.end, words)
    if estimated:
        scores["time_estimated"] = True
    scores["p"] = {k: _round(v) for k, v in sorted(sig.items())}
    return Flag(
        kind="word",
        segment_seq=s.seq,
        word_index=t.index,
        word_count=1,
        char_start=tok.start,
        char_len=tok.end - tok.start,
        start_sec=a,
        end_sec=b,
        original=original,
        context=s.text,
        language=pack.code,
        reasons=reason_list,
        scores=scores,
        priority=_round(priority),
        candidates=cands[:MAX_CANDIDATES],
        key=key,
        pack=f"{getattr(pack, 'pack_id', pack.code)}:{pack.version}",
    )


_ORDER = ("quran_mismatch", "listeners_disagree", "low_confidence", "not_in_dictionary",
          "near_vocabulary")  # fmt: skip


def scoring_order(sig: Mapping[str, float]) -> list[str]:
    return [r for r in _ORDER if r in sig]


def _merge(flags: list[Flag], sentences: Mapping[int, SentenceIn]) -> list[Flag]:
    """Adjacent flagged words of one sentence (only spaces between) as one span."""
    out: list[Flag] = []
    for f in flags:
        prev = out[-1] if out else None
        if (
            prev is not None
            and prev.segment_seq == f.segment_seq
            and prev.language == f.language
            and prev.word_count < scoring.MAX_SPAN
            and f.word_index == prev.word_index + prev.word_count
            and not sentences[f.segment_seq]
            .text[prev.char_start + prev.char_len : f.char_start]
            .strip()
        ):
            s = sentences[f.segment_seq]
            end = f.char_start + f.char_len
            prev.char_len = end - prev.char_start
            prev.original = s.text[prev.char_start : end]
            prev.word_count += 1
            prev.end_sec = f.end_sec
            prev.reasons = [r for r in dict.fromkeys(prev.reasons + f.reasons)]
            prev.priority = max(prev.priority, f.priority)
            if prev.scores.get("quran") or f.scores.get("quran"):
                prev.scores.setdefault("quran", f.scores.get("quran"))
            prev.key = prev.key + " " + f.key
            prev.candidates = _span_candidates(prev, f)
            continue
        out.append(f)
    return out


def _span_candidates(a: Flag, b: Flag) -> list[dict[str, Any]]:
    """A merged span keeps its words' joined first candidates, then each one's own."""
    out: list[dict[str, Any]] = []
    if a.candidates and b.candidates:
        first = a.candidates[0]
        out.append(
            {
                "text": first["text"] + " " + b.candidates[0]["text"],
                "source": first["source"],
                "score": first["score"],
            }
        )
    return out[:MAX_CANDIDATES]


def check(inp: Inputs) -> list[Flag]:
    """The transcript's flags, ordered by (sentence, place): open, skipped over budget,
    and the "may be poor" flag last when there is one."""
    toks = _tokens(inp)
    _scripture(toks, "quran_mismatch" in inp.settings.reasons)
    by_seq = {s.seq: s for s in inp.sentences}
    flags: list[Flag] = []
    words = 0
    seen_word: set[int] = set()
    for t in toks:
        if t.tok.kind != "word":
            continue
        words += 1
        first = t.seq not in seen_word
        seen_word.add(t.seq)
        if t.protected:
            continue
        s = by_seq[t.seq]
        if s.no_speech > 0.6 and s.words and t.quran is None:
            continue  # a hallucinated sentence over silence: the transcript flag says it
        f = _word_flag(inp, s, t, first)
        if f is not None:
            flags.append(f)
    flags = _merge(flags, by_seq)
    return _budget(inp, flags, words)


def _budget(inp: Inputs, flags: list[Flag], words: int) -> list[Flag]:
    hours = max(0.0, inp.audio_sec) / 3600.0
    n_open = max(scoring.BUDGET_FLOOR, int(round(inp.settings.budget_per_hour * hours)))
    ranked = sorted(flags, key=lambda f: (-f.priority, f.segment_seq, f.char_start))
    over = 0
    for i, f in enumerate(ranked):
        if i >= n_open:
            f.status = "skipped"
            f.scores["over_budget"] = True
            over += 1
    out = sorted(flags, key=lambda f: (f.segment_seq, f.char_start))
    hallucinated = sum(1 for s in inp.sentences if s.no_speech > 0.6 and s.words)
    suspect_share = len(flags) / words if words else 0.0
    if flags and (
        (over and over / len(flags) > scoring.POOR_OVER_BUDGET)
        or (words >= scoring.POOR_MIN_WORDS and suspect_share > scoring.POOR_SUSPECT_SHARE)
        or hallucinated >= 3
    ):
        counts: dict[str, int] = {}
        for f in flags:
            for r in f.reasons:
                counts[r] = counts.get(r, 0) + 1
        out.append(
            Flag(
                kind="transcript",
                segment_seq=-1,
                word_index=0,
                word_count=0,
                char_start=0,
                char_len=0,
                start_sec=None,
                end_sec=None,
                original="",
                context="",
                language=inp.language,
                reasons=sorted(counts),
                scores={
                    "suspect": len(flags),
                    "over_budget": over,
                    "words": words,
                    "reasons": dict(sorted(counts.items())),
                    "hallucinated_sentences": hallucinated,
                },  # fmt: skip
                priority=1.0,
                candidates=[],
                key="",
                pack="",
            )
        )
    return out


def words_of(sentences: Sequence[SentenceIn]) -> int:
    return sum(len(s.text.split()) for s in sentences)
