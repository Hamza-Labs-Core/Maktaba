# Story 60.07 — Legal: privacy, terms, and copyright with Epic 50's notice form

> Epic 60 · Phase 1 (pages), Phase 3 (onto the full design) · Depends on: 60.03, 60.04, 60.02
> part A; the notice form's sending part on Epic 50 story 50.24's cloud handlers · Owner actions:
> A3 (mailboxes), A4 (the designated agent), Q6, Q7

## Why

- **Privacy:** a household deciding whether to trust Maktaba with its library reads the privacy
  page first. App Store and Google Play listings need a privacy policy URL too.
- **Terms:** the project offers downloads and, with Epic 50, a paid cloud service, and both need
  terms.
- **Copyright:** Epic 50 (50.24) requires a copyright page with a designated agent, a notice form,
  a counter-notice form and a repeat-infringer policy before public channels go live. It placed
  that page in the broadcast edge's site, but it is a legal page for every Maktaba service, and
  it belongs on maktaba.tv.

None of the three exists today.

## Description

**The legal page layout** (60.01 part A): a text page with the date it last changed, a short
summary at the top in plain words, the sections, and a table of contents on wide screens.

**`/legal/privacy/`** (and `/ar/…`), built from what the code already says, not written apart
from it:
- **The website:**
  - no cookies;
  - no analytics script;
  - no third-party requests, except Turnstile on the copyright page's form (named, with a link to
    Cloudflare's Turnstile privacy terms);
  - Cloudflare keeps aggregate request counts for the zone, as for any site it serves, and the
    page says what Cloudflare's own policy covers;
  - the install forms keep every answer in the page;
  - the copyright form sends only what it asks for, to the place it names.
- **The apps:** what they send and to whom, from `docs/app-privacy.md` (the home, never a third
  party; problem reports to Maktaba only with both consents; 90 days; delete).
- **Maktaba Cloud and the relay:** from `cloud/internal/privacy.CurrentPolicy()`:
  - the controller;
  - the contact (`privacy@maktaba.tv`);
  - the purpose;
  - the data categories and what is not collected;
  - the lawful basis;
  - the retention;
  - the rights.

  The build reads it through a small Go program (`go run ./cloud/cmd/privacy-json`) that prints
  the policy as JSON, so the page and the relay's `GET /privacy` cannot disagree. A test compares
  them.
- **Children:** Maktaba is a household's own server and the site collects nothing. The page says
  so plainly.
- **Changes:** the date, and a link to the file's history on GitHub.

**`/legal/terms/`:**
- **Using the software:** the licence (from `LICENSE`), no warranty, the household's own
  responsibility for its library.
- **Maktaba Cloud:** the plans and the acceptable use, as Epic 50 decides them. Until the cloud is
  live this section says the cloud's terms come with its sign-up.
- **The text** is drafted from Epic 50's research (`research-broadcasting.md`), marked "not legal
  advice" in the spec, and reviewed by a lawyer the owner chooses before 50.24's public channels
  go live (Q7).

**`/legal/copyright/`**, as 50.24 §1 defines it:
- **The designated agent:** the name (Q6, default Hamza Labs), `copyright@maktaba.tv` (A3) and
  the postal address the owner registers (A4). Until A4 is done, the page gives the email only and
  no postal line.
- **The notice form:** the § 512(c)(3) elements and the DSA Art. 16 fields of 50.24 §2.
  - Turnstile.
  - Before sending, the form says where the notice goes and what Maktaba does with it.
  - **Sending:** `POST https://api.maktaba.tv/v1/moderation/notices`, the public endpoint this
    epic proposes in 50.24 §2 ("Where the form posts"), served by 50.24's
    `cloud/internal/handlers/moderation`. CORS allows `https://maktaba.tv` only, and the answer
    is `202` with the `notice_id`.
  - **The page's CSP** allows exactly what the form needs, on `/legal/copyright/` and its Arabic
    twin only: `script-src` and `frame-src https://challenges.cloudflare.com` (Turnstile) and
    `connect-src https://api.maktaba.tv`. Every other page keeps `default-src 'self'`.
  - **Until that endpoint is live,** the form is not shown. The page shows the agent's email and
    the list of what a notice must contain, so a notice can be sent today. No placeholder form.
- **Counter-notices:** 50.24 has the publisher file them from the signed-in Broadcast page on
  app.maktaba.tv. The copyright page explains the counter-notice and links there. The site has no
  accounts, so it hosts no counter-notice form.
- **The repeat-infringer policy:** 3 strikes in 12 months (Epic 50 F16), and what happens after a
  notice.
- **The image credits** for the demo content in screenshots (60.09): Blender's open movies,
  CC BY, with titles and links.

**Epic 50's spec:** 50.24's build-plan row for `web/packages/broadcast-edge/site/legal/` is changed
in this epic's spec pull request to point at `web/packages/website` (this story). The broadcast
edge links to `https://maktaba.tv/legal/copyright/`, and its Report button's copyright choice
links there too.

## Acceptance criteria

- **Given** the three pages, **then** each exists in English and Arabic, dated, in the legal
  layout, and passes 60.04's bar.
- **Given** the privacy page, **then** its cloud section equals `CurrentPolicy()`, and its app
  section matches `docs/app-privacy.md`'s list of what is sent. A change to either without the
  page fails CI.
- **Given** the copyright page before 50.24's endpoint is live, **then** it shows the agent's email
  and the notice's required contents, and no form.
- **Given** the endpoint is live, **when** a person sends a notice, **then** it reaches the
  moderation queue with every required field, behind Turnstile, and the page says what happens
  next.
- **Given** any legal page, **then** it sets no cookie and loads nothing from another origin
  (Turnstile is the one exception, on the copyright form only, and the CSP allows it on that path
  only).

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | Go + build | `privacy-json` output vs the built page's cloud section | compare | equal |
| T02 | build | `docs/app-privacy.md` changed (fixture) without the page | build | fails naming the section |
| T03 | Playwright | copyright page, endpoint flag off | load | email and contents list; no form |
| T04 | Playwright | copyright page, endpoint faked | fill and send | one POST with exactly the form's fields, Turnstile token present |
| T05 | Worker unit | CSP on `/legal/copyright/`, `/ar/legal/copyright/` vs other pages | fetch | Turnstile's origin and `connect-src https://api.maktaba.tv` only there |
| T06 | Playwright | every legal page | check cookies and requests | none set; no other origin, except Turnstile and the API on the copyright page |
| T07 | Go (cloud) | the moderation endpoint's CORS | preflight from `https://maktaba.tv` and from another origin | allowed, refused |

## Strings

| Key | English | Arabic |
|---|---|---|
| `legal.updated` | Last changed {date} | آخر تعديل: {date} |
| `legal.summary` | In short | باختصار |
| `legal.privacy.title` | Privacy | الخصوصية |
| `legal.terms.title` | Terms | الشروط |
| `legal.copyright.title` | Copyright | حقوق النشر |
| `copyright.agent` | Our designated agent | وكيلنا المعيّن |
| `copyright.form.where` | Your notice goes to Maktaba's moderation team. We reply by email. | يصل إشعارك إلى فريق الإشراف في Maktaba، ونردّ عليك بالبريد الإلكتروني. |
| `copyright.form.send` | Send the notice | أرسل الإشعار |

## Edge cases

- **The policy date in `CurrentPolicy` changes:** the page's "Last changed" follows it, and the
  deploy is triggered by the path filter, which includes `cloud/internal/privacy/`.
- **The designated agent's postal address** is the owner's to give, never invented. Until A4, the
  page carries no postal line.
- **A person without JavaScript:** the form is a plain HTML form with server-side validation by the
  endpoint, and Turnstile's no-JS fallback.

## Build plan

| File | Change |
|---|---|
| `web/packages/website/copy/legal/{privacy,terms,copyright}.{en,ar}.md`, rendered by `renderLegal` in `scripts/legal.mjs` | the pages: their copy, and the one function that draws it |
| `cloud/cmd/privacy-json/main.go` (new, tiny) | prints `CurrentPolicy()` as JSON for the build |
| `web/packages/website/scripts/legal.mjs` | builds the privacy page from the two sources |
| `web/packages/website/src/copyright-form.ts` | the forms (shown only when the endpoint is configured) |
| `web/packages/website/src/worker.ts` | the copyright path's CSP |
| `cloud/internal/handlers/moderation/` (50.24) | CORS for `https://maktaba.tv` |
| `specs/epics/50-cloud-no-downtime/story-50-24-takedown-reports.md` | build-plan row → this story; §2's endpoint (proposed in this epic's spec pull request) |
