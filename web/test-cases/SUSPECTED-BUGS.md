# Suspected bugs found while writing the test cases

These came from reading the code. The tests written from the cases have since checked them:
most were real and are fixed, each with a test. The list below the status section is the
original one, kept for reference. Paths are under `web/src/` unless they start with `api/` or
`design-system/`.

## Status: still open after the tests

Each of these has an `it.fails` (asserts the right behaviour, fails today) or `it.todo` test
named with its case id.

**Shared code (`lib/`, design system, shared components)**

- A 401 in the middle of a session shows an error instead of going to `/login` (SHL-07,
  ACC-13, ADV-14, ASE-06, ASE-14, ATK-22, BIL-17, CST-08, USR-16, AUD-13, QDT-04, QIH-05).
  Fix: one 401 handler in `lib/api.ts` + `lib/auth.tsx`.
- A failed "sign out everywhere" is treated as success (SAC-05, `lib/auth.tsx`).
- Demoting or deleting yourself does not refresh your own access (USR-11, USR-12).
- No unsaved-changes prompt when leaving an editor (CHN-20, SRL-26, ACL-14). `useBlocker`
  needs a data router; the app uses `BrowserRouter`.
- `design-system` Tabs: switching tabs unmounts the panel and drops drafts (CHN-21); arrow
  keys are not mirrored in RTL (SRL-29, VID-25).
- `components/EnrichmentPanel.tsx`: write actions shown to non-admins (VID-08), the queue is
  not refetched after accept or dismiss (ENR-12), Enter in its search box does nothing (VID-23).
- `components/charts`: `BarList` turns a negative value into a negative width (ANL-22).
- Queue pages show write controls to non-admins; the server refuses them (QOV-15, QDT-12,
  QIH-12). Needs a UX decision.

**Live TV**

- `lib/channels.ts`: one bad guide header hides the whole guide (GDE-11); `category` is
  dropped (GDE-18); `video_id` is dropped, so "Watch from beginning" never shows (LPL-23).
- `pages/LiveTV/Player.tsx`: `/live/%20` plays channel 0 (LPL-05); any channel-list failure
  says "Channel not found" (LPL-06).
- `packages/tvplayer`: after a LAN failure switches to remote, nothing tunes (LPL-22); the
  number overlay is not announced and the mini guide does not take focus (LPL-31).

**Need a new translated string** (en + ar), then a one-line page change

LTC-06 (slots/releases failure), LTV-08 ("Change #0 is"), LTV-20 (remote stale hint),
CHN-15 (whole channel numbers), FIL-16 (no-match text), BIL-02 (empty entitlements), SSE-03
(stale sessions copy), SPR-21 and ASE-21 / ADV-21 (per-row button labels), SMD-06 ("None"
model), SHL-02 (`NotFound.tsx` is English only), REG-05 (password length hint), SED-17
("watched"), SRC-20 (saved), LIB-06 (scan feedback), LIB-08 (no access).

**Other**

- `ar.json`: "Revoke" and "Cancel" are both "إلغاء" (ATK-26); "Devices" and admin
  "Machines" are both "الأجهزة".
- Billing "Revoke license" has no confirm and is enabled on the free tier (BIL-10).
- Cloud status treats any service named `*cloud*` as the relay (CST-15).
- The desktop drop overlay never closes after a clean drop and is English only (SHL-22).
- A stored density only applies once the Appearance tab opens (SAP-12).
- Models flash "No models in this category." before loading (SMD-09).
- Item history: a live reload drops older pages already loaded (QIH-18).
- No resume from a saved position on series and the player: no API for it (SED-02, VPL-09).
- No confirm before "Accept all episode matches" (SED-18); no visible shortcut list (VPL-21).

## Look at first

- **Same `hosts: null` crash on `/admin/health`:** `pages/Admin/SystemHealth.tsx:152-154`
  calls `tv.hosts.filter` / `tv.on_air.filter` on the same `/api/tv/cluster` response.
  Sending `[]` from the server fixes both pages. A fix only in `LiveTV.tsx` does not.
- **CSV formula injection:** `pages/Admin/AuditLog.tsx:288-291` (`csvCell` lets cells
  that start with `=`, `+`, `-`, `@` through unchanged).
- **Unchecked link:** `pages/Settings.tsx:360` puts any `release_url` into an `href`, so a
  `javascript:` URL would be clickable.
- **"Load more" repeats page 1:** the `api/internal/handlers/videos/videos.go` list
  handler never reads `cursor`, so the library shows duplicate videos.
- **Request loop:** `pages/Admin/LiveTV/PreviewPanel.tsx:54-55`. A bad `expires_at` makes
  a NaN timeout, which calls `POST /preview` over and over.
- **No 401 handling app-wide:** when a session expires mid-use, nothing sends the user
  to `/login` (`lib/api.ts`, `lib/auth.tsx`).
- **Admin pages with no `AdminGate`:** `/admin/extensions` and `/admin/enrichment` don't
  have one. The queue pages, the Models tab and other write actions also show controls to
  non-admins that then 403.

## Admin users / ACL / extensions / audit

- Extensions.tsx:205 no AdminGate; non-admin sees forms that 403.
- Extensions.tsx:214 `extensions: null`/missing -> "Loading…" forever.
- Extensions.tsx:124,:178 `secrets_set`/`settings_schema` null -> crash.
- Extensions.tsx:36-39 toggling Enabled / Plex sign-in wipes unsaved edits.
- Extensions.tsx:136 cleared number field saves 0.
- Extensions hash deep links (#dizquetv) don't scroll (cards render after load).
- CloudflareRemote.tsx:33, :82-84 null `lines`/`errors`/`deleted_inputs` -> crash.
- LibraryACL.tsx:153 `matrix.users`/`libraries` null -> crash.
- LibraryACL.tsx:82 Retry shows "Nothing to map" instead of Loading.
- LibraryACL unsaved matrix edits lost on leave without prompt.
- Users.tsx:41 bad localStorage roster ("null", {}, [null]) -> crash.
- Users.tsx:211-217 create with no body -> "User created." then error, no row.
- Users.tsx self-demote/delete: auth not refreshed, no warning.
- Users.tsx:219-226 no-op edit sends PATCH {}.
- AuditLog.tsx:100-102 no debounce, stale responses can win.
- AuditLog.tsx:91 Load-more failure replaces table; retry restarts page 1.
- AuditLog.tsx:288-291 CSV formula injection (=,+,-,@ not neutralised). SECURITY.
- AuditLog.tsx:247 null ts -> 1970; empty -> blank.
- AuditLog.tsx:113-121 "To" filter drops selected minute; event filter not reset on Source change; Load more hidden when filters hide all rows.
- AuditLog.tsx:113 non-array `items` -> crash.
- All four: 401 mid-use doesn't redirect to /login.

## Shell / auth

- auth.tsx:127 RequireAuth drops query string from `from`.
- auth.tsx:121-126 "Loading…" hardcoded English.
- api.ts/auth.tsx: no 401 handling after start-up; expired session keeps stale user.
- auth.tsx:63-64,69-74 200 without `user` -> setUser(undefined) -> bounce to /login silently.
- Register.tsx:30-33 nav() during render.
- Register.tsx:48 problem without `type` -> TypeError, unhandled.
- Register.tsx:36 `valid` ignores 8-char min (ResetPassword checks it).
- Register.tsx:51-53, ResetPassword.tsx:39 + api.ts:168-173 raw HTML 502 body shown in alert.
- ResetPassword.tsx:39 any error w/o detail (incl. 500) says "link invalid/expired".
- ResetPassword.tsx:36 + Login.tsx:48-50 stale signed-in user skips reset banner.
- ForgotPassword.tsx:39-45 "sent" message not a live region; focus lost.
- DesktopBridge.tsx:78-99 + DropOverlay.tsx:26-38 overlay never closes after all-accepted drop.
- DropOverlay.tsx:20,28, NotFound.tsx:6-10 hardcoded English.
- NotificationsBell.tsx:106,119 missing `unread` -> "NaN" badge.
- AppShell.tsx:41-48 /account NavLink lacks `end` (active on sub-pages).
- AppShell.tsx:52-65 /admin/enrichment not in nav or More menu.
- AppShell.tsx:150-160 sign-out label-in-name mismatch; two nav landmarks both "Primary".
- AppShell.tsx:266-275 MoreMenu sign-out has no pending state (double POST).
- ResetPassword.tsx:20,27 whitespace token counts as present; `+` in token becomes space.

## Channels / filler / analytics / enrichment

- ChannelList.tsx:165, Channels.tsx:96 `name: null` -> crash.
- Channels.tsx:71 load failure -> [] -> "No channels yet".
- ChannelEditor.tsx:379 modal closes without unsaved-changes prompt.
- design-system Tabs.tsx:86 only active panel mounted; tab switch drops drafts.
- ChannelEditor.tsx:192-208 new channel + live save fails -> next Save re-POSTs (duplicate).
- FillerSection.tsx:91 vs ChannelEditor.tsx:165 max filler gap error not in `blocked`.
- FillerSection.tsx:76 pool `name: null` -> crash on search.
- ScheduleSection.tsx:51 non-array `mode_config.slots` -> crash.
- ChannelEditor.tsx:255 channel number accepts 2.5.
- PreviewSection.tsx:48 load failure shows "No programs scheduled".
- FillerPools.tsx:44 load failure -> empty state.
- FillerPools.tsx:100 `name: null` -> crash.
- FillerPools.tsx:130 no-match search shows "No filler pools / Create".
- Analytics.tsx:74,:134 `sessions: null` -> Loading forever; missing -> crash.
- Charts.tsx:125,:132 null/missing `heatmap` or null row -> crash.
- Analytics.tsx:170-178 null numbers render "null"/"NaN%".
- Analytics.tsx:56 Promise.all: one failing endpoint blanks all cards.
- Analytics.tsx:53 no stale-response guard on range switch.
- Analytics.tsx:67 raw e.message shown, not localised.
- analytics.ts:203 formatWatchTime hardcodes "h"/"m".
- Enrichment.tsx no AdminGate.
- Enrichment.tsx:50 j/k handler fires while typing in search box.
- Enrichment: queue not refetched after accept/dismiss.
- Enrichment.tsx:61 error state has no retry.
- Enrichment.tsx:101 missing confidence -> "NaN%"; not clamped.
- Enrichment.tsx:87-91 role=option inside li; focus doesn't follow j/k.

## Health / machines / logs / devices

- SystemHealth.tsx:152-154 `tv.hosts`/`tv.on_air` null from /api/tv/cluster -> crash (SAME BUG as Live TV).
- SystemHealth.tsx:169 `health.services` null/missing or HTML body -> crash.
- SystemHealth.tsx:268 missing `status` -> crash.
- Processes.tsx:112 `processes` null -> crash.
- Machines.tsx:372 HomeCard `home` null -> crash.
- Machines.tsx:1126,1160 `workers[].stages` null -> crash.
- Machines.tsx:511 storage server `addresses` null -> crash.
- CanonicalizePanel.tsx:109 `servers_added[].addresses` null -> crash.
- RelayPanel.tsx:117,134,148,167 null `extensions`/`allow`/`home_only`/`calls`/`settings` -> crash.
- machineSummary.ts:44-46 `nodes` null or node `name: null` -> "Couldn't load the machines." instead of empty.
- RelayDoorCard.tsx:28-34 null totals/calls -> "Status unknown".
- Logs.tsx:254 one null entry -> whole batch dropped, "Could not load logs.".
- clusterApi.ts:225 pairing code rejects Arabic-Indic digits.
- Devices.tsx:126 unknown platform shows raw i18n key.
- Devices.tsx:130-131 missing dates show "undefined".
- Devices.tsx:176 remove confirm no busy guard (double DELETE).
- Devices.tsx:333 locale can't be cleared (undefined dropped).
- Devices.tsx:60-68 retry flashes "No devices".
- Devices.tsx:240-245 token/bundle ID not trimmed.
- Machines.tsx StorageServers remove has no confirmation.
- Logs.tsx:223 aria-label="details" hardcoded English; Logs.tsx:261 object msg -> "[object Object]"; search not debounced.
- app.css:1132 pre grid-column has no effect inside <details>.
- nav.admin.devices "All devices" vs page note (own account only); Arabic h1 same on devices and machines.
- DS Tabs arrow keys not mirrored in RTL; DS Modal "Close" hardcoded English.
- Machines tab not in URL (no deep link, Back leaves page).

## Account / cloud

- ServerStatus.tsx:39 `health.services` null/missing or HTML body -> crash; 200 null -> Loading forever.
- ServerStatus.tsx:120 `svc.status` null -> crash.
- Billing.tsx:114 `ent.features` null/missing -> crash; 200 null -> Loading forever.
- APITokens.tsx:128 `tok.scopes` null -> crash.
- APITokens.tsx:292 uses navigator.clipboard directly (fails on plain-http LAN) instead of copyText()/CopyField.
- APITokens.tsx:219 create response without `token`: success toast, no reveal, secret lost.
- APITokens.tsx:114 missing `revoked_at` treated as revoked.
- APITokens.tsx:249 submit ignores `busy` (double create).
- APITokens.tsx:174, ConnectedDevices.tsx:120 confirm buttons no busy guard (double DELETE).
- ar.json: tokens.revoke == common.cancel == "إلغاء" (same label on both dialog buttons); sessions.revoke same.
- Account.tsx:137-138 empty confirm password counts as valid.
- Account.tsx:80-81 null display_name/email -> uncontrolled inputs.
- i18n.tsx formatDate(null) -> 1970; undefined -> "undefined".
- ConnectedDevices.tsx:85 unknown platform shows raw i18n key.
- ServerStatus.tsx:40 /cloud|relay|tunnel/ can match the wrong service (false "Connected").
- ServerStatus.tsx:58 401/403 shown as server "Down".
- Billing.tsx:181 "Revoke license" no confirm; enabled on free tier.
- Billing.tsx:142-162 `features: {}` -> header-only table; status column header says "Actions".
- Token reveal dialog closes on Escape (only chance to see token); per-row buttons lack distinct accessible names.

## Library / series / video / search

- Search.tsx:388 `youtube.items` null -> crash; :412 `match` null -> crash; :90 null snippet -> crash.
- SeriesDetail.tsx:152 season `episodes` null -> crash; :90 null entry in `gaps` -> crash.
- MediaContextCard.tsx:135 null entry in `cast` -> crash.
- LibraryBrowser.tsx:167 title+filename null -> localeCompare crash on sort.
- LibraryBrowser.tsx:261 "Try again" sets ?\_r= which buildQuery ignores -> no refetch.
- api videos.go list handler ignores `cursor` -> Load more appends page 1 again (duplicates).
- api videos.go:175 non-UUID library param ignored -> /library/garbage lists all videos.
- LibraryBrowser.tsx:109 unknown ?sort= sends sort=undefined -> 400.
- Search.tsx:182 ?q= read only on mount: Back/Forward and header search while on /search don't update results; submits use replace.
- Search.tsx:339 saved-search delete button named "Close"; save errors swallowed.
- VideoPlayer.tsx:211 always uses manifest_url (m3u8) even on Chrome/Firefox, contrary to header comment.
- VideoPlayer.tsx no <video> error listener, no expires_at handling, no message when session has no URL.
- VideoPlayer.tsx:56-79 state not reset on videoId change; leaving before POST resolves leaks session (no DELETE).
- VideoPlayer.tsx:166 shortcuts ignore focus/modifiers (Ctrl+F -> fullscreen, keys in select seek, Space on Back); fail on Caps Lock/Arabic layout.
- VideoPlayer.tsx no <track>, so `c` and subtitle size do nothing; nav(-1) on deep link; "←" arrow hardcoded in RTL.
- VideoDetail.tsx:113 every error shows "Video not found."
- MediaContextCard.tsx:170 ?tab=enrichment ignored by Tabs.
- VideoDetail.tsx:272 transcript Load more is plain text not a button; stats show "undefined"/"NaN%".
- DisplayTitle.tsx:72 saved untrimmed; VideoDetail hardcodes English strings.
- EnrichmentPanel, SeriesDetail.tsx:136, Search YouTube import show write actions to non-admins (server 403s).
- SeriesDetail.tsx:66-85 no request cancel: old series name over new episodes; gaps for missing seasons dropped.
- SeriesBrowser.tsx sort race; error no retry; year 0 prints "0".

## Queue / transcription

- api.ts:178-181 non-JSON 200 (proxy HTML) returned as string; queue/transcription pages throw on it.
- QueueOverview.tsx:57,141 /api/issues `groups: null` -> crash.
- IssueList.tsx:36,55 `groups`/`sample_subjects` null -> crash.
- LibrariesTable.tsx:50,136 `rows`/`r.strip` null -> crash; RetriggerDialog `libraries`, RunPanel.tsx:187, ExplainPanel.tsx:186 same.
- WorkersStrip.tsx:16,20 missing `workers` key passes `=== null` check -> crash.
- useQueueStream.ts:39-41,113 isSnapshot only checks `queues`; null stages/pools/flows -> FlowLanes.tsx:56-58 crash; SSE payload unchecked.
- HealthStrip.tsx:13 `points`, PulseBanner.tsx:51 `stuck`, InsightsPanel.tsx:69,74 `styles`/`genres`, ExplainPanel.tsx:203,215 `remote`/`videos` null -> crash.
- ActivityLog.tsx:36-41,62 null items -> Loading forever; :99 older() crash; Load-older failure silent.
- hostFilter.tsx:59 `jobs: null` -> Loading forever.
- RetriggerDialog.tsx:44, ItemHistory.tsx:147 `res.results` unguarded.
- QueueDetail.tsx:77, ItemHistory.tsx:94 any error shows "no queue called X"/"Nothing is known" with no retry.
- QueueDetail.tsx:70-82, ItemHistory.tsx:87-95 no stale-response guard.
- QueueDetail.tsx:95,153-154,341 `stages`/`failures`/`edges`/`fed_by`/`kpis` null -> crash; RunningJobs.tsx:28, NextUp.tsx:11, PoolStepper.tsx:22 same.
- QueueDetail.tsx:80 + RateChart.tsx:18 `points: null` -> crash.
- ItemParts.tsx:13,33,67,155,160 `kinds`/`appears_in`/`timeline`/`triggered`/`reasons` null -> crash; ItemHistory.tsx:167, LibraryCollections.tsx:27 same.
- ItemHistory.tsx:87-102 live reload drops loaded older pages.
- Raw i18n keys for unknown values: queue.outcome._, queue.enq._, queue.appears._ (ItemParts), transcription.stage._ (Transcription.tsx:394).
- Reports.tsx:34,96 `reports: null` -> Loading forever; missing -> crash.
- PlanPreviewPanel.tsx:101,103 `log`/`ready` null -> crash.
- Transcription.tsx:219,270,384 null `stages`/`libraries`/`failures` -> crash; :89,:182,:192 null `totals`/`rate`/`eta` -> crash.
- Transcription.tsx:255,275 library `name: null` -> crash on sort.
- useTranscriptionSummary.ts:38 `counts` null -> crash in SSE listener.
- Queue write actions shown to non-admins (403 after the fact); no /login redirect on 401.
- Reports picking uses replace:true, Back leaves the page (confirm intent, QRP-18).

## Settings

- Settings.tsx:432 Privacy: /api/me/history `{}` (no items) -> crash; `items: null` -> Loading forever; load error shows empty text.
- ModelsSection.tsx:69,81 /api/models null or object -> crash.
- RulesSection.tsx:39 section `fields` null/missing -> crash in useMemo; `sections: null` shows technical message.
- UpdatesSettings.tsx:95 `plan: null` -> crash takes down About tab.
- Settings.tsx:279,396-411 pollVersionUntilChanged reports "Updated" on rollback / failed first load; rolled_back never set; polling not cancelled on leave.
- Settings.tsx:374 after "Update failed." button stays disabled.
- Settings.tsx:326,331 raw key update.channel.<x>; "Invalid Date"; toLocaleString ignores app locale.
- Settings.tsx:429 privacy load failure assumes tracking on.
- auth.tsx:89-97 logout-all failure only console.warn.
- Settings.tsx:144-153,:104 stale "endpoint not yet available" copy for sessions/tokens.
- Settings.tsx:60-77 tab nav plain Link (no aria-current); no `*` route -> /settings/nope empty panel.
- app.css:399 settings nav no wrap, overflows at 375px.
- Settings.tsx:160 density applied only when Appearance tab mounts.
- ModelsSection.tsx:121,135-143 no placeholder when no STT model active.
- ModelsSection.tsx:111-117 OpenAI key only in localStorage, never sent, no feedback.
- ModelsSection: no loading state; errors shown as "Could not load models."; buttons not disabled; actions shown to all (server models.go:107 no admin check?).
- app.css:412 --color-danger undefined; hardcoded #c0392b in dark mode.
- RulesSection unsaved edits lost on nav; section tab not in URL.
- RuleEditors.tsx:435 empty map key silently dropped.
- Settings.tsx:493 history video ids not URL-encoded; NaN% / NaNm.
- Settings.tsx:360 release_url used as href unchecked (javascript: URL). SECURITY-ish.
- UpdatesSettings.tsx:104 raw key for unknown plan.waiting; :139 "(undefined)" time zone.

## Live TV

- StreamingHosts.tsx:55-57,85,89,106,250-251,269,388 `notices`/`hosts`/`settings`/`load`/`can_push`/`encoders`/`channels` unguarded (the reported blank page).
- OnAir.tsx:39 `on_air` null -> crash; :100-102 `viewers` null -> crash.
- RemotePanel.tsx:169 `st.inputs` unguarded; :159-160 null estimate `.toFixed`; :33 `channel_id` null; :97 `integration` null -> Loading forever.
- Simulcast.tsx:101,133-143,165,172,176,253,350,494,546,563 `usage`/`destinations`/`attachments`/`output`/`default_ingest`/`settings` unguarded.
- LiveTV.tsx:445 `d.devices` unguarded; :386,419 `st.plex` null -> crash.
- changeBits.tsx:54 `kinds` null -> crash (Changes rows, releases, detail header).
- ChangeDetail.tsx:120,277,525,537 `allowed_actions`/`approved_by`/`audit` unguarded.
- diff/LineupDiff.tsx:41-54,64-100, diff/ArtDiff.tsx:71, diff/ScheduleDiff.tsx:56,117-118 arrays unguarded.
- ApproveDialog.tsx:193 `bounds.channels`; PreviewPanel.tsx:126,132,293 `channels`/`d.after` unguarded.
- PreviewPanel.tsx:54-55 bad `expires_at` -> NaN timeout -> POST /preview in a tight loop.
- channels.ts:381-397 fromHeader drops `category` (guide category filter never filled).
- channels.ts:415-430 fromBlock drops `video_id` ("Watch from beginning" never shows).
- Player.tsx:128 any channel-list failure shows "Channel not found".
- Simulcast.tsx:369 destination form closes on failed save (input lost); :88-90 load error hides panel silently; :199 Edit does nothing for unknown channel.
- RemotePanel.tsx:55-58 keeps polling every 5s after 403/404; :137 stale hint says "streaming hosts".
- ChangeDetail.tsx:194 error after good load never shown; :108 404 keeps polling every 2s.
- Changes.tsx:90-91 slots/releases failures swallowed (skeleton forever).
- Guide.tsx:182 "Create a channel" shown to non-admins; :233 bad timestamps -> NaN width.
- LiveTV.tsx:228 "Change #0 is " banner on missing fields; loading <p> lacks role=status; Changes filter / detail tab not in URL.
