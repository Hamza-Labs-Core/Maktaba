"""tools/ci/word_review_languages.py (Epic 52, story 52.3)."""

from __future__ import annotations

import importlib.util
from pathlib import Path

_P = Path(__file__).resolve().parents[1] / "word_review_languages.py"
_spec = importlib.util.spec_from_file_location("word_review_languages", _P)
assert _spec is not None and _spec.loader is not None
wrl = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(wrl)


def _tree(tmp: Path, body: str) -> Path:
    src = tmp / "pipeline" / "src" / "maktaba_pipeline"
    (src / "word_review").mkdir(parents=True)
    (src / "engine" / "stages").mkdir(parents=True)
    (src / "word_review" / "x.py").write_text(body, encoding="utf-8")
    (src / "engine" / "stages" / "word_check.py").write_text("x = 1\n", encoding="utf-8")
    return tmp


def test_a_language_literal_fails(tmp_path: Path) -> None:
    root = _tree(tmp_path, 'lang = "ar"\n')
    assert wrl.offenders(root) == ['pipeline/src/maktaba_pipeline/word_review/x.py:1: lang = "ar"']
    assert wrl.main(["x", str(root)]) == 1


def test_comments_and_longer_words_pass(tmp_path: Path) -> None:
    root = _tree(tmp_path, 'x = "area"  # "ar" in a comment\ny = r["id"]\n')
    assert wrl.offenders(root) == []


def test_the_real_tree_is_clean() -> None:
    assert wrl.offenders(Path(__file__).resolve().parents[3]) == []
