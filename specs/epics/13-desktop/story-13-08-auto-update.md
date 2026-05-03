# Story 13.8 — Auto-update

Tauri's built-in updater fetches signed delta updates from a server-side
manifest; user is prompted to install on next quit.

**Anchors:** [`architecture.md` §6.4](../../architecture.md), §12.4.

## AC

- Update channel: `stable` (default), `beta` (opt-in via Settings →
  Advanced).
- Update check on launch + every 24 h.
- Updates are signed with an Ed25519 key; the public key is bundled at
  build time.
- "Update available" toast with "Install on quit" or "Install now"
  (restarts).
- Background download with resume; once downloaded, applied at restart.
- Version skew with the server is surfaced in Settings → About.

## TC

- Publish a new build to the manifest: a running client picks it up
  within 24 h; user installs on quit.
- Updater fails signature check: refuses to install; logs warning.
- User on `beta` rolls back to `stable`: next available `stable` is
  installed (no downgrade unless user opts in to "Allow downgrades" in
  Advanced).

## EC

- Update server unreachable: silently retry next interval; do not nag.
- Disk full mid-download: pause; surface error.
- Update introduces a breaking schema change: we surface "Server is
  older than client — update server first" and refuse to migrate.
