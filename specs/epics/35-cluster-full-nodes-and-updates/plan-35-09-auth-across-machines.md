# Plan 35.9 — Auth across machines — implementation

> Implementation plan for [story-35-09-auth-across-machines](story-35-09-auth-across-machines.md). Design: [design.md](design.md) §10.

## 0. Decisions

| # | Decision | Rationale |
|---|---|---|
| D1 | Shared sessions resolved by the home, not redirect-to-home. | Keeps "open any machine"; nodes keep Epic 31 least privilege. |
| D2 | Per-node HMAC delegation key delivered over the v2 channel. | The channel already authenticates both sides; HTTP proxying (WS, SSE, uploads) stays plain HTTP. |
| D3 | 30 s positive / 5 s negative cache, pushed revocation; with the home gone, the node's copy of the home's database answers (Epic 37), the home not asked per request after 30 s. | Bounded staleness without flooding the home. |
| D4 | Host-only cookies, unchanged attributes and CSRF scheme. | Each machine is its own origin; no CORS changes. |
| D5 | Cluster sign-in by top-level redirects with PKCE and single-use codes. | Standard, phishing-resistant enough for a LAN, no cross-origin cookies. |

## 1. Files

**Create**
- `api/internal/cluster/delegation/sign.go` — HMAC sign/verify, replay cache (`_test.go`, vectors).
- `api/internal/cluster/delegation/keys.go` — issue/rotate/revoke in `home_secrets`, `keys` frame.
- `api/internal/handlers/cluster/delegate.go` — `/api/cluster/delegate/introspect`.
- `api/internal/handlers/cluster/sso.go` — `authorize`, `exchange` (home), `callback` (node).
- `api/internal/auth/middleware/delegated.go` — node-side resolver with cache and JWKS verify;
  home-side acceptance of `X-Maktaba-Via: node` + signature + `X-Maktaba-Delegated-Session`.
- `shared/testdata/cluster/delegation_vectors.json`.
- `shared/db/migrations/<S2>_cluster_sessions.sql` (+ `.sqlite.sql`), slot next free at implementation time.
- `web/src/components/ClusterSignIn.tsx` (+ test), i18n keys en/ar.

**Modify**
- `api/internal/auth/sessions/sessions.go` — `origin_node_id`, `parent_session_id`, `RevokeChildren`.
- `api/internal/handlers/auth/auth.go`, `selfservice.go`, `usersadmin.go` — call `cluster.NotifyRevoked`
  on every revocation; honour `X-Maktaba-Origin-Node` from a signed node.
- `api/internal/handlers/auth/csrf.go` — compare against `csrf_sha256` on nodes.
- `api/internal/cluster/agent/hub.go` — send `keys` after `welcome`; broadcast `auth.revoked` to
  machine agents.
- `cmd/maktaba-server/internal/machineagent/frames.go` — forward `keys` and `auth.revoked` to the API.
- `api/internal/httpsec/cookies.go` — no change in attributes; a test pins them for proxied responses.

## 2. Steps

1. Vectors and sign/verify (TC1, TC2).
2. Key issue/rotation in the hub and storage; migration S2.
3. Introspection endpoint on the home; node resolver with caches and JWKS (TC3).
4. Revocation fan-out from every path; fail-closed timer (TC3, TC4).
5. Login through a node (proxied auth routes, origin tag, cookie, CSRF) (TC5).
6. Cluster sign-in flow and UI (TC6, TC7).

## 3. Tests

Story TCs; a security test suite: forged node id, missing signature, body swapped after signing,
session id of another user, SSO code replay, absolute `return` URL, code used by another node.

## 4. Rollout

Phase C, before 35.10's `home` proxy is switched on for authenticated routes. Cluster sign-in ships
behind `rules.cluster.sso` (default on) so it can be switched off if a network breaks redirects.
