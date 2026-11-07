---
schema: 1
kind: pr
pr: 837
title: A plugin report can carry the plugin's own logs, for the store's reviewers (Epic 54, 54.18, #758)
date: 2026-11-07
session: 13624713-ebc0-4816-aea9-815781ec4f0d
prs: [815, 836]
---

## What happened
- Reporting an installed plugin from Browse, the admin can attach the plugin's own log lines from the last day, scrubbed by the shared redaction rules and capped (200 lines, 256 KB); the store keeps them only from a signed home report, in the private bucket, for its reviewers alone (each read audited). Store migration catalog/0005.

## What we learned
- Reuse the redaction a problem report applies (the logs package's scrub) for anything else that leaves the home, and filter to the plugin's own service again after the index answers: an index query is a request, not a guarantee.
- Data a third party can send (a web report's `logs`) is ignored unless it comes over the signed path that is allowed to carry it.

## Open
- The owner adds the 90-day lifecycle rule on `report-logs/` (store README).
