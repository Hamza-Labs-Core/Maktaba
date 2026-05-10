"""Render ``transcript_segments`` rows into SRT / VTT cues.

:func:`segments_to_cues` adapts the raw segment shape (a mapping of
``start_sec``, ``end_sec``, ``text``, optional ``speaker``) into the
:class:`SubtitleCue` shape both renderers consume. The renderers
themselves (:func:`generate_srt`, :func:`generate_vtt`) accept either
the raw segments or pre-built cues — the conversion is idempotent.

Cue indices in SRT are 1-based and contiguous. VTT does not require an
identifier; we omit it.

Overlapping cues are emitted in the order received. The renderer does
not merge adjacent cues with identical text — the caller may pre-merge
if desired (live captioning prefers separate cues for sync; static
SRTs may merge).
"""

from __future__ import annotations

from collections.abc import Iterable, Mapping
from dataclasses import dataclass
from typing import Any

from .formats import (
    escape_srt_text,
    escape_vtt_text,
    format_srt_timestamp,
    format_vtt_timestamp,
)

__all__ = [
    "SubtitleCue",
    "generate_srt",
    "generate_vtt",
    "segments_to_cues",
]


@dataclass(slots=True, frozen=True)
class SubtitleCue:
    """One subtitle cue.

    ``speaker`` is rendered as a leading bold prefix in VTT and as a
    plain ``Speaker: text`` line in SRT (SRT has no formatting tags).
    ``None`` skips the prefix.
    """

    start_sec: float
    end_sec: float
    text: str
    speaker: str | None = None


def segments_to_cues(segments: Iterable[Mapping[str, Any]]) -> list[SubtitleCue]:
    """Project ``transcript_segments`` rows into :class:`SubtitleCue` items.

    Accepts any mapping with ``start_sec``, ``end_sec``, ``text`` keys
    (``speaker`` is optional). Rows whose text is empty after stripping
    are dropped — there's nothing to display.
    """
    cues: list[SubtitleCue] = []
    for row in segments:
        text = (row.get("text") or "").strip()
        if not text:
            continue
        cues.append(
            SubtitleCue(
                start_sec=float(row["start_sec"]),
                end_sec=float(row["end_sec"]),
                text=text,
                speaker=row.get("speaker"),
            )
        )
    return cues


def _coerce_cues(items: Iterable[SubtitleCue | Mapping[str, Any]]) -> list[SubtitleCue]:
    out: list[SubtitleCue] = []
    for item in items:
        if isinstance(item, SubtitleCue):
            out.append(item)
        else:
            converted = segments_to_cues([item])
            out.extend(converted)
    return out


def _ensure_endpoints_ordered(cue: SubtitleCue) -> tuple[float, float]:
    start = max(0.0, cue.start_sec)
    end = max(start + 0.001, cue.end_sec)  # WebVTT/SRT require end > start
    return start, end


def generate_srt(cues: Iterable[SubtitleCue | Mapping[str, Any]]) -> str:
    """Render cues as a complete SRT document.

    Output ends with a trailing newline (SRT consumers tolerate either,
    but ffmpeg / VLC are picky about the final blank line between cues).
    """
    parts: list[str] = []
    for index, cue in enumerate(_coerce_cues(cues), start=1):
        start, end = _ensure_endpoints_ordered(cue)
        ts = f"{format_srt_timestamp(start)} --> {format_srt_timestamp(end)}"
        body = escape_srt_text(cue.text)
        if cue.speaker:
            body = f"{escape_srt_text(cue.speaker)}: {body}"
        parts.append(f"{index}\n{ts}\n{body}\n")
    return "\n".join(parts) + ("\n" if parts else "")


def generate_vtt(cues: Iterable[SubtitleCue | Mapping[str, Any]]) -> str:
    """Render cues as a complete WebVTT document.

    The ``WEBVTT`` header is mandatory; we emit it even for an empty
    cue list so the file is parseable.
    """
    parts: list[str] = ["WEBVTT", ""]
    for cue in _coerce_cues(cues):
        start, end = _ensure_endpoints_ordered(cue)
        ts = f"{format_vtt_timestamp(start)} --> {format_vtt_timestamp(end)}"
        body = escape_vtt_text(cue.text)
        if cue.speaker:
            body = f"<v {escape_vtt_text(cue.speaker)}>{body}"
        parts.append(ts)
        parts.append(body)
        parts.append("")
    return "\n".join(parts)
