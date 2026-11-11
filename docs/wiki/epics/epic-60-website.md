# Epic 60 — maktaba.tv, the website households find Maktaba on

> **Status:** spec (2026-11-11). **Source:** [`specs/epics/60-website/`](../../../specs/epics/60-website/README.md).
> **Anchors:** [Epic 17](epic-17-ux-design-system.md) (design tokens) · [Epic 41](../../../specs/epics/41-help-guide/README.md) (help topics) · [Epic 59](epic-59-nas.md) (the install guides) · [Epic 50](../../../specs/epics/50-cloud-no-downtime/README.md) (the copyright page of 50.24, the plans, Workers deploys) · maktaba.dev (`web/packages/devportal`, the model for the build and deploy).

## Goal

Every maktaba.tv address the product prints opens a real page, in English and Arabic, and a
household that hears of Maktaba can learn what it is, see it, download it for each of its devices,
install it and get help, on a site that is well designed, fast, accessible and private. The site
lives on the apex and `www` only; the zone's other names stay with their epics.

## Key decisions

| | |
|---|---|
| Hosting | one Worker in front of static assets, as maktaba.dev; custom domains on `maktaba.tv` and `www.maktaba.tv` (no record there today; app. and admin. keep their Workers Routes on the Tunnel's records) |
| Deploy | from CI on main after the pull request's checks; green and skipped without secrets; version upload, preview-URL smoke, atomic promote, live smoke, automatic rollback; `rollback: true` by hand as in `cloud-web-deploy.yml` |
| Problem pages | a registry, `shared/api/problems.json`, that the API sends from (`httperror.Type`, panics under test on an unregistered slug); one page per type in both languages; clients checked against it |
| Design | a design story first: part A (the shell) gates Phase 1's pages, part B (the full direction, layouts, components) gates Phase 2; mockups as HTML with the real tokens and fonts, rendered to PNG and reviewed; a design review closes each phase |
| Copy | plain and warm for households, no jargon; Arabic written natively; the tagline and voice proposed, the owner approves |
| Imagery | real dark screenshots of each app on an openly licensed demo library, in both languages, from CI and throwaway simulators |
| Quality bar | Lighthouse 95+ ×4 on mobile, WCAG 2.2 AA, CLS 0, RTL mirroring tested, social cards, icons, sitemap with hreflang; visual references at 360/768/1280 × en/ar, reference changes listed and reviewed |
| Privacy | no analytics, no cookies, no third-party requests; Cloudflare's aggregate zone counts only |
| Help | rendered from Epic 41's own Markdown; app routes become "In Maktaba: Settings → …" |
| Download | from the release's `release.json`; only what a household can install today; store and package-repository rows when they exist |
| Pricing | no page until Epic 50's plans are decided; then generated from the enforced plan data |
| Image | the server image made public (owner action); a release check pulls it anonymously |

## Stories

| # | Story | Phase |
|---|---|---|
| 60.01 | [Design: the visual direction, page layouts and components, as reviewed mockups](../../../specs/epics/60-website/story-60-01-design.md) | 1, 2 |
| 60.02 | [Voice and copy: plain and warm for households, Arabic written natively](../../../specs/epics/60-website/story-60-02-voice-copy.md) | 1, 2 |
| 60.03 | [The site, its hosting and its deploy: Workers static assets on the apex and www, staging, rollback](../../../specs/epics/60-website/story-60-03-site-hosting.md) | 1 |
| 60.04 | [The quality bar in CI: Lighthouse, WCAG 2.2 AA, no layout shift, visual references, SEO and social cards](../../../specs/epics/60-website/story-60-04-quality-gates.md) | 1 |
| 60.05 | [The install guides on maktaba.tv](../../../specs/epics/60-website/story-60-05-install-guides.md) | 1, 3 |
| 60.06 | [Problem types: one registry the API sends from, one page per type](../../../specs/epics/60-website/story-60-06-problems.md) | 1, 3 |
| 60.07 | [Legal: privacy, terms, and copyright with Epic 50's notice form](../../../specs/epics/60-website/story-60-07-legal.md) | 1, 3 |
| 60.08 | [No dead links: the maktaba.tv link check, and a public image after every release](../../../specs/epics/60-website/story-60-08-link-integrity.md) | 1 |
| 60.09 | [Product imagery: real dark screenshots of every app, in English and Arabic, on demo content](../../../specs/epics/60-website/story-60-09-product-imagery.md) | 2 |
| 60.10 | [The home page](../../../specs/epics/60-website/story-60-10-home.md) | 2 |
| 60.11 | [Download: every device, with its real release asset](../../../specs/epics/60-website/story-60-11-download.md) | 2 |
| 60.12 | [Help on maktaba.tv, from the app's own topics](../../../specs/epics/60-website/story-60-12-help.md) | 3 |
| 60.13 | [Pricing, from the plans the cloud enforces](../../../specs/epics/60-website/story-60-13-pricing.md) | 3 |

## Where it will live (the developer view)

| Part | Where |
|---|---|
| The site: Worker, build, pages, design, copy, tests | `web/packages/website/` |
| The install guides (built into the site) | `web/packages/install`, `web/vite.install.config.ts` |
| Problem types | `shared/api/problems.json`, `api/internal/httperror` |
| Help topics (rendered into the site) | `web/src/help/content/{en,ar}/` |
| Tokens (with the new `site` group) | `web/design-system/tokens/tokens.json` |
| Deploy and checks | `.github/workflows/_website.yml`, `website-deploy.yml`, `tools/cloudflare/smoke.mjs` (`website`), the anonymous image pull in `release.yml` |
