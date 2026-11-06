"""From sentences to flags, without a database (Epic 52, story 52.6)."""

from __future__ import annotations

from maktaba_pipeline.language_packs.en import EnglishPack
from maktaba_pipeline.language_packs.fallback import FallbackPack
from maktaba_pipeline.stt.settings import Word
from maktaba_pipeline.word_review import signals
from maktaba_pipeline.word_review.settings import WordReviewConfig, effective

from ..language_packs.fixtures import DICTS

EN = EnglishPack(DICTS / "en")


def _inp(sentences: list[signals.SentenceIn], **kw: object) -> signals.Inputs:
    return signals.Inputs(
        sentences=sentences,
        language="en",
        settings=effective(WordReviewConfig(), kw.pop("library", None)),
        pack_for=lambda script, lang: EN if script == "Latn" else FallbackPack("und"),
        audio_sec=float(kw.pop("audio", 3600.0)),  # type: ignore[arg-type]
    )


def test_adjacent_suspect_words_merge_into_one_span() -> None:
    s = signals.SentenceIn(0, 0.0, 3.0, "the sky zxq qwv is blue")
    flags = signals.check(_inp([s]))
    assert len(flags) == 1
    f = flags[0]
    assert f.original == "zxq qwv" and f.word_count == 2 and f.char_start == 8


def test_spans_stop_at_four_words() -> None:
    s = signals.SentenceIn(0, 0.0, 3.0, "zxa zxb zxc zxd zxe the sky")
    flags = signals.check(_inp([s]))
    assert [f.word_count for f in flags] == [4, 1]


def test_a_capitalised_word_mid_sentence_alone_is_not_flagged() -> None:
    s = signals.SentenceIn(0, 0.0, 3.0, "the sky Ahmadou is blue")
    assert signals.check(_inp([s])) == []


def test_low_sensitivity_flags_less() -> None:
    s = signals.SentenceIn(0, 0.0, 1.0, "the zxq")  # text only: one dictionary signal
    assert len(signals.check(_inp([s]))) == 1
    assert signals.check(_inp([s], library={"sensitivity": "low"})) == []


def test_whisper_s_confidence_without_a_curve_counts_under_the_raw_floor() -> None:
    words = [Word(0.0, 0.5, "the", 0.9), Word(0.5, 1.0, "sky", 0.28)]
    s = signals.SentenceIn(0, 0.0, 1.0, "the sky", words)
    (f,) = signals.check(_inp([s]))
    assert f.reasons == ["low_confidence"] and f.original == "sky"
    s = signals.SentenceIn(0, 0.0, 1.0, "the sky", [words[0], words[1]._replace(prob=0.35)])
    assert signals.check(_inp([s])) == []


def test_a_hallucinated_sentence_over_silence_is_one_transcript_flag() -> None:
    sentences = [
        signals.SentenceIn(
            i,
            i * 2.0,
            i * 2.0 + 1,
            "zxq the",
            [
                Word(i * 2.0, i * 2.0 + 0.5, "zxq", 0.1),
                Word(i * 2.0 + 0.5, i * 2.0 + 1, "the", 0.9),
            ],
            no_speech=0.9,
        )  # fmt: skip
        for i in range(3)
    ]
    flags = signals.check(_inp(sentences))
    assert [f.kind for f in flags] == ["transcript"]
    assert flags[0].scores["hallucinated_sentences"] == 3


def test_the_same_input_gives_the_same_flags() -> None:
    s = signals.SentenceIn(0, 0.0, 3.0, "teh sky zxq is blue")
    a, b = signals.check(_inp([s])), signals.check(_inp([s]))
    assert a == b and a
