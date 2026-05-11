# Implementation Plan — Story 25.25 Abuse detection & response

> Companion to [story-25-25-abuse-detection.md](story-25-25-abuse-detection.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Detectors | Live (request-time) + batch (cron) detectors writing `cloud_abuse_events`. |
| Scoring | Per-actor (user_id or server_id) decaying score over 90 days. |
| Responses | Suspension, rate halving, hot-link mode. |
| Endpoints | Internal `s.abuse.Record(ctx, kind, actor, severity)` + admin REST under `/api/admin/abuse-events`. |
| Out of scope | CAPTCHA, geo-blocking. |

## 1. Migration `00080002_abuse_events.sql` (slot 0008 extension)

```sql
-- +goose Up
CREATE TABLE cloud_abuse_events (
    id           BIGSERIAL PRIMARY KEY,
    ts           TIMESTAMPTZ NOT NULL DEFAULT now(),
    kind         TEXT NOT NULL,
    severity     INT NOT NULL,
    user_id      UUID,
    server_id    UUID,
    ip_block     TEXT,
    payload      JSONB NOT NULL,
    resolved_at  TIMESTAMPTZ
);
CREATE INDEX cloud_abuse_events_kind_ts_idx ON cloud_abuse_events(kind, ts DESC);
CREATE INDEX cloud_abuse_events_user_ts_idx ON cloud_abuse_events(user_id, ts DESC);
CREATE INDEX cloud_abuse_events_open_idx ON cloud_abuse_events(resolved_at) WHERE resolved_at IS NULL;

-- +goose Down
DROP TABLE IF EXISTS cloud_abuse_events;
```

## 2. Detector inventory

| Detector | Trigger | Severity |
|---|---|---|
| `relay_host_abuse` | >100 unknown-path requests in 5min from one server | 2 |
| `bandwidth_anomaly` | hourly bw > 10× 7-day MA AND > 5 GB | 3 |
| `port_scan_via_relay` | >50 distinct paths/IP/60s | 3 |
| `claim_token_brute` | >10 invalid claim attempts/min/IP (already in 25.6/25.24) | 2 |
| `oauth_state_mismatch` | bad state cookie | 2 |
| `refresh_token_replay` | rotated token reused | 4 |
| `signup_velocity` | >10 signups/5min from one /24 | 3 |
| `payment_chargeback` | Stripe dispute.created | 5 |
| `tunnel_flap` | >100 reconnects/hour | 2 |
| `stream_path_oddity` | path > 1 KiB or unusual shape | 2 |
| `cross_user_push` | server pushing to non-owner | 4 |
| `stripe_signature_forgery` | bad webhook sig | 4 |
| `lan_probe_id_mismatch` | client-reported server-id mismatch | 3 |
| `tier_circuit_breaker` | >200% cap | 4 |

## 3. Live detector hooks

The hooks are inline in the original modules; this story owns the *recording* utility and the *scoring*.

```go
// cloud/internal/abuse/recorder.go
type Recorder struct {
    db   *pgxpool.Pool
}

func (r *Recorder) Record(ctx context.Context, kind string, actor *Actor, severity int, payload map[string]any) {
    safe := sanitizePayload(payload)
    _, _ = r.db.Exec(ctx, `
        INSERT INTO cloud_abuse_events(kind, severity, user_id, server_id, ip_block, payload)
        VALUES($1,$2,$3,$4,$5,$6)
    `, kind, severity, actor.UserID, actor.ServerID, actor.IPBlock, safe)
}

func sanitizePayload(p map[string]any) []byte {
    // Strip query strings, full paths beyond shape, request bodies.
    sanitized := map[string]any{}
    for k, v := range p {
        switch k {
        case "method", "status", "kind", "version", "ua_class", "path_shape":
            sanitized[k] = v
        }
    }
    b, _ := json.Marshal(sanitized); return b
}
```

`path_shape` = `/api/libraries/[id]/videos` (with placeholders) rather than the raw path so user content isn't stored.

## 4. Relay-host-abuse detector (batch, every 60s)

```go
func DetectRelayHostAbuse(ctx context.Context, db *pgxpool.Pool, prom Promscope, rec *Recorder) {
    // Query Prom for unknown-path counters per server over last 5min.
    rows := prom.QueryRange("sum by (server_id) (rate(relay_unknown_path_total[5m]))", 5*time.Minute)
    for r := range rows {
        if r.Value*300 > 100 {  // 100+ unknown paths over 5min window
            rec.Record(ctx, "relay_host_abuse", &Actor{ServerID: &r.ServerID}, 2, map[string]any{"count": int(r.Value*300)})
        }
    }
}
```

The relay (25.9) emits `relay_unknown_path_total{server_id}` whenever the response is 404 with a path not matching known shapes.

## 5. Bandwidth-anomaly detector (cron hourly)

```go
func DetectBandwidthAnomaly(ctx context.Context, db *pgxpool.Pool, rec *Recorder) {
    rows, _ := db.Query(ctx, `
        WITH last_hour AS (
            SELECT server_id, SUM(bytes_in + bytes_out) AS lh
            FROM cloud_bandwidth_daily
            WHERE date = current_date
            GROUP BY server_id
        ),
        avg7 AS (
            SELECT server_id, AVG(bytes_in + bytes_out) AS mean
            FROM cloud_bandwidth_daily
            WHERE date >= current_date - 7
            GROUP BY server_id
        )
        SELECT l.server_id, l.lh, a.mean
        FROM last_hour l JOIN avg7 a USING (server_id)
        WHERE l.lh > GREATEST(a.mean * 10, 5e9)`)
    for rows.Next() { /* Record abuse + flip hot-link mode */ }
}
```

`bandwidth_daily` is daily granularity; hourly tracking is in Redis: extend the meter (25.11) to keep a 24-key rolling hour count (`bw:hourly:{sid}:{hour}`). Detector reads from there.

## 6. Scoring

```go
// cloud/internal/abuse/scorer.go
func ScoreFor(ctx context.Context, db *pgxpool.Pool, actorRef string) (int, error) {
    var total float64
    err := db.QueryRow(ctx, `
        SELECT COALESCE(SUM(severity * EXP(-LN(2) * EXTRACT(EPOCH FROM (now() - ts))/(7*86400))), 0)
        FROM cloud_abuse_events
        WHERE (user_id::text = $1 OR server_id::text = $1)
          AND ts >= now() - INTERVAL '90 days'
    `, actorRef).Scan(&total)
    if err != nil { return 0, err }
    return int(math.Round(total)), nil
}
```

Half-life: weekly (decay = `2^(-Δd/7)`).

## 7. Response actions

```go
func (r *Responder) OnEvent(ctx context.Context, ev AbuseEvent) {
    score, _ := ScoreFor(ctx, r.db, ev.Actor())
    switch {
    case score >= 50:
        r.suspend(ctx, ev.Actor(), "abuse_threshold")
    case score >= 25:
        r.halveRateLimits(ctx, ev.Actor())
    case score >= 10:
        r.notifyOps(ctx, ev)
    }
    if ev.Kind == "bandwidth_anomaly" {
        r.enableHotLinkMode(ctx, ev.ServerID)
    }
    if ev.Kind == "chargeback" {
        r.suspend(ctx, ev.Actor(), "chargeback")
    }
}
```

### 7.1 Hot-link middleware

```go
// cloud/internal/relay/hotlink.go
func HotLinkMiddleware(repo *Repo) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            sub := hostSubdomain(r.Host)
            if !isStreamPath(r.URL.Path) { next.ServeHTTP(w, r); return }
            if !repo.HotLinkActive(sub) { next.ServeHTTP(w, r); return }
            if !validReferer(r.Header.Get("Referer"), sub) && !hasInAppToken(r) {
                w.Header().Set("Content-Type", "application/json")
                writeJSON(w, 451, map[string]string{"error":"hot_link_blocked","reset_url": "https://app.maktaba.app/r/"+sub})
                return
            }
            next.ServeHTTP(w, r)
        })
    }
}
```

`HotLinkActive` is a Redis flag per subdomain (set on detection, cleared via user "reset" button or admin).

## 8. Admin queue

```go
// GET /api/admin/abuse-events?status=open&kind=...
// POST /api/admin/abuse-events/{id}/resolve  body: {action:"dismiss"|"clear_score"|"suspend"|"unsuspend", reason}
```

Resolve actions touch the response engine accordingly; audit all.

## 9. Test plan

### 9.1 Unit

| Test | Pins |
|---|---|
| `TestPathShapeSanitization` | `/api/libraries/abc-123/videos?key=...` → `/api/libraries/[id]/videos`. |
| `TestPayloadSanitizationDropsBody` | Body fields absent. |
| `TestScoreDecayWeekly` | Severity 4 event 14 days ago contributes ~1.0. |
| `TestThresholds10/25/50` | Score above each fires correct action. |

### 9.2 Integration

| Test | Pins |
|---|---|
| `TestRelayHostAbuseFires` | 200 unknown paths/5min → row recorded. |
| `TestBandwidthAnomalyHotLink` | 10× spike → hot-link middleware blocks. |
| `TestRefreshReplaySeverity4` | abuse row severity=4 + sessions revoked. |
| `TestChargebackSuspends` | dispute.created → suspend + abuse row severity=5. |
| `TestSuspensionLiftCascades` | Admin clears → score reset, suspension off. |
| `TestHotLinkUserReset` | User clicks reset → flag cleared; legit deep-link works. |
| `TestAuditStorageRetention` | 91-day-old unresolved row dropped; resolved retained 365d. |
| `TestAdminSelfAbuseSafeguards` | Mass-suspend requires type-the-count confirm (UI). |

## 10. Edge cases — handling table

| Case | Behaviour | Pinned |
|---|---|---|
| False positives | User reset button; admin override. | `TestHotLinkUserReset`. |
| Privacy of abuse payload | Sanitized shape only. | `TestPayloadSanitizationDropsBody`. |
| Email-link breakage | Hot-link rejects; user can opt out in settings. | UX. |
| DDoS reflection via push | Push ingest rate-limited (25.24). | Cross-story. |
| Geo anomalies | Out for v1. | Spec. |
| Account share | Tier 25.12 handles bandwidth; not abuse. | Spec. |
| Audit storage cost | 90d unresolved / 365d resolved retention. | Implementation. |
| Admin self-abuse | Type-the-count UI guard. | UX. |
| Hot-link backpressure | User dashboard exposes a reset button. | UI. |

## 11. Dependencies

- 25.6, 25.14, 25.17, 25.20 (abuse queue UI), 25.24 (rate limits + overrides).

## 12. Acceptance checklist

- [ ] Migration 00080002 applies.
- [ ] `Recorder.Record` used at all detection sites in 25.* stories.
- [ ] Live + batch detectors implemented.
- [ ] Scoring + thresholds + responses.
- [ ] Hot-link middleware toggleable per subdomain.
- [ ] Admin queue endpoints.
- [ ] Tests in §9 pass.
