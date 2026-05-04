# Implementation Plan — Story 8.11 Live Subtitle Rendering

> Companion to [story-08-11-live-subtitle.md](story-08-11-live-subtitle.md).
> The story states *what* and *why*; this plan states *how*. Architecture
> reference: [§4.5](../../architecture.md#45-subtitle-handling). Embedded
> extraction depends on Pipeline's `ExtractEmbeddedSubtitle` RPC (Epic 4
> Story 4.x). Burned-in subs flow is owned by [Story 8.5](plan-08-05-hls-transcode.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Package | `streaming/internal/subs/` — VTT generation (auto), VTT conversion (sidecar), embedded-fetch client. |
| Auto-VTT generation | Streamed: read `transcript_segments` paginated by `start_sec`; emit cues lazily. We never load the whole transcript into memory. |
| Sidecar SRT→VTT | One-shot conversion, cached at `cache/subs/{hash}/{lang}.vtt` with atomic rename. |
| Embedded extraction | Out-of-process via Pipeline gRPC `ExtractEmbeddedSubtitle(video_id, stream_index)`; result is a path on the **shared cache volume**. The Streaming binary never spawns ffmpeg for embedded extraction (security boundary: only Pipeline owns ffmpeg over media files). |
| HLS subtitle wrapping | Single-segment playlist referencing the monolithic VTT; `EXT-X-TARGETDURATION = duration_sec`. |
| Burn-subs case | `/subs/*.vtt` returns 204 (Story 8.5 owns the visual rendering). |
| Bidi safety | Each cue text is wrapped with U+2068 / U+2069 (FSI/PDI) to keep mixed-script text from reordering. |
| Out of scope | The transcript itself (Epic 3). Sidecar discovery (Epic 1). Translation (different feature). |

## 1. Architecture diagram

```
                client GET /stream/{sid}/subs/{lang}.vtt?sig=…
                          │
                          ▼  (signed-URL middleware: aud=streaming-static)
        ┌────────────────────────────────────────────────────────┐
        │ subs.Handler.ServeVTT                                  │
        │   1. resolve session → video_id + duration_sec         │
        │   2. dispatch on track source:                         │
        │       - 'auto'        → streamAutoVTT(video_id)         │
        │       - sidecar SRT   → conversion + cache              │
        │       - sidecar VTT   → cached re-emit                  │
        │       - embedded N    → Pipeline RPC, then cache        │
        │   3. apply HTML escape + bidi-isolation per cue         │
        │   4. respond text/vtt; Cache-Control varies by source  │
        └────────────────────────────────────────────────────────┘
                          │
                          ▼
        ┌────────────────────────────────────────────────────────┐
        │ subs.AutoStreamer (transcript_segments paginated)      │
        │ subs.SRTConverter (cache-then-stream)                  │
        │ subs.EmbeddedClient (gRPC to Pipeline)                 │
        └────────────────────────────────────────────────────────┘
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `streaming/internal/subs/handler.go` | HTTP handlers for `.vtt` and `.m3u8` subtitle paths. |
| `streaming/internal/subs/auto_streamer.go` | `StreamAutoVTT(ctx, w, videoID, page)` — reads `transcript_segments` paginated. |
| `streaming/internal/subs/srt_converter.go` | `ConvertSRTToVTT(in io.Reader, out io.Writer)` — pure-Go converter with HTML-escape + bidi wrap. |
| `streaming/internal/subs/embedded_client.go` | gRPC client for Pipeline's `ExtractEmbeddedSubtitle`. |
| `streaming/internal/subs/cue.go` | `Cue` struct, `WriteVTTHeader(w)`, `WriteCue(w, cue)`, formatting helpers. |
| `streaming/internal/subs/playlist.go` | `WriteSubsPlaylist(w, durationSec, vttRelURL)` — single-segment wrapper. |
| `streaming/internal/subs/handler_test.go` | End-to-end. |
| `streaming/internal/subs/auto_streamer_test.go` | Pagination + live-update tests. |
| `streaming/internal/subs/srt_converter_test.go` | Format conversion + escaping. |
| `streaming/internal/subs/embedded_client_test.go` | gRPC mock. |
| `streaming/internal/subs/playlist_test.go` | M3U8 wrapper conformance. |
| `shared/proto/pipeline/v1/pipeline.proto` (modified) | Adds `ExtractEmbeddedSubtitle` RPC if not already there. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `streaming/internal/server/router.go` | Wire `GET /stream/{sid}/subs/{lang}.vtt`, `GET /stream/{sid}/subs/{lang}.m3u8`. The signed-URL middleware uses `aud=streaming-static` (per Epic 8 README's table). |
| `streaming/internal/hls/manifest.go` | When emitting the master playlist, point each `EXT-X-MEDIA:TYPE=SUBTITLES` URI at `/stream/{sid}/subs/{lang}.m3u8`. |
| `streaming/internal/observability/metrics.go` | `subs_auto_cues_emitted_total`, `subs_srt_conversions_total`, `subs_embedded_fetches_total{result}`, `subs_serve_duration_seconds`. |
| `specs/epics/08-streaming/README.md` | Tick 8.11. |

### 2.3 Type definitions

```go
// streaming/internal/subs/cue.go
package subs

type Cue struct {
    Idx      int
    Start    time.Duration
    End      time.Duration
    Text     string  // newline-separated lines as supplied by upstream
    Lang     string  // 'ar', 'en' — used to pick LRO/RLO direction wrap
}

func WriteVTTHeader(w io.Writer) (int, error) {
    return io.WriteString(w, "WEBVTT\n\n")
}

func WriteCue(w io.Writer, c Cue) error {
    if _, err := fmt.Fprintf(w, "%s --> %s\n", vttTimestamp(c.Start), vttTimestamp(c.End)); err != nil {
        return err
    }
    for _, line := range strings.Split(c.Text, "\n") {
        wrapped := bidiIsolate(c.Lang, htmlEscape(line))
        if _, err := io.WriteString(w, wrapped+"\n"); err != nil {
            return err
        }
    }
    _, err := io.WriteString(w, "\n")
    return err
}

// bidiIsolate wraps text in a Unicode bidi isolate (FSI…PDI) so that
// mixed-script lines render with the source language's natural direction
// inside an HTML/VTT context. This matches Epic 7 Story 7.6.
func bidiIsolate(lang, text string) string {
    const fsi = "⁨"
    const pdi = "⁩"
    return fsi + text + pdi
}

// htmlEscape escapes <, >, & in cue text; we DO NOT escape quotes
// because the WebVTT cue body is literal text, not an attribute.
// Story-stated requirement: prevent injection through external SRT or
// LLM output that contains markup.
func htmlEscape(s string) string {
    s = strings.ReplaceAll(s, "&", "&amp;")
    s = strings.ReplaceAll(s, "<", "&lt;")
    s = strings.ReplaceAll(s, ">", "&gt;")
    return s
}

// vttTimestamp produces "HH:MM:SS.mmm".
func vttTimestamp(d time.Duration) string {
    if d < 0 { d = 0 }
    h := int(d / time.Hour)
    m := int(d/time.Minute) % 60
    s := int(d/time.Second) % 60
    ms := int(d/time.Millisecond) % 1000
    return fmt.Sprintf("%02d:%02d:%02d.%03d", h, m, s, ms)
}
```

### 2.4 Auto streamer

```go
// streaming/internal/subs/auto_streamer.go
package subs

import (
    "context"
    "io"
    "time"

    "github.com/google/uuid"
    "github.com/jackc/pgx/v5"
)

const (
    autoStreamPageSize = 200
)

type AutoStreamer struct {
    DB      DBConn
    Now     func() time.Time
    Lang    func(videoID uuid.UUID) string  // returns the source lang for bidi wrap
}

// StreamAutoVTT writes a valid WebVTT body to w by reading
// transcript_segments in stable start_sec order, page-by-page. The
// transcript may grow under our feet; we do NOT loop forever — once we
// hit the duration_sec ceiling the loop exits. A subsequent fetch will
// pick up new cues (story AC-1 — refresh after 10 s contains more).
func (a *AutoStreamer) StreamAutoVTT(ctx context.Context, w io.Writer, videoID uuid.UUID, durationSec float64) error {
    if _, err := WriteVTTHeader(w); err != nil {
        return err
    }
    lang := a.Lang(videoID)

    cursor := time.Duration(0)
    idx := 0
    for {
        page, err := a.DB.QueryTranscriptSegments(ctx, videoID, cursor, autoStreamPageSize)
        if err != nil {
            return err
        }
        if len(page) == 0 {
            return nil
        }
        for _, row := range page {
            cue := Cue{
                Idx:   idx,
                Start: time.Duration(row.StartSec * float64(time.Second)),
                End:   time.Duration(row.EndSec * float64(time.Second)),
                Text:  row.Text,
                Lang:  lang,
            }
            if cue.End > time.Duration(durationSec*float64(time.Second)) {
                cue.End = time.Duration(durationSec * float64(time.Second))
            }
            if err := WriteCue(w, cue); err != nil {
                return err
            }
            idx++
            cursor = cue.End
        }
        if len(page) < autoStreamPageSize {
            return nil
        }
        if cursor >= time.Duration(durationSec*float64(time.Second)) {
            return nil
        }
    }
}
```

`DBConn.QueryTranscriptSegments` is a thin abstraction over a sqlc query:

```sql
-- name: QueryTranscriptSegments :many
-- Canonical schema: transcript_segments has no `video_id` column.
-- The video link is held by `transcripts.video_id`. We JOIN through
-- transcripts and additionally filter `superseded_at IS NULL` so only
-- the current transcript revision contributes cues.
SELECT s.id, s.start_sec, s.end_sec, s.text
  FROM transcript_segments s
  JOIN transcripts t ON t.id = s.transcript_id
 WHERE t.video_id = $1
   AND t.superseded_at IS NULL
   AND s.end_sec > $2
 ORDER BY s.start_sec ASC
 LIMIT $3;
```

### 2.5 SRT conversion

```go
// streaming/internal/subs/srt_converter.go
package subs

import (
    "bufio"
    "fmt"
    "io"
    "regexp"
    "strconv"
    "strings"
    "time"
)

var srtTimestamp = regexp.MustCompile(
    `^(\d{2}):(\d{2}):(\d{2})[,.](\d{3})\s*-->\s*(\d{2}):(\d{2}):(\d{2})[,.](\d{3})\s*$`)

type SRTConverter struct {
    Lang string
}

// ConvertSRTToVTT reads an SRT body and writes WebVTT to w. It
// HTML-escapes cue text and applies bidi isolation per cue line.
// Tolerant of CRLF / lone CR / Windows-1252 BOM in SRTs.
func (c SRTConverter) Convert(in io.Reader, out io.Writer) error {
    if _, err := WriteVTTHeader(out); err != nil {
        return err
    }
    sc := bufio.NewScanner(in)
    sc.Buffer(make([]byte, 64*1024), 1024*1024)

    var cur Cue
    state := 0 // 0=expect index, 1=expect timestamp, 2=expect text
    for sc.Scan() {
        line := strings.TrimRight(sc.Text(), "\r")
        line = strings.TrimPrefix(line, "﻿")
        switch state {
        case 0:
            if line == "" { continue }
            cur = Cue{}
            n, err := strconv.Atoi(strings.TrimSpace(line))
            if err == nil {
                cur.Idx = n
                state = 1
                continue
            }
            // Some malformed SRTs skip the index — fall through to timestamp.
            if matchSRTTimestamp(line, &cur) {
                state = 2
            }
        case 1:
            if matchSRTTimestamp(line, &cur) {
                state = 2
            } else {
                state = 0 // resync
            }
        case 2:
            if line == "" {
                cur.Lang = c.Lang
                if err := WriteCue(out, cur); err != nil {
                    return err
                }
                state = 0
                continue
            }
            if cur.Text == "" {
                cur.Text = line
            } else {
                cur.Text += "\n" + line
            }
        }
    }
    if state == 2 && cur.Text != "" {
        cur.Lang = c.Lang
        if err := WriteCue(out, cur); err != nil { return err }
    }
    return sc.Err()
}

func matchSRTTimestamp(line string, c *Cue) bool {
    m := srtTimestamp.FindStringSubmatch(line)
    if m == nil { return false }
    c.Start = parseHMSms(m[1], m[2], m[3], m[4])
    c.End   = parseHMSms(m[5], m[6], m[7], m[8])
    return true
}

func parseHMSms(h, m, s, ms string) time.Duration {
    H, _ := strconv.Atoi(h); M, _ := strconv.Atoi(m); S, _ := strconv.Atoi(s); MS, _ := strconv.Atoi(ms)
    return time.Duration(H)*time.Hour + time.Duration(M)*time.Minute +
        time.Duration(S)*time.Second + time.Duration(MS)*time.Millisecond
}
```

### 2.6 Embedded subtitle fetcher

```go
// streaming/internal/subs/embedded_client.go
package subs

import (
    "context"
    "errors"
    "io"
    "os"

    "google.golang.org/grpc/status"
    "google.golang.org/grpc/codes"

    pipelinev1 "maktaba/shared/proto/pipeline/v1"
)

type EmbeddedClient struct {
    Pipeline      pipelinev1.PipelineServiceClient
    Cache         *cache.Store
    SingleFlight  singleflight.Group
}

// Fetch returns a path to a cached VTT file containing the embedded
// subtitle stream. Single-flighted on (video_id, stream_index).
func (c *EmbeddedClient) Fetch(ctx context.Context, videoID uuid.UUID, streamIdx int) (string, error) {
    key := fmt.Sprintf("%s_s%d", videoID, streamIdx)
    out, err, _ := c.SingleFlight.Do(key, func() (any, error) {
        // 1. Cache hit?
        path := c.Cache.PathForEmbedded(videoID, streamIdx)
        if _, err := os.Stat(path); err == nil {
            return path, nil
        }

        // 2. Ask Pipeline to extract.
        resp, err := c.Pipeline.ExtractEmbeddedSubtitle(ctx, &pipelinev1.ExtractEmbeddedSubtitleRequest{
            VideoId:     videoID.String(),
            StreamIndex: int32(streamIdx),
        })
        if err != nil {
            // Map UNSUPPORTED-CODEC sub-error to a typed error.
            if st, ok := status.FromError(err); ok && st.Code() == codes.InvalidArgument {
                if hasUnsupportedCodec(st) {
                    return "", ErrUnsupportedSubtitleCodec
                }
            }
            return "", err
        }
        return resp.GetCachePath(), nil
    })
    if err != nil {
        return "", err
    }
    return out.(string), nil
}
```

Pipeline returns the absolute path on a volume that Streaming has read
access to (`/var/maktaba/cache/subs/{hash}/...`). This avoids streaming
multi-megabyte VTTs over gRPC.

### 2.7 HLS subtitle playlist wrapper

Per AC-4, the segmented VTT path is dropped. A single-segment playlist:

```go
// streaming/internal/subs/playlist.go
package subs

func WriteSubsPlaylist(w io.Writer, durationSec float64, vttRelURL string) error {
    return tmpl.Execute(w, map[string]any{
        "Duration": int(math.Ceil(durationSec)),
        "URI":      vttRelURL,
    })
}

var tmpl = template.Must(template.New("subs.m3u8").Parse(strings.TrimSpace(`
#EXTM3U
#EXT-X-VERSION:6
#EXT-X-TARGETDURATION:{{.Duration}}
#EXT-X-PLAYLIST-TYPE:VOD
#EXT-X-MEDIA-SEQUENCE:0
#EXTINF:{{.Duration}}.000,
{{.URI}}
#EXT-X-ENDLIST
`) + "\n"))
```

### 2.8 The HTTP handler

```go
// streaming/internal/subs/handler.go
package subs

import (
    "context"
    "fmt"
    "io"
    "net/http"
    "os"
    "strings"

    "github.com/go-chi/chi/v5"

    "maktaba/streaming/internal/httpx"
)

type Handler struct {
    Store     SessionStore
    Auto      *AutoStreamer
    Embedded  *EmbeddedClient
    Cache     *cache.Store
    Metrics   *Metrics
}

func (h *Handler) ServeVTT(w http.ResponseWriter, r *http.Request) {
    sid := chi.URLParam(r, "session_id")
    lang := chi.URLParam(r, "lang") // 'auto', 'ar', 'en', or 's<N>' for embedded

    sess, err := h.Store.Get(r.Context(), sid)
    if err != nil {
        httpx.Write(w, http.StatusNotFound, "session-not-found", "session not found", "")
        return
    }
    if sess.BurnSubs {
        // AC-6: when burned in, no external subs.
        w.WriteHeader(http.StatusNoContent)
        return
    }

    w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
    switch {
    case lang == "auto":
        w.Header().Set("Cache-Control", "no-cache, must-revalidate")
        if err := h.Auto.StreamAutoVTT(r.Context(), w, sess.VideoID, sess.DurationSec); err != nil {
            // Headers already written — log and bail.
            return
        }
    case strings.HasPrefix(lang, "s") && len(lang) > 1:
        idx, err := parseEmbeddedIdx(lang)
        if err != nil {
            httpx.Write(w, http.StatusNotFound, "subs-not-found", "unknown subtitle track", "")
            return
        }
        path, err := h.Embedded.Fetch(r.Context(), sess.VideoID, idx)
        if err != nil {
            if errors.Is(err, ErrUnsupportedSubtitleCodec) {
                httpx.Write(w, 415, "subtitle-format-unsupported",
                    "embedded subtitle codec unsupported", lang)
                return
            }
            httpx.Write(w, 502, "subs-extract-failed", "extraction failed", err.Error())
            return
        }
        h.serveCachedFile(w, r, path)
    default:
        // Sidecar SRT / VTT for {lang}.
        cachePath := h.Cache.PathForSidecar(sess.VideoID, lang, "vtt")
        if _, err := os.Stat(cachePath); err == nil {
            h.serveCachedFile(w, r, cachePath)
            return
        }
        // Session.SidecarSubtitles is populated at OpenSession time by
        // the Manager (Story 8.8). On Open, the Manager runs:
        //
        //   SELECT language, path, format
        //     FROM subtitle_files
        //    WHERE video_id = $1 AND is_external = true
        //
        // and stashes the result on the session row as
        // `SidecarSubtitles map[language]SidecarFile{Path,Format}`.
        // No `subtitle_tracks` table is involved (canonical schema is
        // `subtitle_files`). We never re-query here so .vtt requests do
        // not hit the DB on the hot path.
        sidecar, ok := sess.SidecarSubtitles[lang]
        if !ok {
            httpx.Write(w, http.StatusNotFound, "subs-not-found",
                "no subtitle for lang", lang)
            return
        }
        srtPath := sidecar.Path
        if err := h.convertAndCache(r.Context(), srtPath, cachePath, lang); err != nil {
            httpx.Write(w, 500, "subs-conversion-failed", "SRT conversion failed", err.Error())
            return
        }
        h.serveCachedFile(w, r, cachePath)
    }
}

func (h *Handler) ServeM3U8(w http.ResponseWriter, r *http.Request) {
    sid := chi.URLParam(r, "session_id")
    lang := chi.URLParam(r, "lang")
    sess, err := h.Store.Get(r.Context(), sid)
    if err != nil {
        httpx.Write(w, http.StatusNotFound, "session-not-found", "session not found", "")
        return
    }

    rel := fmt.Sprintf("%s.vtt?%s", lang, r.URL.RawQuery)
    w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
    w.Header().Set("Cache-Control", "no-store")
    _ = WriteSubsPlaylist(w, sess.DurationSec, rel)
}
```

The relative URL inherits the same `?sig=<jwt>` (the client's incoming
query string is reused via `r.URL.RawQuery`) so the player's range fetch
of the VTT carries a valid signed-static JWT (Story 8.1 AC-5).

## 3. Test plan

### 3.1 Cue / VTT formatting (`auto_streamer_test.go` + `cue_test.go`)

| Test | What it pins |
|---|---|
| `TestWriteVTTHeader` | Output is exactly `WEBVTT\n\n`. |
| `TestWriteCue_Timestamp` | 1.5 s → `00:00:01.500`. |
| `TestWriteCue_HTMLEscape` | Cue with `<script>alert(1)</script>` → output contains `&lt;script&gt;`, no raw `<`. AC-1. |
| `TestWriteCue_BidiIsolated` | Arabic line wrapped with U+2068…U+2069. |
| `TestWriteCue_NewlinesPreserved` | Multi-line cue text preserved as separate VTT lines. |

### 3.2 Auto streamer (`auto_streamer_test.go`)

| Test | What it pins |
|---|---|
| `TestAuto_PaginatesCues` | Mock DB returns 350 rows in batches of 200; output contains all 350. |
| `TestAuto_LiveAdditionsVisibleOnRefetch` | First fetch streams 100 cues; insert 50 more; second fetch returns 150. AC-1. |
| `TestAuto_EmptyTranscriptValidVTT` | No rows → output is exactly `WEBVTT\n\n` (valid empty VTT). AC edge case. |
| `TestAuto_ClipCuesToDuration` | A cue whose `end_sec=duration_sec+5` → emitted with `end = duration_sec`. AC edge case (subtitle longer than video). |
| `TestAuto_ClipCuesLogsWarning` | Same as above; assert a structured warning is logged. |

### 3.3 SRT conversion (`srt_converter_test.go`)

| Test | What it pins |
|---|---|
| `TestSRT_BasicCue` | `1\n00:00:01,000 --> 00:00:02,000\nHello\n\n` → VTT with cue `00:00:01.000 --> 00:00:02.000` and `Hello`. |
| `TestSRT_HTMLEscape` | SRT containing `<script>` → escaped in VTT output. AC-2. |
| `TestSRT_RoundTripMillisecondPrecision` | Timestamps preserved to ms precision (no rounding). AC-2. |
| `TestSRT_CRLFTolerated` | CRLF and lone CR line endings parse correctly. |
| `TestSRT_BOMTolerated` | UTF-8 BOM at the start is stripped. |
| `TestSRT_MissingIndexLine_TolerantParse` | Malformed SRT with no index line still parses. |
| `TestSRT_DotDecimalSeparator` | `00:00:01.000` (dot) variant accepted. |

### 3.4 Embedded extraction (`embedded_client_test.go`)

| Test | What it pins |
|---|---|
| `TestEmbedded_CacheHitNoRPC` | File present at expected path → no Pipeline RPC issued. |
| `TestEmbedded_SingleFlight` | Two parallel Fetch calls for same (video, stream) → exactly one Pipeline RPC. AC-3. |
| `TestEmbedded_UnsupportedCodec_ErrTyped` | Pipeline returns `INVALID_ARGUMENT code='unsupported-codec'` → handler returns `ErrUnsupportedSubtitleCodec`; HTTP layer maps to 415. AC edge case. |
| `TestEmbedded_PipelineDownPropagatesError` | Pipeline RPC returns `UNAVAILABLE` → handler returns 502. |

### 3.5 Playlist wrapper (`playlist_test.go`)

| Test | What it pins |
|---|---|
| `TestSubsPlaylist_Structure` | Output contains `#EXTM3U`, `#EXT-X-VERSION:6`, `#EXT-X-TARGETDURATION:N`, exactly one `#EXTINF` and one URI line, `#EXT-X-ENDLIST`. AC-4. |
| `TestSubsPlaylist_DurationCeiling` | duration=12.4 → TARGETDURATION=13. |
| `TestSubsPlaylist_RelativeURIPreservesQueryString` | Input `vttRelURL=ar.vtt?sig=abc` → URI line is exactly `ar.vtt?sig=abc`. |

### 3.6 Handler integration (`handler_test.go`)

| Test | What it pins |
|---|---|
| `TestHandler_AutoStreamWebVTTValid` | GET `/stream/{sid}/subs/auto.vtt` → response passes the W3C WebVTT validator (we use `vtt.js`-equivalent or a minimal Go validator). AC-1. |
| `TestHandler_SidecarSRTLazyConversion` | First fetch converts SRT → VTT, caches; second fetch hits cache (asserted via mtime + spy). AC-2. |
| `TestHandler_BurnSubsReturns204` | Session with `burn_subs=true` → GET .vtt returns 204. AC-6. |
| `TestHandler_M3U8Variant` | GET `/stream/{sid}/subs/ar.m3u8` → single-segment playlist referencing `ar.vtt?sig=...`. AC-4. |
| `TestHandler_EmbeddedExtractInvokesPipeline` | GET `s2.vtt` for a video with embedded sub at index 2 → Pipeline RPC called once; cache file exists; second fetch served from cache. |
| `TestHandler_UnsupportedEmbeddedReturns415` | Pipeline RPC returns unsupported-codec → HTTP 415 `subtitle-format-unsupported`. |
| `TestHandler_AutoVTT_MidFetchGrowth` | Start streaming auto.vtt with 50 transcript rows; insert 50 more during streaming → response contains the 50 that existed at the moment of generation; refetch sees all 100. |

### 3.7 W3C WebVTT validator gate (CI)

A small Go-side validator parses the output and asserts:
- header line `WEBVTT`
- timestamps are `HH:MM:SS.mmm`
- `-->` separator
- empty line between cues

`TestVTT_FixtureSetPasses` runs over `testdata/sub-fixtures/*.vtt` to
guard against regressions in our generator.

## 4. Test code scaffolding

```go
// streaming/internal/subs/srt_converter_test.go
package subs_test

import (
    "bytes"
    "strings"
    "testing"

    "github.com/stretchr/testify/require"

    "maktaba/streaming/internal/subs"
)

func TestSRT_HTMLEscape(t *testing.T) {
    input := strings.TrimSpace(`
1
00:00:01,000 --> 00:00:02,000
Hello <script>alert(1)</script>
`)
    var out bytes.Buffer
    require.NoError(t, subs.SRTConverter{Lang: "en"}.Convert(strings.NewReader(input), &out))

    require.Contains(t, out.String(), "WEBVTT")
    require.Contains(t, out.String(), "00:00:01.000 --> 00:00:02.000")
    require.Contains(t, out.String(), "&lt;script&gt;alert(1)&lt;/script&gt;")
    require.NotContains(t, out.String(), "<script>")
}

func TestSRT_BOMTolerated(t *testing.T) {
    input := "﻿1\n00:00:01,000 --> 00:00:02,000\nHello\n\n"
    var out bytes.Buffer
    require.NoError(t, subs.SRTConverter{Lang: "en"}.Convert(strings.NewReader(input), &out))
    require.Contains(t, out.String(), "Hello")
}
```

```go
// streaming/internal/subs/auto_streamer_test.go
func TestAuto_LiveAdditionsVisibleOnRefetch(t *testing.T) {
    db := newFakeDB()
    db.AddSegments(genCues(0, 100))
    a := &subs.AutoStreamer{DB: db, Now: time.Now, Lang: func(uuid.UUID) string { return "ar" }}

    var first, second bytes.Buffer
    require.NoError(t, a.StreamAutoVTT(context.Background(), &first, uuid.New(), 600))
    require.Equal(t, 100, strings.Count(first.String(), "-->"))

    db.AddSegments(genCues(100, 50))
    require.NoError(t, a.StreamAutoVTT(context.Background(), &second, uuid.New(), 600))
    require.Equal(t, 150, strings.Count(second.String(), "-->"))
}
```

## 5. Edge cases — handling table

| Case | Behaviour | Where it's pinned |
|---|---|---|
| Transcript empty (transcribe job not started) | `WEBVTT\n\n` returned — valid empty VTT so the player can initialize the track. | `TestAuto_EmptyTranscriptValidVTT` |
| Subtitle longer than the video (wrong sidecar / SRT) | Cues clipped to `duration_sec`; warning logged. | `TestAuto_ClipCuesToDuration` |
| Embedded PGS / image-based subtitles | Pipeline returns `INVALID_ARGUMENT code='unsupported-codec'`; HTTP 415 `subtitle-format-unsupported`; the API filters such tracks out of `GET /api/videos/{id}/subtitles` (Epic 7). | `TestEmbedded_UnsupportedCodec_ErrTyped` |
| HTML / markup injection in cue text | `htmlEscape` runs on every line before bidi wrap. AC-1 / AC-2. | `TestSRT_HTMLEscape`, `TestWriteCue_HTMLEscape`. |
| Multi-line cues | Each line is escaped + bidi-wrapped independently; line breaks preserved. | `TestWriteCue_NewlinesPreserved`. |
| Mixed-script cue (Arabic + English) | Bidi isolation around the whole line; the line's natural direction wins inside the FSI/PDI wrap. | `TestWriteCue_BidiIsolated`. |
| Burn-subs session | `.vtt` endpoint returns 204; the `.m3u8` endpoint similarly returns 204 (or the manifest dispatcher omits the subs group entirely — Story 8.5). | `TestHandler_BurnSubsReturns204`. |
| Concurrent first-fetch of the same embedded sub | Single-flight on `(video_id, stream_index)`; one Pipeline RPC. | `TestEmbedded_SingleFlight`. |
| Sidecar SRT modified on disk | Cache key uses `content_hash` of the SRT (computed on first read). Subsequent reads compare; if hash changed, the cache is invalidated and re-converted. | Implicit — we hash the SRT on each cache miss; on cache hit we trust the stored hash. |
| Player reloads `.m3u8` mid-fetch | The playlist is monolithic; one `EXT-X-ENDLIST` per fetch. The VTT URL is fresh for each manifest fetch, carrying the current `?sig=`. | `TestSubsPlaylist_RelativeURIPreservesQueryString`. |
| Auto-VTT fetched during a live transcript update | The streamer reads at request time; cues present at the moment of generation are emitted. The caller can refetch to see new cues. | `TestHandler_AutoVTT_MidFetchGrowth`. |
| Player asks for `.vtt` after session closed | 404 `session-not-found`. | Implicit (`Store.Get` returns NotFound). |
| Pipeline binary crashed | `embedded_client.Fetch` returns the gRPC error; HTTP 502. | `TestEmbedded_PipelineDownPropagatesError`. |

## 6. Dependencies

| Dep | Version | Why this one |
|---|---|---|
| `golang.org/x/text/unicode/bidi` | latest | Optional — for richer bidi handling later. v1 uses bare U+2068/U+2069. |
| Pipeline gRPC client | from shared/proto | Embedded extraction. |

## 7. Acceptance checklist

**Auto-VTT (story ACs)**
- [ ] AC-1: `auto.vtt` is a valid WebVTT body streamed from `transcript_segments` paginated; cues are HTML-escaped; `Cache-Control: no-cache, must-revalidate`.

**Sidecar**
- [ ] AC-2: SRT → VTT converted on first request, cached at `cache/subs/{hash}/{lang}.vtt`; subsequent requests served from cache. HTML-escaping applied.

**Embedded**
- [ ] AC-3: First fetch invokes Pipeline `ExtractEmbeddedSubtitle`; subsequent fetches hit local cache; Streaming never invokes ffmpeg directly. Single-flight verified.

**HLS wrapping**
- [ ] AC-4: `subs/{lang}.m3u8` is a single-segment playlist referencing the monolithic `.vtt`; the original signed-URL query string is preserved on the inner URI.

**Bidi safety**
- [ ] AC-5: Mixed-script cues are bidi-isolated.

**Burn-subs**
- [ ] AC-6: Sessions with `burn_subs=true` return 204 from `.vtt` and `.m3u8` paths.

**Validation**
- [ ] W3C WebVTT validator passes for the fixture set.
- [ ] HTML injection vectors in transcripts/SRTs do not survive into cue text.

**Observability**
- [ ] Counters: `subs_auto_cues_emitted_total`, `subs_srt_conversions_total`, `subs_embedded_fetches_total{result}`.

**Docs**
- [ ] `specs/epics/08-streaming/README.md` ticks 8.11.
