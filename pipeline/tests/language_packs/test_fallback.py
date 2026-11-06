"""The fallback pack (Epic 52, story 52.3): any language, never UNKNOWN."""

from __future__ import annotations

import pytest

from maktaba_pipeline.language_packs import fallback
from maktaba_pipeline.language_packs.base import Known
from maktaba_pipeline.language_packs.fallback import FallbackPack


def test_japanese_without_spaces_is_more_than_one_token() -> None:
    toks = FallbackPack("ja").tokens("これはペンです")
    assert len(toks) > 1


def test_known_is_never_unknown(monkeypatch: pytest.MonkeyPatch) -> None:
    pack = FallbackPack("sw")
    for w in ("habari", "xqzzv", "", "123"):
        assert pack.known(w)[0] in (Known.KNOWN, Known.UNSURE)
    monkeypatch.setattr(fallback, "HAS_WORDFREQ", True)
    monkeypatch.setattr(fallback, "_zipf", lambda w, lang: 3.0 if w == "habari" else 1.0)
    assert pack.known("habari")[0] is Known.KNOWN
    assert pack.known("xqzzv")[0] is Known.UNSURE


def test_known_never_raises(monkeypatch: pytest.MonkeyPatch) -> None:
    def boom(w: str, lang: str) -> float:
        raise RuntimeError("no data")

    monkeypatch.setattr(fallback, "_zipf", boom)
    assert FallbackPack("sw").known("x")[0] is Known.UNSURE


def test_its_version_names_its_tokenizer() -> None:
    p = FallbackPack("th")
    assert p.tokenizer in ("icu", "regex") and p.tokenizer in p.version
    assert p.suggest("anything", 3) == [] and p.scripture([]) == []
