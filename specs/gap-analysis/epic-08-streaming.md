# Epic 08 — Streaming: spec-vs-implementation gap analysis

**One-line verdict:** The HTTP byte-serving scaffold (auth/JWKS, direct-play
range serving, in-memory caches) is real and reachable, but the entire
playback engine is **non-functional end-to-end**: no gRPC wire exists
(no `.proto`, no server, API client is a hard `ErrNotImplemented` stub),
FFmpeg is never spawned for HLS/DASH, no Postgres backend exists (probe +
session stores are empty in-memory fakes), and the API and Streaming write
two divergent `streaming_sessions` schemas that never share state.

## Method

Every AC was traced from a real runtime entry point: `streaming/main.go`
`runServe` → `buildPublicHandler` → `server.New`, and on the API side
`api/main.go` → `grpcstreaming.NewRealClient` → `streamingServiceAdapter`.
Existence of a type/function was **not** accepted as satisfaction — the
call had to be reachable from `main` with production wiring.

## Critical cross-cutting findings (affect most ACs)

1. **No gRPC transport exists at all.** Story 8.8 AC-1 mandates
   `shared/proto/streaming.proto`. There is **no `shared/proto`
   directory** and no `.proto` file anywhere. No `grpc.NewServer`,
   `grpc.Dial`, or `grpc.NewClient` call exists in `streaming/` or
   `api/`. `streaming/internal/grpcsrv/server.go` defines a plain Go
   struct that is **never instantiated** anywhere in `streaming/main.go`
   (`buildPublicHandler` builds only the HTTP mux; grep for `grpcsrv.New`
   in non-test code returns nothing).
2. **API streaming client is a permanent stub.**
   `api/internal/grpcclients/streaming/realclient.go:51-61` —
   `OpenSession`, `CloseSession`, `EvictHashCache` all
   `return ErrNotImplemented`. `NewRealClient` only does a 2 s TCP dial
   (`realclient.go:27`) and never speaks any RPC. The Streaming binary's
   `grpcsrv.Server.OpenSession`/`CloseSession`/`EvictHashCache` are
   therefore **unreachable from any runtime path**. This is the suspected
   "API never dials it; session bookkeeping is stale" — confirmed.
3. **No Postgres backend for probe or session stores.** `grep` for
   `pgx|database/sql|sql.DB` across `streaming/` (non-test) returns only
   `probe.go` (interface comment). `streaming/main.go:215` wires
   `session.NewMemoryStore`; `:221` wires `probe.NewFakeBackend()` (an
   empty map). `config.go:103` reads `MAKTABA_DATABASE_URL` but **no code
   consumes it**. A live deployment's probe `Lookup` always returns
   `ErrNotFound` → every `/stream/direct` and session lookup 404s.
4. **Divergent session schema, owned by the wrong service.** README +
   Story 8.9 AC-1 require Streaming to own `streaming_sessions` with
   `{host, pid, format, started_at, last_segment_at, closed_reason,
   state}` and `mode` including `direct-degraded`. The only migration,
   `shared/db/migrations/0039_streaming_sessions.sql:11-27`, has a
   different shape (`opened_at`, `last_seen_at`, `audio_track_id`,
   `subtitle_lang`, no `host/pid/format/state/closed_reason`; `mode`
   CHECK omits `direct-degraded`) and is written **by the API**
   (`api/internal/handlers/streaming/streaming.go:210-217`), not
   Streaming. Streaming's `session.MemoryStore` is a separate in-process
   map. The two never reconcile → reaper/heartbeat operate on data the
   API can't see and vice-versa.
5. **FFmpeg is never spawned for HLS/DASH.** Only `ffmpeg/remux.go:53`
   calls `Spawn`, and `Remuxer.Run` itself has no runtime caller (grep:
   no non-test reference to `Remuxer{`/`HLSArgs`/`DASHArgs`/`.Run(`).
   `ManifestHandler` (`handlers/manifest.go`) only `os.ReadFile`s
   manifests/segments that nothing ever writes → every manifest and
   segment request 404s in production.
6. **Subtitle / chapter / static-asset routes are not registered in the
   running server.** `server.New` gates them on `deps.Transcripts`,
   `deps.Chapters`, `deps.StaticAssets` being non-nil
   (`server.go:90,99,111`), but `buildPublicHandler`
   (`main.go:251-260`) never sets those fields → Stories 8.11/8.12/8.13
   endpoints are **dead routes** (fall to the NotFound handler).

## Per-story AC tables

### Story 8.1 — Server skeleton, signed-URL middleware

| AC | Status | Evidence |
|----|--------|----------|
| AC-1 Signed-URL RS256/aud/sub/exp/lib | partial | `auth/middleware.go:59-103` verifies sig/aud/sub/exp; lib coverage in `LibraryGuard` (`:108-131`). But `lib`-resolution uses the empty `FakeBackend` probe, so `LibraryGuard` 404s every real request. Sub-type `detail` mapping present (`mapVerifyError :155`). Reachable. |
| AC-2 JWKS async refresh, stale-on-error | complete | `auth/jwks.go`: blocking first fetch (`:73`), timer loop (`StartRefreshLoop :126`), single-flight (`tryRefresh :144` `TryLock`), keeps old keys on failure (`fetchOnce` only swaps on success `:195`), `FailureCount` metric (`:123`). Wired in `main.go:203-208`. |
| AC-3 404 problem+json on missing segment | partial | `manifest.go:135` returns 404 problem+json (not empty 200) — correct shape. But spec EC requires **410 Gone** for rolled-past segment and immediate 404 for closed session and `segment_wait_ms` polling — none implemented (no wait loop, no 410, no closed-session check). |
| AC-4 Direct-play JWT in query or Bearer | complete | `middleware.go:62-67` reads `?sig` then `Authorization: Bearer`; route wired with `AudDirect`+`"video_id"` (`server.go:70-73`). |
| AC-5 Static-asset JWT `sub=sha256(path)` | partial/unwired | Routes use `subParam="video_id"` (`server.go:116,120,124`), **not** `sha256(artifact-path)` as AC-5 + README table require — wrong-sub semantics deviate. Moot anyway: static routes unregistered (finding 6). |

### Story 8.2 — Capability matrix

| AC | Status | Evidence |
|----|--------|----------|
| AC-1 Matrix lookup | complete | `capability.Registry.Decide` consulted in `grpcsrv/server.go:159` and `handlers/direct.go:96`. Logic resident; unit tests exist. |
| AC-2 Per-session overrides | partial | `Override{ForceTranscode, MaxBitrateKbps}` passed (`grpcsrv:159-162`); `force_transcode`/`max_bitrate` honoured. But override path is only reachable via `grpcsrv.OpenSession`, which has no transport (finding 1) → unreachable in production. |
| AC-3 Unknown-profile → generic + warn | partial | `Profiles.Get` falls back, but no warning log with supplied profile/UA observed in `grpcsrv` path; unverified runtime warn. |

### Story 8.3 — Direct play

| AC | Status | Evidence |
|----|--------|----------|
| AC-1 Range 206 + Content-Range | complete (logic) / unwired (data) | `handlers/direct.go:135-161` correct single-range 206, `Content-Range`, clamps end to size (`parseRange :202`). But `Probe.Lookup` is the empty FakeBackend → 404 before bytes (finding 3). |
| AC-2 HEAD support | complete | `direct.go:126-129,153-155` returns 200/206 headers, no body. |
| AC-3 Multi-range → 416 | complete | `direct.go:136-143` detects comma, returns 416 + `Content-Range: bytes */total`. |
| AC-4 Not-direct → 409 + manifest_url | partial | Returns 409 `not-direct-playable` (`:98`) **only if** `?profile=` query present; spec/README expect server-side matrix verdict and a `manifest_url` in `detail` — neither the manifest URL nor profile-from-session is supplied. |
| EC ETag/If-Range/BLAKE3 | missing | No `ETag`/`If-Range` handling; `Last-Modified` only (`:120`). Cache-Control is `private, max-age=60`, spec is unspecified here but EC behavior absent. |

### Story 8.4 — Direct stream (remux)

| AC | Status | Evidence |
|----|--------|----------|
| AC-1 Cache-then-stream, atomic rename, 503 while writing | partial/unwired | `ffmpeg/remux.go` does temp+atomic-rename correctly, but **no HTTP handler invokes `Remuxer.Run`** (no runtime caller). No 503+`Retry-After` path. |
| AC-2 Cache hit serves immediately | partial | `Remuxer.Run :42` returns existing path on stat hit — logic only, never called. |
| AC-3 Streaming write / TTFB | missing | No streaming-write path; no single-flight by content_hash; no caller. |
| EC remux-failed → 502 + verdict downgrade | missing | `Run` returns the raw ffmpeg error; no `502 remux-failed`, no per-session verdict downgrade. |

### Story 8.5 — HLS adaptive transcode

| AC | Status | Evidence |
|----|--------|----------|
| AC-1 Master playlist §4.3 shape | partial | `manifest.go:175 BuildMasterPlaylist` emits only `#EXT-X-STREAM-INF` video variants — **no audio group, no subtitle group** as AC-1/README require. `Cache-Control: no-store` correct (`:89`). Master file is never written to disk anyway (no FFmpeg spawn). |
| AC-2 Live variant playlists / MEDIA-SEQUENCE / ENDLIST | missing | Relies entirely on FFmpeg `-f hls` output; FFmpeg is never spawned (finding 5). `ServeRenditionIndex :103` just `os.ReadFile`s a file nothing writes. |
| AC-3 Segment serving + wait/poll | partial | Content-Type/Cache-Control headers correct (`:146-147`); but **no `segment_wait_ms` polling loop**, no 410, no closed-session short-circuit. Returns 404 immediately. |
| AC-4 Bitrate ceiling | partial | `ffmpeg.DefaultLadder(maxKbps)` trims rungs (`ffmpeg.go:230-250`) and `ladderFor` passes it (`grpcsrv:238`). Unreachable (no transport / no spawn). |
| AC-5 Seek → cold restart | missing | No code kills/respawns FFmpeg with `-ss`; no discontinuity tag. `StartSec` is carried in structs but never reaches an ffmpeg arg builder. |
| AC-6 Burned-in subs `-vf subtitles=` | missing | grep for `subtitles=`/`-vf` in ffmpeg arg builders: none. `burn_subs` flag is plumbed into request structs only; never affects FFmpeg args. |

### Story 8.6 — DASH manifest

| AC | Status | Evidence |
|----|--------|----------|
| AC-1 DASH-only session, mpd served, HLS→409 | partial | Format-mismatch 409 implemented (`manifest.go:62-66`). But MPD is `os.ReadFile` of a file nothing writes; `DASHArgs` exists (`ffmpeg.go:184`) but is never spawned. |
| AC-2 MPD validation | missing | No MPD generator reachable; nothing to validate. |
| AC-3 live→static MPD transition | missing | No code flips `type=dynamic→static` or sets `mediaPresentationDuration`. |

### Story 8.7 — Hwaccel auto-detect

| AC | Status | Evidence |
|----|--------|----------|
| AC-1 Boot-time detection | partial | `ffmpeg/hwaccel.go Detect` correctly parses `ffmpeg -encoders` + nvidia-smi/vainfo; called in `main.go:225`. But result is assigned to `_ = hw` (`main.go:249`) — **never plumbed into the gRPC server or any encode**, and not exposed via `GetCapabilities`/`/api/stream/capabilities` at runtime (grpcsrv has no transport). |
| AC-2 force_software override | missing | `force_software` carried in `OpenSessionRequest` struct only; `encoderForHWAccel`/`HLSArgs` have no force-software branch; no caller. |
| AC-3 hwaccel-fail → SW restart → 502 | missing | No retry-on-encoder-error logic anywhere. |

### Story 8.8 — gRPC server

| AC | Status | Evidence |
|----|--------|----------|
| AC-1 OpenSession (proto shape, spawn FFmpeg, insert row) | stub/unwired | `grpcsrv/server.go:130` exists but: (a) no `.proto` (finding 1); (b) step 4 "spawn FFmpeg & wait for master playlist" **entirely absent** — `OpenSession` only inserts an in-memory row; (c) never instantiated in `main.go`; (d) API client returns `ErrNotImplemented`. Unreachable. |
| AC-2 CloseSession (SIGTERM/SIGKILL grace, purge dir) | partial/unwired | `grpcsrv:247 CloseSession` marks the row closed and `MemoryStore.Close :196` calls `Transcoder.Stop` — but no transcoder is ever attached, no 2 s grace/SIGKILL escalation (`ffmpeg.Process.Stop` uses `Kill()` directly, no SIGTERM), no HLS dir purge. Unreachable. |
| AC-3 EvictHashCache (remux/posters/sprites/thumbs + probe) | partial/unwired | `grpcsrv:268` only calls `Probe.EvictHash` — does **not** evict remux/poster/sprite/thumb disk caches as AC-3 requires. Unreachable (no transport). |
| AC-4 GetCapabilities (cached, no child proc, p95≤50ms) | partial/unwired | `grpcsrv:276` returns static struct (codecs hardcoded `["h264","aac"]`, no `LISTEN profiles_changed` refresh, no cache_used/cap GiB fields populated). Unreachable. |
| AC-5 HealthCheck liveness-only | partial/unwired | `grpcsrv:292` returns `{Healthy, Detail}` — no `last_error/last_error_at`. Unreachable. |

### Story 8.9 — Session store / sticky / reaper

| AC | Status | Evidence |
|----|--------|----------|
| AC-1 Row shape + library cascade | missing/divergent | Migration `0039` schema diverges from README/AC-1 (finding 4): no `host/pid/format/started_at/last_segment_at/closed_reason/state`. The Go `session.Row` has the right fields but is in-memory only, never persisted. |
| AC-2 Last-segment heartbeat (≤1 write/5s) | partial | `MemoryStore.Touch :165` debounces at `touchEvery` (5 s) — correct logic, but in-memory; the API's separate `last_seen_at` column is never updated by segment fetches (API doesn't serve segments). |
| AC-3 Reaper (30 s loop, 90 s idle, kill+purge+metric) | partial | `session/reaper.go` loop+cutoff+`Reaped()` metric correct; wired `main.go:245-246`. But operates on the empty in-memory store; no cache-dir purge; `SELECT FOR UPDATE SKIP LOCKED` semantics (EC) impossible without Postgres. |
| AC-4 Cross-host stickiness / 421 | missing | No sticky-cookie set on manifest response, no canonical-host check, no `421 Misdirected Request` anywhere (grep: none). |

### Story 8.10 — Concurrency caps / backpressure

| AC | Status | Evidence |
|----|--------|----------|
| AC-1 Slot accounting → RESOURCE_EXHAUSTED | partial/unwired | `slots.Allocator` derives `NumCPU/4` (`slots.go:54`), `grpcsrv:168-181` maps decisions. But unreachable (no gRPC transport); `suggested_action` detail not surfaced. |
| AC-2 Direct-cap fallback `mode=direct-degraded` | partial/unwired | `grpcsrv:175` sets `ModeDirectDegraded`; DB CHECK constraint (migration 0039) forbids `direct-degraded` → would fail insert if ever reached. |
| AC-3 Queue mode (202, position, eta, WS notify, promotion) | partial/unwired | `StateQueued` + `QueueState` produced (`grpcsrv:225-227`), but no slot-free promotion loop, no WS notification, no queue-cleaner for abandoned sessions. Unreachable. |

### Story 8.11 — Live subtitle

| AC | Status | Evidence |
|----|--------|----------|
| AC-1 Auto VTT live from DB, HTML-escaped, no-cache | partial/unwired | `handlers/subtitle.go ServeAuto` paginates+escapes+correct headers — solid logic. **Route never registered** (`deps.Transcripts` nil, finding 6). No Postgres transcript streamer. |
| AC-2 Sidecar SRT→VTT cached | partial/unwired | `SrtToVtt`/`ServeSidecar` exist but `ServeSidecar` route is **not wired** in `server.New` at all (only `subs/auto.vtt` is, and only when Transcripts!=nil); no `cache/subs/{hash}` write. |
| AC-3 Embedded via Pipeline `ExtractEmbeddedSubtitle` RPC | missing | No call to any Pipeline `ExtractEmbeddedSubtitle` RPC anywhere in streaming/. |
| AC-4 Single-format HLS subtitle wrapper `.m3u8` | missing | No `/subs/{lang}.m3u8` route or playlist generator. |
| AC-5 Bidi FSI/PDI isolation | missing | `writeCue :113` HTML-escapes but emits no FSI/PDI bidi isolates. |
| AC-6 burn_subs → 204 | missing | No 204 path for burn_subs sessions. |

### Story 8.12 — Chapter delivery

| AC | Status | Evidence |
|----|--------|----------|
| AC-1 chapters.json sorted, priority merge, static path | partial/unwired | `MergeChapters` priority logic correct (`chapter.go:60-89`); session route gated on nil `deps.Chapters` (unregistered, finding 6). The README's session-less `/stream/posters/{video_id}/chapters.json` (`aud=streaming-static`) route is **not registered at all**. |
| AC-2 DATERANGE in master playlist | missing | `DateRangeTagsForPlaylist` exists (`:93`) but `BuildMasterPlaylist` never calls it; no DATERANGE emitted into any served manifest. |

### Story 8.13 — Posters / sprites / thumbs

| AC | Status | Evidence |
|----|--------|----------|
| AC-1 Poster serve + Cache-Control private/immutable + 404 | partial/unwired | `StaticHandler` exists but route gated on nil `deps.StaticAssets` (finding 6) → unregistered. Middleware uses `aud=streaming-static` but `subParam="video_id"` not `sha256(path)` (AC-1 deviation). |
| AC-2 Sprite + VTT with sig rewrite | partial/unwired | Route unregistered; no `.vtt` URL-rewrite-at-serve logic verified. |
| AC-3 Chapter thumbs | partial/unwired | `/stream/thumbs/{video_id}/{name}` route exists in `server.New` but unregistered (nil resolver). |

### Story 8.14 — Cache layout & LRU GC

| AC | Status | Evidence |
|----|--------|----------|
| AC-1 §4.8 layout + 2-char shards | complete | `cache.Layout` (`internal/cache/layout.go`) + `Remuxer.OutputPath` two-char shard (`remux.go:30`). |
| AC-2 LRU eviction to 90%, atime/noatime fallback | partial | `cache.NewGC`+`Sweep` wired (`main.go:232-243`). atime/mtime+bbolt-sidecar fallback (AC-2) needs verification — bbolt not imported (grep clean) → noatime fallback likely missing. |
| AC-3 Per-tier soft caps (remux-first, poster floor) | partial | Needs confirmation in `cache` GC; not verified to implement 1 GiB poster floor / remux-priority. |
| AC-4 `maktaba-streaming gc` CLI | missing | `main.go` only handles `--version`/`serve`; no `gc` subcommand. |

### Story 8.15 — Probe cache

| AC | Status | Evidence |
|----|--------|----------|
| AC-1 Cache-hit, no DB query | complete (logic) | `probe.Cache.Lookup :86-102` LRU hit path. |
| AC-2 DB fallback (videos⋈media_info⋈audio_tracks) | missing | **No pgx/sql Backend implementation exists.** Only `FakeBackend`; `main.go:221` wires the fake → AC-2's SELECT never happens. |
| AC-3 On-disk re-probe forbidden → FAILED_PRECONDITION | partial | `FakeBackend.Lookup :196` returns `ErrNotProbed`; `grpcsrv:142` maps to `ErrFailedPrecondition`. Logic correct but unreachable (no transport) and `detail:"video-not-probed"` not surfaced over wire. |
| AC-4 EvictHashCache invalidates probe entries | partial/unwired | `Cache.EvictHash :146` works; reachable only via the stubbed gRPC `EvictHashCache`. |

## Top gaps by impact

1. **No gRPC transport between API and Streaming (BLOCKER).** No
   `shared/proto/streaming.proto`, no gRPC server, API client hard-codes
   `ErrNotImplemented`. Session open/close/evict and capabilities are
   completely disconnected. Stories 8.8/8.9/8.10/8.15 ACs are
   structurally unreachable. *Files:*
   `api/internal/grpcclients/streaming/realclient.go:51-61`;
   `streaming/main.go:198-261` (no `grpcsrv` wiring); absence of
   `shared/proto/`.

2. **FFmpeg is never spawned for HLS/DASH (BLOCKER).** The transcode
   engine that backs Stories 8.5/8.6/8.7 has arg-builders
   (`ffmpeg/ffmpeg.go`) but zero runtime caller; `ManifestHandler`
   serves files nothing writes. Every manifest/segment 404s in
   production. *Files:* `streaming/internal/handlers/manifest.go`,
   `streaming/internal/grpcsrv/server.go:130-206` (no `Spawn`).

3. **No Postgres backend; stores are empty in-memory fakes.**
   `probe.NewFakeBackend` + `session.NewMemoryStore` in
   `streaming/main.go:215,221`; `MAKTABA_DATABASE_URL` read but unused.
   Direct-play, probe lookup, session/reaper all operate on empty data
   → 404/no-op against any real library. Stories 8.3/8.9/8.15 fail.

4. **Divergent, mis-owned `streaming_sessions` schema.** API writes
   migration-0039 columns (`opened_at`/`last_seen_at`, no
   `host/pid/state/closed_reason/format`); Streaming's spec-correct
   `session.Row` is a separate in-memory map. README + Story 8.9 AC-1
   require Streaming to own a richer schema. Heartbeat/reaper/stickiness
   (8.9 AC-2/3/4) cannot function across the split. *Files:*
   `shared/db/migrations/0039_streaming_sessions.sql:11-27`,
   `api/internal/handlers/streaming/streaming.go:210-217`.

5. **Subtitle/chapter/static routes are dead in the running server.**
   `server.New` gates them on deps that `buildPublicHandler` never
   sets (`streaming/main.go:251-260` vs `server.go:90,99,111`). Stories
   8.11/8.12/8.13 endpoints 404 regardless of their internal logic
   quality. Plus AC-5/8.13 use `sub=video_id` instead of the
   spec-mandated `sub=sha256(artifact-path)`.

6. **Behavioral HLS gaps even if FFmpeg were wired:** no audio/subtitle
   `EXT-X-MEDIA` groups in master playlist (8.5 AC-1), no
   `segment_wait_ms` polling or 410-Gone (8.5 AC-3/EC), no seek cold
   restart (8.5 AC-5), no `-vf subtitles=` burn-in (8.5 AC-6), no
   DATERANGE injection (8.12 AC-2), no 421 sticky routing (8.9 AC-4),
   no `gc` CLI subcommand (8.14 AC-4).
