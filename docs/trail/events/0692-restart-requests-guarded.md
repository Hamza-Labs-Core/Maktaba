---
schema: 1
kind: pr
pr: 692
title: Restart requests are the home's, retried when a read fails, and counted apart
date: 2026-11-06
session: b705488f-6ddd-4a03-8d08-60545e9b7ffa
prs: [663]
---

## What happened
- A review of #663 found three problems. A joined machine could rewrite or lower a restart's `seq`, which the runners then ignored. One failed read of the requests waited for an unrelated change before anyone read them again. And the route counted loops in other processes as restarted.
- Slot 0190's `component_restarts_guard()` refuses writes from `maktaba_worker` members that are not members of the table's owner, and any `seq` that goes down; the grants are unchanged. A failed read is retried at the next poll (Python: `revision = -1`; Go: a retry timer). The answer lists `requested` hosts apart from `restarted`.

## What we learned
- A trigger can take writes away from the worker group without touching its grants. The grants have to stay, because the previous release re-makes `maktaba_worker` with the default grants and `TestWorkerGroupAsTheMigrationsMadeIt` reads any revoke as a change. "A member of the owner" lets both the home's own role and a superuser through.
- `make compat-prev` runs locally with the Postgres in the installed Maktaba's update payload on `PATH` (it needs psql), in about 20 minutes. It leaves `api/internal/compatprobe/` behind while it runs; don't commit it.
- This PR's slot moved three times (0186, 0187, 0190) while #672 and #678 merged. Number a slot only after the PRs ahead of it have merged: a branch whose slot sits above a gap fails `migration-lint`.
- The OpenAPI prose has an Arabic twin (`shared/api/openapi.ar.json`, keyed by JSON pointer with the English it translates): change both.
