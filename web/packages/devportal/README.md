# maktaba.dev: the developer portal

maktaba.dev is the developer domain: the wiki, the source, the API reference, the plugin
developer docs, the changelog and `security.txt`. (maktaba.tv is for users. maktaba.app is
not ours.)

The site is a Cloudflare Worker (`src/worker.ts`) in front of static assets (`dist/`). It
has no backend, no KV and no secrets. Every request goes through the Worker, which:

- redirects `www.maktaba.dev` and plain http to `https://maktaba.dev`;
- redirects `/security.txt` to `/.well-known/security.txt`;
- serves the assets, and the 404 page with a 404 status for a missing path;
- sets the headers on every response (`src/headers.ts`): a CSP for each part of the site,
  HSTS, `X-Content-Type-Options`, `Referrer-Policy`, `frame-ancestors 'none'` and
  `X-Frame-Options: DENY`, and the cache policy (hashed wiki bundles immutable, HTML
  revalidated, the rest an hour, with `?v=<hash>` on the portal's own scripts and styles).

## What is on the site

| Path | What | From |
| --- | --- | --- |
| `/` | the landing page, English and Arabic | `scripts/build.mjs`, `scripts/strings.mjs` |
| `/wiki/` | the wiki app, without the design mockups | `web/wiki-app` (built with `--base /wiki/`), `public/db/*.json`, `specs/diagrams` |
| `/api/` | the API reference: Redoc, served from this site | `shared/api/openapi.json`, the `redoc` package |
| `/plugins/` | Epic 54's developer-facing stories | `specs/epics/54-plugins` (the list in `build.mjs`) |
| `/changelog/` | the changelog | `CHANGELOG.md` |
| `/security/policy/`, `/security/hall-of-fame/` | the disclosure policy | `scripts/strings.mjs` |
| `/.well-known/security.txt` | RFC 9116 | `api/internal/security/disclosure.go` (the API's own policy) |
| `/robots.txt`, `/sitemap.xml` | for crawlers | the build |

Every string the portal's own pages show is in English and Arabic (`scripts/strings.mjs`).
The EN/AR button swaps them and sets `dir="rtl"` for Arabic. The first visit follows the
browser's language, and the choice is kept in the browser. The wiki, the plugin docs, the
changelog and the API reference stay in their source language, English.

### The public-safety filter

The site is public, so the build is an **allow-list**. Nothing reaches `dist/` unless
`scripts/build.mjs` names it: `DOCS` for markdown pages, `FILES` for files copied as they
are, and `WIKI_ALLOW` for the wiki's build output. Publishing a new doc means adding it there.
Never published: `docs/claude-trail.md`, `docs/audits/`, `docs/security/advisories/` (the
findings; only the policy is public), the wiki's mockups (their sample data has home paths and
LAN addresses), and anything else not on a list.

Markdown is scrubbed as it is rendered. A changelog or spec bullet that mentions internal
material is dropped, a machine's own `.local` name becomes `[name].local`, and a link to an
unpublished file goes to GitHub, or nowhere if the file is internal.

The build then runs `scripts/scan.mjs` over `dist/` and fails on any finding. The scan looks
for home paths (`/Users/…`), the Claude trail, audits, private and tailnet IP ranges,
personal `.local` and `.ts.net` names, private keys, cloud and API tokens, JWTs, secret
assignments and Cloudflare account ids. CI runs it again, and `src/site.test.ts` checks the
scan against samples and runs it on the built site.

## Build and preview locally

Node 22 (`.nvmrc`; wrangler 4 needs it).

```sh
cd web/packages/devportal
npm ci
npm ci --prefix ../../wiki-app   # the wiki's own dependencies, once
npm run build                    # assembles dist/ and runs the public-safety scan
npm test                         # the Worker, the build and the built site
npm run dev                      # wrangler dev on http://localhost:8787
```

`npm run dev` turns the canonical redirects off (`--var CANONICAL_REDIRECTS:off`). wrangler
dev hands the Worker the route's own URL over http, and the redirects would loop. Rebuild
(`npm run build`) to see a change; wrangler dev serves `dist/` as it is.

## How it deploys

`.github/workflows/devportal-deploy.yml` deploys **on every push to main that changes what
the site is built from** (the portal, the wiki, the Epic 54 docs, the changelog, the OpenAPI
description, the disclosure policy, the diagrams, the icons), and by hand from Actions →
*Deploy maktaba.dev* → *Run workflow*. Within one run:

1. **build**: the same `_devportal.yml` as the pull request check. Typecheck, build, the
   public-safety scan and the tests, then the built `dist/` is uploaded.
2. **deploy**: runs only if build passed. It downloads that same `dist/` and runs
   `wrangler deploy` through `cloudflare/wrangler-action`, with wrangler pinned to the
   package's version.
3. **smoke check**: fetches `https://maktaba.dev/` (200, the landing page, HSTS and CSP),
   `https://maktaba.dev/.well-known/security.txt` (the contact and an `Expires`) and checks
   that `www` redirects to the apex. It retries for up to 5 minutes, which is enough for a
   first deploy's certificate.

One deploy runs at a time (concurrency group `devportal-deploy`, never cancelled). Pull
requests never deploy: `ci.yml`'s `devportal` job builds and tests them and uploads the
built site as the `devportal-site` artifact, for a look before merging.

If `CLOUDFLARE_API_TOKEN` or `CLOUDFLARE_ACCOUNT_ID` is missing, the deploy job prints
"Cloudflare secrets not set: skipping deploy" and passes, so main stays green.

## Owner setup (once)

The only step is the token and the two secrets. Wrangler creates the rest itself.

1. **Create a Cloudflare API token** (dashboard → My Profile → API Tokens → Create Token →
   Custom token) with:
   - Account → **Workers Scripts: Edit** (this account only);
   - Zone → **Workers Routes: Edit**, **Zone: Read** and **DNS: Edit**, limited to
     *Specific zone: maktaba.dev*. A custom domain writes its own DNS record and
     certificate, which needs DNS: Edit.
2. **Add the secrets** to the repository or the organisation (Settings → Secrets and
   variables → Actions):
   - `CLOUDFLARE_API_TOKEN`: the token;
   - `CLOUDFLARE_ACCOUNT_ID`: the account id (dashboard → Workers & Pages, right column).

The next push to main that touches the site deploys it, or run the workflow by hand.

The domains are custom domains in `wrangler.toml` (`maktaba.dev` and `www.maktaba.dev`).
On deploy, Cloudflare creates their DNS records and certificates. Nothing needs pointing by
hand, but check in the dashboard that:

- the maktaba.dev zone is in the account `CLOUDFLARE_ACCOUNT_ID` names, and active;
- no other DNS record (A, AAAA or CNAME) is on `maktaba.dev` or `www.maktaba.dev`, and no other
  Worker or Pages project holds either name. A custom domain owns its hostname, so delete a
  conflicting record first.

The Worker has no `workers.dev` URL (`workers_dev = false`).

## Changing the site

- **A new page or doc:** add it to `DOCS` (or `FILES`) in `scripts/build.mjs`, and its
  strings to both languages in `scripts/strings.mjs`. The tests fail on a key that is missing
  in one language.
- **The security contact or expiry:** change `DefaultPolicy()` in
  `api/internal/security/disclosure.go`, which the API serves too. The build fails once
  `Expires` has passed, and warns 30 days before.
- **HSTS:** a year, without `includeSubDomains` or `preload`. Add them only when every
  maktaba.dev host is HTTPS-only.
