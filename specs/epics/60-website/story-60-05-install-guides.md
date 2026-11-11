# Story 60.05 — The install guides on maktaba.tv

> Epic 60 · Phase 1 (publish) and Phase 3 (onto the full design) · Depends on: 60.03, 60.04,
> owner action A1 (the public image)

## Why

Epic 59 built nine install guides in English and Arabic (`web/packages/install`), with forms that
fill the compose file in the page. They are linked from:
- the release notes;
- maktaba.dev's Apps pages;
- the repo README;
- the Unraid template;
- the TrueNAS app's README;
- each other.

They have never been deployed, and the image every guide pulls is private. Publishing them is the
fastest way to turn 45 dead links into working ones, and to let a household actually install.

## Description

**Phase 1, publish as they are.**
- The site's build runs `pnpm -C web build:install` and copies `web/dist-install/` under `dist/`.
  The paths stay exactly as built: `/install/<guide>/` and `/ar/install/<guide>/` for `nas`,
  `docker`, `synology`, `truenas`, `unraid`, `qnap`, `ugreen`, `after-setup` and
  `troubleshooting`.
- `/install/` and `/ar/install/` (no guide) answer the `nas` guide's chooser, through a small
  index page. Today they have no page.
- The guides keep their own stylesheet and tests (Epic 59's T04/T05 in `web-ui.yml` stay).
- The site adds:
  - **the shell's header and footer** (60.01 part A) around each guide, through the install
    build's HTML template, so a visitor can reach the front door, help and the other language;
  - **head tags:** canonical, `hreflang`, Open Graph and the social card (60.04);
  - **the quality run:** axe, Lighthouse, visual references at three widths in both languages
    (60.04). The guides must pass the bar like any page. Faults found are fixed in the install
    package in this story.
- **Screenshots still to come** keep their placeholder figure ("Screenshot to come",
  `web/packages/install/README.md`) until each device checklist takes them. A placeholder is not
  a missing image for the link check.
- **The image** they pull: the guides name `ghcr.io/hamza-labs-core/maktaba-server`. The phase
  does not close until owner action A1 has made the package public and 60.08's anonymous pull
  passes.

**The release notes and the scheme.**
- `tools/release/releasenotes/notes.md.tmpl` names `maktaba.tv/install/synology` and
  `maktaba.tv/install/truenas` without a scheme or trailing slash. Both become full links
  (`https://maktaba.tv/install/synology/`), so the link check reads them and GitHub renders them
  as links.
- The notes also gain one line pointing at `https://maktaba.tv/download/` once 60.11 lands.

**Phase 3, onto the full design.**
- The guides move to 60.01 part B's install layout:
  - the step list component;
  - the code and compose blocks with copy;
  - callouts;
  - screenshot frames.
- The generator code and its tests do not change.
- The visual references change in one reviewed pull request per guide family (NAS guides, the
  generators, after setup and troubleshooting).
- Copy is moved onto the voice guide where a sentence is touched (60.02).

## Acceptance criteria

- **Given** the deployed site, **then** each of the 18 guide pages answers 200 at the path the
  product links, in its language, right to left in Arabic.
- **Given** `/install/` and `/ar/install/`, **then** they answer the chooser.
- **Given** the install page's form, **then** no request carries an answer (Epic 59's T04 still
  passes on the deployed build).
- **Given** each guide, **then** it passes 60.04's bar (Lighthouse ≥ 95 × 4 on mobile, axe clean,
  CLS 0) in both languages.
- **Given** the release notes, **then** every maktaba.tv mention is a full `https://` link the link
  check resolves.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | build | site build | list `dist/install/*`, `dist/ar/install/*` | the nine guides × 2 and the two index pages |
| T02 | unit | `notes.md.tmpl` | render with a fixture | maktaba.tv mentions are `https://maktaba.tv/…/` links |
| T03 | Playwright | deployed-shape `dist/` under `wrangler dev` | Epic 59's T04 against it | the compose equals the generator's; no answer in any request |
| T04 | Lighthouse + axe | each guide × 2 languages | run | 60.04's bar |
| T05 | visual | each guide × 3 widths × 2 languages | compare | matches references |
| T06 | smoke | live | `/install/docker/` | 200 with the generator's marker |

## Edge cases

- **A guide added to `PAGES` in `vite.install.config.ts`** without its Arabic twin: the install
  build already fails, and the site's build fails with it.
- **An old link without the trailing slash** (`/install/unraid`, as two files write it): the
  assets' `auto-trailing-slash` answers it. The link check accepts both forms.
- **The image goes private again later:** 60.08's release check fails the next release.

## Build plan

| File | Change |
|---|---|
| `web/packages/website/scripts/build.mjs` | the install build and copy |
| `web/packages/install/site/**`, `web/vite.install.config.ts` | the shell's header and footer, head tags, fixes the quality run finds |
| `web/packages/website/src/pages/install/index.html` (+ `ar/`) | the chooser |
| `tools/release/releasenotes/notes.md.tmpl`, `notes_test.go` | full links |
| `web/packages/install/README.md` | Hosting: this site |
