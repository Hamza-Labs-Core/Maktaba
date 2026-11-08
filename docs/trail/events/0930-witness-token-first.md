---
schema: 1
kind: pr
pr: 930
title: The witness's token check never ran (50.11), and one deny-first check for every Caddyfile
date: 2026-11-07
session: 4a773653-5bd9-4035-a87c-484f3431746c
prs: [927]
---

## What happened
- After #927, every Caddyfile in the repo was audited for a bare `respond @matcher` beside `handle` blocks. `deploy/cloud-ha/monitoring/Caddyfile.witness` (50.11) had one: `respond @unauthed 401`. In `caddy adapt` the 401 was the last route, so anyone could push rounds to the Pushgateway and read network 1's view without the token. The fix is `handle @unauthed { respond 401 }`, placed first. The file was not deployed: no nodes exist.
- `tests/aop-deny-first.sh` became `tests/deny-first.sh <json> <host> <status> [matcher]`. The site's deny route must come before every route that reverse-proxies or serves files. An optional key (`remote_ip`, `header`) must appear in the deny's match, so an unrelated matched route answering the same status doesn't pass. The Epic 50 stream's review asked for that last part.
- `monitoring/check.sh` runs the check on the witness file (`401 header`), and relay.yml's T04 step on the aop fallback (`403 remote_ip`). Both old files fail it.

## What we learned
- One bug pattern found in a config is worth a search across every config of the same kind. Here the second copy was an auth check, which made it worse than the first.

## Keep doing
- Every new Caddyfile with a deny rule gets a `deny-first.sh` call in the CI step that validates it.

## Open
- Nothing.
