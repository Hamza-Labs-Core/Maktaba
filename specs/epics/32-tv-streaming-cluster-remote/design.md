# Design — Maktaba TV across the cluster and remote viewing (Epic 32)

Requirements: [README.md](README.md). Stories and plans: `story-32-*`, `plan-32-*`.

## 1. Shape

Three pieces, all on top of what exists:

| Piece | Where it runs | Built on |
|---|---|---|
| **Placement service + front door** | the home's streaming process (`streaming/`), beside the HDHomeRun handler and the channel engine | `streaming/internal/channel` (Engine, registry, Station, Hub), `streaming/internal/hdhr` (leases, Auto), `cmd/maktaba-server` web port proxy and `stream_gate.go` |
| **Streaming nodes** | `maktaba-server serve --role streaming` on a cluster node, in node mode | Epic 31 node config (`node.json`, database role, path map), `feat/tv-core` output settings and encoder gates |
| **Remote controller** | the home's API (`api/`), new package `api/internal/tvremote` | rules settings, `home_secrets`, Cloudflare Stream API |

Today a request for a channel reaches `Engine.Ensure` on the host that received it. After this
epic, on the home it reaches `placement.Resolve` first: *local* → the engine as today; *another
host* → proxy to that host. On a node the engine refuses to start a station unless the placement
row says this host, with this generation.

```
request ─► front door (home streaming)
            │ resolve(channel):  placement row?  ── yes, host alive ──► host
            │                    no / host dead ─► choose(host) under advisory lock,
            │                                      generation++ , write row ─► host
            ├─ host == home  ─► Engine.Ensure (local station)          [today's path]
            └─ host == node  ─► signed reverse proxy ─► node streaming ─► Engine.Ensure
                                                         (checks generation == placement)
```

## 2. Hosts, heartbeats, capacity

### 2.1 Identity

`streaming_hosts` (created by `feat/tv-core`, slot 0117; Epic 32 adds the columns it needs in
0122 rather than a second hosts table) is keyed by `host_id` (TEXT): the home's
`instance_identity.instance_id` or the node's `cluster_nodes.id` (Epic 31). Its `host` is the
host name, `max_active` the capacity (§2.3), `active_channels` the count on air and
`heartbeat_at` the last beat. The home's streaming process creates its row at boot
(role `home`, `enabled = true`); a node's row is created by its first heartbeat (role `node`,
`enabled = false` until the admin switches it on). A revoked or removed cluster node loses its
host row (FK `ON DELETE CASCADE`; revoke sets `enabled = false` and the node's role can no longer
log in, so it stops beating).

### 2.2 Heartbeat

Every 5 s each streaming host calls

```sql
tv_host_beat(host_id text, info jsonb) RETURNS jsonb
-- info: {version, encoders:["videotoolbox","software"], capacity, active:[channel_id…],
--        load:{cpu, gpu_enc, gpu_sessions, nvenc_max}, stream_base_url,
--        can_push:{rtmps:true, srt:false}, can_live:true}
-- returns: {enabled, draining, capacity_override, stream_key_b64, settings_rev}
```

`SECURITY DEFINER`, own row only: on the home the API user calls it directly; on a node the
function checks `cluster_nodes.db_role = session_user` for `host_id` (same guard as
`cluster_node_beat`, slot 0103). The returned `stream_key_b64` is the cluster stream key (R3.5),
read from `tv_cluster_secret` (singleton, revoked from `maktaba_worker`, created by the home at
first boot) inside the function, so it never sits in a table the node can read. It is not in
`home_secrets`: those values are sealed with a key file the database cannot open.

`active` is what the host's engine really runs (`Engine.ActiveCount` and the station ids); the
home cross-checks it against `channel_placements` (§3.4).

### 2.3 Capacity

`capacity = capacity_override ?: OutputSettings.EffectiveMaxActive(encoder)` (from `feat/tv-core`:
16 with VideoToolbox, else 8, or the setting). For NVENC hosts it is further capped at
`floor(nvenc_max / 2)` when the NVENC cap is set, because a channel briefly holds two sessions at a
block boundary (the next block pre-starts); a host whose NVENC gate refuses falls back to software
for that block (tv-core behaviour), which is visible in `load` but does not change capacity.

`free(host) = capacity − |placements on host in state placing|live|moving|`. Placements, not the
host's own `active` list, are what the balancer counts: a placement is taken the moment it is
written, so two requests arriving together cannot overfill a host.

### 2.4 Health

A host is **online** when `now − last_beat_at < dead_after` (15 s), its `version` is compatible
(same major `streaming` protocol version, R1.3), it is `enabled`, not `draining`, and — for nodes —
`tv_cluster.nodes_stream` is on and its Epic 31 storage check reads. Otherwise it is
**down** (missed beats), **disabled**, **draining** or **unfit** (with the reason), and takes no
new placements.

Besides heartbeats the front door keeps a per-host error counter: three consecutive failed proxy
attempts (connect refused, 5xx from the node's `/internal/tv/health`, TLS/timeouts) mark the host
**down** at once, without waiting for `dead_after`. A successful beat clears it.

## 3. Placement (the balancer)

### 3.1 Table

```sql
channel_placements(
  channel_id     uuid primary key references channels(id) on delete cascade,
  host_id        text not null references streaming_hosts(host_id),
  generation     bigint not null,          -- cluster-wide sequence; fencing token
  state          text not null check (state in ('placing','live','moving','released')),
  reason         text not null,            -- first_request | failover | drain | move | pin | remote_push
  placed_at      timestamptz not null,
  acked_at       timestamptz,              -- host confirmed the station runs with this generation
  released_at    timestamptz
)
```

`generation` comes from a sequence (`tv_placement_generation_seq`), so it is unique and grows
across the cluster.

### 3.2 Resolve

```
resolve(channel):
  p := SELECT … FROM channel_placements WHERE channel_id = $1
  if p and p.state in (placing, live, moving) and online(p.host): return p
  return place(channel, reason = p ? "failover" : "first_request")

place(channel, reason):
  BEGIN; SELECT pg_advisory_xact_lock(hashtext('tv_place:'||channel))
  re-read p (another request may have placed it meanwhile) → return if valid
  h := choose(channel)                       -- §3.3; none → ErrAtCapacity (R2.5)
  gen := nextval('tv_placement_generation_seq')
  UPSERT channel_placements(channel, h, gen, 'placing', reason, now())
  NOTIFY tv_placement, '<channel>:<host>:<gen>'
  COMMIT
```

The front door caches resolved placements for 2 s (keyed by channel) and drops the cache entry on
any proxy error or `NOTIFY tv_placement` for that channel; HLS segment fetches therefore cost no
query.

### 3.3 Choose

```
candidates := online hosts with free > 0 and, when the channel has live blocks in the next
              horizon window, can_live (a remote push needs nothing of the host: the
              home's push relay pushes, §7.4)
pin := tv_cluster.pins[channel.slug]
if pin in candidates: return pin
if policy == home_first:
    if home in candidates and used(home) < spill_at% × capacity(home): return home
    others := candidates − home
    return argmin over others of (used/capacity, −weight, host_id)        -- ties: stable
    (no others → home if home in candidates)
if policy == balanced:
    return argmin over candidates of (used/capacity, home_bias, −weight, host_id)
           -- home_bias: 0 for the home, 1 otherwise (prefer the home on ties)
none → ErrAtCapacity
```

The policy never moves a placed channel (R2.4): rebalancing would interrupt viewers, and the
load of a TV channel is constant (one encode), so the first placement stays good.

### 3.4 Fencing

A station on any host starts only through `Engine.Ensure`, which now asks a `Fence`:

- **home** (front door, same process): `Fence.Allow(channel)` = the placement it just resolved is
  for this host; it remembers `generation`.
- **node**: the signed front-door request carries `X-Maktaba-Placement: <gen>`; the node checks the
  row (`SELECT host_id, generation FROM channel_placements`; nodes have SELECT) matches itself and
  `gen`. On start it calls `tv_placement_ack(channel, gen)` (own host only) → `state = live`,
  `acked_at`.

Every station re-checks its placement every 5 s and on `NOTIFY tv_placement`; if the row now names
another host or a newer generation, it stops (non-blocking `Station.Stop`) and clears its
`channel_runtime` row. This bounds a split brain (a host that was wrongly declared dead but still
runs) to one check period.

### 3.5 Release

When the engine reaps an idle station (warm grace, today's reaper) it releases the placement if it
still holds that generation (`state = released`, `released_at`). A released row is reused by the
next `place`. A remote push counts as a viewer (`reg.attach`), so a pushed channel is not reaped
while the push runs.

## 4. Front door

### 4.1 Per entry point

| Client / path | Decision | Why |
|---|---|---|
| SSDP, `/device.xml`, `/discover.json`, `/lineup.json`, `/lineup_status.json`, `/lineup.post` | served by the home from the database (unchanged) | one device, one `DeviceID`, one `BaseURL` |
| `/xmltv.xml`, `/lineup.m3u` (streaming port and web port) | served by the home (unchanged) | guide data is in Postgres |
| `/auto/v{n}` (Plex, Jellyfin, Emby) | **proxy** (always) | HDHomeRun clients bind to one device; redirects are not part of the protocol and Plex's tuner client is not known to follow them reliably; the tuner lease must be counted where Plex connects |
| `/stream/channel/{id}/live.ts` (raw TS, VLC) | **proxy** | same as `/auto` |
| `/stream/channel/{id}/*.m3u8` (web port behind the session gate, streaming port for M3U players) | **proxy** | tiny, rewritten every segment; the gate and the single origin stay |
| `/stream/channel/{id}/seg-N.ts` | **proxy** (default) or **307** to a signed node URL (`hls_mode = redirect`) | redirect saves the home's bandwidth for many browser viewers; off by default because it exposes node addresses and needs nodes reachable from every client |

### 4.2 Proxy

`streaming/internal/frontdoor`:

- One `httputil.ReverseProxy` per host (`FlushInterval: -1`, no response buffering, keep-alives
  on, `MaxIdleConnsPerHost` 32), target `stream_base_url`.
- The director signs the request (`X-Maktaba-Front: <exp>.<hex hmac>` over
  `METHOD\npath\nexp` with the cluster stream key) and adds `X-Maktaba-Placement: <gen>`; it strips
  `Cookie`/`Authorization` (the web port already does).
- `/auto/v{n}` and `live.ts` go through a **splicing copier** instead of the plain proxy:
  it opens the upstream TS, copies to the client, and when the upstream ends or errors while the
  client is still connected it resolves again (§6) and continues with the new host's TS on the
  same client connection; at most 3 splices per connection (as `Engine.StreamChannelTS` restarts
  today). The tuner lease is held by the front door for the whole client connection.
  When the encoding host is not the home (e.g. the PC), the bytes cross the LAN twice
  (node → home → Plex, ≈ 6.5 Mb/s per tuned channel); accepted (README Q10), revisit only if the
  home's NIC is busy.
- Playlists and segments: plain proxy; a failed attempt counts against the host (§2.4); a
  playlist request retries once after re-resolving.

### 4.3 Redirect (opt-in, HLS segments only)

`307 Location: {stream_base_url}/stream/channel/{id}/seg-N.ts?fx=<exp>.<hmac>` with a 60 s
expiry. The node accepts the query-string form of the signature only for segment paths. Playlists
stay proxied so the front door keeps seeing every viewer (touch, failover).

### 4.4 Node streaming port

In node mode the streaming service mounts only:

- `/stream/channel/{id}/…` — requires a valid front-door signature (header, or query for
  segments); rejects with 403 otherwise;
- `/internal/tv/health` — signed; answers `{host_id, version, active, load}`.

It does not mount the HDHomeRun routes, XMLTV, M3U, nor the video-session routes, and never
starts SSDP (R1.4). This also closes the audit's "unauthenticated streaming port" on nodes; the
home's own streaming port keeps its current openness for LAN players (Epic 27 P1-4 remains).

## 5. Streaming nodes

`maktaba-server serve --role streaming` with a node config present (`MAKTABA_NODE_CONFIG` or the
OS default path from Epic 31 §7) runs in node mode:

| Need | Source |
|---|---|
| `DATABASE_URL` | `node.json.database_url` (the node's own Epic 31 role) |
| path map | `cluster_node_beat(node)` returns the stored map (inferred or admin) → `MAKTABA_STREAM_PATH_MAP` |
| host id | `node.json.node_id` |
| stream base URL | first private IPv4 of the interface that reaches the home, `:` streaming port; admin-editable in `streaming_hosts` |
| output settings | `rules.tv_output` (tv-core), per-host overrides applied by capacity override |
| channel art | `GET {home}/internal/tv/assets/{channel}/{kind}` (signed), cached under the node's cache dir by `sha256`; the station's `localAsset` resolves through this cache |
| fonts, ffmpeg, yt-dlp | local install; missing yt-dlp → `can_live = false` |

Database access needed by a streaming node with the `maktaba_worker` group role: `channels`,
`channel_programs`, `channel_schedule_state`, `videos`, `channel_art_overrides` (read),
`channel_runtime` (write), `channel_placements`/`streaming_hosts`/`tv_remote_pushes` (read), the
functions of §2.2, §3.4, §7.4. `streaming_sessions` is revoked from workers (slot 0103) and is not
used by channels.

## 6. Failover and drain

### 6.1 Detection → re-placement

When a host turns **down** (§2.4): a sweeper on the home (every 2 s) re-places every placement on
it whose channel still has demand (a front-door client attached, an HLS touch within the warm
grace, or a remote push) — `place(channel, "failover")`, which picks another host and bumps the
generation; placements without demand are released. Requests that arrive first trigger the same
re-placement through `resolve`.

### 6.2 What viewers see

- **New station, same timeline.** The new host joins the channel at the wall-clock position
  (Epic 27 `join.go`); nothing about the schedule depends on the host.
- **HLS.** The new station starts its muxer with `discont_start` (as a muxer restart does today) and
  `hls_start_number_source epoch` keeps media sequence numbers increasing, so a player sees a
  discontinuity and continues after a stall of about one ready timeout (first segment, typically
  4–8 s).
- **TS (`/auto`, `live.ts`).** The splicing copier (§4.2) keeps Plex's connection; Plex sees a PCR/
  continuity jump (it tolerates these at concat boundaries today) instead of a dropped tuner.
- **Remote push.** The push restarts on the new host to the same input (§7.5); within
  `timeoutSeconds` Cloudflare continues the same broadcast (same video id, same tokens).

### 6.3 Drain and move

`drain` (admin) → `online = false` for new placements. Channels on it: idle ones are released by
the reaper; busy ones stay until idle, or move at once on "move now" (`place(channel, "drain")`).
`POST /api/tv/cluster/channels/{id}/move {host_id?}` re-places one channel (to a host, or the
balancer's choice): the same path as failover, so it interrupts that channel's viewers briefly.

### 6.4 The home itself

If the home's streaming process dies, there is no front door: LAN clients lose TV until it
restarts (as today); stations on nodes keep running until their placement check fails to read the
database or the warm grace passes with no front-door requests, then stop. When the home comes
back, `resolve` finds the node placements still valid (nodes kept beating) and keeps them.
A second home is out of scope (Epic 31: one home).

## 7. Remote viewing through Cloudflare Stream

### 7.1 Components

- **Cloudflare client** (`api/internal/tvremote/cf`): live inputs (create, get, update enabled,
  rotate keys), lifecycle (`customer-…/{input}/lifecycle`), videos of an input, delete video,
  signing keys, views, GraphQL analytics. Token from `home_secrets`.
- **Remote controller** (`api/internal/tvremote`): the input pool, the push state machine,
  sessions, token minting, limits and budget; one reconcile loop every 2 s (and on NOTIFY), with a
  Postgres advisory lock so only one API process acts.
- **Push relay** (`streaming/internal/frontdoor/relay`, on the home only; Epic 33.8): pulls the
  channel's TS from the encoding host and feeds one continuous ffmpeg stream-copy session to the
  input (§7.4).

### 7.2 Live input pool

Inputs are created lazily (first need) up to `max_channels`, named
`maktaba-<instance8>-<n>`, with:

```json
{"meta":{"name":"maktaba-1a2b3c4d-1"},
 "enabled": false,
 "recording": {"mode":"automatic","requireSignedURLs":true,"timeoutSeconds":30,
               "hideLiveViewerCount":false},
 "preferLowLatency": false}
```

`recording.mode = automatic` because Cloudflare serves HLS/DASH for a live input only in that mode
(README Q1, decided by the owner); each broadcast's recording is deleted as soon as the broadcast
ends (§7.7). `preferLowLatency` follows `tv_remote.low_latency`, off by default (README Q11).
`deleteRecordingAfterDays` is not used (minimum 30 days). The pool holds at most `max_channels`
inputs (default 22, every channel, 1–50: ≈ 6.5 Mb/s of the home's 1 Gb/s upload each, ≈ 143 Mb/s
for 22, README Q4; encoder capacity, not bandwidth, limits how many channels can be pushed).
Lowering `max_channels` deletes idle inputs above the limit. The stream key / SRT passphrase go to `home_secrets`
(`cf_input_<uid>_key`); `tv_remote_inputs.ingest` keeps only the URLs. When a push is requested
the API opens the key and writes `{url, key}` to `tv_push_secrets` (push id, `expires_at` = now +
`max_push_hours`); the row is deleted when the push ends.

Input states:

```
            create                assign (enabled=true)           push live
 (absent) ────────► idle ───────────────────────────► assigned ─────────────► broadcasting
                     ▲   ◄──────── release ────────────  │                        │
                     │      (enabled=false, wait for      │ errored (ERR_*)       │ push stopped
                     │       recording, delete video)     ▼                        ▼
                     └────────────────────────────── cleanup ◄──────────────── ending
 errored: ERR_STORAGE_QUOTA_EXHAUSTED / ERR_MISSING_SUBSCRIPTION → the whole remote feature
          turns unavailable with that reason; ERR_GOP_* / codec / UNHEALTHY → the input goes
          to cleanup, the push is retried once on a fresh input, then the channel is marked
          "remote failed" for 10 min.
```

### 7.3 Push state machine (per channel)

```
              tune (no push for channel)
                       │  limits ok? input free?
                       ▼
   ┌────────────── requested ─────────┐  input assigned + enabled; placement ensured
   │                   │ host picks it up (NOTIFY tv_push / 2 s poll), gets credentials
   │                   ▼
   │               connecting ── ffmpeg up; CF input status connected; lifecycle live=true,
   │                   │          videoUID known (poll 1 s, timeout 30 s → retry/fail)
   │                   ▼
   │                  live  ◄──── viewers heartbeat
   │                   │  no viewer for idle_timeout   │ age ≥ max_push_hours → rotate:
   │                   ▼                               │   new push (new video) on the same
   │                stopping ── relay stops the push   │   input after this one ends
   │                   ▼
   │               cleanup ── input enabled=false; wait ≤ 120 s for the recording;
   │                   │       DELETE video; input → idle
   │                   ▼
   └── failed ◄──── (any step past its timeout / CF error)          done
```

Failover (§6): when the channel's placement moves while the push is `connecting|live`, the push
row's `host_id`/`generation` are updated and the home's push relay (§7.4) switches its source to
the new host's TS; the RTMPS/SRT session to the input stays up, so Cloudflare continues the same
video and the session tokens stay valid. Only when the front door itself is down longer than
`timeoutSeconds` (30 s) does Cloudflare create a new video: the controller notices the new `videoUID` from the
lifecycle endpoint, and the next heartbeat of each session returns a new token and URL.

Rotation (age ≥ `max_push_hours`, default 8) costs remote viewers a short re-buffer once per 8 h
of a continuous push: the web player swaps to the new video's URL from its next heartbeat, native
apps re-tune. Accepted as the default (README Q9); it is what keeps two pushes inside one
1,000-minute storage block (§7.7).

### 7.4 The push relay (in the home's front door)

> Amended by Epic 33 story 33.8 ([continuous push across swaps](../33-tv-staging-hot-swap/story-33-08-continuous-push-simulcast.md)):
> the push runs in the home's front door, not on the encoding host, so one RTMPS/SRT session per
> channel survives hot swaps, rollbacks and failovers.

- **Where.** `streaming/internal/frontdoor/relay` on the home. For each pushed channel the relay
  pulls the channel's MPEG-TS from wherever it is encoded — the home's own station (local Hub) or
  a node's station (the signed `/stream/channel/{id}/live.ts`, §4.4, through the splicing copier of
  §4.2) — and feeds one long-lived ffmpeg. No node runs a pusher.
- **No second encode.** The relay's ffmpeg copies video (and audio unless it must be AAC):

  RTMPS (default):
  `ffmpeg -hide_banner -loglevel warning -f mpegts -i pipe:0 -map 0:v:0 -map 0:a:0 -c:v copy
   -c:a copy|aac -b:a <kbps> -f flv -flvflags no_duration_filesize rtmps://live.cloudflare.com:443/live/<streamKey>`

  SRT:
  `ffmpeg … -c copy -f mpegts "<srt.url>?streamid=<streamId>&passphrase=<passphrase>&mode=caller"`
  (host and port from the input's `srt.url`, as returned by the API).

- **One continuous session.** When the source changes — a block boundary, a muxer restart, an
  Epic 33 swap or rollback, an Epic 32 failover or move to another host — the relay switches its
  *source* at an IDR with continuous timestamps (33.8 AC2–AC3); the ffmpeg process and its
  RTMPS/SRT connection stay up, so Cloudflare keeps the same broadcast (video id, tokens) and
  simulcast outputs (§7.9) see no disconnect. A relay ffmpeg crash is restarted within 1 s on the
  same input (inside `timeoutSeconds`).
- **LAN cost.** A node-encoded channel that is pushed crosses the LAN once more (node → home,
  ≈ 6.5 Mb/s per channel) before the home uploads it; with the home's 1 Gb/s uplink and LAN this
  is fine (22 channels ≈ 143 Mb/s each way).
- Audio: Cloudflare accepts AAC only; when `tv_output.audio_codec = ac3` the relay transcodes audio
  to AAC (cheap, continuous across switches, 33.8 AC9) and leaves video copied.
- Video already fits Cloudflare's input rules: H.264 high, CBR, keyframe every 2 s, closed GOP,
  `-bf 0` (feat/tv-core encoder args), 6 Mb/s default (inside 2–14 Mb/s); the settings page warns
  when `video_kbps + audio_kbps` leaves that range while remote is on.
- Credentials: `tv_push_credentials(push_id)` (SECURITY DEFINER) answers only the home's own
  database user (the front door); it returns the `tv_push_secrets` row `{url, key}`, held in
  memory, never logged (the ffmpeg command line is logged with the key masked). Node roles never
  see an ingest key.
- Supervision: report via `tv_push_report(push_id, state, detail)`; the relay holds a front-door
  lease on the channel so its station (local or on a node) is not reaped while pushing.
- Ability check: the home's `ffmpeg -protocols` contains `rtmps` (TLS) / `libsrt` (`can_push` of
  the home's `streaming_hosts` row).

### 7.5 Tune, heartbeat, end

```
POST /api/tv/remote/tune {channel_id, device}
  auth: signed-in; tv_remote.viewers allows the user (default `all` signed-in users, README Q6);
        channel enabled and readable (libraryacl)
  gates (in order): integration ready → remote enabled → budget (402) →
                    user sessions < max_streams_per_user (default 2; 429 user_streams_full) →
                    channel pushing? reuse : (pushes < max_channels ? new push : 429 remote_channels_full)
  create tv_remote_sessions row (push_id)
  push live with video_uid → 200 {session_id, hls_url, expires_at, refresh_after, channel}
  push starting           → wait up to 20 s (long poll) → 200, else 202 {state:"starting", retry_after:3}
POST /api/tv/remote/sessions/{id}/heartbeat  (every 60 s)
  last_beat_at = now; when now ≥ token_exp − ttl/2 or the push's video_uid changed:
  return {token, hls_url, expires_at}; else {expires_at, state}
DELETE /api/tv/remote/sessions/{id}  → ended_at = now
A session with no heartbeat for 150 s is ended by the reconcile loop.
A push whose sessions are all ended starts its idle_timeout countdown.
```

The client may retry `tune` on 202 with the same `session_id` (idempotent per user+channel+device).

### 7.6 Token minting

```
header  {"alg":"RS256","kid":"<signing key id>"}
claims  {"sub":"<video_uid>","kid":"<signing key id>",
         "exp": now + token_ttl, "nbf": now − 30,
         "accessRules":[{"type":"ip.geoip.country","action":"allow","country":[…]},
                        {"type":"any","action":"block"}]}      -- only when allowed_countries is set
hls_url = https://customer-<code>.cloudflarestream.com/<token>/manifest/video.m3u8
```

Signed on the home with the key from `home_secrets` (created by the integration test with
`POST /accounts/{id}/stream/keys`); no Cloudflare API call per viewer. `exp` is capped at 24 h
(Cloudflare's limit). "Rotate signing key" creates a new key, switches minting to it, then deletes
the old key at Cloudflare, which invalidates every outstanding token (R8.4); sessions recover on
their next heartbeat with a token from the new key.

If the 32.6 spike shows that a token whose `sub` is the in-progress video id does not play the
live broadcast, `sub` becomes the live input id and the pool enforces a cool-down of one
`token_ttl` before an input serves another channel (so an old token cannot show the next channel).

### 7.7 Recording cleanup and storage guard

After `stopping`: disable the input (no reconnects), wait for the broadcast's video to leave
`live-inprogress` (poll `GET …/live_inputs/{uid}/videos`, ≤ 120 s; recordings appear about 60 s
after a broadcast ends), `DELETE /accounts/{id}/stream/{video_uid}`, record
`recording_deleted_at`. A startup sweep deletes any video of a Maktaba input that is not the
current broadcast of a live push (crash recovery). Before starting a push the controller checks
`Σ age(live pushes) + max_push_hours × 60 ≤ storage_plan_minutes`; otherwise the tune gets 503
`remote_storage_full` with a hint to raise the storage plan or lower `max_push_hours`.

The account is sized for one $5/month block of 1,000 stored minutes (README Q1). Because the
recording of a running broadcast is stored until it ends and is deleted right after, stored
minutes ≈ Σ age(live pushes). With `max_push_hours = 8` (480 min) one block has room for
**two** full-length concurrent pushes (2 × 480 ≤ 1,000); once the running pushes' ages plus 480
would pass 1,000, the next push gets `remote_storage_full` even though `max_channels` is 22. On the
default plan the storage guard is therefore usually the binding concurrency limit, and the admin
page shows the stored-minutes headroom next to "remote channels: n of 22". Each further
1,000-minute block makes room for about two more full-length pushes; a lower `max_push_hours` fits more pushes per block at the
cost of more frequent rotation (§7.3).

### 7.8 Polling and webhooks

Polling is the source of truth (README Q12): input `status` and `lifecycle` every 1 s while `connecting`,
every 15 s while `live`; `views` (live viewers) every 60 s for the admin page and as a second
signal for idleness (sessions are primary). Webhooks (`live_input.connected|disconnected|errored`)
are optional: if the admin configures a Cloudflare Notifications webhook to
`https://<user>.maktaba.app/api/tv/remote/webhook` with a secret, the handler verifies the secret
and triggers an immediate reconcile (it never changes state by itself); errored events carry the `ERR_*` code shown on the admin page.

### 7.9 Simulcast (story 32.13)

Cloudflare retransmits whatever a live input receives to its **live outputs** (RTMP/SRT, up to 50
per input; `POST /accounts/{id}/stream/live_inputs/{input}/outputs` `{url, streamKey, enabled}`;
outputs can be added, disabled or deleted during a broadcast, a deleted one disconnects within
30 s — [Simulcasting](https://developers.cloudflare.com/stream/stream-live/simulcasting/)). Maktaba
simulcasts by adding outputs to the input its push already uses, so a channel is uploaded once
whatever the number of destinations.

- **Destinations** (`tv_simulcast_destinations`): name, kind (`youtube` | `facebook` | `rtmp`),
  ingest URL; the stream key in `home_secrets` (`simulcast_<id>_key`).
- **Attachments** (`tv_simulcast_attachments`): channel × destination, `always` or `schedule`
  (rules: weekly windows in the channel's time zone; "while video X / series Y airs" and "during
  live slots", resolved against `channel_programs`), with `lead_seconds` / `tail_seconds`;
  adjacent matches merge into one window. Enabling needs the rights confirmation (README R13.6).
- **Holds.** A push has `holds` (`viewers`, `simulcast`). The reconcile loop (§7.1) adds the
  `simulcast` hold when a window opens (requesting a push as a tune would, §7.3, minus the
  per-user gates) and removes it when the last window of that channel closes; the idle timeout
  (§7.3) runs only when the push has no hold and no session.
- **Outputs follow the push, not the input.** When the push is `live`, the controller creates one
  output per open window on the push's input with the key from `home_secrets`, and deletes it when
  the window closes or the push ends. An idle pooled input never keeps an output, so a later
  channel on that input cannot reach a destination.
- **Continuity.** Outputs belong to the input, so everything that keeps the input keeps them: a
  block change, muxer restart, Epic 33 hot swap or rollback, or failover: the home's push relay
  keeps one RTMPS/SRT session and only switches its source (§7.4, Epic 33.8); a rotation at `max_push_hours`
  (same input, a new broadcast; the destination sees a gap of seconds).
- **Status.** Maktaba's own output state (`scheduled`, `starting`, `live`, `stopping`, `off`,
  `errored`) plus the input's status and lifecycle (§7.8). Cloudflare documents no per-output
  health; the 32.6 spike checks for one (README Q13).
- **Cost.** Output minutes are minutes delivered (§9): they count towards the budget and towards
  `tv_simulcast.max_hours_per_month`. At the cap no output starts and `always` outputs stop; at
  the budget with `block_new` no output starts and running windows finish.
- **Out of scope.** The platform's event metadata (title, description, privacy) is set on the
  platform; Cloudflare forwards only the media.

## 8. App routing (LAN or remote)

### 8.1 Server classification

`GET /api/tv/info` adds:

```json
"remote": {"available": true, "via_relay": true, "reason": null,
           "max_streams": 2, "token_ttl_minutes": 60}
```

`via_relay` is true when the request carries `X-Maktaba-Via: relay`, set by the server-side relay
agent on its loopback calls (the agent strips any client-supplied copy). The agent is wired into
`maktaba-server` with the Epic 25 work before Phase B (README Q5). With the opt-in
`remote_detection = relay_or_public_ip` ("a public client IP counts as remote"; default `relay`)
a request from a non-private client address (not RFC 1918,
not ULA, not loopback, not CGNAT 100.64/10) also counts as remote. The API strips
`X-Maktaba-Via` from requests that do not come from loopback.

### 8.2 Web (first)

- The live player loads hls.js lazily (audit P1-3) and plays either the LAN URL
  (`/stream/channel/{id}/index.m3u8` on the web origin, as today) or the remote `hls_url`.
- Choice: if `info.remote.via_relay` → remote; else LAN. A LAN playback that fails with 401/502
  three times while `remote.available` offers "Watch remotely".
- Token swap without reload: a custom hls.js loader rewrites the token path segment of every
  playlist/segment URL to the latest token from the heartbeat, so a new token takes effect on the
  next request; the media element never reloads.
- Heartbeat every 60 s while playing; `DELETE` on stop/unload (`navigator.sendBeacon`).
- Errors: 402 → "Remote viewing paused: monthly budget reached"; 429 → which limit; 202 → "Starting
  remote stream…" with the channel slate.

### 8.3 Native apps (later)

Use the Epic 25.10 resolver: LAN endpoint chosen → LAN HLS from the home; relay chosen → the
remote tune API and Cloudflare HLS. AVPlayer/ExoPlayer cannot rewrite URLs cheaply, so they
re-tune at `refresh_after` (≈ 90 % of the token life) and swap the item at the next segment
boundary (AVQueuePlayer / ExoPlayer playlist), accepting a short re-buffer. Stream affinity from
25.10 applies: a session stays on its path until it ends.

### 8.4 Guide for remote clients

Unchanged JSON APIs (`/api/channels`, `/api/channels/guide`, `/api/channels/now`) through the relay.
XMLTV/M3U are not offered remotely (non-goal).

## 9. Cost controls

- Limits at tune (§7.5). Budget: the month's estimate =
  `minutes_delivered × delivery_usd_per_1000_min / 1000 + storage_plan_minutes / 1000 × 5`;
  minutes delivered come from GraphQL `streamMinutesViewedAdaptiveGroups` summed over the video ids
  of this month's Maktaba pushes (hourly; between polls the estimate adds
  `Σ live sessions × minutes since last poll`, so it errs high).
- 80 % → admin notification (existing notifications/log event); 100 % → `block_new` (the default,
  README Q8: 402 on every new tune, including a tune of a channel that is already pushing;
  running sessions continue until they end, like Epic 25.12; their heartbeats keep refreshing
  tokens, and a rotation at `max_push_hours` or a failover of a channel they watch is not a new
  tune, so it is not refused) or `warn` (notification only).
- Idle timeout and `max_push_hours` bound pushes with no viewers and the stored minutes.
- Simulcast (§7.9): each output adds its own minutes delivered (one always-on output ≈ 43,200
  min ≈ $43 per 30-day month), counted in the estimate and capped separately by
  `tv_simulcast.max_hours_per_month`; a simulcast push is not idle-stopped (holds) and its
  recording counts towards the storage guard.
- No audio-only mode: Cloudflare bills minutes, not bytes, and live inputs need H.264 video.

## 10. Observability

Prometheus (the existing `/metrics` of each service; Epic 21) plus `health_samples` rows for the
control centre:

| Metric | Labels | From |
|---|---|---|
| `maktaba_tv_host_up` | host, role | home placement service |
| `maktaba_tv_host_capacity`, `maktaba_tv_host_used` | host | placements |
| `maktaba_tv_channels_on_air` | host, encoder | each host's engine |
| `maktaba_tv_placements_total` | reason | placement service |
| `maktaba_tv_failovers_total` | from_host | placement service |
| `maktaba_tv_frontdoor_bytes_total` | kind (ts, hls), host | front door |
| `maktaba_tv_frontdoor_errors_total` | host, kind | front door |
| `maktaba_tv_remote_viewers` | channel | sessions |
| `maktaba_tv_remote_pushes` | state | controller |
| `maktaba_tv_remote_push_errors_total` | code | controller / push relay |
| `maktaba_tv_remote_minutes_delivered` | month | analytics poll |
| `maktaba_tv_remote_cost_usd` | month | estimate |

Events (structured logs with `event=`): `tv_placement`, `tv_failover`, `tv_host_down`,
`tv_host_up`, `tv_push_start`, `tv_push_live`, `tv_push_stop`, `tv_push_error`,
`tv_recording_deleted`, `tv_budget_80`, `tv_budget_100`, `tv_cf_error`.

## 11. Security summary

See README "Security". The trust boundary for cluster streaming is the LAN plus Epic 31's approval
of a machine; the front-door HMAC keeps a LAN client from pulling channels straight from a node.
For remote viewing it is the Maktaba session (through the relay) plus a short-lived token per
broadcast; the Cloudflare API token never leaves the home. Every extension secret (the Plex token
today, the Cloudflare token, any `secret` setting field) lives in `home_secrets`, not in
`extensions.settings`, so node roles cannot read it (README Q7, story 32.6).

## 12. Testing approach

- Go unit tests with fakes: placement choose/resolve/fencing (table tests for policies, pins,
  capacity, NVENC pairs), a fake clock for heartbeats and dead detection, the splicing copier
  against two fake upstream TS servers, HMAC sign/verify, the push state machine against a fake
  Cloudflare server (httptest) that models inputs, lifecycle, videos, keys, outputs (simulcast),
  errors; simulcast window resolution with a fake clock and timeline.
- Python/SQL: migration up/down tests on SQLite twins; `SECURITY DEFINER` guards on Postgres
  (skip when no Postgres), as for slot 0103.
- Two-process end-to-end on one machine: home stack + a second `serve --role streaming` with its own
  node config and port; place a channel on it (pin), pull `/auto/v{n}` from the home, kill the
  node, see the splice to the home within 20 s.
- Cloudflare spike (manual, real account, story 32.6): token subject, `exp` per segment, latency,
  reconnect within `timeoutSeconds` keeps the video id, recording deletion, storage accounting,
  whether a live output reports its destination's state (README Q13), and that outputs survive a
  reconnect and a new broadcast on the same input.
