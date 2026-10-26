# Maktaba TV across machines and away from home

This page tells you how to let another machine of your cluster stream Maktaba TV channels, how
to turn on remote viewing through your own Cloudflare Stream account, what that costs, and what
to do when something breaks. It is the runbook of
[Epic 32](../specs/epics/32-tv-streaming-cluster-remote/README.md); the design is in
[design.md](../specs/epics/32-tv-streaming-cluster-remote/design.md).

Everything here is **off by default**: until you change a setting, the home encodes every channel
and TV is watched on the LAN only, as before.

Contents:
[1. Streaming on another machine](#streaming-hosts) ·
[2. Where channels go](#placement) ·
[3. Failover and maintenance](#failover) ·
[4. Cloudflare setup](#cloudflare) ·
[5. Limits, budget and costs](#costs) ·
[6. Remote viewers](#remote-viewers) ·
[7. Failure playbook](#failures) ·
[8. Plex away from home](#plex-remote) ·
[9. Measured behaviour](#measured)

---

<a id="streaming-hosts"></a>

## 1. Streaming on another machine

The home (the machine that runs `maktaba-server serve` with the database) always streams. Another
machine of the cluster can encode channels too — for example a Windows PC with an NVIDIA card
(NVENC) next to a Mac Studio home (VideoToolbox) — "for more users". There is still **one encode
per channel in the whole cluster**, whatever the number of viewers, and every viewer, Plex
included, keeps talking to the home only.

**Before you start**

1. The machine has joined the home (Epic 31: Admin → Machines → Join). It uses the credentials it
   got when it joined; there is no second pairing.
2. It can read the library: its storage check on the Machines page is green, and its path map
   turns the home's paths into its own (Epic 31.3). A machine that cannot read the library
   refuses to start streaming ("unfit: storage").
3. Its ffmpeg has the encoder you want: `ffmpeg -encoders | grep -E 'nvenc|videotoolbox'`.

**Turning it on**

1. On the machine: `maktaba-server serve --role streaming` (or `maktaba-server serve`, which
   starts every role).
2. On the home: Admin → Settings → *Maktaba TV machines* → **Other machines may stream** on
   (`tv_cluster.nodes_stream`).
3. Admin → Live TV → **Streaming hosts**: switch on **Stream on this machine** for that machine.

**The Streaming hosts page** lists every host with its role (home or node), state, encoders,
capacity and use, the channels it carries and its last heartbeat (every 5 s). The states:

| State | Meaning |
|---|---|
| online | heartbeating and taking channels |
| down | no heartbeat for `tv_cluster.dead_after_seconds` (default 15), or three proxied requests failed in a row |
| draining | takes no new channels; its channels move when idle (or at once with *Move now*) |
| disabled | "Stream on this machine" is off, or `nodes_stream` is off for a node |
| unfit: storage | the machine cannot read the library (fix its path map or mounts) |
| unfit: version | its `maktaba-server` speaks another cluster protocol than the home: update it |
| unfit: flapping | it went down twice within 10 minutes; held out of new channels for 10 minutes |

The capacity is what the machine's output settings allow (`tv_output.max_active_channels`, and
for NVENC the session cap: a channel can hold two sessions at a block boundary). You can lower it
per machine with the capacity override, and weight hosts for the balanced policy.

<a id="placement"></a>

## 2. Where channels go

A channel is placed when its first viewer arrives (a browser, a tuner, a remote push) and released
when nobody watches it past the warm grace. A placed channel is **sticky**: it never moves just to
even out the load.

| Setting | Default | |
|---|---|---|
| `tv_cluster.policy` | `home_first` | `home_first`: the home until it is `spill_at` % full, then the node with the most room. `balanced`: always the host with the most room, the home included. |
| `tv_cluster.spill_at` | 100 | the share of the home's capacity used before nodes take new channels |
| `tv_cluster.pins` | `{}` | a preferred machine per channel (set it on the channel's page); used while that machine is up and has room |
| `tv_cluster.hls_mode` | `proxy` | `proxy`: the home forwards every playlist and segment (one address, works everywhere). `redirect`: the home answers segment requests with a short-lived signed URL of the node (playlists stay proxied) — only when every viewer can reach every machine. |

When no host has room, viewers get today's answer: the browser shows the channel as unavailable
(HLS 503) and Plex says all tuners are in use. The On air list says why.

**The home's network.** In `proxy` mode a node-encoded channel crosses the LAN twice (node → home →
viewer), about 6.5 Mbit/s each way per channel at the default bitrate. On a 1 Gbit/s home NIC that
is small; switch to `redirect` only if the home's NIC is busy. `/auto/v{n}` (Plex, Jellyfin,
Emby) is always proxied.

<a id="failover"></a>

## 3. Failover and maintenance

- A host that misses heartbeats for `dead_after_seconds`, or whose proxied requests fail three times
  in a row, is marked **down**; with `tv_cluster.failover` on (default) its channels are placed on
  another host at once and resume at the wall-clock position of their schedule.
- Browser viewers see a short stall and continue at the right programme position. A Plex tuner
  connection is spliced onto the new host without closing (up to 3 times per connection), so Plex
  does not re-tune.
- A Cloudflare push of a failed host's channel continues on the same live input from the new host
  within the input's 30 s reconnect window, so remote viewers keep the same URL.

**Maintenance.** Before you restart or update a node: Live TV → Streaming hosts → **Drain**. It takes
no new channels and its channels move when idle; **Move now** moves them at once (viewers see a
short stall). Turn drain off when it is back. *Move* on the On air list moves one channel; *Stop*
stops it everywhere.

<a id="cloudflare"></a>

## 4. Cloudflare setup (remote viewing)

Remote viewing sends a channel to **your own** Cloudflare Stream account once, whatever the number
of remote viewers, and remote viewers play it from Cloudflare with a signed link. The Maktaba relay
(Epic 25) only carries the small API calls (guide, tune, heartbeat) — never the video.

1. **Stream subscription.** In the Cloudflare dashboard: Stream → subscribe.
2. **Storage.** Live HLS needs recording on, so a running broadcast is stored while it runs
   (Maktaba deletes each recording as soon as its broadcast ends). Buy prepaid storage: **one
   1,000-minute block = $5/month**, which holds about **two** concurrent 8-hour broadcasts
   (2 × 480 min). Buy another block for every two more channels you want away from home at once,
   and set `tv_remote.storage_plan_minutes` to match.
3. **API token.** My Profile → API Tokens → Create Token → *Create Custom Token*, with these
   permissions and nothing else:

   | Permission | Level | Needed for |
   |---|---|---|
   | Account → **Stream** | **Edit** | live inputs, recordings, signing keys, live outputs (simulcast), storage use |
   | Account → **Account Analytics** | **Read** | the minutes watched (budget). Optional: without it the estimate uses session minutes |

   *Account Resources*: include **only your account**. No zone permission is needed. Test connection
   reports a token without Stream access, or with Analytics missing, as its own line.
4. **Enter it.** Admin → Extensions → *Cloudflare Stream*: the account id and the token. The token is
   stored on the home only, encrypted (`home_secrets`), never readable by other machines and never
   shown again.
5. **Test connection.** It checks the token, the subscription and storage, creates the signing key
   (kept on the home), learns your playback domain (`customer-<code>.cloudflarestream.com`) and
   checks that the home's ffmpeg can push (RTMPS needs TLS; SRT needs libsrt).
6. **Enable remote viewing** on the same page (needs a green test from the last 24 h).

Optional:

- `tv_remote.low_latency` — Cloudflare's LL-HLS (beta) for new inputs. The encoder already uses a
  2 s GOP without B-frames, which LL-HLS needs.
- Webhooks — polling is the source of truth; a Cloudflare Notifications webhook only speeds up
  error detection. `POST /api/tv/remote/webhook-secret` (admin) returns a new secret, the header
  and the URL path. At Cloudflare: Notifications → Destinations → *Webhooks* → the home's public
  URL (through the relay) + `/api/tv/remote/webhook`, with that secret (Cloudflare sends it in the
  `cf-webhook-auth` header); then Notifications → Add → *Stream Live Input* (connected,
  disconnected, errored) to that destination. An errored event's code
  (`data.live_input_errored.error.code`) is kept on the input.

<a id="public-site"></a>

### 4.1 The public TV site (Epic 34, first slice)

A public channel can be watched by anyone, on your own site running on Cloudflare (a Worker). The
visitor reaches only the Worker and Cloudflare Stream, never the home: the home posts its lineup
and live video ids outward, signed, every 10 s.

1. Remote viewing set up (above), Test connection green.
2. **Deploy the Worker** from `web/packages/tvsite` with your own Cloudflare login:
   `npx wrangler@4 login`, then `npx wrangler@4 deploy`. It needs the Workers Paid plan (Durable
   Objects). Note its address (`https://maktaba-tv-site.<you>.workers.dev`).
3. Admin → Live TV → **Public site**: *Create the site's secrets*. This creates the site's own
   Stream signing key and a home secret, shown once. Put each into the Worker:
   `npx wrangler@4 secret put HOME_HMAC`, `… STREAM_KEY_ID`, `… STREAM_KEY_JWK`.
4. Same panel: the site address, *Publish public channels* on, Save. The panel shows the last
   update the site received.
5. Make a channel public with its switch: you confirm you hold the rights to show it to the public
   (asked again every time, kept for audit).

A public channel is sent to Cloudflare **all the time**, whether or not anyone watches, so it uses
one of `tv_remote.max_channels` and storage like any running broadcast. Replacing the site's
secrets stops every token the old key signed. Removing the Cloudflare integration deletes the site's
key too.

Not yet: the guide, exclusion slates, a custom domain, Turnstile or invite-only access, and the
dedicated A/B public inputs (see specs/epics/34-tv-public-site). To try the site locally without a
Cloudflare account, see `TestPublicSiteDemo` in `api/internal/tvremote/public_e2e_test.go`.

<a id="costs"></a>

## 5. Limits, budget and costs

Cloudflare bills **minutes delivered** ($1 per 1,000 minutes watched, counted per segment
requested) and **minutes stored** (prepaid, $5 per 1,000). Ingest and encoding are free, and a
broadcast nobody watches costs no delivery. There is **no audio-only saving**: minutes, not bytes,
are billed.

| Setting | Default | |
|---|---|---|
| `tv_remote.viewers` | `all` | who may watch away from home: every signed-in user, admins, or a list |
| `tv_remote.max_streams_per_user` | 2 | remote streams per user (each device counts) |
| `tv_remote.max_channels` | 22 | channels sent to Cloudflare at once (1–50) |
| `tv_remote.idle_timeout_minutes` | 3 | keep sending after the last remote viewer |
| `tv_remote.token_ttl_minutes` | 60 | signed link lifetime; the player renews it while it plays |
| `tv_remote.max_push_hours` | 8 | a longer broadcast starts again (one short re-buffer) |
| `tv_remote.monthly_budget_usd` | 10 | at 80 % the admin is told; at 100 % new remote tunes get 402 |
| `tv_remote.budget_action` | `block_new` | `warn`: only a notice at 100 % |
| `tv_remote.allowed_countries` | `[]` | two-letter codes; empty: anywhere |
| `tv_remote.remote_detection` | `relay` | `relay_or_public_ip`: a request from a public address also counts as away (see §6) |

On the default single storage block the **storage guard**, not `max_channels`, is usually the
limit: it starts a new broadcast only when every running broadcast's full length (up to 8 h), the
recordings not yet deleted, the new one's 8 h and a margin fit in the account's storage.

**Examples** (delivery at $1/1,000 minutes plus one $5 storage block):

| Use | Minutes watched / month | Delivery | Total |
|---|---|---|---|
| one person, 1 h a day | 1,800 | $1.80 | $6.80 |
| two people, 2 h a day each | 7,200 | $7.20 | $12.20 |
| a family of four, 1 h a day each | 7,200 | $7.20 | $12.20 |

**Upload bandwidth.** Each channel sent to Cloudflare uses its bitrate of the home's upload once:
about **6.5 Mbit/s** at the default 6 Mbit/s video plus audio. The default 22 channels would be
about **143 Mbit/s** of a 1 Gbit/s uplink — encoders and storage, not bandwidth, are the limit.

Admin → Live TV → **Remote** shows this month's minutes and cost estimate, every limit, the
channels being sent, the remote sessions and the last remote events.

<a id="remote-viewers"></a>

## 6. Remote viewers

**Web.** Open the web app through your relay address (`https://<you>.maktaba.app`) and tune a
channel as at home. The server tells the player it came through the relay
(`/api/tv/info` → `remote.via_relay`), and the player tunes through Cloudflare instead of the LAN
stream; nothing to choose. Switching channels works the same everywhere (▲/▼, PageUp/PageDown,
digits + Enter, L for the last channel, I or a tap for the mini-guide, G for the guide, a vertical
swipe on a phone): the channel playing keeps playing until the new one can play, and a remote
session ends as soon as you leave its channel. If you are at the per-user limit the old channel is
let go first.

Without an Epic 25 account (your own reverse proxy or tunnel), set
`tv_remote.remote_detection = relay_or_public_ip`: a request from a public address (not RFC 1918,
not ULA, not loopback, not CGNAT 100.64/10) then counts as away from home. Your proxy must pass
the client address (`X-Forwarded-For`) and must not be reachable from the LAN with a forged one.

What remote viewers may see:

| Message | Why |
|---|---|
| Starting remote stream… | the channel is starting at Cloudflare (up to ~25 s the first time) |
| Remote viewing paused: the monthly budget is reached | the budget is at 100 % with `block_new` |
| Too many remote streams | the user is at `max_streams_per_user` |
| All remote channels are in use | `max_channels` or the storage guard is full |
| Remote viewing is not set up (reason) | the integration is off, untested or Cloudflare is unavailable |
| Can't reach the remote stream host | `*.cloudflarestream.com` is blocked on this network (some offices) |

On the LAN, a channel that fails three times while remote viewing is available offers **Watch
remotely**; a phone moving from Wi-Fi to mobile data switches to the remote path by itself.

**Apps.** The mobile shell (Capacitor) uses the web player. The native iOS/tvOS/Android TV apps
will follow the same rule through the Epic 25.10 resolver (LAN endpoint → LAN HLS; relay → the
remote API), re-tuning when a link is about to expire; see story 32.9 "Native apps".

<a id="failures"></a>

## 7. Failure playbook

| Symptom | What happened | What to do |
|---|---|---|
| Remote viewing unavailable, LAN fine | Cloudflare is down or the token stopped working | nothing for LAN viewers; run *Test connection* when Cloudflare is back |
| `ERR_STORAGE_QUOTA_EXHAUSTED` | the Stream storage is full | buy another 1,000-minute block (and raise `storage_plan_minutes`), or lower `max_push_hours` |
| `ERR_MISSING_SUBSCRIPTION` | no Stream subscription | subscribe, then *Test connection* |
| `ERR_GOP_OUT_OF_RANGE` | keyframe interval outside 2–8 s | check `tv_output` (keep the 2 s GOP); Maktaba retries once on a fresh input |
| `ERR_UNSUPPORTED_VIDEO_CODEC` / `_AUDIO_CODEC` | not H.264 / not AAC | the push transcodes AC-3 to AAC; check the channel's output settings |
| `ERR_UNHEALTHY` | Cloudflare found the input unhealthy | retried once on a fresh input; a channel failing twice is "remote failed" for 10 minutes |
| a node fails mid-push | its channels move (§3); the push continues from the new host on the same input | nothing; watch the Remote panel's events |
| a node loses power mid-push (its connection stays open) | the home's relay hears nothing from it for 5 s, drops it and follows the channel to its new host, on the same push session (same Cloudflare video) | nothing; `tv_push_source_lost` in the events |
| the home's uplink is down | the relay restarts its ffmpeg with a growing pause (up to 5 s); Cloudflare keeps the video for 30 s | if longer, the push fails and viewers tune again; check the uplink |
| a public channel is off air on the site | the home has not posted for 60 s, or its push is not live | Admin → Live TV → Public site shows the last update and its error |
| the home is down | everything stops: the home is the front door and the database | bring the home back; nodes and Cloudflare wait |
| a playback link leaked | someone has a signed URL | Admin → Extensions → Cloudflare Stream → **Rotate signing key**: every remote link stops at once; viewers' players pick up new links at their next heartbeat |
| a stream key leaked | someone could push to your input | delete the live input in the Cloudflare dashboard (Maktaba creates a fresh one on demand) or remove and re-add the integration |
| budget reached | 100 % of `monthly_budget_usd` | running sessions finish; raise the budget or wait for the next month (UTC) |

Metrics are on each service's `/metrics` (the API's admin port and the streaming service's admin
port, `:9101` by default): `maktaba_tv_host_up`, `maktaba_tv_channels_on_air`,
`maktaba_tv_failovers_total`, `maktaba_tv_frontdoor_*`, `maktaba_tv_remote_*`. The control centre's
health view charts `tv_hosts_up`, `tv_channels_on_air`, `tv_remote_viewers`,
`tv_remote_pushes_live` and `tv_remote_cost_usd` (one sample a minute). Log lines carry `event=`
(`tv_placement`, `tv_failover`, `tv_host_down`, `tv_push_start`, `tv_push_error`, `tv_budget_80`,
…); stream keys and tokens are never logged.

<a id="plex-remote"></a>

## 8. Plex away from home

Plex Live TV outside the house is **Plex's own remote access**. Plex pulls the channel from the
home's HDHomeRun tuner (`/auto/v{n}`, always on the LAN, always proxied by the home) and serves its
remote users itself. Maktaba sends nothing to Cloudflare for Plex: those viewers use tuners like
local Plex viewers (they count against `tuner_count`) and cost nothing in Cloudflare. Turn on
*Remote Access* in Plex's own settings if you want it.

**How Plex finds the tuner at home.** While the tuner is on, the home answers both discovery
protocols Plex's *Set up Plex DVR* scan uses: SSDP (UDP 1900, an absolute `LOCATION` such as
`http://192.168.50.177:8081/device.xml`, plus `ssdp:alive` every five minutes and `ssdp:byebye`
on shutdown) and the native HDHomeRun discover protocol (UDP 65001 broadcast, the libhdhomerun
packet format). Each reply carries the address of the interface the asking client can reach.
Both stop when the tuner is turned off. Allow UDP 1900 and 65001 in the home's firewall. On a
network that blocks broadcast or multicast (guest Wi-Fi, some mesh systems, Docker bridges), type
the *HDHomeRun address* from the Live TV page (for example `192.168.50.177:8081`) into Plex's
manual entry box.

<a id="measured"></a>

## 9. Measured behaviour

Cloudflare does not document a few things the design relies on. The 32.6 spike
(`maktaba-api cloudflare spike`, see [tools/tv/cf-spike.md](../tools/tv/cf-spike.md)) measures them
against a real account and writes a JSON report; its numbers go here, not guesses.

| Question | What the design assumes | Measured |
|---|---|---|
| Does a token whose `sub` is the in-progress video id play the live broadcast? | yes (fallback: sign the input id with a cool-down) | not yet run |
| Is `exp` checked on every segment, or only on playlists? | on every request; the web player swaps tokens without a reload either way | not yet run |
| Time from push to playable (standard HLS / LL-HLS) | under 25 s | not yet run |
| Glass-to-glass latency (standard HLS / LL-HLS) | tens of seconds / a few seconds | not yet run |
| A reconnect within the 30 s window keeps the video id | yes | not yet run |
| Deleting the recording frees storage at once | within minutes | not yet run |

Run the spike once with your account (it costs a few cents) and fill this table from its report.
