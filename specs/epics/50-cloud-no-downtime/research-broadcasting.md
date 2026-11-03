# Epic 50 research: home broadcasting (publishing a home's TV channels to people outside it)

Read with the [README](README.md) §14. The owner, 2026-11-03: *"the ability to stream TV from
home servers to others, let's say an entertainment tier or a home streamer tier. Explore that
and what it requires."*

Prices and outside facts were looked up on **2026-11-03**, and each carries its source. Facts we
could not confirm are marked **unverified**. Numbers we worked out are marked **estimate**. The
rights part (§6) sums up what
the law and the services' terms say. **It is not legal advice; confirm with a lawyer before
launch** (§6.4).

## 1. What Maktaba has today

| Piece | Where | What it gives broadcasting |
|---|---|---|
| Live channels, 24/7, from the library | Epic 27 (scheduler, guide, filler), `streaming/internal/channel/` | The programme to publish: one HLS mux per channel. The watermark and channel strip are burned in by the block encodes (Epic 27.11). |
| Channel output settings | `streaming/internal/channel/settings.go` (`tv_output.*`: `video_kbps` 300–50,000, `segment_seconds` 1–10, `keyint_seconds`, `max_active_channels` 0–64, `nvenc_max_sessions`, `videotoolbox_max_sessions`) | The encode a published channel would carry, and the slot limit it has to fit in. |
| Placement across hosts | Epic 32, `docs/tv-cluster-and-remote.md` | A channel is placed at its first viewer and released after the warm grace. A published channel would have to stay placed. |
| The public TV site | Epic 34, `web/packages/tvsite/` (first slice on main: `worker.ts`, `homeauth.ts`, `sign.ts`) | A Cloudflare Worker with static assets and a `SiteControl` Durable Object. The home posts its state, signed with **one** shared secret (`HOME_HMAC`). The Worker mints Cloudflare Stream tokens with the site's key (`STREAM_KEY_ID`, `STREAM_KEY_JWK`) for `/watch/<slug>`. It runs **on the home owner's own Cloudflare account**, for **one** home. Epic 34's spec adds rights confirmation, exclusions and slates (34.1, 34.3), the guide publisher (34.2), access modes (34.7) and budget and abuse limits (34.9). |
| Maktaba Cloud | Epic 25, this epic | Accounts, homes, the relay, plans (Free / Basic / Pro, 50.19), billing through a provider-agnostic `billing.Provider` (TBD, never Stripe), signed entitlements. |
| Signed lists homes honour | Epic 54 §6.3 | Ed25519-signed, expiring, anti-rollback catalog and revocation list. The pattern for a broadcast kill switch (§7). |
| Logs, metrics, analytics | Epic 53 (53.01 schema, 53.12 dashboards, 53.17 cloud) | The place viewer analytics and the edge's logs go. |

**The gap.** Epic 34 is self-hosted: each home owner brings their own Cloudflare account, pays
Cloudflare directly, and is the only publisher. Nothing lets a Maktaba home owner who has no
Cloudflare account publish through **Maktaba Cloud** with Maktaba as the host. Nothing yet
handles many publishers on one edge, metering per publisher, takedown notices sent to Maktaba,
or a Maktaba-side kill switch.

## 2. How others do it

### 2.1 Media servers: sharing, not broadcasting

| Product | Sharing with outsiders | Source |
|---|---|---|
| **Plex** | You share with people who have a free plex.tv account. Community answers put the limit at about 100 people per account (**unverified**, no official page found). Plex Home holds 15 members. Since 2025-04-29, remote play of personal media needs a Plex Pass or Remote Watch Pass ([research.md](research.md) §2). **Live TV & DVR can be shared only with Plex Home members, not with "Friends"** (forum and support-linked posts, **unverified** against an official page). Users on the forum say sharing live TV outside the home could amount to retransmission to the public (their opinion). | [forum: shared library limit](https://forums.plex.tv/t/shared-library-limit/936129), [forum: users per server](https://forums.plex.tv/t/hello-limits-users-free/527627), [forum: Live TV for friends](https://forums.plex.tv/t/live-tv-for-friends/224735), [forum: sharing Live TV & DVR with friends](https://forums.plex.tv/t/sharing-live-tv-and-dvr-with-plex-friends/776087) |
| **Jellyfin** | Every viewer needs an account on the server. SyncPlay ("watch together") also needs accounts. No public share link and no built-in guest access. One contributor suggests making a temporary user through the API. Remote access is a reverse proxy, a VPN or a forwarded port. | [Jellyfin networking](https://jellyfin.org/docs/general/post-install/networking/), [finamp issue 640](https://www.github.com/jmshrv/finamp/issues/640), [SyncPlay policy](https://kotlin-sdk.jellyfin.org/dokka/jellyfin-model/org.jellyfin.sdk.model.api/-user-policy/sync-play-access.html) |
| **Emby** | Emby Connect links accounts. "All connections are direct from your devices to your Emby Server." No relay and no public broadcast. | [Emby Connect](https://emby.media/support/articles/Emby-Connect.html) |

**What follows:** no media server we looked at broadcasts to the public. Plex deliberately keeps
Live TV inside the household. Maktaba broadcasting is a new kind of product for this category,
closer to Owncast or Twitch than to Plex. That makes rights (§6) the main thing to get right, more than the technology.

### 2.2 Self-hosted broadcasting

| Product | Model | Source |
|---|---|---|
| **Owncast** | One self-hosted server takes RTMP in, makes HLS, and serves it. With **passthrough** it copies the video without re-encoding (bitrate and resolution settings are ignored). "A single CPU core can typically handle one transcoded output quality at 30fps." Each extra variant adds CPU. For more viewers it offloads segments to **S3-compatible storage** behind a CDN. Its bandwidth formula: GB = kbps × seconds × viewers ÷ 8,000,000. | [video quality](https://owncast.online/docs/video/), [resources and requirements](https://owncast.online/docs/resources-requirements/) |
| **PeerTube** | RTMP (1935) or RTMPS (1936) in, HLS out, **P2P over WebRTC** between viewers to save server bandwidth. Live transcoding runs one ffmpeg per live stream ("10 FFmpeg processes for 10 lives"). **Remote runners** can take live transcoding off the server, and the docs warn that runners can alter video, so they must be trusted. | [configuration](https://docs.joinpeertube.org/admin/configuration), [remote runners](https://docs.joinpeertube.org/admin/remote-runners) |

**What follows:** Owncast's "passthrough, then segments to object storage behind a CDN" is
the shape we want: the server makes the segments once, and object storage plus a CDN do the
fan-out. PeerTube's P2P cuts egress but adds WebRTC complexity and leaks viewer IPs to other
viewers. We don't need P2P when egress is free (§3), so we leave it out.

### 2.3 Platforms: ingest and ladders

| Platform | Ingest | Ladder | Latency | Source |
|---|---|---|---|---|
| **Twitch** | RTMP. **Enhanced Broadcasting** (2024): the streamer's own machine encodes up to three renditions and sends all of them ("multi-encode", Enhanced RTMP multitrack). | Server transcodes are tiered: Partners are guaranteed them, Affiliates get them "as available, with priority", others get whatever is left. Twitch says it can't always guarantee transcodes because they are expensive. | (not looked up) | [Twitch blog: Enhanced Broadcasting](https://blog.twitch.tv/en/2024/01/08/introducing-the-enhanced-broadcasting-beta), [OBS KB: transcodes](https://obsproject.com/kb/transcodes-transcoding), [PCWorld](https://www.pcworld.com/article/2199898/twitch-enhanced-broadcasting-changes-everything-about-streaming-to-twitch.html) |
| **YouTube Live** | RTMPS is recommended, HLS if the encoder needs it. Keyframes every 2 s, never more than 4 s, CBR. 1080p60 H.264 table values: 4 / 10 / 12 Mbps (the columns aren't labelled in the snippet we saw). | YouTube transcodes. | A low-latency option exists. 4K is always "normal latency". Exact mode figures **unverified**. | [YouTube: live encoder settings](https://support.google.com/youtube/answer/2853702?hl=en) |

**What follows:** both big platforms have moved toward **the broadcaster encoding the ladder**
(Twitch Enhanced Broadcasting) because server-side transcoding is the expensive part. A Maktaba
home already encodes every channel and often has a hardware encoder, so **the ladder at home** is
the natural default (§5).

### 2.4 Managed video services

| Service | Live ingest and encoding | Delivery | Storage | Latency | Source |
|---|---|---|---|---|---|
| **Cloudflare Stream / Stream Live** | **Free** ("ingress and encoding are always free"). Live inputs over RTMPS, SRT and WebRTC (WHIP). ABR renditions are produced by Stream. | **$1 per 1,000 minutes delivered**, counted per segment request. Preloading counts. Billing is on minutes only, **regardless of resolution or file size**. | $5 per 1,000 minutes of capacity, prepaid. Live recordings use it. | LL-HLS on a live input, "as little as three seconds" glass to glass in Cloudflare's test. | [Stream pricing](https://developers.cloudflare.com/stream/pricing/), [Stream changelog](https://developers.cloudflare.com/stream/changelog/), [LL-HLS blog (via plushcap)](https://plushcap.com/analysis/cloudflare/low-latency-hls-support-for-cloudflare-stream), Epic 34's facts table |
| **Mux Video** | **Live encoding $0.03125 a minute** (1080p "Plus", the only live tier listed). | $0.001 a minute at 1080p, after **100,000 free minutes a month**. Lower resolutions are "lower", with no figure given. | $0.003 a minute a month at 1080p. | Standard 25–30 s, reduced 12–20 s, low (LL-HLS) "as low as 5 s". | [Mux pricing](https://www.mux.com/pricing), [Mux live latency](https://www.mux.com/docs/guides/reduce-live-stream-latency), [Mux live FAQs](https://www.mux.com/docs/guides/live-streaming-faqs) |
| **Bunny Stream / Bunny CDN** | Stream: "no transcoding fees". **No published live-streaming price** (the page shows only an image). | CDN Standard network: **$0.01/GB** Europe and North America, $0.03 Asia and Oceania, $0.045 South America, **$0.06 Middle East and Africa**. Volume network (10 PoPs): **$0.005/GB** for the first 500 TB. $1 a month minimum, no request fees, bandwidth caps you can set. | Stream storage from $0.01/GB. | (CDN only) | [Bunny CDN pricing](https://bunny.net/pricing/cdn/), [Bunny Stream pricing](https://bunny.net/pricing/stream/) |
| **Cloudflare R2 + Workers** (build it ourselves) | Nothing to ingest. The home uploads finished segments. | **No egress charge** on R2 or Workers. R2 Class B (read) $0.36 a million. Workers Paid: $5 a month, 10 M requests included, then $0.30 a million. Durable Objects: $0.15 a million requests, $12.50 a million GB-s of duration. | R2 $0.015 per GB-month. Class A (write) **$4.50 a million**. Lifecycle rules work in days; deletes are free. | Whatever the segment length gives (§4.3). | [R2 pricing](https://developers.cloudflare.com/r2/pricing/), [Workers pricing](https://developers.cloudflare.com/workers/platform/pricing/), [R2 lifecycles](https://developers.cloudflare.com/r2/buckets/object-lifecycles/) |
| **Hetzner (our relay nodes)** | Ours to run. | 20 TB a month included per server in the EU, then **€1/TB**. Planning capacity is **300 Mbit/s a node** until 50.14 measures it. | — | One hop through our relay. | [research.md](research.md) §4 |

#### Cloudflare's terms on video

Cloudflare's service terms say a non-Enterprise customer must use specific paid services
"(e.g., the Developer Platform, Images, and Stream) that you must use in order to serve video and
other large files via the CDN". Otherwise Cloudflare may limit or disable CDN access
([service-specific terms](https://www.cloudflare.com/service-specific-terms-application-services/)).

- **Caching HLS from our own origin through Cloudflare's ordinary CDN is ruled out** on our plan.
  It is also not the default: `.m3u8` and `.ts` aren't among the extensions Cloudflare caches
  by default ([default cache behaviour](https://developers.cloudflare.com/cache/concepts/default-cache-behavior/)).
- **Serving segments from R2 through a Worker** is, **on our reading**, the "Developer Platform"
  route the terms point to (R2 and Workers belong to it). The page doesn't name R2 explicitly.
  **Confirm this in writing with Cloudflare before launch** (owner question 8). The fallbacks
  are Stream (named in the terms) and Bunny.

### 2.5 Edge fan-out mechanics

- **Request collapsing.** When simultaneous requests for one uncached asset reach one Cloudflare
  data centre, a **cache lock** sends only the first to the origin. The others wait for it. The
  lock is **per location** ([default cache behaviour](https://developers.cloudflare.com/cache/concepts/default-cache-behavior/)).
  Across locations, Tiered Cache (Cloudflare's name for an origin shield) adds an upper tier, so
  the origin sees about one request per upper-tier location
  ([vendor-neutral guide](https://www.forasoft.com/learn/video-streaming/articles-streaming/origin-shield-tiered-caching);
  the savings figures there are **unverified**).
- **With push-ingest to R2 none of this decides whether the home is hit.** The home uploads each
  segment once. The origin is R2, never the home, so the home's upload doesn't change with the
  number of viewers. Collapsing then only reduces R2 Class B reads.
- **LL-HLS** (Apple): partial segments (`EXT-X-PART`), blocking playlist reload (`_HLS_msn`,
  `_HLS_part`, `CAN-BLOCK-RELOAD=YES`), preload hints, `PART-HOLD-BACK` ≥ 3 × part target, and a
  few seconds of latency in well-tuned setups. A CDN must forward the `_HLS_*` query, hold
  blocking requests, and cache playlists only briefly
  ([Apple: enabling LL-HLS](https://developer.apple.com/documentation/http-live-streaming/enabling-low-latency-http-live-streaming-hls)).
  This was summarised from the spec as we know it, because the page body didn't load. **Check
  the exact values against Apple's page.**

## 3. Cost per viewer-hour

**Bytes per viewer-hour** (video plus audio at the stated total bitrate):

| Rendition | Total bitrate | GB per viewer-hour |
|---|---|---|
| 1080p | 10 Mbps | **4.5** |
| 720p | 3 Mbps (Basic's relay cap, Epic 34's `rendition_kbps`) | **1.35** |
| 480p | 1.2 Mbps | **0.54** |
| (Epic 34 default channel encode) | 6.5 Mbps | 2.93 |

**Delivery cost per viewer-hour** (USD unless marked €; list prices from §2.4; **estimates**):

| Path | 1080p 10 Mbps | 720p 3 Mbps | 480p 1.2 Mbps | Notes |
|---|---|---|---|---|
| Cloudflare Stream | $0.060 | $0.060 | $0.060 | Per minute, any resolution. |
| Mux (delivery) | $0.060 | ≤ $0.060 | ≤ $0.060 | After 100k free minutes a month. **Plus live encoding $1.875 an hour per channel = ≈ $1,369 a month per 24/7 channel.** |
| Bunny CDN, Volume ($0.005/GB) | $0.0225 | $0.0068 | $0.0027 | 10 PoPs. |
| Bunny CDN, Standard, EU/NA ($0.01/GB) | $0.045 | $0.0135 | $0.0054 | |
| Bunny CDN, Standard, Middle East/Africa ($0.06/GB) | **$0.27** | $0.081 | $0.032 | Much of the likely audience is here. |
| Hetzner relay, traffic past the 20 TB included (€1/TB) | €0.0045 | €0.0014 | €0.0005 | **Capacity, not price, is the limit:** at 300 Mbit/s a node, 30 / 100 / 250 concurrent viewers a node. |
| **R2 + Worker + Durable Object** (ours) | **≈ $0.001** | ≈ $0.001 | ≈ $0.001 | An upper bound that treats every request as a cache miss: with 4 s segments a viewer makes 900 segment and 900 playlist requests an hour. That is 1,800 Worker requests × $0.30/M = $0.00054, 900 DO requests × $0.15/M = $0.00014 and 900 R2 reads × $0.36/M = $0.00032. Egress is free. **Doesn't depend on resolution.** |

**Fixed cost per published channel a month on the R2 path** (**estimate**, 4 s segments,
2,592,000 s a month, 648,000 segments per rendition):

| Item | 1 rendition | 3 renditions (1080p + 720p + 480p) |
|---|---|---|
| R2 Class A writes: 648k per rendition × $4.50/M | $2.92 | $8.75 |
| Worker requests for ingest: 648k × $0.30/M | $0.19 | $0.58 |
| DO requests for ingest: 648k × $0.15/M | $0.10 | $0.29 |
| DO duration (awake all month, 128 MB): 331,776 GB-s × $12.50/M | $4.15 | $4.15 |
| R2 storage (the edge deletes segments after 10 min, so ≈ 1 GB held) | ≈ $0.02 | ≈ $0.02 |
| **Total** | **≈ $7.4** | **≈ $13.8** |

6 s segments cut the write rows by a third and add about 2 s of latency. The account-wide
included amounts (10 M Worker requests, 1 M DO requests, 400k GB-s) are ignored here, so small
numbers of channels cost less.

**For comparison, a 24/7 channel on each path, with nobody watching:** R2 ≈ $7–14; Stream ≈ $0.63
(125 min of recording capacity with Epic 34's A/B rotation); Mux ≈ $1,369 (encoding).
**Mux is out** for 24/7 channels.

**What follows:**

1. **R2 + Worker is the cheapest delivery by 20× to 60×** per viewer-hour, and its cost doesn't
   grow with resolution. It costs a few dollars a channel a month in writes and Durable Object
   time. It hangs on our reading of Cloudflare's terms (§2.4).
2. **Stream is the managed fallback.** Encoding is free (so cloud ABR comes free), it's named in
   Cloudflare's terms, and it costs $0.06 a viewer-hour whatever the quality.
3. **Bunny is the second fallback** and the cheapest per GB outside Cloudflare, but its Middle
   East and Africa price makes 1080p cost $0.27 a viewer-hour there.
4. **Our Hetzner relay is the wrong place to fan out.** Traffic is cheap, but a node carries only
   30 viewers at 1080p, has no edge near viewers, and shares its capacity with paying remote-play
   homes (Epic 50's no-downtime promise).
5. **Plans should meter viewer-hours, not GB.** On the primary path the cost follows requests
   (time), not bytes, and viewers understand hours.

## 4. Fan-out: how one home serves many viewers

A home uplink can't serve many viewers: 100 viewers at 6.5 Mbps is 650 Mbps. Every design below
makes the home send **each rendition once**, whatever the number of viewers.

### 4.1 Pull (the relay fetches from the home once and caches)

```
viewer ─► edge (Worker / CDN) ─miss─► relay (Hetzner) ─tunnel─► home HLS
            └ cache lock per location; Tiered Cache upper tier
```

- **Pros:** no new upload code at the home. The home's HLS endpoint already exists behind the
  tunnel. A channel nobody watches sends nothing.
- **Cons:**
  - The first viewer in each location (or each upper tier) waits for a round trip through
    the relay and the tunnel.
  - Fetches from the home are one per tier location per segment, not exactly one.
  - Every segment crosses the Hetzner relay, using capacity that paid remote play needs.
  - Caching video through Cloudflare's ordinary CDN from a non-R2 origin conflicts with the terms
    (§2.4). Making it compliant means a Durable Object per channel that pulls each segment once
    and writes it to R2. That is push with extra steps.
  - The home has to be reachable inbound through the tunnel, against Epic 34's rule that
    visitors' requests never reach the home.

### 4.2 Push-ingest (the home sends segments out; the cloud never calls in)

```
home channel mux ─► (ladder at home) ─► uploader ── HTTPS PUT ──► ingest Worker ─► R2 (segments)
                                                                          └──► ChannelLive DO (playlist)
viewer ─► watch / live Worker ─► Cache API (per location) ─miss─► R2 ;  playlist ◄─ DO (edge-cached 1 s)
```

Two variants:

| Variant | What the home sends | Who makes the ladder | Delivery |
|---|---|---|---|
| **P-seg (recommended)** | Finished HLS segments (the channel mux's own, plus any lower renditions), one HTTPS PUT each | The home (hardware encoder) | R2 + Worker, ≈ $0.001 a viewer-hour |
| **P-rtmp** | One RTMPS or SRT stream per channel (Epic 34's push relay, `streaming/internal/frontdoor/relay`) to a Stream live input on **Maktaba's** Cloudflare account | Cloudflare Stream (free) | Stream, $0.06 a viewer-hour |

- **Pros of P-seg:**
  - The home is fetched **exactly once** per rendition, by construction.
  - Only outbound calls, as in Epic 34.
  - It doesn't touch the Hetzner relay at all, so broadcasting can't hurt remote play.
  - The segments are the channel mux's own (watermark and strip already burned in), so `copy` needs no new encode.
  - The cheapest delivery.
- **Cons of P-seg:**
  - The always-on cost of the uploads (§3, fixed) whether or not anyone watches. Invite-only
    channels avoid it by publishing on demand (§5.3).
  - The home spends uplink on every rendition it sends.
- **P-rtmp** is the fallback for a home whose uplink can't carry a ladder. It sends one rendition
  and Stream makes the rest. It is also the way out if Cloudflare reads its terms against R2
  delivery.

### 4.3 Latency targets

| | Target | Why |
|---|---|---|
| Glass to glass (home encode → viewer screen), p95 | **≤ 20 s**; typical ≈ 12 s | Standard HLS with 4 s segments: ~3 segments of player buffer + upload (≤ 1 s) + edge. A 24/7 library channel isn't sport; Mux's standard mode is 25–30 s ([Mux FAQs](https://www.mux.com/docs/guides/live-streaming-faqs)). |
| Segment visible at the edge after the home finishes it, p95 | ≤ 1.5 s | Upload + R2 write + DO playlist update. |
| Startup (press play → first frame), p75 | ≤ 3 s | Same as Epic 34 R6.4 / its acceptance. |
| Channel switch, warm, p75 | < 2 s | Epic 34 R6.4, `@maktaba/tvplayer` make-before-break. |
| On-demand (invite-only) cold start | tuning card < 100 ms, video ≤ 15 s | The home wakes the channel, encodes and uploads two segments. |
| LL-HLS | **not in the first version** | It needs part uploads every ~0.3 s (≈ 10× the R2 writes, §3) and blocking playlist requests held at the edge. We can revisit it for event-style broadcasts. |

### 4.4 One fetch from the home per channel, whatever the viewers

- **The home → edge:** one PUT per segment per rendition. Viewers never cause a request to the home.
- **Edge → R2:** segment URLs are **immutable** (`/<channel>/<rendition>/<seq>-<hash>.ts`,
  `Cache-Control: public, max-age=86400, immutable`). The Worker checks the viewer's token, then
  looks the segment up in the Cache API under a **cache key without the token**, so one cached
  copy serves every viewer in that location. A miss reads R2 once per location (Cloudflare's cache
  lock collapses simultaneous misses in a location).
- **Playlists:** made by the channel's Durable Object from the segment list, cached at the edge
  for 1 s under a token-free key. So the DO sees **at most about one playlist request per
  rendition per location per second**, however many viewers are there.

## 5. The ladder: at home or in the cloud

| | **At home** (recommended) | **In the cloud** |
|---|---|---|
| Who encodes | The home's hardware encoder (VideoToolbox, NVENC, Quick Sync, VAAPI). One decode, N encodes; the top rung is `copy` of the channel's own encode. | Cloudflare Stream (free with P-rtmp), or our own transcoders on Hetzner. |
| Cost to Maktaba | none | Stream: covered by $0.06 a viewer-hour. Hetzner: a software 1080p→720p+480p ladder takes about 2–4 cores per channel, 24/7 (Owncast's "one core per quality at 30 fps"; **estimate**), so about one CCX13 (€43.49, 2 dedicated vCPU) per channel. **Too expensive.** |
| Uplink | the sum of the rungs (§5.1) | one rendition |
| Watermark | burned in at the block encode, so every rung carries it | same (it's in the source) |
| Precedent | Twitch Enhanced Broadcasting | YouTube, Twitch Partner transcodes |

### 5.1 Uplink a published channel needs

The rungs' total × 1.1 for HLS, TCP and TLS overhead (**estimate**):

| Ladder | Rungs | Uplink per channel |
|---|---|---|
| `copy` (Epic 34 default encode) | 6.5 | **7.2 Mbps** |
| `copy` + 480p | 6.5 + 1.2 | **8.5 Mbps** |
| 1080p + 720p + 480p | 10 + 3 + 1.2 | **15.6 Mbps** |
| 720p + 480p (small uplink) | 3 + 1.2 | **4.6 Mbps** |
| 480p only | 1.2 | **1.3 Mbps** |

The rule in 50.22: **published channels together may use at most 50 % of the measured uplink**
(the rest is for remote play, the relay tunnel and the household). Examples:

- A 20 Mbps upload carries one full-ladder channel, or two `720p + 480p` channels.
- The owner's 1 Gbps carries the Broadcaster tier's three full-ladder channels (≈ 47 Mbps)
  with plenty of room.

### 5.2 Encoder load

- **One ffmpeg per published channel, always on while the channel is published.** Watch-only
  channels are different: Epic 32 places a channel at its first viewer and releases it after the
  warm grace. A published channel is placed and stays placed, like Epic 34 R3.1/R3.5.
- **Each extra rung is one more encode session** in the same ffmpeg (one decode, a split, N
  encodes). It counts against `nvenc_max_sessions` / `videotoolbox_max_sessions`.
- **Slots:** a published channel takes one `max_active_channels` slot permanently. Each lower
  rung counts as `broadcast.rung_slot_weight` of a slot (default 0.5, measured in 50.22). The
  home keeps Epic 34's `reserve_slots` (default 2) free for LAN and Plex viewers.
- `docs/hardware.md` doesn't exist on main as of 2026-11-03. 50.22 describes the load itself and
  links that page if it lands first.

### 5.3 Always-on or on demand

- **Open channels: always-on** (as Epic 34), so a viewer arriving from a share link starts in ≤ 3 s.
- **Invite-only channels: on demand.** The edge wakes the home over its control WebSocket at the
  first guest. The home stops after 5 minutes without a guest, so the fixed cost (§3) is paid
  only while someone watches. Epic 34 R3.6 `tiering = top_n` wakes channels the same way.

## 6. Rights: what applies, and the practical way through

> **Not legal advice. Confirm with a lawyer before launch.** This section sums up what the
> statutes and the services' terms say, with sources, and what that means for the design.

### 6.0 The position: Maktaba Cloud is an infrastructure and hosting provider

The owner, 2026-11-03: *"we charge for infra, not content"*.

- **What Maktaba Cloud sells:** relay, bandwidth and uptime. It never selects, uploads, curates or
  owns what a publisher airs.
- **Where the content decisions are made:** channel building, scheduling and YouTube intake all
  run on the **publisher's own home server**, under the publisher's control. The cloud only
  receives finished segments and the guide the home publishes. The design keeps it that way:
  - no cloud-side picks, rankings or recommendations;
  - a directory, if one ever exists, lists only what publishers choose to list.
- **Who is responsible for rights:** the publisher.
- **What the safe harbours ask in return.** Hosting safe harbours protect a host that has a
  designated agent, notice and takedown, a repeat-infringer policy, action on actual knowledge,
  and terms that put rights on the user (§6.2, §6.3). Those are **what the status requires**, and
  all of them are cheap to run.

### 6.1 "YouTube is public, so if nobody pays, what's the issue?"

| Point | What the sources say |
|---|---|
| **Free to watch is not free to re-broadcast** | Copyright gives the owner the exclusive right to reproduce a work and to perform or show it publicly (US: 17 U.S.C. § 106 ([Cornell LII](https://www.law.cornell.edu/uscode/text/17/106)); EU: "communication to the public"). This holds whether or not money changes hands. |
| **YouTube's Terms** | They bar accessing, reproducing, downloading, distributing or broadcasting content except through the service, unless YouTube or the rights holders authorise it ([Waterloo's copyright office, quoting the 2022 Terms](https://uwaterloo.ca/copyright-at-waterloo/node/198); live text at youtube.com/t/terms). |
| **Non-commercial helps, but isn't a pass** | The US Copyright Office's fair-use index lists the four factors and says plainly: "This does not mean, however, that all nonprofit education and noncommercial uses are fair" ([fair use index](https://www.copyright.gov/fair-use/)). Airing whole programmes 24/7 weighs against fair use on the "amount" and "market" factors. Maktaba charging publishers for the tier also makes the platform commercial. |
| **Creative Commons on YouTube** | YouTube lets uploaders choose **CC BY** instead of the standard licence. Reusers must credit the author (title, author, source URL, licence). Only content the uploader may license qualifies, and a video with a Content ID claim can't be CC BY ([YouTube Help: Creative Commons](https://support.google.com/youtube/answer/2797468?hl=en)). The Data API exposes it as `status.license = creativeCommon` (or `youtube`) ([YouTube Data API: videos](https://developers.google.com/youtube/v3/docs/videos)). |
| **Explicit permission from the source** | Many Islamic lecturers and dawah channels state that their lectures may be shared freely. A recorded statement from the rights holder, with a link to it, is the ordinary form of permission. |
| **Quran recitations** | The Qur'an's text has no copyright, but a specific **recording** belongs to its reciter or label. A scholar's answer on IslamQA puts it the same way (religious opinion, not law: [IslamQA](https://islamqa.org/?p=42830)). Recordings are claimed in practice (one **unverified** report of strikes over an Al-Sudais recitation). Treat a recitation like any other source. |

**What follows for the design (the publisher-side safety feature, story 50.23).** On the
publisher's home, an item can air on a published channel only if it is **publishable**:

1. the publisher's **own recording**;
2. a **CC BY** item, detected from `status.license` at YouTube intake and shown with attribution;
3. an item from a **source with recorded permission**: a speaker, a channel or a playlist that
   allows sharing, recorded once with a proof link.

While a channel is published, the scheduler skips every other item.

### 6.2 United States: DMCA § 512 (what hosting status asks for)

From 17 U.S.C. § 512 ([Cornell LII](https://www.law.cornell.edu/uscode/text/17/512)):

- **§ 512(c), storage at the direction of a user.** The host must:
  - lack actual knowledge or "red flag" awareness, and act expeditiously once it is aware
    ((c)(1)(A));
  - receive no financial benefit directly attributable to infringement it has the right and
    ability to control ((c)(1)(B));
  - remove material expeditiously on a notice ((c)(1)(C));
  - have a **designated agent** published on its site and filed with the Copyright Office
    ((c)(2)).

  (c)(3) lists what a notice must contain. On (c)(1)(B), we charge a flat infrastructure price
  and usage, the same whatever airs, which is the posture the owner chose; counsel confirms it.
- **§ 512(g), counter-notice.** The material is restored **10–14 business days** after a
  counter-notice, unless the complainant sues.
- **§ 512(i)(1)(A), repeat infringers.** A policy that is adopted, reasonably implemented and
  told to users, for terminating repeat infringers "in appropriate circumstances".
  - Courts treat this as a threshold for every safe harbour
    ([Perfect 10 v. CCBill](https://ilt.eff.org/Perfect_10%2C_Inc._v.html)).
  - A provider that didn't enforce its policy lost the safe harbour
    ([DWT on the *Cox* cases](https://www.dwt.com/blogs/media-law-monitor/2020/10/dmca-for-online-providers)).
- **The designated agent.** Filed online for **$6**, renewed **every 3 years**, otherwise it
  expires ([DMCA directory](https://www.copyright.gov/dmca-directory/),
  [FAQ](https://www.copyright.gov/dmca-directory/faq.html)).

### 6.3 European Union: DSA hosting rules

The Digital Services Act is Regulation (EU) 2022/2065
([EUR-Lex](https://eur-lex.europa.eu/eli/reg/2022/2065/oj)). Our fetch of the text failed, so
these summaries come from secondary sources; check them against EUR-Lex.

| Article | What it covers | Source |
|---|---|---|
| Art. 6 | The hosting exemption: no liability without knowledge, and quick action once aware | — |
| Art. 8 | No general duty to monitor | — |
| Art. 16 | A notice-and-action mechanism; a substantiated notice can give the host knowledge | [sota.io](https://sota.io/blog/eu-dsa-notice-action-system-saas-2026) |
| Art. 17 | A statement of reasons to the affected user | [presencis](https://presencis.com/regulations/dsa/article-17/) |
| Art. 23 | Measures against repeated misuse | — |

The DSM Copyright Directive's Art. 17 applies to services that **organise and promote** large
amounts of uploaded works ([Commission guidance, Merlin](https://merlin.obs.coe.int/article/9254)).
Keeping all curation on the publisher's home, with no Maktaba picks or recommendations, keeps
Maktaba Cloud on the hosting side of that line. Counsel confirms.

### 6.4 To confirm with a lawyer before launch

This is the same list as README §14.9, kept short:

1. That the hosting-status list (designated agent, notice and takedown, repeat-infringer
   policy, acting on knowledge, publisher terms) is sufficient in the US and the EU for our
   setup. Our setup: segments kept for minutes, an infrastructure price, and curation on the
   home.
2. What counts as enough proof of a source's permission, and that CC BY attribution as shown is
   enough.
3. That airing CC BY items and items whose source has given permission is consistent with
   YouTube's Terms' download and redistribution rules.
4. Where Maktaba is established, and any duty that brings (a contact point, an EU
   representative).
5. The short Broadcaster agreement, the viewer terms and the privacy notice.

## 7. Abuse and moderation patterns

- **Viewer limits** (Epic 34 R9.4): concurrent viewers per channel and per publisher, sessions
  per IP (salted hash, kept ≤ 24 h), Turnstile for open channels, 10-minute tokens refreshed by a
  heartbeat (R4.3/R4.4), and Workers rate limiting per IP (permissive, per location, not an
  accounting system:
  [Rate Limiting binding](https://developers.cloudflare.com/workers/runtime-apis/bindings/rate-limit/)).
- **The signed revocation list** (Epic 54 §6.3): Ed25519, a version that only goes up, an expiry
  for copies held by homes, entries naming a target and an action. Here the entries name a
  channel, publisher, home or `global`, and the actions are `off_air`, `suspend` and `terminate`.
  The edge is our own code, so it keeps enforcing the last list it has; entries are sticky.
  Homes honour it too (they stop uploading and tell the admin why).
- **Cost abuse:** every Worker request is billed, so a request flood costs money. Rate limits,
  Turnstile and token checks run before any R2 read. A monthly account-wide spend alert sits on
  the admin revenue page (25.21).

## 8. Summary of choices (detail in README §14)

| Question | Choice | Main alternative and why not |
|---|---|---|
| Where to fan out | Cloudflare edge: Worker + Durable Object + R2 on **Maktaba's** account | Hetzner relay: 30 viewers a node at 1080p, no edge, competes with remote play. |
| How the home sends | **Push finished segments** (P-seg), outbound HTTPS | Pull through the relay: inbound to the home, first-viewer latency, terms issue. |
| Ladder | **At home**, top rung `copy` | Cloud transcode: Stream is fine as a fallback (P-rtmp); Hetzner software encoding costs ≈ €43 a channel a month. |
| Latency | Standard HLS, 4 s segments, ≤ 20 s p95 | LL-HLS: ≈ 10× writes; not needed for 24/7 library channels. |
| Metering | **Viewer-hours** | GB: doesn't match the primary path's costs or viewers' intuition. |
| Fallbacks | Stream (P-rtmp), then Bunny | Mux: $1,369 a channel a month to encode. |
| Discovery | **Unlisted only** (link or code); a later directory would list only what publishers choose | Maktaba-picked listings: curation by the host (§6.0). |
| Viewer payments | **None: viewers never pay** (owner, 2026-11-03); access is open or invite | Paid passes: brings a seller-of-record problem (Epic 54.19). |
| What can air | **Publishable items only**, decided on the publisher's home (own recordings, CC BY, sources with permission) | Anything in the library: YouTube's Terms and copyright don't allow re-broadcasting without permission. |
| Maktaba's role | **Infrastructure and hosting provider**: no cloud-side curation | Curating or recommending: weakens the hosting position (§6.3). |
