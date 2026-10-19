# Plan 34.7 — Access modes — implementation

> Implementation plan for [story-34-07](story-34-07-access-modes.md). Design: [design.md](design.md) §7.

## 0. Decisions

| # | Decision | Rationale |
|---|---|---|
| D1 | Default `turnstile`. | Stops scripted abuse of paid minutes with no sign-up; guide stays open and indexable if wanted. |
| D2 | Turnstile once per browser session → signed cookie (12 h). | Turnstile tokens are single-use and valid 5 min; a cookie avoids a challenge per play. |
| D3 | `access` uses Cloudflare Access on the hostname; Worker re-verifies the JWT. | Invite-only without building accounts; defence in depth. |
| D4 | `accounts` uses Epic 25 cloud identity, phase D. | The only identity reachable without the home. |

## 1. Files

**Create** (`web/tv-site/worker/src/`)
- `access/turnstile.ts` (siteverify with `remoteip`, `idempotency_key`; cookie issue/verify)
- `access/cfaccess.ts` (JWT verify against `https://<team>.cloudflareaccess.com/cdn-cgi/access/certs`, `aud`)
- `access/accounts.ts` (OIDC code + PKCE flow, ID token verify, allow-list check)
- `access/index.ts` (mode switch used by `/api/v1/*` and HTML routes)

**Create** (Go)
- `api/internal/tvpublic/deploy_access.go` — Turnstile widget create/update (hostnames, mode managed),
  Access app + policy (emails, One-time PIN), delete on mode change

**Modify**
- `web/tv-site/src/components/Gate.tsx` — Turnstile widget (explicit render), sign-in buttons
- Epic 25 cloud — register the site as an OAuth client (phase D)

## 2. Steps

1. `public` and `turnstile` (with Cloudflare test keys in CI).
2. Deploy-side widget management; cookie key in Worker secrets (`SESSION_KEY`).
3. `access` mode: app/policy via API; JWT verification; whole site gated.
4. `accounts` mode (phase D): OIDC client, allow-list publishing from the home.

## 3. Tests

Story TCs.

## 4. Rollout

Phase B: `turnstile`. Phase D: `access` for a private beta, `accounts` when the cloud OAuth is ready.
