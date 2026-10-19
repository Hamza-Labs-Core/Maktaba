# Story 34.3 — Public feed: always-on push, slates, rotation

## Description

*As a visitor, I want every public channel to be running when I open it, and a clear "not available
online" card instead of a programme the owner may not publish; as the owner, I want the public feed
never to break on rotations, hot swaps or failovers, and to see what it costs my encoders.*

Design: [design.md](design.md) §4, §5; README R3.

## Acceptance criteria

- **AC1** For each public channel the home creates an A/B pair of live inputs with recording
  `automatic`, `requireSignedURLs`, `allowedOrigins` = the site hostnames, `timeoutSeconds` 30; keys go
  to `home_secrets`; input ids go to SiteControl and are never published.
- **AC2** With `tiering = all_always_on` (default) every public channel is placed (reason `public`,
  never reaped) and pushed, by stream copy of its public mux, 24/7 while it is public and
  `tv_public.enabled` is on.
- **AC3** The public mux equals the channel mux except during unavailable blocks, which carry a slate
  block of the same duration encoded with the channel's output settings; LAN, Plex and remote viewers
  are unaffected (checked by comparing the two Hubs' block sequences).
- **AC4** An exclusion added while the programme is on air switches the public feed to the slate at
  the next GOP; removing it switches back.
- **AC5** A/B rotation every `rotate_hours` (staggered across channels): B goes live, SiteControl moves
  tokens and sessions to B, A stops once its sessions left (≤ 5 min), A's recording is deleted. A
  site viewer sees at most one stall ≤ 2 s; storage never exceeds `n × (rotate_hours × 60 + 10)` minutes.
- **AC6** Epic 33 hot swap: no stall on the public feed. Epic 32 failover: the push resumes on the new
  host to the same input within 30 s; viewers continue with the same token.
- **AC7** Capacity: the admin page shows public encodes per host and the free slots; with `free < 0`
  a public channel shows `off_air(no_capacity)` on the site and a warning at home.
- **AC8** Options (off by default): `rendition = 720p|540p` adds one encode per public channel at
  `rendition_kbps`; `tiering = top_n` keeps `always_on_count` channels always-on and wakes the others
  through SiteControl (`wake` → placed and pushing → `on_air`), stopping them after
  `on_demand_idle_minutes`. The settings page shows the resulting uplink in Mb/s and % of `uplink_mbps`.
- **AC9** A channel turned private: its pushes stop within 10 s, both inputs are disabled, their videos
  deleted, and it leaves the next publish.
- **AC10** Simulcast outputs (32.13) are attached to both inputs of a pair; an Epic 32 remote tune of a
  public channel reuses the public input (one push), subject to the spike result for native apps.
- **AC11 (spike, real account)** Measure and record: cold start (push start → first playable segment),
  A→B switch stall, `allowedOrigins` with a native player, storage accounting during rotation. Results
  go into the runbook and the README facts table.

## Test cases

- **TC1** `TestPublicMuxSlateBlock` (fake ffmpeg), `TestPublicMuxMidBlockSwitch`.
- **TC2** `TestPublicPlacementPinned`, `TestCapacityWarning`.
- **TC3** `TestRotationAB` (fake Cloudflare client: lifecycle, videos, delete), `TestRotationFailureRetry`.
- **TC4** `TestFailoverSameInput`.
- **TC5** `TestTieringTopN` (wake, idle stop, ranking by minutes, pins first).
- **TC6** `TestPrivateStopsPush`.

## Edge cases

- **EC1** Cloudflare storage exhausted (`ERR_STORAGE_QUOTA_EXHAUSTED`): rotation stops creating new
  broadcasts, the site shows "off air", the admin is alerted (E32 §7.2).
- **EC2** A slate block shorter than one GOP: merged into the neighbouring block.
- **EC3** The slate encoder fails: the public feed plays a pre-encoded fallback slate for that
  output-settings hash — never the excluded programme.
- **EC4** A host carrying public channels dies and the other host is full: those channels show
  `off_air(no_capacity)` until capacity returns.

## Dependencies

34.1; Epic 32 (32.2 placement, 32.4 failover, 32.6 client, 32.7 pusher, 32.13 simulcast); Epic 27.10
filler path; Epic 33 hot swap.
