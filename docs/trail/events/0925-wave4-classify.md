---
schema: 1
kind: pr
pr: 925
title: Epic 54 wave 4 moves the rest of classify into tv.maktaba.classify, and enrich stays in the worker
date: 2026-11-08
session: b0ea7fd9-6e6d-4c4f-ab23-200b28d0b3bd
---

## What happened
- `detect`, `tag`, `check_tags`, `check_clips`, `books` and `review_pages` now run in `tv.maktaba.classify`. The model's questions go through `compute.generate`: a first-party token whose stage names `model_rules` may send `messages`, `format` and `options`, which go to the server and model in `rules.<section>`. A third-party plugin that sends them gets 400. The roles are in `0215_fp_roles`. Merged at f5063f7b1.
- `books` sets `runner = "net"` with `net = ["ar.wikipedia.org"]`. The runner gives network only to the `net` group. A stage that runs outside its plugin's own runner group takes only the network there, not the plugin's files, tools or GPU. A catalog test allows one plugin per component, so `books` stays in classify and is not a plugin of its own.
- `enrich` was not moved. It reads any page a search result links to, so no host list can cover it, and its keys are sealed in `home_secrets`, which the plugin API does not serve. Moving it waits on an owner decision.
- The PR's migration slot collided three times while waves 9 and dizqueTV (#942) merged: 0210, then 0213, then 0214, and finally 0215. Each time, the roles migration was regenerated from `roles.py sql` and SchemaRev was raised in all three places.

## What we learned
- A named statement must be one fixed text. A list argument is `= ANY($1::uuid[])` on Postgres and `IN (SELECT value FROM json_each($1))` on SQLite. Code that builds `IN (…)` at run time fails as an unregistered statement once the stage runs as a plugin, as `engine.propagate`'s `last_success` did.
- A plugin reads only the `rules.<section>` names its manifest lists in `settings`. `tag` read `rules.tag` and failed `plugin_scope` until the manifest listed it.
- `roles.py check` compares the newest `NNNN_fp_roles` migration with the roles of every manifest, not only those this PR changed. After a renumber, regenerate the whole file; do not copy the old one.
- Rehearsal waves 16.4–16.6 failed for two reasons that had nothing to do with the move. First, the runner restarts at the flip, so a job claimed during the restart ran in-process; the scenario now runs warm-up jobs until each stage has run in its runner (0814c1177). Second, `tag` and `exact_dates` debounce for 300 s; jobs the scenario queues are now due at once (1d5e518b8). That was not enough. A `detect` run enqueues its video's `tag` with the same debounce, and 16.4's warm-up timed out waiting for it. The waits now make every pending job of the wave's stages due as it appears (4e492e58d).
- A parity test that patches HTTP must give its seeder its own `MonkeyPatch.context()`. When the seeder's `undo()` also removed the test's patch, the test reached the real Wikipedia.

- After the last renumber, `0215_fp_roles.sql` opened a `StatementBegin` twice: the script that copied 0214's header kept reading lines that start with `--`, and `-- +goose StatementBegin` is one of them. `roles.py check`, `make lint-migrations` and every local test passed. In CI, `lint / lint` was green while integration, replica and media-e2e failed at `goose up`. The fix is eca0a38e3. #949 adds the lint check that would have caught it.
- The api/internal/cluster migration tests skip on a scratch server that already has `maktaba_worker`. To prove a migration parses, run `go run . migrate up` from `api/` against a new database on the scratch server.

## Keep doing
- Re-check main's newest migration right before the merge (the watcher's slot gate), and use `tools/dev/merge-main.sh`. Several branches take migration slots in the same hours.

## Open
- Moving `enrich` needs a secrets route for first-party plugins and a rule for its page reads (owner decision).
