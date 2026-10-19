# ruff: noqa: E501  (real library titles read better unwrapped)
"""Titles that quote Quran verses (Story 27.11): the verse is found in the bundled Tanzil
text, never cut inside, and its reference stands in for it when a title must be short.
The titles are real library titles."""

from __future__ import annotations

import pytest

from maktaba_pipeline.channels.display import merge_snapshot
from maktaba_pipeline.classify.display_title import (
    DisplayTitleConfig,
    brand_phrases,
    clean_title,
    verse_reference,
)
from maktaba_pipeline.classify.quran import _index, find_quotes, locate, surah_by_name


@pytest.fixture(scope="module", autouse=True)
def _quran_index() -> None:
    """The Quran index is built once (a third of a second) before the timed unit tests."""
    _index()


OTHMAN = brand_phrases(["Dr. Othman Alkamees - الشيخ الدكتور عثمان الخميس"])
SIYAM = "681- تفسير قوله تعالى: ﴿يا أيها الذين آمنوا كتب عليكم الصيام كما كتب على الذين من قبلكم لعلكم تتقون﴾"
AJAL = "954- المقصود ببلوغ الأجل في قوله تعالى: ﴿وإذا طلقتم النساء فبلغن أجلهن فأمسكوهن بمعروف...﴾ الآية"
HASRA = "434-إدعاؤهم أن الله خلقهم جزءا من جنب الله في قوله تعالى يا حسرة على ما فرطنا في جنب الله"


@pytest.mark.unit
@pytest.mark.parametrize(
    ("quote", "ar", "en"),
    [
        ("يا أيها الذين آمنوا كتب عليكم الصيام", "البقرة ١٨٣", "Al-Baqara 2:183"),
        ("قالوا لا توجل إنا نبشرك بغلام عليم", "الحجر ٥٣", "Al-Hijr 15:53"),
        ("أَرَأَيْتَ إِن كَانَ عَلَى الْهُدَىٰ", "العلق ١١", "Al-Alaq 96:11"),
        # across two verses
        ("إِنَّ الْأَبْرَارَ لَفِي نَعِيمٍ. وَإِنَّ الْفُجَّارَ لَفِي جَحِيمٍ", "الإنفطار ١٣–١٤",
         "Al-Infitaar 82:13-14"),
        # «حسرة» / «فرطنا» where the text has «حسرتى» / «فرطت»
        ("يا حسرة على ما فرطنا في جنب الله", "الزمر ٥٦", "Az-Zumar 39:56"),
        # a surah's first verse (the text puts the basmala before it)
        ("سَبّح اسم رَبّك الأعلى", "الأعلى ١", "Al-A'laa 87:1"),
        ("بما أغويتني", "الحجر ٣٩", "Al-Hijr 15:39"),
    ],
)  # fmt: skip
def test_locate(quote: str, ar: str, en: str) -> None:
    ref = locate(quote)
    assert ref is not None
    assert ref.label(True) == ar and ref.label(False) == en


@pytest.mark.unit
def test_locate_refuses_what_it_cannot_tell() -> None:
    assert locate("ويعلمهم الكتاب والحكمة") is None  # four places
    assert locate("فاخرج منها") is None
    assert locate("نظرية التطور") is None
    assert surah_by_name("سورة البقرة") == 2 and surah_by_name("آل عمران") == 3


@pytest.mark.unit
def test_find_quotes() -> None:
    q = find_quotes(SIYAM)
    assert len(q) == 1 and q[0].open == "﴿" and q[0].ref and q[0].ref.label() == "البقرة ١٨٣"
    assert find_quotes("{ نظرية التطور } (3) كريج فنتر") == []
    assert find_quotes("سلسلة أسد الغابة {09} | محاصر قريش") == []
    tafsir = find_quotes("تفسير الآية ( ورفعنا لك ذكرك )")
    assert tafsir and tafsir[0].ref and tafsir[0].ref.label() == "الشرح ٤"
    bare = find_quotes(HASRA)
    assert bare and bare[0].open == "" and bare[0].ref and bare[0].ref.surah == 39


@pytest.mark.unit
def test_stored_titles_keep_the_whole_verse() -> None:
    got = clean_title(SIYAM, DisplayTitleConfig(), brands=OTHMAN)
    assert got.episode == 681
    assert got.title == (
        "تفسير قوله تعالى: ﴿يا أيها الذين آمنوا كتب عليكم الصيام كما كتب على الذين من قبلكم "
        "لعلكم تتقون﴾"
    )
    assert verse_reference(got.title) == "البقرة ١٨٣"
    # brackets inside the title survive the separators and the trims
    nuh = clean_title("401 - تفسير قوله تعالى ( قال يا نوح إنه ليس من أهلك ) - عثمان الخميس",
                      brands=OTHMAN)  # fmt: skip
    assert nuh.title == "تفسير قوله تعالى (قال يا نوح إنه ليس من أهلك)"
    # the title already names the surah: no reference page
    assert (
        verse_reference("التفسير الميسر سورة البقرة 4 من قول تعالى {مَثَلُهُمْ كَمَثَلِ الَّذِي اسْتَوْقَدَ نَاراً}")
        == ""
    )


@pytest.mark.unit
@pytest.mark.parametrize(
    ("raw", "mode", "want"),
    [
        (SIYAM, "reference", "تفسير البقرة ١٨٣"),
        (SIYAM, "opening_words", "تفسير قوله تعالى ﴿يا أيها الذين آمنوا كتب عليكم الصيام كما كتب على﴾…"),
        (AJAL, "reference", "المقصود ببلوغ الأجل في البقرة ٢٣١"),
        (HASRA, "reference", "إدعاؤهم أن الله خلقهم جزءا من جنب الله في الزمر ٥٦"),
        ("﴿يا أيها الذين آمنوا كتب عليكم الصيام كما كتب على الذين من قبلكم لعلكم تتقون﴾ تفسير",
         "reference", "البقرة ١٨٣ تفسير"),
    ],
)  # fmt: skip
def test_a_cut_title_never_cuts_inside_a_verse(raw: str, mode: str, want: str) -> None:
    cfg = DisplayTitleConfig(max_length=70, verse_display=mode)
    got = clean_title(raw, cfg, brands=OTHMAN).title
    assert got == want
    assert len(got) <= 70
    if "﴿" in got:  # the brackets close before «…»
        assert got.count("﴿") == got.count("﴾") and got.rstrip("…").endswith("﴾")


@pytest.mark.unit
def test_full_if_fits_keeps_the_verse_alone() -> None:
    cfg = DisplayTitleConfig(max_length=60, verse_display="full_if_fits")
    raw = "تفسير قوله تعالى ﴿قالوا لا توجل إنا نبشرك بغلام عليم﴾ وفوائد عظيمة من القصة كاملة للشيخ"
    assert clean_title(raw, cfg).title == "﴿قالوا لا توجل إنا نبشرك بغلام عليم﴾"


@pytest.mark.unit
def test_the_strip_gets_the_reference_as_its_last_page() -> None:
    title = clean_title(SIYAM, brands=OTHMAN).title
    snap = merge_snapshot({"title": SIYAM}, {"title": title, "series": "", "episode": None},
                          DisplayTitleConfig())  # fmt: skip
    assert snap["strip_ref"] == "البقرة ١٨٣"
    assert snap["on_air"] == title  # the whole verse: the strip pages it
