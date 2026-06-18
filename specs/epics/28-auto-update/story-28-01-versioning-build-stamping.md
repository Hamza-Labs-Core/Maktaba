# Story 28.1 — Versioning & build stamping

> Epic 28 · Auto-Update · Phase 1 (foundation)

## Description

Every Maktaba artifact must be able to answer "what version am I?"
identically, and that answer must come from one source of truth: the git
tag the maintainer pushed. This story makes version, commit and build
date a compile-time fact in every binary and surfaces it over a uniform
API, so Stories 28.2–28.6 have something to compare against.

Scope:

- **Compile-time stamping.** Each Go module
  (`api`, `streaming`, `cmd/maktaba-server`, `cloud`) already carries an
  `internal/version` package (or `main.Version`) whose vars are
  overridden via
  `-ldflags "-X <pkg>.Version=<tag> -X <pkg>.Commit=<sha> -X <pkg>.BuildDate=<epoch>"`.
  This story makes that contract uniform and verified across every build
  path (Makefile, `release.yml`, `ci.yml`, `relay.yml`).
- **Desktop.** `apps/desktop/src-tauri/tauri.conf.json`'s `version` field
  is set from the tag at build time (it drives the Tauri updater's
  current-version comparison and the installer's product version).
- **Web.** The web build writes `dist/version.json`
  (`{version, commit, build_date}`) and exposes the version to the SPA via
  `VITE_APP_VERSION`, so the in-app "About" reflects the real build.
- **Mobile.** Android `versionName` (= tag) and a monotonic `versionCode`
  are derived from the tag at packaging time.
- **API.** `GET /api/system/version` returns
  `{version, commit, build_date, channel}` — `channel` derived from the
  running version string (a `-beta`/`-rc` suffix ⇒ `beta`, else
  `stable`), with the operator override `MAKTABA_UPDATE_CHANNEL` taking
  precedence.

Semver contract: `vMAJOR.MINOR.PATCH` with optional `-beta.N` / `-rc.N`
prerelease suffixes. The leading `v` is the tag form; the API reports the
string as built.

## Acceptance criteria

- **Given** a binary built by `release.yml` from tag `v1.4.2`,
  **when** I call `GET /api/system/version`,
  **then** it returns `version="v1.4.2"` (or `1.4.2`), the full commit
  SHA, a build date, and `channel="stable"`.

- **Given** a binary built from tag `v1.5.0-rc.1`,
  **when** I call `GET /api/system/version`,
  **then** `channel="beta"` (prerelease suffix detected), unless
  `MAKTABA_UPDATE_CHANNEL` overrides it.

- **Given** a developer runs `go build` directly with no ldflags,
  **when** the binary starts,
  **then** it still runs and reports `version="unknown"` / `"dev"` rather
  than crashing (defaults preserved).

- **Given** the web bundle built by CI,
  **when** I fetch `/version.json`,
  **then** it returns the same `{version, commit, build_date}` the Go
  binaries report for that tag.

- **Given** the desktop app built from `v1.4.2`,
  **when** it checks for updates,
  **then** its self-reported current version is `1.4.2` (read from
  `tauri.conf.json`), so the updater compares correctly.

- **Given** the Android app packaged from `v1.4.2`,
  **when** I inspect the APK,
  **then** `versionName` is `1.4.2` and `versionCode` is a monotonically
  increasing integer derived from the tag.

## Test cases

| ID  | Type        | Setup | Action | Expected |
|-----|-------------|-------|--------|----------|
| T01 | unit        | `version.String()` with stamped vars | render | `"v1.4.2 (sha, built epoch)"` |
| T02 | unit        | channel-from-version `1.5.0-rc.1` | derive | `beta` |
| T03 | unit        | channel-from-version `1.4.2` | derive | `stable` |
| T04 | unit        | `MAKTABA_UPDATE_CHANNEL=beta` on a stable build | derive | `beta` (override wins) |
| T05 | integration | built api binary | `GET /api/system/version` | all four fields present, non-empty |
| T06 | integration | CI web build | read `dist/version.json` | matches tag |
| T07 | regression  | `go build` no ldflags | run + version | `unknown`/`dev`, no panic |

## Edge cases

- **Dirty working tree.** `make` falls back to `git describe --dirty`;
  releases always use the clean tag (`github.ref_name`).
- **No git (tarball build).** `VERSION ?= dev` keeps the build green.
- **Tag without `v` prefix.** Accepted; comparison strips a leading `v`.
- **`versionCode` overflow.** Derivation
  (`MAJOR*1_000_000 + MINOR*1_000 + PATCH`) stays well inside int32 for
  realistic versions; documented.
- **Two builds of one commit.** `BuildDate` is `SOURCE_DATE_EPOCH` (the
  commit timestamp), not wall-clock, so they're byte-identical.

## Files / packages

- `api/internal/version/version.go`, `api/internal/system/version.go`
  (add `channel`).
- `streaming/internal/version/`, `cmd/maktaba-server/internal/version/`,
  `cloud/cmd/maktaba-cloud` (`main.Version`) — verified uniform.
- `Makefile` (`*_ldflags`, `build-web` writes `version.json`).
- `web/package.json` (build writes `dist/version.json`; `VITE_APP_VERSION`).
- `.github/workflows/{release,ci,_build-artifacts,relay,desktop-release,mobile-release}.yml`.
- `apps/desktop/src-tauri/tauri.conf.json` (`version` from tag in CI).

## Open questions

- **Should `channel` ever be `nightly`?** Out of scope here; the API
  field is a free string so a future nightly build can report it without
  a schema change.
