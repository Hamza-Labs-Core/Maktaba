# Story 60.11 — Download: every device, with its real release asset

> Epic 60 · Phase 2 · Depends on: 60.01 part B (download cards), 60.02 part B, 60.09, 60.08 (the
> public image) · Owner actions: A6 (store listings), A7 (package repositories)

## Why

Today a household that wants Maktaba opens a GitHub release with about 150 files and has to guess
which one is theirs. The release notes explain it in developer terms. The download page names each
device in household words and offers the one right file, with the others a click away. It never
offers something a household cannot install.

## Description

**The data.** `scripts/download.mjs` builds the page from:
- **The latest stable release's `release.json`** (fetched at build time from the GitHub release,
  with its checksum verified against the signed `checksums.txt`):
  - the version and date;
  - each asset's name, size and sha256.

  Asset names are matched by pattern, never typed with a version.
- **`downloads.json`** (in the site), one entry per platform: the patterns, the requirements line,
  the install guide link, and the optional public listing URLs:
  - `appStore`;
  - `testFlightPublic`;
  - `googlePlay`;
  - `playTestingPublic`.

  An entry with no matching asset and no listing **is not shown**.
- **The package repositories** read the repository variables (`MAKTABA_PACKAGES_URL`,
  `HOMEBREW_TAP_REPO`) at build time. The apt, dnf and Homebrew rows show only when they are set,
  with the same commands the release notes print.

**The cards** (60.01's download card: icon, name, version, size, primary button, other formats,
requirements):

| Card | Primary | Other formats | Shown when |
|---|---|---|---|
| **Mac** | `Maktaba_<v>_aarch64.dmg` (the app that runs everything) | `Maktaba-Core_<v>_aarch64.dmg`, Homebrew cask | always (asset in every release) |
| **Windows** | `Maktaba_<v>_x64-setup.exe` | `maktaba-windows-portable-<v>.zip`; the Player alone (`Maktaba-Player_<v>_x64.msi`) | always |
| **Linux** | `Maktaba_<v>_amd64.AppImage` (arm64 by toggle) | `.deb`, `.rpm`, the headless payload for a machine without a desktop | always |
| **NAS and Docker** | link to `/install/nas/` (the chooser) | `maktaba-docker-compose.yml`, the Synology and TrueNAS files | always, once 60.08's public image check passes |
| **Server packages** | apt and dnf commands, `brew install …/maktaba` | `install.sh`, the server archives | the apt, dnf and brew rows only with A7 |
| **iPhone and iPad** | App Store badge | public TestFlight link | only with a public listing or public TestFlight link (Q5, A6) |
| **Apple TV** | App Store badge | — | the same |
| **Android and Android TV** | Google Play badge | public testing link | the same |

- **Platform first:** the visitor's platform card comes first (from the user agent, as on the home
  page), and `#<platform>` anchors work.
- **Each card shows** the version, the release date, the file size, and a "Verify this download"
  disclosure with the sha256 and the cosign command from the release notes (in a code block with
  copy, in English inside the Arabic page).
- **"What do I need?"** A short block in household words:
  - the computer or NAS that holds your films runs Maktaba;
  - your TV and phones get the Player apps;
  - links to the install guides.
- **All files:** a link to the GitHub release.
- **Store badges** are the stores' official badges in each language, used under each store's badge
  rules, self-hosted as SVG.

**When it rebuilds.** The release workflow dispatches `website-deploy.yml` after a stable release
is published (a `workflow_dispatch` call with the tag), so the page names the new version within
minutes. The page shows the version it was built for. It never computes "latest" in the browser.

**Release notes.** `notes.md.tmpl` gains one line at the top of Install: "Choosing a file?
`https://maktaba.tv/download/` picks the right one for each device." The line ships with this
story, so the link check passes.

## Acceptance criteria

- **Given** a stable release, **then** within one deploy the page shows its version, and every
  primary and secondary link downloads an asset of that release whose sha256 matches the signed
  `checksums.txt`.
- **Given** a platform with no asset and no public listing, **then** it has no card.
- **Given** the package repositories unset, **then** no apt, dnf or brew row shows. Given them set,
  **then** the rows show the release notes' commands.
- **Given** each card, **then** it passes 60.04's bar, and its screenshot (60.09) is in the page's
  language.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit | a fixture `release.json` from v0.3.128 | build | the cards above; names and sizes from the fixture |
| T02 | unit | a fixture missing the Windows exe | build | no Windows primary; the portable zip promoted or the card hidden per `downloads.json` |
| T03 | unit | `downloads.json` with `googlePlay` set | build | the Android card with the badge |
| T04 | unit | `release.json` whose checksum does not match `checksums.txt` | build | fails |
| T05 | unit | package vars set / unset | build | rows shown / hidden |
| T06 | Playwright | user agents | load `/download/` | that card first |
| T07 | workflow | a release run (rehearsed with a test tag on a fork or dry run) | publish | the deploy dispatched with the tag |
| T08 | link check | rendered `notes.md.tmpl` | 60.08 | `/download/` resolves |

## Strings

| Key | English | Arabic |
|---|---|---|
| `download.title` | Download Maktaba | نزّل Maktaba |
| `download.version` | Version {v}, {date} | الإصدار {v}، {date} |
| `download.other` | Other formats | صيغ أخرى |
| `download.verify` | Verify this download | تحقّق من هذا الملف |
| `download.need` | What do I need? | ما الذي أحتاجه؟ |
| `download.all` | All files of this release | كل ملفات هذا الإصدار |
| `download.mac.req` | A Mac with Apple silicon | جهاز Mac بمعالج Apple |

(Final wording follows 60.02.)

## Edge cases

- **A release whose desktop leg failed** (an asset missing): the card falls back as `downloads.json`
  says, or hides. A partial release never shows a dead button.
- **A pre-release tag:** never used. The page reads the latest stable release only.
- **GitHub unreachable at build time:** the build fails and the live page stays at the previous
  version. A deploy never ships an empty download page.

## Build plan

| File | Change |
|---|---|
| `web/packages/website/scripts/download.mjs`, `downloads.json`, tests | the data and the cards |
| `web/packages/website/src/pages/download/index.html` (+ `ar/`) | the page shell |
| `web/packages/website/static/badges/` | store badges (en, ar) |
| `.github/workflows/release.yml` | dispatch the site's deploy after a stable release |
| `tools/release/releasenotes/notes.md.tmpl`, `notes_test.go` | the download line |
