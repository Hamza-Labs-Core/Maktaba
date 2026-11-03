# Story 50.09 — Backups and restore

> Epic 50 · Phase 1 · Depends on: 50.07 · Needed by: 50.18

## Why

The only backup today is a line in `deploy/cloud-relay/README.md`: `pg_dump > backup.sql`.
Nothing runs it, nothing leaves the machine, and no restore has ever been tried. Replication
(50.07) protects against a dead machine, not against a bad migration, a mistaken `DELETE`, or
losing the whole data centre zone. The cloud database holds accounts, home links and billing
state, and the cloud's secrets sign every home's entitlement.

## Description

### 1. Database: pgBackRest

| Setting | Value |
|---|---|
| repo1 | Hetzner Object Storage, bucket `maktaba-prod-pg` in **HEL1** (not FSN1/NBG1, where the nodes are) |
| repo2 | Cloudflare R2 bucket, if the owner says yes (README §13 question 2) |
| Encryption | `repo-cipher-type=aes-256-cbc`, a passphrase per repo, encrypted before upload |
| WAL archive | `archive_command = pgbackrest --stanza=cloud archive-push %p`; `archive_timeout = 60` (RPO ≤ 60 s if both nodes are lost) |
| Full backup | weekly, Sunday 03:00 UTC |
| Differential | daily, 03:00 UTC |
| Retention | 30 days of point-in-time restore (`repo-retention-full-type=time`, `=30`) |
| Where it runs | on the current Patroni leader (a systemd timer that checks the role first), so it moves with failover |

### 2. Machines: Hetzner backups

Hetzner's server backups (20 % of the server price, 7 daily slots) are on for A and B. They are
for rebuilding a node's OS quickly, not for the database (a disk image of a running Postgres is
not a consistent backup; pgBackRest is).

### 3. Secrets kept apart

These are not in any backup bucket in plain form. They are in the owner's password manager,
and an encrypted copy (age, the owner's key) sits in repo1 under `secrets/`:

| Secret | What losing it means |
|---|---|
| `MAKTABA_CLOUD_TOKEN_SECRET` | every access token and resume ticket stops working; users sign in again |
| Entitlement signing key (`secrets/entitlement.key`) | homes can't verify new entitlements; after 7 days cloud features stop on every home until each home trusts a new key. **The worst one to lose.** |
| Stripe secret and webhook secret | billing stops until new ones are made in Stripe |
| OAuth client secrets, APNs key, FCM service account | sign-in with Google/Apple and push stop until replaced |
| pgBackRest repo passphrases | the backups can't be read |
| etcd CA key, Hetzner and Cloudflare tokens | can be made again; listed so a rebuild knows them |

### 4. Weekly restore drill

Every Monday 04:00 UTC on the witness W:

1. `pgbackrest restore` the latest backup from repo1 into a throwaway Postgres in a scratch
   directory, to a point 5 minutes ago (PITR).
2. Run checks: it starts; `cloud_users`, `cloud_servers` and `cloud_subscriptions` row counts
   are within 1 % of the live replica's; the newest `cloud_audit` row is less than 10 minutes
   older than the restore target; `maktaba-cloud migrate status` says no migration is missing.
3. Push `backup_restore_drill_ok` (0/1), its duration and the restored size to Prometheus
   (50.10). A failed or missing drill alerts.
4. Delete the scratch data.

### 5. Disaster runbook (the whole zone or Hetzner is gone)

In `docs/runbooks/cloud-disaster-restore.md` (50.12 owns the runbook set): rebuild two nodes in
another location or provider with `deploy/cloud-ha/setup.sh`, restore from repo1 (or repo2 if
Hetzner is down), put the secrets back from the password manager, point the DNS names at the
new address through Cloudflare. Target **RTO 4 h**, **RPO ≤ 60 s** (README §4.2).

## Acceptance criteria

- **Given** the cluster is running,
  **when** 60 s pass with writes,
  **then** the WAL for them is in repo1 (and repo2 if set).
- **Given** a full backup and WAL in repo1,
  **when** the drill restores to 5 minutes ago,
  **then** the restored database starts and the row-count and freshness checks pass.
- **Given** the leader fails over to B,
  **when** the next backup is due,
  **then** it runs on B and the WAL archive continues with no gap.
- **Given** a backup is 26 h old or a WAL push has failed for 5 minutes,
  **when** Prometheus evaluates,
  **then** `backup_stale` or `wal_archive_failing` fires.
- **Given** someone reads the bucket without the passphrase,
  **when** they download a backup file,
  **then** they get ciphertext.
- **Given** a fresh machine, the runbook, the password-manager secrets and repo1,
  **when** the owner follows the runbook,
  **then** the cloud serves again within 4 h (timed once before launch, README §11).

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit (`bats`) | backup timer script | run on a replica | does nothing, exits 0 |
| T02 | unit | drill checks script with a fixture database | counts off by 5 % | fails with the table named |
| T03 | rehearsal (CI) | 50.06 `restore-pitr`: A/B/W containers, MinIO as the bucket | write rows, note time T, write more, restore to T | exactly the rows up to T |
| T04 | rehearsal (CI) | `restore-pitr` variant | kill A mid-archive, fail over, restore latest | no WAL gap; RPO ≤ 60 s |
| T05 | rehearsal (CI) | bucket without passphrase | `pgbackrest info` with wrong key | refuses |

## Edge cases

- **Object Storage down.** WAL piles up on the leader's disk; the alert fires; Postgres keeps
  running. Disk alert at 80 % gives hours of room.
- **A bad migration.** Restore to just before it into a scratch database, copy the lost data
  back by hand (runbook). Never restore over production while it runs.
- **GDPR deletion.** A deleted account is gone from live data at once but stays in backups up
  to 30 days. The privacy page (Epic 30) says so.

## Build plan

### Files to touch

| File | Change |
|---|---|
| `deploy/cloud-ha/pgbackrest/pgbackrest.conf` (new) | §1. |
| `deploy/cloud-ha/patroni/patroni.yml` | `archive_command`, `archive_timeout`, restore command for replicas. |
| `deploy/cloud-ha/systemd/pg-backup.{service,timer}` (new) | Full/diff, leader only. |
| `deploy/cloud-ha/bin/restore-drill` (new) and `systemd/restore-drill.{service,timer}` (new) | §4, on W. |
| `deploy/cloud-ha/setup.sh` | Install pgBackRest, create the stanza. |
| `docs/runbooks/cloud-disaster-restore.md` (new) | §5. |
| `deploy/cloud-relay/README.md` | Replace the `pg_dump` line with a pointer to this setup. |

### Tests

- `bats` for the scripts in `relay.yml`.
- **CI only:** `restore-pitr` (MinIO stands in for Object Storage; no live bucket is touched).

### Rollout

1. The owner creates the bucket and keys (README §12) and, if chosen, the R2 bucket.
2. `setup.sh` creates the stanza and takes the first full backup.
3. The first drill runs by hand on W; then the timer takes over.
4. The timed full restore before launch (README §11).

### Rollback

Turning backups off is never the fix. If pgBackRest breaks archiving, set
`archive_command = /bin/true` only with the owner's OK and a page open, fix, then take a new
full backup at once.

### Dependencies

50.07 (Patroni, the witness). Needed by 50.18.
