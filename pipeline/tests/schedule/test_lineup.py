"""The lineup: at most max_channels, each with enough content; library tags; the
kids' hour; a slot owned by one presenter."""

from __future__ import annotations

from collections.abc import Sequence

import pytest

from maktaba_pipeline.schedule.channel_planner import (
    DEFAULT_DAYPARTS,
    EpisodeRef,
    PlannerConfig,
    PlanResult,
    SeasonCandidate,
    ThemeSpec,
    suggest_channels,
)

pytestmark = pytest.mark.unit


def season(sid: str, name: str, *, genre: str = "fiqh", presenter: str = "أحمد",
           library: str = "", n: int = 20, minutes: float = 30,
           tags: dict[str, float] | None = None) -> SeasonCandidate:  # fmt: skip
    t = {f"topic:{genre}": 1.0, f"genre:{genre}": 1.0, "format:lecture": 0.9, **(tags or {})}
    folder = f"Y:/{library or presenter}"
    return SeasonCandidate(
        series_id=sid, name=name, season=1, kind="episodic", series_key=name, folder=folder,
        presenter=presenter, library=library or presenter, first_air="2010-01-01", genre=genre,
        tags=t,
        episodes=tuple(EpisodeRef(path=f"{folder}/{sid}/{i}.mp4", duration_s=minutes * 60,
                                  title=f"ح{i}", episode=i, plex_rating_key=f"{sid}{i}")
                       for i in range(1, n + 1)),
    )  # fmt: skip


def plan(seasons: Sequence[SeasonCandidate], **cfg: object) -> PlanResult:
    base: dict[str, object] = {"channel_min_days": 1.0, "channel_day_hours": 10.0}
    base.update(cfg)
    return suggest_channels(seasons, (), PlannerConfig(**base))  # type: ignore[arg-type]


THEMES = (
    ThemeSpec("fiqh", "Fiqh", "الفقه", 111, ("fiqh",)),
    ThemeSpec("seerah", "Seerah", "السيرة", 103, ("seerah",)),
    ThemeSpec("tafsir", "Tafsir", "التفسير", 105, ("tafsir",)),
)


def test_a_channel_needs_days_of_content_without_a_repeat() -> None:
    lib = [season("f1", "فقه 1", n=30), season("s1", "سيرة", genre="seerah", n=6)]
    p = plan(lib, themes=THEMES, mix_channel=False, presenter_channels=False)
    assert [c.key for c in p.channels] == ["fiqh"]  # 15 h ≥ 10 h; seerah 3 h < 10 h
    why = dict(p.unplaced)["theme seerah"]
    assert "needs 10 h" in why and "1 days at 10 h a day" in why


def test_at_most_max_channels_themes_first_in_their_order() -> None:
    lib = [season(f"{g}{i}", f"{g} {i}", genre=g, presenter=f"شيخ {i}", n=30)
           for g in ("fiqh", "seerah", "tafsir") for i in range(2)]  # fmt: skip
    p = plan(lib, themes=THEMES, max_channels=2, presenter_min_genres=1)
    assert [c.key for c in p.channels] == ["seerah", "fiqh"]  # sorted by number
    left = dict(p.unplaced)
    assert "past max_channels (2)" in left["theme tafsir"]
    assert "past max_channels (2)" in left["mix"]


def test_presenter_channels_only_for_the_largest_libraries() -> None:
    lib = [season(f"p{i}", f"برنامج {i}", presenter=f"شيخ {i}", n=10 * (i + 3))
           for i in range(4)]  # fmt: skip
    p = plan(
        lib,
        themes=(),
        mix_channel=False,
        presenter_max_channels=2,
        presenter_min_genres=1,
        presenter_min_hours=0.0,
    )
    assert [c.name for c in p.channels] == ["شيخ 3", "شيخ 2"]  # the two largest
    assert "past presenter_max_channels (2)" in dict(p.unplaced)["presenter شيخ 1"]


def test_library_tags_make_a_kids_channel_and_adults_stay_off_it() -> None:
    kids = ThemeSpec("kids", "Kids", "الأطفال", 110, ("seerah",),
                     audiences=("kids", "kids_friendly"), exclude=("audience:adults",))  # fmt: skip
    lib = [
        season("h1", "تاريخنا", genre="islamic_history", library="تاريخنا", n=30),
        season("s1", "سيرة للكبار", genre="seerah", library="الشريف", n=30),
        season("s2", "سيرة", genre="seerah", library="إلهامي", n=30),
    ]
    p = plan(lib, themes=(kids,), mix_channel=False, presenter_channels=False,
             library_tags={"تاريخنا": ("audience:kids_friendly",),
                           "الشريف": ("audience:adults",)})  # fmt: skip
    ch = p.channels[0]
    names = {u.name for k in ch.lanes for u in p.lanes[k].units}
    assert names == {"تاريخنا", "سيرة"}  # the adults' seerah stays off


def test_the_kids_hour_takes_kids_shows_and_never_adults() -> None:
    fiqh = ThemeSpec("fiqh", "Fiqh", "الفقه", 111, ("fiqh",))
    lib = [season(f"f{i}", f"فقه {i}", presenter=f"شيخ {i}", n=60, minutes=60) for i in range(6)]
    lib.append(season("k1", "قصص للأطفال", tags={"topic:fiqh": 1.0, "audience:kids": 1.0},
                      genre="stories", n=60, minutes=60))  # fmt: skip
    p = plan(lib, themes=(fiqh,), mix_channel=False, presenter_channels=False,
             dayparts=DEFAULT_DAYPARTS)  # fmt: skip
    ch = p.channels[0]
    at4 = next(s for s in ch.slots if s.start <= 16 * 60 < s.end and s.on("mon"))
    assert at4.lane is not None and p.lanes[at4.lane].shows[0] == "قصص للأطفال"


def test_a_slot_belongs_to_a_presenter_his_next_show_follows() -> None:
    fiqh = ThemeSpec("fiqh", "Fiqh", "الفقه", 111, ("fiqh",))
    # many shows of two sheikhs, more than the grid has slots: the rest queue behind. Two-hour
    # episodes keep the grid small (80 one-hour shows no longer overflowed it)
    lib = [season(f"{p}{i}", f"{p} {i}", presenter=p, n=8, minutes=120)
           for p in ("أ", "ب") for i in range(30)]  # fmt: skip
    p = plan(lib, themes=(fiqh,), mix_channel=False, presenter_channels=False)
    assert any(len(p.lanes[k].units) > 1 for k in p.channels[0].lanes)  # a queue formed
    for k in p.channels[0].lanes:
        ln = p.lanes[k]
        assert len({u.presenter for u in ln.units}) == 1, ln.name  # one sheikh per slot


def test_tajweed_lessons_are_not_recitation_by_default() -> None:
    lib = [season("t1", "دروس التجويد", genre="tajweed", n=30)]
    themes = (ThemeSpec("quran_learning", "Quran Learning", "تعليم القرآن", 118, ("tajweed",)),)
    p = plan(lib, themes=themes, mix_channel=False, presenter_channels=False)
    assert [c.key for c in p.channels] == ["quran_learning"]


def test_a_themes_own_minimum_replaces_the_month_rule() -> None:
    lib = [season("f1", "فقه", n=10)]  # 5 h: short of the 10 h rule
    own = (ThemeSpec("fiqh", "Fiqh", "الفقه", 111, ("fiqh",), min_hours=2.0),)
    p = plan(lib, themes=own, mix_channel=False, presenter_channels=False)
    assert [c.key for c in p.channels] == ["fiqh"]


def test_a_theme_takes_a_talk_shows_episodes_about_it_as_one_collection() -> None:
    from dataclasses import replace

    talk = season("bh", "بلا حدود", genre="current_affairs", presenter="أحمد منصور", n=40,
                  minutes=50)  # fmt: skip
    eps = [replace(e, tags={"topic:palestine": 0.9}) if i % 2 else e
           for i, e in enumerate(talk.episodes)]  # fmt: skip
    talk = replace(talk, episodes=tuple(eps))
    pal = (ThemeSpec("palestine", "Palestine", "فلسطين", 125, ("palestine",)),)
    p = plan([talk], themes=pal, mix_channel=False, presenter_channels=False)
    ch = p.channels[0]
    units = [u for k in ch.lanes for u in p.lanes[k].units]
    assert {u.name for u in units} == {"بلا حدود · فلسطين"}  # a special run of the show
    assert sum(len(u.episodes) for u in units) == 20  # only its Palestine episodes
    off = plan([talk], themes=pal, mix_channel=False, presenter_channels=False,
               theme_episodes=False)  # fmt: skip
    assert off.channels == ()


def test_a_show_on_two_channels_never_airs_at_the_same_time() -> None:
    fiqh = ThemeSpec("fiqh", "Fiqh", "الفقه", 111, ("fiqh",))
    salah = ThemeSpec("salah", "Salah", "فقه الصلاة", 121, ("salah",))
    lib = [season(f"s{i}", f"صلاة {i}", tags={"topic:salah": 1.0}, n=200, minutes=60)
           for i in range(3)]  # fmt: skip
    p = plan(lib, themes=(fiqh, salah), mix_channel=False, presenter_channels=False)
    assert {c.key for c in p.channels} == {"fiqh", "salah"}  # the shows air on both
    by_ch = {c.key: {(sl.start, p.lanes[sl.lane].shows[0]) for sl in c.slots
                     if sl.lane and sl.kind == "program"} for c in p.channels}  # fmt: skip
    assert not by_ch["fiqh"] & by_ch["salah"]  # never the same show at the same time


def test_a_show_goes_to_its_topics_channel_before_an_audience_one() -> None:
    hist = ThemeSpec("history", "History", "التاريخ", 108, ("islamic_history",))
    kids = ThemeSpec("kids", "Kids", "الأطفال", 110, ("seerah",), audiences=("beginners",))
    lib = [season("h1", "100 سؤال في التاريخ", genre="islamic_history", n=40, minutes=60,
                  tags={"audience:beginners": 1.0}),
           season("k1", "سيرة", genre="seerah", n=40, minutes=60)]  # fmt: skip
    p = plan(lib, themes=(hist, kids), mix_channel=False, presenter_channels=False,
             show_max_channels=1)  # fmt: skip
    on = {c.key: {u.name for k in c.lanes for u in p.lanes[k].units} for c in p.channels}
    assert "100 سؤال في التاريخ" in on["history"]
    assert "100 سؤال في التاريخ" not in on["kids"]


def test_a_short_channel_takes_back_a_show_rather_than_dropping_out() -> None:
    hist = ThemeSpec("history", "History", "التاريخ", 108, ("islamic_history",))
    kids = ThemeSpec("kids", "Kids", "الأطفال", 110, ("seerah",), audiences=("beginners",),
                     min_hours=70)  # fmt: skip
    lib = [season("h1", "100 سؤال في التاريخ", genre="islamic_history", n=40, minutes=60,
                  tags={"audience:beginners": 1.0}),
           season("k1", "سيرة", genre="seerah", n=40, minutes=60)]  # fmt: skip
    kw = dict(themes=(hist, kids), mix_channel=False, presenter_channels=False,
              show_max_channels=1, channel_min_days=1, channel_day_hours=1)  # fmt: skip
    p = plan(lib, **kw)
    on = {c.key: {u.name for k in c.lanes for u in p.lanes[k].units} for c in p.channels}
    assert on["kids"] == {"سيرة", "100 سؤال في التاريخ"}  # 40 h of its own + the history show
    assert "100 سؤال في التاريخ" in on["history"]
    off = plan(lib, **{**kw, "show_reuse_to_fill": False})
    assert "kids" not in {c.key for c in off.channels}


def test_talk_shows_give_up_a_special_runs_episodes_a_topic_channel_keeps_its_show() -> None:
    from dataclasses import replace

    talk = season("bh", "بلا حدود", genre="current_affairs", presenter="أحمد منصور", n=40,
                  minutes=50, tags={"format:program": 0.9})  # fmt: skip
    eps = [replace(e, tags={"topic:palestine": 0.9}) if i % 2 else e
           for i, e in enumerate(talk.episodes)]  # fmt: skip
    talk = replace(talk, episodes=tuple(eps))
    themes = (
        ThemeSpec("palestine", "Palestine", "فلسطين", 125, ("palestine",)),
        ThemeSpec("news", "News", "الأخبار", 124, ("current_affairs",)),
        ThemeSpec("talk", "Talk", "البرامج الحوارية", 126, formats=("program",)),
    )
    # one show: the same-time rule would leave a second channel no time for it
    p = plan([talk], themes=themes, mix_channel=False, presenter_channels=False,
             show_same_time=True)  # fmt: skip

    def eps_on(key: str) -> int:
        c = next(c for c in p.channels if c.key == key)
        return sum(len(u.episodes) for k in c.lanes for u in p.lanes[k].units
                   if u.name == "بلا حدود")  # fmt: skip

    assert eps_on("talk") == 20  # without the 20 the Palestine channel runs specially
    assert eps_on("news") == 40  # its topic's channel keeps the show whole


def test_the_quran_is_never_a_themes_show_or_special_run() -> None:
    """A recitation outside the reciters' libraries (a scholar's تهجد recitations,
    weakly tagged nasheed, each episode a surah about Hajj) airs on no theme channel:
    not as a show by a secondary tag, not as a theme's special run."""
    from dataclasses import replace

    reciter = [season(f"q{i}", f"تلاوة {i}", genre="quran_recitation", presenter="القارئ",
                      n=30, tags={"format:recitation": 1.0}) for i in range(3)]  # fmt: skip
    tahajjud = season("t1", "تلاوات صلاة التهجد", genre="quran_recitation", presenter="الشيخ",
                      n=30, tags={"format:recitation": 1.0, "topic:nasheed": 0.78})  # fmt: skip
    tahajjud = replace(tahajjud, episodes=tuple(
        replace(e, tags={"occasion:hajj": 0.9}) for e in tahajjud.episodes))  # fmt: skip
    nasheed = season("n1", "أناشيد", genre="nasheed", presenter="المنشد", n=40)
    hajj = season("h1", "دروس الحج", genre="fiqh", presenter="الشيخ", n=40,
                  tags={"occasion:hajj": 1.0})  # fmt: skip
    themes = (
        ThemeSpec("nasheed", "Nasheed", "الأناشيد", 114, ("nasheed", "poetry")),
        ThemeSpec("hajj", "Hajj", "الحج", 127, occasions=("hajj",)),
    )
    p = plan([*reciter, tahajjud, nasheed, hajj], themes=themes, mix_channel=False,
             presenter_channels=False)  # fmt: skip

    def paths(key: str) -> set[str]:
        c = next(c for c in p.channels if c.key == key)
        return {e.path for k in c.lanes for u in p.lanes[k].units for e in u.episodes}

    tahajjud_paths = {e.path for e in tahajjud.episodes}
    assert {e.path for e in nasheed.episodes} <= paths("nasheed")
    assert {e.path for e in hajj.episodes} <= paths("hajj")
    assert not tahajjud_paths & paths("nasheed")
    assert not tahajjud_paths & paths("hajj")


def test_a_khutbah_is_never_on_the_quran_lane() -> None:
    """A khutbah series tagged recitation too (a sermon about a surah) never airs as
    Quran recitation, though its presenter is a reciter."""
    reciter = [season(f"q{i}", f"تلاوة {i}", genre="quran_recitation", presenter="القارئ",
                      n=30, tags={"format:recitation": 1.0}) for i in range(3)]  # fmt: skip
    sermon = season("k1", "نظرات في سورة الأنفال", genre="quran_recitation",
                    presenter="القارئ", n=9,
                    tags={"format:recitation": 1.0, "format:khutbah": 0.9})  # fmt: skip
    quran_theme = ThemeSpec("quran", "Quran", "القرآن", 101, ("quran_recitation",), kind="quran")
    p = plan([*reciter, sermon], themes=(quran_theme,), mix_channel=False,
             presenter_channels=False)  # fmt: skip
    sermon_paths = {e.path for e in sermon.episodes}
    quran = next(c for c in p.channels if c.key == "quran")
    on_quran = {e.path for k in quran.lanes for u in p.lanes[k].units for e in u.episodes}
    assert {e.path for e in reciter[0].episodes} <= on_quran
    assert not sermon_paths & on_quran


def test_a_topic_channel_puts_its_specialists_in_the_slots_first() -> None:
    """On the Hadith channel the hadith scholars' lessons take the weekday evening slots;
    a talk show tagged hadith, from a presenter who mostly does other things, comes after."""
    hadith = ThemeSpec("hadith", "Hadith", "الحديث", 102, ("hadith",))
    books = (
        "البخاري",
        "مسلم",
        "أبي داود",
        "الترمذي",
        "النسائي",
        "ابن ماجه",
        "الموطأ",
        "بلوغ المرام",
        "عمدة الأحكام",
        "رياض الصالحين",
        "الأربعين",
        "مسند أحمد",
    )
    lib = [season(f"a{i}", f"شرح {b}", genre="hadith", presenter=("العدوي", "الحويني")[i % 2],
                  n=60, minutes=60) for i, b in enumerate(books)]  # fmt: skip
    lib += [
        season("m1", "حوار", genre="current_affairs", presenter="منصور", n=200, minutes=60,
               tags={"topic:hadith": 0.9, "format:program": 0.9}),
        season("m2", "بلا حدود", genre="current_affairs", presenter="منصور", n=200, minutes=60),
    ]  # fmt: skip

    def evening() -> list[str]:
        p = plan(lib, themes=(hadith,), mix_channel=False, presenter_channels=False)
        ch = next(c for c in p.channels if c.key == "hadith")
        return [p.lanes[s.lane].shows[0] for s in ch.slots
                if s.lane and s.kind == "program" and 17 * 60 <= s.start < 23 * 60
                and len(s.days) >= 4]  # fmt: skip

    got = evening()
    assert got and not {"حوار", "بلا حدود"} & set(got)  # the evening is the scholars'


def test_a_chapter_playlist_names_its_episodes_topic() -> None:
    from maktaba_pipeline.classify.topic_tags import chapter_tags

    assert chapter_tags("كتاب الصيام").get("topic:fasting", 0) > 0.8
    assert chapter_tags("كتاب الصلاة من صحيح البخاري").get("topic:salah", 0) > 0.8
    assert not chapter_tags("مجالس 1436")  # nothing named: nothing inherited


def test_a_channel_is_mainly_its_libraries_and_never_an_excluded_one() -> None:
    news = ThemeSpec("news", "News", "الأخبار", 124, ("current_affairs",),
                     libraries=("أحمد منصور",), exclude_libraries=("محمد إلهامي",))  # fmt: skip
    talk = ThemeSpec("talk", "Talk", "البرامج الحوارية", 126, formats=("program",),
                     libraries=("محمد إلهامي",))  # fmt: skip
    lib = [
        season("m1", "شاهد على العصر", genre="islamic_history", presenter="أحمد منصور",
               n=40, minutes=60),
        season("e1", "قراءات في التاريخ", genre="current_affairs", presenter="محمد إلهامي",
               n=40, minutes=60),
        season("o1", "أخبار وتعليقات", genre="current_affairs", presenter="آخر", n=40,
               minutes=60),
    ]  # fmt: skip
    from dataclasses import replace

    # its episodes are about current affairs too: still never a News special run
    lib[1] = replace(lib[1], episodes=tuple(
        replace(e, tags={"topic:current_affairs": 0.95}) for e in lib[1].episodes))  # fmt: skip
    p = plan(lib, themes=(news, talk), mix_channel=False, presenter_channels=False,
             show_max_channels=1, channel_min_days=1, channel_day_hours=1)  # fmt: skip
    on = {c.key: {u.name for k in c.lanes for u in p.lanes[k].units} for c in p.channels}
    assert "شاهد على العصر" in on["news"]  # its library, though tagged history
    assert "قراءات في التاريخ" not in on["news"]  # never on News
    news_paths = {e.path for c in p.channels if c.key == "news" for k in c.lanes
                  for u in p.lanes[k].units for e in u.episodes}  # fmt: skip
    assert not any("/e1/" in path for path in news_paths)  # not even single episodes
    assert "قراءات في التاريخ" in on["talk"]
    news_ch = next(c for c in p.channels if c.key == "news")
    first = p.lanes[news_ch.slots[0].lane] if news_ch.slots[0].lane else None
    prime = [p.lanes[s.lane].units[0].presenter for s in news_ch.slots
             if s.lane and s.kind == "program" and s.daypart == "prime"]  # fmt: skip
    assert prime and prime.count("أحمد منصور") >= len(prime) / 2, (first, prime)
