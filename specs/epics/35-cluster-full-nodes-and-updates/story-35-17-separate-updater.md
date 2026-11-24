# Story 35.17 — A separate updater, and additive-only migrations

## Description

*As the owner, I want every update judged and undone by a small program that the update does not
replace, and every migration to run while the release before keeps serving, so that a bad release
can never take the library down or decide its own way back.*

Today the release being installed judges itself:
- It runs its own migrations after the switch, with the library stopped.
- Its own watch decides whether it is healthy.
- Its own code rolls it back.

So a fix to the update path protects only updates *to* a release that has it. Every update that
migrates is an outage. On the desktop apps even the boot-loop guard ships inside the bundle being
replaced.

A new `maktaba-updater` takes over verify, stage, pre-flight, migrations, switch, watch, commit and
rollback.
- It lives outside the app bundle and outside `bin/releases/`.
- It changes rarely and has its own protocol.
- It talks to each release through a fixed contract: what to install, what it did, a health
  document and the migrate command.
- The release keeps the Updates page and the policy.

Migrations become additive only. They run before the switch, while the release before serves. A
rollback is a binary switch, and anything that removes schema waits for a post-commit set.

A bridge release with no migrations brings the homes in the field, the owner's first, onto the
updater.

Design: [design-separate-updater.md](design-separate-updater.md), which covers the shape, the
contract, the self-update, the bridge, the risks and the stories it splits into. Research:
[`docs/research/update-process.md`](../../../docs/research/update-process.md) (#1601) §6, items 6 and 7.

## Acceptance criteria

- **AC1** No release judges itself. On every install kind the updater, not the release, decides
  commit or rollback, and only the updater pushes the launcher's deadline.
- **AC2** The judge survives the update. The launcher and the updater live outside the app bundle
  and outside `bin/releases/`. A new bundle that cannot start its own processes is switched back.
- **AC3** A fixed contract: `wanted.json`, `status.json`, `maktaba-server health --contract 1` and
  `maktaba-api migrate` with fixed exit codes. A release names the updater protocol it needs, and
  an updater refuses one it does not speak and says so.
- **AC4** The updater updates itself last and rarely. A new updater installs beside the old one,
  qualifies with a self-test, and is switched to by the launcher with the old one kept. It never
  ships in a release that carries a migration.
- **AC5** Migrations are additive only in `shared/db/migrations/` from the first slot after the
  bridge, enforced by migration-lint and `make compat-prev`. Removals go to
  `shared/db/migrations/post/` and run after every machine committed.
- **AC6** Migrations run before the switch, with the release before serving. A failed migration
  switches nothing and stops nothing.
- **AC7** A rollback after additive migrations is a binary switch. Nothing puts a backup back by
  itself (#1605's rule holds).
- **AC8** The bridge release carries no migration. It installs the updater and the launcher in
  their new places, and every later release requires it (`min_upgrade_from`). The in-server path
  stays as a fallback for two releases.

## Dependencies

- #1598, #1605, #1607, #1608 and #1609.
- The signed release feed.
