"""SRT / VTT formatting primitives.

The two formats differ only in the timestamp separator and the prelude:

  SRT uses ``HH:MM:SS,mmm`` (comma decimal) with no header.
  VTT uses ``HH:MM:SS.mmm`` (dot decimal) and requires ``WEBVTT`` as the
  first line.

Both wrap cue text with the same set of escapes: in SRT, the ``<`` and
``>`` characters are passed through verbatim (consumers tolerate
HTML-ish bold/italic tags); in VTT, ``<``, ``>``, ``&`` get HTML-style
escapes so the WebVTT parser doesn't interpret them as tag openers.
Both formats forbid the literal ``-->`` sequence inside cue text —
that's the cue-timing delimiter; we escape the second ``>`` to break
the pattern.

Negative timestamps are clamped to ``00:00:00`` so a malformed segment
can't corrupt the file. Timestamps over ``99:59:59,999`` are clamped to
the cap; this is well past any plausible video duration.
"""

from __future__ import annotations

from enum import StrEnum

__all__ = [
    "MAX_TIMESTAMP_SEC",
    "SubtitleFormat",
    "escape_srt_text",
    "escape_vtt_text",
    "format_srt_timestamp",
    "format_vtt_timestamp",
]


class SubtitleFormat(StrEnum):
    """The two output formats Maktaba writes."""

    SRT = "srt"
    VTT = "vtt"


# 99h 59m 59.999s — the upper limit of the HH:MM:SS,mmm shape.
# Anything past this gets clamped to keep the on-disk file syntactically
# valid; a single video that long is implausible.
MAX_TIMESTAMP_SEC: float = 99 * 3600 + 59 * 60 + 59 + 0.999


def format_srt_timestamp(seconds: float) -> str:
    """Format ``seconds`` as ``HH:MM:SS,mmm`` (SRT)."""
    return _format_clock(seconds, decimal=",")


def format_vtt_timestamp(seconds: float) -> str:
    """Format ``seconds`` as ``HH:MM:SS.mmm`` (VTT)."""
    return _format_clock(seconds, decimal=".")


def _format_clock(seconds: float, *, decimal: str) -> str:
    if seconds < 0 or seconds != seconds:  # NaN check
        seconds = 0.0
    if seconds > MAX_TIMESTAMP_SEC:
        seconds = MAX_TIMESTAMP_SEC
    # Round-half-up to milliseconds. Using `int` after multiply truncates;
    # we add 0.5 first so 0.0005 → 1 ms rather than 0.
    total_ms = int(seconds * 1000 + 0.5)
    ms = total_ms % 1000
    total_s = total_ms // 1000
    s = total_s % 60
    total_m = total_s // 60
    m = total_m % 60
    h = total_m // 60
    return f"{h:02d}:{m:02d}:{s:02d}{decimal}{ms:03d}"


def escape_srt_text(text: str) -> str:
    """Escape cue text for SRT.

    SRT is permissive — consumers display unknown tags as text — but the
    literal ``-->`` sequence collides with the cue-timing delimiter.
    Break the pattern by zero-width-substituting the trailing ``>``.
    """
    # Replace the bare delimiter sequence; the unicode replacement keeps
    # the visible glyph the same while making the file parseable.
    return text.replace("-->", "-‐>")


def escape_vtt_text(text: str) -> str:
    """Escape cue text for WebVTT.

    Per the WebVTT spec, ``&``, ``<``, ``>`` must be replaced with their
    HTML entity forms inside cue text. ``-->`` is also illegal inside
    text; the entity escape of ``>`` handles it for us.
    """
    # Order matters: replace ``&`` first so it doesn't double-escape the
    # ampersands we introduce for ``<`` and ``>``.
    return (
        text.replace("&", "&amp;")
        .replace("<", "&lt;")
        .replace(">", "&gt;")
    )
