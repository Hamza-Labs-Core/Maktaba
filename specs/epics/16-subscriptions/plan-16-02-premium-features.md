# Implementation Plan — Story 16.2 Premium features (remote access, multi-user, cloud backup)

> Companion to [story-16-02-premium-features.md](story-16-02-premium-features.md).
> The story states *what* and *why*; this plan states *how*.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Tier flag declarations | `api/internal/flags/declarations.go` (Story 16.6/16.8 own the resolver). |
| Quota enforcement | Per-feature: relay quota in `relay_usage` from [Story 15.2](../15-discovery/story-15-02-cloud-relay.md); seat-count enforcement in `api/internal/auth/seats.go`; backup gating in `api/internal/backup/`. |
| Backup engine | `api/internal/backup/engine.go` — produces `.maktaba-backup` archives; scheduled by Epic 22's scheduler with cadence per tier. |
| Analytics dashboards | `web/src/features/analytics/` — basic vs. advanced gated by `analytics_advanced` flag. |
| Downgrade behavior | 30-day grace period implemented in the flag resolver: tier-downgrade keeps premium features `read_only` for 30 days. |
| Out of scope | License validation ([Story 16.4](story-16-04-license-validation.md)); flag resolution mechanics ([Story 16.6](story-16-06-feature-flags.md), [Story 16.8](story-16-08-feature-flags-api.md)). |

## 1. Tier matrix (canonical)

| Feature | `free` | `home` | `pro` |
|---|---|---|---|
| Local library, streaming, search, transcribe | ✓ | ✓ | ✓ |
| Cloud relay quota | — | 200 GB/mo | 1 TB/mo |
| User seats | 1 | 4 | unlimited |
| Backup cadence | — | daily | hourly |
| Analytics | — | basic (30 d retention) | advanced (unlimited) |
| Federation | — | — | ✓ |

The matrix is encoded in `flags/declarations.go` as defaults; per-user overrides ([Story 16.8](story-16-08-feature-flags-api.md)) can grant exceptions but the default-by-tier is the canonical answer.

## 2. Seat enforcement

### 2.1 Schema

`shared/db/migrations/0060_seat_counter.sql`:

Postgres variant (`shared/db/migrations/0060_seat_counter.sql`):

```sql
-- +goose Up
-- +goose StatementBegin
-- We don't add a column; seats are computed live from users.
-- This migration adds a function for atomic seat-count check.
CREATE OR REPLACE FUNCTION current_seat_count() RETURNS INTEGER AS $$
    SELECT count(*)::int FROM users WHERE id != '00000000-0000-0000-0000-000000000001'
$$ LANGUAGE sql STABLE;
-- +goose StatementEnd
```

SQLite has no `CREATE FUNCTION` and only limited support for stored
procedures; the SQLite variant
(`shared/db/migrations/0060_seat_counter.sqlite.sql`) is a no-op
migration (just records the version) and the seat-count is computed in
Go via a parameterized query against the same predicate. The
SQLite-side enforcement is otherwise identical because all calls go
through `g.db.CurrentSeatCount(ctx)`, which sqlc routes to the
function on Postgres or to the inline query on SQLite.

```sql
-- shared/db/queries/seat_counter.sql (sqlc fallback for SQLite)
-- name: CurrentSeatCount :one
SELECT count(*) FROM users WHERE id != '00000000-0000-0000-0000-000000000001';
```

The sentinel admin doesn't count toward seats (it's the bootstrap
row, not a "user").

### 2.2 Enforcement

`api/internal/auth/seats.go`:

```go
func (g *Gate) AssertCanCreateUser(ctx context.Context) error {
    tier := g.tier.Current(ctx)
    cap := tier.SeatCap()      // 1, 4, or math.MaxInt
    n, _ := g.db.CurrentSeatCount(ctx)
    if int(n) >= cap {
        return ErrSeatExhausted
    }
    return nil
}
```

Wired into the `POST /api/users` handler from Epic 10 Story 10.1:

```go
if err := seatGate.AssertCanCreateUser(ctx); err != nil {
    if errors.Is(err, ErrSeatExhausted) {
        problem(w, 402, "seat-exhausted",
            fmt.Sprintf("Your %s plan allows %d seats; please upgrade.", tier.Name, cap))
        return
    }
}
```

## 3. Quota & relay

The relay quota is enforced by the relay edge ([Story 15.2](../15-discovery/story-15-02-cloud-relay.md)) which has access to per-server tier via the license token in the agent's `Hello`. We do not duplicate enforcement in the API; the edge is authoritative for byte counting.

The API surfaces the cap to admins:

```go
// GET /api/admin/relay/usage
{
  "period_start": "2026-05-01",
  "bytes_used": 12_000_000_000,
  "bytes_cap":  214_748_364_800,   // 200 GB
  "tier": "home"
}
```

## 4. Backup

`api/internal/backup/engine.go`:

```go
type Engine struct {
    db          *sql.DB
    libraryRoot string
    dest        Destination       // s3://, file://, etc.
    encrypt     EncryptOpts       // AES-256-GCM key from license, see §4.1
    cadence     time.Duration
}

func (e *Engine) Snapshot(ctx context.Context) (Manifest, error) {
    // 1. pg_dump --format=custom for Postgres; .sql for SQLite
    dump, err := e.dumpDatabase(ctx)
    if err != nil { return Manifest{}, err }
    // 2. archive metadata: licenses, settings, libraries.toml
    // 3. NOT included: video files (operator's responsibility, too large)
    // 4. encrypt + push to dest
    archive, err := e.assembleArchive(dump, ...)
    if err != nil { return Manifest{}, err }
    return e.dest.Put(ctx, archive)
}
```

### 4.1 Encryption

Backups are encrypted with AES-256-GCM. The data key is derived from a
**user-supplied passphrase** (entered at backup-create time and again
at restore time) via `HKDF(passphrase || license_id || "backup")`.
This is an integrity-and-confidentiality scheme:

- The license file is *not* the only secret. Earlier drafts derived
  the key from `license.signature`, but the signature is *public*
  (anyone with the license file has it), so deriving from the
  signature alone offered integrity but no confidentiality against an
  attacker who exfiltrates the customer's license file. Adding the
  passphrase restores confidentiality.
- Loss of either the passphrase or the license = loss of the backup
  (operator must keep both safe).
- A passphrase-less mode exists for headless setups but is documented
  as **integrity-only** (the data key is then `HKDF(license.signature
  || "backup")` and any holder of the license file can decrypt).
- Documented in `docs/operations/backup.md`.

### 4.2 Cadence by tier

```go
func cadenceFor(tier Tier) time.Duration {
    switch tier.Name {
    case "home": return 24 * time.Hour
    case "pro":  return 1  * time.Hour
    default:     return 0   // disabled
    }
}
```

The Epic 22 scheduler reads this per server.

## 5. Downgrade grace

`api/internal/license/grace.go`:

```go
type GraceTracker struct {
    db    *sql.DB
}

// On every tier change (license refresh), record the previous tier.
func (g *GraceTracker) OnTierChange(ctx context.Context, prev, next Tier) error {
    if downgrade(prev, next) {
        return g.db.Exec(`INSERT INTO tier_grace (started_at, prev_tier)
                          VALUES (now(), $1)`, prev.Name)
    }
    if upgrade(prev, next) {
        // Clear ALL grace rows on upgrade, not just rows with the
        // immediately-previous tier — a chain of downgrades could have
        // left rows for several distinct prev_tier values, and an
        // upgrade should resolve all of them.
        return g.db.Exec(`DELETE FROM tier_grace`)
    }
    return nil
}

// Asked by the flag resolver: are we still within grace?
func (g *GraceTracker) Active(ctx context.Context) (bool, *time.Time) {
    var startedAt sql.NullTime
    g.db.QueryRow(`SELECT started_at FROM tier_grace ORDER BY started_at DESC LIMIT 1`).Scan(&startedAt)
    if !startedAt.Valid { return false, nil }
    end := startedAt.Time.Add(30 * 24 * time.Hour)
    if time.Now().After(end) { return false, nil }
    return true, &end
}
```

`shared/db/migrations/0061_tier_grace.sql`:

```sql
CREATE TABLE tier_grace (
    started_at TIMESTAMPTZ NOT NULL,
    prev_tier  TEXT NOT NULL,
    PRIMARY KEY (started_at, prev_tier)
);
```

The flag resolver:

- During grace: feature flags resolve as if the previous tier were still active, except for **mutating** actions which return 403 `tier-grace-readonly`. Reads (e.g., browsing the analytics dashboard, viewing federation list) succeed; new federation pairings, new backups, new relay sessions are blocked.
- After grace: flags resolve to the new (lower) tier; the UI hides those panels entirely.

## 6. Test plan

### 6.1 Seat enforcement

| Test | What it pins |
|---|---|
| `TestSeatLimitOnHome` | Apply `home` license; create 4 users + sentinel; 5th creation → 402 `seat-exhausted`. |
| `TestSeatLimitProUnlimited` | Apply `pro`; create 100 users → all succeed. |
| `TestSentinelDoesNotCountTowardSeats` | Empty users table = 0 seats; sentinel exists. |

### 6.2 Backup

| Test | What it pins |
|---|---|
| `TestBackupProducesEncryptedArchive` | Run snapshot; output archive's first 4 bytes are not the SQL header (i.e., it's encrypted). |
| `TestBackupCadenceFromTier` | Apply `home`; cadence resolver returns 24h. Apply `pro`; returns 1h. |
| `TestBackupSkippedOnFreeTier` | Free tier; backup engine refuses with `ErrBackupDisabled`. |
| `TestBackupKeyDerivationStable` | Same license file → same KDF output → same archive decryptable. |
| `TestBackupInProgressOnLicenseExpiryCompletes` | Trigger snapshot; mock license to expire mid-write; archive completes; next is blocked. |

### 6.3 Downgrade grace

| Test | What it pins |
|---|---|
| `TestDowngradeProToHomeTriggersGrace` | License refresh from `pro`→`home`; `tier_grace` row inserted. |
| `TestDuringGraceFederationListReadable` | After downgrade, `GET /api/federation` works. |
| `TestDuringGraceNewFederationBlocked` | `POST /api/federation/init` returns 403 `tier-grace-readonly`. |
| `TestPostGraceUIHidesPanel` | After 30 days, federation flag false, panel hidden. |
| `TestUpgradeAbortsGrace` | Re-upgrade to `pro` deletes the grace row. |

### 6.4 Integration

| Test | What it pins |
|---|---|
| `e2e_HomeLicenseSeats` | Real license → users, backup, analytics behave per tier. |
| `e2e_DowngradeFlow` | License changed → 30 d grace → blocked mutations → flags flip after expiry. |

## 7. Edge cases — handling table

| Case | Behaviour | Where pinned |
|---|---|---|
| License clock skew | 72 h hard-grace from Story 16.4. We just consume the grace flag. | (Story 16.4) |
| License revoked due to fraud | `tier_grace` not started (revoke is not "downgrade"); flags flip immediately. | `TestRevokedSkipsGrace` |
| Mid-backup license expiry | Current backup completes; next blocked. | `TestBackupInProgressOnLicenseExpiryCompletes` |
| Seat full + admin tries to create user | 402 with quota message; no DB write. | `TestSeatLimitOnHome` |
| Sentinel + 4 home users | 4 seats counted (sentinel excluded). | `TestSentinelDoesNotCountTowardSeats` |
| Re-upgrade after grace already expired | New license restores access; analytics history that was preserved (Story 16.1) is visible again. | `e2e_DowngradeRestoreFlow` |
| Concurrent user creation racing seat cap | Both create requests serialize on `current_seat_count()`'s SELECT; the second hits 402. (Race window narrow but real; for hard cap, a `SELECT ... FOR UPDATE` could be added.) Documented as acceptable since seat cap is not a hard security boundary. | `TestSeatRaceTwoCreations` |
| Backup destination unreachable | Engine retries 3× with backoff; on persistent failure, surfaces an admin alert; next cadence retries. | `TestBackupDestUnreachable` |
| Analytics retention drift | Free tier: 0 d (no rows kept); home: 30 d rolling; pro: unlimited. The retention sweeper reads tier on each tick. | `TestAnalyticsRetentionByTier` |

## 8. Acceptance checklist

**Tier matrix**
- [ ] `flags/declarations.go` encodes the §1 matrix.

**Seats**
- [ ] `current_seat_count()` excludes sentinel.
- [ ] `POST /api/users` enforces tier cap.

**Backup**
- [ ] Encrypted archive produced; cadence matches tier.

**Grace**
- [ ] `tier_grace` table; reads OK during grace; writes blocked.

**Tests**
- [ ] All §6 tests pass.

**Docs**
- [ ] `docs/operations/backup.md` includes the license-key dependency for restoration.
- [ ] `specs/epics/16-subscriptions/README.md` ticks story 16.2.
