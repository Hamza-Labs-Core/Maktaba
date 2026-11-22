---
schema: 1
kind: pr
pr: 1497
title: "Search suggestions, did-you-mean, facets and sort (story 49.8): seven CI fixes after review"
date: 2026-11-22
session: 7998f2a8-8a3e-41cf-99b3-4cf9a473ddc3
agent: claude-code
prs: [1491, 1498, 1499, 1503]
---

## What happened
- #1497 merged as b698e192a. Epic 49 story 49.8. Its review findings are in the event of #1498
  (`d20261122-498-suggestions-review`).
- **What shipped:**
  - `search_terms` and `search_speakers` (migration 0235), kept by the `keyword_index` stage
    at version 2;
  - suggestions as you type;
  - did-you-mean, facets and sort on `POST /api/search`;
  - a library merge drops the merged library's documents and dictionary rows.
- **CI needed seven fixes after the reviews, each a gate doing its job:**
  - `plugins / first-party`: the stage graph's golden held `keyword_index` at version 1;
  - `affected-tests`: the SchemaRev bumps needed a `No-Test-Change` trailer;
  - `parental-bypass`: suggest was on the rehearsal's blind list ("nothing of a video"),
    and now answers titles and speakers;
  - `lint`: names that shadow the built-ins `max` and `any`;
  - `integration (api)`: the API level check refused dropping `SearchFacets.tag`, an empty
    field older apps may read;
  - `integration / pipeline`: a fresh database's `job_stages` seed must equal the graph's
    stage version;
  - `update-rehearsal`: the first version of that seed was `UPDATE job_stages SET version =
    2`. The updater's `Reversible` calls an Up `UPDATE ... SET` lossy, so a rollback from
    this commit to 0.3.153 put the whole database back from its dump. It is now
    `INSERT ... ON CONFLICT DO UPDATE ... WHERE version < 2`, and the Down sets 1 back.
- **Also:**
  - `compat-prev`: v0.3.154's slot-0232 test needs a patch for slot 0235's exclusions
    (`tools/compat-prev/testfixes/below-schema-0235/`);
  - `unit`: an unrelated memory test at the time cap moved to the integration tier (#1499);
  - a Docker Hub timeout, passed on re-run.

## What we learned
- A migration's Up must not overwrite a row with a plain `UPDATE`, even a seed's version.
  The updater cannot know a Down undoes it, and the rollback restores the whole database.
  Seed with `INSERT ... ON CONFLICT DO UPDATE`.
- A migration that redefines `cluster_copy_excluded()` breaks the older releases' tests of
  the slot before it. Add a `below-schema-NNNN` compat-prev patch in the same PR.
- A stage version bump needs, in the same PR:
  - the graph golden (`graph_golden.py --write`);
  - the `job_stages` seed in a migration;
  - the updater's rollback rules.
- Running the slow gates locally before pushing would have saved four CI rounds:
  - `golangci-lint` on the changed packages;
  - `go test -tags parental_bypass ./internal/router/`;
  - `tools/plugins/graph_golden.py`;
  - `pipeline/tests/db/test_queue_graph_migrations.py`;
  - the compat level check (`./internal/compat/...`).

## Open
- v0.3.154's `TestSlot0232BM25Indexes` and `TestBuildOnceOneAtATime` fail on this build's
  migrations on PostgreSQL 18 (0234 renamed the indexes). compat-prev runs on 16, where they
  skip.
