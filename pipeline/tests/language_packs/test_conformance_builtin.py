"""The conformance suite over every built-in pack (Epic 52, story 52.3)."""

from __future__ import annotations

from pathlib import Path

import pytest

from maktaba_pipeline.language_packs import conformance
from maktaba_pipeline.language_packs.ar import ArabicPack
from maktaba_pipeline.language_packs.base import LanguagePack
from maktaba_pipeline.language_packs.en import EnglishPack
from maktaba_pipeline.language_packs.fallback import FallbackPack
from maktaba_pipeline.language_packs.hunspell import HunspellPack

from .fixtures import DICTS, fake_analyser

PACKS = Path(conformance.__file__).resolve().parent


def _samples(code: str) -> list[str]:
    p = PACKS / code / "samples.txt"
    return p.read_text(encoding="utf-8").splitlines() if p.is_file() else []


@pytest.mark.parametrize(
    ("name", "make", "samples"),
    [
        ("ar", lambda: ArabicPack(DICTS / "ar", analyse=fake_analyser({"قال"})), "ar"),  # type: ignore[arg-type]
        ("en", lambda: EnglishPack(DICTS / "en"), "en"),
        ("hunspell-fr", lambda: HunspellPack("fr", DICTS / "fr"), ""),
        ("hunspell-de", lambda: HunspellPack("de", DICTS / "de"), ""),
        ("hunspell-tr", lambda: HunspellPack("tr", DICTS / "tr"), ""),
        ("fallback", lambda: FallbackPack("ja"), ""),
    ],
)
def test_every_built_in_pack_conforms(name: str, make: object, samples: str) -> None:
    pack: LanguagePack = make()  # type: ignore[operator]
    assert isinstance(pack, LanguagePack)
    fails = conformance.check(pack, _samples(samples) if samples else [], n_random=1000)
    assert fails == [], fails[:5]


def test_the_random_strings_are_the_same_every_run() -> None:
    assert conformance.random_strings(20) == conformance.random_strings(20)
