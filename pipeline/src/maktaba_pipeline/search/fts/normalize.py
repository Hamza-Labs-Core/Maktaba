"""Python mirror of the SQL ``maktaba_normalize()`` function.

Slot 0019 declares an IMMUTABLE Postgres function used to normalize
text before it goes into the FTS tsvector. SQLite has no equivalent
built-in, so we register this Python implementation as a custom
function (see :func:`search.fts.sqlite.register_arabic_normalize`)
and call it at the application layer for the query path.

The two implementations MUST stay byte-for-byte equivalent — a
mismatch would let indexed text not match its query form.
"""

from __future__ import annotations

import regex  # type: ignore[import-untyped]

__all__ = ["arabic_normalize"]


# 1) tashkeel + tatweel: U+064B..U+0652, U+0670 (superscript alef),
#    U+0640 (tatweel). The SQL character-class is `[ً-ْٰـ]` which
#    expands to exactly this range.
_COMBINING_MARKS_RE = regex.compile(r"[ً-ْٰـ]")

# 2) alef, ya, taa-marbuta unification — matches the `translate()`
#    call in slot 0019: 'إأآٱىة' → 'اااايه'.
_TRANSLATE_TABLE = str.maketrans(
    {
        "إ": "ا",
        "أ": "ا",
        "آ": "ا",
        "ٱ": "ا",
        "ى": "ي",
        "ة": "ه",
    }
)

# 3) whitespace collapse (matches `regexp_replace(..., '\s+', ' ')`).
_WS_RE = regex.compile(r"\s+")


def arabic_normalize(text: str) -> str:
    """Apply the same normalization as the SQL ``maktaba_normalize``.

    Steps, in order:

    1. lowercase
    2. strip Arabic combining marks (tashkeel + tatweel)
    3. unify alef variants → ا, ya variant → ي, taa marbuta → ه
    4. collapse whitespace to single ASCII space

    The function is pure and deterministic — safe to register as a
    SQLite user function with ``deterministic=True``.
    """
    s = text.lower()
    s = _COMBINING_MARKS_RE.sub("", s)
    s = s.translate(_TRANSLATE_TABLE)
    return str(_WS_RE.sub(" ", s))
