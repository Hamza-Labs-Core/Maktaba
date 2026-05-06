# Story 25.34 — Auto-update mechanism

> Epic 25 · Cloud relay · Phase 6 (server distribution)

## Description

A cross-platform updater that keeps installed Maktaba servers
current without users having to remember. Each platform has its
native update path; this story unifies them under one model:
**release manifest + channel + signed payloads**.

Release manifest:

- Hosted at `https://releases.maktaba.app/manifest.json`,
  CDN-cached, ECDSA-signed (sig at
  `https://releases.maktaba.app/manifest.json.sig`).
- Schema:
  ```json
  {
    "channels": {
      "stable":   { "version": "1.4.2",  "min_supported": "1.0.0" },
      "beta":     { "version": "1.5.0-rc.1", "min_supported": "1.0.0" }
    },
    "releases": [
      {
        "version": "1.4.2",
        "released_at": "2026-04-22T...",
        "notes_url": "https://releases.maktaba.app/notes/1.4.2.html",
        "breaking": false,
        "artifacts": {
          "macos-arm64-dmg":   { "url": "...", "sha256": "...", "size": 78234234 },
          "macos-x64-dmg":     { "url": "...", "sha256": "..." },
          "windows-x64-msi":   { "url": "...", "sha256": "..." },
          "linux-amd64-deb":   { "url": "...", "sha256": "..." },
          "linux-arm64-deb":   { "url": "...", "sha256": "..." },
          "appimage-x64":      { "url": "...", "sha256": "..." },
          "docker-tag":        "ghcr.io/hamza-labs-core/maktaba:1.4.2"
        }
      }
    ]
  }
  ```

Per-platform behavior:

- **macOS (25.27).** Sparkle 2 reads our appcast, downloads the
  DMG, validates EdDSA, applies on quit.
- **Windows (25.28).** A small `MaktabaUpdater.exe` runs from a
  scheduled task daily; downloads a delta-MSI when available,
  else full MSI; uses Windows Restart Manager to stop the
  service before update.
- **Linux deb/rpm (25.29).** Apt/Dnf handle it via the repo;
  user opts into `unattended-upgrades` for security-only or
  full updates.
- **Docker (25.30).** Watchtower-style auto-pull is opt-in;
  the recommended pattern is `docker compose pull` on a cron.
- **NAS (25.31).** Vendor's package manager handles it.
- **AppImage (25.29).** AppImageUpdate via `--update`.

Channels:

- `stable` (default) — manually-promoted releases.
- `beta` — releases that survived staging; users opt in via
  Preferences.
- `nightly` — internal only; not exposed to users.

Update workflow inside the running server:

1. **Daily check.** Cron (or per-platform timer) hits
   `releases.maktaba.app/manifest.json`, validates signature,
   reads channel.
2. **Compare.** If `manifest.channels[user_channel].version >
   current_version`, surface a notification in the local UI
   ("Update available: 1.4.2") and allow the user to start
   the update from there.
3. **Download.** Background download to a staging dir;
   verify SHA-256.
4. **Apply.** Pre-update DB dump (`pg_dump` or `cp` for
   SQLite) to `~/Library/Application Support/Maktaba/.previous/`
   (macOS) or equivalent. Stop services, replace binaries,
   start services.
5. **Verify.** Post-update health check (3 retries, 30s
   intervals). On failure, revert binaries and DB dump.

Rollback:

- A `maktaba rollback` command (CLI, also exposed in admin UI)
  swaps to `.previous` directory and restarts. One previous
  version retained; older versions purged.

## Acceptance criteria

- **Given** a user is on `stable` 1.4.1 and `manifest.json`
  publishes 1.4.2,
  **when** the daily check runs,
  **then** the user sees a "Update available" notification
  with release notes link.
- **Given** the user clicks "Update now",
  **when** the update flow runs,
  **then** binaries are replaced, services restart, and
  health checks pass within 60s.
- **Given** the post-update health check fails,
  **when** the rollback fires,
  **then** the previous version is restored and the user
  sees a "Update failed and was rolled back" toast.
- **Given** the manifest's signature doesn't verify,
  **when** the updater checks,
  **then** the update is refused, an error is logged, and a
  metric `update_signature_failures_total` increments.
- **Given** a user opts into `beta`,
  **when** the next beta releases,
  **then** their server updates to it; if a stable later
  catches up, they remain on the latest beta.
- **Given** a release is marked `breaking: true`,
  **when** the user is offered the update,
  **then** the dialog requires explicit "I read the notes"
  before proceeding (defends against blind auto-update of
  breaking changes).
- **Given** the update fails to download (network drop),
  **when** retried later,
  **then** a partial-resume is attempted, then a clean
  retry; no silent failure.

## Test cases

| ID  | Type        | Setup | Action | Expected |
|-----|-------------|-------|--------|----------|
| T01 | unit        | manifest signature verify | flip a byte | rejected |
| T02 | unit        | version compare semver | 1.10.0 > 1.9.9 | true |
| T03 | integration | macOS Sparkle update | run | applies on quit |
| T04 | integration | Windows updater service | run | service restart, healthy |
| T05 | integration | Linux dpkg upgrade | apt upgrade | no service downtime > 5s |
| T06 | regression  | rollback on health-check fail | inject failure | reverts |
| T07 | unit        | breaking flag dialog | render | confirm step shown |
| T08 | regression  | manifest CDN stale (5 min) | force | accepts cached then re-checks |
| T09 | a11y        | update dialog (macOS) | screen reader | reads release notes summary |
| T10 | regression  | clock skew on signature ts | accept ±60s | passes |

## Edge cases

- **Update during a running scan.** The pipeline drains
  jobs; the scanner state survives via Epic 06's
  durable queue.
- **Partial failures.** If services restart but DB
  migrations failed, we revert the binaries; the DB stays
  on the last successfully-applied schema.
- **Version skew across services.** API + Streaming +
  Pipeline must be the same version (they share gRPC
  schemas). Updater enforces atomicity.
- **Manual rollback.** `maktaba rollback` works
  cross-platform via supervisor; documented in
  troubleshooting.
- **Disk-space check pre-update.** Refuse if free space
  < 2× artifact size.
- **Cosmic-ray bit-flips on disk.** SHA-256 check on
  download catches them.
- **Battery-on-laptop policy.** macOS/Windows skip
  updates if running on battery + low charge.
- **Cluster of servers.** Out of v1 — we update
  per-server; user manages roll-outs themselves.
- **Update channel switch.** Switching from `beta` back
  to `stable` does NOT auto-downgrade; user must wait
  for stable to surpass their installed version.
- **GitHub Releases as alternate channel.** We mirror
  artifacts to GitHub Releases; `releases.maktaba.app`
  is canonical.

## Files / packages

- `packaging/updater/manifest.go` (in cloud release
  pipeline; signs and publishes).
- `internal/updater/check.go` — common code in the
  server.
- `internal/updater/apply.go` — common pre/post hooks.
- Per platform: `desktop/macos/Maktaba/Sparkle/...`,
  `desktop/windows/MaktabaUpdater/`,
  `packaging/appimage/AppRun-update.sh`.

## Open questions

- **Delta updates.** Significant size win; defer to v2 to
  avoid Sparkle's complexity early.
- **Staged rollout.** Roll a release out to 5%, then 25%,
  then 100%. Defer; v1 is binary stable/beta switch.
