"""Structure of YouTube-style episode titles (Arabic-first).

Unlike scene filenames (:mod:`.title_parser`), YouTube series titles carry
their structure as free text separated by ``|`` / ``｜`` / ``-`` / ``:``:

    نبيل العوضي ｜ قصة موسى عليه السلام الجزء التاسع
    رحمة الأنبياء مع أهلهم #الراحمون حلقة 25 الشيخ نبيل العوضي
    الحبيب ｜ البيعتان ｜ حصريا على #تلفاز_نت
    قصص الأنبياء (الموسم الثاني)

This module extracts, without any corpus or I/O:

- :func:`season_number` — ``الموسم الثاني`` / ``الموسم 2`` / ``season 2`` / ``S2``
- :func:`episode_number` — ``الحلقة 25`` / ``الحلقة الأولى`` / ``الجزء التاسع`` /
  ``(12)`` / a leading ``12 -`` / ``#7``
- :func:`year_marker` — ``رمضان 1445`` / ``2024`` (Gregorian or Hijri → Gregorian)
- :func:`is_teaser` — promos, "next episode" trailers, coming-soon clips
- :func:`segments` — the separator-delimited parts, folded; constant parts
  across a channel (presenter name, series name, "exclusive on …") are what
  identify a template (the corpus side lives in :mod:`.series_detector`)
- :func:`strip_markers` — the title without season/episode/year markers, for
  comparing series names (``قصص الأنبياء رمضان 2024`` ~ ``قصص الأنبياء (الموسم الثاني)``)

Pure and deterministic; ``TEMPLATE_VERSION`` bumps when rules change.
"""

from __future__ import annotations

import re

from ..text.arabic import fold

__all__ = [
    "TEMPLATE_VERSION",
    "episode_number",
    "is_teaser",
    "season_number",
    "segments",
    "strip_markers",
    "year_marker",
]

TEMPLATE_VERSION: int = 1

# --- Arabic ordinals (folded forms: ة→ه, ى→ي, hamza→ا) ---------------------

_UNITS_M = [
    "الاول",
    "الثاني",
    "الثالث",
    "الرابع",
    "الخامس",
    "السادس",
    "السابع",
    "الثامن",
    "التاسع",
    "العاشر",
]
_UNITS_F = [
    "الاولي",
    "الثانيه",
    "الثالثه",
    "الرابعه",
    "الخامسه",
    "السادسه",
    "السابعه",
    "الثامنه",
    "التاسعه",
    "العاشره",
]
_COMPOUND_M = [
    "الحادي",
    "الثاني",
    "الثالث",
    "الرابع",
    "الخامس",
    "السادس",
    "السابع",
    "الثامن",
    "التاسع",
]
_COMPOUND_F = [
    "الحاديه",
    "الثانيه",
    "الثالثه",
    "الرابعه",
    "الخامسه",
    "السادسه",
    "السابعه",
    "الثامنه",
    "التاسعه",
]
_TENS = {
    "العشرون": 20,
    "العشرين": 20,
    "الثلاثون": 30,
    "الثلاثين": 30,
    "الاربعون": 40,
    "الاربعين": 40,
}


def _ordinal_table() -> list[tuple[str, int]]:
    t: dict[str, int] = {}
    for i, (m, f) in enumerate(zip(_UNITS_M, _UNITS_F, strict=True), 1):
        t[m] = t[f] = i
    t["الاوله"] = 1
    t["الاخير"] = t["الاخيره"] = 9999  # "the last" — sorts after every numbered episode
    for i, (m, f) in enumerate(zip(_COMPOUND_M, _COMPOUND_F, strict=True), 1):
        t[f"{m} عشر"] = t[f"{f} عشره"] = t[f"{f} عشر"] = 10 + i
        for tens_word, tens in _TENS.items():
            t[f"{m} و{tens_word}"] = t[f"{f} و{tens_word}"] = tens + i
            t[f"{m} و {tens_word}"] = t[f"{f} و {tens_word}"] = tens + i
    for tens_word, tens in _TENS.items():
        t[tens_word] = tens
    # longest first so "الحادي عشر" wins over "الحادي"
    return sorted(t.items(), key=lambda kv: -len(kv[0]))


_ORDINALS = _ordinal_table()
_ORD_ALT = "|".join(re.escape(k) for k, _ in _ORDINALS)
_ORD_VALUE = dict(_ORDINALS)

_EP_WORDS = (
    r"(?:الحلقه|حلقه|الحلقات|الدرس|درس|المجلس|مجلس|المحاضره|محاضره|الجزء|جزء|اللقاء|لقاء"
    r"|episode|ep|part|lesson)"
)
_EP_NUM_RE = re.compile(rf"(?<!\w){_EP_WORDS}\s*(?:رقم\s*)?(\d{{1,4}})(?!\d)")
_EP_ORD_RE = re.compile(rf"(?<!\w){_EP_WORDS}\s+({_ORD_ALT})(?!\w)")
_SEASON_NUM_RE = re.compile(r"(?<!\w)(?:الموسم|موسم|season|s)\s*(\d{1,2})(?!\d)")
_SEASON_ORD_RE = re.compile(rf"(?<!\w)(?:الموسم|موسم)\s+({_ORD_ALT})(?!\w)")
_YEAR_RE = re.compile(r"(?<!\d)(19[89]\d|20[0-4]\d|14[0-6]\d)(?!\d)")

_RAW_PAREN_NUM_RE = re.compile(r"[(\[（]\s*([0-9٠-٩]{1,4})\s*[)\]）]")
_RAW_LEADING_NUM_RE = re.compile(r"^\s*([0-9٠-٩]{1,4})\s*[-–—.)ـ]")
_RAW_HASH_NUM_RE = re.compile(r"#\s*([0-9٠-٩]{1,4})(?![0-9٠-٩])")
_AR_DIGITS = str.maketrans("٠١٢٣٤٥٦٧٨٩", "0123456789")

_SEP_RE = re.compile(r"\s*(?:[|｜¦⧸/\\:：–—]|\s-\s|\s-|-\s|\(|\)|（|）|\[|\]|«|»)\s*")

_TEASER_WORDS = (
    "برومو", "promo", "trailer", "تريلر", "اعلان", "تشويقي", "ترقبوا", "انتظرونا",
    "الحلقه القادمه", "قريبا", "قريبًا", "coming soon", "teaser",
)  # fmt: skip
_TEASER_FOLDED = tuple(fold(w) for w in _TEASER_WORDS)


def season_number(title: str) -> int | None:
    t = fold(title)
    if m := _SEASON_NUM_RE.search(t):
        return int(m.group(1))
    if m := _SEASON_ORD_RE.search(t):
        v = _ORD_VALUE[m.group(1)]
        return v if v < 100 else None
    return None


def episode_number(title: str) -> int | None:
    """Episode/part number, or ``None``. "The last episode" → 9999."""
    t = fold(title)
    if m := _EP_NUM_RE.search(t):
        return int(m.group(1))
    if m := _EP_ORD_RE.search(t):
        return _ORD_VALUE[m.group(1)]
    raw = title or ""
    for rx in (_RAW_PAREN_NUM_RE, _RAW_LEADING_NUM_RE, _RAW_HASH_NUM_RE):
        if m := rx.search(raw):
            n = int(m.group(1).translate(_AR_DIGITS))
            if not _is_year(n):
                return n
    return None


def _is_year(n: int) -> bool:
    return 1980 <= n <= 2049 or 1400 <= n <= 1469


def year_marker(title: str) -> int | None:
    """Gregorian year mentioned in the title (Hijri years converted, ±1)."""
    m = _YEAR_RE.search(fold(title))
    if not m:
        return None
    y = int(m.group(1))
    return y if y >= 1980 else round(y * 0.970224 + 621.5774)


def is_teaser(title: str, duration_s: float | None, *, typical_s: float | None = None) -> bool:
    """Promo / next-episode trailer / coming-soon clip rather than an episode.

    A teaser word alone is not enough for a full-length video (a real episode
    may say "انتظرونا" in its title); it must also be short — under 3 minutes,
    or under a quarter of the series' typical duration when that is known.
    """
    t = fold(title)
    if not any(w in t for w in _TEASER_FOLDED):
        return False
    if duration_s is None:
        return True
    limit = 180.0 if typical_s is None else max(60.0, min(180.0, typical_s * 0.25))
    return duration_s < limit


_RECAP_WORDS = ("ومضات", "ملخص", "recap", "highlights", "best of")
_RECAP_FOLDED = tuple(fold(w) for w in _RECAP_WORDS)


def is_recap(title: str, series_key: str = "") -> bool:
    """Highlights / recap compilation of a season ("ومضات الموسم الأول"), at any length.

    Not when the word is part of the series name itself (a show called "ومضات").
    """
    t = fold(title)
    return any(w in t and w not in series_key for w in _RECAP_FOLDED)


def segments(title: str) -> list[str]:
    """Separator-delimited parts of the title, folded, empties dropped."""
    return [s for s in (fold(p) for p in _SEP_RE.split(title or "")) if s]


def strip_markers(title: str) -> str:
    """Folded title without season / episode / year markers and teaser words."""
    t = fold(title)
    t = _SEASON_ORD_RE.sub(" ", _SEASON_NUM_RE.sub(" ", t))
    t = _EP_ORD_RE.sub(" ", _EP_NUM_RE.sub(" ", t))
    t = _YEAR_RE.sub(" ", t)
    t = re.sub(r"(?<!\w)(?:رمضان|ramadan)(?!\w)", " ", t)
    return " ".join(t.split())
