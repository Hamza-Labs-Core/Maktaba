"""Generic text normalization for the chunker.

The functions here are deliberately small — Arabic-specific
normalization lives in :mod:`search.fts.normalize` so the chunker
stays language-agnostic. NFC composition + whitespace collapse is
enough for the segmenter to align character offsets back to source
segments.
"""

from __future__ import annotations

import unicodedata

import regex  # type: ignore[import-untyped]

__all__ = ["collapse_whitespace", "nfc"]


# ``\s`` would match a wider class than we want — the regex package's
# ``\p{Z}`` matches any unicode separator and is the canonical choice
# for whitespace collapse.
_WHITESPACE_RUN = regex.compile(r"[\s\p{Z}]+")


def nfc(text: str) -> str:
    """Return the NFC-normalized form of ``text``.

    Combining marks compose with their base character so downstream
    grapheme counts line up with what a reader perceives. Idempotent.
    """
    return unicodedata.normalize("NFC", text)


def collapse_whitespace(text: str) -> str:
    """Replace runs of whitespace with a single ASCII space, then strip.

    The replacement uses :mod:`regex` so unicode separators (e.g.
    ``\\u00a0`` NBSP) collapse along with ASCII whitespace.
    """
    return str(_WHITESPACE_RUN.sub(" ", text)).strip()
