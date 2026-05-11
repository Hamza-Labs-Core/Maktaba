# Maktaba Cloud migrations

Goose-managed migrations for the cloud Postgres instance. **Separate
database from the on-prem `api/` migrations** — do not point the goose
runner at the same DSN.

## Conventions

- Files are named `00<slot><seq>_<topic>.sql`. The leading four digits
  are the Epic slot (`0001`–`0010` reserved for Epic 25), the next four
  are the ordering within that slot.
- Every file must contain `-- +goose Up` and `-- +goose Down` markers so
  rollbacks work.
- DDL only — data backfills go in workers, not migrations, so they can
  be retried without resetting the schema head.

## Concurrency

`Migrator.Up` takes `pg_advisory_lock(8472612)`. Two pods running
`maktaba-cloud migrate up` simultaneously serialize on this lock — the
first runs, the second observes `current = head` and exits cleanly.

## Slot allocation

| Slot | Epic / story | Topic |
|---|---|---|
| 0001 | 25.1 bootstrap | system tables; `schema_versions` view |
| 0002 | 25.2 identity | users, sessions, refresh tokens, OAuth links |
| 0003 | 25.5 account | profile fields, email change tokens, deletes |
| 0004 | 25.6 server linking | servers, server_claims, server_health |
| 0005 | 25.11 bandwidth | bandwidth_samples, monthly_buckets |
| 0006 | 25.13 billing | plans, subscriptions, stripe_events |
| 0007 | 25.17 push | push_devices, push_dispatch_log |
| 0008 | 25.22 subdomains | subdomains, reserved_slugs |
| 0009 | 25.25 abuse | abuse_signals, blocklist |
| 0010 | 25.26 entitlement | entitlement_keys, entitlement_grants |
