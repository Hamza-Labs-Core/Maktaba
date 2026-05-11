# Implementation Plan — Story 25.20 Admin: user & server fleet console

> Companion to [story-25-20-admin-fleet.md](story-25-20-admin-fleet.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Host | `admin.maktaba.app` — separate Cloudflare zone with stricter WAF rules, separate React app in `web/admin/`. |
| Auth | Google Workspace OIDC via `cloud/internal/admin/sso.go`. Only `@hamzalabs.com` allowed. ACR freshness ≤ 5 min for sensitive ops. |
| Router | Separate chi router `cloud/internal/admin/router.go` mounted at `admin.maktaba.app`; not exposed under `/api`. |
| Pages | Users, User detail, Servers, Server detail, Audit, Abuse events. |
| Cross-pod registry RPC | `POST /internal/registry/has`, `POST /internal/registry/force-disconnect` between relay pods; mTLS in cluster network. |
| Out of scope | Role hierarchy (admin vs support) — v1 single role. |

## 1. Migration `00060001_admin_audit_revenue.sql` (slot 0006 per README adds `cloud_audit` indexes + admin tables)

```sql
-- +goose Up
ALTER TABLE cloud_audit
    ADD COLUMN is_admin BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN reason TEXT;

CREATE INDEX cloud_audit_action_idx ON cloud_audit(action, ts DESC);

-- pg_trgm for fast partial-email search
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE INDEX cloud_users_email_trgm_idx ON cloud_users USING gin (email gin_trgm_ops);
CREATE INDEX cloud_servers_subdomain_trgm_idx
    ON cloud_servers USING gin (subdomain gin_trgm_ops) WHERE deleted_at IS NULL;

-- +goose Down
DROP INDEX IF EXISTS cloud_servers_subdomain_trgm_idx, cloud_users_email_trgm_idx, cloud_audit_action_idx;
ALTER TABLE cloud_audit DROP COLUMN IF EXISTS is_admin, DROP COLUMN IF EXISTS reason;
```

## 2. SSO

```go
// cloud/internal/admin/sso.go
type AdminSSO struct {
    oauthCfg *oauth2.Config
    verifier *oidc.IDTokenVerifier  // Google OIDC
    domain   string                 // "hamzalabs.com"
}

func (s *AdminSSO) Callback(w http.ResponseWriter, r *http.Request) {
    // Validate state, exchange code, parse ID token.
    tok, _ := s.oauthCfg.Exchange(r.Context(), r.URL.Query().Get("code"))
    raw, _ := tok.Extra("id_token").(string)
    idt, _ := s.verifier.Verify(r.Context(), raw)
    var c struct{ Email string; Hd string; AtHash string `json:"at_hash"` }
    idt.Claims(&c)
    if !strings.HasSuffix(strings.ToLower(c.Email), "@"+s.domain) {
        s.audit("admin.access_denied", c.Email)
        problem(w, 403, "admin_access_denied", ""); return
    }
    user, _ := s.repo.UpsertAdmin(r.Context(), c.Email)
    sess := s.issueAdminSession(user, time.Now())   // 4h TTL; acr=auth_now
    setAdminCookie(w, sess)
    http.Redirect(w, r, "/", 302)
}
```

ACR freshness: each session carries `last_reauth_at` updated on `POST /admin/reauth` (Google re-prompt). Sensitive endpoints check `now-last_reauth_at <= 5min` else return `401 reauth_required`.

## 3. Routes (excerpt)

```go
router.With(adminSession, adminACL).Group(func(r chi.Router) {
    r.Get("/api/admin/users", listUsers)
    r.Get("/api/admin/users/{id}", userDetail)
    r.Post("/api/admin/users/{id}/suspend", reauthRequired(suspendUser))
    r.Post("/api/admin/users/{id}/unsuspend", reauthRequired(unsuspendUser))
    r.Post("/api/admin/users/{id}/force-verify-email", reauthRequired(forceVerify))
    r.Post("/api/admin/users/{id}/soft-delete", reauthRequired(adminSoftDelete))
    r.Post("/api/admin/users/{id}/export", kickoffExport)   // reuses 25.5 worker
    r.Get("/api/admin/servers", listServers)
    r.Get("/api/admin/servers/{id}", serverDetail)
    r.Post("/api/admin/servers/{id}/suspend", reauthRequired(suspendServer))
    r.Post("/api/admin/servers/{id}/force-disconnect", reauthRequired(forceDisconnect))
    r.Post("/api/admin/servers/{id}/reset-bearer", reauthRequired(resetBearer))
    r.Get("/api/admin/audit", auditSearch)
    r.Get("/api/admin/abuse-events", abuseQueue)
    r.Post("/api/admin/abuse-events/{id}/resolve", resolveAbuse)
})
```

## 4. User detail

```sql
-- Single SELECT with multiple subqueries (cheap with trigram indexes)
SELECT u.*, s.stripe_customer_id,
       (SELECT json_agg(s2) FROM cloud_servers s2 WHERE s2.user_id=u.id) AS servers,
       (SELECT json_agg(s3) FROM cloud_subscriptions s3 WHERE s3.user_id=u.id) AS subs,
       (SELECT json_agg(a) FROM cloud_audit a WHERE a.actor_user_id=u.id ORDER BY a.ts DESC LIMIT 200) AS audit,
       (SELECT json_agg(e) FROM cloud_abuse_events e WHERE e.user_id=u.id ORDER BY e.ts DESC LIMIT 50) AS abuse
FROM cloud_users u WHERE u.id = $1
```

## 5. Suspend user

```go
func suspendUser(s *Service) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        var req struct{ Reason string }
        decodeJSON(r, &req, 4<<10)
        if len(req.Reason) < 5 { problem(w, 400, "reason_required", ""); return }
        uid, _ := uuid.Parse(chi.URLParam(r, "id"))
        // 1. Mark suspended
        _, _ = s.db.Exec(r.Context(), `UPDATE cloud_users SET suspended_at=now() WHERE id=$1`, uid)
        // 2. Revoke sessions
        s.repo.RevokeAllUserSessions(r.Context(), uid)
        // 3. Revoke server tunnels for each linked server
        servers, _ := s.repo.ServersForUser(r.Context(), uid)
        for _, sv := range servers {
            if t, ok := s.registry.Get(sv.ID); ok {
                t.Send(FrameRevoke, nil); t.Close("admin_suspend")
            }
            // multi-pod: RPC
            s.peers.ForceDisconnect(r.Context(), sv.ID)
        }
        // 4. NOTIFY tier_changed
        s.db.Exec(r.Context(), `SELECT pg_notify('tier_changed', $1::text)`, uid.String())
        // 5. Audit
        s.audit(r.Context(), AdminAudit{Action: "admin.suspend_user", TargetID: uid.String(), Reason: req.Reason})
        w.WriteHeader(204)
    }
}
```

## 6. Force-disconnect server

```go
func forceDisconnect(s *Service) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        sid, _ := uuid.Parse(chi.URLParam(r, "id"))
        // Local pod first
        if t, ok := s.registry.Get(sid); ok {
            t.Close("admin_force_disconnect")
            s.audit(r.Context(), AdminAudit{Action: "admin.force_disconnect", TargetID: sid.String()})
            w.WriteHeader(204); return
        }
        // Multicast to peer relay pods.
        ok := s.peers.ForceDisconnect(r.Context(), sid)
        if !ok { problem(w, 504, "registry_uncertain", ""); return }
        s.audit(r.Context(), AdminAudit{Action: "admin.force_disconnect", TargetID: sid.String()})
        w.WriteHeader(204)
    }
}
```

`s.peers` is a small inter-pod client using mTLS + signed identity.

## 7. Audit search

```go
// GET /api/admin/audit?actor=...&action=...&since=...&until=...&cursor=...
func auditSearch(s *Service) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        // parse filters; build parameterized query
        rows, _ := s.repo.SearchAudit(r.Context(), filters, 50)
        // cursor = ts of last row (base64 encoded)
        writeJSON(w, 200, map[string]any{"rows": rows, "next_cursor": nextCursor})
    }
}
```

CSV export:

```go
// GET /api/admin/audit/export?...
func auditExport(s *Service) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        if !s.acr(r, 5*time.Minute) { problem(w, 401, "reauth_required", ""); return }
        w.Header().Set("Content-Type", "text/csv")
        w.Header().Set("Content-Disposition", `attachment; filename="audit.csv"`)
        cw := csv.NewWriter(w)
        cw.Write([]string{"ts","actor","action","target","payload"})
        s.repo.StreamAudit(r.Context(), filters, func(row AuditRow){
            cw.Write([]string{row.TS.Format(time.RFC3339), row.Actor.String(), row.Action, row.Target, row.PayloadJSON})
        })
        cw.Flush()
    }
}
```

## 8. Front-end

`web/admin/` is a separate Vite + React app sharing UI primitives with `web/`. Uses `useQuery` against the admin API, identical patterns to the main app but no Stripe/Auth views.

## 9. Test plan

### 9.1 Unit

| Test | Pins |
|---|---|
| `TestACLNonHamzaLabsDomain` | other email → 403. |
| `TestACRFreshnessRequired` | stale `last_reauth_at` → 401. |
| `TestSearchSQLEscapes` | `' OR 1=1` in search → no leak. |

### 9.2 Integration

| Test | Pins |
|---|---|
| `TestSearchByPartialEmail` | "alice@" → matches via trigram, < 200ms. |
| `TestSuspendRevokesSessionsAndTunnels` | Servers receive revoke; registry cache purged. |
| `TestForceDisconnectMultiPod` | Local registry empty; RPC to peers; one returns ok. |
| `TestAuditCSVExportSignedURL` | URL TTL 24h; content matches DB. |
| `TestConcurrentSuspendVsAdmin` | Two operators suspend same user → one wins, second 409 with state. |
| `TestKeyboardOnlyOperator` | All actions keyboard reachable (a11y). |

## 10. Edge cases — handling table

| Case | Behaviour | Pinned |
|---|---|---|
| Off-boarded operator | Manual SQL revoke; SCIM v2. | Doc. |
| SQL injection in search | Parameterized queries; linter enforces. | `TestSearchSQLEscapes`. |
| PII in CSV exports | Signed URL TTL 24h; `confidential` watermark. | Implementation. |
| Action confirmation | Type-the-target before button enable. | UI. |
| Mass-suspend bulk ops | Out for v1; queue UI deferred. | Spec. |
| Read-only vs write admin roles | v1 single role; v2 by Workspace group. | Spec. |
| Admin telemetry | Every page view audited. | Audit log integration. |
| Server detail liveness | RPC timeout → "uncertain" surfaced. | UX. |

## 11. Dependencies

- 25.1, 25.2 (sessions), 25.6/25.7/25.8 (registry, bearer revoke), 25.11 (usage), 25.14 (sub state), 25.25 (abuse events table).

## 12. Acceptance checklist

- [ ] Migration 00060001 applies; trigram indexes.
- [ ] SSO @hamzalabs.com gating.
- [ ] ACR freshness on sensitive actions.
- [ ] User & server suspend / force-disconnect / reset-bearer flows.
- [ ] Audit search + CSV export.
- [ ] Concurrent operator handling.
- [ ] Tests in §9 pass.
