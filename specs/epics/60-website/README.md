# Epic 60: maktaba.tv, the website households find Maktaba on

> **Status:** spec (2026-11-11). **Where:** the apex `maktaba.tv` and `www.maktaba.tv` only, on
> the Cloudflare zone the repo already describes (`deploy/cloudflare/zone.maktaba.tv.json`). The
> other names on the zone belong to other epics and are not touched:
> - `app.`, `api.` and `admin.`, `relay.` and `*.relay.`: [Epic 50](../50-cloud-no-downtime/README.md);
> - `watch.`: Epic 50 (50.21, 50.27), on [Epic 34](../34-tv-public-site/README.md)'s site;
> - `plugins.`: [Epic 54](../54-plugins/README.md).
>
> **Builds on:**
> - [Epic 17](../17-ux-design-system/README.md) (the design tokens; it left the marketing site as
>   a separate effort, and no epic has owned it since);
> - [Epic 41](../41-help-guide/README.md) (the help topics, in English and Arabic);
> - [Epic 59](../59-nas/README.md) (the install guides, already built by `web/packages/install`
>   and never deployed);
> - [Epic 50](../50-cloud-no-downtime/README.md) (the copyright page and notice form of 50.24, the
>   plans, the Workers deploy and smoke checks of 50.34);
> - [Epic 53](../53-logs-errors-reports/README.md) (what the apps send, and to whom);
> - maktaba.dev (`web/packages/devportal`): the model for the build, the deploy and the
>   Latin-share check.
>
> **Wiki:** [epic-60-website.md](../../../docs/wiki/epics/epic-60-website.md).

## 1. Why

Requirement: every maktaba.tv address the product prints opens a real page, in English and in
Arabic, and a household that hears of Maktaba can learn what it is, see it, download it for each
of its devices, install it and get help, on a site that looks as good as the apps.

Today maktaba.tv serves nothing. The zone is active on Cloudflare with no record on the apex, on
`www` or on any other name. The product links there anyway:
- **Errors:** every API error carries an RFC 9457 type URI under `https://maktaba.tv/problems/`.
  A person who follows one lands on nothing.
- **Install:** the install guides of Epic 59 are built (`pnpm -C web build:install` writes
  `web/dist-install/`) and linked from the release notes, from maktaba.dev and from the guides
  themselves, but never deployed.
- **Front door:** TrueNAS's app catalog entry names `https://maktaba.tv/` as Maktaba's home page,
  and the desktop app's menu opens `https://maktaba.tv/docs`.
- **Copyright:** Epic 50 requires a copyright page with a notice form before public channels go
  live (50.24).
- **The image:** every install guide pulls `ghcr.io/hamza-labs-core/maktaba-server`, and an
  anonymous pull is refused (`authentication required`). A household that follows a guide cannot
  install.

A website is also the first thing a household judges Maktaba by. It has to be well designed,
fast and accessible in both languages. Good design is a goal of this epic, with its own story
that comes first (60.01) and a quality bar in Acceptance (§10).

## 2. What exists, and the gaps

Checked on `main` at 154438067 (2026-11-11).

### The links the product prints

`git grep -o -E 'https://maktaba\.tv/[A-Za-z0-9_./#?=%-]*' -- ':!docs/trail' ':!specs'` finds
**321** links. Without the scheme there are a few more (the release notes).

| Path | Links | Where | Today |
|---|---|---|---|
| `/problems/<slug>` | 257, **120 distinct slugs** as literals | the API's handlers, `httperror/types.go`, the OpenAPI file, the Android, Apple and web clients that compare types, `docs/licensing.md`, the plugin guide | dead. There is **no registry**: `types.go` holds 18 constants, the rest are literals in each handler, and **seven places build the slug at runtime from a code** (`handlers/cluster/cluster.go:332`, `components/components.go:104`, `plugins/admin.go:145`, `review/review.go:455` and `:459`, `rules/rules.go:410`, `tvremote/viewer.go:68`), so no grep can list every type the API sends |
| `/install/<guide>/`, `/ar/install/<guide>/` | 45 | the guides' own canonical and language links, maktaba.dev's Apps pages (`scripts/apps.mjs`), the repo README, `deploy/nas/` (the Unraid template and READMEs, the TrueNAS app's README) | built (9 guides × 2 languages), dead |
| `maktaba.tv/install/synology`, `/install/truenas` (no scheme) | 2 | the release notes template (`tools/release/releasenotes/notes.md.tmpl`) | dead |
| `/` | 6 (4 outside tests) | TrueNAS app (`deploy/nas/truenas/ix-dev/community/maktaba/app.yaml` `home:` and sources), its README; two tests | dead |
| `/docs` | 1 | the desktop app's Help menu (`web/src/lib/desktop.ts:46`) | dead |
| `/schema/log-line.json` | 1 | the `$id` of `shared/log/schema/line.schema.json` | an identifier. Publishing the schema there costs nothing |
| `/help`, `/video/v1`, `/watch/123`, `/library/abc`, `/billing` | 10 | tests (deep-link parser inputs, a billing fixture) and two code comments that document the deep-link parser (`web/src/lib/native.ts:44`, `apps/mobile/src/native-shell.ts:39`) | not pages. No app claims maktaba.tv as an associated domain, so no `apple-app-site-association` or `assetlinks.json` is needed. The two comments are allow-listed with that reason (60.08) |
| `/pricing`, `/legal/copyright` | 0 in the product | specs only: Epic 25 story 25.15 (also in the wiki database maktaba.dev publishes, as spec text); Epic 50 story 50.24 (planned in `web/packages/broadcast-edge/site/legal/`) | not built |

### What can be built on

| Area | Today |
|---|---|
| maktaba.dev | `web/packages/devportal`: plain Node build scripts (no client framework), a Worker in front of static assets (`run_worker_first`, security headers, www and http redirects, `404-page`), custom domains on the apex and `www`. Deployed by `devportal-deploy.yml`, which builds, tests and scans first, **skips green without its Cloudflare secrets**, and smoke-checks the live site after. Live (`https://maktaba.dev/` answers 200), so the account's secrets exist |
| Cloud web deploy | `cloud-web-deploy.yml`: a token check, build and test, `wrangler deploy`, `tools/cloudflare/smoke.mjs`, and **`rollback: true` by hand**, which runs `wrangler rollback`. app. and admin. attach by **Workers Routes**, because their DNS records are the Tunnel's CNAMEs |
| Install guides | `web/packages/install` + `web/vite.install.config.ts`: `nas`, `docker`, `synology`, `truenas`, `unraid`, `qnap`, `ugreen`, `after-setup`, `troubleshooting`, each with its `/ar/` twin; dark; the docker and synology pages fill a compose file in the page, and nothing leaves it; the build fails an Arabic page over 20% Latin (`src/latin.ts`, from devportal's `parity.mjs`); Playwright T04/T05 in `web-ui.yml`. Its README ends: publishing it is a maintainer's action |
| Help | Epic 41: **44 topics** in `web/src/help/content/{en,ar}/*.md`, shown in the app at `/help/<topic>`. They link to app routes (`/settings/…`, `/admin/…`, `/account/…`, `/queue…` and others: 167 links in the English topics, 104 of them to settings and admin) and to each other (`/help/…`: 330) |
| Privacy | `cloud/internal/privacy`: the relay's machine-readable policy (`CurrentPolicy`: controller Hamza Labs, `privacy@maktaba.tv`, what is and is not collected, retention, rights); `docs/app-privacy.md`: what the apps send, and to whom. No analytics or crash-reporting service is built into any app |
| Design tokens | `web/design-system/tokens/tokens.json` (color brand/neutral/semantic, `type` and **`type-arabic`** scales, spacing, radius, elevation, motion, breakpoints) → `web/src/styles/tokens.css`. `--font-family` names IBM Plex Sans Arabic, but the web ships no font file; the Apple and Android apps ship IBM Plex Sans Arabic and Amiri Bold |
| Brand | the app icons in `web/public/icons/` (favicon 32, touch icon, 192, 512, maskable); no wordmark; the Arabic product name in the web catalog is «مكتبة» |
| Screenshots | maktaba.dev's Apps pages take dark, test-data-only screenshots of every app (MockHome UI tests, Roborazzi references, a scratch branch stack for the web), WebP ≤ 100 KB (`web/packages/devportal/README.md`) |
| Test tools | `axe-core` is a web dev dependency; no Playwright screenshot references and no Lighthouse run anywhere in CI |
| Streaming's problems | the streaming service types its problems `https://maktaba.invalid/problems/…` on purpose (`streaming/internal/httpx/problem.go:12`: not a URL a person follows); the Apple and Android clients compare them |
| Release assets | the GitHub release carries the Mac app (`Maktaba_<v>_aarch64.dmg`, `Maktaba-Core_…dmg`), Windows (`Maktaba_<v>_x64-setup.exe`, `maktaba-windows-portable-<v>.zip`, `Maktaba-Player_<v>_x64.msi`), Linux (`.AppImage`, `.deb`, `.rpm`, the headless payload), the server packages, the Docker, Synology and TrueNAS files, and `release.json` / `latest.json`. `release.json`'s `assets` lists only the server archives, and `checksums.txt` has no desktop installers; the Releases API gives every asset's size and `sha256` digest |
| Phone and TV apps | TestFlight (`docs/testflight.md`) and Google Play's internal track (`docs/google-play.md`) only: **no public store listing** |
| Package repositories | `packages.yml` publishes apt, dnf and the Homebrew tap once `vars.MAKTABA_PACKAGES_URL` and `vars.HOMEBREW_TAP_REPO` are set; **neither is set**, so the release notes' repository lines do not render |
| The server image | `ghcr.io/hamza-labs-core/maktaba-server`, signed, amd64 + arm64; **private**: an anonymous token request to ghcr.io is refused, while the repository is public |

### Gap list

- **G1** maktaba.tv serves nothing, and 321 links in the product, its docs and its catalog
  entries are dead.
- **G2** The API's problem types have no registry, so no page per type can be generated and no
  check can prove every type has one.
- **G3** The install guides are built but unpublished, and the image they pull is private.
- **G4** No front door: nothing says what Maktaba is, shows it, or offers a download per device.
- **G5** No copyright page, privacy page or terms on the household-facing domain, though Epic 50
  needs the copyright page before public channels.
- **G6** No visual direction for anything outside the apps: no wordmark, no marketing type scale,
  no Arabic display face on the web, no imagery.
- **G7** Nothing stops a new maktaba.tv link from shipping dead, or the image going private again.

## 3. Goals

1. **No dead links.** Every maktaba.tv URL in the product, the release notes, the docs and the
   catalog entries opens a page, and CI fails a new one that does not (60.08).
2. **One page per problem type,** generated from a registry the API itself sends from, so a type
   cannot ship without its page (60.06).
3. **Designed first.** A visual direction, page layouts for every width in both directions, and
   component specs, reviewed as mockups before any page is built on them (60.01).
4. **Written for households.** Plain, warm copy without jargon, and Arabic written natively, not
   translated in tone (60.02).
5. **What Maktaba is, shown truthfully.** Real dark screenshots of the real apps on demo content,
   in English and Arabic (60.09).
6. **The right download for each device,** from the release's own assets, showing only what a
   household can install today (60.11).
7. **Help before and after installing,** from the same topics the app shows (60.12).
8. **Fast, static, accessible and private:**
   - Lighthouse 95+ on mobile;
   - WCAG 2.2 AA;
   - no layout shift;
   - no tracking, no cookies and no third-party requests (60.04).
9. **English and Arabic everywhere,** `/ar/…` right to left, passing the same Latin-share check as
   maktaba.dev.
10. **Deployed like maktaba.dev:**
    - from CI on main;
    - green and skipped without secrets;
    - smoke-checked;
    - rolled back by hand the way `cloud-web-deploy.yml` does (60.03).

## 4. Non-goals

- **The other names on the zone.** app., api., admin., relay., watch. and plugins. stay with
  their epics. The website attaches to `maktaba.tv` and `www.maktaba.tv` only, and changes no
  zone-wide rule.
- **Universal links / app links on maktaba.tv.** No app claims the domain today. Adding it is an
  app change in its own epic; the site would then serve the association files.
- **Accounts, sign-in or billing on the site.** Those are app.maktaba.tv (Epic 50). The site
  links there once it is live.
- **A blog, news or a forum.** Release notes stay on GitHub; the site links to them.
- **The developer wiki.** It stays on maktaba.dev. The site links to it in the footer.
- **Video on the home page,** and marketing motion beyond small transitions that honour
  `prefers-reduced-motion` (Epic 17 kept motion out of scope too).
- **A pricing page with invented prices.** It waits for Epic 50's plans (60.13).
- **Pages for the streaming service's `maktaba.invalid` problem types.** They are deliberately
  not URLs; moving them to maktaba.tv would be a streaming and client change of its own.

## 5. The site at a glance

| Path (each with `/ar/…`) | What it is | Story | Phase |
|---|---|---|---|
| `/` | Phase 1: a short front door (what Maktaba is, install, download from GitHub, help, maktaba.dev). Phase 2: the home page | 60.03, 60.10 | 1, 2 |
| `/download/` | every device, with its real release asset | 60.11 | 2 |
| `/install/…` | Epic 59's nine guides, then the per-platform install pages | 60.05 | 1 |
| `/help/`, `/help/<topic>/` | Epic 41's topics | 60.12 | 3 |
| `/problems/`, `/problems/<slug>/` | one page per problem type, and an index | 60.06 | 1 |
| `/legal/privacy/`, `/legal/terms/`, `/legal/copyright/` | privacy, terms, copyright with the notice form | 60.07 | 1 |
| `/pricing/` | once Epic 50's plans are decided | 60.13 | 3 |
| `/schema/log-line.json` | the log schema, at its `$id` | 60.03 | 1 |
| `/.well-known/security.txt` | the same file maktaba.dev serves, both `Canonical` lines | 60.03 | 1 |
| `/docs` | a permanent redirect to `/help/` (Phase 1: to `/`) | 60.03 | 1 |
| anything else | a 404 page in the visitor's language, with search links to help and problems | 60.03 | 1 |

## 6. Design

### 6.1 Where it lives and how it is built (60.03)

```
web/packages/website/            the site: a package in web's pnpm workspace, with its wrangler.toml
├── design/                      60.01: the direction, mockups (HTML → PNG), review records
├── copy/                        60.02: the voice guide and each page's copy, en + ar
├── src/worker.ts                headers, redirects (www → apex, http → https, /docs), 404 status
├── src/pages/                   hand-made pages (home, download, legal, 404), en + ar
├── scripts/build.mjs            the build: pages + generated families + install + assets → dist/
├── scripts/problems.mjs         /problems from shared/api/problems.json (60.06)
├── scripts/help.mjs             /help from web/src/help/content (60.12)
├── scripts/download.mjs         /download from the GitHub Releases API + downloads.json (60.11)
├── scripts/links.mjs            the link check (60.08)
├── static/                      fonts (self-hosted), icons, social cards, screenshots
└── e2e/                         Playwright: visual references, axe, Lighthouse (60.04)
```

- **Built like maktaba.dev:** Node build scripts writing static HTML, CSS and a little script for
  copy buttons, the install forms and the language hint. No client framework.
- **One build, one dist:** the build runs `pnpm -C web build:install` and copies the contents of
  `web/dist-install/` (which already holds `install/<guide>/` and `ar/install/<guide>/`) to the
  root of `dist/`. The install build's hashed files move to `install/assets/`
  (`build.assetsDir`), so they never share the site's `/assets/`. Because that build runs over
  the web workspace, the site's CI and deploy install the web workspace
  (`pnpm -C web install --frozen-lockfile`). The install package stays where it is, with its own
  tests.
- Everything a page shows comes from a source the product already owns:
  - problem pages from the registry;
  - help from Epic 41's Markdown;
  - downloads from the release;
  - privacy from `CurrentPolicy` and `docs/app-privacy.md`;
  - the schema from `shared/log/schema`.
- Only the home, download layout, legal text and 404 are written for the site.

### 6.2 Hosting: Workers static assets, custom domains on the apex and www (60.03)

- **One Worker, `maktaba-website`, in front of static assets:** the shape maktaba.dev already
  runs.
  - `run_worker_first = true`, so the Worker sets the security headers and the redirects;
  - `not_found_handling = "404-page"`;
  - `html_handling = "auto-trailing-slash"`;
  - `workers_dev = false`.
- **Attached as custom domains on `maktaba.tv` and `www.maktaba.tv`.**
  - Neither name has a record today. Wrangler creates both records and their certificates on
    deploy.
  - A custom domain owns its exact hostname only, so no other name on the zone is affected.
  - app. and admin. use Workers Routes because the Tunnel already owns their records. The apex
    has no such record, and a route would need a dummy proxied record to hang on.
- **Why Workers static assets, not Pages:**
  - It is what maktaba.dev and the two cloud web Workers already run, so the deploy action,
    `wrangler rollback`, `tools/cloudflare/smoke.mjs`, the token check and the token table are
    reused, extended by 60.03 (a `--base` and negative checks in the smoke, a `dns` piece in the
    token check, a rollback to a recorded version).
  - Versions give a preview URL per upload for staging and an atomic switch for the deploy.
  - The Worker gives headers and redirects in code that is tested, not in `_headers` files.
  - Cloudflare's own direction is to fold Pages into Workers.
  - Pages would bring a second deploy model, a second rollback path (the dashboard or its API)
    and preview branches the repo does not use.
- **Security headers:**
  - HSTS with `includeSubDomains` **off**: the relay's names are DNS-only and must not inherit
    it by surprise;
  - a strict CSP (`default-src 'self'`, no third-party origin; `frame-ancestors 'none'`). The one
    exception is the copyright page (`/legal/copyright/`, `/ar/legal/copyright/`), whose notice
    form needs Turnstile (`script-src` and `frame-src https://challenges.cloudflare.com`) and the
    cloud API (`connect-src https://api.maktaba.tv`), on that path only;
  - `Referrer-Policy: strict-origin-when-cross-origin`;
  - `X-Content-Type-Options`;
  - `Permissions-Policy` denying everything the site does not use.
- **Caching:**
  - Hashed assets are immutable for a year.
  - HTML is `max-age=0, must-revalidate`, so a deploy shows at once.

### 6.3 Deploy, staging and rollback (60.03)

`website-deploy.yml`, on a push to main that changes the site or its sources, by hand, and
monthly (security.txt's `Expires`, as on maktaba.dev):

1. **Build and test.** This is the same job the pull request ran (`_website.yml`):
   - the build;
   - unit tests;
   - the link check (60.08);
   - the Latin-share check;
   - axe;
   - the visual references;
   - Lighthouse on every page in both languages (60.04).

   The deploy uses that job's `dist/` artifact.
2. **Secrets and the go-live switch.** The token is `CLOUDFLARE_API_TOKEN_WEBSITE` when set,
   and otherwise the shared `CLOUDFLARE_API_TOKEN` that also deploys maktaba.dev. The repository
   variable `WEBSITE_LIVE = true` is the only go-live switch: without it, or without a token or
   `CLOUDFLARE_ACCOUNT_ID`, the job writes nothing, prints a notice naming what is missing and
   passes. `token-check.mjs --pieces workers,dns --zone maktaba.tv` is read-only and runs on
   every run once a token and the account are set, live or not, so a missing permission is
   named before go-live; it fails the run only once the site is live.
3. **Record the live version.** `wrangler deployments status` gives the version now serving. It
   is the version a rollback returns to. A bare `wrangler rollback` would pick the version
   uploaded before the latest, which with upload-then-promote can be one that never passed.
4. **Stage.** `wrangler versions upload` makes a new version with a preview URL, and is not yet
   live. `smoke.mjs website-preview --base <preview URL>` checks what a preview can show: the
   front door in both languages, one problem page, one install guide, the 404 status, and the
   headers.
5. **Promote.** `wrangler versions deploy <id>@100%`, one atomic switch, then
   `wrangler triggers deploy`, which applies the custom domains. `versions upload` and
   `versions deploy` do not apply routes or domains. The wrangler version is pinned, since
   `triggers deploy` is marked experimental.
   - No percentage split for a static site: a page from the new version could ask for a hashed
     asset only the old version has.
   - Epic 50's gradual rollouts are for Workers with state, which this one does not have.
6. **Smoke live.** The full `website` target on `https://maktaba.tv`, plus the www and http
   redirects. When a check never passes, the job runs `wrangler rollback <recorded id>` and
   fails.
7. **By hand,** `rollback: true` runs the same rollback as in `cloud-web-deploy.yml`, to an
   explicit version: the optional `version` input, or else the version of the deployment before
   the current one (`wrangler deployments list`).

**No downtime:**
- each switch is atomic;
- the previous version stays one command away;
- one deploy runs at a time (`concurrency`, never cancelling);
- nothing has state to migrate.

### 6.4 Problem types: one registry, one page each (60.06)

- **The registry is a data file,** `shared/api/problems.json`, read by the API, the site and the
  checks. Each entry has:
  - the slug;
  - the HTTP status;
  - who meets it (anyone, a signed-in person, an admin, an app or a node);
  - a title, what it means and what you can do, each in English and Arabic;
  - optional links to a help topic or an install troubleshooting section.
- **The API sends only registered types.** The check sits in `httperror.Write`, which every
  problem passes through, whether it was built by `httperror.Type(slug)`, by a constant or as a
  struct literal.
  - The 18 constants and every literal become `Type("…")` values. The seven places (six
    functions) that build a slug from a code call `TypeForCode(code)`.
  - **In test binaries** (`testing.Testing()`), an unregistered type is recorded. The API's test
    run fails when any was recorded (the file named by `MAKTABA_UNREGISTERED_PROBLEMS`, checked
    after `go test ./...`). A panic would be turned into a 500 by the recoverer middleware, and a
    test that checks only for an error would still pass. This follows the "should never happen"
    rule (#662).
  - **In production,** it logs `problem_type_unregistered`, keeps the status, and sends the
    registered generic type for that status (`conflict`, `forbidden`, `internal` …). A 409 never
    becomes a 500.
  - A Go test fails a literal `https://maktaba.tv/problems/` anywhere outside the registry.
  - Slugs keep today's spelling: `[a-z0-9_-]`. Four shipped slugs use underscores
    (`smart_playlist`, `automatic_collection`, `stale_queue`, `empty_source`) and clients match
    them, so they stay. New slugs use hyphens.
- **Clients:**
  - The Kotlin, Swift and TypeScript constants that compare types are checked against the
    registry by a test in each client.
  - A web test reads the slugs from the same file.
- **The site** generates `/problems/<slug>/` and `/ar/problems/<slug>/` for every entry, and an
  index grouped by area.
  - Each page says what happened, what you can do and whom to ask (the home's admin, or
    Maktaba).
  - It shows the status and the machine identifier in small type for people who need them.
- **The API's own HTML error page** (`httperror.writePage`, shown when a browser navigates to an
  error) gains a "What this means" link to the type's page.

### 6.5 Languages

- **Paths:** English at `/…`, Arabic at `/ar/…`, every page in both.
  - `<html lang dir>` is set per page.
  - `hreflang` alternates and `x-default` point at the English page.
  - The language switch links each page to its twin.
- **No automatic redirect** by `Accept-Language`: pages stay cacheable and crawlable.
  - On an English page in a browser that prefers Arabic, a small line offers the Arabic page.
  - The line can be dismissed; the dismissal is kept in `localStorage`, never a cookie.
- **Copy:** Arabic is Modern Standard Arabic, written natively in the voice of 60.02, and checked
  by the Latin-share check, at 20% as on maktaba.dev.
  - The check counts code blocks, commands, file names and product names as code.
  - Generated families (problems, help) are checked on what they render.
- **RTL:**
  - layout in logical properties only (`margin-inline-start`, `inset-inline-end`), and a lint
    refuses physical ones in the site's CSS;
  - arrows that mean "next" mirror;
  - icons and images that don't mean a direction stay as they are: logos, play symbols, media
    controls, screenshots, code;
  - numbers stay Western Arabic, as in the apps.

### 6.6 Design system for the site (60.01)

- **Start from Epic 17's tokens** (`tokens.json`): colour, spacing, radius, elevation, motion,
  breakpoints, and its separate `type` and `type-arabic` scales.
- **Extend them in a `site` group** of the same file, built the same way: display sizes, wider
  measure, marketing surfaces and accents. No hand-made token table.
- **Type:**
  - IBM Plex Sans Arabic for text in both scripts, the apps' own face;
  - a display face for headings, chosen by 60.01 (Amiri, as the apps' display face, is the
    first candidate);
  - all self-hosted as subset WOFF2 (OFL), preloaded, with metric-matched fallbacks
    (`size-adjust`) so swapping fonts shifts nothing;
  - no Google Fonts request.
- **Dark first:**
  - dark is the default;
  - a light palette follows `prefers-color-scheme: light`;
  - both pass contrast;
  - visual references are taken dark, with one light set at desktop width.
- **Components** specified in 60.01 and built once:
  - header and nav;
  - language switch;
  - footer;
  - download card per platform;
  - step list;
  - code and compose blocks with copy;
  - callouts (note, warning, tip);
  - screenshot frame per device;
  - the problem page layout;
  - the legal page layout;
  - the 404.

### 6.7 Privacy on the site

- **No analytics, no cookies, no third-party requests:**
  - fonts and images are self-hosted;
  - no embedded video;
  - no consent banner, because nothing needs consent.
- **Cloudflare's zone analytics only:** the aggregate request counts Cloudflare keeps for any
  proxied name, with no script in the page.
  - The privacy page says so.
- **The install forms** keep their rule: nothing a household types leaves the page.
- **The copyright notice form** sends only what it asks for, to Epic 50's endpoint, with
  Turnstile, and says where it goes before it is sent (60.07). Turnstile is the site's one
  third party, loaded on the copyright page only, and the privacy page names it.

### 6.8 Quality bar and visual regression (60.04)

- **Lighthouse CI** (`@lhci/cli`) on mobile emulation against the built `dist/`:
  - 95 or more for performance, accessibility, best practices and SEO;
  - cumulative layout shift 0;
  - pull requests and the deploy run every hand-made page and three representatives of each
    generated family (the longest page, the shortest, and one with code), in both languages,
    three runs each;
  - a pull request that changes a generator or a template runs the full family it renders;
  - a nightly sweep on main runs **every page** in both languages, one run per URL, in eight
    shards within 60 minutes. A failure makes the nightly red and the next deploy refuses to run
    until a fix lands.
- **axe-core** (already a dev dependency) through Playwright, with WCAG 2.2 AA rules, on every
  page in both languages. Keyboard paths, focus visible, target size (2.5.8) and reflow at 320 px
  are tested directly.
- **Visual references:**
  - Playwright `toHaveScreenshot` at **360, 768 and 1280** px wide, English and Arabic, dark;
  - every hand-made page, and each generated template with its representatives;
  - pixels compared, not bytes;
  - fonts self-hosted, so CI renders deterministically.
  - References live in `web/packages/website/e2e/__screenshots__/`, each under 1 MB.
- **A reference change needs review to merge.**
  - A pull request that changes a reference PNG must list each changed file under
    `Reference images:` in its body, with why. `_pr-body-check.yml` enforces this.
  - The reviewer reads every changed image before merging (the rule of #893).
- **Social and SEO:**
  - Open Graph and Twitter cards per page and language, rendered at build time from a template
    (1200×630, dark, the page title in its script);
  - favicon, touch and maskable icons from the app icons;
  - `sitemap.xml` with `hreflang` alternates;
  - `robots.txt`;
  - a canonical link per page.

### 6.9 Product imagery (60.09)

- Real dark screenshots of the web app, the Mac app, iPhone and iPad, Apple TV and Android TV,
  each in English and Arabic.
- **Taken in CI or on throwaway simulators**, never from the owner's library:
  - Apple from the MockHome UI tests on a per-run simulator;
  - Android TV from Roborazzi;
  - the web from a scratch branch stack;
  - the Mac app in CI only.
- **Demo content:**
  - openly licensed films and series with their own artwork (Blender's open movies, CC BY,
    credited on the legal page);
  - invented household names;
  - Arabic titles;
  - no person's name, LAN address or media path.
- **Delivery:**
  - WebP or AVIF;
  - sized per breakpoint (`srcset`), with explicit width and height;
  - lazy below the fold;
  - each in a device frame from 60.01.
- The capture is a script that can be rerun at each release, so the pictures follow the apps.

## 7. Phases

| Phase | Stories | Leaves a visitor with |
|---|---|---|
| **1. No dead links** | 60.01 part A (the shell), 60.02 part A (the voice), 60.03, 60.04, 60.05, 60.06, 60.07, 60.08 | every link the product prints opens a real page in their language: the install guides, a plain page per error, privacy, terms and copyright with its notice form, a short front door and a real 404. The image the guides pull is public, and CI keeps both true |
| **2. The front of the site** | 60.01 part B (full direction and mockups), 60.02 part B (tagline and page copy), 60.09, 60.10, 60.11 | a designed home page that says what Maktaba is and shows it in real screenshots, and a download page with the right file for each device |
| **3. Help, one design everywhere, pricing** | 60.12; 60.05, 60.06 and 60.07 moved onto the full design; 60.13 once Epic 50's plans are decided | help readable before installing, the whole site in one design, and prices once there are prices |

- **The design gate:** 60.01 starts first.
  - Its part A, the shell, is enough for Phase 1's text pages: tokens, type, header, footer,
    language switch, and the text page and problem page layouts. It is reviewed as mockups
    before Phase 1's pages are built.
  - Its part B is reviewed before any Phase 2 page story builds.
- **A design review closes each phase:** the lead compares the built pages with the mockups at
  all three widths in both languages, using the visual run's images. The review is recorded in
  `design/review-phase-N.md` before the phase is called done.

## 8. Stories

| # | Story | Area | Phase |
|---|---|---|---|
| 60.01 | [Design: the visual direction, page layouts and components, as reviewed mockups](story-60-01-design.md) | design | 1 (A), 2 (B) |
| 60.02 | [Voice and copy: plain and warm for households, Arabic written natively](story-60-02-voice-copy.md) | copy | 1 (A), 2 (B) |
| 60.03 | [The site, its hosting and its deploy: Workers static assets on the apex and www, staging, rollback](story-60-03-site-hosting.md) | web, CI, Cloudflare | 1 |
| 60.04 | [The quality bar in CI: Lighthouse, WCAG 2.2 AA, no layout shift, visual references, SEO and social cards](story-60-04-quality-gates.md) | CI, web | 1 |
| 60.05 | [The install guides on maktaba.tv](story-60-05-install-guides.md) | web | 1, 3 |
| 60.06 | [Problem types: one registry the API sends from, one page per type](story-60-06-problems.md) | API, clients, web | 1, 3 |
| 60.07 | [Legal: privacy, terms, and copyright with Epic 50's notice form](story-60-07-legal.md) | web, cloud | 1, 3 |
| 60.08 | [No dead links: the maktaba.tv link check, and a public image after every release](story-60-08-link-integrity.md) | CI, release | 1 |
| 60.09 | [Product imagery: real dark screenshots of every app, in English and Arabic, on demo content](story-60-09-product-imagery.md) | apps, CI | 2 |
| 60.10 | [The home page](story-60-10-home.md) | web | 2 |
| 60.11 | [Download: every device, with its real release asset](story-60-11-download.md) | web, release | 2 |
| 60.12 | [Help on maktaba.tv, from the app's own topics](story-60-12-help.md) | web | 3 |
| 60.13 | [Pricing, from the plans the cloud enforces](story-60-13-pricing.md) | web, cloud | 3 |

## 9. Test and rehearsal plan

Everything runs in CI on jobs scoped to the site's paths (`ci-paths.yml` gains a `website`
filter), and nothing runs against the live home or a person's library.

| What | How | Where |
|---|---|---|
| Build | `scripts/build.mjs` writes every page in both languages; fails on a missing twin, a missing image, a page over the Latin share | `_website.yml` |
| Links | every maktaba.tv URL in the repo and the release notes resolves to a file in `dist/`; every internal link on every page resolves | 60.08 |
| Problem registry | Go: every sent type registered (panic under test), no literal outside it; Kotlin, Swift, TypeScript: client constants ⊂ registry; site: one page per entry | 60.06 |
| Accessibility | axe WCAG 2.2 AA on every page × 2 languages; keyboard, focus, target size, 320 px reflow | 60.04 |
| Performance | Lighthouse mobile ≥ 95 ×4, CLS 0: representatives on pull requests and deploys, every page nightly | 60.04 |
| Visual | 360 / 768 / 1280 × en / ar, dark (+ light at 1280); reviewed reference changes | 60.04 |
| Worker | unit tests of headers, redirects, the 404 status, `/docs`; `wrangler dev` smoke in CI | 60.03 |
| Deploy | token check, live version recorded, version upload, preview smoke, promote and triggers, live smoke, rollback to the recorded version on failure; the rollback input dispatched once after the first deploy | 60.03 |
| Public image | after each release, an anonymous `docker pull` of the tag and `:latest` on amd64 and arm64, with no login | 60.08 |
| Imagery | the capture script reruns from fixtures; a test refuses an image with metadata, over budget, or missing a language | 60.09 |

**Logs in every run:** the build log, the Worker's `wrangler dev` log, the Lighthouse reports
(HTML and JSON), axe's findings and the visual diff images are kept in the run's artifacts, pass
or fail.

## 10. Acceptance (the epic)

- **Links:** every maktaba.tv URL the product, the release notes, maktaba.dev's pages and guides,
  the docs and the TrueNAS and Unraid files print answers 200 on `https://maktaba.tv`, in its
  language. Spec text (the specs and the wiki database built from them) is not product, and is
  left out. A pull
  request that adds one with no page fails CI.
- **Problems:** every problem type the API can send has a page in English and Arabic, and the API
  cannot send a type that has none.
- **Image:** an anonymous `docker pull ghcr.io/hamza-labs-core/maktaba-server:latest` works, and
  the release run fails when it stops working.
- **Quality bar, for every page in both languages** (the nightly sweep measures every page; pull
  requests and deploys measure the representatives):
  - Lighthouse on mobile scores **95 or more** for performance, accessibility, best practices
    and SEO;
  - **WCAG 2.2 AA**, with axe clean and keyboard paths tested;
  - **no layout shift** (CLS 0), with fonts and images sized before they load;
  - **correct RTL mirroring:** layout and direction arrows mirror, while logos, media controls,
    screenshots and code do not;
  - **Open Graph and social cards** per page and language;
  - **favicon and app icons**;
  - **`sitemap.xml` with `hreflang`** and a canonical link.
- **Visual regression:** screenshots of every page at 360, 768 and 1280 px in both languages run
  in CI, and a change of reference images merges only with each one listed and reviewed.
- **Design:**
  - the mockups in `design/` were reviewed before the pages built on them;
  - each phase closed with a recorded design review against them;
  - the Arabic uses a real Arabic typeface, self-hosted.
- **Imagery:** the home and download pages show real dark screenshots of each app in the page's
  language, taken on demo content.
- **Copy:** the copy follows the voice guide, and the tagline and voice carry the owner's
  approval.
- **Privacy:** no page sets a cookie or runs analytics, and no page but the copyright page loads a
  third-party resource (Turnstile, named on the privacy page). The privacy page says what
  Cloudflare's zone analytics keep.
- **Deploy:**
  - a push to main deploys after the same checks the pull request ran;
  - without the secrets the run is green and says why;
  - a failed live smoke rolls back on its own;
  - `rollback: true` returns the previous version.
- **Other names:** app., api., admin., relay., watch. and plugins. answer exactly as before the
  epic.

## 11. Decisions (2026-11-11)

1. **Workers static assets with a Worker in front, as maktaba.dev.**
   - Rejected: Pages (a second deploy and rollback model, and no reuse of the smoke and token
     tools).
   - Rejected: a Worker that runs only on a missing asset. It could not redirect `www` or set
     headers on asset hits.
2. **Custom domains on `maktaba.tv` and `www.maktaba.tv`.**
   - Rejected: Workers Routes. They need a proxied record to attach to, and the apex has none.
     Routes stay right for app. and admin., whose records the Tunnel owns.
3. **Atomic promote after a preview-URL smoke, automatic rollback to the recorded live version on
   a failed live smoke.**
   - Rejected: percentage rollouts. A split serves pages whose hashed assets only the other
     version has.
   - Rejected: a bare `wrangler rollback`. It returns to the version uploaded before the latest,
     which may be one that failed its preview.
4. **A problem registry as data (`shared/api/problems.json`), checked where every problem is
   written (`httperror.Write`).**
   - Rejected: generating pages from a grep. The seven runtime-built slugs make any grep
     incomplete.
   - Rejected: a Go-only table. The site and three clients need the same list.
   - Rejected: renaming the four underscore slugs. Clients match them.
   - Rejected: sending `internal` for an unregistered type in production. It would turn a 409
     into a 500.
5. **Help on maktaba.tv is rendered from Epic 41's own Markdown,** with app routes turned into
   "In Maktaba: Settings → …" text.
   - Rejected: linking to a home's `/help`. A visitor has no home yet.
   - Rejected: a second copy of the help. It would fall behind.
6. **The copyright page lives on maktaba.tv**, not on the broadcast edge's site as 50.24's build
   plan had it. It is a legal page for every Maktaba service. Its notice form posts to the public
   endpoint this spec proposes in 50.24 §2. Counter-notices stay where 50.24 puts them, on the
   signed-in Broadcast page, and the copyright page links there. 50.24's row and §2 are updated.
7. **The download page lists only what a household can install today,** read from the release.
   - Store badges appear when a public listing URL exists in `downloads.json`.
   - The apt, dnf and Homebrew rows appear when the package repositories are configured.
   - Rejected: "coming soon" entries. The project builds a feature or does not show it.
8. **No pricing page until Epic 50's plans are decided,** and then it is generated from the plans
   the cloud enforces.
   - Rejected: a placeholder page. Rejected: prices typed into the site.
9. **No analytics, no cookies, no third-party requests.**
   - Rejected: a consent banner with analytics behind it, which would be the only thing on the
     site needing consent.
10. **Dark first, light by system preference.**
    - Rejected: dark only (it fails people who need light).
    - Rejected: light first (unlike every app).
11. **Visual references on every hand-made page, and on representatives of the generated
    families.** Lighthouse covers every page nightly.
    - Rejected: a screenshot of all 120+ problem pages × 6 on every pull request. They differ
      only in text, which the structural and Latin-share tests check on every entry.
12. **Mockups as HTML with the real tokens and fonts, rendered to PNG,** kept in the repo with
    their source.
    - Rejected: a design tool file outside the repo, which drifts from the tokens and cannot be
      reviewed in a pull request.
13. **No automatic language redirect.** The visitor chooses, and a dismissible hint offers the
    Arabic page.
    - Rejected: redirecting by `Accept-Language`, which breaks caching and links people share.
14. **Streaming's `maktaba.invalid` problem types get no pages.** They are deliberately not URLs.
    - Rejected: moving them under maktaba.tv in this epic. That would be a streaming and client
      change with no page a person follows.

## 12. Questions only the owner can answer

All nine are decided (2026-11-11). The stories follow these decisions.

| # | Question | Decided |
|---|---|---|
| Q1 | The tagline and the site's voice | 60.02 part B proposes several taglines in English and Arabic, each with one line on what it says; the owner chooses one. No direction is set in advance |
| Q2 | Who signs off the Arabic copy? | written natively by us in Modern Standard Arabic, in the same voice; the owner reads every Arabic page and approves it before Phase 2 is done. No other reader |
| Q3 | The wordmark: the app icon alone, or a wordmark in Latin and «مكتبة»? | the app icon with the name in both scripts, "Maktaba" and «مكتبة»; 60.01 shows it in the mockups for the owner's approval |
| Q4 | Any analytics on the site? | none; Cloudflare's aggregate zone counts only (§6.7) |
| Q5 | Show the phone and TV apps on `/download/` while they are only in TestFlight and Play's internal track? | no; they appear only once a public store listing or a public TestFlight or Play testing link exists |
| Q6 | The name on the legal pages | Hamza Labs, the controller `CurrentPolicy` already names |
| Q7 | Who reviews the terms and the copyright page's legal text? | we draft them from Epic 50's research, marked as not legal advice; a lawyer the owner chooses reviews them before 50.24's public channels go live |
| Q8 | A pricing page before Epic 50 decides the plans? | no page and no link until Epic 50 decides the plans (60.13) |
| Q9 | The display typeface | 60.01 chooses from open-licensed faces, Amiri first; the owner reviews it in the part B mockups |

### Owner actions (the epic cannot do these)

| # | Action | Needed by |
|---|---|---|
| A1 | **Done (2026-11-11).** The `maktaba-server` package is public: an anonymous token request to ghcr.io answers 200 and its tags list without signing in. Every install guide depends on it; 60.08's release and daily checks keep it so | Phase 1 |
| A2 | No separate website token (decided 2026-11-11): the shared `CLOUDFLARE_API_TOKEN` deploys maktaba.tv. It needs the website's row of `deploy/cloudflare/README.md` (on `maktaba.tv`: Workers Routes Edit, Zone Read and **DNS Edit**, since custom domains write their records; Workers Scripts Edit on the account). The deploy's read-only token check names any missing permission on each run; add it to the existing token. Set the repository variable `WEBSITE_LIVE` to `true` only when the site should go live | Phase 1 |
| A3 | Mail for `privacy@maktaba.tv` (already in `CurrentPolicy`) and `copyright@maktaba.tv` (the designated agent's address), for example through Cloudflare Email Routing | Phase 1 |
| A4 | Register the designated agent with the US Copyright Office (50.24 lists it) | before 50.24's public channels |
| A5 | Approve the tagline, the voice, the wordmark and the Arabic copy (Q1–Q3) | Phase 2 |
| A6 | Public store listings, or public TestFlight and Play testing links, for the apps the download page should show | when wanted |
| A7 | Set `vars.MAKTABA_PACKAGES_URL` and `vars.HOMEBREW_TAP_REPO` and their secrets (`packages.yml`), so apt, dnf and Homebrew can be offered | when wanted |
