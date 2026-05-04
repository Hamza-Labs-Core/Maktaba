# Implementation Plan — Story 8.12 Chapter Delivery

> Companion to [story-08-12-chapter-delivery.md](story-08-12-chapter-delivery.md).
> The story states *what* and *why*; this plan states *how*. Architecture
> reference: [§4.6](../../architecture.md#46-chapters). Generation is
> owned by Epic 9 Story 9.18 (priority merge happens during generation;
> we **read** here).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Package | `streaming/internal/chapters/` — DB read + JSON / DATERANGE assembly. |
| Source of truth | `chapters` table (Epic 9 Story 9.18). Streaming reads, never writes. |
| Two endpoints | `GET /stream/{sid}/chapters.json` (session-scoped, `aud=streaming` JWT) AND `GET /stream/posters/{video_id}/chapters.json` (static, `aud=streaming-static` JWT, `sub=sha256("chapters.json:"+videoID)`). |
| DATERANGE assembly | Computed at master-playlist build time (Story 8.5 calls into this package). The `START-DATE` is anchored to `streaming_sessions.started_at` so DATERANGE math works against `EXT-X-PROGRAM-DATE-TIME`. |
| Out of scope | Chapter generation (Epic 9). The poster-thumbs of chapters are owned by Story 8.13. |

## 1. Architecture diagram

```
        master-playlist build (Story 8.5)
                          │
                          ▼
        ┌────────────────────────────────────────────────┐
        │ chapters.LoadForVideo(ctx, videoID)            │
        │   SELECT seq, start_sec, end_sec, title, source │
        │     FROM chapters WHERE video_id = $1           │
        │     ORDER BY start_sec ASC, seq ASC             │
        └────────────────────┬───────────────────────────┘
                             │  []Chapter
                             ▼
        ┌────────────────────────────────────────────────┐
        │ chapters.WriteDATERANGE(w, chapters, started)   │
        │   one #EXT-X-DATERANGE per chapter             │
        │   anchored to started_at                        │
        └────────────────────────────────────────────────┘

        GET .../chapters.json
                          │
                          ▼
        ┌────────────────────────────────────────────────┐
        │ chapters.ServeJSON(w, r, videoID)              │
        │   1. LoadForVideo → []Chapter                  │
        │   2. emit JSON: [{seq, start_sec, end_sec,     │
        │                   title, source}]              │
        │   3. Cache-Control: private, max-age=300        │
        └────────────────────────────────────────────────┘
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `streaming/internal/chapters/chapters.go` | `Chapter` struct, `LoadForVideo`, `WriteDATERANGE`, `ServeJSON`. |
| `streaming/internal/chapters/handler.go` | HTTP handlers for the two routes. |
| `streaming/internal/chapters/chapters_test.go` | Loader + DATERANGE tests. |
| `streaming/internal/chapters/handler_test.go` | End-to-end. |
| `shared/db/queries/chapters.sql` | sqlc input. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `streaming/internal/server/router.go` | Wire `/stream/{session_id}/chapters.json` (aud=streaming) and `/stream/posters/{video_id}/chapters.json` (aud=streaming-static). |
| `streaming/internal/hls/manifest.go` | `WriteMaster` calls `chapters.WriteDATERANGE` after the variant lines (already drafted in Story 8.5; this story implements the writer). |
| `streaming/internal/observability/metrics.go` | `chapters_served_total{format}`, `chapters_cache_hit_total`. |
| `specs/epics/08-streaming/README.md` | Tick 8.12. |

### 2.3 Type definitions

```go
// streaming/internal/chapters/chapters.go
package chapters

import (
    "context"
    "fmt"
    "io"
    "time"

    "github.com/google/uuid"
)

type Source string

const (
    SourceEmbedded Source = "embedded"
    SourceManual   Source = "manual"
    SourceInferred Source = "inferred"
)

type Chapter struct {
    Seq      int       `json:"seq"`
    StartSec float64   `json:"start_sec"`
    EndSec   float64   `json:"end_sec"`
    Title    string    `json:"title"`
    Source   Source    `json:"source"`
}

func LoadForVideo(ctx context.Context, db DBConn, videoID uuid.UUID, durationSec float64) ([]Chapter, error) {
    rows, err := db.QueryChapters(ctx, videoID)
    if err != nil {
        return nil, err
    }
    out := make([]Chapter, 0, len(rows))
    for _, r := range rows {
        end := r.EndSec
        if end > durationSec {
            end = durationSec
        }
        out = append(out, Chapter{
            Seq:      r.Seq,
            StartSec: r.StartSec,
            EndSec:   end,
            Title:    r.Title,
            Source:   Source(r.Source),
        })
    }
    return out, nil
}
```

### 2.4 sqlc query

```sql
-- name: QueryChapters :many
SELECT seq, start_sec, end_sec, title, source
  FROM chapters
 WHERE video_id = $1
 ORDER BY start_sec ASC, seq ASC;
```

`chapters` is owned by Epic 9 Story 9.18. The priority merge
(`embedded > manual > inferred` on overlap) is done at insert time, so
the rows we read are already authoritative.

### 2.5 DATERANGE writer

Per AC-2, one `#EXT-X-DATERANGE` per chapter, anchored to the session's
`started_at`. This depends on the master playlist also emitting
`#EXT-X-PROGRAM-DATE-TIME` (which Story 8.5's variant playlists do via
ffmpeg's `-hls_flags +program_date_time`):

```go
// streaming/internal/chapters/chapters.go (continued)

// WriteDATERANGE writes one #EXT-X-DATERANGE per chapter to w. The
// `started` parameter is the session's start time (used as the
// reference for START-DATE arithmetic).
//
// Output line shape:
//   #EXT-X-DATERANGE:CLASS="chapter",ID="<seq>",START-DATE="<iso>",
//                  DURATION=<seconds>,X-TITLE="<escaped title>"
func WriteDATERANGE(w io.Writer, chapters []Chapter, started time.Time) error {
    for _, c := range chapters {
        startDate := started.Add(time.Duration(c.StartSec * float64(time.Second))).UTC()
        line := fmt.Sprintf(
            "#EXT-X-DATERANGE:CLASS=%q,ID=%q,START-DATE=%q,DURATION=%.3f,X-TITLE=%q\n",
            "chapter",
            strconv.Itoa(c.Seq),
            startDate.Format("2006-01-02T15:04:05.000Z07:00"),
            c.EndSec-c.StartSec,
            quoteEscape(c.Title),
        )
        if _, err := io.WriteString(w, line); err != nil {
            return err
        }
    }
    return nil
}

// quoteEscape escapes embedded double-quotes per RFC 8216 §4.2 quoted-string.
func quoteEscape(s string) string {
    return strings.ReplaceAll(s, `"`, `\"`)
}
```

### 2.6 HTTP handlers

```go
// streaming/internal/chapters/handler.go
package chapters

import (
    "encoding/json"
    "net/http"
    "strconv"

    "github.com/go-chi/chi/v5"
    "github.com/google/uuid"

    "maktaba/streaming/internal/httpx"
)

type Handler struct {
    DB      DBConn
    Probe   probe.Lookup
    Store   SessionStore
    Metrics *Metrics
}

// ServeSessionJSON handles GET /stream/{session_id}/chapters.json.
// The signed-URL middleware has already validated aud=streaming, sub=session_id.
func (h *Handler) ServeSessionJSON(w http.ResponseWriter, r *http.Request) {
    sid := chi.URLParam(r, "session_id")
    sess, err := h.Store.Get(r.Context(), sid)
    if err != nil {
        httpx.Write(w, http.StatusNotFound, "session-not-found", "session not found", "")
        return
    }
    h.serveJSON(w, r, sess.VideoID, sess.DurationSec)
}

// ServeStaticJSON handles GET /stream/posters/{video_id}/chapters.json.
// The signed-URL middleware has validated aud=streaming-static and
// sub=sha256("chapters.json:"+video_id) — see Epic 8 README.
func (h *Handler) ServeStaticJSON(w http.ResponseWriter, r *http.Request) {
    vidStr := chi.URLParam(r, "video_id")
    vid, err := uuid.Parse(vidStr)
    if err != nil {
        httpx.Write(w, http.StatusBadRequest, "bad-video-id", "invalid video id", "")
        return
    }
    row, err := h.Probe.LookupVideo(r.Context(), vid)
    if err != nil {
        httpx.Write(w, http.StatusNotFound, "video-not-found", "video not found", "")
        return
    }
    h.serveJSON(w, r, vid, row.MediaInfo.DurationSec)
}

func (h *Handler) serveJSON(w http.ResponseWriter, r *http.Request, vid uuid.UUID, durationSec float64) {
    chs, err := LoadForVideo(r.Context(), h.DB, vid, durationSec)
    if err != nil {
        httpx.Write(w, 500, "chapters-load-failed", "could not load chapters", err.Error())
        return
    }
    w.Header().Set("Content-Type", "application/json; charset=utf-8")
    w.Header().Set("Cache-Control", "private, max-age=300")
    if err := json.NewEncoder(w).Encode(chs); err != nil {
        return
    }
    h.Metrics.Served.WithLabelValues("json").Inc()
}
```

## 3. Test plan

### 3.1 Loader tests (`chapters_test.go`)

| Test | What it pins |
|---|---|
| `TestLoadForVideo_EmptyReturnsEmptySlice` | No rows → empty `[]Chapter`. |
| `TestLoadForVideo_SortedByStartSecThenSeq` | Two chapters with identical `start_sec=10` and seq 2 / seq 5 → output order is seq 2, seq 5. AC edge case. |
| `TestLoadForVideo_ClampsEndSecToDuration` | Row `end_sec = duration_sec + 5` → loaded chapter has `end_sec = duration_sec`. AC edge case. |
| `TestLoadForVideo_PriorityMergeAlreadyApplied` | Insert one embedded + one manual + one inferred chapter at overlapping ranges (priority merge done at insert by Epic 9) → loader returns whatever insert resolved to; we don't re-merge here. |

### 3.2 DATERANGE tests (`chapters_test.go`)

| Test | What it pins |
|---|---|
| `TestWriteDATERANGE_OneLinePerChapter` | 3 chapters → 3 `#EXT-X-DATERANGE:CLASS="chapter"` lines. AC-2. |
| `TestWriteDATERANGE_StartDateAnchoredToStartedAt` | started=2030-01-01T00:00:00Z, chapter start_sec=120 → line has `START-DATE="2030-01-01T00:02:00.000Z"`. |
| `TestWriteDATERANGE_DurationDecimal` | start=10.0, end=23.456 → DURATION=13.456. |
| `TestWriteDATERANGE_TitleEscapesQuotes` | Title `He said "hi"` → `X-TITLE="He said \"hi\""`. |
| `TestWriteDATERANGE_EmptyChaptersWritesNothing` | Empty slice → 0 bytes written. |
| `TestWriteDATERANGE_TitleWithUnicode` | Title `الفصل الأول` round-trips byte-for-byte (UTF-8 not escaped). |

### 3.3 Handler integration (`handler_test.go`)

| Test | What it pins |
|---|---|
| `TestHandler_SessionJSON_ReturnsArray` | GET `/stream/{sid}/chapters.json` → 200, JSON array of `{seq,start_sec,end_sec,title,source}`. AC-1. |
| `TestHandler_SessionJSON_NoChaptersReturnsEmptyArray` | Session for a video with no chapters → 200, body `[]`. AC edge case. |
| `TestHandler_StaticJSON_ReturnsArray` | GET `/stream/posters/{vid}/chapters.json` (aud=streaming-static) → 200, same shape. AC-1. |
| `TestHandler_StaticJSON_RejectsWrongSub` | JWT with `sub=video_id` instead of `sub=sha256("chapters.json:"+video_id)` → 401 `wrong-sub`. (Cross-link to Story 8.1.) |
| `TestHandler_SessionJSON_SortAndClamp` | Mixed start_sec rows including an end_sec past duration → output is sorted and clamped. |
| `TestHandler_StaticJSON_VideoNotFound_404` | Unknown video_id → 404 `video-not-found`. |
| `TestHandler_CacheControlPrivateMaxAge300` | Both endpoints set `Cache-Control: private, max-age=300`. |

### 3.4 AVPlayer DATERANGE compatibility (manual/CI gate)

`TestE2E_AVPlayerChapterMetadataPopulated` — drives an AVPlayer-equivalent
(via a small Go program using `mediastreamvalidator`'s introspection
mode, or via `ffprobe -show_chapters` on an Apple-compliant fixture).
Asserts that `EXT-X-DATERANGE` chapters are recognised. Skipped in
sandboxes without the tool.

## 4. Test code scaffolding

```go
// streaming/internal/chapters/chapters_test.go
package chapters_test

import (
    "bytes"
    "testing"
    "time"

    "github.com/stretchr/testify/require"

    "maktaba/streaming/internal/chapters"
)

func TestWriteDATERANGE_StartDateAnchoredToStartedAt(t *testing.T) {
    var buf bytes.Buffer
    started := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
    chs := []chapters.Chapter{
        {Seq: 1, StartSec: 120, EndSec: 240, Title: "Intro", Source: chapters.SourceEmbedded},
    }
    require.NoError(t, chapters.WriteDATERANGE(&buf, chs, started))
    out := buf.String()

    require.Contains(t, out, `#EXT-X-DATERANGE:CLASS="chapter"`)
    require.Contains(t, out, `ID="1"`)
    require.Contains(t, out, `START-DATE="2030-01-01T00:02:00.000Z"`)
    require.Contains(t, out, `DURATION=120.000`)
    require.Contains(t, out, `X-TITLE="Intro"`)
}

func TestWriteDATERANGE_TitleEscapesQuotes(t *testing.T) {
    var buf bytes.Buffer
    chs := []chapters.Chapter{
        {Seq: 1, StartSec: 0, EndSec: 1, Title: `He said "hi"`, Source: chapters.SourceManual},
    }
    require.NoError(t, chapters.WriteDATERANGE(&buf, chs, time.Now()))
    require.Contains(t, buf.String(), `X-TITLE="He said \"hi\""`)
}

func TestWriteDATERANGE_EmptyChaptersWritesNothing(t *testing.T) {
    var buf bytes.Buffer
    require.NoError(t, chapters.WriteDATERANGE(&buf, nil, time.Now()))
    require.Equal(t, 0, buf.Len())
}
```

## 5. Edge cases — handling table

| Case | Behaviour | Where it's pinned |
|---|---|---|
| Two chapters with identical `start_sec` | Secondary sort by `seq` ASC. | `TestLoadForVideo_SortedByStartSecThenSeq` |
| `end_sec > duration_sec` (wrong embedded chapter) | Clamped to `duration_sec` at load time. | `TestLoadForVideo_ClampsEndSecToDuration` |
| Inferred chapter where the player has no segment yet (live HLS window) | Still emitted in JSON. The DATERANGE in the live playlist is added when the segment containing it enters the rolling window — handled by Story 8.5's master rebuild on each fetch (no special case here). | Documented; no test (live-window timing is owned by 8.5). |
| Title with embedded `"` | Backslash-escaped per RFC 8216 quoted-string. | `TestWriteDATERANGE_TitleEscapesQuotes` |
| Title in non-Latin scripts | UTF-8 passes through; we don't apply bidi marks (DATERANGE titles are display-only and players handle direction). | `TestWriteDATERANGE_TitleWithUnicode` |
| Session JSON requested without auth | Caught by Story 8.1's middleware before this handler runs. | Cross-link to Story 8.1. |
| Static JSON requested with the wrong sub | 401 `wrong-sub` from middleware. | `TestHandler_StaticJSON_RejectsWrongSub`. |
| `chapters` table has no rows for a video | Endpoint returns `[]` (200), playlist has no DATERANGE entries. | `TestHandler_SessionJSON_NoChaptersReturnsEmptyArray` |
| Player ignores DATERANGE | Browsers without HLS-DATERANGE support fall back to the JSON endpoint via the player UI. | Documented in client epic. |

## 6. Dependencies

No new top-level deps. Uses `encoding/json`, `time`, `text/template` (no
templates; raw `Fprintf`).

## 7. Acceptance checklist

**JSON endpoint (story ACs)**
- [ ] AC-1: `GET /stream/{sid}/chapters.json` returns `[{seq, start_sec, end_sec, title, source}]` sorted by start_sec.
- [ ] AC-1 (static): `GET /stream/posters/{vid}/chapters.json` available with `aud=streaming-static`; rejected without correct sub.
- [ ] Empty returns `[]`, not 404.

**HLS DATERANGE**
- [ ] AC-2: One `#EXT-X-DATERANGE:CLASS="chapter"` per chapter; `START-DATE` anchored to `streaming_sessions.started_at`.
- [ ] DURATION is `end_sec - start_sec` to ms precision.
- [ ] Title quotes are escaped per RFC 8216.

**Sorting and clamping**
- [ ] Identical `start_sec` rows tie-break on `seq`.
- [ ] `end_sec` clamped to `duration_sec`.

**Caching**
- [ ] `Cache-Control: private, max-age=300` on the JSON endpoints.

**Compatibility**
- [ ] AVPlayer / `AVPlayerItemChapterMetadata` populates from the playlist DATERANGE (CI gate when tooling is available).

**Observability**
- [ ] `chapters_served_total{format}` counter (`json`, `daterange`).

**Docs**
- [ ] `specs/epics/08-streaming/README.md` ticks 8.12.
