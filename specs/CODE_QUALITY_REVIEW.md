# Maktaba — Comprehensive Code Quality Review

**Date:** 2026-06-10
**Branch reviewed:** `main` @ `11d3b96` (pulled latest before review)
**Scope:** Full repository — Go (`api/`, `streaming/`, `cloud/`, `cmd/`, `shared/`, `tools/`), Python (`pipeline/`), Web (`web/`), Native (`apps/`), and Infrastructure (Dockerfiles, compose, CI, Makefile, migrations).

This document records **every** finding (all severities). CRITICAL and HIGH issues that
were safe to fix mechanically have been fixed in the same change set; see
[§ Fixes applied](#fixes-applied). Findings that require deliberate, test-backed
implementation are documented with a remediation plan rather than blind-patched.

---

## Executive summary

The codebase is in **good** overall health. Linters, type checkers, formatters and
test suites pass green across every actively-gated module:

| Toolchain | Result |
|-----------|--------|
| `golangci-lint` — api / streaming | ✅ 0 issues |
| `go vet` — api / streaming / cloud / cmd | ✅ clean |
| `go test` — api / streaming / cloud | ✅ pass |
| Python `ruff check` / `ruff format` | ✅ clean / 222 files formatted |
| Python `mypy --strict src/` | ✅ no issues in 103 files |
| Python `pytest -m unit` | ✅ 394 passed |
| Web `tsc --noEmit` / `eslint --max-warnings=0` | ✅ clean |
| Web `pnpm build` / `vitest run` | ✅ build OK / 128 tests pass (22 files) |
| tvOS `swift build` | ✅ Build complete |
| i18n EN ↔ AR key parity | ✅ 391 ↔ 391, zero drift |
| Dockerfiles / compose / CI YAML / Makefile | ✅ valid; Go base images match `go.mod` 1.25 |
| SQL injection scan (api/cloud/streaming) | ✅ none — all dynamic SQL uses placeholders / whitelisted columns |
| Hardcoded-secret scan | ✅ none |

**The single biggest structural finding:** the entire **`cloud/` control-plane module
(auth, billing, relay) was excluded from CI lint/vet/test/coverage gating.** It is absent
from the `GO_MODULES` list in the `Makefile` and had no `golangci-lint` step in
`.github/workflows/_lint.yml`. This is *why* a cluster of real issues (a crashing OAuth
path, a fragile billing route, an account-linking weakness, dead code, and 31 lint
violations) accumulated unnoticed. The lint half of this gap is now closed.

No CRITICAL issues were found. No SQL injection, no hardcoded credentials, auth fails
closed, the Stripe webhook verifies HMAC signatures, and the admin-token bypass is opt-in
with a minimum-length check.

---

## Findings by severity

### CRITICAL
_None._

### HIGH

#### H1 — Google OAuth `id_token` verifier wired as `nil` → panic on every callback ✅ mitigated
`cmd/maktaba-cloud/role_api.go:67` constructs the Google flow with
`oauth.NewGoogleFlow(cfg, nil)`. `internal/auth/oauth/google.go` then calls
`g.Verifier.Verify(ctx, tr.IDToken)` with **no nil-guard and no default verifier**. When an
operator enables Google OAuth (`OAuthGoogle.ClientID != ""`), every callback dereferences a
nil interface → `panic` (recovered to a 500). Google login is therefore **completely broken
whenever the feature is enabled**. Fails safe (no auth bypass) but is broken functionality.
- **Fix applied:** added an explicit nil-guard in `Exchange` that returns
  `"google oauth: id_token verifier not configured"` instead of panicking.
- **Remaining work (tracked, not auto-fixed):** wire a real JWKS-backed `GoogleVerifier`
  in `role_api.go` before enabling Google OAuth in production. Hand-rolling a verifier was
  out of scope for a mechanical review pass — it needs its own tests and live-key fixtures.

#### H2 — Duplicate `/v1/billing/webhook` route relied on chi ordering to dodge auth ✅ fixed
`internal/handlers/billing/billing.go` `Mount()` registered `POST /v1/billing/webhook`
**inside** the `RequireUser` group, while `role_api.go` *also* registers it **outside** the
group (correct, since Stripe carries no bearer token). chi's `Group` shares one routing
tree and silently lets the **last** registration win, so billing worked **only** because
`role_api.go` mounts the unauthenticated handler after `billingh.Mount`. A reorder — or a
future chi version that changes dedup semantics — would route Stripe through `RequireUser`
→ `401` → silent, retry-exhausting billing breakage. Verified empirically (chi v5.3.0
does not panic; outer registration wins).
- **Fix applied:** removed the in-group registration from `Mount()`; the webhook is now
  mounted exactly once, unauthenticated, in `role_api.go`, with an explanatory comment.

#### H3 — `cloud/` control-plane excluded from all CI Go gating ✅ lint half closed
`GO_MODULES` in the `Makefile` (used by `lint-go`, `test-unit-go`, and `test-coverage`)
**omits `cloud`**, and `_lint.yml` had `golangci-lint` steps only for `api`, `streaming`,
and `shared/health/go`. The result: the security-critical control-plane (OAuth, Stripe
billing, relay tunnel, admin) had **no lint, vet, format, test, or coverage gate**. This is
the root cause of H1, H2, M1, M2, and the M5 coverage gap below.
- **Fix applied:** added `cloud/.golangci.yml` (mirrors the tuned api/streaming config) and
  a `golangci-lint (cloud)` step to `.github/workflows/_lint.yml`. cloud is now lint-clean
  (0 issues) and gated.
- **Remaining work (tracked):** cloud is **not** yet in the test/coverage gate. Many cloud
  packages sit at 0 % statement coverage (handlers, billing webhook, OAuth federation), so
  wiring it into `GO_MODULES`/the coverage-floor ratchet requires writing tests first — a
  dedicated follow-up, not a safe blind change to `main`. See M5.

### MEDIUM

#### M1 — 31 `golangci-lint` violations in `cloud/` (under the team's standard config) ✅ fixed
With the team config now applied, cloud reported 31 issues: 2 genuine dead-code symbols
(`oauth.bigFromBytes`, `account.emailChangeReq`), 3 `errcheck` (`tx.Rollback`/`srv.Shutdown`
on defers), 3 `forbidigo` (`fmt.Print*` in CLI version output), 1 `QF1001`, ~18 `revive`
(unused parameters, builtin shadowing `max`/`cap`, an uncommented blank import), and 3 gofmt
files. **All fixed** (dead code removed, errors explicitly discarded on defers, version
output routed through `fmt.Fprintf(os.Stdout, …)`, params renamed to `_`, shadows renamed).
cloud now lints clean. *Severity is MEDIUM/LOW per the rubric but fixing them was the
prerequisite to gating cloud in CI (H3).*

#### M2 — OAuth account auto-link by **unverified** email (account-takeover vector) ✅ fixed
`internal/handlers/auth/oauth.go` `federate()` linked an external identity to an existing
local account purely by matching `id.Email`, **without checking `id.EmailVerified`**. A
provider (or relay/alias) that returns an unverified address matching a victim's account
would let an attacker link their identity and log in as the victim.
- **Fix applied:** the email-linking branch is now gated on `id.EmailVerified`. Unverified
  identities fall through to creating a fresh account (fail-safe). Google sets
  `EmailVerified` from its verified token; Apple from the id_token payload — so the common
  Google path is unaffected.

#### M3 — Apple `id_token` signature is not JWKS-verified (defense-in-depth gap)
`internal/auth/oauth/apple.go` `Exchange()` decodes the id_token payload **without verifying
the ES256 signature** (it self-documents this: *"production wires a JWKS-backed verifier"*).
**Not currently exploitable:** the token is fetched server-side from
`https://appleid.apple.com/auth/token` over TLS in exchange for an auth `code` + our
`client_secret`, so it is not attacker-supplied (OIDC §3.1.3.7 permits skipping local
verification in the code flow). Still, JWKS verification should be wired before GA as
defense-in-depth and to support any future native/implicit flow.
- **Remediation:** implement a shared JWKS verifier (reused by both Google and Apple),
  with key caching and `kid`/`alg`/`iss`/`aud`/`exp` checks.

#### M4 — Apple OAuth callback performs no `state`/CSRF validation
`GoogleCallback` validates `state` against the `oauth_state` cookie; `AppleCallback`
(`form_post` mode) reads only `code` and never checks `state`. Login-CSRF risk is reduced
because the native SDK initiates the flow, but the callback should still validate a
signed/stored `state` to be consistent with the Google path.
- **Remediation:** propagate and verify `state` (or a nonce) through the Apple flow.

#### M5 — Untested payment / auth / control-plane paths in `cloud/`
There are **no `_test.go` files** for the billing or OAuth *handlers*, and many cloud
packages report 0 % statement coverage. The Stripe webhook, `federate()`, and the admin
endpoints — all security- or money-sensitive — are exercised only indirectly. Combined with
H3 (no CI test gate), regressions here would ship silently.
- **Remediation:** add handler-level tests (webhook signature accept/reject + idempotency,
  `federate` link/create matrix, `RequireAdmin` allow/deny), then add `cloud` to the test +
  coverage-floor gate.

#### M6 — Password-reset token logged in cleartext
`api/internal/handlers/auth/selfservice.go:182` logs the raw `reset_token` at INFO
(*"surface the token to the operator log so a self-hoster can complete the flow"*). Intended
for mailer-less self-hosts, but it places a password-reset credential in logs — anyone with
log access (or any shipped/aggregated log pipeline) can reset any user's password.
- **Remediation:** gate this behind an explicit `dev`/`no-mailer` flag, log only a truncated
  reference, or emit the token to a separate operator-only channel.

#### M7 — `RequestEmailChange` is a success-returning no-op stub
`cloud/internal/handlers/account/account.go` `RequestEmailChange` returns `202 Accepted`
without reading the request body or persisting anything (the `emailChangeReq` type was dead).
Clients get a success response while nothing happens — a misleading partial implementation.
- **Fix applied (cosmetic):** removed the dead `emailChangeReq` type and documented the stub
  inline.
- **Remediation:** implement persistence + the verification-email worker, or return
  `501 Not Implemented` until then so callers aren't misled.

#### M8 — Stripe webhook dedup does not actually short-circuit reprocessing
`billing.go` `Webhook` inserts into `stripe_events … ON CONFLICT DO NOTHING` for dedup, but
then runs `applyEvent` **unconditionally** (it never checks rows-affected). Reprocessing is
prevented only by `applyEvent` being idempotent (it is, today). The dedup row is currently
advisory.
- **Remediation:** branch on whether the insert was a conflict, or wrap insert+apply in one
  transaction, so dedup is enforced rather than incidental.

### LOW

- **L1 — Tauri CSP breadth.** `apps/desktop/src-tauri/tauri.conf.json` uses
  `'unsafe-inline'` (script/style) and `connect-src … https://*`. Broad but defensible for a
  self-hosted app that connects to arbitrary user servers; tighten if/when feasible.
- **L2 — `RequireAdmin` empty-domain edge.** `cloud/internal/handlers/admin/admin.go`
  compares `domainOf(email) != strings.ToLower(d.AllowedDomain)`. If `AllowedDomain` is
  misconfigured as `""` *and* an email had an empty domain, `"" == ""` would grant admin.
  Emails are validated upstream so it is not currently reachable; add an explicit
  `if d.AllowedDomain == "" { deny }` guard for fail-closed clarity.
- **L3 — `relay.stream.fail(err)` discards its cause.** The error argument was unused; the
  reader surfaces its own generic *"stream closed before response"*. Minor diagnostic loss.
  Documented inline; consider storing the cause and returning it from `collectResponse`.
- **L4 — cloud migrations are Postgres-only (informational).** `cloud/migrations/*.sql` have
  no `.sqlite.sql` variants — **by design** (the cloud control-plane is Postgres-only; no
  SQLite references exist in `cloud/`). The dual-engine on-prem migrations in
  `shared/db/migrations` are fully paired. No action needed.

---

## Section-by-section results

### 1. Go (`api/`, `streaming/`, `cloud/`, `cmd/`, `shared/`, `tools/`)
- `golangci-lint`: api ✅ 0, streaming ✅ 0, **cloud ✅ 0 (after fixes; previously ungated)**.
- `go vet`: all modules clean. `go test`: api / streaming / cloud pass.
- Error handling: idiomatic; `defer Close()`/`Rollback()` patterns are intentional and
  excluded by the `std-error-handling` preset. Resource cleanup (defer Close, tx rollback,
  connection pools) is correct.
- Handler patterns: consistent RFC-7807-style problem responses (`httperror`), proper
  `r.Context()` propagation, sensible middleware ordering (Recover → RequestID → Logging →
  CORS; rate-limit on unauth routes; `RequireUser` group).
- Security: parameterized SQL throughout; `ORDER BY`/`sort` inputs are **whitelisted**
  before concatenation (`videos.go`, `collections/smart.go`); admin-token bypass is opt-in
  with min-length; no hardcoded secrets. See H1/H2/M2/M3/M4 for cloud-auth specifics.
- TODO/FIXME/HACK: only **7** repo-wide, all benign and ticket-referenced.

### 2. Python (`pipeline/`)
- `ruff check` ✅, `ruff format --check` ✅ (222 files), `mypy --strict src/` ✅ (103 files),
  `pytest -m unit` ✅ (394 passed, 612 deselected). 2 well-scoped `TODO(...)` markers tied to
  future stories. No exception swallowing, async anti-patterns, or resource leaks found.

### 3. Web (`web/`)
- `tsc --noEmit` ✅, `eslint --max-warnings=0` ✅, `pnpm build` ✅, `vitest run` ✅
  (128 tests / 22 files). The `useToast must be used within a <ToastProvider>` console line
  is an **intentional negative-path test**, not a failure.
- i18n: `en.json` and `ar.json` have **identical 391-key sets** — zero parity drift.
- No `any` leaks past eslint; the 13 new pages follow existing routing/i18n conventions.

### 4. Native (`apps/`)
- Capacitor config (`apps/mobile/capacitor.config.ts`): valid; secure-by-default
  (`cleartext: false` in prod, custom iOS scheme, app-bound domains, no bundled runtime).
- Tauri config: valid v2 schema; see L1 on CSP breadth.
- tvOS Swift: `swift build` ✅ compiles cleanly (17 sources).
- Android TV Kotlin: idiomatic Compose (`error!!` is post-null-check smart-cast; `lateinit`
  Application container is standard). Not gradle-built here (no Android SDK in review env).
- `.gitignore` coverage: thorough across desktop/mobile/tv — build artifacts, keystores,
  signing keys, generated native projects all ignored. No build artifacts tracked in git.

### 5. Infrastructure
- Dockerfiles: all `golang:` base images use `ARG GO_VERSION=1.25`, matching `go 1.25.0`
  in every `go.mod`. ✅
- `docker-compose.yml` (deploy/compose + cloud installer): valid YAML. ✅
- CI workflow YAML: all 10 workflows parse; added cloud lint step parses. ✅
- Makefile: `lint`, `test`, `build`, `lint-go/py/web`, `help` all resolve via dry-run. ✅
- Migrations: `shared/db/migrations` fully `.sql`/`.sqlite.sql`-paired; cloud is PG-only by
  design (L4).

### 6. Cross-cutting
- **Config consistency gap (now fixed):** cloud lacked the `.golangci.yml` that api/streaming
  share — the inconsistency *was* the H3 root cause.
- Logging: no secret **values** logged except the deliberate self-host reset-token (M6);
  other matches log event names/labels only.
- Naming/structure: consistent across modules; no significant copy-paste duplication found
  beyond the shared-by-design `signES256`/`SignAPNsToken` ES256 helpers.

---

## Fixes applied

All fixes are in the `cloud/` module, its CI gate, plus one config addition. cloud was
re-verified after every change: **`golangci-lint` 0 issues, `go build` clean, `go test`
passes**; api/streaming still build.

| ID | Change | Files |
|----|--------|-------|
| H1 | nil-guard for missing Google `id_token` verifier (panic → clear error) | `cloud/internal/auth/oauth/google.go` |
| H2 | removed duplicate in-`RequireUser`-group webhook registration | `cloud/internal/handlers/billing/billing.go` |
| H3 | added tuned `cloud/.golangci.yml` + `golangci-lint (cloud)` CI step | `cloud/.golangci.yml`, `.github/workflows/_lint.yml` |
| M1 | cloud made lint-clean: dead code removed, errcheck on defers, `fmt.Fprintf(os.Stdout,…)`, param/shadow renames, gofmt | ~30 cloud files |
| M2 | gate OAuth email auto-link on `EmailVerified` | `cloud/internal/handlers/auth/oauth.go` |
| M7 | removed dead `emailChangeReq`; documented the stub | `cloud/internal/handlers/account/account.go` |
| L3 | documented `stream.fail` cause-discard | `cloud/internal/relay/tunnel.go` |

## Recommended follow-ups (not auto-fixed — need tests / deliberate implementation)

1. **Wire a JWKS-backed OAuth verifier** for Google (H1 remaining) and Apple (M3), shared,
   with key caching and full claim validation.
2. **Add cloud handler tests** (webhook signature + idempotency, `federate` matrix,
   `RequireAdmin`) then add `cloud` to the test + coverage-floor gate (H3 remaining / M5).
3. **Apple callback `state`/CSRF** validation (M4).
4. **Reset-token logging** — gate behind a no-mailer dev flag (M6).
5. **`RequestEmailChange`** — implement or return `501` until the mail worker lands (M7).
6. **Webhook dedup** — enforce via rows-affected check or transaction (M8).
