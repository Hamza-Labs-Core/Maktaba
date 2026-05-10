"""Story 2.2 — pick the audio track(s) to transcribe.

The selection is pure function over an :class:`AudioTrack` list and a
:class:`LibrarySettings`. Priority order, first match wins:

1. ``library.settings.preferred_audio_language`` matches an
   ``audio_tracks.language`` (ISO 639-3 normalised).
2. The track tagged ``ara`` (Arabic) — Maktaba's first-class language.
3. The track marked ``is_default = true`` by the container.
4. The first track by ``index``.

Identical-language duplicates resolve by channel count (more channels
win), then ``is_default``, then ``index``.

Commentary / audio-description tracks are excluded by default; they're
detected by the ``commentary`` / ``descriptions`` disposition flags or
by a title regex (``audio description``, ``described``, ``sdh``, ``cc``).

When ``library.settings.multi_audio = True`` the function returns
*all* non-commentary tracks; the caller enqueues one ``transcribe`` job
per selected track.
"""

from __future__ import annotations

import re
from dataclasses import dataclass

from .probe import AudioTrack

__all__ = ["LibrarySettings", "select_tracks"]


_DESCRIPTION_RE = re.compile(
    r"\b(audio description|described|sdh|cc|commentary)\b",
    re.IGNORECASE,
)

# ISO 639-1 → 639-3 fold for the languages we expect from typical
# user input. ffprobe usually emits 639-3 already, but library
# settings may carry the more familiar 639-1 codes.
_LANG_FOLD = {
    "ar": "ara",
    "en": "eng",
    "fr": "fra",
    "es": "spa",
    "de": "deu",
    "ru": "rus",
    "tr": "tur",
    "ur": "urd",
    "fa": "fas",
}


@dataclass(slots=True, frozen=True)
class LibrarySettings:
    """The track-selection-relevant subset of ``libraries.settings``."""

    preferred_audio_language: str | None = None
    multi_audio: bool = False
    include_commentary: bool = False


def select_tracks(
    tracks: list[AudioTrack],
    settings: LibrarySettings | None = None,
) -> list[AudioTrack]:
    """Return the selected track(s).

    Empty list when no candidates remain after excluding commentary —
    the caller should treat that as ``READY_NO_AUDIO``.
    """
    if not tracks:
        return []

    settings = settings or LibrarySettings()
    pool = [t for t in tracks if settings.include_commentary or not _is_commentary(t)]
    if not pool:
        return []

    if settings.multi_audio:
        return list(pool)

    # 1. preferred language
    pref = _normalise_lang(settings.preferred_audio_language)
    if pref:
        candidates = [t for t in pool if _normalise_lang(t.language) == pref]
        if candidates:
            return [_break_ties(candidates)]

    # 2. Arabic
    arabic = [t for t in pool if _normalise_lang(t.language) == "ara"]
    if arabic:
        return [_break_ties(arabic)]

    # 3. is_default
    default = [t for t in pool if t.is_default]
    if default:
        return [_break_ties(default)]

    # 4. fallback to lowest index
    return [_break_ties(pool)]


def _is_commentary(track: AudioTrack) -> bool:
    disp = track.disposition or {}
    if int(disp.get("commentary") or 0) == 1:
        return True
    if int(disp.get("descriptions") or 0) == 1:
        return True
    return bool(track.title and _DESCRIPTION_RE.search(track.title))


def _break_ties(candidates: list[AudioTrack]) -> AudioTrack:
    """Most channels → is_default → lowest index."""
    return sorted(
        candidates,
        key=lambda t: (-(t.channels or 0), 0 if t.is_default else 1, t.index),
    )[0]


def _normalise_lang(value: str | None) -> str | None:
    if not value:
        return None
    v = value.strip().lower()
    if not v:
        return None
    return _LANG_FOLD.get(v, v)
