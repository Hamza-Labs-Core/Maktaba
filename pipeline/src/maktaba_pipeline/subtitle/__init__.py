"""Epic 4 — subtitle generation, extraction, and storage.

Module map:

- :mod:`.formats`    SRT / VTT formatting primitives (timestamps + escaping).
- :mod:`.generator`  Render ``transcript_segments`` rows into SRT or VTT.
- :mod:`.extractor`  Pull embedded subtitle tracks out of containers via ffprobe + ffmpeg.
- :mod:`.manager`    Atomic write / delete / registry-row helpers for ``subtitle_files``.

Everything is pure Python except :mod:`.extractor`, which shells out to
ffprobe/ffmpeg the same way :mod:`maktaba_pipeline.audio.probe` does.
Tests inject curated ffprobe JSON directly.
"""

from __future__ import annotations

from .extractor import EmbeddedSubtitle, ExtractSubtitleError, extract_embedded, list_embedded
from .formats import (
    SubtitleFormat,
    escape_srt_text,
    escape_vtt_text,
    format_srt_timestamp,
    format_vtt_timestamp,
)
from .generator import SubtitleCue, generate_srt, generate_vtt, segments_to_cues
from .manager import (
    SubtitleRecord,
    SubtitleSource,
    register_subtitle,
    soft_delete_subtitle,
    write_atomic,
)

__all__ = [
    "EmbeddedSubtitle",
    "ExtractSubtitleError",
    "SubtitleCue",
    "SubtitleFormat",
    "SubtitleRecord",
    "SubtitleSource",
    "escape_srt_text",
    "escape_vtt_text",
    "extract_embedded",
    "format_srt_timestamp",
    "format_vtt_timestamp",
    "generate_srt",
    "generate_vtt",
    "list_embedded",
    "register_subtitle",
    "segments_to_cues",
    "soft_delete_subtitle",
    "write_atomic",
]
