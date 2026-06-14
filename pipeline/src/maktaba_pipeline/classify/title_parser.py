"""Story 26.1 — title parser (filename → structured metadata).

The single highest-signal, lowest-cost classifier in Epic 26: a name like
``Show.Name.S01E02.720p.x265-GRP.mkv`` already encodes the show, season,
episode, resolution, codec, and release group with no network and no
model. :func:`parse` recovers all of it.

Design (mirrors :mod:`library_mgmt.content_type` / :mod:`.topics`): a
**pure function** — no I/O, no DB, no network — so it is trivially
testable against a large fixture corpus and is reused as a library by
series detection (26.3) and enrichment matching (26.5).

Contract (AC-1): ``parse`` is **total** (never raises) and
**deterministic** — the same input yields a byte-identical
:class:`ParsedTitle` for a fixed :data:`PARSER_VERSION`. An unparseable
name yields a low-confidence result carrying just the cleaned title.
"""

from __future__ import annotations

import re
import unicodedata
from collections.abc import Sequence
from dataclasses import dataclass, field
from datetime import date

__all__ = [
    "PARSER_VERSION",
    "ParsedTitle",
    "parse",
]

#: Bumped whenever the parsing rules change so the ``classify`` stage can
#: re-run videos whose ``media_parsed_titles.parser_version`` is older.
PARSER_VERSION: int = 1

#: Names longer than this are truncated before processing (edge case: an
#: absurdly long name must still return within the ≤2 ms/name budget).
_MAX_NAME_LEN: int = 512

# --- token vocabularies -------------------------------------------------
# Each maps a matched token (case-folded) to its canonical value.

_RESOLUTION_RE = re.compile(r"(?i)(?<![a-z0-9])(480|576|720|1080|1440|2160|4320)[pi](?![a-z0-9])")
_RESOLUTION_4K_RE = re.compile(r"(?i)(?<![a-z0-9])(4k|8k|uhd)(?![a-z0-9])")

_SOURCE_RE = re.compile(
    r"(?i)(?<![a-z0-9])"
    r"(bluray|blu-ray|brrip|bdrip|bdremux|remux|webrip|web-dl|webdl|web|hdtv|"
    r"dvdrip|dvd|hdrip|hdcam|cam|ts|tvrip|pdtv)"
    r"(?![a-z0-9])"
)
_SOURCE_CANON = {
    "blu-ray": "bluray",
    "web-dl": "webdl",
    "web": "webdl",
}

_VCODEC_RE = re.compile(
    r"(?i)(?<![a-z0-9])(x264|x265|h\.?264|h\.?265|hevc|avc|av1|xvid|divx|vp9)(?![a-z0-9])"
)
_VCODEC_CANON = {"h264": "h264", "h.264": "h264", "h265": "h265", "h.265": "h265"}

_ACODEC_RE = re.compile(
    r"(?i)(?<![a-z0-9])(aac|ac3|eac3|dts(?:-hd)?|dd5\.1|ddp?5\.1|truehd|flac|mp3|opus)(?![a-z0-9])"
)

_EDITION_RE = re.compile(
    r"(?i)(?<![a-z0-9])"
    r"(extended|director'?s?(?:[ ._-]?cut)?|remastered|unrated|theatrical|imax|"
    r"special[ ._-]?edition)"
    r"(?![a-z0-9])"
)
_EDITION_CANON = {
    "directors": "directors",
    "director's": "directors",
    "directors cut": "directors",
    "director's cut": "directors",
    "special edition": "special",
}

# Language hints: Latin tokens + Arabic cue words (dubbed / subtitled).
_LANG_RE = re.compile(
    r"(?i)(?<![a-z0-9])(arabic|english|french|spanish|turkish|german)(?![a-z0-9])"
)
_LANG_CANON = {
    "arabic": "ar",
    "english": "en",
    "french": "fr",
    "spanish": "es",
    "turkish": "tr",
    "german": "de",
}
_AR_LANG_RE = re.compile(r"(مدبلج|مترجم)")  # dubbed / subtitled → Arabic context

# --- structural markers -------------------------------------------------

# SxxExx with optional second episode (S01E01E02) or range (S01E01-E03).
_SEASON_EP_RE = re.compile(r"(?i)s(\d{1,2})[ ._]?e(\d{1,3})(?:[ ._-]?e(\d{1,3}))?")
# Alt 01x02 form; guarded so a resolution like 1080x… or 2x speed won't hit.
_ALT_EP_RE = re.compile(r"(?<![\dxX])(\d{1,2})[xX](\d{1,3})(?![\dpiPI])")
# Absolute numbering for anime: a bare "E137" with no season.
_ABS_EP_RE = re.compile(r"(?i)(?<![a-z0-9])e(\d{2,4})(?![a-z0-9])")

# Arabic: الموسم (season) / الحلقة (episode) followed by digits (either script).
_DIGITS = r"[0-9٠-٩۰-۹]+"
_AR_SEASON_RE = re.compile(rf"الموسم\s*({_DIGITS})")
_AR_EP_RE = re.compile(rf"الحلق[ةه]\s*({_DIGITS})")

# Dates: 2024.03.14 (airdate-based shows).
_DATE_RE = re.compile(r"(?<!\d)((?:19|20)\d{2})[ ._-](\d{2})[ ._-](\d{2})(?!\d)")
# Parenthesised / bracketed year — the strong year signal.
_PAREN_YEAR_RE = re.compile(r"[\(\[]\s*((?:19|20)\d{2})\s*[\)\]]")
# Bare year token (movie dotted form); never directly before a 'p'/'i'.
_BARE_YEAR_RE = re.compile(r"(?<!\d)((?:19|20)\d{2})(?![\dpi])")

# Trailing scene release group: "-GRP" at the very end of the stem.
_GROUP_RE = re.compile(r"-([A-Za-z0-9]{2,})$")
# Bracketed noise: [www.site.com], {edition-x}, etc.
_NOISE_RE = re.compile(r"[\[\{][^\]\}]*[\]\}]")
_SAMPLE_RE = re.compile(r"(?i)(?<![a-z0-9])sample(?![a-z0-9])")

_AR_DIGIT_MAP = {ord(c): str(i % 10) for i, c in enumerate("٠١٢٣٤٥٦٧٨٩۰۱۲۳۴۵۶۷۸۹")}


@dataclass(slots=True, frozen=True)
class ParsedTitle:
    """Structured result of :func:`parse`.

    Every field but ``title``, ``kind``, ``confidence`` and
    ``parser_version`` is nullable; an unparseable name leaves them
    ``None`` and yields a low ``confidence``. ``metadata`` carries
    out-of-band flags (e.g. ``is_sample``).
    """

    title: str
    show_name: str | None = None
    season: int | None = None
    episode: int | None = None
    episode_end: int | None = None
    episode_title: str | None = None
    year: int | None = None
    airdate: date | None = None
    resolution: str | None = None
    source: str | None = None
    video_codec: str | None = None
    audio_codec: str | None = None
    release_group: str | None = None
    edition: str | None = None
    language_hint: str | None = None
    kind: str = "unknown"  # episode | movie | unknown
    confidence: float = 0.0
    parser_version: int = PARSER_VERSION
    metadata: dict[str, object] = field(default_factory=dict)


def _to_int(token: str) -> int:
    """Parse a run of Latin- or Arabic-Indic digits to ``int``."""
    return int(token.translate(_AR_DIGIT_MAP))


def _has_arabic(s: str) -> bool:
    return any("؀" <= ch <= "ۿ" for ch in s)


def _clean_segment(segment: str) -> str:
    """Normalise a raw show/title/episode-title segment for display.

    Strips bracketed noise, normalises ``. _ -`` separators to spaces,
    collapses whitespace, and title-cases Latin text while leaving Arabic
    untouched (AC: "title-case for Latin, untouched for Arabic").
    """
    s = _NOISE_RE.sub(" ", segment)
    s = re.sub(r"[._]+", " ", s)
    s = s.replace("-", " ")
    s = re.sub(r"\s+", " ", s).strip()
    if not s:
        return ""
    if _has_arabic(s):
        return s
    # Title-case Latin words but preserve all-digit tokens (e.g. "1923").
    return " ".join(w if w.isdigit() else w[:1].upper() + w[1:].lower() for w in s.split())


def _strip_metadata(segment: str, *, strip_years: bool = True) -> str:
    """Remove every metadata token from a segment (used for show names /
    episode titles so codec/res/group residue never bleeds in).

    ``strip_years`` is disabled for episodic show names: a leading 4-digit
    token there is the show (e.g. ``1923``), not a year — "S/E presence
    wins" per the spec.
    """
    rgxs = [
        _RESOLUTION_RE,
        _RESOLUTION_4K_RE,
        _SOURCE_RE,
        _VCODEC_RE,
        _ACODEC_RE,
        _EDITION_RE,
        _LANG_RE,
        _SAMPLE_RE,
    ]
    if strip_years:
        rgxs += [_BARE_YEAR_RE, _PAREN_YEAR_RE]
    for rgx in rgxs:
        segment = rgx.sub(" ", segment)
    return segment


def parse(filename: str, *, dirnames: Sequence[str] = ()) -> ParsedTitle:
    """Parse ``filename`` (with optional parent ``dirnames``) into a
    :class:`ParsedTitle`. Pure, total, deterministic.

    ``dirnames`` is the ordered list of containing directory names
    (outermost first); it is consulted only as a fallback when the
    filename alone lacks a show name or season.
    """
    raw = (filename or "").strip()
    if len(raw) > _MAX_NAME_LEN:
        raw = raw[:_MAX_NAME_LEN]

    # Strip the path and the final extension; flag and drop a ".sample".
    stem = raw.rsplit("/", 1)[-1].rsplit("\\", 1)[-1]
    stem = re.sub(r"\.[A-Za-z0-9]{1,4}$", "", stem)  # drop extension
    is_sample = bool(_SAMPLE_RE.search(stem))
    stem = unicodedata.normalize("NFC", stem)

    meta: dict[str, object] = {}
    if is_sample:
        meta["is_sample"] = True

    # --- release group (trailing -GRP), before we mangle separators ----
    release_group: str | None = None
    gm = _GROUP_RE.search(stem)

    # --- metadata tokens -----------------------------------------------
    resolution = _first(stem, _RESOLUTION_RE, lambda m: m.group(1) + "p")
    if resolution is None and _RESOLUTION_4K_RE.search(stem):
        resolution = "2160p"
    source = _first(stem, _SOURCE_RE, lambda m: _SOURCE_CANON.get(m.group(1).lower(), m.group(1).lower()))
    video_codec = _first(
        stem, _VCODEC_RE, lambda m: _VCODEC_CANON.get(m.group(1).lower().replace(".", ""), m.group(1).lower())
    )
    audio_codec = _first(stem, _ACODEC_RE, lambda m: m.group(1).lower())
    edition = _first(
        stem, _EDITION_RE, lambda m: _EDITION_CANON.get(_norm_edition(m.group(1)), _norm_edition(m.group(1)))
    )
    language_hint = _first(stem, _LANG_RE, lambda m: _LANG_CANON[m.group(1).lower()])
    if language_hint is None and _AR_LANG_RE.search(stem):
        language_hint = "ar"

    # A trailing group is only meaningful once we know the name carries
    # release metadata; otherwise "-Word" is part of the title.
    if gm and (resolution or video_codec or source):
        release_group = gm.group(1)

    # --- structural markers (find the earliest one to split the name) --
    markers: list[tuple[int, str, re.Match[str]]] = []
    for kind_tag, rgx in (
        ("se", _SEASON_EP_RE),
        ("alt", _ALT_EP_RE),
        ("ar_se", _AR_SEASON_RE),
        ("ar_ep", _AR_EP_RE),
        ("date", _DATE_RE),
        ("paren_year", _PAREN_YEAR_RE),
    ):
        m = rgx.search(stem)
        if m:
            markers.append((m.start(), kind_tag, m))

    season: int | None = None
    episode: int | None = None
    episode_end: int | None = None
    year: int | None = None
    airdate: date | None = None
    kind = "unknown"

    se = _SEASON_EP_RE.search(stem)
    alt = _ALT_EP_RE.search(stem)
    ar_se = _AR_SEASON_RE.search(stem)
    ar_ep = _AR_EP_RE.search(stem)
    dm = _DATE_RE.search(stem)
    pym = _PAREN_YEAR_RE.search(stem)

    if se:
        season = _to_int(se.group(1))
        episode = _to_int(se.group(2))
        if se.group(3):
            episode_end = _to_int(se.group(3))
        kind = "episode"
    elif alt:
        season = _to_int(alt.group(1))
        episode = _to_int(alt.group(2))
        kind = "episode"

    if ar_se:
        season = _to_int(ar_se.group(1))
        kind = "episode"
    if ar_ep:
        episode = _to_int(ar_ep.group(1))
        kind = "episode"

    if dm:
        try:
            airdate = date(int(dm.group(1)), int(dm.group(2)), int(dm.group(3)))
            kind = "episode"
        except ValueError:
            airdate = None

    # Year: a parenthesised year always wins; a bare year promotes an
    # otherwise-unmarked name to a movie. Never treat a date's year as a
    # standalone year.
    if pym:
        year = int(pym.group(1))
    if kind == "unknown" and airdate is None:
        bym = _BARE_YEAR_RE.search(stem)
        if bym and year is None:
            year = int(bym.group(1))
        if year is not None:
            kind = "movie"

    # --- show name / title segmentation --------------------------------
    # Everything before the earliest structural marker is the name.
    split_at = min((pos for pos, _, _ in markers), default=len(stem))
    name_segment = stem[:split_at]
    # Drop a trailing -GRP from the tail before cleaning the name region.
    if release_group and name_segment.rstrip().endswith("-" + release_group):
        name_segment = name_segment.rsplit("-" + release_group, 1)[0]

    show_clean = _clean_segment(_strip_metadata(name_segment, strip_years=(kind == "movie")))

    # Episode title: the text after the episode/date marker, with metadata
    # stripped. Empty / all-metadata tails (scene releases) yield None.
    episode_title: str | None = None
    tail_marker = se or alt or ar_ep or dm
    if kind == "episode" and tail_marker is not None:
        tail = stem[tail_marker.end():]
        # Drop a trailing group from the tail too.
        if release_group:
            tail = _GROUP_RE.sub("", tail)
        tail_clean = _clean_segment(_strip_metadata(tail))
        if tail_clean and not tail_clean.isdigit():
            episode_title = tail_clean

    # --- directory fallback --------------------------------------------
    # A bare-number filename (e.g. "02.mkv") carries only the episode
    # number; the show + season come from the parent directories.
    if (not show_clean or kind == "unknown" or show_clean.isdigit()) and dirnames:
        d_show, d_season = _from_dirs(dirnames)
        if d_season is not None and season is None:
            season = d_season
        if episode is None:
            bare = _bare_episode_number(stem)
            if bare is not None:
                episode = bare
        if d_show and (not show_clean or show_clean.isdigit()):
            show_clean = d_show
        if season is not None or episode is not None:
            kind = "episode"

    title = (
        show_clean
        or _clean_segment(_strip_metadata(stem, strip_years=(kind != "episode")))
        or stem.strip()
    )

    show_name = show_clean or None if kind == "episode" else None

    confidence = _score(
        kind=kind,
        season=season,
        episode=episode,
        year=year,
        airdate=airdate,
        extras=[resolution, video_codec, release_group, source, audio_codec, edition],
    )

    return ParsedTitle(
        title=title,
        show_name=show_name,
        season=season,
        episode=episode,
        episode_end=episode_end,
        episode_title=episode_title,
        year=year,
        airdate=airdate,
        resolution=resolution,
        source=source,
        video_codec=video_codec,
        audio_codec=audio_codec,
        release_group=release_group,
        edition=edition,
        language_hint=language_hint,
        kind=kind,
        confidence=confidence,
        parser_version=PARSER_VERSION,
        metadata=meta,
    )


def _first(s: str, rgx: re.Pattern[str], render):  # type: ignore[no-untyped-def]
    m = rgx.search(s)
    return render(m) if m else None


def _norm_edition(token: str) -> str:
    return re.sub(r"[._-]+", " ", token.lower()).strip()


def _bare_episode_number(stem: str) -> int | None:
    m = re.fullmatch(r"\s*0*(\d{1,3})\s*", stem)
    return int(m.group(1)) if m else None


def _from_dirs(dirnames: Sequence[str]) -> tuple[str | None, int | None]:
    """Recover (show, season) from parent directory names.

    A ``Season 01`` / ``S01`` directory yields the season; the nearest
    non-season directory above it yields the show.
    """
    show: str | None = None
    season: int | None = None
    for d in dirnames:
        sm = re.fullmatch(r"(?i)\s*(?:season|s)\s*0*(\d{1,2})\s*", d.strip())
        if sm:
            season = int(sm.group(1))
        else:
            cleaned = _clean_segment(d)
            if cleaned:
                show = cleaned
    return show, season


def _score(
    *,
    kind: str,
    season: int | None,
    episode: int | None,
    year: int | None,
    airdate: date | None,
    extras: Sequence[str | None],
) -> float:
    """Confidence reflects how much structure was recovered (AC).

    Full scene release ≈ 0.95; bare ``Movie (2024)`` ≈ 0.8; a name that
    yielded only a cleaned title ≈ 0.2.
    """
    if kind == "episode":
        conf = 0.7
        if season is not None:
            conf += 0.1
        if episode is not None:
            conf += 0.05
        if airdate is not None:
            conf += 0.05
    elif kind == "movie":
        conf = 0.8
    else:
        return 0.2
    conf += 0.03 * min(sum(1 for e in extras if e), 5)
    return round(min(conf, 0.97), 4)
