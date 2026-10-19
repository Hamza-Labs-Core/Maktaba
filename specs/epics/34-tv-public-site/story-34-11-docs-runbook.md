# Story 34.11 — Docs and runbook

## Description

*As the owner (or whoever runs Maktaba), I want one guide to set up the public site, run it day to day,
and act fast when something goes wrong or costs too much.*

Design: all sections; README R12.

## Acceptance criteria

- **AC1** `docs/tv-public-site.md` (en) and `docs/ar/tv-public-site.md` (ar) cover:
  prerequisites (Workers Paid, Stream subscription and storage plan, R2, Turnstile, Zero Trust only for
  `access`), API token permissions per option, first deploy to staging and promotion, choosing and
  attaching the domain (and apex/`www`), making a channel public (rights checklist), exclusions and the
  slate, access modes, branding, budget sizing with the cost table, reading the usage panel.
- **AC2** Failure playbook: home offline, Cloudflare Stream errors (`ERR_*`), storage exhausted, rotation
  failing, capacity exhausted, budget hit, abuse spike (rate limits, WAF rule), takedown request (make
  the channel private or exclude, then panic off-air if needed), key rotation, rollback.
- **AC3** A legal checklist (not legal advice): rights per channel, takedown contact, privacy page
  mentions Cloudflare as processor, cookie notice (one functional cookie).
- **AC4** The spike results of 34.3/34.4 (cold start, A→B stall, origins, IP binding) are recorded with
  dates, and the README facts table is updated.
- **AC5** The control-centre help links point to the relevant runbook sections.

## Test cases

- **TC1** Docs lint (links, headings) in CI.
- **TC2** A dry run of the runbook on a test account by someone other than the author, with findings fixed.

## Edge cases

- **EC1** Cloudflare changes a price or limit: the runbook cites the pricing page and the settings
  carry the price (`delivery_usd_per_1000_min`) so it can be updated without a release.

## Dependencies

34.1–34.10.
