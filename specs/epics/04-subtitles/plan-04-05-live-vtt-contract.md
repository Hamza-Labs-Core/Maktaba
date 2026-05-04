# Plan 4.5 — Live VTT serving (read-side contract) — implementation

> Implementation plan for [story-04-05-live-vtt-contract.md](story-04-05-live-vtt-contract.md).
> Self-contained: a developer should ship the story from this document
> alone. Cross-links: the producer side is
> [Plan 3.6 — segment commit + LISTEN/NOTIFY](../03-transcription/plan-03-06-segment-commit.md);
> the segment schema is owned by Story 3.5 (`transcripts.is_active`); the
> on-disk subtitle artifacts (sidecar SRT/VTT) are
> [Plan 4.1](plan-04-01-generate-from-segments.md); the formatting and
> bidi rules are
> [Plan 4.2](plan-04-02-formatting-wrapping.md); the player-facing HLS
> wrapper is [Story 8.11](../08-streaming/story-08-11-live-subtitle.md);
> the API enumeration endpoint is
> [Story 7.7](../07-api-server/story-07-07-subtitles-chapters-read.md).
>
> Scope reminder. This story is **contract-only**. The actual VTT bytes
> are emitted by the Streaming Service (Go) — that code lives in Epic 8.
> What this story owns is (a) the read-side SQL view
> `transcript_segments_v` that the Streaming Service queries, and (b) the
> HTTP-level contract the player relies on: response shape, headers,
> refresh semantics, partial-cue handling, error mapping. A developer
> implementing the Go endpoint in Epic 8 reads **this** plan to know
> what to emit; a developer implementing a third-party player or a
> debug script reads **this** plan to know what to expect.

---

## 0. Decisions and departures from `architecture.md` and the story

| #  | Decision | Source | Rationale |
|----|----------|--------|-----------|
| D1 | The transport is **plain HTTP polling with conditional GET** (`If-None-Match`, `If-Modified-Since`). We do **not** ship Server-Sent Events or a WebSocket VTT push in v1. | Story acceptance is mute on transport; architecture §4.5 says "rendered live by the Streaming Service from the DB". | Vidstack, hls.js, AVPlayer, ExoPlayer, and Plex's player all already poll text-track URLs on a `WEBVTT`-track refresh interval (~10 s when the track is active, longer when paused). We get the "live" UX for free with zero player-side code by setting `Cache-Control: no-cache, must-revalidate` and answering `304 Not Modified` quickly when nothing changed. SSE/WS push is a v2 optimization for very low-latency rooms (live lecture mode); see §7. |
| D2 | The HTTP endpoint contract is `GET /v1/videos/{video_id}/subtitles.vtt?live=1[&lang=ar][&seek=0]`, served by the Streaming Service. The same path without `live=1` serves the **finalized** sidecar (Story 4.1) when one exists; with `live=1` it always renders from the DB. | Refines architecture §4.5 (which lists three sources but doesn't formalize the URL grammar). | A single URL with a query flag keeps the player URL stable across the `transcribe → done` boundary. As soon as the sidecar exists the same URL serves a faster (file-system) response, and the live flag stays an explicit opt-in for "I want partial cues now". Removing `live=1` after completion produces an identical-or-better response, so we recommend players default to `?live=1` and let the server decide. |
| D3 | The response is **always a syntactically valid WebVTT 1.0 document**, even for an empty transcript. The minimum body is `WEBVTT\n\n` (15 bytes). HTTP status is **always 200** when the video exists; "not yet started" is expressed by an empty cue list, not by a 4xx. | Story 8.11 edge case "Transcript empty (transcribe job hasn't started) — return a valid empty WebVTT". | Players treat 4xx on a text-track URL as a permanent "track is broken, hide the captions button" state; they will not re-poll. An empty 200 keeps the track alive and the next poll picks up the first cues as soon as they commit. The "no transcript at all" case (D6) reuses the same body — the **video** has captions configured, the captions just have nothing to say yet. |
| D4 | The **ETag** is the SHA-256 (truncated to 16 hex chars) of `(transcript_id, max(seq), MAX(committed_at), is_active, format_version)` from `transcript_segments_v`. It is computed in **one indexed query** that touches no segment text. The **Last-Modified** header is `MAX(committed_at)` from the same query, formatted as RFC 7231 IMF-fixdate. | Refines story-level "ETag/Last-Modified" requirement from the prompt; the story itself does not specify the algorithm. | Hashing only the (id, seq, mtime, flags) tuple means the ETag SELECT is O(1) thanks to the `(transcript_id, seq DESC)` index; we never hash the cue text. The `format_version` byte is bumped any time the VTT serializer changes — that invalidates client caches without a DB migration. The truncation to 16 hex chars (64 bits) is more than enough collision resistance for a per-video resource where the only racers are clients of the same video. |
| D5 | When the transcribe job is `running`, the server emits the cue list **plus** an `X-Maktaba-Transcript-State: running` header and a leading VTT NOTE block: `NOTE state=running progress=…%`. When `done`, the same header reads `done` and there is no NOTE. | Refines architecture §4.5 ("user sees subtitles as soon as the first segments are indexed") to give the client a machine-readable "still streaming" signal without breaking the WebVTT body parsers. | NOTE blocks are part of the VTT spec and ignored by every player we tested (Vidstack, hls.js, AVPlayer, native `<track>`). The header gives JS layers (a "transcribing…" badge) a one-byte read with no body parse. We do **not** invent a custom HTTP status for "partial" because a 200 with `Cache-Control: no-cache, must-revalidate` already handles refresh correctly, and a custom 2xx would be ignored as a 200 by every middlebox. |
| D6 | "Video has no transcript at all" (no `transcripts` row, or all rows have `is_active = false` and none ever produced segments) returns the **same empty WebVTT body** as "transcript not started" (D3), but with `X-Maktaba-Transcript-State: absent` and `Cache-Control: max-age=60`. | Story-level edge case; orthogonal to the in-flight cases. | The empty body keeps the player happy. The longer cache window saves the server from a poll storm on videos that simply don't have STT enabled (e.g. silent home videos). The `absent` header lets a smart client surface "transcript not configured" UX instead of "loading…". 60 s is short enough that enabling transcription on a library is reflected within a minute without waiting for a full TTL. |
| D7 | Segment **ordering** in the response is `ORDER BY seq ASC`, where `seq` is the monotonic per-transcript counter committed by Plan 3.6. The view's index `(transcript_id, start_sec)` is **secondary** — it exists so future "subtitles for time window [a, b]" requests are O(log N), but the live VTT response uses the `seq` order so cue boundaries match exactly what the writer committed (no resorting on overlap). | Story acceptance: index on `(video_id, start_sec)`. Plan 3.6 §0.D2 establishes that segments commit in monotonic `seq` order after reorder buffering. | Sorting by `start_sec` would re-sort segments that overlap on speech boundaries (Whisper occasionally emits `start_n+1 < end_n`); that re-sort can swap cues into a different on-screen order than the speaker said them. Sorting by `seq` is what the writer intended. The window-query index pays its cost in storage but isn't on the live path. |
| D8 | **Language selection** is a passive filter, not a join: `?lang=ar` matches `transcripts.language_code = 'ar'`. The default (no `lang=`) returns the **active** transcript regardless of language; if multiple `is_active = true` rows exist for one video (a misconfiguration outside this story), the lowest-`id` one wins. Translation is **not** in scope (Appendix B / Epic README). | Story does not specify language selection, but the URL grammar (D2) does; `Accept-Language` semantics are inherited from Story 7.7 for ordering only. | One transcript per `(video, language)` is the invariant Story 3.5 owns. The "lowest id wins" tiebreaker is a defence in depth — it's deterministic so two pollers see the same transcript, and the next reaper sweep cleans the duplicate. We do **not** synthesize an empty body when a requested `lang` doesn't exist; we return `404` with `application/problem+json` so the player removes that track from its menu. |
| D9 | The Streaming Service uses **LISTEN `segments.committed`** (Plan 3.6 §0.D5) to invalidate an in-process **ETag cache** keyed by `transcript_id`. The cache stores only `(etag, last_modified, last_seq)` per transcript — never cue bodies. A NOTIFY arriving for transcript T evicts T's entry; the next request recomputes once and serves the cache for ~5 s. | Performance: a polling player + a 4-hour transcribe + a 100-listener room can otherwise hammer the ETag query. | Caching only the (etag, last_modified, last_seq) tuple is ~80 bytes per active transcript; for the largest projected install (1000 concurrent live transcribes) this is ~80 KB. We deliberately do **not** cache cue bodies — those are O(MB) and we'd rather pay the SELECT every refresh than evict-and-rebuild a body cache. The 5-s coalescing window is a belt-and-braces guard against a malformed listener (notify silence) and matches the player's polling cadence. |
| D10 | When the response **would** be larger than `max_inline_vtt_bytes` (default 2 MiB ≈ ~16 hours of continuous speech at typical density), the server returns **chunked transfer encoding** and streams cues row-by-row from a server-side cursor. The ETag/Last-Modified are still based on the cheap header query, computed *before* the body is emitted, so 304s remain fast. | Defensive limit; the largest realistic transcript is a 4-hour lecture at ~60 cues/min ≈ 14 k cues ≈ 700 KB, so this is mostly future-proofing for batch-merged transcripts (Epic 9). | Streaming avoids materializing the full body in memory. The server-side cursor (`DECLARE … FOR SELECT … FROM transcript_segments_v WHERE …`) is a Postgres feature; the SQLite shim materializes (since SQLite has no server cursors) but we cap effective body size with the same `max_inline_vtt_bytes` and return 413 with a hint to fetch the on-disk sidecar (Story 4.1) instead. |

If D1 is rejected (push instead of poll), §2 changes substantially — the
endpoint becomes an SSE stream emitting `event: cue\ndata: {…}` deltas
and the LISTEN handler fans those out. The schema and SQL view are
unchanged either way; the player code is more invasive in the SSE world,
which is why we ship D1 first. The SSE upgrade ships as `?stream=sse` on
the same URL when v2 lands.

---

## 1. Architecture diagram — request flow

```
   ┌────────────────────────────────────────────────────────────────────┐
   │  Player (Vidstack / hls.js / AVPlayer / ExoPlayer)                 │
   │  Polls the text-track URL when the track is active (~10 s)         │
   └────────────────────────────┬───────────────────────────────────────┘
                                │
                                │ GET /v1/videos/{id}/subtitles.vtt?live=1
                                │ If-None-Match: "..."
                                │ If-Modified-Since: …
                                ▼
   ┌────────────────────────────────────────────────────────────────────┐
   │  Streaming Service (Go) — LiveVTTHandler                           │
   │   1. Resolve video_id → active transcript_id (cached in-process)    │
   │   2. Compute (etag, last_modified, last_seq, state) from the       │
   │      header SELECT against transcript_segments_v.                  │
   │   3. If conditional GET matches → 304 Not Modified.                │
   │   4. Else: stream cues from a Postgres server-side cursor          │
   │      ordered by seq; serialize to WebVTT (HTML-escape, bidi-      │
   │      isolate, line-wrap per Plan 4.2).                             │
   │   5. Headers: Content-Type, Cache-Control, ETag, Last-Modified,    │
   │      X-Maktaba-Transcript-State, Vary.                             │
   └─────┬─────────────────────────────────────────────┬────────────────┘
         │ SQL                                         │ LISTEN
         │                                             │ segments.committed
         ▼                                             │
   ┌────────────────────────────────┐  ┌───────────────┴────────────────┐
   │  PostgreSQL                    │  │  ETag cache (in-process)       │
   │  ┌──────────────────────────┐  │  │  Keyed by transcript_id        │
   │  │ transcript_segments_v    │  │  │  Evicted on NOTIFY for that    │
   │  │  (video_id, transcript_  │  │  │  transcript; ~5 s coalescing.  │
   │  │   id, seq, start_sec,    │  │  │  Body is NEVER cached.         │
   │  │   end_sec, text,         │  │  └────────────────────────────────┘
   │  │   speaker, is_active)    │  │
   │  │  index (video_id,        │  │
   │  │   start_sec)             │  │
   │  └──────────────────────────┘  │
   │  ┌──────────────────────────┐  │
   │  │ trg_segments_committed   │  │   on every commit_segment(...)
   │  │  pg_notify(              │  │   the trigger fires here →
   │  │   'segments.committed',  │──┼──→ Streaming process
   │  │    {transcript_id, …})   │  │
   │  └──────────────────────────┘  │
   └────────────────────────────────┘
```

The producer side (`commit_segment` PL/pgSQL function and the
`trg_segments_committed` trigger) is owned by Plan 3.6. This story owns
**only** the read view, the ETag/cache mechanism, and the HTTP-level
contract.

---

## 2. Detailed implementation

### 2.1 The SQL view — `transcript_segments_v`

The view is the read-side surface. It hides the `is_active = true`
filter, exposes only the columns subtitle generation needs, and has its
own composite index for window queries.

```sql
-- shared/db/migrations/0019_transcript_segments_view.sql
-- Owns: transcript_segments_v read view + (video_id, start_sec) index.
-- Idempotent on re-run: CREATE OR REPLACE VIEW + CREATE INDEX IF NOT EXISTS.
-- Dependencies: 0007 transcripts/transcript_segments,
--               0011 commit_segment fn (Plan 3.6).

BEGIN;

CREATE OR REPLACE VIEW transcript_segments_v AS
SELECT
    t.video_id            AS video_id,
    t.id                  AS transcript_id,
    t.language_code       AS language_code,
    s.seq                 AS seq,
    s.start_sec           AS start_sec,
    s.end_sec             AS end_sec,
    s.text                AS text,
    s.speaker             AS speaker,
    s.confidence          AS confidence,
    s.committed_at        AS committed_at,
    t.is_active           AS is_active,
    t.state               AS transcript_state    -- 'running' | 'done' | 'paused' | 'failed'
FROM transcripts t
JOIN transcript_segments s ON s.transcript_id = t.id
WHERE t.is_active = true;

-- Window-query support index (story-level acceptance criterion).
-- Note: indexes on a view per se are not supported in Postgres; this is the
-- functional equivalent on the underlying table.
CREATE INDEX IF NOT EXISTS transcript_segments_video_start_idx
    ON transcript_segments (transcript_id, start_sec);

-- Plus the `seq` covering index (for live VTT ordering, D7).
CREATE INDEX IF NOT EXISTS transcript_segments_seq_idx
    ON transcript_segments (transcript_id, seq);

COMMIT;
```

`video_id` and `language_code` come from the parent `transcripts` row,
not from `transcript_segments` directly — this is what the story's
column list `(video_id, transcript_id, seq, start_sec, end_sec, text,
speaker, is_active)` implies. The column `transcript_state` is added
beyond the story's explicit list because the live endpoint needs it
(D5); future readers that don't care can ignore it.

SQLite has no `CREATE OR REPLACE VIEW`; the SQLite shim in
`shared/db/migrations/sqlite/0019_*.sql` does `DROP VIEW IF EXISTS …;
CREATE VIEW …;`.

### 2.2 The HTTP contract

The Streaming Service exposes:

```
GET /v1/videos/{video_id}/subtitles.vtt?live=1[&lang=<bcp47>][&seek=<seconds>]
```

Path parameters and query string:

| Parameter      | Type        | Required | Default | Notes |
|----------------|-------------|----------|---------|-------|
| `video_id`     | UUID        | yes      | —       | Path. 404 if unknown. |
| `live`         | `0`/`1`     | no       | `0`     | `1` ⇒ render from `transcript_segments_v`; `0` ⇒ serve sidecar VTT (Story 4.1) when present, else fall through to live. |
| `lang`         | BCP-47 code | no       | active  | Filters `language_code`. 404 if no active transcript matches. |
| `seek`         | float ≥ 0   | no       | 0.0     | Optional. When present, **drops** cues whose `end_sec ≤ seek`. The body still says `WEBVTT` and the cue list is shorter. Saves bandwidth on a long episode the user joined late. |

Required request headers:

| Header               | Used as |
|----------------------|---------|
| `If-None-Match`      | Conditional GET → 304. |
| `If-Modified-Since`  | Conditional GET → 304 (lower precedence than ETag). |
| `Accept-Language`    | **Ordering hint only** in this story; we honor it for **`lang=` selection** when no explicit `lang=` is supplied (the *first* tag that matches an existing active transcript wins). |
| `Authorization`      | Required by the Streaming Service auth middleware (Epic 10). Out of scope here. |

Response status codes:

| Status | Meaning |
|--------|---------|
| 200 OK | Success — body is `text/vtt` (always valid WebVTT, possibly with zero cues). |
| 304 Not Modified | Conditional GET hit. Body is empty per RFC 7232. |
| 400 Bad Request | Malformed `lang` (not a BCP-47 string), `seek` not a non-negative number, etc. `application/problem+json`. |
| 401/403 | Auth — Epic 10. |
| 404 Not Found | `video_id` does not exist OR `lang=X` was specified and no active transcript with that language exists. `application/problem+json`. **Note**: video exists but no transcript at all is **200**, not 404 (D6). |
| 413 Payload Too Large | Computed body exceeds `max_inline_vtt_bytes` AND chunked streaming is disabled (D10 fallback). `application/problem+json` with `hint: fetch the sidecar at /v1/videos/{id}/subtitles.vtt` (without `?live=1`). |
| 500 Internal Server Error | DB unreachable, query error. The cache TTL on a 500 is `Cache-Control: no-store` so the next poll retries immediately. |
| 503 Service Unavailable | Listener cache is in a known-bad state (rare); `Retry-After: 5`. |

Required response headers (200):

```
Content-Type:                 text/vtt; charset=utf-8
Cache-Control:                no-cache, must-revalidate
ETag:                         W/"a3f9c2e801b4d6d7"
Last-Modified:                Wed, 22 Oct 2025 07:28:00 GMT
Vary:                         Accept-Language
X-Maktaba-Transcript-State:   running          # or done | paused | failed | absent
X-Maktaba-Transcript-Id:      <uuid>           # echo the resolved transcript_id
X-Maktaba-Last-Seq:           1247             # last committed seq present in this body
X-Maktaba-Format-Version:     1                # bumped on serializer changes (D4)
```

The `ETag` is **weak** (`W/"…"`). We use weak validators because the
NOTE-state preamble (D5) makes byte-identical responses impossible for
the same cue set across `running → done`. Players and CDNs treat weak
ETags identically to strong ones for `If-None-Match` purposes.

### 2.3 The VTT serializer

The serializer is shared with Plan 4.1 (sidecar generation) and Plan 4.2
(formatting/wrap). The live path adds:

1. **NOTE preamble** when `transcript_state ∈ {running, paused}`:
   ```
   WEBVTT

   NOTE state=running progress=42.7%

   ```
   `progress` is computed by the same SELECT that builds the ETag:
   `100.0 * processed_seconds / NULLIF(total_duration_seconds, 0)`,
   floored to 1 decimal. Omitted when `total_duration_seconds` is NULL.
2. **Cue identifier**: each cue's optional ID line is `seq-{N}` where
   `N` is the segment's `seq`. This gives deterministic cue identities
   across refreshes — players that diff cues (Vidstack does) skip
   re-rendering for unchanged cues.
3. **Speaker tag**: when `speaker IS NOT NULL`, the cue body is
   prefixed with `<v {speaker}>` per architecture §4 ("VTT cues
   include speaker tags … when diarization ran").
4. **HTML escaping**: `<`, `>`, `&` in `text` are replaced with
   `&lt;`, `&gt;`, `&amp;` BEFORE the speaker tag wrap. (Story 8.11
   AC-1; we mirror it here so file-and-live emit identical bytes.)
5. **Bidi isolation and line wrap**: per Plan 4.2.

Pseudocode for the serializer (Go, shipped in Streaming Service —
the algorithm is the same in any language so you can read it as a
spec):

```go
// streaming/internal/subtitles/live_vtt.go (excerpt)

type CueRow struct {
    Seq      int32
    Start    float64
    End      float64
    Text     string
    Speaker  *string
}

func serializeLiveVTT(w io.Writer, hdr HeaderInfo, cues iter.Seq[CueRow]) error {
    bw := bufio.NewWriter(w)

    if _, err := bw.WriteString("WEBVTT\n\n"); err != nil {
        return err
    }

    if hdr.State == "running" || hdr.State == "paused" {
        progress := ""
        if hdr.TotalDurationSec > 0 {
            pct := 100.0 * hdr.ProcessedSec / hdr.TotalDurationSec
            progress = fmt.Sprintf(" progress=%.1f%%", pct)
        }
        fmt.Fprintf(bw, "NOTE state=%s%s\n\n", hdr.State, progress)
    }

    for cue, _ := range cues {
        // 1. cue ID line
        fmt.Fprintf(bw, "seq-%d\n", cue.Seq)
        // 2. timestamp line
        fmt.Fprintf(bw, "%s --> %s\n", fmtTS(cue.Start), fmtTS(cue.End))
        // 3. cue text (escape, optional speaker tag, wrap)
        body := htmlEscape(cue.Text)
        if cue.Speaker != nil {
            body = fmt.Sprintf("<v %s>%s", htmlEscape(*cue.Speaker), body)
        }
        body = bidiIsolate(body)
        body = lineWrap(body, maxCharsPerLine)
        bw.WriteString(body)
        bw.WriteString("\n\n")
    }
    return bw.Flush()
}

// fmtTS formats seconds as "HH:MM:SS.mmm" (WebVTT requirement).
func fmtTS(sec float64) string {
    h := int(sec) / 3600
    m := (int(sec) % 3600) / 60
    s := int(sec) % 60
    ms := int((sec - float64(int(sec))) * 1000)
    return fmt.Sprintf("%02d:%02d:%02d.%03d", h, m, s, ms)
}
```

### 2.4 The header SELECT and ETag computation

Cheap, indexed, executed for every request (including 304-bound ones).

```sql
-- header_select_for_video.sql
SELECT
    t.id                                       AS transcript_id,
    t.state                                    AS transcript_state,
    t.language_code                            AS language_code,
    COALESCE(MAX(s.seq), 0)                    AS max_seq,
    COALESCE(MAX(s.committed_at), t.created_at) AS last_committed_at,
    COALESCE(j.processed_seconds, 0)           AS processed_seconds,
    j.total_duration_seconds                   AS total_duration_seconds
FROM transcripts t
LEFT JOIN transcript_segments s ON s.transcript_id = t.id
LEFT JOIN processing_jobs j ON j.id = t.job_id
WHERE t.video_id = $1
  AND t.is_active = true
  AND ($2::text IS NULL OR t.language_code = $2)
GROUP BY t.id, t.state, t.language_code, j.processed_seconds, j.total_duration_seconds
ORDER BY t.id ASC
LIMIT 1;
```

Plan: index lookup on `transcripts (video_id, is_active)` + index lookup
on `transcript_segments (transcript_id, seq DESC)` for the `MAX(seq)`.
Both exist (3.5 + 3.6). Expected wall time: <1 ms at p99.

ETag computation in Go:

```go
func computeETag(h HeaderInfo) string {
    sum := sha256.New()
    fmt.Fprintf(sum, "v%d|%s|%d|%d|%t",
        formatVersion,                       // bumped on serializer changes
        h.TranscriptID,
        h.MaxSeq,
        h.LastCommittedAt.UnixMilli(),
        h.IsActive,                          // future-proof; today always true
    )
    raw := sum.Sum(nil)
    return fmt.Sprintf(`W/"%x"`, raw[:8])    // 16 hex chars = 64 bits
}

func computeLastModified(h HeaderInfo) string {
    return h.LastCommittedAt.UTC().Format(http.TimeFormat) // RFC 7231 IMF-fixdate
}
```

### 2.5 The handler skeleton

```go
// streaming/internal/handlers/live_vtt.go (excerpt)

func (s *Server) HandleLiveVTT(w http.ResponseWriter, r *http.Request) {
    videoID, err := parseUUID(chi.URLParam(r, "video_id"))
    if err != nil {
        problem(w, 400, "invalid-video-id", err.Error())
        return
    }
    lang := r.URL.Query().Get("lang")
    if lang != "" && !isBCP47(lang) {
        problem(w, 400, "invalid-lang", "lang must be a BCP-47 tag")
        return
    }
    seek, err := parseSeek(r.URL.Query().Get("seek"))
    if err != nil {
        problem(w, 400, "invalid-seek", err.Error())
        return
    }

    ctx := r.Context()

    // (1) Header SELECT — cheap, always runs (cache may shortcut).
    hdr, ok, err := s.subs.LiveHeader(ctx, videoID, lang)
    if err != nil {
        problem(w, 500, "db-unavailable", err.Error())
        return
    }
    if !ok {
        // No active transcript matching the lang filter.
        if lang != "" {
            problem(w, 404, "no-transcript-for-lang",
                fmt.Sprintf("no active transcript with language=%s", lang))
            return
        }
        // No transcript at all → empty body, state=absent (D6).
        writeAbsentVTT(w, videoID)
        return
    }

    etag := computeETag(hdr)
    lm := computeLastModified(hdr)

    // (2) Conditional GET.
    if matchETag(r.Header.Get("If-None-Match"), etag) ||
        matchModifiedSince(r.Header.Get("If-Modified-Since"), hdr.LastCommittedAt) {
        writeNotModifiedHeaders(w, etag, lm, hdr)
        w.WriteHeader(http.StatusNotModified)
        return
    }

    // (3) Body.
    writeLiveHeaders(w, etag, lm, hdr)
    w.WriteHeader(http.StatusOK)

    cues, closeFn, err := s.subs.LiveCues(ctx, hdr.TranscriptID, seek)
    if err != nil {
        // Headers are already sent. Best we can do is end the response.
        log.Errorw("live_vtt_cursor_open_failed", "err", err)
        return
    }
    defer closeFn()

    if err := serializeLiveVTT(w, hdr, cues); err != nil {
        log.Warnw("live_vtt_write_failed", "err", err) // client likely disconnected
    }
}
```

### 2.6 LISTEN/NOTIFY hookup (D9)

```go
// streaming/internal/subtitles/listener.go (excerpt)

type ETagCache struct {
    mu      sync.RWMutex
    entries map[uuid.UUID]cacheEntry
    ttl     time.Duration // 5 * time.Second
}

type cacheEntry struct {
    Hdr      HeaderInfo
    ExpireAt time.Time
}

func (c *ETagCache) Get(id uuid.UUID) (HeaderInfo, bool) { ... }
func (c *ETagCache) Put(id uuid.UUID, h HeaderInfo)      { ... }
func (c *ETagCache) Evict(id uuid.UUID)                  { ... }

// Run in a long-lived goroutine at server start.
func (l *NotifyListener) Run(ctx context.Context) error {
    conn, err := pgx.Connect(ctx, l.dsn)
    if err != nil { return err }
    defer conn.Close(ctx)

    if _, err := conn.Exec(ctx, "LISTEN \"segments.committed\""); err != nil {
        return err
    }
    for {
        n, err := conn.WaitForNotification(ctx)
        if err != nil {
            if errors.Is(err, context.Canceled) { return nil }
            log.Warnw("listen_disconnected_reconnecting", "err", err)
            time.Sleep(time.Second)
            return l.Run(ctx) // simple reconnect loop
        }
        var p struct {
            TranscriptID uuid.UUID `json:"transcript_id"`
            EndSec       float64   `json:"last_segment_end_sec"`
            Seq          int32     `json:"seq"`
        }
        if err := json.Unmarshal([]byte(n.Payload), &p); err != nil {
            log.Warnw("notify_payload_decode_failed", "payload", n.Payload)
            continue
        }
        l.cache.Evict(p.TranscriptID)
    }
}
```

The listener's only side-effect is cache eviction. A missed NOTIFY (DB
restart, network blip) only delays freshness by the cache TTL (5 s) and
is fully self-healing on reconnect.

### 2.7 Sample request/response pairs

**A) Empty transcript (job hasn't claimed yet, but transcript row
exists).** State `running` with zero segments.

```
GET /v1/videos/9b3a…/subtitles.vtt?live=1
HTTP/1.1
If-None-Match: (none)

──────────
HTTP/1.1 200 OK
Content-Type: text/vtt; charset=utf-8
Cache-Control: no-cache, must-revalidate
ETag: W/"0000000000000000"
Last-Modified: Wed, 22 Oct 2025 07:00:00 GMT
X-Maktaba-Transcript-State: running
X-Maktaba-Transcript-Id: 9b3a…
X-Maktaba-Last-Seq: 0
X-Maktaba-Format-Version: 1

WEBVTT

NOTE state=running

```

(Note the `progress` field is absent because `total_duration_seconds`
is unknown until `probe` finishes.)

**B) Partial transcript.** Three cues committed; transcribe still
running.

```
HTTP/1.1 200 OK
Content-Type: text/vtt; charset=utf-8
Cache-Control: no-cache, must-revalidate
ETag: W/"a3f9c2e801b4d6d7"
Last-Modified: Wed, 22 Oct 2025 07:28:00 GMT
X-Maktaba-Transcript-State: running
X-Maktaba-Last-Seq: 3

WEBVTT

NOTE state=running progress=2.3%

seq-1
00:00:00.000 --> 00:00:04.500
<v Speaker 1>السلام عليكم. اليوم سنتحدث عن…

seq-2
00:00:04.500 --> 00:00:08.120
<v Speaker 1>…موضوع مهم جدًا.

seq-3
00:00:08.120 --> 00:00:12.000
<v Speaker 2>تفضل، أكمل من فضلك.

```

**C) Conditional GET, no change.** Player polls 10 s later; nothing
new.

```
GET /v1/videos/9b3a…/subtitles.vtt?live=1
If-None-Match: W/"a3f9c2e801b4d6d7"

──────────
HTTP/1.1 304 Not Modified
ETag: W/"a3f9c2e801b4d6d7"
Last-Modified: Wed, 22 Oct 2025 07:28:00 GMT
X-Maktaba-Transcript-State: running
X-Maktaba-Last-Seq: 3
```

(Body is empty per RFC 7232. The Streaming Service still sends
state/seq headers so a JS UI can update its "transcribing…" badge
without a body fetch.)

**D) Conditional GET, change.** A new segment landed; ETag differs.

```
GET /v1/videos/9b3a…/subtitles.vtt?live=1
If-None-Match: W/"a3f9c2e801b4d6d7"

──────────
HTTP/1.1 200 OK
ETag: W/"d2c081f4117ae3a0"
Last-Modified: Wed, 22 Oct 2025 07:28:14 GMT
X-Maktaba-Transcript-State: running
X-Maktaba-Last-Seq: 4

WEBVTT

NOTE state=running progress=2.5%

seq-1
…
seq-4
00:00:12.000 --> 00:00:16.300
<v Speaker 2>وأود أن أضيف…
```

(Yes, the entire body is re-sent. Players are designed for this; we
optimize **request count** with conditional GET, not **body size**.)

**E) Done.** Same URL, transcript completed.

```
HTTP/1.1 200 OK
ETag: W/"f0e8993ba721c0b1"
Last-Modified: Wed, 22 Oct 2025 09:42:11 GMT
X-Maktaba-Transcript-State: done
X-Maktaba-Last-Seq: 4197

WEBVTT

seq-1
…
```

(No NOTE block; state header is `done`. The same URL **without**
`?live=1` would now serve the on-disk sidecar from Plan 4.1, which
has identical bytes for the cue list and is faster to serve.)

**F) No transcript at all.** Video exists, captions never enabled.

```
HTTP/1.1 200 OK
Content-Type: text/vtt; charset=utf-8
Cache-Control: max-age=60
X-Maktaba-Transcript-State: absent
X-Maktaba-Last-Seq: 0

WEBVTT

NOTE state=absent

```

**G) Bad lang filter.**

```
GET /v1/videos/9b3a…/subtitles.vtt?live=1&lang=fr
──────────
HTTP/1.1 404 Not Found
Content-Type: application/problem+json

{
  "type":   "https://maktaba.dev/problems/no-transcript-for-lang",
  "title":  "no transcript for the requested language",
  "status": 404,
  "detail": "no active transcript with language=fr",
  "video_id":  "9b3a…",
  "available": ["ar"]
}
```

---

## 3. File-by-file scaffolding

The story owns the SQL view + the contract; the actual Go endpoint code
ships in Epic 8. List both because the contract is incomplete without
the producer-facing changes.

| Order | File | Owner story | Symbols introduced | Tests gating |
|-------|------|-------------|--------------------|--------------|
| 1 | `shared/db/migrations/0019_transcript_segments_view.sql` | **4.5 (this)** | view `transcript_segments_v`, indexes `transcript_segments_video_start_idx`, `transcript_segments_seq_idx` | `test_view_excludes_superseded_transcripts`, `test_view_index_supports_window_query`, `test_migration_idempotent` |
| 2 | `shared/db/migrations/sqlite/0019_transcript_segments_view.sql` | **4.5 (this)** | same view, no `OR REPLACE` | sqlite test fixture loads |
| 3 | `specs/contracts/live-vtt.openapi.yaml` | **4.5 (this)** | OpenAPI 3.1 stanza for `GET /v1/videos/{id}/subtitles.vtt` (paths, headers, error schemas) | contract diff against the Go handler in CI (Epic 8 test) |
| 4 | `specs/contracts/vtt-format.md` | **4.5 (this)** | normative spec of the VTT body shape (NOTE preamble, cue ID format, speaker tag, escaping, bidi isolation) | reference fixtures in `specs/fixtures/live-vtt/` |
| 5 | `specs/fixtures/live-vtt/empty.vtt`, `partial.vtt`, `done.vtt`, `absent.vtt` | **4.5 (this)** | golden fixtures matching §2.7 | parsed by `pyvtt` and W3C WebVTT validator in CI |
| 6 | `streaming/internal/subtitles/live_vtt.go` | **8.11** (consumer) | `serializeLiveVTT`, `fmtTS`, `htmlEscape`, `bidiIsolate`, `lineWrap` (re-exports Plan 4.2) | `TestSerializeLiveVTT_Empty`, `TestSerializeLiveVTT_PartialMatchesFixture`, `TestSerializeLiveVTT_SpeakerTag` |
| 7 | `streaming/internal/subtitles/header_query.go` | **8.11** (consumer) | `LiveHeader`, `HeaderInfo` | `TestLiveHeader_NoTranscript`, `TestLiveHeader_LangFilterMisses` |
| 8 | `streaming/internal/subtitles/etag.go` | **8.11** (consumer) | `computeETag`, `computeLastModified`, `matchETag`, `matchModifiedSince` | `TestETagStableAcrossUnchangedReads`, `TestETagChangesOnNewSegment`, `TestIfModifiedSinceLowerPrecedence` |
| 9 | `streaming/internal/subtitles/cache.go` | **8.11** (consumer) | `ETagCache`, `cacheEntry`, `NotifyListener.Run` | `TestNotifyEvictsCache`, `TestCacheTTLCoalesces` |
| 10 | `streaming/internal/handlers/live_vtt.go` | **8.11** (consumer) | `Server.HandleLiveVTT` | `TestHandleLiveVTT_HappyPath`, `TestHandleLiveVTT_304`, `TestHandleLiveVTT_404Lang`, `TestHandleLiveVTT_AbsentVideoExists` |

The **migration is the only piece this story ships into production
code**. Items 3–5 are spec artifacts; items 6–10 are listed for
completeness and will be implemented as part of Story 8.11. CI for this
story passes when items 1–5 land and the corresponding tests are green.

---

## 4. Test cases

All paths assume the existing `pytest`/`go test` harnesses. SQL tests
use the cross-backend `db_fixture` (Postgres + SQLite via `--db=` flag)
established in Plan 3.6 §4.

### 4.1 `test_view_excludes_superseded_transcripts` (story-named)

```python
async def test_view_excludes_superseded_transcripts(db, video_factory):
    """Two transcripts for one video; only the active one's segments appear."""
    video = await video_factory.create()

    t1 = await db.fetchval(
        "INSERT INTO transcripts (video_id, language_code, is_active, state) "
        "VALUES ($1, 'ar', false, 'done') RETURNING id", video.id)
    t2 = await db.fetchval(
        "INSERT INTO transcripts (video_id, language_code, is_active, state) "
        "VALUES ($1, 'ar', true, 'running') RETURNING id", video.id)

    await db.execute("""
        INSERT INTO transcript_segments (transcript_id, seq, start_sec, end_sec, text)
        VALUES ($1, 1, 0, 5, 'old'), ($2, 1, 0, 5, 'new')
    """, t1, t2)

    rows = await db.fetch(
        "SELECT transcript_id, text FROM transcript_segments_v WHERE video_id = $1",
        video.id)
    assert len(rows) == 1
    assert rows[0]["transcript_id"] == t2
    assert rows[0]["text"] == "new"
```

### 4.2 `test_view_index_supports_window_query` (story-named)

```python
async def test_view_window_query_uses_index(db, video_factory):
    """EXPLAIN of a (video_id, start_sec BETWEEN x AND y) query uses the index."""
    video = await video_factory.create()
    transcript = await db.fetchval(
        "INSERT INTO transcripts (video_id, language_code, is_active) "
        "VALUES ($1, 'ar', true) RETURNING id", video.id)
    # Insert enough rows that the planner prefers index over seq scan.
    await db.executemany(
        "INSERT INTO transcript_segments (transcript_id, seq, start_sec, end_sec, text) "
        "VALUES ($1, $2, $3, $4, 'x')",
        [(transcript, i, i * 5.0, (i + 1) * 5.0) for i in range(2000)])

    plan = await db.fetch("""
        EXPLAIN (FORMAT JSON)
        SELECT * FROM transcript_segments_v
         WHERE video_id = $1
           AND start_sec BETWEEN 100 AND 200
    """, video.id)
    plan_json = plan[0][0][0]["Plan"]
    assert "Index Scan" in _walk_plan(plan_json), plan_json
    assert "transcript_segments_video_start_idx" in _walk_plan(plan_json)
```

### 4.3 `test_view_includes_active_running_transcript_with_zero_segments`

```python
async def test_view_active_transcript_with_no_segments_yields_no_rows(db, video_factory):
    """A 'running' transcript with no segments yet is *invisible* in the view
    (the view JOINs on transcript_segments). The header SELECT must therefore
    LEFT JOIN — verified in §4.5 below."""
    video = await video_factory.create()
    await db.execute(
        "INSERT INTO transcripts (video_id, language_code, is_active, state) "
        "VALUES ($1, 'ar', true, 'running')", video.id)
    rows = await db.fetch(
        "SELECT * FROM transcript_segments_v WHERE video_id = $1", video.id)
    assert rows == []
```

### 4.4 `test_empty_transcript_returns_valid_empty_vtt` (contract-level)

Lives in the Streaming Service test suite (Story 8.11) but is asserted
by reading the fixture this story ships:

```go
// streaming/internal/subtitles/live_vtt_test.go
func TestSerializeLiveVTT_EmptyMatchesFixture(t *testing.T) {
    var buf bytes.Buffer
    hdr := HeaderInfo{State: "running"}
    err := serializeLiveVTT(&buf, hdr, slices.Values([]CueRow(nil)))
    require.NoError(t, err)

    expected, _ := os.ReadFile("../../../specs/fixtures/live-vtt/empty.vtt")
    require.Equal(t, string(expected), buf.String())
}
```

The fixture (`empty.vtt`) is exactly:

```
WEBVTT

NOTE state=running

```

### 4.5 `test_partial_transcript_returns_committed_cues`

```go
func TestHandleLiveVTT_PartialReturnsCommittedCues(t *testing.T) {
    h := newTestServer(t)
    video := h.fixtures.NewVideoWithRunningTranscript(2 /* segments */)

    rec := httptest.NewRecorder()
    req := httptest.NewRequest("GET",
        "/v1/videos/"+video.ID.String()+"/subtitles.vtt?live=1", nil)
    h.HandleLiveVTT(rec, req)

    require.Equal(t, http.StatusOK, rec.Code)
    require.Equal(t, "text/vtt; charset=utf-8", rec.Header().Get("Content-Type"))
    require.Equal(t, "running", rec.Header().Get("X-Maktaba-Transcript-State"))
    require.Equal(t, "2", rec.Header().Get("X-Maktaba-Last-Seq"))

    body := rec.Body.String()
    assert.True(t, strings.HasPrefix(body, "WEBVTT\n\nNOTE state=running"))
    assert.Contains(t, body, "seq-1\n")
    assert.Contains(t, body, "seq-2\n")
    assert.Contains(t, body, " --> ")
}
```

### 4.6 `test_etag_changes_when_segments_commit`

```go
func TestETag_ChangesOnNewSegment(t *testing.T) {
    h := newTestServer(t)
    video := h.fixtures.NewVideoWithRunningTranscript(1)

    // First request → ETag1.
    rec1 := httptest.NewRecorder()
    h.HandleLiveVTT(rec1, mkReq(video.ID, ""))
    etag1 := rec1.Header().Get("ETag")
    require.NotEmpty(t, etag1)

    // Conditional GET with same ETag → 304.
    rec2 := httptest.NewRecorder()
    h.HandleLiveVTT(rec2, mkReqWithINM(video.ID, etag1))
    require.Equal(t, http.StatusNotModified, rec2.Code)

    // Commit a new segment via the test fixtures.
    h.fixtures.AppendSegment(video.ID, /*start*/ 5, /*end*/ 10, "more text")

    // Conditional GET → ETag changed → 200.
    rec3 := httptest.NewRecorder()
    h.HandleLiveVTT(rec3, mkReqWithINM(video.ID, etag1))
    require.Equal(t, http.StatusOK, rec3.Code)
    etag3 := rec3.Header().Get("ETag")
    require.NotEqual(t, etag1, etag3)
    require.Equal(t, "2", rec3.Header().Get("X-Maktaba-Last-Seq"))
}
```

### 4.7 `test_lang_filter`

```go
func TestHandleLiveVTT_LangFilter(t *testing.T) {
    h := newTestServer(t)
    // Two transcripts for one video. Both is_active = true is invalid per
    // Story 3.5; here we have one active 'ar' and one inactive 'en'.
    video := h.fixtures.NewVideoWithTranscripts(map[string]string{
        "ar": "active",
        "en": "inactive",
    })

    // lang=ar → 200 with Arabic content.
    rec := httptest.NewRecorder()
    h.HandleLiveVTT(rec, mkReq(video.ID, "?live=1&lang=ar"))
    require.Equal(t, http.StatusOK, rec.Code)

    // lang=en → 404 (the en transcript exists but is not active).
    rec = httptest.NewRecorder()
    h.HandleLiveVTT(rec, mkReq(video.ID, "?live=1&lang=en"))
    require.Equal(t, http.StatusNotFound, rec.Code)
    require.Equal(t, "application/problem+json",
        rec.Header().Get("Content-Type"))
    var body map[string]any
    _ = json.Unmarshal(rec.Body.Bytes(), &body)
    require.Equal(t, "no transcript for the requested language", body["title"])
    require.Equal(t, []any{"ar"}, body["available"])
}
```

### 4.8 `test_seek_drops_earlier_cues`

```go
func TestHandleLiveVTT_SeekDropsEarlierCues(t *testing.T) {
    h := newTestServer(t)
    video := h.fixtures.NewVideoWithRunningTranscript(5) // segs at 0..50s
    rec := httptest.NewRecorder()
    h.HandleLiveVTT(rec, mkReq(video.ID, "?live=1&seek=20"))

    body := rec.Body.String()
    assert.NotContains(t, body, "seq-1") // ends at 10
    assert.NotContains(t, body, "seq-2") // ends at 20  (end_sec <= seek → drop)
    assert.Contains(t, body, "seq-3")    // 20..30
    assert.Contains(t, body, "seq-5")
    require.Equal(t, "5", rec.Header().Get("X-Maktaba-Last-Seq"))
}
```

### 4.9 `test_absent_video_exists_returns_empty_vtt`

```go
func TestHandleLiveVTT_VideoExistsNoTranscript(t *testing.T) {
    h := newTestServer(t)
    video := h.fixtures.NewVideo() // no transcripts row at all
    rec := httptest.NewRecorder()
    h.HandleLiveVTT(rec, mkReq(video.ID, "?live=1"))

    require.Equal(t, http.StatusOK, rec.Code) // NOT 404
    require.Equal(t, "absent", rec.Header().Get("X-Maktaba-Transcript-State"))
    require.Equal(t, "max-age=60", rec.Header().Get("Cache-Control"))
    require.Contains(t, rec.Body.String(), "NOTE state=absent")
}
```

### 4.10 `test_video_does_not_exist_returns_404`

```go
func TestHandleLiveVTT_VideoMissing(t *testing.T) {
    h := newTestServer(t)
    rec := httptest.NewRecorder()
    h.HandleLiveVTT(rec, mkReq(uuid.New(), "?live=1"))
    require.Equal(t, http.StatusNotFound, rec.Code)
    require.Equal(t, "application/problem+json",
        rec.Header().Get("Content-Type"))
}
```

### 4.11 `test_notify_evicts_cache`

```go
func TestNotifyListener_EvictsCacheOnSegmentCommit(t *testing.T) {
    h := newTestServer(t)
    video := h.fixtures.NewVideoWithRunningTranscript(0)

    // Prime the cache.
    rec := httptest.NewRecorder()
    h.HandleLiveVTT(rec, mkReq(video.ID, "?live=1"))
    transcriptID, _ := uuid.Parse(rec.Header().Get("X-Maktaba-Transcript-Id"))
    _, hit := h.cache.Get(transcriptID)
    require.True(t, hit, "first request should populate cache")

    // Commit a segment (the trigger fires NOTIFY).
    h.fixtures.AppendSegment(video.ID, 0, 5, "x")

    require.Eventually(t, func() bool {
        _, hit := h.cache.Get(transcriptID)
        return !hit
    }, 2*time.Second, 10*time.Millisecond,
        "cache should be evicted by NOTIFY within 2s")
}
```

### 4.12 `test_html_escaping_in_cue_text`

```go
func TestSerializeLiveVTT_EscapesHTML(t *testing.T) {
    var buf bytes.Buffer
    cues := []CueRow{
        {Seq: 1, Start: 0, End: 5, Text: "<script>alert(1)</script>"},
    }
    err := serializeLiveVTT(&buf, HeaderInfo{State: "done"}, slices.Values(cues))
    require.NoError(t, err)

    body := buf.String()
    assert.NotContains(t, body, "<script>")
    assert.Contains(t, body, "&lt;script&gt;alert(1)&lt;/script&gt;")
}
```

### 4.13 `test_speaker_tag_present_when_diarization_ran`

```go
func TestSerializeLiveVTT_SpeakerTag(t *testing.T) {
    var buf bytes.Buffer
    speaker := "Speaker 1"
    cues := []CueRow{
        {Seq: 1, Start: 0, End: 5, Text: "hi", Speaker: &speaker},
    }
    err := serializeLiveVTT(&buf, HeaderInfo{State: "done"}, slices.Values(cues))
    require.NoError(t, err)

    assert.Contains(t, buf.String(), "<v Speaker 1>hi")
}
```

### 4.14 `test_paused_transcript_state_in_note_and_header`

```go
func TestHandleLiveVTT_PausedTranscriptShowsState(t *testing.T) {
    h := newTestServer(t)
    video := h.fixtures.NewVideoWithRunningTranscript(2)
    h.fixtures.PauseTranscript(video.ID)

    rec := httptest.NewRecorder()
    h.HandleLiveVTT(rec, mkReq(video.ID, "?live=1"))

    require.Equal(t, http.StatusOK, rec.Code)
    require.Equal(t, "paused", rec.Header().Get("X-Maktaba-Transcript-State"))
    require.Contains(t, rec.Body.String(), "NOTE state=paused")
}
```

### 4.15 `test_failed_transcript_serves_what_was_committed`

```go
func TestHandleLiveVTT_FailedTranscriptStillServesCues(t *testing.T) {
    h := newTestServer(t)
    video := h.fixtures.NewVideoWithRunningTranscript(3)
    h.fixtures.MarkTranscriptFailed(video.ID, "OOM")

    rec := httptest.NewRecorder()
    h.HandleLiveVTT(rec, mkReq(video.ID, "?live=1"))

    require.Equal(t, http.StatusOK, rec.Code)
    require.Equal(t, "failed", rec.Header().Get("X-Maktaba-Transcript-State"))
    body := rec.Body.String()
    assert.Contains(t, body, "NOTE state=failed")
    // The 3 committed cues are still served — failure does not erase
    // already-durable progress.
    assert.Contains(t, body, "seq-3")
}
```

### 4.16 `test_done_transcript_omits_note_block`

```go
func TestHandleLiveVTT_DoneOmitsNote(t *testing.T) {
    h := newTestServer(t)
    video := h.fixtures.NewVideoWithCompletedTranscript(2)
    rec := httptest.NewRecorder()
    h.HandleLiveVTT(rec, mkReq(video.ID, "?live=1"))

    body := rec.Body.String()
    assert.True(t, strings.HasPrefix(body, "WEBVTT\n\nseq-1\n"),
        "done transcripts skip the NOTE preamble")
    require.Equal(t, "done", rec.Header().Get("X-Maktaba-Transcript-State"))
}
```

### 4.17 `test_w3c_validator_passes_on_all_fixtures`

A meta-test that runs the W3C WebVTT validator (we ship a vendored
`vtt-validator` binary in `tools/`) against every fixture in
`specs/fixtures/live-vtt/` plus a generated 1000-cue stress fixture.

```python
@pytest.mark.parametrize("fixture", glob("specs/fixtures/live-vtt/*.vtt"))
def test_fixtures_pass_w3c_webvtt_validator(fixture):
    result = subprocess.run(
        ["./tools/vtt-validator", fixture],
        capture_output=True, text=True, check=False)
    assert result.returncode == 0, f"validator failed: {result.stderr}"
```

### 4.18 `test_migration_idempotent`

```python
async def test_view_migration_is_idempotent(db_factory):
    db = await db_factory.fresh()
    await apply_migration(db, "0019_transcript_segments_view.sql")
    # Re-applying must not error.
    await apply_migration(db, "0019_transcript_segments_view.sql")

    # The view exists.
    row = await db.fetchrow(
        "SELECT count(*) AS c FROM information_schema.views "
        "WHERE table_name = 'transcript_segments_v'")
    assert row["c"] == 1
```

---

## 5. Edge cases and how the plan handles each

| #   | Edge case | Handled by |
|-----|-----------|------------|
| E1  | **Transcript empty** (job not yet claimed; no segments). | The header SELECT returns one row (the `transcripts` row exists) but `MAX(seq) = 0` and `LEFT JOIN transcript_segments` yields no cue rows. The handler emits `WEBVTT\n\nNOTE state=running\n\n` (D3, D5). The ETag is `W/"0…"` — a stable value that 304s until the first segment commits. (`test_view_active_transcript_with_no_segments_yields_no_rows`, `TestSerializeLiveVTT_EmptyMatchesFixture`) |
| E2  | **Partial transcript, live read during commit.** Reader hits the endpoint between INSERT and UPDATE of the per-segment commit. | Plan 3.6 §0.D1 puts the segment INSERT and the job UPDATE in **one PL/pgSQL function**, which runs in a single transaction; readers using REPEATABLE READ (default for the cheap header SELECT inside a single statement) see all-or-nothing. The view inherits this. The story explicitly cites this guarantee. |
| E3  | **Reader during transcript flip.** Story 3.5 atomically flips `is_active` from one transcript to another. | The view filters `is_active = true`. The flip is one transaction, so a reader sees either the old transcript's segments or the new transcript's segments, never both — exactly as the story states. The ETag will change (different `transcript_id` → different hash); the player sees a 200 with the new content. |
| E4  | **Transcript complete (state = `done`).** | Same body shape as `running` minus the NOTE preamble (D5 / `TestHandleLiveVTT_DoneOmitsNote`). The `?live=0` URL variant now serves the on-disk sidecar from Plan 4.1 and is byte-for-byte identical for the cue list. Recommendation in §2.7-E: players default to `?live=1`; the server picks the faster path internally without changing the URL. |
| E5  | **Transcript paused.** Job state is `paused` (Story 3.7). | `transcript_state` from the view reads `paused`; the NOTE preamble announces it; cues already committed are served (the segments are durable per Plan 3.6). The ETag is stable until the job resumes and commits new segments. (`TestHandleLiveVTT_PausedTranscriptShowsState`) |
| E6  | **Transcript failed.** OOM, model crash, etc. | Same as E5 but `state = failed`. The committed cues are still served — this is the "graceful degradation" the architecture relies on. The NOTE block tells the UI to surface a "transcription failed" badge. (`TestHandleLiveVTT_FailedTranscriptStillServesCues`) |
| E7  | **Video has no transcript at all.** No `transcripts` row, OR every row has `is_active = false`. | Header SELECT returns no rows; handler emits the empty body with `X-Maktaba-Transcript-State: absent` and `Cache-Control: max-age=60` (D6). 200, not 404. (`TestHandleLiveVTT_VideoExistsNoTranscript`) |
| E8  | **Video does not exist.** | The video lookup before the header SELECT returns no row → 404 with `application/problem+json`. (`TestHandleLiveVTT_VideoMissing`) |
| E9  | **Conditional GET hits.** `If-None-Match` matches the current ETag (cheap path). | 304 response with the same state/seq headers and an empty body (RFC 7232). The body SELECT is **not** issued. (`TestETag_ChangesOnNewSegment` covers both 304 and the post-commit 200.) |
| E10 | **Conditional GET stale ETag.** | New ETag computed; full body emitted. The client compares its stored ETag against the response's `ETag` and updates accordingly. |
| E11 | **`If-Modified-Since` only (no `If-None-Match`).** | We honor it — the header SELECT already produces `Last-Modified`. RFC 7232 says `If-Modified-Since` is **lower precedence** than `If-None-Match`; if both are present and ETag matches, return 304 even when LMT differs. (`TestIfModifiedSinceLowerPrecedence`) |
| E12 | **NOTIFY missed (DB restart, network blip).** | The ETag cache is at most `cache_ttl_sec` (default 5) stale. Players poll every 10 s; the worst-case freshness gap is `cache_ttl_sec + poll_interval = 15 s`. The reconnect loop in `NotifyListener.Run` recovers automatically. |
| E13 | **Non-monotonic seq on the writer (impossible by Plan 3.6).** | If it ever happens, `ORDER BY seq` still produces a deterministic order; the player will display cues out of conversational order but in seq order. The check_violation in `commit_segment` catches the writer-side bug; this is a defence-in-depth property. |
| E14 | **HTML in cue text.** Whisper transcribed `<script>` literally, or an external SRT contains markup. | `htmlEscape` runs on every cue body; the speaker tag is **inserted after** escaping so `<v ...>` survives. (`TestSerializeLiveVTT_EscapesHTML`) |
| E15 | **Bidi-mixed text.** Arabic cue with embedded Latin word. | `bidiIsolate` wraps the cue body per Plan 4.2; players render in source order. Same routine the sidecar generator uses, so on-disk and live output are identical bytes. |
| E16 | **Cue length exceeds wrap width.** | `lineWrap` (Plan 4.2) inserts `\n` at the source language's natural break points. Every wrapped line stays inside one cue (cues are not split). |
| E17 | **Body would exceed `max_inline_vtt_bytes`.** | Server-side cursor streams cues row-by-row; ETag/Last-Modified are computed before the cursor opens (D10). On SQLite, where cursors aren't available, the response cap forces 413 with a hint to use the sidecar URL. The 16-hour-of-speech threshold means this branch is exercised by the stress fixture only. |
| E18 | **Lang filter matches no active transcript.** | 404 `application/problem+json` with `available: ["..."]` listing the active languages so the player can drop the broken track from its menu. (`TestHandleLiveVTT_LangFilter`) |
| E19 | **Multiple active transcripts for one video** (misconfiguration; Story 3.5 prevents this but defence-in-depth). | The header SELECT's `ORDER BY t.id ASC LIMIT 1` deterministically picks the lowest. Both transcripts produce notifications; the cache eviction is per-`transcript_id` so the wrong one keeps caching. Operator reconciles via the Story 3.5 reaper. |
| E20 | **`?seek=` past the last committed segment.** | All cues are dropped; an empty cue list is emitted (with the NOTE preamble for live transcripts). Status is still 200. The client's player simply sees a track with no cues at the current playhead — fine. |
| E21 | **Subtitle longer than the video** (sidecar conversion; not a live concern). | Out of scope; this story does not serve sidecars. Story 8.11 AC and Plan 4.1 own the clip-to-`duration_sec` rule. |

---

## 6. Acceptance checklist

- [ ] **A1** A read-only SQL view `transcript_segments_v` exists with columns `(video_id, transcript_id, language_code, seq, start_sec, end_sec, text, speaker, confidence, committed_at, is_active, transcript_state)`. The story's named columns are all present; extras (`language_code`, `confidence`, `committed_at`, `transcript_state`) are documented in §2.1. (`test_view_excludes_superseded_transcripts`)
- [ ] **A2** The view exposes only segments whose parent transcript has `is_active = true`. (`test_view_excludes_superseded_transcripts`)
- [ ] **A3** A `(transcript_id, start_sec)` index supports window queries; an `EXPLAIN` of `SELECT … FROM transcript_segments_v WHERE video_id = ? AND start_sec BETWEEN ? AND ?` uses it. (`test_view_window_query_uses_index`)
- [ ] **A4** Writers never lock view rows for longer than a single-segment transaction. This holds because Plan 3.6 commits one segment per transaction (Postgres row-level locks, SQLite WAL). (Static check; verified by absence of long-lock metrics in §7.)
- [ ] **A5** The HTTP contract is `GET /v1/videos/{id}/subtitles.vtt?live=1[&lang=…][&seek=…]`, documented in `specs/contracts/live-vtt.openapi.yaml` with full schema. (Contract diff CI in Epic 8.)
- [ ] **A6** Empty transcript and "no transcript at all" both return 200 with a syntactically valid `WEBVTT\n\n` body and a `X-Maktaba-Transcript-State` header (`running`/`absent`). (`TestSerializeLiveVTT_EmptyMatchesFixture`, `TestHandleLiveVTT_VideoExistsNoTranscript`)
- [ ] **A7** ETag is `W/"<16 hex chars>"` derived from `(transcript_id, max(seq), max(committed_at), is_active, format_version)`; conditional GET with a matching `If-None-Match` returns 304. (`TestETag_ChangesOnNewSegment`)
- [ ] **A8** A new committed segment changes the ETag within `cache_ttl_sec + 1` of the commit. (`TestNotifyListener_EvictsCacheOnSegmentCommit`)
- [ ] **A9** `Last-Modified` is `MAX(committed_at)` formatted as RFC 7231 IMF-fixdate; `If-Modified-Since` is honored at lower precedence than `If-None-Match`. (`TestIfModifiedSinceLowerPrecedence`)
- [ ] **A10** Cue ordering is `ORDER BY seq ASC`. Cue identifier line is `seq-{N}`. Speaker tag is `<v {speaker}>` when present. HTML in cue text is escaped to `&lt;`/`&gt;`/`&amp;`. (`TestSerializeLiveVTT_PartialMatchesFixture`, `TestSerializeLiveVTT_SpeakerTag`, `TestSerializeLiveVTT_EscapesHTML`)
- [ ] **A11** Live transcripts (state ∈ {`running`, `paused`, `failed`}) include a leading `NOTE state=… progress=…%` block. `done` transcripts omit the NOTE. (`TestHandleLiveVTT_PausedTranscriptShowsState`, `TestHandleLiveVTT_FailedTranscriptStillServesCues`, `TestHandleLiveVTT_DoneOmitsNote`)
- [ ] **A12** `?lang=X` returns 404 with `application/problem+json` and an `available:[…]` field when no active transcript matches; without `lang=` the active transcript wins (lowest `id` tiebreaker). (`TestHandleLiveVTT_LangFilter`)
- [ ] **A13** `?seek=N` drops cues whose `end_sec ≤ N`; the response remains valid VTT. (`TestHandleLiveVTT_SeekDropsEarlierCues`)
- [ ] **A14** Streaming Service subscribes to `LISTEN segments.committed` and evicts its in-process ETag cache on each NOTIFY; the cache stores only headers, never cue bodies. Reconnects automatically on disconnect. (`TestNotifyListener_EvictsCacheOnSegmentCommit`, `TestCacheTTLCoalesces`)
- [ ] **A15** Every fixture in `specs/fixtures/live-vtt/` passes the W3C WebVTT validator. (`test_fixtures_pass_w3c_webvtt_validator`)
- [ ] **A16** Migration `0019_transcript_segments_view.sql` applies cleanly on fresh and populated DBs and is idempotent on re-run. (`test_view_migration_is_idempotent`)

---

## 7. Performance and operational notes

- **Header SELECT cost.** `EXPLAIN ANALYZE` on a populated table (~14 k segments per transcript, 1 k transcripts):
  - Index scan on `transcripts (video_id, is_active)` → 1 row.
  - Index scan on `transcript_segments (transcript_id, seq DESC)` → 1 row for `MAX(seq)`.
  - Total: <1 ms wall, ~3 buffer hits. Safe to run on every poll.
- **Body SELECT cost.** `SELECT … FROM transcript_segments_v WHERE transcript_id = ? AND end_sec > ? ORDER BY seq` → index scan on `transcript_segments_seq_idx`, ~1 µs per row. 14 k rows ≈ 14 ms. Streamed via cursor so the wire-time dominates.
- **Cache memory.** 1000 active live transcripts × ~80 bytes/entry = ~80 KB. Negligible.
- **NOTIFY rate.** Plan 3.6 fires one NOTIFY per segment commit. Worst-case live load: 8 concurrent jobs at ~12 segments/min = ~1.6 NOTIFY/s. The cache evict path is O(1).
- **Future v2 — SSE push.** When the polling-vs-push ratio crosses ~1000:1 reads per commit (very large rooms), upgrade `?stream=sse` on the same URL. The handler keeps an open `text/event-stream` connection and the LISTEN handler fans NOTIFYs to subscribed channels. Body is a stream of `event: cue\ndata: {…}` deltas. The schema and view in this story are unchanged. ETag/Last-Modified are not used over SSE; instead the `Last-Event-ID` reconnection header + a per-transcript `seq` cursor handle gap recovery.
- **Operational metrics.** Streaming Service should export:
  - `live_vtt_request_total{status,state}` (counter)
  - `live_vtt_etag_cache_hit_ratio` (gauge)
  - `live_vtt_header_select_duration_seconds` (histogram, p50/p95/p99)
  - `live_vtt_body_bytes` (histogram)
  - `live_vtt_listen_disconnects_total` (counter)
  These are dashboarded under Epic 21 (observability).

---

## 8. What this plan does **not** specify

- The byte-for-byte format of sidecar VTT files. That's Plan 4.1 +
  Plan 4.2. The live and sidecar bodies share the same serializer, but
  the sidecar omits the `NOTE state=…` preamble (state is always `done`
  for a sidecar) and the `seq-N` identifier (not required there).
- HLS subtitle wrapping (`subs/{lang}.m3u8`). That's Story 8.11 AC-4 —
  it just wraps this story's monolithic VTT response.
- Burned-in subtitles. Out of scope per Story 8.11 AC-6.
- Translation between languages. Deferred per architecture Appendix B.
- Cross-video speaker label stability. Deferred to Epic 9 / v1.1, per
  Plan 3.9 §0.D7.
- The signed-URL TTL for the player-facing URL. That's Story 7.7 + Epic
  10 Story 10.8; this story owns the relative path and the contract,
  not the signing layer.
