# Story 35.13 — Updates page, notifications and history

## Description

*As the admin, I want one page that shows which version every machine runs, what will be installed
tonight, what happened last time and why, and to be told when something needs me.*

Updates are automatic, so the page is mostly for reading: the cluster's versions, the next planned
update, a live view of a run, the history with logs. The few actions — Update now, Skip, Defer, Pin,
Retry/Skip a machine, Resume — are here too. Notifications go to the Epic 33.13 centre; every action
and result goes to the audit log.

Design: [design.md](design.md) §4, §13, §16; README R9, UI, APIs.

## Acceptance criteria

- **AC1** Admin → System → **Updates** (`/admin/updates`, nav `nav.admin.updates`), admin-only, served
  through any machine (`home` class). English and Arabic, RTL.
- **AC2** Header: cluster version (the home's), the next planned update ("v1.5.0 tonight at 03:00",
  release notes, verified badge, release age) or why there is none (up to date, held: needs approval,
  blocked: reason, deferred until, pinned at, auto install off). Buttons: Update now (confirmation
  dialog naming the version and that TV continues), Skip this version, Defer (date picker), Pin.
- **AC3** Machines table: home first, then nodes; name (link to its web address), role, platform,
  version chip, state (up to date, preparing, prepared, updating, paused, failed, offline, behind,
  held, worker-only, ahead of the home), last update time; filter by state.
- **AC4** Current run: a step timeline (home steps of design §5.3, then one row per target with its
  states), live through the hub topic `updates` over the existing browser WebSocket; the run log
  streams from `GET /api/cluster/updates/runs/{id}/log`. Target actions Retry, Skip; run actions
  Cancel (before migrate only), Resume (held).
- **AC5** History: runs with version, from, trigger (auto, manual, catch_up, retry), result, duration,
  per-machine results; detail shows steps with timings (migration duration from the rehearsal too),
  errors with the failing check, and the archived log.
- **AC6** Settings card: every `rules.updates` key with its default and help text; saves through
  `PUT /api/cluster/updates/settings`; validation (window format, policy enum, numbers ≥ 0).
- **AC7** Epic 28.6's version section links here; the Machines page shows a version chip and a
  "behind" badge; a banner on every page during a run ("Updating to v1.5.0 — TV continues").
- **AC8** Notifications (Epic 33.13 `notifications`, audience admins, localised by key + params):
  `update_scheduled`, `update_installed_home`, `update_rollout_complete`, `update_failed` (with the
  check), `update_rolled_back`, `update_node_failed`, `update_node_paused_schema`,
  `update_verify_failed` (severity critical), `update_release_held` (contract), `update_release_withdrawn`,
  plus `update_check_stale` (no successful check for 14 days). Repeats of the same kind and version
  at most once per hour.
- **AC9** Without the notifications table (slot 0130 not yet applied), `notify` writes only the audit
  row and the Updates page shows the latest unresolved item as a banner.
- **AC10** Audit (`audit_log`, category `update`): every run start/end, step failure, target result,
  admin action (with actor), settings change.
- **AC11** APIs of the README block are implemented with problem+json errors; `POST
  /api/admin/system/update` (Epic 28.3) becomes Update now on the home and returns `{run_id}`.

## Test cases

- **TC1** `Updates.test.tsx` — header states (planned, held, blocked, deferred, pinned, off), machines
  table states, Arabic RTL snapshot.
- **TC2** `UpdatesRunLive.test.tsx` — timeline updates from mocked `updates` topic frames.
- **TC3** `UpdatesSettings.test.tsx` — validation and save.
- **TC4** `TestUpdatesAPIAdminOnly` / `TestUpdateNowConfirmRequired` (`api/internal/cluster/updates`).
- **TC5** `TestCancelRefusedAfterMigrate`.
- **TC6** `TestNotifyKindsOnRunTransitions` — table of run/target transitions → notification kinds.
- **TC7** `TestNotifyFallbackWithoutTable` — audit row only, banner source returned.
- **TC8** `TestNotifyRateLimitPerKindVersion`.
- **TC9** `TestAuditRowsForUpdateActions`.
- **TC10** `TestLegacySystemUpdateReturnsRunID` (Epic 28.3 endpoint).

## Edge cases

- **EC1** The home's API restarts during a run (step 11): the page reconnects its socket and refetches
  the run; nothing is lost (the supervisor's journal is the truth).
- **EC2** The page opened on a node while the node itself is updating: its web blips, the paused page
  or a reconnect follows; the page reloads into the new version.
- **EC3** No run ever happened: history empty, header says "up to date" or "auto install off".
- **EC4** A held platform: the held targets show which node failed and the failing check, with
  Retry/Skip.

## Dependencies

35.3 (policy, catalog), 35.4 (runs), 35.12 (targets). Epic 33.13 (notifications; fallback otherwise),
Epic 28.6 (version section), Epic 31 hub browser socket.
