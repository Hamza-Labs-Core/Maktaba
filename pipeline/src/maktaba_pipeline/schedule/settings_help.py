"""What the channel planner's settings mean (:mod:`maktaba_pipeline.settings`)."""

from __future__ import annotations

import dataclasses

from maktaba_pipeline.settings import Help, register

from .channel_planner import PlannerConfig

CH = "Channels"
GRID = "Grid and slots"
CAD = "How often shows air"
RANK = "Prime time"
FILL = "Fillers"
SPECIAL = "Quran at dawn, Friday khutbah"
QURAN = "Quran channel"
RAM = "Ramadan"
WHAT = "Programmes and filler"
DZ = "dizqueTV"

PLANNER_HELP: dict[str, Help] = {
    # channels
    "themes": Help(
        CH,
        "Themed channels",
        "One channel per theme: its number, names, and the "
        "topics / audiences / formats whose shows it takes. Leave min hours empty to "
        "use the general minimum.",
    ),
    "topic_min_score": Help(
        CH,
        "Topic match score",
        "A show belongs to a theme when its genre is "
        "one of the theme's topics, or its tag score for one reaches this.",
    ),
    "theme_min_hours": Help(
        CH, "Themed channel minimum (hours)", "A theme with fewer hours of shows gets no channel."
    ),
    "show_member_min_share": Help(
        CH,
        "Whole show share",
        "A show joins a channel whole (all its "
        "years) when this share of its hours matches the channel.",
    ),
    "mix_channel": Help(CH, "Mix channel", "A general channel mixing every genre."),
    "mix_number": Help(CH, "Mix channel number"),
    "mix_name": Help(CH, "Mix channel name"),
    "mix_name_ar": Help(CH, "Mix channel name (Arabic)"),
    "mix_min_hours": Help(CH, "Mix channel minimum (hours)"),
    "mix_exclude_topics": Help(CH, "Topics kept off the mix channel"),
    "mix_daypart_topics": Help(
        CH,
        "Mix channel: topics per daypart",
        "The genres the mix channel "
        "prefers in each daypart (its key: morning, prime …). The prime "
        "list also counts towards a show's prime-time rank.",
    ),
    "presenter_channels": Help(
        CH, "Presenter channels", "A channel for each presenter with enough hours and genres."
    ),
    "presenter_min_hours": Help(CH, "Presenter channel minimum (hours)"),
    "presenter_min_genres": Help(CH, "Presenter channel minimum genres"),
    "presenter_first_number": Help(CH, "First presenter channel number"),
    # grid
    "dayparts": Help(
        GRID,
        "Day parts",
        "The day's windows. Role: quran (recitation), high (the "
        "strongest shows first), low (the daypart's own topics first). Pattern: "
        "weekly (each day its own show), strip (one show every weekday), strip7 "
        "(every day), quran.",
        choices=(),
    ),
    "min_slot_minutes": Help(GRID, "Shortest slot (min)"),
    "max_slot_minutes": Help(GRID, "Longest slot (min)"),
    "slot_round_minutes": Help(GRID, "Slot step (min)", "Slot lengths are rounded up to this."),
    "max_lanes_per_daypart": Help(GRID, "Shows per daypart (most)"),
    "overrun_tolerance_minutes": Help(
        GRID,
        "Overrun allowed (min)",
        "An episode this much longer than its slot still gets the slot.",
    ),
    "weekend_days": Help(
        GRID,
        "Weekend days",
        "Strip shows air on the other days; weekly shows take these (mon … sun).",
    ),
    # cadence
    "cadence_min_gaps": Help(
        CAD,
        "Exact dates needed",
        "How many exact air dates a show needs before its own rhythm (daily or weekly) is used.",
    ),
    "strip_max_gap_days": Help(CAD, "Daily if the gap is at most (days)"),
    "weekly_formats": Help(CAD, "Weekly formats", "Shows of these formats air once a week."),
    "strip_formats": Help(CAD, "Daily formats", "Shows of these formats air every weekday."),
    "strip_max_limited_episodes": Help(
        CAD,
        "Short series air daily (episodes)",
        "A limited series with at most this many episodes airs every weekday.",
    ),
    "extra_airings": Help(
        CAD,
        "Extra airings",
        "A channel with fewer shows than slots repeats "
        "its shows. Off: the rest of the week is filler.",
    ),
    "extra_min_weeks_left": Help(
        CAD,
        "Extra airings keep (weeks)",
        "An extra airing never brings a slot under this many weeks of episodes.",
    ),
    "fill_target": Help(
        CAD,
        "Fill target",
        "When a channel's shows fill less than this share of "
        "its week, its biggest weekly shows move to daily strips first.",
    ),
    "promote_min_episodes": Help(CAD, "Moved to daily from (episodes)"),
    # rank
    "rank_formats": Help(RANK, "Prime-time formats"),
    "rank_format_bonus": Help(RANK, "Bonus: prime-time format"),
    "rank_prime_topic_bonus": Help(RANK, "Bonus: prime-time topic"),
    "rank_size_bonus": Help(RANK, "Bonus: size (most)"),
    "rank_size_full_hours": Help(RANK, "Full size bonus at (hours)"),
    "rank_ongoing_bonus": Help(RANK, "Bonus: ongoing programme"),
    "rank_collection_bonus": Help(RANK, "Bonus: lecture collection", "Usually negative."),
    "rank_style_bonus": Help(
        RANK, "Bonus: produced style", "Production style is only a tie-breaker."
    ),
    "prime_unknown_formats": Help(
        RANK, "Formats ranked like produced shows", "When a show's production style is unknown."
    ),
    "format_min_score": Help(RANK, "Format match score"),
    # what is a programme
    "short_series_max_minutes": Help(WHAT, "Series shorter than this are filler (min)"),
    "loose_programs": Help(
        WHAT,
        "Stand-alone videos as programmes",
        "Long videos in no series become programmes, grouped by topic.",
    ),
    "loose_program_min_minutes": Help(WHAT, "Stand-alone programme from (min)"),
    "loose_min_group_videos": Help(WHAT, "Stand-alone group from (videos)"),
    "loose_skip_genres": Help(WHAT, "Stand-alone genres never grouped"),
    # filler
    "filler_max_minutes": Help(FILL, "Longest filler (min)"),
    "filler_min_s": Help(FILL, "Shortest filler (s)"),
    "filler_topic_min_score": Help(FILL, "Filler topic match score"),
    "filler_exclude_topics": Help(FILL, "Topics never used as filler"),
    "generic_filler_topics": Help(
        FILL,
        "General filler topics",
        "The library's general filler, used after a slot's own and the channel's.",
    ),
    "dawn_filler_topics": Help(FILL, "Dawn filler topics"),
    "include_vertical_shorts": Help(FILL, "Vertical shorts as filler"),
    "filler_recent_window": Help(FILL, "No repeat of the last (fillers)"),
    "filler_between_episodes": Help(
        FILL,
        "Filler between episodes",
        "Pad between the episodes of a long block too, not only at its end.",
    ),
    "slot_filler_topics": Help(
        FILL, "Slot filler topics", "A slot's own filler matches its shows' first N topics."
    ),
    "dead_air_warn_minutes": Help(FILL, "Warn about gaps over (min)"),
    # special lanes
    "recitation_min_score": Help(SPECIAL, "Recitation score"),
    "dawn_quran": Help(
        SPECIAL,
        "Quran at dawn",
        "The quran dayparts play the library's Quran recitation on every channel.",
    ),
    "khutbah_min_score": Help(SPECIAL, "Khutbah score"),
    "friday_khutbah": Help(SPECIAL, "Friday khutbah slot"),
    "khutbah_day": Help(
        SPECIAL, "Khutbah day", choices=("mon", "tue", "wed", "thu", "fri", "sat", "sun")
    ),
    "khutbah_start": Help(SPECIAL, "Khutbah from", kind="time"),
    "khutbah_end": Help(SPECIAL, "Khutbah until", kind="time"),
    "khutbah_only_friday": Help(
        SPECIAL, "Khutbahs only in their slot", "Khutbah seasons leave the regular shows."
    ),
    # quran channel
    "quran_rotation": Help(
        QURAN,
        "Reciter rotation",
        "by_day: a reciter (or a few) per day; by_time: reciters change every few hours.",
        choices=("by_day", "by_time"),
    ),
    "quran_rotation_hours": Help(QURAN, "by_time: change every (hours)"),
    "quran_max_reciters_per_day": Help(
        QURAN,
        "Reciters per day (most)",
        "More reciters than days split each day into up to this many parts.",
    ),
    "quran_time_rules": Help(
        QURAN,
        "Styles at set times",
        "A style plays at these hours every "
        "day, rotating among its reciters (mujawwad after Fajr, warsh at "
        "night).",
        columns=("from", "until", "style"),
    ),
    "quran_styles": Help(
        QURAN,
        "Recitation styles",
        "The words that name each style in a "
        "reciter's library or season names; the first match wins.",
    ),
    "quran_default_style": Help(QURAN, "Style when none matches"),
    "quran_library_min_share": Help(
        QURAN,
        "Reciter library share",
        "A library is a reciter's "
        "when this share of its hours is recitation. Only those feed "
        "the Quran channel and Quran at dawn.",
    ),
    "quran_fallback_any_recitation": Help(
        QURAN,
        "No reciter library: use any recitation",
        "When "
        "no library reaches the share, the library's "
        "recitations still make the Quran lanes. Off: no "
        "Quran lane then.",
    ),
    "quran_filler_topics": Help(QURAN, "Quran channel filler topics"),
    "quran_filler_exclude_formats": Help(QURAN, "Formats never filler on the Quran channel"),
    # ramadan
    "ramadan_grid": Help(
        RAM, "Ramadan schedule", "Channels with Ramadan shows get a second grid for Ramadan."
    ),
    "ramadan_dayparts": Help(RAM, "Ramadan day parts"),
    "ramadan_min_score": Help(RAM, "Ramadan tag score"),
    "ramadan_exclusive": Help(RAM, "Ramadan shows only in Ramadan"),
    # dizqueTV
    "timezone": Help(DZ, "Time zone", "IANA name (Asia/Riyadh); empty: the server's."),
    "dz_lateness_ms": Help(DZ, "Allowed lateness (ms)"),
    "dz_max_days": Help(DZ, "Schedule length (days)"),
    "dz_pad_ms": Help(DZ, "Pad (ms)"),
    "dz_filler_cooldown_ms": Help(DZ, "Filler cooldown (ms)"),
    "dz_filler_max_items": Help(DZ, "Filler list size (most clips)", "A dizqueTV filler list "
                                "holds the first this many clips of its pool (own promos, then "
                                "on-topic clips). dizqueTV takes at most 50 MB per request."),
}

#: the smallest value each number may take: below it a rule divides by zero, takes the
#: median of nothing or schedules nothing
_MINIMUM: dict[str, float] = {
    "cadence_min_gaps": 1, "strip_max_gap_days": 0.1, "min_slot_minutes": 5,
    "max_slot_minutes": 5, "slot_round_minutes": 1, "max_lanes_per_daypart": 1,
    "overrun_tolerance_minutes": 0, "theme_min_hours": 0, "mix_min_hours": 0,
    "presenter_min_hours": 0, "presenter_min_genres": 1, "short_series_max_minutes": 0,
    "loose_program_min_minutes": 0, "loose_min_group_videos": 1, "filler_max_minutes": 0,
    "filler_min_s": 0, "filler_recent_window": 0, "slot_filler_topics": 1,
    "dead_air_warn_minutes": 0, "strip_max_limited_episodes": 0, "fill_target": 0,
    "promote_min_episodes": 1, "extra_min_weeks_left": 0, "show_member_min_share": 0,
    "rank_size_full_hours": 0.1, "quran_rotation_hours": 1, "quran_max_reciters_per_day": 1,
    "quran_library_min_share": 0, "dz_lateness_ms": 0, "dz_max_days": 1, "dz_pad_ms": 0,
    "dz_filler_cooldown_ms": 0, "dz_filler_max_items": 1,
}  # fmt: skip
for _name, _min in _MINIMUM.items():
    PLANNER_HELP[_name] = dataclasses.replace(PLANNER_HELP[_name], minimum=_min)
PLANNER_HELP["quran_time_rules"] = dataclasses.replace(
    PLANNER_HELP["quran_time_rules"], time_columns=(0, 1)
)

register(PlannerConfig, PLANNER_HELP)
