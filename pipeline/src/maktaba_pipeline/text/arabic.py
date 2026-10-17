"""Arabic-aware text normalisation for matching titles.

Video titles from YouTube channels mix harakat, tatweel, hamza variants,
Arabic-Indic digits, hashtags (``#سلسلة_الحبيب``) and the fullwidth
punctuation yt-dlp substitutes into filenames (``｜``, ``：``, ``⧸``). Two
spellings of the same series title must compare equal, so matching code
works on a *folded* form.

Two strengths are provided:

- :func:`light` removes only marks that never change meaning (harakat,
  tatweel, bidi controls) and unifies digits/punctuation. Safe for prefix
  matching: ``فائ`` must not match ``فأين``.
- :func:`fold` additionally merges letter variants (أ/إ/آ/ٱ → ا, ة → ه,
  ى → ي, ؤ → و, ئ → ي). Use for whole-token equality.

Pure functions, no I/O.
"""

from __future__ import annotations

import re
import unicodedata

__all__ = [
    "ARABIC_VERSION",
    "fold",
    "light",
    "tokens",
    "ytdlp_filename",
]

#: Bumped when folding rules change (cached folded forms must be rebuilt).
ARABIC_VERSION: int = 1

_MARKS_RE = re.compile("[ً-ٰٟۖ-ۭـ‌-‏‪-‮⁦-⁩]")
_DIGITS = str.maketrans("٠١٢٣٤٥٦٧٨٩۰۱۲۳۴۵۶۷۸۹", "01234567890123456789")
_LETTERS = str.maketrans(
    {"أ": "ا", "إ": "ا", "آ": "ا", "ٱ": "ا", "ة": "ه", "ى": "ي", "ؤ": "و", "ئ": "ي"}
)
_NON_WORD_RE = re.compile(r"[^\w]+|_+")


def light(text: str) -> str:
    """NFKC, strip harakat/tatweel/bidi marks, Latin digits, punctuation → space, lowercase."""
    s = unicodedata.normalize("NFKC", text or "")
    s = _MARKS_RE.sub("", s).translate(_DIGITS)
    return " ".join(_NON_WORD_RE.sub(" ", s).split()).lower()


def fold(text: str) -> str:
    """:func:`light` plus Arabic letter-variant folding (hamza forms, ة/ه, ى/ي)."""
    return light(text).translate(_LETTERS)


def tokens(text: str) -> list[str]:
    """Folded word tokens."""
    return fold(text).split()


# yt-dlp (non-restricted mode) replaces characters that are illegal in
# filenames with look-alikes; Plex then shows the *filename* as the title.
_YTDLP_SUBST = {"/": "⧸", "\\": "⧹"} | {c: chr(ord(c) + 0xFEE0) for c in '"*:<>?|'}


def ytdlp_filename(title: str) -> str:
    """Reproduce yt-dlp's default ``%(title)s`` filename sanitisation.

    Lets a YouTube title be matched against a file stem exactly. Equivalent
    to ``yt_dlp.utils.sanitize_filename(title, restricted=False)`` (checked
    against yt-dlp on a 2,600-title corpus): illegal characters become
    fullwidth look-alikes; whitespace and leading dashes are kept.
    """
    return "".join(_YTDLP_SUBST.get(ch, ch) for ch in title)
