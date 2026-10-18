"""Transcripts as tagging evidence: what was said, read more strictly than a title."""

from __future__ import annotations

import dataclasses

from maktaba_pipeline.classify.topic_tags import (
    Tag,
    TaggerConfig,
    looks_recited,
    sample_evenly,
    tag_series,
    tag_video,
    transcript_hits,
)

GENERIC_TITLES = [f"الحلقة {i}" for i in range(1, 11)]

SEERAH_TALK = (
    "يعني يا إخواني نكمل اليوم في السيرة النبوية، وصلنا إلى وفاة عبد المطلب وكفالة أبو طالب، "
    "ثم زواجه من خديجة، وبعدها البعثة ونزول الوحي في غار حراء، ثم الهجرة النبوية. "
)
TAFSIR_TALK = (
    "طيب انتبهوا، قال الله تعالى يا أيها الذين آمنوا اتقوا الله، يعني التقوى هنا معناها "
    "أن تجعل بينك وبين عذاب الله وقاية، وهذا كلام المفسرين في هذه الآية. "
)
KHUTBAH = (
    "إن الحمد لله نحمده ونستعينه ونستغفره ونعوذ بالله من شرور أنفسنا ومن سيئات أعمالنا، "
    "أوصيكم ونفسي بتقوى الله، قال تعالى يا أيها الذين آمنوا اتقوا الله حق تقاته. "
)
# a short sura, recited over and over: Quran text and nothing else
RECITED = "قل هو الله أحد الله الصمد لم يلد ولم يولد ولم يكن له كفوا أحد "


def _tags(tags: list[Tag]) -> dict[str, Tag]:
    return {t.tag: t for t in tags}


def _series(transcripts: list[str], **kw: object) -> dict[str, Tag]:
    return _tags(
        tag_series(
            "سلسلة دروس",
            episode_titles=GENERIC_TITLES,
            median_duration_s=2400.0,
            transcripts=transcripts,
            **kw,  # type: ignore[arg-type]
        )
    )


def test_generic_titles_get_their_topic_from_what_was_said() -> None:
    without = _series([])
    assert "topic:seerah" not in without
    with_speech = _series([SEERAH_TALK * 3] * 10)
    seerah = with_speech["topic:seerah"]
    assert seerah.score >= 0.5
    assert "transcript 10/10:" in seerah.evidence
    assert with_speech["genre:seerah"].score == seerah.score


def test_missing_transcripts_change_nothing() -> None:
    base = tag_series("سلسلة دروس", episode_titles=GENERIC_TITLES, median_duration_s=2400.0)
    assert _series([]) == _tags(base)
    assert _series(["", "   "]) == _tags(base)
    title = "قصة نوح عليه السلام"
    assert tag_video(title, duration_s=1800.0) == tag_video(title, duration_s=1800.0, transcript="")


def test_one_stray_mention_is_weak_evidence() -> None:
    once = "تكلمنا في الدرس الماضي عن غزوة بدر، " + "واليوم نتكلم عن الصبر عند البلاء. " * 3
    assert "topic:seerah" not in _series([once] * 10)


def test_formulas_every_talk_says_are_ignored() -> None:
    formulas = (
        "بسم الله الرحمن الرحيم الحمد لله والصلاة والسلام على رسول الله صلى الله عليه وسلم، "
        "رضي الله عنه، عليه السلام، أما بعد. "
    ) * 5
    hits = transcript_hits(formulas)
    assert "topic:dua_dhikr" not in hits
    assert "topic:sahaba" not in hits
    assert "topic:prophets_stories" not in hits


def test_transcripts_give_topics_only_by_default() -> None:
    hits = transcript_hits("في هذه الحلقة من البرنامج نتكلم عن خطبة الجمعة ورمضان. " * 4)
    assert all(t.startswith("topic:") for t in hits)


def test_a_khutbah_is_never_a_recitation() -> None:
    assert not looks_recited(KHUTBAH * 4)
    generic = _series([KHUTBAH * 4] * 6)
    assert "format:recitation" not in generic and "topic:quran_recitation" not in generic
    named = _tags(
        tag_series(
            "خطب الجمعة",
            episode_titles=[f"خطبة الجمعة {i}" for i in range(1, 7)],
            transcripts=[RECITED * 8] * 6,  # even a khutbah that is mostly recited verses
        )
    )
    assert "format:khutbah" in named
    assert "format:recitation" not in named and "topic:quran_recitation" not in named


def test_continuous_quran_is_a_recitation_but_a_lecture_quoting_it_is_not() -> None:
    assert looks_recited(RECITED * 8)
    assert not looks_recited(TAFSIR_TALK * 4)
    assert not looks_recited(RECITED)  # too short to judge
    recited = _series([RECITED * 8] * 6)
    assert "format:recitation" in recited and "topic:quran_recitation" in recited
    assert "transcript recited 6/6" in recited["format:recitation"].evidence
    lecture = _series([TAFSIR_TALK * 4] * 6)
    assert "format:recitation" not in lecture


def test_settings_drive_the_transcript_evidence() -> None:
    off = TaggerConfig(w_transcript=0.0, w_transcript_recitation=0.0)
    assert "topic:seerah" not in _series([SEERAH_TALK * 3] * 10, config=off)
    assert "format:recitation" not in _series([RECITED * 8] * 6, config=off)
    strict = TaggerConfig(recitation_max_commentary=0.0, recitation_min_quranic_share=0.9)
    assert not looks_recited(RECITED * 8, strict)
    # the character cap: nothing past it is read
    late = "كلام عام عن الحياة اليومية والعمل. " * 40 + SEERAH_TALK * 3
    assert "topic:seerah" in transcript_hits(late)
    assert "topic:seerah" not in transcript_hits(late, TaggerConfig(transcript_max_chars=400))
    narrower = dataclasses.replace(TaggerConfig(), transcript_namespaces=())
    assert transcript_hits(SEERAH_TALK * 3, narrower) == {}


def test_a_big_season_is_sampled() -> None:
    assert sample_evenly(list(range(10)), 4) == [0, 3, 6, 9]
    assert sample_evenly([1, 2], 5) == [1, 2]
    assert sample_evenly([1, 2, 3], 1) == [1]
    assert sample_evenly([1, 2, 3], 0) == []
    many = [SEERAH_TALK * 3] * 40
    tags = _series(many, config=TaggerConfig(transcript_max_episodes=5))
    assert "transcript 5/5:" in tags["topic:seerah"].evidence


def test_a_loose_video_reads_its_transcript() -> None:
    plain = _tags(tag_video("مقطع 12", duration_s=1800.0))
    assert "topic:seerah" not in plain
    spoken = _tags(tag_video("مقطع 12", duration_s=1800.0, transcript=SEERAH_TALK * 3))
    assert "topic:seerah" in spoken
    assert "transcript 1/1:" in spoken["topic:seerah"].evidence
