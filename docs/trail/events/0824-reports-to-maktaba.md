---
schema: 1
kind: pr
pr: 824
title: Problem reports to Maktaba, when the home allows it and the sender chooses it
date: 2026-11-07
session: 03d72f4f-f05c-4d72-897b-0e3d0c10f530
---

## What happened
- Story 53.16, with 53.18's last catalog entry (`reports.share`): the catalog job needs an entry and the loop it names in the same release.
- **The setting.** It is the `reports.share` component (no bundle, requires `cloud.link`), switched with `components.Switch`.
- **Who sends.** A report goes to Maktaba only with the setting on and the box ticked, or when an admin sends it after their own preview.
- **The export pass.** `mlog.Export` applies 53.01's `export_only` rules (plus `ip_mask`): titles, library paths, addresses and the sender's name are removed, and each person becomes user-1, user-2.
- **The route out.** The `api:reports_forwarder` loop sends through a new loopback route on the relay agent, which adds the server's secret. It backs off for 7 days and brings Maktaba's replies back into the report.
- **The cloud.** `problem_reports` lives in Postgres (slot 0053): Epic 50.09's buckets hold only the database's backups, so §4 of the spec now says so. It has agent routes for the home, `/v1/admin/reports` for the service admin only, a 90-day sweep, and `MAKTABA_CLOUD_REPORTS=off` as the rollback.

## What we learned
- A home reaches the cloud as itself only through its relay agent: the API calls the agent on loopback (`MAKTABA_CLOUDLINK_ADMIN`), and only the agent holds the server's secret. `cloudlink_reporter` already worked this way, and a new home-to-cloud call should too.
- The forwarder's clock and the request handler's clock must agree. A queued report stamped "next try at the wall clock now" was never due under a test clock. A new item is due at once, and only a retry sets a time.
- The cloud's role test walks every `/v1/admin` route, so a new admin route is covered without a list. An explicit assertion that the walk reached it guards that.
- Writing a Swift String Catalog back sorted with Python's sort reorders hundreds of keys. Append new keys and keep the existing order.
- The wiki stores only the start of each story's text, so a change deep in a story's description needs no new Arabic. `generate_wiki.py --check` says so.

## Keep doing
- Give each test that needs Postgres its own throwaway container on a free port, keep its ID and remove it by that ID afterwards. Never touch another workload's database.

## Open
- 53.16 still lacks the symbolication of crash stacks with the release's dSYMs, the "3 reports" count beside the anonymous error groups (53.17's Errors view; the fingerprints are stored), and T07 (a canary home's report through the relay in the cloud rehearsal).
