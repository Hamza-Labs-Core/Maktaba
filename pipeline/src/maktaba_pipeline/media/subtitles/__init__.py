"""Public API for subtitle authoring (Epic 4, Stories 4.1–4.3).

The package is dialect-free Python: each submodule does one thing
(cue model, escape, writers, paths, atomic IO, alias copy, shaper,
filename parsing, external discovery). The stage handler at
``pipeline.stages.subtitle_gen`` wires these together; tests can
exercise each module in isolation.
"""

from __future__ import annotations

from .alias import alias_copy
from .atomic import write_atomic_pair
from .cue import Cue, Segment
from .discovery import SubtitleFileRow, discover_subtitles_for_video_sync
from .escape import escape_cue_text, escape_speaker_label
from .filename import (
    ParsedSubtitleFilename,
    compile_subtitle_regex,
    normalize_lang,
    parse_filename,
)
from .paths import alias_path_for, canonical_subtitle_path, ensure_sidecar_dirs
from .shaper import CueShaper, PassThroughShaper, WrappingShaper, default_shaper
from .srt_writer import format_srt_timestamp, write_srt
from .vtt_writer import format_vtt_timestamp, write_vtt

__all__ = [
    "Cue",
    "CueShaper",
    "ParsedSubtitleFilename",
    "PassThroughShaper",
    "Segment",
    "SubtitleFileRow",
    "WrappingShaper",
    "alias_copy",
    "alias_path_for",
    "canonical_subtitle_path",
    "compile_subtitle_regex",
    "default_shaper",
    "discover_subtitles_for_video_sync",
    "ensure_sidecar_dirs",
    "escape_cue_text",
    "escape_speaker_label",
    "format_srt_timestamp",
    "format_vtt_timestamp",
    "normalize_lang",
    "parse_filename",
    "write_atomic_pair",
    "write_srt",
    "write_vtt",
]
