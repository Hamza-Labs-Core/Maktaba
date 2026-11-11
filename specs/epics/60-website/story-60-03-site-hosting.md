# Story 60.03 — The site, its hosting and its deploy: Workers static assets on the apex and www, staging, rollback

> Epic 60 · Phase 1 · Depends on: 60.01 part A (the shell), owner actions A2 (the token) ·
> Needed by: every other story

## Why

maktaba.tv serves nothing. Before any page can stop a dead link, the site needs a package, a
build, a home on Cloudflare that leaves the zone's other names alone, and a deploy that is as
safe as maktaba.dev's:
- it runs from CI;
- it is green when its secrets are missing;
- it is smoke-checked;
- it can be rolled back.

## Description

**The package.** `web/packages/website/`, laid out as README §6.1:
- its own `package.json` and lockfile, as devportal has, so the deploy installs only what the
  site needs;
- Node build scripts that write `dist/`;
- `scripts/build.mjs` runs in this order:
  1. the install build (`pnpm -C web build:install`), copying `web/dist-install/` into
     `dist/install/` and `dist/ar/install/` (60.05);
  2. the generated families (`problems.mjs` 60.06, `help.mjs` 60.12, `download.mjs` 60.11);
  3. the hand-made pages;
  4. the copied files;
  5. the checks (Latin share, twins, links).
- **Copied files:**
  - `shared/log/schema/line.schema.json` to `dist/schema/log-line.json`, the URL its `$id`
    names;
  - `/.well-known/security.txt`, rendered the way maktaba.dev renders it
    (`api/internal/security/disclosure.go`, Expires 11 months ahead), with `Canonical` lines for
    both domains;
  - `robots.txt` and `sitemap.xml` (60.04).

**The Phase 1 front door** (`/`, `/ar/`): the text page layout of 60.01 part A, a few lines in the
voice of 60.02:
- what Maktaba is (a home media server for films, series and recordings, in Arabic and English);
- links to the install guides, the latest GitHub release, help (Phase 1: the troubleshooting
  guide), and maktaba.dev.

Every link is real. Phase 2 replaces the page with the home page (60.10).

**The Worker** (`src/worker.ts`), with `run_worker_first = true`, as maktaba.dev's:
- `http://` → `https://` (the zone's `always_use_https` does it too; the Worker's is for tests);
- `www.maktaba.tv/<path>` → 301 `https://maktaba.tv/<path>`;
- `/docs` and `/docs/` → 301 `/help/` once 60.12 lands. Until then they go to `/`;
- security headers on every answer (README §6.2): HSTS without `includeSubDomains`, CSP
  `default-src 'self'` with `frame-ancestors 'none'`, `Referrer-Policy`,
  `X-Content-Type-Options`, `Permissions-Policy`;
- **404:** a missing path answers `dist/404.html` with status 404. Under `/ar/` it answers
  `dist/ar/404.html`;
- **caching:** hashed assets `public, max-age=31536000, immutable`; HTML
  `public, max-age=0, must-revalidate`.

**`wrangler.toml`:**

```toml
name = "maktaba-website"
main = "src/worker.ts"
compatibility_date = "2026-09-01"
workers_dev = false
preview_urls = true          # each uploaded version gets a preview URL for staging
routes = [
  { pattern = "maktaba.tv", custom_domain = true },
  { pattern = "www.maktaba.tv", custom_domain = true },
]
[assets]
directory = "./dist"
binding = "ASSETS"
not_found_handling = "404-page"
html_handling = "auto-trailing-slash"
run_worker_first = true
```

- Custom domains own only their exact hostnames. app., api., admin., relay., watch. and plugins.
  keep their records and routes.
- **To check before relying on it:** that `preview_urls = true` gives preview URLs while
  `workers_dev = false`. If it does not, staging uses a second Worker, `maktaba-website-staging`,
  on `staging.maktaba.tv` (a name no epic uses). That Worker sends `X-Robots-Tag: noindex` and
  is deployed by the same job.

**CI** (`_website.yml`, called from `ci.yml` on the new `website` path filter, and from the
deploy):
- `npm ci`, the build, the unit tests (Worker, scripts), the link check (60.08), and the quality
  run (60.04);
- the `dist/` artifact uploaded (`website-site`).
- **The `website` filter covers:**
  - `web/packages/website/**`;
  - `web/packages/install/**` and `web/vite.install.config.ts`;
  - `web/src/help/content/**`;
  - `shared/api/problems.json`;
  - `shared/log/schema/**`;
  - `api/internal/security/disclosure.go`;
  - `web/design-system/tokens/**`;
  - the two workflows.
- It also covers every file the link check reads, so a new maktaba.tv link runs the check. That
  list comes from `scripts/links.mjs --paths`, and a `tools/ci` test keeps it equal to the
  filter.

**Deploy** (`website-deploy.yml`):
- **When:** on a push to main matching the filter, `workflow_dispatch` (input
  `rollback: boolean`), and monthly on the 1st (security.txt).
- `concurrency: website-deploy`, never cancelling.
1. `build`: `_website.yml` with `upload: true`. Its Lighthouse step runs **every page** in both
   languages (60.04).
2. `deploy`:
   1. **Secrets:** both `CLOUDFLARE_API_TOKEN` (or `CLOUDFLARE_API_TOKEN_WORKERS`) and
      `CLOUDFLARE_ACCOUNT_ID` must be set. Otherwise `::notice title=maktaba.tv not
      deployed::…` and the job passes.
   2. `node tools/cloudflare/token-check.mjs --pieces workers,dns --zone maktaba.tv`.
   3. Download `website-site` (the same three-attempt download as devportal's).
   4. `wrangler versions upload --message "<sha>"` → the version id and preview URL.
   5. `node tools/cloudflare/smoke.mjs website --base <preview URL>`.
   6. `wrangler versions deploy <id>@100% --yes`.
   7. `node tools/cloudflare/smoke.mjs website`. On failure: `wrangler rollback --message
      "website-deploy: live smoke failed at <sha>"`, then the job fails.
   - **With `rollback: true`:** step 4 onwards is replaced by `wrangler rollback --message
     "website-deploy rollback by <actor>"` and the live smoke, as in `cloud-web-deploy.yml`.

**Smoke target** `website` in `tools/cloudflare/smoke.mjs`:
- `/` answers 200 with `<html lang="en" dir="ltr">` and the language switch to `/ar/`;
- `/ar/` answers 200 with `<html lang="ar" dir="rtl">`;
- `/problems/not-found/` answers 200;
- `/install/docker/` answers 200 with the compose generator's marker;
- `/legal/privacy/` answers 200;
- `/this-does-not-exist` answers **404** with the 404 page's marker;
- `/.well-known/security.txt` has `Expires:`;
- the HSTS and CSP headers on `/`;
- `https://www.maktaba.tv/install/` answers 301 to `https://maktaba.tv/install/`;
- `https://app.maktaba.tv/` does not answer from this Worker (its answer carries no
  `maktaba-website` marker). This guards that no other name was taken.

**Token.** `deploy/cloudflare/README.md`'s token table gains the website row:
- account: Workers Scripts Edit;
- the `maktaba.tv` zone: Workers Routes Edit, Zone Read, and **DNS Edit**, because custom domains
  write their records.

`token-check.mjs` learns the `dns` piece. The owner grants it (A2).

## Acceptance criteria

- **Given** a push to main that changes the site, **when** the secrets are set, **then** the new
  version is uploaded, passes the smoke on its preview URL, is promoted at 100%, and passes the
  live smoke.
- **Given** a live smoke that fails, **then** the job rolls back to the previous version and fails.
- **Given** the dispatch with `rollback: true`, **then** the previous version is live and the live
  smoke passes.
- **Given** no Cloudflare secrets, **then** the deploy job passes with a notice naming them.
- **Given** the deploy, **then** app., api., admin., relay., watch. and plugins. answer as before,
  and the zone has no new record except `maktaba.tv` and `www.maktaba.tv`.
- **Given** a missing path, **then** the answer is the 404 page in the path's language with status
  404.
- **Given** `/schema/log-line.json`, **then** it is the schema file whose `$id` names it.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | Worker unit | `www.` request | fetch | 301 to the apex, path and query kept |
| T02 | Worker unit | any page | fetch | the headers of §6.2; no `includeSubDomains` |
| T03 | Worker unit | `/nope`, `/ar/nope` | fetch | 404 with the English, Arabic 404 page |
| T04 | Worker unit | `/docs` | fetch | 301 to `/help/` (to `/` before 60.12) |
| T05 | build | the build | run | `dist/schema/log-line.json` equals the source; security.txt has Expires and both Canonical lines |
| T06 | smoke unit | `smoke.mjs website` against a local server (`SMOKE_BASE_MAKTABA`) | run | each check passes; a 200 on the 404 path fails |
| T07 | `tools/ci` | `ci-paths.yml` and `links.mjs --paths` | compare | equal |
| T08 | workflow | deploy without secrets | run | green, notice printed |
| T09 | workflow | dispatch `rollback: true` (once, after the first deploy) | run | previous version live; smoke passes |
| T10 | `wrangler dev` | built `dist/` | the smoke against it | passes; the Worker's log kept in the run's artifacts |

## Strings

| Key | English | Arabic |
|---|---|---|
| `notfound.title` | This page isn't here | هذه الصفحة غير موجودة |
| `notfound.body` | The link may be old, or the address mistyped. | ربما يكون الرابط قديمًا، أو في العنوان خطأ. |
| `notfound.help` | Get help | احصل على المساعدة |
| `notfound.home` | Go to the home page | اذهب إلى الصفحة الرئيسية |
| `lang.switch.toAr` | العربية | العربية |
| `lang.switch.toEn` | English | English |
| `lang.hint` | This page is also in Arabic. | هذه الصفحة متوفرة بالعربية أيضًا. |

(The final wording follows 60.02's voice guide.)

## Edge cases

- **A first deploy's custom domain** takes minutes to get its certificate. Each smoke check retries
  for up to five minutes, as `smoke.mjs` already does.
- **A path under `/install/` that the install build no longer makes:** the link check (60.08) fails
  the build before it deploys.
- **`maktaba.tv/Install/Docker`** (case): answers 404. Paths are lower case, and links in the
  product are checked as written.
- **A monthly run with no change:** it rebuilds and redeploys, rolling security.txt's Expires
  forward.

## Build plan

| File | Change |
|---|---|
| `web/packages/website/{package.json,package-lock.json,wrangler.toml,src/worker.ts,scripts/build.mjs}` | the package, the Worker, the build |
| `web/packages/website/src/pages/{index,404,ar/index,ar/404}.html` | the front door and the 404 |
| `.github/workflows/_website.yml`, `website-deploy.yml` | build and deploy |
| `.github/ci-paths.yml`, `.github/workflows/ci.yml`, `_unit.yml` if needed, `tools/ci/tests/test_ci_paths.py` | the `website` filter |
| `tools/cloudflare/smoke.mjs`, `smoke.test.mjs`, `token-check.mjs` | the `website` target, the `dns` piece |
| `deploy/cloudflare/README.md` | the website's token row, and the names it owns |
| `web/packages/install/README.md` | "Hosting:" points at this site |
