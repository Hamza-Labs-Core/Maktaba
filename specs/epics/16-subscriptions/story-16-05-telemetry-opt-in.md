# Story 16.5 — Usage analytics (opt-in)

Anonymous usage analytics, strictly opt-in, with a clear scope and a
visible kill switch. The on-server collection and sink are owned by
[Story 16.7](story-16-07-telemetry-api.md).

**Anchors:** [`architecture.md` §10.4](../../architecture.md). Depends
on [Story 16.7](story-16-07-telemetry-api.md).

## AC

- First-launch: opt-in dialog "Help improve Maktaba" with bullet list of
  what's collected; no telemetry until accepted.
- What's collected: app version, OS, anonymized library size, feature
  usage counts, error stack traces (no file paths or content).
- What's never collected: video filenames, transcript text, search
  queries, user identifiers, IP addresses (after sampling).
- Aggregated server-side; per-user data deletable via "Forget my
  device" button.
- Endpoint: `POST /api/telemetry` and `POST /api/telemetry/web-vitals`
  (owned by [Story 16.7](story-16-07-telemetry-api.md)).
- Self-host server-side opt-out: `[telemetry] enabled = false`.

## TC

- Opt in: the next session's events appear on the telemetry server
  within minutes.
- Toggle off: events stop firing within one app launch.
- "Forget my device": the device's pseudonymous ID and history are
  purged.

## EC

- Network drops while sending events: queued locally; capped at 1,000
  events; oldest dropped first.
- Telemetry endpoint returns 5xx: client retries with exponential
  backoff; never blocks UI.
- A locale that requires explicit consent (EU): treat the opt-in dialog
  as the consent record.
