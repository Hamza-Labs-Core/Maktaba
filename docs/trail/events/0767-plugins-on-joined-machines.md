---
schema: 1
kind: pr
pr: 767
title: Third-party plugins on joined machines (Epic 54, 54.42)
date: 2026-11-07
session: b17a35c4-3127-455d-bdb2-c21890fa281a
agent: claude-code
prs: [759, 762, 763]
---

## What happened
- A manifest's `placement` (`home`, `any`, `gpu`) says where a plugin's stages run; a joined machine with a Postgres home runs its own `maktaba-plugind` under the same sandbox, and gates what its placement does not fit (`plugin_home_only`, `plugin_no_gpu`).
- A machine fetches an installed version's files from the home over its signed link and checks them against the digest in `plugin_installs`, the store's signature when there is one, and every file, before placing anything (`plugin_package_untrusted` otherwise).
- The machine's plugin API serves reads and media itself and sends every write, quota, lineage and compute call to the home (`/internal/cluster/plugin-api/v1/…`), which answers it as a call on its own socket. The home mints and ends each token; the machine adopts it and checks the plugin is still on at every call, so off, hold and revocation reach it at its next call. Job outcomes go to the home's breaker with the dispatcher's own statements.
- The two-machine rig (`api/twomachine_e2e_test.go`) runs plugin A on the home starting B on the machine, B's write applied by the home with a lineage of 2, and off refused at the machine's next call.

## What we learned
- `plugin_tables_guard` stays as it is: routing a machine's writes through the home's API keeps one place for capability, quota and lineage checks, and the previous release's tests still pass on the new schema.
- The home's auth gate must list the machine routes (`/internal/cluster/plugins/`, `/internal/cluster/plugin-api/`) beside the delegation middleware, or a signed machine gets 401 before the handler runs.
- Main moved twice while the stack waited (compute took plugin API 1.1 and slot 0197, first-party plugins slot 0198): events became 1.2 and the lineage migration 0199. Check `git ls-tree origin/main shared/db/migrations/` right before each push, not only at the start.
- `make compat-prev` writes `api/internal/compatprobe` while it runs; a run stopped part-way leaves it, and `git add -A` then commits it (lint refuses its `fmt.Printf`). Stage paths by name after stopping one.
- The flow check's N22 budget is timed as the process's own CPU time (getrusage, `process.cpuUsage`): the integration runner shares its cores, and its wall clock measured the queue.
- The first-party parity self-test fails locally on plain main with a dev API binary built from this tree; CI is its judge.

## Open
- Compute on a machine always asks the home's models; a machine's own GPU models are not offered to plugins.
