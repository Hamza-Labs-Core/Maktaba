"""The Arabic pack (Epic 52, story 52.4), with a tiny fake analyser and a fixture Hunspell."""

from __future__ import annotations

from maktaba_pipeline.language_packs.ar import ArabicPack
from maktaba_pipeline.language_packs.base import Known
from maktaba_pipeline.text.arabic import ARABIC_VERSION, fold

from ..fixtures import DICTS, fake_analyser

KNOWN = {"وسيكتبونها", "يكتب", "مدرسة", "قال", "الحمد"}


def _pack(analyser: bool = True) -> ArabicPack:
    return ArabicPack(DICTS / "ar", analyse=fake_analyser(KNOWN) if analyser else None)  # type: ignore[arg-type]


def test_a_cliticised_verb_is_known_with_its_analysis() -> None:
    state, why = _pack().known("وَسَيَكْتُبُونَهَا")
    assert state is Known.KNOWN and why is not None and "noun" in why


def test_hamza_forms_share_a_key() -> None:
    p = _pack()
    assert p.key("اسلام") == p.key("إسلام") == p.key("إِسْلَام")
    assert p.key(p.key("أُمَّة")) == p.key("أُمَّة")


def test_persian_look_alikes_fold_too() -> None:
    assert fold("کتاب") == fold("كتاب") and fold("علی") == fold("علي")
    assert ARABIC_VERSION >= 2


def test_a_dialect_word_is_unsure_not_unknown() -> None:
    assert _pack().known("بيكتب")[0] is Known.UNSURE


def test_nonsense_is_unknown_and_hunspell_knows_nouns() -> None:
    p = _pack()
    assert p.known("كتسبلمن")[0] is Known.UNKNOWN
    assert p.known("كتاب")[0] is Known.KNOWN  # Hunspell, not the analyser
    assert p.known("٣")[0] is Known.KNOWN and p.known("و")[0] is Known.KNOWN


def test_a_ta_marbuta_spelling_is_suggested_first() -> None:
    assert _pack().suggest("مدرسه", 3)[0] == "مدرسة"


def test_the_honorific_ligature_is_a_symbol() -> None:
    toks = _pack().tokens("قال النبي ﷺ: إنما")
    assert [(t.text, t.kind) for t in toks if t.text == "ﷺ"] == [("ﷺ", "symbol")]


def test_without_the_analyser_nothing_is_unknown() -> None:
    p = _pack(analyser=False)
    assert p.analyser == "unavailable" and "unavailable" in p.version
    assert p.known("كتسبلمن")[0] is Known.UNSURE
    assert p.known("كتاب")[0] is Known.KNOWN


def test_a_correct_verse_run_is_protected_and_a_misheard_word_is_not() -> None:
    p = _pack()
    toks = p.tokens("الحمد لله رب العالمين الرحمن الرحيم")
    assert p.protected(toks) == [True] * 6
    toks = p.tokens("الحمد لله رب العالمون الرحمن الرحيم مالك يوم الدين")
    prot = p.protected(toks)
    assert prot[3] is False and all(prot[i] for i in range(9) if i != 3)
    assert any(r for r in p.scripture(toks) if not r.rejected)


def test_warm_checks_are_cheap() -> None:
    import time

    p = _pack()
    words = ("قال الشيخ في الدرس هذا كتاب مهم والطالب ذهب الى المدرسة " * 900).split()
    for w in words:
        p.known(w)
    t = time.process_time()
    for w in words:
        p.known(w)
    assert time.process_time() - t < 1.0
