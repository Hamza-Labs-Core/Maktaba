"""tools/release/packs/minitoml.py reads the word review lists as tomllib does."""

from __future__ import annotations

import sys
import tomllib
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(ROOT / "tools" / "release" / "packs"))

import minitoml  # noqa: E402


def test_the_lists_read_the_same() -> None:
    for p in ("tools/release/packs/hunspell-languages.toml", "tools/release/packs/lang-sources.toml"):
        text = (ROOT / p).read_text(encoding="utf-8")
        assert minitoml.loads(text) == tomllib.loads(text), p


def test_shapes() -> None:
    text = 'a = 1  # c\n[t."x.y"]\nb = "q#r"\nfiles = [\n  { p = "a", n = 2 },\n]\n[[l]]\nc = true\n'
    assert minitoml.loads(text) == tomllib.loads(text)
