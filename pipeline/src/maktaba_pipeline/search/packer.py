"""Greedy sentence → unit packer.

Implements Story 5.1's packing rules: aim for ~200 chars per unit,
emit at the next sentence boundary, never exceed a hard char cap or
the embedding model's token budget. Falls back to word-boundary
splits for pathological inputs (single sentence over the cap) and
records the fallback in ``metadata["split_method"]`` for auditing.
"""

from __future__ import annotations

from collections.abc import Sequence

import regex  # type: ignore[import-untyped]

from .models import Sentence, UnitDraft
from .token_estimate import estimate_tokens

__all__ = ["Packer"]


# ``\p{Z}`` matches any unicode separator — a safe word boundary for
# both Latin and Arabic scripts. We split lazily (only when a single
# sentence is too long to emit whole).
_WORD_SPLIT_RE = regex.compile(r"(?<=\S)[\p{Z}\s]+")


class Packer:
    """Pack :class:`Sentence`\\ s into :class:`UnitDraft`\\ s greedily.

    Parameters
    ----------
    target_chars
        Soft minimum — once the buffer reaches this size we emit at
        the next sentence boundary.
    cap_chars
        Hard maximum char count for one unit. A single sentence
        exceeding this is split at a word boundary and tagged
        ``metadata["split_method"] = "word"``.
    token_cap
        Hard maximum token estimate per unit. Same fallback as above.
    """

    def __init__(
        self,
        *,
        target_chars: int = 200,
        cap_chars: int = 400,
        token_cap: int = 384,
    ) -> None:
        if target_chars <= 0 or cap_chars < target_chars:
            raise ValueError("cap_chars must be >= target_chars > 0")
        if token_cap <= 0:
            raise ValueError("token_cap must be > 0")
        self._target_chars = target_chars
        self._cap_chars = cap_chars
        self._token_cap = token_cap

    def pack(self, sentences: Sequence[Sentence]) -> list[UnitDraft]:
        """Return packed units; ``seq`` is 1-based and contiguous."""
        if not sentences:
            return []

        units: list[UnitDraft] = []
        # Working buffer: pieces (sentence texts) accumulated since
        # the last flush, plus span bookkeeping.
        buf_text: list[str] = []
        buf_segments: list[int] = []  # ordered, may contain dups
        buf_start: float | None = None
        buf_end: float | None = None

        # Track whether *every* emitted unit needed a word-split
        # fallback. If so, the caller is informed via
        # ``metadata["no_punctuation"] = True`` on the last unit's
        # metadata so the run can be audited.
        any_punctuation_emit = False
        seq = 1

        def flush(*, split_method: str | None = None) -> None:
            nonlocal seq, buf_start, buf_end, any_punctuation_emit
            if not buf_text:
                return
            text = " ".join(buf_text).strip()
            if not text:
                buf_text.clear()
                buf_segments.clear()
                buf_start = None
                buf_end = None
                return
            metadata: dict[str, object] = {}
            if split_method is not None:
                metadata["split_method"] = split_method
            else:
                any_punctuation_emit = True
            # Dedupe segment ids while keeping order.
            seen: set[int] = set()
            seg_ids: list[int] = []
            for sid in buf_segments:
                if sid in seen:
                    continue
                seen.add(sid)
                seg_ids.append(sid)
            assert buf_start is not None and buf_end is not None
            units.append(
                UnitDraft(
                    seq=seq,
                    start_sec=buf_start,
                    end_sec=buf_end,
                    text=text,
                    language="",  # caller tags
                    segment_ids=tuple(seg_ids),
                    metadata=metadata,
                )
            )
            seq += 1
            buf_text.clear()
            buf_segments.clear()
            buf_start = None
            buf_end = None

        for sent in sentences:
            # Path 1 — sentence is itself larger than the cap (or its
            # token estimate is over the cap): flush whatever we have,
            # then word-split the oversize sentence into multiple
            # units, each tagged "word".
            if len(sent.text) > self._cap_chars or estimate_tokens(sent.text) > self._token_cap:
                flush()
                for chunk in _word_split(
                    sent.text,
                    cap_chars=self._cap_chars,
                    token_cap=self._token_cap,
                ):
                    buf_text.append(chunk)
                    buf_segments.extend(sent.segment_ids)
                    buf_start = sent.start_sec if buf_start is None else buf_start
                    buf_end = sent.end_sec
                    flush(split_method="word")
                continue

            # Path 2 — adding this sentence would exceed the cap:
            # flush first at the boundary we already have.
            projected = sum(len(p) for p in buf_text) + (len(buf_text) - 1 if buf_text else 0)
            projected += (1 if buf_text else 0) + len(sent.text)
            projected_tokens = estimate_tokens(
                (" ".join([*buf_text, sent.text])).strip(),
            )
            if buf_text and (projected > self._cap_chars or projected_tokens > self._token_cap):
                flush()

            buf_text.append(sent.text)
            buf_segments.extend(sent.segment_ids)
            buf_start = sent.start_sec if buf_start is None else buf_start
            buf_end = sent.end_sec

            # Hit the soft target — emit at this sentence boundary.
            cur = sum(len(p) for p in buf_text) + max(len(buf_text) - 1, 0)
            if cur >= self._target_chars:
                flush()

        flush()

        if units and not any_punctuation_emit:
            # Mutate metadata on the last unit; dict is owned by the
            # UnitDraft (frozen dataclass holds the reference but the
            # dict itself is mutable).
            last = units[-1]
            last.metadata["no_punctuation"] = True

        return units


def _word_split(text: str, *, cap_chars: int, token_cap: int) -> list[str]:
    """Split ``text`` at word boundaries into ``<= cap_chars`` pieces.

    Each piece additionally respects ``token_cap``. If a single word
    is longer than the cap (rare — e.g. a URL), it is hard-split at
    the cap boundary.
    """
    words = [w for w in _WORD_SPLIT_RE.split(text) if w]
    if not words:
        return []
    pieces: list[str] = []
    current: list[str] = []
    current_len = 0
    for word in words:
        # Single-word cap-breach → emit current, then chop the word.
        if len(word) > cap_chars:
            if current:
                pieces.append(" ".join(current))
                current = []
                current_len = 0
            for i in range(0, len(word), cap_chars):
                pieces.append(word[i : i + cap_chars])
            continue
        added = len(word) + (1 if current else 0)
        candidate_len = current_len + added
        candidate_text = (" ".join([*current, word])).strip()
        if current and (candidate_len > cap_chars or estimate_tokens(candidate_text) > token_cap):
            pieces.append(" ".join(current))
            current = [word]
            current_len = len(word)
        else:
            current.append(word)
            current_len = candidate_len
    if current:
        pieces.append(" ".join(current))
    return pieces
