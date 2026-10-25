# Upgrade and rollback

Every install that updates itself — the `.deb`/`.rpm`, the Homebrew formula,
`setup --self-managed`, the Linux app and `install.sh --app`, the Docker image, the desktop apps,
a full node — installs each release by itself and goes back by itself when one does not hold
([docs/updates.md](../../docs/updates.md); the layout and the launcher:
[README](README.md#the-layout-and-the-launcher-epic-35)). This page is for doing it by hand, and
for the multi-service stack built from source (`deploy/compose`), which does not update itself.

The rules every path follows:

1. Forward migrations run **before** a new release serves: its start's database step dumps the
   database first and puts the dump back if a migration fails.
2. A release refuses a database whose schema it does not fit (the schema guard), and a machine
   whose code does not fit the home's database pauses until its update lands.
3. Migrations are additive: a release keeps working on the next release's schema (`make
   compat-prev` checks it). A migration that breaks the previous release says so in
   `shared/db/migrations/MANIFEST.md`, and such a release waits for an admin's approval.
4. The updater never runs a down migration. Going back further than the previous release is a
   [restore of a backup](#automatic-updates-the-database-only-moves-forward-epic-35-story-355).

## Rolling back by hand

- **A machine of the cluster**: Admin → Updates → the run → **Roll back** on its row.
- **Any self-managed install**, on that machine: `maktaba-server update --rollback` makes the
  previous release current again (the layout's `previous`; the Linux runtime copy's previous
  runtime; a plain binary's `<binary>.bak`), then restart the service. It does not lower the
  watermark: an automatic update will not install the release you left until you unblock it
  (Admin → Updates → **Try it again**), and the next release moves past it.
- **The database**: the release before runs on the newer schema (rule 3). When it cannot, restore
  the backup taken before the update (below).
- **A desktop app**: install the release you want from its GitHub release by hand.

## Container upgrade

The Docker install (one container, `ghcr.io/hamza-labs-core/maktaba-server`, [cloud/installers/docker](../../cloud/installers/docker/docker-compose.yml)) needs none of this: it updates the runtime on its volume by itself, as a headless install does ([docs/updates.md](../../docs/updates.md)). The steps below are for the multi-service stack built from source (`deploy/compose`).

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
from a database backup (rule 4 above).

> NOTE (Story 22.6, deferred): the broader upgrade runtime —
> `tools/upgrade.sh` / `rollback.sh` / `version-jump-guard.sh`,
> `/admin/drain`, pre-upgrade `migrate doctor` simulation, and the
> `--accept-long-migration` ack — is **not yet implemented**. This
> release wires the concrete, tested rollback path (`migrate down` /
> `down-to`, guarded by `MAKTABA_DISABLE_DOWN`); the orchestration
> wrappers and drain endpoint remain tracked in HLB-360.

## Packaged upgrade (Homebrew / dpkg / rpm)

A `.deb`/`.rpm` and the Homebrew formula update themselves: the server installs each release in
its quiet window (Epic 35; the package owns the launcher, the updater the releases). The package
manager brings a new launcher, from the apt/dnf repositories or the tap, which follow each stable
release:

```sh
brew upgrade maktaba                               # macOS
sudo apt install --only-upgrade maktaba-server     # Debian / Ubuntu
sudo dnf upgrade maktaba-server                    # Fedora / RHEL
```

The package's release becomes current only when it is newer than what the updater installed
(the `.deb`/`.rpm` postinstall, the formula's service: `setup --self-managed --identity package`);
its start then migrates the database in its database step (a dump first, put back if a
migration fails), as an automatic update does. Neither the packages nor the formula migrate
anything themselves.

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
