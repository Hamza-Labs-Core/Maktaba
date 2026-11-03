# Story 50.32 — The operator console on admin.maktaba.tv: its own host, bundle, cookies and CSP

> Epic 50 · Phase 1 · Depends on: 50.34 (deploys) · Needed by: 50.33, 50.30 (its admin rules),
> the launch gate (README §11)

## Why

The operator console can suspend accounts, set plans, revoke licenses, block IPs and delete a
user's data. Today it is the `/admin` part of the user web app (`web/cloud/src/App.tsx`), so its
code ships to every user's browser, and `/v1/admin/*` is served on every name the api role
answers. One cookie, one CSP and one bundle serve both.

This story puts the console on **its own hostname, `admin.maktaba.tv`** (README §15 decision),
with its own listener, bundle, cookies and CSP, and makes every admin route **404** on every
user name, at the origin and at the edge (50.30 rule 1). 50.33 then replaces how staff sign in.

## Description

### 1. Two listeners in the api role

| | User listener | Admin listener |
|---|---|---|
| Address | `server.listen_addr` (`127.0.0.1:8090`) | `server.admin_listen_addr` (`127.0.0.1:8091`) |
| Reached through | the Tunnel for `api.maktaba.tv` and `app.maktaba.tv` (50.31) | the Tunnel for `admin.maktaba.tv` only |
| Mounts | every `/v1/*` route except admin; `/.well-known/*`; `/healthz`, `/readyz` | `/v1/staff/*` (staff auth, 50.33), `/v1/admin/*` (today's admin and metrics/privacy handlers), `/healthz`, `/readyz` |
| `Host` it accepts | `server.user_hosts` (`api.maktaba.tv`, `app.maktaba.tv`, `maktaba.tv`) | exactly `server.admin_host` (`admin.maktaba.tv`) |
| A request for an admin path | **404** `not_found` (the routes don't exist here) | served, after 50.33's checks |
| A request with another `Host` | 404 | **404** |
| CORS | the web origins of the user apps (the list no longer includes `admin.maktaba.tv`) | none: same-origin only; a preflight is `403` |

- `/v1/admin/*` handlers move from the user router to an admin router
  (`cloud/internal/server/admin_router.go`), unchanged inside. The user router has no admin
  route at all, so a routing mistake can't expose one.
- The 404 for an admin path on a user listener is the same body as any unknown route, so it
  doesn't reveal that the path exists elsewhere. It is not audited (it carries no identity);
  50.10 counts them.
- The Host check on the admin listener catches a wrong Tunnel ingress (an `api` rule pointed at
  8091 by mistake): every request is 404 and an error is logged once a minute.
- In dev, `pnpm dev:admin` serves the admin bundle on :5175 and proxies `/v1` to the admin
  listener on :8091 with `Host: admin.localhost`; `server.admin_host` is `admin.localhost` in
  `cloud.example.toml`.

### 2. Two bundles, and no admin code on a user name

- **Split the web app.** `web/admin/` (new Vite root, `vite.admin.config.ts`, output
  `dist-admin/`) holds the console: today's `web/cloud/src/pages/admin/*` move there, with the
  staff sign-in screens of 50.33. It imports shared pieces (the design system, `ui.tsx`, the
  API client's fetch wrapper, the i18n provider) from `web/cloud/src/shared/`, which has no
  admin code.
- `web/cloud/src/App.tsx` loses the `admin` routes and the *Admin* nav link. A signed-in user
  never sees a link to the console; staff go to `admin.maktaba.tv` directly.
- **The check.** `tools/lint/no-admin-in-user-bundle.mjs` (new) builds nothing itself; it runs
  after `pnpm build:cloud` and fails if any file in `dist-cloud/` contains `/v1/admin/`,
  `/v1/staff/`, an admin i18n key (`"admin.`), or a module from `web/admin/` (the source map's
  `sources`). ESLint's `no-restricted-imports` in `web/cloud/` forbids importing from
  `web/admin/` at all.
- **The Arabic and English strings** for the console move with it to
  `web/admin/src/i18n/{en,ar}.json`; the i18n test that fails on a key missing in one language
  runs on both bundles.

### 3. The two web Workers

Like the developer portal (`web/packages/devportal`), each bundle is a Worker in front of static
assets, deployed by CI (50.34):

| Worker | Routes | Assets | Passed to the origin (`fetch(request)`, through the Tunnel) | Everything else |
|---|---|---|---|---|
| `maktaba-cloud-web` (`web/packages/cloud-web/`, new) | `app.maktaba.tv/*`, `maktaba.tv/developers*` | `dist-cloud/` | `/v1/*` except `/v1/admin/*` and `/v1/staff/*` (those are 404 here); `/.well-known/*` | assets, SPA fallback |
| `maktaba-admin-web` (`web/packages/admin-web/`, new) | `admin.maktaba.tv/*` | `dist-admin/` | `/v1/staff/*`, `/v1/admin/*`, `/healthz` | assets, SPA fallback; any other `/v1/*` is 404 |

- They are **routes on proxied names, not custom domains**: the hostnames' DNS records point at
  the Tunnel (50.31), so a pass-through `fetch(request)` goes to the origin.
- `run_worker_first = true`, so every response, asset or API, gets the host's headers (§5).
- `workers_dev = false` on both: no `*.workers.dev` copy of either site exists.
- Native apps and homes keep calling `api.maktaba.tv` directly (no Worker on that name).

### 4. Cookies

| | User hosts | Admin host |
|---|---|---|
| Name | `__Host-maktaba_refresh` (today's `maktaba_refresh`, renamed; no live sessions exist yet) | `__Host-maktaba_staff` (50.33) |
| Scope | host-only (no `Domain`), `Path=/`, `Secure`, `HttpOnly` | host-only, `Path=/`, `Secure`, `HttpOnly` |
| `SameSite` | `Lax` (the OAuth callbacks are top-level navigations; Apple's `form_post` state cookie keeps `None`, as today) | `Strict` |
| Lifetime | 30 days, rotating (today's rule) | a session cookie; the server ends it at 15 min idle or 8 h (50.33) |

- The `__Host-` prefix makes the browser refuse either cookie if anything tries to give it a
  `Domain`, so neither can be widened to `.maktaba.tv` and reach a home on `*.relay.maktaba.tv`
  (the account-takeover bug the cloud docs list as fixed).
- Different names and different hosts: the staff cookie is never sent to a user name, and a
  user's cookie is never sent to `admin.maktaba.tv`.
- **CSRF on the admin host:** a state-changing request must carry
  `Origin: https://admin.maktaba.tv` and `X-Maktaba-Staff: 1`; otherwise `403 bad_origin`.

### 5. CSP and headers, per host

Set by each Worker on assets and passed-through responses, and by the API on its own JSON:

| Host | `Content-Security-Policy` |
|---|---|
| `app.maktaba.tv`, `maktaba.tv/developers` | `default-src 'self'; script-src 'self' https://challenges.cloudflare.com; frame-src https://challenges.cloudflare.com; connect-src 'self'; img-src 'self' data: https:; style-src 'self'; font-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'` |
| `admin.maktaba.tv` | `default-src 'none'; script-src 'self' https://challenges.cloudflare.com; frame-src https://challenges.cloudflare.com; connect-src 'self'; img-src 'self' data:; style-src 'self'; font-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'` |
| `api.maktaba.tv` (JSON) | `default-src 'none'; frame-ancestors 'none'` |

Plus, on every host: HSTS (50.30 §6), `X-Content-Type-Options: nosniff`, `X-Frame-Options:
DENY`. On the admin host also: `Referrer-Policy: no-referrer`,
`Cross-Origin-Opener-Policy: same-origin`, `Cross-Origin-Resource-Policy: same-origin`,
`Permissions-Policy: camera=(), microphone=(), geolocation=(), publickey-credentials-get=(self),
publickey-credentials-create=(self)` (passkeys, 50.33), `Cache-Control: no-store` on HTML and
`/v1/*`, `X-Robots-Tag: noindex, nofollow`, and `robots.txt` disallowing everything. The user
host's `img-src https:` is for avatar URLs (https only, cloud docs); the admin host loads no
outside image.

### 6. Strings

The console's existing strings move unchanged. New ones:

| Key | English | Arabic |
|---|---|---|
| `admin.title` | Maktaba operator console | لوحة تشغيل مكتبة |
| `admin.notFound` | This page doesn't exist. | هذه الصفحة غير موجودة. |
| `admin.badOrigin` | This request didn't come from the console. Reload the page. | لم يصدر هذا الطلب من اللوحة. أعد تحميل الصفحة. |

## Acceptance criteria

- **Given** every route the admin router serves,
  **when** it is requested on the user listener with `Host: api.maktaba.tv` or `app.maktaba.tv`,
  as no one, as a customer and with a valid staff session cookie,
  **then** each answer is **404**, with the same body as an unknown route.
- **Given** the admin listener,
  **when** a request arrives with `Host: api.maktaba.tv`,
  **then** it is 404.
- **Given** the built user bundle,
  **when** CI runs the check,
  **then** it contains no `/v1/admin/`, no `/v1/staff/`, no admin key and no admin module.
- **Given** a signed-in customer on `app.maktaba.tv`,
  **when** the page loads,
  **then** there is no *Admin* link and no admin route (`/admin` shows the user app's 404).
- **Given** a staff session cookie,
  **when** the browser requests anything on `app.maktaba.tv`,
  **then** the cookie is not sent (host-only, `__Host-`).
- **Given** a state-changing request to the admin host without `Origin: https://admin.maktaba.tv`,
  **when** it arrives,
  **then** `403 bad_origin`, and nothing changes.
- **Given** each host,
  **when** any page or `/v1` response is fetched,
  **then** it carries that host's CSP and headers from §5.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | integration (go) | `chi.Walk` over the admin router (the list builds itself, like `adminRoutes` in `admin_integration_test.go`) | each route on the user router, as anon, customer, staff | 404 ×3 for every route |
| T02 | integration (go) | admin router | `Host: api.maktaba.tv`, `app.maktaba.tv`, `admin.maktaba.tv.evil.test` | 404 |
| T03 | unit (go) | user router | `chi.Walk` | no route starts with `/v1/admin/` or `/v1/staff/` |
| T04 | unit (go) | CORS | preflight from `https://admin.maktaba.tv` to the user API; any preflight to the admin listener | not allowed; 403 |
| T05 | unit (go) | CSRF | POST without `Origin`, with a wrong one, without `X-Maktaba-Staff` | 403 `bad_origin` ×3 |
| T06 | unit (node:test) | `no-admin-in-user-bundle.mjs` | the real `dist-cloud/`; a fixture with `/v1/admin/` in a chunk | pass; fail naming the file |
| T07 | unit (vitest) | user `AppRoutes` | signed in as a customer; navigate to `/admin` | no admin link; 404 page |
| T08 | unit (vitest, Miniflare) | `cloud-web` and `admin-web` Workers with a fake origin | asset, SPA path, `/v1/billing/me`, `/v1/admin/accounts`, `/v1/staff/login` on each | headers per §5; pass-through only for the allowed paths; 404 otherwise |
| T09 | unit (vitest) | admin i18n | every key | in `en.json` and `ar.json`; RTL layout snapshot in Arabic |
| T10 | unit (go) | cookies | the user refresh cookie; the staff cookie | `__Host-` names, no `Domain`, `Path=/`, `Secure`, `HttpOnly`; `Lax` / `Strict` |

## Edge cases

- **A bookmarked `app.maktaba.tv/admin`.** It is the user app's 404 page with a plain
  "This page doesn't exist", not a redirect: the user app doesn't know the console's address.
- **One person is both a customer and staff.** They are two accounts (50.33); each host has its
  own cookie, so being signed in on one says nothing on the other.
- **The developer console at `maktaba.tv/developers`** (Epic 54) is a user page in the user
  bundle. The plugin store's review pages are the store's own (its own hostname and auth), not
  this console's.
- **The admin Worker is down** (a bad deploy). The console is unreachable; the API keeps
  serving users. `wrangler rollback` restores the previous version (50.34).

## Build plan

### Files to touch

| File | Change |
|---|---|
| `cloud/internal/server/router.go`, `admin_router.go` (new) | Split the routers; `DefaultOrigins` without `admin.maktaba.tv`. |
| `cloud/cmd/maktaba-cloud/role_api.go` | Two listeners; graceful shutdown for both. |
| `cloud/internal/config/config.go`, `cloud/configs/cloud.example.toml` | `admin_listen_addr`, `admin_host`, `user_hosts`. |
| `cloud/internal/handlers/auth/` | `__Host-maktaba_refresh`. |
| `cloud/cmd/maktaba-cloud/admin_integration_test.go` | T01–T02 next to the existing role test. |
| `web/admin/` (new), `web/vite.admin.config.ts` (new), `web/package.json` | The admin bundle; `dev:admin`, `build:admin`. |
| `web/cloud/src/App.tsx`, `web/cloud/src/shared/` (new), `web/cloud/src/pages/admin/` (moved) | The split. |
| `web/packages/cloud-web/`, `web/packages/admin-web/` (new) | The two Workers, their `wrangler.toml`, headers and tests. |
| `tools/lint/no-admin-in-user-bundle.mjs` (new), `web/eslint.config.js` | The checks. |
| `.github/ci-paths.yml` | `web/admin/**` and the two Worker packages in the web filter. |
| `cloud/README.md`, `cloud/docs/accounts-billing-admin.md` | Where the console lives now. |

### Tests

T01–T05, T10 in the cloud Go jobs; T06–T09 in the web unit job (`build:cloud` runs before T06).

### Rollout

Merged code works locally at once (`dev:cloud`, `dev:admin`). In production it serves nothing
until 50.31's Tunnel and 50.34's Workers are deployed; the console has no other address.

### Rollback

Revert the PR: the console returns inside the user bundle. Not to be done once production has
paying users without 50.33's replacement in place.

### Dependencies

50.34 (the Workers' deploy). 50.31 routes the admin name to the admin listener; 50.33 adds the
staff sign-in this listener mounts.
