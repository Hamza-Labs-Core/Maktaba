---
schema: 1
kind: pr
pr: 608
title: Epic 51: configurable components, all 19 stories
date: 2026-11-03
session: unknown
source: claude-trail.md
---

## What happened
- Merge cdffe7913 (merged 2026-11-04). All 19 stories in one integration PR: story branches `e51/*` merged into `claude/epic51-configurable-components-b0f723` by the coordinator.
- One catalog (`shared/components/catalog.toml`, copied to the pipeline by `make catalog-check`; every stage sits in exactly one component).
- Desired state in Postgres with SQLite twins at migrations 0178 `components`, 0179 `component_override_seed`, 0180 `component_stage_gate` and 0181 `component_licence`, plus SchemaRev 181. Existing homes are seeded from today's switches.
- One gate rule, `runner.GatesAt`, is shared by the API's capabilities, the supervisor and the pipeline's `stages.py`. The `[components] reconcile = off` kill switch makes runners report but act on nothing. Packs ship with each server release under its signature. Apps hide or explain what is off (51.11).
- Migration slots were renumbered twice: for main's 0173 (`write_channels_logo_on_filler`) and for #611's 0174 (`second_listen_stage`). Only unshipped files moved (`git mv`). The new `second_listen` stage joined the transcription component.
- Bug found during the merge: an asset (`runtime.ml`, models) that the seed writes as `installed` was gated off, which turned transcription off on a seeded home. Now an asset is on unless `absent`. Test: `TestGatesInstalledAssetIsMet`.
- The 20 failing checks were each fixed at their cause (84531b3b4):
  - Lint: revive, staticcheck and shellcheck findings.
  - Update metrics: the new `packs` journal step was missing.
  - Capabilities coalesce window: now an option; the test uses 50 ms.
  - LISTEN tests: the pq and pgx tests shared one database and terminated each other's listener. Each tags its connection with `application_name` and terminates only its own.
  - compat-prev: v0.3.69's worker-group test cannot know Epic 51's newer tables; it gets a test-only patch, `tools/compat-prev/testfixes/v0.3.69/workergroup-newer-tables.patch`.
  - Update-rehearsal scenario 13: the release after the rollback carries both kept migrations, rev+1 and rev+2.
  - N9 bundle size: main's mDNS announcer (`sh` running `dns-sd`, a supervisor child) was counted as a fifth service. `bundle_size.process_tree` lists a supervisor child whose subtree runs a responder as a helper; its memory still counts.
  - Cloud billing test: steps down past every newer migration instead of `Down(1)`.
  - CodeQL, 25 high path-injection and zip-slip alerts in packs: pack and revision folder names are reduced to one path element (`filepath.Base(filepath.Clean("/"+s))`); routes refuse ids and import paths containing `..`, and import paths must be absolute; offline-file names and release versions containing `..` are refused.
  - Player UI: the 51.11 change broke launching straight into Live TV while it is off. `CapabilitiesStore.answered` rebuilds the shell once, at the first answer. The open-screen test leaves the player before tapping Home. The iPad guide test uses 40 channels, because 16 overflowed the screen by about 40 pt.
- During story 51.06 a test rig contacted the live API; root-fixed with a regression test so tests never reach the live home.
- N7 was redefined to measure the core home. N9 counts services, not helpers.
- Before the merge, live re-run fixes and the red CI at a31a579e8 (pushed b5f876d23):
  - Missing packs: the packs runner installs an asset with no desired row (the catalog default, enabled), honouring offline mode; the install route writes the row again (component.install, new generation) so Install and Retry in the drawer act (06460a7de, merged fb038f8f9).
  - One requirements rule for GET and preview: an asset whose status row says Installed false makes a switchable component wait (`requirement_missing:<asset>`), also with no process of its own (transcription with workers = 0); a preview's "from" is the actual phase (30b1ad243).
  - Reason texts name components by title, also from params (77e556eae, ce87fe746); the drawer's Close is localised (4a84c2e81); a process whose component is off reads held, not backoff (3997fbf5f).
  - Every update rehearsal and the auto-update e2e rolled back on "ollama is stopped" / role_not_running: the dormant process of an off component counted as a role that should run. The preflight and the home's watch skip a blocked process with no pid (b5f876d23).
  - Replica: a node restarted in a home-database outage never served, because its API dialled the components listener on the startup path (lib/pq bounds only the dial). Start dials off the caller's path and waits at most ConnectTimeout. The runner restarted a loop in a tight loop when its parent context ended before Close (8d68c83ab).
  - The catalog copy check asserted byte equality, and pytest's untruncated diff under CI=true took over a second (soft cap); TestCrashBackoffAndCrashLoop read the restart count before the sixth crash was counted (351c6f7f1).
  - The packs job's shellcheck needed -x for the recipes' source= directive; SC2015 in runtime.ollama (409e18842).
- 2026-11-04 (pushed 9bf413503): the design system's close button follows the page's language through a LabelsProvider given by I18nProvider (37e5ae7ee); the four unnamed process roles have English and Arabic on web, Apple and Android (45ac64dbb); components-live T13 on b5f876d23 caught the pipeline runner reporting phases before its LISTEN was up, so a change in that gap waited for the 30 s poll: it listens first (9bf413503).
- 2026-11-04 (pushed f598e474d): CI at 9bf413503 caught a data race in the packs tests (a no-row pack installs beside a removal, and both wrote the manager's log): logf locks, the test marks the other packs absent and waits for the removal. Rehearsal 12 expected a row for every component, but its 0.3.78 install runs the stand-in pipeline wheel with no ML runtime, so the seed rightly leaves runtime.ml with no row: the test allows that for an asset an enabled component needs and that is not on disk.
- 2026-11-04 (pushed 4e3796887, the head #608 merged at): CI at f598e474d had three real failures, each fixed at its cause:
  - Rehearsal 12 read `wanted=true` for the dormant process of an off component. The supervisor's snapshot reports an always-on (not on-demand) process held by its component as not wanted; the internal want is unchanged, so it starts once the gate opens. An on-demand held process keeps Wanted, because TestSlotGateClosed needs it (e34c2a529).
  - The Mac app's .dmg build failed on `hdiutil: create failed - Resource busy`. `tauri-build.sh` retries up to HDIUTIL_TRIES=3 times, for that error only, with a test using a fake pnpm (ce307cddf). The bundle-size budget was not raised.
  - The Player UI's shimmer contrast audit measured a moving gradient. The gallery's `-maktaba-gallery-still` argument pauses the shimmer, as Reduce Motion does, so the audit measures a still light (4e3796887).

## What we learned
- Tests sharing one Postgres database must not terminate each other's LISTEN connections; tag connections with `application_name`.
- pg_ctl's own -p is the postgres binary path; for a scratch Postgres the port and `-k ''` go in `-o "-p N -k ''"`.
- A dormant process of an off component must not count as a role that should run, or update preflights roll back.
- lib/pq bounds only the dial; a listener dial on a startup path can block serving in a database outage.
- pytest's untruncated diff under CI=true can take over a second on a large byte-equality assert.
- A workspace hook refused writes to worktrees an agent created itself, which blocked some story agents; agents launched with an isolated worktree worked in their own directory instead.

## Open
- Left as is: the CLI import records the OS user, as every CLI component write does (story 51.08).
