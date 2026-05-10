"""Build highlighted text snippets around the first matching term.

The snippet is grapheme-aware (no splitting inside a base+combining
sequence) and uses :func:`arabic_normalize` to align query tokens to
indexed text — so a query for ``الحمد`` highlights ``الحَمد`` in the
original (un-normalized) display text.
"""

from __future__ import annotations

from collections.abc import Sequence

import regex  # type: ignore[import-untyped]

from .normalize import arabic_normalize

__all__ = ["build_snippet"]


# Grapheme-aware iteration: ``\X`` matches one extended grapheme
# cluster in the regex package. We use it to count display width and
# to walk the text without splitting a base+combining pair.
_GRAPHEME_RE = regex.compile(r"\X")


def build_snippet(
    text: str,
    query_terms: Sequence[str],
    *,
    max_chars: int = 240,
    mark_open: str = "<mark>",
    mark_close: str = "</mark>",
) -> str:
    """Return a snippet of ``text`` with each query term wrapped.

    Algorithm:

    1. Normalize both ``text`` and each query term via
       :func:`arabic_normalize`. The normalized text shares its
       *grapheme cluster boundaries* with the original — every step
       of the normalization is per-character — so a span ``[i:j]``
       in normalized form maps back to the same span in the
       original.
    2. Find every occurrence of every term in the normalized text;
       the first occurrence anchors the window.
    3. Build a window of up to ``max_chars`` graphemes centered on
       the first match (but biased forward when the match is near
       the start).
    4. Walk the matches inside the window in left-to-right order and
       insert mark tags into the *original* text (preserves casing
       and diacritics for display).

    Empty ``query_terms``, or no match → returns the head of the
    text up to ``max_chars`` graphemes, no tags.
    """
    if not text:
        return ""

    graphemes = _GRAPHEME_RE.findall(text)
    norm_graphemes = [arabic_normalize(g) for g in graphemes]
    # Joining the normalized graphemes back together gives a string
    # whose character indices map 1:1 to grapheme indices, because
    # normalization is per-grapheme. (We don't actually need the
    # joined string for matching — we walk graphemes directly.)

    norm_terms: list[list[str]] = []
    for term in query_terms:
        t = arabic_normalize(term).strip()
        if not t:
            continue
        # A multi-grapheme term is matched as a sequence — split the
        # normalized term into its own graphemes for the comparison.
        norm_terms.append(_GRAPHEME_RE.findall(t))

    if not norm_terms:
        head = "".join(graphemes[:max_chars])
        return head + ("…" if len(graphemes) > max_chars else "")

    # Find all match spans in grapheme-index terms. Each span is
    # ``(start_idx, end_idx_exclusive)`` over the grapheme list.
    matches: list[tuple[int, int]] = []
    for term_graphemes in norm_terms:
        tlen = len(term_graphemes)
        if tlen == 0:
            continue
        i = 0
        limit = len(norm_graphemes) - tlen
        while i <= limit:
            if norm_graphemes[i : i + tlen] == term_graphemes:
                matches.append((i, i + tlen))
                i += tlen
            else:
                i += 1
    if not matches:
        head = "".join(graphemes[:max_chars])
        return head + ("…" if len(graphemes) > max_chars else "")

    matches.sort()
    first_start = matches[0][0]

    # Center the window on the first match, biased forward.
    half = max_chars // 2
    window_start = max(0, first_start - half // 2)
    window_end = min(len(graphemes), window_start + max_chars)
    # Pull the window back if we hit the right edge.
    window_start = max(0, window_end - max_chars)

    # Filter matches to those entirely within the window, then merge
    # overlaps (defensive — duplicate terms can yield overlap).
    in_window = [(s, e) for s, e in matches if s >= window_start and e <= window_end]
    in_window.sort()
    merged: list[tuple[int, int]] = []
    for s, e in in_window:
        if merged and s <= merged[-1][1]:
            merged[-1] = (merged[-1][0], max(merged[-1][1], e))
        else:
            merged.append((s, e))

    parts: list[str] = []
    if window_start > 0:
        parts.append("…")
    cursor = window_start
    for s, e in merged:
        if cursor < s:
            parts.append("".join(graphemes[cursor:s]))
        parts.append(mark_open)
        parts.append("".join(graphemes[s:e]))
        parts.append(mark_close)
        cursor = e
    if cursor < window_end:
        parts.append("".join(graphemes[cursor:window_end]))
    if window_end < len(graphemes):
        parts.append("…")
    return "".join(parts)
