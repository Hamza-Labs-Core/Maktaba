from maktaba_pipeline.classify import books as bk

CFG = bk.BooksConfig(request_gap_s=0)


def test_book_of_reads_the_book_and_its_chapter():
    assert bk.book_of("شرح بلوغ المرام") == bk.BookRef("بلوغ المرام")
    assert bk.book_of("فوائد من كتاب ثلاثة الأصول") == bk.BookRef("ثلاثة الأصول")
    assert bk.book_of("شرح ( سنن أبي داود ) - المنبر الصوتي") == bk.BookRef("سنن أبي داود")
    assert bk.book_of("(كتاب الصيام من بلوغ المرام)") == bk.BookRef("بلوغ المرام", "كتاب الصيام")
    assert bk.book_of("شرح (كتاب التفسير) من (مختصر صحيح البخاري) كاملا") == bk.BookRef(
        "مختصر صحيح البخاري", "كتاب التفسير"
    )
    assert bk.book_of("شرح مختصر صحيح البخاري كتاب الدعوات") == bk.BookRef(
        "مختصر صحيح البخاري", "كتاب الدعوات"
    )


def test_book_of_ignores_names_that_name_no_book():
    for name in ("(ذو الجلال)", "المؤمنُ القويُّ ((تطوير النفس))", "مواعظ متنوعة", "علمتني آية"):
        assert bk.book_of(name) is None, name


def test_matches_needs_most_of_the_names_words():
    assert bk.matches("بلوغ المرام", "بلوغ المرام من أدلة الأحكام (كتاب)", 0.75)
    assert not bk.matches("تلخيص مختصر المقنع", "النوادر والزيادات", 0.75)


def test_fields_read_only_the_categories_of_books():
    cats = ("تصنيف:كتب حديث سنية", "تصنيف:كتب فقه", "تصنيف:كتب ابن حجر العسقلاني",
            "تصنيف:محمد بن عبد الوهاب")  # fmt: skip
    assert bk.fields(cats, CFG) == ("hadith", "fiqh")
    assert bk.fields(("تصنيف:كتب عقيدة أثرية",), CFG) == ("aqeedah",)
    assert bk.fields(("تصنيف:الكتب الستة", "تصنيف:كتب أهل السنة والجماعة"), CFG) == ("hadith",)


def test_lookup_takes_the_first_matching_book_article():
    def get(url: str) -> dict:
        return {"query": {"pages": {
            "1": {"index": 1, "title": "سلمان العودة", "categories": [{"title": "تصنيف:دعاة"}]},
            "2": {"index": 2, "title": "بلوغ المرام من أدلة الأحكام (كتاب)",
                  "fullurl": "https://ar.wikipedia.org/wiki/x",
                  "categories": [{"title": "تصنيف:كتب حديث سنية"}]},
        }}}  # fmt: skip

    b = bk.lookup("بلوغ المرام", CFG, get=get)
    assert b.found and b.title.startswith("بلوغ المرام")


def test_apply_a_whole_fiqh_book_is_not_about_one_chapter():
    tags = {"topic:fiqh": (0.6, "kw"), "topic:salah": (0.7, "kw"), "topic:hadith": (0.5, "kw"),
            "genre:hadith": (0.5, "kw"), "format:lecture": (0.9, "kw")}  # fmt: skip
    out = bk.apply(tags, ("fiqh",), {}, "book «زاد المستقنع»", CFG)
    assert out["topic:fiqh"][0] == CFG.book_score
    assert out["topic:salah"][0] < 0.3 and out["topic:hadith"][0] < 0.3
    assert "genre:fiqh" in out and "genre:hadith" not in out
    assert out["format:lecture"] == tags["format:lecture"]


def test_apply_a_chapter_keeps_its_topic():
    tags = {"topic:hadith": (0.4, "kw"), "topic:fasting": (0.6, "kw")}
    out = bk.apply(tags, ("hadith", "fiqh"), {"topic:fasting": 0.86}, "book «بلوغ المرام»", CFG)
    assert out["topic:fasting"][0] == 0.86
    assert out["topic:hadith"][0] == CFG.book_score and out["topic:fiqh"][0] == CFG.book_score
    assert out["genre:hadith"][0] == CFG.book_score


def test_apply_without_fields_changes_nothing():
    tags = {"topic:hadith": (0.4, "kw")}
    assert bk.apply(tags, (), {}, "book", CFG) == tags


def test_apply_twice_is_apply_once():
    tags = {"topic:fiqh": (0.8, "kw"), "topic:salah": (0.9, "kw"), "topic:hadith": (0.5, "kw")}
    once = bk.apply(tags, ("hadith",), {}, "book «b»", CFG)
    assert bk.apply(once, ("hadith",), {}, "book «b»", CFG) == once


def test_a_chapter_never_lowers_its_own_topic():
    out = bk.apply({"topic:fasting": (0.95, "kw")}, ("hadith",), {"topic:fasting": 0.865},
                   "book «b»", CFG)  # fmt: skip
    assert out["topic:fasting"] == (0.95, "kw")
