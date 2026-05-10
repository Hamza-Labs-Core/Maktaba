"""HTML-style escape used by both SRT and VTT writers.

Per WebVTT (W3C TTWG) and SRT-compatible player conventions, the only
three characters that need escaping inside cue text are ``&``, ``<``,
and ``>``. The order matters: ``&`` must be escaped first so we don't
re-escape the ampersands we ourselves introduce.
"""

from __future__ import annotations

__all__ = ["escape_cue_text", "escape_speaker_label"]


def escape_cue_text(s: str) -> str:
    """Escape ``&``, ``<``, ``>`` for cue body text.

    Replaces ``&`` first to avoid double-escaping the ampersands we
    insert for ``<`` / ``>``. Single-pass on the input — leaves all
    other characters untouched.
    """
    return s.replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")


def escape_speaker_label(s: str) -> str:
    """Escape a speaker label going into a VTT ``<v …>`` tag.

    Same rules as cue text — the W3C VTT parser treats the voice tag
    attribute as text content for the purpose of entity references.
    """
    return escape_cue_text(s)
