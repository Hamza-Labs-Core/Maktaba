# Implementation Plan — Story 7.7 Subtitles & Chapters Read Endpoints

> Companion to [story-07-07-subtitles-chapters-read.md](story-07-07-subtitles-chapters-read.md).
> Read-only enumeration. Bytes are streamed by Streaming (Epic 8).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Routes | `GET /api/videos/{id}/subtitles`, `GET /api/videos/{id}/chapters`. |
| Storage | `subtitle_files`, `chapters` tables (Pipeline-owned schema; see architecture §8). Both have `BIGSERIAL` primary keys (`int64` in Go). |
| Signed URLs | Minted via `auth.Sign(claims)` from Epic 10 Story 10.8. The audience is `streaming-static`. |
| `Accept-Language` ordering | Stable sort: requested language first, then existing order. |
| Out of scope | Producing the subtitle bytes (Pipeline Story 4.4), Streaming-side serving (Epic 8 Story 8.5), inferred-chapter generation (Epic 9 Story 9.18). |

## 1. Architecture diagram

```
   GET /api/videos/{id}/subtitles
   Accept-Language: ar
        │
        ▼
   ┌────────────────────────────────────────────────────┐
   │ 1. SELECT * FROM subtitle_files                    │
   │      WHERE video_id = $1                           │
   │      ORDER BY is_default DESC, language, source     │
   │ 2. For each row, mint signed URL via auth.Sign({   │
   │       aud: "streaming-static",                     │
   │       sub: subtitle_file_id,                       │
   │       usr: user_id,                                │
   │       exp: now + ttl                                │
   │    }) → Streaming /subtitles/{id}.vtt?sig=...      │
   │ 3. Stable-sort by Accept-Language preference       │
   │ 4. Return [...]                                    │
   └────────────────────────────────────────────────────┘

   GET /api/videos/{id}/chapters
        │
        ▼
   ┌────────────────────────────────────────────────────┐
   │ SELECT seq, start_sec, end_sec, title, source      │
   │   FROM chapters WHERE video_id = $1                │
   │  ORDER BY seq                                      │
   └────────────────────────────────────────────────────┘
```

## 2. New files

| Path | Purpose |
|---|---|
| `api/internal/subtitles/handler.go` | Both GET routes. |
| `api/internal/subtitles/sign.go` | Signing wrapper (delegates to Epic 10's `auth.Sign`). |
| `api/internal/subtitles/lang.go` | `Accept-Language` parser + stable sort. |
| `api/internal/subtitles/handler_test.go` | Integration. |
| `api/internal/subtitles/lang_test.go` | Unit. |
| `shared/db/queries/subtitles.sql` | sqlc inputs. |

## 3. Type definitions

```go
// api/internal/subtitles/types.go
package subtitles

import (
    "time"
    "github.com/google/uuid"
)

// Subtitle.ID is int64 because subtitle_files.id is BIGSERIAL per
// architecture §8.
type Subtitle struct {
    ID        int64     `json:"id"`
    Language  string    `json:"language"`     // ISO 639-1
    Format    string    `json:"format"`       // "vtt" | "srt" | "ass" | "embedded"
    Source    string    `json:"source"`       // "auto" | "external" | "embedded"
    IsDefault bool      `json:"is_default"`
    URL       string    `json:"url"`
    ExpiresAt time.Time `json:"expires_at"`
}

type Chapter struct {
    Seq      int     `json:"seq"`
    StartSec float64 `json:"start_sec"`
    EndSec   float64 `json:"end_sec"`
    Title    string  `json:"title"`
    Source   string  `json:"source"` // "embedded" | "manual" | "inferred"
}
```

## 4. Handler scaffolding

```go
// api/internal/subtitles/handler.go
package subtitles

import (
    "encoding/json"
    "net/http"

    "github.com/go-chi/chi/v5"
    "github.com/google/uuid"

    "maktaba/api/internal/auth"
    "maktaba/api/internal/httperror"
)

type Deps struct {
    DB     DB
    Signer auth.Signer
    TTL    time.Duration   // subtitle_url_ttl_sec, default 3600s
    Clock  func() time.Time
}

func (h *handler) listSubtitles(w http.ResponseWriter, r *http.Request) {
    id, err := uuid.Parse(chi.URLParam(r, "id"))
    if err != nil { httperror.Write(w, r, httperror.BadRequest("invalid id")); return }

    rows, err := h.db.SubtitleFilesByVideo(r.Context(), id)
    if err != nil { httperror.Write(w, r, httperror.Internal("db error")); return }

    user := userFromCtx(r.Context()) // Epic 10 wires this in
    out := make([]Subtitle, 0, len(rows))
    for _, row := range rows {
        exp := h.clock().Add(h.ttl)
        idStr := strconv.FormatInt(row.ID, 10)
        url, err := h.signer.Sign(auth.Claims{
            Aud:       "streaming-static",
            Sub:       idStr,
            UserID:    user.ID,
            ExpiresAt: exp,
        }, "/subtitles/"+idStr+"."+row.Format)
        if err != nil { httperror.Write(w, r, httperror.Internal("sign failed")); return }
        out = append(out, Subtitle{
            ID: row.ID, Language: row.Language, Format: row.Format,
            Source: row.Source, IsDefault: row.IsDefault,
            URL: url, ExpiresAt: exp,
        })
    }

    out = orderByAcceptLanguage(out, r.Header.Get("Accept-Language"))
    json.NewEncoder(w).Encode(out)
}

func (h *handler) listChapters(w http.ResponseWriter, r *http.Request) {
    id, err := uuid.Parse(chi.URLParam(r, "id"))
    if err != nil { httperror.Write(w, r, httperror.BadRequest("invalid id")); return }

    rows, err := h.db.ChaptersByVideo(r.Context(), id)
    if err != nil { httperror.Write(w, r, httperror.Internal("db error")); return }
    if rows == nil { rows = []ChapterRow{} } // [] not null

    out := make([]Chapter, 0, len(rows))
    for _, r := range rows {
        out = append(out, Chapter{Seq: int(r.Seq), StartSec: r.StartSec,
            EndSec: r.EndSec, Title: r.Title, Source: r.Source})
    }
    json.NewEncoder(w).Encode(out)
}
```

## 5. `Accept-Language` parser

```go
// api/internal/subtitles/lang.go
package subtitles

import (
    "sort"
    "strconv"
    "strings"
)

// orderByAcceptLanguage performs a STABLE sort of subs so that languages
// the user prefers come first; everything else preserves original order.
// Quality factors (q=) are honored; missing q is treated as 1.0.
func orderByAcceptLanguage(subs []Subtitle, header string) []Subtitle {
    if header == "" || header == "*" { return subs }

    pref := parseAcceptLanguage(header)
    rankOf := make(map[string]int, len(subs))
    for i, s := range subs { rankOf[s.Language] = i + 1 } // stable fallback

    score := func(lang string) (float64, int) {
        if q, ok := pref[lang]; ok { return q, 0 }
        // Match prefix: "ar" matches "ar-SA" → fall back.
        if dash := strings.IndexByte(lang, '-'); dash > 0 {
            if q, ok := pref[lang[:dash]]; ok { return q, 1 }
        }
        return 0, 2
    }

    sort.SliceStable(subs, func(i, j int) bool {
        qi, ti := score(subs[i].Language)
        qj, tj := score(subs[j].Language)
        if ti != tj { return ti < tj }
        if qi != qj { return qi > qj }
        return rankOf[subs[i].Language] < rankOf[subs[j].Language]
    })
    return subs
}

func parseAcceptLanguage(h string) map[string]float64 {
    out := map[string]float64{}
    for _, item := range strings.Split(h, ",") {
        item = strings.TrimSpace(item)
        if item == "" { continue }
        lang, q := item, 1.0
        if semi := strings.Index(item, ";"); semi >= 0 {
            lang = strings.TrimSpace(item[:semi])
            for _, p := range strings.Split(item[semi+1:], ";") {
                if kv := strings.SplitN(strings.TrimSpace(p), "=", 2); len(kv) == 2 && kv[0] == "q" {
                    if f, err := strconv.ParseFloat(kv[1], 64); err == nil { q = f }
                }
            }
        }
        out[lang] = q
    }
    return out
}
```

## 6. SQL — sqlc inputs

`shared/db/queries/subtitles.sql`:

```sql
-- name: SubtitleFilesByVideo :many
SELECT id, video_id, language, format, source, is_default, path
  FROM subtitle_files
 WHERE video_id = $1
 ORDER BY is_default DESC, language, source;

-- name: ChaptersByVideo :many
SELECT seq, start_sec, end_sec, title, source
  FROM chapters
 WHERE video_id = $1
 ORDER BY seq;
```

## 7. Test plan

### 7.1 Unit (`lang_test.go`)

| Test | What it pins |
|---|---|
| `TestParseAcceptLanguageBasic` | `"ar,en;q=0.5"` → `{ar:1.0, en:0.5}`. |
| `TestParseAcceptLanguageEmpty` | `""` → empty map → no reordering. |
| `TestOrderArFirst` | Subs `[en, ar, fr]` with header `ar` → `[ar, en, fr]`. |
| `TestOrderPreservesUnmatched` | Subs `[en, ar, fr]` with header `de` → unchanged order. |
| `TestPrefixMatch` | Sub `ar-SA` with header `ar` → ranked first. |
| `TestStableForEqualRank` | Two `en` subs in input order → preserved. |
| `TestQualityFactor` | Subs `[en, ar]` with header `ar;q=0.1, en;q=0.9` → `[en, ar]`. |

### 7.2 Integration (`handler_test.go`)

| Test | What it pins |
|---|---|
| `TestSubtitlesEnumerates` | Fixture with auto VTT, external SRT, embedded → response array of three with correct `format`/`source` values. |
| `TestSubtitlesEmpty` | Video with zero subtitles → `[]`, status 200 (not 404). |
| `TestSubtitlesURLSigned` | Each item's `url` decodes to a JWT with `aud=streaming-static`, `sub=subtitle_id`, `exp=now+ttl`. |
| `TestSubtitlesURLTTL` | Frozen clock; `expires_at = clock.Now()+ttl`; configurable TTL respected. |
| `TestSubtitlesAcceptLanguage` | Three subs (ar, en, fr); `Accept-Language: ar` → ar first. |
| `TestSubtitlesNoStat` | Pre-delete the subtitle file; URL is still minted (the API does not stat per request). |
| `TestChaptersEnumerates` | Fixture with embedded + inferred chapters → all returned, ordered by seq. |
| `TestChaptersEmpty` | No chapters → `[]`. |
| `TestChaptersInferredSource` | `source` field is `inferred` for the topic-shift fixture. |

## 8. Edge cases — handling table

| Case | Behaviour | Where it's pinned |
|---|---|---|
| Subtitle file deleted from disk between Pipeline write and API read | URL is still minted; Streaming returns 404 to the client at fetch time. We do not stat per request. | `TestSubtitlesNoStat` |
| `Accept-Language` is `*` | No reordering; original DB order is returned. | `TestParseAcceptLanguageEmpty` (variant) |
| `Accept-Language` lists languages not in the catalog | Ignored; original order preserved. | `TestOrderPreservesUnmatched` |
| TTL configured to 0 (testing) | URLs expire immediately; clients get 401 at fetch. The endpoint still mints them; deferring to Streaming. | Integration |
| External SRT served as VTT | The `format` field in the response is `srt` (matches the source); Streaming converts on the fly. The response is unchanged. | `TestSubtitlesEnumerates` |
| Two subs marked `is_default` (data bug) | Both rendered with `is_default: true`; clients pick the first by sort order. Fixed at the schema level by Pipeline (uniqueness constraint not added here). | Documented |
| Chapters whose `source` is unknown | Stored value is returned verbatim; the API does not normalise. | Documented |
| Many subtitles (e.g. 50 languages) | Response is unbounded; not paginated. Document a soft cap of 64 in the API reference; if exceeded, server logs a warning but still returns all. | Documented |
| User has no access to the video | Returns 403 (auth middleware will short-circuit before this handler runs once Epic 10 lands). | Out-of-scope here. |

## 9. Acceptance checklist

- [ ] `GET /subtitles` returns `[]` for a video with zero subtitles.
- [ ] Each item's `url` is signed with `aud=streaming-static`, `exp=now+TTL`.
- [ ] `Accept-Language` reorders results stably.
- [ ] No file system stats are performed per request.
- [ ] `GET /chapters` returns rows ordered by seq, with `source` preserved.
- [ ] All `Test*` cases pass.
- [ ] `specs/epics/07-api-server/README.md` ticks story 7.7.
