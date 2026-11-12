# maktaba.tv: how the website is built and deployed

maktaba.tv is the site households find Maktaba on (Epic 60): the front door, the install guides,
and later the download, help, problem and legal pages, each in English and Arabic. It lives in
`web/packages/website` ([README](https://github.com/Hamza-Labs-Core/Maktaba/tree/main/web/packages/website)),
and is built and deployed the way this portal is: a Cloudflare Worker in front of static files.

## The build

`npm run build` in `web/packages/website` writes `dist/`, in this order:

1. the install build (`pnpm -C web build:install`, Epic 59's guides), copied to the root of the
   site at `/install/` and `/ar/install/`, its hashed files under `/install/assets/`, without
   each "Screenshot to come" figure whose image does not exist yet (the install package's own
   preview keeps them);
2. the generated families (the problem pages, help, download), as their stories add them;
3. the hand-made pages (`src/pages/`: the front door, the `/install/` chooser, the 404 page) and
   the legal pages, each wrapped in the page shell (`scripts/shell.mjs`);
4. the copied files: the app icons (`favicon.ico`, `apple-touch-icon.png`, the 192, 512 and
   maskable icons and `manifest.webmanifest`), the log schema at `/schema/log-line.json` (its
   `$id`), and `security.txt` with a `Canonical` line for each domain;
5. what every page carries: the head tags (`scripts/head.mjs`: canonical, `hreflang` en, ar and
   x-default, Open Graph, Twitter's card, the icons) added where an install guide's own head
   lacks them; a social card per page and language (`scripts/cards.mjs`, 1200×630 PNG under
   200 KB); `sitemap.xml` with each page's alternates, and `robots.txt` naming it;
6. the checks: every page has its twin in the other language, and an Arabic page passes this
   portal's Latin-share check;
7. the link check's manifest (`scripts/links.mjs --write-manifest dist`): commit
   `dist-manifest.json` when the pages change, because CI fails when it differs from the build.

## The legal pages

`/legal/privacy/`, `/legal/terms/` and `/legal/copyright/` (and `/ar/…`) are written in
`copy/legal/<page>.<lang>.md`, each language on its own, and `scripts/legal.mjs` builds them;
one function (`renderLegal`) draws the layout, so the design changes it and not the copy. The
privacy page is built from its sources: its cloud section is `privacy.CurrentPolicy()`, read
through `go run ./cmd/privacy-json` in `cloud/` (so the build needs Go), with each value's
Arabic in `copy/legal/policy.ar.json`; its apps section is written from `docs/app-privacy.md`,
and each language's copy records that section's sha256 (`apps-source:`). A change to the policy
or to that section without the page's copy fails the build, naming the section. The terms page
names the licence in `LICENSE`.

The copyright page's notice form (`src/copyright-form.ts`) is shown only when the build has
both `WEBSITE_NOTICE_ENDPOINT` (Epic 50's `POST https://api.maktaba.tv/v1/moderation/notices`)
and `WEBSITE_TURNSTILE_SITEKEY`. Neither is set until that endpoint is live, so the page gives the
designated agent's email and what a notice must contain, and no form. Only that page's CSP
allows Turnstile and the API.

## The Worker

`src/worker.ts` sends `www.maktaba.tv` and plain http to `https://maktaba.tv`, sends `/docs` to
the front door (to `/help/` once the help pages exist), answers a missing path with the 404 page
in its language and status 404, and puts the headers on every answer: a strict CSP, HSTS without
`includeSubDomains` (the relay's names must not inherit it), and the cache policy.

## Pull requests

`ci.yml`'s `website` jobs (`_website.yml`) run on a pull request that changes the site or a
source it is built from:

- **website:** the build, the unit tests (the Worker, the build, the head tags, cards and
  sitemap, the scripts), the CSS lint, the committed `dist-manifest.json` against the build, the
  link check against the build (every maktaba.tv link in the repository, and every `href`, `src`
  and `#fragment` on every built page, with each page's link to its twin), and a smoke of the
  built site through the Worker under `wrangler dev`. The built site is the `website-site`
  artifact; the Worker's log is `website-worker-log`.
- **quality:** Playwright on every page in both languages, served by the Worker: axe with the
  WCAG 2.0, 2.1 and 2.2 A and AA rules, dark and light; the keyboard (a skip link first, Tab in
  reading order, the phone menu once the shell declares one); a 2 px focus ring at 3:1, not
  covered; 24 px targets; no sideways scroll at 320 px; text spacing; reduced motion; the
  languages; no layout shift; RTL mirroring; and the visual references. It runs in four shards
  (`--shard`), one job each. Kept, per shard `<n>`: `website-playwright-<n>` (the report),
  `website-axe-<n>` (axe's findings), `website-visual-diff-<n>` (on a mismatch, the expected,
  actual and diff images), `website-worker-log-quality-<n>`.
- **lighthouse:** Lighthouse on mobile, three runs per page, the median judged: 95 or more for
  performance, accessibility, best practices and SEO, layout shift 0, and 500 KB or less on the
  hand-made pages. It measures the hand-made pages (with the problem index) and each family's
  representatives (`e2e/representatives.json`) in both languages, and, when the pull request
  changes what a family is generated from, its other pages, one run each: all of them up to 16,
  and beyond that 16 spread evenly over it (the nightly measures every page). A fixture page with an unsized image
  must fail it first (the positive control). Kept: `website-lighthouse`, with the HTML and JSON
  reports and a table of the scores in the run's summary.

## Running the gates locally

In `web/packages/website`, after `npm ci` and `npm run build`:

```sh
npm test                 # the unit tests and the scripts' tests
npm run lint:css         # logical properties only: margin-inline-start, never margin-left
node scripts/links.mjs --dist dist   # the link check against this build
npm run e2e              # Playwright: starts wrangler dev on :8788 (its log: test-results/worker.log)
npm run lighthouse -- run --set pr --base http://127.0.0.1:8788 --out lighthouse-out/pr
```

`npm run e2e` skips the visual references on a Mac: they are rendered only in the image CI runs
in. `npx playwright test e2e/a11y.spec.ts` runs one spec; `WEBSITE_BASE` points the tests at a
`wrangler dev` already running.

## Updating the visual references

The references live in `e2e/__screenshots__/` as `<page>-<width>-<lang>[-light].png`: every
page at 360, 768 and 1280 px in both languages, dark, and each hand-made page in light at 1280.
When a change to a page's look is meant:

1. `npm run build`, then `npm run e2e:update` (`pnpm -C web/packages/website e2e:update` from the
   repository root). It needs Docker: it renders the references in
   `mcr.microsoft.com/playwright:v<the pinned Playwright>-noble` on `linux/amd64`, the image CI
   runs, and rewrites only those whose pixels changed. A reference never comes from a Mac.
2. Open every changed image, in both languages.
3. Commit them, and list each one under a `Reference images:` section of the pull request's body
   with one line on why. The `Reference images` check fails, naming the files, until each is
   listed. Each image stays under 1 MB, with no metadata.

## The nightly sweep

`website-nightly.yml` runs every night on `main` (and by hand): Lighthouse on every page in both
languages, one run per page, in sixteen shards of 15 minutes. A page under the bar is measured
three more times and judged on that median. A failure makes the run red and names the pages in
its summary and in the `website-nightly-failures` artifact. The next deploy measures those pages
again in its own build and goes on only when they pass, so a fix that lands, or a revert, unblocks
it. A red sweep whose list of pages cannot be read blocks the deploy until a sweep passes.

## The deploy

`website-deploy.yml` runs on a push to `main` that changes the site, by hand, and monthly. It
builds and checks the site as a pull request does (with the pages the latest nightly sweep
failed), refuses while that sweep is red and unreadable, then runs these steps
(`tools/cloudflare/website-deploy.mjs`):

1. the token check (`token-check.mjs --pieces workers,dns`);
2. record the version serving now (`wrangler deployments status`): a rollback returns to it;
3. upload the new version, not yet live (`wrangler versions upload`), and smoke its preview URL;
4. promote it at 100% (`wrangler versions deploy`), then apply the custom domains
   (`wrangler triggers deploy`): `maktaba.tv` and `www.maktaba.tv`, and no other name;
5. smoke the live site; when that fails, roll back to the recorded version and fail.

A first deploy creates the Worker with no routes before step 3, because a version cannot be
uploaded to a Worker that does not exist. If its live smoke fails, the custom domains are
detached again.

## Going live

The deploy goes live only when all three are set: the secret `CLOUDFLARE_API_TOKEN_WEBSITE` (the
website's own token, with DNS Edit on `maktaba.tv`, because custom domains write their records),
the secret `CLOUDFLARE_ACCOUNT_ID`, and the repository variable `WEBSITE_LIVE` set to `true`.
Without any of them the job prints a notice naming what is missing and passes. The shared
`CLOUDFLARE_API_TOKEN`, which deploys this portal, is never read, so maktaba.tv cannot go live by
accident. `deploy/cloudflare/README.md` has the token's permissions.

## Rolling back

Actions → **Deploy maktaba.tv** → Run workflow, with `rollback: true`. Leave `version` empty to
return to the deployment before the current one, or give a version id from a deploy's run
summary. The live smoke runs after the rollback, and the run fails if it does not pass.
