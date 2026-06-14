# Plan 26.4 — Auto-collection builder — implementation

> Implementation plan for [story-26-04-auto-collection-builder.md](story-26-04-auto-collection-builder.md).
> Self-contained. Cross-links: extends Epic 7 `collections`/`smart_query`
> ([Story 7.14](../07-api-server/README.md), slot 0033) and the
> `api/internal/handlers/collections` serving path; reuses the dismissal
> pattern from `recommendation_dismissals`
> ([Story 14.7](../14-discovery/README.md), slot 0067); consumes
> classification (26.2), series (26.3), and speakers. Triggered as a
> debounced library pass by
> [Plan 26.7](plan-26-07-background-enrichment-pipeline.md). Writes slot
> 0076 (ALTER `collections` + `collection_suggestions`).

---

## 0. Decisions

| #  | Decision | Rationale |
|----|----------|-----------|
| D1 | **Auto-collections are `collections` rows with `origin='auto'` + `is_smart=true`**, reusing the existing `smart_query` shape and serving path. No new membership store. | Story AC: extend, don't fork. The serving path, web UI, and ACL for smart collections already exist; auto ones are just rows the server proposes. |
| D2 | **`auto_rule` compiles to `smart_query`.** Each rule kind (topic/content_type/language/decade/speaker) has a deterministic compiler to the existing filter JSON. | Keeps membership resolution in one place (the existing smart-query evaluator); the rule is the *source*, `smart_query` the *compiled form*. |
| D3 | **Suggestions are a separate lifecycle table** (`collection_suggestions`) with states suggested→accepted/dismissed; accept materialises a `collections` row. | The proposal lifecycle is distinct from the collection itself; mirrors enrichment suggestions (26.6) and recommendation dismissals (14.7). |
| D4 | **Dismissals are permanent** via a stable `rule_key` (a hash of the normalised `auto_rule`), so the same cluster is never re-proposed even as ids shift. | Story AC: dismiss persists across re-runs and devices. Hashing the rule (not the row id) makes it stable across recomputes. |
| D5 | **Thresholds gate emission:** `min_members` (default 5) + per-kind `min_score`. | Story AC: no one-video collections, no flood. |
| D6 | **Manual-name collisions: manual wins.** If a `manual` collection shares the proposed `name_norm`, the auto suggestion is suppressed (or suffixed on accept). | Story AC. |
| D7 | **Pass is idempotent + incremental**: recompute candidates, diff against existing suggestions/accepted/dismissed, insert only the genuinely new. | Story AC. |

---

## 1. Package layout

```
pipeline/src/maktaba_pipeline/classify/collections/
├── __init__.py
├── pass_.py          # run_auto_collections(library_id) — debounced entry (D7)
├── rules.py          # candidate generation per kind (topic/type/lang/decade/speaker)
├── compile.py        # auto_rule → smart_query (D2)
├── rulekey.py        # stable rule_key hash (D4)
├── repo.py           # reads classification/series/speakers; writes suggestions
└── tests/
    ├── test_rules.py
    ├── test_compile.py
    ├── test_pass.py
    └── test_dismiss.py
```

Go API (accept/dismiss/rename/list) extends
`api/internal/handlers/collections/`.

## 2. Candidate generation (`rules.py`, D5)

```python
def candidates(conn, library_id) -> list[Candidate]:
    out = []
    out += topic_candidates(conn, library_id)        # group video_topics by topic_id
    out += content_type_candidates(conn, library_id) # group video_classification
    out += language_candidates(conn, library_id)     # group by dominant language_dist
    out += decade_candidates(conn, library_id)        # bucket parsed/enriched year
    out += speaker_candidates(conn, library_id)       # group by recurring speaker_id
    return [c for c in out if c.member_count >= MIN_MEMBERS and c.score >= min_score(c.kind)]
```

Each `Candidate` carries `kind`, `auto_rule`, proposed `name`,
`member_count`, `score`, and `rule_key = rulekey.compute(auto_rule)`.

## 3. Compile to `smart_query` (`compile.py`, D2)

```python
def compile_rule(auto_rule: dict) -> dict:
    by = auto_rule["by"]
    if by == "topic":         return {"filters": {"topic_id": auto_rule["topic_id"]}}
    if by == "content_type":  return {"filters": {"content_type": auto_rule["value"]}}
    if by == "language":      return {"filters": {"language": auto_rule["value"]}}
    if by == "decade":        return {"filters": {"year_gte": auto_rule["from"],
                                                  "year_lte": auto_rule["to"]}}
    if by == "speaker":       return {"filters": {"speaker_id": auto_rule["speaker_id"]}}
    raise ValueError(by)
```

> The exact `smart_query` keys mirror the search-filter keys the existing
> collection evaluator already understands (Story 7.14). If a filter key
> (e.g. `content_type`, `topic_id`, `speaker_id`) is not yet supported by
> that evaluator, this plan adds it there — a small, additive change
> reusing the indexes from slots 0046/0035/0074.

## 4. The pass (`pass_.py`, D7)

```python
async def run_auto_collections(conn, library_id):
    cands = rules.candidates(conn, library_id)
    existing = await repo.load_suggestions_and_collections(conn, library_id)
    for c in cands:
        if c.rule_key in existing.dismissed:   continue   # D4
        if c.rule_key in existing.accepted:    continue
        if manual_name_collision(c, existing): continue   # D6
        await repo.upsert_suggestion(conn, library_id, c) # D7 (no-op if unchanged)
    await repo.expire_stale_suggestions(conn, library_id, cands)
```

## 5. Data model — migration slot 0076

```sql
-- Slot 0076 (Epic 26 / Story 26.4) — extend collections + suggestions.
ALTER TABLE collections ADD COLUMN IF NOT EXISTS origin TEXT NOT NULL DEFAULT 'manual'
    CHECK (origin IN ('manual','auto'));
ALTER TABLE collections ADD COLUMN IF NOT EXISTS auto_rule JSONB;
ALTER TABLE collections ADD COLUMN IF NOT EXISTS rule_key TEXT;   -- set when origin='auto'

CREATE TABLE IF NOT EXISTS collection_suggestions (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    library_id    UUID NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    kind          TEXT NOT NULL,
    rule_key      TEXT NOT NULL,                 -- stable hash (D4)
    auto_rule     JSONB NOT NULL,
    name          TEXT NOT NULL,                 -- proposed; user can rename before accept
    member_estimate INTEGER NOT NULL DEFAULT 0,
    score         REAL NOT NULL DEFAULT 0,
    status        TEXT NOT NULL DEFAULT 'suggested'
                  CHECK (status IN ('suggested','accepted','dismissed')),
    collection_id UUID REFERENCES collections(id) ON DELETE SET NULL,  -- set on accept
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (library_id, rule_key)                -- one row per cluster; status carries lifecycle
);

CREATE INDEX CONCURRENTLY IF NOT EXISTS collection_suggestions_status_idx
    ON collection_suggestions (library_id, status);
```

Dismissal is just `status='dismissed'` on the unique `(library_id,
rule_key)` row — re-runs upsert and the `WHERE status='suggested'` filter
hides it, exactly the recommendation-dismissal idea (14.7).

## 6. API contract (Go)

```
GET   /api/collections/suggestions[?library_id=]   → status='suggested' rows
POST  /api/collections/suggestions/{id}/accept     → create collection(origin=auto), status=accepted
POST  /api/collections/suggestions/{id}/dismiss    → status=dismissed
PATCH /api/collections/suggestions/{id}            → {name} for accept-time rename
```

Accept (transaction): insert `collections` (`is_smart=true`,
`origin='auto'`, `auto_rule`, `rule_key`, `smart_query=compile_rule(...)`,
name = user override or proposed), then mark the suggestion accepted and
link `collection_id`.

## 7. Web (React)

- A "Suggested collections" rail on the Library Browser
  (`web/src/pages/LibraryBrowser.tsx`) rendering suggestion cards with
  Accept / Dismiss / Rename, using existing design-system `Card`,
  `Button`, `Chip`.
- Accepted collections appear in the normal collections list (no new
  rendering needed — they're ordinary smart collections).

## 8. Files to create / modify

**Create:** `pipeline/.../classify/collections/*`, migration pair, web
suggestion-rail component + tests.

**Modify:** `api/internal/handlers/collections/` (suggestion endpoints +
accept logic), the smart-query evaluator (add any missing filter keys),
`api/internal/router`, `LibraryBrowser.tsx`, `MANIFEST.md` (slot 0076).

## 9. Dependencies

- **26.2** (topics/content type/language), **26.3** (speaker-based via
  series? no — speaker collections read `segment_speakers` directly),
  **Story 7.14** collections + smart-query evaluator, **Story 14.7**
  dismissal pattern, **Story 9.11** speakers.

## 10. Test strategy

`test_rules` seeds classification rows and asserts candidate emission
above/below thresholds; `test_compile` checks each rule → smart_query;
`test_pass` asserts idempotence and no re-proposal of
accepted/dismissed; `test_dismiss` asserts permanence via `rule_key`.
An API test asserts accept produces a working smart collection whose
membership the existing evaluator resolves.
