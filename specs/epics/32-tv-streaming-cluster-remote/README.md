# Epic 32 — Maktaba TV across the cluster, and remote viewing through Cloudflare Stream

> **Status:** spec. **Source:** `specs/epics/32-tv-streaming-cluster-remote/`.
> **Anchors:** [Epic 27 — Live Channels](../27-live-channels/README.md) (the channel engine,
> HDHomeRun, XMLTV/M3U), [Epic 31 — Multi-instance](../31-multi-instance/README.md) (home and
> nodes, database roles, path maps), [Epic 25 — Cloud relay](../25-cloud-relay/README.md)
> (accounts, the tunnel, LAN-first client routing), [Epic 08 — Streaming](../08-streaming/README.md).
> **Builds on the in-flight branch `feat/tv-core`** (encoder settings in the database, VideoToolbox,
> the NVENC session cap, the 7-day guide horizon, the live SSDP toggle, `channel_runtime` as a
> cluster-wide record — slot 0117, `tv_core_runtime`, still in flight).

**Documents.** [design.md](design.md) (balancer, leases, front door, failover, the Cloudflare push
pipeline, tokens, app routing, simulcast) · stories `story-32-01` … `story-32-13` · plans `plan-32-01` … `plan-32-13`.

## Goal

Maktaba TV today encodes every channel on the one host that runs `maktaba-server serve`, and it
can only be watched on the LAN. This epic does two things:

1. **One TV service across the cluster.** The home (a Mac Studio M3 Ultra, VideoToolbox) streams
   by default. A setting lets other machines of the cluster stream too (for example the Windows PC
   with NVENC) "for more users". The home balances channels across the hosts that may stream:
   **one encode per channel in the whole cluster**, whatever the number of viewers. The home stays
   the **single front door** for everything a client sees — HDHomeRun discovery (SSDP,
   `discover.json`, `lineup.json`, `/auto/v{n}`), XMLTV, M3U and the web app's HLS — and forwards
   each request to the host that encodes the channel. When a host dies, its channels move to
   another host and resume at the wall-clock position.
2. **Remote viewing through Cloudflare Stream.** A viewer outside the LAN tunes a channel through
   the Maktaba API (reached through the Epic 25 relay). The host that already encodes that channel
   pushes the same encoded stream (stream copy, no second encode) to a Cloudflare Stream live
   input; the viewer plays it from Cloudflare's edge with a short-lived signed token. Pushes start
   when a remote viewer tunes and stop a few minutes after the last one leaves, under limits and a
   monthly budget the admin sets.

The video bytes for remote viewers never cross the Maktaba relay: the relay carries only the API
calls (guide, tune, heartbeat). Cloudflare delivers the video and adds the adaptive bitrate ladder
that the LAN stream does not have.

## Decisions already made (owner)

| Question | Decision |
|---|---|
| Who streams | **The home, by default (VideoToolbox).** Other machines stream only when a setting allows it, to carry more channels at once. |
| Balancing | **The home balances, one encode per channel cluster-wide.** |
| Front door | **The home only**, for HDHomeRun/SSDP, XMLTV, M3U and the web HLS path. It proxies (or redirects) to the encoding host. |
| Failover | **Move the channel to another host at the wall-clock position.** |
| Remote | **Cloudflare Stream Live**: live inputs (RTMPS/SRT ingest), HLS/DASH playback, signed tokens, optional webhooks (polling is the truth), simulcast available. |
| Recording | **Mode `automatic`** (Cloudflare serves live HLS only then); **each recording is deleted as soon as its broadcast ends**; the account is sized for the **$5/month 1,000-minute storage block** (Q1). |
| Upload and remote channels | The home's upload is **1 Gb/s**: no separate remote encode; ≈ 6.5 Mb/s per remote channel; **default max 22 concurrent remote channels** (every channel; ≈ 143 Mb/s, ~15 % of the uplink). Encoder capacity, not bandwidth, is the limit (Q4). |
| Remote viewers | **All signed-in users**, **2 concurrent remote streams each** (a setting) (Q6). |
| Budget at 100 % | **Refuse new remote tunes; running sessions finish** (Q8). |
| Simulcast | **Added (2026-10-18):** a channel can be simulcast to YouTube, Facebook or any RTMP/RTMPS destination through Cloudflare live outputs, always-on or on a schedule, with a rights confirmation and its own cap (R13, story 32.13). |
| Other questions | Q5, Q7, Q9–Q12: the spec's defaults, accepted (revisit if needed) — see "Open questions — decided". |

## Scope

- Streaming on cluster nodes: the setting, a node's streaming role, its capacity and encoders.
- A placement service on the home: leases, heartbeats, fencing, the balancing policy.
- The front door on the home for every TV entry point (proxy by default; redirect only for HLS
  and only when chosen).
- Failover and drain.
- Admin pages for streaming hosts and channels on air.
- A Cloudflare Stream integration (credentials, test connection, signing keys), moving every
  extension secret (the Plex token too) into `home_secrets` (Q7), the live input pool, the push from the encoding host, signed playback, the remote session API.
- LAN-or-remote routing in the web app first; mobile and TV apps after.
- Simulcast of a channel to YouTube, Facebook or any RTMP/RTMPS destination through Cloudflare live
  outputs, always-on or on a schedule (32.13).
- Cost controls, metrics, the runbook.

## Non-goals

- Re-encoding for remote viewers (a lower "remote rendition"). v1 pushes the channel's own encode;
  Cloudflare builds the quality ladder. Not needed: the home's upload is 1 Gb/s (Q4, decided).
- Remote HDHomeRun, remote XMLTV/M3U for third-party players, remote Plex. **Plex Live TV remote
  viewing is Plex's own feature**: Plex pulls `/auto/v{n}` from the front door on the LAN and
  serves its remote users itself. Maktaba does nothing extra for it beyond keeping `/auto/v{n}`
  working (and those Plex sessions count as tuner leases as today).
- On-demand video (Epic 08 sessions) on nodes or through Cloudflare. TV channels only.
- Recording/DVR. Cloudflare recordings are deleted as soon as each broadcast ends.
- WebRTC (WHIP/WHEP) delivery: Cloudflare does not play RTMPS/SRT inputs over WHEP, and Maktaba's
  encoder output is MPEG-TS. Not used.
- Creating or editing YouTube/Facebook events (title, description, privacy) through their APIs:
  simulcast (32.13) sends the media only; the event is set up on the platform.
- Running a second front door (two homes). There is one home (Epic 31).

---

## Requirements

### R1 — Streaming hosts
- R1.1 The home is always a streaming host. A cluster node (Epic 31) becomes one only when the
  cluster setting `tv_cluster.nodes_stream` is on **and** the admin enables streaming for that
  machine. Both are off by default (today's behaviour: the home streams everything).
- R1.2 A streaming node runs the streaming role of `maktaba-server` with the credentials it got
  when it joined (no second pairing). It refuses to start the streaming role when its storage
  check (Epic 31 §6) cannot read the library.
- R1.3 Each streaming host reports, every 5 s: encoders it can use (videotoolbox, nvenc, software),
  its capacity (channels it may carry at once, from the `feat/tv-core` output settings), the
  channels on air, CPU/GPU load, its stream base URL, its code version, whether it can push to
  Cloudflare (ffmpeg with TLS for RTMPS, libsrt for SRT) and run live blocks (yt-dlp).
- R1.4 A node never advertises HDHomeRun (no SSDP, its `discover.json`/`lineup.json`/`/auto`
  answer 404) and serves channel streams only to the front door (signed requests).
- R1.5 A node encodes with the channel's look: art files (logo, watermark, loading picture) are
  fetched from the home and cached; media paths go through the node's path map.

### R2 — Placement and balancing
- R2.1 A channel is encoded by at most one host in the cluster at any time. Every station start is
  fenced by the placement's generation number; a host whose generation is stale stops its station
  within 5 s.
- R2.2 The home places a channel when the first request for it arrives (LAN viewer, tuner, remote
  push) and releases it when the channel is reaped (no viewers past the warm grace).
- R2.3 Policy `home_first` (default): place on the home until the home is at `spill_at` percent of
  its capacity (default 100), then on the enabled node with the lowest load ratio. Policy
  `balanced`: lowest load ratio across all hosts, the home included. A per-channel pin
  (preferred host) wins while that host is healthy and has room.
- R2.4 A placed channel is sticky: it is never moved to rebalance load. It moves only on failover,
  drain, an admin "move", or a pin change while it is idle.
- R2.5 When no host has room: the request gets today's answer (HLS 503 with `Retry-After`; the
  HDHomeRun "all tuners in use"); the admin page shows why.
- R2.6 Capacity counts what an encoder can take: a host's `EffectiveMaxActive`, and for NVENC the
  session cap (each channel holds up to two sessions at a block boundary).

### R3 — Front door
- R3.1 Clients only ever see the home's addresses. SSDP, `discover.json`, `lineup.json`, XMLTV and
  M3U are served by the home from the database, as today.
- R3.2 `/auto/v{n}` (Plex, Jellyfin, Emby): always **proxied** by the home. Tuner leases are counted
  on the home. Plex keeps one stable `DeviceID` and one `BaseURL`.
- R3.3 Web HLS (`/stream/channel/{id}/…` on the web port and the streaming port): **proxied** by
  default. Setting `tv_cluster.hls_mode = redirect` makes the home answer segment requests with a
  307 to a signed, short-lived node URL (playlists stay proxied).
- R3.4 The front door adds no buffering: playlists are passed through, MPEG-TS is flushed per write.
- R3.5 The front door authenticates itself to nodes with a cluster stream key (HMAC on method,
  path, expiry). A node rejects unsigned stream requests.

### R4 — Failover and drain
- R4.1 A host that misses heartbeats for `dead_after` (default 15 s), or whose proxied requests fail
  three times in a row, is marked down; its channels are re-placed at once.
- R4.2 The new station joins the channel at the wall-clock position (Epic 27 join). HLS viewers see a
  discontinuity and continue; a proxied `/auto/v{n}` connection is spliced onto the new host
  without closing the client's connection (up to 3 times per connection).
- R4.3 Drain: an admin drains a host; it takes no new channels; its channels move when idle, or at
  once with "move now".
- R4.4 A Cloudflare push on a failed host is restarted on the new host on the same live input within
  the input's reconnect window, so remote viewers keep the same playback URL.

### R5 — Admin
- R5.1 Admin → Live TV gains a **Streaming hosts** section: each host, its role, state (online,
  down, draining, disabled), encoders, capacity and use, the channels it carries, last heartbeat,
  "stream on this machine" switch, drain, capacity override.
- R5.2 An **On air** list: channel, host, encoder, viewers (HLS, tuners, remote), since, restarts,
  NVENC fallbacks, push state; actions: move, stop.
- R5.3 English and Arabic.

### R6 — Cloudflare Stream integration
- R6.1 The admin enters the Cloudflare account id and an API token (permissions: Stream Write;
  Account Analytics Read for usage). They are stored on the home only, encrypted at rest, never
  readable by node database roles, never returned by the API.
- R6.2 "Test connection" checks the token, the Stream subscription and storage, creates the RS256
  signing key if none exists (kept on the home), learns the customer playback domain, and checks
  that every streaming host can push (ffmpeg protocols).
- R6.3 Remote viewing is off until the integration tests green and `tv_remote.enabled` is on.

### R7 — Live inputs and the push
- R7.1 Maktaba keeps a pool of live inputs (created on demand, at most `tv_remote.max_channels`),
  each named `maktaba-<instance>-<n>`, recording mode `automatic` (required for HLS playback),
  `requireSignedURLs: true`, reconnect window 30 s, disabled while idle.
- R7.2 When a remote viewer tunes a channel that is not pushing: take an idle input, enable it,
  make sure the channel is placed, and have the encoding host push its MPEG-TS (stream copy) to the
  input over RTMPS (default) or SRT. AC-3 audio is transcoded to AAC in the push only.
- R7.3 A channel is pushed at most once, whatever the number of remote viewers.
- R7.4 After the last remote viewer leaves, the push continues for `idle_timeout` (default 3 min),
  then stops; the input is disabled; the broadcast's recording is deleted from Cloudflare.
- R7.5 A push longer than `max_push_hours` (default 8) is rotated (new broadcast, viewers re-tuned
  by their next heartbeat) so that one recording never grows past the storage plan.

### R8 — Signed playback for remote viewers
- R8.1 `POST /api/tv/remote/tune` (signed-in user allowed to watch remotely) returns an HLS URL on
  the Cloudflare customer domain carrying a token signed on the home (RS256, `sub` = the broadcast's
  video id, `exp` = now + `token_ttl`, default 60 min, at most 24 h, optional country rules).
- R8.2 The client heartbeats every 60 s; the heartbeat keeps the push alive and returns a fresh
  token once the current one is past half its life.
- R8.3 Ending a session (or missing two heartbeats) frees the viewer's slot.
- R8.4 Rotating the signing key invalidates every token at once (panic button).

### R9 — App routing
- R9.1 A client on the LAN plays from the home (as today). A client that reached the API through the
  relay (or from a non-private address, when the admin allows that) plays from Cloudflare.
- R9.2 Web first: the live player uses hls.js (lazy-loaded) for both paths and swaps tokens without a
  reload. Native apps (iOS/Android/tvOS/Android TV) follow, using the Epic 25.10 LAN probe.
- R9.3 The guide and channel list for remote clients are the existing JSON APIs through the relay.

### R10 — Cost and limits
- R10.1 Limits: concurrent remote channels (`max_channels`, default 22 = every channel, 1–50), remote streams per user
  (default 2), which users may watch remotely (default: every signed-in user), allowed countries.
- R10.2 A monthly budget (USD, default 10) estimated from delivered minutes (Cloudflare analytics)
  and the storage plan; at 80 % the admin is notified; at 100 % new remote tunes get 402 (running
  sessions finish), or only a warning if `budget_action = warn`.
- R10.3 There is no "audio-only" saving: Cloudflare bills minutes delivered, not bytes.

### R11 — Observability
- R11.1 Metrics: hosts and their state, channels on air per host and encoder, placements and
  failovers, front-door bytes, remote viewers, pushes by state, push errors by code, minutes
  delivered (month), cost estimate (month).
- R11.2 Events in the logs and the control centre: placement, failover, push start/stop/error,
  budget thresholds, Cloudflare errors.

### R12 — Docs
- R12.1 A runbook: enable a streaming node, read the Streaming hosts page, set up Cloudflare (token
  permissions, storage purchase), test, enable remote, what to do when Cloudflare is down, cost
  examples, Plex remote, simulcast.

### R13 — Simulcast (story 32.13)
- R13.1 The admin defines destinations (YouTube Live, Facebook Live, custom RTMP/RTMPS: name,
  ingest URL, stream key in `home_secrets`) and attaches them to a channel, `always` or on a
  `schedule` (weekly windows, while a programme or series airs, during live slots).
- R13.2 The channel is pushed to Cloudflare once (R7.3) and Cloudflare retransmits it to each
  destination as a live output of the push's input (up to 50 per input); outputs are created when
  a window starts and deleted when it ends, so a pooled input never keeps another channel's outputs.
- R13.3 A simulcast holds the push: it is not stopped by the idle timeout while a window is
  active; remote viewers share it. Start/stop and status per output on the Live TV page.
- R13.4 The push and its outputs survive block changes, muxer restarts, Epic 33 hot swaps,
  Epic 32 failovers and rotations: one continuous push session; the outputs stay attached to the
  live input.
- R13.5 Simulcast minutes are minutes delivered: they count towards the monthly budget and towards
  a separate cap in output-hours (`tv_simulcast.max_hours_per_month`).
- R13.6 Turning a simulcast on needs the admin's confirmation that they hold the rights to what the
  channel airs (YouTube/Facebook copyright matching); nothing is simulcast unless attached.

## Acceptance (epic)

- With the PC enabled as a streaming node and the home at its cap, the next channel tuned in Plex
  plays from the PC through the home's `/auto/v{n}`; Plex never sees the PC's address.
- Killing the PC's `maktaba-server` moves its channels to the home within 20 s; Plex resumes
  without re-tuning; a browser viewer continues after a short stall at the right programme position.
- With Cloudflare set up and remote enabled, a phone on mobile data opens
  `https://<user>.maktaba.app`, tunes channel 105, and plays it from `customer-….cloudflarestream.com`
  within 25 s; a second phone tuning 105 does not start a second push.
- Three minutes after both phones stop, the push ends, the input is disabled and the recording is
  gone from the Cloudflare account.
- A fourth remote channel when `max_channels = 3` (and a 2,000-minute storage plan, room for four
  full 8 h pushes) gets 429 `remote_channels_full`; a user over the budget gets 402.

---

## Architecture

```
                         LAN                                                     Internet
 ┌──────────────── HOME (Mac Studio, VideoToolbox) ─────────────────┐
 │ web port   ── /stream/ gate ──────┐                               │   ┌── Epic 25 relay (Hetzner) ─┐
 │ streaming port ─ FRONT DOOR ◄─────┘   SSDP / discover / lineup    │   │ https://<user>.maktaba.app │
 │   │  placement service (balancer, leases, fencing)                │◄──┤  API only: guide, tune,    │
 │   │  HDHR /auto/v{n}  → proxy  ───────────────┐                    │   │  heartbeat (JSON)          │
 │   │  HLS /stream/channel/… → proxy|redirect ──┤                    │   └─────────────▲──────────────┘
 │   │  local engine (stations on the home)      │                    │                 │ API calls
 │ API   /api/tv/cluster/*, /api/tv/remote/*     │                    │          ┌──────┴───────┐
 │   │  remote controller: input pool, pushes,   │                    │          │ remote viewer│
 │   │  token minting, budget, analytics poll    │                    │          │ web / apps   │
 │   │  home_secrets (CF token, signing key)     │                    │          └──────▲───────┘
 │ Postgres: tv_stream_hosts, channel_placements, channel_runtime,   │                 │ HLS (signed token)
 │           tv_remote_*  ◄── node role via SECURITY DEFINER funcs   │   ┌─────────────┴──────────────┐
 └──────────────▲─────────────────────────────────▲──────────────────┘   │ Cloudflare Stream          │
                │ SQL (Epic 31 role) + NOTIFY      │ signed HTTP         │  live inputs (pool)        │
 ┌──────────────┴──── NODE (Windows PC, NVENC) ────┴──────────────┐      │  HLS/DASH + ABR ladder     │
 │ maktaba-server serve --role streaming (node mode)              │      │  customer-<code>.cloudflare│
 │   stations (only channels placed here, fenced by generation)   │      │  stream.com/<token>/…      │
 │   hub → pusher (ffmpeg -c copy → RTMPS/SRT) ───────────────────┼─────►│                            │
 │   asset cache (channel art from the home), path map (Epic 31)  │      └────────────────────────────┘
 └────────────────────────────────────────────────────────────────┘
 Plex / Jellyfin / VLC / browsers on the LAN talk to the HOME only.
 Any streaming host (home included) pushes the channels it encodes.
```

## Data model (proposed migrations)

Slots 0116–0118 are taken: 0116 (`plex_livetv`, PR #61) and 0118 (`channel_edited_at`, PR #59)
are merged on `main` (its highest slot is 0118), and 0117 (`tv_core_runtime`, `feat/tv-core`) is
in flight. Epic 32's slots therefore start at **0119**. Nothing is added to `MANIFEST.md` until the
files exist (its rule: next free integer, no reservations); the rows go in with each plan's PR, so
these numbers are provisional and move up if other work lands first.

| Slot | Story | File | Tables / changes |
|---|---|---|---|
| 0119 | 32.6 (first, small) | `0119_home_secrets.sql` | `home_secrets` (name pk, ciphertext bytea, nonce bytea, key_id, updated_at, updated_by): AES-256-GCM sealed by the home's API with a key file in the home's config directory (never in the database); REVOKE ALL from `maktaba_worker`. Holds the Cloudflare API token, the signing key PEM, the live inputs' stream keys. |
| 0120 | 32.1, 32.2 | `0120_tv_stream_cluster.sql` | `tv_stream_hosts` (host_id pk = `instance_identity.instance_id` or `cluster_nodes.id`, role home/node, name, enabled, draining, stream_base_url, encoders jsonb, capacity, capacity_override, weight, active, load jsonb, can_push jsonb, can_live, unfit, version, last_beat_at); `channel_placements` (channel_id pk, host_id, generation bigint, state placing/live/moving/released, reason, placed_at, acked_at, released_at); sequence `tv_placement_generation_seq`; `tv_cluster_secret` (singleton: the cluster stream key, REVOKE from `maktaba_worker`); functions `tv_host_beat(...)` (returns the stream key to the caller's own host) and `tv_placement_ack(...)` (SECURITY DEFINER, own row only); `maktaba_worker` gets SELECT only on hosts and placements. |
| 0121 | 32.7, 32.8, 32.10, 32.13 | `0121_tv_remote.sql` | `tv_remote_inputs` (id, cf_uid, name, state, channel_id, ingest jsonb (no secret), last_video_uid, assigned_at, errors); `tv_remote_pushes` (id, channel_id, input_id, host_id, generation, state, video_uid, started_at, live_at, ended_at, end_reason, minutes_delivered, recording_deleted_at); `tv_remote_sessions` (id, user_id, channel_id, push_id, device, started_at, last_beat_at, ended_at, token_exp); `tv_remote_usage_daily` (day, minutes_delivered, peak_recording_minutes, cost_usd); `tv_push_secrets` (push_id pk, ingest url, key, expires_at — written by the home API for a running push only, deleted when it ends, REVOKE from `maktaba_worker`); function `tv_push_credentials(push_id)` returns that row only to the push's host role; `tv_push_report(...)`. Simulcast (32.13, here if it ships with 32.7, else the next free slot): `tv_simulcast_destinations` (id, name, kind, ingest_url, key_secret → `home_secrets`), `tv_simulcast_attachments` (channel, destination, mode always/schedule, rules jsonb, enabled, manual override, rights_confirmed_by/at), `tv_simulcast_outputs` (attachment, push, input, cf_output_uid, state, window, minutes, error); `tv_remote_pushes.holds` (viewers, simulcast); `tv_remote_usage_daily.simulcast_minutes`. |

`channel_runtime` (slot 0083, made cluster-wide by 0117 on `feat/tv-core`) stays the record of *what
is actually on air where*, written by the encoding host; `channel_placements` is *what the home
decided*. Settings are rules sections in `app_settings` (as `rules.tv` / `rules.tv_output` on
`feat/tv-core`): `rules.tv_cluster`, `rules.tv_remote`.

## Settings (defaults keep today's behaviour)

| Section.key | Default | Meaning |
|---|---|---|
| `tv_cluster.nodes_stream` | `false` | Machines other than the home may stream. |
| `tv_cluster.policy` | `home_first` | `home_first` \| `balanced`. |
| `tv_cluster.spill_at` | `100` | `home_first`: percent of the home's capacity used before spilling. |
| `tv_cluster.hls_mode` | `proxy` | `proxy` \| `redirect` (segments only). |
| `tv_cluster.failover` | `true` | Re-place channels of a dead host. |
| `tv_cluster.dead_after_seconds` | `15` | Missed-heartbeat window. |
| `tv_cluster.pins` | `{}` | `{channel_slug: host_name}` preferred host. |
| per host (`tv_stream_hosts`) | home on, nodes off | "Stream on this machine", capacity override, weight, drain. |
| `tv_remote.enabled` | `false` | Remote viewing through Cloudflare. |
| `tv_remote.max_channels` | `22` | Concurrent pushes (size of the input pool), 1–50; ≈ 6.5 Mb/s of upload each (22 ≈ 143 Mb/s of the 1 Gb/s uplink). |
| `tv_remote.max_streams_per_user` | `2` | Concurrent remote sessions per user. |
| `tv_remote.viewers` | `all` | `all` \| `admins` \| list of user ids. |
| `tv_remote.idle_timeout_minutes` | `3` | Keep a push after its last remote viewer. |
| `tv_remote.token_ttl_minutes` | `60` | Signed token lifetime (5–1440). |
| `tv_remote.ingest` | `rtmps` | `rtmps` \| `srt`. |
| `tv_remote.low_latency` | `false` | Cloudflare LL-HLS beta (`preferLowLatency`). |
| `tv_remote.max_push_hours` | `8` | Rotate longer broadcasts. |
| `tv_remote.monthly_budget_usd` | `10` | Estimated spend cap. |
| `tv_remote.budget_action` | `block_new` | `block_new` \| `warn`. |
| `tv_remote.delivery_usd_per_1000_min` | `1.00` | Price used by the estimate. |
| `tv_remote.storage_plan_minutes` | `1000` | Prepaid storage the estimate and the guard assume (one $5/month block; ≈ 2 concurrent 8 h pushes per block). |
| `tv_remote.allowed_countries` | `[]` | ISO codes; empty = any (token access rules). |
| `tv_remote.remote_detection` | `relay` | `relay` (requests through the tunnel) \| `relay_or_public_ip`. |
| `tv_simulcast.enabled` | `false` | Simulcast to destinations (32.13); needs the Cloudflare integration, independent of `tv_remote.enabled`. |
| `tv_simulcast.max_outputs_per_channel` | `3` | Destinations attached to one channel (Cloudflare allows 50 per input). |
| `tv_simulcast.max_hours_per_month` | `60` | Output-hours per month across all destinations; at 100 % no new output starts. |
| `tv_simulcast.lead_seconds` / `tail_seconds` | `60` / `30` | Start an output before a scheduled window, stop it after. |

## APIs

```
# Cluster (admin) — API on the home
GET    /api/tv/cluster                         hosts, placements, why a host is not taking channels
PUT    /api/tv/cluster/hosts/{host_id}         {enabled?, drain?, capacity_override?, weight?, stream_base_url?}
POST   /api/tv/cluster/channels/{id}/move      {host_id?}  (empty: let the balancer choose)
DELETE /api/tv/cluster/channels/{id}/placement stop the channel everywhere

# Remote (signed-in users, through the relay)
POST   /api/tv/remote/tune                     {channel_id, device?} → 200 {session_id, hls_url, expires_at,
                                               refresh_after, channel} | 202 {state:"starting", retry_after}
                                               | 402 budget_exhausted | 403 remote_not_allowed
                                               | 429 remote_channels_full|user_streams_full | 503 remote_unavailable
POST   /api/tv/remote/sessions/{id}/heartbeat  → {expires_at, token?, hls_url?, state}
DELETE /api/tv/remote/sessions/{id}

# Remote (admin)
GET    /api/tv/remote                          status: integration, inputs, pushes, sessions, month usage, budget
POST   /api/tv/remote/test                     test connection (R6.2)
POST   /api/tv/remote/pushes/{id}/stop
POST   /api/tv/remote/signing-key/rotate       invalidates every token
POST   /api/tv/remote/webhook                  Cloudflare Notifications (public, secret header; optional)

# Simulcast (admin, 32.13)
GET    /api/tv/simulcast                       destinations, attachments, outputs (status), output-hours vs cap
POST   /api/tv/simulcast/destinations          {name, kind: youtube|facebook|rtmp, ingest_url, stream_key}
PUT    /api/tv/simulcast/destinations/{id}     DELETE /api/tv/simulcast/destinations/{id} (409 while in use)
GET    /api/channels/{id}/simulcast            the channel's attachments and next windows
PUT    /api/channels/{id}/simulcast            [{destination_id, mode: always|schedule, rules, enabled,
                                               rights_confirmed?}] | 409 rights_confirmation_required
POST   /api/tv/simulcast/outputs/{attachment_id}/start|stop   manual override until the next boundary

# Extended
GET    /api/tv/info                            + {cluster:{hosts_on_air}, remote:{available, reason, via_relay}}

# Internal (signed with the cluster stream key; never through the relay)
GET    {node}/stream/channel/{id}/…            stations on a node, front door only
GET    {node}/internal/tv/health
GET    {home}/internal/tv/assets/{channel}/{kind}   channel art for a node's station
POST   {home-streaming}/internal/tv/ensure/{channel}  loopback: API asks the front door to place/warm
```

## Security

- **Secrets.** The Cloudflare API token, the signing key (PEM), the live inputs' stream keys and
  every other extension secret (the Plex token, today in `extensions.settings`; Q7) live
  in `home_secrets` (sealed by the home's API with a key file that never enters the database;
  REVOKE from `maktaba_worker`). For a running push only, the API writes that input's ingest URL
  and key to `tv_push_secrets` (revoked from workers, deleted when the push ends); a node reads it
  only through `tv_push_credentials(push_id)`, which checks that `session_user` is the database role
  of the host the push is placed on (the Epic 31 pattern), and keeps it in memory only.
- **Front door ↔ nodes.** HMAC-SHA256 over `method + path + expiry` with the cluster stream key
  (in `tv_cluster_secret`, handed to each host by `tv_host_beat`); requests expire after 60 s; a node's
  streaming port answers nothing else (no HDHR, no XMLTV, no unsigned `/stream`).
- **Remote viewers.** A Maktaba session (local account, through the relay) is required to tune. The
  Cloudflare token is per broadcast, short-lived, optionally country-restricted. Inputs require
  signed URLs, so a leaked input id plays nothing. Rotating the signing key (R8.4) cuts everyone off.
- **Inputs at rest.** Idle inputs are disabled, so a leaked stream key cannot broadcast into the
  account; keys can be rotated from the admin page.
- **Webhooks** (optional) are accepted only with the configured secret, and only change state that
  the poller would also observe (they speed things up; they never grant anything).
- **Split brain.** Placement decisions take a Postgres advisory lock; stations are fenced by
  generation; only the home's streaming process (role `home`) answers SSDP.

## Cost (Cloudflare Stream pricing, as published)

Stream bills two things: **minutes delivered** ($1 per 1,000, counted per segment requested by a
player, ingest and encoding free) and **minutes stored** (prepaid, $5 per month per 1,000 minutes of
capacity). Live HLS playback needs recording mode `automatic`, so each broadcast is recorded while
it runs; Maktaba deletes the recording when the broadcast ends, so stored minutes stay near the
length of the broadcasts running at that moment. With no remote viewer a running push costs nothing
in delivery (but holds storage and an encoder slot).

| Example | Delivered / month | Delivery | Storage plan | Total |
|---|---|---|---|---|
| 1 viewer, 2 h/day | 3,600 min | $3.60 | $5 (1,000 min) | ≈ $8.60 |
| 2 viewers on the same channel, 3 h/day | 10,800 min | $10.80 | $5 | ≈ $15.80 |
| 3 viewers, different channels, 4 h/day | 21,600 min | $21.60 | $10 (2,000 min: 3 × 8 h rotation cap) | ≈ $31.60 |

Upload bandwidth at home: each pushed channel uses its bitrate once (≈ 6.5 Mb/s at the default
6 Mb/s video + audio), whatever the number of remote viewers. The home's upload is 1 Gb/s, so
the default `max_channels = 22` (every channel, ≈ 143 Mb/s, ~15 % of the uplink) fits easily;
what limits concurrent channels is the encoders (a channel is pushed only while it is encoded on
some host, R1/R2), not the bandwidth; on the default 1,000-minute
storage block there is room for about 2 concurrent full-length (8 h) pushes (Q1), so running more remote
channels at once means buying another block per two channels (or lowering `max_push_hours`).

**Simulcast (32.13).** Each simulcast output is billed like a viewer watching the whole time
("Simulcasting via SRT or RTMP live outputs" counts as minutes delivered): one output for one hour
is 60 minutes ≈ $0.06, an always-on output ≈ 43,200 min ≈ **$43 per 30-day month**, two outputs of
the same channel twice that. It adds no upload at home (Cloudflare fans out from the one push) but
the push holds one input and, while it runs, its recording counts towards the storage guard.
`tv_simulcast.max_hours_per_month` (default 60 output-hours ≈ $3.60) caps it separately from the
budget.

## Rollout

1. **Phase A — cluster streaming** (32.1–32.5): ships dark (`nodes_stream=false`; the home is the
   only host, so placement always answers "home" and the front door serves locally — today's
   behaviour). Turn on for the PC, watch the Streaming hosts page, test failover.
2. **Phase B — Cloudflare** (32.6–32.8, 32.10, 32.11): integration page and test; remote stays off
   until enabled; first on the web app.
   Simulcast (32.13) follows, behind `tv_simulcast.enabled` (off).
3. **Phase C — apps** (32.9 native part): iOS/Android/tvOS/Android TV.
4. **Docs** (32.12) with each phase.

## Stories

| # | Story | Summary |
|---|---|---|
| 32.1 | [Streaming nodes and capacity](story-32-01-streaming-nodes-capacity.md) | Setting + per-machine switch; node runs the streaming role from its join config; capacity, encoders, push ability reported by heartbeat; art fetched from the home. |
| 32.2 | [Channel placement and balancer](story-32-02-channel-placement-balancer.md) | One encode per channel cluster-wide; `home_first`/`balanced`; pins; generation fencing; sticky placements. |
| 32.3 | [Front door](story-32-03-front-door.md) | The home serves every entry point; `/auto/v{n}` proxied; HLS proxied or redirected; signed node requests. |
| 32.4 | [Failover and drain](story-32-04-failover-drain.md) | Dead host detection, re-placement at wall clock, TS splice, HLS discontinuity, drain and move. |
| 32.5 | [Admin: streaming hosts and on air](story-32-05-admin-hosts-on-air.md) | Live TV admin sections for hosts and channels on air, actions, en + ar. |
| 32.6 | [Cloudflare Stream integration](story-32-06-cloudflare-integration.md) | Credentials in `home_secrets`, test connection, signing key, customer domain, host push check. |
| 32.7 | [Live input pool and push](story-32-07-live-input-push.md) | Pool lifecycle, push from the encoding host (stream copy to RTMPS/SRT), keep-alive, rotation, recording cleanup. |
| 32.8 | [Signed playback for remote viewers](story-32-08-signed-remote-playback.md) | Tune/heartbeat/end API, RS256 tokens, limits checked at tune, key rotation. |
| 32.9 | [LAN or remote in the apps](story-32-09-app-lan-remote-routing.md) | Web player with hls.js on both paths and token swap; native apps through the 25.10 probe. |
| 32.10 | [Cost, limits and budget](story-32-10-cost-limits-budget.md) | Limits, monthly estimate from Cloudflare analytics, 80/100 % thresholds, storage guard. |
| 32.11 | [Observability](story-32-11-observability.md) | Metrics, events, control-centre health samples, admin usage panel. |
| 32.12 | [Docs and runbook](story-32-12-docs-runbook.md) | Setup, operations, failure playbook, costs, Plex remote. |
| 32.13 | [Simulcast to YouTube, Facebook and RTMP](story-32-13-simulcast.md) | Destinations (keys in `home_secrets`), always-on or scheduled attachments, Cloudflare live outputs on the channel's push input, rights confirmation, cap in output-hours, survives failover/rotation/hot swap. |

## Dependencies

- `feat/tv-core` (slot 0117 and the output settings): capacity, encoders, NVENC cap, cluster-wide
  `channel_runtime`. Epic 32 code starts after it merges.
- Epic 31 (merged): node identity, database roles, `SECURITY DEFINER` own-row functions, path maps.
- Epic 25 relay: the server-side tunnel agent must run inside `maktaba-server` and mark relayed
  requests (`X-Maktaba-Via: relay`, set by the agent on its loopback calls). Today
  `cloud/internal/cloudlink.LocalProxy` exists but `maktaba-server` does not start it — see Q5.
- Web player hls.js (audit P1-3) — folded into 32.9.

## Open questions — decided (owner, 2026-10-18)

Every question below is closed. "Owner decision" means the owner chose it; "accepted default,
revisit if needed" means the spec's recommendation stands as the default and can be revisited
later without changing the rest of the design. The decisions are folded into design.md and the
stories.

1. **Recording must be on for live HLS — owner decision.** Cloudflare plays a live input over
   HLS/DASH only with recording mode `automatic` ("off" = not recorded *and not available for
   playback*). Decided: recording mode **`automatic`**; Maktaba **deletes each recording as soon
   as its broadcast ends** (design §7.7); the account is sized for the **$5/month 1,000-minute
   storage block** (`tv_remote.storage_plan_minutes = 1000`). Because a running broadcast holds
   storage until it ends, one block has room for about **2 concurrent full-length pushes** at
   `max_push_hours = 8` (2 × 480 min ≤ 1,000); each further block adds about two. The storage
   guard (design §7.7) refuses a new push with 503 `remote_storage_full` once the running pushes'
   ages plus 480 min would exceed the plan, so on the default plan it, not `max_channels`, is
   usually the binding limit.
2. **Token subject.** The design signs tokens for the broadcast's video id (a new id per
   broadcast, so tokens die with it). Verify in the 32.6 spike that a signed token for the live
   video id plays the in-progress broadcast (the docs show live input ids and video ids both work as
   playback ids). Fallback: `sub` = live input id with an input cool-down of one token lifetime
   before reuse. *(Spike item, not an owner question; stays open until the 32.6 spike.)*
3. **Is `exp` checked on every segment?** The docs say rules are checked continuously. The web
   player swaps tokens without a reload (32.9); native apps re-tune at expiry. Confirm in the
   spike. *(Spike item, as Q2.)*
4. **Upload bandwidth — owner decision.** The home's upload is **1 Gb/s**, so no separate
   "remote rendition" encode is needed: each remote channel is pushed once at the channel bitrate
   (**≈ 6.5 Mb/s**, the default 6 Mb/s video + audio). `tv_remote.max_channels` defaults to **22**
   concurrent remote channels — every channel of the lineup (≈ 143 Mb/s, ~15 % of the uplink);
   the setting allows 1–50. **Encoder capacity, not bandwidth, is the limit**: a channel can be
   pushed only while a streaming host encodes it (R1/R2, `feat/tv-core` capacity) — and, on a
   single storage block, the storage guard (Q1).
5. **Remote API path without the relay agent — accepted default, revisit if needed.** The relay
   agent is wired into `maktaba-server` as part of the Epic 25 work before Phase B; until then, and
   for users with their own reverse proxy or tunnel, `remote_detection = relay_or_public_ip` ("a
   public client IP counts as remote") is an **opt-in**; the default stays `relay`.
6. **Who may watch remotely — owner decision.** **All signed-in users** (`tv_remote.viewers =
   all`), **2 concurrent remote streams per user** (`tv_remote.max_streams_per_user = 2`, a
   setting).
7. **Other secrets in `extensions` — accepted default, revisit if needed.** All extension secrets
   (the Plex token in `extensions.settings` today, and any future `secret` field) move to
   `home_secrets` in story 32.6, so node roles cannot read them.
8. **Budget at 100 % — owner decision.** **Refuse new remote tunes (402 `budget_exhausted`) and let
   running sessions finish**; nothing is cut mid-programme (`budget_action = block_new`, the
   default; `warn` remains a setting).
9. **Rotation at `max_push_hours` — accepted default, revisit if needed.** A short re-buffer for
   remote viewers every 8 h of a continuous push is acceptable; `max_push_hours` stays 8.
10. **Plex next to the PC — accepted default, revisit if needed.** Proxying `/auto/v{n}` makes
    PC-encoded bytes cross the LAN twice (PC → home → Plex, ≈ 6.5 Mb/s per tuned channel).
    Accepted; revisit only if the home's NIC is busy.
11. **LL-HLS beta — accepted default, revisit if needed.** `tv_remote.low_latency`
    (`preferLowLatency`) is **off** by default (beta; the encoder already uses 2 s GOP and no
    B-frames, which LL-HLS needs, so it can be switched on later).
12. **Webhooks need a public URL — accepted default, revisit if needed.** **Polling is the source
    of truth** (live input status and lifecycle); webhooks are only an optional speed-up through
    the relay URL.
13. **Simulcast output health (32.13).** Cloudflare documents an output as `uid`, `url`,
    `streamKey`, `enabled` only — no connection status, and no output events in the live webhooks.
    *Default:* Maktaba shows its own output state plus the input's status; the 32.6 spike checks
    whether the output object or Notifications report a destination's rejection, and 32.13 uses it
    if so. *(Spike item, as Q2/Q3.)* Simulcast itself was added by the owner on 2026-10-18
    (scope, R13).

## Cloudflare facts this design relies on (with sources)

| Fact | Source |
|---|---|
| Live inputs are created with `POST /accounts/{id}/stream/live_inputs`; response has `uid`, `rtmps {url, streamKey}`, `srt {url, streamId, passphrase}`, `webRTC`, `status`, `recording`, `enabled`, `preferLowLatency`, `deleteRecordingAfterDays`. API token needs Stream Write. | [Start a live stream](https://developers.cloudflare.com/stream/stream-live/start-stream-live/), [API: create live input](https://developers.cloudflare.com/api/resources/stream/subresources/live_inputs/methods/create/) |
| Ingest over RTMPS or SRT (SRT caller mode only). Inputs accept H.264 video and AAC audio (ADTS, not LATM); combined bitrate 2–14 Mb/s; closed GOP of 2–10 s (4–8 s recommended for HLS; 2–4 s and no B-frames for LL-HLS); encoders must reconnect on disconnect. | [Start a live stream](https://developers.cloudflare.com/stream/stream-live/start-stream-live/) |
| `recording.mode`: default `off` = not recorded **and not available for playback**; `automatic` = available over HLS/DASH and recorded. `timeoutSeconds` = how long the feed may drop before a new video is created. `requireSignedURLs`, `allowedOrigins`, `hideLiveViewerCount` per input. `deleteRecordingAfterDays` minimum 30. Recordings truncated after 7 days. | [Start a live stream](https://developers.cloudflare.com/stream/stream-live/start-stream-live/) |
| Inputs can be disabled/enabled (`PUT … {"enabled": false}` rejects RTMPS/SRT connections); broadcast keys can be rotated. | [Start a live stream](https://developers.cloudflare.com/stream/stream-live/start-stream-live/), [changelog 2026-02-24](https://developers.cloudflare.com/changelog/post/2026-02-24-disable-live-inputs/) |
| Playback URLs: `https://customer-<CODE>.cloudflarestream.com/<INPUT_ID|VIDEO_ID>/manifest/video.m3u8` (DASH `video.mpd`); a new video id per broadcast; `GET …/<INPUT_ID>/lifecycle` → `{isInput, videoUID, live}`; when viewing by input id the input's `requireSignedURLs`/`allowedOrigins` apply; recordings appear ~60 s after a broadcast ends. | [Watch a live stream](https://developers.cloudflare.com/stream/stream-live/watch-live-stream/) |
| Signed URLs: create a signing key once (`POST /accounts/{id}/stream/keys` → `id`, `pem`, `jwk`; up to 1,000 keys; deleting a key invalidates its tokens); sign RS256 tokens locally with `sub`, `kid`, `exp` (≤ 24 h), `nbf`, `accessRules` (≤ 5 rules: `ip.geoip.country`, `ip.src`, `any`); use the token in place of the id in the URL. The `/token` API is for testing or < 1,000 tokens/day. | [Secure your Stream](https://developers.cloudflare.com/stream/viewing-videos/securing-your-stream/) |
| Pricing: minutes delivered $1/1,000 (counted per segment request, rounded to the segment = GOP for live; preloading counts; simulcast counts); minutes stored prepaid $5 per 1,000 minutes; live recordings count as stored; running out of storage blocks new live streams; ingest and encoding free; a broadcast with no viewers costs nothing in delivery. | [Pricing](https://developers.cloudflare.com/stream/pricing/) |
| Live webhooks via Cloudflare Notifications: `live_input.connected`, `live_input.disconnected`, `live_input.errored` with codes `ERR_GOP_OUT_OF_RANGE`, `ERR_UNSUPPORTED_VIDEO_CODEC`, `ERR_UNSUPPORTED_AUDIO_CODEC`, `ERR_STORAGE_QUOTA_EXHAUSTED`, `ERR_MISSING_SUBSCRIPTION`, `ERR_UNHEALTHY`. Live input `status` values include `connected`, `reconnecting`, `client_disconnect`, `failed_to_connect`. | [Live webhooks](https://developers.cloudflare.com/stream/stream-live/webhooks/), [API: create live input](https://developers.cloudflare.com/api/resources/stream/subresources/live_inputs/methods/create/) |
| Simulcast: up to 50 concurrent outputs per input; `POST /accounts/{id}/stream/live_inputs/{input}/outputs` with `url`, `streamKey`, optional `enabled` (default on); outputs can be added, disabled or deleted during a broadcast (a deleted output disconnects within 30 s); response `uid`, `url`, `streamKey`, `enabled`. "Simulcasting via SRT or RTMP live outputs" is billed as minutes delivered. | [Simulcasting](https://developers.cloudflare.com/stream/stream-live/simulcasting/), [Pricing](https://developers.cloudflare.com/stream/pricing/) |
| Live viewer count: `GET https://customer-<CODE>.cloudflarestream.com/<INPUT_ID>/views` → `{liveViewers}`. | [Live viewer count](https://developers.cloudflare.com/stream/getting-analytics/live-viewer-count/) |
| Usage: GraphQL dataset `streamMinutesViewedAdaptiveGroups` (`sum.minutesViewed` by `uid`, `clientCountryName`), token with Account Analytics, 31-day query span, 90-day retention. | [Fetching bulk analytics](https://developers.cloudflare.com/stream/getting-analytics/fetching-bulk-analytics/) |
| LL-HLS is a beta (`preferLowLatency`, needs recording `automatic`); the Stream player uses it when possible. DVR is opt-in with `dvrEnabled=true` (HLS v8, not DASH, may degrade past 3 h). Sub-second SRT/RTMPS *playback* exists but needs an ffmpeg-based player. | [Using the Stream player](https://developers.cloudflare.com/stream/viewing-videos/using-the-stream-player/), [DVR for live](https://developers.cloudflare.com/stream/stream-live/dvr-for-live/), [Using your own player](https://developers.cloudflare.com/stream/viewing-videos/using-own-player/) |
| WebRTC (WHIP/WHEP) is GA but WHEP does not play RTMPS/SRT inputs, and WHIP inputs have no HLS. | [WebRTC](https://developers.cloudflare.com/stream/webrtc-beta/) |
| Keyframe interval should stay flat between 2 and 8 s; check the input's Metrics tab when buffering. | [Troubleshooting](https://developers.cloudflare.com/stream/stream-live/troubleshooting/) |

**Not documented (to verify in the 32.6 spike):** a numeric HLS glass-to-glass latency for
standard live HLS; per-account limits on concurrent live inputs; whether `exp` is checked on every
segment; whether a token whose `sub` is the in-progress video id plays the live broadcast.

**Epic 25 relay and Cloudflare.** The relay runs on Hetzner with Cloudflare only in front for DNS,
TLS and DDoS protection; tunnelled traffic bypasses Cloudflare and streaming endpoints bypass its
cache ([Epic 25 README](../25-cloud-relay/README.md), [plan 25-22](../25-cloud-relay/plan-25-22-subdomain-provisioning.md),
[plan 25-23](../25-cloud-relay/plan-25-23-tls-edge.md), [plan 25-24](../25-cloud-relay/plan-25-24-rate-limiting.md)).
It does not use Cloudflare Stream; the Stream account in this epic is the **user's own**
Cloudflare account. Remote TV video therefore never counts against relay bandwidth (Epic 25.11/25.12);
only the small API calls cross the relay.
