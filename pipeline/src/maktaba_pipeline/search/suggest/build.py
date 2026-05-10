"""Offline n-gram extraction for ``search_suggestion_terms``.

The builder walks every unit in a library, splits the text into
Unicode-letter tokens, and counts overlapping n-grams of length 2-4.
Results are filtered by minimum frequency and minimum
document-frequency, then upserted into ``search_suggestion_terms``
keyed by ``(library_id, term_normalized)``. The build is idempotent
— re-running it refreshes the counts and updates ``last_seen_at``.
"""

from __future__ import annotations

from collections import Counter, defaultdict
from collections.abc import Iterable
from contextlib import AbstractAsyncContextManager
from typing import Any, Protocol
from uuid import UUID

import regex as _regex  # type: ignore[import-untyped]

from ..fts.normalize import arabic_normalize

__all__ = ["NgramExtractor", "build_ngrams_for_library"]


# Arabic-aware word splitter — matches any run of Unicode letters.
# ``regex`` (not ``re``) is required for \p{L}.
_TOKEN_RE = _regex.compile(r"\p{L}+")


class _Row(Protocol):
    def __getitem__(self, key: str) -> Any: ...


class _DBConn(Protocol):
    dialect: str

    def transaction(self) -> AbstractAsyncContextManager[Any]: ...

    async def fetch(self, sql: str, *args: Any) -> list[_Row]: ...

    async def execute(self, sql: str, *args: Any) -> Any: ...


class NgramExtractor:
    """Pure-Python overlapping n-gram extractor.

    The extractor is stateless across :meth:`extract` calls. Each
    invocation re-counts everything; the caller is responsible for
    chunking large corpora into manageable batches if memory is a
    concern (a library's worth of units is typically fine in one
    pass).
    """

    def __init__(
        self,
        *,
        min_n: int = 2,
        max_n: int = 4,
        min_frequency: int = 3,
        min_doc_frequency: int = 2,
    ) -> None:
        if min_n < 1 or max_n < min_n:
            raise ValueError("min_n must be >= 1 and max_n must be >= min_n")
        self._min_n = min_n
        self._max_n = max_n
        self._min_frequency = min_frequency
        self._min_doc_frequency = min_doc_frequency

    def extract(self, texts: Iterable[str]) -> list[tuple[str, int, int, int]]:
        """Return ``(term, ngram, frequency, doc_frequency)`` tuples.

        ``ngram`` is the n in n-gram (2, 3, or 4 with defaults).
        ``frequency`` counts every occurrence across all documents;
        ``doc_frequency`` counts the number of distinct input texts
        the term appeared in at least once. Terms that fail either
        minimum are dropped.
        """
        # Per-n counters: frequency by term, doc-frequency by term.
        freq: dict[int, Counter[str]] = defaultdict(Counter)
        df: dict[int, dict[str, int]] = defaultdict(dict)

        for text in texts:
            tokens = _TOKEN_RE.findall(text)
            if not tokens:
                continue
            # Track which terms appeared in this document, per n,
            # to compute df without double-counting.
            seen_in_doc: dict[int, set[str]] = defaultdict(set)
            for n in range(self._min_n, self._max_n + 1):
                if len(tokens) < n:
                    continue
                for i in range(len(tokens) - n + 1):
                    term = " ".join(tokens[i : i + n])
                    freq[n][term] += 1
                    seen_in_doc[n].add(term)
            for n, terms in seen_in_doc.items():
                bucket = df[n]
                for term in terms:
                    bucket[term] = bucket.get(term, 0) + 1

        out: list[tuple[str, int, int, int]] = []
        for n, counter in freq.items():
            for term, f in counter.items():
                if f < self._min_frequency:
                    continue
                doc_f = df[n].get(term, 0)
                if doc_f < self._min_doc_frequency:
                    continue
                out.append((term, n, f, doc_f))
        return out


_SELECT_UNITS_SQL = """
SELECT u.text AS text
  FROM transcript_units u
  JOIN transcripts t ON t.id = u.transcript_id
  JOIN videos v ON v.id = t.video_id
 WHERE v.library_id = $1
"""

_UPSERT_TERM_SQL = """
INSERT INTO search_suggestion_terms
       (library_id, term, term_normalized, ngram, frequency, doc_frequency, last_seen_at)
VALUES ($1, $2, $3, $4, $5, $6, now())
ON CONFLICT (library_id, term_normalized) DO UPDATE
   SET frequency = EXCLUDED.frequency,
       doc_frequency = EXCLUDED.doc_frequency,
       last_seen_at = now()
"""


async def build_ngrams_for_library(
    db: _DBConn,
    *,
    library_id: UUID,
    extractor: NgramExtractor | None = None,
) -> int:
    """Rebuild the suggestion-term table for one library.

    Returns the number of terms upserted (i.e. the number of rows
    that passed both frequency thresholds). The build runs in a
    single transaction so partial failure leaves the previous build
    intact.
    """
    used_extractor = extractor or NgramExtractor()

    rows = await db.fetch(_SELECT_UNITS_SQL, library_id)
    texts: list[str] = []
    for row in rows:
        text = row["text"]
        if text is None:
            continue
        texts.append(str(text))

    tuples = used_extractor.extract(texts)

    written = 0
    async with db.transaction():
        for term, n, frequency, doc_frequency in tuples:
            normalized = arabic_normalize(term).strip()
            if not normalized:
                continue
            await db.execute(
                _UPSERT_TERM_SQL,
                library_id,
                term,
                normalized,
                n,
                frequency,
                doc_frequency,
            )
            written += 1
    return written
