# Implementation Plan — Story 16.8 API: feature-flag resolution endpoint

> Companion to [story-16-08-feature-flags-api.md](story-16-08-feature-flags-api.md).
> The story states *what* and *why*; this plan states *how*.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Migration | `shared/db/migrations/0065_feature_flags.sql`. |
| sqlc queries | `shared/db/queries/feature_flags.sql`. |
| Resolver | `api/internal/flags/resolver.go` — orchestrates default → tier → cohort → user. |
| Declarations | `api/internal/flags/declarations.go` — flag keys, defaults-by-tier, beta-eligibility. |
| Signature | Ed25519 over the canonicalized bundle JSON. The long-term Ed25519 key set is owned by **Epic 10 Story 10.18** ("Ed25519 long-term server identity keys") — NOT Story 10.6, which covers RS256 / JWKS for short-lived API JWTs only. Until Story 10.18 lands this plan blocks: there is no Ed25519 source to sign with. |
| In-memory cache | Per `(user_id, license_state_version)` for 60 s; invalidation via Postgres `LISTEN flags_changed`. |
| Audit | Every admin write writes `audit_log.category = 'flags'`. |
| Out of scope | Client surface ([Story 16.6](story-16-06-feature-flags.md)). |

## 1. Architecture diagram

```
              ┌──────────────────────────────────────┐
              │ flags.Resolver                       │
              │  layered:                            │
              │  1. Declarations.DefaultsByTier      │
              │  2. feature_flag_overrides (tier)    │
              │  3. beta_cohorts → cohort overrides  │
              │  4. feature_flag_overrides (user)    │
              └─────────────┬────────────────────────┘
                            │
                            ▼
              ┌──────────────────────────────────────┐
              │ in-memory LRU                        │  invalidated by
              │  key = (user_id, lic_version)        │  Postgres LISTEN flags_changed
              │  TTL = 60 s                          │
              └─────────────┬────────────────────────┘
                            │
                            ▼
              GET /api/me/flags →  Ed25519 sign → JSON
```

## 2. Database migration

`shared/db/migrations/0065_feature_flags.sql`:

```sql
-- +goose Up
-- +goose StatementBegin
CREATE TABLE feature_flag_overrides (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    flag_key        TEXT NOT NULL,
    scope           TEXT NOT NULL CHECK (scope IN ('global','tier','user','cohort')),
    scope_value     TEXT,                            -- NULL for global
    value           JSONB NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at      TIMESTAMPTZ,
    created_by      UUID REFERENCES users(id) ON DELETE SET NULL,
    -- scope_value shape varies by scope; pin per-scope shape so admin
    -- writes that put a UUID in a 'tier' row (or vice versa) error
    -- loudly instead of silently mis-applying:
    --   scope='global' → scope_value IS NULL
    --   scope='tier'   → scope_value IN ('free','home','pro')
    --   scope='user'   → scope_value matches a UUID
    --   scope='cohort' → scope_value matches the cohort name pattern
    CONSTRAINT feature_flag_overrides_scope_value_chk CHECK (
        (scope = 'global' AND scope_value IS NULL) OR
        (scope = 'tier'   AND scope_value IN ('free','home','pro')) OR
        (scope = 'user'   AND scope_value ~* '^[0-9a-f-]{36}$') OR
        (scope = 'cohort' AND scope_value ~* '^[a-z0-9_-]{1,64}$')
    )
);
CREATE INDEX feature_flag_overrides_key_scope_idx
    ON feature_flag_overrides (flag_key, scope, scope_value)
    WHERE expires_at IS NULL OR expires_at > now();

CREATE TABLE beta_cohorts (
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    cohort     TEXT NOT NULL,
    joined_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, cohort)
);

-- LISTEN/NOTIFY trigger so replicas invalidate cache on writes.
-- For DELETE events `NEW` is undefined and the trigger function would
-- raise; pick the right tuple per operation. INSERT/UPDATE → NEW.flag_key,
-- DELETE → OLD.flag_key.
CREATE OR REPLACE FUNCTION notify_flags_changed() RETURNS trigger AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        PERFORM pg_notify('flags_changed', OLD.flag_key);
        RETURN OLD;
    ELSE
        PERFORM pg_notify('flags_changed', NEW.flag_key);
        RETURN NEW;
    END IF;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER flags_changed_trigger
    AFTER INSERT OR UPDATE OR DELETE ON feature_flag_overrides
    FOR EACH ROW EXECUTE FUNCTION notify_flags_changed();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS flags_changed_trigger ON feature_flag_overrides;
DROP FUNCTION IF EXISTS notify_flags_changed();
DROP TABLE IF EXISTS beta_cohorts;
DROP TABLE IF EXISTS feature_flag_overrides;
-- +goose StatementEnd
```

SQLite variant: omit the LISTEN/NOTIFY trigger; SQLite caches use a 60 s polling fallback.

## 3. Declarations

`api/internal/flags/declarations.go`:

```go
type Declaration struct {
    Key            string
    DefaultByTier  map[string]any   // free/home/pro
    UserOptIn      bool             // for cohorts
    Description    string
}

var Declarations = []Declaration{
    {Key: "relay",        DefaultByTier: map[string]any{"free": false, "home": true, "pro": true}, Description: "Cloud relay enabled"},
    {Key: "multi_user",   DefaultByTier: map[string]any{"free": false, "home": true, "pro": true}},
    {Key: "backup",       DefaultByTier: map[string]any{"free": false, "home": true, "pro": true}},
    {Key: "analytics",    DefaultByTier: map[string]any{"free": false, "home": true, "pro": true}},
    {Key: "federation",   DefaultByTier: map[string]any{"free": false, "home": false, "pro": true}},
    {Key: "library",      DefaultByTier: map[string]any{"free": true, "home": true, "pro": true}},
    {Key: "search",       DefaultByTier: map[string]any{"free": true, "home": true, "pro": true}},
    {Key: "transcribe",   DefaultByTier: map[string]any{"free": true, "home": true, "pro": true}},
    {Key: "preview_2026", DefaultByTier: map[string]any{"free": false, "home": false, "pro": false}, UserOptIn: true},
}
```

The story EC: "A flag that's never declared in the binary's defaults: ignored by the client (forward-compat); admin UI warns 'unknown flag'." We enforce by warning in the admin UI when an override targets an unknown key.

## 4. Resolver

```go
// api/internal/flags/resolver.go
type Resolver struct {
    db          *db.Queries
    license     license.Service
    cache       *lru.Cache[CacheKey, ResolvedFlags]
    cacheLock   sync.RWMutex
}

type CacheKey struct { UserID uuid.UUID; LicVer string }

func (r *Resolver) Resolve(ctx context.Context, userID uuid.UUID) (ResolvedFlags, error) {
    licStatus, _ := r.license.Status(ctx)
    key := CacheKey{userID, licStatus.Version}
    if v, ok := r.cache.Get(key); ok && v.ExpiresAt.After(time.Now()) { return v, nil }

    // 1. Defaults
    out := defaultsForTier(licStatus.Tier)

    // 2. Tier overrides
    tierOverrides, _ := r.db.GetActiveOverridesByScope(ctx, "tier", licStatus.Tier)
    for _, o := range tierOverrides { out[o.FlagKey] = o.Value }

    // 3. Cohort overrides
    cohorts, _ := r.db.GetUserCohorts(ctx, userID)
    for _, c := range cohorts {
        cohortOverrides, _ := r.db.GetActiveOverridesByScope(ctx, "cohort", c.Cohort)
        for _, o := range cohortOverrides { out[o.FlagKey] = o.Value }
    }

    // 4. User overrides
    userOverrides, _ := r.db.GetActiveOverridesByScope(ctx, "user", userID.String())
    for _, o := range userOverrides { out[o.FlagKey] = o.Value }

    rf := ResolvedFlags{
        Flags:     out,
        Tier:      licStatus.Tier,
        IssuedAt:  time.Now(),
        ExpiresAt: time.Now().Add(60 * time.Second),
    }
    r.cache.Add(key, rf)
    return rf, nil
}
```

The "Higher-numbered overrides win" precedence is implemented by application order (later overwrites earlier).

## 5. Endpoint signing

`api/internal/http/flags.go`:

```go
func meFlags(s *Service) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        u := auth.UserFromContext(r.Context())
        rf, err := s.Resolver.Resolve(r.Context(), u.ID)
        if err != nil { problem(w, 500, "internal", ""); return }
        body := map[string]any{
            "flags":      rf.Flags,
            "kid":        s.Keys.CurrentKID(),
            "issued_at":  rf.IssuedAt.UTC(),
            "expires_at": rf.ExpiresAt.UTC(),
            "tier":       rf.Tier,
        }
        canonical, _ := jcs.Canonicalize(body)
        sig := ed25519.Sign(s.Keys.Private(s.Keys.CurrentKID()), canonical)
        body["signature"] = base64.RawURLEncoding.EncodeToString(sig)
        writeJSON(w, 200, body)
    }
}
```

Key rotation (Epic 10 Story 10.18 — the long-term Ed25519 keyset; Story 10.6 is RS256 only): the response includes the active `kid`. The story TC: "Token rotated: signatures with the old key are still accepted for one cycle (the response includes `kid`); after that they fail." Implementation: the client bundles **both** active and previous public keys for at least a 30-day overlap (sized to App Store / Play Store review cycles, which 7 days does not cover); after that window the previous key is dropped from the bundle in the next client release. The active+previous list is itself published in a small static file per release; long-running clients that fall behind on releases beyond two rotations will refuse the bundle and force a refresh on the next foreground.

## 6. Admin endpoints

```go
r.Route("/admin/flags", func(r chi.Router) {
    r.Use(requireAdmin)
    r.Get("/", listOverrides(s))
    r.Get("/resolve", adminResolveForUser(s))   // ?user_id=
    r.Post("/overrides", createOverride(s))
    r.Patch("/overrides/{id}", patchOverride(s))
    r.Delete("/overrides/{id}", deleteOverride(s))
})

r.Route("/admin/cohorts/{cohort}/users", func(r chi.Router) {
    r.Use(requireAdmin)
    r.Post("/", batchAddCohort(s))           // body: {user_ids: [uuid, ...]}
    r.Delete("/{user_id}", removeFromCohort(s))
})

r.Post("/me/cohorts", joinCohort(s))         // user opt-in if UserOptIn=true
```

`batchAddCohort` accepts up to 1k IDs per request; > 1k → 413 (per AC).

## 7. LISTEN-based invalidation

```go
func (s *Service) RunInvalidationListener(ctx context.Context) error {
    conn, err := s.dbpool.Acquire(ctx)
    if err != nil { return err }
    defer conn.Release()
    _, err = conn.Exec(ctx, "LISTEN flags_changed")
    if err != nil { return err }
    for {
        notif, err := conn.Conn().WaitForNotification(ctx)
        if err != nil { return err }
        s.cache.Purge()  // simple; per-key purge is an optimization
        s.log.Info("flags cache purged", "channel", notif.Channel, "payload", notif.Payload)
    }
}
```

SQLite fallback: a 60 s polling loop reads `feature_flag_overrides.created_at` MAX; on change, purge.

## 8. Test plan

### 8.1 Resolver

| Test | What it pins |
|---|---|
| `TestDefaultsForTier` | New user `home` tier → flags equal `home` defaults. |
| `TestTierOverrideApplies` | Insert override `scope=tier, value=true`; resolve → flag true. |
| `TestUserOverrideOutranksTier` | Tier says false; user override true → resolve true. |
| `TestExpiresAtFiltersOldOverrides` | Override with `expires_at < now()` → ignored. |
| `TestCohortOverrideAppliesOnlyForCohortMembers` | User in cohort → override applies; user not in → does not. |
| `TestStaleConflictWarning` | Two user overrides for the same key; the older is exposed in admin UI as `stale=true`. |

### 8.2 Endpoint

| Test | What it pins |
|---|---|
| `TestMeFlagsRequiresAuth` | Anon → 401. |
| `TestMeFlagsSigned` | Verify with current pubkey passes; tampered body fails. |
| `TestKIDInResponse` | Response contains `kid`. |
| `TestExpiresAt60sFromNow` | TTL window in the response. |

### 8.3 Admin

| Test | What it pins |
|---|---|
| `TestCreateOverrideAuditedAndPropagates` | POST → next `/me/flags` reflects within 60 s. |
| `TestBatchCohortChunked` | 1500 IDs → 413; 1k IDs OK; another 1k OK. |
| `TestUserOptInGated` | User POSTs `/me/cohorts {cohort}` for a `UserOptIn=false` cohort → 403. |
| `TestPostgresLISTENPurges` | Insert override; cache purged within 1 s on the same node. |

### 8.4 Edge

| Test | What it pins |
|---|---|
| `TestUnknownFlagKeyIgnoredByClientButWarned` | Admin creates override for `made.up.key`; admin GET returns it with `unknown=true`. |
| `TestLicenseLapsedExpiryEnforced` | Resolver returns `home`-defaults with `expires_at` set; once stale and license has lapsed to `free`, next resolve returns `free`-defaults. |
| `TestKIDRotationOverlap` | Old kid still verifies for 7 days. |

## 9. Edge cases — handling table

| Case | Behaviour | Where pinned |
|---|---|---|
| Two user-scoped overrides for same flag | The most recently created wins; the older marked stale in admin UI. | `TestStaleConflictWarning` |
| License lapsed mid-session | Cache TTL forces re-resolution; `expires_at` shrinks for the lapsed-license case. | `TestLicenseLapsedExpiryEnforced` |
| Unknown flag created by admin | Stored; admin warning; client ignores. | `TestUnknownFlagKeyIgnoredByClientButWarned` |
| Cohort with 100k users | Add in chunks of ≤ 1k; > 1k → 413. | `TestBatchCohortChunked` |
| Tampered cached override row | Trigger fires on every UPDATE; cache purged; next read re-evaluates. | `TestPostgresLISTENPurges` |
| User opts into a non-`UserOptIn` cohort | 403. | `TestUserOptInGated` |
| Cohort removed mid-session | Within 60 s next resolve excludes; signature changes; client recompares. | `TestCohortRemoveTakesEffect` |
| `expires_at` in override is past at insert time | Ignored from index due to partial predicate; admin warning at create. | `TestPastExpiryAtCreateWarns` |
| SQLite fallback polling | Every 60 s, max(created_at) compared; cache purged on change. | `TestSQLiteFallbackPolling` |
| Concurrent override edits | DB serializes; LISTEN fires after both; cache purged once per change. | `TestConcurrentEditsListenFires` |

## 10. Acceptance checklist

**Schema**
- [ ] `feature_flag_overrides`, `beta_cohorts`, LISTEN trigger.

**Resolver**
- [ ] Layered resolution; cache; license-version-keyed.

**Endpoints**
- [ ] `GET /me/flags` signed with current Ed25519 key; `kid` in response.
- [ ] Admin CRUD on overrides; cohort batch add/remove.
- [ ] User opt-in cohort POST gated.

**Audit**
- [ ] `audit_log.category = 'flags'` on every admin write.

**Tests**
- [ ] All §8 tests pass on Postgres + SQLite.

**Docs**
- [ ] `specs/epics/16-subscriptions/README.md` ticks story 16.8.
