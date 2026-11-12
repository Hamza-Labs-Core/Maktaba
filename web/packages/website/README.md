# maktaba.tv: the website

maktaba.tv is the site households find Maktaba on (Epic 60): the front door, the install
guides, and later the download, help, problem and legal pages, every page in English and Arabic.
(maktaba.dev is for developers.)

The site is a Cloudflare Worker (`src/worker.ts`) in front of static assets (`dist/`), the same
shape as maktaba.dev. It has no backend, no KV and no secrets. Every request goes through the
Worker, which:

- redirects `www.maktaba.tv` and plain http to `https://maktaba.tv`;
- redirects `/docs` to the front door (`/help/` once the help pages exist, story 60.12), and
  `/ar/docs` to `/ar/`: the table is `src/redirects.json`, which the link check reads too (a link
  to `/docs` passes when the page it redirects to exists);
- answers a missing path with the 404 page in its language (`/404.html`, or `/ar/404.html`
  under `/ar/`) and status 404;
- sets the headers on every answer (`src/headers.ts`): a strict CSP (no other origin; the
  copyright page alone may reach Turnstile and the cloud API), HSTS without
  `includeSubDomains`, `Referrer-Policy`, `X-Content-Type-Options`, `Permissions-Policy`, and
  the cache policy (HTML revalidated, content-hashed files for a year).

## What is on the site

| Path                            | What                                           | From                                                 |
| ------------------------------- | ---------------------------------------------- | ---------------------------------------------------- |
| `/`, `/ar/`                     | the front door (Phase 1)                       | `src/pages/index.html`, `src/pages/ar/index.html`    |
| `/install/`, `/ar/install/`     | the install chooser (60.05)                    | `src/pages/install/index.html` (+ `ar/`)             |
| `/install/…`, `/ar/install/…`   | Epic 59's install guides                       | `web/packages/install` (`pnpm -C web build:install`) |
| `/legal/…`, `/ar/legal/…`       | privacy, terms, copyright (60.07)              | `copy/legal/`, `scripts/legal.mjs`                   |
| `/problems/…`, `/ar/problems/…` | a page per problem type, and the index (60.06) | `shared/api/problems.json`, `scripts/problems.mjs`   |
| `/404.html`, `/ar/404.html`     | the 404 page                                   | `src/pages/404.html`, `src/pages/ar/404.html`        |
| `/schema/log-line.json`         | the log schema, at its `$id`                   | `shared/log/schema/line.schema.json`                 |
| `/.well-known/security.txt`     | the disclosure policy, both `Canonical`s       | `api/internal/security/disclosure.go`                |
| `/robots.txt`                   | every page may be crawled                      | `scripts/build.mjs`                                  |
| `/shots/…`                      | the apps' product screenshots (60.09)          | `scripts/shots.mjs`, `shots-manifest.json`           |

A page source in `src/pages/` is the page's `<main>` content after a leading comment
(`title:`, `description:`, optionally `meta:`, and `main:`, a class for `<main>`).
`scripts/shell.mjs` wraps it in the document and the shell of story 60.01 part A (the spec and
mockups are `design/`): the header with the nav, the phone menu and the language switch to its
twin, the language hint (§6.5), the footer and the skip link. The install guides go in the same
header and footer (`inShell`), and lose each "Screenshot to come" figure whose image does not
exist yet (60.05 T07). The shell's stylesheet (`design/` with `src/site.css`), scripts
(`design/shell.js`, `src/shell-head.js`), fonts and mark are published under content-hashed
names in `/assets/`. The Worker writes the address asked for into the 404 page.

The problem pages are generated from the registry the API sends from, `shared/api/problems.json`
(`scripts/problems.mjs`): one page per entry in each language, and the index, `/problems/`, grouped
by area, with a filter that runs in the page (`src/problems-filter.ts`, hidden without JavaScript,
when the whole list shows anyway). The registry becomes a structured document per page and one
function renders each (`renderProblem`, `renderIndex`), in the sections of 60.01's problem mockups,
so the design replaces those two functions. The build fails, naming the entry and the field, when
an entry lacks a text in either language or names an area `AREAS` has no heading for. A help topic
or install anchor is linked only when its page is built.

The legal pages are copy (`copy/legal/<page>.<lang>.md`) that `scripts/legal.mjs` turns into a
structured document and renders with one function (`renderLegal`). The privacy page reads
`privacy.CurrentPolicy()` through `go run ./cmd/privacy-json` (in `cloud/`: the build needs Go)
and is written from `docs/app-privacy.md`; a change to either without the page's copy fails the
build, naming the section. The copyright page's notice form (`src/copyright-form.ts`) is built
only when `WEBSITE_NOTICE_ENDPOINT` and `WEBSITE_TURNSTILE_SITEKEY` are both set, once Epic 50's
endpoint is live; until then the page gives the agent's email and what a notice must contain.

## Product screenshots

`static/shots/` holds every app's screenshots on the demo library (`tests/fixtures/demo-library/`),
dark, in English and Arabic, as WebP and AVIF at each width `shots.config.json` sets, each under
150 KB, with their sizes in `shots-manifest.json`. `npm run shots -- --from-run <run id>` makes
them from a CI run's `product-shots-*` artifacts and Android's Roborazzi references; an app whose
job did not pass keeps its previous images. `npm run shots:web` captures the web app locally
(`scripts/capture-web.mjs`). The steps, and who captures what, are in
`docs/developers/contributing/website.md` ("Product screenshots"). Tests: `scripts/shots.test.mjs`
(T01, T06).

## Build and run

```sh
npm ci                     # wrangler and vitest, at the versions package-lock.json pins
pnpm -C ../.. install      # the web workspace: the install build runs over it
npm run build              # writes dist/ (--no-install skips the install guides)
npm test                   # the Worker, the build and the built site
npm run dev                # wrangler dev on http://localhost:8787, no canonical redirects
```

`scripts/build.mjs` runs, in order: the install build, the generated families (`FAMILIES`:
problems 60.06, then help 60.12 and download 60.11), the hand-made pages, the copied files, and
the checks (every page has its twin in the other language; an Arabic page is Arabic, by
maktaba.dev's Latin-share check), and writes the link check's manifest (60.08).

## Deploy, go-live and rollback

`.github/workflows/website-deploy.yml` deploys on a push to `main` that changes the site or a
source it is built from, by hand, and monthly (security.txt's Expires). The steps are in
`tools/cloudflare/website-deploy.mjs`: record the version serving now, upload the new version
(not live) and smoke its preview URL, promote it at 100% and apply the custom domains
(`maktaba.tv`, `www.maktaba.tv`), smoke the live site, and roll back to the recorded version
when that smoke fails. A first deploy creates the Worker with no routes first, since a version
cannot be uploaded to a Worker that does not exist.

It uses `CLOUDFLARE_API_TOKEN_WEBSITE` when set, else the shared `CLOUDFLARE_API_TOKEN` (no
DNS Edit: Cloudflare creates the custom domains' records itself). It goes live only when the repository variable `WEBSITE_LIVE = true`
is set, with a token and `CLOUDFLARE_ACCOUNT_ID`; otherwise it writes nothing, prints a notice
naming what is missing and passes. The read-only token check runs on every run once a token and
the account are set, and names any missing permission before go-live. See
`deploy/cloudflare/README.md` for the token.

**Rollback by hand:** Actions → Deploy maktaba.tv → Run workflow with `rollback: true`, and
optionally `version` (an id from the run summary or `wrangler deployments list`). Without a
version it returns to the deployment before the current one. The live smoke runs after it.

Never run `wrangler deploy`, `versions deploy` or `triggers deploy` by hand with this
`wrangler.toml`: it carries the custom domains.
