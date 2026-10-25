# Upgrade and rollback (Story 22.6)

Two paths supported: in-place container upgrade, and packaged
upgrade via Homebrew / dpkg / rpm. Both follow the same invariants:

1. Forward migrations run **before** the new binary starts serving.
2. The new binary is **schema-rev–pinned** at build time. If the DB
   schema_rev does not match the binary's expected SCHEMA_REV, the
   binary refuses to start.
3. Backward compatibility: every release is guaranteed compatible
   with `schema_rev - 1` for the duration of one minor release.
4. Rollback is **always** to the immediately preceding release. We
   do not support `goose down` across more than one minor version.

## Container upgrade

```sh
# 1. Stage: pull the new images, do not switch traffic yet.
docker compose pull api streaming pipeline web

# 2. Migrate forward.
docker compose run --rm api maktaba-api migrate up

# 3. Switch traffic. Compose performs a rolling restart of api +
#    streaming; pipeline workers finish their current claim, then
#    take a SIGTERM and re-enqueue.
docker compose up -d api streaming pipeline web
```

## Rollback

```sh
# 1. Switch traffic back to the previous image (compose-managed).
MAKTABA_IMAGE_TAG=v0.1.0 docker compose up -d api streaming pipeline web

# 2. If the new release had a destructive migration, roll the schema
#    back. `migrate down` reverts the single most-recent migration;
#    `migrate down-to <version>` reverts down to a specific schema
#    version. This is the only step that mutates Postgres.
#
#    Rollback is destructive, so it is refused unless you explicitly
#    opt in: the binary aborts when MAKTABA_DISABLE_DOWN is truthy
#    (recommended as a standing production env var). Clear it only for
#    the duration of the rollback.
MAKTABA_DISABLE_DOWN= docker compose run --rm -e MAKTABA_DISABLE_DOWN= \
  api maktaba-api migrate down-to 53
```

The release manifest (`release-manifest.json`) records the
`schema_rev` for every release so the rollback target is unambiguous.
Only roll back within one minor version; for anything wider, restore
from a database backup (see invariant 4 above).

> NOTE (Story 22.6, deferred): the broader upgrade runtime —
> `tools/upgrade.sh` / `rollback.sh` / `version-jump-guard.sh`,
> `/admin/drain`, pre-upgrade `migrate doctor` simulation, and the
> `--accept-long-migration` ack — is **not yet implemented**. This
> release wires the concrete, tested rollback path (`migrate down` /
> `down-to`, guarded by `MAKTABA_DISABLE_DOWN`); the orchestration
> wrappers and drain endpoint remain tracked in HLB-360.

## Packaged upgrade (Homebrew / dpkg / rpm)

```sh
brew upgrade maktaba           # macOS
sudo apt upgrade maktaba       # Debian / Ubuntu
sudo dnf upgrade maktaba       # Fedora / RHEL
```

The post-install hook of each package runs forward migrations and
restarts the launchd / systemd units in the same order:

1. `maktaba-api` (carries the migrator)
2. `maktaba-streaming`
3. `maktaba-pipeline`

## Automatic updates: the database only moves forward (Epic 35 story 35.5)

The updater never runs a down migration (`MAKTABA_DISABLE_DOWN` stays the default above).
Migrations are additive, so a release rolled back after its migrations runs on the newer
schema, or its start puts back the dump taken before them (install-kinds.md, "The
database"). `make compat-prev` checks every build against the previous stable release's
repository tests on the new schema; a migration that breaks it must say "Breaking for the
previous release" in `shared/db/migrations/MANIFEST.md` (the release is then held for an
admin).

**Backups.** Before every update that migrates, the home dumps its database into
`<data_dir>/backups/pre-<from>-to-<to>.dump` (checked with `pg_restore --list`), and keeps
`rules.updates.backups_keep` (3) of each kind: those, the bundled database's daily
`maktaba-<time>.dump`, the database a failed update left (`failed-*`) and the one a restore
replaced (`replaced-*`).

**Putting a backup back** (story 35.5 AC8): Admin → Updates → *Backups of the library
database* → Restore, or

```sh
maktaba-server restore                                  # the backups and the releases kept
maktaba-server restore --backup pre-0.3.5-to-0.3.6.dump # what restoring it would do
maktaba-server restore --backup pre-0.3.5-to-0.3.6.dump --confirm
```

The home restarts on the kept release whose schema is the backup's (the dump's newest
applied migration) and that release puts the backup back before anything else runs; the
database it replaces is set aside first. Every change made since the backup was taken is
lost. With no release of that schema kept, it refuses and names the release to install
first. It refuses while an update runs. A machine of a home has no backups of its own: it
copies the restored database anew.
