# Runbook: restoring the plugin store's data

The plugin store (Epic 54, story 54.15) keeps its data in three Cloudflare D1 databases,
`store-accounts`, `store-catalog` and `store-installs`, and its files in two R2 buckets,
`maktaba-plugins` (public, content-addressed: packages, icons, the signed lists) and
`maktaba-plugins-private` (submissions, check reports, exports). The signing keys are not in
any of them: the online key is a Worker secret with an offline copy held by the owner, and the
root keys never leave the owner's offline media.

Homes keep working while the store is down: they install from the CDN, and their copies of
the catalog and the revocation list stay valid (the list expires after 48 hours and then only
warns). So a restore is never a rush that justifies skipping a step.

## 1. Within the last 30 days: D1 Time Travel (preferred)

For each database to restore, pick the minute before the damage, then:

```sh
cd store
pnpm exec wrangler d1 time-travel info store-catalog --timestamp=2026-11-06T10:41:00Z
pnpm exec wrangler d1 time-travel restore store-catalog --timestamp=2026-11-06T10:41:00Z
```

Restore only the database that was damaged. The signed lists' next versions are taken above
both the database's counters and the copies on the CDN, so a restored database never
publishes a list homes refuse as a rollback. Afterwards, regenerate the signed lists so the CDN
matches the database: a store admin calls `POST /v1/admin/resign` (or waits for the six-hourly
Cron Trigger, which re-signs the catalog and the revocation list).

**Never restore the catalog to a time before a revocation without adding that revocation
again**: the next list would drop it. A home that already applied a `disable` keeps the plugin
held (lifting is the admin's), but a home that had not fetched it yet would never see it. List
`revocations` before and after and add any missing one through the admin API.

## 2. Older than 30 days: the nightly export

Each night a Cron Trigger writes `maktaba-plugins-private/exports/<db>/<date>.sql.gz.enc`,
encrypted to the owner's RSA key (`EXPORT_PUBLIC_KEY`). Exports are kept 90 days (an R2
lifecycle rule).

1. Download the file (Cloudflare dashboard, R2, the private bucket; or
   `pnpm exec wrangler r2 object get maktaba-plugins-private/exports/store-catalog/2026-10-01.sql.gz.enc --file catalog.enc`).
2. On the owner's machine, decrypt it with the private key kept outside Cloudflare:
   `pnpm exec tsx scripts/decrypt-export.ts export-private.pem catalog.enc > catalog.sql`.
3. Make a new, empty D1 database, apply the migrations
   (`pnpm exec wrangler d1 migrations apply <new db> --remote`), then load the rows:
   `pnpm exec wrangler d1 execute <new db> --remote --file catalog.sql`.
4. Check it (counts of `plugins`, `versions`, `developer_keys`, `revocations`), then point the
   binding in `store/wrangler.jsonc` at the new database id and deploy with a `store-v*` tag.
5. Sessions and passkey challenges are not exported: everyone signs in again.

## 3. R2

Packages are content-addressed and never overwritten, so a lost object is copied back from any
copy with the same sha256. The signed lists are regenerated (step 1's last paragraph). Object
versioning on the public bucket keeps earlier versions of the lists.

## The drill

CI runs a restore drill on every change to the store and weekly (`store-weekly.yml`): an
export is decrypted and restored into Miniflare's D1 and the integration tests run against it
(`store/test/workers/restore.test.ts`). A live restore is only this runbook.
