# Story 54.47 — Updates of Maktaba plugins through the store

> Epic 54 · Phase 5 · Depends on: 54.45, 54.46, 54.17 · Needed by: 54.48 · Not built

## Why

Today a Maktaba plugin's version is the server's and a fix to one plugin needs a server release. Once each is a store package, it has its own version, and an admin updates or rolls back one plugin without touching the rest.

## Description

- **Versions.** Each Maktaba plugin has its own semantic version and a `maktaba` range naming the server versions it runs on. The lock-step rule of README §15.6 (`maktaba = "=<version>"`) ends.
- **Compatibility.** A server update checks every installed Maktaba plugin against the new server before it starts, as it does third-party plugins today (update-rehearsal scenario 15). A plugin with no compatible version installed is updated in the same staging when a compatible package is available (from the release's files or the store), else the update is refused before anything changes, naming the plugin. A Maktaba plugin is never left `incompatible` after an update the admin did not choose.
- **Auto-update.** On by default for Maktaba plugins, with the admin's switch per plugin (54.17 §2).
- **Roll back.** The previous package is kept as for third-party plugins (54.07); a rollback of the server rolls its plugins back to the versions that ran with it.
- **Withdrawal.** The store's kill switch applies to a Maktaba plugin as to any other (54.17 §3).

## Tests

- update-rehearsal: a server update across a plugin version boundary, and its rollback, with jobs in the queue: none lost or rerun.
- An update that would leave a Maktaba plugin with no compatible version is refused before the swap.
- A plugin updated alone keeps parity (54.26) on its stages.
