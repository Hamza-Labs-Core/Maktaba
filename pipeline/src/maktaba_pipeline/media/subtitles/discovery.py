"""External subtitle discovery (Story 4.3).

Scans for subtitle sidecars next to a source video and inside the
common ``Subs/`` / ``subs/`` / ``Subtitles/`` subdirectories. Returns
a list of :class:`SubtitleFileRow` describing each match. This module
does *not* touch the database — the stage handler is responsible for
upserting rows into ``subtitle_files`` so the dialect-specific SQL
stays in one place.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

from .filename import parse_filename

__all__ = ["SubtitleFileRow", "discover_subtitles_for_video_sync"]


# Conventional sibling subdirectories players check. Names are
# case-insensitive on macOS/Windows; we match the canonical Linux
# spellings and rely on the regex matcher inside ``parse_filename``
# to be case-insensitive for the stem itself.
_SIBLING_DIRS: tuple[str, ...] = ("Subs", "subs", "Subtitles", "subtitles")


@dataclass(frozen=True, slots=True)
class SubtitleFileRow:
    """One row destined for ``subtitle_files``.

    The ``flags`` dict mirrors the JSONB column on the table; today
    we set ``forced``, ``sdh``, ``cc``, ``hi`` booleans from the
    parsed filename flag. ``metadata`` carries the unrecognised raw
    language tag for ``"und"`` rows so the UI can show the user
    what was on disk.
    """

    video_id: str
    format: str
    language: str
    path: str
    is_external: bool
    is_embedded: bool
    flags: dict[str, bool] = field(default_factory=dict)
    track_index: int | None = None
    metadata: dict[str, Any] = field(default_factory=dict)


def _flag_dict(flag: str | None) -> dict[str, bool]:
    return {
        "forced": flag == "forced",
        "sdh": flag == "sdh",
        "cc": flag == "cc",
        "hi": flag == "hi",
    }


def _iter_candidates(video_path: Path) -> list[Path]:
    """Yield every file we should try to parse for this video.

    Looks at:

    - direct siblings of the video file;
    - files inside any ``Subs/``-style subdirectory next to the
      video.

    Missing directories are silently skipped — the discovery is
    advisory.
    """
    parent = video_path.parent
    candidates: list[Path] = []
    try:
        for entry in parent.iterdir():
            if entry.is_file():
                candidates.append(entry)
    except OSError:
        # Permission denied / not a directory — nothing to discover.
        return candidates

    for sub in _SIBLING_DIRS:
        sub_dir = parent / sub
        if not sub_dir.is_dir():
            continue
        try:
            for entry in sub_dir.iterdir():
                if entry.is_file():
                    candidates.append(entry)
        except OSError:
            continue
    return candidates


def discover_subtitles_for_video_sync(
    video_path: Path,
    video_id: str,
) -> list[SubtitleFileRow]:
    """Scan the filesystem for sidecars belonging to ``video_path``.

    Synchronous on purpose — async callers wrap with
    ``asyncio.to_thread`` to keep the event loop free of blocking
    syscalls. The stage handler is responsible for upserting the
    returned rows into ``subtitle_files``; this module returns pure
    data so the parsing logic stays unit-testable without a DB.
    """
    stem = video_path.stem
    rows: list[SubtitleFileRow] = []
    for candidate in _iter_candidates(video_path):
        parsed = parse_filename(candidate, stem)
        if parsed is None:
            continue
        lang = parsed.lang or "und"
        metadata: dict[str, Any] = {}
        if parsed.raw_lang is not None:
            metadata["raw_lang_tag"] = parsed.raw_lang
        rows.append(
            SubtitleFileRow(
                video_id=video_id,
                format=parsed.ext,
                language=lang,
                path=str(candidate),
                is_external=True,
                is_embedded=False,
                flags=_flag_dict(parsed.flag),
                track_index=None,
                metadata=metadata,
            )
        )
    return rows
