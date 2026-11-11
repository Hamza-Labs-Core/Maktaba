---
schema: 1
kind: decision
pr: 1158
title: "Epic 60 spec: maktaba.tv, the website households find Maktaba on"
date: 2026-11-11
session: b7d30639-873e-43dc-a75c-fd53a9437c22
agent: claude-code
---

## What happened
- Spec only: a README, 13 stories, the wiki page and its Arabic store. It adds 50.24's notice
  endpoint as a proposal and moves the copyright page to maktaba.tv.
- **Decisions:**
  - one Worker in front of static assets, as maktaba.dev, with custom domains on the apex and
    `www` only. app. and admin. keep their Routes on the Tunnel's records;
  - deploy: record the live version, upload, preview smoke, promote, `wrangler triggers deploy`,
    live smoke, roll back to the recorded version;
  - a problem registry (`shared/api/problems.json`), checked in `httperror.Write`, with one page
    per type;
  - a design story first (part A gates Phase 1, part B gates Phase 2), with HTML mockups on the
    real tokens and fonts;
  - Lighthouse 95+ ×4 on mobile, WCAG 2.2 AA, CLS 0, and visual references at three widths in
    both languages, whose changes must be listed and reviewed;
  - no analytics or cookies; Turnstile only on the copyright page;
  - download shows only what is installable; pricing waits for Epic 50's plans.
- An independent review against the code and Cloudflare's docs found 22 problems before merge.
  All were fixed in the same PR.

## What we learned
- **maktaba.tv:**
  - The zone is active (Cloudflare is also the registrar) with no records at all: not the apex,
    not `www`, not even `app.`.
  - 321 links outside the trail and specs point at it: 257 to `/problems/` (120 slugs), 45 to
    the install guides.
  - `/help`, `/watch`, `/video`, `/library` and `/billing` appear only in tests and in two
    parser comments. No app claims maktaba.tv as an associated domain.
- **The server image `ghcr.io/hamza-labs-core/maktaba-server` is private** (an anonymous token
  request is refused) while the repository is public. Every install guide pulls it.
- **Problem types have no registry:**
  - `httperror/types.go` claims to be one and holds 18 constants;
  - seven call sites build slugs from codes at runtime, and `Error.Type` is a public field that
    struct literals set;
  - the recoverer middleware turns a panic into a 500, so a panic-based guard is invisible to
    tests that only check the status;
  - four shipped slugs use underscores and clients match them;
  - streaming types its problems `maktaba.invalid` on purpose.
- **Wrangler:**
  - `versions upload` and `versions deploy` never apply routes or custom domains (`wrangler
    triggers deploy` does);
  - a bare `wrangler rollback` returns to the version uploaded before the latest, which with
    upload-then-promote can be a version that never went live.
- **Releases:** `release.json`'s `assets` lists only the server archives, and `checksums.txt` has no
  desktop installers. The Releases API gives every asset's size and sha256 `digest`.
- **CI:**
  - `_pr-body-check.yml` reads the body from the event payload, and `ci.yml` does not trigger on
    `edited`, so a fixed body never re-runs the check;
  - `ci-paths.yml` has no negation, so "every tracked file but X" cannot be a filter.
- **Where things are:**
  - help is in `web/src/help/content/{en,ar}` (44 topics, 167 app-route links);
  - the relay's privacy policy is `cloud/internal/privacy.CurrentPolicy`;
  - the apps ship IBM Plex Sans Arabic and Amiri, and the web names Plex but ships no font file;
  - `axe-core` is already a web dev dependency.
- **Facts in the brief differed from main:** link counts, `/pricing` and `/legal/copyright` exist
  only in specs, and the phone and TV apps and apt, dnf and Homebrew are not publicly available.
  Recount before relying on a brief's numbers.

## Open
- **Owner actions A1–A7** (README §12):
  - make the `maktaba-server` package public (blocks Phase 1);
  - DNS Edit on the `maktaba.tv` zone for the deploy token;
  - the `privacy@` and `copyright@` mailboxes;
  - the designated agent;
  - approvals of the tagline, voice, wordmark and Arabic;
  - public store listings;
  - the package repository variables.
- **Owner questions Q1–Q9** keep their defaults until answered.
- **Checks for the implementing stories:** that `preview_urls = true` works with
  `workers_dev = false`, and that `wrangler triggers deploy` attaches the custom domains.
- **The build PRs,** starting with Phase 1: 60.01 part A, 60.02 part A, 60.03, 60.04, 60.05,
  60.06, 60.07 and 60.08.
