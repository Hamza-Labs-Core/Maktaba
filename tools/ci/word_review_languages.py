#!/usr/bin/env python3
"""No language named outside a language pack (Epic 52, story 52.3).

Word review must work the same in every language: only ``language_packs/`` may name one.
This fails when the word review code (``word_review/`` and the ``word_*`` stages) holds a
language code literal (``"ar"``, ``'en'``, ...), naming the file and line.

    python3 tools/ci/word_review_languages.py [ROOT]
"""

from __future__ import annotations

import re
import sys
from pathlib import Path

#: the codes a slip would most likely name (Whisper's languages the library hears)
#: ("id" and "ms" are left out: an "id" key is everywhere)
CODES = ("ar", "en", "fr", "de", "tr", "ur", "fa", "es")
LITERAL = re.compile(r"""(?<![\w.])(["'])(%s)\1""" % "|".join(CODES))


def offenders(root: Path) -> list[str]:
    src = root / "pipeline" / "src" / "maktaba_pipeline"
    files = sorted((src / "word_review").rglob("*.py")) + sorted(
        (src / "engine" / "stages").glob("word_*.py")
    )
    out = []
    for f in files:
        for n, line in enumerate(f.read_text(encoding="utf-8").splitlines(), 1):
            code = line.split("#", 1)[0]
            if LITERAL.search(code):
                out.append(f"{f.relative_to(root)}:{n}: {line.strip()}")
    return out


def main(argv: list[str]) -> int:
    root = Path(argv[1]) if len(argv) > 1 else Path(__file__).resolve().parents[2]
    bad = offenders(root)
    for b in bad:
        print(f"a language named outside language_packs/: {b}", file=sys.stderr)
    if not bad:
        print("word_review_languages: no language literal outside the packs")
    return 1 if bad else 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
