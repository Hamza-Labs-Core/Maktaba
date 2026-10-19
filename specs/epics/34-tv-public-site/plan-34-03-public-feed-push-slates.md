# Plan 34.3 — Public feed: always-on push, slates, rotation — implementation

> Implementation plan for [story-34-03](story-34-03-public-feed-push-slates.md).
> Uses slot 0122 (`tv_public_channels.input_a/b, active_input, rotated_at`). Design: [design.md](design.md) §4, §5.

## 0. Decisions

| # | Decision | Rationale |
|---|---|---|
| D1 | All public channels always-on at full quality by default. | 1 Gbps uplink: 22 × 6.5 Mb/s ≈ 15 %; ingest free; every switch is warm. Capacity is the limit. |
| D2 | A second muxer (public mux) per public channel, slate blocks for unavailable programmes. | Per-programme exclusions without touching LAN/Plex output and without a second full encode. |
| D3 | Dedicated A/B public inputs, rotation every 2 h. | Stable playback ids (input), bounded storage (~3,000 min for 22 channels), no 7-day truncation, no break. |
| D4 | Reuse the Epic 32 pusher and Cloudflare client. | One code path for pushes; credentials through `tv_push_credentials`. |
| D5 | Rendition and top-N tiering are settings, off by default. | Only for smaller uplinks. |

## 1. Files

**Create**
- `streaming/internal/channel/publicmux.go` — second muxer + Hub; block routing (available → copy bytes;
  unavailable → slate encoder output); mid-block switch at GOP
- `streaming/internal/channel/slate.go` — slate encoder args from `tv_output` + slate art/texts; pre-encoded
  fallback slate per output-settings hash (cache dir)
- `api/internal/tvpublic/inputs.go` — create/update/disable/delete A/B inputs (Epic 32 `tvremote/cf` client),
  `allowedOrigins` sync, keys to `home_secrets`
- `api/internal/tvpublic/feed.go` — reconcile loop: desired public pushes → placement requests (reason
  `public`, pinned) → push rows; rotation scheduler; tiering (`top_n`, wake/idle from SiteControl)
- `api/internal/tvpublic/capacity.go`
- tests (fake ffmpeg, fake CF client)

**Modify**
- `streaming/internal/channel/station.go` — start/stop the public mux when the channel is public; feed both
  muxers; expose `PublicHub()`
- `streaming/internal/channel/pusher.go` (Epic 32.7) — source Hub selectable (channel or public), optional
  rendition args
- Epic 32 placement (`api/internal/tvcluster`) — reason `public` = pinned, not reaped; counts in capacity
- Epic 32 `tvremote` tune — a public channel returns a token for the public active input (R3.7)
- Epic 32.13 simulcast — attach outputs to both inputs of a public pair

## 2. Steps

1. **Spike (real account, 1 day):** push to A, read lifecycle; measure cold start; switch a hls.js player
   from A to B with a fresh token; test `allowedOrigins` with a native player; record storage during rotation.
2. Public mux + slate with fake ffmpeg; compare block sequences of both Hubs.
3. Inputs lifecycle and key storage; origins sync hook for 34.8.
4. Feed reconcile: placement pinning, pusher on `publicHub`, report state to SiteControl (34.4).
5. Rotation scheduler (staggered), SiteControl `rotate{}` handshake, recording cleanup.
6. Capacity computation and warnings; admin page numbers.
7. Options: rendition args; `top_n` with wake/idle.

## 3. Tests

Story TCs; plus `TestPublicMuxNotStartedForPrivate`, `TestOriginsSyncOnDomainChange`.

## 4. Rollout

Phase B: one channel public on staging (with `staging_video`), then two on production; watch encoder load
on the Streaming hosts page before making all 22 public.
