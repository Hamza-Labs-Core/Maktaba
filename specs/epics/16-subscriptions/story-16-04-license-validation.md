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
