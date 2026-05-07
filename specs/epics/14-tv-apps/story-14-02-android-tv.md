# Story 14.2 — Android TV app (Kotlin / Leanback)

A native Android TV app targeting Android TV 9+ (API 28).

**Anchors:** [`architecture.md` §6.5](../../architecture.md).

## AC

- Built with Compose for TV + Leanback row layouts.
- ExoPlayer for HLS / DASH adaptive playback; HDR10 / Dolby Vision
  where supported.
- Recommendations channel on the home screen with "Continue Watching"
  and "Recently Added" (sourced from
  [Story 14.7](story-14-07-recommendations-api.md) and
  [Story 14.5](story-14-05-continue-watching.md)).
- Apollo Kotlin GraphQL client.
- D-pad / remote / game-controller input.
- Server pairing via QR
  ([Story 15.5](../15-discovery/story-15-05-qr-pairing.md)).

## TC

- Cold launch on a Chromecast with Google TV: ≤ 6 s.
- D-pad navigation across a row of 50 items: smooth, no focus loss.
- Voice search via Google Assistant: dispatches to `/api/search/suggest`.

## EC

- Manufacturer skin (Sony, Sharp) with non-standard launcher: rec
  channel API has caveats; we document and gracefully degrade to
  in-app rows.
- HDR auto-engagement fails on a misconfigured TV: fall back to SDR
  with a one-time toast.
- Network drop mid-playback: ExoPlayer's default retry kicks in; we add
  a 5 s grace before showing a recoverable error.
