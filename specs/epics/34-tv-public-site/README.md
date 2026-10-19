# Epic 34 — Maktaba TV public site (working name `tv.maktaba.com`)

> **Status:** spec. **Source:** `specs/epics/34-tv-public-site/`.
> **Anchors:** [Epic 32 — TV across the cluster and remote viewing through Cloudflare Stream](../32-tv-streaming-cluster-remote/README.md)
> (branch `docs/epic-32-tv-streaming`, PR #60: live inputs, the push by stream copy, signed playback,
> `home_secrets`, cost and budget 32.10, simulcast 32.13), Epic 33 — TV staging, hot swap and change
> management (branch `docs/epic-33-tv-staging`, in progress), [Epic 27 — Live channels](../27-live-channels/README.md)
> (scheduler, EPG/XMLTV, live player 27.7, filler 27.10), [Epic 25 — Cloud relay](../25-cloud-relay/README.md)
> (Hetzner + Cloudflare DNS/TLS, Maktaba cloud accounts), [Epic 31 — Multi-instance](../31-multi-instance/README.md).

**Documents.** [design.md](design.md) (site architecture, the publisher, the public feed and the
push, the Worker and its Durable Object, tokens, access modes, channel switching, budget, deployment)
· stories `story-34-01` … `story-34-11` · plans `plan-34-01` … `plan-34-11`.

## Goal

The owner said: *"why not have a tv.maktaba.com, will check domain later, as another feature for
people to watch using Cloudflare"*.

Epic 32 lets **signed-in Maktaba users** watch a channel away from home through Cloudflare Stream.
This epic adds a **public website** where **anyone the owner allows** (everyone, everyone who passes
a bot check, invited people, or Maktaba account holders) opens a web page, sees the channel grid and
the guide, and watches the channels the owner made public. Everything a visitor touches runs on
Cloudflare: a Worker with static assets serves the site and its API, R2 holds the published guide,
a Durable Object coordinates sessions and talks to the home, and Cloudflare Stream delivers the
video. **The visitor never reaches the home.** The home only connects outward: it publishes the
lineup and guide, keeps an outbound control connection to the Worker, and pushes the public
channels to their Stream live inputs.

The domain is not decided: it is a setting (`tv_public.domain`). Until it is set the site runs on its
`*.workers.dev` address.

## Decisions (owner and recommendations)

| Question | Decision |
|---|---|
| Hosting | **One Cloudflare Worker with static assets** (Cloudflare's recommendation for new projects; Pages still works but new features go to Workers), on the **owner's own Cloudflare account** — the same account as Epic 32's Stream. |
| What the home sends | Outbound only: the public lineup, logos, the guide for the next `guide_days` days, and live channel state. Published to R2 through the Worker's home endpoint on every schedule change. |
| Video | The Epic 32 push (same encode, stream copy) to **dedicated public live inputs**, one A/B pair per public channel. |
| Always-on or on demand | **Always-on, full quality, for every public channel** (owner's uplink is 1 Gbps: 22 × ≈ 6.5 Mb/s ≈ 143 Mb/s ≈ 15 %). Ingest is free and delivery is billed only when watched. The constraint is **encoder capacity** (22 encodes 24/7 across the M3 Ultra's VideoToolbox and the PC's NVENC via Epic 32 balancing), not bandwidth. "Lower public rendition" and "always-on only for the top N, on demand for the rest" exist as settings for smaller uplinks, **off by default**. |
| Control channel | The home keeps an **outbound WebSocket** to a Durable Object (`SiteControl`); the Worker never calls the home. Used for on-demand wake (only when that option is on), state reports, panic off-air. Fallback: long-poll over HTTPS. |
| Which channels | Per-channel **"public" flag** with a **rights confirmation** by an admin, default off; per-programme **exclusions** (series, collection, item) shown as a slate "Not available online". |
| Viewer access | Modes `public`, `turnstile` (default), `access` (Cloudflare Access, invite-only), `accounts` (Maktaba cloud accounts, phase D). |
| Tokens | Minted by the Worker (RS256, Stream signing key in a Worker secret), TTL 10 min, optionally bound to the viewer IP; public inputs `requireSignedURLs: true` and `allowedOrigins` = the site's hostnames. |
| Deployment | Maktaba deploys the site to the owner's account through the Cloudflare API (bundle shipped with each Maktaba release); developers use `wrangler deploy --env staging|production`; CI builds and tests the bundle. |

## Scope

- Per-channel public flag, rights confirmation, per-programme exclusions, the "public" admin page.
- The publisher: lineup, logos, guide (now/next/EPG) for `guide_days`, versioned snapshots in R2.
- The public feed on the encoding host: always-on push to an A/B pair of public inputs, exclusion
  slates, rotation without a break, optional lower rendition and tiering.
- The Worker: static site, public API, token minting, sessions, the `SiteControl` Durable Object,
  the home endpoints.
- The site UI: channel grid, now/next, EPG, player, mobile-first, Arabic (RTL) and English, share
  links, embeddable player, SEO basics.
- Channel switching (controls and speed) as a shared player module also used by Epic 27.7 and Epic 32.9.
- Access modes, branding and domain settings.
- Budget, abuse controls, viewer counts, analytics.
- Deployment (prod and staging), versioning, rollback.
- Docs and runbook.

## Non-goals

- On-demand video (VOD), recordings, catch-up, DVR on the public site.
- Comments, chat, user profiles, likes. The site stores no user data.
- Monetisation (ads, subscriptions, payments).
- Native apps for the public site (the site is a responsive web app; an installable PWA is enough).
- Any path where a visitor's request reaches the home or the Epic 25 relay.
- Hosting the site anywhere other than the owner's Cloudflare account.
- Re-encoding for the public by default (only behind the `rendition` setting for small uplinks).
- Legal advice. The rights confirmation records the owner's statement; it does not check rights.

---

## Requirements

### R1 — Public channels and rights
- R1.1 Each channel has `public` (default **off**). Turning it on requires an admin to tick a rights
  confirmation ("I hold the rights, or permission, to show every programme of this channel to the
  public, except the exclusions below") with an optional note; the confirmation stores who and when.
- R1.2 Changing a public channel's programming source (rules, collections, Epic 33 staged change set)
  shows a reminder to re-check rights; adding a new series to a public channel lists it as
  "new on a public channel" until reviewed (not blocking).
- R1.3 Exclusions: a series, a collection, a single item, or a tag can be marked "not for public".
  Rule `tv_public.default_programme_policy` = `allow` (default: everything not excluded plays) or
  `deny` (only items in a "cleared" list play; everything else gets the slate).
- R1.4 During an excluded programme the public feed shows the slate "Not available online" (branded,
  with the next programme's title and start time); LAN, Plex and Epic 32 remote viewers keep the
  real programme. The guide shows the slot as "Not available online" without the title.
- R1.5 A global **Panic off-air** stops every public token at once (R4.6) and turns the site to
  "Off air" in under 30 s.

### R2 — Publishing and the live guide (home → Cloudflare, outbound only)
- R2.1 The publisher keeps on Cloudflare: `site.json` (branding, languages, access mode, public
  config), `lineup.json` (public channels), one immutable blob per channel-day of guide for
  `guide_days` days (default 3, max 7 = the `feat/tv-core` horizon), logos and slate art.
- R2.2 **Real time.** Every schedule change is published as a **small per-channel delta within
  seconds**: `write_channels`, horizon top-up, an Epic 33 commit or hot swap, a live-slot change,
  an edit mid-programme, a public flag/exclusion change, and **now/next drift** (a block that starts
  late or runs long: the station reports actual start/end and the publisher re-times the rest of the
  day). Triggers are coalesced per channel for at most 2 s. A full refresh also runs every 6 h and on
  demand. Each run is a queue-engine job `tv_public_publish` (visible in the control centre; delta
  runs are batched into one job per 2 s window).
- R2.3 A delta uploads only the changed channel-day blobs (content-addressed, `HEAD` first), then
  commits a new index in one call; the Worker's `SiteControl` Durable Object records the new
  snapshot id (atomic for readers) and **notifies every open page** (R2.7). Old snapshots are
  deleted after 24 h.
- R2.4 Published data never contains file paths, library or item ids, user data, hostnames, IPs,
  Plex data or internal channel ids; channels are identified by `public_id` (HMAC of the channel id
  with a site salt) and a public slug.
- R2.5 Artwork (posters) is published only when `tv_public.publish_artwork` is on (default off:
  posters may carry their own rights); channel logos are always published.
- R2.6 A publish failure keeps the previous snapshot live, retries with backoff and raises a
  control-centre warning; the site shows the guide's "last updated" time.
- R2.7 **Live pages.** Open pages hold one WebSocket (`/api/v1/live`) to a `LiveHub` Durable Object
  (Hibernation API, so idle pages cost nothing; sharded, 32,768 sockets per object at most);
  `SiteControl` fans each commit out to the shards; a commit sends
  `{type:"guide", channel, date, blob}` (and `{type:"state", channel, on_air, slate}` for live
  state); the page fetches the changed blob and redraws. Fallback when the WebSocket cannot connect: a short
  poll of `/api/v1/version` every 15 s. **A schedule change on the home is visible in an open
  public guide within 10 s** (p95), including an edit to the programme on air now.
- R2.8 **Now/next and progress are computed in the browser** from the published schedule and the
  server clock offset (from the `Date` of API responses / an `X-Server-Time` header); nothing polls
  the home, and the Worker does not recompute now/next per request.
- R2.9 **Every link stays on Cloudflare.** Every guide cell and channel tile links to a site URL —
  `/<lang>/watch/<slug>` (live) or `/<lang>/watch/<slug>?at=<ISO time>` (a programme: if it is on
  now, play live; if it is later, show its details with "starts in …" and switch to live when it
  starts; if it is past, show "aired at …" and offer live). The Worker behind that page mints the
  signed Stream token and the player plays Cloudflare HLS. No URL on the site, in published data, in
  share links or in the embed ever points to the home or the Epic 25 relay. Channel switching
  (up/down, number, guide tap) uses the same URLs and the warm path of R6.

### R3 — Video for public channels
- R3.1 Every public channel is pushed **always-on** while `tv_public.enabled` and the channel is
  public, at the channel's own encode (stream copy, no second encode). Default
  `tv_public.tiering = all_always_on`.
- R3.2 Each public channel gets a dedicated pair of live inputs (A/B), `recording.mode = automatic`
  (needed for HLS), `requireSignedURLs = true`, `allowedOrigins` = the site hostnames (+ the Maktaba
  app origin when Epic 32 remote reuses the input, R3.7), `timeoutSeconds = 30`.
- R3.3 Rotation without a break: every `rotate_hours` (default 2) the host starts pushing to the
  idle input of the pair, the Worker moves new tokens and live sessions to it (players switch at a
  segment boundary, design §6.4), then the old push stops and its recording is deleted. This bounds
  storage and avoids the 7-day recording truncation.
- R3.4 Exclusion slates are produced on the encoding host as a parallel "public mux" fed with the
  channel's blocks, except excluded blocks, which are replaced by a slate block encoded with the
  channel's output settings (design §5). The slate costs one small extra encode only while an
  excluded programme runs.
- R3.5 Capacity: always-on public channels hold one encode each, 24/7, on the encoding hosts
  (Epic 32 placement). The admin page shows "public channels on air / capacity" per host and warns
  when public channels would leave fewer than `tv_public.reserve_slots` (default 2) free encode
  slots for LAN/Plex channels. A public channel that cannot be placed shows "Off air (no capacity)".
- R3.6 Options for small uplinks (off by default): `tv_public.rendition` = `copy` (default) |
  `720p` | `540p` (one extra encode per public channel at `rendition_kbps`, default 3000, inside
  Cloudflare's 2–14 Mb/s ingest range); `tv_public.tiering` = `top_n` keeps the `always_on_count`
  most-watched (or pinned) channels always-on and wakes the rest on demand through `SiteControl`
  (idle stop after `on_demand_idle_minutes`, default 5). The settings page shows the uplink use of
  the current choice against `tv_public.uplink_mbps` (default 1000).
- R3.7 One push per channel: when a public channel is also tuned by an Epic 32 remote viewer, the
  remote session plays the public input (no second push); simulcast outputs (Epic 32.13) attach to
  the same live input and therefore also get the exclusion slate.
- R3.8 Continuity: an Epic 33 hot swap changes what the station plays, not the push; an Epic 32
  failover restarts the push on the new host on the same input inside `timeoutSeconds`, so public
  viewers keep the same playback URL and token.

### R4 — The Worker, API and tokens
- R4.1 One Worker serves the static site (`assets`), the public API (`/api/v1/*`), the embed page,
  `sitemap.xml`/`robots.txt`, and the home endpoints (`/_home/*`, HMAC-authenticated).
- R4.2 `POST /api/v1/play {channel}` checks access mode, on-air state, budget and limits, then
  returns `{session_id, hls_url, expires_at, refresh_after, state, neighbours[]}` with a Stream token
  (`sub` = the channel's active input id, `exp` = now + `token_ttl_minutes` (default 10),
  optional `accessRules` for countries and the viewer IP).
- R4.3 The player heartbeats every 60 s; the heartbeat refreshes the token after half its life and
  returns the new URL after an A/B rotation.
- R4.4 Sessions without a heartbeat for 150 s end; no new token is minted for them, so a scraper that
  does not heartbeat loses access within one TTL.
- R4.5 The Stream signing key for the site is **separate** from Epic 32's home key (created by the
  home through the Stream keys API, stored as Worker secret `STREAM_KEY_PEM`/`STREAM_KEY_ID`, never
  in R2), so rotating one never cuts the other's viewers.
- R4.6 Panic off-air: the DO refuses all play/heartbeat calls and the home rotates the site key
  (deleting the old key at Cloudflare invalidates every outstanding public token).
- R4.7 When the home is offline (no control connection for 60 s) the site keeps serving the last
  guide and shows channels as "Off air"; no token is minted for an input that is not live.

### R5 — Site UI
- R5.1 Pages: home (channel grid with now/next and progress), guide (EPG grid, channels × time,
  `guide_days`, live-updating), channel page `/<lang>/watch/<slug>` (player + now/next + today's schedule), embed (`/embed/<slug>`), about,
  legal (terms, privacy, copyright/takedown contact — placeholders the owner edits).
- R5.2 Mobile-first, works at 360 px; keyboard and screen-reader accessible (axe clean).
- R5.3 Arabic (RTL, default) and English; language from the URL prefix (`/ar/…`, `/en/…`), then
  `Accept-Language`; programme titles shown as published (Arabic/English as stored).
- R5.4 Share links per channel and per programme (`/<lang>/watch/<slug>`, `?at=`), Web Share API on mobile, copy-link elsewhere;
  embeddable player (`<iframe src=".../embed/<slug>">`) allowed only for `embed_origins`
  (CSP `frame-ancestors`).
- R5.5 SEO basics: per-page title/description/OG image/`hreflang` injected by the Worker,
  JSON-LD (`TelevisionChannel`, `BroadcastEvent` for now/next), `sitemap.xml`, `robots.txt`;
  `tv_public.indexable` (default **off**: `noindex` until the owner opts in; forced off in `access`
  and `accounts` modes).
- R5.6 Times shown in the viewer's time zone with the channel's zone in a tooltip; the guide states
  "last updated".

### R6 — Channel switching
- R6.1 Controls: on-screen channel up/down, swipe up/down on the player (mobile), keys ↑/↓ and
  PageUp/PageDown, digits + Enter (or 1.5 s timeout), `L`/Backspace for last channel, `G` for the
  guide, tap a channel in the grid or the guide, a mini-guide overlay with now/next for all channels,
  deep links per channel.
- R6.2 Make before break: the current channel keeps playing until the new channel has its first
  segment buffered; then the player cuts. While a channel is coming up its "tuning" card (logo,
  now/next) is shown immediately — never a black screen.
- R6.3 Speed: the play response includes tokens for the neighbours (previous, next, last channel);
  the player prefetches **manifests only** (not segments) of the neighbours so a switch skips the
  manifest round trip without paying delivery minutes (Stream bills segment requests; preloaded
  segments are billable).
- R6.4 Targets: warm channel (always-on, the default for every public channel) < 2 s from the key
  press to the first frame (p75 on broadband); cold channel (only with `tiering = top_n`) tuning
  card immediately (< 100 ms) and video < 20 s (to be measured in the 34.3 spike; Cloudflare
  documents "a brief delay" before a newly connected input becomes playable).
- R6.5 The switching module (`@maktaba/tvplayer`) is shared with the Maktaba web live player
  (Epic 27.7: up/down/digits already specified) and Epic 32.9 remote viewing.

### R7 — Access modes
- R7.1 `public`: anyone. `turnstile` (default): one Turnstile check per browser session (server-side
  siteverify in the Worker) → a signed, HttpOnly session cookie (12 h) required by `play`.
  `access`: a Cloudflare Access application on the site hostname with an allow-list of emails
  (One-time PIN or an identity provider); the Worker verifies the `Cf-Access-Jwt-Assertion`.
  `accounts` (phase D): sign-in with the Epic 25 Maktaba cloud account; the home publishes the
  allowed account ids (hashed).
- R7.2 The guide and lineup are readable in `public` and `turnstile` modes without a check; in
  `access`/`accounts` modes the whole site is behind sign-in.
- R7.3 Changing the mode is applied by the deploy job (Access application, Turnstile widget) and
  takes effect without redeploying the site code (config in `site.json` + Worker vars).

### R8 — Branding and domain
- R8.1 Settings: site name (ar/en), logo, favicon, colours (primary, accent, background; contrast
  checked AA), about page (Markdown, ar/en), footer text, legal page texts, slate image and texts,
  "off air" message, default language.
- R8.2 Domain: `tv_public.domain` (e.g. `tv.example.com`, empty = workers.dev); must be a zone in the
  owner's Cloudflare account; the deploy job attaches it as a Workers Custom Domain (Cloudflare creates
  the DNS record and certificate); `tv_public.staging_domain` likewise for staging.
- R8.3 Changing the domain updates `allowedOrigins` on every public input, the Turnstile widget
  hostnames, the Access application and the canonical URLs, in that order, before the old hostname
  is removed.

### R9 — Budget, abuse and analytics
- R9.1 Extends Epic 32.10: a separate public budget `tv_public.monthly_budget_usd` (default 20), with
  the account-wide estimate (remote + public + storage) shown next to it.
- R9.2 Live accounting in the DO from heartbeats (session minutes, errs high), reconciled hourly
  with Stream GraphQL `streamMinutesViewedAdaptiveGroups` for the public inputs' video ids.
- R9.3 At 80 %: admin notification. At 100 %: `budget_action = block_new` (default; running sessions
  finish their current programme, at most 60 min) or `off_air` (site switches to "Off air" at once).
- R9.4 Limits: `max_concurrent_viewers` (default 50), `max_sessions_per_ip` (default 3),
  `viewer_daily_minutes` per browser session (default 240; 0 = none), `allowed_countries`
  (token access rules), Workers Rate Limiting on the API (per IP: 30 play / 60 s, 120 other / 60 s).
- R9.5 Viewer counts: Stream `…/<input>/views` (`liveViewers`) per channel every 60 s; shown on the
  admin page and on the site only if `show_viewer_count` (default off; `hideLiveViewerCount` set
  accordingly on the inputs).
- R9.6 Analytics without personal data: Workers Analytics Engine events (play, switch, heartbeat,
  error; channel, country, device class, hashed session) + Stream minutes; admin usage panel on the
  home pulls aggregates from the Worker.

### R10 — Deployment
- R10.1 The site lives in `web/tv-site/` (Vite + React SPA + Worker in TypeScript) and is built by CI;
  each Maktaba release attaches `maktaba-tv-site-<version>.tar.gz` (assets + bundled Worker + manifest).
- R10.2 The home deploys it to the owner's account through the Cloudflare API (Workers Scripts with
  static assets upload, R2 bucket, Durable Object migration, secrets, rate limit and Analytics Engine
  bindings, Custom Domain, Turnstile widget, Access app) from the admin page: **Deploy to staging**,
  **Promote to production**, **Roll back**.
- R10.3 Environments: `staging` (`maktaba-tv-site-staging`, its own bucket and DO, staging domain or
  workers.dev, always `noindex`) and `production`. The home can publish to both; staging gets the
  same data so a new site version is checked against real data before promotion.
- R10.4 Versioning: site version = Maktaba version; the published snapshot carries `schema: 1`; the
  Worker refuses a snapshot with a newer major schema and keeps the previous one.
- R10.5 Developer path: `pnpm -C web/tv-site deploy:staging` (`wrangler deploy --env staging`) with a
  local `.dev.vars`; never used by the product.

### R11 — Security and privacy
- R11.1 Nothing from the home is reachable: no inbound port, no relay, no proxying through the Worker;
  the home only makes outbound HTTPS/WSS calls (Worker home endpoints, Cloudflare API, RTMPS/SRT).
- R11.2 Home ↔ Worker: HMAC-SHA256 over `method + path + timestamp + body hash` with the home key
  (Worker secret `HOME_KEY`; on the home in `home_secrets`), 60 s clock window, nonce replay cache in
  the DO.
- R11.3 No visitor data stored: no accounts (except `accounts` mode, which keeps only the cloud
  account id in the session cookie), no IP stored (rate limiting and per-IP counts use a salted hash
  kept in memory/DO for at most 24 h), one functional cookie.
- R11.4 Security headers: CSP (self + Stream customer domain + Turnstile), HSTS, `X-Content-Type-Options`,
  `Referrer-Policy: strict-origin-when-cross-origin`, `Permissions-Policy`.
- R11.5 Legal pages are placeholders the owner must review before turning `tv_public.enabled` on;
  the enable switch asks for that confirmation.

### R12 — Docs
- R12.1 Runbook: account prerequisites (Workers Paid, Stream, R2, Turnstile/Access), API token
  permissions, first deploy, choosing the domain, making a channel public, exclusions, budget sizing,
  panic off-air, home offline, rotation, rollback, costs.

## Acceptance (epic)

- With `tv_public.enabled`, two channels public and the site deployed to workers.dev, a phone on mobile
  data opens the site (Arabic, RTL), passes Turnstile, sees the grid with now/next, and plays channel
  105 within 3 s; switching to 106 with a swipe shows the first frame in < 2 s.
- No request from the phone reaches the home (home firewall log and Worker logs show only home →
  Cloudflare traffic).
- A schedule change on the home (an edit to tonight's programme, or a hot swap of the programme on
  air) is visible in an open public guide within 10 s, without reloading the page.
- An excluded series on channel 105 shows the slate on the site while the LAN player shows the
  episode; the guide shows "Not available online" for that slot.
- A rotation at the 2-hour mark and an Epic 32 failover of channel 105's host do not stop playback
  for a site viewer (at most one short stall); an Epic 33 hot swap changes the programme without any stall.
- With the budget at 100 % and `block_new`, a new visitor gets "Off air: monthly limit reached";
  panic off-air stops all playback within 30 s.
- Setting the domain `tv.example.com` makes the site available there with a valid certificate, and
  playback works from it (and stops working from the old hostname after removal).

---

## Architecture

```
                 HOME (Mac Studio, + PC node via Epic 32)                            CLOUDFLARE (owner's account)
 ┌──────────────────────────────────────────────────────────────┐      ┌────────────────────────────────────────────────────────┐
 │ Maktaba API                                                   │      │ Worker  maktaba-tv-site (custom domain = tv_public.domain)│
 │  tvpublic: settings, rights, exclusions, publisher job ───────┼─HTTPS┼─► /_home/publish  (HMAC) ──► R2 bucket                │
 │            deploy job (Cloudflare API) ───────────────────────┼─HTTPS┼─► Workers/R2/DO/Custom Domain/Turnstile/Access APIs    │
 │            control client ────────────────────────────────────┼─WSS──┼─► /_home/ws ──► Durable Object SiteControl            │
 │            budget reconcile, viewer counts                    │      │      sessions, limits, budget, on-air state, wake      │
 │  home_secrets: CF token, HOME_KEY, site signing key           │      │  /api/v1/* lineup, guide, play, heartbeat (tokens)     │
 │ Station (per public channel, Epic 27/32 placement)            │      │  DO LiveHub (shards): WebSocket fan-out to open pages  │
 │  block encodes → channel mux → Hub → LAN / Plex / Epic 32     │      │  static assets: SPA (ar/en), embed, sitemap; AE; RL    │
 │                └→ public mux (excluded blocks → slate) ──┐    │      └───────────────▲────────────────────────────────────────┘
 │  public pusher (ffmpeg -c copy) ◄────────────────────────┘    │                      │ HTML/JSON + token
 │       └──── RTMPS/SRT (outbound) ─────────────────────────────┼──────► Cloudflare Stream: public live inputs A/B per channel
 └──────────────────────────────────────────────────────────────┘        (recording automatic, signed URLs, allowedOrigins=site)
            ▲ nothing inbound                                                      │ HLS (signed, customer-<code>.cloudflarestream.com)
                                                                         ┌─────────┴──────────┐
                                                                         │ visitors (browser)  │
                                                                         └────────────────────┘
```

AE = Workers Analytics Engine, RL = Workers Rate Limiting binding. Every arrow that crosses the
home boundary starts at the home.

## Data published (R2, prefix per environment)

| Key | Content | Written |
|---|---|---|
| `current.json` | `{schema:1, snapshot, published_at, guide_until, home_version}` (also held by `SiteControl`, which is the source of truth for readers) | last, each commit |
| `s/<snapshot>/site.json` | branding (names, colours, logo key, about/legal Markdown ar/en), languages, access mode, slate texts, `indexable`, `show_viewer_count`, feature flags | when changed |
| `s/<snapshot>/lineup.json` | `[{public_id, slug, number, name{ar,en}, logo, category, description{ar,en}, tier}]` | when changed |
| `b/<sha256>.json` | one channel-day of guide: `[{start, end, title, subtitle, episode, description, category, rating, artwork?, available}]`; `available:false` → title and description replaced by the slate text | content-addressed |
| `b/<sha256>.<ext>` | logos, slate art, optional artwork, resized (≤ 512 px logos, ≤ 640 px posters) | content-addressed |
| `s/<snapshot>/index.json` | map `guide/<public_id>/<date>` → blob, `logo/<public_id>` → blob | each run |

**Never published:** item/library/channel ids, file paths, hostnames/IPs, user names or ids,
Plex data, encoder settings, Stream keys or input ids (input ids are known only to the Worker DO,
pushed over the control channel).

Live state (not in R2): per channel `{on_air, active_input, slate_now, viewers}` reported by the home
over the control connection into the DO.

## Settings (rules section `tv_public`; defaults keep today's behaviour: off)

| Key | Default | Meaning |
|---|---|---|
| `enabled` | `false` | The public site publishes and pushes. |
| `domain` / `staging_domain` | `""` | Custom domains (zones in the owner's account); empty = workers.dev. |
| `access_mode` | `turnstile` | `public` \| `turnstile` \| `access` \| `accounts`. |
| `access_emails` | `[]` | Allow-list for `access` mode. |
| `languages` / `default_language` | `["ar","en"]` / `ar` | Site languages. |
| `guide_days` | `3` | Days of guide published (1–7). |
| `publish_artwork` | `false` | Publish programme posters. |
| `default_programme_policy` | `allow` | `allow` (exclusions) \| `deny` (cleared list only). |
| `tiering` | `all_always_on` | `all_always_on` \| `top_n`. |
| `always_on_count` | `22` | With `top_n`: channels kept always-on. |
| `on_demand_idle_minutes` | `5` | With `top_n`: stop a woken channel after its last viewer. |
| `rendition` / `rendition_kbps` | `copy` / `3000` | `copy` \| `720p` \| `540p`; extra encode only when not `copy`. |
| `uplink_mbps` | `1000` | For the uplink check on the settings page. |
| `reserve_slots` | `2` | Encode slots kept free for LAN/Plex channels. |
| `rotate_hours` | `2` | A/B input rotation period (1–24). |
| `token_ttl_minutes` | `10` | Public token lifetime (2–60). |
| `bind_token_ip` | `true` | Add an `ip.src` access rule to each token (re-minted on IP change). |
| `allowed_countries` | `[]` | ISO codes; empty = any. |
| `max_concurrent_viewers` | `50` | Site-wide. |
| `max_sessions_per_ip` | `3` | Per hashed IP. |
| `viewer_daily_minutes` | `240` | Per browser session; 0 = none. |
| `monthly_budget_usd` | `20` | Public delivery + share of storage. |
| `budget_action` | `block_new` | `block_new` \| `off_air`. |
| `show_viewer_count` | `false` | Show live viewers on the site. |
| `indexable` | `false` | Allow search engines. |
| `embed_origins` | `[]` | Origins allowed to embed the player. |
| `branding.*` | Maktaba defaults | Name, logo, colours, about, legal, slate, off-air texts (ar/en). |

Per channel (`tv_public_channels`): `public`, `rights_confirmed_by/at`, `rights_note`, `pin_always_on`,
`slug`. Per programme (`tv_public_exclusions`): kind (`series|collection|item|tag`), ref, mode
(`exclude|clear`), reason.

## APIs

```
# Home (admin), through the normal Maktaba API
GET    /api/tv/public                         settings, deploy state, publish state, channels, usage, warnings
PUT    /api/tv/public/settings                rules section tv_public
PUT    /api/tv/public/channels/{id}           {public, rights_confirmed, rights_note, slug?, pin_always_on?}
GET|POST|DELETE /api/tv/public/exclusions     list / add {kind, ref, mode, reason} / remove
POST   /api/tv/public/publish                 run the publisher now (queue job)
POST   /api/tv/public/deploy                  {env: staging|production, version?}  (queue job)
POST   /api/tv/public/promote | /rollback     production from staging / previous version
POST   /api/tv/public/off-air                 {on: bool}  panic switch
GET    /api/tv/public/preview/{channel}       what the public feed shows now (slate or programme)

# Worker — public
GET    /api/v1/site | /lineup | /now          cached 30 s
GET    /api/v1/guide?channel=&date=           cached 60 s
POST   /api/v1/session                        {turnstile_token} → Set-Cookie (turnstile mode)
POST   /api/v1/play                           {channel, prefer_neighbours?} → 200 {session_id, hls_url, expires_at,
                                              refresh_after, state:"live"|"slate", neighbours:[{slug, hls_url}]}
                                              | 202 {state:"tuning", retry_after} | 403 access | 429 limit | 503 off_air
POST   /api/v1/play/{session}/beat            → {expires_at, hls_url?, state}
DELETE /api/v1/play/{session}
GET    /api/v1/live                           WebSocket (LiveHub DO): guide deltas and live state for open pages
GET    /api/v1/version                        {snapshot, server_time}  (short-poll fallback)
GET    /embed/{slug}, /{lang}/, /{lang}/guide, /{lang}/watch/{slug}[?at=], /{lang}/about, /{lang}/legal/{page}
GET    /sitemap.xml, /robots.txt

# Worker — home (HMAC, outbound from the home)
GET    /_home/ws                              WebSocket to SiteControl: state reports up, wake/off-air/limits down
POST   /_home/publish/blob/{sha}              upload one blob (skip if present: HEAD first)
POST   /_home/publish/commit                  {snapshot, index delta, site?, lineup?, changed[]} → new snapshot,
                                              DO notifies open pages
POST   /_home/state                           fallback for state reports when WS is down
GET    /_home/usage?from=&to=                 aggregates for the admin panel
```

## Cost model

Cloudflare bills Stream on **minutes delivered** ($1 per 1,000, counted per segment requested,
preloading included) and **minutes stored** (prepaid, $5 per 1,000 minutes of capacity; live
recordings count while they exist). Ingest and encoding are free; a broadcast nobody watches costs
nothing in delivery. Workers Static Assets requests are free; the Worker API, Durable Object, R2 and
Analytics Engine fit in the Workers Paid plan ($5/month minimum) for this load.

**Storage with always-on A/B rotation:** each public channel holds at most one recording of
`rotate_hours` (plus a few minutes of overlap) → 22 channels × (120 + 5) min ≈ 2,750 min → a
**3,000-minute plan, $15/month**. (`rotate_hours = 4` → 5,500 min → $30.)

**Worked example — 100 viewers × 2 h/day:**

| Item | Calculation | Month |
|---|---|---|
| Delivery | 100 × 120 min × 30 days = 360,000 min × $1/1,000 | **$360** |
| Stream storage | 3,000-min plan (22 always-on channels, 2 h rotation) | $15 |
| Workers Paid | base plan; requests, DO, R2, KV within the included usage | $5 |
| **Total** | | **≈ $380 / month** |

Other sizes: 10 viewers × 2 h/day ≈ $36 + $20 = **$56**; 1,000 viewers × 2 h/day ≈ **$3,620**.
Public audiences scale the bill linearly: the default budget ($20) covers about **20,000 delivered
minutes ≈ 11 viewer-hours per day**. The runbook explains how to size it.

**Resources at home:** always-on means one encode per public channel 24/7 (22 channels → 22
VideoToolbox/NVENC sessions across the hosts) plus upload of ≈ 6.5 Mb/s each (≈ 143 Mb/s, 15 % of the
1 Gbps uplink); slates add one small encode only while an excluded programme runs; the A/B rotation
doubles one channel's upload for ≈ 1–3 min every `rotate_hours`.

## Security

- Inbound: nothing. The home makes only outbound calls; the Worker cannot reach the home.
- Secrets: Cloudflare API token (scopes in the runbook), `HOME_KEY`, the site signing key PEM — on the
  home in `home_secrets` (Epic 32 slot 0119); in Cloudflare as Worker secrets. The signing key never
  enters R2; input ids live only in the DO.
- Viewers: signed, short-lived tokens; signed inputs; `allowedOrigins`; optional IP binding and
  country rules; Turnstile or Access; rate limits; concurrency caps; panic off-air with key rotation.
- Content: rights confirmation per channel, exclusions with slates, takedown contact on the site,
  per-channel "make private" that removes the channel from the next publish and stops its push.
- Privacy: no visitor accounts or profiles; no IPs stored; aggregate analytics only; privacy page
  lists Cloudflare (Workers, Stream, Turnstile) as processors.

## Rollout

1. **Phase A — guide only** (34.1, 34.2, 34.4 without play, 34.5 grid/guide, 34.8, 34.10):
   the site with the public lineup and guide, no video ("watch at home" call to action). Zero Stream cost.
2. **Phase B — video** (34.3, 34.4 play, 34.6, 34.7 `public`/`turnstile`, 34.9): one or two public
   channels always-on, turnstile mode, workers.dev, budget $20.
3. **Phase C — full lineup and domain**: all chosen channels public, custom domain, `indexable` if wanted.
4. **Phase D — invite and accounts modes** (34.7 `access`, `accounts`).
5. Docs (34.11) with each phase.

## Stories

| # | Story | Summary |
|---|---|---|
| 34.1 | [Public channel flag and rights](story-34-01-public-channel-flag-rights.md) | Per-channel public flag, rights confirmation, exclusions and cleared lists, admin page, panic switch state. |
| 34.2 | [Guide and lineup publisher (real time)](story-34-02-guide-lineup-publisher.md) | Per-channel deltas within seconds on every schedule change (incl. Epic 33 swaps, drift), content-addressed upload, live fan-out to open pages, client-side now/next, all links on the site. |
| 34.3 | [Public feed: always-on push, slates, rotation](story-34-03-public-feed-push-slates.md) | Public mux with exclusion slates, A/B inputs and rotation, capacity reserve, optional rendition and tiering. |
| 34.4 | [Worker API, tokens and SiteControl](story-34-04-worker-api-tokens.md) | Public API, token minting, sessions, the Durable Object, the home WebSocket and HMAC endpoints. |
| 34.5 | [Site UI: guide and player](story-34-05-site-ui-guide-player.md) | Grid, now/next, EPG, channel page, embed, share, SEO, RTL/ar + en, accessibility. |
| 34.6 | [Channel switching](story-34-06-channel-switching.md) | Shared `@maktaba/tvplayer` controls and fast switching (make before break, neighbour manifests, tuning card). |
| 34.7 | [Access modes](story-34-07-access-modes.md) | public, Turnstile, Cloudflare Access invite-only, Maktaba accounts. |
| 34.8 | [Branding and domain settings](story-34-08-branding-domain.md) | Branding fields, custom domain attach/switch, origins and widget updates. |
| 34.9 | [Budget, abuse controls and analytics](story-34-09-budget-abuse-analytics.md) | Public budget, limits, rate limits, viewer counts, Analytics Engine, reconcile with Stream analytics. |
| 34.10 | [Deployment pipeline](story-34-10-deployment-pipeline.md) | Build in CI, release artifact, deploy/promote/rollback via the Cloudflare API, staging, versions. |
| 34.11 | [Docs and runbook](story-34-11-docs-runbook.md) | Setup, operations, failure playbook, costs, legal checklist. |

## Dependencies

- **Epic 32** (spec; code after `feat/tv-core`): `home_secrets` (0119), the Cloudflare integration
  32.6, the pusher 32.7, signing 32.8, the budget 32.10, simulcast 32.13, placement and failover
  32.2/32.4. Epic 34 reuses the Cloudflare client and the pusher; it adds the public mux and the
  public input pairs.
- **Epic 33** (in progress): the "committed change" event the publisher listens to; hot swap must
  keep feeding the station's muxers (it does: the swap happens before the mux).
- **Epic 27**: scheduler (guide data), filler 27.10 (slate block uses the filler block path), player 27.7.
- **Epic 25**: Maktaba cloud accounts for `accounts` mode (phase D only).
- **Queue engine** (`feat/queue-engine`): jobs `tv_public_publish`, `tv_public_deploy`.

## Data model (proposed migration — provisional slot)

After Epic 32's 0119–0121 and any Epic 33 slots; renumbered to the next free integer when the file
lands (MANIFEST rule).

| Slot | Story | File | Tables |
|---|---|---|---|
| 0122 (prov.) | 34.1, 34.2, 34.3, 34.10 | `0122_tv_public.sql` | `tv_public_channels` (channel_id pk, public, slug unique, public_id, rights_confirmed_by, rights_confirmed_at, rights_note, pin_always_on, input_a, input_b, active_input, rotated_at, state, updated_at); `tv_public_exclusions` (id, kind, ref_id, mode exclude/clear, reason, created_by, created_at); `tv_public_publishes` (id, env, snapshot, started_at, finished_at, blobs_sent, bytes, status, error); `tv_public_deploys` (id, env, version, cf_version_id, status, started_at, finished_at, by, error); `tv_public_usage_daily` (day, env, minutes_delivered, sessions, peak_viewers, cost_usd). REVOKE from `maktaba_worker` except SELECT on `tv_public_channels`/`tv_public_exclusions` (stations on nodes need them for slates). |

## Open questions (with the recommendation used in this spec)

1. **Domain.** `tv.maktaba.com` is the working name; the owner will check. Any zone in the owner's
   Cloudflare account works; until then workers.dev. *Is `maktaba.com` available and wanted, or a
   subdomain of a domain the owner already has?*
2. **Rights.** Which channels are publishable at all? Library content (books' lectures, films,
   series) may not be. *Recommendation:* start with channels made only from content the owner made or
   has written permission for; default programme policy `allow` with exclusions, or `deny` for
   mixed channels.
3. **Default access mode.** `turnstile` (recommended) or `public`? Invite-only (`access`) for a
   private beta first?
4. **Budget.** $20/month default ≈ 11 viewer-hours/day. What monthly ceiling is acceptable, and
   should the site go `off_air` or `block_new` at 100 %?
5. **Encoder capacity.** 22 always-on public channels hold 22 encodes 24/7. Confirm the M3 Ultra +
   PC NVENC carry that plus LAN/Plex channels (`reserve_slots`), or make fewer channels public.
6. **Recording/storage.** Same as Epic 32 Q1 (HLS needs `recording.mode = automatic`); A/B rotation
   every 2 h bounds storage to ~3,000 min ($15). Confirm.
7. **`allowedOrigins` and native apps.** When an Epic 32 remote viewer in a native app plays the public
   input, there is no browser `Origin`; does `allowedOrigins` block it? Verify in the 34.3 spike;
   fallback: remote viewers of a public channel get their own pool push (two pushes).
8. **Token `sub` = live input id.** Public tokens name the input (stable across broadcasts); verify
   playback continues across the A/B switch with a fresh token and that `ip.src` rules behave on
   mobile networks (CGNAT, IPv4/IPv6 switching). Fallback: `bind_token_ip = false`.
9. **Cold start time** (only with `tiering = top_n`): not documented; measure in the spike.
10. **Accounts mode identity.** Use Epic 25 cloud accounts (OAuth at maktaba.app) — the only identity
    reachable without the home. OK, or drop `accounts` mode?
11. **Artwork.** Posters off by default (their rights differ from the programme's). OK?
12. **Workers Paid plan** ($5/month) is assumed (Durable Objects, KV/R2 volumes, CPU). OK?
13. **Access seats.** Invite-only via Cloudflare Access uses Zero Trust seats; check the seat limits of
    the owner's Zero Trust plan before choosing `access` for a large invite list.

## Cloudflare facts this design relies on (with sources)

Epic 32's facts table (live inputs, recording modes, signed URLs, pricing, webhooks, viewer count,
GraphQL analytics, simulcast) applies unchanged. In addition:

| Fact | Source |
|---|---|
| Workers Static Assets is the recommended way to deploy static sites, SPAs and full-stack apps; new projects should use Workers instead of Pages. One collection of assets per Worker; static asset requests are free. | [Workers best practices](https://developers.cloudflare.com/workers/best-practices/workers-best-practices/), [Static Assets](https://developers.cloudflare.com/workers/static-assets/), [Static assets binding](https://developers.cloudflare.com/workers/static-assets/binding/), [Migrate from Pages](https://developers.cloudflare.com/workers/static-assets/migration-guides/migrate-from-pages/) |
| Static assets can be uploaded with Wrangler or through the API (upload session with a manifest, upload files, then deploy the script with the completion token). | [Workers for Platforms: static assets](https://developers.cloudflare.com/cloudflare-for-platforms/workers-for-platforms/configuration/static-assets/) |
| Environments: `wrangler deploy --env staging` deploys `<name>-staging`; `workers_dev = true` per environment. | [Wrangler environments](https://developers.cloudflare.com/workers/wrangler/environments/), [Workers Builds advanced setups](https://developers.cloudflare.com/workers/ci-cd/builds/advanced-setups/) |
| Custom Domains: Cloudflare creates the DNS record and the certificate; the zone must be active in the account; no Custom Domain on a hostname with an existing CNAME; exact hostname match, no wildcards. | [Custom Domains](https://developers.cloudflare.com/workers/configuration/routing/custom-domains/) |
| When viewing a live stream by the live input id, the input's `requireSignedURLs` and `allowedOrigins` apply (independent of video-level settings); `hideLiveViewerCount` per input; `allowedOrigins` default `null` (any). | [Watch a live stream](https://developers.cloudflare.com/stream/stream-live/watch-live-stream/), [Start a live stream](https://developers.cloudflare.com/stream/stream-live/start-stream-live/) |
| Ingest: H.264 + AAC (ADTS), 2–14 Mb/s combined, GOP 2–10 s closed; GOP 4–8 s recommended for standard HLS; live recordings truncated after 7 days. | [Start a live stream](https://developers.cloudflare.com/stream/stream-live/start-stream-live/) |
| A newly connected input can take a moment to become playable ("Wait thirty seconds. There is a brief delay between when your encoder connects and the stream becomes playable"). No numeric startup latency is documented. | [Live troubleshooting](https://developers.cloudflare.com/stream/stream-live/troubleshooting/) |
| Delivery: $1 per 1,000 minutes, counted by HTTP requests for video **segments**; client-side preloading and buffering are billable; storage prepaid $5 per 1,000 minutes; live recordings consume storage; no viewers = $0 delivery; running out of storage blocks new live streams. | [Stream pricing](https://developers.cloudflare.com/stream/pricing/) |
| Live viewer count: `GET customer-<CODE>.cloudflarestream.com/<INPUT_ID>/views` → `liveViewers`. | [Live viewer count](https://developers.cloudflare.com/stream/getting-analytics/live-viewer-count/) (via Epic 32 table) |
| Turnstile: server-side siteverify is mandatory; tokens are valid 300 s and single-use; `remoteip` and `idempotency_key` optional. | [Turnstile server-side validation](https://developers.cloudflare.com/turnstile/get-started/server-side-validation/), [Client-side rendering](https://developers.cloudflare.com/turnstile/get-started/client-side-rendering/) |
| Cloudflare Access can protect a Worker or a specific Custom Domain; needs Zero Trust enabled on the account; every request is checked before the Worker runs. | [Workers: Cloudflare Access](https://developers.cloudflare.com/workers/configuration/cloudflare-access/), [Choose application type](https://developers.cloudflare.com/cloudflare-one/access-controls/applications/choose-application-type/) |
| Workers Rate Limiting binding (GA): `limit({key})`, period 10 or 60 s, counters per Cloudflare location, permissive and eventually consistent (not an accounting system). | [Rate Limiting binding](https://developers.cloudflare.com/workers/runtime-apis/bindings/rate-limit/), [GA changelog](https://developers.cloudflare.com/changelog/post/2025-09-19-ratelimit-workers-ga/) |
| Durable Objects: WebSocket server with the Hibernation API (`ctx.acceptWebSocket`, auto ping/pong without waking), up to 32,768 WebSockets per object; hibernation only when the DO is the server. | [Durable Objects WebSockets](https://developers.cloudflare.com/durable-objects/best-practices/websockets/), [DurableObjectState](https://developers.cloudflare.com/durable-objects/api/state/) |
| Queues pull consumers (HTTP pull from outside Workers, API token with queues read+write) — considered for the control channel, not chosen. | [Queues pull consumers](https://developers.cloudflare.com/queues/configuration/pull-consumers/) |
| KV is eventually consistent (writes visible elsewhere within up to 60 s or `cacheTtl`; min `cacheTtl` 30 s); Free plan 1,000 writes/day — why the snapshot goes to R2, not KV. | [KV write guidance](https://developers.cloudflare.com/kv/api/write-key-value-pairs/), [KV cacheTtl changelog](https://developers.cloudflare.com/changelog/post/2026-01-30-kv-reduced-minimum-cachettl/), [Workers pricing](https://developers.cloudflare.com/workers/platform/pricing/) |
| Workers Paid: $5/month minimum, no egress charges; R2: no egress charges, 1 M Class A and 10 M Class B ops free per month; Workers Free: 100,000 requests/day, 10 ms CPU. | [Workers pricing](https://developers.cloudflare.com/workers/platform/pricing/), [Workers limits](https://developers.cloudflare.com/workers/platform/limits/) |

**Not documented (to verify in the 34.3/34.4 spikes):** startup time of a freshly connected live
input; whether `allowedOrigins` blocks requests without an `Origin`/`Referer` (native players); token
`ip.src` behaviour across IPv4/IPv6; whether a player on input id `A` with a still-valid token keeps
playing while the next token names input `B` (the design reloads the source at a segment boundary).
