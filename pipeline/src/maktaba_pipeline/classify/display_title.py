"""Streaming-friendly display titles: a short, clean on-air title for every video.

Library titles are what YouTube or the file system gave: «في الخنادق… لا يوجد ملحدون! ｜
برنامج من زاوية أخرى ｜ الحلقة 45», «كيف نخاطب الجيل الجديد؟ - 1438⧸10⧸12». A channel's
guide and its on-screen strip want «في الخنادق… لا يوجد ملحدون!» with the series («من زاوية
أخرى») and «الحلقة 45» kept apart.

:func:`clean_title` applies the rules (:class:`DisplayTitleConfig`, rules section
``display_titles``), in order:

1. the fullwidth look-alikes yt-dlp writes into file names (``｜ ： ⧸``) are put back, a
   file extension is dropped;
2. URLs, hashtags, emojis, quality tags (``(1080p)``, ``[HD]``), dates
   (``1438/10/12``, ``2019-05-01``) and the configured words (``Official``, ``Full``…) go;
3. the title is cut into its segments (``|``, `` - ``, ``•``…); a segment that is the
   channel's or presenter's name (:func:`~.topic_tags.branding`), the series' name, a phrase
   most titles of the library or the series share (:func:`title_boilerplate`) or only an
   episode marker (``الحلقة 45``: its number is kept) is dropped;
4. repeated punctuation is folded, and the result is cut at a word boundary to
   ``max_length``.

What the rules leave long or still made of several segments can be rewritten by the local
language model (:func:`llm_prompt` / :func:`parse_llm`, the same model the tag checks ask;
off by default). A title set by hand always wins (``videos.display_title_manual``).

Pure functions, no I/O.
"""

from __future__ import annotations

import hashlib
import json
import re
import unicodedata
from collections import Counter
from collections.abc import Iterable, Mapping, Sequence
from dataclasses import dataclass
from typing import Any

from ..settings import Help, register
from ..text.arabic import fold
from .topic_tags import branding

__all__ = [
    "DISPLAY_TITLE_VERSION",
    "Cleaned",
    "DisplayTitleConfig",
    "brand_phrases",
    "clean_series_name",
    "clean_title",
    "episode_label",
    "fit_title",
    "verse_reference",
    "is_arabic",
    "llm_input_hash",
    "llm_prompt",
    "needs_llm",
    "on_air_line",
    "parse_llm",
    "title_boilerplate",
]

#: bumps when the rules change what they produce (every library is cleaned again)
DISPLAY_TITLE_VERSION = 1
#: bumps when the model's prompt changes (cached answers are asked again)
LLM_PROMPT_VERSION = 1

_DEFAULT_PATTERNS = (
    # quality / format tags, bracketed or not
    r"[\[(（【]\s*(?:full\s*)?(?:hd|fhd|uhd|4k|8k|\d{3,4}p|hq|sd|official(?:\s+video)?|"
    r"lyrics?|audio|video|مترجم|حصري[اًا]*|بجودة\s+عالية)\s*[\])）】]",
    r"\b\d{3,4}p\b",
    r"\b(?:4k|8k|uhd|fhd)\b",
)
_DEFAULT_WORDS = (
    "official video", "official", "full episode", "full video", "full hd", "full",
    "hd", "hq", "حصريا", "حصرياً", "بجودة عالية", "جودة عالية",
)  # fmt: skip


@dataclass(frozen=True)
class DisplayTitleConfig:
    """How on-air titles are made from the library's titles (rules ``display_titles``)."""

    #: off: the guide and the strip show the library titles as they are
    enabled: bool = True
    #: the longest stored title (characters; 0: no limit, the default: the guide takes the
    #: whole title and the channel strip pages a long one); a longer one is cut at a word
    #: boundary, never inside a quoted verse (see ``verse_display``)
    max_length: int = 0
    #: a title that quotes a Quran verse and must be cut: ``reference`` puts the verse's
    #: reference in its place («تفسير البقرة ١٨٣»); ``opening_words`` keeps the verse's
    #: first words («﴿يا أيها الذين آمنوا﴾…»); ``full_if_fits`` keeps the whole verse
    #: without the rest of the title when it fits, else the reference
    verse_display: str = "reference"
    #: regular expressions removed from every title (case-insensitive)
    strip_patterns: tuple[str, ...] = _DEFAULT_PATTERNS
    #: whole words / phrases removed from every title (case-insensitive)
    strip_words: tuple[str, ...] = _DEFAULT_WORDS
    #: the episode number from the season (else from the title) shown as «الحلقة 45»
    keep_episode_number: bool = True
    episode_label_ar: str = "الحلقة {n}"
    episode_label_en: str = "Ep {n}"
    #: a title segment shared by at least this share of a library's (or a series') titles
    #: is its branding and is dropped
    boilerplate_share: float = 0.3
    boilerplate_min_count: int = 5
    #: the guide's and the dizqueTV export's programme titles are the display titles
    use_in_guide: bool = True
    #: the local language model rewrites what the rules leave long or messy (the model
    #: and server of rules ``tagcheck``); answers are kept per title
    use_llm: bool = False
    llm_batch: int = 20


register(
    DisplayTitleConfig,
    {
        "enabled": Help(
            "Display titles", "Clean on-air titles",
            "The guide, the channel strip and the dizqueTV export show a short title made "
            "from the library title: no channel names, hashtags, emojis, links, dates or "
            "file leftovers, the series and «الحلقة 45» kept apart. A title set by hand on "
            "the video always wins.",
        ),
        "max_length": Help("Display titles", "Longest title (characters)",
                           "0: no limit (the guide shows the whole title; the channel strip "
                           "shows a long one in pages). Else cut at a word, never inside a "
                           "quoted verse.", minimum=0, maximum=500),
        "verse_display": Help(
            "Display titles", "A quoted verse in a cut title",
            "reference: the verse's surah and number in its place («تفسير البقرة ١٨٣»); "
            "opening_words: its first words, «…» after the closing bracket; full_if_fits: "
            "the whole verse alone when it fits, else the reference.",
            choices=("reference", "opening_words", "full_if_fits"),
        ),
        "strip_patterns": Help("Display titles", "Patterns removed",
                               "Regular expressions (case-insensitive) removed from every "
                               "title."),
        "strip_words": Help("Display titles", "Words removed",
                            "Whole words or phrases removed from every title."),
        "keep_episode_number": Help("Display titles", "Show the episode number",
                                    "From the season's order, else from the title."),
        "episode_label_ar": Help("Display titles", "Episode label (Arabic)",
                                 "{n} is the number."),
        "episode_label_en": Help("Display titles", "Episode label (other languages)",
                                 "{n} is the number."),
        "boilerplate_share": Help(
            "Display titles", "Shared phrase from (share of titles)",
            "A title part that this share of a library's or a series' titles carry (a "
            "programme's or channel's name) is dropped.", minimum=0.05, maximum=1,
        ),
        "boilerplate_min_count": Help("Display titles", "Shared phrase from (titles)",
                                      minimum=2),
        "use_in_guide": Help("Display titles", "Use in the guide and dizqueTV",
                             "Off: only the channel strip shows the display titles."),
        "use_llm": Help(
            "Display titles", "Let the language model tidy what is left",
            "The local model (Tag check settings) rewrites titles the rules leave long or "
            "messy. Each answer is kept, so a title is asked once.",
        ),
        "llm_batch": Help("Display titles", "Titles per question", minimum=1, maximum=100),
    },
)  # fmt: skip


@dataclass(frozen=True, slots=True)
class Cleaned:
    """A cleaned title: the on-air ``title`` (may be empty: the series and episode say it
    all) and the episode number the title named, if any."""

    title: str
    episode: int | None = None
    #: segments the rules could not tell apart (more than one kept)
    segments: int = 1


# ---------------------------------------------------------------- character level

_YTDLP_BACK = {
    "｜": " | ", "：": ":", "⧸": "/", "⧹": "/", "＂": '"', "＊": "*", "＜": "<", "＞": ">",
    "？": "?", "｢": "«", "｣": "»",
}  # fmt: skip
_URL_RE = re.compile(r"(?:https?://|www\.)\S+", re.I)
_HASHTAG_RE = re.compile(r"[#＃][\w؀-ۿ_]+")
_MENTION_RE = re.compile(r"(?<!\w)@[\w.]+")
_DIGIT = r"[0-9٠-٩۰-۹]"
_DATE_RE = re.compile(
    rf"(?<!{_DIGIT}){_DIGIT}{{1,4}}\s*[/.\-]\s*{_DIGIT}{{1,2}}\s*[/.\-]\s*{_DIGIT}{{1,4}}"
    rf"(?!{_DIGIT})(?:\s*(?:هـ|ه|م)(?![\w]))?"
)
_EXT_RE = re.compile(r"\.(?:mp4|mkv|webm|avi|mov|m4v|ts|flv|wmv|mp3|m4a)$", re.I)
_ARABIC_RE = re.compile(r"[؀-ۿ]")
# keep Arabic ligatures (ﷺ) and letters; drop pictographs, dingbats, symbols
_KEEP_SO = frozenset("ﷺ﷽")


def is_arabic(text: str) -> bool:
    return bool(_ARABIC_RE.search(text or ""))


def _drop_emoji(s: str) -> str:
    out = []
    for ch in s:
        cp = ord(ch)
        if ch in _KEEP_SO:
            out.append(ch)
            continue
        cat = unicodedata.category(ch)
        if cat == "So" or cat == "Cs" or 0xFE00 <= cp <= 0xFE0F or cp in (0x200D, 0x20E3):
            out.append(" ")
            continue
        if 0x1F000 <= cp <= 0x1FAFF or 0x2600 <= cp <= 0x27BF or 0x1F1E6 <= cp <= 0x1F1FF:
            out.append(" ")
            continue
        out.append(ch)
    return "".join(out)


_LEAD_NUM_RE = re.compile(
    rf"^\s*(?:#\s*({_DIGIT}{{1,4}})\s+|({_DIGIT}{{1,4}})\s*[-.)_]\s*(?=[^\d\s/.\-]))"
)


def _lead_number(raw: str) -> tuple[int | None, str]:
    """A title that starts with its number («#20 …», «175- …»): the number and the rest."""
    m = _LEAD_NUM_RE.match(raw or "")
    if not m:
        return None, raw
    return _to_int(m.group(1) or m.group(2)), raw[m.end() :]


def _prep(raw: str, cfg: DisplayTitleConfig) -> str:
    s = raw or ""
    for a, b in _YTDLP_BACK.items():
        s = s.replace(a, b)
    s = _EXT_RE.sub("", s.strip())
    s = _URL_RE.sub(" ", s)
    s = _HASHTAG_RE.sub(" ", s)
    s = _MENTION_RE.sub(" ", s)
    s = _drop_emoji(s)
    for pat in cfg.strip_patterns:
        try:
            s = re.sub(pat, " ", s, flags=re.I)
        except re.error:
            continue  # a bad pattern in the settings is skipped
    s = _DATE_RE.sub(" ", s)
    for w in sorted(cfg.strip_words, key=len, reverse=True):
        w = w.strip()
        if w:
            s = re.sub(rf"(?<!\w){re.escape(w)}(?!\w)", " ", s, flags=re.I)
    return s


# ---------------------------------------------------------------- segments

# separators between a title's parts: pipes, bullets, dashes with spaces around them
_SEP_RE = re.compile(
    r"\s*(?:\||•|·|◉|●|►|▶|«\s*»)\s*|\s+#\s+|\s+ـ+\s+|\s+ـ+$|\s+[-–—]+\s+|\s*::\s*|\s+[-–—]+$|^[-–—]+\s+"
)
_ORDINALS = {
    "الاولي": 1, "الاول": 1, "الثانيه": 2, "الثاني": 2, "الثالثه": 3, "الثالث": 3,
    "الرابعه": 4, "الرابع": 4, "الخامسه": 5, "الخامس": 5, "السادسه": 6, "السادس": 6,
    "السابعه": 7, "السابع": 7, "الثامنه": 8, "الثامن": 8, "التاسعه": 9, "التاسع": 9,
    "العاشره": 10, "العاشر": 10,
}  # fmt: skip
_EP_NUM = rf"({_DIGIT}+)"
_EP_RE = re.compile(
    rf"(?:(?<!\w)(?:ال)?حلق[ةه](?:\s+رقم)?\s*[:#.\-]?\s*{_EP_NUM}"
    rf"|(?<!\w)(?:episode|ep\.?|e)\s*[:#.\-]?\s*{_EP_NUM}"
    rf"|(?<!\w)(?:ال)?حلق[ةه]\s+(\w+))",
    re.I,
)
_TRIM_CHARS = " \t-–—_|:;,،.•·/\\*~+=<>^`"
#: brackets and quotes are trimmed only when left alone at an end («(» at the end, «)» at
#: the start); a balanced pair («(قال يا نوح إنه ليس من أهلك)») is kept
_OPENERS = "([{﴿«“"
_CLOSERS = ")]}﴾»”"


def _trim(s: str) -> str:
    prev = None
    while prev != s:
        prev = s
        s = s.strip(_TRIM_CHARS).rstrip(_OPENERS).lstrip(_CLOSERS)
    return s


_PUNCT_RUNS = (
    (re.compile(r"\.{3,}|…+"), "…"),
    (re.compile(r"!{2,}"), "!"),
    (re.compile(r"[?؟]{2,}"), "؟"),
    (re.compile(r"([,،:;])\1+"), r"\1"),
    (re.compile(r"\s+([!?؟,،:;.…])"), r"\1"),
    (re.compile(r"\(\s*\)|\[\s*\]"), " "),
)


def _to_int(digits: str) -> int:
    return int(digits.translate(str.maketrans("٠١٢٣٤٥٦٧٨٩۰۱۲۳۴۵۶۷۸۹", "01234567890123456789")))


def _episode_in(seg: str) -> tuple[int | None, str]:
    """The episode number a segment names and the segment without its marker."""
    m = _EP_RE.search(seg)
    if not m:
        return None, seg
    n: int | None = None
    if m.group(1):
        n = _to_int(m.group(1))
    elif m.group(2):
        n = _to_int(m.group(2))
    elif m.group(3):
        n = _ORDINALS.get(fold(m.group(3)))
        if n is None:
            return None, seg  # «حلقة نقاش»: not a marker
    rest = _trim(seg[: m.start()] + " " + seg[m.end() :])
    return n, rest


def _tidy(s: str) -> str:
    # a dangling ellipsis is a cut YouTube title, not part of it
    s = re.sub(r"\s+(?:…+|\.{3,})\s*$", "", s)
    for rx, rep in _PUNCT_RUNS:
        s = rx.sub(rep, s)
    s = " ".join(s.split())
    s = _trim(s)
    # unbalanced quotes/brackets left by the cuts
    for o, c in ("()", "«»", "[]", "“”", "{}", "﴿﴾"):
        if s.count(o) != s.count(c):
            s = s.replace(o, " ").replace(c, " ")
    if s.count('"') % 2:
        s = s.replace('"', " ")
    if s.count("''") % 2:
        s = s.replace("''", " ")
    s = " ".join(s.split())
    # no space just inside a bracket: «( قال يا نوح )» → «(قال يا نوح)»
    s = re.sub(r"([(\[{﴿«“])\s+", r"\1", s)
    s = re.sub(r"\s+([)\]}﴾»”])", r"\1", s)
    s = _trim(s)
    return (
        " ".join(s.split()).strip(_TRIM_CHARS + "!") if not s.endswith(("!", "؟", "?", "…")) else s
    )


def _segments(s: str) -> list[str]:
    return [p for p in (_trim(x) for x in _SEP_RE.split(s)) if p]


_HONOR = frozenset(fold(
    "الشيخ للشيخ شيخ الدكتور د دكتور لفضيلة فضيلة الامام العلامة الاستاذ القارئ للقارئ "
    "مع برنامج قناة القناة الرسمية dr sheikh shaykh with official channel"
).split())  # fmt: skip


def _strip_phrase(seg: str, phrases: Sequence[str]) -> str:
    """The segment without any of the (folded) phrases, word by word, and without an
    honorific left dangling before one («الشيخ عثمان الخميس» → «»)."""
    words = seg.split()
    folded = [fold(w) for w in words]
    keep = [True] * len(words)
    for ph in phrases:
        toks = ph.split()
        if not toks:
            continue
        n = len(toks)
        i = 0
        while i + n <= len(words):
            if folded[i : i + n] == toks and all(keep[i : i + n]):
                for k in range(i, i + n):
                    keep[k] = False
                j = i - 1
                while j >= 0 and keep[j] and folded[j] in _HONOR:
                    keep[j] = False
                    j -= 1
                i += n
            else:
                i += 1
    out = [w for w, k in zip(words, keep, strict=True) if k]
    return " ".join(out)


_CREDIT = frozenset(fold(
    "الشيخ للشيخ فضيلة فضيله د دكتور الدكتور للدكتور الاستاذ القارئ للقارئ المقرئ الامام "
    "العلامة sheikh shaykh dr ustadh imam"
).split())  # fmt: skip


def _drop_credit(seg: str, first: bool) -> str:
    """A presenter credit goes: in a later part everything from an honorific on («د حسام
    عقل», «… (1) د. أحمد عبد المنعم»); in the first part a short one at its end."""
    words = seg.split()
    for i, w in enumerate(words):
        # in the title's own first part only a short credit at its end («… د. محمد حسن عبد
        # الغفار»), never «قصة الشيخ …»
        if fold(w) in _CREDIT and (not first or (i >= 3 and len(words) - i <= 5)):
            return " ".join(words[:i])
    return seg


def _is_noise(seg: str, brands: Sequence[str], drop: frozenset[str]) -> bool:
    f = fold(seg)
    if not f:
        return True
    if f in drop or any(f == b for b in brands):
        return True
    # only honorifics / programme words left
    return all(w in _HONOR for w in f.split())


def brand_phrases(channel_names: Iterable[str], series_names: Iterable[str] = ()) -> list[str]:
    """The folded phrases removed from titles: the channel's and presenter's names
    (:func:`~.topic_tags.branding`) and the series' names, longest first."""
    out = set(branding([c for c in channel_names if c]))
    for s in series_names:
        f = fold(s)
        if f:
            out.add(f)
            words = [w for w in f.split() if w not in _HONOR]
            if len(words) >= 2:
                out.add(" ".join(words))
    return sorted(out, key=lambda x: (-len(x), x))


def title_boilerplate(
    titles: Iterable[str], cfg: DisplayTitleConfig | None = None
) -> frozenset[str]:
    """Folded title segments that at least ``boilerplate_share`` of these titles (and
    ``boilerplate_min_count`` of them) carry: a programme's or channel's name pasted into
    every title («برنامج من زاوية أخرى»). The title-segment counterpart of
    :func:`~.topic_tags.boilerplate_phrases`."""
    cfg = cfg or DisplayTitleConfig()
    counts: Counter[str] = Counter()
    n = 0
    for t in titles:
        if not t:
            continue
        n += 1
        segs = {fold(_episode_in(s)[1]) for s in _segments(_prep(t, cfg))}
        counts.update(s for s in segs if s)
    if n < cfg.boilerplate_min_count:
        return frozenset()
    need = max(cfg.boilerplate_min_count, cfg.boilerplate_share * n)
    return frozenset(s for s, c in counts.items() if c >= need)


def _cut(s: str, limit: int) -> str:
    """``s`` cut to ``limit`` characters at a word boundary, with «…» (never mid-word: a
    first word longer than the limit is the only exception)."""
    if limit <= 0 or len(s) <= limit:
        return s
    cut = s[: limit - 1]
    sp = cut.rfind(" ")
    if sp > 0 and not s[limit - 1].isspace():
        cut = cut[:sp]
    return cut.rstrip(_TRIM_CHARS) + "…"


def _join(*parts: str) -> str:
    return " ".join(p.strip(" :：-–—") for p in parts if p and p.strip(" :：-–—"))


_AYA_TAIL = re.compile(r"^\s*(?:الآية|الاية|الآيات|الايات|الآيه)(?=\s|$)")


def fit_title(title: str, cfg: DisplayTitleConfig) -> str:
    """``title`` within ``max_length``: a quoted verse is never cut inside; per
    ``verse_display`` it gives way to its reference or keeps its opening words."""
    limit = cfg.max_length
    if limit <= 0 or len(title) <= limit:
        return title
    from .quran import find_quotes

    quotes = find_quotes(title)
    if not quotes:
        return _cut(title, limit)
    q = quotes[0]
    arabic = is_arabic(title)
    before, after = title[: q.start], _AYA_TAIL.sub("", title[q.end :])
    if q.ref is not None and cfg.verse_display in ("reference", "full_if_fits"):
        if cfg.verse_display == "full_if_fits":
            alone = f"{q.open or '﴿'}{q.text}{q.close or '﴾'}"
            if len(alone) <= limit:
                return alone
        new = _tidy(_join(before, q.ref.label(arabic), after))
        if len(new) <= limit:
            return new
        return _cut(_tidy(_join(before, q.ref.label(arabic))), limit)
    # the verse's opening words, «…» after its closing bracket
    op, cl = q.open or "﴿", q.close or "﴾"
    inner = title.find(q.text, q.start)
    lead = title[: max(q.start, inner - len(q.open))] if inner >= 0 else before
    head = _tidy(lead) + " " if lead.strip() else ""
    budget = limit - len(head) - len(op) - len(cl) - 1
    words = q.text.split()
    kept: list[str] = []
    for w in words:
        if len(" ".join([*kept, w])) > budget:
            break
        kept.append(w)
    if len(kept) < 2:
        return _cut(_tidy(before) or title, limit)
    return f"{head}{op}{' '.join(kept)}{cl}" + ("…" if len(kept) < len(words) else "")


def clean_title(
    raw: str,
    cfg: DisplayTitleConfig | None = None,
    *,
    brands: Sequence[str] = (),
    boilerplate: frozenset[str] = frozenset(),
) -> Cleaned:
    """The on-air title of one library title (see the module doc). ``brands``: folded
    phrases to drop (:func:`brand_phrases`); ``boilerplate``: folded segments to drop
    (:func:`title_boilerplate`)."""
    cfg = cfg or DisplayTitleConfig()
    episode, raw = _lead_number(raw)
    s = _prep(raw, cfg)
    kept: list[str] = []
    for i, seg in enumerate(_segments(s)):
        seg = _drop_credit(seg, i == 0)
        n, rest = _episode_in(seg)
        if n is not None and episode is None:
            episode = n
            seg = rest
        if _is_noise(seg, brands, boilerplate):
            continue
        seg = _strip_phrase(seg, brands)
        if _is_noise(seg, brands, boilerplate):
            continue
        seg = _tidy(seg)
        if seg and fold(seg) not in {fold(k) for k in kept}:
            kept.append(seg)
    title = " - ".join(kept)
    title = _tidy(title) if title else ""
    return Cleaned(fit_title(title, cfg), episode, max(1, len(kept)))


def clean_series_name(name: str, cfg: DisplayTitleConfig | None = None,
                      *, brands: Sequence[str] = ()) -> str:  # fmt: skip
    """A series' display name: its name cleaned like a title (brands, artefacts), without
    an episode marker."""
    cfg = cfg or DisplayTitleConfig()
    c = clean_title(name, cfg, brands=brands)
    if c.title:
        return c.title
    # the name is itself one of the phrases (the series' own name): its first part
    segs = _segments(_prep(name, cfg))
    return fit_title(_tidy(segs[0]), cfg) if segs else ""


def episode_label(n: int | None, arabic: bool, cfg: DisplayTitleConfig | None = None) -> str:
    cfg = cfg or DisplayTitleConfig()
    if n is None or not cfg.keep_episode_number or n <= 0:
        return ""
    fmt = cfg.episode_label_ar if arabic else cfg.episode_label_en
    try:
        return fmt.format(n=n)
    except (KeyError, IndexError, ValueError):
        return f"{fmt} {n}".strip()


def verse_reference(title: str) -> str:
    """The reference of the first verse the title quotes («البقرة ١٨٣»), for the strip's
    last page; "" when there is none or the title already names the surah («سورة البقرة»)."""
    if not title or re.search(r"(?<!\w)سور[ةه]\s+\S", title):
        return ""
    from .quran import find_quotes

    for q in find_quotes(title):
        if q.ref is not None:
            return q.ref.label(is_arabic(title))
    return ""


def on_air_line(title: str, series: str = "", label: str = "") -> str:
    """The one line the strip shows: «من زاوية أخرى · الحلقة 45 — في الخنادق…»."""
    head = " · ".join(p for p in (series, label) if p)
    if head and title and fold(title) != fold(series):
        return f"{head} — {title}"
    return title or head


# ---------------------------------------------------------------- the language model


def _llm_limit(cfg: DisplayTitleConfig) -> int:
    """What the model is asked to stay within: ``max_length``, else 70 characters."""
    return cfg.max_length if cfg.max_length > 0 else 70


def needs_llm(raw: str, cleaned: Cleaned, cfg: DisplayTitleConfig) -> bool:
    """The rules left this title long (cut), in several parts, or empty while the library
    title was long: worth a rewrite by the model."""
    from .quran import find_quotes

    if find_quotes(cleaned.title):
        return False  # a quoted verse is kept word for word; the strip pages it
    if len(cleaned.title) > _llm_limit(cfg) or (
        cleaned.title.endswith("…") and len(raw) > cfg.max_length
    ):
        return True
    if cleaned.segments > 1:
        return True
    return not cleaned.title and len(raw.strip()) > 20


def llm_input_hash(raw: str, series: str, model: str, cfg: DisplayTitleConfig) -> str:
    return hashlib.sha256(
        json.dumps(
            [LLM_PROMPT_VERSION, raw, series, model, cfg.max_length], ensure_ascii=False
        ).encode()  # fmt: skip
    ).hexdigest()


def llm_prompt(items: Sequence[tuple[str, str]], cfg: DisplayTitleConfig) -> str:
    """One question for a batch of ``(library title, series name)``."""
    rows = [{"n": i, "title": t, "series": s} for i, (t, s) in enumerate(items, 1)]
    return (
        "أنت محرر عناوين لقناة تلفزيونية. لكل عنوان فيديو أدناه اكتب عنواناً قصيراً نظيفاً "
        f"للعرض على الشاشة (لا يزيد عن {_llm_limit(cfg)} حرفاً) بنفس لغة العنوان: احذف اسم "
        "القناة والمقدم واسم البرنامج (series) ورقم الحلقة والوسوم والرموز التعبيرية "
        "والروابط والتواريخ وكلمات الجودة، ولا تضف معلومة ليست في العنوان. "
        "You are a TV title editor: for each video title write a short clean on-air title "
        f"(at most {_llm_limit(cfg)} characters) in the title's language, without channel, "
        "presenter or programme names, episode numbers, hashtags, emojis, links, dates or "
        'quality words. Answer JSON only: {"titles": [{"n": 1, "title": "..."}]}\n'
        + json.dumps({"titles": rows}, ensure_ascii=False)
    )


def parse_llm(
    answer: str | Mapping[str, Any], count: int, cfg: DisplayTitleConfig
) -> dict[int, str]:
    """The model's titles by number (1..count); an empty, missing or over-long answer is
    left out (the rules' title stands)."""
    try:
        data = json.loads(answer) if isinstance(answer, str) else dict(answer)
    except (ValueError, TypeError):
        return {}
    rows = data.get("titles") if isinstance(data, dict) else None
    if not isinstance(rows, list):
        return {}
    out: dict[int, str] = {}
    for row in rows:
        if not isinstance(row, dict):
            continue
        try:
            i = int(str(row.get("n")).strip())
        except ValueError:
            continue
        t = _tidy(_drop_emoji(str(row.get("title") or "")))
        if 1 <= i <= count and t and len(t) <= _llm_limit(cfg) * 1.2:
            out[i] = _cut(t, _llm_limit(cfg))
    return out
