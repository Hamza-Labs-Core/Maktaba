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
  `auto_install` true (decided), `channel` stable, `window` "03:00-05:00", `window_overrun_minutes` 120,
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
  passed, no active run, every home role running and no crash in 10
  min. *Amended (2026-10-28: a TV change never blocks an update):* a TV change never holds a
  run (the `tv_change_in_flight` blocker is removed). The run starts; the TV change controller sets
  the change in flight aside while the update holds the TV (a draft or a ready one is left as it is, a
  staged or verifying one is cancelled cleanly back to draft with its staging slot released) and checks
  it again on the new release after the update (Epic 33 "As built: who made a change, and app
  updates"); the update's own binary change gets the staging slot first; only the home's restart waits
  for a change that is swapping, at most 2 min, then goes on (the swap follows its failure rollback). Start = insert `update_runs(state=planned, trigger=auto)` then `POST /update/start` on the
  supervisor loopback.
- **AC6** Just before starting, the GitHub release is re-read: gone, turned prerelease on `stable`, or a
  different `checksums.txt` → `withdrawn_at` set, not installed, notified.
- **AC7** "Update now" (`POST /api/cluster/updates/run {version?, confirm:true}`, admin) skips only the
  window and age conditions and a recent crash (the running release's, which the update may cure;
  audited `update_past_recent_crash`); `POST /api/admin/system/update` maps to it and returns `{run_id}`.
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
- **TC3** `TestSchedulerBlockers` — active run, crashed role, deferral; a TV change in any in-flight
  state does not block (`TestDriverUpdateGoesAheadDuringAReadyTVChange`,
  `TestDriverRestartWaitsForATVSwap*`, and in `tvchange` `TestManualReadyChangeIsSetAsideByAnUpdateAndCheckedAgain`,
  `TestStagedChangeIsSetAsideByAnUpdateAndStagedAgain`).
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

## As built (2026-11-24): the release feed

AC2's poller no longer reads GitHub's list of releases. On 2026-11-23 that list was 10.5 MB for 30
releases of about 190 files each, past every home's cap, and no home saw a release for a day (#1592 read
it in pages as a stopgap). A home now reads the **release feed**: one small signed file per channel and
platform, `https://maktaba.tv/updates/v1/<channel>/<platform>.json`, with its Sigstore bundle beside it.

- **Contents.** The newest 10 releases of the channel, each with only that platform's files (name,
  address, size, SHA-256), its `checksums.txt` and `release.json` digests, its migration set and its
  published time. A pulled release stays listed with `withdrawn` and the reason. Shape and checks:
  `shared/go/releaseverify/feed.go`.
- **Signed.** `update-feed.yml` on main signs each file keyless after every release is published and
  verified. A home accepts only that identity (`Identity.FeedSAN`, trigger `workflow_run`,
  `workflow_dispatch` or `schedule`), then checks that the content is the channel, platform and
  repository it asked for, with every file at its release's own GitHub address. The files are still
  downloaded from GitHub and verified as in 35.1.
- **Fallback.** GitHub's list (in pages, #1592) is read only when the feed cannot be reached, or is older
  than the one the home read last, or too old. A feed that fails its checks stops the check and is never passed over.
- **The cluster.** The home's own platform's feed says which releases there are; the feed of each platform
  its machines run adds their files. Once a release verifies, the catalog keeps every file its signed
  `checksums.txt` lists, from beside it, so machine jobs (packs, plugins) and the mirror find each by name.
- **Freshness.** A feed older than the one last read, or signed more than 21 days ago, is not believed
  (GitHub's list is read): a rollback of maktaba.tv never stops updates.
- **Withdrawal.** TC5's re-check reads the feed first: a release pulled there
  (`tools/release/pull-release.sh`, the repository variable `UPDATE_FEED_WITHDRAWN`) is withdrawn.
  Homes before the feed still learn of a pull only from GitHub.
- **Hosting.** maktaba.tv's Worker serves the feed as static files; every site deploy carries the newest
  feed. No new server or service. A live check fails `update-feed.yml` when maktaba.tv does not list a new
  release for every platform.
- **Tests.** `TestFeedFromTheRealV0_3_161` and `TestFeedSizeOfAFullWindow` (generation from the real
  v0.3.161), `TestVerifyFeedSignature` (good, tampered, missing, other signers),
  `TestFeedIsReadInsteadOfGitHubsList`, `TestFeedFallsBackToGitHubOnlyWhenUnreachable`,
  `TestFeedRefusedNeverFallsBack`, `TestFeedOlderThanTheLastFallsBack`, `TestFeedCapAndARealGeneratedFeed`.
  The update rehearsal serves the feed at its real shape, and this commit's home must find each release
  there.
