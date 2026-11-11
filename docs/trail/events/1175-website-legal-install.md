---
schema: 1
kind: pr
pr: 1175
title: "website: legal pages (data and logic), the install chooser, guides without placeholder figures (Epic 60 stories 60.07, 60.05)"
date: 2026-11-11
session: f925e963-4260-4f93-af9c-65b64478f846
---

## What happened
- #1175 (merge ea0087b53) built the parts of stories 60.07 and 60.05 that do not wait on the
  design (#1163), on the placeholder shell:
  - `cloud/cmd/privacy-json` prints `privacy.CurrentPolicy()` as JSON; its Go test checks it
    equals the policy and `GET /privacy`.
  - `web/packages/website/scripts/legal.mjs` builds `/legal/{privacy,terms,copyright}/` and
    `/ar/…` from copy in `copy/legal/<page>.<lang>.md` (a small Markdown with `{{slot}}`
    blocks) into a structured document that one function, `renderLegal`, draws. The design
    replaces that function only.
  - The privacy page's cloud section is the policy value by value; the Arabic of each value is
    in `copy/legal/policy.ar.json`, and a missing or stale entry fails the build naming
    `#cloud`. Each copy records the sha256 of `docs/app-privacy.md`'s "What the apps send, and
    where" section (`apps-source:`); a change there without the copy fails naming `#apps`. The
    page's date follows the policy's `updated_at` when newer. The terms page checks `LICENSE`.
  - `src/copyright-form.ts`: the notice form, built only when `WEBSITE_NOTICE_ENDPOINT` and
    `WEBSITE_TURNSTILE_SITEKEY` are both set (off: Epic 50's endpoint is not live). Today the
    page gives `copyright@maktaba.tv` and the seven things a notice must contain, no postal
    line (owner action A4 not done), no form, no script. T04 runs in Playwright with Turnstile
    and the endpoint faked through `page.route`.
  - `/install/` and `/ar/install/` choosers; the site build drops each "Screenshot to come"
    figure whose image does not exist (60.05 T07); the release notes' install links are full
    `https://maktaba.tv/install/…/` links.
  - The website job sets up Go and downloads the cloud module; the website filter and the
    deploy's paths add `cloud/cmd/privacy-json/**`, `docs/app-privacy.md` and `LICENSE`. Smoke
    gains `/install/` and `/legal/privacy/`. 56 reference images (24 guides changed, 32 new).
- Problems hit on the way, each fixed in the pull request:
  - `CSP_COPYRIGHT` lacked `form-action`: the no-JS plain form would have been blocked by the
    base `form-action 'none'`. It now allows `https://api.maktaba.tv` there only.
  - A setup-go step without `./.github/actions/go-mod-download` failed
    `test_go_mod_download.py` in `lint / repo`.
  - The `/install/` chooser counted as the install family (e2e `kindOf` matched `/install/`),
    so it became the family's "shortest" and broke `representatives.spec.ts`. A family's own
    index is now hand-made (it is in build.mjs's `PAGES`), which gave it light references.
  - CodeQL read a test's `html.includes("https://app.maktaba.tv/")` as incomplete URL
    sanitization; the assertion is now an exact anchored match.
  - Rendering references in the amd64 container on an Apple Silicon Mac timed out 12 shots
    under the default workers; `--workers=2` rendered them clean.
  - `integration` and `node-packaging / package repositories` failed five times on
    `apt-get update` (`security.ubuntu.com` unreachable from the runners for about two hours);
    `lint / repo` once hit its 10-minute limit inside the trail step after its tests passed.
    All were re-run until green; nothing in the change touched them.

## What we learned
- A page that loads Go output at build time needs both setup-go and the go-mod-download step:
  the repository's guard requires every Go job to download its modules first and go offline.
- The e2e page kinds and build.mjs's `PAGES` must agree: a family's index page written by hand
  is hand-made everywhere (Lighthouse byte bar, light references, representatives).
- `node:module`'s `stripTypeScriptTypes` lets one `.ts` file be both the build's renderer and
  the page's script, with no bundler.

## Keep doing
- Build legal and policy text from the code that makes it true, with a hash or value-by-value
  check that fails naming the section.
- Render references in the CI image with a private Docker volume per session.

## Stop doing
- Sending a full-site visual update on emulated amd64 at the default worker count.

## Open
- The design (#1163) replaces `renderLegal`, the shell and the stylesheet; the legal and
  chooser references change then.
- The notice form goes live with Epic 50's `POST /v1/moderation/notices` (CORS for
  `https://maktaba.tv`, 202 with `notice_id`, T07 in the cloud). The endpoint must also accept
  the no-JS form's urlencoded post, or the plain-form path is to be dropped.
- Owner actions: A3 (the `privacy@` and `copyright@` mailboxes), A4 (registering the designated
  agent, then the postal line), Q7 (a lawyer reviews the terms and copyright text before
  50.24's public channels).
- The demo-content image credits on the copyright page wait for 60.09's images.
- The link baseline still holds 9 entries (8 `/problems/` for 60.06, 1 `/docs` for 60.03).
