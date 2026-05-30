# Epic 23 — Security: Spec-vs-Implementation Gap Analysis

**Verdict:** Primitives largely exist and are unit-tested, but several
controls are NOT wired into the live request path (no global auth gate,
empty `lib` claim, dead lockout code, no streaming JWT minting, no
SECURITY.md, supply-chain gate is a stub) — the prior 8/8 structural
rating overstates behavioral completeness. **Real status ≈ 3 complete /
8 partial / 8 missing-or-unwired across 38 ACs.**

Scope note: Argon2id, JWT, user store, refresh, ACL are owned by Epic
10; Epic 23 layers hardening + wiring on top. Gaps below are scored
against Epic 23's ACs (which supersede earlier Epic 10 drafts per the
story preambles), not Epic 10's.

---

## Method

Live path traced: `api/main.go:runServe` → `auth.applySecurity`
(`api/auth_bootstrap.go:99`) wraps the public mux with
`Headers→CORS→AdminToken→JWTBearer`; inner mux is `router.New`
(`api/internal/router/router.go:71`) + `MountP6/P9/P10`. Critically,
`JWTBearer`/`AdminToken`/`CookieAuth` only *attach* a principal if a
credential is present (`api/internal/auth/middleware/middleware.go:93,49`);
no `RequireAuth`/`authz.Can` runs as global middleware
(`api/internal/router/p6.go:49` mounts every handler with zero auth
middleware). Enforcement is per-handler-discretion via
`principal.FromContext`, with no CI lint guaranteeing the call.

---

## Story 23.1 — Authentication

| AC | Status | Evidence / Gap |
|----|--------|----------------|
| AC1 argon2id, RFC9106 2nd rec (`m=65536,t=3,p=1`), rehash on login | **partial** | `argon2id.DefaultParams()` (`api/internal/auth/argon2id/argon2id.go:46`) sets `Time:2`, **not `t=3`** as the AC mandates ("RFC 9106 second recommendation … iterations=3"). PHC stores params so old rows verify, but the default is wrong. **No rehash-on-login**: `auth.Handler.Login` (`api/internal/handlers/auth/auth.go:135`) calls `u.VerifyPassword` then issues tokens; never re-hashes on param drift. Only an offline "rehash" job stage exists (`api/internal/handlers/jobs/jobs.go:366`). plan-23-01 §2.7 `login.go` rehash path does not exist. |
| AC2 web cookie httpOnly/Secure/SameSite=lax + CSRF validated | **partial** | Cookies set correctly (`auth.go:239-256`, `SameSiteLaxMode`, `HttpOnly`, `Secure` gated on `cookiesSecure()`). CSRF token *minted* (`mkt_csrf`) but **no middleware validates it on state-changing requests** — `TypeCSRFMismatch` const (`auth.go:68`) is unused; comment at `auth.go:523` says CSRF "layered separately" but no guard found in middleware/router/handlers. State-changing requests are not CSRF-protected on the live path. |
| AC3 native bearer RS256 15min + opaque refresh 30d, rotation revokes prev | **complete** | `respondNative` (`auth.go:189`) signs RS256 access (15min via `accessTokenTTL`), issues opaque refresh; `Refresh` (`auth.go:266`) rotates with replay detection (`refresh.ErrReplay` → family revoke). Refresh stored hashed (refresh pkg). Behaviorally satisfied. |
| AC4 JWKS at `/api/.well-known/jwks.json`; 90d rotation/30d overlap; streaming caches ≤5min | **partial** | Endpoint wired (`api/main.go:280`, `keys.JWKSHandler`), `Cache-Control: public, max-age=300`. **Overlap default is 24h, not 30d** (`keys.DefaultRotationOverlap = 24*time.Hour`, `api/internal/auth/keys/keys.go:47`). **No 90-day auto-rotation daemon** — plan-23-01 §2.9 `KeyStore.Daemon` absent; only a manual `keys rotate` CLI (`api/keys_cli.go`) + an overlap *reaper* (`main.go:311`). Streaming JWKS cache exists (`streaming/internal/auth/jwks.go`). |
| AC5 single-user `MAKTABA_ADMIN_TOKEN` → sentinel UUID; disabled when `auth.multi_user=true` | **partial** | `AdminToken` middleware (`middleware.go:49`) maps token → `users.SentinelAdminID`, constant-time compare, ≥32-char guard (`auth_bootstrap.go:59`). **No `auth.multi_user` feature-flag**: the middleware is unconditionally active whenever the env var is set; spec requires it "feature-flagged off when `auth.multi_user = true`". Sentinel UUID not verified to equal `00000000-0000-0000-0000-000000000001` here (defined in users pkg). |

EC1 (30s skew): satisfied — `jwt.Verify` allows ±30s (`api/internal/auth/jwt/jwt.go:146-150`). Algorithm-confusion: satisfied — hard `hdr.Alg != "RS256"` reject (`jwt.go:117`). EC3 admin-token rotation = env+restart: matches design.

## Story 23.2 — Authorization and ACLs

| AC | Status | Evidence / Gap |
|----|--------|----------------|
| AC1 every handler runs `authorize(action,resource)`; CI lint enforces | **missing** | No global authz middleware (`p6.go:49` mounts handlers bare). `authz.V1.Can` exists (`api/internal/auth/authz/authz.go`) but invocation is per-handler discretion (e.g. `segments.go:55 h.canRead`). **No `tools/authz-lint`** — plan-23-02 §2.6 absent; `find tools` shows no authz-lint. Handlers missing the call are not detected. |
| AC2 roles admin/editor/viewer, `library_acl`, creator defaults admin | **partial** | `authz.V1` implements an admin/library-membership model but **not the three named roles**: `roles.go` matrix from plan-23-02 §2.4 (`RoleAdmin/Editor/Viewer` + `Can(action)`) does not exist. authz.go is `*.read`=lib-member, `*.write`=admin/owner, `library.*`=admin — no `editor` (ingest+edit) vs `viewer` (read+watch) distinction. |
| AC3 streaming JWT claims (`usr`,`lib`,`is_admin`,`aud∈{streaming,…}`); streaming checks `lib`; expired→**403** | **partial** | Streaming verifier is solid: `lib[]` coverage (`streaming/.../auth/middleware.go:124 CoversLibrary`), audience policy (`AudSession/Direct/Static`), well-formed `lib` required (`claims.go:55`). **Two real gaps:** (1) **expired JWT returns 401, not 403** — `httpx.WriteSignedURLError` hard-codes `http.StatusUnauthorized` for ALL sub-types incl. `expired`/`wrong-lib` (`streaming/internal/httpx/problem.go:48-52`); AC3 explicitly: "Expired JWTs produce a clear 403 (not 401)". (2) **API never mints `lib`** — `auth.go:201,322` sign with `Lib: []string{}` always; subtitle URLs are unsigned plaintext placeholders (`videos/segments.go:233`, `URLSigner` only "if present"). The streaming check is correct but the API side that must populate the claim is unwired → all native sessions carry empty `lib`. |
| AC4 revocation lag ≤15min documented; `EvictHashCache` + key rotate for instant | **partial** | 15-min access TTL holds; no operator-guide doc found for the lag; `EvictHashCache` not located in API. |
| AC5 admin-only `/api/system/*`, `/api/auth/users` require `is_admin` | **partial** | Per-handler checks exist (`auth.go:428,458,497 !p.IsAdmin → 403`; `libraries Delete:374`). No route-level `RequireAdmin` middleware on `/api/system/*`; relies on each handler. `/api/auth/users` CRUD handler not located (may be unimplemented). |

TC4 "verifier rejects JWT missing lib": streaming satisfies. EC2 last-admin: handled in Epic 10 user store.

## Story 23.3 — Transport security

| AC | Status | Evidence / Gap |
|----|--------|----------------|
| AC1 Caddy TLS, local-CA Mac / LE Linux | **partial** | Caddyfile/snippets under `deploy/docker/caddy/` not verified present in this scope; HSTS toggle plumbed via `MAKTABA_HSTS` (`auth_bootstrap.go:72`). plan-23-03 `tls-modern.conf` snippet existence unconfirmed. |
| AC2 HSTS default-on `max-age=31536000; includeSubDomains`, opt-out | **partial** | `httpsec.DefaultHeaders()` + `HSTSOneYear` exist, but **HSTS is opt-IN** (`auth_bootstrap.go:72`: only set when `MAKTABA_HSTS==1`); AC2 requires **default-on** with opt-out. Inverted default. |
| AC3 TLS1.2 min, modern ciphers, OCSP, ALPN h2 | **missing/unverified** | Caddy-config concern; `tools/tls-doctor.sh` not present (`tools/` has only `sbom.sh`). No evidence the vetted cipher list ships. |
| AC4 internal gRPC mTLS when not co-located; loopback documented | **missing** | No `api/internal/intca`, no `streaming/.../grpcclient/mtls.go`; gRPC clients (`api/internal/grpcclients/*`) use plaintext. plan-23-03 §2.4-2.6 internal-CA unimplemented. |

## Story 23.4 — Secrets management

| AC | Status | Evidence / Gap |
|----|--------|----------------|
| AC1 canonical secret list per arch §11.5 w/ owners | **partial** | `api/internal/secret/secret.go` provides `FromEnvOrFile`; no `secrets/registry.go` enumerating owners per plan-23-04 §2.3. |
| AC2 Streaming never sees JWT private key / STT keys; binary has no code path | **partial** | Streaming loads only public key (`MAKTABA_JWT_PUBLIC_KEY_PEM`); but **no `tools/secret-allowlist-lint`** static-string check on the streaming binary (plan §2.8) — the negative assertion is unenforced. |
| AC3 `/api/settings` returns metadata only, write-only secrets | **partial** | `settings.Handler` redacts via `secretKeyPattern` (`api/internal/handlers/settings/settings.go:74,208`) replacing values for `api_key|token|password|secret` keys. Returns redacted config rather than the spec'd `{key,configured,source}` metadata shape; no write-only `PUT /api/settings/{key}` with `source` reporting found. |
| AC4 redaction middleware rewrites secret-shaped values (entropy, `*_KEY/_TOKEN/_PASSWORD`) in escaped log lines | **partial** | `shared/log/go/redact.go` is a **fixed key-name allowlist** (`DefaultRedactedFields`) matched in `makeReplaceAttr` (`logger.go:148`). **No high-entropy value regex and no `*_KEY/_TOKEN/_PEM` suffix matching** as plan-23-04 §2.7 `entropyRE`/`secretSuffixes` require — a secret in a free-form message or an unlisted attr key escapes. EC1 stack-trace redaction not implemented. |

## Story 23.5 — Input validation and content safety

| AC | Status | Evidence / Gap |
|----|--------|----------------|
| AC1 inputs validated vs schema, 400 `problem+json` | **partial** | `httperror` problem+json exists; `api/internal/security/validation.go` has ad-hoc validators; no OpenAPI/validator-v10 schema enforcement layer per plan §2. |
| AC2 single `paths.canonical_under_roots` helper (symlink-resolve, reject `..`/NUL, assert under roots) + CI lint | **missing** | **No `shared/paths/canonical.go`**, no `pipeline/.../paths.py`. `security.ValidateLibraryPath` (`validation.go:83`) does string-only checks (`..`, NUL, leading `/`) — **does not resolve symlinks nor assert resolved path under configured roots** (AC2's core requirements). `pipeline library_mgmt/roots.py:canonicalise` does `os.path.realpath` but for *overlap detection*, not a security gate, and silently returns input on `OSError`. No `tools/path-lint`. Symlink-escape (TC1 case 3) and resolved-root assertion unmet. |
| AC3 ffmpeg/pyannote argv slice, no `sh -c` | **complete** | `streaming/internal/ffmpeg/ffmpeg.go:56` uses `exec.CommandContext(ctx, bin, args...)`; pipeline ffmpeg uses subprocess arg lists. No shell string. |
| AC4 SSRF defense (no RFC1918/loopback/link-local, ≤3 redirects) | **missing** | No `shared/httpsec/safe_fetcher.go`; no `isPrivate`/dialer-Control hook anywhere in api/shared. Poster/URL fetch has no SSRF guard. |
| AC5 probe size-bounded; malformed→error not panic; subtitle HTML-escaped (VTT + SRT) | **partial** | No `subtitles/sanitize.py`/`sanitize_srt.py` located; cue HTML-escape on write (TC4) unverified-present. |
| AC6 `ExtractEmbeddedSubtitle` validates `stream_index` vs probed streams before ffmpeg | **partial/unverified** | plan §2.8 gate in `pipeline grpc_server.py` not confirmed present in scope. |

## Story 23.6 — Rate limiting, lockout, destructive-confirm

| AC | Status | Evidence / Gap |
|----|--------|----------------|
| AC1 per-route auth limits (login 10/min/IP, refresh 60, other-auth 30; structured 429+Retry-After) | **missing** | Live limiter is the **generic Epic-7.19 per-IP/per-user limiter** (`api/internal/middleware/ratelimit.go`, defaults 6000/600 per min, wired in `router.go:81-86`). **No per-route policy table** — plan-23-06 §2.4 `classes` (auth-login/refresh/other) absent. `/api/auth/login` gets the generic 6000/min/IP cap, not 10/min. `security.TokenBucket` (`api/internal/security/ratelimit.go`) is an unused primitive (no per-route caller found). 429+Retry-After shape is correct generically. |
| AC2 per-user search 60/min, bulk-submit 10/min | **missing** | Generic per-user limiter only; no search/bulk-specific caps. |
| AC3 lockout 5-in-15min sliding → 15min lock per `(user,ip)`; audit `auth`; admin `/unlock` | **missing (unwired)** | `users.IncrementFailedAttempt` (`users.go:330`) implements threshold/lock SQL and `Unlock` (`users.go:310`) exists — **but the login handler never calls them**. `auth.Handler.Login` only calls `h.logFailedLogin` (audit-only, `auth.go:151,166`); no failed-attempt increment, so lockout never triggers on the live path (dead code). **No `POST /api/users/{id}/unlock` route** (`grep` in router/handlers: none). `u.IsLocked` *is* checked at login (`auth.go:159`) but the counter that would set `locked_until` is never incremented. |
| AC4 limits configurable, single-user relaxed not disabled | **partial** | Generic limiter is env-configurable (`MAKTABA_IP_RATE_PER_MIN`); no single-user 10× relaxation logic tied to `auth.multi_user`. |
| AC5 destructive confirm token (lib.name / user.username / "rotate-immediate"), 412 mismatch, audit | **partial** | Only library-purge implemented: `libraries Delete` (`libraries.go:402`) checks `?confirm=<name>` → `412 Precondition Failed`. **Gaps:** confirm read from **query string, not request body** `confirm` field (AC5 says "explicit `confirm` field in the request body"); **plain `!=`, not constant-time**; **no audit row** on success; `DELETE /api/users/{id}` confirm and `POST /api/keys/rotate?immediate=true` HTTP endpoint **do not exist** (key rotation is CLI-only with interactive `yes-invalidate-all-tokens` prompt, `api/keys_cli.go:84`, wrong token + wrong surface). |

## Story 23.7 — Supply-chain security

| AC | Status | Evidence / Gap |
|----|--------|----------------|
| AC1 SBOM (gomod/py/npm) per release artifact, published | **partial** | `tools/sbom.sh` exists but self-documents as a **Story 22.2 stub** that skips missing tools and "does NOT gate"; `api/internal/security/sbom.go` parses an embedded SBOM for `GET /api/system/sbom`. plan-23-07 promises `tools/sbom-generate.sh` + signed bundle — not present. |
| AC2 CVE gate in CI (govulncheck/pip-audit/npm audit), high blocks merge, recorded suppression | **missing** | No `tools/suppression-lint.sh`, no `.github/workflows/_supply-chain.yml`, no `tools/cve-suppress.sh`. No CVE gate. |
| AC3 base images pinned by digest, no `:latest` | **missing** | No `tools/dockerfile-pin-lint.sh`; pin enforcement absent. |
| AC4 dependabot/renovate weekly, security PRs auto-merge | **missing** | No `renovate.json`, no `.github/dependabot.yml`. |
| EC1 `security/suppressions/<cve>.md` | **missing** | No `security/suppressions/` directory. |

Story self-acknowledges (story-23-07 "Deferred from P0") the SBOM stub — but the AC-level CVE gate / pin lint / renovate are all unbuilt, not merely deferred-signing.

## Story 23.8 — Coordinated disclosure

| AC | Status | Evidence / Gap |
|----|--------|----------------|
| AC1 `SECURITY.md` with address, SLA (3d ack/90d fix), scope | **missing** | **No top-level `SECURITY.md`** (`ls SECURITY.md` → absent). A `security.DisclosurePolicy` struct + `/.well-known/security.txt` (RFC 9116) exists (`api/internal/security/disclosure.go`) — related but **not the AC1 deliverable** (the human-readable repo policy file with the 3-day/90-day SLA and scope). |
| AC2 GHSA draft tracking, published advisory w/ CVE | **missing** | No `advisories.json`, no `docs/security/incident-runbook.md`, no `tools/publish-advisory.sh`. |
| AC3 critical fixes as patch versions on supported branches, notes link GHSA | **missing** | No patch-release/`release/v*.x` flow doc found in scope. |
| AC4 web client "what version am I running?" + advisory link | **missing** | No `web/src/lib/advisories.ts` / `AboutDialog.tsx` advisory surface located. |

---

## Top gaps by impact

1. **No global authorization gate + empty `lib` claim (23.2 AC1/AC3,
   23.1 AC2).** Handlers are mounted with zero auth/authz middleware
   (`api/internal/router/p6.go:49`); `JWTBearer`/`CookieAuth` only
   *attach* a principal, never *require* one, and `MountP9`'s
   `CookieAuth` return is discarded in `main.go` (never installed).
   Every native access token is signed with `Lib: []string{}`
   (`api/internal/handlers/auth/auth.go:201,322`), so the (correct)
   streaming `lib`-coverage check can never pass for a real cross-tenant
   user — and no CI lint enforces the `authorize()` call. This is the
   single worst gap: authorization correctness depends on each
   handler remembering to self-check, with empty entitlement claims.

2. **Failed-login lockout is dead code (23.6 AC3).**
   `users.IncrementFailedAttempt`/`Unlock` exist and are tested but the
   login handler never calls them (`auth.go:151,166` audit-only); no
   `POST /api/users/{id}/unlock` route. Brute-force lockout does not
   function on the live path.

3. **Streaming expired JWT returns 401, not 403 (23.2 AC3).**
   `httpx.WriteSignedURLError` hard-codes 401 for all sub-types
   (`streaming/internal/httpx/problem.go:48`); AC3 explicitly requires
   403 for expired manifest tokens.

4. **No per-route auth rate-limit table (23.6 AC1/AC2).** Only the
   generic 6000/min-per-IP limiter runs; `/api/auth/login` is not
   capped at 10/min, refresh not at 60/min.

5. **HSTS default inverted + no SSRF defense + no central path
   canonicalizer (23.3 AC2, 23.5 AC4/AC2).** HSTS is opt-in not
   default-on; no `safe_fetcher`; `ValidateLibraryPath` is string-only
   (no symlink resolution / root-containment), so symlink-escape and
   normalized-traversal cases are not defended at the helper level.

6. **Argon2id default `t=2` not `t=3`; no rehash-on-login (23.1 AC1).**

7. **23.7 / 23.8 substantially unbuilt:** no CVE CI gate, no
   dockerfile-pin lint, no renovate, no `SECURITY.md`, no advisory
   feed / web surface.

All findings READ-ONLY; verified against code, not audit/spec
self-claims.
