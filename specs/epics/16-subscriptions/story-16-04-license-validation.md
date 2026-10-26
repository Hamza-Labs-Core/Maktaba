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
- License covers seats=4 but server has 5 users: existing 5 keep working
  read-only; new logins refused. Admin warned.
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
- Over-seat: existing users keep working; new users are refused; the admin sees a
  warning. (The AC's "read-only" for the extra users is not built: they keep full
  access, which is what the owner chose.)
