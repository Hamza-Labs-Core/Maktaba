"""Display titles (Story 27.11): the rules that make a short, clean on-air title."""

from __future__ import annotations

import json

import pytest

from maktaba_pipeline.channels.display import merge_snapshot
from maktaba_pipeline.classify.display_title import (
    Cleaned,
    DisplayTitleConfig,
    brand_phrases,
    clean_series_name,
    clean_title,
    episode_label,
    llm_input_hash,
    llm_prompt,
    needs_llm,
    on_air_line,
    parse_llm,
    title_boilerplate,
)

CFG = DisplayTitleConfig()
BRANDS = brand_phrases(["الشيخ الدكتور عثمان الخميس", "Mishary Rashid Alafasy"], ["من زاوية أخرى"])


@pytest.mark.unit
@pytest.mark.parametrize(
    ("raw", "title", "episode"),
    [
        # the real examples
        (
            "في الخنادق… لا يوجد ملحدون! ｜ برنامج من زاوية أخرى ｜ الحلقة 45",
            "في الخنادق… لا يوجد ملحدون!",
            45,
        ),
        ("كيف نخاطب الجيل الجديد؟ - 1438⧸10⧸12", "كيف نخاطب الجيل الجديد؟", None),
        (
            "الإيمان سؤال وجواب ｜ حلقة 14： ثمرات عبادة الله ومعيار التقدم …",
            "الإيمان سؤال وجواب - ثمرات عبادة الله ومعيار التقدم",
            14,
        ),
        # channel / presenter names, honorifics, hashtags, emojis, links
        ("الشيخ عثمان الخميس | حكم صيام الست من شوال؟؟", "حكم صيام الست من شوال؟", None),
        ("قصة يوسف 🌙✨ #قصص_الأنبياء https://youtu.be/x", "قصة يوسف", None),
        ("الحلقة الأولى - بداية القصة.mp4", "بداية القصة", 1),
        ("سورة الكهف (1080p) [HD] حصريا", "سورة الكهف", None),
        ("الحلقه ٣٢ ｜ الصبر عند البلاء", "الصبر عند البلاء", 32),
        ("محاضرة الصبر 2019-05-01", "محاضرة الصبر", None),
        # presenter credits, numbered lessons, tatweel separators (library titles)
        ("583- هل تجب الزكاة في المال؟ - عثمان الخميس", "هل تجب الزكاة في المال؟", 583),
        ("#20 خلاصة التفسير | تفسير سورة آل عمران", "خلاصة التفسير - تفسير سورة آل عمران", 20),
        ("من معجزات نبي الله داود | د سيد عطوة", "من معجزات نبي الله داود", None),
        ("ما حكم صبغ الشعر بالسواد؟ د. محمد حسن عبد الغفار", "ما حكم صبغ الشعر بالسواد؟", None),
        ("قصة الشيخ والبحر", "قصة الشيخ والبحر", None),
        ("تفسير سورة الحجرات ـ الجزء 5 ـ الشيخ سعيد الكملي", "تفسير سورة الحجرات - الجزء 5", None),
        # Latin
        (
            "Surah Al-Kahf (Full) | Mishary Rashid Alafasy [HD] 1080p 🔥🔥",
            "Surah Al-Kahf",
            None,
        ),
        ("Official Video - The Story of Yusuf - Episode 3!!!", "The Story of Yusuf", 3),
        ("Ep. 12: Patience in hard times", "Patience in hard times", 12),
    ],
)
def test_clean_title(raw: str, title: str, episode: int | None) -> None:
    got = clean_title(raw, CFG, brands=BRANDS)
    assert got.title == title
    assert got.episode == episode


@pytest.mark.unit
def test_series_name_is_dropped_when_it_is_the_series() -> None:
    brands = brand_phrases([], ["الإيمان سؤال وجواب"])
    got = clean_title("الإيمان سؤال وجواب ｜ حلقة 14： ثمرات عبادة الله", CFG, brands=brands)
    assert got == Cleaned("ثمرات عبادة الله", 14, 1)
    # only the series and the episode: nothing left; the strip says «series · الحلقة n»
    assert clean_title("من زاوية أخرى - الحلقة 7", CFG, brands=BRANDS).title == ""


@pytest.mark.unit
def test_boilerplate_shared_by_most_titles_is_dropped() -> None:
    titles = [f"عنوان رقم {i} | برنامج في رحاب الإيمان | قناة المجد" for i in range(8)] + [
        "حلقة خاصة"
    ]
    boiler = title_boilerplate(titles, CFG)
    assert "برنامج في رحاب الايمان" in boiler and "قناه المجد" in boiler
    assert clean_title(titles[3], CFG, boilerplate=boiler).title == "عنوان رقم 3"
    assert title_boilerplate(titles[:3], CFG) == frozenset()  # too few titles to say


@pytest.mark.unit
def test_max_length_cuts_at_a_word() -> None:
    cfg = DisplayTitleConfig(max_length=20)
    got = clean_title("كلمة طويلة جدا وكلمات كثيرة بعدها في العنوان", cfg)
    assert len(got.title) <= 20 and got.title.endswith("…")
    assert " " not in got.title[-2:]


@pytest.mark.unit
def test_settings_patterns_and_words() -> None:
    cfg = DisplayTitleConfig(strip_patterns=(r"\bLIVE\b", "[("), strip_words=("مباشر",))
    # a bad pattern is skipped, the good one applies
    assert clean_title("LIVE درس مباشر الفجر", cfg).title == "درس الفجر"


@pytest.mark.unit
def test_series_name_and_labels() -> None:
    assert clean_series_name("برنامج من زاوية أخرى ｜ قناة الشيخ عثمان الخميس", brands=BRANDS) == (
        "برنامج من زاوية أخرى"
    )
    assert episode_label(45, True) == "الحلقة 45"
    assert episode_label(3, False) == "Ep 3"
    assert episode_label(3, True, DisplayTitleConfig(keep_episode_number=False)) == ""
    assert episode_label(3, True, DisplayTitleConfig(episode_label_ar="ح{n")) == "ح{n 3"
    assert on_air_line("في الخنادق", "من زاوية أخرى", "الحلقة 45") == (
        "من زاوية أخرى · الحلقة 45 — في الخنادق"
    )
    assert on_air_line("", "من زاوية أخرى", "الحلقة 45") == "من زاوية أخرى · الحلقة 45"
    assert on_air_line("قصة", "", "") == "قصة"


@pytest.mark.unit
def test_merge_snapshot_guide_and_strip() -> None:
    snap = {"title": "raw ｜ title", "duration_ms": 1000}
    info = {"title": "في الخنادق", "series": "من زاوية أخرى", "episode": 45}
    got = merge_snapshot(snap, info, CFG)
    assert got["title"] == "في الخنادق" and got["source_title"] == "raw ｜ title"
    assert got["series"] == "من زاوية أخرى" and got["episode"] == 45
    assert got["on_air"] == "من زاوية أخرى · الحلقة 45 — في الخنادق"
    assert got["strip_episode"] == "الحلقة 45" and got["duration_ms"] == 1000
    # merged twice: the source title stays the library's
    assert merge_snapshot(got, info, CFG)["source_title"] == "raw ｜ title"
    off = merge_snapshot(snap, info, DisplayTitleConfig(use_in_guide=False))
    assert off["title"] == "raw ｜ title" and off["on_air"].endswith("في الخنادق")
    assert merge_snapshot({"title": "x"}, None, CFG) == {"title": "x"}


@pytest.mark.unit
def test_llm_prompt_parse_and_hash() -> None:
    raw = "Surah Al-Kahf - Mishary Alafasy - very long text " * 3
    c = clean_title(raw, CFG)
    assert needs_llm(raw, c, CFG)
    assert not needs_llm("قصة يوسف", clean_title("قصة يوسف", CFG), CFG)
    prompt = llm_prompt([("t1", "s1"), ("t2", "")], CFG)
    data = json.loads(prompt.splitlines()[-1])
    assert [r["n"] for r in data["titles"]] == [1, 2]
    answer = json.dumps(
        {
            "titles": [
                {"n": 1, "title": "سورة الكهف 🔥"},
                {"n": 2, "title": ""},
                {"n": 9, "title": "x"},
            ]
        }
    )
    assert parse_llm(answer, 2, CFG) == {1: "سورة الكهف"}
    assert parse_llm("not json", 2, CFG) == {}
    h = llm_input_hash("t", "s", "m", CFG)
    assert h == llm_input_hash("t", "s", "m", CFG) != llm_input_hash("t", "s", "m2", CFG)
