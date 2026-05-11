# Implementation Plan — Story 25.17 Push notification ingest

> Companion to [story-25-17-push-notification-ingest.md](story-25-17-push-notification-ingest.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Endpoints | `POST /api/push/dispatch` (server bearer), `POST /api/push/devices` (user JWT), `DELETE /api/push/devices/{id}` (user JWT). |
| Auth for dispatch | `X-Server-Token` header — same bearer used for tunnel; bcrypt-verified against `servers`. Cross-user pushes → 403 + abuse event. |
| Device token storage | AES-GCM-sealed with cloud's data key; never returned in any response. |
| Templates | `push_templates(kind, locale, title_template, body_template)`; named-placeholder substitution only. |
| Outbox | `push_outbox` rows for delivery durability; APNs (25.18) and FCM (25.19) drain. |
| Dedup | `(user_id, dedupe_key)` uniqueness within `ttl_seconds`. |
| Rate limit | Per-server: 1000/hour default; 10000 for trusted (overrides in `rate_overrides`, declared by plan-25-24 slot 0009 sub-migration). |
| Out of scope | APNs/FCM dispatch (25.18/25.19). Web push (Safari). |

## 1. Migration `00070001_push.sql` (slot 0007 per README)

```sql
-- +goose Up
CREATE TABLE push_devices (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    platform        TEXT NOT NULL CHECK (platform IN ('ios','ipad','tvos','android','androidtv','web')),
    app_bundle_id   TEXT,                  -- iOS / tvOS bundle distinguishing
    environment     TEXT NOT NULL DEFAULT 'production',  -- 'production' | 'sandbox'
    token_sealed    BYTEA NOT NULL,
    token_hash      BYTEA NOT NULL,        -- SHA-256 of plaintext token; deterministic for upsert
    locale          TEXT,
    channel_id      TEXT,                  -- Android channel id
    app_version     TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at      TIMESTAMPTZ
);
CREATE UNIQUE INDEX push_devices_unique_token_idx
    ON push_devices(user_id, platform, token_hash)
    WHERE revoked_at IS NULL;
CREATE INDEX push_devices_user_idx ON push_devices(user_id);

CREATE TABLE push_outbox (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    device_id     UUID NOT NULL REFERENCES push_devices(id) ON DELETE CASCADE,
    server_id     UUID REFERENCES servers(id) ON DELETE SET NULL,
    user_id       UUID NOT NULL,
    kind          TEXT NOT NULL,
    dedupe_key    TEXT,
    title         TEXT NOT NULL,
    body          TEXT NOT NULL,
    data_json     JSONB,
    priority      INT NOT NULL DEFAULT 10,
    apns_collapse_id TEXT,
    fcm_tag       TEXT,
    enqueued_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    not_after     TIMESTAMPTZ NOT NULL,
    dispatched_at TIMESTAMPTZ,
    failed_at     TIMESTAMPTZ,
    fail_reason   TEXT,
    retries       INT NOT NULL DEFAULT 0,
    in_flight_lock UUID,                   -- claimed by a worker
    locked_until  TIMESTAMPTZ              -- worker claim expiry; replaces the unowned `expired_locks` view
);
CREATE INDEX push_outbox_pending_idx
    ON push_outbox(enqueued_at)
    WHERE dispatched_at IS NULL AND failed_at IS NULL;
CREATE INDEX push_outbox_user_dedupe_idx
    ON push_outbox(user_id, dedupe_key, enqueued_at);

CREATE TABLE push_templates (
    kind            TEXT NOT NULL,
    locale          TEXT NOT NULL,
    title_template  TEXT NOT NULL,
    body_template   TEXT NOT NULL,
    PRIMARY KEY (kind, locale)
);

-- Seed templates (locale fallback chain: <locale> → 'en')
INSERT INTO push_templates VALUES
 ('library.video_ready', 'en', '{library_name}', '{title} is ready to watch'),
 ('library.video_ready', 'ar', '{library_name}', '‎{title} جاهز للمشاهدة'),
 ('library.scan_complete', 'en', 'Maktaba', 'Library scan complete'),
 ('library.scan_complete', 'ar', 'مكتبة', 'اكتمل فحص المكتبة'),
 ('download.complete',  'en', 'Maktaba', 'Download ready'),
 ('download.complete',  'ar', 'مكتبة',  'اكتمل التنزيل'),
 ('system.alert',       'en', 'Maktaba server', 'Server alert'),
 ('system.alert',       'ar', 'خادم مكتبة', 'تنبيه الخادم'),
 ('system.error',       'en', 'Maktaba server', 'Server error'),
 ('system.error',       'ar', 'خادم مكتبة', 'خطأ الخادم'),
 ('family.invite',      'en', 'Maktaba', 'You were added to a family plan'),
 ('family.invite',      'ar', 'مكتبة',  'تمت إضافتك إلى خطة عائلية');

-- +goose Down
DROP TABLE IF EXISTS push_templates, push_outbox, push_devices;
```

## 2. Devices endpoints

```go
// POST /api/push/devices  body: {platform, token, locale, app_bundle_id?, channel_id?}
func registerDevice(s *Service) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        var req registerReq
        decodeJSON(r, &req, 4<<10)
        if !validPlatform(req.Platform) { problem(w, 400, "bad_platform", ""); return }
        if len(req.Token) < 10 || len(req.Token) > 4096 { problem(w, 400, "bad_token", ""); return }
        sealed, err := s.crypto.Seal([]byte(req.Token))
        if err != nil { problem(w, 500, "internal", ""); return }
        // Upsert via unique partial index — if a row exists for (user, platform, token), reactivate.
        id, _ := s.repo.UpsertDevice(r.Context(), currentUserID(r), req, sealed)
        writeJSON(w, 200, map[string]string{"device_id": id.String()})
    }
}

// DELETE /api/push/devices/{id}
func deleteDevice(s *Service) http.HandlerFunc { /* mark revoked_at = now() */ }
```

## 3. Dispatch endpoint

```go
// POST /api/push/dispatch  X-Server-Token: <bearer>
type dispatchReq struct {
    UserID     uuid.UUID         `json:"user_id"`
    Kind       string            `json:"kind"`
    RefID      string            `json:"ref_id"`
    Data       map[string]string `json:"data"`
    DedupeKey  string            `json:"dedupe_key"`
    TTLSeconds int               `json:"ttl_seconds"`
    Priority   int               `json:"priority"`
}

func dispatch(s *Service) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        bearer := r.Header.Get("X-Server-Token")
        sid, ownerUID, ok := s.repo.VerifyBearer(r.Context(), bearer)
        if !ok { problem(w, 401, "invalid_token", ""); return }
        var req dispatchReq
        if err := decodeJSON(r, &req, 16<<10); err != nil { problem(w, 400, "bad_request", ""); return }
        if req.UserID != ownerUID {
            s.abuse.Record(r.Context(), "cross_user_push", &ownerUID, 4)
            problem(w, 403, "not_your_user", ""); return
        }
        if !validKind(req.Kind) { problem(w, 400, "unknown_kind", ""); return }
        if req.TTLSeconds <= 0 || req.TTLSeconds > 7*24*3600 { req.TTLSeconds = 3600 }
        if req.Priority == 0 { req.Priority = 10 }

        // Rate limit per server bearer (25.24 helper)
        if blocked := s.rl.Check(r.Context(), "push_per_server:"+sid.String(), 1000, time.Hour); blocked {
            problem(w, 429, "rate_limit", ""); return
        }

        // Dedup window check
        if req.DedupeKey != "" {
            recent, _ := s.repo.RecentDedupExists(r.Context(), req.UserID, req.DedupeKey, time.Duration(req.TTLSeconds)*time.Second)
            if recent {
                writeJSON(w, 200, map[string]any{"sent": 0, "deduped": true}); return
            }
        }

        // Fetch devices
        user, _ := s.repo.GetUser(r.Context(), req.UserID)
        devs, _ := s.repo.ActiveDevicesForUser(r.Context(), req.UserID)
        if len(devs) == 0 { writeJSON(w, 200, map[string]int{"sent": 0}); return }

        // Render template
        title, body, err := s.tpl.Render(r.Context(), req.Kind, user.Locale, req.Data)
        if err != nil { problem(w, 500, "render_failed", ""); return }

        // Truncate if necessary (APNs payload limit ~4 KB)
        title, body = truncateForAPNS(title, body, 4096)

        // Insert outbox rows; APNs / FCM workers will pick up.
        notAfter := time.Now().Add(time.Duration(req.TTLSeconds)*time.Second)
        for _, d := range devs {
            _, _ = s.repo.InsertOutbox(r.Context(), OutboxRow{
                DeviceID: d.ID, ServerID: sid, UserID: req.UserID,
                Kind: req.Kind, DedupeKey: req.DedupeKey, Title: title, Body: body,
                DataJSON: mergeRefID(req.Data, req.RefID, sid),
                Priority: req.Priority, APNsCollapseID: req.DedupeKey, FCMTag: req.DedupeKey,
                NotAfter: notAfter,
            })
        }
        s.audit(r.Context(), "push.dispatch", req.Kind)
        writeJSON(w, 200, map[string]int{"sent": len(devs)})
    }
}
```

## 4. Template renderer

```go
func (t *Templates) Render(ctx context.Context, kind, locale string, data map[string]string) (string, string, error) {
    row, err := t.repo.Get(ctx, kind, locale)
    if errors.Is(err, ErrNotFound) {
        row, err = t.repo.Get(ctx, kind, "en")  // fallback
    }
    if err != nil { return "", "", err }
    title := substitute(row.TitleTemplate, data)
    body  := substitute(row.BodyTemplate,  data)
    return title, body, nil
}

// substitute replaces {placeholder} with data[placeholder]; unknown placeholders are dropped (no leak).
func substitute(tmpl string, data map[string]string) string {
    return placeholderRe.ReplaceAllStringFunc(tmpl, func(m string) string {
        key := strings.Trim(m, "{}")
        if v, ok := data[key]; ok {
            return strings.ReplaceAll(v, "\n", " ")  // safety
        }
        return ""
    })
}
```

`placeholderRe = regexp.MustCompile(`\{[a-z_][a-z0-9_]*\}`)`.

## 5. Worker outbox drainer (driven by 25.18/25.19)

```go
// cloud/internal/push/outbox.go
type Drainer struct {
    db   *pgxpool.Pool
    apns APNs
    fcm  FCM
}

func (d *Drainer) Run(ctx context.Context) error {
    t := time.NewTicker(500 * time.Millisecond); defer t.Stop()
    for {
        select {
        case <-ctx.Done(): return ctx.Err()
        case <-t.C:
            d.tick(ctx)
        }
    }
}

func (d *Drainer) tick(ctx context.Context) {
    // Atomically claim up to 100 rows.
    workerID := uuid.New()
    rows, _ := d.db.Query(ctx, `
        UPDATE push_outbox o
        SET in_flight_lock = $1
        WHERE o.id IN (
          SELECT id FROM push_outbox
          WHERE dispatched_at IS NULL AND failed_at IS NULL
            AND not_after > now()
            AND (in_flight_lock IS NULL OR in_flight_lock IN (
                SELECT id FROM expired_locks  -- view: rows with stale claim
              ))
          ORDER BY enqueued_at LIMIT 100
        )
        RETURNING o.*, (SELECT platform FROM push_devices WHERE id=o.device_id), (SELECT token_sealed FROM push_devices WHERE id=o.device_id)
    `, workerID)
    ...
    for rows.Next() {
        switch platform {
        case "ios", "ipad", "tvos":  go d.apns.Send(ctx, row)
        case "android", "androidtv", "web": go d.fcm.Send(ctx, row)
        }
    }
}
```

After APNs/FCM resolve, set `dispatched_at` or `failed_at`. Permanent failures (`BadDeviceToken`, FCM 404) → mark device revoked AND mark row failed.

## 6. Test plan

### 6.1 Unit

| Test | Pins |
|---|---|
| `TestPlatformValidation` | unknown → 400. |
| `TestTokenLengthLimits` | <10 or >4096 → 400. |
| `TestPlaceholderSubstitution` | Known replaced; unknown dropped. |
| `TestKindNotInTemplates400` | Unknown kind → 400. |
| `TestLocaleFallbackEN` | `fr` falls back to `en`. |
| `TestRTLArabicRender` | bidi marks preserved. |
| `TestTTLOutsideRange` | 0 or >7d → clamp 3600s. |
| `TestPayloadTruncationAt4KB` | Largest field trimmed. |

### 6.2 Integration

| Test | Pins |
|---|---|
| `Test2DevicesEnqueue2OutboxRows` | Happy path. |
| `TestDedupeWithinWindowSkips` | Second dispatch suppressed. |
| `TestDedupeOutsideWindowAllows` | After window → fresh row. |
| `TestCrossUserPushBlocked` | Server bearer for user A → push for user B → 403 + abuse. |
| `TestRateLimitDispatch1000/h` | 1001st → 429. |
| `TestTokenReregistrationUpsert` | Same token, second register → reactivates row. |
| `TestTokensNeverReturned` | Get device → no `token` field in response. |
| `TestRevokedDeviceSkipped` | Outbox writer skips revoked. |
| `TestUnauthenticated` | Missing bearer → 401. |

## 7. Edge cases — handling table

| Case | Behaviour | Pinned |
|---|---|---|
| Re-registration | Upsert via unique partial index. | `TestTokenReregistration`. |
| Tokens never logged | Audit + logs only carry `device_id`. | Code review. |
| High-volume sender | Rate limit per server + trusted override. | `TestRateLimit`. |
| Backpressure APNs/FCM | Worker outbox; retries handled in 25.18/19. | Cross-story. |
| Stale TTL after restart | `not_after > now()` skip in drain. | `TestDrainSkipsExpired`. |
| Cross-account family push | 403 in v1. | `TestCrossUserPushBlocked`. |
| Templates as data | DB-backed; deployed via migration. | Migration. |
| Variable substitution safety | Named placeholders only; unknown dropped. | `TestPlaceholderSubstitution`. |
| Oversized payload | Truncate body via `truncateForAPNS`. | `TestPayloadTruncationAt4KB`. |
| Encrypted at rest | Token decrypted only in dispatcher. | Crypto layer. |
| Multiple devices same token | Unique partial index keeps one active. | Schema. |
| Bounced email/etc. | Out of scope here. | Spec. |

## 8. Dependencies

- 25.1, 25.6 (server bearer auth), 25.5 (user locale for templates).
- 25.18 / 25.19 (downstream dispatchers).
- 25.24 (rate limit middleware).
- 25.25 (abuse events).

## 9. Acceptance checklist

- [ ] Migration 00070001 applies; templates seeded.
- [ ] All 3 endpoints implemented.
- [ ] Cross-user push blocked with abuse event.
- [ ] Tokens AES-GCM-sealed.
- [ ] Dedup window honored.
- [ ] Template rendering safe + i18n.
- [ ] Outbox claim-by-worker mechanism.
- [ ] Tests in §6 pass.
