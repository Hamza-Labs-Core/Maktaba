# Implementation Plan — Story 12.10 Device Registration & Push Fan-Out API

> Companion to [story-12-10-device-registration-api.md](story-12-10-device-registration-api.md).
> Owns: `devices` table, registration endpoints, push fan-out worker, secrets.
>
> **Ownership note.** This plan owns the `devices` table migration per
> architecture §8.2.3. `plan-07-22-devices-register.md` is superseded; only
> its handler stubs remain. Schema columns (including `bundle_id`, required
> for APNs topic routing on iOS/macOS/tvOS) are defined here.

## 0.0 Audit-log dependency

Audit-log writes from this plan go to **plan-21-06's canonical
`audit_log` table** (architecture §8.6.1). `category='device'` is
reserved per arch §8.6.1 for device registration / token rotation.
Per `PLAN_REVIEW_18_24.md` §1.4 the canonical column names are:

- `actor_user` (NOT `actor_user_id`)
- `actor_ip` (NOT `ip`)
- `target_id` (the device UUID, as text)
- `target_kind = 'device'`
- `payload` (JSONB; arbitrary event detail)
- `dedupe_key` (optional string, e.g., `"device_register:<token_hash>"`)
- `created_at` (TIMESTAMPTZ, default `now()`)

Inserts in this plan (e.g., `Register`, `Revoke`, `cross_user_claim`)
use these column names. The `category='device'` insert that
`PLAN_REVIEW_07_13` previously flagged as failing is now valid against
plan-21-06's CHECK enum (which includes `'device'`).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Migration | `shared/db/migrations/0040_devices.sql` (Postgres) + `0040_devices.sqlite.sql`. Schema matches architecture §8.2.3 exactly (canonical). |
| Endpoints | `api/internal/http/devices.go`. Routes `POST /api/devices`, `PATCH /api/devices/{id}`, `DELETE /api/devices/{id}`, `GET /api/me/devices` (architecture §9.7.2). |
| Worker | `pipeline/src/maktaba_pipeline/push_fanout/` (Python). Consumes domain events from internal event bus (architecture §1.4). |
| APNs | `pipeline/src/maktaba_pipeline/push_fanout/apns.py` — HTTP/2 to `/3/device/{token}`; ES256 JWT signed with `.p8`. |
| FCM | `pipeline/src/maktaba_pipeline/push_fanout/fcm.py` — HTTP v1 with Google service-account JWT. |
| Secrets | TOML config `[push.apns]`, `[push.fcm]`. Read at boot; redaction filter (Epic 21 Story 21.1) covers `*-key`, `*-secret`, `*service_account*`. |
| Cross-user token claim | 24 h grace before previous owner's row is hard-revoked. |
| Audit | Per-call audit_log row using plan-21-06's canonical schema (`category='device'`, `event`, `actor_user`, `actor_ip`, `target_id` = device UUID, `target_kind='device'`, `payload`, `dedupe_key`, `created_at`). See §0. |
| Out of scope | Push UI (Story 12.4); downloaded-flag (Story 12.11). |

## 1. Schema

```sql
CREATE TABLE devices (
  id            UUID PRIMARY KEY,
  user_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  platform      TEXT NOT NULL CHECK (platform IN ('ios','android','web','macos','tvos')),
  bundle_id     TEXT,                                  -- required for APNs (iOS/macOS/tvOS); nullable for android/web
  token         TEXT NOT NULL,
  token_hash    BYTEA GENERATED ALWAYS AS (sha256(token::bytea)) STORED,
  app_version   TEXT,
  os_version    TEXT,
  locale        TEXT NOT NULL DEFAULT 'en',
  categories    JSONB NOT NULL DEFAULT '[]',
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_seen_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  revoked_at    TIMESTAMPTZ,
  UNIQUE (user_id, token_hash)
);

CREATE INDEX devices_user_active_idx ON devices (user_id, revoked_at);
CREATE INDEX devices_token_hash_idx  ON devices (token_hash);
```

Per architecture §8.2.3, `bundle_id` is required when `platform IN ('ios','macos','tvos')` because APNs uses it as the topic for routing; `categories` is a JSONB array of opted-in category strings (default empty — clients must explicitly opt in).

SQLite analogue: `BYTEA → BLOB`, `JSONB → TEXT` with `CHECK(json_valid)`. Generated column for SQLite expressed as a trigger.

## 2. Token validation

```go
// devices.go
func validateRegistration(platform, bundleID, token string) error {
    switch platform {
    case "ios", "macos", "tvos":
        if bundleID == "" {
            return errors.New("bundle-id-required")
        }
        if matched, _ := regexp.MatchString(`^[a-fA-F0-9]{64,200}$`, token); !matched {
            return errors.New("invalid-token")
        }
    case "android":
        if !strings.Contains(token, ":") || len(token) < 100 {
            return errors.New("invalid-token")
        }
    case "web":
        if len(token) < 32 { return errors.New("invalid-token") }
    default:
        return errors.New("unsupported-platform")
    }
    return nil
}
```

## 3. Endpoints

### `POST /api/devices`

Body: `{platform, bundle_id?, token, app_version?, os_version?, locale?, categories?}`.
`bundle_id` is required for `platform ∈ {ios, macos, tvos}`.

```go
type RegisterPayload struct {
    Platform   string   `json:"platform"`
    BundleID   string   `json:"bundle_id,omitempty"`
    Token      string   `json:"token"`
    AppVersion string   `json:"app_version,omitempty"`
    OSVersion  string   `json:"os_version,omitempty"`
    Locale     string   `json:"locale,omitempty"`
    Categories []string `json:"categories,omitempty"`
}

func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
    var p RegisterPayload
    if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
        problem.Write(w, 400, "bad-request"); return
    }
    if err := validateRegistration(p.Platform, p.BundleID, p.Token); err != nil {
        problem.Write(w, 400, err.Error()); return
    }
    ident := auth.IdentityFrom(r)
    res, err := h.svc.Register(r.Context(), ident.UserID, p)
    switch {
    case errors.Is(err, ErrTokenClaimedByOther):
        problem.Write(w, 409, "token-claimed-by-other-user"); return
    case err != nil:
        problem.Write(w, 500, "internal"); return
    }
    if res.Created {
        w.WriteHeader(201)
    } else {
        w.WriteHeader(200)
    }
    json.NewEncoder(w).Encode(map[string]string{"id": res.ID})
}
```

`Register` resolution:

1. UPSERT by `(user_id, token_hash)`. If row exists → update `last_seen_at`, `app_version`, `os_version`, `locale`, `categories`, `bundle_id`; un-revoke if previously revoked. Return `{Created:false}`.
2. Otherwise check `token_hash` across all users; if owned by another, schedule `revoked_at = now() + 24h` on the previous owner's row + insert new row.
3. Audit: insert into plan-21-06's `audit_log` with `category='device'`,
   `event=Created?'device.register':'device.update'`, `actor_user`=user
   id, `actor_ip`=client IP, `target_id`=device UUID,
   `target_kind='device'`, `payload`={platform, app_version, locale},
   `dedupe_key`=`"device_register:<token_hash_hex>"`. See §0.

### `PATCH /api/devices/{id}`, `DELETE`, `GET /api/me/devices`

Standard scoped CRUD. PATCH only allows `categories` and `locale`. DELETE sets `revoked_at`. GET excludes `token` and `token_hash`.

## 4. Push fan-out worker

```python
# push_fanout/worker.py
async def run(events: AsyncIterator[Event]) -> None:
    async for ev in events:
        match ev.kind:
            case 'job.completed':   await fanout(ev, category='processing')
            case 'job.failed':      await fanout_admins(ev, category='job_failed')
            case 'library.video.added': await fanout(ev, category='new_content')
            case 'subscription.expiring': await fanout(ev, category='subscription')

async def fanout(ev, *, category):
    rows = await db.devices.list_active_by_event(ev.user_id, category=category)
    payloads = [build_payload(ev, d.locale) for d in rows]
    await batched_dispatch(rows, payloads, key=ev.user_id, category=category)
```

`batched_dispatch` coalesces ≥ 5 events for the same `(user_id, category)` within a 60-second window into a single payload "5 jobs completed".

```python
async def deliver_apns(d: Device, payload: dict):
    for attempt, delay in enumerate([0, 1, 4, 16]):
        if delay: await asyncio.sleep(delay)
        try:
            res = await apns_client.push(d.token, payload)
            if res.ok: return
            if res.status in (400, 410):  # BadDeviceToken, Unregistered
                await db.devices.revoke(d.id)
                return
        except TransientError:
            continue
```

## 5. Configuration

```toml
[push.apns]
team_id      = "ABC123XYZ"
key_id       = "K1Y0001"
p8_path      = "/etc/maktaba/apns.p8"
environment  = "production"   # or "sandbox"

[push.fcm]
service_account_json_path = "/etc/maktaba/fcm-sa.json"
```

Boot-time read; if neither is configured, worker emits a single warning and disables itself. The HTTP registration endpoint still accepts tokens.

## 6. Security & redaction

- Plaintext token never returned by GET.
- Audit log entries on `register`, `revoke`, `cross_user_claim` — all written to plan-21-06's canonical table with `category='device'` and the canonical column names (`actor_user`, `actor_ip`, `target_id`, `target_kind`, `payload`, `dedupe_key`, `created_at`). See §0.
- Redaction filter (Epic 21 Story 21.1) masks anything that looks like an APNs/FCM token in logs (`token=********`).

## 7. Edge cases

| Case | Handling |
|---|---|
| Re-register revoked token | Un-revoke; update `last_seen_at`. |
| 100+ devices | `GET /api/me/devices` paginates. |
| APNs/FCM downtime > 16 s | Drop with logged warning in v1; `push_outbox` table is post-v1. |
| Token rotation mid-fanout | Fanout uses snapshot; the new POST arrives before next event → no duplicate. |
| User deletion mid-fanout | Cascade removes row; in-flight push may still send once (acceptable race). |

## 8. Test cases

### 8.1 Unit (Go)

| Test | Asserts |
|---|---|
| `register new returns 201` | Row inserted. |
| `register existing returns 200, updates last_seen` | Row updated; `last_seen_at` advanced. |
| `register revoked un-revokes` | `revoked_at` cleared. |
| `cross-user claim returns 409` | Previous owner's row scheduled for revocation in 24h. |
| `invalid token format` | 400 `invalid-token`. |
| `cascade delete on user removal` | Devices removed in same TX. |

### 8.2 Unit (Python worker)

| Test | Asserts |
|---|---|
| `category filter suppresses` | Device whose `categories` array does not contain `'new_content'` is not in fanout list for that category. |
| `coalesce window batches` | 6 job.completed → 1 push payload "5 jobs completed" (1 dropped to overflow). |
| `BadDeviceToken revokes` | Worker revokes the row; subsequent events skip it. |
| `transient retries 3 times` | 3 retries with backoff, then drop. |

### 8.3 Integration

- End-to-end happy path with a real APNs sandbox token: registration → emit `job.completed` → device receives push.

## 9. Performance

- Fanout latency ≤ 30 s p95 from event to APNs/FCM submit.
- Single-token verify (registration UPSERT) ≤ 5 ms with `(user_id, token_hash)` index.

## 10. Dependencies

- REVIEW §1.1.h: this story owns the `devices` migration.
- REVIEW §1.1.f: audit table.
- Epic 21 Story 21.1: secret redaction.
- Story 12.11 reads from this table for downloaded-flag scoping.
