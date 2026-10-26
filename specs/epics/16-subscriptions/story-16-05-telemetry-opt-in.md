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

## Implementation

**Status: done (web client).** Built on branch `worktree-agent-a80d9c8f289bb66b9`.

- `web/src/lib/telemetry/`: `consent.ts` (the dated decision and the pseudonym, per device
  in localStorage), `queue.ts` (the queue), `sanitize.ts`, `client.ts` (`optIn`, `optOut`,
  `track`, `noteRoute`, `reportError`, `forgetDevice`).
- `web/src/components/TelemetryPrompt.tsx`: the first-launch "Help improve Maktaba" notice
  in the app shell (a notice above the page, not a modal), with the bullets of what is and
  is never shared; "Share usage data" / "No thanks"; asked once per device.
- `web/src/pages/Settings/TelemetrySettings.tsx` under Settings → Privacy: the switch (the
  kill switch: off stops at once and drops what waits) and "Forget my device"
  (`DELETE /api/telemetry/devices/{pseudonym}` for every pseudonym the device used).
- Strings in `en` and `ar`.
- Sent: `app.open` (version, OS, locale, library size as a range: 0, 1-99, 100-999,
  1k-10k, 10k+), `feature.used` (visits per app area, summed and sent every 5 minutes),
  `error.uncaught` (message and first 5 frames without home folders, the server's address
  or query strings; at most 20 a launch).
- Queue: at most 1,000 (the oldest go), 100 a request, kept in localStorage across
  launches; exponential backoff 2 s ... 60 s on a 5xx, 429 or network failure; a 4xx batch
  is dropped. Nothing awaits on the UI's path.
- The server's opt-out (`[telemetry] enabled = "off"`) and the sink are story 16.7.

**Differences from the plan:**

- The pseudonym is 16 base32 characters from 80 random bits (the plan's 12 bytes do not
  fit 16 base32 characters).
- An opt-out retires the pseudonym (the next opt-in makes a fresh one, as the plan asks),
  but keeps it aside so "Forget my device" can still delete what it sent; Forget deletes
  every pseudonym the device used. Still opted in, the device carries on under a new one.
- Consent is per device only; it is not synced through `PUT /api/me/preferences` (each
  device has its own pseudonym, and the self-hosted server has no such preference).
- Not built: the client's Web Vitals reporting (the sink accepts them), the native
  tvOS/Android TV/Capacitor-crash/Tauri-panic clients, the admin server-wide toggle in
  Settings → Advanced (the plan calls it a UX nicety; server.toml is the switch), and
  `docs/privacy.md` (no such document exists; `cmd/maktaba-server/README.md` documents
  `[telemetry]`).
