# Admin Live TV

- **Route:** `/admin/live-tv` (no params)
- **Component:** `src/pages/Admin/LiveTV.tsx` (`AdminLiveTV` → `LiveTVInner`, plus in-file `StagingBanner`, `ClusterSections`, `PlexPanel`, `Message`); children under `src/pages/Admin/LiveTV/`: `StreamingHosts.tsx` (`ClusterSettings`, `HostCard`), `OnAir.tsx`, `MoveDialog.tsx`, `ConfirmDialog.tsx`, `RemotePanel.tsx`, `Simulcast.tsx` (`SimulcastPanel`, `OutputRow`, `Destinations`, `ChannelEditor`, `RulesEditor`), `useTvCluster.ts`, `docs.ts`; `src/components/CopyField.tsx`, `src/components/AdminGate.tsx`. (`PreviewPanel`, `HeldReleaseCard`, `SlotBar`, `ApproveDialog`, `ChecksTable` are not on this page: they live on `/admin/live-tv/changes` and `/admin/live-tv/changes/:id`, see `admin-live-tv-changes.md` and `admin-live-tv-change-detail.md`. `src/lib/remoteTv.ts` is used by the live player, see `live-player.md`.)
- **API calls:**
  - `GET /api/tv/info` → `TvInfo` and `GET /api/tv/hdhr` → `HdhrDevice` (together, `Promise.all`), `PATCH /api/tv/hdhr` `{enabled?|friendly_name?, tuner_count?}` — `tvApi` in `src/lib/channels.ts`
  - `GET /api/tv/plex` → `PlexLiveStatus` (re-read every 3 s while `job.state` is pending/claimed/running/resuming, max 40 polls), `POST /api/tv/plex/refresh`, `POST /api/tv/plex/register` — `tvApi`
  - `GET /api/rules` → `{sections: RuleSection[]}`, `PATCH /api/rules/plexlive` — `rulesApi` in `src/lib/rulesApi.ts`
  - `GET /api/tv/cluster` → `TvClusterView {settings, hosts[], on_air[], notices[], failovers_24h?}` every 5 s while visible (`useTvCluster`, `CLUSTER_POLL_MS`); 403/404 hides the sections and stops polling. `PUT /api/tv/cluster/hosts/{id}`, `POST /api/tv/cluster/channels/{id}/move` `{host_id?}`, `DELETE /api/tv/cluster/channels/{id}/placement`, `PATCH /api/rules/tv_cluster` — `tvClusterApi` in `src/lib/tvClusterApi.ts`
  - `GET /api/tv/remote` → `{integration: RemoteIntegration, remote?: RemoteControllerStatus}` every 5 s while visible (`REMOTE_POLL_MS`); 403/404 hides the panel — `tvRemoteApi` in `src/lib/tvRemoteApi.ts`
  - `GET /api/tv/simulcast` → `SimulcastView`, `GET /api/channels` → `{items}`, `POST/PUT/DELETE /api/tv/simulcast/destinations[/{id}]`, `POST .../destinations/{id}/test`, `PUT /api/channels/{id}/simulcast` (409 `rights_confirmation_required`), `POST /api/tv/simulcast/outputs/{id}/start|stop` — `tvSimulcastApi` in `src/lib/tvSimulcastApi.ts`
- **Access:** admin only. `AdminGate` shows `common.forbidden.*` ("Admins only" / "You need administrator access to view this page.") when `user.is_admin` is false; signed out → `RequireAuth` → `/login`. Server also 403s; a 403 on cluster/remote hides those panels.
- **Existing tests:** `src/pages/Admin/LiveTV.test.tsx` — staging banner + in-flight link, addresses and Plex steps, copy (secure, plain-http fallback, blocked), Plex status with dizqueTV DVR, add-to-Plex confirm, add disabled while tuner off, refresh refusal, Plex not configured, plexlive rules save, tuner on, name + count save and count validation, cluster sections + 5 s visible-only poll, cluster 404 hides sections. `StreamingHosts.test.tsx` — host card facts, switch off, home cannot switch off, drain + move now, save capacity/weight/url, refusal, cluster settings via rules, two-homes banner, Arabic. `OnAir.test.tsx` — table rows incl. stale, move to a host, move automatic, stop after confirm, Arabic. `RemotePanel.test.tsx` — not set up, switched off, inputs/pushes/sessions/month/events, hidden CF viewers, problem reason, session paging, Arabic. `Simulcast.test.tsx` — destinations masked + usage + output state, stop/start, add destination with prefilled URL, rights confirm before turn-on, server 409 opens rights dialog, Arabic. **Gaps:** non-admin / signed out, load errors, every null/missing array (the `"hosts": null` blank-page bug), cluster error/stale view, remote/simulcast errors, destination/editor validation, Plex null fields, crash recovery, polling cleanup, responsive, theme, keyboard.

## Summary

| ID     | Title                                                              | Category      | Priority | Covered by existing test?                                          |
| ------ | ------------------------------------------------------------------ | ------------- | -------- | ------------------------------------------------------------------ |
| LTV-01 | Admin sees tuner, addresses and Plex steps                         | Happy path    | P1       | Yes (`LiveTV.test.tsx`)                                            |
| LTV-02 | Non-admin sees "Admins only" and no requests are made              | Permissions   | P1       | Yes (`LiveTV.cases.test.tsx`)                                      |
| LTV-03 | Signed-out user is sent to `/login`                                | Permissions   | P1       | Yes (`LiveTV.cases.test.tsx`)                                      |
| LTV-04 | Loading text until info and tuner arrive                           | Loading       | P3       | No                                                                 |
| LTV-05 | Info or tuner load fails                                           | Error         | P1       | Yes (`LiveTV.cases.test.tsx`)                                      |
| LTV-06 | Tuner on/off, and a failed PATCH                                   | Happy path    | P2       | Yes (`LiveTV.cases.test.tsx`)                                      |
| LTV-07 | Tuner name and count validation                                    | Validation    | P2       | Yes (`LiveTV.cases.test.tsx`)                                      |
| LTV-08 | Staging banner: in flight, none, staging missing                   | Happy path    | P2       | Yes (`LiveTV.cases.test.tsx`; missing number/state: it.fails: bug) |
| LTV-09 | Streaming hosts and On air render and poll every 5 s while visible | Happy path    | P1       | Yes (`LiveTV.test.tsx`)                                            |
| LTV-10 | Cluster 403 / 404 hides the sections and stops polling             | Permissions   | P2       | Yes (`LiveTV.cases.test.tsx`)                                      |
| LTV-11 | Cluster error on first load, then stale view after a good load     | Error         | P2       | Yes (`LiveTV.cases.test.tsx`)                                      |
| LTV-12 | `/api/tv/cluster` top-level arrays null or missing                 | Empty state   | P1       | Yes (`LiveTV.cases.test.tsx`)                                      |
| LTV-13 | `/api/tv/cluster` nested arrays and objects null or missing        | Empty state   | P1       | Yes (`LiveTV.cases.test.tsx`)                                      |
| LTV-14 | Host controls: switch, drain, move now, save                       | Happy path    | P1       | Yes (`StreamingHosts.test.tsx`)                                    |
| LTV-15 | Host and cluster settings validation                               | Validation    | P2       | Yes (`LiveTV.cases.test.tsx`)                                      |
| LTV-16 | On air: move and stop with confirm                                 | Happy path    | P1       | Yes (`OnAir.test.tsx`)                                             |
| LTV-17 | On air: no movable hosts, move / stop failures                     | Error         | P2       | Yes (`LiveTV.cases.test.tsx`)                                      |
| LTV-18 | Remote panel states and tables                                     | Happy path    | P2       | Yes (`RemotePanel.test.tsx`)                                       |
| LTV-19 | `/api/tv/remote` arrays and objects null or missing                | Empty state   | P1       | Yes (`LiveTV.cases.test.tsx`)                                      |
| LTV-20 | Remote panel errors: 403/404 hidden, 500 message, stale            | Error         | P2       | Yes (`LiveTV.cases.test.tsx`; stale-hint wording: it.fails: bug)   |
| LTV-21 | Simulcast: destinations, outputs, start/stop, rights               | Happy path    | P2       | Yes (`Simulcast.test.tsx`)                                         |
| LTV-22 | `/api/tv/simulcast` arrays and objects null or missing             | Empty state   | P1       | Yes (`LiveTV.cases.test.tsx`)                                      |
| LTV-23 | Simulcast load failure is not silent                               | Error         | P2       | Yes (`LiveTV.cases.test.tsx`)                                      |
| LTV-24 | Destination form validation and a failed save                      | Validation    | P2       | Yes (`LiveTV.cases.test.tsx`)                                      |
| LTV-25 | Delete destination: confirm, in use, 409                           | Happy path    | P2       | Yes (`LiveTV.cases.test.tsx`)                                      |
| LTV-26 | Channel editor: schedule rules, max outputs, unknown channel       | Validation    | P2       | Yes (`LiveTV.cases.test.tsx`)                                      |
| LTV-27 | Plex panel: status, add with confirm, refresh, automatic settings  | Happy path    | P2       | Yes (`LiveTV.test.tsx`)                                            |
| LTV-28 | `/api/tv/plex` and `/api/rules` null or missing fields             | Empty state   | P1       | Yes (`LiveTV.cases.test.tsx`)                                      |
| LTV-29 | Plex errors, busy-job polling and minutes validation               | Error         | P2       | Yes (`LiveTV.cases.test.tsx`)                                      |
| LTV-30 | Render crash shows the route error fallback; Back works            | Error         | P1       | Yes (`LiveTV.cases.test.tsx`)                                      |
| LTV-31 | Links: Changes, in-flight change, extensions, docs                 | Navigation    | P2       | Yes (`LiveTV.cases.test.tsx`)                                      |
| LTV-32 | Leaving the page stops every poll                                  | Navigation    | P2       | Yes (`LiveTV.cases.test.tsx`)                                      |
| LTV-33 | Phone width: wide tables scroll inside their box                   | Responsive    | P3       | No                                                                 |
| LTV-34 | Arabic: whole page RTL, URLs stay LTR                              | RTL           | P2       | Yes (`LiveTV.cases.test.tsx`)                                      |
| LTV-35 | Light and dark theme                                               | Theme         | P3       | No                                                                 |
| LTV-36 | Keyboard and screen reader basics                                  | Accessibility | P2       | Yes (`LiveTV.cases.test.tsx`)                                      |

## Cases

Shared data: `info`, `device` (`enabled:false`, `max_tuners:16`), `rules`, `plexEmpty`, `plexRegistered` from `LiveTV.test.tsx`; `clusterView()`, `host()`, `onAir()` from `src/pages/Admin/LiveTV/fixtures.ts`; remote payloads from `RemotePanel.test.tsx`; simulcast view from `Simulcast.test.tsx`. Signed in as `{username:"admin", is_admin:true}` unless stated. Unmocked GETs reject.

### LTV-01 — Admin sees tuner, addresses and Plex steps

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes — `LiveTV.test.tsx` "shows the Plex addresses and steps", "copies an address to the clipboard"
- **Preconditions / data:** `GET /api/tv/info` → `info`; `GET /api/tv/hdhr` → `device`; others as shared data.
- **Steps:**
  1. Open `/admin/live-tv`.
- **Expected:** Heading "Live TV access"; card "HDHomeRun tuner" with toggle, "Tuner name" = "Maktaba", "Tuners" = 4, device id `A1B2C3D4`. Addresses card with copy fields for HDHomeRun address, XMLTV and M3U URLs. Plex steps list mentions `192.168.1.20:8782`. With `base_from_config:false` the "guessed" hint shows.

### LTV-02 — Non-admin sees "Admins only" and no requests are made

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** Yes (`LiveTV.cases.test.tsx`).
- **Preconditions / data:** `useAuth` → `{user:{username:"u", is_admin:false}}`; also `is_admin` missing.
- **Steps:**
  1. Open `/admin/live-tv`; wait 6 s (fake timers).
- **Expected:** `ErrorState` "Admins only" / "You need administrator access to view this page." No call to any `/api/tv/*`, `/api/rules` or `/api/channels`; no polling.

### LTV-03 — Signed-out user is sent to `/login`

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** Yes (`LiveTV.cases.test.tsx`).
- **Preconditions / data:** full `App`; `useAuth` → `{user:null, loading:false}`.
- **Steps:**
  1. Navigate to `/admin/live-tv`.
- **Expected:** Redirect to `/login` with `state.from="/admin/live-tv"`; no API calls from the page.

### LTV-04 — Loading text until info and tuner arrive

- **Priority:** P3
- **Category:** Loading
- **Covered by existing test?** No
- **Preconditions / data:** `GET /api/tv/hdhr` never resolves; `info` resolves.
- **Steps:**
  1. Open `/admin/live-tv`.
- **Expected:** "Loading…" only (should be `role="status"`; currently a plain `<p>`). Cluster, remote, simulcast and Plex panels do not render yet.

### LTV-05 — Info or tuner load fails

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes (`LiveTV.cases.test.tsx`).
- **Preconditions / data:** run with `GET /api/tv/info` rejecting `ApiError` 500 "Internal Server Error"; `GET /api/tv/hdhr` rejecting 404; network `TypeError("Failed to fetch")`; 200 with malformed JSON; 401; 403.
- **Steps:**
  1. Open `/admin/live-tv`.
- **Expected:** Heading "Live TV access" and `role="alert"` "Could not load the Live TV settings: <message>". No blank page. Note: there is no retry button and no further panels, so one failing call hides cluster/remote/simulcast too (gap).

### LTV-06 — Tuner on/off, and a failed PATCH

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes (`LiveTV.cases.test.tsx`). Earlier: Partial — `LiveTV.test.tsx` "turns the tuner on".
- **Preconditions / data:** `PATCH /api/tv/hdhr` → `{...device, enabled:true}`; second run rejects `ApiError` 500 "boom".
- **Steps:**
  1. Click the toggle.
- **Expected:** Success: body `{enabled:true}`, label "Enabled", `role="status"` "✓ Tuner turned on." Failure: toggle stays off, `role="alert"` "✕ boom"; toggle re-enabled after.

### LTV-07 — Tuner name and count validation

- **Priority:** P2
- **Category:** Validation
- **Covered by existing test?** Yes (`LiveTV.cases.test.tsx`). Earlier: Partial — `LiveTV.test.tsx` "saves the name and tuner count, and validates the count".
- **Preconditions / data:** `device` as shared; second run `max_tuners: 0` and `max_tuners` missing.
- **Steps:**
  1. Clear "Tuner name".
  2. Type spaces only.
  3. Set "Tuners" to 0, 17, 1.5, empty.
  4. Restore valid values equal to the server's.
- **Expected:** Empty or blank name → "Give the tuner a name." Count errors → "Between 1 and 16." Save disabled for any error and when nothing changed. With `max_tuners` 0/missing the limit is 16. Save sends trimmed name.

### LTV-08 — Staging banner: in flight, none, staging missing

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes (`LiveTV.cases.test.tsx`; missing number/state: it.fails: bug). Earlier: Partial — `LiveTV.test.tsx` "shows the change in flight and the way to the Changes page (Epic 33)".
- **Preconditions / data:** `info.staging` = (a) `{enabled:true, change_in_flight:"ch-1", change_in_flight_number:7, change_in_flight_state:"verifying", live_release:null, live_gen:3}`; (b) `change_in_flight:null`; (c) `staging` missing; (d) `change_in_flight_state` missing, `change_in_flight_number` missing.
- **Steps:**
  1. Open `/admin/live-tv`.
- **Expected:** (a) `data-testid="staging-banner"` with link "Change #7 is Verifying" → `/admin/live-tv/changes/ch-1` and "Open Changes" → `/admin/live-tv/changes`. (b) "No change is in flight." (c) no banner. (d) "Change #0 is " — should not show a dangling state or "#0" (minor gap).

### LTV-09 — Streaming hosts and On air render and poll every 5 s while visible

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes — `LiveTV.test.tsx` "shows the streaming hosts and on air, and re-reads them every 5 s only while visible"
- **Preconditions / data:** `GET /api/tv/cluster` → `clusterView()`.
- **Steps:**
  1. Open the page; advance 5 s; hide the tab 15 s; show it.
- **Expected:** Heading "Streaming hosts", table "On air", help links row. One extra read per 5 s visible, none while hidden, one at once on show.

### LTV-10 — Cluster 403 / 404 hides the sections and stops polling

- **Priority:** P2
- **Category:** Permissions
- **Covered by existing test?** Yes (`LiveTV.cases.test.tsx`). Earlier: Partial — `LiveTV.test.tsx` "leaves the cluster sections out when the server has none" (404 only).
- **Preconditions / data:** `GET /api/tv/cluster` rejects `ApiError` 403; second run 404. Fake timers.
- **Steps:**
  1. Open the page; advance 20 s; toggle visibility.
- **Expected:** No "Streaming hosts" / "On air"; no error text. Exactly one cluster request in total (no more polls, none on visibility change). Tuner and Plex cards still render.

### LTV-11 — Cluster error on first load, then stale view after a good load

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (`LiveTV.cases.test.tsx`).
- **Preconditions / data:** fake timers. (a) first `GET /api/tv/cluster` rejects `ApiError` 500 "boom", second → `clusterView()`. (b) first → `clusterView()`, second rejects network `TypeError("Failed to fetch")`.
- **Steps:**
  1. Open the page; advance 5 s.
- **Expected:** (a) hint "Could not load the streaming hosts: boom", then sections appear and the message clears. (b) sections stay with the last view and the hint "Could not refresh the streaming hosts (Failed to fetch); showing the last view." shows above them; it clears on the next good read.

### LTV-12 — `/api/tv/cluster` top-level arrays null or missing

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** Yes (`LiveTV.cases.test.tsx`)
- **Preconditions / data:** `GET /api/tv/cluster` → `clusterView()` with, one run each:
  - `"hosts": null`, and `hosts` key deleted (read by `StreamingHosts.tsx:57,85,89` and `MoveDialog` via `movableHosts(view.hosts)`)
  - `"on_air": null`, and `on_air` deleted (`OnAir.tsx:39` spreads it)
  - `"notices": null`, and `notices` deleted (`StreamingHosts.tsx:55-56`)
  - `"settings": null`, and `settings` deleted (`ClusterSettings` reads `s.spill_at`, `s.nodes_stream`, `s.policy`)
  - all four null at once: `{ "settings": null, "hosts": null, "on_air": null, "notices": null }`
  - `{}` and a JSON `null` body
- **Steps:**
  1. Open `/admin/live-tv`; wait for the cluster read.
  2. Where On air has rows, click "Move" on one.
- **Expected:** The page never goes blank and nothing throws. `hosts` null/missing → "No machine streams TV yet." and the Move dialog lists only "Automatic" with "No machine is online and taking channels; Automatic picks the best one when there is." `on_air` null/missing → "No channel is on air." `notices` null/missing → no banners. `settings` null/missing → the settings block is hidden or shows defaults, not a crash. The tuner, Remote, Simulcast and Plex cards still render. (Currently unhandled — crashes the whole page; fix in progress on `fix/livetv-null-hosts`.)

### LTV-13 — `/api/tv/cluster` nested arrays and objects null or missing

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** Yes (`LiveTV.cases.test.tsx`).
- **Preconditions / data:** `clusterView()` with one host / on-air row changed per run:
  - `host.encoders: null` and missing (`StreamingHosts.tsx:269` `.length`)
  - `host.channels: null` and missing, on a draining host (`StreamingHosts.tsx:388`, read when "Move now" confirm opens)
  - `host.load: null` (`:250` `host.load.cpu`)
  - `host.can_push: null` (`:251`)
  - `notices[0] = {kind:"two_homes", message:"x", hosts:null}` (guarded, falls back to home names)
  - `on_air[0].viewers: null` (`OnAir.tsx:100-102`)
  - `on_air[0].push: null` (guarded) and `since: null` (guarded)
  - `host.state: "weird"` and `on_air[0].state: "weird"` (unknown enum)
- **Steps:**
  1. Open the page; on the draining host click "Move now".
- **Expected:** No crash. Encoders "—"; CPU "—"; can push "No"; viewers cells "0" or "—"; Move now body says "0" channels; unknown states show a neutral badge (key fallback text, no throw). Currently `encoders`, `channels`, `load`, `can_push`, `viewers` null each throw (currently unhandled — likely bug).

### LTV-14 — Host controls: switch, drain, move now, save

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes — `StreamingHosts.test.tsx` "switches a machine off with PUT {enabled}", "cannot switch the home off", "drains with PUT {drain:true} and offers Move now only while draining", "saves the capacity, weight and stream address of a machine", "shows the refusal when the server says no"
- **Preconditions / data:** `clusterView()`; `PUT /api/tv/cluster/hosts/{id}` resolves.
- **Steps:**
  1. Switch a node off; drain it; confirm "Move now"; change capacity/weight/URL and Save.
- **Expected:** Bodies `{enabled:false}`, `{drain:true}`, `{move_now:true}`, `{capacity_override, weight, stream_base_url}`; success `role="status"`; refusal `role="alert"` with the server text; the view reloads after each.

### LTV-15 — Host and cluster settings validation

- **Priority:** P2
- **Category:** Validation
- **Covered by existing test?** Yes (`LiveTV.cases.test.tsx`). Earlier: Partial — `StreamingHosts.test.tsx` saves valid values; error messages not checked.
- **Preconditions / data:** `clusterView()`.
- **Steps:**
  1. Capacity: 65, -1, 1.5, then empty.
  2. Weight: 0, 101, empty.
  3. Stream address on a node: `ftp://x`, `not a url`, then empty.
  4. Cluster "spill at": 0, 101, 50.5; then the current value.
- **Expected:** "A whole number from 0 to 64, or empty for automatic." (empty capacity is valid = automatic, sends `capacity_override:null`); "Between 1 and 100." for weight and spill; "An http:// or https:// address." for the URL (empty sends `stream_base_url:null`). Save / save-spill disabled while invalid or unchanged. Home host has no address field.

### LTV-16 — On air: move and stop with confirm

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes — `OnAir.test.tsx` "moves a channel to a host picked from the online, not draining ones", "moves automatically when no host is picked", "stops a channel only after a confirm, then deletes its placement"
- **Preconditions / data:** `clusterView()`.
- **Steps:**
  1. Click "Move Seerah" (`aria-label`), pick a host, confirm.
  2. Click "Stop Seerah", confirm.
- **Expected:** `POST /api/tv/cluster/channels/c1/move` with `{host_id}` (or `{}` for Automatic); `DELETE .../placement`; success messages; reload. Draining/down hosts are not offered; the current host is labelled as current.

### LTV-17 — On air: no movable hosts, move / stop failures

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (`LiveTV.cases.test.tsx`).
- **Preconditions / data:** (a) all hosts `state:"down"` or draining; (b) move rejects `ApiError` 409 `{error:"no_room"}` text "no room"; (c) stop rejects 500 "boom".
- **Steps:**
  1. Open Move dialog; confirm. 2. Stop and confirm.
- **Expected:** (a) only "Automatic" plus the "No machine is online…" hint. (b) dialog stays open with `role="alert"` "no room"; confirm button re-enabled. (c) dialog closes, card shows `role="alert"` "boom", buttons re-enabled. Cancel closes without a request; Escape closes.

### LTV-18 — Remote panel states and tables

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes — `RemotePanel.test.tsx` (not set up, off, full tables, CF viewers hidden, problem reason, paging, Arabic)
- **Preconditions / data:** payloads from `RemotePanel.test.tsx`.
- **Steps:**
  1. Open the page.
- **Expected:** `data-testid="remote-panel"` "Remote viewing"; `remote-status` text per state ("Cloudflare Stream is not set up." with a link to `/admin/extensions#cloudflare_stream`); inputs, pushes, sessions tables; month line; events in `<details>`; "More" after 50 rows.

### LTV-19 — `/api/tv/remote` arrays and objects null or missing

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** Yes (`LiveTV.cases.test.tsx`).
- **Preconditions / data:** `GET /api/tv/remote` → `{integration:{configured:true, enabled:true, ...}, remote:{ready:true, ...}}` with one change per run:
  - `remote.inputs: null` and missing (`RemotePanel.tsx:169` reads `st.inputs.length` unguarded)
  - `remote.pushes: null` / missing; `remote.session_list: null` / missing; `remote.events: null` / missing (guarded with `?? []`)
  - `remote` missing (st = null)
  - `integration: null` and missing
  - `remote.estimate: null`; `estimate.total_usd: null`, `estimate.budget_usd` missing (`:159-160` `.toFixed`)
  - a push `{id:"p1", state:"live", channel_id:null, channel_number:0, viewers:1}` (`channelLabel` calls `channel_id.slice`)
  - `remote.limits` missing, `remote.storage` missing
- **Steps:**
  1. Open the page.
- **Expected:** No crash, other panels intact. Inputs null/missing → no inputs table. Pushes/sessions/events null → "No channel is being sent to Cloudflare.", "Nobody is watching away from home.", "No remote events since the server started." `integration` null/missing → a clear message, not "Loading…" forever (today it stays on "Loading…" — likely bug). Estimate numbers null → no month line or "—", not a throw. Push without `channel_id` → "—". Today `inputs`, `estimate.*_usd` and push `channel_id` null throw (currently unhandled — likely bug).

### LTV-20 — Remote panel errors: 403/404 hidden, 500 message, stale

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (`LiveTV.cases.test.tsx`; stale-hint wording: it.fails: bug).
- **Preconditions / data:** fake timers. (a) rejects 403, then 404; (b) rejects 500 "boom"; (c) good, then `TypeError("Failed to fetch")`.
- **Steps:**
  1. Open the page; advance 5 s.
- **Expected:** (a) panel absent; note polling keeps running every 5 s after a 403/404 (unlike the cluster hook — likely wasteful, consider stopping). (b) card "Remote viewing" with "Could not read remote viewing: boom". (c) tables stay, hint "Could not refresh the streaming hosts (Failed to fetch); showing the last view." (reuses the cluster string — wording bug: should mention remote viewing).

### LTV-21 — Simulcast: destinations, outputs, start/stop, rights

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes — `Simulcast.test.tsx` all six cases
- **Preconditions / data:** view from `Simulcast.test.tsx`.
- **Steps:**
  1. Open the page; stop and start an output; add a destination; turn an attachment on.
- **Expected:** Masked keys, usage line, state badges; `POST .../outputs/{id}/stop|start`; Add form prefilled with the YouTube ingest URL; the rights dialog "Confirm your rights" before turn-on and on the server's 409 `rights_confirmation_required`.

### LTV-22 — `/api/tv/simulcast` arrays and objects null or missing

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** Yes (`LiveTV.cases.test.tsx`).
- **Preconditions / data:** the simulcast view with one change per run:
  - `destinations: null` / missing (`Simulcast.tsx:165,172,375,389`, editor `:548`)
  - `attachments: null` / missing (`:176,192`, editor `:546`)
  - `usage: null` / missing (`:101,133-143` incl. `u.cost_usd.toFixed`)
  - `settings: null` (editor `:563` `max_outputs_per_channel`)
  - `default_ingest: null` / missing (read on "Add destination" click `:350` and on kind change `:494`)
  - an attachment with `output: null` (`:253-256` `o.state`)
  - an attachment with `rules: null` and a window rule with `days: null` (guarded)
  - `GET /api/channels` → `{items:null}` (guarded by `.catch`)
- **Steps:**
  1. Open the page; click "Add destination"; change the kind; open a channel editor.
- **Expected:** No crash; tuner/cluster/Plex still render. Destinations null → "No destinations yet." and the channel "Edit" button disabled with the need-destination hint. Attachments null → "No channel is attached to a destination." Usage null → no usage line (or zeros). `default_ingest` null → empty URL field. `output` null → state "—"/off badge. Today `destinations`, `attachments`, `usage`, `settings`, `default_ingest` and `output` null each throw (currently unhandled — likely bug).

### LTV-23 — Simulcast load failure is not silent

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (`LiveTV.cases.test.tsx`).
- **Preconditions / data:** `GET /api/tv/simulcast` rejects 500 "boom"; then 404.
- **Steps:**
  1. Open the page.
- **Expected:** 404 → panel hidden (feature not on this server). 500/network → a short "could not load" message in a Simulcast card. Today every failure hides the panel with no message (currently unhandled — likely bug).

### LTV-24 — Destination form validation and a failed save

- **Priority:** P2
- **Category:** Validation
- **Covered by existing test?** Yes (`LiveTV.cases.test.tsx`). Earlier: Partial — `Simulcast.test.tsx` "adds a destination with the platform's URL prefilled".
- **Preconditions / data:** view with no destinations; `POST /api/tv/simulcast/destinations` rejects `ApiError` 400 `{code:"invalid_ingest_url", detail:"host missing"}` on one run.
- **Steps:**
  1. Click "Add destination"; leave name empty; set URL `http://x`, then `rtmp://`, then `rtmps://live.example/app`; leave key empty.
  2. Fill all and Save (failing run).
  3. Edit an existing destination with an empty key.
- **Expected:** URL error "Use an rtmp:// or rtmps:// address with a host." for `http://x` and `rtmp://`. Save disabled while name blank, URL bad, or (new only) key empty. Kind select disabled when editing; an empty key keeps the stored key. Failed save: the error shows as `role="alert"` — but the modal closes and the typed values are lost because `save()` calls `setForm(null)` after `act()` even on failure (`Simulcast.tsx:369`; currently unhandled — likely bug). The key input is `type="password"`, `autoComplete="off"`.

### LTV-25 — Delete destination: confirm, in use, 409

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes (`LiveTV.cases.test.tsx`).
- **Preconditions / data:** two destinations, one `in_use:true`; `DELETE /api/tv/simulcast/destinations/{id}` resolves, and on another run rejects 409 `{code:"destination_in_use"}`.
- **Steps:**
  1. Click Delete on the free one; answer Cancel in `window.confirm`; then OK.
  2. Check the in-use row.
- **Expected:** Cancel → no request. OK → DELETE, "deleted" status message, reload. In-use Delete is disabled. 409 → alert "A channel is simulcasting to this destination: stop or detach it first.". (Uses native `window.confirm`, not the app's dialog — not styled, not translated in RTL layout; consider `ConfirmDialog`.)

### LTV-26 — Channel editor: schedule rules, max outputs, unknown channel

- **Priority:** P2
- **Category:** Validation
- **Covered by existing test?** Yes (`LiveTV.cases.test.tsx`). Earlier: Partial — `Simulcast.test.tsx` rights cases only.
- **Preconditions / data:** 3 destinations, `settings.max_outputs_per_channel: 2`; channels list with `c1`; an attachment for channel `c9` not in the list.
- **Steps:**
  1. Pick channel 1 → "Edit"; attach one destination with mode "On a schedule"; remove all rules.
  2. Add a rule, tick days, set times.
  3. Attach all three destinations.
  4. Click "Edit" on the `c9` output row.
- **Expected:** No rules → "A schedule needs at least one rule." and Save disabled. With a rule, Save sends `{destination_id, mode:"schedule", rules:[{kind:"window", days:[...sorted], start, end}], enabled}`. Three attached → alert "Too many destinations for one channel." and Save disabled. Row "Edit" for `c9` does nothing at all (no dialog, no message) — currently unhandled, likely bug.

### LTV-27 — Plex panel: status, add with confirm, refresh, automatic settings

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes — `LiveTV.test.tsx` "shows the Plex status with the dizqueTV DVR left alone", "adds Maktaba to Plex only after a confirm that says what changes", "cannot add Maktaba to Plex while the tuner is off", "refreshes Plex now and shows a refusal", "asks to sign in with Plex when the extension is not set up", "saves the automatic Plex settings as rules"
- **Preconditions / data:** `plexEmpty`, `plexRegistered`, `rules`.
- **Steps:**
  1. Open the page; "Add Maktaba to Plex" → confirm "Add to Plex"; "Refresh Plex now"; toggle an automatic setting.
- **Expected:** As in the existing tests: facts list, dizqueTV kept hint, confirm dialog lists the tuner URL and XMLTV URL, `POST /api/tv/plex/register`, "Queued. The status updates when Plex has answered.", `PATCH /api/rules/plexlive`.

### LTV-28 — `/api/tv/plex` and `/api/rules` null or missing fields

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** Yes (`LiveTV.cases.test.tsx`).
- **Preconditions / data:** `GET /api/tv/plex` → `plexEmpty` with one change per run:
  - `status.dvrs: null` / missing (guarded `?? []`)
  - `status.dvrs[0].devices: null` / missing (`LiveTV.tsx:445` `.map` unguarded)
  - `status.warnings: null` / missing (guarded)
  - `status: null` / missing (guarded `?? {}`)
  - `status.channels` missing; `status.maktaba` missing; `status.target` missing (guarded)
  - `plex: null` / missing (`:386` `st?.plex.enabled`, `:419` `st.plex.url`)
  - `job: null` (guarded)
  - `GET /api/rules` → `{sections:null}`, `{}`, and a `plexlive` section with `fields: null`
- **Steps:**
  1. Open the page.
- **Expected:** Page and other panels render. DVRs null → "None" (or "—" before the first check). A DVR without devices → "dizqueTV: —". Warnings null → none shown. `plex` null → treated as not configured ("Turn on the Plex extension and sign in with Plex first." with link "Open Extensions" → `/admin/extensions`), not a crash. Rules null → the automatic settings block is hidden; no crash. Today `devices` null and `plex` null throw (currently unhandled — likely bug).

### LTV-29 — Plex errors, busy-job polling and minutes validation

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (`LiveTV.cases.test.tsx`). Earlier: Partial — `LiveTV.test.tsx` "refreshes Plex now and shows a refusal", "saves the automatic Plex settings as rules".
- **Preconditions / data:** fake timers. (a) `GET /api/tv/plex` rejects 500 "boom". (b) `job:{state:"running", ...}` on every read. (c) `job:{state:"paused", paused_reason:"plex down"}`. (d) register rejects 409 "already registered". (e) `PATCH /api/rules/plexlive` rejects 400.
- **Steps:**
  1. Open the page; advance 3 s × 45.
  2. Set "Guide reload at most every" to 0, -5, 1.5, then back to 60.
- **Expected:** (a) `role="alert"` "Could not load the Plex status: boom"; actions still visible but disabled. (b) "Talking to Plex…" row; re-reads every 3 s and stops after 40 polls. (c) row "Waiting: plex down". (d) confirm closes, alert "already registered". (e) alert with the rules error, toggles re-enabled. Minutes errors → "At least 1 minute."; save disabled while invalid or equal to the stored value.

### LTV-30 — Render crash shows the route error fallback; Back works

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes (`LiveTV.cases.test.tsx`).
- **Preconditions / data:** full `App`, history `["/admin/users", "/admin/live-tv"]`; `GET /api/tv/cluster` → `{...clusterView(), hosts: null}` (crashes until the fix lands), or mock `StreamingHosts` to throw.
- **Steps:**
  1. Render at `/admin/live-tv`.
  2. Press browser Back. Repeat and click the "Users" admin nav link instead.
- **Expected:** A route-level error fallback (boundary in `src/components/AppShell.tsx`) with the app nav still visible — never a blank page. Back renders `/admin/users` without a refresh; the nav link renders its page. Polls of the crashed page stop (no more `/api/tv/cluster`, `/api/tv/remote` requests).

### LTV-31 — Links: Changes, in-flight change, extensions, docs

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** Yes (`LiveTV.cases.test.tsx`). Earlier: Partial — `LiveTV.test.tsx` checks the in-flight link.
- **Preconditions / data:** staging in flight; Plex not configured; remote not set up.
- **Steps:**
  1. Click "Open Changes"; Back. 2. Click the in-flight link; Back. 3. Click "Open Extensions" and the remote "set up" link.
- **Expected:** Routes `/admin/live-tv/changes`, `/admin/live-tv/changes/ch-1`, `/admin/extensions`, `/admin/extensions#cloudflare_stream`. Back returns to Live TV with fresh data. Doc links (`docLink(...)`) open in a new tab with `target="_blank" rel="noreferrer"`.

### LTV-32 — Leaving the page stops every poll

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** Yes (`LiveTV.cases.test.tsx`).
- **Preconditions / data:** fake timers; cluster, remote, plex (busy job) mocked.
- **Steps:**
  1. Open the page; navigate to `/admin/users`; advance 30 s.
- **Expected:** No further `/api/tv/cluster`, `/api/tv/remote` or `/api/tv/plex` calls; no "state update on unmounted component" warnings; no `visibilitychange` listeners left (dispatching it triggers nothing).

### LTV-33 — Phone width: wide tables scroll inside their box

- **Priority:** P3
- **Category:** Responsive
- **Covered by existing test?** No (browser e2e)
- **Preconditions / data:** viewport 375 × 812; `clusterView()` with 2 on-air rows; remote with pushes; simulcast with outputs.
- **Steps:**
  1. Open the page and scroll down.
- **Expected:** `document.documentElement.scrollWidth <= 375`. The 13-column On air table scrolls inside `.mkt-tvc-table-wrap`; remote and simulcast tables do not push the page wider; host cards stack; copy buttons and dialogs fit; admin nav reachable.

### LTV-34 — Arabic: whole page RTL, URLs stay LTR

- **Priority:** P2
- **Category:** RTL
- **Covered by existing test?** Yes (`LiveTV.cases.test.tsx`). Earlier: Partial — section tests "renders in Arabic" / "reads in Arabic" render each section alone.
- **Preconditions / data:** `localStorage["mkt:locale"]="ar"`; full happy data.
- **Steps:**
  1. Open `/admin/live-tv`.
- **Expected:** `<html dir="rtl">`; all headings and hints from `ar.json` (no English `livetv.*` / `tv.cluster.*` keys leaking). Addresses, ingest URLs and stream addresses keep LTR (`<bdi>`, `dir="ltr"`). Numbers use `ar-EG` formatting where `n()` is used. Dialogs mirror.

### LTV-35 — Light and dark theme

- **Priority:** P3
- **Category:** Theme
- **Covered by existing test?** No (visual)
- **Preconditions / data:** `data-theme="light"` / `"dark"`.
- **Steps:**
  1. Open the page with the two-homes banner, stale badges, error messages and a failed push row.
- **Expected:** Banners, badges (`success`/`warn`/`error`), `mkt-q-status--ok/--bad` text and table rows are readable in both themes; no hardcoded colours in `tvcluster.css` / `simulcast.css`.

### LTV-36 — Keyboard and screen reader basics

- **Priority:** P2
- **Category:** Accessibility
- **Covered by existing test?** Yes (`LiveTV.cases.test.tsx`).
- **Preconditions / data:** happy data.
- **Steps:**
  1. Tab through the page; open the Move, Stop, Move now, Add to Plex, destination and channel editor dialogs; press Escape in each.
- **Expected:** Every input has a label (tuner name, tuners, capacity, weight, address, spill, policy, guide minutes, destination fields, rule fields); toggles are labelled. Each host card is an `article` named by the host. Row buttons have names like "Move Seerah" / "Stop Seerah". Dialogs are `role="dialog" aria-modal="true"`, trap focus, close on Escape, and return focus to the opener. Success messages use `role="status"`, failures `role="alert"`. Focus is visible on every control.
