"""The English pack (Epic 52, story 52.5) against small fixture dictionaries."""

from __future__ import annotations

from maktaba_pipeline.language_packs.base import Known
from maktaba_pipeline.language_packs.en import EnglishPack

from ..fixtures import DICTS


def _en() -> EnglishPack:
    return EnglishPack(DICTS / "en")


def test_inflections_are_known() -> None:
    p = _en()
    assert all(p.known(w)[0] is Known.KNOWN for w in ("running", "ran", "runs", "cities"))


def test_american_and_british_spellings_are_both_known() -> None:
    assert _en().known("colour")[0] is Known.KNOWN and _en().known("color")[0] is Known.KNOWN


def test_a_typo_is_unknown_and_the_first_suggestion_is_right() -> None:
    assert _en().known("teh")[0] is Known.UNKNOWN
    assert _en().suggest("teh", 3)[0] == "the"


def test_a_capitalised_unknown_mid_sentence_is_unsure() -> None:
    assert _en().known_at("Ahmad's", True)[0] is Known.UNSURE


def test_transliterations_and_acronyms() -> None:
    p = _en()
    assert p.known("insha'Allah")[0] is Known.KNOWN and p.known("Insha’Allah")[0] is Known.KNOWN
    assert p.known("NASA")[0] is Known.KNOWN and p.known("XYZQ")[0] is Known.UNSURE


def test_tokens_of_contractions_hyphens_and_times() -> None:
    toks = _en().tokens("don't well-known 10:30")
    assert [(t.text, t.kind) for t in toks] == [("don't", "word"), ("well-known", "word"),
                                               ("10:30", "number")]  # fmt: skip
    assert _en().known("well-known")[0] is Known.KNOWN  # part by part


def test_keys_fold_case_quotes_and_diacritics() -> None:
    p = _en()
    assert p.key("Café") == p.key("cafe") and p.key("’tis'") == "tis"


def test_without_its_data_it_is_unsure() -> None:
    p = EnglishPack(None)
    assert p.known("teh")[0] is Known.UNSURE and p.dictionary == "unavailable"
