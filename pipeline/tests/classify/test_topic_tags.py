"""Topic tagging on the shapes real Islamic YouTube archives have.

Cases come from the libraries this was built for: a presenter's Ramadan
programs (Nabil Al-Awadi), reciters' mushaf uploads, khutbah, nasheed and
fatwa channels — including the traps: boilerplate under every video,
people named after prophets/Companions, a surah's name that is also a topic
word, a reciter who also sings nasheeds.
"""

from __future__ import annotations

from datetime import date, timedelta

import pytest

from maktaba_pipeline.classify.topic_tags import (
    SUBTOPICS,
    TOPICS,
    Tag,
    boilerplate_phrases,
    branding,
    content_type,
    match_text,
    primary_topic,
    tag_series,
    tag_video,
)

pytestmark = pytest.mark.unit

NABIL = ["الشيخ نبيل العوضي"]


def _tags(tags: list[Tag]) -> dict[str, float]:
    return {t.tag: t.score for t in tags}


def _genre(tags: list[Tag]) -> str:
    return next(t.tag for t in tags if t.tag.startswith("genre:"))


def _daily(start: date, n: int) -> list[date]:
    return [start + timedelta(days=i) for i in range(n)]


# --- series -----------------------------------------------------------------


def test_prophets_stories_program_in_ramadan() -> None:
    eps = [
        "نبيل العوضي | قصة بداية الخلق وكيف خلق الله العالم وخلق آدم عليه السلام",
        "نبيل العوضي | قصة هابيل و قابيل و أول جريمة قتل في التاريخ",
        "نبيل العوضي | قصة نوح عليه السلام و صناعة الفلك",
        "نبيل العوضي | قصة يونس عليه السلام و ماذا حدث في بطن الحوت",
        "نبيل العوضي | قصة كليم الله موسى عليه السلام | الجزء الاول",
    ] * 6
    tags = tag_series(
        "قصص الأنبياء",
        playlist_titles=["قصص الأنبياء رمضان 2024"],
        episode_titles=eps,
        channel=NABIL,
        median_duration_s=1265,
        air_dates=_daily(date(2024, 3, 11), 30),
    )
    t = _tags(tags)
    assert _genre(tags) == "genre:prophets_stories"
    assert t["topic:prophets_stories"] > 0.9
    assert t["format:program"] > 0.5
    assert t["occasion:ramadan"] > 0.9
    assert "topic:akhlaq" not in t  # "بداية الخلق" is creation, not character
    assert t["audience:general"] == 0.5


def test_seerah_from_series_name_and_episode_vocabulary() -> None:
    tags = tag_series(
        "السيرة النبوية",
        playlist_titles=["برنامج السيرة النبوية نبيل العوضي HD"],
        episode_titles=[
            "السيرة النبوية الحلقة 1 الشيخ نبيل العوضي ( حال العرب في الجاهلية ) HD",
            "السيرة النبوية الحلقة 2 الشيخ نبيل العوضي ( عاام الفيل ) HD",
            "السيرة النبوية الحلقة 5 ( نزول الوحي ) HD",
        ],
        channel=NABIL,
    )
    assert _genre(tags) == "genre:seerah"
    assert _tags(tags)["topic:seerah"] > 0.95


def test_al_habib_is_seerah_from_its_episode_titles() -> None:
    """The name says only "the beloved"; the episodes say Makkah, Jahiliyyah, the Kaaba."""
    eps = [
        "نبيل العوضي | الحبيب | الجاهلية الاولى",
        "نبيل العوضي | الحبيب | للبيت رب يحميه",
        "نبيل العوضي | الحبيب | يتيم مكة",
        "نبيل العوضي | الحبيب | هدم الكعبة",
        "نبيل العوضي | الحبيب | البيعتان",
    ]
    tags = tag_series("الحبيب", playlist_titles=["#سلسلة_الحبيب | نبيل العوضي | في #رمضان"],
                      episode_titles=eps, channel=NABIL)  # fmt: skip
    assert _genre(tags) == "genre:seerah"


def test_qissa_wa_aya_is_tafsir_and_stories() -> None:
    tags = tag_series(
        "قصة وآية",
        playlist_titles=["برنامج #قصة_وآية"],
        episode_titles=["قصة وآية ( 1 ) الشيخ نبيل العوضي الروم والغناء والظلم الكبير"] * 5,
        channel=NABIL,
    )
    t = _tags(tags)
    assert _genre(tags) == "genre:tafsir"
    assert t["topic:stories"] > 0.5
    assert "topic:quran_recitation" not in t


def test_ya_allah_is_aqeedah() -> None:
    eps = [
        "يا الله  الشيخ نبيل العوضي الحلقة 1- الله أهل الثناء والمجد - حلقة إيمانية مؤثرة",
        "الحلقة 2 برنامج #يا_الله الشيخ نبيل العوضي  الرحمن الرحيم الملك ..",
        "القدوس العزيز السلام .. الحلقة 3 برنامج #يا_الله الشيخ نبيل العوضي",
    ]
    tags = tag_series("يا الله", episode_titles=eps, channel=NABIL)
    t = _tags(tags)
    assert _genre(tags) == "genre:aqeedah"
    assert t.get("topic:raqaiq", 0) > 0.3  # "حلقة إيمانية"


def test_women_violations_is_women_and_fiqh_for_women() -> None:
    tags = tag_series(
        "مخالفات نسائية رمضان",
        playlist_titles=["نبيل العوضي مخالفات نسائية رمضان"],
        episode_titles=[f"مخالفات نسائية في رمضان - الشيخ نبيل العوضي ج{i}" for i in range(4)],
        channel=NABIL,
    )
    t = _tags(tags)
    assert t["topic:women"] > 0.9 and t["topic:fiqh"] > 0.9
    assert t["audience:women"] > 0.9
    assert "audience:general" not in t
    assert t["occasion:ramadan"] > 0.9


def test_reciter_channel_surah_uploads_are_recitation() -> None:
    eps = ["سورة البقرة - سعد الغامدي", "سورة آل عمران - سعد الغامدي", "سورة يس - سعد الغامدي"]
    tags = tag_series("سورة البقرة", episode_titles=eps, channel=["Sa'ad Al Ghamdi"])
    t = _tags(tags)
    assert _genre(tags) == "genre:quran_recitation"
    assert t["format:recitation"] > 0.9
    assert "topic:tafsir" not in t or t["topic:tafsir"] < t["topic:quran_recitation"]


def test_mushaf_rip_with_latin_surah_titles() -> None:
    tags = tag_video("Al-Waqi'a： 56", channel=["Mahmoud Khalil Al Hussary"])
    assert _genre(tags) == "genre:quran_recitation"
    assert "format:recitation" in _tags(tags)
    assert _genre(tag_video("104. Al Humaza： Der Stichler")) == "genre:quran_recitation"
    # a Latin title with a number is not automatically a surah
    assert "topic:quran_recitation" not in _tags(tag_video("Episode 5"))


def test_tafsir_of_a_surah_is_not_a_recitation() -> None:
    tags = tag_video("#16 تفسير سورة النساء ｜ [الآيات 84 - 90] ｜ خلاصة التفسير")
    t = _tags(tags)
    assert _genre(tags) == "genre:tafsir"
    assert "format:recitation" not in t
    # the surah's name is not a topic: سورة النساء is not about women
    assert "topic:women" not in t and "audience:women" not in t


def test_khutbah_on_friday() -> None:
    tags = tag_video("خطبة الجمعة | فضل العشر من ذي الحجة", duration_s=1800)
    t = _tags(tags)
    assert t["format:khutbah"] > 0.9
    assert t["occasion:jumuah"] > 0.9
    assert t["occasion:hajj"] > 0.9
    assert content_type(tags) == "sermon"


def test_nasheed_series() -> None:
    tags = tag_series(
        "أناشيد الحج",
        playlist_titles=["اناشيد الحج"],
        episode_titles=["نشيد لبيك اللهم لبيك بدون موسيقى", "أنشودة يا حجاج بيت الله"],
    )
    t = _tags(tags)
    assert _genre(tags) == "genre:nasheed"
    assert t["occasion:hajj"] > 0.5
    assert content_type(tags) == "music_video"


def test_reciter_singing_a_nasheed_is_a_nasheed() -> None:
    tags = tag_video("نشيدة عمر الفاروق - مشاري العفاسي")
    t = _tags(tags)
    assert _genre(tags) == "genre:nasheed"
    assert "topic:quran_recitation" not in t and "format:recitation" not in t


def test_kids_audience() -> None:
    tags = tag_series("قصص الأنبياء للأطفال", episode_titles=["قصة نوح عليه السلام للأطفال"])
    t = _tags(tags)
    assert t["audience:kids"] > 0.9
    assert _genre(tags) == "genre:prophets_stories"


def test_fatwa_and_qa_format() -> None:
    tags = tag_video("ما حكم صيام الست من شوال قبل القضاء؟", duration_s=240)
    t = _tags(tags)
    assert t["format:qa_fatwa"] > 0.9
    assert _genre(tags) == "genre:fiqh"


def test_questions_of_the_grave_are_not_a_qa_session() -> None:
    tags = tag_series("الأسئلة الأخيرة", episode_titles=["الأسئلة الأخيرة - حاسب نفسك ج1"])
    assert "format:qa_fatwa" not in _tags(tags)
    assert _genre(tags) == "genre:raqaiq"


def test_clip_series_are_clips_not_programs() -> None:
    tags = tag_series(
        "مقاطع مميزة من يا الله",
        playlist_titles=["مقاطع مميزة من برنامج يا الله"],
        episode_titles=["الخلوة مع الله .. مقطع رائع من برينامج #يا_الله الشيخ نبيل العوضي"] * 5,
        kind="clips",
        median_duration_s=240,
        air_dates=_daily(date(2014, 6, 29), 12),
        channel=NABIL,
    )
    t = _tags(tags)
    assert t["format:clips"] > 0.9
    assert "format:program" not in t
    assert _genre(tags) == "genre:aqeedah"


def test_ramadan_from_air_dates_alone() -> None:
    eps = [f"برنامج مكارم الحلقة ({i}) كلامك ميزانك" for i in range(30)]
    in_ramadan = tag_series(
        "برنامج مكارم", episode_titles=eps, air_dates=_daily(date(2016, 6, 6), 30)
    )
    outside = tag_series("برنامج مكارم", episode_titles=eps, air_dates=_daily(date(2016, 9, 1), 30))
    assert "occasion:ramadan" in _tags(in_ramadan)
    assert "occasion:ramadan" not in _tags(outside)
    assert _genre(in_ramadan) == "genre:akhlaq"


# --- the traps ---------------------------------------------------------------


def test_channel_boilerplate_in_descriptions_is_ignored() -> None:
    footer = (
        "لا تنسي الإشتراك في قناة الشيخ نبيل العوضي ليصلك كل جديد\n"
        "#الشيخ_نبيل_العوضي #السيرة_النبوية_نبيل_العوضي #nabil_al_awadi"
    )
    channel_descs = [footer] * 40 + ["عن التوبة"] * 5
    noise = boilerplate_phrases(channel_descs)
    assert "السيره النبويه" in noise
    tags = tag_series(
        "التوبة",
        episode_titles=["التوبة - الشيخ نبيل العوضي ج1", "التوبة - الشيخ نبيل العوضي ج2"],
        descriptions=[footer, footer],
        channel=NABIL,
        noise=noise,
    )
    assert "topic:seerah" not in _tags(tags)
    assert _genre(tags) == "genre:raqaiq"


def test_only_the_description_opening_counts() -> None:
    """Pasted blocks after the first link (another video's blurb) say nothing."""
    desc = (
        "الليالي المباركات..عشرة أيام لا تُنسى!\n\nhttps://example.com/join\n"
        "ما حكم عيد الحب | نبيل العوضي\n#قصص_الأنبياء"
    )
    tags = tag_video("الليالي المباركات | الليلة الأولى", desc)
    t = _tags(tags)
    assert "topic:fiqh" not in t and "topic:prophets_stories" not in t
    assert "occasion:eid" not in t


def test_person_names_are_not_topics() -> None:
    # Ottoman sultan, not the Companion; a contestant named Shu'ayb, not the prophet
    assert "topic:sahaba" not in _tags(tag_video("عائلة عثمان بن أرطغرل تاريخيًا"))
    assert "topic:prophets_stories" not in _tags(tag_video("المتسابق محمد فاروق شعيب من لبنان"))
    # عبد الرحمن is a man, not the divine name
    assert "topic:aqeedah" not in _tags(tag_video("لقاء مع د. عبد الرحمن السميط"))
    # but the prophet in a story is
    assert "topic:prophets_stories" in _tags(tag_video("قصة شعيب عليه السلام"))


def test_presenter_name_in_titles_is_branding() -> None:
    channel = ["Dr. Othman Alkamees - الشيخ الدكتور عثمان الخميس"]
    assert "عثمان الخميس" in branding(channel)
    tags = tag_video("اطمئنان القلب وراحته - عثمان الخميس", channel=channel)
    assert "topic:sahaba" not in _tags(tags)


def test_modern_times_is_not_hadith() -> None:
    assert "topic:hadith" not in _tags(tag_video("التجارب الإصلاحية في العصر الحديث 12"))
    assert "topic:hadith" in _tags(tag_video("شرح حديث جبريل"))


def test_bani_israel_is_prophets_not_current_affairs() -> None:
    tags = tag_series(
        "يا بني اسرائيل",
        episode_titles=["محاولة قتل يوسف ! الشيخ نبيل العوضي بني إسرائيل الحلقة (3)"] * 3,
        channel=NABIL,
    )
    t = _tags(tags)
    assert _genre(tags) == "genre:prophets_stories"
    assert "topic:current_affairs" not in t


def test_proclitics_are_tolerated() -> None:
    assert "topic:seerah" in match_text("دروس في السيرة والشمائل")
    assert "audience:kids" in match_text("قصص للأطفال")
    assert "topic:tafsir" in match_text("وتفسير القرآن")


# --- contract ---------------------------------------------------------------


def test_scores_evidence_and_one_genre() -> None:
    tags = tag_series(
        "قصص الأنبياء",
        playlist_titles=["قصص الأنبياء رمضان 2024"],
        episode_titles=["قصة نوح عليه السلام"],
    )
    assert all(0.0 <= t.score <= 1.0 and t.evidence for t in tags)
    assert sum(t.tag.startswith("genre:") for t in tags) == 1
    top = primary_topic(tags)
    assert top is not None and top.tag == "topic:prophets_stories"
    assert all(t.tag.split(":")[0] in {"topic", "format", "audience", "occasion", "genre"}
               for t in tags)  # fmt: skip
    assert all(t.tag[6:] in TOPICS + SUBTOPICS for t in tags if t.tag.startswith("topic:"))
    assert tag_series("قصص الأنبياء", episode_titles=["قصة نوح عليه السلام"]) == tag_series(
        "قصص الأنبياء", episode_titles=["قصة نوح عليه السلام"]
    )


def test_no_evidence_gives_general_genre() -> None:
    tags = tag_series("مشاهد", episode_titles=["مشاهد3 / الحلقة الخامسة / الشيخ نبيل العوضي"])
    assert _genre(tags) == "genre:general"
    assert primary_topic(tags) is None
    assert "audience:general" in _tags(tags)


def test_shorts_are_clips() -> None:
    tags = tag_video("أتعرف أحلى لحظات العمر؟ #shorts", duration_s=50, is_short=True)
    assert _tags(tags)["format:clips"] > 0.9


def test_long_untitled_video_defaults_to_lecture() -> None:
    tags = tag_video("هدية السماء إلى الأرض", duration_s=2500)
    assert "format:lecture" in _tags(tags)
    assert content_type(tags) == "lecture"
