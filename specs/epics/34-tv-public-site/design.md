# Design — Maktaba TV public site (Epic 34)

Companion to [README.md](README.md). Section numbers are referenced by the stories and plans.
Epic 32 design sections are cited as "E32 §n" ([design](../32-tv-streaming-cluster-remote/design.md)).

## 1. Shape

```
 home (outbound only)                                Cloudflare (owner's account)
 ─────────────────────                               ─────────────────────────────────────────────
 tvpublic.Publisher ── HTTPS (HMAC) ──────────────►  Worker /_home/publish/* ──► R2 (blobs, index)
                                                                  └──► DO SiteControl: snapshot id ──► DO LiveHub[0..n] ──► open pages (WS)
 tvpublic.Control   ── WSS (HMAC) ────────────────►  Worker /_home/ws ──► DO SiteControl
      ▲ state reports up, commands down                  sessions, limits, budget, on-air map, wake requests
 tvpublic.Deployer  ── HTTPS (CF API token) ──────►  Cloudflare API: Workers scripts+assets, R2, DO, secrets,
                                                     Custom Domains, Turnstile widgets, Access apps, Stream keys
 tvremote (Epic 32) ── HTTPS (CF API token) ──────►  Stream API: public live inputs (A/B pairs), videos, views, GraphQL
 public pusher      ── RTMPS/SRT ─────────────────►  Stream ingest
                                                     visitors: Worker (HTML/JSON/WS) + Stream HLS (signed)
```

Three Maktaba components are new on the home side (`api/internal/tvpublic`): **Publisher**,
**Control** (the outbound WebSocket client) and **Deployer**. On the encoding hosts the station gets
a **public mux** and a **public pusher** (`streaming/internal/channel`). On Cloudflare there is one
Worker (`web/tv-site/worker`) with two Durable Object classes (`SiteControl`, singleton per
environment; `LiveHub`, sharded) and one R2 bucket.

Why these Cloudflare products:

| Need | Choice | Why not the others |
|---|---|---|
| Site + API | Worker with static assets | Cloudflare recommends Workers over Pages for new projects; one deployable unit; static requests free. |
| Published guide | R2 (immutable blobs + index) | Strongly consistent; 1 M writes/month free; no egress. KV is eventually consistent (up to 60 s) and the Free plan allows 1,000 writes/day; D1 adds a schema we do not need. |
| Coordination | Durable Object `SiteControl` | Single writer for sessions, counters, budget, snapshot id; WebSocket server with hibernation for the home. |
| Fan-out to pages | Durable Object `LiveHub` (sharded) | Hibernating WebSockets: idle pages cost no duration; ≤ 32,768 sockets per object. |
| Control channel | Home → DO WebSocket | Outbound only, instant both ways. Queues HTTP pull would also be outbound but needs a queues token on the home and adds poll latency to wake-ups. |
| Analytics | Workers Analytics Engine + Stream GraphQL | No personal data; Stream's own minutes are what is billed. |
| Abuse | Turnstile, Access, Rate Limiting binding | Native, no third party. |

## 2. Public channels, rights, exclusions (story 34.1)

### 2.1 Model

`tv_public_channels` row per channel that was ever public. `public = true` is accepted by the API
only together with `rights_confirmed = true` (the API stores `rights_confirmed_by = principal`,
`rights_confirmed_at = now`). Making a channel private clears nothing but `public`; the confirmation
stays for audit and must be ticked again next time (the UI pre-fills the note).

`slug` defaults to the channel slug (Epic 27), unique, `[a-z0-9-]`, editable; `public_id` =
`base32(HMAC-SHA256(site_salt, channel_id))[:16]`, `site_salt` in `home_secrets`, so ids cannot be
mapped back to the channel.

`tv_public_exclusions`: `kind ∈ {series, collection, item, tag}`, `ref_id`, `mode ∈ {exclude,
clear}`. Evaluation for a programme block `b` of channel `c`:

```
available(b) =
  policy == allow:  not any(exclude matches b)
  policy == deny:   any(clear matches b) and not any(exclude matches b)
matches: series → b.series_id; collection → b.item ∈ collection (Epic 26 collection membership);
         item → b.item_id; tag → b.item has tag
filler/bumper blocks (Epic 27.10): available unless their own item is excluded
live blocks (yt-dlp): available only with mode=clear on the item (policy-independent)
```

The function lives in one place (`tvpublic.Availability`) and is used by the publisher (guide
text) and the station (slate decision) so both always agree.

### 2.2 Admin

Admin → Live TV → **Public site**: master switch, deploy/publish state, channel table (public,
rights, slug, on air, host, viewers, pin), exclusions editor (pick series/collection/item/tag;
shows the next 7 days of slots affected), a "Preview public feed" link (what the public sees now),
**Panic off-air** button. "New on a public channel" badge lists series that entered a public
channel's schedule since the last rights review (R1.2), with "Mark reviewed" and "Exclude".

## 3. The publisher (story 34.2)

### 3.1 Inputs and output

Reads the **committed** schedule only (Epic 27 `program_blocks` within the horizon; Epic 33 staged
change sets are invisible until committed), channel metadata, logos, the exclusion function, branding
settings. Writes through the Worker's home endpoints, never directly to R2 (one validation point,
same code for staging and prod, no R2 S3 credentials on the home).

Channel-day blob (`application/json`, ≤ ~40 KB):

```json
{"schema":1,"channel":"k3m9…","date":"2026-10-18","tz":"Asia/Riyadh",
 "items":[{"s":"2026-10-18T17:00:00Z","e":"2026-10-18T17:52:10Z","t":{"ar":"…","en":"…"},
           "st":{"ar":"…"},"ep":"S2 E4","d":{"ar":"…"},"cat":"documentary","r":"PG",
           "a":true,"img":"b/9f…webp"}]}
```

`a:false` items carry the slate text instead of `t/st/d/img` (R1.4). Dates are the channel's local
days; the browser renders in the viewer's zone.

### 3.2 Triggers and real time (R2.2, R2.7)

```
events (Postgres NOTIFY tv_schedule_changed {channel_id, from, to, reason}):
  scheduler write_channels · horizon top-up · Epic 33 commit / hot swap · live-slot change ·
  rights/exclusion/public change · station drift report (actual block start/end differs > 5 s)
→ Publisher.debounce(channel, 2 s) → delta job (queue engine, kind tv_public_publish, mode delta)
     1. rebuild the channel-days touched by [from, to] (and today/tomorrow on drift)
     2. hash; HEAD /_home/publish/blob/{sha} (cached locally: set of shas known to be present)
     3. POST missing blobs
     4. POST /_home/publish/commit {base: <snapshot>, set: {"guide/<id>/<date>": sha, …}}
        → Worker: SiteControl.commit() checks base == current (else 409 → publisher retries
          with a full index), stores index delta in R2 (s/<new>/index.json = previous ⊕ delta),
          sets current snapshot, notifies LiveHub shards
full job every 6 h and on demand: all channels, all days, site.json, lineup.json, logos.
```

Budget for the 10 s target: debounce 2 s + rebuild ≤ 0.5 s + upload ≤ 1 s + commit ≤ 0.5 s +
fan-out ≤ 1 s + page fetch of the blob ≤ 0.5 s ≈ 5.5 s typical.

**Drift.** Epic 27 blocks are wall-clock anchored, but a live block or a late start can shift the
real start/end. The station reports `{block_id, actual_start, actual_end?}` to the API
(`channel_runtime`); if the difference is > 5 s, the publisher re-times the affected items for the
rest of that day (the schedule itself is not changed; only what the public guide shows).

### 3.3 Reading on the site (R2.8, R2.9)

- `SiteControl` is the source of truth for the current snapshot id; `GET /api/v1/version` returns
  `{snapshot, server_time}` from it. Blobs are immutable (`Cache-Control: public, max-age=31536000,
  immutable`); `index.json`, `site.json` and `lineup.json` are served by snapshot id
  (`/api/v1/s/<snapshot>/…`, immutable) so caches never serve a stale mix.
- The page computes **now/next and progress** from the blobs and `offset = server_time − Date.now()`
  measured at load and on every API response (`X-Server-Time`), re-rendering every second for the
  progress bar and at each item boundary. No request is needed for now/next.
- The page opens `wss://<site>/api/v1/live` (`LiveHub`), receives
  `{"type":"guide","channel":"k3m9…","date":"2026-10-18","sha":"…","snapshot":"…"}` and
  `{"type":"state","channel":"…","on_air":true,"slate":false,"viewers":12?}`, fetches the blob,
  redraws only that row. On reconnect it calls `/api/v1/version` and reloads the index if the
  snapshot moved. Fallback: poll `/api/v1/version` every 15 s.
- Links: every tile and guide cell is `<a href="/<lang>/watch/<slug>">` or
  `…/watch/<slug>?at=<start ISO>`. The SPA intercepts the click (client routing + switching, §8);
  without JS the Worker serves the page. Nothing references the home or the relay; a CI test scans
  the built assets and a test snapshot for any non-site, non-Stream, non-Turnstile host.

`?at=` semantics: `start ≤ now < end` → live; `now < start` → programme card with countdown, player
shows the channel live underneath (muted until the user taps) and a toast when it starts; `now ≥ end`
→ "aired at …" card + "watch live".

## 4. Live inputs for public channels (story 34.3)

Two inputs per public channel, created by the home's Cloudflare client (E32 §7.1) on first need:

```json
{"meta":{"name":"maktaba-<instance8>-pub-<slug>-a"},
 "enabled": true,
 "recording":{"mode":"automatic","requireSignedURLs":true,"timeoutSeconds":30,
              "allowedOrigins":["tv.example.com","maktaba-tv-site.<acct>.workers.dev"],
              "hideLiveViewerCount": true},
 "preferLowLatency": false}
```

`allowedOrigins` lists the production and staging hostnames (and the Maktaba app origin when R3.7
reuse is enabled); updated by the deployer on a domain change (R8.3). Stream keys go to
`home_secrets` (`cf_pub_<uid>_key`), the push reads them through `tv_push_credentials` exactly like
E32 §7.4. Input ids are sent to `SiteControl` over the control channel; they are not in R2.

Public inputs are **not** part of Epic 32's pool (`tv_remote.max_channels` does not count them) and
are never disabled while the channel is public; when it turns private both inputs are disabled, their
videos deleted, and they are kept for reuse.

## 5. The public feed on the encoding host (story 34.3)

### 5.1 Public mux

The Epic 27 station runs one encode per block and a persistent muxer that concatenates block outputs
into the channel's continuous MPEG-TS (the Hub). For a public channel the station starts a **second
muxer** ("public mux") with its own Hub (`publicHub`):

```
block k available    → bytes of block k are written to both muxers (no extra encode)
block k not available→ channel mux gets block k; public mux gets a slate block of the same
                       duration, encoded by a slate encoder with the channel's output settings
                       (same codec/profile/size/fps/GOP/audio as tv_output) from the slate image
                       + texts ("Not available online", next programme, start time) + silence or
                       the slate audio; very cheap (still image, software x264 ultrafast or the
                       host's hardware encoder)
```

Because both muxers re-stamp at block boundaries the same way the channel mux already does, the
public stream stays continuous. If a block starts already in progress (join, failover) the slate
block is cut to the remaining duration. A mid-block change of availability (an exclusion added while
the programme runs) switches at the next GOP: the station ends the current block segment on the
public mux early and starts the slate (or back), same mechanism as a hot swap (Epic 33).

Non-public channels do not run the public mux (zero cost).

### 5.2 Public pusher

Identical to E32 §7.4 but subscribed to `publicHub` and targeting the channel's active public input:
`ffmpeg -f mpegts -i pipe:0 -map 0:v:0 -map 0:a:0 -c:v copy -c:a copy|aac -f flv rtmps://…/<key>`.
With `rendition ≠ copy` it adds `-c:v <hw encoder> -s 1280x720 -b:v <kbps> -g <2 s> -bf 0` (one extra
encode, counted in capacity).

### 5.3 Always-on and tiering

`tiering = all_always_on` (default): for every public channel the API asks the placement service
(E32 §3) for a **pinned-on** placement with reason `public`, which is never reaped for lack of
viewers. `tiering = top_n`: only `always_on_count` channels (pinned first, then by the last 7 days'
public minutes) are always-on; the others are placed when `SiteControl` sends `wake{channel}` and
released `on_demand_idle_minutes` after their last public session.

### 5.4 Capacity (R3.5)

Each always-on public channel holds one encode slot 24/7 (plus one more with `rendition ≠ copy`,
plus a slate encode during exclusions). With 22 public channels: 22 encodes across the home (M3
Ultra VideoToolbox) and the PC (NVENC, session cap from `feat/tv-core`), balanced by Epic 32
(`home_first` spills to the PC). The API computes
`free = Σ capacity − Σ placed − reserve_slots`; when making a channel public would leave `free < 0`
it warns and asks for confirmation; a channel that cannot be placed reports `off_air(no_capacity)`.
The Streaming hosts page (E32 §R5) shows public placements with a "public" badge. **Capacity, not
uplink, is the limit** at the owner's 1 Gbps: 22 × 6.5 Mb/s ≈ 143 Mb/s.

### 5.5 A/B rotation (R3.3)

```
every rotate_hours per channel (staggered: channel i starts at i × rotate_hours / n):
  1. host starts a second pusher (same publicHub) to the idle input B
  2. lifecycle(B).live = true (poll 1 s, ≤ 60 s) → home sends SiteControl {channel, active:B}
  3. SiteControl: new tokens name B; each live session's next heartbeat returns hls_url(B)
     (heartbeat interval shortened to 15 s for sessions of a rotating channel)
  4. after max(2 × heartbeat, 90 s) and when sessions on A == 0 (or 5 min): stop pusher A,
     wait for A's recording (≤ 120 s), DELETE video, A becomes idle
```

Upload doubles for that channel for 2–5 min. If B fails to go live, rotation is abandoned (A keeps
running) and retried in 10 min; after 3 failures the admin is warned (storage grows).

### 5.6 Continuity

- **Epic 33 hot swap:** the swap changes which block the station feeds; both muxers continue →
  no break, the guide delta follows (§3.2).
- **Epic 32 failover:** the new host starts the station, the public mux and the pusher to the same
  active input; under 30 s gap Cloudflare continues the same video, tokens (sub = input id) stay
  valid, players stall then continue. Over 30 s: a new video on the same input; playback by input id
  continues after the player reloads the manifest (the player retries on a fatal error with the
  same URL).
- **Simulcast (32.13):** outputs are attached to the public input (both A and B, so rotation keeps
  them); they carry the slate too, which is correct for rights.
- **Epic 32 remote on a public channel:** the remote tune of a public channel returns a token (signed
  with the home key) for the public active input instead of starting a pool push (one push per
  channel, R3.7), subject to the native-app `allowedOrigins` check (README Q7).

## 6. Worker and Durable Objects (story 34.4)

### 6.1 Routes

```
assets (SPA, hashed files)                       served by the assets binding, run_worker_first for
                                                  "/", "/<lang>/*", "/embed/*" (HTML meta injection)
/api/v1/*                                         Worker → SiteControl / R2
/_home/*                                          HMAC → SiteControl / R2
/sitemap.xml, /robots.txt                         generated from lineup + settings
```

HTML responses are the SPA shell passed through `HTMLRewriter` to inject `<title>`, description,
OG/Twitter tags, `hreflang`, canonical, JSON-LD, `lang`/`dir`, and the inline boot data
(`snapshot`, `server_time`, access mode) — this is the "SEO basics" without a full SSR.

### 6.2 SiteControl state

```
snapshot: {id, published_at, guide_until}
channels: {public_id → {slug, input_a, input_b, active, on_air, slate, host_ok, viewers, tier}}
sessions: {sid → {channel, iph, started, last_beat, minutes_today, token_exp, mode_subject}}
counters: {by_iph → n, total → n}
budget:   {month, minutes_est, minutes_reconciled, usd_est, state: ok|80|100}
flags:    {off_air: bool, home_connected_at, schema}
```

Persisted in the DO's SQLite storage; sessions table pruned every minute (alarm). `iph` =
HMAC(daily salt, IP) — never the IP.

### 6.3 Play flow

```
POST /api/v1/play {channel}
  rate limit (binding, key iph:play)
  access check (mode, §7) → 403
  SiteControl.play(channel, iph, subject):
    off_air flag or home not connected for 60 s → 503 off_air{reason}
    channel not on_air → (tiering top_n and tier on_demand) ? send wake → 202 tuning{retry_after:2}
                                                            : 503 off_air{reason:"channel"}
    budget 100 % and block_new → 503 off_air{reason:"budget"}
    total ≥ max_concurrent_viewers → 429 full ; counters[iph] ≥ max_sessions_per_ip → 429 ip_limit
    session minutes_today ≥ viewer_daily_minutes → 429 daily_limit
    create session; mint token(active input); mint neighbour tokens (prev/next/last)
  200 {session_id, hls_url, expires_at, refresh_after, state:"live"|"slate", neighbours}
```

`state: "slate"` is informative (the player shows a small "not available online" chip; the video
is the slate itself).

### 6.4 Tokens

```
header {"alg":"RS256","kid":STREAM_KEY_ID}
claims {"sub": <active input uid>, "kid": STREAM_KEY_ID, "exp": now + ttl, "nbf": now − 30,
        "accessRules": [ {type:"ip.src", action:"allow", ip:[<viewer ip>/32 or /64]}?,
                         {type:"ip.geoip.country", action:"allow", country:[…]}?,
                         {type:"any", action:"block"} ]   -- only when a rule is present}
hls_url = https://customer-<code>.cloudflarestream.com/<token>/manifest/video.m3u8
```

Signed with WebCrypto (`RSASSA-PKCS1-v1_5`, SHA-256) from the PKCS#8 PEM in the Worker secret.
The site key is created by the home (`POST /accounts/{id}/stream/keys`) at first deploy and on
rotation, then set as Worker secrets via the API. Rotation (panic or scheduled): create new key →
set secrets → wait until the Worker version is live → delete the old key at Cloudflare. Tokens are
never logged.

### 6.5 Heartbeat

Every 60 s (15 s during a rotation of that channel): updates `last_beat`, adds minutes, returns a
fresh token after half the TTL, a new URL on rotation, `state` changes, or `403/503` to stop. Missing
150 s → session ended (alarm). `navigator.sendBeacon(DELETE)` on page hide.

### 6.6 Home control channel

`GET /_home/ws` with headers `X-Maktaba-Ts`, `X-Maktaba-Sig = HMAC(HOME_KEY, "GET\n/_home/ws\n"+ts)`.
`SiteControl` accepts it with `ctx.acceptWebSocket(ws, ["home"])` (one at a time; a new connection
replaces the old). Messages (JSON, `seq`-numbered, acked):

| Up (home → DO) | Down (DO → home) |
|---|---|
| `hello{version, schema, env}` | `welcome{snapshot, budget, off_air}` |
| `channels{list with input ids, active, on_air, slate}` (full on connect, then deltas) | `wake{channel}` (top_n only) |
| `rotate{channel, active}` | `idle{channel}` (no public session for idle minutes) |
| `budget{reconciled_minutes, usd}` (hourly, from Stream GraphQL) | `usage{minutes, sessions, peak}` (every 5 min) |
| `off_air{on}` | `alert{kind}` (limit hit, abuse) |

Ping/pong every 20 s via `setWebSocketAutoResponse` (does not wake the DO). The home reconnects
with backoff 1 s → 30 s. If the WebSocket cannot be established (proxy), the home falls back to
`POST /_home/state` + `GET /_home/poll` long-poll (25 s).

### 6.7 LiveHub

`LiveHub` shards `0..n−1` (`n` = 4 default); a page connects to shard `hash(session cookie or random)
mod n`. `SiteControl` sends each event to every shard (`stub.fetch` with the event); the shard
broadcasts to its sockets (`ctx.getWebSockets()`), which wakes it briefly. Messages are tiny; a
guide event never carries the guide itself (the page fetches the immutable blob, which the edge
caches once for everyone).

## 7. Access modes (story 34.7)

| Mode | Gate | Guide/lineup | Play |
|---|---|---|---|
| `public` | none | open | open (limits apply) |
| `turnstile` | Turnstile widget (managed mode) on first play; Worker calls siteverify (`secret`, `response`, `remoteip`, `idempotency_key`), then sets `__Host-mtv` cookie = HMAC-signed `{sid_seed, exp: 12 h}` (HttpOnly, Secure, SameSite=Lax) | open | cookie required |
| `access` | Cloudflare Access app on the site hostnames (deployer creates/updates it with `access_emails`, One-time PIN by default); the Worker validates `Cf-Access-Jwt-Assertion` (team certs, `aud`) | behind Access | behind Access |
| `accounts` | "Sign in with Maktaba" (OAuth 2.0 / OIDC at the Epic 25 cloud); Worker verifies the ID token, checks `sub` hash against `allowed_accounts` published in `site.json`; session cookie as above | behind sign-in | signed-in |

The embed page inherits the mode; in `turnstile` mode it shows the widget inside the frame on first
play. `indexable` is forced off for `access`/`accounts`.

## 8. Channel switching (story 34.6)

### 8.1 Module

`web/packages/tvplayer` (`@maktaba/tvplayer`, framework-light TypeScript + a React binding), used by:
the public site, the Maktaba web live player (Epic 27.7, which already specifies up/down/digits) and
Epic 32.9 remote playback. It owns: the input map, the channel ring (only channels the viewer may
watch; wrap at the ends), the two-element video pipeline, the tuning card, the mini-guide overlay,
token refresh hooks (`resolve(channel) → {url, expires_at}` supplied by the host app: the public
site calls `/api/v1/play`, the Maktaba app calls the LAN stream or `/api/tv/remote/tune`).

### 8.2 Controls

| Input | Action |
|---|---|
| ↑ / PageUp / on-screen "▲" / swipe up | next channel (higher number) |
| ↓ / PageDown / on-screen "▼" / swipe down | previous channel |
| digits | number entry overlay; Enter or 1.5 s idle commits; unknown number → "No channel N" |
| `L`, Backspace, on-screen "↺" | last channel |
| `G` / "Guide" button | open guide; `Esc` closes |
| `I` / tap on video | mini-guide overlay (now/next for all channels, scrollable, tap to tune) |
| TV remote (D-pad, CH+/CH−, number keys) | mapped through the same table (key codes per platform) |

Swipes are vertical on the video area only (horizontal is left to the browser/RTL); in RTL the
on-screen arrows keep "up = next". Rapid presses are debounced: the target channel moves
immediately in the UI (tuning card) and the tune is committed 300 ms after the last press.

### 8.3 Speed

```
t0 key press → tuning card of target (logo, now/next from the local guide) on top of the old video
           → resolve(target): neighbours already have a token from the last play/beat → no API call
              (otherwise POST /api/v1/play, ~50–150 ms at the edge)
           → hidden <video> B: hls.js loadSource(url) — manifest already prefetched → first segment
           → B has buffered ≥ 1 segment and canplay → swap B/A, fade the card, stop A (detach,
              DELETE old session) → prefetch manifests of the new neighbours
```

- **Prefetch manifests, not segments:** after each settle the player fetches the master and media
  playlists of prev/next/last (`fetch(url)`, no hls.js attach), refreshing every 10 s while idle so
  the live edge is known; hls.js is then started with `startPosition = −1` at the live edge. Stream
  bills segment requests (and preloaded segments), not playlists, so this costs nothing in delivery.
  The player never preloads segments of a channel the viewer has not chosen.
- **Make before break:** A keeps playing (and billing) until B is ready; the overlap is ≤ 1–2
  segments (2–4 s of one extra stream).
- **Tuning card** is shown within one frame (< 100 ms) for any switch; it stays until the first
  frame, so there is never a black screen; with a cold channel (`top_n` only) it shows "Starting
  channel…" with a progress hint and the programme on now.
- **Targets (R6.4):** warm < 2 s first frame (p75) — every public channel is warm by default
  (always-on); cold < 20 s, to be measured (Cloudflare documents a brief delay before a newly
  connected input plays). Live edge latency itself (standard HLS, 2 s GOP) is not what the switch
  time measures.
- Segment length: the encoder's 2 s GOP gives 2 s segments, which keeps the first-segment wait short.

### 8.4 Measurement

The player emits `switch{from, to, ms_to_card, ms_to_first_frame, path: warm|cold, prefetched}` to
Analytics Engine; the admin usage panel shows p50/p75/p95.

## 9. Budget and abuse (story 34.9)

- Estimate (errs high): `usd = (Σ session minutes since last reconcile + reconciled minutes) / 1000 ×
  delivery_usd_per_1000_min` (from `tv_remote`), plus the public share of the storage plan in the
  account-wide view. Reconcile hourly: the home queries `streamMinutesViewedAdaptiveGroups` filtered
  to the public inputs' video ids (E32 plan-32-10) and sends `budget{}`; if the home is offline, a
  Worker cron (hourly) runs the same query with a read-only Analytics token (Worker secret
  `CF_ANALYTICS_TOKEN`) so the budget still holds.
- Thresholds: 80 % → `alert` to the home (notification) ; 100 % → `block_new` (no new sessions;
  running ones stop at the end of their programme or after 60 min) or `off_air` (all heartbeats
  answer 503).
- Limits (§6.3), Rate Limiting binding namespaces `play` (30/60 s), `api` (120/60 s), `ws` (10/60 s)
  per `iph`; optional WAF rate-limiting rule documented in the runbook for heavier abuse.
- Token hygiene: TTL 10 min, IP binding, no token without heartbeat, countries, panic rotation.
- Viewer counts: home polls `…/<active input>/views` every 60 s for public channels and sends them up;
  `show_viewer_count` exposes them on the site (rounded to 5 below 50).

## 10. Deployment (story 34.10)

### 10.1 Repository layout

```
web/packages/tvplayer/          shared player + switching
web/tv-site/                    Vite + React SPA (routes ar/en), design tokens from web/design-system
web/tv-site/worker/src/         index.ts (router), sitecontrol.ts, livehub.ts, tokens.ts, access.ts,
                                html.ts (HTMLRewriter meta), home.ts (HMAC endpoints), budget.ts
web/tv-site/wrangler.jsonc      name maktaba-tv-site; assets ./dist; DO bindings + migrations;
                                R2 binding SNAPSHOTS; ratelimits; analytics_engine_datasets;
                                env.staging / env.production
web/tv-site/deploy-manifest.json  bindings, DO migration tags, schema version, required secrets
```

### 10.2 Build and release

CI (`ci.yml` web tier): typecheck, lint, unit (Vitest + Miniflare/`@cloudflare/vitest-pool-workers`
for the Worker and DOs), Playwright e2e against `wrangler dev` with a fake publisher, the
"no home host" link scan, bundle size budget. `release.yml` builds and attaches
`maktaba-tv-site-<version>.tar.gz` (`dist/` assets, bundled `worker.js`, `deploy-manifest.json`,
SHA-256 sums); the Maktaba server release embeds or downloads the matching tarball.

### 10.3 Maktaba deploys (product path)

`tv_public_deploy` job on the home (queue engine), using the Cloudflare token from `home_secrets`:

```
1. ensure R2 bucket maktaba-tv-<env>; ensure Turnstile widget (domains) / Access app per mode
2. assets upload session: manifest of hashes → upload missing files → completion JWT
3. PUT worker script (module worker.js + metadata: main_module, compatibility_date, assets{jwt,
   config}, bindings: R2, DO namespaces (SiteControl, LiveHub), ratelimits, analytics engine,
   vars {ENV, SITE_VERSION, SCHEMA}), DO migrations from the manifest
4. secrets: HOME_KEY, STREAM_KEY_ID/PEM, TURNSTILE_SECRET, CF_ANALYTICS_TOKEN, SESSION_KEY
5. custom domain attach (tv_public.domain / staging_domain) or workers.dev
6. health: GET /api/v1/version (expects SITE_VERSION), WS connect from the home, test publish
7. record tv_public_deploys row; on failure re-deploy the previous version
```

Required token permissions (runbook): Workers Scripts Edit, Workers R2 Storage Edit, Workers Routes /
Custom Domains (zone) Edit, DNS Edit on the zone (custom domain), Turnstile Edit, Access: Apps and
Policies Edit (only for `access` mode), Stream Edit, Account Analytics Read. The admin page lists
which are missing after "Test connection" (extends E32 §R6.2).

**Promote:** deploys the version currently on staging to production (same tarball). **Rollback:**
re-deploys the previous production version from `tv_public_deploys` (tarballs cached on the home).
DO migrations are additive only, so a rollback never needs a down migration.

### 10.4 Developer path

`pnpm -C web/tv-site dev` (wrangler dev, local R2/DO), `deploy:staging` / `deploy:production`
(`wrangler deploy --env …`) for maintainers on a test account; secrets via `wrangler secret put`.
A GitHub Actions job (`tv-site.yml`, manual dispatch) can deploy staging to a project test account
with repository secrets; it is never used for the owner's production.

### 10.5 Environments and data

The home publishes to every environment that is deployed (staging and production get the same
snapshots); the control channel is per environment; staging never gets tokens for the production
inputs unless `staging_video` is on (default off: staging shows the guide and a test pattern
input). Staging is always `noindex` and, if `access_mode = public`, gets a Turnstile gate anyway.

## 11. Security summary

| Threat | Control |
|---|---|
| Reaching the home | No inbound path; the Worker has no route to the home; CI link scan; published data whitelist (§3.1 schema, validated by the Worker on commit). |
| Forged publish / control | HMAC with `HOME_KEY`, 60 s window, nonce cache, schema validation, size caps (blob ≤ 1 MB, commit ≤ 256 KB). |
| Stream hot-linking | Signed URLs, short TTL, IP binding, `allowedOrigins`, heartbeat-gated refresh, key rotation. |
| Bots / scraping | Turnstile, rate limits, per-IP caps, concurrency cap, budget. |
| Copyright | Default off, rights confirmation with audit, exclusions/slates, takedown contact, panic off-air. |
| Privacy | No IP storage (salted daily hash), one functional cookie, aggregate analytics, privacy page. |
| Secret leakage | Secrets only in `home_secrets` and Worker secrets; never in R2, logs or the bundle; deploy job masks them. |

## 12. Testing approach

- Unit: availability function (policies × kinds), publisher diffing and hashing, HMAC, token claims
  (decoded and verified with the public JWK), DO state machine (Miniflare), budget thresholds.
- Integration: publisher → Worker (wrangler dev) → R2 → LiveHub event → page receives within budget.
- Streaming: fake ffmpeg (existing `fakeffmpeg_test.go`) for the public mux slate switch, pusher to
  A then B, failover restart on the same input.
- E2E (Playwright, mobile and desktop, ar and en): grid, guide, watch, switching by swipe/keys/digits,
  tuning card, guide update within 10 s, off-air states, Turnstile test keys.
- Spike (34.3/34.4, real Cloudflare account): cold start time, A→B switch, `ip.src` rule on mobile,
  `allowedOrigins` with native players, rotation storage accounting.
