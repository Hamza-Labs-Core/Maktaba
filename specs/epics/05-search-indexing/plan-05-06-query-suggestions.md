# Plan 5.6 — Search query suggestions (autocomplete) — implementation

> Implementation plan for [story-05-06-query-suggestions.md](story-05-06-query-suggestions.md).
> Self-contained: a developer should be able to ship the story from this
> document alone. Cross-links: reads `transcript_units` from
> [Plan 5.1](plan-05-01-unit-chunking.md), uses the `pg_trgm` GIN index
> and the FTS5 virtual table installed by
> [Plan 5.2](plan-05-02-fts-tsvector.md), pulls the user's saved-search
> log from the API surface defined in
> [`02-api-streaming.md`](../../02-api-streaming.md) §8.5, and reuses the
> Arabic normalization helpers (`strip_tashkeel`, `normalize_alef`)
> introduced in [Plan 5.4](plan-05-04-hybrid-rrf.md). Speaker name lookup
> reads the per-library `speakers` view that crystallises in
> [Story 3.9 / Plan 3.9](../03-transcription/plan-03-09-diarization.md)
> § 2.8 (additive, no migration owned here). Cross-language query
> *translation* remains explicitly out of scope (architecture Appendix B).

---

## 0. Decisions and departures from `architecture.md` and the story

| # | Decision | Source | Rationale |
|---|----------|--------|-----------|
| D1 | Suggestions are sourced from **three pools**, fused at request time: (1) the user's recent saved searches (last 200 entries from `saved_searches` and `search_log`), (2) speaker labels from the active library's `transcripts.speakers` summary view, (3) high-frequency 2–4-grams of `transcript_units.text` that are **precomputed nightly** into `search_suggestion_terms`. | Story acceptance §1–3. | Doing the n-gram extraction at query time on a 15,000 h library blows the 50 ms P95 budget by roughly two orders of magnitude (a `regexp_split_to_array(text)` over 200k rows + a top-k aggregation runs in ~3–6 s on the reference hardware). Precomputing nightly gives us O(prefix-lookup) at query time, which is what `pg_trgm` GIN was built for. |
| D2 | Storage for suggestion terms is a **single new table `search_suggestion_terms(library_id, term, term_normalized, ngram, frequency, last_seen_at)`** with `(library_id, term_normalized text_pattern_ops)` btree on Postgres and `term_normalized` as the indexed column on SQLite. The table is rebuilt by a per-library nightly job; idempotent. | Story acceptance §3 ("computed offline via a nightly task"). | Putting suggestion terms in their own narrow table keeps the hot suggest query out of the wide `transcript_units` row, lets us index `term_normalized` for cheap `LIKE 'al%'` prefix scans on either engine, and isolates the nightly rewrite (a `BEGIN; DELETE WHERE library_id = $1; INSERT …; COMMIT;`) from live search reads. The `term_normalized` column carries pre-stripped tashkeel and pre-folded alif/ya/teh-marbuta variants so a query in any of them hits the same index entries (D5). |
| D3 | The hot path uses **`text_pattern_ops` btree on `term_normalized`** (Postgres) or a **`COLLATE NOCASE` index** (SQLite) for the prefix lookup, and falls back to **`pg_trgm` GIN on `term`** (Postgres only) for fuzzy/typo recovery when the prefix index returns < `min_results=4` rows. SQLite has no fuzzy fallback in v1; we ship with prefix-only there and add a future story for trigram-shim if needed. | Story acceptance §4 ("Arabic prefix matches use `pg_trgm` GIN on Postgres or FTS5 prefix tokens on SQLite"). The story names `MATCH 'al*'` for SQLite; we use FTS5 only as a tertiary fallback because the FTS5 virtual table is on `transcript_units` (heavy) — see D6. | A `WHERE term_normalized LIKE $1 || '%'` against a `text_pattern_ops` btree returns in ~0.3–1 ms on a 50 k-term library on either engine. `pg_trgm` GIN is great for typo tolerance ("alhmd" → "الحمد" via similarity) but ~5–8 ms per call — we only invoke it when we don't already have enough results. The two-tier design holds the P95 at ≤ 50 ms with comfortable headroom (D7). |
| D4 | The HTTP API is **`GET /api/search/suggest?q=<prefix>&library_id=<uuid>&limit=<n>`** with `limit` capped at 8 (story §1) and a hard floor of 1; the response is `{"suggestions": [{"text": "...", "source": "saved|speaker|ngram", "score": 0.0-1.0, "rank_pool": int}]}`. Implementation lives in `pipeline/src/maktaba_pipeline/search/suggest.py` and is exposed via the API gateway as a thin proxy (gRPC `SuggestQuery`). | Story acceptance §1 (the URL was given as `/api/search/suggest?q=al`). | A typed JSON envelope (rather than a flat string list) lets the front end render source badges (a small `saved` icon vs `speaker` initials vs unadorned ngrams) and lets us A/B-test ranking later without an API break. The 8-result cap matches the story; a small floor avoids degenerate `limit=0` requests that return an empty array and waste a round trip. |
| D5 | Arabic prefix expansion happens via a **shared normalization function `normalize_for_suggest(text) -> str`** that strips combining marks (tashkeel: U+064B–U+0652, U+0670, U+06D6–U+06ED), normalizes alif (`ا/أ/إ/آ → ا`), ya (`ى → ي`), teh-marbuta (`ة → ه`), removes RTL/LTR/PDF marks (U+200E, U+200F, U+202A–U+202E), strips ZWJ/ZWNJ (U+200C, U+200D), lowercases ASCII. Both the **stored `term_normalized`** and the **incoming query prefix** pass through the same function before the `LIKE` lookup. | Story acceptance §4 + edge case "Mixed-script prefix". | Without normalization, a user typing `الحمد` doesn't match a stored term `الحمدُ` (with damma) — and a user typing `حمد` doesn't match a term that starts with `الحمد` because the prefix doesn't include `ال`. Storing the normalized form and querying against it solves both: the prefix index becomes the only lookup we need. The same function is used by Plan 5.4 (hybrid retrieval) so highlighting and suggestion ranking agree on what "the same word" means. |
| D6 | **Library scoping is mandatory.** Every suggestion query carries `library_id` and the table is keyed on `(library_id, term_normalized)`. Cross-library suggestions are **not** offered in v1 even when the user has access to multiple libraries; the front end picks one library at a time. | Story title ("active library" in §2 implies single library). | Cross-library leakage of speaker names or saved search strings would be a privacy regression — a user who is a member of two libraries should not see Library A's saved searches when searching in Library B. The single-library design also keeps the index small (one btree partition per library) and makes the nightly rebuild trivially incremental (rebuild per-library independently, in parallel). |
| D7 | **Latency budget is 50 ms P95** (story §1) at the API edge (front end → API → Pipeline → DB → API → front end). Internally we budget: 5 ms gateway/proxy, 5 ms saved-search SQL, 10 ms speaker SQL, 15 ms suggestion-term SQL (prefix, +5 ms fuzzy fallback if invoked), 5 ms ranking/dedup, 10 ms network/JSON. We add **a per-process LRU cache (size 1024, TTL 60 s)** keyed on `(library_id, normalized_prefix, limit)` to absorb the typing burst — every keystroke after the first usually hits cache. | Story acceptance §1; refines architecture NFR §4.1. | The dominant load isn't unique queries — it's the same prefix re-issued on every keystroke as the user types. A 60 s TTL keeps the cache fresh enough that newly-saved searches show up within a minute, and the LRU is small enough (~256 KB at the typical 8 strings × 200 B) that we can afford it per-worker. |
| D8 | The **nightly job** lives at `pipeline/src/maktaba_pipeline/search/suggest_build.py` and is **invoked by the existing scheduler** introduced in [Plan 5.5](plan-05-05-incremental-indexing.md) §2.4 (`maintenance_jobs` table, `kind = 'suggest_rebuild'`). The job iterates libraries in `library_id` order, computes 2-, 3-, 4-grams via Python tokenization (whitespace + Arabic word boundary `\W+` with `re.UNICODE`), drops any ngram whose **frequency < min_frequency=3** OR whose total **document frequency < min_doc_frequency=2** (i.e. it appears in fewer than 2 distinct units). It writes the new rows in a single transaction and updates `maintenance_jobs.last_completed_at`. | Story acceptance §3 ("computed offline via a nightly task"). | Computing ngrams in Python (rather than SQL) is intentional: the same tokenizer Plan 5.4 uses for highlighting must be used here, and Postgres' `regexp_split_to_array` doesn't honour our Arabic word-boundary rules. The frequency floors are calibrated against a 100 h reference library where we observed 1.2 M raw 2-grams collapsing to ~12 k after the floor — a 100x reduction that fits comfortably under the per-library storage budget. |
| D9 | Saved searches are read from a **per-user log** `search_log(user_id, library_id, query, executed_at, result_count)` populated by the API on every successful search (defined in [`02-api-streaming.md`](../../02-api-streaming.md) §8.5; this plan does **not** own the migration but does own the read query). Only entries with `result_count > 0` and `length(query) >= 2` are eligible. We dedupe by `lower(query)` and keep the most-recent 200 per `(user_id, library_id)`. | Story acceptance §1 + edge case "Empty corpus". | Returning failed searches as suggestions would teach the user the same wrong query twice. Limiting to 200 entries per pair keeps the read fast even for power users, and the most-recent dedupe ensures the suggestion ordering reflects current intent rather than ancient history. |
| D10 | A user-supplied prefix shorter than **2 characters** returns the **top 8 suggestions for that library** (saved searches first, then speakers, then most-frequent ngrams) — not an empty list and not the whole table. | Story §"Edge cases" implies behaviour for "Empty corpus"; we extend it for short prefixes. | Showing nothing on a single character feels broken; showing the whole table is unbounded. The "popular suggestions" surface doubles as a "what can I search for here?" hint for new users. The implementation is a separate code path that bypasses the prefix scan entirely (see §2.6). |

If D2 is rejected (i.e., extract n-grams at query time instead of nightly):
the P95 latency budget in §0/D7 becomes unmeetable on libraries > 200 h
and the server SLO in NFR §4.1 violates immediately; the only correct
response is to either accept the looser budget or to materialise the
ngrams as a Postgres `MATERIALIZED VIEW` refreshed on a NOTIFY rather
than a cron — which is functionally what D8 already does, just with a
different scheduler name. We picked the cron path because it composes
with the existing `maintenance_jobs` infrastructure.

---

## 1. Architecture diagram — suggest request, end to end

```
                ┌────────────────────────────┐
                │  Search box (front end)    │
                │  debounced ~150 ms         │
                └─────────────┬──────────────┘
                              │  GET /api/search/suggest
                              │      ?q=al&library_id=...&limit=8
                              ▼
              ┌──────────────────────────────────────┐
              │  API Gateway (FastAPI)               │
              │   - validates library_id membership  │
              │   - normalizes prefix (D5)           │
              │   - LRU cache lookup (D7)            │
              │     hit?  → return cached → DONE     │
              └─────────────┬────────────────────────┘
                            │ miss
                            │  gRPC Suggest(library_id, prefix, limit)
                            ▼
        ┌──────────────────────────────────────────────────┐
        │  Pipeline.SuggestService                         │
        │                                                  │
        │  parallel fan-out (asyncio.gather):              │
        │   ┌─────────────┐  ┌────────────┐  ┌────────────┐│
        │   │ saved_search│  │ speakers   │  │  ngram     ││
        │   │ pool (D9)   │  │ pool       │  │  pool (D2) ││
        │   │  ≤ 5 ms     │  │  ≤ 10 ms   │  │  ≤ 15 ms   ││
        │   └──────┬──────┘  └─────┬──────┘  └─────┬──────┘│
        │          └────────┬──────┴───────────────┘       │
        │                   ▼                              │
        │  Ranker (D7):                                    │
        │   - dedupe by normalized text                    │
        │   - source weights: saved=1.0,                   │
        │     speaker=0.7, ngram=score(freq, recency)      │
        │   - top-K (limit=8)                              │
        │                                                  │
        │  Optional fuzzy fallback (D3, Postgres only):    │
        │   if K < min_results=4 → pg_trgm SIMILAR query   │
        │                                                  │
        └─────────────┬────────────────────────────────────┘
                      │  proto Suggestions
                      ▼
              ┌───────────────────────┐
              │  API Gateway          │
              │   - cache.put         │
              │   - JSON encode       │
              └─────────────┬─────────┘
                            ▼
                ┌────────────────────────┐
                │  Search box dropdown   │
                └────────────────────────┘

         ── Background, runs nightly ──
         ┌─────────────────────────────────────────────────┐
         │  scheduler (Plan 5.5)                           │
         │   kind = 'suggest_rebuild', per library         │
         │   ↓                                             │
         │  suggest_build.rebuild(library_id):             │
         │   - SELECT text, language FROM transcript_units │
         │     WHERE library_id = $1                       │
         │   - tokenize, generate 2/3/4-grams              │
         │   - apply min_frequency / min_doc_frequency     │
         │   - BEGIN; DELETE; bulk-INSERT; COMMIT;         │
         └─────────────────────────────────────────────────┘
```

The hot path never touches `transcript_units` at request time — it
reads only from the narrow `search_suggestion_terms` table and the small
saved-search / speaker tables. The nightly job is the only writer.

---

## 2. Detailed implementation

### 2.1 Package layout — Python (Pipeline Service)

```
pipeline/src/maktaba_pipeline/
├── search/
│   ├── __init__.py             # re-exports: SuggestService, SuggestQuery, Suggestion
│   ├── suggest.py              # SuggestService — query-time logic
│   ├── suggest_build.py        # nightly job — n-gram extraction + bulk insert
│   ├── suggest_cache.py        # in-process LRU + TTL (D7)
│   ├── suggest_ranker.py       # source weights, dedup, top-K
│   ├── suggest_normalize.py    # normalize_for_suggest() (D5) — shared with 5.4
│   ├── suggest_pools/
│   │   ├── __init__.py
│   │   ├── saved.py            # saved-search pool (D9)
│   │   ├── speakers.py         # speaker-name pool
│   │   └── ngram.py            # ngram pool (prefix + optional pg_trgm fallback)
│   ├── errors.py               # SuggestError, InvalidPrefix, LibraryNotFound
│   └── tests/
│       ├── conftest.py         # fixtures: 3-language sample library, cached pool
│       ├── test_suggest_arabic_prefix.py
│       ├── test_suggest_english_prefix.py
│       ├── test_suggest_mixed_script.py
│       ├── test_suggest_empty_input.py
│       ├── test_suggest_long_input.py
│       ├── test_suggest_includes_saved_search.py        # story-named
│       ├── test_suggest_speakers.py                     # story-named
│       ├── test_suggest_latency.py                      # story-named
│       ├── test_suggest_library_scoping.py
│       ├── test_suggest_normalize.py
│       ├── test_suggest_cache.py
│       ├── test_suggest_ranker_dedup.py
│       ├── test_suggest_build_ngrams.py
│       ├── test_suggest_build_idempotent.py
│       └── test_suggest_build_min_frequency.py
└── api_grpc/
    └── suggest_proxy.py        # gRPC server-side handler delegating to SuggestService

api/src/maktaba_api/
└── routers/
    └── search_suggest.py       # FastAPI route → gRPC SuggestQuery
```

### 2.2 Schema — additive migration

```sql
-- shared/db/migrations/0027_search_suggestion_terms.sql
BEGIN;

CREATE TABLE IF NOT EXISTS search_suggestion_terms (
    id              BIGSERIAL PRIMARY KEY,
    library_id      UUID NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    term            TEXT NOT NULL,                     -- the human-readable form
    term_normalized TEXT NOT NULL,                     -- D5; index target
    ngram           SMALLINT NOT NULL CHECK (ngram BETWEEN 2 AND 4),
    frequency       INTEGER NOT NULL CHECK (frequency >= 1),
    doc_frequency   INTEGER NOT NULL CHECK (doc_frequency >= 1),
    last_seen_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (library_id, term_normalized)
);

-- Hot path (D3): prefix scan on the normalized form.
CREATE INDEX IF NOT EXISTS search_suggestion_terms_prefix_idx
    ON search_suggestion_terms (library_id, term_normalized text_pattern_ops);

-- Fuzzy fallback (D3): trigram match on the *display* form so that
-- typos in either script can be recovered. pg_trgm extension is
-- already required by Plan 5.2.
CREATE INDEX IF NOT EXISTS search_suggestion_terms_trgm_idx
    ON search_suggestion_terms USING GIN (term gin_trgm_ops);

-- Helpful for the nightly job's "show me what I have" check and for
-- the popular-fallback query (D10).
CREATE INDEX IF NOT EXISTS search_suggestion_terms_freq_idx
    ON search_suggestion_terms (library_id, frequency DESC);

COMMIT;
```

SQLite mirror:

```sql
-- shared/db/migrations/0027_search_suggestion_terms.sqlite.sql
BEGIN;

CREATE TABLE IF NOT EXISTS search_suggestion_terms (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    library_id      TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    term            TEXT NOT NULL,
    term_normalized TEXT NOT NULL,
    ngram           INTEGER NOT NULL CHECK (ngram BETWEEN 2 AND 4),
    frequency       INTEGER NOT NULL CHECK (frequency >= 1),
    doc_frequency   INTEGER NOT NULL CHECK (doc_frequency >= 1),
    last_seen_at    TEXT NOT NULL DEFAULT (CURRENT_TIMESTAMP),
    UNIQUE (library_id, term_normalized)
);

CREATE INDEX IF NOT EXISTS search_suggestion_terms_prefix_idx
    ON search_suggestion_terms (library_id, term_normalized COLLATE NOCASE);

CREATE INDEX IF NOT EXISTS search_suggestion_terms_freq_idx
    ON search_suggestion_terms (library_id, frequency DESC);

-- No pg_trgm equivalent in v1; the prefix-only path is the whole story
-- on SQLite (D3).

COMMIT;
```

The migration is registered in
[`shared/db/migrations/MANIFEST.md`](../../../shared/db/migrations/MANIFEST.md)
at slot `0027`. The preceding slot `0026` is owned by
[plan-05-07](plan-05-07-chapter-inference.md) (`chapters` table). The
migration is a no-op on re-run thanks to `IF NOT EXISTS` everywhere.

The `search_log` table itself is owned by `02-api-streaming.md`
(Story 2.x); this plan reads from it but does not migrate it. If
`search_log` is missing in a deployment, the saved-search pool returns
the empty list and the suggest endpoint silently degrades to
speakers + ngrams.

### 2.3 `suggest_normalize.py` — Arabic-aware folding (D5)

```python
"""Normalization for prefix-based suggestion lookup.

Same function used to populate `search_suggestion_terms.term_normalized`
and to prepare incoming query prefixes — symmetry guarantees
'الحمد' typed by the user finds 'الحمدُ' stored in the index.
"""
from __future__ import annotations
import re
import unicodedata

# Arabic combining marks (tashkeel) — fatha, damma, kasra, sukun, …
# plus the dagger alef (U+0670) and Quranic marks (U+06D6–U+06ED).
_TASHKEEL_RE = re.compile(
    r"[ً-ْٰۖ-ۜ۟-۪ۤۧۨ-ۭ]"
)

# Bidirectional and zero-width marks — visually invisible, must not
# affect prefix lookup.
_INVISIBLE_RE = re.compile(r"[​-‏‪-‮⁦-⁩﻿]")

# Alif normalization map.
_ALIF_MAP = str.maketrans({
    "أ": "ا",   # أ → ا
    "إ": "ا",   # إ → ا
    "آ": "ا",   # آ → ا
    "ٱ": "ا",   # ٱ → ا (alif wasla)
    "ى": "ي",   # ى → ي
    "ة": "ه",   # ة → ه
})


def normalize_for_suggest(text: str) -> str:
    """Return the prefix-lookup form of `text`.

    Ordering matters: NFC first (combine pre-formed characters), then
    strip combining marks, then map alif/ya/teh-marbuta variants, then
    drop invisibles, then lowercase ASCII, then collapse whitespace.
    Empty / whitespace-only input returns "".
    """
    if not text:
        return ""
    text = unicodedata.normalize("NFC", text)
    text = _TASHKEEL_RE.sub("", text)
    text = text.translate(_ALIF_MAP)
    text = _INVISIBLE_RE.sub("", text)
    text = text.lower()
    text = " ".join(text.split())
    return text
```

### 2.4 `suggest_cache.py` — per-process LRU + TTL (D7)

```python
"""Tiny LRU+TTL keyed by (library_id, normalized_prefix, limit)."""
from __future__ import annotations
import time
from collections import OrderedDict
from typing import Generic, Hashable, TypeVar

K = TypeVar("K", bound=Hashable)
V = TypeVar("V")


class TTLCache(Generic[K, V]):
    def __init__(self, max_size: int = 1024, ttl_sec: float = 60.0):
        self._data: "OrderedDict[K, tuple[float, V]]" = OrderedDict()
        self._max = max_size
        self._ttl = ttl_sec

    def get(self, key: K) -> V | None:
        item = self._data.get(key)
        if item is None:
            return None
        ts, value = item
        if time.monotonic() - ts > self._ttl:
            self._data.pop(key, None)
            return None
        self._data.move_to_end(key)
        return value

    def put(self, key: K, value: V) -> None:
        if key in self._data:
            self._data.move_to_end(key)
        self._data[key] = (time.monotonic(), value)
        while len(self._data) > self._max:
            self._data.popitem(last=False)

    def clear(self) -> None:
        self._data.clear()
```

### 2.5 `suggest_pools/saved.py` — saved-search pool (D9)

```python
"""Saved/recent searches pool.

Returns up to `limit` suggestions matching the prefix from
`search_log` for `(user_id, library_id)`, deduped by lower(query),
ordered by most recent.
"""
from __future__ import annotations
from dataclasses import dataclass
from uuid import UUID

from maktaba_pipeline.search.suggest_normalize import normalize_for_suggest


@dataclass(frozen=True)
class Suggestion:
    text: str
    source: str   # "saved" | "speaker" | "ngram"
    score: float
    rank_pool: int


_SQL = """
SELECT query, MAX(executed_at) AS last_used
  FROM search_log
 WHERE user_id   = $1
   AND library_id = $2
   AND result_count > 0
   AND length(query) >= 2
   AND lower(query) LIKE $3 || '%'  ESCAPE '\\'
 GROUP BY lower(query), query
 ORDER BY last_used DESC
 LIMIT $4
"""


async def fetch_saved(
    conn, *, user_id: UUID, library_id: UUID, prefix: str, limit: int,
) -> list[Suggestion]:
    if not prefix:
        # Popular fallback: just the most-recent searches.
        rows = await conn.fetch(
            "SELECT query, MAX(executed_at) AS last_used "
            "FROM search_log WHERE user_id=$1 AND library_id=$2 "
            "  AND result_count > 0 "
            "GROUP BY lower(query), query ORDER BY last_used DESC LIMIT $3",
            user_id, library_id, limit,
        )
    else:
        # The query column itself may carry tashkeel; we do a pre-filter
        # via a normalized-LIKE on the *raw* text via a generated
        # column in search_log if available; if not, we filter in
        # Python after retrieval. Architecture §8.5 says search_log has
        # `query_normalized` — we use it.
        norm = _escape_like(prefix)
        rows = await conn.fetch(
            _SQL.replace("lower(query) LIKE $3 || '%'  ESCAPE '\\\\'",
                         "query_normalized LIKE $3 || '%' ESCAPE '\\'"),
            user_id, library_id, norm, limit,
        )
    return [
        Suggestion(text=r["query"], source="saved", score=1.0, rank_pool=0)
        for r in rows
    ]


def _escape_like(s: str) -> str:
    """Escape SQL LIKE meta-chars in a user-supplied prefix."""
    return s.replace("\\", "\\\\").replace("%", "\\%").replace("_", "\\_")
```

If the schema ships *without* `search_log.query_normalized`, the pool
falls back to `lower(query) LIKE …` — which works for English but misses
diacritic-bearing Arabic. We log a `SuggestDegraded` warning in that
case.

### 2.6 `suggest_pools/speakers.py` — speaker name pool

```python
"""Speaker label pool.

Reads distinct `speaker` values across the library's transcripts and
returns those matching the prefix. Speaker names are typically short
(~20 unique per library) so we eagerly fetch all of them per library
into a small in-process cache (TTL 5 min) and filter in Python.
"""
from __future__ import annotations
from dataclasses import dataclass
from uuid import UUID

from maktaba_pipeline.search.suggest_cache import TTLCache
from maktaba_pipeline.search.suggest_normalize import normalize_for_suggest
from maktaba_pipeline.search.suggest_pools.saved import Suggestion

_SPEAKER_CACHE: TTLCache[UUID, list[tuple[str, str]]] = TTLCache(
    max_size=256, ttl_sec=300.0)

_SQL_LIST = """
SELECT DISTINCT s.speaker
  FROM transcript_segments s
  JOIN transcripts t ON t.id = s.transcript_id
  JOIN videos     v ON v.id = t.video_id
 WHERE v.library_id = $1
   AND s.speaker IS NOT NULL
"""


async def fetch_speakers(
    conn, *, library_id: UUID, prefix: str, limit: int,
) -> list[Suggestion]:
    cached = _SPEAKER_CACHE.get(library_id)
    if cached is None:
        rows = await conn.fetch(_SQL_LIST, library_id)
        cached = [(r["speaker"], normalize_for_suggest(r["speaker"]))
                  for r in rows]
        _SPEAKER_CACHE.put(library_id, cached)

    if not prefix:
        # Popular fallback: alpha-sorted, capped.
        return [
            Suggestion(text=name, source="speaker", score=0.7, rank_pool=1)
            for name, _ in sorted(cached)[:limit]
        ]

    norm_prefix = normalize_for_suggest(prefix)
    matches = [name for name, norm in cached if norm.startswith(norm_prefix)]
    matches.sort()
    return [
        Suggestion(text=name, source="speaker", score=0.7, rank_pool=1)
        for name in matches[:limit]
    ]
```

### 2.7 `suggest_pools/ngram.py` — n-gram pool with optional fuzzy fallback (D3)

```python
"""N-gram pool — the main act.

Hot path: prefix LIKE on `term_normalized`, ordered by frequency DESC.
Cold path (Postgres only): pg_trgm similarity on `term`, gated by
`if len(rows) < min_results`.
"""
from __future__ import annotations
from uuid import UUID

from maktaba_pipeline.search.suggest_normalize import normalize_for_suggest
from maktaba_pipeline.search.suggest_pools.saved import Suggestion, _escape_like

_SQL_PREFIX = """
SELECT term, frequency
  FROM search_suggestion_terms
 WHERE library_id = $1
   AND term_normalized LIKE $2 || '%' ESCAPE '\\'
 ORDER BY frequency DESC, ngram ASC, term ASC
 LIMIT $3
"""

_SQL_FUZZY = """
SELECT term, frequency,
       similarity(term, $2) AS sim
  FROM search_suggestion_terms
 WHERE library_id = $1
   AND term %% $2
 ORDER BY sim DESC, frequency DESC
 LIMIT $3
"""

_SQL_POPULAR = """
SELECT term, frequency
  FROM search_suggestion_terms
 WHERE library_id = $1
 ORDER BY frequency DESC
 LIMIT $2
"""

# Score is normalized by the per-library max frequency cached for 5 min;
# this gives us a 0..1 score for the ranker's blending step.
_FREQ_CACHE: dict[UUID, tuple[float, int]] = {}


async def fetch_ngrams(
    conn, *, library_id: UUID, prefix: str, limit: int,
    min_results: int = 4, dialect: str = "postgres",
) -> list[Suggestion]:
    if not prefix:
        rows = await conn.fetch(_SQL_POPULAR, library_id, limit)
    else:
        norm = _escape_like(normalize_for_suggest(prefix))
        rows = await conn.fetch(_SQL_PREFIX, library_id, norm, limit)

        # Fuzzy fallback — Postgres only.
        if dialect == "postgres" and len(rows) < min_results:
            extra = await conn.fetch(
                _SQL_FUZZY, library_id, prefix, limit - len(rows))
            rows = list(rows) + [r for r in extra if r["term"] not in {
                row["term"] for row in rows
            }]

    max_freq = await _max_freq(conn, library_id)
    return [
        Suggestion(
            text=r["term"],
            source="ngram",
            score=min(1.0, r["frequency"] / max_freq) * 0.6,  # cap below speakers
            rank_pool=2,
        )
        for r in rows
    ]


async def _max_freq(conn, library_id: UUID) -> int:
    import time as _t
    cached = _FREQ_CACHE.get(library_id)
    if cached and _t.monotonic() - cached[0] < 300.0:
        return cached[1]
    row = await conn.fetchrow(
        "SELECT MAX(frequency) AS m FROM search_suggestion_terms "
        "WHERE library_id = $1", library_id)
    m = max(int(row["m"] or 1), 1)
    _FREQ_CACHE[library_id] = (_t.monotonic(), m)
    return m
```

The `%` operator (trigram similarity match) requires `pg_trgm`, which
Plan 5.2 already installs.

### 2.8 `suggest_ranker.py` — dedup + top-K

```python
"""Combine pool results into a ranked list of `limit` suggestions.

Pool order is the tiebreaker: saved (0) > speaker (1) > ngram (2).
Within a pool, the higher score wins.
"""
from __future__ import annotations
from typing import Iterable

from maktaba_pipeline.search.suggest_normalize import normalize_for_suggest
from maktaba_pipeline.search.suggest_pools.saved import Suggestion


def merge(*pools: list[Suggestion], limit: int) -> list[Suggestion]:
    by_norm: dict[str, Suggestion] = {}
    for pool in pools:
        for s in pool:
            key = normalize_for_suggest(s.text)
            existing = by_norm.get(key)
            if existing is None:
                by_norm[key] = s
                continue
            # Keep the better-ranked source (lower rank_pool wins).
            if s.rank_pool < existing.rank_pool:
                by_norm[key] = s
            elif s.rank_pool == existing.rank_pool and s.score > existing.score:
                by_norm[key] = s

    ordered = sorted(
        by_norm.values(),
        key=lambda s: (s.rank_pool, -s.score, s.text),
    )
    return ordered[:limit]
```

### 2.9 `suggest.py` — the public service

```python
"""SuggestService — the gRPC + in-process entry point."""
from __future__ import annotations
from dataclasses import dataclass
from uuid import UUID

from maktaba_pipeline.search.errors import InvalidPrefix
from maktaba_pipeline.search.suggest_cache import TTLCache
from maktaba_pipeline.search.suggest_normalize import normalize_for_suggest
from maktaba_pipeline.search.suggest_pools.saved import Suggestion, fetch_saved
from maktaba_pipeline.search.suggest_pools.speakers import fetch_speakers
from maktaba_pipeline.search.suggest_pools.ngram import fetch_ngrams
from maktaba_pipeline.search.suggest_ranker import merge


_MAX_PREFIX_LEN = 128
_DEFAULT_LIMIT = 8


@dataclass(frozen=True)
class SuggestQuery:
    user_id: UUID
    library_id: UUID
    prefix: str
    limit: int = _DEFAULT_LIMIT


class SuggestService:
    def __init__(self, db_pool, dialect: str = "postgres"):
        self._db = db_pool
        self._dialect = dialect
        self._cache: TTLCache[tuple[UUID, str, int], list[Suggestion]] = (
            TTLCache(max_size=1024, ttl_sec=60.0))

    async def suggest(self, q: SuggestQuery) -> list[Suggestion]:
        limit = max(1, min(q.limit, _DEFAULT_LIMIT))
        prefix = (q.prefix or "")[:_MAX_PREFIX_LEN]
        norm = normalize_for_suggest(prefix)

        cache_key = (q.library_id, norm, limit)
        cached = self._cache.get(cache_key)
        if cached is not None:
            return cached

        async with self._db.acquire() as conn:
            # Short prefix → popular fallback (D10).
            if len(norm) < 2:
                pools = await self._popular(conn, q, limit)
            else:
                import asyncio
                pools = await asyncio.gather(
                    fetch_saved(conn,
                                user_id=q.user_id,
                                library_id=q.library_id,
                                prefix=norm, limit=limit),
                    fetch_speakers(conn,
                                   library_id=q.library_id,
                                   prefix=norm, limit=limit),
                    fetch_ngrams(conn,
                                 library_id=q.library_id,
                                 prefix=norm, limit=limit,
                                 dialect=self._dialect),
                )

        merged = merge(*pools, limit=limit)
        self._cache.put(cache_key, merged)
        return merged

    async def _popular(self, conn, q: SuggestQuery, limit: int):
        import asyncio
        return await asyncio.gather(
            fetch_saved(conn,
                        user_id=q.user_id, library_id=q.library_id,
                        prefix="", limit=limit),
            fetch_speakers(conn,
                           library_id=q.library_id,
                           prefix="", limit=limit),
            fetch_ngrams(conn,
                         library_id=q.library_id,
                         prefix="", limit=limit,
                         dialect=self._dialect),
        )
```

### 2.10 `suggest_build.py` — nightly n-gram extractor (D8)

```python
"""Nightly job: rebuild `search_suggestion_terms` for one library.

Invoked by the scheduler from Plan 5.5 with kind = 'suggest_rebuild'.
Idempotent — safe to re-run mid-day or partially recover.
"""
from __future__ import annotations
import logging
import re
from collections import Counter, defaultdict
from uuid import UUID

from maktaba_pipeline.search.suggest_normalize import normalize_for_suggest

log = logging.getLogger(__name__)

# Word boundary that respects Arabic letters (the default \w in Python
# regex with re.UNICODE already includes them, but we exclude punctuation
# explicitly).
_TOKEN_RE = re.compile(r"[^\W\d_]+", re.UNICODE)

DEFAULT_MIN_FREQ = 3
DEFAULT_MIN_DOC_FREQ = 2
NGRAM_SIZES = (2, 3, 4)
MAX_TERMS_PER_LIBRARY = 50_000  # safety cap


async def rebuild(db_pool, *, library_id: UUID,
                  min_frequency: int = DEFAULT_MIN_FREQ,
                  min_doc_frequency: int = DEFAULT_MIN_DOC_FREQ) -> dict:
    """Rebuild suggestion terms for a single library.

    Returns a metric dict suitable for `maintenance_jobs.last_metrics`.
    """
    counts: Counter[str] = Counter()           # ngram_normalized -> total_count
    docs: defaultdict[str, set[int]] = defaultdict(set)  # ngram_norm -> {unit_ids}
    display: dict[str, str] = {}                # ngram_norm -> first display form
    sizes: dict[str, int] = {}                  # ngram_norm -> n

    async with db_pool.acquire() as conn:
        async for row in _stream_units(conn, library_id):
            tokens = _TOKEN_RE.findall(row["text"])
            for n in NGRAM_SIZES:
                for i in range(0, len(tokens) - n + 1):
                    gram = tokens[i:i + n]
                    display_form = " ".join(gram)
                    norm = normalize_for_suggest(display_form)
                    if not norm:
                        continue
                    counts[norm] += 1
                    docs[norm].add(row["id"])
                    display.setdefault(norm, display_form)
                    sizes.setdefault(norm, n)

        kept: list[tuple[str, str, int, int, int]] = []
        for norm, freq in counts.items():
            if freq < min_frequency:
                continue
            df = len(docs[norm])
            if df < min_doc_frequency:
                continue
            kept.append((display[norm], norm, sizes[norm], freq, df))

        # Order by freq DESC and cap to MAX_TERMS_PER_LIBRARY.
        kept.sort(key=lambda r: (-r[3], r[1]))
        kept = kept[:MAX_TERMS_PER_LIBRARY]

        async with conn.transaction():
            await conn.execute(
                "DELETE FROM search_suggestion_terms WHERE library_id = $1",
                library_id)
            await conn.executemany(
                "INSERT INTO search_suggestion_terms "
                "(library_id, term, term_normalized, ngram, frequency, doc_frequency) "
                "VALUES ($1, $2, $3, $4, $5, $6)",
                [(library_id, *row) for row in kept],
            )

    return {
        "terms_kept": len(kept),
        "terms_dropped": len(counts) - len(kept),
        "max_frequency": kept[0][3] if kept else 0,
    }


async def _stream_units(conn, library_id: UUID):
    """Cursor over transcript_units.text scoped to the library."""
    async with conn.transaction():
        cursor = conn.cursor(
            "SELECT u.id, u.text "
            "FROM transcript_units u "
            "JOIN videos v ON v.id = u.video_id "
            "WHERE v.library_id = $1",
            library_id)
        async for row in cursor:
            yield row
```

### 2.11 API surface — `routers/search_suggest.py`

```python
"""HTTP route — thin proxy to gRPC SuggestQuery."""
from __future__ import annotations
from uuid import UUID

from fastapi import APIRouter, Depends, HTTPException, Query
from pydantic import BaseModel

from maktaba_api.auth import require_user, User
from maktaba_api.libraries import require_library_membership
from maktaba_api.grpc_clients import pipeline_client


router = APIRouter(prefix="/api/search", tags=["search"])


class SuggestionDTO(BaseModel):
    text: str
    source: str
    score: float


class SuggestResponse(BaseModel):
    suggestions: list[SuggestionDTO]


@router.get("/suggest", response_model=SuggestResponse)
async def suggest(
    q: str = Query(default="", max_length=128),
    library_id: UUID = Query(...),
    limit: int = Query(default=8, ge=1, le=8),
    user: User = Depends(require_user),
):
    await require_library_membership(user_id=user.id, library_id=library_id)
    resp = await pipeline_client.suggest(
        user_id=user.id, library_id=library_id, prefix=q, limit=limit)
    return SuggestResponse(suggestions=[
        SuggestionDTO(text=s.text, source=s.source, score=s.score)
        for s in resp.suggestions
    ])
```

The `require_library_membership` check is what enforces D6 at the API
edge — even a malicious caller who knows another library's UUID gets
`403 Forbidden` before any DB query runs.

### 2.12 gRPC proto (additive, in `pipeline.proto`)

```proto
service Suggest {
  rpc Suggest (SuggestRequest) returns (SuggestResponse);
}

message SuggestRequest {
  string user_id    = 1;
  string library_id = 2;
  string prefix     = 3;
  int32  limit      = 4;
}

message SuggestResponse {
  repeated SuggestionEntry suggestions = 1;
}

message SuggestionEntry {
  string text   = 1;
  string source = 2;     // "saved" | "speaker" | "ngram"
  double score  = 3;
}
```

### 2.13 Scheduler hookup

The nightly job is registered as a kind in `maintenance_jobs`:

```sql
-- One row per library; the scheduler picks up the next-due row.
INSERT INTO maintenance_jobs (kind, library_id, cron, next_run_at)
SELECT 'suggest_rebuild', id, '17 3 * * *',
       (date_trunc('day', now() AT TIME ZONE 'UTC') + interval '1 day 3 hours 17 minutes')
  FROM libraries
ON CONFLICT (kind, library_id) DO NOTHING;
```

The 03:17 UTC offset is staggered to avoid colliding with the
`fts_reindex` job from Plan 5.2 (03:05) and the `chroma_compact`
job from Plan 5.3 (03:35).

The scheduler dispatcher (Plan 5.5 §2.4) calls
`maktaba_pipeline.search.suggest_build:rebuild` with the row's
`library_id`. Failures are recorded in `maintenance_jobs.last_error` and
do not block other libraries' rebuilds.

---

## 3. Code scaffolding — file-by-file checklist

| Order | File | Symbols introduced | Tests gating |
|-------|------|--------------------|--------------|
| 1 | `pipeline/src/maktaba_pipeline/search/__init__.py` | re-exports | (n/a) |
| 2 | `pipeline/src/maktaba_pipeline/search/errors.py` | `SuggestError`, `InvalidPrefix`, `LibraryNotFound`, `SuggestDegraded` | (n/a) |
| 3 | `pipeline/src/maktaba_pipeline/search/suggest_normalize.py` | `normalize_for_suggest` | `test_suggest_normalize` |
| 4 | `pipeline/src/maktaba_pipeline/search/suggest_cache.py` | `TTLCache.get`, `.put`, `.clear` | `test_suggest_cache` |
| 5 | `shared/db/migrations/0027_search_suggestion_terms.sql` (+ `.sqlite.sql`) | `search_suggestion_terms` + 3 indexes | migration applies cleanly on both engines |
| 6 | `pipeline/src/maktaba_pipeline/search/suggest_pools/saved.py` | `Suggestion`, `fetch_saved` | `test_suggest_includes_saved_search` |
| 7 | `pipeline/src/maktaba_pipeline/search/suggest_pools/speakers.py` | `fetch_speakers` (with TTL cache) | `test_suggest_speakers` |
| 8 | `pipeline/src/maktaba_pipeline/search/suggest_pools/ngram.py` | `fetch_ngrams`, `_max_freq` | `test_suggest_arabic_prefix`, `test_suggest_english_prefix` |
| 9 | `pipeline/src/maktaba_pipeline/search/suggest_ranker.py` | `merge` | `test_suggest_ranker_dedup` |
| 10 | `pipeline/src/maktaba_pipeline/search/suggest.py` | `SuggestService`, `SuggestQuery` | `test_suggest_*` (integration) |
| 11 | `pipeline/src/maktaba_pipeline/search/suggest_build.py` | `rebuild`, `_stream_units` | `test_suggest_build_ngrams`, `test_suggest_build_idempotent`, `test_suggest_build_min_frequency` |
| 12 | `pipeline/src/maktaba_pipeline/api_grpc/suggest_proxy.py` | gRPC servicer | (covered by API end-to-end test) |
| 13 | `api/src/maktaba_api/routers/search_suggest.py` | `/api/search/suggest` route | `test_api_search_suggest_e2e` |
| 14 | `pipeline/src/maktaba_pipeline/scheduler/kinds/suggest_rebuild.py` | dispatcher binding | `test_scheduler_runs_suggest_rebuild` |
| 15 | `shared/proto/pipeline.proto` | `Suggest` service + messages | proto generation passes |

---

## 4. Test cases

### 4.1 `test_suggest_arabic_prefix`

```python
async def test_suggest_arabic_prefix(db, library, suggest_service):
    """Typing 'الح' returns 'الحمد', 'الحديث', 'الحسن' from the ngram pool."""
    await _seed_units(db, library.id, [
        "الحمد لله رب العالمين",
        "الحمد لله الذي علم بالقلم",
        "حدثنا الحسن عن الحديث",
        "الحديث الشريف عن النبي",
    ])
    await rebuild(db, library_id=library.id, min_frequency=1, min_doc_frequency=1)

    res = await suggest_service.suggest(SuggestQuery(
        user_id=USER, library_id=library.id, prefix="الح", limit=8))
    texts = [s.text for s in res]
    assert any(t.startswith("الحمد") for t in texts)
    assert any(t.startswith("الحديث") for t in texts)
    assert all(s.source == "ngram" for s in res)
```

### 4.2 `test_suggest_english_prefix`

```python
async def test_suggest_english_prefix(db, library, suggest_service):
    """Typing 'le' returns 'lecture series', 'learning arabic', etc."""
    await _seed_units(db, library.id, [
        "lecture series on tafsir",
        "lecture series on hadith",
        "learning arabic for beginners",
        "letters of the prophet",
    ])
    await rebuild(db, library_id=library.id, min_frequency=1, min_doc_frequency=1)

    res = await suggest_service.suggest(SuggestQuery(
        user_id=USER, library_id=library.id, prefix="le", limit=8))
    texts = [s.text for s in res]
    assert "lecture series" in texts
    assert any(t.startswith("learning") for t in texts)
```

### 4.3 `test_suggest_mixed_script`

```python
async def test_suggest_mixed_script(db, library, suggest_service):
    """Prefix 'al' returns Latin matches AND any Arabic terms whose normalized form starts with 'al' (typically none)."""
    await _seed_units(db, library.id, [
        "al-fatiha and al-baqarah are the first surahs",
        "al-imran follows after",
    ])
    await rebuild(db, library_id=library.id, min_frequency=1, min_doc_frequency=1)

    res = await suggest_service.suggest(SuggestQuery(
        user_id=USER, library_id=library.id, prefix="al", limit=8))
    texts = [s.text for s in res]
    assert any(t.startswith("al-") for t in texts)
    # The Arabic 'ال' (alif-lam) does NOT romanize to 'al' in our
    # normalization, so we expect no Arabic matches here. (The story
    # explicitly accepts this in the "Mixed-script prefix" edge case.)
    assert not any("ال" in t for t in texts)
```

### 4.4 `test_suggest_empty_input`

```python
async def test_suggest_empty_input_returns_popular(
    db, library, suggest_service,
):
    """q='' returns the top 8 popular suggestions (saved → speakers → ngrams)."""
    await _seed_units(db, library.id, ["the quick brown fox jumps over the lazy dog"])
    await rebuild(db, library_id=library.id, min_frequency=1, min_doc_frequency=1)

    res = await suggest_service.suggest(SuggestQuery(
        user_id=USER, library_id=library.id, prefix="", limit=8))
    assert 0 < len(res) <= 8
    # No exceptions, no 500 error, no empty body even though the user
    # typed nothing.
```

### 4.5 `test_suggest_long_input`

```python
async def test_suggest_long_input_truncated_safely(
    db, library, suggest_service,
):
    """A 5,000-character prefix is truncated to MAX_PREFIX_LEN and returns []."""
    long_prefix = "a" * 5000
    res = await suggest_service.suggest(SuggestQuery(
        user_id=USER, library_id=library.id, prefix=long_prefix, limit=8))
    # No exception; result is an empty list because no term in any
    # library starts with 128 'a's.
    assert res == []
```

### 4.6 `test_suggest_includes_saved_search` (story-named)

```python
async def test_suggest_includes_saved_search(
    db, library, suggest_service, search_log_factory,
):
    """Saved search 'الحمد' → typing 'ال' includes it, ranked above ngrams."""
    await search_log_factory.add(
        user_id=USER, library_id=library.id,
        query="الحمد", result_count=42)

    res = await suggest_service.suggest(SuggestQuery(
        user_id=USER, library_id=library.id, prefix="ال", limit=8))
    saved = [s for s in res if s.source == "saved"]
    assert any(s.text == "الحمد" for s in saved)
    # Saved appears before any ngram in the list.
    saved_idx = next(i for i, s in enumerate(res) if s.source == "saved")
    ngram_idx = next((i for i, s in enumerate(res) if s.source == "ngram"), None)
    if ngram_idx is not None:
        assert saved_idx < ngram_idx
```

### 4.7 `test_suggest_speakers` (story-named)

```python
async def test_suggest_speakers(db, library, suggest_service):
    """Speakers ['Sheikh A', 'Sheikh B'] → typing 'Sh' suggests both."""
    await _seed_speakers(db, library.id, ["Sheikh A", "Sheikh B", "Imam C"])

    res = await suggest_service.suggest(SuggestQuery(
        user_id=USER, library_id=library.id, prefix="Sh", limit=8))
    speakers = [s.text for s in res if s.source == "speaker"]
    assert "Sheikh A" in speakers
    assert "Sheikh B" in speakers
    assert "Imam C" not in speakers
```

### 4.8 `test_suggest_latency` (story-named)

```python
import statistics, time

async def test_suggest_latency_p95_under_50ms(
    db, library_with_15k_units, suggest_service,
):
    """1,000 calls; P95 ≤ 50 ms (story acceptance §1)."""
    await rebuild(db, library_id=library_with_15k_units.id)
    prefixes = ["al", "الح", "ku", "الذ", "le", "ll", "ال", "th", "an", "be"]

    elapsed_ms = []
    for i in range(1000):
        prefix = prefixes[i % len(prefixes)]
        t0 = time.perf_counter()
        await suggest_service.suggest(SuggestQuery(
            user_id=USER, library_id=library_with_15k_units.id,
            prefix=prefix, limit=8))
        elapsed_ms.append((time.perf_counter() - t0) * 1000)

    p95 = statistics.quantiles(elapsed_ms, n=20)[18]  # 95th percentile
    assert p95 <= 50, f"P95={p95:.1f} ms exceeds 50 ms budget"
```

### 4.9 `test_suggest_library_scoping`

```python
async def test_suggest_library_scoping_isolates_libraries(
    db, library_factory, suggest_service,
):
    """A term in Library A is not suggested when querying Library B."""
    lib_a = await library_factory.create()
    lib_b = await library_factory.create()
    await _seed_units(db, lib_a.id, ["unique-to-library-a-foobar"])
    await rebuild(db, library_id=lib_a.id, min_frequency=1, min_doc_frequency=1)
    await rebuild(db, library_id=lib_b.id, min_frequency=1, min_doc_frequency=1)

    res = await suggest_service.suggest(SuggestQuery(
        user_id=USER, library_id=lib_b.id, prefix="unique", limit=8))
    assert all("foobar" not in s.text for s in res)
```

### 4.10 `test_suggest_normalize`

```python
def test_normalize_strips_tashkeel():
    assert normalize_for_suggest("الْحَمْدُ") == "الحمد"

def test_normalize_folds_alif_variants():
    assert normalize_for_suggest("أحمد") == "احمد"
    assert normalize_for_suggest("إحمد") == "احمد"
    assert normalize_for_suggest("آدم")  == "ادم"

def test_normalize_folds_ya_and_teh_marbuta():
    assert normalize_for_suggest("على") == "علي"
    assert normalize_for_suggest("مدرسة") == "مدرسه"

def test_normalize_drops_rtl_marks():
    assert normalize_for_suggest("‏hello‎") == "hello"

def test_normalize_lowercases_ascii():
    assert normalize_for_suggest("Hello WORLD") == "hello world"

def test_normalize_collapses_whitespace():
    assert normalize_for_suggest("  the   quick  brown  ") == "the quick brown"

def test_normalize_empty_input():
    assert normalize_for_suggest("") == ""
    assert normalize_for_suggest("   ") == ""
```

### 4.11 `test_suggest_cache`

```python
def test_cache_hit_returns_same_object():
    c = TTLCache(max_size=4, ttl_sec=60.0)
    c.put("k", [1, 2, 3])
    assert c.get("k") == [1, 2, 3]

def test_cache_evicts_lru():
    c = TTLCache(max_size=2, ttl_sec=60.0)
    c.put("a", 1); c.put("b", 2); c.put("c", 3)
    assert c.get("a") is None
    assert c.get("b") == 2 and c.get("c") == 3

def test_cache_ttl_expires(monkeypatch):
    import time as _t
    c = TTLCache(max_size=4, ttl_sec=0.05)
    c.put("k", "v")
    _t.sleep(0.1)
    assert c.get("k") is None
```

### 4.12 `test_suggest_ranker_dedup`

```python
def test_merge_dedupes_across_pools():
    saved = [Suggestion("الحمد", "saved", 1.0, 0)]
    ngram = [Suggestion("الحمد", "ngram", 0.5, 2),
             Suggestion("الحديث", "ngram", 0.4, 2)]
    merged = merge(saved, [], ngram, limit=8)
    texts = [s.text for s in merged]
    assert texts.count("الحمد") == 1
    # The saved-pool entry wins (lower rank_pool).
    assert next(s for s in merged if s.text == "الحمد").source == "saved"

def test_merge_orders_by_rank_pool_then_score():
    a = [Suggestion("a", "saved", 0.9, 0)]
    b = [Suggestion("b", "speaker", 0.95, 1)]
    c = [Suggestion("c", "ngram", 0.99, 2)]
    out = merge(a, b, c, limit=3)
    assert [s.text for s in out] == ["a", "b", "c"]

def test_merge_normalizes_for_dedup():
    a = [Suggestion("الحمدُ", "saved", 1.0, 0)]
    b = [Suggestion("الحمد", "ngram", 0.5, 2)]
    merged = merge(a, b, limit=8)
    assert len(merged) == 1
    assert merged[0].source == "saved"
```

### 4.13 `test_suggest_build_ngrams`

```python
async def test_rebuild_extracts_2_3_4_grams(db, library):
    await _seed_units(db, library.id, [
        "the quick brown fox jumps over the lazy dog",
        "the quick brown fox runs",
        "lazy dog sleeps in the sun",
    ])
    metric = await rebuild(db, library_id=library.id,
                           min_frequency=1, min_doc_frequency=1)
    rows = await db.fetch(
        "SELECT term, ngram, frequency FROM search_suggestion_terms "
        "WHERE library_id = $1 ORDER BY frequency DESC", library.id)
    terms = {r["term"] for r in rows}
    assert "the quick brown" in terms
    assert "quick brown fox" in terms
    assert "the quick brown fox" in terms
    # 2-, 3-, and 4-grams all present.
    sizes = {r["ngram"] for r in rows}
    assert sizes == {2, 3, 4}
    assert metric["terms_kept"] == len(rows)
```

### 4.14 `test_suggest_build_idempotent`

```python
async def test_rebuild_idempotent(db, library):
    await _seed_units(db, library.id, ["the quick brown fox"])
    m1 = await rebuild(db, library_id=library.id,
                       min_frequency=1, min_doc_frequency=1)
    m2 = await rebuild(db, library_id=library.id,
                       min_frequency=1, min_doc_frequency=1)
    assert m1 == m2
    n = await db.fetchval(
        "SELECT count(*) FROM search_suggestion_terms WHERE library_id=$1",
        library.id)
    assert n == m1["terms_kept"]
```

### 4.15 `test_suggest_build_min_frequency`

```python
async def test_rebuild_drops_below_min_frequency(db, library):
    await _seed_units(db, library.id, ["one off ngram only here"])
    await rebuild(db, library_id=library.id, min_frequency=3, min_doc_frequency=1)
    rows = await db.fetch(
        "SELECT * FROM search_suggestion_terms WHERE library_id=$1", library.id)
    assert rows == []   # nothing meets the floor
```

### 4.16 `test_api_search_suggest_e2e`

```python
async def test_api_suggest_returns_json(client, library, user_token):
    r = await client.get(
        "/api/search/suggest",
        params={"q": "al", "library_id": str(library.id), "limit": 8},
        headers={"Authorization": f"Bearer {user_token}"})
    assert r.status_code == 200
    body = r.json()
    assert "suggestions" in body
    assert isinstance(body["suggestions"], list)
    assert len(body["suggestions"]) <= 8

async def test_api_suggest_403_on_other_library(client, other_library, user_token):
    r = await client.get(
        "/api/search/suggest",
        params={"q": "al", "library_id": str(other_library.id)},
        headers={"Authorization": f"Bearer {user_token}"})
    assert r.status_code == 403

async def test_api_suggest_rejects_oversize_q(client, library, user_token):
    r = await client.get(
        "/api/search/suggest",
        params={"q": "x" * 5000, "library_id": str(library.id)},
        headers={"Authorization": f"Bearer {user_token}"})
    # FastAPI Query(max_length=128) → 422.
    assert r.status_code == 422
```

### 4.17 `test_scheduler_runs_suggest_rebuild`

```python
async def test_scheduler_dispatches_suggest_rebuild(db, library, scheduler):
    await scheduler.enqueue(kind="suggest_rebuild", library_id=library.id)
    await scheduler.tick_until_idle()
    n = await db.fetchval(
        "SELECT last_completed_at FROM maintenance_jobs "
        "WHERE kind='suggest_rebuild' AND library_id=$1", library.id)
    assert n is not None
```

---

## 5. Edge cases and how the plan handles each

| # | Edge case | Handled by |
|---|-----------|------------|
| E1 | **Empty corpus.** A new library with no transcripts and no saved searches. | All three pools return empty lists. `merge(...)` returns `[]`. The route returns `{"suggestions": []}` with status 200. The popular fallback path (D10) also degrades cleanly. (`test_suggest_empty_input_returns_popular` covers the populated-but-empty-prefix variant; an empty-corpus variant lives in the same fixture.) |
| E2 | **Single-character query** (e.g. `q="ا"`). | `len(normalize_for_suggest(prefix)) < 2` triggers the popular fallback (D10) — saved searches first, then speakers, then most-frequent ngrams, capped at `limit`. We deliberately do **not** return an empty list because that feels broken on the very first keystroke. |
| E3 | **Tashkeel in input.** User types `الْحَمْدُ` (with marks). | `normalize_for_suggest` strips them before the lookup, and the stored `term_normalized` is also stripped, so the prefix index hit is identical to the un-marked case. Saved-search pool relies on `search_log.query_normalized` (D9 / 2.5); if the column is missing we degrade to lower(query) and log `SuggestDegraded`. |
| E4 | **RTL/LTR/PDF marks** in input (e.g. `‏الحمد‎`). | Stripped by the `_INVISIBLE_RE` pattern in `normalize_for_suggest` (covers U+200B–U+200F, U+202A–U+202E, U+2066–U+2069, U+FEFF). The user's clipboard or IME may inject these silently; we never want them to flip the prefix lookup. (`test_normalize_drops_rtl_marks`.) |
| E5 | **Case sensitivity for English.** A library with the term `Lecture Series` should match a query for `lec`. | `normalize_for_suggest` lowercases ASCII; the stored `term_normalized` is lowercase; the prefix-index hit is case-insensitive. The display form (`term`) preserves the original casing so the dropdown shows "Lecture Series" rather than "lecture series". |
| E6 | **Mixed-script prefix.** "al" returns Latin matches and any Arabic matches whose **normalized** form starts with "al" — which, for our normalization rules, means none (the Arabic alif-lam article does not romanize to ASCII). The story explicitly accepts this. (`test_suggest_mixed_script`.) | We do **not** transliterate. Cross-script suggestion would require a phonetic table (e.g. Buckwalter or ALA-LC) and ambiguity resolution; that's deferred per architecture Appendix B. |
| E7 | **Very long input** (paste of a paragraph into the search box). | The route has `Query(q: str, max_length=128)` → FastAPI returns 422 above 128 chars before any DB query. Inside the service, `prefix = (q.prefix or "")[:_MAX_PREFIX_LEN]` is a defense-in-depth truncation in case a future caller bypasses the route. The result is `[]` (no term is 128 chars long). (`test_suggest_long_input_truncated_safely`.) |
| E8 | **Debounce on the client side.** The story implies typeahead UX, which would normally fire one request per keystroke. | We **do not** implement client-side debouncing here (front-end concern, tracked in the Epic 7 search-UI story). The server-side LRU cache (D7) absorbs back-to-back identical prefixes from a fast typist. The 60 s TTL is short enough that newly-saved searches show up on the next typing burst. We add an OpenAPI-level `Cache-Control: private, max-age=10` response header so a caching reverse-proxy doesn't hold stale results. |
| E9 | **Stale ngrams.** A library that was active last month but is now idle still has yesterday's nightly snapshot — fine. A library whose last unit was committed 30 minutes ago has not yet appeared in the index. | This is acceptable: the story specifies "computed offline via a nightly task". For small libraries, the operator can trigger an on-demand rebuild via the existing scheduler API (`POST /api/maintenance/jobs?kind=suggest_rebuild&library_id=...`). The architecture document does not commit to sub-day freshness for suggestions. |
| E10 | **Saved-search log missing `query_normalized` column.** | The `fetch_saved` pool catches `UndefinedColumnError`, logs `SuggestDegraded`, and falls back to `lower(query) LIKE …` for English-only prefixes. Arabic prefixes will have reduced recall against the saved-search pool; speakers and ngrams are unaffected. A migration to add the column is owned by `02-api-streaming.md` Story 2.x. |
| E11 | **A user has access revoked from a library mid-session.** | `require_library_membership` (FastAPI dep) re-checks membership on every call, so the next request returns 403. The LRU cache is keyed on `(library_id, prefix, limit)` — *not* on user_id — so it does not leak data: the cache may retain results, but the membership check happens *before* the cache lookup (the cache lives behind the auth boundary). |
| E12 | **Diacritic-only difference between two saved searches** (e.g. user typed `الحَمد` once and `الحمد` later). | `normalize_for_suggest` collapses both to `الحمد`, the merge step dedupes them, and only one suggestion is shown — using the most-recent display form. (`test_merge_normalizes_for_dedup`.) |
| E13 | **An ngram contains only stop words.** "the the" or "and the" would dominate a frequency list. | The `_TOKEN_RE` does not strip stop words at extraction time (we want them preserved in the suggestion text — "the lazy dog" is a useful suggestion). However, the front end filters out single-token suggestions matching a small stop-word list before display. The server returns the data as-is; the policy lives in the UI. (Documented in Epic 7 search-UI plan.) |
| E14 | **`pg_trgm` extension missing on Postgres.** | The hot-path prefix LIKE works without `pg_trgm` — only the fuzzy fallback path is gated. We `try: CREATE EXTENSION pg_trgm; except ProgrammingError:` in the migration and log a warning. The fuzzy path checks `dialect == "postgres" and pg_trgm_present` (a feature flag refreshed on connect); if false, it skips the fallback. SQLite already runs without fuzzy fallback, so this is a strict superset. |

---

## 6. Acceptance checklist

- [ ] **A1** `GET /api/search/suggest?q=al&library_id=<uuid>` returns ≤ 8 ranked suggestions in JSON, drawn from saved searches, speaker names, and high-frequency ngrams. (`test_api_search_suggest_e2e`, `test_suggest_includes_saved_search`, `test_suggest_speakers`, `test_suggest_arabic_prefix`.)
- [ ] **A2** Suggestions are **library-scoped**: a term in Library A is never returned when querying Library B; cross-library access returns `403 Forbidden` at the API edge. (`test_suggest_library_scoping_isolates_libraries`, `test_api_suggest_403_on_other_library`.)
- [ ] **A3** Saved-search hits **outrank** speaker hits, which outrank ngram hits, with deduplication across pools by normalized text. (`test_merge_orders_by_rank_pool_then_score`, `test_suggest_includes_saved_search`, `test_merge_dedupes_across_pools`.)
- [ ] **A4** Arabic prefix queries match diacritic-bearing stored terms via the shared `normalize_for_suggest` function (tashkeel stripped, alif/ya/teh-marbuta folded). (`test_normalize_*`, `test_suggest_arabic_prefix`.)
- [ ] **A5** RTL/LTR/PDF marks and zero-width joiners in the user's input do not affect the prefix lookup. (`test_normalize_drops_rtl_marks`.)
- [ ] **A6** English prefixes are case-insensitive; the display form preserves the original casing. (`test_suggest_english_prefix`.)
- [ ] **A7** Empty input (`q=""`) and single-character input return the **popular** suggestions (saved → speakers → ngrams) instead of an empty list. (`test_suggest_empty_input_returns_popular`.)
- [ ] **A8** Inputs longer than 128 characters are rejected by the route with `422 Unprocessable Entity`; the service-level truncation guards the gRPC path. (`test_api_suggest_rejects_oversize_q`, `test_suggest_long_input_truncated_safely`.)
- [ ] **A9** P95 latency ≤ **50 ms** measured at the service entry point over 1,000 calls on a 15 k-unit library. (`test_suggest_latency_p95_under_50ms`.)
- [ ] **A10** A per-process LRU cache (size 1024, TTL 60 s) keyed on `(library_id, normalized_prefix, limit)` absorbs typing bursts without cross-user data leakage; the cache lookup happens **after** the membership check. (`test_suggest_cache`, code review of `SuggestService.suggest`.)
- [ ] **A11** The nightly `suggest_rebuild` job extracts 2-, 3-, and 4-grams from `transcript_units.text`, drops anything below `min_frequency=3` AND `min_doc_frequency=2`, and writes the per-library result in a single transaction. (`test_suggest_build_ngrams`, `test_suggest_build_idempotent`, `test_suggest_build_min_frequency`.)
- [ ] **A12** The job is registered in `maintenance_jobs(kind='suggest_rebuild')`, scheduled at 03:17 UTC, and dispatched by the Plan 5.5 scheduler; failures land in `last_error` and do not block other libraries' rebuilds. (`test_scheduler_dispatches_suggest_rebuild`.)
- [ ] **A13** Migration `0027_search_suggestion_terms.sql` applies cleanly on Postgres and SQLite, is idempotent, and creates the `text_pattern_ops` btree (PG) / `COLLATE NOCASE` index (SQLite) plus a `pg_trgm` GIN index (PG only). Migration is a no-op on re-run.
- [ ] **A14** Fuzzy fallback via `pg_trgm` is invoked **only** when prefix-index hits are < `min_results=4`, and is silently skipped when the extension is unavailable. SQLite has no fuzzy fallback in v1.
- [ ] **A15** When `search_log.query_normalized` is missing, the saved-search pool degrades to `lower(query) LIKE …`, logs `SuggestDegraded`, and the suggest endpoint continues serving speakers and ngrams without 5xx errors.
- [ ] **A16** No code path in this story writes to `transcript_units`, `transcript_segments`, `chroma_collections`, or any other index owned by another story. The only writes are to `search_suggestion_terms` (by the nightly job) and to `maintenance_jobs.last_*` (by the scheduler). (Static check; lint rule.)
- [ ] **A17** The OpenAPI response includes `Cache-Control: private, max-age=10` so a caching reverse-proxy can absorb identical-prefix bursts within a 10 s window; the header is **not** `public` because suggestions are user-scoped.
- [ ] **A18** Cross-language *translation* of queries is explicitly **not** implemented (deferred per architecture Appendix B). Cross-language *retrieval* via a shared multilingual embedding lives in Plan 5.3 and is unaffected by this story.
