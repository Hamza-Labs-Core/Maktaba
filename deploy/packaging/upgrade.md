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
#    back to the previous slot. `goose down` is idempotent; this is
#    the only step that mutates Postgres.
docker compose run --rm api maktaba-api migrate down --to=0053
```

The release manifest (`release-manifest.json`) records the
`schema_rev` for every release so the rollback target is unambiguous.

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
