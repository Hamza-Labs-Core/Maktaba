---
schema: 1
kind: pr
pr: 815
title: Plugins Phase 2 part 5, the store browser, listing pages, ratings and reports (Epic 54, 54.18)
date: 2026-11-07
session: 13624713-ebc0-4816-aea9-815781ec4f0d
prs: [753, 770, 778]
---

## What happened
- Settings → Plugins → Browse the store reads the catalog the home keeps (no store call of its own), filtered on the server to this server's version, plugin API and platform, with Arabic-folded search, categories and sorts, and a plugin page with Install; admins rate and report from the home, signed by it.
- The store keeps ratings (one per account, from a linked home with the plugin for 24 hours, shown from 5), a developer reply, review and plugin reports (malware and data misuse alert the store admins and lead the queue; the reporter hears the outcome). plugins.maktaba.tv renders the list and each plugin's page on the server from the signed catalog, in English and Arabic, every text escaped, with the plugin's flow drawn and its published events and subscriptions.
- Rating and reporting are listed outbound calls and answer `store_offline` offline. This ends Epic 54 Phase 2 (54.14 to 54.18: #733, #753, #770, #778, #815).

## What we learned
- A merge watcher must fail closed: read `statusCheckRollup` with `jq -e` under `set -euo pipefail`, merge only when the read worked, at least one check exists and every check completed SUCCESS, SKIPPED or NEUTRAL, and pin the merge with `--match-head-commit`. A watcher that reads a failed command's output as "nothing pending" merges red PRs (#809).
- Background work gets a hard limit (`perl -e 'alarm shift; exec @ARGV' N …` where `timeout` is missing on macOS), its PID saved, and is stopped by that PID and checked gone.
- Migration slots move under you while a PR waits: #778 went 0197 → 0200 → 0201 → 0202 → 0203. Renumber only at merge time, and check each new slot's migrations for a redefinition of `cluster_copy_excluded()` before keeping yours on the older one.
- A store migration (D1) numbers separately from the home's, but two branches of one series still collide: 54.18's ratings moved to 0004 after 54.16's flows took 0003.

## Open
- The home's Browse page draws no flow chart yet (the home's catalog carries no graph); the release-time embedding of the lists (#755), the listing's address fallback (#757), logs on reports (#758), the runner on Cloudflare Containers (#756).
