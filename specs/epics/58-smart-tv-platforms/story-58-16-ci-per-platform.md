# Story 58.16 — CI for each TV: emulators where they exist, real-device checklists where they don't, logs kept

> Epic 58 · Phase 1 (jobs and log upload), each platform's tests land with its story · Depends on: — · Needed by: every platform story

## Why

Every app needs automated tests in CI, run only for the code they cover. Each test keeps the
app's log in the CI report. Screenshots are dark, in English and Arabic. The TV platforms differ
in what can run on a CI runner at all, and this story says it plainly for each one, so no one
mistakes a fake for a real device.

## Description

**Jobs** (each a reusable workflow, triggered only by its paths through `tools/ci/ci_paths.py`):

| Job | Paths | Runs |
|---|---|---|
| `_android-tv.yml` (exists) | `apps/tv/android/**`, `apps/android/core/**`, `apps/android/client/**` | both flavors; 58.05 T01–T05 |
| `_tv-web.yml` (new) | `apps/tv/web/**`, `web/src/bigscreen/**`, `web/vite.config.ts` | Vitest; `build:tv` + 58.08 T01; Playwright launcher + `/tv/` against the mock home; package lint for `.wgt` and `.ipk` |
| `_cast-receiver.yml` (new) | `web/packages/castreceiver/**` | Vitest; Playwright with the CAF stub |
| `_roku.yml` (new) | `apps/tv/roku/**`, `shared/i18n/visual-vectors.json` | BrighterScript build and lint; brs-engine unit and render tests |
| `_vega.yml` (58.18) | `apps/tv/vega/**` | Jest; Vega Virtual Device run if KVM is available on the runner |
| existing `api`, `streaming`, `cloud` jobs | their paths | 58.01–58.03, 58.07's grants, 58.14's bypass rows |

**What runs where, honestly:**

| Platform | In CI, automated | Needs a real device |
|---|---|---|
| Fire TV | Robolectric screenshots; instrumented tests on Google's Android TV emulator (API 30, no Google APIs); dependency and manifest checks | codecs, HDR, Dolby, Amazon's remote and launcher behaviour (58.05 T06) |
| Samsung | launcher and `/tv/` in Playwright with a `webapis` shim; package lint; signing rehearsed on each tag | AVPlay playback, HDR, passthrough, keys on the real remote, performance (58.10 T05) |
| LG | the same in Playwright with a webOS shim; the `.ipk` packed and linted | native HLS, HDR, Dolby Vision per year, Magic Remote, performance (58.11 T05) |
| Cast receiver | Playwright with a CAF stub | anything involving Google's SDK on a device, LAN fetch rules, formats (58.07 T05) |
| Cast senders | unit and UI tests with SDK fakes | discovery and the session with a real receiver (58.06 T06) |
| Roku | brs-engine logic, focus and layout tests; build and lint | the Video node, rendering, fonts, timing, certification (58.12 T04–T05, 58.13 T05–T06) |
| Vega | Jest; the Vega Virtual Device where the runner has KVM | real Fire TV Stick playback (58.18) |

**No emulator job for Samsung or LG.** The Samsung TV emulator needs Tizen Studio's TV extension
and a KVM runner; LG's webOS TV Simulator downloads only behind LG's license page, with no
address a runner can fetch. Neither has a documented headless mode, and neither has a media
pipeline (no AVPlay, no UHD, no real media), so a smoke run would show less than the Playwright
shims do. They are not run in CI (58.10, 58.11; `docs/stores/samsung.md`, `docs/stores/lg.md`).

**Real-device checklists.** Each platform has a checklist in `docs/stores/<platform>.md`. It is run
by a person on the devices it lists before a release candidate is promoted or submitted, and its
result is kept as a release note line ("Samsung checklist passed on 2020/2022/2025 TVs, launcher
1.3.0"). A release session does not submit a TV build whose checklist has no result for that
version.

**An optional device lab** (owner question Q6) would turn the checklists into scheduled runs:
- a self-hosted runner on the home network, with a Fire TV Stick, a Samsung and an LG TV in
  Developer Mode, a Roku in developer mode and a Google TV Streamer;
- `adb`, `sdb`, `ares-*`, Roku's WebDriver and the Cast CaC Tool driving them;
- it runs on a schedule and on release candidates, never on every pull request, since TVs
  are shared hardware.

The default is no lab, with checklists by hand.

**Logs in every report.** Every job uploads its app logs as artifacts, linked from the job summary,
kept 30 days:
- **Playwright:** the console log, the network log and the trace.
- **Android:** Robolectric stdout (`logging=stdout`, as the Android roots already set) and the
  emulator's logcat.
- **Roku:** brs-engine stdout.
- **Emulators and simulators:** their own logs.
- **The launcher:** its in-app log buffer (58.09).

On a device run, the device's log (Roku's debug console on port 8085, `sdb dlog`, `ares-log`,
logcat, the CaC Tool's capture) is attached to the checklist's result. Logs pass the existing
redaction (no `sig`, tokens or grants).

**Screenshots.**
- Each platform's UI tests take dark screenshots in English and Arabic of: the code screen,
  Home, a library's Library tab, a series, the player overlay, the blocked page, and Update the
  home.
- They are kept as references, Roborazzi-style, where the renderer is stable (Robolectric,
  Playwright).
- brs-engine screenshots are kept with a tolerance and labelled as not Roku's renderer.
- maktaba.dev's Apps pages get the platform's screenshots from these runs on test data (CLAUDE.md
  rule 7), in a later pull request per platform.

## Acceptance criteria

- **Given** a change only under `apps/tv/roku/`, **then** only `_roku.yml` runs among the TV jobs.
- **Given** any TV test failure, **then** its job summary links the app log and the screenshots.
- **Given** a release candidate with no checklist result for Samsung, **then** the release
  session's submission step stops with that reason.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | Python unit (`tools/ci/tests`) | `ci_paths.py` with the new jobs | changed-file sets | the right jobs only |
| T02 | Workflow lint (actionlint) | the new workflows | lint | clean; every job has an artifact upload step for logs |
| T03 | Python unit | release submission guard | checklist result missing / present | stops / continues |

## Strings

None.

## Edge cases

- **An emulator run is flaky** (KVM missing on a runner, for Vega's Virtual Device): it is not required. It reports
  "skipped: no KVM" and the required jobs decide the merge. Flakiness in a required job is
  fixed, never retried away.

## Build plan

| File | Change |
|---|---|
| `.github/workflows/_tv-web.yml`, `_cast-receiver.yml`, `_roku.yml` (new) | jobs |
| `tools/ci/ci_paths.py` | paths |
| `tools/release/tv_checklist_guard.py` (new) | T03 |
| `docs/stores/*.md` | checklists |
