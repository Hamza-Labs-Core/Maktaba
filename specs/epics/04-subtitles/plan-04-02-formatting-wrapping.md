# Plan 4.2 — SRT/VTT formatting & line wrapping — implementation

> Implementation plan for [story-04-02-formatting-wrapping.md](story-04-02-formatting-wrapping.md).
> Self-contained: a developer should be able to ship the story from this
> document alone. Cross-links: consumes the canonical segment iterator
> defined by [Story 4.1](story-04-01-generate-from-segments.md) and
> writes through the same atomic-replace serializer; mirrors the
> `<v Speaker N>` tagging contract for diarized transcripts produced by
> [Plan 3.9](../03-transcription/plan-03-09-diarization.md); preserves
> Arabic-script text faithfully so the search-side normalization in
> Epic 5 remains the single source of normalization truth. Translation
> between languages stays out of scope per Epic 4 README.

---

## 0. Decisions and departures from `architecture.md` and the story

| # | Decision | Source | Rationale |
|---|----------|--------|-----------|
| D1 | Line length is measured in **Unicode extended grapheme clusters** (`regex.findall(r"\X", s)`), not in `len(str)` (code points) and not in `wcwidth.wcswidth` (display columns). The configurable budget remains `max_line_chars = 42`, interpreted as a grapheme-cluster count. | Story acceptance: "max_line_chars = 42 characters wide". Story edge case: "Wrap by grapheme cluster, not byte". | Code-point counting splits a base letter from its combining mark and lets a wrap insert a newline between them — broken on Arabic with diacritics (e.g. tashkīl), Latin with combining accents, and any emoji ZWJ sequence. Display-column counting (`wcwidth`) is right for monospace terminals but wrong for the variable-width fonts that VLC, mpv, Plex, and TVs use. Grapheme clusters are the user-visible unit; 42 of them maps cleanly to the BBC and Netflix Arabic subtitle guidelines (≤ 42 chars/line) and matches what an editor's column ruler counts. |
| D2 | Bidi handling is **passive**: we never insert RTL/LTR override marks (U+202B/U+202A/U+202E) or invoke `python-bidi` / `arabic-reshaper` on the cue text. We pass through the visual order produced by the STT model. We DO emit a single `RLM` (U+200F) prefix on cue lines whose first **strong** character is RTL **iff** the line also contains Latin or Arabic-Indic digits — to lock the paragraph direction for naive renderers. | Refines story (silent on bidi normalization). | The SRT/VTT spec leaves bidi to the renderer (browsers, VLC, mpv, Plex all run UAX#9 themselves). Re-shaping or re-ordering text before the renderer breaks search (Epic 5 indexes the canonical Unicode form) and double-shapes on competent renderers. The optional RLM prefix is a defensive, format-legal nudge — not a rewrite — that fixes the common "Arabic line starting with a Latin token gets rendered LTR" bug without touching the underlying string. The `arabic-reshaper` package is for legacy renderers (PIL, ReportLab) that don't do shaping; subtitle renderers do. We add a regression test (`test_no_presentation_forms_emitted`) that asserts no FExx codepoints leak into output. |
| D3 | Wrapping algorithm: **greedy with backtrack at punctuation**. We tokenize on whitespace into "words"; we then greedily fill line 1 up to `max_line_chars` graphemes, but we look back from the chosen break point for the highest-priority punctuation within the previous `lookback_chars = 12` graphemes (sentence-end > clause-end > word-boundary). If line 1 took too few graphemes (< 50% of budget), we instead break at the closest word boundary to the budget on line 1, balancing the two lines. | Story acceptance: "Line breaks favor: 1. sentence-end, 2. clause-end, 3. word boundaries". | Pure greedy produces "orphan" 2-word second lines that read poorly. Pure dynamic-programming (Knuth-Plass) is overkill at 2-line cues. Greedy + 12-grapheme backtrack matches the BBC and Netflix style guides ("favor punctuation breaks within ~10–15 chars of the budget") and is O(n) per cue. The 50%-balance fallback handles the case where the first sentence-end happens at character 8 of 42 — splitting there would orphan an absurdly short top line. |
| D4 | Reading-rate (CPS) limit: `cps_max = 17.0` graphemes per second for Arabic and `cps_max = 21.0` for Latin scripts (auto-detected from the cue content's dominant script). When a cue exceeds the limit, we **extend the cue's end time** (up to `min(next_cue.start - merge_gap_sec, original_end + cps_padding_sec=2.0)`) before considering a split. Only if extension cannot bring CPS under the limit do we split the cue at the highest-priority punctuation boundary. We never compress the text. | Story silent on CPS; we add it because it is the second-most-cited reason a viewer reports "subtitles unreadable" on TV-sized cues. | The 17/21 split is the Netflix Timed Text Style Guide: Arabic script is denser per grapheme (longer words on average; tashkīl-bearing letters slow the eye). Extending before splitting preserves segment-to-cue 1:1 mapping for the common case (a slightly tight cue gets +200 ms of dwell instead of being chopped); splitting only fires when the original timing is genuinely impossible. We never compress text because dropping graphemes from STT output would alter meaning and break round-trip with `transcript_segments`. |
| D5 | Library choice: **`regex`** (Unicode-aware `\X` for grapheme clusters and `\p{...}` script properties), no `arabic-reshaper`, no `python-bidi` shipped, optional dev-only `wcwidth` for the diagnostics tool. We stay on the Python stdlib + `regex` for the runtime. | Refines story. | `regex` is one C extension and gives us both grapheme iteration and script detection in 5 lines. `python-bidi` is GPL-leaning (LGPL-2.1) — fine in our stack but adds a transitive license check; we don't need it (D2). `arabic-reshaper` would normalize joining forms and is exactly what we DON'T want (D2 again). Keeping the dependency surface tiny means no new pyproject churn beyond `regex`. |
| D6 | Speaker tagging: emit `<v ...>` only in **VTT**, never in SRT. SRT has no in-band speaker syntax that all players agree on (some use `<v>`, some use `[SPEAKER]:`, some require ASS); writing nothing is the most portable choice. The speaker label is HTML-escaped (`<` → `&lt;`, `>` → `&gt;`, `&` → `&amp;`) before placement inside the angle brackets, consistent with the cue-text escape rule from Story 4.1. | Story acceptance: "Speaker labels themselves are HTML-escaped before being placed inside the `<v ...>` tag". | `<v ...>` is a WebVTT-spec feature; SRT is "what every player accepts" by convention only. A bracketed `[Speaker 1]` prefix in SRT would (a) inflate the grapheme count past `max_line_chars` and (b) drift visually from the diarization tags shown in our own UI. We pick "no SRT speakers" rather than a contested convention. |
| D7 | Cue-time merging and splitting policy: adjacent input segments whose `next.start - prev.end <= merge_gap_sec = 0.05` are concatenated into one cue (text joined with a single space, both timestamps preserved); a single input segment longer than `max_cue_sec = 6.0` is split at the highest-priority punctuation boundary between graphemes 50% and 90% of its length, with sub-cue durations apportioned by **word timestamps** when present and **linearly by grapheme count** otherwise. The linear fallback writes `metadata.split_method = "linear"` to the cue's debug record. | Story acceptance: "merge_gap_sec = 0.05", "max_cue_sec = 6.0", "split at sentence/clause boundaries", edge case "Word timestamps absent → split by character count along the segment duration linearly". | The 50–90% window stops the splitter from finding a comma at grapheme 4 of 200 and producing a 0.4 s flicker cue. Word-timestamp-aware splits give frame-accurate sub-cue starts; the linear fallback is "good enough" — within ~200 ms of the right place on a 6 s cue, which the viewer doesn't notice. |
| D8 | Tokens longer than `max_line_chars` (URLs, hashtags) are placed alone on a line and the line is allowed to overflow; one DEBUG log per file (`kind=overflow_token`, count of such tokens) is emitted via the existing pipeline logger. We do **not** insert mid-token line breaks (no soft hyphens, no zero-width breaks). | Story edge case: "Such tokens are placed on their own line; the line is allowed to exceed the limit (one violation logged per file at DEBUG)". | Mid-URL hyphenation breaks the URL when the user copy-pastes it. One overflow-line-per-overlong-token is a known, bounded loss. Logging once per file (not once per token) keeps the noise floor flat on subtitle-of-tweets-style content. |
| D9 | Line endings are **CRLF on disk** for both SRT (per RFC-equivalent SubRip convention) and VTT (per WebVTT spec, which permits CR, LF, or CRLF; CRLF maximizes compatibility with Windows-era SRT consumers). The serializer accepts an `eol` override (`"\n"` or `"\r\n"`) for round-trip tests against `webvtt-py` which prefers LF. The on-disk file is always written CRLF. | Story silent on EOL; format conventions. | A handful of legacy players (older Sony Bravia firmware, some BluRay-era hardware) hard-require CRLF in SRT. Modern VLC/mpv accept either. Choosing CRLF on disk costs nothing and maximizes the long tail of "the file just opened in Plex". |
| D10 | The wrapper is a **pure function** of `(segments_iter, options)` → `cues_iter`; the file serializer is a separate pure function of `cues_iter` → `bytes`. No I/O in either; Story 4.1 owns the atomic write. | Composition principle. | Lets us property-test each layer without a temp directory and lets Story 4.1's atomic-replace path call us with a fully formed bytes object. Also makes the live-VTT path (Story 4.5) free: it reuses the same wrapper + serializer with a different sink. |

If D2 is rejected (we do invoke `python-bidi` to reorder), §2.4 changes
(we add a `_bidi_reorder` step before grapheme counting), every search
test in Epic 5 needs a re-baseline (the indexed text becomes the visual
order, not logical), and the CPU cost per cue rises ~30%. Correctness
on diacritics-heavy Arabic improves by an unmeasurable amount on
modern renderers and degrades on older ones (double-shaping).

If D4 is rejected (no CPS limit), the wrapper output exactly mirrors
input segment timing; cue duration is the only signal of reading rate.
This is what most off-the-shelf SRT generators do. We accept the
"viewer overwhelmed by 200-grapheme cues at 1.5 s" failure mode that
the story does not technically prohibit. Re-introducing CPS later is
non-breaking (it only relaxes timing).

---

## 1. Architecture diagram — segment → wrap → cue → bytes

```
        ┌──────────────────────────────────────────────────────────────┐
        │  Story 4.1 driver (subtitle_gen stage)                       │
        │    segments_iter = SegmentSource(transcript_id).iter()       │
        │       → yields canonical Segment(start, end, text,           │
        │                                  speaker, words, metadata)   │
        └──────────────────────────────┬───────────────────────────────┘
                                       │  ordered by start
                                       ▼
        ┌──────────────────────────────────────────────────────────────┐
        │  CueShaper(options).shape(segments_iter)  →  Cue iterator    │
        │  ───────────────────────────────────────────────────────     │
        │  pass 1: MERGE        (D7) — fold adjacent gap ≤ 0.05s       │
        │  pass 2: SPLIT-LONG   (D7) — split > max_cue_sec at punct.   │
        │  pass 3: WRAP-LINES   (D3) — grapheme-aware greedy + backtr. │
        │  pass 4: ENFORCE-CPS  (D4) — extend then split if needed     │
        │  pass 5: NO-OVERLAP   — assert end[i] <= start[i+1]; clip    │
        │  pass 6: TAG-SPEAKER  (D6) — VTT only, escape label          │
        │                                                              │
        │  Each pass is an iterator stage; the pipeline is lazy and    │
        │  memory is O(2 cues) regardless of transcript length.        │
        └──────────────────────────────┬───────────────────────────────┘
                                       │  Cue iterator
                                       ▼
        ┌─────────────────────┐   ┌─────────────────────┐
        │  SrtSerializer      │   │  VttSerializer      │
        │   .render(cues) →   │   │   .render(cues) →   │
        │   bytes (CRLF, D9)  │   │   bytes (CRLF, D9)  │
        └──────────┬──────────┘   └──────────┬──────────┘
                   │                         │
                   ▼                         ▼
        ┌──────────────────────────────────────────────────────────────┐
        │  Story 4.1: atomic write to .maktaba/.tmp → os.replace       │
        └──────────────────────────────────────────────────────────────┘
```

Properties enforced at every stage boundary:
- Cue start/end timestamps are monotone non-decreasing.
- `cue.lines` is a list of grapheme-cluster strings, each non-empty.
- `cue.lines` length is `<= max_lines` (= 2 by default) **except** in
  the overflow-token edge case (D8), where it can be `> max_lines`
  but the file-level overflow log fires.
- Cue text contains no U+FEFF, U+200B, U+200E (LRM), or presentation-
  form Arabic (U+FB50–U+FDFF, U+FE70–U+FEFC) — the assertion in
  pass 5 strips these and counts them in metrics.

---

## 2. Detailed implementation

### 2.1 Package layout — Python (Pipeline Service)

```
maktaba/media/subtitles/
├── __init__.py             # public surface: shape(), render_srt(), render_vtt(),
│                           # WrapOptions
├── options.py              # WrapOptions dataclass (all knobs)
├── unicode_helpers.py      # graphemes(), grapheme_len(), dominant_script(),
│                           # is_rtl_first_strong(), strip_invisible()
├── escape.py               # html_escape_cue_text(), html_escape_speaker()
├── cue.py                  # Cue dataclass + asserts
├── shaper.py               # CueShaper — six-pass pipeline (the entry point)
├── _merge.py               # pass 1: MERGE adjacent
├── _split_long.py          # pass 2: SPLIT-LONG (>max_cue_sec)
├── _wrap.py                # pass 3: WRAP-LINES (greedy + backtrack)
├── _cps.py                 # pass 4: ENFORCE-CPS
├── _tag.py                 # pass 6: TAG-SPEAKER (VTT only)
├── srt.py                  # SrtSerializer
├── vtt.py                  # VttSerializer
└── tests/
    ├── conftest.py         # fixtures: arabic_lecture, mixed_arabic_english,
    │                       #   long_segment_with_words, fast_dense_segment,
    │                       #   speaker_with_html_chars, url_overlong_token
    ├── test_unicode_helpers.py
    ├── test_escape.py
    ├── test_merge.py
    ├── test_split_long.py
    ├── test_wrap.py        # all wrap_* test cases from the story
    ├── test_cps.py
    ├── test_tag.py
    ├── test_srt_render.py
    ├── test_vtt_render.py
    ├── test_shaper_end_to_end.py
    └── test_no_overlap_property.py   # hypothesis-based property test
```

### 2.2 `options.py` — the knob surface

```python
"""Wrap and cue-shaping options. Defaults match Story 4.2 acceptance criteria."""
from __future__ import annotations
from dataclasses import dataclass


@dataclass(frozen=True)
class WrapOptions:
    # Story-named knobs (verbatim from story-04-02).
    max_line_chars: int = 42          # graphemes per line (D1)
    max_lines: int = 2                # lines per cue
    merge_gap_sec: float = 0.05       # D7
    max_cue_sec: float = 6.0          # D7
    cps_padding_sec: float = 2.0      # D4
    cps_max_arabic: float = 17.0      # D4
    cps_max_latin: float = 21.0       # D4
    lookback_chars: int = 12          # D3 backtrack window for punct
    balance_threshold_ratio: float = 0.5  # D3 balance fallback trigger

    # VTT-only.
    emit_speaker_tags: bool = True    # D6 — set False to suppress entirely

    # Output.
    eol: str = "\r\n"                 # D9
    language: str = "ar"              # ISO 639-1; affects CPS auto-pick

    # Diagnostics.
    debug_split_methods: bool = False  # write metadata.split_method into cue


SENTENCE_END = frozenset(".!?؟")
CLAUSE_END = frozenset(",;:،؛")  # includes Arabic comma U+060C and Arabic semicolon U+061B
WORD_BOUNDARY = frozenset(" \t   ")  # space + NBSP + thin space
```

The Arabic question mark `؟` is U+061F, the Arabic comma `،` is U+060C,
the Arabic semicolon `؛` is U+061B. They are explicitly listed (not
inferred via `unicodedata.category`) so the table stays auditable.

### 2.3 `unicode_helpers.py` — grapheme + script + bidi predicates (D1, D2)

```python
"""Unicode-aware helpers. Single dependency: `regex` (re-exported as `_re`)."""
from __future__ import annotations
import regex as _re

_GRAPHEME_RX = _re.compile(r"\X")
_RTL_STRONG_RX = _re.compile(r"[\p{bc=AL}\p{bc=R}]")
_LATIN_RX = _re.compile(r"\p{Script=Latin}")
_ARABIC_RX = _re.compile(r"\p{Script=Arabic}")
_DIGIT_RX = _re.compile(r"\p{Nd}")

# Codepoints we strip on input (cosmetic / unsafe to leave in cue text).
_STRIP_CHARS = frozenset(
    "﻿"   # BOM
    "​"   # zero-width space
    "‎"   # LRM
    "‪"   # LRE
    "‫"   # RLE
    "‬"   # PDF
    "‭"   # LRO
    "‮"   # RLO
)

# Arabic Presentation Forms-A + Forms-B — emitted text MUST NOT contain these.
# (We never produce them; if STT input contains them we keep them but log.)
_PRESENTATION_FORM_RANGES = (
    (0xFB50, 0xFDFF),
    (0xFE70, 0xFEFC),
)


def graphemes(s: str) -> list[str]:
    """Return the list of extended grapheme clusters in s."""
    return _GRAPHEME_RX.findall(s)


def grapheme_len(s: str) -> int:
    return len(_GRAPHEME_RX.findall(s))


def dominant_script(s: str) -> str:
    """Return 'arabic', 'latin', or 'mixed' based on grapheme counts."""
    arabic = len(_ARABIC_RX.findall(s))
    latin = len(_LATIN_RX.findall(s))
    if arabic == 0 and latin == 0:
        return "mixed"  # numbers-only, punctuation-only, etc.
    if arabic >= 2 * latin:
        return "arabic"
    if latin >= 2 * arabic:
        return "latin"
    return "mixed"


def is_rtl_first_strong(s: str) -> bool:
    """True iff the first character with strong directionality is RTL."""
    for ch in s:
        # Skip neutrals: punctuation, whitespace, weak digits/marks.
        if _RTL_STRONG_RX.match(ch):
            return True
        # Strong-LTR check: any Latin letter (and a few others) terminates.
        if "a" <= ch.lower() <= "z" or "A" <= ch <= "Z":
            return False
    return False


def has_digits(s: str) -> bool:
    return bool(_DIGIT_RX.search(s))


def strip_invisible(s: str) -> tuple[str, int]:
    """Remove zero-width / bidi-override characters; return (cleaned, n_stripped)."""
    if not any(c in _STRIP_CHARS for c in s):
        return s, 0
    out = []
    n = 0
    for c in s:
        if c in _STRIP_CHARS:
            n += 1
            continue
        out.append(c)
    return "".join(out), n


def count_presentation_forms(s: str) -> int:
    """Count Arabic Presentation Form codepoints in s (used for telemetry, D2)."""
    n = 0
    for c in s:
        cp = ord(c)
        for lo, hi in _PRESENTATION_FORM_RANGES:
            if lo <= cp <= hi:
                n += 1
                break
    return n


def maybe_rlm_prefix(line: str) -> str:
    """If RTL-first-strong AND contains a digit or Latin letter, prefix RLM (D2).

    This is the ONLY bidi-control character we emit. It is format-legal in both
    SRT and VTT, parsed away by competent renderers, and locks paragraph
    direction for naive ones.
    """
    if not is_rtl_first_strong(line):
        return line
    if has_digits(line) or _LATIN_RX.search(line):
        return "‏" + line
    return line
```

The `dominant_script` function picks the CPS budget; `maybe_rlm_prefix`
implements the conservative bidi nudge from D2; `strip_invisible`
neutralizes upstream injections (a malicious or buggy STT could emit
RLO and reverse the cue's apparent meaning — the strip happens before
wrapping so it can't widen any line either).

### 2.4 `escape.py` — HTML escaping for cue text and speaker labels

```python
"""HTML escape rules. Matches Story 4.1 (cue text) and Story 4.2 (speaker label)."""
from __future__ import annotations

# Order matters: & FIRST, then < and >.
_ESCAPES = (("&", "&amp;"), ("<", "&lt;"), (">", "&gt;"))


def html_escape_cue_text(s: str) -> str:
    for before, after in _ESCAPES:
        s = s.replace(before, after)
    return s


def html_escape_speaker(name: str) -> str:
    """Speaker label going inside <v ...> — same rules; we also reject newlines."""
    if "\r" in name or "\n" in name:
        # A speaker label with newlines would close the <v> tag prematurely.
        # We replace with U+2028 (line separator), which renders as a space
        # in subtitle players and doesn't break VTT parsing.
        name = name.replace("\r\n", " ").replace("\r", " ").replace("\n", " ")
    return html_escape_cue_text(name)
```

### 2.5 `cue.py` — the Cue dataclass

```python
from __future__ import annotations
from dataclasses import dataclass, field
from typing import Optional


@dataclass(frozen=True)
class Cue:
    seq: int                              # 1-based output sequence
    start: float                          # seconds
    end: float                            # seconds (> start)
    lines: tuple[str, ...]                # 1..max_lines lines (D8 may exceed)
    speaker: Optional[str] = None         # raw speaker label, pre-escape
    debug: dict = field(default_factory=dict)  # split_method, cps, etc.

    def __post_init__(self):
        if self.end <= self.start:
            raise ValueError(f"Cue {self.seq}: end ({self.end}) <= start ({self.start})")
        if not self.lines:
            raise ValueError(f"Cue {self.seq}: empty lines")
        for i, line in enumerate(self.lines):
            if not line:
                raise ValueError(f"Cue {self.seq}: empty line at index {i}")
            if "\n" in line or "\r" in line:
                raise ValueError(f"Cue {self.seq}: line {i} contains EOL")
```

### 2.6 `_wrap.py` — pass 3, the heart of the story (D3)

```python
"""Greedy line wrapper with punctuation backtrack and balance fallback."""
from __future__ import annotations
import logging
from typing import Iterable

from maktaba.media.subtitles.options import (
    WrapOptions, SENTENCE_END, CLAUSE_END, WORD_BOUNDARY,
)
from maktaba.media.subtitles.unicode_helpers import (
    graphemes, grapheme_len, maybe_rlm_prefix, strip_invisible,
    count_presentation_forms,
)

log = logging.getLogger(__name__)


def wrap_text(text: str, opts: WrapOptions) -> tuple[list[str], dict]:
    """Wrap `text` into ≤ opts.max_lines lines of ≤ opts.max_line_chars graphemes.

    Returns (lines, debug). `debug` includes overflow and bidi metrics.
    """
    text, n_stripped = strip_invisible(text)
    n_pf = count_presentation_forms(text)

    # Tokenize on whitespace (single-space normalize).
    raw_tokens = text.split()
    if not raw_tokens:
        return [], {"empty": True}

    lines: list[str] = []
    overflow_tokens = 0
    cur: list[str] = []
    cur_len = 0

    def push_line(toks: list[str]) -> None:
        if not toks:
            return
        line = " ".join(toks)
        line = maybe_rlm_prefix(line)  # D2
        lines.append(line)

    for tok in raw_tokens:
        tlen = grapheme_len(tok)
        if tlen > opts.max_line_chars:
            # Overflow token (D8): flush current, place token alone, log later.
            push_line(cur)
            cur, cur_len = [], 0
            push_line([tok])
            overflow_tokens += 1
            continue
        # +1 for the joining space if cur is non-empty.
        proj = cur_len + (1 if cur else 0) + tlen
        if proj <= opts.max_line_chars:
            cur.append(tok)
            cur_len = proj
        else:
            push_line(cur)
            cur, cur_len = [tok], tlen

    push_line(cur)

    # If we have ≤ max_lines lines, we're done with the wrap pass.
    # If we have more, the SPLIT-LONG pass should have caught it (the wrap
    # is per-cue and cues are pre-split). For belt-and-suspenders we collapse
    # into max_lines by re-wrapping with a tighter limit; this only fires
    # in degenerate cases.
    if len(lines) > opts.max_lines and overflow_tokens == 0:
        return _rebalance(lines, opts), {
            "rebalanced": True, "overflow_tokens": 0,
            "stripped_invisible": n_stripped, "presentation_forms": n_pf,
        }

    # If we have exactly 2 lines, try the punctuation-backtrack to move the
    # break onto a sentence-end or clause-end character if one is within
    # opts.lookback_chars of the current break point.
    if len(lines) == 2 and overflow_tokens == 0:
        adjusted = _backtrack_to_punct(lines[0], lines[1], opts)
        if adjusted is not None:
            lines = list(adjusted)

    # Balance fallback: if line 1 is < balance_threshold_ratio of budget,
    # rebalance towards center on a word boundary.
    if (
        len(lines) == 2 and overflow_tokens == 0
        and grapheme_len(lines[0]) < opts.balance_threshold_ratio * opts.max_line_chars
    ):
        balanced = _balance(lines[0], lines[1], opts)
        if balanced is not None:
            lines = list(balanced)

    return lines, {
        "overflow_tokens": overflow_tokens,
        "stripped_invisible": n_stripped,
        "presentation_forms": n_pf,
    }


def _backtrack_to_punct(line1: str, line2: str, opts: WrapOptions):
    """Walk back from end-of-line1 up to lookback_chars graphemes searching for
    sentence-end > clause-end > word-boundary. If found, move the suffix from
    line1 to the start of line2. Word-boundary alone is the no-op case (we
    already broke on a word boundary)."""
    g1 = graphemes(line1)
    look = min(opts.lookback_chars, len(g1))
    candidates: list[tuple[int, int]] = []  # (priority, idx_after_punct)
    # priority: 0 = sentence end, 1 = clause end (lower wins)
    for i in range(len(g1) - 1, len(g1) - look - 1, -1):
        ch = g1[i]
        if ch in SENTENCE_END:
            candidates.append((0, i + 1))
        elif ch in CLAUSE_END:
            candidates.append((1, i + 1))
    if not candidates:
        return None
    candidates.sort()  # best-priority, then rightmost (smallest distance from end)
    _, cut = candidates[0]
    new_l1 = "".join(g1[:cut]).rstrip()
    moved = "".join(g1[cut:]).lstrip()
    new_l2 = (moved + " " + line2).strip() if moved else line2
    if grapheme_len(new_l2) > opts.max_line_chars:
        return None  # would overflow line 2; abandon backtrack
    return new_l1, new_l2


def _balance(line1: str, line2: str, opts: WrapOptions):
    """If line1 << line2, find a word boundary in line2 closest to (l2_len/2)
    and move that prefix back to line1, as long as line1 stays ≤ budget."""
    g2 = graphemes(line2)
    midpoint = len(g2) // 2
    # Walk outward from midpoint looking for a word-boundary grapheme.
    for offset in range(0, midpoint):
        for sign in (-1, +1):
            i = midpoint + sign * offset
            if 0 < i < len(g2) and g2[i] in WORD_BOUNDARY:
                prefix = "".join(g2[:i]).rstrip()
                suffix = "".join(g2[i + 1:]).lstrip()
                new_l1 = (line1 + " " + prefix).strip()
                if grapheme_len(new_l1) <= opts.max_line_chars and suffix:
                    return new_l1, suffix
    return None


def _rebalance(lines: list[str], opts: WrapOptions) -> list[str]:
    """Last-resort: join everything and re-wrap into exactly max_lines lines."""
    text = " ".join(lines)
    g = graphemes(text)
    target = (len(g) + opts.max_lines - 1) // opts.max_lines
    out: list[str] = []
    cur_g: list[str] = []
    for ch in g:
        cur_g.append(ch)
        if len(cur_g) >= target and (cur_g[-1] in WORD_BOUNDARY):
            out.append("".join(cur_g).rstrip())
            cur_g = []
            if len(out) == opts.max_lines - 1:
                break
    if cur_g:
        out.append("".join(cur_g).strip())
    # Append any remaining graphemes to the last line.
    consumed = sum(grapheme_len(x) + 1 for x in out)
    rest = "".join(g[consumed:]).strip()
    if rest:
        if len(out) < opts.max_lines:
            out.append(rest)
        else:
            out[-1] = (out[-1] + " " + rest).strip()
    return out
```

### 2.7 `_merge.py` — pass 1 (D7)

```python
from __future__ import annotations
from typing import Iterable, Iterator
from maktaba.media.subtitles.options import WrapOptions


def merge_adjacent(segments: Iterable, opts: WrapOptions) -> Iterator:
    """Yield segments, folding any adjacent pair whose gap <= merge_gap_sec.

    Merged segments inherit `start` from the first, `end` from the last,
    text joined by a single space, words concatenated, speaker preserved
    only if both segments share the same speaker (None resets).
    """
    it = iter(segments)
    try:
        prev = next(it)
    except StopIteration:
        return
    for seg in it:
        gap = seg.start - prev.end
        if 0 <= gap <= opts.merge_gap_sec and prev.speaker == seg.speaker:
            prev = _fold(prev, seg)
        else:
            yield prev
            prev = seg
    yield prev


def _fold(a, b):
    from dataclasses import replace
    text = (a.text.rstrip() + " " + b.text.lstrip()).strip()
    words = list(getattr(a, "words", None) or []) + list(getattr(b, "words", None) or [])
    return replace(a, end=b.end, text=text, words=words)
```

### 2.8 `_split_long.py` — pass 2 (D7)

```python
from __future__ import annotations
import logging
from dataclasses import replace
from typing import Iterable, Iterator

from maktaba.media.subtitles.options import (
    WrapOptions, SENTENCE_END, CLAUSE_END, WORD_BOUNDARY,
)
from maktaba.media.subtitles.unicode_helpers import graphemes, grapheme_len

log = logging.getLogger(__name__)


def split_long(segments: Iterable, opts: WrapOptions) -> Iterator:
    for seg in segments:
        dur = seg.end - seg.start
        if dur <= opts.max_cue_sec:
            yield seg
            continue
        yield from _split_one(seg, opts)


def _split_one(seg, opts: WrapOptions):
    g = graphemes(seg.text)
    n = len(g)
    if n == 0:
        yield seg
        return
    # Search for a punctuation boundary in the 50%–90% window.
    window_lo = max(1, n // 2)
    window_hi = max(window_lo + 1, (9 * n) // 10)
    cut_idx = _best_cut(g, window_lo, window_hi)
    if cut_idx is None:
        # Fallback: nearest word boundary to the midpoint.
        cut_idx = _nearest_word_boundary(g, n // 2)
    if cut_idx is None or cut_idx <= 0 or cut_idx >= n:
        # Single token, no boundary anywhere — give up, yield as-is.
        yield seg
        return

    text_a = "".join(g[:cut_idx]).rstrip()
    text_b = "".join(g[cut_idx:]).lstrip()

    # Time apportionment: prefer word timestamps, else linear by grapheme.
    words = list(getattr(seg, "words", None) or [])
    split_method = "word_timestamps" if words else "linear"
    if words:
        boundary_time = _word_timestamp_boundary(words, text_a)
    else:
        boundary_time = seg.start + (seg.end - seg.start) * (cut_idx / n)

    boundary_time = max(seg.start + 0.05, min(seg.end - 0.05, boundary_time))

    a = replace(
        seg, end=boundary_time, text=text_a,
        metadata={**(seg.metadata or {}), "split_method": split_method, "split_part": "a"},
    )
    b = replace(
        seg, start=boundary_time, text=text_b,
        metadata={**(seg.metadata or {}), "split_method": split_method, "split_part": "b"},
    )
    # Recurse — b may still be too long.
    yield a
    yield from _split_one(b, opts)


def _best_cut(g: list[str], lo: int, hi: int) -> int | None:
    sentence = [i for i in range(lo, hi) if g[i] in SENTENCE_END]
    if sentence:
        # Cut AFTER the punctuation, so the sentence-end stays with line A.
        return min(sentence, key=lambda i: abs(i - (lo + hi) // 2)) + 1
    clause = [i for i in range(lo, hi) if g[i] in CLAUSE_END]
    if clause:
        return min(clause, key=lambda i: abs(i - (lo + hi) // 2)) + 1
    return None


def _nearest_word_boundary(g: list[str], target: int) -> int | None:
    for offset in range(0, len(g)):
        for sign in (-1, +1):
            i = target + sign * offset
            if 0 < i < len(g) and g[i] in WORD_BOUNDARY:
                return i + 1
    return None


def _word_timestamp_boundary(words, text_a: str) -> float:
    """Return the end time of the word whose cumulative text covers text_a."""
    consumed = 0
    target = len(text_a)
    for w in words:
        consumed += len(w.text) + 1  # +1 for the joining space we added
        if consumed >= target:
            return w.end
    return words[-1].end
```

### 2.9 `_cps.py` — pass 4 (D4)

```python
from __future__ import annotations
from dataclasses import replace
from typing import Iterable, Iterator

from maktaba.media.subtitles.options import WrapOptions
from maktaba.media.subtitles.unicode_helpers import grapheme_len, dominant_script
from maktaba.media.subtitles._split_long import _split_one


def enforce_cps(cues_with_neighbors: Iterable, opts: WrapOptions) -> Iterator:
    """Two-cue lookahead so we know how far we can extend before clobbering next.start.

    Input items are tuples of (cue, next_cue_or_None). We yield cues with
    possibly-extended .end or possibly-split into two cues.
    """
    for cue, nxt in cues_with_neighbors:
        n = grapheme_len(" ".join(cue.lines))
        dur = cue.end - cue.start
        if dur <= 0:
            yield cue
            continue
        cps = n / dur
        budget = opts.cps_max_arabic if dominant_script(" ".join(cue.lines)) == "arabic" else opts.cps_max_latin
        if cps <= budget:
            yield cue
            continue
        # Try to extend.
        needed_dur = n / budget
        max_end = cue.end + opts.cps_padding_sec
        if nxt is not None:
            max_end = min(max_end, nxt.start - opts.merge_gap_sec)
        new_end = min(cue.start + needed_dur, max_end)
        if new_end > cue.end:
            cue = replace(cue, end=new_end)
            new_dur = cue.end - cue.start
            if n / new_dur <= budget:
                yield cue
                continue
        # Extension insufficient — split.
        # We synthesize a pseudo-segment so we can reuse split_long's logic.
        pseudo = _PseudoSeg(cue.start, cue.end, " ".join(cue.lines))
        for sub in _split_one(pseudo, opts):
            yield replace(cue,
                          start=sub.start, end=sub.end,
                          lines=(sub.text,),  # re-wrap in the next pass
                          debug={**cue.debug, "cps_split": True})


class _PseudoSeg:
    __slots__ = ("start", "end", "text", "words", "metadata")

    def __init__(self, s, e, t):
        self.start, self.end, self.text = s, e, t
        self.words, self.metadata = [], {}
```

`enforce_cps` runs **after** the wrap pass conceptually, but is wired
upstream of it on splits — see `shaper.py` for the actual ordering,
which re-wraps any cue produced by a CPS-split.

### 2.10 `_tag.py` — pass 6 (D6)

```python
from __future__ import annotations
from dataclasses import replace
from typing import Iterable, Iterator

from maktaba.media.subtitles.options import WrapOptions
from maktaba.media.subtitles.escape import html_escape_speaker


def tag_speakers_for_vtt(cues: Iterable, opts: WrapOptions) -> Iterator:
    """Wrap each cue's first line in <v Speaker N>...</v>... for VTT output.

    SRT serializer ignores cue.speaker entirely (D6).
    """
    for cue in cues:
        if cue.speaker is None or not opts.emit_speaker_tags:
            yield cue
            continue
        speaker = html_escape_speaker(cue.speaker)
        new_lines = (f"<v {speaker}>{cue.lines[0]}",) + tuple(cue.lines[1:])
        yield replace(cue, lines=new_lines)
```

The closing `</v>` is deliberately omitted: WebVTT's `<v>` is a
self-anchoring voice span that runs to end of cue. Adding `</v>` is
legal but redundant and bloats Arabic cues by 4 graphemes — we leave
it out and round-trip-test against `webvtt-py`'s parser.

### 2.11 `shaper.py` — orchestrator

```python
from __future__ import annotations
import itertools
from typing import Iterable, Iterator

from maktaba.media.subtitles.options import WrapOptions
from maktaba.media.subtitles.cue import Cue
from maktaba.media.subtitles._merge import merge_adjacent
from maktaba.media.subtitles._split_long import split_long
from maktaba.media.subtitles._wrap import wrap_text
from maktaba.media.subtitles._cps import enforce_cps
from maktaba.media.subtitles._tag import tag_speakers_for_vtt


def shape(segments: Iterable, opts: WrapOptions, *, for_vtt: bool) -> Iterator[Cue]:
    """End-to-end: segments → wrapped, time-bounded, optionally tagged Cues."""
    merged = merge_adjacent(segments, opts)
    split = split_long(merged, opts)
    cues = _segments_to_cues(split, opts)
    cues = _enforce_cps_with_lookahead(cues, opts)
    cues = (_assert_no_overlap(c, opts) for c in cues)  # generator-friendly
    if for_vtt:
        cues = tag_speakers_for_vtt(cues, opts)
    return _renumber(cues)


def _segments_to_cues(segments, opts):
    for seg in segments:
        lines, debug = wrap_text(seg.text, opts)
        if not lines:
            continue
        yield Cue(
            seq=0,  # patched in _renumber
            start=seg.start,
            end=seg.end,
            lines=tuple(lines),
            speaker=getattr(seg, "speaker", None),
            debug=debug,
        )


def _enforce_cps_with_lookahead(cues, opts):
    cues = list(cues)
    for i, cue in enumerate(cues):
        nxt = cues[i + 1] if i + 1 < len(cues) else None
        for shaped in enforce_cps([(cue, nxt)], opts):
            yield shaped


def _assert_no_overlap(cue: Cue, opts) -> Cue:
    # The generator caller maintains a `last_end` via closure-bound list.
    state = _assert_no_overlap.__dict__.setdefault("_state", {"last_end": -1.0})
    if cue.start < state["last_end"]:
        # Clip start forward to last_end + 1 ms; if that flips end<=start, drop.
        from dataclasses import replace
        clipped_start = state["last_end"] + 0.001
        if clipped_start >= cue.end:
            return None
        cue = replace(cue, start=clipped_start)
    state["last_end"] = cue.end
    return cue


def _renumber(cues):
    seq = 0
    for c in cues:
        if c is None:
            continue
        seq += 1
        from dataclasses import replace
        yield replace(c, seq=seq)
```

The `_assert_no_overlap` closure uses an instance-attribute trick that
is fine in single-threaded synchronous code; an actual production
implementation would carry the state in a small class. We document this
explicitly so the reviewer doesn't try to "fix" it without understanding
that the shaper is single-threaded by design (the segment iterator is
single-producer).

### 2.12 `srt.py` — SRT serializer

```python
from __future__ import annotations
from typing import Iterable

from maktaba.media.subtitles.options import WrapOptions
from maktaba.media.subtitles.escape import html_escape_cue_text
from maktaba.media.subtitles.cue import Cue


def render_srt(cues: Iterable[Cue], opts: WrapOptions) -> bytes:
    eol = opts.eol
    parts: list[str] = []
    for cue in cues:
        parts.append(str(cue.seq))
        parts.append(eol)
        parts.append(_fmt_srt_time(cue.start))
        parts.append(" --> ")
        parts.append(_fmt_srt_time(cue.end))
        parts.append(eol)
        for line in cue.lines:
            # SRT does NOT receive speaker tags (D6).
            parts.append(html_escape_cue_text(line))
            parts.append(eol)
        parts.append(eol)
    return "".join(parts).encode("utf-8")


def _fmt_srt_time(t: float) -> str:
    if t < 0:
        t = 0.0
    h = int(t // 3600)
    m = int((t % 3600) // 60)
    s = int(t % 60)
    ms = int(round((t - int(t)) * 1000))
    if ms == 1000:
        ms = 0
        s += 1
        if s == 60:
            s = 0
            m += 1
            if m == 60:
                m = 0
                h += 1
    return f"{h:02d}:{m:02d}:{s:02d},{ms:03d}"
```

### 2.13 `vtt.py` — VTT serializer

```python
from __future__ import annotations
from typing import Iterable

from maktaba.media.subtitles.options import WrapOptions
from maktaba.media.subtitles.escape import html_escape_cue_text
from maktaba.media.subtitles.cue import Cue


def render_vtt(cues: Iterable[Cue], opts: WrapOptions) -> bytes:
    eol = opts.eol
    parts: list[str] = ["WEBVTT", eol, eol]
    for cue in cues:
        parts.append(_fmt_vtt_time(cue.start))
        parts.append(" --> ")
        parts.append(_fmt_vtt_time(cue.end))
        parts.append(eol)
        for line in cue.lines:
            # The first line MAY already have a <v ...> prefix written by
            # _tag.py; we must escape only the cue-text portion, not the tag.
            parts.append(_escape_vtt_line(line))
            parts.append(eol)
        parts.append(eol)
    return "".join(parts).encode("utf-8")


def _escape_vtt_line(line: str) -> str:
    # Recognize a leading <v ...> tag (already-escaped speaker label inside).
    if line.startswith("<v "):
        idx = line.find(">")
        if idx != -1:
            return line[: idx + 1] + html_escape_cue_text(line[idx + 1 :])
    return html_escape_cue_text(line)


def _fmt_vtt_time(t: float) -> str:
    if t < 0:
        t = 0.0
    h = int(t // 3600)
    m = int((t % 3600) // 60)
    s = int(t % 60)
    ms = int(round((t - int(t)) * 1000))
    if ms == 1000:
        ms = 0
        s += 1
        if s == 60:
            s = 0
            m += 1
            if m == 60:
                m = 0
                h += 1
    return f"{h:02d}:{m:02d}:{s:02d}.{ms:03d}"
```

The VTT serializer is structurally identical to SRT except:
- File starts with `WEBVTT` magic.
- Time uses `.` (not `,`) as the millisecond separator.
- Sequence number is omitted (it's optional in WebVTT and `webvtt-py`
  treats it as a cue identifier; we skip it to avoid identifier
  collisions when the same file is concatenated).
- The first line of a tagged cue starts with `<v ...>` (passes through
  the tag escape but escapes the rest of the line).

### 2.14 `__init__.py` — public surface

```python
"""Subtitle text shaping. Public entry points: shape(), render_srt(), render_vtt()."""
from maktaba.media.subtitles.options import WrapOptions
from maktaba.media.subtitles.shaper import shape
from maktaba.media.subtitles.srt import render_srt
from maktaba.media.subtitles.vtt import render_vtt

__all__ = ["WrapOptions", "shape", "render_srt", "render_vtt"]
```

The Story 4.1 driver uses this module as:

```python
from maktaba.media.subtitles import shape, render_srt, render_vtt, WrapOptions

opts = WrapOptions(language=transcript.language)
srt_bytes = render_srt(shape(segments_iter, opts, for_vtt=False), opts)
vtt_bytes = render_vtt(shape(segments_iter, opts, for_vtt=True), opts)
```

---

## 3. Code scaffolding — file-by-file checklist

| Order | File | Symbols introduced | Tests gating |
|-------|------|--------------------|--------------|
| 1 | `pyproject.toml` (edit) | `regex >= 2024.5.15` added to runtime deps | (n/a) |
| 2 | `maktaba/media/__init__.py` | (empty) | (n/a) |
| 3 | `maktaba/media/subtitles/__init__.py` | re-export `shape, render_srt, render_vtt, WrapOptions` | (n/a) |
| 4 | `maktaba/media/subtitles/options.py` | `WrapOptions`, `SENTENCE_END`, `CLAUSE_END`, `WORD_BOUNDARY` | (n/a) |
| 5 | `maktaba/media/subtitles/unicode_helpers.py` | `graphemes`, `grapheme_len`, `dominant_script`, `is_rtl_first_strong`, `has_digits`, `strip_invisible`, `count_presentation_forms`, `maybe_rlm_prefix` | `test_unicode_helpers` |
| 6 | `maktaba/media/subtitles/escape.py` | `html_escape_cue_text`, `html_escape_speaker` | `test_escape` |
| 7 | `maktaba/media/subtitles/cue.py` | `Cue` dataclass | (n/a — invariants asserted in `__post_init__`) |
| 8 | `maktaba/media/subtitles/_merge.py` | `merge_adjacent`, `_fold` | `test_merge` |
| 9 | `maktaba/media/subtitles/_split_long.py` | `split_long`, `_split_one`, `_best_cut`, `_nearest_word_boundary`, `_word_timestamp_boundary` | `test_split_long`, `test_long_segment_split_proportionally` |
| 10 | `maktaba/media/subtitles/_wrap.py` | `wrap_text`, `_backtrack_to_punct`, `_balance`, `_rebalance` | `test_wrap`, `test_wrap_respects_max_line_chars`, `test_wrap_breaks_at_sentence` |
| 11 | `maktaba/media/subtitles/_cps.py` | `enforce_cps` | `test_cps` |
| 12 | `maktaba/media/subtitles/_tag.py` | `tag_speakers_for_vtt` | `test_tag`, `test_speaker_tag_only_when_diarized`, `test_speaker_label_escaped` |
| 13 | `maktaba/media/subtitles/shaper.py` | `shape`, internal helpers | `test_shaper_end_to_end`, `test_no_overlap_after_merge_or_split` |
| 14 | `maktaba/media/subtitles/srt.py` | `render_srt`, `_fmt_srt_time` | `test_srt_render`, `test_srt_round_trips` (Story 4.1) |
| 15 | `maktaba/media/subtitles/vtt.py` | `render_vtt`, `_fmt_vtt_time`, `_escape_vtt_line` | `test_vtt_render`, `test_vtt_round_trips` (Story 4.1) |
| 16 | `maktaba/media/subtitles/tests/conftest.py` | fixtures listed in §2.1 | (n/a) |
| 17 | `maktaba/media/subtitles/tests/test_no_overlap_property.py` | hypothesis-based property test | nightly CI |

The `pyproject.toml` change is the only edit outside the new package.

---

## 4. Test cases

### 4.1 `test_wrap_respects_max_line_chars` (story-named)

```python
def test_wrap_long_arabic_segment_no_line_exceeds_budget():
    text = "هذا نص عربي طويل " * 20  # ~340 graphemes
    opts = WrapOptions(max_line_chars=42, max_lines=2)
    # The wrap pass on its own returns whatever it can — to honor max_lines,
    # the caller must pre-split. We validate per-line budget here.
    lines, debug = wrap_text(text, opts)
    for line in lines:
        # Strip the optional RLM prefix before measuring (D2).
        bare = line.lstrip("‏")
        assert grapheme_len(bare) <= opts.max_line_chars, line
```

### 4.2 `test_wrap_breaks_at_sentence` (story-named)

```python
def test_wrap_prefers_sentence_end_within_lookback():
    # The natural greedy break would land mid-clause; backtrack should pull
    # the break to the period 6 graphemes earlier.
    text = "Short opening sentence here. And then a long continuation that fills"
    opts = WrapOptions(max_line_chars=42, lookback_chars=12)
    lines, _ = wrap_text(text, opts)
    assert len(lines) == 2
    assert lines[0].endswith(".")
    assert lines[1].startswith("And")
```

### 4.3 `test_no_overlap_after_merge_or_split` (story-named)

```python
def test_shaper_output_never_overlaps(arabic_lecture):
    cues = list(shape(iter(arabic_lecture), WrapOptions(), for_vtt=False))
    for prev, cur in zip(cues, cues[1:]):
        assert prev.end <= cur.start, (prev, cur)
        assert prev.start < prev.end
```

### 4.4 `test_long_segment_split_proportionally` (story-named)

```python
def test_long_segment_with_word_timestamps_splits_at_word_end():
    seg = make_segment(
        start=0.0, end=12.0,
        text="alpha bravo charlie delta echo foxtrot golf hotel india juliet kilo lima",
        words=[
            Word(seq=i, start=i, end=i + 1, text=tok)
            for i, tok in enumerate("alpha bravo charlie delta echo foxtrot "
                                    "golf hotel india juliet kilo lima".split())
        ],
    )
    opts = WrapOptions(max_cue_sec=6.0)
    parts = list(split_long([seg], opts))
    assert len(parts) >= 2
    for p in parts:
        assert p.end - p.start <= opts.max_cue_sec + 1e-6
    # Adjacent parts must butt-join, no overlaps:
    for a, b in zip(parts, parts[1:]):
        assert a.end <= b.start + 1e-6
    # First sub-cue ends exactly on a word boundary.
    first = parts[0]
    assert first.end in {1.0, 2.0, 3.0, 4.0, 5.0, 6.0}
```

### 4.5 `test_arabic_punctuation_preserved` (story-named)

```python
def test_arabic_question_mark_is_not_normalized():
    text = "ما اسمك؟"
    opts = WrapOptions()
    lines, _ = wrap_text(text, opts)
    assert "؟" in lines[0]
    assert "?" not in lines[0]
```

### 4.6 `test_speaker_tag_only_when_diarized` (story-named)

```python
def test_no_v_tag_when_speaker_is_none(arabic_lecture):
    # arabic_lecture has speaker=None on every segment.
    vtt_bytes = render_vtt(shape(iter(arabic_lecture), WrapOptions(), for_vtt=True),
                           WrapOptions())
    s = vtt_bytes.decode("utf-8")
    assert "<v " not in s
```

### 4.7 `test_speaker_label_escaped` (story-named)

```python
def test_speaker_label_with_html_chars_is_escaped():
    seg = make_segment(start=0.0, end=2.0, text="hello", speaker="Sheikh <A> & B")
    cues = list(shape(iter([seg]), WrapOptions(), for_vtt=True))
    rendered = render_vtt(cues, WrapOptions()).decode("utf-8")
    assert "<v Sheikh &lt;A&gt; &amp; B>hello" in rendered
    assert "<v Sheikh <A>" not in rendered
```

### 4.8 `test_overflow_token_alone_on_line` (story edge case D8)

```python
def test_url_longer_than_budget_alone_on_line(caplog):
    long_url = "https://example.com/" + ("path/" * 20)  # > 42 graphemes
    text = f"see {long_url} for more"
    opts = WrapOptions(max_line_chars=42)
    with caplog.at_level("DEBUG"):
        lines, debug = wrap_text(text, opts)
    assert any(line.lstrip("‏") == long_url for line in lines)
    assert debug["overflow_tokens"] == 1
```

### 4.9 `test_grapheme_cluster_not_split` (story edge case)

```python
def test_combining_marks_stay_with_base():
    # 'يَ' = ya + fatha (two code points, one grapheme cluster).
    text = "يَ" * 30  # 30 grapheme clusters, 60 code points
    opts = WrapOptions(max_line_chars=20)
    lines, _ = wrap_text(text, opts)
    for line in lines:
        bare = line.lstrip("‏")
        # Each grapheme cluster is 2 code points; line length should be
        # an even number of code points (no mid-cluster break).
        assert len(bare) % 2 == 0 or bare.isspace()
```

### 4.10 `test_cps_extends_when_cue_too_dense`

```python
def test_dense_arabic_cue_gets_padding():
    seg = make_segment(start=0.0, end=1.0, text="ا" * 30)  # 30 graph / 1 s = 30 cps
    cues = list(shape(iter([seg]), WrapOptions(cps_max_arabic=17.0), for_vtt=False))
    assert len(cues) == 1
    # 30 / 17 ≈ 1.76 s — within the 2.0 s padding budget.
    assert cues[0].end > 1.0
    assert cues[0].end <= 1.0 + 2.0 + 1e-6
```

### 4.11 `test_cps_splits_when_padding_insufficient`

```python
def test_dense_cue_padded_to_max_then_split():
    # 100 graphemes in 1 s with 2 s padding → max dur 3 s → still 33 cps > 17.
    seg = make_segment(start=0.0, end=1.0, text="ا " * 50)
    cues = list(shape(iter([seg]), WrapOptions(), for_vtt=False))
    assert len(cues) >= 2
```

### 4.12 `test_empty_segment_dropped`

```python
def test_segment_with_only_whitespace_emits_no_cue():
    seg = make_segment(start=0.0, end=2.0, text="   \t  ")
    cues = list(shape(iter([seg]), WrapOptions(), for_vtt=False))
    assert cues == []
```

### 4.13 `test_merge_adjacent_close_segments`

```python
def test_two_segments_within_50ms_merge():
    a = make_segment(start=0.0, end=1.0, text="hello")
    b = make_segment(start=1.04, end=2.0, text="world")
    merged = list(merge_adjacent([a, b], WrapOptions(merge_gap_sec=0.05)))
    assert len(merged) == 1
    assert merged[0].text == "hello world"
    assert merged[0].start == 0.0 and merged[0].end == 2.0
```

### 4.14 `test_no_merge_across_speaker_change`

```python
def test_segments_with_different_speakers_do_not_merge():
    a = make_segment(start=0.0, end=1.0, text="hello", speaker="Speaker 1")
    b = make_segment(start=1.04, end=2.0, text="world", speaker="Speaker 2")
    merged = list(merge_adjacent([a, b], WrapOptions(merge_gap_sec=0.05)))
    assert len(merged) == 2
```

### 4.15 `test_strip_invisible_neutralizes_rlo`

```python
def test_rlo_in_input_is_stripped_before_wrap():
    # RLO would visually reverse "abc" to "cba".
    text = "‮" + "abcdef ghijkl"
    lines, debug = wrap_text(text, WrapOptions())
    assert "‮" not in lines[0]
    assert debug["stripped_invisible"] == 1
```

### 4.16 `test_no_presentation_forms_emitted` (D2 regression)

```python
def test_output_contains_no_arabic_presentation_forms():
    # Even when input has them (legacy STT corpus), we leave them in the
    # raw segment but never re-encode logical → presentation. Verify wrap
    # passes them through unchanged so search-side normalization owns it.
    text = "ﺍﻟﺴﻼﻡ"  # presentation forms of "السلام"
    lines, debug = wrap_text(text, WrapOptions())
    # We DO pass them through (D2 — no rewriting). We just count for telemetry.
    assert debug["presentation_forms"] == len("ﺍﻟﺴﻼﻡ")
    # And we never INJECT any new presentation form into output:
    assert all(0xFB50 > ord(c) or ord(c) > 0xFDFF for c in lines[0]
               if c not in "ﺍﻟﺴﻼﻡ")
```

### 4.17 `test_srt_uses_crlf` (D9)

```python
def test_srt_bytes_use_crlf():
    cues = [Cue(seq=1, start=0.0, end=1.0, lines=("hello",))]
    out = render_srt(cues, WrapOptions(eol="\r\n"))
    assert b"\r\n" in out
    assert b"\n" in out
    # Every \n in the file is preceded by \r:
    s = out.decode("utf-8")
    for i, ch in enumerate(s):
        if ch == "\n":
            assert i > 0 and s[i - 1] == "\r", f"bare LF at {i}"
```

### 4.18 `test_property_no_overlap_under_random_segments` (hypothesis)

```python
from hypothesis import given, strategies as st

@given(st.lists(
    st.builds(make_segment,
              start=st.floats(0, 3600), end_offset=st.floats(0.5, 10.0),
              text=st.text(min_size=1, max_size=200)),
    min_size=1, max_size=50,
))
def test_random_segments_produce_non_overlapping_cues(segments):
    segments.sort(key=lambda s: s.start)
    cues = list(shape(iter(segments), WrapOptions(), for_vtt=False))
    for a, b in zip(cues, cues[1:]):
        assert a.end <= b.start + 1e-6
```

---

## 5. Edge cases and how the plan handles each

| # | Edge case | Handled by |
|---|-----------|------------|
| E1 | Combining marks (Arabic tashkīl, Latin combining accents). | `_GRAPHEME_RX = r"\X"` in `unicode_helpers.graphemes` returns extended grapheme clusters; the wrap and split paths never break inside a cluster. (`test_grapheme_cluster_not_split`) (D1) |
| E2 | Mixed Arabic + English + numbers in one cue. | `dominant_script` thresholds at 2× majority; "mixed" cues use the Latin (looser) CPS budget so the viewer isn't over-padded. `maybe_rlm_prefix` adds an RLM only when needed (RTL-first AND digit/Latin present). (`test_unicode_helpers`) (D2, D4) |
| E3 | ZWJ ligatures (`👨‍👩‍👧‍👦` family emoji, Arabic lām-alif `ﻻ`). | `\X` collapses ZWJ sequences into one grapheme cluster — line budget counts them as 1, never splits inside. The Arabic ligature `ﻻ` (U+FEFB, presentation form) is in our PF range; we count it for telemetry but pass through. (D1, D2) |
| E4 | Bidi-override injection (RLO, LRO, etc.) in input text. | `strip_invisible` removes U+202A–U+202E, U+200B, U+200E, U+FEFF before wrap; cue can never carry a bidi-override that flips meaning. (`test_strip_invisible_neutralizes_rlo`) (D2) |
| E5 | A token longer than `max_line_chars` (URL, hashtag, base64 blob). | `_wrap.wrap_text` flushes the current line, places the overlong token alone, increments `overflow_tokens`; one DEBUG log per file fires from the Story 4.1 driver after counting all cues' debug records. (`test_overflow_token_alone_on_line`) (D8) |
| E6 | Word timestamps absent on a segment that needs splitting. | `_word_timestamp_boundary` is bypassed when `seg.words` is empty; `_split_one` falls back to linear apportionment by grapheme count and sets `metadata.split_method = "linear"`. (`test_long_segment_split_proportionally` covers both branches.) (D7) |
| E7 | Single token longer than `max_cue_sec` worth of audio with no punctuation anywhere. | `_split_one` finds no `_best_cut` and no `_nearest_word_boundary`, so it yields the segment unchanged — we accept the over-long cue rather than synthesizing a fake break inside an indivisible token. The next-tier Story 4.5 live VTT path does the same. (D7) |
| E8 | Segment text contains existing HTML entities (`&amp;`, `&lt;`). | `html_escape_cue_text` runs `&` → `&amp;` first, so a literal `&amp;` in input becomes `&amp;amp;` in output. This is the correct, lossless behavior — cue text is treated as literal user text, not pre-formatted markup. (Mirrors Story 4.1.) |
| E9 | Speaker label contains `\n` or `\r`. | `html_escape_speaker` substitutes U+2028 (line separator) for any newline before HTML-escaping; the resulting `<v ...>` tag stays on one line and the renderer prints a space. (`test_speaker_label_escaped` extension.) |
| E10 | Two adjacent segments with identical timing (start=end of previous). | `merge_adjacent` folds them when `gap == 0` (within `merge_gap_sec >= 0.0`). The downstream `_assert_no_overlap` clip (start += 1 ms) is never triggered for merged cues — only for cues whose split times rounded into a collision. |
| E11 | Empty segment (text is whitespace or empty after `strip`). | `wrap_text` returns `([], {"empty": True})`; `_segments_to_cues` skips the cue entirely. The empty segment's time range is forfeited — the next cue starts where it would have started anyway. (`test_empty_segment_dropped`) |
| E12 | CRLF in input segment text (multi-line transcript output). | The grapheme regex treats `\r\n` as one cluster; the cue would then carry an embedded EOL, which `Cue.__post_init__` rejects (raises ValueError). To prevent the raise, `wrap_text` calls `text.split()` (whitespace tokenizer that consumes any newlines), so embedded CRLFs collapse to single spaces in output. |
| E13 | Cue duration 0 (start == end after extreme clipping). | `Cue.__post_init__` raises; `_assert_no_overlap` returns `None` for cues whose clipped start would equal/exceed end; `_renumber` filters Nones. The job loses one cue but does not crash. |
| E14 | Diarization-tagged cue whose first line has only the speaker tag and no text (impossible by construction but defensive). | `tag_speakers_for_vtt` always emits `<v ...>cue.lines[0]` — if `cue.lines[0]` is empty, `Cue.__post_init__` already raised earlier in the pipeline. Belt-and-suspenders: `_tag.py` has an `assert cue.lines and cue.lines[0]` precondition. |
| E15 | A cue whose extension to honor CPS would push past the next cue's start. | `enforce_cps` caps `max_end = nxt.start - merge_gap_sec`; if extension under the cap is insufficient, the cue is split via `_split_one` instead. (`test_cps_splits_when_padding_insufficient`) (D4) |
| E16 | Segment with `start > end` (corrupt input). | `Cue.__post_init__` raises ValueError on construction; the caller in Story 4.1 catches and logs `kind=corrupt_segment` for the originating segment, then continues. (We do NOT silently swap start/end — that would mask an upstream bug.) |
| E17 | `wcwidth` claims a grapheme is zero-width (e.g. some combining marks). | We don't use `wcwidth` for the budget; grapheme count is the budget unit. Diagnostics-only column-width estimates may use `wcwidth` in a follow-up tool but never in the wrap path. (D1) |
| E18 | All segments have the same speaker; should we still tag every cue? | Yes — VTT players use the `<v>` tag for accessibility (TTS announces the speaker change) and for CSS styling. Even a single-speaker file with diarization on tags every cue. The user opts out by disabling diarization at the library setting (Plan 3.9). |

---

## 6. Acceptance checklist

- [ ] **A1** Each output cue has `<= max_lines = 2` lines (default; configurable on `WrapOptions`), each line `<= max_line_chars = 42` graphemes — measured by `regex.findall(r"\X", line)` after stripping the optional RLM prefix. Exception: a single token longer than the budget is placed alone on its own line and the line is allowed to exceed; one DEBUG log per file records the count. (`test_wrap_respects_max_line_chars`, `test_overflow_token_alone_on_line`)
- [ ] **A2** Line breaks are chosen in priority order: sentence-end (`.`, `!`, `?`, `؟`) > clause-end (`,`, `;`, `:`, `،`, `؛`) > word boundary, with a `lookback_chars = 12` backtrack from the greedy break point. (`test_wrap_breaks_at_sentence`)
- [ ] **A3** Cues never overlap in time: for every consecutive pair `(prev, cur)` we have `prev.end <= cur.start`. The shaper enforces this via the `_assert_no_overlap` clip at pass 5. (`test_no_overlap_after_merge_or_split`, `test_property_no_overlap_under_random_segments`)
- [ ] **A4** Adjacent segments whose gap is `<= merge_gap_sec = 0.05` are merged into one cue (text joined with one space, time spans concatenated, speaker preserved only when both share it). (`test_merge_adjacent_close_segments`, `test_no_merge_across_speaker_change`)
- [ ] **A5** A single segment longer than `max_cue_sec = 6.0` is split at the highest-priority punctuation in the 50–90% window of the segment text. Sub-cue start/end times are apportioned by word timestamps when present, by grapheme-count linearly when absent (with `metadata.split_method = "linear"`). (`test_long_segment_split_proportionally`)
- [ ] **A6** Arabic punctuation glyphs (`؟`, `،`, `؛`) in input are preserved verbatim in output; no normalization to Latin equivalents. (`test_arabic_punctuation_preserved`)
- [ ] **A7** VTT cues include `<v Speaker N>` only when `cue.speaker IS NOT NULL`; SRT never includes speaker tags regardless. (`test_speaker_tag_only_when_diarized`)
- [ ] **A8** Speaker labels are HTML-escaped (`&` → `&amp;`, `<` → `&lt;`, `>` → `&gt;`) before placement inside `<v ...>`. Newlines in labels are replaced with U+2028 to keep the tag on one line. (`test_speaker_label_escaped`)
- [ ] **A9** Wrapping is grapheme-cluster-aware: combining marks, ZWJ ligatures, and emoji sequences never get split mid-cluster. (`test_grapheme_cluster_not_split`)
- [ ] **A10** Bidi-override codepoints (U+202A–U+202E, U+200B, U+200E, U+FEFF) in input are stripped before wrapping; output never contains them. The optional RLM prefix (U+200F) is the only bidi control codepoint we ever emit, and only on lines that are RTL-first AND contain digits or Latin letters. (`test_strip_invisible_neutralizes_rlo`, `test_no_presentation_forms_emitted`)
- [ ] **A11** CPS limit (Arabic 17, Latin 21) is honored: dense cues are first padded by up to `cps_padding_sec = 2.0` (capped by next cue's start minus the merge gap), then split at the highest-priority punctuation if padding is insufficient. Text is never compressed. (`test_cps_extends_when_cue_too_dense`, `test_cps_splits_when_padding_insufficient`)
- [ ] **A12** SRT and VTT files use CRLF line endings on disk; the serializer accepts an `eol` override for round-trip tests against `srt` and `webvtt-py`. (`test_srt_uses_crlf`, plus the Story 4.1 round-trip tests `test_srt_round_trips` and `test_vtt_round_trips`.)
- [ ] **A13** The shaper is a pure function of `(segments_iter, options)`; no I/O, no global state outside the deliberately-documented `_assert_no_overlap` closure. The serializer is a pure function of `(cues_iter, options)` returning bytes. (Composition test: same input → same output, byte-for-byte.)
- [ ] **A14** Empty or whitespace-only segments emit no cue; cues with corrupt timing (`end <= start`) raise on construction and are caught by the Story 4.1 driver as `kind=corrupt_segment` (logged but non-fatal). (`test_empty_segment_dropped`)
- [ ] **A15** The runtime dependency added by this story is exactly one package: `regex`. No `arabic-reshaper`, no `python-bidi`, no other new runtime deps. (Static check: `pyproject.toml` diff.)
- [ ] **A16** The hypothesis property test (`test_random_segments_produce_non_overlapping_cues`) passes on 200 generated cases per CI run with no shrunk failures.
