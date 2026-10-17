"""content_hash finds a file whose name the share stores in another Unicode form."""

from __future__ import annotations

import os
import unicodedata
from pathlib import Path

import pytest

from maktaba_pipeline.engine.stages.content_hash import on_disk

pytestmark = pytest.mark.unit


def test_a_decomposed_name_on_disk_is_found_from_the_composed_one(tmp_path: Path) -> None:
    composed = "ألش خانة ｜ ١٤.mp4"
    decomposed = unicodedata.normalize("NFD", composed)
    assert decomposed != composed  # أ is ا + U+0654 decomposed
    real = tmp_path / decomposed
    real.write_bytes(b"x")
    found = on_disk(str(tmp_path / composed))
    assert found is not None and os.path.samefile(found, real)
    assert on_disk(str(tmp_path / "nope.mp4")) is None
    assert on_disk(str(tmp_path / "missing-dir" / "a.mp4")) is None
