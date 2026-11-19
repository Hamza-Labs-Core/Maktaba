# Story 16.4 — License key validation

Offline-tolerant license validation: keys are public-key signed; the
server checks the signature locally and refreshes against the license
server periodically.

**Anchors:** [`architecture.md` §11.5 (secrets)](../../architecture.md).

## AC

- License keys are Ed25519-signed JSON: `{license_id, tier, seats,
  expires_at, signature}`.
- Server bundles the license-server public key at build time.
- Validation: signature check + expiry check + seat-count check.
- Daily refresh against the license server; 30 d offline grace before
  features lock.
- Revocation list fetched daily; if a license id is on the list, lock
  features immediately with admin notification.
- License keys are never logged or returned by `/api/settings`; admin
  can paste in but the field is write-only after submission.

## TC

- Apply a valid signed license: server unlocks features within 5 s.
- Tamper with the license JSON: signature fails; features stay free.
- Disconnect from the license server for 35 d: features lock with a
  clear "Reconnect to validate license" admin banner.

## EC

- Clock manipulation (user sets system clock back): we trust the
  license server's `expires_at` over local time when reachable.
- License covers seats=4 but server has 5 users (owner's decision, 2026-11-19): admins
  are exempt, and only the most recently created accounts beyond the seat count go
  read-only. A read-only account can still sign in, watch, and save its own progress
  and history, but cannot upload, edit metadata or manage anything. Removing users or
  adding seats restores them at once. No new account can be added meanwhile. Admin
  warned, with the read-only accounts named.
- Revocation reaches the server while offline: we use the last-known
  list; on reconnect we re-evaluate.

## Implementation (2026-10-26)

**Status: done.** See [`docs/licensing.md`](../../../docs/licensing.md).

- One format: Ed25519-signed JSON `{license:{license_id, tier, seats, issued_at,
  expires_at, features?, server_id?, holder?}, signature}`, issued by the cloud
  (plan licenses) or the operator CLI (offline licenses), verified by the home;
  a golden vector pins it in both modules.
- The key: the public half is embedded at build time
  (`tools/release/license-key.sh` in every release workflow, from the GitHub
  variable `MAKTABA_LICENSE_PUBKEY`; a release without it fails); the private half
  is in the operator's Keychain and is the cloud's entitlement key.
- Validation: signature, tier, seats, expiry (+72 h skew grace), revocation, and
  for a plan license the home's own cloud id.
- Refresh: the revocation list daily; a cloud-claimed home's plan license every
  minute through its relay agent. Plan licenses last 31 days: the 30-day offline
  grace; then Settings says _Reconnect to validate the license_.
- Revocation: list or cloud answer → locked at once, admin message, audit row;
  the last-known list holds while offline and is re-evaluated on reconnect.
- Clock: a high-water mark (persisted, slot 0155) so a clock set back cannot
  revive a license; the license server's `Date` wins when reachable.
- Write-only: no response carries the signature or the signed JSON; apply/remove
  are admin-only with role tests.
- Over-seat (2026-11-19, the owner's decision): see "Seats (as built)" below. New
  accounts are refused, as before.

## Seats (as built, 2026-11-19)

The owner decided that when a home has more users than its license's seats, admins are
exempt and only the most recently created accounts beyond the seat count go read-only.
A read-only account can watch and save its own progress and history, but cannot
upload, edit metadata or manage anything. Removing users or adding seats restores them
at once. This replaces "they keep full access, by decision".

- **Which accounts** (`subscriptions.SeatGuard`, `api/internal/subscriptions/seats.go`):
  - with N real accounts and S seats, the read-only ones are the N − S newest non-admin
    accounts by `created_at`. The admins count toward N but are never chosen, and the
    disabled sentinel row is no seat (as `CountUsers`);
  - an unlimited plan has none;
  - nothing is cached: each write asks, so a removal or more seats count at the next
    request.
- **The gate** (`SeatGuard.Gate`, in front of the API's router in `main.go`):
  - **Reads are never refused.** Every GET, HEAD and OPTIONS goes through.
  - **Writes that stay open** to a read-only account (`AllowedWhileSeatReadOnly`):
    - signing in and out, its own password and sessions;
    - playback (`/api/stream/sessions*`, `/api/watch/*`, live TV's
      `/api/channels/{id}/play`, casting, play queues, a TV remote's session, a video
      kept for offline watching);
    - its own progress and history (`/api/me/watched`, `/api/me/history/*`);
    - searching, notifications, and the parental unlock requests a child needs to keep
      watching.
  - **Everything else is refused** with `403 seat-read-only` (in the problem
    registry, English and Arabic), saying to ask the admin: uploads, metadata edits,
    collections, the account's own playlists and favourites, settings, and every
    management route.
  - **What it never refuses.** An admin, a request with no account, or the admin token
    always goes through. A database error lets the request through, logged.
- **The admin's view.** `GET /api/admin/license` lists `read_only_users` (newest
  first), and its message names them. Settings → Subscription shows them under the
  seats.
- **The account's view.** `GET /api/entitlements` says `read_only: true` to a read-only
  account, and Settings → Subscription tells it so.
- **Tests.**
  - `seats_test.go` (SQLite): which accounts, admins exempt, a removal and more seats
    restoring, the gate's allowed and refused writes, a database error.
  - `seats_pg_test.go`: the statements on Postgres.
  - `license_test.go`: the admin's view and the entitlements flag.
  - `SubscriptionSettings.test.tsx` SUB-13 to SUB-13c.
