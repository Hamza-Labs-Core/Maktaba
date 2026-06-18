# Story 28.4 — Desktop auto-update (Tauri)

> Epic 28 · Auto-Update · Phase 4 (desktop)

## Description

The Tauri desktop app uses Tauri 2's **built-in updater plugin** pointed
at GitHub Releases. No custom update code in the app — configuration plus
CI signing.

- **Endpoint.** `tauri.conf.json → plugins.updater.endpoints` points at
  the GitHub Release's `latest.json` (the updater's "dynamic" feed):
  `https://github.com/Hamza-Labs-Core/Maktaba/releases/latest/download/latest.json`.
  Tauri compares the app's own version (28.1, from `tauri.conf.json`)
  against `latest.json`'s `version`.
- **`latest.json`.** Produced by the desktop release job for each tag and
  uploaded as a Release asset. Schema (Tauri v2):
  ```json
  {
    "version": "1.4.2",
    "notes": "…release notes…",
    "pub_date": "2026-04-22T00:00:00Z",
    "platforms": {
      "darwin-aarch64": { "signature": "…", "url": "https://github.com/.../Maktaba_1.4.2_aarch64.app.tar.gz" },
      "darwin-x86_64":  { "signature": "…", "url": "…" },
      "windows-x86_64": { "signature": "…", "url": "https://github.com/.../Maktaba_1.4.2_x64-setup.exe" },
      "linux-x86_64":   { "signature": "…", "url": "https://github.com/.../maktaba_1.4.2_amd64.AppImage" }
    }
  }
  ```
- **Behaviour.** On launch (and on a manual "Check for updates"), the app
  checks the feed; if newer, it shows a notification, downloads in the
  background, and applies on the next relaunch. The updater verifies the
  per-platform `signature` against the bundled **public key** before
  installing.
- **Signing.** Tauri's updater requires a key pair
  (`tauri signer generate`). The **public key** goes in
  `tauri.conf.json → plugins.updater.pubkey`; the **private key** +
  password become CI secrets
  (`TAURI_SIGNING_PRIVATE_KEY`, `TAURI_SIGNING_PRIVATE_KEY_PASSWORD`)
  that `tauri-action` reads to sign each bundle and emit the
  `latest.json` signatures.

## Acceptance criteria

- **Given** the desktop app `v1.4.1` and a Release `v1.4.2` with a valid
  `latest.json`,
  **when** the app checks for updates,
  **then** it reports an update available, downloads the signed bundle,
  and applies it on relaunch.

- **Given** a `latest.json` whose signature does not verify against the
  bundled pubkey,
  **when** the app checks,
  **then** it refuses to install and surfaces an error (no silent
  install of unsigned bytes).

- **Given** the app is already on `latest.json`'s version,
  **when** it checks,
  **then** it reports "up to date" and downloads nothing.

- **Given** CI builds a tagged desktop release with the signing secrets
  present,
  **when** the release job runs,
  **then** signed bundles **and** a `latest.json` are attached to the
  GitHub Release.

- **Given** the signing secrets are absent (a fork),
  **when** the release job runs,
  **then** it still produces unsigned installers (current behaviour) and
  skips `latest.json` generation, logging a notice — the build stays
  green.

## Test cases

| ID  | Type        | Setup | Action | Expected |
|-----|-------------|-------|--------|----------|
| T01 | config      | `tauri.conf.json` | parse | `plugins.updater.active=true`, endpoint = GitHub `latest.json` |
| T02 | config      | `tauri.conf.json` | inspect | `version` set from tag in CI (not hardcoded `0.1.0`) |
| T03 | manual/QA   | signed release | install update | applies on relaunch |
| T04 | manual/QA   | tampered bundle | install | rejected by signature check |
| T05 | ci          | secrets present | release job | bundles signed; `latest.json` present |
| T06 | ci          | secrets absent | release job | unsigned build; green; notice logged |

## Edge cases

- **First install (no prior version).** Nothing to update; updater is
  inert until a newer feed appears.
- **Downgrade.** Tauri only applies strictly-newer versions; a stale
  feed never downgrades.
- **Offline.** Check fails gracefully; the app keeps running on the
  installed version.
- **Key rotation.** Document: ship one release signed by both old and new
  keys before retiring the old pubkey.
- **macOS Gatekeeper / notarisation.** Independent of the updater
  signature; notarisation is still the deferred code-signing TODO in
  `desktop-release.yml`. The updater signature is mandatory regardless.
- **Linux AppImage.** Tauri updates the AppImage in place; the user must
  have write access to it (documented).

## Files / packages

- `apps/desktop/src-tauri/tauri.conf.json` — updater endpoint, pubkey,
  windows install mode.
- `.github/workflows/desktop-release.yml` — set `version` from tag; pass
  `TAURI_SIGNING_*` secrets to `tauri-action`; `latest.json` is emitted
  by `tauri-action` when signing is configured.
- `docs/auto-update.md` — `tauri signer generate` instructions + secret
  setup.

## Open questions

- **Auto-check cadence in the app.** Default to on-launch + manual; a
  periodic in-session check is a nice-to-have, deferred.
