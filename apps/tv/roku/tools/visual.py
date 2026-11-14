"""Roku's visual text, the reference (story 58.15).

Roku draws text left to right, one glyph per character, with no Arabic shaping and no
bidirectional reordering. Arabic must reach a `Label` already joined (presentation forms) and
in display order. This module is the reference both Roku paths are held to:

- the build step (`shape_strings.py`) shapes the static strings of `i18n/ar.json` with it;
- the device's `VisualText` (`source/visualtext.brs`) is checked against it on
  `shared/i18n/visual-vectors.json`, whose expected outputs `gen_vectors.py` writes from here.

The steps, the same on both sides:

1. Normalise: tab, CR, LF and the line and paragraph separators become spaces; default-ignorable
   format characters (bidi class BN: soft hyphen, ZWSP, BOM...) are removed.
2. Gate: a string holding a character the device cannot shape or order (an Arabic-script letter
   outside the shaping table, such as Persian peh or Urdu letters; Hebrew; ZWJ or ZWNJ; the
   explicit embedding and isolate controls) is left in logical order, untouched but for the
   width limit. The device logs it.
3. Tashkeel (harakat) is removed: Roku has no mark positioning, so a mark would sit on the
   wrong letter once the line is reversed. Text without marks reads the same.
4. Width: a line longer than its width (in characters, after steps 1 to 3) is cut and ends with
   an ellipsis, which the reordering puts at the visual end (the left, in Arabic).
5. Arabic joining with lam-alef ligatures (`arabic-reshaper`, limited to the shaping table and
   to the four lam-alef ligatures).
6. UAX #9 for one line: `python-bidi` resolves the levels (weak and neutral types, bracket
   pairs) and its display order is checked; then L4, mirroring (`(` drawn as `)` on an odd
   level), which python-bidi leaves to the renderer and Roku's renderer does not do. The
   marks LRM, RLM and ALM steer the order and are then removed: Roku's font would draw them
   as boxes.

The shaping table is the reshaper's letters whose forms are all in Arabic Presentation
Forms-B (U+FE70-U+FEFF), plus alef maksura (U+0649), whose initial and medial forms are in
Forms-A and in IBM Plex Sans Arabic, the font the channel bundles. Persian and Urdu letters, whose forms are only
in Forms-A, are outside it (story 58.15's fallback).

    visual_text(s, max_width=0, base="auto") -> str          # one line
    visual_lines(s, width, max_lines, base="auto") -> [str]   # wrapped, each line reordered
"""

from __future__ import annotations

import re
import unicodedata
from collections.abc import Iterable

import arabic_reshaper
from arabic_reshaper import ligatures as _ligatures
from arabic_reshaper.arabic_reshaper import HARAKAT_RE
from arabic_reshaper.letters import LETTERS_ARABIC
from bidi import get_display
from bidi.mirror import MIRRORED

ELLIPSIS = "…"
ALEF_MAKSURA = "ى"
TATWEEL = "ـ"
LAM_ALEF = (
    "ARABIC LIGATURE LAM WITH ALEF",
    "ARABIC LIGATURE LAM WITH ALEF WITH HAMZA ABOVE",
    "ARABIC LIGATURE LAM WITH ALEF WITH HAMZA BELOW",
    "ARABIC LIGATURE LAM WITH ALEF WITH MADDA ABOVE",
)
BASES = {"auto": None, "rtl": "R", "ltr": "L"}

# Whitespace that becomes a plain space (bidi classes S and B, and LF/CR/TAB).
_TO_SPACE = {"\t", "\n", "\r", "\x0b", "\x0c", "\x1c", "\x1d", "\x1e", "\x1f", "\x85", " ", " "}
# Steering marks: kept for the ordering, removed from the output.
_MARKS = {"‎", "‏", "؜"}
# Format characters the device cannot honour: the whole string stays logical.
_GATED_FORMAT = {"‌", "‍"} | {chr(c) for c in range(0x202A, 0x202F)} | {chr(c) for c in range(0x2066, 0x206A)}
# Arabic-block characters that are not letters but are drawn as they are (digits, punctuation).
_ARABIC_PLAIN = (
    {chr(c) for c in range(0x0660, 0x066E)}
    | {chr(c) for c in range(0x06F0, 0x06FA)}
    | {
        "،",  # ، comma
        "؛",  # ؛ semicolon
        "؟",  # ؟ question mark
        "۔",  # ۔ full stop
    }
)


def _in_table(forms: Iterable[str], letter: str) -> bool:
    return all((not f) or f == letter or 0xFE70 <= ord(f) <= 0xFEFF for f in forms)


# The shaping table: letter -> (isolated, initial, medial, final), "" where there is none.
TABLE: dict[str, tuple[str, str, str, str]] = {
    k: tuple(v)  # type: ignore[misc]
    for k, v in LETTERS_ARABIC.items()
    if 0x0600 <= ord(k) <= 0x06FF and (_in_table(v, k) or k == ALEF_MAKSURA)
}


def is_harakah(ch: str) -> bool:
    return bool(HARAKAT_RE.match(ch)) and 0x0600 <= ord(ch) <= 0x06FF


def _arabic_script(cp: int) -> bool:
    """Hebrew and the Arabic-script blocks: the device knows only what TABLE and _ARABIC_PLAIN hold."""
    return 0x0590 <= cp <= 0x08FF or 0xFB1D <= cp <= 0xFDFF or 0xFE70 <= cp <= 0xFEFF


def unsupported(s: str) -> str | None:
    """The first character that keeps `s` in logical order, or None."""
    for ch in s:
        if ch in _GATED_FORMAT:
            return ch
        cp = ord(ch)
        if (
            _arabic_script(cp)
            and ch not in TABLE
            and ch not in _ARABIC_PLAIN
            and ch not in _MARKS
            and not is_harakah(ch)
        ):
            return ch
    return None


def normalise(s: str) -> str:
    out = []
    for ch in s:
        if ch in _TO_SPACE:
            out.append(" ")
        elif ch in _GATED_FORMAT or ch in _MARKS:
            out.append(ch)  # gated or kept for the ordering
        elif unicodedata.bidirectional(ch) == "BN":
            continue
        else:
            out.append(ch)
    return "".join(out)


def strip_harakat(s: str) -> str:
    return "".join(ch for ch in s if not is_harakah(ch))


def ellipsize(s: str, width: int) -> str:
    if width <= 0 or len(s) <= width:
        return s
    if width == 1:
        return ELLIPSIS
    return s[: width - 1].rstrip(" ") + ELLIPSIS


def _reshaper() -> arabic_reshaper.ArabicReshaper:
    config: dict[str, object] = {
        "language": "Arabic",
        "delete_harakat": True,
        "delete_tatweel": False,
        "support_zwj": False,
        "support_ligatures": True,
    }
    for group in (_ligatures.SENTENCES_LIGATURES, _ligatures.WORDS_LIGATURES, _ligatures.LETTERS_LIGATURES):
        for name, _ in group:
            config[name] = name in LAM_ALEF
    return arabic_reshaper.ArabicReshaper(configuration=config)


_RESHAPER = _reshaper()


def shape(s: str) -> str:
    """Arabic joining and lam-alef ligatures, in logical order."""
    return _RESHAPER.reshape(s)


def first_strong(s: str) -> str:
    """'rtl' or 'ltr' by the first strong character (UAX #9 P2, P3); 'ltr' when there is none."""
    for ch in s:
        t = unicodedata.bidirectional(ch)
        if t == "L":
            return "ltr"
        if t in ("R", "AL"):
            return "rtl"
    return "ltr"


_LEVELS = re.compile(r"levels: \[(.*?)\]", re.S)
_PARAGRAPH_LEVEL = re.compile(r"ParagraphInfo \{.*?level: Level\(\s*(\d+)", re.S)
# Characters L1 puts back at the paragraph level at the end of a line (and before none other here:
# a line holds no segment or paragraph separator once normalised).
_L1 = {"WS", "BN", "S", "B", "LRI", "RLI", "FSI", "PDI"}


def levels(s: str, base: str) -> tuple[int, list[int]]:
    """python-bidi's paragraph level and resolved level of each character (UAX #9 up to I2).

    The Rust core reports a level per UTF-8 byte (`get_display(debug=True)`); each character takes
    its first byte's."""
    info = get_display(s, base_dir=BASES[base], debug=True)
    assert isinstance(info, str)
    per_byte = [int(n) for n in re.findall(r"Level\(\s*(\d+)", _LEVELS.search(info).group(1))]  # type: ignore[union-attr]
    para = _PARAGRAPH_LEVEL.search(info)
    out: list[int] = []
    offset = 0
    for ch in s:
        out.append(per_byte[offset])
        offset += len(ch.encode("utf-8"))
    assert offset == len(per_byte), "python-bidi's debug levels changed shape"
    return (int(para.group(1)) if para else 0), out


def _order(s: str, base: str) -> str:
    """UAX #9 for one line: python-bidi's levels, L1 and L2 (checked against its own display
    order), then L4: a character with a mirrored glyph on an odd level is drawn mirrored, which
    python-bidi's display order leaves to the renderer and Roku's renderer does not do."""
    if not s:
        return s
    para, lv = levels(s, base)
    i = len(s)
    while i > 0 and unicodedata.bidirectional(s[i - 1]) in _L1:  # L1: trailing whitespace
        i -= 1
        lv[i] = para
    order = list(range(len(s)))
    top = max(lv)
    low = min(x for x in lv if x % 2) if any(x % 2 for x in lv) else top + 1
    for level in range(top, low - 1, -1):  # L2
        j = 0
        while j < len(order):
            if lv[order[j]] >= level:
                k = j
                while k < len(order) and lv[order[k]] >= level:
                    k += 1
                order[j:k] = reversed(order[j:k])
                j = k
            else:
                j += 1
    plain = "".join(s[k] for k in order)
    assert plain == get_display(s, base_dir=BASES[base]), f"L1/L2 disagree with python-bidi on {s!r}"
    drawn = "".join(MIRRORED.get(s[k], s[k]) if lv[k] % 2 else s[k] for k in order)
    return "".join(ch for ch in drawn if ch not in _MARKS)


def visual_text(s: str, max_width: int = 0, base: str = "auto") -> str:
    """One line of `s` in display order, joined, cut to `max_width` characters (0: no limit)."""
    s = normalise(s)
    if unsupported(s) is not None:
        return ellipsize(s, max_width)
    s = ellipsize(strip_harakat(s), max_width)
    return _order(shape(s), base)


def wrap(s: str, width: int) -> list[str]:
    """Greedy word wrap to `width` characters, in logical order; a longer word is split."""
    lines: list[str] = []
    cur = ""
    for word in (w for w in s.split(" ") if w):
        while len(word) > width:
            if cur:
                lines.append(cur)
                cur = ""
            lines.append(word[:width])
            word = word[width:]
        if not word:
            continue
        if not cur:
            cur = word
        elif len(cur) + 1 + len(word) <= width:
            cur += " " + word
        else:
            lines.append(cur)
            cur = word
    if cur:
        lines.append(cur)
    return lines


def fit_lines(lines: list[str], width: int, max_lines: int) -> list[str]:
    """At most `max_lines` lines, the last ending with an ellipsis when text was left over."""
    if max_lines <= 0 or len(lines) <= max_lines:
        return lines
    rest = " ".join(lines[max_lines - 1 :])
    return lines[: max_lines - 1] + [ellipsize(rest, width)]


def visual_lines(s: str, width: int, max_lines: int = 0, base: str = "auto") -> list[str]:
    """`s` wrapped to `width` before reordering, then each line in display order.

    Lines are broken in logical order, so the first line holds the start of the text; every line
    takes the paragraph direction of the whole text (`base`, or its first strong character)."""
    s = normalise(s)
    if unsupported(s) is not None:
        return fit_lines(wrap(s, width), width, max_lines)
    s = strip_harakat(s)
    direction = first_strong(s) if base == "auto" else base
    return [_order(shape(line), direction) for line in fit_lines(wrap(s, width), width, max_lines)]
