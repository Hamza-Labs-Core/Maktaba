"""SRT writer (SubRip) — emits 1-indexed cues with ``HH:MM:SS,mmm`` timestamps.

SRT has no formal spec; the de-facto rules used by ffmpeg, VLC, and
mpv are:

- 1-based monotonically increasing cue number;
- timestamps with a comma decimal separator (``HH:MM:SS,mmm``);
- CRLF line endings;
- a blank line between cues.

We HTML-escape cue text (``&``, ``<``, ``>``) so a transcript that
contains a literal ``<`` doesn't poison players that try to render
basic styling tags. Speaker labels are intentionally dropped — SRT
has no portable voice mechanism.
"""

from __future__ import annotations

from collections.abc import Iterable

from .cue import Cue
from .escape import escape_cue_text

__all__ = ["format_srt_timestamp", "write_srt"]

# SRT spec: CRLF between every line, blank line between cues.
_CRLF = "\r\n"


def format_srt_timestamp(seconds: float) -> str:
    """Render a non-negative float as ``HH:MM:SS,mmm``.

    Clamps negatives to zero (a guard against ASR timing drift). The
    millisecond field is rounded, not truncated, to keep the rendered
    duration as close to the source as possible.
    """
    if seconds < 0:
        seconds = 0.0
    total_ms = int(round(seconds * 1000))
    hours, rem_ms = divmod(total_ms, 3_600_000)
    minutes, rem_ms = divmod(rem_ms, 60_000)
    secs, ms = divmod(rem_ms, 1000)
    return f"{hours:02d}:{minutes:02d}:{secs:02d},{ms:03d}"


def write_srt(cues: Iterable[Cue]) -> bytes:
    """Render an iterable of :class:`Cue` to a UTF-8 byte string."""
    out: list[str] = []
    for index, cue in enumerate(cues, start=1):
        start = format_srt_timestamp(cue.start_sec)
        end = format_srt_timestamp(cue.end_sec)
        body_lines = [escape_cue_text(line) for line in cue.lines]
        block_lines = [str(index), f"{start} --> {end}", *body_lines]
        # Each block ends with a blank line; SRT spec uses CRLF
        # between every line including the trailing separator.
        out.append(_CRLF.join(block_lines) + _CRLF + _CRLF)
    return "".join(out).encode("utf-8")
