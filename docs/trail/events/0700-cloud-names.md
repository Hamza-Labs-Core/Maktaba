---
schema: 1
kind: pr
pr: 700
title: Epic 50 starts: the cloud's names, reserved infrastructure slugs, a foreign-domain lint
date: 2026-11-06
session: c8daad15-7bc0-4b14-a10c-8b559dcb37d3
---

## What happened
- Story 50.01, the first of Epic 50 (Hetzner, no downtime): `docs/cloud-dns.md` lists the cloud's public names; cloud migration 0050 reserves `canary`, `direct`, `staging`, `plugins`, `plugins-cdn` and `r1`…`r99`; a reserved slug is now `409 subdomain_reserved` at redeem and at `/v1/subdomains/claim` (which never checked the list); `tools/lint/no-foreign-domains.sh` runs in the lint job's repo checks.

## What we learned
- Cloud migrations are numbered by epic slot, and goose's `UpContext` refuses a version below the database's head. Epic 50's 0050 after Epic 51's 0051 would have failed every cloud database already migrated; `Migrator.Up` now passes `goose.WithAllowMissing()`. Any later epic taking a lower slot relies on it.
- The spec's lint regex would flag the bare word `maktaba.app` in prose, which specs need. The lint flags a host (a label before the domain) or a URL only. A repo-wide scan also found the log line schema's `$id` on `maktaba.app`.
- A per-line bash scan of the whole repo took minutes; pre-filtering with one `grep -i` before the loop made it 5 s.
- `tools/ci/ci_paths.py guard` fails on any new top-level tools folder that no filter or `NOT_IN_CI` names: `tools/lint/**` was added to `NOT_IN_CI` (the always-run repo job runs it).
