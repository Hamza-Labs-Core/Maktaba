---
schema: 1
kind: pr
pr: 750
title: Maktaba's own stages as first-party plugins, the foundation (Epic 54, 54.22-54.27)
date: 2026-11-06
session: 8687f2a4-808d-47dd-93b7-2c1167e0f385
prs: [683, 693, 707, 717]
---

## What happened
- The foundation of Epic 54 Phase 4. It adds:
  - the trusted manifest, refused in any third-party manifest by all four validators;
  - the bundle of first-party manifests and statements embedded in the server and carried in the pipeline wheel;
  - `maktaba-plugin-runner`, sandboxed with Seatbelt on macOS and Landlock plus seccomp on Linux;
  - named statements run by the plugin API under per-plugin roles;
  - the golden graph, `_fp()` stages and the per-stage switch (`job_stages.impl`, migration 0197, `PUT /api/queues/{stage}/impl`, `maktaba-server stages impl`);
  - the parity harness with the real Go plugin API (`api/tools/plugin-api-dev`).
- No stage moved: every stage is still `inproc`.

## What we learned
- Moved stage code can stay unchanged if the runner's database maps each SQL text to its registered name. The API runs only the registered text, under `SET LOCAL ROLE maktaba_fp_<plugin>`. Parity then compares two paths of one code base.
- A first-party plugin's code in the pipeline wheel needs a hatch build hook. `force-include` also applies to editable installs, and its stale copy shadows the working tree.
- `audit_log.category` is a closed list: use `components` for a new action. Its `actor_user_id` references `users`, so write it through a subselect.
- The queues handler's sqlmock tests pin the stage SELECT's 22 columns. Read a new column in a separate query.
- `display_title` asks the language model over HTTP when it is enabled, so it moves with wave 4 (`compute.generate`), not wave 1.

## Keep doing
- Run `make compat-prev` and the full pipeline suite before pushing a migration. Neither may run while the same worktree is being merged.

## Open
- Waves 1-9 (54.28-54.36) and the removal of the in-process path.
- Per-plugin `.mkplugin` packages in `checksums.txt` wait for a release-workflow change (spec §15.12).
