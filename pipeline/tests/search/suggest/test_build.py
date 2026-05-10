"""NgramExtractor: known input → expected n-grams above thresholds."""

from __future__ import annotations

import pytest

from maktaba_pipeline.search.suggest.build import NgramExtractor


@pytest.mark.unit
def test_extracts_2gram_above_threshold() -> None:
    # "fox jumps" appears in 3 documents, "lazy dog" in 3.
    docs = [
        "the quick brown fox jumps over the lazy dog",
        "another quick brown fox jumps high",
        "fox jumps yet again over the lazy dog of mine",
        "lazy dog dreams alone",
    ]
    extractor = NgramExtractor(min_n=2, max_n=2, min_frequency=2, min_doc_frequency=2)

    out = extractor.extract(docs)
    bigram_terms = {term: (n, freq, df) for term, n, freq, df in out}

    assert "fox jumps" in bigram_terms
    assert "lazy dog" in bigram_terms
    n, freq, df = bigram_terms["fox jumps"]
    assert n == 2
    assert freq >= 3
    assert df >= 2


@pytest.mark.unit
def test_filters_below_min_frequency() -> None:
    docs = ["alpha beta gamma", "alpha beta delta"]
    extractor = NgramExtractor(min_n=2, max_n=2, min_frequency=5, min_doc_frequency=1)
    assert extractor.extract(docs) == []


@pytest.mark.unit
def test_filters_below_min_doc_frequency() -> None:
    # "alpha beta" appears five times but only in one doc.
    docs = [
        "alpha beta alpha beta alpha beta alpha beta alpha beta",
        "completely different content here",
    ]
    extractor = NgramExtractor(min_n=2, max_n=2, min_frequency=2, min_doc_frequency=2)
    assert extractor.extract(docs) == []


@pytest.mark.unit
def test_extracts_3_and_4_grams() -> None:
    docs = [
        "the quick brown fox jumps",
        "the quick brown fox runs",
        "the quick brown fox sleeps",
    ]
    extractor = NgramExtractor(min_n=3, max_n=4, min_frequency=2, min_doc_frequency=2)
    out = extractor.extract(docs)
    terms = {(term, n) for term, n, _, _ in out}
    assert ("the quick brown", 3) in terms
    assert ("the quick brown fox", 4) in terms


@pytest.mark.unit
def test_handles_arabic_tokens() -> None:
    docs = [
        "الحمد لله رب العالمين الرحمن الرحيم",
        "الحمد لله رب العالمين كل صباح",
        "الحمد لله رب الناس",
    ]
    extractor = NgramExtractor(min_n=2, max_n=3, min_frequency=2, min_doc_frequency=2)
    out = extractor.extract(docs)
    terms = {term for term, _, _, _ in out}
    assert "الحمد لله" in terms


@pytest.mark.unit
def test_rejects_invalid_bounds() -> None:
    with pytest.raises(ValueError):
        NgramExtractor(min_n=0)
    with pytest.raises(ValueError):
        NgramExtractor(min_n=3, max_n=2)
