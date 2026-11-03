# Story 51.18 — Updates and rollback carry packs

> Epic 51 · Phase 3 · Depends on: 51.16 · Needed by: —

## Why

First-party packs only fit the server version they were built with (H5, 51.15: the index's
`server` is the exact version). A Core home that installed `runtime.ml` 0.4.2 and updates to
0.4.3 must have `runtime.ml` 0.4.3 on disk **before** 0.4.3 starts, or transcription breaks
after every update. A rollback to 0.4.2 must find 0.4.2's packs again. And old packs must not
pile up: `runtime.ml` is about 2 GB per version.

The update path is where the site went down twice on 2026-10-28. The standing rule
since: every update, rollback and restore path is **proven in the update rehearsal in CI**,
never tried on the live Mac (`docs/update-rehearsal.md`). This story changes that path, so it
ships with its rehearsal scenarios.

## Description

### 1. A new prepare step: `packs`

The updater's prepare steps today are preflight → stage → smoke → rehearse
(`cmd/maktaba-server/internal/updater/machine.go` `prepare`). A failure in any of them sets
`prepare_failed` and calls `Installer.Discard`: nothing has changed and the home keeps serving.
This story adds **`packs`** between `stage` and `smoke`, for every install kind (layout,
runtime copy, app bundle) and for joined machines:

1. **What is installed.** Read `<data>/components/*/current`: every component installed from a
   pack (its folder holds `pack.json`) or a pinned model (`model.json`). Components that came
   with the payload need nothing.
2. **The new index.** `components-<new version>.json` from the new release (its
   `release.json` names it), verified with `releaseverify.VerifyComponentsIndex` (51.15)
   against the new version. A release with no index while packs are installed fails the step
   (`index_missing`): updating would leave those components broken.
3. **Fetch and verify** each installed pack for the new version, with 51.16's download, verify
   and safe unpack, into `<data>/components/<id>/<new version>_<digest12>/`. Models are fetched
   only when the index pins a new revision; the old revision stays.
4. **Prepare** where it can be done before the switch: nothing runs from the new pack yet. The
   venv of the new release is built from the new `runtime.ml` pack's wheelhouse at its first
   start, by the existing venv gate (`serve_appbundle.go`, `venvGate`), exactly where today it
   takes the home's wheelhouse.
5. Record in the journal `packs: [{id, version, dir}]`.

**`current` is not touched.** The running release keeps reading its own version's folder; the
new release reads its own at boot and rewrites `current` (51.16 §2). So the switch to packs is
exactly as atomic as the switch to the payload: it is the same switch.

**All-or-nothing.** If any pack fails (download, size, sha256, `files.sha256`, an unsafe entry,
an expired or wrong index), the step fails with that code (`pack_verify_failed`,
`pack_download_failed`, `index_expired`, …), the update is `prepare_failed`, and `Discard`
deletes every folder this run created. Nothing switched; the home never stopped.

### 2. Room

`Preflight` already checks free space (`checkRoom`: `roomRelease`, plus `roomVenv` for a plain
server). It adds, for each installed pack, the new pack's `size + unpacked_bytes`, and the
bytes of any model revision that changes. Not enough: `disk_space` before anything is fetched,
the same answer scenario 08 proves for the release itself.

### 3. Rollback

`Revert` needs nothing new for packs: the previous release's folders were never deleted, and
the previous release reads its own version's folder at boot and points `current` back at it.
What this story adds is a check in `Revert`'s preflight: if a kept release's pack folder is
missing (someone deleted it by hand), the rollback still goes ahead (serving beats a component)
and the component reports `Failed (pack_missing)` with an *Install again* action.

### 4. Pruning

`Commit` prunes releases to `KeepVersions` (never fewer than 2). It now also prunes pack
folders: a `<version>_*` folder is kept when its version is the running one or one of the kept
releases (`Installer.Kept()`), and deleted otherwise. Model revisions no kept release's index
pins are removed with `maktaba-pipeline models remove --revision`. A prune that cannot delete a
folder (busy on Windows) logs it and retries at the next boot; it never fails the commit.

### 5. Joined machines

A machine updates after its home commits (scenario 10a). Its `packs` step fetches from the
home's mirror first, then GitHub, like the release itself (`internal/homemirror`). Every file
is verified the same way: the home's word is never the trust anchor.

### 6. Rehearsal scenarios

Scenario **12** (no behaviour change: processes, stages, extensions and loops diffed before and
after the first release with this epic) belongs to story 51.03. This story adds **13** and
**14** to `.github/workflows/update-rehearsal.yml`'s matrix and to
`docs/update-rehearsal.md`'s table, on the `small` profile. The rehearsal's fake GitHub
publishes each release with its index and packs, built by `tools/release/packs/build.sh` for
`linux-amd64` and signed by the fake Sigstore (`-tags e2etrust`). The pack used is the real
`tool.yt-dlp` wheelhouse (a few MB). The rehearsal's pipeline is the stand-in (its Python is
set, so no venv is built): the pack's venv preparation is skipped and logged, which the
scenario checks.

| # | Scenario | Asserts | Downtime budget |
|---|---|---|---|
| 13 | Update with packs installed: the previous release with `tool.yt-dlp` installed from its pack; update to this commit; then update to the release after it, whose API refuses what it migrated (as 03), so it rolls back | first update `done`; the new pack folder exists, verified, before the switch (journal step `packs` ends before `switch` starts); `current` names it after the new release starts; after the rollback the previous release runs, `current` names its kept folder, the component is Installed; after a later passing update, folders outside the kept releases are pruned | **2 min** for the update, as without packs (N13); 4 min for the rollback, as 03 |
| 14 | A pack fails verification mid-update: this commit's `tool.yt-dlp` pack on the fake GitHub has one byte changed after signing | the run ends `prepare_failed` at step `packs` with `pack_verify_failed`; no switch, no restart, no restore; every folder the run made is gone; the previous pack folder and `current` unchanged; the record and the rows unchanged; the site serves throughout | **5 s** (nothing changes, as 08) |

### 7. Words (English + Arabic, same PR)

The Updates page shows the step and its errors through the existing `updates.step.*` and `updates.code.*` keys:

| Key | English | Arabic |
|---|---|---|
| `updates.step.packs` | Getting the parts you added | جارٍ جلب الأجزاء التي أضفتها |
| `updates.code.pack_verify_failed` | A part for the new version did not pass its check. Nothing was changed. | لم يجتز جزء للإصدار الجديد فحصه، ولم يتغيّر شيء. |
| `updates.code.pack_download_failed` | A part for the new version could not be downloaded. Nothing was changed. | تعذّر تنزيل جزء للإصدار الجديد، ولم يتغيّر شيء. |
| `updates.code.index_missing` | This version has no list of parts, but this home uses some. Nothing was changed. | لا توجد قائمة أجزاء لهذا الإصدار، وهذا الخادم يستخدم بعضها، فلم يتغيّر شيء. |
| `components.reason.pack_missing` | Its files were deleted. Install it again. | حُذفت ملفاته. ثبّته مرة أخرى. |

## Acceptance criteria

- **Given** a home with a pack installed,
  **when** it updates,
  **then** the new version's pack is downloaded and verified before the switch, and the update's
  downtime stays within the budget of the same update without packs (N13).
- **Given** a pack of the new release that fails its sha256,
  **when** the home updates,
  **then** the update is `prepare_failed` at `packs`, nothing switches or restarts, and the
  site serves the whole time (scenario 14).
- **Given** an update rolled back after its switch,
  **when** the previous release starts,
  **then** it runs on its kept pack folder and the component is Installed (scenario 13).
- **Given** a commit with `KeepVersions = 2`,
  **when** it finishes,
  **then** only the running and the kept release's pack folders remain.
- **Given** packs installed and a new release with no index,
  **when** the update prepares,
  **then** it fails with `index_missing` and nothing changes.
- **Given** a Full home with no pack installed,
  **when** it updates,
  **then** the `packs` step is skipped (`skipped: no packs`) and downloads nothing.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit | updater, fake release with index and one pack | prepare | step `packs` done; folder for the new version; `current` unchanged |
| T02 | unit | same, pack sha256 wrong | prepare | `prepare_failed`, `pack_verify_failed`; new folders gone |
| T03 | unit | no pack installed | prepare | `packs` skipped; no network call |
| T04 | unit | packs installed, release without index | prepare | `index_missing` |
| T05 | unit | fake disk | free space below release + pack | `disk_space` at preflight |
| T06 | unit | three versions' folders, kept = {running, previous} | commit | oldest folder deleted; Windows busy file → logged, commit ok |
| T07 | unit | revert with a kept folder deleted by hand | revert | rollback proceeds; component `pack_missing` |
| T08 | unit | joined machine, home mirror holds the pack | prepare | fetched from the home, verified |
| T09 | unit (web) | Updates page | step `packs` and each error | en + ar words exist |
| T10 | rehearsal (CI) | scenario 13 | as §6 | as §6, within 2 min / 4 min |
| T11 | rehearsal (CI) | scenario 14 | as §6 | as §6, within 5 s |

## Edge cases

- **An install running when the update starts.** The `packs` step waits for it (51.16) up to
  10 min, then fails `components_busy`; the next scheduled try runs it.
- **A model whose revision changed is 3 GB.** Fetched during prepare, before any downtime; the
  update simply takes longer to become `prepared`. The update's own deadline counts from the
  switch, not from prepare.
- **The home removed a component during prepare.** The fetched folder is pruned at commit.
- **A Core Mac app's update** is installed by the desktop app (`autoinstall.rs`); the packs were
  staged by the server's updater before it ordered the app, so the app does nothing new.
- **Rollback by the launcher** (3 exits in 10 min, no confirm in 15 min, `run.go`): the same
  as `Revert`, the previous release reads its own folders.
- **Never on the live Mac.** Scenarios 13 and 14 run only in CI; running them locally follows
  `docs/update-rehearsal.md` (a Linux machine or a scratch `HOME`, never a live install).

## Build plan

### Files to touch

| File | Change |
|---|---|
| `cmd/maktaba-server/internal/updater/packs.go` (new) | the `packs` step: what is installed, fetch, verify, unpack, journal; `Discard` and prune helpers (uses `internal/packs` from 51.16). |
| `cmd/maktaba-server/internal/updater/machine.go` | `StepPacks` in `prepare` between stage and smoke; prune after `Commit`. |
| `cmd/maktaba-server/internal/updater/journal.go` | `StepPacks`; `Packs` in the journal. |
| `cmd/maktaba-server/internal/updater/appbundle.go`, `layout.go`, `runtime.go` | `Preflight` room adds packs; `Discard` removes the run's pack folders; `Revert` check for missing kept folders. |
| `cmd/maktaba-server/internal/updater/source.go` | fetch the index through the same `Source` (trust root, home mirror). |
| `cmd/maktaba-server/internal/updater/packs_test.go` (new) | T01–T08. |
| `api/rehearsal_packs_e2e_test.go` (new) | scenarios 13 and 14. |
| `api/rehearsal_harness_e2e_test.go` | publish an index and packs with each release; corrupt a pack after signing. |
| `tools/rehearsal/build.sh` | build the `linux-amd64` `tool.yt-dlp` pack and index for both releases. |
| `.github/workflows/update-rehearsal.yml` | matrix rows 13 and 14; `paths` gains `cmd/maktaba-server/internal/packs/**`, `tools/release/packs/**`, `shared/go/releaseverify/components*`. |
| `docs/update-rehearsal.md` | the two rows; scenario 12 links to 51.03. |
| `web/src/i18n/en.json`, `web/src/i18n/ar.json` | §7 words. |

### Tests

- T01–T08 in the Go unit job (`updater`); T09 web unit.
- **CI only:** update-rehearsal scenarios 13 and 14 (T10, T11). They gate every tag through
  `tools/release/tag-guard.sh`, like every scenario.

### Rollout

Ships with 51.16. A home with no pack sees one skipped step. The first tag after it is gated by
scenarios 13 and 14 passing on that exact commit.

### Rollback

The release before this story has no `packs` step: a home rolled back to it with packs installed
keeps its kept pack folders (that release does not read them; Phase 3 components wait). The
`[components] packs = off` switch (51.16) also skips the step and refuses updates while packs
are installed (`packs_disabled`), rather than switching to a release whose packs are missing.

### Dependencies

51.16 (the installer, folders and `current`), 51.15 (the index). Scenario 12 is 51.03's.
