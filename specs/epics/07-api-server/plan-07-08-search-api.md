# Implementation Plan — Story 7.8 Search API (FTS, semantic, hybrid)

> Companion to [story-07-08-search-api.md](story-07-08-search-api.md).
> The most complex read surface in Epic 7. Joins FTS + Chroma + RRF +
> filters + bidi + degraded fallback.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Routes | `POST /api/search`, `GET /api/search/suggest`. |
| FTS | Postgres `search_tsv` (already added in Story 7.4) + a new `transcript_units_fts` index for unit-level search. SQLite uses FTS5. |
| Embedding | Pipeline gRPC `Embed(text) → vector`. The API caches the query embedding for `embedding_cache_ttl_sec` (default 300 s) per query. |
| Vector store | Chroma over its HTTP API. The API holds a thin `chroma.Client` (no SDK). |
| Fusion | Reciprocal Rank Fusion, `k=60`. |
| Degraded mode | Falls back to FTS-only when Pipeline or Chroma is unavailable; sets `degraded: true` in the response. |
| Out of scope | The unit-level segmentation (Pipeline Story 5.1), embedding model choice (Pipeline Story 5.4), saved searches (Story 7.9). |

## 1. Architecture diagram

```
   POST /api/search { q, mode?, filters?, k? }
        │
        ▼
   ┌────────────────────────────────────────────────────────────┐
   │ 1. Validate q (1..1024 chars, non-whitespace)              │
   │ 2. Build filter SQL fragments + Chroma `where` filter      │
   │ 3. Resolve mode:                                           │
   │      "fts"      → ftsOnly()                                │
   │      "semantic" → semanticOnly()                           │
   │      "hybrid"   → both, in parallel                        │
   │                                                            │
   │ 4. ftsBranch:                                              │
   │     SELECT unit_id, score                                  │
   │       FROM transcript_units                                │
   │      WHERE search_tsv @@ plainto_tsquery('simple', $q)     │
   │        AND <filters>                                       │
   │      ORDER BY ts_rank(search_tsv, ...) DESC                │
   │      LIMIT $k                                              │
   │                                                            │
   │ 5. semanticBranch (parallel goroutine):                    │
   │     vec = pipelineClient.Embed(q)   (cached)               │
   │     chroma.Query(vec, where=filters, k=$k)                  │
   │                                                            │
   │ 6. RRF fusion:                                             │
   │     score(d) = Σ 1 / (k_rrf + rank_i(d))   k_rrf=60        │
   │                                                            │
   │ 7. Map unit_ids to segment coordinates                     │
   │     unit.segment_id → transcript_segments row              │
   │     → start/end_sec                                        │
   │                                                            │
   │ 8. Highlight FTS matches: <mark>...</mark>, max 240 chars  │
   │ 9. Bidi-isolate text                                       │
   │ 10. Return shape from arch §9.3                            │
   └────────────────────────────────────────────────────────────┘
```

## 2. New files

| Path | Purpose |
|---|---|
| `api/internal/search/handler.go` | `POST /api/search` + `GET /api/search/suggest`. |
| `api/internal/search/fts.go` | FTS branch (Postgres + SQLite via dialect switch). |
| `api/internal/search/semantic.go` | Embedding cache + Chroma round-trip. |
| `api/internal/search/fuse.go` | Reciprocal Rank Fusion. |
| `api/internal/search/highlight.go` | `<mark>` wrapping + 240-char excerpt. |
| `api/internal/search/filter.go` | Translate API filters into SQL + Chroma `where`. |
| `api/internal/search/types.go` | DTOs. |
| `api/internal/search/handler_test.go` | Integration. |
| `api/internal/search/fuse_test.go` | Unit tests for RRF. |
| `api/internal/search/highlight_test.go` | Unit tests for excerpt + bidi. |
| `api/internal/search/filter_test.go` | Unit tests for filter translation. |
| `shared/db/queries/search.sql` | sqlc inputs. |
| `api/internal/chroma/client.go` | Thin HTTP client for Chroma. |

## 3. SQL — FTS index

This plan owns no migrations of its own. The `transcript_units.tsv`
generated column, the GIN index, and the SQLite FTS5 virtual table are
all owned by [plan-05-02](../05-search-indexing/plan-05-02-fts-tsvector.md)
at slots 0019–0024. The only addition this plan declares is the
`pg_trgm` extension + trigram index for typeahead — which lands as
part of the same slot family in plan-05-02.

## 4. Type definitions

```go
// api/internal/search/types.go
package search

import (
    "time"
    "github.com/google/uuid"
)

type Mode string

const (
    ModeHybrid   Mode = "hybrid"
    ModeFTS      Mode = "fts"
    ModeSemantic Mode = "semantic"
)

type Request struct {
    Q       string  `json:"q"       validate:"required,min=1,max=1024"`
    Mode    Mode    `json:"mode,omitempty" validate:"omitempty,oneof=hybrid fts semantic"`
    K       int     `json:"k,omitempty" validate:"omitempty,gte=1,lte=200"`
    Filters Filters `json:"filters,omitempty"`
    Cursor  string  `json:"cursor,omitempty"`
    Limit   int     `json:"limit,omitempty" validate:"omitempty,gte=1,lte=50"`
}

type Filters struct {
    Language    []string  `json:"language,omitempty"`
    LibraryID   []uuid.UUID `json:"library_id,omitempty"`
    DurationSec *Range    `json:"duration_sec,omitempty"`
    Speaker     []string  `json:"speaker,omitempty"`
    Date        *DateRange`json:"date,omitempty"`
    Tag         []string  `json:"tag,omitempty"`
}

type Range struct {
    GTE *float64 `json:"gte,omitempty"`
    LTE *float64 `json:"lte,omitempty"`
}
type DateRange struct {
    GTE *time.Time `json:"gte,omitempty"`
    LTE *time.Time `json:"lte,omitempty"`
}

type Response struct {
    Hits      []Hit         `json:"hits"`
    Total     int           `json:"total"`
    Took      TookBreakdown `json:"took_ms"`
    Degraded  bool          `json:"degraded,omitempty"`
    Reason    string        `json:"reason,omitempty"`
    Cursor    *string       `json:"next,omitempty"`
}

type Hit struct {
    VideoID   uuid.UUID  `json:"video_id"`
    Score     float64    `json:"score"`
    Matches   []Match    `json:"matches"`
    Snippet   string     `json:"snippet"`
}

// Match.SegmentID is int64 because transcript_segments.id is BIGSERIAL
// (architecture §8). Same convention applies to transcript_units.id.
type Match struct {
    SegmentID int64   `json:"segment_id"`
    StartSec  float64 `json:"start_sec"`
    EndSec    float64 `json:"end_sec"`
    Text      string  `json:"text"`
}

type TookBreakdown struct {
    FTS      int64 `json:"fts"`
    Semantic int64 `json:"semantic"`
    Fusion   int64 `json:"fusion"`
    Total    int64 `json:"total"`
}
```

## 5. Handler scaffolding

```go
// api/internal/search/handler.go
package search

import (
    "context"
    "encoding/json"
    "errors"
    "net/http"
    "time"

    "maktaba/api/internal/httperror"
)

func (h *handler) search(w http.ResponseWriter, r *http.Request) {
    var req Request
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        httperror.Write(w, r, httperror.BadRequest("invalid json"))
        return
    }
    if strings.TrimSpace(req.Q) == "" {
        httperror.Write(w, r, &httperror.Error{
            Type: TypeInvalidQuery, Title: "invalid query",
            Status: 400, Detail: "q must be non-empty",
        })
        return
    }
    if req.Mode == "" { req.Mode = ModeHybrid }
    if req.K == 0 { req.K = 50 }
    if req.Limit == 0 { req.Limit = 20 }

    out, perr := h.svc.search(r.Context(), req)
    if perr != nil { httperror.Write(w, r, perr); return }
    json.NewEncoder(w).Encode(out)
}

func (s *service) search(ctx context.Context, req Request) (*Response, *httperror.Error) {
    started := time.Now()
    var ftsHits, semHits []rankedDoc
    var ftsTime, semTime time.Duration
    var ftsErr, semErr error

    runFTS := func() {
        t0 := time.Now()
        ftsHits, ftsErr = s.fts.Query(ctx, req.Q, req.Filters, req.K)
        ftsTime = time.Since(t0)
    }
    runSemantic := func() {
        t0 := time.Now()
        semHits, semErr = s.semantic.Query(ctx, req.Q, req.Filters, req.K)
        semTime = time.Since(t0)
    }

    switch req.Mode {
    case ModeFTS:        runFTS()
    case ModeSemantic:   runSemantic()
    default: // hybrid
        var wg sync.WaitGroup
        wg.Add(2)
        go func(){ defer wg.Done(); runFTS() }()
        go func(){ defer wg.Done(); runSemantic() }()
        wg.Wait()
    }

    degraded, reason := false, ""
    if req.Mode == ModeHybrid && semErr != nil {
        degraded, reason = true, "embedding-unavailable"
        s.log.Warn("hybrid_degraded", "err", semErr.Error())
    }

    fusionT0 := time.Now()
    var fused []rankedDoc
    switch {
    case req.Mode == ModeFTS:      fused = ftsHits
    case req.Mode == ModeSemantic: fused = semHits
    case semErr != nil:            fused = ftsHits
    default:                       fused = rrfFuse(ftsHits, semHits, 60)
    }
    fused = applyDedup(fused)
    fusionTime := time.Since(fusionT0)

    hits, perr := s.materializeHits(ctx, fused, req.Q, req.Limit)
    if perr != nil { return nil, perr }

    return &Response{
        Hits:     hits,
        Total:    len(fused),
        Took:     TookBreakdown{
            FTS: ftsTime.Milliseconds(),
            Semantic: semTime.Milliseconds(),
            Fusion: fusionTime.Milliseconds(),
            Total: time.Since(started).Milliseconds(),
        },
        Degraded: degraded, Reason: reason,
    }, nil
}
```

## 6. RRF fusion

```go
// api/internal/search/fuse.go
package search

import "sort"

// rankedDoc.UnitID is int64 (transcript_units.id is BIGSERIAL per
// architecture §8). VideoID stays uuid.UUID.
type rankedDoc struct {
    UnitID  int64
    VideoID uuid.UUID
    Score   float64
    Source  string // "fts" | "semantic"
}

// rrfFuse returns docs sorted DESC by combined score.
// score(d) = Σ 1 / (k + rank_i(d))   for each list i where d appears.
func rrfFuse(fts, semantic []rankedDoc, k float64) []rankedDoc {
    rank := func(list []rankedDoc) map[int64]int {
        out := make(map[int64]int, len(list))
        for i, d := range list { out[d.UnitID] = i + 1 }
        return out
    }
    rFTS := rank(fts)
    rSem := rank(semantic)

    seen := make(map[int64]rankedDoc)
    add := func(list []rankedDoc) {
        for _, d := range list {
            doc, ok := seen[d.UnitID]
            if !ok { doc = rankedDoc{UnitID: d.UnitID, VideoID: d.VideoID} }
            score := 0.0
            if r, ok := rFTS[d.UnitID]; ok { score += 1.0 / (k + float64(r)) }
            if r, ok := rSem[d.UnitID]; ok { score += 1.0 / (k + float64(r)) }
            doc.Score = score
            seen[d.UnitID] = doc
        }
    }
    add(fts); add(semantic)

    out := make([]rankedDoc, 0, len(seen))
    for _, d := range seen { out = append(out, d) }
    sort.Slice(out, func(i, j int) bool {
        if out[i].Score != out[j].Score { return out[i].Score > out[j].Score }
        // Deterministic tiebreak by UnitID ascending.
        return out[i].UnitID < out[j].UnitID
    })
    return out
}
```

## 7. Highlight + excerpt

```go
// api/internal/search/highlight.go
package search

import (
    "regexp"
    "strings"
    "unicode/utf8"
)

const (
    excerptMax = 240
    fsi        = "⁨"
    pdi        = "⁩"
)

func highlight(text, q string) string {
    if text == "" || q == "" { return bidiWrap(text) }
    terms := splitTerms(q)
    if len(terms) == 0 { return bidiWrap(text) }

    pat := regexp.MustCompile(`(?i)(` + strings.Join(escapeAll(terms), "|") + `)`)
    excerpted := excerptAround(text, terms, excerptMax)
    marked := pat.ReplaceAllString(excerpted, "<mark>$1</mark>")
    return bidiWrap(marked)
}

func bidiWrap(s string) string { return fsi + s + pdi }

// excerptAround finds the first match position and returns up to `max`
// chars around it; if no term matches (semantic-only hit), returns the
// first `max` chars.
func excerptAround(text string, terms []string, max int) string {
    pos := -1
    for _, t := range terms {
        if i := strings.Index(strings.ToLower(text), strings.ToLower(t)); i >= 0 {
            if pos < 0 || i < pos { pos = i }
        }
    }
    if pos < 0 { pos = 0 }
    half := max / 2
    start := pos - half
    if start < 0 { start = 0 }
    end := start + max
    if end > utf8.RuneCountInString(text) { end = utf8.RuneCountInString(text) }
    return safeSub(text, start, end)
}
```

## 8. Filters

```go
// api/internal/search/filter.go
package search

func toSQL(f Filters, baseArgs []any) (clauses []string, args []any) {
    args = baseArgs
    add := func(col string, vals []string) {
        if len(vals) == 0 { return }
        args = append(args, vals)
        clauses = append(clauses, fmt.Sprintf("%s = ANY($%d)", col, len(args)))
    }
    addUUID := func(col string, vals []uuid.UUID) {
        if len(vals) == 0 { return }
        args = append(args, vals)
        clauses = append(clauses, fmt.Sprintf("%s = ANY($%d)", col, len(args)))
    }

    // Language now lives on the active transcript row (architecture §8).
    if len(f.Language) > 0 {
        args = append(args, f.Language)
        clauses = append(clauses, fmt.Sprintf(`EXISTS (
            SELECT 1 FROM transcripts t
             WHERE t.video_id = v.id
               AND t.superseded_at IS NULL
               AND t.detected_language = ANY($%d))`, len(args)))
    }
    addUUID("v.library_id", f.LibraryID)
    if r := f.DurationSec; r != nil {
        if r.GTE != nil { args = append(args, *r.GTE); clauses = append(clauses, fmt.Sprintf("v.duration_sec >= $%d", len(args))) }
        if r.LTE != nil { args = append(args, *r.LTE); clauses = append(clauses, fmt.Sprintf("v.duration_sec <= $%d", len(args))) }
    }
    if r := f.Date; r != nil {
        if r.GTE != nil { args = append(args, *r.GTE); clauses = append(clauses, fmt.Sprintf("v.created_at >= $%d", len(args))) }
        if r.LTE != nil { args = append(args, *r.LTE); clauses = append(clauses, fmt.Sprintf("v.created_at <= $%d", len(args))) }
    }
    if len(f.Speaker) > 0 {
        // segment_speakers.segment_id is BIGINT (refs transcript_segments.id).
        args = append(args, f.Speaker)
        clauses = append(clauses, fmt.Sprintf(`EXISTS (
            SELECT 1 FROM segment_speakers ss
             WHERE ss.segment_id = u.segment_id
               AND ss.speaker_id = ANY($%d::bigint[]))`, len(args)))
    }
    if len(f.Tag) > 0 {
        args = append(args, f.Tag)
        clauses = append(clauses, fmt.Sprintf(`EXISTS (
            SELECT 1 FROM video_tags vt JOIN tags t ON t.id = vt.tag_id
             WHERE vt.video_id = v.id AND t.name = ANY($%d))`, len(args)))
    }
    return clauses, args
}

// toChromaWhere mirrors the same filter into Chroma's `where` JSON.
func toChromaWhere(f Filters) map[string]any { /* ... */ }
```

## 9. SQL — sqlc inputs

`shared/db/queries/search.sql`:

```sql
-- name: FTSUnits :many
SELECT u.id           AS unit_id,
       v.id           AS video_id,
       u.segment_id   AS segment_id,
       u.text         AS text,
       ts_rank(u.search_tsv,
               plainto_tsquery('simple', $1)) AS score
  FROM transcript_units u
  JOIN videos v ON v.id = u.video_id
 WHERE u.search_tsv @@ plainto_tsquery('simple', $1)
   AND v.deleted_at IS NULL
   /* filter clauses inserted at $2.. */
 ORDER BY score DESC
 LIMIT $K_PLACEHOLDER;

-- name: SuggestPrefix :many
SELECT DISTINCT text
  FROM transcript_units
 WHERE text ILIKE ($1 || '%')
    OR text % $1   -- pg_trgm similarity
 ORDER BY similarity(text, $1) DESC
 LIMIT 10;

-- name: SegmentByID :one
-- transcript_segments.id is BIGSERIAL.
SELECT id, start_sec, end_sec, text
  FROM transcript_segments WHERE id = $1;

-- name: SegmentsForUnits :many
-- transcript_units.id and transcript_segments.id are both BIGSERIAL.
SELECT s.id, s.start_sec, s.end_sec, s.text, u.id AS unit_id
  FROM transcript_segments s
  JOIN transcript_units u ON s.id = u.segment_id
 WHERE u.id = ANY($1::bigint[]);
```

## 10. Suggest endpoint

```go
func (h *handler) suggest(w http.ResponseWriter, r *http.Request) {
    q := r.URL.Query().Get("q")
    if len(q) < 2 {
        json.NewEncoder(w).Encode(map[string]any{"suggestions": []string{}}); return
    }
    rows, err := h.db.SuggestPrefix(r.Context(), q)
    if err != nil { httperror.Write(w, r, httperror.Internal("suggest")); return }
    out := struct{ Suggestions []string `json:"suggestions"` }{rows}
    json.NewEncoder(w).Encode(out)
}
```

## 11. Test plan

### 11.1 Unit (`fuse_test.go`)

| Test | What it pins |
|---|---|
| `TestRRFDeterministic` | Two synthetic ranked lists → exact, deterministic order. |
| `TestRRFEmptyFTS` | FTS empty, semantic populated → returns semantic order. |
| `TestRRFEmptySemantic` | Semantic empty, FTS populated → returns FTS order. |
| `TestRRFNoNaN` | Both empty → empty result, no division-by-zero. |
| `TestRRFDocInBoth` | Doc ranked #1 in both → highest combined score. |

### 11.2 Unit (`highlight_test.go`)

| Test | What it pins |
|---|---|
| `TestHighlightWraps` | `"the quick brown"` + `q="quick"` → contains `<mark>quick</mark>`. |
| `TestExcerpt240` | 1000-char text → returned ≤ 240 chars. |
| `TestExcerptCenteredOnMatch` | Match at offset 700 → excerpt centred there, not at offset 0. |
| `TestBidiWrapsArabic` | Arabic snippet wrapped in U+2068/U+2069. |
| `TestNoMatchReturnsHead` | Semantic-only hit (no FTS term) → first 240 chars returned. |

### 11.3 Unit (`filter_test.go`)

| Test | What it pins |
|---|---|
| `TestLanguageFilter` | `language=[ar]` → EXISTS subquery against active `transcripts` row with `detected_language = ANY($1)` (the column lives on transcripts, not videos, per architecture §8). |
| `TestDurationGTE` | `{gte: 1800}` → `>= $n`. |
| `TestSpeakerFilter` | Speaker filter generates the EXISTS subquery against `segment_speakers`. |
| `TestEmptyFilterNoClauses` | `Filters{}` → no WHERE additions. |

### 11.4 Integration (`handler_test.go`)

| Test | What it pins |
|---|---|
| `TestHybridDefault` | POST `{q: "الحمد"}` → 200; `took_ms.fts > 0`, `took_ms.semantic > 0`. |
| `TestFTSOnly` | POST `{mode: "fts"}` → no Pipeline gRPC call (fake records 0 calls). |
| `TestSemanticOnly` | POST `{mode: "semantic"}` → no FTS query (db counter records 0 FTS queries). |
| `TestEmptyQRejected` | `{q: "   "}` → 400 `invalid-query`. |
| `TestQTooLong` | 50 000-char `q` → 400 `q must be ≤1024 chars`. |
| `TestPipelineDownDegrades` | Kill Pipeline gRPC; hybrid → 200 with `degraded: true, reason: "embedding-unavailable"`. |
| `TestChromaReturnsStaleId` | Insert Chroma row, delete underlying segment → result excludes the dropped id; no error. |
| `TestUnitToSegmentMapping` | A unit spanning two segments → `segment_id = unit.segment_ids[0]`; start/end taken from that segment. |
| `TestArabicDiacriticsMatch` | Query without diacritics matches segments with diacritics (FTS5 `unicode61 remove_diacritics 2`). |
| `TestCrossLanguageSemantic` | English query → returns Arabic hits via `multilingual-e5-large` (uses a stub embed that returns canned vectors). |
| `TestHybridFusionContains` | Both branches contain a doc → it ranks above docs in only one. |
| `TestSuggestArabic` | `?q=الحم` → 10 suggestions, p99 < 80 ms on the 100 000-segment fixture. |

### 11.5 Performance (gated nightly)

| Test | What it pins |
|---|---|
| `TestSearchWarmCacheP95` | 100 000-segment fixture, warm cache → p95 ≤ 500 ms, p99 ≤ 800 ms, p50 ≤ 250 ms. |
| `TestSearchColdCache` | First call after restart → records `cold_search_p95_ms`; reported separately. |
| `TestSuggestThroughput` | 50 RPS suggest load for 30 s → no 5xx; p99 latency under 80 ms. |

## 12. Edge cases — handling table

| Case | Behaviour | Where it's pinned |
|---|---|---|
| `q` empty / whitespace | 400 `invalid-query`. | `TestEmptyQRejected` |
| `q` over 1024 chars | 400 with `q must be ≤1024 chars`. | `TestQTooLong` |
| Pipeline gRPC down (hybrid) | Falls back to FTS-only; `degraded: true`. | `TestPipelineDownDegrades` |
| Chroma down | Same as Pipeline down (semantic branch fails). | `TestChromaDown` |
| Chroma id pointing at deleted segment | Dropped from results, no error. | `TestChromaReturnsStaleId` |
| Filter on `speaker_id` after rename | Filters by id, not name; renames don't break in-flight queries. | Documented |
| Diacritic-bearing query against bare-text corpus (and vice-versa) | FTS5/Postgres unicode61 normalises both. | `TestArabicDiacriticsMatch` |
| Embedding cache LRU eviction | TTL 300 s, size 4096 entries; oldest evicted first. | Cache unit test |
| RRF tie | Tie-broken by lexicographic `unit_id`; deterministic. | `TestRRFDeterministic` |
| Highlight regex with regex metachars in query | All terms `regexp.QuoteMeta`'d before compiling. | `TestHighlightSafeRegex` |
| User filtered to libraries they can't read | Filter is intersected with the user's authorised library set (auth middleware computes it; out-of-scope here, but documented). | Auth-integration test (Epic 10) |
| Query with right-to-left text | Bidi isolates wrap each excerpt; client never has to manage it. | `TestBidiWrapsArabic` |
| `?cursor=...` carrying a cursor from a prior search | Cursor encodes `(score, unit_id)`; the next page applies `WHERE (score, unit_id) < cursor` over the fused list. (Pagination on hybrid results uses the in-memory fused list slice). | Documented; integration test stable |

## 13. Acceptance checklist

- [ ] Hybrid is default mode; `fts` and `semantic` modes work as documented.
- [ ] All filters push down to SQL/Chroma; no in-memory filtering.
- [ ] `took_ms` includes `fts`, `semantic`, `fusion`, `total`.
- [ ] `degraded: true` on hybrid degradation; `reason` populated.
- [ ] Excerpts are at most 240 chars; matches are `<mark>`-wrapped.
- [ ] Bidi isolates wrap each `text`/`snippet`.
- [ ] Suggest endpoint p99 < 80 ms on the 100k-segment fixture.
- [ ] Search warm-cache p95 ≤ 500 ms on the same fixture.
- [ ] All `Test*` cases pass.
- [ ] `specs/epics/07-api-server/README.md` ticks story 7.8.
