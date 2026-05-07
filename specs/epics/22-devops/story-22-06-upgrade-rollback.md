# Story 22.6 — Upgrade and rollback

Self-hosters must be able to upgrade and roll back without losing data.

## Acceptance criteria

- AC1. Upgrade path on the canonical compose deployment is `git pull;
  docker compose pull; docker compose up -d`; the platform supports
  rolling each service independently when its API surface is
  back-compatible.
- AC2. Rollback path within one minor version is documented and
  tested: pull the previous tag, `docker compose up -d`. Migrations
  are forward-compatible across one minor (the new minor reads old
  rows; the old minor reads new rows that don't use new columns).
- AC3. A pre-upgrade `maktaba-api migrate doctor` runs the planned
  migrations against a pg_dump in a temp DB and prints a duration
  estimate before touching production data.
- AC4. Long-running migrations (> 30 s) require an explicit operator
  ack via `--accept-long-migration`.

## Test cases

- TC1. Forward+back: upgrade a seeded fixture from v1.0 → v1.1 →
  rollback to v1.0; data is intact, the app boots, no
  `migrate-down` was needed.
- TC2. Doctor: with a synthetic 1 M row migration, doctor reports
  the duration; without ack, upgrade refuses.
- TC3. Rolling: bump streaming alone to a new patch version while
  api and pipeline stay; clients connected during the rolling
  restart drop fewer than 1 % of in-flight streams.

## Edge cases

- EC1. Two-minor jump (v1.0 → v1.2) — supported only via v1.0 → v1.1
  → v1.2; documented and tested. A direct jump fails fast with a
  clear error.
- EC2. Custom config path — upgrades preserve config; a
  configuration-schema bump is handled by a `config migrate` step in
  the doctor.
- EC3. Postgres major upgrade — out of scope for Maktaba's upgrade
  path; documented separately as a Postgres operator task.
