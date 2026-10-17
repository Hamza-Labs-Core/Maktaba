"""The grid tests' settings: small libraries, so no lineup limits, and the dayparts and
rules the tests were written against (the defaults now favour a 20-channel lineup)."""

from __future__ import annotations

from maktaba_pipeline.schedule.channel_planner import Daypart, _hm

DAYPARTS: tuple[Daypart, ...] = (
    Daypart("dawn", _hm("04:00"), _hm("07:00"), "quran", "Dawn: Quran", "quran"),
    Daypart("morning", _hm("07:00"), _hm("12:00"), "low", "Morning", "strip"),
    Daypart("afternoon", _hm("12:00"), _hm("17:00"), "low", "Afternoon", "strip"),
    Daypart("evening", _hm("17:00"), _hm("20:00"), "high", "Evening", "strip"),
    Daypart("prime", _hm("20:00"), _hm("23:00"), "high", "Prime time", "weekly"),
    Daypart("late", _hm("23:00"), _hm("04:00"), "low", "Late night", "weekly"),
)

SMALL: dict[str, object] = {
    "channel_min_days": 0.0,
    "max_channels": 99,
    "presenter_max_channels": 99,
    "slot_owner": "show",
    "recitation_genres": ("quran_recitation", "tajweed"),
    "dayparts": DAYPARTS,
}
