# Implementation Plan — Story 7.22 Device Registration for Push

> Companion to [story-07-22-devices-register.md](story-07-22-devices-register.md).
> **STATUS: SUPERSEDED.** The canonical owner of the `devices` schema and
> the device-registration migration is `plan-12-10-device-registration-api.md`
> (architecture §8.2.3). This plan is retained only for the device-registration
> HTTP handler stubs that 12-10 references during the Epic 12 rollout — it
> no longer ships its own migration. `bundle_id` is preserved by the canonical
> schema in 12-10; do **not** introduce a separate validator here.
>
> Concretely:
> - The `0022_devices.sql` migration in §3 below is **deleted**. The schema
>   in plan-12-10 is the single source of truth.
> - The DTOs and handler/service code below remain as a sketch of the HTTP
>   surface for 12-10 to mount once the schema is canonical.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Routes | `POST /api/devices/register`, `DELETE /api/devices/{id}`, `GET /api/devices` (own list). |
| Storage | New `devices` table (already specified in this epic's README). Soft delete via `revoked_at`. |
| Push delivery | Internal `Notify(user_id, payload)` interface; vendor bridges (APNs/FCM/Web Push) live behind it. The bridge implementations are stubbed here; full impl lives in Epic 12. |
| Token rotation | Same `(user_id, platform, bundle_id)` with a different `push_token` revokes the previous row at insert time. |
| Out of scope | The actual APNs/FCM credentials, the worker that calls `Notify` from job-completion handlers (Epic 12). |

## 1. Architecture diagram

```
   POST /api/devices/register
   { platform, push_token, bundle_id, app_version?, locale? }
        │
        ▼
   ┌────────────────────────────────────────────────────────────┐
   │ Validate platform ∈ {ios, android, web}                    │
   │ Tx:                                                        │
   │   1. Upsert by (user_id, platform, push_token):            │
   │      INSERT ... ON CONFLICT DO UPDATE SET last_seen_at=now │
   │   2. If insert (new row): revoke prior rows for the same   │
   │      (user_id, platform, bundle_id) by setting revoked_at  │
   │ → 201 if new; 200 if replaced; body has device id          │
   └────────────────────────────────────────────────────────────┘

   DELETE /api/devices/{id}
        │
        ▼ UPDATE devices SET revoked_at = now() WHERE id=$1 AND user_id=$2

   internal: Notify(ctx, user_id, payload)
        │
        ▼
   ┌────────────────────────────────────────────────────────────┐
   │ SELECT * FROM devices                                      │
   │  WHERE user_id=$1 AND revoked_at IS NULL                   │
   │ For each device:                                           │
   │   bridge[platform].Send(push_token, payload)               │
   │   if BadDeviceToken/Unregistered → revoke row              │
   └────────────────────────────────────────────────────────────┘
```

## 2. New files

| Path | Purpose |
|---|---|
| `api/internal/devices/handler.go` | Routes. |
| `api/internal/devices/service.go` | Upsert + rotation + revoke logic. |
| `api/internal/devices/types.go` | DTOs. |
| `api/internal/devices/notify.go` | Internal Notify entrypoint + vendor bridge interface. |
| `api/internal/devices/bridges/apns.go` | Stub (Epic 12 finishes). |
| `api/internal/devices/bridges/fcm.go` | Stub. |
| `api/internal/devices/bridges/webpush.go` | Stub. |
| `api/internal/devices/handler_test.go` | Integration. |
| `api/internal/devices/service_test.go` | Unit. |
| `shared/db/queries/devices.sql` | sqlc inputs. |
| `shared/db/migrations/0022_devices.sql` | Schema. |

## 3. SQL — schema

**REMOVED.** The `devices` schema is owned by `plan-12-10-device-registration-api.md`
(architecture §8.2.3 documents the canonical columns including `bundle_id`).
This plan no longer ships a `0022_devices.sql` migration. Refer to plan-12-10
for the authoritative `CREATE TABLE devices (...)` statement and the
`(user_id, platform, push_token)` uniqueness constraint.

## 4. Type definitions

```go
// api/internal/devices/types.go
package devices

import (
    "time"
    "github.com/google/uuid"
)

type Platform string
const (
    PlatformIOS     Platform = "ios"
    PlatformAndroid Platform = "android"
    PlatformWeb     Platform = "web"
)

type Device struct {
    ID           uuid.UUID  `json:"id"`
    Platform     Platform   `json:"platform"`
    PushToken    string     `json:"push_token,omitempty"`
    BundleID     string     `json:"bundle_id"`
    AppVersion   *string    `json:"app_version,omitempty"`
    Locale       *string    `json:"locale,omitempty"`
    RegisteredAt time.Time  `json:"registered_at"`
    LastSeenAt   time.Time  `json:"last_seen_at"`
    RevokedAt    *time.Time `json:"revoked_at,omitempty"`
}

// bundle_id is preserved by the canonical schema in plan-12-10 (architecture
// §8.2.3) but this plan does NOT enforce its presence with a validator
// here — 12-10 owns the schema constraint. The DTO simply carries the
// value through.
type RegisterInput struct {
    Platform   Platform `json:"platform"   validate:"required,oneof=ios android web"`
    PushToken  string   `json:"push_token" validate:"required,min=1,max=4096"`
    BundleID   string   `json:"bundle_id,omitempty"`
    AppVersion *string  `json:"app_version,omitempty"`
    Locale     *string  `json:"locale,omitempty"`
}
```

## 5. Service layer

```go
// api/internal/devices/service.go
package devices

import (
    "context"
    "errors"

    "github.com/google/uuid"
    "github.com/jackc/pgx/v5"

    "maktaba/api/internal/httperror"
)

type registerOutcome string

const (
    outcomeInserted registerOutcome = "inserted"
    outcomeReplaced registerOutcome = "replaced"
)

func (s *service) register(ctx context.Context, user User, in RegisterInput) (*Device, registerOutcome, *httperror.Error) {
    if len([]byte(in.PushToken)) > 4096 {
        return nil, "", httperror.BadRequest("push_token too long")
    }

    var (
        device  Device
        outcome registerOutcome
    )

    err := s.db.Tx(ctx, func(tx Tx) error {
        // Upsert by (user, platform, token).
        d, err := tx.UpsertDevice(ctx, UpsertDeviceParams{
            ID:         uuid.Must(uuid.NewV7()),
            UserID:     user.ID,
            Platform:   string(in.Platform),
            PushToken:  in.PushToken,
            BundleID:   in.BundleID,
            AppVersion: in.AppVersion,
            Locale:     in.Locale,
        })
        if err != nil { return err }
        outcome = outcomeReplaced
        if d.Created { outcome = outcomeInserted }
        device = toDTO(d)

        if outcome == outcomeInserted {
            // New row → revoke older rows for same (user, platform, bundle_id).
            if err := tx.RevokePriorTokens(ctx, RevokePriorTokensParams{
                UserID: user.ID, Platform: string(in.Platform),
                BundleID: in.BundleID, KeepID: device.ID,
            }); err != nil { return err }
        }
        return nil
    })
    if err != nil {
        if errors.Is(err, pgx.ErrTxClosed) {
            return nil, "", httperror.Internal("tx error")
        }
        return nil, "", httperror.Internal("device register")
    }
    return &device, outcome, nil
}

func (s *service) unregister(ctx context.Context, user User, id uuid.UUID) *httperror.Error {
    n, err := s.db.RevokeDevice(ctx, RevokeDeviceParams{ID: id, UserID: user.ID})
    if err != nil { return httperror.Internal("revoke") }
    if n == 0 { return httperror.NotFound("device") }
    return nil
}
```

## 6. Notify

```go
// api/internal/devices/notify.go
package devices

import (
    "context"
    "errors"
    "log/slog"
)

type Bridge interface {
    Send(ctx context.Context, token string, payload map[string]any) error
}

type Notifier struct {
    db       DB
    bridges  map[Platform]Bridge
    log      *slog.Logger
}

func (n *Notifier) Notify(ctx context.Context, userID uuid.UUID, payload map[string]any) error {
    rows, err := n.db.ListActiveDevices(ctx, userID)
    if err != nil { return err }
    for _, d := range rows {
        bridge, ok := n.bridges[Platform(d.Platform)]
        if !ok { continue }
        if err := bridge.Send(ctx, d.PushToken, payload); err != nil {
            if errors.Is(err, ErrBadDeviceToken) || errors.Is(err, ErrUnregistered) {
                _ = n.db.RevokeDevice(ctx, RevokeDeviceParams{ID: d.ID, UserID: userID})
                continue
            }
            n.log.Warn("push_send_failed", "device_id", d.ID, "err", err.Error())
        }
    }
    return nil
}

var (
    ErrBadDeviceToken = errors.New("bad device token")
    ErrUnregistered   = errors.New("token unregistered")
)
```

The vendor bridges live in `bridges/`; in this story they're stubs that
return `nil` (or, in tests, the canned errors above).

## 7. Handler scaffolding

```go
// api/internal/devices/handler.go
package devices

import (
    "encoding/json"
    "net/http"

    "github.com/go-chi/chi/v5"
    "github.com/google/uuid"

    "maktaba/api/internal/httperror"
)

func (h *handler) register(w http.ResponseWriter, r *http.Request) {
    user := userFromCtx(r.Context())
    var in RegisterInput
    if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
        httperror.Write(w, r, httperror.BadRequest("invalid json")); return
    }
    if err := validate(in); err != nil { httperror.Write(w, r, err); return }

    d, outcome, perr := h.svc.register(r.Context(), user, in)
    if perr != nil { httperror.Write(w, r, perr); return }

    if outcome == outcomeInserted { w.WriteHeader(http.StatusCreated) }
    json.NewEncoder(w).Encode(d)
}

func (h *handler) unregister(w http.ResponseWriter, r *http.Request) {
    user := userFromCtx(r.Context())
    id, err := uuid.Parse(chi.URLParam(r, "id"))
    if err != nil { httperror.Write(w, r, httperror.BadRequest("invalid id")); return }
    if perr := h.svc.unregister(r.Context(), user, id); perr != nil {
        httperror.Write(w, r, perr); return
    }
    w.WriteHeader(http.StatusNoContent)
}

func (h *handler) list(w http.ResponseWriter, r *http.Request) {
    user := userFromCtx(r.Context())
    rows, err := h.db.ListUserDevices(r.Context(), user.ID)
    if err != nil { httperror.Write(w, r, httperror.Internal("list")); return }
    json.NewEncoder(w).Encode(map[string]any{"items": rows})
}
```

## 8. SQL — sqlc inputs

`shared/db/queries/devices.sql`:

```sql
-- name: UpsertDevice :one
INSERT INTO devices (id, user_id, platform, push_token, bundle_id, app_version, locale)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (user_id, platform, push_token) DO UPDATE
   SET last_seen_at = now(),
       bundle_id    = EXCLUDED.bundle_id,
       app_version  = EXCLUDED.app_version,
       locale       = EXCLUDED.locale,
       revoked_at   = NULL
RETURNING *, (xmax = 0) AS created;

-- name: RevokePriorTokens :exec
UPDATE devices
   SET revoked_at = now()
 WHERE user_id = $1
   AND platform = $2
   AND bundle_id = $3
   AND id <> $4
   AND revoked_at IS NULL;

-- name: RevokeDevice :execrows
UPDATE devices SET revoked_at = now()
 WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL;

-- name: ListActiveDevices :many
SELECT * FROM devices
 WHERE user_id = $1 AND revoked_at IS NULL;

-- name: ListUserDevices :many
SELECT * FROM devices WHERE user_id = $1
 ORDER BY registered_at DESC;
```

The `xmax = 0` trick reports whether the upsert was an INSERT (true)
versus an UPDATE (false), letting the handler pick 201 vs 200.

## 9. Test plan

### 9.1 Unit (`service_test.go`)

| Test | What it pins |
|---|---|
| `TestPushTokenTooLong` | 5 KiB push_token → 422 (or 400). |
| `TestPlatformInvalid` | `platform=desktop` → 422. |
| `TestRegistrationInsertOutcome` | First time → outcome `inserted`, status 201. |
| `TestRegistrationReplacedOutcome` | Same `(user, platform, push_token)` → outcome `replaced`, status 200. |

### 9.2 Integration (`handler_test.go`)

| Test | What it pins |
|---|---|
| `TestRegisterAndList` | POST → GET shows the device. |
| `TestRegisterTwiceSameToken` | Two POSTs with same `(user, platform, push_token)` → one row; `last_seen_at` advances. |
| `TestTokenRotation` | POST token A; POST token B for same (user, platform, bundle_id) → token A row is `revoked_at`-stamped. |
| `TestUnregister` | DELETE → `revoked_at` set; subsequent `Notify` skips it. |
| `TestUnregisterAnotherUser` | User B tries to DELETE user A's device → 404 (no leak). |
| `TestNotifySkipsRevoked` | One active + one revoked device → only the active one is `Send`'d to. |
| `TestNotifyAutoRevokeOnBadToken` | Stub bridge returns `ErrBadDeviceToken` → row's `revoked_at` set. |
| `TestUserDeletedCascades` | Delete user → all their devices removed via FK cascade. |
| `TestSameTokenDifferentUsers` | Two `user_id`s with the same `push_token` → both rows exist (verifying the unique key is `(user, platform, token)`, not `(token)`). |
| `TestMissingBundleID` | POST without `bundle_id` is accepted at this layer; plan-12-10's schema or higher-layer validators decide whether to require it for the platform. |

## 10. Edge cases — handling table

| Case | Behaviour | Where it's pinned |
|---|---|---|
| Token longer than 1 KiB | Capped at 4 KiB by validator; over 4 KiB → 422. | `TestPushTokenTooLong` |
| Re-registration with the same token, different user (account switch) | New row created under new user; previous user's row stays active until that app re-registers. | `TestSameTokenDifferentUsers` |
| Same `(user, platform, bundle_id)` with rotating tokens | New token wins; older rows revoked at insert. | `TestTokenRotation` |
| `BadDeviceToken` from APNs | Row auto-revoked. | `TestNotifyAutoRevokeOnBadToken` |
| `Unregistered` from FCM | Same. | Variant |
| Device that registers without `bundle_id` | Required for APNs; 422. | `TestMissingBundleID` |
| User has 0 devices | `Notify` is a no-op; not an error. | Documented |
| User has 100 devices | `Notify` iterates serially; future optimisation could parallelise per-platform. | Documented |
| Revoked device tries to re-register (token unchanged) | `ON CONFLICT DO UPDATE SET revoked_at = NULL` re-activates the row. | Integration |
| User deleted | FK cascade removes all device rows. | `TestUserDeletedCascades` |

## 11. Acceptance checklist

- [ ] `devices` schema lives in plan-12-10 (architecture §8.2.3). This plan **does not** ship a migration.
- [ ] `POST /register` upserts by `(user, platform, push_token)`; returns 201 (new) / 200 (replaced).
- [ ] Token rotation revokes prior rows for `(user, platform, bundle_id)`.
- [ ] `DELETE /{id}` is a soft delete (`revoked_at`).
- [ ] Internal `Notify(user_id, payload)` iterates active devices and auto-revokes on `BadDeviceToken`/`Unregistered`.
- [ ] All `Test*` cases pass.
- [ ] `specs/epics/07-api-server/README.md` ticks story 7.22.
