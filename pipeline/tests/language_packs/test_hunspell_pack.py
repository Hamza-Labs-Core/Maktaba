"""The generic Hunspell pack (Epic 52, story 52.3) over small fixture dictionaries."""

from __future__ import annotations

from maktaba_pipeline.language_packs.base import Known
from maktaba_pipeline.language_packs.hunspell import HunspellPack
from maktaba_pipeline.language_packs.hunspell_reader import load

from .fixtures import DICTS


def test_a_french_misspelling_is_unknown_with_a_suggestion() -> None:
    fr = HunspellPack("fr", DICTS / "fr")
    assert fr.known("bonjoru")[0] is Known.UNKNOWN
    assert "bonjour" in fr.suggest("bonjoru", 3)
    assert fr.known("bonjoru") == fr.known("bonjoru")  # the same answer twice
    assert fr.known("mondes")[0] is Known.KNOWN  # a suffix rule
    assert fr.known("aujourd'hui")[0] is Known.KNOWN


def test_wordchars_join_words() -> None:
    fr = HunspellPack("fr", DICTS / "fr")
    assert [t.text for t in fr.tokens("aujourd'hui c'est-à-dire")] == [
        "aujourd'hui",
        "c'est-à-dire",
    ]


def test_german_and_turkish_affixes() -> None:
    de = HunspellPack("de", DICTS / "de")
    assert de.known("Hause")[0] is Known.KNOWN and de.known("Häuserx")[0] is Known.UNKNOWN
    tr = HunspellPack("tr", DICTS / "tr")
    assert tr.known("kitaplar")[0] is Known.KNOWN and tr.known("evler")[0] is Known.KNOWN
    assert tr.known("kitapler")[0] is Known.UNKNOWN


def test_a_capitalised_unknown_mid_sentence_is_unsure() -> None:
    fr = HunspellPack("fr", DICTS / "fr")
    assert fr.known_at("Dupont", True)[0] is Known.UNSURE
    assert fr.known_at("Dupont", False)[0] is Known.UNKNOWN


def test_rep_and_one_edit_suggestions_are_in_a_fixed_order() -> None:
    d = load(DICTS / "en" / "en_US.aff", DICTS / "en" / "en_US.dic")
    assert d.suggest("teh", 3)[0] == "the"
    assert d.suggest("teh", 3) == d.suggest("teh", 3)
