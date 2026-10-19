"""The second listen (stt/refine.py): Whisper's words corrected by Cohere's."""

from __future__ import annotations

from typing import Any

import pytest

from maktaba_pipeline.classify.quran import verse_runs
from maktaba_pipeline.engine.stages import transcribe as stage
from maktaba_pipeline.stt import refine as rf
from maktaba_pipeline.stt.settings import SAMPLE_RATE, Sentence, Transcript
from maktaba_pipeline.text.arabic import fold


class FakeDictionary:
    def __init__(self, spellings: dict[str, str]) -> None:
        self.spellings = spellings

    def spell(self, word: str) -> str | None:
        return self.spellings.get(word)


def timed(text: str, start: float = 0.0, step: float = 0.5) -> list[tuple[float, float, str]]:
    return [(start + i * step, start + (i + 1) * step, w) for i, w in enumerate(text.split())]


def sentence(text: str, start: float = 0.0, step: float = 0.5) -> Sentence:
    words = timed(text, start, step)
    return Sentence(start, words[-1][1], text, -0.1, 0.01, words)


def no_speller() -> rf.Speller:
    return rf.Speller([], None)


# ------------------------------------------------------------------ verse runs


def test_verse_runs_marks_recitation_and_not_speech() -> None:
    words = fold("قال الشيخ والسماء ذات البروج واليوم الموعود ثم سكت").split()
    marks = verse_runs(words)
    assert marks == [False, False, True, True, True, True, True, False, False]


def test_verse_runs_ignores_short_matches() -> None:
    assert not any(verse_runs(fold("قال الله تعالى").split()))


# ------------------------------------------------------------------ spelling


def test_speller_prefers_whisper_spelling_in_the_same_file() -> None:
    sp = rf.Speller(["الأرض", "خليفة", "خليفة"], FakeDictionary({"خليفه": "خليفهx"}))
    assert sp.spell("الارض") == "الأرض"
    assert sp.spell("خليفه،") == "خليفة،"  # punctuation kept around the word


def test_speller_falls_back_to_the_dictionary() -> None:
    sp = rf.Speller([], FakeDictionary({"بحفنه": "بحفنة"}))
    assert sp.spell("بحفنه") == "بحفنة"


def test_speller_refuses_a_dictionary_word_with_other_letters() -> None:
    sp = rf.Speller([], FakeDictionary({"تبغون": "تبعون"}))
    assert sp.spell("تبغون") == "تبغون"


def test_speller_keeps_unknown_words() -> None:
    assert rf.Speller([], FakeDictionary({})).spell("ملوش") == "ملوش"


# ------------------------------------------------------------------ merging


def test_merge_keeps_whisper_where_they_agree() -> None:
    w = timed("قال الإمام مالك")
    merged, changed = rf.merge_words(w, "قال الامام مالك", [False] * 3, no_speller())
    assert merged == w and changed == 0


def test_merge_takes_cohere_where_they_disagree_on_the_same_time() -> None:
    w = timed("أن الله لا يعيب جل وعلا")
    merged, changed = rf.merge_words(w, "أن الله لا يغيب جل وعلا", [False] * 6, no_speller())
    assert [x[2] for x in merged] == ["أن", "الله", "لا", "يغيب", "جل", "وعلا"]
    assert merged[3][:2] == w[3][:2] and changed == 1


def test_merge_spreads_several_words_over_the_replaced_ones() -> None:
    w = timed("وقيل لأي بك خليفة")
    merged, _ = rf.merge_words(w, "وقيل لأبي بكر خليفة", [False] * 4, no_speller())
    assert [x[2] for x in merged] == ["وقيل", "لأبي", "بكر", "خليفة"]
    assert merged[1][0] == w[1][0] and merged[2][1] == w[2][1]


def test_merge_puts_missed_words_in_the_gap() -> None:
    w = [(0.0, 0.5, "قال"), (2.0, 2.5, "خليفة")]
    merged, changed = rf.merge_words(w, "قال رسول الله خليفة", [False] * 2, no_speller())
    assert [x[2] for x in merged] == ["قال", "رسول", "الله", "خليفة"]
    assert merged[1][0] == 0.5 and merged[2][1] == 2.0 and changed == 2


def test_merge_never_touches_a_verse() -> None:
    w = timed("والسماء ذات البروج")
    merged, changed = rf.merge_words(w, "والسماء ذات البروز", [True] * 3, no_speller())
    assert merged == w and changed == 0


def test_merge_ignores_punctuation_only_differences() -> None:
    w = timed("نعم ؟")
    merged, changed = rf.merge_words(w, "نعم", [False] * 2, no_speller())
    assert merged == w and changed == 0


def test_merge_respells_what_cohere_heard() -> None:
    w = timed("أخذ بحثنة من التمر")
    sp = rf.Speller([x[2] for x in w], FakeDictionary({"بحفنه": "بحفنة"}))
    merged, _ = rf.merge_words(w, "اخذ بحفنه من التمر", [False] * 4, sp)
    assert [x[2] for x in merged] == ["أخذ", "بحفنة", "من", "التمر"]


# ------------------------------------------------------------------ stretches


def test_chunks_group_whole_sentences_up_to_the_limit() -> None:
    ss = [sentence("ا ب", 0), sentence("ج د", 10), sentence("ه و", 25), sentence("ز", 40)]
    ss.insert(2, Sentence(20, 21, "…", -1, 0.9, None))  # no words: splits the stretch
    assert rf.chunks(ss, limit=28) == [[0, 1], [3, 4]]
    assert rf.chunks(ss, limit=10) == [[0], [1], [3], [4]]


def transcript(*ss: Sentence) -> Transcript:
    return Transcript(list(ss), 60.0, "ar", "turbo", 1.0)


def silence(sec: float = 60.0) -> list[float]:
    return [0.0] * int(sec * SAMPLE_RATE)


def test_refine_corrects_words_and_reports() -> None:
    t = transcript(sentence("أن الله لا يعيب جل وعلا", 0), sentence("وقيل لأي بك خليفة", 5))
    stats = rf.refine(t, silence(), lambda _a: ["أن الله لا يغيب جل وعلا وقيل لأبي بكر خليفة"],
                      None, "cohere")  # fmt: skip
    assert [s.text for s in t.sentences] == ["أن الله لا يغيب جل وعلا", "وقيل لأبي بكر خليفة"]
    assert stats.as_dict() == {
        "model": "cohere", "stretches": 1, "kept": 0, "words": 10, "changed": 3,
        "quran_words": 0,
    }  # fmt: skip


def test_refine_keeps_whisper_when_the_two_barely_agree() -> None:
    t = transcript(sentence("والسماء ذات البروج واليوم الموعود"))
    before = [s.text for s in t.sentences]
    stats = rf.refine(t, silence(), lambda _a: ["سورة الأعراف الدرس الثاني"], None, "cohere")
    assert [s.text for s in t.sentences] == before and stats.kept == 1


def test_refine_survives_a_failing_recogniser() -> None:
    t = transcript(sentence("قال الشيخ كلاما طويلا"))

    def boom(_a: Any) -> list[str]:
        raise RuntimeError("GPU")

    stats = rf.refine(t, silence(), boom, None, "cohere")
    assert t.sentences[0].text == "قال الشيخ كلاما طويلا"
    assert stats.kept == 1 and stats.errors == ["RuntimeError: GPU"]


def test_refine_never_drops_words_cohere_did_not_hear() -> None:
    # Cohere loses words at the edge of a stretch: a gap in its text is not silence
    t = transcript(sentence("قال الشيخ في الدرس الأول كلاما", 0), sentence("بلا عقل ولا وعي", 4))
    stats = rf.refine(t, silence(), lambda _a: ["قال الشيخ في الدرس الأول كلاما"], None, "c")
    assert [s.text for s in t.sentences] == ["قال الشيخ في الدرس الأول كلاما", "بلا عقل ولا وعي"]
    assert stats.changed == 0


def test_merge_keeps_whisper_where_cohere_heard_nothing() -> None:
    w = timed("هذا كلام بلا عقل ولا وعي")
    merged, changed = rf.merge_words(w, "هذا كلام", [False] * 6, no_speller())
    assert merged == w and changed == 0


def test_refine_hears_the_stretch_of_audio_it_corrects() -> None:
    t = transcript(sentence("ا ب ج", 2.0, 1.0))
    seen: list[int] = []

    def hear(stretches: list[Any]) -> list[str]:
        seen.extend(len(a) for a in stretches)
        return ["ا ب ج"] * len(stretches)

    rf.refine(t, list(range(10 * SAMPLE_RATE)), hear, None, "cohere")
    assert seen == [3 * SAMPLE_RATE + SAMPLE_RATE // 4]


# ------------------------------------------------------------------ the stage


def test_refine_is_on_by_default_and_can_be_switched_off(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    monkeypatch.delenv("MAKTABA_STT_REFINE", raising=False)
    monkeypatch.delenv("MAKTABA_STT_REFINE_MODEL", raising=False)
    assert stage.refine_model() == "CohereLabs/cohere-transcribe-arabic-07-2026"
    monkeypatch.setenv("MAKTABA_STT_REFINE_MODEL", "x/y")
    assert stage.refine_model() == "x/y"
    monkeypatch.setenv("MAKTABA_STT_REFINE", "0")
    assert stage.refine_model() is None


def test_a_worker_without_the_model_keeps_whisper(monkeypatch: pytest.MonkeyPatch) -> None:
    from maktaba_pipeline.stt import cohere

    def missing(model: str, share: Any = None) -> Any:
        raise RuntimeError("gated repo")

    monkeypatch.setattr(cohere, "recogniser", missing)
    t = transcript(sentence("قال الشيخ"))
    out = stage._refine(t, silence(), "m")
    assert out == {"model": "m", "source": "skipped", "skipped": "RuntimeError: gated repo"}
    assert t.sentences[0].text == "قال الشيخ"


def test_refine_hears_a_files_stretches_together_in_order() -> None:
    t = transcript(sentence("لا يعيب جل وعلا", 0), sentence("وقيل لأي بك خليفة", 40))
    calls: list[int] = []

    def hear(stretches: list[Any]) -> list[str]:
        calls.append(len(stretches))
        return ["لا يغيب جل وعلا", "وقيل لأبي بكر خليفة"]

    stats = rf.refine(t, silence(), hear, None, "c")
    assert calls == [2] and stats.stretches == 2
    assert [s.text for s in t.sentences] == ["لا يغيب جل وعلا", "وقيل لأبي بكر خليفة"]


def test_a_merge_error_keeps_the_transcript(monkeypatch: pytest.MonkeyPatch) -> None:
    from maktaba_pipeline.stt import cohere

    monkeypatch.setattr(cohere, "recogniser", lambda _m, _share=None: lambda _s: ["x"])
    monkeypatch.setattr(cohere, "dictionary", lambda: None)

    def broken(*_a: Any, **_k: Any) -> Any:
        raise ValueError("bug")

    monkeypatch.setattr(rf, "refine", broken)
    t = transcript(sentence("قال الشيخ"))
    assert stage._refine(t, silence(), "m") == {
        "model": "m",
        "source": "hf",
        "failed": "ValueError: bug",
    }
    assert t.sentences[0].text == "قال الشيخ"
