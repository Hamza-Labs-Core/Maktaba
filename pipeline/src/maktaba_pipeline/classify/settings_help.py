"""What the series detector's and the topic tagger's settings mean
(:mod:`maktaba_pipeline.settings`)."""

from __future__ import annotations

import dataclasses

from maktaba_pipeline.settings import Help, register

from .series_detector import DetectorConfig
from .topic_tags import TaggerConfig

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

_MINIMUM: dict[str, float] = {
    "catch_all_min": 1, "topic_min": 1, "min_episode_s": 0, "min_season_size": 1,
    "core_band": 1, "same_show_ratio": 1, "short_form_s": 0, "ongoing_min_episodes": 1,
    "ongoing_min_number": 1, "ongoing_min_span_days": 0, "ongoing_month_coverage": 0,
    "brand_min": 1, "brand_share": 0, "brand_scattered_series": 1, "brand_named_share": 0,
    "date_window_days": 0, "approx_window_days": 0, "sig_length_band": 0,
    "sig_window_days": 0, "dhash_close_bits": 0,
}  # fmt: skip
for _name, _min in _MINIMUM.items():
    DETECTOR_HELP[_name] = dataclasses.replace(DETECTOR_HELP[_name], minimum=_min)

register(DetectorConfig, DETECTOR_HELP)

# ------------------------------------------------------------------------- topic tagger

WEIGHTS = "Evidence weights"
TAGS = "Tags"
SPEECH = "Transcripts"
RECITED = "Recitation in transcripts"

TAGGER_HELP: dict[str, Help] = {
    "w_name": Help(WEIGHTS, "Series name weight", "How much the series' own name counts."),
    "w_playlist": Help(WEIGHTS, "Playlist title weight"),
    "w_episodes": Help(
        WEIGHTS, "Episode titles weight", "Times the share of episode titles naming a topic."
    ),
    "w_description": Help(WEIGHTS, "Description weight", "Only a description's opening counts."),
    "w_channel": Help(
        WEIGHTS, "Channel weight", "A prior: a reciter's channel leans everything to recitation."
    ),
    "w_title": Help(WEIGHTS, "Video title weight", "For a video in no season."),
    "min_score": Help(TAGS, "Lowest tag score", "Weaker tags are dropped (0 to 1)."),
    "clip_max_s": Help(TAGS, "Clip up to (s)", "A shorter season median or video is clip-like."),
    "long_form_s": Help(TAGS, "Long-form from (s)", "Long videos naming no format are lectures."),
    "occasion_share": Help(
        TAGS,
        "Occasion share",
        "The share of exact air dates in Ramadan / Dhu al-Hijjah that makes a season seasonal.",
    ),
    "min_dated_episodes": Help(TAGS, "Occasion from (dated episodes)"),
    "program_min_episodes": Help(
        TAGS,
        "Broadcast program from (episodes)",
        "A season airing this many episodes within the window below is a program.",
    ),
    "program_window_days": Help(TAGS, "Broadcast window (days)"),
    "w_transcript": Help(
        SPEECH,
        "Transcript weight",
        "Times the share of transcripts that talk about a topic. Below the titles: speech "
        "is noisy and talks wander.",
    ),
    "transcript_max_minutes": Help(
        SPEECH, "Read the first (min)", "Only the opening of each transcript is read."
    ),
    "transcript_max_chars": Help(SPEECH, "Read at most (characters)"),
    "transcript_max_episodes": Help(
        SPEECH,
        "Transcripts per season",
        "A bigger season is judged on this many transcripts, spread over its episodes.",
    ),
    "transcript_namespaces": Help(
        SPEECH,
        "Tag kinds from speech",
        "topic, format, audience, occasion: which kinds of tag a transcript may give.",
    ),
    "transcript_min_weight": Help(
        SPEECH,
        "Lightest phrase in speech",
        "Weaker lexicon phrases (النبي, القلب) never count in a transcript.",
    ),
    "transcript_single_word_weight": Help(
        SPEECH,
        "Single words in speech from",
        "A one-word phrase counts only from this weight (السيرة, تفسير); lighter evidence "
        "must be a phrase of several words (غزوة بدر).",
    ),
    "transcript_full_strength": Help(
        SPEECH,
        "Mentions for full evidence",
        "The summed phrase weight in one transcript that makes it full evidence.",
    ),
    "transcript_stop_phrases": Help(
        SPEECH, "Formulas to ignore", "Said in every talk; never evidence of a topic."
    ),
    "w_transcript_recitation": Help(
        RECITED,
        "Recitation weight",
        "Times the share of transcripts that sound recited (Quran text, no commentary).",
    ),
    "recitation_min_words": Help(
        RECITED, "Judge from (words)", "Shorter transcripts are never called recited."
    ),
    "recitation_max_commentary": Help(
        RECITED,
        "Commentary words per 100, at most",
        "More than this and it is a talk quoting verses, not a recitation.",
    ),
    "recitation_min_quranic_share": Help(
        RECITED, "Quranic word share, at least", "The share of words typical of the Quran."
    ),
    "recitation_commentary_words": Help(
        RECITED,
        "Commentary words",
        "Words a speaker says and the Quran does not: fillers, addresses to the audience, "
        "the khutbah opening, hadith citations. Whole words.",
    ),
    "recitation_quranic_words": Help(
        RECITED, "Quranic words", "Words frequent in the Quran's text (prefixes و ف ب ل allowed)."
    ),
}

_TAGGER_MINIMUM: dict[str, float] = {
    "w_name": 0, "w_playlist": 0, "w_episodes": 0, "w_description": 0, "w_channel": 0,
    "w_title": 0, "min_score": 0, "clip_max_s": 0, "long_form_s": 0, "occasion_share": 0,
    "min_dated_episodes": 1, "program_min_episodes": 1, "program_window_days": 1,
    "w_transcript": 0, "transcript_max_minutes": 0, "transcript_max_chars": 0,
    "transcript_max_episodes": 1, "transcript_min_weight": 0,
    "transcript_single_word_weight": 0, "transcript_full_strength": 0.1,
    "w_transcript_recitation": 0, "recitation_min_words": 1, "recitation_max_commentary": 0,
    "recitation_min_quranic_share": 0,
}  # fmt: skip
for _name, _min in _TAGGER_MINIMUM.items():
    TAGGER_HELP[_name] = dataclasses.replace(TAGGER_HELP[_name], minimum=_min)

register(TaggerConfig, TAGGER_HELP)
