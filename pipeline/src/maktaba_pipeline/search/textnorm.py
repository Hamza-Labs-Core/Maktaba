"""Arabic normalisation for keyword search (Epic 49, story 49.4), in Python.

The same folding as Postgres's ``maktaba_normalize()`` (migration 0231) and Go's
``api/internal/textnorm.Normalize``: harakat (U+064B-U+0652, U+0670) and tatweel (U+0640)
go; أ إ آ ٱ become ا, ى ي, ة ه, ؤ و, ئ ي; Arabic-Indic and Persian digits become ASCII.
The ``keyword_index`` stage writes SQLite's FTS5 rows with it (Postgres indexes apply the
SQL function themselves). A test keeps the three equal.
"""

from __future__ import annotations

__all__ = ["normalize"]


def _table() -> dict[int, int | None]:
    t: dict[int, int | None] = {ord(a): ord("ا") for a in "أإآٱ"}
    t.update({ord("ى"): ord("ي"), ord("ة"): ord("ه"), ord("ؤ"): ord("و"), ord("ئ"): ord("ي")})
    t[0x0640] = None  # tatweel
    t[0x0670] = None  # superscript alef
    for c in range(0x064B, 0x0653):  # the harakat
        t[c] = None
    for i in range(10):
        t[0x0660 + i] = ord("0") + i  # Arabic-Indic digits
        t[0x06F0 + i] = ord("0") + i  # Persian digits
    return t


_FOLD = _table()


def normalize(s: str) -> str:
    """``s`` with the folding applied."""
    return s.translate(_FOLD)
