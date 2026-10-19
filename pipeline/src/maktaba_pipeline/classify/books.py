"""The book behind a lesson series.

Most lesson series explain a known book: «شرح بلوغ المرام» is a hadith book of rulings,
«فوائد من كتاب ثلاثة الأصول» is aqeedah, «شرح زاد المستقنع» is a whole fiqh manual (not a
Fiqh of Salah programme), «كتاب الصيام من بلوغ المرام» is that book's fasting chapter.
Titles say which book; the book says what the series is about. So:

* :func:`book_of` reads the book's name (and a chapter, when the series is one) from the
  series' name;
* the book is looked up (Arabic Wikipedia: :func:`lookup`); an article whose title
  matches the name (:func:`matches`) is the book, and its categories («كتب حديث»,
  «كتب فقه حنبلي», «كتب عقيدة») give its fields (:func:`fields`);
* :func:`apply` corrects the series' tags: the book's fields are its topics; a topic the
  book is not about is damped; a fiqh sub-topic (salah, fasting …) stays only when the
  series is that chapter, since a whole fiqh book is not about one of its chapters.

Pure except :func:`lookup` (HTTP). Settings: :class:`BooksConfig`.
"""

from __future__ import annotations

import json
import re
import ssl
import time
import urllib.error
import urllib.parse
import urllib.request
from collections.abc import Callable, Mapping, Sequence
from dataclasses import dataclass, field
from typing import Any

from .topic_tags import SUBTOPICS, TOPICS

#: bumps when finding or applying books changes (lookups are redone)
BOOKS_VERSION = 1

_DEFAULT_FIELDS: dict[str, tuple[str, ...]] = {
    "حديث": ("hadith",), "مصطلح": ("hadith",), "سنن": ("hadith",), "مسانيد": ("hadith",),
    "صحاح": ("hadith",), "جوامع": ("hadith",),
    "كتب سته": ("hadith",), "اربعينات": ("hadith",), "تخريج": ("hadith",),
    "فقه": ("fiqh",), "فتاوى": ("fiqh",),
    "عقيده": ("aqeedah",), "توحيد": ("aqeedah",), "اعتقاد": ("aqeedah",),
    "تفسير": ("tafsir",), "علوم القران": ("tafsir",),
    "سيره": ("seerah",), "شمائل": ("seerah",),
    "تجويد": ("tajweed",), "قراءات": ("tajweed",),
    "نحو": ("arabic_language",), "لغه عربيه": ("arabic_language",), "بلاغه": ("arabic_language",),
    "تصوف": ("raqaiq",), "زهد": ("raqaiq",), "رقائق": ("raqaiq",),
    "اخلاق": ("akhlaq",), "اداب": ("akhlaq",),
    "تاريخ": ("islamic_history",), "تراجم": ("islamic_history",),
    "اذكار": ("dua_dhikr",), "ادعيه": ("dua_dhikr",),
    "صحابه": ("sahaba",), "فرق": ("shubuhat",), "ردود": ("shubuhat",),
}  # fmt: skip


@dataclass(frozen=True, slots=True)
class BooksConfig:
    #: off: series tags are not checked against the books they explain
    enabled: bool = True
    #: the encyclopedia searched for a book (a MediaWiki API)
    wiki_api: str = "https://ar.wikipedia.org/w/api.php"
    #: seconds between two requests (the encyclopedia limits fast clients)
    request_gap_s: float = 1.5
    #: share of the name's words an article's title must have to be the book
    min_title_match: float = 0.75
    #: a word in a book's category → the topics it means («كتب فقه حنبلي» → fiqh)
    category_fields: dict[str, tuple[str, ...]] = field(
        default_factory=lambda: dict(_DEFAULT_FIELDS)
    )
    #: score of a topic the book is about
    book_score: float = 0.9
    #: a topic the book is not about keeps this share of its score
    damp: float = 0.3
    #: days before a book that was not found is searched again
    retry_missing_days: int = 30
    timeout_s: float = 20.0


# ---- the book's name ------------------------------------------------------------------

_DIACRITICS = re.compile(r"[ً-ٰٟـ]")
_LEAD = re.compile(
    r"^(?:(?:فوائد|دروس|درس|مجالس|مجلس|قراءة|تعليق|التعليق|مدارسة|تحقيق|تخريج|وتخريج|شرح|"
    r"شروح|مختارات|اختصار|متن|من|في|على|لكتاب|كتاب|ٌ)\s+)+"
)
_LEAD_NO_BOOK = re.compile(
    r"^(?:(?:فوائد|دروس|درس|مجالس|مجلس|قراءة|تعليق|التعليق|مدارسة|تحقيق|تخريج|وتخريج|شرح|"
    r"شروح|مختارات|اختصار|متن|من|في|على|ٌ)\s+)+"
)
_COLLECTION = re.compile(
    r"^(?:صحيح|سنن|مسند|موطأ|جامع|رياض الصالحين|بلوغ المرام|عمدة الأحكام|الأربعين|"
    r"زاد المستقنع|عمدة الفقه|كتاب التوحيد|الأصول الثلاثة|ثلاثة الأصول|العقيدة)\b"
)
_HAS_LEAD = re.compile(r"(?:^|\s)(?:شرح|التعليق على|مدارسة|تحقيق|فوائد من|دروس في|درس|"
                       r"قراءة في)\s")  # fmt: skip
_QUOTED = re.compile(r"[(\[«\"＂“]\s*([^)\]»\"＂”]{3,}?)\s*[)\]»\"＂”]")
_CHAPTER_OF = re.compile(r"^((?:كتاب|باب|أبواب|فقه|أحكام)\s+.+?)\s+من\s+(?:كتاب\s+)?(.+)$")
_CHAPTER_IN = re.compile(r"\s((?:كتاب|باب)\s+\S+(?:\s+\S+)?)")
_CUT = re.compile(
    r"\s*(?:[-–|⧸/:،,]|\d|\b(?:البث|بث|اللقاء|المنبر|رقم|الدرس|الحلقة|الحلقات|المجلس|الجزء|"
    r"القسم|مسجد|الدكتور|الشيخ|للشيخ|حديث حديث|كاملا|كاملة|مكتوب|صوتي|مرئي|المصور|الأول|"
    r"الثاني|في الفقه)\b).*$"
)


@dataclass(frozen=True, slots=True)
class BookRef:
    #: the book's name as the series gives it («بلوغ المرام»)
    name: str
    #: the chapter, when the series is one («كتاب الصيام»)
    chapter: str = ""


def _clean(s: str) -> str:
    s = _DIACRITICS.sub("", s)
    s = re.sub(r"[\"＂“”«»()\[\]{}]", " ", s)
    return re.sub(r"\s+", " ", s).strip()


def book_of(series_name: str, *, strip: Sequence[str] = ()) -> BookRef | None:
    """The book a series explains, read from its name; None when it names no book.

    «شرح ( سنن أبي داود ) - المنبر الصوتي» → سنن أبي داود; «(كتاب الصيام من بلوغ
    المرام)» → بلوغ المرام, chapter كتاب الصيام; «شرح مختصر صحيح البخاري كتاب الدعوات»
    → مختصر صحيح البخاري, chapter كتاب الدعوات. A name without a lead word (شرح، التعليق
    على …), a quoted title or a collection's name («صحيح البخاري») names no book."""
    raw = _DIACRITICS.sub("", series_name)
    for b in strip:
        raw = raw.replace(b, " ")
    quoted = [_clean(q) for q in _QUOTED.findall(raw)]
    whole = _clean(raw)
    if not (
        _HAS_LEAD.search(" " + whole + " ")
        or _COLLECTION.match(whole)
        or _CHAPTER_OF.match(_LEAD_NO_BOOK.sub("", whole))
        or any(_COLLECTION.match(q) or _HAS_LEAD.search(" " + q + " ") for q in quoted)
    ):
        return None
    chapter = ""
    # «كتاب الصيام من بلوغ المرام», «شرح (كتاب التفسير) من (مختصر صحيح البخاري)»
    if m := _CHAPTER_OF.match(_LEAD_NO_BOOK.sub("", whole)):
        chapter, text = m.group(1), m.group(2)
    else:
        # the book is the quoted title when there is one, else what follows the lead word
        text = quoted[0] if quoted else whole
    text = _LEAD.sub("", text).strip()
    if not chapter:  # «… صحيح البخاري كتاب الحيض»: the chapter after the book
        rest = whole if not quoted else whole.replace(_clean(quoted[0]), " ")
        if (m := _CHAPTER_IN.search(" " + (text if not quoted else rest))) and not text.startswith(
            m.group(1)
        ):
            chapter = m.group(1)
            text = text.split(" " + m.group(1))[0]
    name = _CUT.sub("", text).strip()
    if len(name.replace(" ", "")) < 4 or len(name.split()) > 8:
        return None
    return BookRef(name, _CUT.sub("", chapter).strip())


# ---- matching an article to the name ------------------------------------------------------

_STOP = {"كتاب", "شرح", "في", "من", "على", "الامام", "متن", "مختصر"}


def norm_words(s: str) -> list[str]:
    s = _DIACRITICS.sub("", s)
    s = re.sub("[إأآٱ]", "ا", s).replace("ى", "ي").replace("ة", "ه").replace("ؤ", "و")
    s = re.sub(r"[^\w\s]", " ", s)
    out = []
    for w in s.split():
        if w.startswith("ال") and len(w) > 3:
            w = w[2:]
        out.append(w)
    return out


def matches(name: str, title: str, min_share: float) -> bool:
    """The article ``title`` names the book ``name`` (most of the name's words are in it)."""
    want = [w for w in norm_words(name) if w not in _STOP]
    have = set(norm_words(title))
    if not want:
        return False
    return sum(w in have for w in want) / len(want) >= min_share


def fields(categories: Sequence[str], cfg: BooksConfig) -> tuple[str, ...]:
    """The topics a book's categories name, in the categories' order: only the categories
    of books («تصنيف:كتب فقه حنبلي»), not the author's or the century's."""
    known = set(TOPICS) | set(SUBTOPICS)
    keys = {" ".join(norm_words(k)): v for k, v in cfg.category_fields.items()}
    out: list[str] = []
    for c in categories:
        words = norm_words(c.split(":", 1)[-1])
        if "كتب" not in words:
            continue
        text = " " + " ".join(words) + " "
        for k, topics in keys.items():
            if f" {k} " in text:
                out.extend(t for t in topics if t in known)
    return tuple(dict.fromkeys(out))


# ---- the lookup ---------------------------------------------------------------------------


@dataclass(frozen=True, slots=True)
class Book:
    name: str
    #: the article found ("" when none matched)
    title: str = ""
    url: str = ""
    categories: tuple[str, ...] = ()
    summary: str = ""

    @property
    def found(self) -> bool:
        return bool(self.title)


def _ssl() -> ssl.SSLContext:
    try:
        import certifi

        return ssl.create_default_context(cafile=certifi.where())
    except ImportError:  # pragma: no cover
        return ssl.create_default_context()


def http_get(url: str, cfg: BooksConfig) -> dict[str, Any]:
    req = urllib.request.Request(
        url, headers={"User-Agent": "maktaba-mediaintel/1.0 (book lookup)"}
    )
    for wait in (10, 30, 60, 0):
        try:
            with urllib.request.urlopen(req, timeout=cfg.timeout_s, context=_ssl()) as r:  # noqa: S310
                return dict(json.loads(r.read().decode("utf-8")))
        except urllib.error.HTTPError as e:
            if e.code != 429 or not wait:
                raise
            time.sleep(wait)
    raise RuntimeError("unreachable")


def lookup(
    name: str, cfg: BooksConfig, get: Callable[[str], Mapping[str, Any]] | None = None
) -> Book:
    """Search the encyclopedia for the book; the first of the top hits whose title matches
    the name and which is filed as a book is it."""
    fetch = get or (lambda u: http_get(u, cfg))
    for query in (name, f"كتاب {name}"):
        params = {
            "action": "query", "format": "json", "generator": "search", "gsrsearch": query,
            "gsrlimit": 5, "prop": "categories|extracts|info", "inprop": "url", "exintro": 1,
            "explaintext": 1, "exlimit": 5, "cllimit": "max", "clshow": "!hidden",
        }  # fmt: skip
        data = fetch(cfg.wiki_api + "?" + urllib.parse.urlencode(params))
        pages = sorted((data.get("query") or {}).get("pages", {}).values(),
                       key=lambda p: p.get("index", 99))  # fmt: skip
        for p in pages:
            cats = tuple(c["title"] for c in p.get("categories", []))
            title = str(p.get("title", ""))
            is_book = any("كتب" in norm_words(c.split(":", 1)[-1]) for c in cats) or str(
                p.get("extract", "")
            ).lstrip().startswith(("كتاب", "رسالة", "متن", "منظومة"))
            if is_book and matches(name, title, cfg.min_title_match):
                return Book(name, title, str(p.get("fullurl", "")), cats,
                            str(p.get("extract", ""))[:400])  # fmt: skip
        if cfg.request_gap_s:
            time.sleep(cfg.request_gap_s)
    return Book(name)


# ---- correcting the tags ------------------------------------------------------------------


def apply(
    tags: Mapping[str, tuple[float, str]],
    book_fields: Sequence[str],
    chapter_topics: Mapping[str, float],
    evidence: str,
    cfg: BooksConfig,
) -> dict[str, tuple[float, str]]:
    """The series' tags corrected by its book (see the module docstring). ``book_fields``:
    the book's topics; ``chapter_topics``: the chapter's, when the series is one."""
    if not book_fields:
        return dict(tags)
    keep = set(book_fields)
    out: dict[str, tuple[float, str]] = {}
    chapter = {t if ":" in t else f"topic:{t}": sc for t, sc in chapter_topics.items()}
    damped = f"{evidence}: not its subject"
    for tag, (score, ev) in tags.items():
        ns, _, val = tag.partition(":")
        if ns == "genre":
            continue
        # damped once: the step runs again on its own output (idempotent)
        if ns == "topic" and val not in keep and tag not in chapter and damped not in ev:
            out[tag] = (round(score * cfg.damp, 3), f"{ev}; {damped}")
        else:
            out[tag] = (score, ev)

    def raise_to(key: str, sc: float, why: str) -> None:
        old, ev = out.get(key, (0.0, ""))
        if old < sc:
            out[key] = (sc, ev if why in ev else f"{ev}; {why}".lstrip("; "))

    for t in book_fields:
        raise_to(f"topic:{t}", cfg.book_score, evidence)
    for key, sc in chapter.items():  # a chapter never lowers its own topic
        raise_to(key, sc, f"{evidence}: its chapter")
    broad = next((t for t in book_fields if t in TOPICS), None)
    if broad:
        out[f"genre:{broad}"] = (out[f"topic:{broad}"][0], f"{evidence}: its subject")
    else:
        old = next((tg for tg in tags if tg.startswith("genre:")), None)
        if old:
            out[old] = tags[old]
    return out


def _register_help() -> None:
    from maktaba_pipeline.settings import Help, register

    g = "Books behind the series"
    register(BooksConfig, {
        "enabled": Help(g, "Look up the books series explain", "A series named «شرح بلوغ "
                        "المرام» explains that book: the book is looked up and its subject "
                        "(hadith, fiqh, aqeedah …) corrects the series' topics. A whole fiqh "
                        "book is not about one of its chapters (salah, fasting)."),
        "wiki_api": Help(g, "Encyclopedia searched", "A MediaWiki API (Arabic Wikipedia)."),
        "request_gap_s": Help(g, "Pause between requests (s)", minimum=0),
        "min_title_match": Help(g, "Title match needed (share of words)", "How many of the "
                                "name's words an article's title must have to be the book.",
                                minimum=0),
        "category_fields": Help(g, "Category word → topics", "A word in a book's category "
                                "and the topics it means («فقه» → fiqh)."),
        "book_score": Help(g, "Score of the book's subject", minimum=0),
        "damp": Help(g, "Topic the book is not about keeps (share of its score)", minimum=0),
        "retry_missing_days": Help(g, "Search a book not found again after (days)", minimum=0),
        "timeout_s": Help(g, "Request timeout (s)", minimum=1),
    })  # fmt: skip


_register_help()
