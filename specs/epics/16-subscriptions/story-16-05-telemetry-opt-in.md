# Story 16.5 — Anonymous usage data (opt-in, two levels)

Anonymous usage data for Maktaba's makers, to improve Maktaba across homes: which
versions, systems, apps and features are used and which errors happen. It goes to
Maktaba Cloud, never to the home's admin, who already sees the home's use by name
(Epic 44's analytics). The transport, the home's pipe and the cloud's store are
[Story 16.7](story-16-07-telemetry-api.md).

**Anchors:** [`architecture.md` §10.4](../../architecture.md). Depends on
[Story 16.7](story-16-07-telemetry-api.md).

## Rework (2026-10-26)

The first version asked every person on every device to "share anonymous usage data with
this server's admin" and stored it on the home, with the library's size counted by 1 + N
library requests on each app open. The owner's verdict: useless for an admin, who has full
analytics already; the value is for Maktaba, across homes. Consent is "per admin for the
server, per app for players and remote and so on". So:

- the data goes to Maktaba Cloud, through the home;
- the server's data has its own consent (the admin's switch), each app's data its own
  (that app's user), and neither implies the other;
- the viewer prompt asks about the app only, and says where the data goes; a yes to the
  old wording is forgotten and asked again;
- the library's size is counted once a day on the server, from a count query; the apps
  make no library requests for it.

## AC

### The server's consent

- A switch in Settings → Privacy, for admins only: **Usage data: this server**. Off by
  default. Runtime setting `telemetry.share_server_data` (`PATCH /api/settings`, which
  refuses anyone but an admin; a role test holds it). No viewer is asked anything.
- While on, the home sends Maktaba Cloud one report a day: its version, OS, architecture
  and install kind (Mac app, Linux, Windows, Docker); ranges of its videos, libraries,
  users and machines; the apps in use over the last week (from `api_usage_daily`) with
  ranges of their people, requests and errors; its features (counts as ranges, flags);
  its errors by kind, with paths, file names and addresses scrubbed.
- The admin sees exactly what the day's report holds before agreeing ("See exactly what
  is sent"), when it last went, and can ask Maktaba to delete what the server sent (the
  home then takes a new id).

### Each app's consent

- Every app asks its own user, once, on that device: the web app (browser, Mac app's
  window, mobile shell), Maktaba Player and Remote on iPhone, iPad and Mac, Apple TV,
  Android Player and Remote on phone, foldable and tablet, and Android TV. A notice, never
  a modal; "Not now" is an answer (off). Off by default.
- That app's settings change the answer (Privacy) and offer "Delete what this app sent".
- Only that app's data follows the answer. The admin's switch does not answer for an app,
  and an app's yes does not turn the server's report on.
- Sent while on: the app's version, platform, kind of device and language; `app.open`;
  how often each part of the app is used (`feature.used`); player starts and errors
  (native apps); uncaught errors scrubbed (web); Web Vitals (web, when sent).

### Never sent

File names, titles, transcripts, searches, user names, IP addresses. Ids are random per
install (the home's for its report, each app install's for its batches) and linked to no
account, user or home.

## TC

- Server switch off (default): the home sends nothing of its own. On: one report a day.
- An app with consent: its batches reach Maktaba Cloud through the home, whatever the
  server switch says. Without: it sends nothing.
- Toggle off: an app stops at once and drops what waits; the next opt-in uses a new id.
- "Delete what this app/server sent": the waiting batches go and the cloud deletes its rows.

## EC

- A home without internet: batches wait up to a week, retried with backoff, then are
  dropped.
- An app's queue: at most 1,000 events, the oldest dropped first; a 5xx, 429, 401 or
  network failure retries with backoff; another 4xx drops the batch; never blocks the UI.
- A locale that requires explicit consent (EU): the dated decision is the consent record.
- `[telemetry] enabled = "off"` in server.toml: nothing leaves the home, the server's
  data nor any app's.

## Implementation

**Status: done (reworked 2026-10-26).**

- Home: `api/internal/handlers/telemetry` (report, error counts, outbox, sender, the pipe);
  the switch is `runtimecfg.ShareServerData`; slot 0161 replaced the old sink.
- Web: `web/src/lib/telemetry/` (consent per install in localStorage under `mkt:usage:*`,
  the queue, the client), `TelemetryPrompt.tsx` (the ask), `Settings/TelemetrySettings.tsx`
  (this app) and `Settings/ServerUsageData.tsx` (this server, admins). English and Arabic.
- Apple apps: `apps/apple/MaktabaKit` `UsageData*.swift` (Player, Remote, Apple TV).
- Android apps: `apps/android/core/.../usage/` (Player, Remote, Android TV).
- Help: Settings → Privacy and "Who gets the usage data?" (`web/src/help/content/*/settings.md`).
