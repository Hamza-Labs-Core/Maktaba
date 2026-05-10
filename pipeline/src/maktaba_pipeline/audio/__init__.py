"""Epic 2 — audio probe, track selection, extraction, accounting.

This package owns the four pre-STT stages:

- :mod:`.probe`       Story 2.1 — ffprobe binding + ``media_info``/``audio_tracks`` writes
- :mod:`.track_selection` Story 2.2 — pick the speech track for transcription
- :mod:`.extract`     Story 2.3 — pipe ffmpeg PCM into the transcriber (or temp WAV)
- :mod:`.accounting`  Story 2.4 — extract concurrency cap + optional CPU throttle

All Python — the audio binaries (ffprobe / ffmpeg) are invoked as subprocesses;
no native bindings are pulled in by importing this package.
"""

from __future__ import annotations

from .accounting import ExtractAccountant, cpu_throttle_not_before
from .extract import ExtractError, extract_to_file, stream_pcm
from .probe import (
    AudioTrack,
    MediaInfo,
    ProbeResult,
    commit_probe,
    parse_ffprobe_json,
    probe,
)
from .track_selection import select_tracks

__all__ = [
    "AudioTrack",
    "ExtractAccountant",
    "ExtractError",
    "MediaInfo",
    "ProbeResult",
    "commit_probe",
    "cpu_throttle_not_before",
    "extract_to_file",
    "parse_ffprobe_json",
    "probe",
    "select_tracks",
    "stream_pcm",
]
