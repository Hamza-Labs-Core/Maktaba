"""Title-structure extraction on real YouTube series titles."""

from __future__ import annotations

import pytest

from maktaba_pipeline.classify.title_template import (
    episode_number,
    is_teaser,
    season_number,
    segments,
    strip_markers,
    year_marker,
)

pytestmark = pytest.mark.unit


@pytest.mark.parametrize(
    ("title", "expected"),
    [
        ("رحمة الأنبياء مع أهلهم  #الراحمون حلقة 25 الشيخ نبيل العوضي", 25),
        ("نبيل العوضي ｜ قصة موسى عليه السلام الجزء التاسع", 9),
        ("نبيل العوضي ｜ قصة موسى عليه السلام الجزء الثالث عشر", 13),
        ("نبيل العوضي ｜ قصة مملكة سليمان عليه السلام  (الجزء الأول)", 1),
        ("الحبيب ｜ برومو الحلقة التاسعة والعشرون", 29),
        ("الحبيب | بل الرفيق الأعلى | الحلقة الأخيرة", 9999),
        ("الشرح المطول على نزهة النظر ١٦ ｜ أحمد السيد", None),
        ("اتاخر خدني جنبك (٣٣) ｜ الشمال يجوع", 33),
        ("77-تفسير سورة البقرة اية ٢٢٩ - ٢٣٠", 77),
        ("قصة رفع عيسى عليه السلام إلى السماء .. يابني إسرائيل الحلقة ( 27 )", 27),
        ("فلسطين في دقيقة (2024)", None),
        ("نبيل العوضي ｜ قصة نوح عليه السلام و الطوفان العظيم", None),
    ],
)
def test_episode_number(title: str, expected: int | None) -> None:
    assert episode_number(title) == expected


@pytest.mark.parametrize(
    ("title", "expected"),
    [
        ("قصص الأنبياء (الموسم الثاني)", 2),
        ("الحبيب الموسم الاول ｜ الحصار", 1),
        ("Show season 3", 3),
        ("قصص الأنبياء رمضان 2024", None),
    ],
)
def test_season_number(title: str, expected: int | None) -> None:
    assert season_number(title) == expected


def test_year_marker_gregorian_and_hijri() -> None:
    assert year_marker("قصص الأنبياء رمضان 2024") == 2024
    assert year_marker("مدرسة الحياة 1429هـ") in (2007, 2008, 2009)
    assert year_marker("قصة نوح") is None


def test_teasers() -> None:
    assert is_teaser("نبيل العوضي ｜ قصة شعيب عليه السلام ｜ الحلقة القادمة انتظرونا", 95)
    assert is_teaser("الحبيب ｜ برومو الحلقة التاسعة والعشرون", 60)
    assert not is_teaser("نبيل العوضي ｜ قصة شعيب عليه السلام", 1440)
    # a full-length episode may still say "انتظرونا"
    assert not is_teaser("حلقة جديدة انتظرونا", 1500)


def test_segments_and_strip() -> None:
    assert segments("الحبيب ｜ البيعتــان ｜ حصريا على #تلفاز_نت") == [
        "الحبيب",
        "البيعتان",
        "حصريا علي تلفاز نت",
    ]
    assert strip_markers("قصص الأنبياء رمضان 2024") == strip_markers("قصص الأنبياء (الموسم الثاني)")
