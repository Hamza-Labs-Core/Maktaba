"""The channel audit: what airs on a channel, judged against the channel's theme."""

from __future__ import annotations

from maktaba_pipeline.schedule.channel_planner import PlannerConfig, ThemeSpec
from scripts.mediaintel.audit_channels import NAME_RAMADAN, airtime, evidence_strength, judge

RAMADAN = ThemeSpec("ramadan", "Ramadan", "رمضان", 115, ("fasting",), occasions=("ramadan",))
KIDS = ThemeSpec("kids", "Kids", "الأطفال", 110, ("seerah",), audiences=("kids_friendly",),
                 exclude=("audience:adults",))  # fmt: skip
CFG = PlannerConfig()


def test_thin_evidence_is_weak() -> None:
    assert evidence_strength("name:رمضان; playlist:رمضان") == "strong"
    assert evidence_strength("eps 24/27:رمضان") == "strong"
    assert evidence_strength("aired in Ramadan 5/5") == "weak"  # aired then, not about it
    assert evidence_strength("playlist:رمضان") == "weak"  # a playlist holding other shows
    assert evidence_strength("eps 3/97:صوم") == "weak"  # a few titles of many


def test_a_sheikh_named_ramadan_is_not_ramadan() -> None:
    assert NAME_RAMADAN.search("بغض المسكرات ｜ كيف رباهم ｜ الشيخ رمضان الشنوري ｜ ح 28")
    assert NAME_RAMADAN.search("خالد الجندي ورمضان عبد المعز يخالفون")
    assert not NAME_RAMADAN.search("مقاصد رمضان ｜ محاضرة منهجية")
    v, why = judge(RAMADAN, CFG, "كيف رباهم ｜ الشيخ رمضان الشنوري", "قناة الندى", {},
                   {"occasion:ramadan": 0.9})  # fmt: skip
    assert v == "weak" and "name" in why


def test_verdicts() -> None:
    assert judge(RAMADAN, CFG, "x", "lib", {"genre:fasting": (1.0, "")}, {})[0] == "strong"
    assert judge(RAMADAN, CFG, "x", "lib", {"occasion:ramadan": (0.9, "aired in Ramadan 5/5")},
                 {})[0] == "weak"  # fmt: skip
    assert judge(RAMADAN, CFG, "x", "lib", {}, {"topic:fasting": 0.8})[0] == "strong"
    assert judge(RAMADAN, CFG, "x", "lib", {"topic:hadith": (0.9, "name:x")}, {}) == (
        "none",
        "about topic:hadith",
    )
    assert judge(KIDS, CFG, "x", "lib", {"audience:kids_friendly": (0.8, "")}, {})[0] == "audience"


def test_airtime_counts_what_airs_from_now() -> None:
    ch = {"startTime": "2027-01-15T08:00:00Z",
          "programs": [{"duration": 3_600_000}, {"duration": 3_600_000}]}  # fmt: skip
    now = 1_800_000_000_000 + 1_800_000  # 08:30
    got = airtime(ch, now, days=1)
    assert round(got[0] + got[1]) == 24 * 60
    assert round(got[0]) == 12 * 60  # 08:30-09:00, eleven whole hours, then 08:00-08:30
