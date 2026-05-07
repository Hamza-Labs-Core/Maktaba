# Implementation Plan — Story 7.6 Transcript Window Endpoint

> Companion to [story-07-06-transcript-window.md](story-07-06-transcript-window.md).
> Drives the player's transcript sidebar; latency-sensitive.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Route | `GET /api/videos/{id}/segments`. |
| Storage | `transcripts` and `transcript_segments` tables (Pipeline owns the schema; see architecture §8). The query reads only; no writes. |
| Word-level | Optional via `?words=true`. The `transcript_words` table is opt-in and large; do not join unless asked. |
| Bidi | Wrap each segment's `text` in U+2068/U+2069 isolates server-side; clients never have to think about it. |
| Out of scope | Subtitles read (Story 7.7), search hits (Story 7.8), persisting the cursor through transcript-supersede swaps. |

## 1. Architecture diagram

```
   GET /api/videos/{id}/segments?from=120&to=300&words=false
        │
        ▼
   ┌──────────────────────────────────────────────────────────┐
   │ 1. Resolve transcript_id:                                │
   │    if ?include_superseded=true → most recent             │
   │    else → most recent where superseded_at IS NULL        │
   │                                                          │
   │ 2. Clamp window:                                         │
   │    from = max(0, from)                                   │
   │    to   = min(duration_sec, to)  (if duration known)     │
   │                                                          │
   │ 3. SQL:                                                  │
   │    SELECT id, seq, start_sec, end_sec, text, confidence  │
   │      FROM transcript_segments                            │
   │     WHERE transcript_id = $1                             │
   │       AND start_sec < $to AND end_sec > $from            │
   │     ORDER BY seq                                         │
   │     LIMIT $limit + 1;                                    │
   │                                                          │
   │ 4. Wrap text in FSI/PDI isolates                         │
   │ 5. If ?words=true → batched loader for transcript_words   │
   │    per segment.                                          │
   │ 6. Detect coverage gap:                                  │
   │      partial = exists(?from > MAX(end_sec))              │
   └──────────────────────────────────────────────────────────┘
```

## 2. New files

| Path | Purpose |
|---|---|
| `api/internal/transcripts/handler.go` | Route + handler. |
| `api/internal/transcripts/window.go` | Query, clamp, bidi wrap. |
| `api/internal/transcripts/words.go` | Batched word-level loader. |
| `api/internal/transcripts/types.go` | DTO. |
| `api/internal/transcripts/handler_test.go` | Integration. |
| `api/internal/transcripts/window_test.go` | Unit (overlap predicate, bidi, clamp). |
| `shared/db/queries/transcripts.sql` | sqlc inputs. |
| `shared/db/migrations/0013_segments_window_idx.sql` | Composite index for the window query. |

## 3. SQL — index

`shared/db/migrations/0013_segments_window_idx.sql`:

```sql
-- +goose Up
-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS transcript_segments_window_idx
    ON transcript_segments (transcript_id, start_sec, end_sec);
-- The (start_sec, end_sec) composite handles both half-open and the
-- overlap predicate; range scans are bounded by the LIMIT.

CREATE INDEX IF NOT EXISTS transcript_words_segment_idx
    ON transcript_words (segment_id, seq);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS transcript_words_segment_idx;
DROP INDEX IF EXISTS transcript_segments_window_idx;
-- +goose StatementEnd
```

## 4. Type definitions

```go
// api/internal/transcripts/types.go
package transcripts

import "github.com/google/uuid"

// Segment.ID is int64 because transcript_segments.id is BIGSERIAL per
// architecture §8. Same for Word.SegmentID (transcript_words.segment_id).
type Segment struct {
    ID         int64    `json:"id"`
    Seq        int      `json:"seq"`
    StartSec   float64  `json:"start_sec"`
    EndSec     float64  `json:"end_sec"`
    Text       string   `json:"text"`         // bidi-isolated
    Confidence *float64 `json:"confidence"`
    Words      []Word   `json:"words,omitempty"`
}

type Word struct {
    SegmentID  int64   `json:"segment_id"`
    Seq        int     `json:"seq"`
    StartSec   float64 `json:"start_sec"`
    EndSec     float64 `json:"end_sec"`
    Text       string  `json:"text"`
    Confidence float64 `json:"confidence"`
}

type WindowResponse struct {
    Items        []Segment  `json:"items"`
    Next         *string    `json:"next"`
    Partial      bool       `json:"partial"`
    TranscriptID *uuid.UUID `json:"transcript_id"`
}

type Query struct {
    From              float64
    To                float64
    Words             bool
    IncludeSuperseded bool
    Limit             int
    Cursor            *paginate.Cursor
}
```

## 5. Handler scaffolding

```go
// api/internal/transcripts/handler.go
package transcripts

import (
    "math"
    "net/http"
    "strconv"

    "github.com/go-chi/chi/v5"
    "github.com/google/uuid"

    "maktaba/api/internal/httperror"
    "maktaba/api/internal/paginate"
)

const defaultPageSize = 200

func (h *handler) window(w http.ResponseWriter, r *http.Request) {
    id, err := uuid.Parse(chi.URLParam(r, "id"))
    if err != nil { httperror.Write(w, r, httperror.BadRequest("invalid id")); return }

    q, perr := parseQuery(r.URL.Query())
    if perr != nil { httperror.Write(w, r, perr); return }

    if q.From > q.To {
        httperror.Write(w, r, &httperror.Error{
            Type: TypeInvalidTimeWindow, Title: "invalid time window", Status: 400,
            Detail: "from must be <= to",
        })
        return
    }

    out, perr := h.svc.window(r.Context(), id, q)
    if perr != nil { httperror.Write(w, r, perr); return }
    json.NewEncoder(w).Encode(out)
}

func parseQuery(q url.Values) (Query, *httperror.Error) {
    parseFloat := func(name string, def float64) (float64, *httperror.Error) {
        s := q.Get(name)
        if s == "" { return def, nil }
        v, err := strconv.ParseFloat(s, 64)
        if err != nil || math.IsNaN(v) {
            return 0, httperror.InvalidQuery(name + " must be numeric")
        }
        return v, nil
    }

    from, perr := parseFloat("from", 0)
    if perr != nil { return Query{}, perr }
    to, perr := parseFloat("to", math.MaxFloat32)
    if perr != nil { return Query{}, perr }
    if from < 0 { from = 0 }

    return Query{
        From:              from,
        To:                to,
        Words:             q.Get("words") == "true",
        IncludeSuperseded: q.Get("include_superseded") == "true",
        Limit:             defaultPageSize,
    }, nil
}
```

## 6. Service layer

```go
// api/internal/transcripts/window.go
package transcripts

import (
    "context"
    "strings"
)

const (
    fsi = "⁨" // FIRST STRONG ISOLATE
    pdi = "⁩" // POP DIRECTIONAL ISOLATE
)

func bidiWrap(text string) string {
    if text == "" { return "" }
    return fsi + text + pdi
}

func (s *service) window(ctx context.Context, videoID uuid.UUID, q Query) (*WindowResponse, *httperror.Error) {
    transcript, err := s.db.LatestTranscript(ctx, LatestTranscriptParams{
        VideoID:           videoID,
        IncludeSuperseded: q.IncludeSuperseded,
    })
    if errors.Is(err, sql.ErrNoRows) {
        return &WindowResponse{Items: []Segment{}, Partial: false}, nil
    }
    if err != nil { return nil, httperror.Internal("transcript lookup failed") }

    // Clamp `to` against the known total duration if available.
    if transcript.DurationSec.Valid && q.To > transcript.DurationSec.Float64 {
        q.To = transcript.DurationSec.Float64
    }

    rows, err := s.db.SelectSegmentWindow(ctx, SelectSegmentWindowParams{
        TranscriptID: transcript.ID,
        FromSec:      q.From,
        ToSec:        q.To,
        Limit:        int32(q.Limit + 1),
    })
    if err != nil { return nil, httperror.Internal("segment query failed") }

    items := make([]Segment, 0, len(rows))
    for _, r := range rows {
        items = append(items, Segment{
            ID:         r.ID,
            Seq:        int(r.Seq),
            StartSec:   r.StartSec,
            EndSec:     r.EndSec,
            Text:       bidiWrap(r.Text),
            Confidence: ptrFloat(r.Confidence),
        })
    }

    if q.Words && len(items) > 0 {
        if err := s.attachWords(ctx, items); err != nil {
            return nil, httperror.Internal("words load failed")
        }
    }

    partial, err := s.isWindowPartial(ctx, transcript.ID, q)
    if err != nil { return nil, httperror.Internal("partial check failed") }

    var next *string
    if len(items) > q.Limit {
        // Use seq as the cursor key; segments are dense and seq-monotonic.
        last := items[q.Limit-1]
        c := paginate.Cursor{ /* synthesise from seq + transcript_id */ }
        enc := paginate.Encode(c)
        next = &enc
        items = items[:q.Limit]
    }

    return &WindowResponse{
        Items: items, Next: next, Partial: partial,
        TranscriptID: &transcript.ID,
    }, nil
}

// isWindowPartial returns true when the requested window starts past the
// last known segment's end_sec — a paused/in-flight transcribe.
func (s *service) isWindowPartial(ctx context.Context, tid uuid.UUID, q Query) (bool, error) {
    if q.From <= 0 { return false, nil }
    maxEnd, err := s.db.MaxSegmentEnd(ctx, tid)
    if err != nil { return false, err }
    return q.From > maxEnd, nil
}
```

## 7. SQL — sqlc inputs

`shared/db/queries/transcripts.sql`:

```sql
-- name: LatestTranscript :one
SELECT t.id,
       v.duration_sec
  FROM transcripts t
  JOIN videos v ON v.id = t.video_id
 WHERE t.video_id = $1
   AND ($2::bool OR t.superseded_at IS NULL)
 ORDER BY t.created_at DESC
 LIMIT 1;

-- name: SelectSegmentWindow :many
SELECT id, seq, start_sec, end_sec, text, confidence
  FROM transcript_segments
 WHERE transcript_id = $1
   AND start_sec < $3
   AND end_sec   > $2
 ORDER BY seq
 LIMIT $4;

-- name: MaxSegmentEnd :one
SELECT COALESCE(MAX(end_sec), 0)::float
  FROM transcript_segments
 WHERE transcript_id = $1;

-- name: SelectWordsForSegments :many
-- transcript_words.segment_id references transcript_segments.id (BIGSERIAL).
SELECT segment_id, seq, start_sec, end_sec, text, confidence
  FROM transcript_words
 WHERE segment_id = ANY($1::bigint[])
 ORDER BY segment_id, seq;
```

## 8. Test plan

### 8.1 Unit (`window_test.go`)

| Test | What it pins |
|---|---|
| `TestOverlapFullyInside` | Segment `[100, 110]` against window `[80, 200]` → returned. |
| `TestOverlapSpansStart` | Segment `[80, 130]` against `[100, 200]` → returned. |
| `TestOverlapSpansEnd` | Segment `[180, 220]` against `[100, 200]` → returned. |
| `TestOverlapFullyContaining` | Segment `[80, 220]` against `[100, 200]` → returned. |
| `TestOverlapDisjoint` | Segment `[300, 400]` against `[100, 200]` → not returned. |
| `TestBidiWrap` | `bidiWrap("الحمد")` → `"⁨الحمد⁩"`. |
| `TestBidiEmpty` | `bidiWrap("")` → `""` (no isolates). |
| `TestParseQueryClampsNegative` | `?from=-5` → from=0. |
| `TestParseQueryRejectsNaN` | `?from=NaN` → 400 `invalid-query-parameter`. |
| `TestParseQueryRejectsFromGTTo` | `?from=300&to=120` → 400 `invalid-time-window`. |

### 8.2 Integration (`handler_test.go`)

| Test | What it pins |
|---|---|
| `TestWindow50Segments` | 50-segment fixture, `?from=10&to=12.5` → exactly the segment whose range straddles 12.0. |
| `TestDefaultWindow` | No params → first 200 segments + a `next` cursor when more exist. |
| `TestSupersededExcludedByDefault` | Two transcripts (one superseded) → only the live one's segments returned. |
| `TestSupersededIncludedExplicitly` | `?include_superseded=true` → segments from the superseded transcript when it's most recent. |
| `TestPartialFlagPaused` | Transcribe paused at 3500 s on a 5000 s video; `?from=4000` → empty `items`, `partial: true`. |
| `TestClampToDuration` | `?to=99999` against a 600 s video → segments in `[0, 600]`, no error, no warning header (clamping is silent). |
| `TestWordsOptIn` | Same fixture with words populated; `?words=true` → each segment carries its `words` array; without `words=true`, no `words` key is present. |
| `TestWordsBatchedQuery` | Wrap DB with a counter; `?words=true` over 50 segments → exactly 2 queries (segments + words bulk). |
| `TestPerformance10K` | 10 000-segment fixture, `?limit=200` → p95 < 100 ms on SQLite. |

## 9. Edge cases — handling table

| Case | Behaviour | Where it's pinned |
|---|---|---|
| `from > to` | 400 `invalid-time-window`. | `TestParseQueryRejectsFromGTTo` |
| `from < 0` | Silently clamped to 0. | `TestParseQueryClampsNegative` |
| `to > duration_sec` | Silently clamped to `duration_sec`. | `TestClampToDuration` |
| `from = NaN` | 400 `invalid-query-parameter`. | `TestParseQueryRejectsNaN` |
| Window entirely past last segment | `items: []`, `partial: true`. | `TestPartialFlagPaused` |
| No transcript exists | `items: []`, `partial: false`, `transcript_id: null`; not 404. | Integration |
| Transcript superseded mid-pagination | The `next` cursor encodes the transcript_id; if the transcript is replaced, the cursor's transcript_id no longer matches the latest — server falls back to "fresh page 1" behaviour. Documented edge case; acceptable. | Documented |
| `?words=true` against a transcript with no word-level capture | Each segment's `words` is `null`; not `[]`. | Integration |
| Mixed-script segment (English embedded in Arabic) | The whole `text` is wrapped in FSI/PDI; the client renders without paragraph re-ordering. | Manual UI test + bidi unit test |
| Very short window (`?from=12&to=12.0001`) | Returns segments straddling 12.0. The half-open semantics avoid empty results when the window is a single instant. | `TestWindow50Segments` |
| Cursor returned in one request, transcript replaced before next | Server returns segments from the new transcript, not the old. The cursor's `transcript_id` is treated as a hint, not a hard filter. | Documented |

## 10. Acceptance checklist

- [ ] `GET /api/videos/{id}/segments` returns segments overlapping the window.
- [ ] Default returns first 200 + `next`.
- [ ] `words=true` opts into word-level (no extra cost when omitted).
- [ ] `partial: true` flag distinguishes "no segments yet" from "out of window."
- [ ] Bidi isolates wrap every `text` value.
- [ ] All `Test*` cases pass.
- [ ] Performance test passes on the 10 000-segment fixture.
- [ ] `specs/epics/07-api-server/README.md` ticks story 7.6.
