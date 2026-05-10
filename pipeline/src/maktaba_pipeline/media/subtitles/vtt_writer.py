"""WebVTT writer — emits a W3C-compliant ``.vtt`` byte string.

Format reference: <https://www.w3.org/TR/webvtt1/>. We render the
subset the rest of the pipeline needs:

- ``WEBVTT`` magic line on row 1;
- optional ``NOTE`` preambles (one per ``header_notes`` entry);
- timestamps with a period decimal (``HH:MM:SS.mmm``);
- optional cue identifier line above each cue;
- ``<v Speaker>…</v>`` voice tag when a cue carries a speaker.

Body text is HTML-escaped. Speaker labels are escaped via
:func:`escape_speaker_label`. Cues are separated by a single blank
line; lines inside a cue body are separated by ``\\n``. Disk-side
line endings are the caller's problem (the stage handler decides
between ``\\n`` and ``\\r\\n`` depending on policy).
"""

from __future__ import annotations

from collections.abc import Iterable

from .cue import Cue
from .escape import escape_cue_text, escape_speaker_label

__all__ = ["format_vtt_timestamp", "write_vtt"]


def format_vtt_timestamp(seconds: float) -> str:
    """Render a non-negative float as ``HH:MM:SS.mmm``.

    Negative inputs are clamped to zero. We always emit the hours
    field even when zero so the timestamp shape is stable; players
    that follow the shorter ``MM:SS.mmm`` form still parse the
    long-form correctly.
    """
    if seconds < 0:
        seconds = 0.0
    total_ms = int(round(seconds * 1000))
    hours, rem_ms = divmod(total_ms, 3_600_000)
    minutes, rem_ms = divmod(rem_ms, 60_000)
    secs, ms = divmod(rem_ms, 1000)
    return f"{hours:02d}:{minutes:02d}:{secs:02d}.{ms:03d}"


def write_vtt(
    cues: Iterable[Cue],
    *,
    header_notes: tuple[str, ...] = (),
) -> bytes:
    """Render an iterable of :class:`Cue` to a UTF-8 byte string.

    ``header_notes`` becomes one ``NOTE`` block per entry directly
    after the ``WEBVTT`` magic line. Each note is escaped with the
    cue-text rules.
    """
    parts: list[str] = ["WEBVTT", ""]
    for note in header_notes:
        parts.append("NOTE " + escape_cue_text(note))
        parts.append("")

    blocks: list[str] = []
    for cue in cues:
        block_lines: list[str] = []
        if cue.cue_id is not None:
            block_lines.append(cue.cue_id)
        start = format_vtt_timestamp(cue.start_sec)
        end = format_vtt_timestamp(cue.end_sec)
        block_lines.append(f"{start} --> {end}")
        body = "\n".join(escape_cue_text(line) for line in cue.lines)
        if cue.speaker is not None:
            speaker = escape_speaker_label(cue.speaker)
            body = f"<v {speaker}>{body}</v>"
        block_lines.append(body)
        blocks.append("\n".join(block_lines))

    if blocks:
        parts.append("\n\n".join(blocks))
    # Final newline so the file ends cleanly.
    text = "\n".join(parts).rstrip("\n") + "\n"
    return text.encode("utf-8")
