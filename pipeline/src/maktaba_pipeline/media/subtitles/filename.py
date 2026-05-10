"""External subtitle filename parsing (Story 4.3 — discovery).

Hosts drop subtitle files next to videos in one of a few common
patterns: ``Video.srt``, ``Video.en.srt``, ``Video.en.forced.srt``,
``Video.ar.sdh.vtt``. This module compiles a regex per video stem
and parses any candidate sibling file. The returned dataclass is
opaque to dialect/storage concerns — the discovery layer turns it
into ``subtitle_files`` rows.
"""

from __future__ import annotations

import re
from dataclasses import dataclass
from pathlib import Path

__all__ = [
    "EXT_RE",
    "FLAG_RE",
    "LANG_RE",
    "ParsedSubtitleFilename",
    "compile_subtitle_regex",
    "normalize_lang",
    "parse_filename",
]

# Loose ISO 639 alpha-2 / alpha-3 range. The regex matches the
# *shape* (2–3 letters); ``normalize_lang`` does the
# alpha-2-only canonicalisation.
LANG_RE = r"(?P<lang>[A-Za-z]{2,3})"
FLAG_RE = r"(?P<flag>forced|sdh|cc|hi)"
EXT_RE = r"(?P<ext>srt|vtt|ass|ssa)"


# ISO 639-1 codes we recognise on input. Anything outside this set
# normalises to ``und`` with the raw tag retained in the row's
# metadata for forensics.
_KNOWN_LANGS: frozenset[str] = frozenset(
    {"ar", "en", "fr", "de", "es", "ru", "tr", "ur"}
)


@dataclass(frozen=True, slots=True)
class ParsedSubtitleFilename:
    """Decoded parts of one ``Stem(.lang)?(.flag)?.ext`` filename.

    ``lang`` is the *normalised* ISO 639-1 code (or ``"und"``).
    ``raw_lang`` preserves the input tag verbatim so unrecognised
    values can be surfaced in metadata. ``flag`` is one of
    ``forced`` / ``sdh`` / ``cc`` / ``hi`` (lowercased) or None.
    """

    lang: str | None
    raw_lang: str | None
    flag: str | None
    ext: str


def compile_subtitle_regex(video_stem: str) -> re.Pattern[str]:
    """Compile a case-insensitive matcher for sidecars of ``video_stem``.

    The pattern: ``^<escaped_stem>(?:\\.<lang>)?(?:\\.<flag>)?\\.<ext>$``.
    The stem is ``re.escape``'d so dots, brackets, and parentheses in
    the source video name don't break the match.
    """
    stem_re = re.escape(video_stem)
    pattern = rf"^{stem_re}(?:\.{LANG_RE})?(?:\.{FLAG_RE})?\.{EXT_RE}$"
    return re.compile(pattern, re.IGNORECASE)


def parse_filename(
    candidate: Path,
    video_stem: str,
) -> ParsedSubtitleFilename | None:
    """Return a parsed filename, or None when no match."""
    matcher = compile_subtitle_regex(video_stem)
    m = matcher.match(candidate.name)
    if m is None:
        return None
    raw_lang = m.group("lang")
    flag = m.group("flag")
    ext = m.group("ext").lower()
    lang, raw = normalize_lang(raw_lang)
    return ParsedSubtitleFilename(
        lang=lang,
        raw_lang=raw,
        flag=flag.lower() if flag else None,
        ext=ext,
    )


def normalize_lang(raw: str | None) -> tuple[str, str | None]:
    """Canonicalise a raw language tag.

    Returns ``(canonical, raw_for_metadata)``. When the input is
    recognised the second element is ``None`` (no need to keep the
    raw). When unrecognised, the canonical is ``"und"`` and the raw
    is retained verbatim so the metadata column can show the user
    what we saw on disk.
    """
    if raw is None:
        return ("und", None)
    lowered = raw.lower()
    if lowered in _KNOWN_LANGS:
        return (lowered, None)
    return ("und", raw)
