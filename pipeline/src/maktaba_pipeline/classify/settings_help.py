"""What the series detector's settings mean (:mod:`maktaba_pipeline.settings`)."""

from __future__ import annotations

from maktaba_pipeline.settings import Help, register

from .series_detector import DetectorConfig

NAMES = "Names and playlists"
SEASONS = "Seasons"
ONGOING = "Ongoing programmes"
BRANDS = "Talk shows (title brands)"
MATCH = "Matching loose videos"

DETECTOR_HELP: dict[str, Help] = {
    "generic_words": Help(
        NAMES,
        "Words that never name a show",
        "Ignored when a show's name is "
        "read from titles (the presenter's and channel's words are added).",
    ),
    "clip_playlist_re": Help(
        NAMES,
        "Clip playlists (pattern)",
        "A playlist whose title matches holds clips, never a show's episodes.",
    ),
    "catch_all_re": Help(
        NAMES,
        "Catch-all playlists (pattern)",
        '"All videos", "منوعات": never a show once this big.',
    ),
    "catch_all_min": Help(NAMES, "Catch-all from (videos)"),
    "topic_min": Help(
        NAMES,
        "Topic collection from (videos)",
        "A curated playlist cutting across shows (الشأن الفلسطيني) becomes a topic collection.",
    ),
    "min_episode_s": Help(
        SEASONS, "Shortest episode (s)", "Anything shorter is a short or a clip, never an episode."
    ),
    "min_season_size": Help(SEASONS, "Smallest season (episodes)"),
    "core_band": Help(
        SEASONS,
        "Episode length band",
        "A season's videos farther than this factor from its usual length are extras.",
    ),
    "same_show_ratio": Help(
        SEASONS,
        "Same show length ratio",
        "Two playlists of one name are one show only if their usual lengths are this close.",
    ),
    "short_form_s": Help(SEASONS, "Promo length (s)", "Shorter extras count as promos."),
    "ongoing_min_episodes": Help(ONGOING, "Ongoing from (episodes)"),
    "ongoing_min_number": Help(ONGOING, "Ongoing when numbers reach"),
    "ongoing_min_span_days": Help(ONGOING, "Ongoing span (days)"),
    "ongoing_month_coverage": Help(
        ONGOING, "Ongoing month coverage", "The share of months in the span with an episode."
    ),
    "brand_min": Help(
        BRANDS,
        "Talk show from (videos)",
        "A title part (شاهد على العصر |) carried by at least this many videos names a show.",
    ),
    "brand_share": Help(BRANDS, "Talk show share of the channel"),
    "brand_scattered_series": Help(BRANDS, "Rebuild when spread over (series)"),
    "brand_named_share": Help(BRANDS, "Rebuild below this share in its own series"),
    "attach_threshold": Help(
        MATCH, "Match score", "A video in no playlist joins a season from this score."
    ),
    "date_window_days": Help(MATCH, "Date window (days)"),
    "approx_window_days": Help(MATCH, "Date window, approximate dates (days)"),
    "sig_length_band": Help(MATCH, "Fingerprint length band"),
    "sig_window_days": Help(MATCH, "Fingerprint date window (days)"),
    "dhash_close_bits": Help(MATCH, "Thumbnail match (bits)"),
}

register(DetectorConfig, DETECTOR_HELP)
