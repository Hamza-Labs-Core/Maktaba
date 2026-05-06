# Story 25.20 — Admin: user & server fleet console

> Epic 25 · Cloud relay · Phase 5 (admin)

## Description

The HamzaLabs operator console — searchable, filterable view of every
cloud user and every linked server. The single most important screen
when something is on fire ("user X says relay is broken; let me look
at their server's tunnel state, last 50 audit events, recent abuse
flags").

Auth:

- Mounted at `https://admin.maktaba.app` — separate hostname,
  separate Cloudflare zone with stricter WAF rules.
- SSO via Google Workspace `@hamzalabs.com` only. No password
  fallback. Sessions are 4h; every sensitive action requires
  re-auth (≤ 5 min freshness via `acr` claim).
- IP allow-list: HamzaLabs office + designated remote IPs;
  out-of-band approvals for travel.
- Every admin action writes a `cloud_audit` row with
  `actor_user_id` (the staff member's user id) and a flag
  `is_admin=true`.

Pages:

1. **Users.** Search by email, name, Stripe customer id, or
   user id. Columns: email, plan, created, last login, # of
   servers, MTD bandwidth GB, abuse-score. Click → user detail.
2. **User detail.** Profile (read-only), servers (mini list),
   subscriptions (Stripe link), audit (last 200 rows),
   abuse events (open + resolved), buttons:
   - Suspend / unsuspend
   - Force re-verify email
   - Reset MFA (v2 stub)
   - Send custom email
   - Export GDPR ZIP (kicks 25.5 export job)
   - Soft-delete (admin-initiated, with reason)
3. **Servers.** Search by subdomain, server id, owner email,
   version. Columns: subdomain, owner, version, online, last
   seen, MTD GB. Click → server detail.
4. **Server detail.** Owner, tunnel state (live registry
   query), version, last seen, claim history, recent
   bandwidth (chart 30d), audit (tunnel.connect /
   tunnel.disconnect events), buttons:
   - Suspend
   - Force-disconnect (closes current tunnel; server
     reconnects in seconds)
   - Reset bearer (revoke token; user must re-claim)
   - Move to a different region (v2 stub)
5. **Audit log.** Global search across `cloud_audit` with
   filters by actor, action, time, target. CSV export.
6. **Abuse events.** Open queue: ungrouped, sortable by
   severity, kind, age. Click → resolve / escalate.

Performance:

- Indexes for fast search:
  `cloud_users(email gin_trgm)`,
  `cloud_users(stripe_customer_id)`,
  `cloud_servers(subdomain)`,
  `cloud_audit(ts DESC, actor_user_id)`.
- Pagination: cursor-based, 50 rows / page.
- Live tunnel queries are 1 RPC to the relay pods (multicast,
  best-effort) with a 250ms deadline; UI shows "tunnel state
  uncertain" if the deadline lapses.

## Acceptance criteria

- **Given** a HamzaLabs operator with valid SSO,
  **when** they open `https://admin.maktaba.app`,
  **then** the user-search page loads within 1s.
- **Given** a non-`@hamzalabs.com` Google account,
  **when** they attempt admin SSO,
  **then** the response is `403 admin_access_denied` and
  an audit row records the attempt.
- **Given** an operator searches "alice@",
  **when** the query runs,
  **then** matching users are returned within 200ms p95.
- **Given** an operator clicks "Suspend" on a user,
  **when** they confirm with a reason,
  **then** `cloud_users.suspended_at = now()`, the reason
  is in `cloud_audit`, all sessions revoked, all servers
  receive tunnel `revoke`, billing left untouched (Stripe
  cancels separately).
- **Given** an operator force-disconnects a server,
  **when** the action posts,
  **then** the relay closes the tunnel within 1s; the
  audit shows `actor=staff_user_id, action=server.force_disconnect`.
- **Given** an operator views an audit log filter
  `actor=user_X, action=auth.login`,
  **when** the page renders,
  **then** the rows are paginated, oldest-first, ≤ 50 rows
  per page.
- **Given** a sensitive action (suspend, soft-delete,
  reset bearer) is posted with an `acr` claim older than
  5 min,
  **when** the API checks freshness,
  **then** the response is `401 reauth_required` with a
  step-up flow.

## Test cases

| ID  | Type        | Setup | Action | Expected |
|-----|-------------|-------|--------|----------|
| T01 | unit        | non-hamzalabs.com email | check ACL | denied |
| T02 | integration | search by partial email | run | matches via trigram |
| T03 | integration | suspend user | observe | sessions revoked, audit, tunnels closed |
| T04 | integration | force-disconnect | observe | tunnel closed, registry empty |
| T05 | unit        | acr freshness | submit stale | 401 |
| T06 | a11y        | keyboard-only operator | navigate | all actions reachable |
| T07 | regression  | SQL injection in search box | submit `' OR 1=1` | escaped, no leak |
| T08 | integration | CSV export of audit | request | file written, manifest valid |
| T09 | regression  | concurrent suspend by two operators | both POST | one wins, second 409 with current state |
| T10 | unit        | live tunnel RPC timeout | observe | "uncertain" state shown gracefully |

## Edge cases

- **Operator handoff.** If an operator's account is
  off-boarded, all their admin sessions need revocation. We
  drive this from Workspace's SCIM provisioning — out
  for v1; manual offboarding via SQL is acceptable
  short-term.
- **Cross-tenancy leakage.** All admin queries are
  parameterized; no user-supplied raw SQL fragments. Linter
  enforces.
- **PII in CSV exports.** Filenames and audit payloads may
  contain PII; we tag CSVs as `confidential` and they
  expire (signed URL TTL = 24h).
- **Action confirmation.** Destructive actions require
  typing the user's email or server's subdomain before the
  button is enabled (defense against fat-finger).
- **Background bulk operations.** Mass suspend (e.g.,
  rolling out a security patch) is a separate runbook
  feature; UI lets you queue rather than execute at request
  time.
- **Read-only admins.** Two roles: `admin` (read+write),
  `support` (read+limited-write: suspend, send email, no
  bearer reset). Ditto resolved by Workspace group
  membership.
- **Admin telemetry.** All admin views log click trails to
  `cloud_audit`; useful for "who saw what when" — typically
  required by support compliance.
- **Server detail liveness.** Live data merge with cached
  data; if a relay pod is unreachable, the page surfaces
  the cached `last_seen_at` only.

## Files / packages

- `cloud/internal/admin/router.go` — separate router,
  separate origin.
- `cloud/internal/admin/users.go`,
  `cloud/internal/admin/servers.go`,
  `cloud/internal/admin/audit.go`,
  `cloud/internal/admin/abuse.go`.
- `cloud/internal/admin/sso.go` — Workspace OIDC.
- `web/admin/` — separate React app (or server-rendered
  pages; v1 uses simple Vite + React, deployed under
  `admin.maktaba.app`).

## Open questions

- **Read-replicas for admin queries.** Currently same
  primary; if heavy read load shows, add a replica.
- **Self-service support tooling.** Letting users
  retrieve "what did support do to my account?"
  visibility — defer; v1 admin actions are noted only
  in `cloud_audit`.
