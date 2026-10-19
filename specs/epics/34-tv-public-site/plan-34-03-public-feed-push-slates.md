# Plan 34.3 — Public feed: always-on push, slates, rotation — implementation

> Implementation plan for [story-34-03](story-34-03-public-feed-push-slates.md).
> Uses the Epic 34 migration (next free slot at implementation time: `tv_public_channels.input_a/b,
> active_input, rotated_at`; `tv_remote_inputs.purpose`, `tv_remote_pushes.feed`). Design: [design.md](design.md) §4, §5.

## 0. Decisions

| # | Decision | Rationale |
|---|---|---|
| D1 | All public channels always-on at full quality by default. | 1 Gbps uplink: 22 × 6.5 Mb/s ≈ 15 %; ingest free; every switch is warm. Capacity is the limit. |
| D2 | A second muxer (public mux) per public channel, slate blocks for unavailable programmes. | Per-programme exclusions without touching LAN/Plex output and without a second full encode. |
| D3 | Dedicated A/B public inputs, rotation every 2 h. | Stable playback ids (input), bounded storage (~3,000 min for 22 channels), no 7-day truncation, no break. |
| D4 | Reuse the Epic 32 push relay (`streaming/internal/frontdoor/relay`) and Cloudflare client (`api/internal/tvremote/cf`); a public push is a `tv_remote_pushes` row with `feed = public`. | One code path for pushes, already continuous across swaps and failovers; credentials through `tv_push_credentials`. |
| D5 | Rendition and top-N tiering are settings, off by default. | Only for smaller uplinks. |

## 1. Files

**Create**
- `streaming/internal/channel/publicmux.go` — second muxer + Hub; block routing (available → copy bytes;
  unavailable → slate encoder output); mid-block switch at GOP
- `streaming/internal/channel/slate.go` — slate block through `blockargs` (output settings + the channel's look:
  watermark, strip) from slate art/texts; pre-encoded
  fallback slate per output-settings hash (cache dir)
- `api/internal/tvpublic/inputs.go` — create/update/disable/delete A/B inputs (Epic 32 `tvremote/cf` client),
  `allowedOrigins` sync, rows in `tv_remote_inputs` (`purpose = public`), keys to `home_secrets` (`cf_input.<uid>`)
- `api/internal/tvpublic/feed.go` — reconcile loop: desired public pushes → placement requests (reason
  `public`, pinned) → `tv_remote_pushes` rows (`feed = public`) + `tv_push_secrets`; rotation scheduler (incl. moving simulcast outputs A → B); tiering (`top_n`, wake/idle from SiteControl)
- `api/internal/tvpublic/capacity.go`
- tests (fake ffmpeg, fake CF client)

**Modify**
- `streaming/internal/channel/station.go`, `engine.go` — start/stop the public mux when the channel is public;
  feed both muxers; `PublicHub()`, `StreamChannelPublicTS`; `handler.go` — `/stream/channel/{id}/public.ts`
- `streaming/internal/frontdoor/relaysource.go` — `PublicRelaySource()`; `relay/ctl.go` — read `feed` and pick
  the source per push
- Epic 32 placement (`streaming/internal/placement`) — reason `public` = pinned, not reaped; counts in capacity
- `api/internal/tvremote` — pool ignores `purpose = public`; tune of a public channel returns a token for the
  public active input when `remote_on_public_input` (R3.7); `max_push_hours` rotation skipped for `feed = public`
- `api/internal/tvremote/simulcast.go` — outputs follow the active public input; move at rotation

## 2. Steps

1. **Spike (real account, 1 day):** push to A, read lifecycle; measure cold start; switch a hls.js player
   from A to B with a fresh token; test `allowedOrigins` with a native player; record storage during rotation.
2. Public mux + slate with fake ffmpeg; compare block sequences of both Hubs.
3. Inputs lifecycle and key storage; origins sync hook for 34.8.
4. Feed reconcile: placement pinning, public push rows run by the relay on `publicHub`, report state to SiteControl (34.4).
5. Rotation scheduler (staggered), SiteControl `rotate{}` handshake, recording cleanup.
6. Capacity computation and warnings; admin page numbers.
7. Options: rendition args; `top_n` with wake/idle.

## 3. Tests

Story TCs; plus `TestPublicMuxNotStartedForPrivate`, `TestOriginsSyncOnDomainChange`.

## 4. Rollout

Phase B: one channel public on staging (with `staging_video`), then two on production; watch encoder load
on the Streaming hosts page before making all 22 public.
