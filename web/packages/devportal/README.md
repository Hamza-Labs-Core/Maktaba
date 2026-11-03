# maktaba.dev: the developer portal

maktaba.dev is the developer domain, for people who extend Maktaba or contribute to it: the
wiki (every epic, story and plan, in English and Arabic), the plugin guides, the contributing
guides, security (the policy, `security.txt` and the rules plugins and contributors follow),
the API reference and the changelog. (maktaba.tv is for users. maktaba.app is not ours.)

The site is a Cloudflare Worker (`src/worker.ts`) in front of static assets (`dist/`). It
has no backend, no KV and no secrets. Every request goes through the Worker, which:

- redirects `www.maktaba.dev` and plain http to `https://maktaba.dev`;
- redirects `/security.txt` to `/.well-known/security.txt`, and the paths the site no longer
  has (`MOVED` in `src/headers.ts`): `/plugins/story-54-…` to `/plugins/`;
- serves the assets, and the 404 page with a 404 status for a missing path;
- sets the headers on every response (`src/headers.ts`): a CSP for each part of the site,
  HSTS, `X-Content-Type-Options`, `Referrer-Policy`, `frame-ancestors 'none'` and
  `X-Frame-Options: DENY`, and the cache policy (HTML revalidated, the wiki's content-hashed
  bundle for good, the rest an hour, with `?v=<hash>` on the portal's own scripts and styles).

## What is on the site

| Path                                           | What                                            | From                                                         |
| ---------------------------------------------- | ----------------------------------------------- | ------------------------------------------------------------ |
| `/`                                            | the landing page, English and Arabic            | `scripts/build.mjs`, `scripts/strings.mjs`                   |
| `/wiki/`, `/ar/wiki/`                          | the wiki: every epic, story and plan, no mockups | `web/wiki-app` (its allow-list, `WIKI_ALLOW` in `build.mjs`) |
| `/plugins/`                                    | building plugins: guides                        | `docs/developers/plugins/` (the list in `build.mjs`)         |
| `/contributing/`                               | working on Maktaba: guides                      | `docs/developers/contributing/`                              |
| `/security/conformance/`                       | security rules for plugins and contributors     | `docs/developers/security/conformance.md`                    |
| `/api/`                                        | the API reference: Redoc, served from this site | `shared/api/openapi.json`, the `redoc` package               |
| `/changelog/`                                  | the changelog                                   | `CHANGELOG.md`                                               |
| `/security/policy/`, `/security/hall-of-fame/` | the disclosure policy                           | `scripts/strings.mjs`                                        |
| `/.well-known/security.txt`                    | RFC 9116                                        | `api/internal/security/disclosure.go` (the API's own policy) |
| `/robots.txt`, `/sitemap.xml`                  | for crawlers                                    | the build                                                    |

Every page is in English and Arabic: `/x/` in English, `/ar/x/` in Arabic, right to left
(`<html lang="ar" dir="rtl">`), each with a link to the other (the language switch, and
`rel="alternate" hreflang` for crawlers). Where each language comes from:

| What                    | English                        | Arabic                                           |
| ----------------------- | ------------------------------ | ------------------------------------------------ |
| the portal's own pages  | `scripts/strings.mjs` (`en`)   | `scripts/strings.mjs` (`ar`)                     |
| a guide                 | `docs/developers/<group>/x.md` | its twin, `docs/developers/<group>/x.ar.md`      |
| the changelog           | `CHANGELOG.md`                 | `CHANGELOG.ar.md`                                |
| the API reference prose | `shared/api/openapi.json`      | `shared/api/openapi.ar.json`, an overlay (below) |
| the wiki                | `web/wiki-app/public/db/`      | `web/wiki-app/public/db/ar/`, from the store in `docs/wiki/db/ar/` |
| the wiki app's own text | `web/wiki-app/src/lib/i18n.ts` (`EN`) | `web/wiki-app/src/lib/i18n.ts` (`AR`)      |

The API reference's Arabic is an overlay: `shared/api/openapi.ar.json` maps the JSON pointer
of every `summary` and `description` (and the info title, and each tag's name) to the English
it translates and its Arabic. The build writes `/ar/api/openapi.json`, the description with
that prose in Arabic (a tag keeps its `name`, which operations refer to, and gets an
`x-displayName`), and the Arabic page points Redoc at it. Redoc's three panels stay left to
right; its rendered prose reads right to left. Redoc's own few labels (search, download) are
in English: Redoc has no way to translate them.

**The parity checks** (`scripts/parity.mjs`) fail the build when the languages drift: a guide
without its `.ar.md` (or the other way round); a release, section or entry in one changelog
and not the other; an OpenAPI string with no Arabic, or whose English changed since it was
translated; a wiki entry with no Arabic, or whose English changed since it was translated
(each Arabic entry records the hash of the English it translates, `i18n.source`); an English
page with no `/ar/` page (or the other way round); and an Arabic page or wiki file that is
mostly English (over 20% of its prose letters, outside code, in Latin script).

**The wiki** is built once (`web/wiki-app`, base `/wiki/`, `VITE_WIKI_NO_MOCKUPS=1`) and
published twice: `/wiki/` reads `/wiki/db/`, and `/ar/wiki/` (the same bundle, which picks
its language from the path) reads `/ar/wiki/db/`. The build writes `/ar/wiki/index.html` with
`lang="ar" dir="rtl"` and its own Arabic title. Its CSP (`CSP_WIKI`) allows Google Fonts and
the diagrams.net viewer frame. CI needs `npm ci` in `web/wiki-app` as well as here.

### The public-safety filter

The site is public, so the build is an **allow-list**. Nothing reaches `dist/` unless
`scripts/build.mjs` names it: `DOCS` (from `GUIDES`) for markdown pages and `FILES` for
files copied as they are. Publishing a new guide means adding it there. Never published:
`docs/claude-trail.md`, `docs/audits/`, `docs/security/advisories/` (the findings; only the
policy is public), the specs, and anything else not on a list.

Markdown is scrubbed as it is rendered. A changelog or guide bullet that mentions internal
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
npm run build    # assembles dist/ and runs the public-safety scan
npm test         # the Worker, the build and the built site
npm run dev      # wrangler dev on http://localhost:8787
```

`npm run dev` turns the canonical redirects off (`--var CANONICAL_REDIRECTS:off`). wrangler
dev hands the Worker the route's own URL over http, and the redirects would loop. Rebuild
(`npm run build`) to see a change; wrangler dev serves `dist/` as it is.

## How it deploys

`.github/workflows/devportal-deploy.yml` deploys **on every push to main that changes what
the site is built from** (the portal, the developer guides, the changelog, the OpenAPI
description, the disclosure policy, the icons), on the 1st of every month (so
`security.txt`'s `Expires` rolls forward), and by hand from Actions → _Deploy maktaba.dev_ →
_Run workflow_. Within one run:

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
     _Specific zone: maktaba.dev_. A custom domain writes its own DNS record and
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

- **A new guide:** write it in `docs/developers/<group>/` with its Arabic twin (`x.ar.md`)
  and add it to `GUIDES` in `scripts/build.mjs`. **A changelog entry:** in `CHANGELOG.md` and,
  in Arabic, in `CHANGELOG.ar.md`. **An API description:** its Arabic in
  `shared/api/openapi.ar.json`. **A new page:** add it to the build, and its
  strings to both languages in `scripts/strings.mjs`. The tests fail on a key that is missing
  in one language.
- **The security contact:** change `DefaultPolicy()` in
  `api/internal/security/disclosure.go`, which the API serves too. The expiry needs no edit:
  `Expires` is `ExpiryMonths` (11) after the day the file is rendered, per request in the API
  and per build here, and the monthly deploy keeps the live file 10 to 11 months ahead
  (RFC 9116 asks for under a year).
- **HSTS:** a year, without `includeSubDomains` or `preload`. Add them only when every
  maktaba.dev host is HTTPS-only.
