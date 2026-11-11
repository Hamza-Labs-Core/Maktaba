# Story 60.08 — No dead links: the maktaba.tv link check, and a public image after every release

> Epic 60 · Phase 1 · Depends on: 60.03; 60.05 and 60.06 for the pages it checks; owner action A1

## Why

The product printed maktaba.tv links for months while the site served nothing. Nobody noticed,
because nothing checks a link against a page. The same happened to the image: every guide pulls
it, it is private, and no check pulls it the way a household would. Both need a check that fails
CI, not a reminder.

## Description

**The link check** (`web/packages/website/scripts/links.mjs`):
- **Which files it reads:**
  - every tracked file except `docs/trail/**`, `specs/**`, the generated wiki databases
    (`web/wiki-app/public/db/**`, `docs/wiki/db/**`) and test files;
  - test files are left out because they hold parser inputs, not links. The test paths are
    listed in the script: `*_test.go`, `*.test.*`, `*Test.kt`, `*Tests.swift`, `src/test/**`,
    `__tests__/**`, `test-cases/**`.
  - `--paths` prints this list for 60.03's path-filter test.
- **What it finds:** each `https://maktaba.tv/…` and each scheme-less `maktaba.tv/…` in prose and
  templates, such as the release notes' Go template, rendered with a fixture first.
- **Problem links:** a link to `/problems/<slug>` is checked against the registry
  (`shared/api/problems.json`). It needs no built page, so the API's pull requests can run the
  check without building the site.
- **Every other link** must resolve to a file in `dist/`, with `auto-trailing-slash` rules (`/x`,
  `/x/` and `/x/index.html` are the same). A `#fragment` must name an `id` on that page.
- **Allowed, with reasons** (`links-allow.json`, each entry with a `reason`): empty at the start.
  The log schema's `$id` (`/schema/log-line.json`) is served as a file (60.03), so it resolves
  like any page. A new entry needs a reason and is reviewed like code.
- **Inside the built site,** every `href` and `src` resolves, in both languages, and every page
  links its twin.
- **Where it runs:**
  - in `_website.yml` (the full check against `dist/`);
  - as a fast job in `ci.yml`'s lint stage on every pull request that touches a file in its
    list (problem links and the list of pages, read from a committed `dist-manifest.json` that
    the site build regenerates and a test keeps equal).
- **On failure:** it prints each dead link with its file and line, and the nearest page name
  when one is close (a typo).

**maktaba.dev and the docs:**
- devportal's `scripts/apps.mjs` and the guides it publishes from `docs/developers/` are in the
  check's file list, so their maktaba.tv links are checked like the product's.
- The release notes template is checked rendered with every optional line on, so a line that
  only appears when, say, the package repositories are set is checked too.

**The public image** (a new job in `release.yml`, after the images are pushed and signed):
- **Anonymous pull:** in a clean runner with no Docker login and `DOCKER_CONFIG` pointing at an
  empty folder:
  - `docker pull ghcr.io/hamza-labs-core/maktaba-server:<tag>` for `linux/amd64` and
    `linux/arm64` (`--platform`);
  - then `:latest` when the release is the newest stable one.
- **The token check:** `curl https://ghcr.io/token?scope=repository:hamza-labs-core/maktaba-server:pull`
  must answer a token. Today it answers `UNAUTHORIZED`.
- **On failure:** the job fails the release run with
  `::error::the server image is not pullable without signing in`. It points at the package's
  visibility setting and names this story.
- **When it runs:** after every release (the tag workflow). A daily scheduled run of the same
  check (in `cloud-probe.yml`'s schedule, or its own small workflow) finds a visibility change
  between releases.

## Acceptance criteria

- **Given** a pull request that adds a maktaba.tv link (product code, docs, release notes,
  devportal, the NAS files) with no page or no registered problem type, **then** CI fails naming
  the file, the line and the link.
- **Given** the built site, **then** every internal link and fragment resolves in both languages.
- **Given** a release, **then** the anonymous pull of the tag on amd64 and arm64 passes, or the run
  fails with the message above.
- **Given** main today, **when** 60.03, 60.05 and 60.06 have landed, **then** the check passes with
  an empty allow list.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit | fixture repo with a dead `https://maktaba.tv/nope` in a `.go` file | run | fails with file:line |
| T02 | unit | the same in a `_test.go` file | run | ignored |
| T03 | unit | `maktaba.tv/install/synology` (no scheme) in a template | run | checked, and resolves |
| T04 | unit | `/problems/not-registered` in a Kotlin file | run | fails, against the registry |
| T05 | unit | `#missing-id` on an existing page | run | fails |
| T06 | unit | a link with a typo | run | suggests the close page |
| T07 | `tools/ci` | `dist-manifest.json` vs the site build | compare | equal |
| T08 | workflow | the image check against a public test image, then against a private one | run | passes, fails with the message (positive and negative controls) |
| T09 | Go unit | `notes.md.tmpl` with every optional value set | render + check | every link resolves |

## Edge cases

- **A link to a page that a later story adds** (`/download/` before 60.11): the pull request that
  adds the link must land with or after the page. The check makes that order visible.
- **An external site that copies an old link:** out of scope. Old paths are kept (problem slugs
  stay, guide paths stay), and a moved page gets a redirect in the Worker.
- **ghcr.io's own outage during the release check:** the job retries three times over two minutes,
  then fails. A failed check never unpublishes anything; it only makes the failure visible.

## Build plan

| File | Change |
|---|---|
| `web/packages/website/scripts/links.mjs`, `links-allow.json`, tests | the check |
| `web/packages/website/dist-manifest.json` | the list of pages, regenerated by the build |
| `.github/workflows/ci.yml` (lint stage), `_website.yml` | where it runs |
| `.github/workflows/release.yml` | the anonymous pull job |
| a scheduled workflow (`image-public.yml`, daily) | the same check between releases |
| `tools/ci/tests/` | manifest and path-list tests |
