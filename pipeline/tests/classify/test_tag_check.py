"""A language model's verdict corrects the keyword tags (no model is called here)."""

from __future__ import annotations

from maktaba_pipeline.classify.tag_check import TagCheckConfig, Verdict, apply, parse, prompt

CFG = TagCheckConfig()


def test_the_answer_keeps_only_known_topics() -> None:
    v = parse('{"topics": ["hadith", "topic:fiqh", "cooking", "akhlaq", "seerah"],'
              ' "ramadan": false, "hajj": true}', CFG)  # fmt: skip
    assert v == Verdict(("hadith", "fiqh", "akhlaq"), ("hajj",))  # at most max_topics
    assert parse("not json", CFG) is None
    assert parse('{"topics": ["cooking"]}', CFG) is None


def test_unconfirmed_keyword_topics_are_damped_and_the_genre_follows_the_model() -> None:
    # «مجالس صحيح البخاري»: the keywords saw salah / fasting in its book titles
    tags = {"topic:hadith": (0.9, "name:البخاري"), "topic:salah": (0.8, "eps 9/97:الصلاة"),
            "topic:fasting": (0.7, "playlist:صوم"), "genre:salah": (0.8, "primary topic"),
            "occasion:ramadan": (0.86, "playlist:رمضان"),
            "format:lecture": (0.6, "default")}  # fmt: skip
    new = apply(tags, Verdict(("hadith", "fiqh"), ()), CFG)
    assert new["topic:hadith"][0] == 0.9
    assert new["topic:salah"][0] < 0.3 and "model: not about it" in new["topic:salah"][1]
    assert new["topic:fiqh"] == (0.8, "model")  # the keywords missed it
    assert "genre:hadith" in new and "genre:salah" not in new
    assert new["occasion:ramadan"][0] < 0.3  # not about Ramadan
    assert new["format:lecture"] == (0.6, "default")  # formats are not the model's call


def test_a_sub_topic_is_never_the_genre() -> None:
    tags = {"topic:fiqh": (0.7, "x"), "genre:fiqh": (0.7, "primary topic")}
    new = apply(tags, Verdict(("salah",), ()), CFG)
    assert "genre:fiqh" in new and new["topic:salah"][0] == 0.8


def test_the_prompt_names_the_series_and_samples_its_titles() -> None:
    titles = [f"الحلقة {i}" for i in range(100)]
    text = prompt("فك الوثاق", "الشيخ أبو إسحاق الحويني", titles, CFG)
    assert "فك الوثاق" in text and "الحويني" in text
    assert text.count("- الحلقة ") == CFG.sample_titles
    assert "prophets_stories" in text and "palestine" in text  # topics and sub-topics
