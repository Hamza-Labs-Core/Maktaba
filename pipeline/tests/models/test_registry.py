"""Catalog integrity for the model registry.

The registry is the source of truth for which models Maktaba knows how
to download. These tests guard the invariants the downloader and
service rely on: every entry resolves to a well-formed Hugging Face
URL, declares a positive size, carries a known type, and any checksum
present is a syntactically valid SHA256.
"""

from __future__ import annotations

import re
from urllib.parse import urlparse

import pytest

from maktaba_pipeline.models import registry

pytestmark = pytest.mark.unit

_SHA256_RE = re.compile(r"^[0-9a-f]{64}$")


def test_catalog_is_non_empty() -> None:
    models = registry.list_models()
    assert models, "catalog must not be empty"


def test_expected_models_present() -> None:
    ids = {m.id for m in registry.list_models()}
    # The four families the product ships (one entry each, plus the
    # second whisper backend for non-Apple hardware).
    assert "mlx-whisper-large-v3" in ids
    assert "faster-whisper-large-v3" in ids
    assert "all-minilm-l6-v2" in ids
    assert "multilingual-e5-large" in ids
    assert "pyannote-diarization-3.1" in ids


def test_every_model_has_known_type() -> None:
    for spec in registry.list_models():
        assert spec.type in registry.MODEL_TYPES, f"{spec.id}: bad type {spec.type!r}"


def test_every_model_has_files_and_positive_size() -> None:
    for spec in registry.list_models():
        assert spec.files, f"{spec.id}: no files declared"
        assert spec.size_bytes > 0, f"{spec.id}: non-positive size"
        for f in spec.files:
            assert f.size_bytes > 0, f"{spec.id}/{f.filename}: non-positive file size"


def test_all_file_urls_resolve_to_valid_https() -> None:
    for spec in registry.list_models():
        for f in spec.files:
            url = registry.resolve_url(spec, f.filename)
            parsed = urlparse(url)
            assert parsed.scheme == "https", f"{url}: not https"
            assert parsed.netloc == "huggingface.co", f"{url}: wrong host"
            assert spec.repo_id in url, f"{url}: missing repo id"
            assert f.filename in url, f"{url}: missing filename"


def test_checksums_when_present_are_valid_sha256() -> None:
    for spec in registry.list_models():
        for f in spec.files:
            if f.sha256 is not None:
                msg = f"{spec.id}/{f.filename}: bad sha256 {f.sha256!r}"
                assert _SHA256_RE.match(f.sha256), msg


def test_get_model_returns_spec() -> None:
    spec = registry.get_model("all-minilm-l6-v2")
    assert spec.id == "all-minilm-l6-v2"
    assert spec.type == "embedding"


def test_get_model_unknown_raises() -> None:
    with pytest.raises(registry.UnknownModel):
        registry.get_model("does-not-exist")


def test_gated_flag_set_for_pyannote() -> None:
    # Pyannote requires accepting the HF license / an auth token.
    assert registry.get_model("pyannote-diarization-3.1").gated is True
    # MiniLM is freely downloadable.
    assert registry.get_model("all-minilm-l6-v2").gated is False


def test_size_human_is_readable() -> None:
    spec = registry.get_model("mlx-whisper-large-v3")
    human = spec.size_human
    assert isinstance(human, str)
    assert any(unit in human for unit in ("KB", "MB", "GB")), human
