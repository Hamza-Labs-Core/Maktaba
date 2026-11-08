---
schema: 1
kind: pr
pr: 927
title: The fallback origin's deny rule for addresses outside Cloudflare's ranges never ran (50.31)
date: 2026-11-07
session: 4a773653-5bd9-4035-a87c-484f3431746c
prs: [883, 922]
---

## What happened
- `deploy/cloud-ha/caddy/origin-aop.caddy` (from #883) denied addresses outside `CLOUDFLARE_RANGES` with a bare `respond @outside 403`, next to a catch-all `handle { reverse_proxy … }`. Caddy sorts directives by a fixed order, and `handle` comes before `respond`. So the catch-all answered every request and the deny never ran: any address holding a valid client certificate reached the api.
- #922's origin-aop rehearsal scenario (T08) found it: it got 200, not 403, from outside the ranges. `caddy adapt` showed the 403 route after both proxy routes.
- #927 split the fix out of #922 so it would not wait on that PR's open questions. The deny is now `handle @outside { respond 403 }`, placed first. `handle` blocks are mutually exclusive and keep the order they are written in.
- relay.yml's T04 step now runs `caddy adapt` on the file and then `deploy/cloud-ha/tests/aop-deny-first.sh`. The script fails when the remote_ip 403 route comes after any reverse_proxy route. It fails on the old file (exit 5) and passes on the new one. The Epic 50 stream reviewed the fix with stock `caddy adapt` before the merge (9b8a7c1dc).
- The config was not deployed anywhere: no Epic 50 secrets are provisioned, no nodes exist, and `ORIGIN_MODE` defaults to tunnel.

## What we learned
- `caddy validate` accepts a Caddyfile whose rules never run. Order is a separate property, and only the adapted JSON shows it.
- In a Caddyfile site with a catch-all `handle`, every rule that must run first has to be a `handle` of its own, written first. A bare `respond`, `redir` or `abort` beside it is sorted after the handle and never runs.
- An end-to-end scenario that asserts the deny (T08: 403 from outside the ranges) catches what a config review misses: #883's review read the file and missed it.

## Keep doing
- For each security rule in a production Caddyfile, check the adapted route order in CI, not just `caddy validate`.
- Split a security fix found inside a bigger PR into its own small PR from fresh main, with a test that fails on the old file.

## Open
- An audit of every Caddyfile in the repo after #927 found the same bug in `deploy/cloud-ha/monitoring/Caddyfile.witness` (50.11). Its token check, `respond @unauthed 401`, never ran. #930 fixes it and generalizes the check as `tests/deny-first.sh <json> <host> <status>`. No other file has the pattern.
