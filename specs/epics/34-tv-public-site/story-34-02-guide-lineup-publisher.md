# Story 34.2 — Guide and lineup publisher (real time)

## Description

*As a visitor, I want the public guide to be exactly what is on the channels, updated the moment the
schedule changes, and every link to play on the site itself; as the owner, I want the home to send
only what the site needs, outward only.*

Design: [design.md](design.md) §3; README R2.

## Acceptance criteria

- **AC1** A full publish builds `site.json`, `lineup.json`, one blob per channel-day for
  `guide_days` days, logos and slate art, uploads only missing blobs (content-addressed) through
  `/_home/publish/*`, and commits a new snapshot; the site serves it right after the commit.
- **AC2** **Real-time deltas:** each of these produces a per-channel delta publish within seconds
  (debounce ≤ 2 s per channel): scheduler `write_channels`, horizon top-up, Epic 33 commit or hot
  swap, live-slot change, an edit to the programme on air, a public flag/exclusion/branding change,
  and now/next drift > 5 s reported by the station.
- **AC3** **A schedule change on the home is visible in an open public guide within 10 s (p95)**
  without a reload — including an edit to the programme on air now (title, end time, replacement).
- **AC4** Open pages receive `guide` and `state` events over `/api/v1/live` (LiveHub WebSocket)
  and fetch only the changed blob; with the WebSocket blocked, the 15 s `/api/v1/version` poll picks
  the change up within 25 s.
- **AC5** Now/next and progress bars are computed in the browser from the published schedule and
  the server clock offset; no request per tick; a viewer whose device clock is 5 min wrong still sees
  the right programme.
- **AC6** Every guide cell and channel tile links to `/<lang>/watch/<slug>` or
  `/<lang>/watch/<slug>?at=<start>`; `?at=` behaves as design §3.3 (live / countdown / aired). The
  Worker behind those URLs mints the token and the player plays Cloudflare HLS. A CI test fails if
  any built asset, published blob or rendered page contains a URL whose host is not the site, the
  Stream customer domain or Turnstile.
- **AC7** Published data validates against the snapshot JSON schema; the Worker rejects a commit
  with unknown fields, file paths, or values over the size caps (design §11).
- **AC8** Excluded programmes are published with `a:false` and the slate text only; posters only
  when `publish_artwork`.
- **AC9** Runs are queue-engine jobs `tv_public_publish` (mode `full|delta`), visible in the control
  centre with blobs sent, bytes and duration; a failure keeps the previous snapshot and retries with
  backoff (1 s → 60 s); after 3 failed full runs a warning is raised.
- **AC10** Commits are optimistic (`base` snapshot); a 409 makes the publisher send a full index. Old
  snapshots and unreferenced blobs are deleted after 24 h.

## Test cases

- **TC1** `TestPublisherFullSnapshot` (fake Worker), `TestDeltaOnlyTouchedDays`.
- **TC2** `TestTriggersCoalesced` (10 NOTIFYs in 1 s → one delta).
- **TC3** `TestDriftRetimesRestOfDay`.
- **TC4** Worker `commit.test.ts` (schema, 409 on stale base, LiveHub notified).
- **TC5** E2E `guide-live-update.spec.ts`: change a block on the home → the open page updates in < 10 s.
- **TC6** `no-home-links.test.ts` (asset and snapshot scan).
- **TC7** web `nowNext.test.ts` with clock offsets ±5 min and item boundaries.

## Edge cases

- **EC1** Home offline: the site keeps the last snapshot and shows "last updated"; channels become
  "off air" after 60 s without the control connection (34.4).
- **EC2** Day boundary in the channel's zone vs the viewer's zone: blobs are per channel day; the page
  merges adjacent days.
- **EC3** A burst (an Epic 33 commit of a whole week): one delta per channel, all within 10 s for
  22 channels × 7 days (≤ 154 blobs, ≤ 6 MB).
- **EC4** Staging and production both deployed: the publisher commits to both; a staging failure
  never blocks production.

## Dependencies

34.1; 34.4 (home endpoints, LiveHub); Epic 27 scheduler; Epic 33 commit events; queue engine.
