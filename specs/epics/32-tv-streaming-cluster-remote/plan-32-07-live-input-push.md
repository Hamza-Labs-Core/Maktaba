# Plan 32.7 — Live input pool and push — implementation

> Implementation plan for [story-32-07](story-32-07-live-input-push.md).
> Writes slot **0123** (`tv_remote_*`, `tv_push_secrets`, `tv_push_credentials`, `tv_push_report`).
> Design: [design.md](design.md) §7.2–§7.4, §7.7, §7.8.

## 0. Decisions

| # | Decision | Rationale |
|---|---|---|
| D1 | A **pool** of inputs (≤ `max_channels`), not one input per channel. | Bounded secrets and Cloudflare objects; the pool size *is* the concurrency limit; per-channel stable URLs are not needed because tokens are per broadcast. |
| D2 | Recording `automatic` + delete each recording at the end of its broadcast. | HLS playback of a live input requires `automatic`; deleting keeps stored minutes ≈ running broadcasts. |
| D3 | The push is a **push relay in the home's front door + ffmpeg `-c copy`** (Epic 33 story 33.8), not a pusher on the encoding host nor a third output of the muxer. One continuous RTMPS/SRT session per channel; the relay switches its source on swaps and failovers. | No second encode; a failing push never disturbs the muxer, HLS or tuners; restartable on its own. |
| D4 | RTMPS default, SRT optional. | RTMPS works with every ffmpeg build with TLS and is Cloudflare's recommendation for LL-HLS; SRT needs libsrt (checked per host). |
| D5 | Polling is the source of truth; webhooks only trigger a reconcile. | The home has no public URL unless the relay is set up; polling works everywhere. |
| D6 | `timeoutSeconds: 30` on inputs. | Failover and short uplink drops continue the same broadcast (same video id, same tokens). |
| D7 | One reconcile loop in the API, under an advisory lock. | Idempotent, crash-safe state machine; one actor. |

## 1. Files

**Create**
- `shared/db/migrations/0123_tv_remote.sql`, `.sqlite.sql`; MANIFEST row; migration test
- `api/internal/tvremote/pool.go` — create/assign/release inputs; shrink
- `api/internal/tvremote/push.go` — push state machine (design §7.3), timeouts, rotation, failover follow
- `api/internal/tvremote/reconcile.go` — loop every 2 s + on NOTIFY (`tv_push`, `tv_placement`), advisory lock `tv_remote`
- `api/internal/tvremote/cleanup.go` — recording deletion, startup sweep
- `api/internal/handlers/tvremote/webhook.go` — `POST /api/tv/remote/webhook` (secret header), allowlisted
- `streaming/internal/frontdoor/relay/relay.go` — per pushed channel: source (local Hub or a node's signed TS through the splicer) → one ffmpeg; source switch at an IDR with continuous timestamps (Epic 33.8); supervision; `tv_push_report`
- `streaming/internal/frontdoor/relay/ctl.go` — on the home: watch `tv_remote_pushes` (NOTIFY + 2 s poll), start/stop relays, fetch credentials
- tests for each (`cftest` fake server from 32.6; fake ffmpeg runner from `fakeffmpeg_test.go`)

**Modify**
- `streaming/internal/channel/engine.go` — `Engine.Pushing(channel)`: a push holds `reg.attach`
- `streaming/internal/placement` — `Need.Push` honoured (32.2 `Choose`)
- `streaming/main.go` — start the relay controller in the home's front door when remote or simulcast is enabled (never on nodes)
- `api/internal/router` — mount webhook (public) and admin push stop

## 2. Migration 0123 (Postgres, abridged)

```sql
CREATE TABLE tv_remote_inputs (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(), cf_uid TEXT UNIQUE NOT NULL, name TEXT NOT NULL,
  state TEXT NOT NULL CHECK (state IN ('idle','assigned','broadcasting','ending','cleanup','errored')),
  channel_id UUID REFERENCES channels(id) ON DELETE SET NULL,
  ingest JSONB NOT NULL,                -- {rtmps_url, srt_url}; never the key
  last_video_uid TEXT, assigned_at TIMESTAMPTZ, error TEXT, created_at TIMESTAMPTZ NOT NULL DEFAULT now());
CREATE TABLE tv_remote_pushes (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  channel_id UUID NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
  input_id UUID NOT NULL REFERENCES tv_remote_inputs(id),
  host_id TEXT, generation BIGINT,
  state TEXT NOT NULL CHECK (state IN ('requested','connecting','live','stopping','cleanup','done','failed')),
  video_uid TEXT, ingest TEXT NOT NULL CHECK (ingest IN ('rtmps','srt')),
  started_at TIMESTAMPTZ NOT NULL DEFAULT now(), live_at TIMESTAMPTZ, ended_at TIMESTAMPTZ,
  end_reason TEXT, detail TEXT, minutes_delivered NUMERIC, recording_deleted_at TIMESTAMPTZ);
CREATE UNIQUE INDEX tv_remote_pushes_one_per_channel ON tv_remote_pushes (channel_id)
  WHERE state IN ('requested','connecting','live','stopping');
CREATE TABLE tv_remote_sessions (…);      -- 32.8
CREATE TABLE tv_remote_usage_daily (day DATE PRIMARY KEY, minutes_delivered NUMERIC NOT NULL DEFAULT 0,
  peak_recording_minutes NUMERIC NOT NULL DEFAULT 0, cost_usd NUMERIC NOT NULL DEFAULT 0, source TEXT);
CREATE TABLE tv_push_secrets (push_id UUID PRIMARY KEY REFERENCES tv_remote_pushes(id) ON DELETE CASCADE,
  url TEXT NOT NULL, key TEXT NOT NULL, expires_at TIMESTAMPTZ NOT NULL);
-- tv_push_credentials(push_id) SECURITY DEFINER: only the home's own user (the front door's
--   push relay, Epic 33.8); returns (url, key). No node role can call it.
-- tv_push_report(push_id, state, detail) SECURITY DEFINER: same guard; only relay-side states
--   (connecting, live_seen, error, stopped).
-- maktaba_worker: SELECT on tv_remote_pushes (no secret columns there), nothing on
--   tv_push_secrets / tv_remote_sessions / tv_remote_usage_daily; EXECUTE on the two functions.
```

## 3. Push lifecycle (code shape)

```go
func (c *Controller) step(ctx context.Context, p Push) error {
  switch p.State {
  case Requested:   // input assigned + enabled, tv_push_secrets written, placement ensured
      if p.HostID == nil { h, gen := c.front.Ensure(ctx, p.Channel, NeedPush(p.Ingest)); c.store.SetHost(p, h, gen) }
      if c.since(p) > 15*time.Second { return c.retryOrFail(p, "host did not start the push") }
  case Connecting:  // host reported ffmpeg up; poll lifecycle every 1 s
      lc := c.cf.Lifecycle(ctx, p.InputUID)
      if lc.Live && lc.VideoUID != "" { return c.store.Live(p, lc.VideoUID) }
      if c.since(p) > 30*time.Second { return c.retryOrFail(p, "Cloudflare did not go live") }
  case Live:        // every 15 s: input status, lifecycle (video id change), viewers, idle, age
      if c.sessions.Active(p) == 0 && c.idleFor(p) >= idleTimeout { return c.store.Stopping(p, "idle") }
      if c.age(p) >= maxPushHours && c.sessions.Active(p) > 0 { return c.rotate(p) }
      if pl := c.placement(p.Channel); pl.Host != p.HostID { c.store.FollowPlacement(p, pl) } // failover
  case Stopping:    // the relay stops the push; then input disabled
      c.cf.SetEnabled(ctx, p.InputUID, false); return c.store.Cleanup(p)
  case Cleanup:     // wait ≤ 120 s for the video to leave live-inprogress; delete it
      return c.cleanupRecording(ctx, p)
  }
  return nil
}
```

## 4. Push relay (home front door; Epic 33.8)

```go
args := []string{"-hide_banner", "-nostats", "-loglevel", "warning",
  "-f", "mpegts", "-i", "pipe:0", "-map", "0:v:0", "-map", "0:a:0", "-c:v", "copy"}
if audioCodec == "ac3" { args = append(args, "-c:a", "aac", "-b:a", itoa(audioKbps)+"k") } else { args = append(args, "-c:a", "copy") }
switch ingest {
case "rtmps": args = append(args, "-f", "flv", "-flvflags", "no_duration_filesize", creds.URL+creds.Key)
case "srt":   args = append(args, "-f", "mpegts", creds.URL+"?streamid="+creds.StreamID+"&passphrase="+creds.Key+"&mode=caller")
}
```

Stdin is fed by the relay from the routed source (the home's `Station.Subscribe()` or a node's
signed `live.ts` through the splicer); a source change is a switch at an IDR with continuous
timestamps, never an ffmpeg restart (Epic 33.8). The log line masks `creds.Key`; an ffmpeg crash
restarts within 1 s on the same input; falling behind three times in 5 min → report
`error: uplink too slow`.

## 5. Tests

State machine with fake clock + fake Cloudflare (inputs, lifecycle with scripted `live`, videos,
delete, errors `ERR_*`); one push per channel under concurrency (unique index); rotation; failover
follow (placement moves → relay source switches, same session); startup sweep; relay args and masking; the
Postgres guard on `tv_push_credentials`; migration up/down.

## 6. Rollout

Behind `tv_remote.enabled` (off). First manual test with one channel and the admin as the only
remote viewer; then the web player (32.9).
