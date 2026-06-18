# Epic 28 — Auto-Update (GitHub Releases)

> **Status:** spec. **Source:** `specs/epics/28-auto-update/`.
> **Anchors:** [`architecture.md` §6 (Clients)](../../architecture.md#6-clients-web--apps),
> [§9.4 (API server)](../../architecture.md#9-api),
> Epic 22 (DevOps / release pipeline),
> Epic 25 (Cloud relay — server distribution).

## Goal

Maktaba ships as a fleet of self-hosted binaries and store-distributed
apps: `maktaba-server` (the unified home-server binary), the
`maktaba-api` / `maktaba-streaming` / `maktaba-pipeline` services, the
`maktaba-cloud` relay, the Tauri **desktop** app, and the Capacitor
**mobile** apps. A self-hoster who installs once and forgets will, six
months later, be running a binary with known bugs and missing features
unless updating is *effortless and safe*.

Epic 28 makes the **GitHub Releases** page the single canonical package
source and builds one update model on top of it:

1. **Every binary knows what it is** — version, commit and build date are
   stamped at compile time and reported over a uniform API
   (`GET /api/system/version`). No more "which build is this?".
2. **The server watches for new releases** — a background service polls
   the GitHub Releases API, respects a **channel** (stable vs beta),
   caches the answer, and surfaces it at `GET /api/system/updates`.
3. **The server can update itself** — an admin clicks one button; the
   server downloads the correct asset for its OS/arch, verifies the
   SHA-256 against the release's `checksums.txt`, atomically swaps the
   binary keeping a `.bak`, and re-execs — rolling back on a failed
   post-restart health check. Package-managed (`.deb`/`.rpm`) and Docker
   installs get the right instruction instead of a blind binary swap.
4. **The desktop app updates itself** — Tauri's built-in updater reads a
   `latest.json` published to the GitHub Release, downloads in the
   background and applies on next launch, gated by a signing key.
5. **The mobile apps tell you to update** — they can't self-update
   (store policy), so they show an in-app banner linking to the store or
   the `.apk` on the Release page.
6. **One update UX everywhere** — web, desktop and mobile show current
   version, last-check time, update availability, release notes, and (on
   the server, for admins) a one-click update with progress.

### Why GitHub Releases (relationship to Story 25.34)

Story 25.34 specced a manifest-based updater hosted at
`releases.maktaba.app` with EdDSA-signed `manifest.json`. Epic 28 makes
**GitHub Releases the canonical source** instead — the release pipeline
([`release.yml`](../../.github/workflows/release.yml)) already publishes
every artifact, a `checksums.txt`, and keyless-cosign signatures there,
so there is no second system to operate, no CDN to provision, and no
private signing key to guard for the *server* path (cosign is keyless via
OIDC; the desktop path keeps a Tauri updater key because Tauri requires
one). The 25.34 manifest stays valid as a *future* mirror; 28 is the
shipping path. Where 25.34 said `releases.maktaba.app/manifest.json`, 28
says `api.github.com/repos/Hamza-Labs-Core/Maktaba/releases`.

## Stories

| # | Story | What it delivers |
|---|---|---|
| 28.1 | [Versioning & build stamping](story-28-01-versioning-build-stamping.md) | Version/commit/build-date in every binary; uniform `/api/system/version` with `channel`; semver from the git tag; Tauri + web version stamping. |
| 28.2 | [Update check service](story-28-02-update-check-service.md) | Background GitHub-Releases poller, channel-aware compare, TTL cache, `GET /api/system/updates`. |
| 28.3 | [Server self-update](story-28-03-server-self-update.md) | `POST /api/admin/system/update`: download → checksum-verify → atomic swap → re-exec → rollback; deb/rpm + Docker paths. |
| 28.4 | [Desktop auto-update (Tauri)](story-28-04-desktop-auto-update.md) | Tauri updater wired to GitHub Releases `latest.json`; signing key generated + threaded through CI. |
| 28.5 | [Mobile update notification](story-28-05-mobile-update-notification.md) | In-app "update available" banner linking to store / release / `.apk`; dismiss-until-next-version. |
| 28.6 | [Update UI](story-28-06-update-ui.md) | Settings version/update section across web, desktop, mobile; admin one-click update; notification badge. |

## Cross-cutting decisions

| Concern | Decision |
|---|---|
| Canonical source | GitHub Releases (`Hamza-Labs-Core/Maktaba`). |
| Version source of truth | The pushed git tag `vMAJOR.MINOR.PATCH[-beta.N|-rc.N]`. CI stamps binaries from `github.ref_name`. |
| Channels | `stable` (no prereleases) and `beta` (includes prereleases). `stable` is the default. Switching `beta`→`stable` never downgrades. |
| Server integrity | SHA-256 from the release's `checksums.txt` (already cosign-signed as a blob). |
| Desktop integrity | Tauri updater signature (minisign-style key; `latest.json` carries the signature). |
| Cache | In-memory, TTL-bounded (default 24 h); manual "check now" bypasses it. |
| Self-update scope | The `maktaba-server` binary it runs as. Docker → 409 + instructions; deb/rpm → invoke the package manager. |
| Rollback | `.bak` of the previous binary; restored if the post-restart health check fails. |

## Non-goals (this epic)

- Delta/binary-diff updates (full-asset download only; defer).
- Staged/percentage rollouts (operator manages their own fleet).
- Auto-applying updates without an explicit admin action on the server
  (desktop background-apply on next launch *is* in scope; the server is
  deliberately click-to-update).
- Cross-service version-skew orchestration for separately-deployed
  api/streaming/pipeline containers (Docker users pull a tag set
  together; the unified `maktaba-server` is one binary so skew is moot).
