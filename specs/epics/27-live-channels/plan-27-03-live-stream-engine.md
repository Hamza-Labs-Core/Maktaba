# Plan 27.3 — Live stream engine — implementation

> Implementation plan for [story-27-03-live-stream-engine.md](story-27-03-live-stream-engine.md).
> Self-contained. Cross-links: reuses `streaming/internal/ffmpeg`
> (transcode ladder, hwaccel — Epic 8), `streaming/internal/session`
> (state + idle reaper, slot 0039 — Story 8.9/8.10), the HLS manifest
> serving (`handlers/manifest.go` — Story 8.5), and the `OpenSession`
> gRPC seam (Story 8.8, §9.9). Reads `channel_programs` (slot 0082,
> [Plan 27.2](plan-27-02-program-scheduler.md)). Writes slot 0083 (ALTER
> `streaming_sessions` + `channel_runtime`). Shares the MPEG-TS path with
> HDHomeRun ([Plan 27.5](plan-27-05-hdhomerun-emulation.md)).

---

## 0. Decisions

| #  | Decision | Rationale |
|----|----------|-----------|
| D1 | **A channel is a long-lived virtual streaming session** in `streaming/internal/channel/`, reusing the session store + reaper. | Lazy activation, idle teardown, host-pinning all already exist for sessions; a channel is a session whose input is a schedule, not one file. |
| D2 | **One FFmpeg per active channel, fed via the concat demuxer**, emitting a sliding live HLS window. | FFmpeg already does gapless concat + segment rotation; we add scheduling, not muxing (README decision). |
| D3 | **Wall-clock join:** start the first concat input seeked to `now − block.start_at + source_offset`; append subsequent blocks in order. | Story AC2 — the join offset is computed from the schedule's absolute times against the shared clock. |
| D4 | **Normalise every program through the same ladder** (no stream-copy of heterogeneous sources); emit `EXT-X-DISCONTINUITY` only on forced codec change. | Concat requires consistent stream params (Story EC3); normalisation keeps boundaries seamless. |
| D5 | **Lazy activation + warm grace window.** Spawn on first tune; keep encoder warm ~60 s after last viewer; reaper kills on grace expiry. | Story AC3/AC6 — cost scales with watched channels; warm window gives instant re-tune (surfing). |
| D6 | **Per-host concurrent-channel cap with LRU eviction of warm (zero-viewer) channels**, falling back to the session queue (8.10). | Story AC7 — bound transcode load; surfing must not spawn unbounded encoders. |
| D7 | **Look-ahead resolves the next block before the current ends** and appends it to the concat input. | Story AC4 — seamless boundaries; no stall at program changes. |
| D8 | **Tune is a new gRPC `OpenChannel` alongside `OpenSession`**, proxied by the API; auth + signed segment URLs identical to on-demand. | Story AC1/AC8 — reuse the streaming auth + proxy seam; channels aren't a parallel auth surface. |
| D9 | **MPEG-TS output is a sibling mode** of the same channel engine (used by HDHomeRun), distinct from the HLS window. | Story EC + [27.5](plan-27-05-hdhomerun-emulation.md) — Plex pulls TS, the player pulls HLS; same schedule, same look-ahead, different mux. |

---

## 1. Package layout (Streaming Service, Go)

```
streaming/internal/channel/
├── engine.go         # Channel lifecycle: activate/attach/detach/reap (D1/D5)
├── join.go           # wall-clock → (block, seek offset) (D3)
├── concat.go         # build/extend the concat input list from channel_programs (D2/D7)
├── window.go         # sliding HLS window config + boundary discontinuity (D2/D4)
├── mpegts.go         # continuous MPEG-TS output for one consumer (D9, used by 27.5)
├── registry.go       # active-channel registry + per-host cap + LRU eviction (D6)
├── repo.go           # read channel_programs; read/write channel_runtime
└── *_test.go         # fake ffmpeg.Runner + fake clock (mirrors transcode_test.go seam)
```

Hooks: `grpcsrv` gains `OpenChannel`; `handlers/manifest.go` serves
`/stream/channel/{id}/...` from the channel's HLS dir; the existing
reaper Sweep also reaps idle channels.

## 2. Activation & join (`engine.go` + `join.go`, D1/D3/D5)

```go
func (e *Engine) Tune(ctx context.Context, chID uuid.UUID, now time.Time) (*Handle, error) {
    if h, ok := e.registry.GetWarm(chID); ok {           // D5 warm re-tune (instant)
        h.Attach()
        return h, nil
    }
    if err := e.registry.Admit(chID); err != nil {        // D6 cap / eviction / queue
        return nil, err
    }
    blk, seek, err := join.Locate(ctx, e.repo, chID, now) // D3 current block + offset
    if err != nil { return nil, err }
    inputs := concat.Build(ctx, e.repo, chID, blk)        // D2 current + look-ahead (D7)
    job := ffmpeg.Job{                                    // reuse the existing Job/Runner
        SessionID: channelSessionID(chID),
        OutputDir: e.layout.HLSDir(channelSessionID(chID)),
        Ladder:    e.ladder, HWAccel: e.hwaccel.Detect(),
        Format:    "hls",
    }
    h := e.spawn(job, inputs, seek)                       // session-scoped ctx (HLB-328 pattern)
    e.registry.Put(chID, h)
    return h, nil
}
```

`join.Locate` is the wall-clock heart: `seek = now - blk.start_at +
blk.source_offset`; if `now == blk.end_at` it advances to the next block
at offset 0 (Story EC1). The encoder is spawned under a **session-scoped
context** (not the RPC ctx) exactly as the existing transcode `Handle`
does (the `HLB-328` note in `ffmpeg/transcode.go`), so grpc cancelling
`OpenChannel` doesn't kill the encoder.

## 3. Concat + look-ahead (`concat.go`, D2/D7)

A concat list (ffconcat / `-f concat -safe 0`) is built from
`channel_programs` resolved to library file paths — **only** from
`video_id`/`filler_item_id`, never a user string (threat model). A
background goroutine extends the list with the next block before the
current one's `end_at`, so the demuxer never reaches EOF at a boundary.
Heterogeneous sources are normalised by the ladder (D4); a forced codec
change emits an HLS discontinuity.

## 4. Lazy lifecycle & cap (`engine.go`/`registry.go`, D5/D6)

- **Attach/detach** track viewer count via segment fetches (reuse the
  session `Touch` mechanism); `last_segment_at` drives idle detection.
- **Warm grace:** on viewer_count→0, start a ~60 s timer; re-tune cancels
  it; expiry → reaper stops the child, clears `channel_runtime`.
- **Cap:** `registry.Admit` enforces the per-host cap; when full it
  evicts the LRU warm (zero-viewer) channel, else queues via the session
  queue (8.10). This is what bounds channel-surfing churn (Story EC7).

## 5. Data model — migration slot 0083

`shared/db/migrations/0083_channel_sessions.sql` (+ `.sqlite.sql`):

```sql
-- +goose Up
-- +goose StatementBegin
-- Slot 0083 (Epic 27 / Story 27.3) — channel sessions + runtime registry.
ALTER TABLE streaming_sessions ADD COLUMN IF NOT EXISTS channel_id UUID
    REFERENCES channels(id) ON DELETE CASCADE;
-- mode enum already (direct|remux|transcode|direct-degraded); extend to add 'channel'.
-- (CHECK-constraint widening shipped here to match the session.Mode constants.)

CREATE TABLE IF NOT EXISTS channel_runtime (
    channel_id      UUID        PRIMARY KEY REFERENCES channels(id) ON DELETE CASCADE,
    host            TEXT        NOT NULL,                 -- pinned host (§4.2)
    pid             INTEGER,
    state           TEXT        NOT NULL DEFAULT 'idle'
                                CHECK (state IN ('idle','warming','live','draining')),
    viewer_count    INTEGER     NOT NULL DEFAULT 0,
    started_at      TIMESTAMPTZ,
    last_segment_at TIMESTAMPTZ
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS channel_runtime_state_idx ON channel_runtime (state, last_segment_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS channel_runtime;
ALTER TABLE streaming_sessions DROP COLUMN IF EXISTS channel_id;
-- +goose StatementEnd
```

The mode-enum widening follows however slot 0039 expressed the
`mode` constraint (a `CHECK` rewrite or a lookup); match that. `.sqlite.sql`
per convention. Register slot 0083 in `MANIFEST.md`.

## 6. API / gRPC contract

```
# gRPC (streaming) — sibling of OpenSession (D8)
rpc OpenChannel(OpenChannelReq) returns (OpenChannelResp)   # {channel_id, now} → {session_id, manifest_url}

# HTTP (streaming) — served like /stream
GET /stream/channel/{id}/manifest.m3u8            # live master
GET /stream/channel/{id}/{rendition}/{seg}.ts     # live segments (signed, same auth)

# API proxy
POST /api/channels/{id}/tune  → calls OpenChannel, returns {session, manifest_url}
```

## 7. Files to create / modify

**Create:** everything under `streaming/internal/channel/`, the migration
pair.

**Modify:**
- `streaming/internal/grpcsrv` — add `OpenChannel`.
- `streaming/internal/handlers/manifest.go` — serve channel HLS dir
  (live playlists: no `ENDLIST`, `delete_segments`).
- `streaming/internal/session/{session,reaper}.go` — recognise channel
  sessions (mode `channel`), reap idle channels via the registry.
- `streaming/internal/ffmpeg` — concat-input + live-HLS flags + optional
  `xfade` (crossfade) and `-f mpegts` (D9) job variants.
- `api` streaming proxy + `POST /api/channels/{id}/tune`.
- `shared/db/migrations/MANIFEST.md` — register slot 0083.

## 8. Dependencies

- **27.1** (`channels`), **27.2** (`channel_programs`), **Epic 8**
  (ffmpeg/session/reaper/manifest/gRPC). Hard runtime dependency on a
  populated schedule; with none, `Tune` returns a slate stream (degraded
  channel).

## 9. Test strategy

Reuse the **fake `ffmpeg.Runner`** seam (as `transcode_test.go` does) +
a **fake clock** so tests assert the seek arg, the concat list contents,
boundary look-ahead, warm re-tune (same handle), cap/eviction, and reaper
teardown — all without exec'ing ffmpeg. An integration test with real
ffmpeg validates a live HLS window actually rotates and joins at offset.

## 10. Performance / cost

Cost = (active channels) × (ladder transcode), not (defined channels).
The cap + warm-window + LRU eviction keep a surfing user from melting the
box (Story EC7). Look-ahead overlaps next-block resolution with current
playback so boundaries add no latency.
