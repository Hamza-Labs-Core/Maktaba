"""A language model's verdict corrects the keyword tags (no model is called here)."""

from __future__ import annotations

import json

from maktaba_pipeline.classify import tag_check as tc
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


def test_the_model_never_damps_twice_nor_against_the_book() -> None:
    cfg = TagCheckConfig()
    v = Verdict(("tafsir",), ())
    tags = {"topic:hadith": (0.9, "book «بلوغ المرام»"), "topic:fiqh": (0.8, "kw")}
    once = apply(tags, v, cfg)
    assert once["topic:hadith"][0] == 0.9  # the book it explains says so
    assert apply(once, v, cfg)["topic:fiqh"] == once["topic:fiqh"]


# ---------------------------------------------------------------- clips


def test_clip_candidates_are_its_keyword_topics() -> None:
    tags = {"topic:prophets_stories": 0.95, "topic:fiqh": 0.78, "topic:seerah": 0.36,
            "format:clips": 0.9}  # fmt: skip
    assert tc.clip_candidates(tags) == ("fiqh", "prophets_stories")


def test_parse_clips_keeps_only_each_clips_candidates() -> None:
    answer = json.dumps({"clips": [{"n": 1, "keep": ["fiqh", "seerah"]}, {"n": 2, "keep": []},
                                   {"n": 9, "keep": ["fiqh"]}]})  # fmt: skip
    got = tc.parse_clips(answer, [("fiqh", "prophets_stories"), ("prophets_stories",)])
    assert got == {1: ("fiqh",), 2: ()}  # seerah was no candidate; n 9 is no clip
    assert tc.parse_clips("not json", [("fiqh",)]) == {}


def test_apply_clip_damps_what_the_model_does_not_keep() -> None:
    cfg = tc.TagCheckConfig()
    tags = {"topic:prophets_stories": 0.95, "genre:prophets_stories": 0.95, "topic:fiqh": 0.78,
            "format:clips": 0.9, "topic:seerah": 0.36}  # fmt: skip
    out = tc.apply_clip(tags, ("fiqh",), cfg)
    assert out["topic:prophets_stories"] == round(0.95 * cfg.damp, 3)
    assert out["topic:fiqh"] == 0.78 and out["format:clips"] == 0.9
    assert out["topic:seerah"] == 0.36  # not asked about: as it was
    assert "genre:prophets_stories" not in out and out["genre:fiqh"] == 0.78


def test_the_clip_prompt_lists_every_clip_with_its_candidates() -> None:
    p = tc.clip_prompt([("lib", "عنوان أ", ("fiqh",)), ("lib", "عنوان ب", ("prophets_stories",))],
                       tc.TagCheckConfig())  # fmt: skip
    assert "1. [lib] عنوان أ" in p and "2. [lib] عنوان ب" in p and "prophets_stories (" in p
