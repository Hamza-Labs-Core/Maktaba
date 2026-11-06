"""Small dictionaries for the packs' tests (tests/fixtures/word_review/dicts), laid out as
an Epic 51 components folder: <root>/<id>/<version>_<sha12>/ with its current pointer."""

from __future__ import annotations

import shutil
from pathlib import Path

DICTS = Path(__file__).resolve().parent.parent / "fixtures" / "word_review" / "dicts"

#: component id -> fixture folder
FOLDERS = {
    "lang.ar": "ar",
    "lang.en": "en",
    "lang.hunspell.fr": "fr",
    "lang.hunspell.de": "de",
    "lang.hunspell.tr": "tr",
}


def install(root: Path, *ids: str, version: str = "0.3.82_aaaaaaaaaaaa") -> Path:
    """The ids' packs under ``root`` (plus runtime.lang, empty), as the packs runner leaves
    them."""
    for cid in ("runtime.lang", *ids):
        folder = root / cid / version
        folder.mkdir(parents=True, exist_ok=True)
        src = FOLDERS.get(cid)
        if src:
            for f in (DICTS / src).iterdir():
                shutil.copy2(f, folder / f.name)
        (root / cid / "current").write_text(version + "\n", encoding="utf-8")
    return root


def fake_analyser(known: set[str]) -> object:
    """An analyser that knows ``known`` (undiacritised), as (lemma, pos, diac, logprob)."""

    def analyse(word: str) -> list[tuple[str, str, str, float]]:
        return [(word, "noun", word, -1.0)] if word in known else []

    return analyse
