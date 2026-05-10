"""Story 9.1 — config schema validation + defaults inheritance."""

from __future__ import annotations

import pytest

from maktaba_pipeline.library_mgmt.config import (
    EFFECTIVE_DEFAULTS,
    SETTINGS_CHANGED_EVENT,
    deep_merge,
    effective_config,
    validate,
)


@pytest.mark.unit
def test_validate_recognises_full_settings_blob() -> None:
    settings = {
        "language": "ar",
        "multi_audio": True,
        "stt": {
            "backend": "whisper-mlx",
            "model": "large-v3",
            "profile": "default",
            "max_usd_per_month": 50,
        },
        "embedding": {"model": "intfloat/multilingual-e5-large", "device": "auto"},
        "diarize": True,
        "chapter_inference": True,
        "auto_tag_topics": True,
        "default_subtitle_lang": "en",
        "ignore_globs": ["**/raw/**"],
        "sweep_interval_sec": 3600,
        "speaker_match_threshold": 0.4,
        "topic_clusters": 16,
    }
    result = validate(settings)
    assert result.ok
    assert result.warnings == []


@pytest.mark.unit
def test_validate_rejects_unknown_stt_backend() -> None:
    result = validate({"stt": {"backend": "invalid"}})
    assert not result.ok
    paths = [e.path for e in result.errors]
    assert "stt/backend" in paths


@pytest.mark.unit
def test_validate_rejects_negative_sweep_interval() -> None:
    result = validate({"sweep_interval_sec": -10})
    assert not result.ok
    assert any(e.path == "sweep_interval_sec" for e in result.errors)


@pytest.mark.unit
def test_validate_rejects_bool_for_int_sweep_interval() -> None:
    # Story 9.1 implicit: don't accept ``True`` as 1 because bool ⊂ int.
    result = validate({"sweep_interval_sec": True})
    assert not result.ok


@pytest.mark.unit
def test_validate_rejects_three_letter_language() -> None:
    result = validate({"language": "eng"})
    assert not result.ok
    assert any(e.path == "language" for e in result.errors)


@pytest.mark.unit
def test_validate_accepts_auto_language() -> None:
    result = validate({"language": "auto"})
    assert result.ok


@pytest.mark.unit
def test_validate_warns_on_unknown_top_level_key() -> None:
    result = validate({"future_feature": True})
    assert result.ok  # warnings don't make !ok
    assert len(result.warnings) == 1
    assert result.warnings[0].path == "future_feature"


@pytest.mark.unit
def test_validate_warns_on_unknown_nested_key() -> None:
    result = validate({"stt": {"backend": "whisper-mlx", "future_knob": 7}})
    assert result.ok
    paths = [w.path for w in result.warnings]
    assert "stt/future_knob" in paths


@pytest.mark.unit
def test_validate_rejects_ignore_globs_non_string() -> None:
    result = validate({"ignore_globs": [123, "**/x"]})
    assert not result.ok


@pytest.mark.unit
def test_validate_rejects_speaker_match_threshold_out_of_range() -> None:
    result = validate({"speaker_match_threshold": 1.5})
    assert not result.ok
    result2 = validate({"speaker_match_threshold": -0.1})
    assert not result2.ok


@pytest.mark.unit
def test_deep_merge_preserves_nested_keys() -> None:
    a = {"stt": {"backend": "whisper-mlx", "model": "large-v3"}}
    b = {"stt": {"model": "small"}}
    out = deep_merge(a, b)
    assert out == {"stt": {"backend": "whisper-mlx", "model": "small"}}


@pytest.mark.unit
def test_deep_merge_replaces_lists_wholesale() -> None:
    a = {"ignore_globs": ["**/raw/**"]}
    b = {"ignore_globs": ["**/x"]}
    assert deep_merge(a, b) == {"ignore_globs": ["**/x"]}


@pytest.mark.unit
def test_deep_merge_does_not_mutate_inputs() -> None:
    a = {"stt": {"backend": "whisper-mlx"}}
    b = {"stt": {"model": "small"}}
    _ = deep_merge(a, b)
    assert a == {"stt": {"backend": "whisper-mlx"}}
    assert b == {"stt": {"model": "small"}}


@pytest.mark.unit
def test_effective_config_layers_defaults_then_pipeline_then_library() -> None:
    library = {"stt": {"model": "small"}}
    pipeline = {"stt": {"profile": "fast"}}
    eff = effective_config(library, pipeline)
    # Library wins for ``model``.
    assert eff["stt"]["model"] == "small"
    # Pipeline wins for ``profile`` (default doesn't set it).
    assert eff["stt"]["profile"] == "fast"
    # Default wins for ``backend``.
    assert eff["stt"]["backend"] == EFFECTIVE_DEFAULTS["stt"]["backend"]


@pytest.mark.unit
def test_effective_config_with_no_overrides_returns_defaults() -> None:
    eff = effective_config({})
    assert eff["language"] == "auto"
    assert eff["chapter_inference"] is True
    assert eff["sweep_interval_sec"] == 6 * 60 * 60


@pytest.mark.unit
def test_settings_changed_event_constant() -> None:
    # Sanity check the canonical NOTIFY name doesn't drift.
    assert SETTINGS_CHANGED_EVENT == "library.settings_changed"
