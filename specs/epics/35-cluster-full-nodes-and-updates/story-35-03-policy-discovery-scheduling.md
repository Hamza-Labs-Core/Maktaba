# Story 35.3 — Update policy, discovery and scheduling

## Description

*As the owner, I want the home to notice a new signed release by itself and install it at night, and I
want to be able to say "not this one", "not before Friday" or "stay on 1.5" from the settings page.*

The API's `Updater` (Epic 28.2, `api/internal/system/updater.go`) stays the discoverer. It now keeps a
catalog of releases newer than the watermark, verifies each one's signed files as soon as it sees them,
chooses the target by policy (`rules.updates`) and starts a run in the quiet window by asking the
supervisor (35.4).

Design: [design.md](design.md) §4, §3.5; README R4, R3.6, settings table.

## Acceptance criteria

- **AC1** Settings `rules.updates` (schema.json, settings UI, `app_settings`) with the README defaults:
  `auto_install` true (owner), `channel` stable, `window` "03:00-05:00", `window_overrun_minutes` 120,
  `min_release_age_hours` 24, `pinned_version` "", `deferred_until` null, `skipped_versions` [],
  `node_failure_policy` hold_platform, `node_health_timeout_minutes` 10, `drain_timeout_minutes` 10,
  `rehearse_migrations` true, `rehearse_max_db_gb` 20, `keep_versions` 3, `backups_keep` 3, `catch_up`
  immediate, `proxy_cache_mb` 2048. `MAKTABA_UPDATE_*` env stays as the base, settings override.
- **AC2** The poller runs every 6 h (and on "check now"); every release of the allowed channel newer than
  the watermark is written to `release_catalog` (slot S1, next free at implementation time); for each new
  entry only `checksums.txt`, its bundle and `release.json` are downloaded and verified (35.1 steps 1–4);
  `verify_state` recorded; a failure is a critical notification at once, whatever the policy.
- **AC3** Target choice (design §4) as a pure function: verified, not withdrawn, not blocked, channel
  allowed, `> watermark`, not skipped, `<= pinned_version` when set, `min_upgrade_from <= current`;
  highest by semver; stepping stones when `min_upgrade_from > current`.
- **AC4** A target with `breaks_previous_home` is `held: needs approval`, notified once, and installed
  only by "Update now".
- **AC5** The scheduler ticks every minute and starts a run only when: `auto_install`, a target exists,
  its age ≥ `min_release_age_hours`, now inside `window` and ≥ 30 min before its end, `deferred_until`
  passed, no active run, no TV change in `staged…swapping`, every home role running and no crash in 10
  min. Start = insert `update_runs(state=planned, trigger=auto)` then `POST /update/start` on the
  supervisor loopback.
- **AC6** Just before starting, the GitHub release is re-read: gone, turned prerelease on `stable`, or a
  different `checksums.txt` → `withdrawn_at` set, not installed, notified.
- **AC7** "Update now" (`POST /api/cluster/updates/run {version?, confirm:true}`, admin) skips only the
  window and age conditions; `POST /api/admin/system/update` maps to it and returns `{run_id}`.
- **AC8** Skip (`/skip {version}`), defer (`/defer {until}`), pin (settings) are audited; a pin below the
  watermark is rejected (400 `pin_below_installed`); switching `beta → stable` never selects a lower
  version.
- **AC9** `auto_install = false` → the Epic 28 behaviour: check, catalog, notify "update available".
- **AC10** `GET /api/system/updates` gains `verified`, `policy`, `next_run_at`; a check older than 14 days
  raises a warning notification (freeze hint).

## Test cases

- **TC1** `TestChooseTarget` — table: channel, pin, skip, watermark, age, withdrawn, blocked,
  `min_upgrade_from` stepping stone, contract hold.
- **TC2** `TestSchedulerWindow` — fake clock across the window edges, the 30-minute margin, DST change.
- **TC3** `TestSchedulerBlockers` — TV change in flight, active run, crashed role, deferral.
- **TC4** `TestCatalogVerifiesNewEntries` (fake GitHub + fixture release from 35.1), failure notifies.
- **TC5** `TestRecheckWithdrawn`.
- **TC6** `TestUpdateNowIgnoresWindowAndAge`, `TestPinBelowWatermark400`, `TestNonAdmin403`.
- **TC7** `UpdatesSettings.test.tsx` — settings card validation (window format, pin).

## Edge cases

- **EC1** Window crossing midnight ("23:00-01:00") is valid.
- **EC2** DST: the window is local wall time; a skipped hour shortens that night's window.
- **EC3** GitHub rate limit: `Retry-After` honoured, stale catalog kept (Epic 28.2 behaviour).
- **EC4** A release is re-tagged with new assets: different `checksums.txt` digest at the same version →
  `ErrReplay` if installed, else re-verified as a new catalog entry and notified.

## Dependencies

35.1; Epic 28.2 (built); 35.4 (supervisor start endpoint); Epic 33.10 (TV change state for the blocker).
