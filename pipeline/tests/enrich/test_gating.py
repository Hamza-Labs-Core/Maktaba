"""Story 26.7 D4 — enrich-enqueue gating."""

from __future__ import annotations

from maktaba_pipeline.enrich import EnrichSettings, ProviderKey, should_enqueue_enrich


def test_disabled_never_enqueues() -> None:
    assert not should_enqueue_enrich(
        EnrichSettings(enabled=False),
        [ProviderKey("tmdb", configured=True)],
    )


def test_enabled_but_no_key_skips() -> None:
    # test_enrich_skipped_without_key: enabled but no provider configured.
    assert not should_enqueue_enrich(
        EnrichSettings(enabled=True),
        [ProviderKey("tmdb", configured=False), ProviderKey("omdb", configured=False)],
    )


def test_enabled_with_one_key_enqueues() -> None:
    assert should_enqueue_enrich(
        EnrichSettings(enabled=True),
        [ProviderKey("tmdb", configured=False), ProviderKey("omdb", configured=True)],
    )
