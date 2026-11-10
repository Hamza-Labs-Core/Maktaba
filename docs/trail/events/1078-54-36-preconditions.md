---
schema: 1
kind: pr
pr: 1078
title: Story 54.36 §4 waits for its real preconditions; story 54.43 counts the in-process fallbacks
date: 2026-11-09
session: afbdedd7-9eb7-4572-a79a-251f2ae4ea96
prs: [998, 1074]
---

## What happened
- Story 54.36 §4 (removing the in-process path) was due one release after wave 9 (#998, first in v0.3.109; v0.3.110 to v0.3.112 shipped since). Its precondition was checked read-only on the live home's bundled Postgres: no `job_stages` row has `impl_changed_by` set, so no stage of any wave was flipped back. 52 stages are `plugin` and 10 `inproc`.
- The removal was stopped before any code change, because the in-process path is still load-bearing:
  - ten stages have no plugin: `component_catchup` (kept in the core on purpose), `enrich` and `categorize` (owner decisions), `word_check`, `display_title`, `youtube_collections` and `youtube_tartube` (need conversion), and `dizquetv_audit`, `dizquetv_channels` and `dizquetv_show`;
  - `plugins/native.py` `choose` runs every first-party stage in-process on SQLite, the default database of a fresh single-host install (`config.Defaults`);
  - it does the same for Windows share libraries, the Chroma index store (`index`, `chapter_infer`), runners that are not serving, stale roles or areas, unregistered statements and sandbox refusals;
  - the fallback counts lived only in the worker's memory.
- Decision: keep the switch and fix the spec. #1074 had just made the switch reliable, and it is an admin's only lever when a plugin stage misbehaves; its in-process target stays anyway.
- §4 now waits for (a) every `GRAPH` stage has a plugin or is kept in the core, with the ten listed and what each waits on; (b) SQLite homes have a path that is not in-process, or an announced end of support; (c) the Windows share fallback is retired; (d) the Chroma index store is retired, or its two stages run over it from the runner; (e) the fallbacks are counted where an admin reads them (new story 54.43), with a full release showing none on the live home. The flip route and the CLI stay until then.
- `dizquetv_*` are retired, not live code (`engine/retired.py`, `shared/components/retired.go`): their code left with the dizqueTV extension, slot 0215 kept their rows, and they need only a cleanup migration.
- Specs only, with Arabic for both changed wiki entries; merged as 541d4f31e on head ced4e8b43 with all 51 checks green or skipped.

## What we learned
- A removal step that says "one release later" also needs the conditions that made the old path necessary, not only the absence of flip-backs. Read the dispatcher's fallback branches before deleting what they fall back to.
- The live home answers a read-only `job_stages` query through the bundled psql with the pgpass file in `keys/` and `PGOPTIONS='-c default_transaction_read_only=on'`.
- In-memory counters prove nothing across restarts. Persist what a decision depends on.

## Keep doing
- Stop and report when a spec's preconditions don't hold in the code, with the evidence, rather than shipping a partial removal.

## Open
- 54.36 §4 waits for (a) to (e). Per (a): owner decisions on `enrich` and `categorize`; conversion of `word_check`, `display_title`, `youtube_collections` and `youtube_tartube`; a cleanup migration for the retired dizqueTV and Plex stages' rows.
- 54.36 §4 (b): what SQLite homes get, a plugin path or an end of support, is a product decision.
