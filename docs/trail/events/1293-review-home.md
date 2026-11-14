---
schema: 1
kind: pr
pr: 1293
title: "Epic 58 story 58.04: a home for TV store reviewers at review.maktaba.tv"
date: 2026-11-14
session: 0c3e16d1-4344-4763-a698-1cb3e76a4e60
agent: claude-code
---

## What happened
- #1293 merged as 7540bfd78. review.maktaba.tv is a normal Maktaba home (Docker install,
  Caddy in front) registered with Maktaba Cloud; reviewers sign in by the same cloud code
  (58.03) and relay customers use.
- Content is public domain and CC BY only, each item with English and Arabic titles and a
  licence note (`deploy/review-home/content/manifest.json`, SHA-1 and size per source), plus
  test files: 4K HEVC HDR10, 1080p H.264, an MKV with EAC3 and en/ar WebVTT, and a Theora file
  that needs a transcode. `tools/review_home/content.py` validates, fetches, checks, derives
  and writes LICENCES.md.
- `maktaba-server review-home seed|rotate` (`api/internal/reviewhome`): `seed` makes the
  libraries at their age tiers, the viewer `reviewer` (never an admin) and the child
  `reviewer-child` (9+), idempotently; `rotate` sets new passwords and signs out every device.
  Passwords come only from the environment.
- `docs/stores/review-home.md` holds the submission notes; each store page points there and
  names only the secrets. Rotation after each submission round is on the release checklist.
- `billing.LowestRelayPlan()` returns `basic`, and a test ties `REVIEW_HOME_PLAN` to it.
- `review-home-probe.yml` runs every 15 minutes when the repo variable `REVIEW_HOME_PROBE`
  is `true`, pushes to the witness Pushgateway, with alerts `review_home_down` and
  `review_home_probe_stale` (promtool tests, runbook).
- `tools/ci/review_credentials.py` fails on any password-shaped value in the store pages or
  the review home's deploy and tools.

## What we learned
- CodeQL's `py/clear-text-logging-sensitive-data` flagged the credentials check printing a
  found value's first characters: a secret scanner reports the line, never any part of a value.
- `rotate` must check both accounts before changing either password, or a failure leaves one
  rotated and one not.

## Keep doing
- Store pages name a secret by placeholder only, with a CI check that fails on anything that
  looks like a password.
- Gate a scheduled probe on a repo variable so it stays quiet until the thing it probes exists.

## Open
- Owner: a VM and a DNS A record for `review.maktaba.tv` (DNS only, 300 s).
- Owner: release secrets `REVIEW_HOME_VIEWER_PASSWORD` and `REVIEW_HOME_CHILD_PASSWORD`, the
  viewer password as a repository secret, `PUSHGATEWAY_URL`, `PUSHGATEWAY_TOKEN`, and
  `REVIEW_HOME_PROBE=true` once the home is up.
- Owner: a Maktaba Cloud account on `basic`, excluded from revenue; first provisioning per
  `deploy/review-home/README.md`; the store console entries.
- The 2-minute real-device timing is checked by hand on a TV.
- This event was written after the merge by a separate session; no session event existed.
