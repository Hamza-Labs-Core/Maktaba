# Maktaba Cloud migrations

Goose-managed migrations for the cloud Postgres instance. **Separate
database from the on-prem `api/` migrations** — do not point the goose
runner at the same DSN.

## Conventions

- Files are named `00<slot><seq>_<topic>.sql`. The leading four digits
  are the Epic slot (`0001`–`0010` were Epic 25's first pass; later slots are taken in order), the next four
  are the ordering within that slot.
- Every file must contain `-- +goose Up` and `-- +goose Down` markers so
  rollbacks work.
- DDL only — data backfills go in workers, not migrations, so they can
  be retried without resetting the schema head.

Slots are applied in number order, and a slot below the database's version is still applied
when it lands later (`Migrator.Up` allows missing versions): Epic 50's 0050 can merge after
Epic 51's 0051.

## Concurrency

`Migrator.Up` takes `pg_advisory_xact_lock(8472612)` in a transaction it keeps open for the
run. Two pods running `maktaba-cloud migrate up` simultaneously serialize on this lock — the
first runs, the second observes `current = head` and exits cleanly. A transaction lock, not a
session lock, so it holds through PgBouncer's transaction pool (Epic 50 story 50.18): the open
transaction keeps its server connection, and the lock ends with it.

The worker's jobs run only on the api node holding `pg_try_advisory_lock(8472613)` (Epic 50
story 50.17, `cloud/internal/db/leader.go`); `cloud_leaders` records the holder and its
heartbeat.

## Slot allocation

| Slot | Epic / story | Topic |
|---|---|---|
| 0001 | 25.1 bootstrap | system tables; `schema_versions` view |
| 0002 | 25.2 identity | users, sessions, refresh tokens, OAuth links |
| 0003 | 25.5 account | profile fields, email change tokens, deletes |
| 0004 | 25.6 server linking | servers, server_claims, server_health |
| 0005 | 25.11 bandwidth | bandwidth_samples, monthly_buckets |
| 0006 | 25.13 billing | plans, subscriptions, the webhook event ledger (renamed provider-neutral in 0016) |
| 0007 | 25.17 push | push_devices, push_dispatch_log |
| 0008 | 25.22 subdomains | subdomains, reserved_slugs |
| 0009 | 25.25 abuse | abuse_signals, blocklist |
| 0010 | 25.26 entitlement | entitlement_keys, entitlement_grants |
| 0011 | 30.1 relay analytics | relay_metrics_raw, relay_metrics_hourly |
| 0012 | 25.2/25.5/25.14/25.20 accounts & admin | users.role + suspension, session rotation, audit_log, invoices, webhook ordering |
| 0013 | 42.7 home links | home_links (linked home accounts) |
| 0014 | 16.4 licenses | licenses_issued, license_revocations (the signed revocation list) |
| 0015 | 16.7 usage data | telemetry_server_reports, telemetry_app_installs, telemetry_app_events, telemetry_error_groups/occurrences, telemetry_web_vitals (anonymous; docs/usage-data.md) |
| 0016 | billing provider-neutral | renames the provider-named billing columns and event ledger (`provider_customer_id`, `provider_subscription_id`, `provider_price_id`, `provider_invoice_id`, `provider_events`) and adds a `provider` column; the payment provider is TBD |
| 0050 | 50 cloud with no downtime | reserved infrastructure slugs (50.01: canary, r1…r99, direct, staging, plugins); staff accounts, their factors and sessions, the `users.role` reset and `audit_log.actor_kind`, passkey records and their WebAuthn challenges (50.33); the shared rate buckets and the leader locks' holders, `cloud_rate_buckets` and `cloud_leaders` (50.17); the relay pool's members and standby tunnels, `relays` and `relay_standby_tunnels` (50.16); Maktaba's country deny list for broadcasting, `broadcast_geo_deny` (50.23) |
| 0051 | 51.14 usage data components | telemetry_server_reports.bundle and .components (a home's bundle and enabled component ids) |
| 0053 | 53.16 problem reports | problem_reports (the reports homes chose to send, scrubbed; 90 days); symbol_tables (the symbols of a release's Apple dSYMs, Android R8 mappings and native libraries, for the reports' crashes; each table's kind and a library's whole build id) |
