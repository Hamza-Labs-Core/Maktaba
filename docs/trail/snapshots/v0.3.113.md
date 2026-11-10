---
schema: 1
tag: v0.3.113
up_to_commit: c14c1434f
built_from: v0.3.112
---

# Trail snapshot: v0.3.113

## Keep doing

**Working**
- Run the PR flow to the merge without asking; ask only for real product decisions (costly backfill, deleting data). Follow-ups start at once as parallel worktree agents. (d20261031-standing-rules, d20261102-coordinator-rules)
- Verify every claim yourself before acting on it, and check a spec's claims against the code: read the code a spec section describes before relying on it (the code was right and the spec stale, twice). Before writing a fix for an issue, or its options, check that it is not already fixed or decided, and before carrying an open item forward or taking a trail line as work look for it in the code (native TV crash capture stayed open after it shipped); before blaming one PR, check the PRs merged around it; before splitting out a fix for a failure that breaks every PR, check main's latest merges. (d20261014-pc-dizquetv-origin-and-rules, d20261031-standing-rules, #587, #795, #879, #888, d20261107-test-infra-missteps, d20261107-correction-0685-player-sentence, #952, d20261108-snapshot-fixture-clock)
- When a check can't be made directly, look for what the context already proves: a page reached over the LAN address already proves the raw IP works. (#906)
- Build tools for the work instead of doing it by hand; a recurring manual step becomes a feature; rules are settings with a UI, not code. (d20261014-pc-dizquetv-origin-and-rules, d20261020-pc-coordinator-merge-rules)
- No placeholder features: a feature is built or it is not shown. (#128, d20261014-pc-dizquetv-origin-and-rules)
- Review every PR yourself; the Copilot reviewer check and its quota comment are noise. Merge with a merge commit on the reviewed head, every check present and green (main has no branch protection). Watchers only report: merge by hand after reading `gh pr checks`, and count a check that could not be read as not green: one JSON read with jq, every check completed SUCCESS, SKIPPED or NEUTRAL, at least one, the merge pinned to the read head. When a gate fails, stop any watcher before rerunning anything, or it merges a lucky rerun. A merge check also refuses any PR whose base is not main (a PR into a branch runs 3 checks, all green, and its merge never reaches main), requires by name the checks the change is for (one skipped by a path filter counts as passing), and stops when the head moves. Write it in bash with arrays and run read-then-merge sequences in `bash -c`, never zsh; never wrap the read in `|| true`; start it with a 7200000 ms background limit (the default 30 minutes kills it). Never edit a script a background process is running (bash reads it as it runs and resumed at a shifted offset onto the merge line): copy it under a new name. Adopting another session's watchers, read them, stop them by PID and replace them. Print the rollup's names (`jq -r '.statusCheckRollup[].name'`) before requiring a check: reusable workflows prefix their jobs (`mac-app / Bundle size (Core payload)`), and a watcher matching the bare name never merges; report MISSING apart from not green (a gate matching `Player UI tests (mac)` saw `apple-apps / Player UI tests (mac)` as absent the whole time). A PR that goes green can still be `DIRTY` because main moved: when only the CHANGELOGs conflict the watcher reruns `tools/dev/merge-main.sh`, pushes and keeps watching (at most three times). (d20261107-watcher-check-name, d20261107-store-stream-merges, #893, d20261031-standing-rules, d20261020-pc-coordinator-merge-rules, #314, #212, d20261107-watcher-merged-early, #813, #808, #810, #815, #879, #877, d20261107-paid-plugins-merge-problems, d20261107-stacked-pr-merged-into-its-base, d20261107-plugin-stream-takeover, d20261107-test-infra-missteps, d20261108-snapshot-fixture-clock)
- Arm a PR watcher only once GitHub reports the new head: armed right after a push, it can read the old head and stop with "head moved". (#1049)
- A child agent without a PR-watch tool hands the merge back explicitly; a child woken by a CI failure fixes it in its own worktree, so the parent reads the child's thread before editing that worktree. (#942, #956)
- Fold small Apple fixes into one integration PR; fix agents push branches, not PRs. (#539)
- When a fix lands on one platform, check whether the other platforms have the same fault. (#813)
- Before merging a fix, confirm the check it fixes actually ran on the PR; a path filter can skip it silently, and a job measuring a whole-product budget needs every input of that budget in its filter. A merge that lands red on a release gate blocks every release after it: restoring the gate on main comes before feature work, and a fix carried inside a large PR is split out to land alone. A PR that changes `ci.yml` runs every job and inherits main's red ones: check main's state for a failed job before debugging it in the PR. Before merging a gate change, look at what it does on main's next run. (#859, #839, #867)
- Stack a follow-up that touches an open PR's files on its branch. Once the base merges, retarget to main (`gh pr edit N --base main`; a base change starts no CI), merge main in and push, run `tools/ci` tests right then (main's repo-wide lint reaches a stacked PR only after the retarget), and let the merge check decide; never merge a stacked PR the moment its base merges. (#835, #851, d20261107-stacked-pr-merged-into-its-base)
- Write close/closes/closed, fix/fixes/fixed or resolve/resolves/resolved before a `#N` only to close it on purpose: GitHub's keywords close open pull requests too, from a merged PR's body or a commit reaching main, attributed to whoever merged with no other trace. Mention another PR as "the merge for PR N" or "(#N)". `tools/ci/pr_closing_refs.py` fails a body that would close an open PR; CI reads the body at the next push, not on an edit. (d20261107-closing-keyword-closed-a-pr, #874)
- Coordinate file by file with the session that owns neighbouring code before branching, and confirm a session's identity (from a message it sent) before messaging it: a thread-search snippet sent one note to the owner's session. (#850, #877)
- A story rollout with a planned second step goes in an event's Open list and gets checked: `server.log`'s switch to warnings waited from v0.3.83 to v0.3.96. (#891)
- Build a new entry point onto an existing flow's state (the store site's Buy makes the home's purchase intent, then one checkout path), not beside it. (#887)
- Bring main in with `tools/dev/merge-main.sh`: it merges the CHANGELOGs entry by entry and stops on anything else; resolve that by hand and run it again. A line-wise resolution or `merge=union` interleaves two entries that share a line and drops one's tail. A merge resolver checks every file for markers before it commits, and the code is compiled after every resolution: keeping both sides of a hunk that ends before a shared closing brace leaves the first block open. When main moves files, typecheck after merging it in (imports in files the branch added never conflict), and run `prettier --check` after a hand resolution in `web/`; two branches that each add a harness constructor variant resolve to one core constructor with thin wrappers. (#826, #808, #846, d20261107-plugin-stream-takeover, d20261107-store-stream-merges)
- Before every push, check that no commit adds a binary or a file over 1 MB (`git diff-tree --numstat` per commit); build Go with `go build -o` outside the tree, and read `git status` after a full test run before any `git add -A`: a 9 MB binary and a test's `areas.json` were both committed that way. Run `git diff --name-only origin/main...HEAD` before every push and delete local build outputs after switching branches: an output ignored only on another branch (`web/dist-admin`) followed one into a PR. (#819, #821, #877)
- Reuse the home's own code where another service must agree with it: the plugin store reads packages with the home's Rust reader built to wasm, runs the import audit as a crate shared with the host (`plugind/crates/audit`) and the flow check as #759's checker. A wrapper that walks the graph again is a second implementation; the store only loads data. Build a store check from the shared corpus's own cases, not hand-made fixtures. (#753, #770)
- Keep worktrees under `.claude/worktrees/`, never `$TMPDIR`; remove merged worktrees and branches. (d20261031-standing-rules, d20261102-worktree-cleanup)
- Start a CPU load generator, or any background work, with a hard time limit (`timeout 120 yes > /dev/null &`; macOS has no `timeout`: `perl -e 'alarm shift; exec @ARGV' N …`), record each PID (`$!`) in a file, kill by those PIDs and confirm none are left (`pgrep -x yes` empty). When a machine is slow, look for long-running processes whose parent is PID 1 before suspecting the app. (d20261107-orphaned-cpu-load, #795, #815)
- Branch stacks point HOME, `MAKTABA_CLUSTER_DIR` and `MAKTABA_ARCHIVE_DIR` at scratch folders. (#621, #623)
- Use only real PR numbers and shas; attribute history with git (blame, `log -L`, PR file lists). (d20261031-standing-rules, #655)
- Never quote or attribute the owner's words in repo text; state the requirement neutrally. (#603, #604)
- Write each PR's event in its own trail-only PR, never in the code PR (check `gh pr diff N --name-only` for `docs/trail/` before merging); open it alongside the code PR and merge it after. A generated block (the agent-trail markers) cannot carry a repository's own rule: put the override outside the markers. Build snapshots in a scratchpad worktree off origin/main and run `trail check` before pushing; check each carried Open item against newer events, and check origin/main's `snapshots/` again right before opening the PR so two sessions do not snapshot the same tag. On a trail-only PR wait for CodeQL (the slowest job), not the lint. The trail's checks are the agent-trail CLI (`uvx --from git+https://github.com/Hamza-Labs-Core/agent-trail@v0 trail check`); `tools/trail/trail.py` is gone. (#655, #532, #706, #726, #739, #1020)
- Keep CI's paths-ignore, `next_tag.py`'s TRAIL_ONLY and `.trail.toml`'s `[trail_only]` paths equal (test_ci_paths.py and test_next_tag.py check it). (#702, #699)
- Merge main early into a long epic branch: a page added there has to follow a move made on main, and the conflict shows only as an "added by us" file on the old path. Check URLs in docs and runbook lints too. (d20261107-852-admin-console-move)
- Hand over an unfinished scenario with its known bugs and their files listed. (#892)
- When adding a TLS rule, find every client that can reach a home, not only the API client (both image caches build their own); when a server-supplied value relaxes a check, bind it to what it is for (the address it pins). (#903)
- Map new API errors through the web app's existing `error.<code>` keys, even when a spec names others, and say so in the PR. (#873)
- With every merge to main conflicting on the CHANGELOGs, script the round: merge-main, gate, push, watch. (#959)
- Compile an app locally (no simulator) after an untested UI fix, and run gofmt before every push (the affected-tests gate does not), so neither costs a full CI round. (#970)
- Before building a leftover from a trail line, check whether a later merge built it: an event can be written on a branch that predates it. (#907)
- Split a fix found by a long-running end-to-end PR into its own small PR, so it merges on its own checks. (#986, #997)
- When a rule file and a remembered instruction disagree, ask before telling delegates which wins. (#1020)

**Releases and live**
- Prove update, rollback, restore and migration paths in CI first: nothing touching update, restore, migration, supervisor, launcher, front door or TV-staging code ships unless `update-rehearsal` passed on that exact commit. One release at a time on the live install. (d20261031-standing-rules, #435, #433, #460, #461)
- Never stop processes on the live home while an update watch runs; read the server log's watch lines before blaming the release. (d20261107-update-watch-rollback)
- Give every role and binary a test that starts its real process once, stays up and stops cleanly, before its deploy ships (re-run the test binary as `serve --role X` from one test, about 5 s): the cloud worker double-bound its port and exited at start from June, and `--role dns` was refused by config, while every job's unit test passed. For each systemd unit, check which script installs its `ExecStart` binary and keeps it updated (`/usr/local/bin/maktaba-cloud` had none; `bin/app-update` now). Put a deploy's per-node logic in a script with env overrides so `scripts-test.sh` tests it with a stubbed `systemctl`. (#877, #882, #895, #892)
- Tags are pushed by the scheduled release session with a person's credentials (`next_tag.py --green`): the newest main commit whose CI and rehearsal both concluded `success`, as the next patch; no workflow tags (`auto-tag.yml` is removed). Trail-only commits are never tagged; check remote tags before picking a version. A tag workflow missing a leg is re-run (release.yml) or dispatched by hand on its tag (the others); release.yml is never dispatched. (#730, #725, #699, #563, #553, d20261031-release-history-to-v0344, d20261020-pc-coordinator-merge-rules)
- Keep the Plex token on the home only; nodes go through the home. Keep secrets out of output, commits and exports; only the owner types credentials. (#78, d20261031-standing-rules, d20261020-pc-coordinator-merge-rules)
- Use live data only for justified recovery: tests use scratch Postgres; pause stages through `POST /api/queues/{stage}/pause`, never raw SQL. (d20261031-standing-rules, #524)
- Write a Down script for every migration that undoes exactly its Up; run `RollbackSafe` on a new slot before pushing (a second, against the rehearsal's 30 minutes). (d20261107-rollback-down-scripts, #799)
- Report disk use with `du -sh`, checked against the folder's retention rules: `ls -l` shows a directory-format dump as a few KB. (d20261108-backups-tidy)
- During a long restore watch `pg_stat_progress_create_index` (blocks_done against blocks_total) for the estimate. (d20261107-restore-vector-index)
- On the PC: restart services only through their sign-in tasks; check an installer's sha256, then the owner runs it. Prove Windows behaviour on CI's runner, not the owner's PC. (d20261025-pc-windows-app-node, #457)
- Prefer keyless federation over downloaded service-account keys. (#557)
- No Stripe or Stripe-owned services for any payment, in specs or code; payment providers are pluggable (`PaymentProvider`, each in its own folder), Adyen for Platforms the first real one. Check a brief's suggestion against the trail before building it. (#591, #593, #735)
- A release step that talks to an optional service gates on its secret being non-empty and warns on failure, so neither a missing secret nor an outage can fail a release. (#957, #961, #967)
- Check every secret the api reads against both deploy paths (`tools/ci/deploy_secrets.py`) when a secret is added, and test deploy scripts against a fake `ssh` that runs the remote command locally. (#1010)

**Tests and CI**
- A code change ships with its tests changed in the same push (affected-tests gate; `No-Test-Change: <reason>` only when the mapped tests cannot run locally). (#516, d20261031-standing-rules, #685)
- Fix flaky tests at the root; read the log first, a flaky-looking test is often a real bug (in Epic 53's series as many failures were real as were flakes; a flaky TempDir cleanup was a shutdown bug). A UI failure is the change's only if the same code fails the shard elsewhere and the failing test does not move between runs. Before calling a UI test a flake, read what it waits for: a self-dismissing element checked after slower queries races a loaded simulator; fix the order, not the timeout. Read the xcresult's activity times (`xcresulttool get test-results activities`) and the job log's `t = …` lines before touching a timeout: they tell a slow runner from a state that never came. Meet test budgets at the cause, never by raising caps or splitting tests: read the slowest-tests list in the integration job's log first. A test that waits for one asynchronous effect waits again for each other thing it reads, even ones sent in order; a race that never reproduces locally (300 `-race` runs) is fixed by reading the code, and checked with `-race -count=50` or more. A test reads a writer's dated folders by glob or the writer's own clock. (d20261031-standing-rules, #364, #635, #271, #716, d20261106-epic53-ci-flakes, #673, #803, #858, #870, #881)
- Turn a "should never happen" rule into a test-time error and run the whole suite under it. (#662)
- Run a wave's full pipeline suite on a freshly initialised Postgres cluster (`initdb -E UTF8`; SQL_ASCII fails the Unicode tests) before opening its PR: roles are cluster-wide, so a reused cluster hides order bugs, and CI's `unit` job runs only the affected tests. (#847, #902)
- When a layout bug is found by reading code, add the reference test that would have caught it; read each reference image for the part, and the language, it should guard before committing it, and compare pixels, not bytes. Name a new test after the file it guards (`affected-tests` maps `SeriesScreen.swift` to `SeriesScreenTests.swift`). (#893, #910)
- Verify each new test fails without its fix (origin/main's sources on a WIP commit); a security alert is fixed only once a test fails on main for it. Latch tests that hold a read's answer reproduce read/write races every time. A test of running without an optional dependency hides that dependency itself; `uv sync --all-extras` locally finds the ones that do not. Pair every "is refused" or "does not build" test with a positive control that passes once the missing thing is added (a refusal test called a function that does not exist; another's import sat in a reactor's never-called `main`). Check a timing test by putting the old behaviour back. A test double answers the real API's shape for every return value, even unread ones (the sqlite shim's `batch()` was not D1's). (#679, #613, #667, #734, #811, #848, #851, #886)
- Jobs run only when their code changes; run the same check on every PR that the release tag runs. (#528, #519)
- Tests get a server's ports from the server once open; tests bind 127.0.0.1. (#571, #451, #127)
- A fake server checks the method and path first, answers 404 to anything else and counts nothing before that check: on a shared runner a recycled port gets stray probes. It never indexes a table with request data (CodeQL flags it); compare against each route in a list. Node's fetch drops a custom `Host` header, so a fake standing in for several hosts needs one server or base URL per host. (#864, #867)
- Test a new process entry point by importing it first in a fresh interpreter, and rehearse a release as installed (its wheel in its own environment): the suites import the graph first, so a first-party import cycle that crashed every runner since 0.3.93 never formed in a test. Judge where each job ran from its own record, not the stage's setting. Give a sandbox test the path the caller gives, unresolved. (#833, #842, #843)
- New Postgres tests in `api` take their database from `clustertest.ScratchDB` (a copy of a migrated template); tests in `cmd/maktaba-server` get maktaba-api from `pgtest.APIBinary` or `pgtest.Migrator`. A test needing its own Postgres gets a throwaway container on a free port, removed by its ID. (#858, #824)
- Test SQLite runs with `synchronous=OFF` and a memory journal, set in the DSN. Scratch Postgres uses `initdb -E UTF8` and `-k ''`. (#521, #540, #467, #543, d20261031-tooling-local-gotchas)
- Tests keep the app's log, and a missed wait says what the app asked and what the screen showed. (#508, #539)
- Check client fixtures against the Go handler types with unknown fields refused. (#464, #467)
- Keep state a leader acts on in the database, and decide from the database at the moment of the write. (#659, #680)
- Signal another process through the desired-state revision it already watches. (#663)
- Keep `coverage-map.yml` cache-free and on its own commit; only main saves Actions caches. (#516, #618)
- Apple: throwaway simulators per run, deleted after; `@State` initializers have no side effects; closures for system-framework callbacks are made in a `nonisolated static func`; UI tests scroll with press-drag-hold. (d20261031-standing-rules, #454, #539, #512, #576)
- Ship Arabic with English on every platform in the same PR; «الجهاز الرئيسي» for the home. (d20261031-standing-rules, #454)
- Put fault scenarios in the rehearsal matrix that already builds the stack. (#685)
- Run the whole-stack test before pushing: the containerised cloud rehearsal (real containers, a health-checked edge, a database that vanishes rather than refuses) found five relay bugs and the whole-server plugin e2e three wiring bugs that every unit test missed. (#705, #707, #715)
- Run `ci_paths.py guard` after `git add` (it sees only tracked files); a new top-level tools folder needs a filter or a `NOT_IN_CI` entry. A new path filter touches `ci-paths.yml`, `ci.yml`'s outputs and unit call, `_unit.yml`'s input and `test_ci_paths.py`'s expectations. (#700, #704, #690, #739)
- Before adding a catalog feature, grep the last feature's id across `apps/` and `web/`: the components snapshot test, `tools/ci/catalog_words.mjs` (off and unavailable sentences on every platform) and MaktabaKit all count features. (#742)
- Run `make compat-prev` and the full pipeline suite before pushing a migration, never while the same worktree is being merged. (#750)
- Before blaming a timing gate on noise, make it print both sides' spans and CPU on every run and find a failing sample. Before pushing a Phase 4 wave run the replica, updates and unit suites (`go test ./...` in cmd/maktaba-server and api), and `git grep "engine/graph.py"` outside the pipeline for tests reading the graph's source; point them at the manifests (`web/src/test/firstPartyStages.ts`). (#760, #786)
- Test lint scripts with `/bin/bash` on the Mac as well as in CI; scripts that loop over lists of arguments run in bash (zsh does not split words). (#785, #794)
- A test of waiting proves it waits: make the awaited work slower than the old window or fixed sleep, so the test fails every time without the fix. A test that signals a child waits for the child to say its handler is set (`ready` after the `trap`), never a fixed time. (#792, #793)
- Run the affected-tests gate locally before every push, follow-up commits included (commit, then `cd tools/affected-tests && go run . gate -C ../.. --range origin/main...HEAD`); a new test file is in no map yet and links to its source only by name, and a test of a module's change goes in a file that imports the module at the top; a router file's hook needs a router test of its own; the gate maps a source to the tests main's coverage links to it, not to imports, so put a module's unit test in its own test file. (#791, #795, #833, #865, d20261107-test-infra-missteps, #948, #956, #962)
- Every environment variable a worker writes under gets an autouse test fixture pointing it at a temporary folder (a default relative to the data folder lands in the checkout). Never write a literal `/tmp` in a test (Windows has none): use `tmp_path`, or `tempfile`'s default folder where a unix socket path must stay short. (#821)
- Run CI's own checks for what changed before pushing, not a subset: `gofmt -l` and `golangci-lint run ./...` in each touched Go module (golangci-lint passing does not mean `make lint-go` passes); `make lint-py` for the pipeline, whose mypy covers `tests/` too (the 3 `stt/cohere_cuda.py` errors are expected without the ML extras); `npm run typecheck` (all three configs) and `npm test` in `store/`; `pytest tools/ci/tests` for any change under `tools/ci`, `tools/dev` or `.github`, and new workflow shell against the repo's lint rules (no `| head`, no `grep -q` pipes). Before pushing a catalog change, run the tests of every module whose `go.mod` requires `shared/components`. Commit before `make catalog-check`, or it reports the change as drift. (#825, #845, #858, #861, #883, #886, d20261107-relay-test-failed-mypy, d20261107-test-infra-missteps)
- Apple UI screenshots have no goldens: a layout change there needs a test asserting what is absent. A test of a committed placeholder (an empty plugin root) uses its own copy of the placeholder's form. (#813, #819)
- Before fixing a wall-clock test that fails near the 33-bit PTS wrap, check whether the product is wrong: shift the station's clock with `ptsShiftTicks` to reproduce on demand. (#797)
- Before each first-party wave, list every `maktaba_pipeline` module the moved code imports, at any depth, that runs SQL (`grep -n "from maktaba_pipeline" stages/*.py`), before writing `statements.toml`. (#789)
- Parity proofs (waves 5-8): give both sides the same ids (replace the `uuid` module's name with one whose `uuid4` is `SameIds`; an id from a column default needs the same sequence-backed default seeded in each test database); seed rules on for every stage that has them (a stage off by default passes without reading its rules, and a section read deep in shared code shows up only when the proof runs that path; an empty `plugin_scope` refusal is the settings check); mask a placed file's mtime, the stages whose input hash reads it, and ffmpeg's `@ 0x…` pointers; freeze `now` in each module that imported it by name; pass the plugin's new `group=` after a move, or everything falls back; every plugin test measures N16 (`compare_n16`). Rebuild the bundle (`tools/plugins/bundle.py --write`) after every `statements_build.py sync`. (#800, #806, #823)
- A sandbox's `exec` must name the program a stage really starts (yt-dlp runs as `MAKTABA_YTDLP` or `python -m yt_dlp`); parity fakes never exec, and a dev machine with Homebrew's tool on PATH hides the gap that CI caught. Capture the exact requests a client sends and use them as an allow-list's test cases. (#806, #844)
- `migration-lint` refuses a slot gap not listed as permanent: a PR taking a slot after two open PRs' slots is red until they land. (#800)
- Run a test harness's own tests with the URL forms dev machines use (with and without a query), not only CI's. (#769)
- Check a guide written before its code against the code (the WIT, `capabilityOf`), not the spec. (#704)
- A PR that ships user-facing work updates maktaba.dev in the same PR, in English and Arabic (`devportal_follows.py` warns; `No-Portal-Change:` says it needs nothing); run `npm run build`, `npm run scan` and `npm test` in `web/packages/devportal` before pushing. (#703, #723)
- Before changing a log format or adding a fixed loopback port, grep the e2e tests: they match `key=value` output and template `server.toml` for two machines on one host. (#673, #686)
- Calibrate a performance budget with work like the work budgeted; a "share of a core" budget is CPU time, not wall time. A timing assertion times only the thing it is about (the runaway-job test counted guest compile time). On a loaded Mac hold the median and log the tail: a p95 assertion is a flake source, and local absolute timings are unusable (profile call counts still are). Measure a calibration constant and report its raw times: a clamp at 1 hid a reference 3.6 times off. Measure with `timeit` before putting a regex in a hot path. A gate on a shared runner needs a counter and a robust statistic: the process's own CPU time (the relay containers' cgroup `cpu.stat` `usage_usec`) as a median of rounds against a baseline measured the same way, and a latency budget judged on the best of three rounds (noise only adds time); `docker stats` samples moved 1.8× on the same code. Before changing a threshold or rerunning, read the gate's spread on unchanged code from main's past artifacts, and watch a gate over several runs on several runners before making it strict. A gate compares on one machine or not at all: build the base commit and measure it beside the change in the same job, rounds alternating (`load-small`, the Python `log-bench`). Before changing a bench gate, sample it across many runners grouped by CPU model (a matrix recording `/proc/cpuinfo`, about 10 minutes); calibrate against the library the code uses, never the code under test; scale a percentile budget by the same percentile of independent work; and dispatch the nightly on the branch to measure a fix on the real runner. (#690, #738, #694, #696, #742, #740, #802, #808, #828, #834, #838, #839, #860, #866, d20261107-test-infra-missteps)
- Measure a flaky suite under real load before and after (three concurrent full copies on the Mac reproduce CI's web failures every time) and report both counts; profile a slow test (`--pool=forks --execArgv=--cpu-prof`): most of its time was page code. (#746)
- Web: window key handlers that read render state use `useWindowKeydown`; formatters made in render come from `lib/intl`. (#746, #698)
- Write a cross-language check once as an algorithm with a deterministic output, and share test vectors across Go, Rust and TypeScript (Ed25519 is deterministic, so a fixed seed suffices). Write the algorithm down to its tie-breaks first, generate the corpus with a script, write the goldens from one language, review them by hand and hold the other languages to them. Run every hostile case through the code a home runs (the loop), not only the verifier. (#751, #733, #743, #759, #778)
- Chain a workflow that must agree with CI on a commit after CI (`workflow_run` on success) rather than running both beside each other on main. (#737)
- After editing a workflow, load every workflow with PyYAML (`tools/ci/tests`): actionlint passes a step `name:` containing ": ". (#727)
- A rehearsal check reads the previous release's state before the update instead of keeping a hand list or comparing versions, which break each time the previous release moves. (#748)
- Run one real UI test for each new native page, not only a compile; run `:player:testDebugUnitTest :player:verifyRoborazziDebug` before pushing an Android layout change (only `verifyRoborazziDebug` compares screenshots). Judge a build by its exit status. (#787)
- Before raising a limit a test hits, measure which phase uses the time; when a product limit fires only in a debug build, fix the build (`[profile.dev.package."*"] opt-level = 2` made the Python hello compile in 1.8 s instead of 15), not the limit. (#848)
- Look at every recorded screenshot reference: the first Roborazzi shots showed overflowing chips and a clipped tab row that no assertion caught. (#745)
- When an earlier PR says something is reachable, check that something actually serves it: the payment registry and webhook had tests and no route. When a frame or payload gains a field, check a store keeps it and a test reads it back; walk a protocol from both ends before shipping one end. Read an earlier PR's user-facing strings when a later one changes the flow. On an RTL page check the Arabic at phone width before pushing. (#739, #845, #850, #861, #897)
- Prove a lint rule or check fires before relying on it: run it on the real case that caused it, or a throwaway violation. (#871, #874)
- Join a library's worker thread when closing a resource whose end the process counts (aiosqlite's `close()` returns just before its thread ends). (#868)
- Read a runtime's start-up code before choosing a stub for it, and probe a platform on a real runner (a throwaway branch whose workflow runs only on its own pushes) before choosing a cross-process transport. (#851, #863)
- Before blaming a change for a local failure, run the same test on a clean origin/main checkout (a full disk failed restore tests on both). (#856)
- Before cutting what a log carries by default, search the tests and harnesses that read it (`log.String()` in the e2e tests, every harness that writes a `server.toml`); harnesses log every line (`[logs] server_log = "all"`), or a check for an absent line passes for nothing. Run the affected-tests gate again after the last file joins a commit. (d20261107-server-log-warn-e2e)
- A test of a mechanism does not also check speed: a p99 of a few thousand calls on a shared runner cannot be bounded, even against the same code; speed belongs in the bench's own verdict on one runner. (#904)
- Before a dashboard lists a series, check something exports it, and test the board from a scrape of the real exporter's names. Search a module's tests for an exact URL before changing what it carries. (#896)
- For each security rule in a Caddyfile, check the adapted route order, not just `caddy validate`: every Caddyfile with a deny rule gets a `tests/deny-first.sh <json> <host> <status>` call in the CI step that validates it, and the static lint over all of them keeps the files that broke as fixtures. When a bug's cause is a pattern, search every config of the same kind and add a repo-wide check. Split a security fix found inside a bigger PR into its own small PR from fresh main, with a test that fails on the old file. (#927, #930, #932)
- When a test asserts who a service saw, echo what each hop saw (a response header from the stand-in edge), and put the addresses a test must tell apart in different /24s whenever logs are redacted. (#922)
- Prove a new check against the code it should catch (a temporary undo on the PR, reverted before the merge) and record the failing run; read each recorded text reference as each image is read. Prove a router split by walking both routers (`chi.Walk`), not with a hand-written list. (#929, #928)
- After a wave, run every first-party parity test, not only the moved stage's, and mypy over the whole pipeline before pushing a wave branch. A parity that passes in CI and fails locally on a time field is a race on the clock. (#934, #920)
- A shell test that runs a script which backgrounds a loop stops that loop itself (a trap that only deletes the temp dir leaves it running against the real tools), including the loop a red-before-green run of the old script starts. After any local run of `deploy/cloud-ha/tests` or a rehearsal, check `pgrep -fl on-role-change` and kill leftovers. (#939)
- Ask for an independent review of every relay and scope change: the writer of a check tests the inputs it expects. (#937)
- Bound every wait in an async test (`asyncio.timeout` around drains and gathers); pytest's `faulthandler_timeout = 120` prints a hung test's stacks. A cancelled `-q` run still names its stuck test: line the progress characters up with `pytest --co -q`, the `s` positions confirming the alignment. (#945)
- Read the home log in the rehearsal's `rehearsal-logs-*` artifact: the supervisor's "wanted: starting it" line and the jobs' `job_dispatched` times show a start race to the millisecond. Make a rehearsal home look real through the routes the app uses (`PUT /api/extensions/plex`), never direct table writes: the route also sets state no one remembers, such as the component flag. (#953, #938)
- A leak test uses random tokens and checks for leaks before coverage; a refusal test checks the reason as well as the error (a case missing other required fields passes on the old code for the wrong reason). Parity fixtures need a real fake behind every upstream address the door may call. (#942, #948)
- A reference test (snapshot, text dump, screenshot) that draws anything read off the clock fails the day after its references are made: pin the clock (`MockHome.clock`), at noon so the date is the same in every zone, and use no future fixture times on the pinned pages. Bootstrap Apple references from a CI run that fails on purpose ("no reference") and keep only the new images `tools/apple/snapshots.py` copies. (#936, d20261108-snapshot-fixture-clock)
- Key a start gate on the dependency a child runs from, not on one role, and test that every `python -m maktaba_*` child is gated (`TestEveryPythonChildRunsFromTheVenv`). (d20261108-runners-wait-venv)
- Read a failed rehearsal row's test output before treating it as a fault: `no tests to run` means the matrix and the checked-out code disagree. (d20261108-rehearsal-matrix-skew)
- A flake seen twice is fixed in the test, not rerun a third time: compare only what the reference is about (the page's words), not layout state such as scroll position. (#963)
- Before pushing a change that makes a hot path costlier, run the touched package's tests with `-race -short` against the test-time budget; it can break unrelated pull requests. Size a new test's fixture to what it asserts. (#971, #970)
- When a fix adds a matrix job, wait for that job by name (`need-check.sh`) before the merge watcher, or the watcher can merge before the job appears. (#961)
- Fetch every pinned GitHub release asset through `tools/release/fetch-pinned.sh`, with the job's `GITHUB_TOKEN` passed to the step. When a download fails, try the same URL from a workstation and by `gh api`: that tells a front-door outage from a bad pin. A tool whose installer downloads for itself gets its pinned copy placed where the installer skips the download (a Gradle wrapper's dist folder, `compose-jb/wix311.zip`). Save a download cache after a failed run too, under a key the exact match never hits. (#1003, #986, #1014)
- Check, in each app's own tests, any value both the apps and the cloud derive, and hold the same value in a Go test. (#944)
- Split a production fix out of a test PR the moment the test finds it, and merge it fail-closed on its own. (#988)
- When an unrelated PR's check fails, find the cause and fix it in its own PR with a test. A flaky test asserting a status is often a real ordering bug: slow one side at the gap to reproduce it. (#1002)
- Read the failure's hierarchy before calling a UI failure flaky: an xcresult keeps it (`xcresulttool export attachments --test-id`) and it tells a wrong screen from a slow one. A temporary commit with `-test-iterations N` proves a UI fix in one CI job; raise the job's `timeout-minutes` with it. (#1000)
- Profile before trimming a slow test, phase by phase: the time is often in shared fixtures or a world's fixed cost, which a split repeats in every test. Build tools and clone databases in a test's setup, since the soft cap judges only the call. (#996, #992, #1068)
- When two PRs touch the same test files, merge main into the second after the first lands and run the combined suite: each PR's green checks prove only its own half. (#1068)
- A warm-up's "warm" is when the measure stops improving, not a fixed time that holds on one device: check a threshold on every device in the PR's own run. (#1001)
- Check a dependency map before removing a feature. A removal is two releases: first the code goes and ignores the leftovers (a lossless migration, so a rollback runs the Down scripts), then a later migration deletes the data once no release that uses it can be rolled back to. (d20261108-remove-plex-sync)
- Write down a product direction that rules features out: Maktaba replaces Plex, and only Plex Live TV stays. Unwritten, features against it were built, moved and hardened across several epics. (d20261108-remove-plex-sync)
- Prove a step behind a secret's gate before the secret exists by building its inputs locally and running its own script, in bash: a zsh array starts at 1. (d20261108-android-native-symbols-path)
- When a guard's description changes mid-review, restate the final scope everywhere it was reported, not only in the PR body. (#990)
- Run a new guard against open wave branches before they merge: on one such branch it found 6 unlisted tables and 8 bare-string `[scope]` overrides the analysis now derives. Prove every declared write check refuses and allows by walking the embedded bundle in a Go test, with one seeded row per kind in two libraries. (#1005)
- Check every downloaded archive against its pin, whichever place sent it. Start a merge watcher from inside the repository, a couple of minutes after the push: started at once it reads the previous head's checks and stops on their failures, and from outside the repository it cannot read checks at all. (#1007)
- Lift a sandbox guard one stage at a time, each with an override-off end to end whose verdict fails on any fallback, and a negative control reverted in a commit of its own: a guard hides its path's bugs from production. (#986, #1018, #1019)
- Give every identity a sandbox boundary relies on a startup self-check, so a refusal shows at start rather than as each job's fallback. (#1018)
- Re-check main's newest migration just before the push and again before the merge, and take the next slot then, not when the branch starts. Merge main right before dispatching a rehearsal: a long branch waiting on a 30-minute rehearsal has fallen behind again by the time it is green. (#998)
- Run first-party plugin tests as their own pytest run (`pytest ../plugins/first-party`), and time them together with `tests/plugins/firstparty` as CI does: alone they load no pipeline pytest config, so the soft-cap hook never judges them. After moving core stages into plugins, run `cmd/maktaba-server`'s tests and check Core's footprint: every check that asks whether a stage is a plugin's changes, and always-on work moved into an on-demand process changes when that process runs. (#998)
- Treat each automated security-review finding on a resolver as a lead, and check it against the schema (foreign keys, check constraints) before keeping or dropping a branch. (#1025)
- When a gate fails on a pull request, check main's latest rehearsal run before debugging the branch, and say main's rehearsal is red on the thread that owns the release before opening a fix: two sessions opened the same fix within a minute. A removal updates the rehearsal's expected components in the same pull request, checking the retired list rather than naming ids. (#1032)
- Pin by tagged release with SHA-256 where the upstream has one, keep `continuous` with a warning only where it has none, hand a tool its inputs as files, and read a bundle step's verbose log once for downloads before calling it pinned. (#1034)
- Before trimming a cache, measure each family from the listing and the jobs' own save lines, count every profile and matrix leg, and price each cut by the job's uncached time on main. One writer per cache entry on main; every other job restores only. (#1036, d20261108-ci-cache-eviction-linuxdeploy)
- Reset a spec worktree to `origin/main` before writing: one started 300+ commits behind. (#1038)
- Make a scenario's coverage an assertion, and give the seed the subjects in the scenario's own setup rather than changing the shared seed: a scenario that skips a stage with no subject passes without it. A row that logs a skipped stage and passes reports coverage it does not have: put the check in every row, not only the one where the gap was noticed. (#1041, #1053)
- A path keyed only by the checkout is shared by every session in that checkout (xdist workers, two runs, CI jobs on one runner). Give each session its own path, seeded from a shared binary swapped in by one atomic rename, which keeps the build cache. (#1049)
- `affected-tests` maps a Go file to Go tests, and a Python end-to-end test does not count: a new Go flag needs its own Go test. (#1049)
- A no-payload log test puts a marker string in the payload and looks for it in the log. A child session that cannot wait on CI says so and hands the merge back. (#1048)
- For a speed-up that must keep its outputs, keep the old algorithm in the tests next to a fixture of its answers, and guard it with counted work and a CPU-time ratio measured in one process, not a wall-clock limit. (#1050)
- Fake host resources (disk room, free ports) in `TestMain` and give the real read one test of its own. A test that fails on one machine only is first a question of what that machine has that the runner lacks; reproduce it anywhere by patching the one host reading. (#1047)
- When a suite runs code through an indirection (a manifest `entry`, a Go test table), derive its path filter from the imports and test that derivation; only direct imports make a useful filter. (#1057)
- Before changing a timing gate, judge the old and new rule offline on stored CI samples and say how many runs each would have failed. (#1059)
- Give every artifact a test writes a name unique to that test, and make the writer refuse a name already taken. Before hunting shared state behind an order-dependent number, check that the number comes from the test you think. (#1066)
- When moving code across a process boundary, list the module-level state it reads and pin each with a test that leaves the global empty, plus a guard against importing it. (#1062)
- Read each failing job's log before touching code: four of six failures on one PR had different causes. (#1027)
- Revoke sessions, not just the credential, when a credential looks cloned. (#1067)

## Stop doing

- Re-running a failed CI job to green, or skipping a flaky test, instead of finding its cause. (#525, d20261031-standing-rules)
- Raising a test tier's cap. (#271, #635)
- Stacking fixes on the live install in one day. (#433)
- Writing operator columns on the live database with raw SQL. (#524)
- Installing or starting Maktaba from Claude's shell on the PC (MSIX private registry); touching the dizqueTV task; changing security settings from Claude. (d20261031-pc-maktaba-link-msix, d20261025-pc-windows-app-node, d20261014-pc-dizquetv-origin-and-rules)
- Bare `git stash`: the stash is shared across worktrees and swapped two sessions' work. (d20261014-pc-dizquetv-origin-and-rules)
- Ending a shell command with a cleanup `git checkout <ref> -- <file>` (or `-- .`) in a worktree with uncommitted edits: it threw edits away, once a whole change. Commit WIP early. (#698, #730)
- Switching branches in a worktree while a background test run reads it; use a separate worktree. (#717)
- Committing build output. (#127)
- `launchctl kickstart -k` on the live server (TERM the launcher); a second `Maktaba.app` in /Applications. (d20261031-tooling-local-gotchas)
- Running Mac or simulator UI tests locally, or rebooting the shared simulator. (d20261031-standing-rules)
- Writing a measured figure into a commit message before reading it from a run. (#666)
- Appending a slow PR's CHANGELOG entry at the end of Unreleased "Added", where other sessions append: it conflicted after every CI round; put it at the top of the section. (#782)
- Picking a port without reading the port registry. (#686, #440)
- Chaining `go test … | tail && git commit`: the pipeline's status is tail's, so a failing test still committed. (#705)
- `git add -A` after a tool that writes into the checkout: `make compat-prev` writes `api/internal/compatprobe/main.go` for the run, and `uv run` writes a root `uv.lock` that then fails ci-paths in the lint job; commit only named paths. (#707, #1007)
- `git reset --soft origin/main` to squash after a fetch: a moved origin/main silently reverted three merged PRs. Reset to the branch's own base sha. (#701)
- Naming a stale host in backticks in prose (CHANGELOG, docs): the foreign-domain lint flags it; write the name out of the text. (#700)
- Cleaning up background load with `kill $(jobs -p)` (each agent command runs in a new shell, so the job table is empty) or hiding a cleanup's errors with `2>/dev/null`: sixteen orphaned `yes` loops held the Mac for three days and WindowServer crashed, ending the login session and every session's watchers. (d20261107-orphaned-cpu-load)
- Loosening or fixing a failing check on main inside an unrelated PR: look for the PR that fixes it (a lowered floor would have hidden moved stages with no titles). (#787, d20261107-paid-plugins-merge-problems)
- Filtering Gradle output through `grep "^e:"` with `-q` to judge a compile: it hid a failed build. (#787)
- Letting a watcher merge on a read that can fail open: its `grep -P` errored on macOS's BSD grep, matched nothing, and four PRs merged with their checks still running; `|| true` around the read did the same. (d20261107-watcher-merged-early, #810, d20261107-paid-plugins-merge-problems)
- Importing at the top of a module another module that imports it back while loading, when either can be a process's first import. (#833)
- Shipping a provider-neutral library without the one route that makes it reachable. (#850)
- Telling users about behaviour the next PR in the same series is about to change. (#861)
- Listing reference files one by one in a generated project (xcodegen): a reference added later is left out of the test bundle; a folder reference cannot leave one out. (#910)
- Putting a registration step in front of `launchctl kickstart` with `?`: a refused registration then blocks starting a job that would have run. (d20261108-login-item-relaunch)
- Reporting a fix as the production protection before an end to end on Windows has run it; a sandbox probe does not exercise the stages' own code paths. (#990, #976)
- Piping `watch-merge2.sh` into `tail`: it hides the exit code, so read its last output line instead. (#983)
- Bringing main in with a plain `git merge origin/main`, even when no conflict is expected: use `tools/dev/merge-main.sh`. (#1025)
- A merge watcher that requires a check by its exact name: CI's names change between runs (`ci / plugins / first-party` and `plugins / first-party`); match without the workflow prefix. (#998)
- Killing a helper process by name (`pkill -n`) after a local regression run: on a shared machine it can hit another session's dev server. Kill only PIDs the run recorded. (#1066)

## What we learned

**Architecture decisions**
- Everything is a queue job; machines pull from queues on the home, no central scheduler. The cluster channel is a sealed WebSocket; the DB is only a cache. (#47, #56, #58)
- Windows joins the Mac's home as a node; paths are canonical `smb://` share paths. (d20261019-pc-windows-joins-the-mac, #81)
- Full nodes keep a live logical-replication copy of the home's DB (physical cannot cross arm64 to x86 and would carry secrets); only `pgoutput` ships. A node newer than its home's schema pauses. (#178, d20261028-pc-update-orders-stuck)
- Plex sees Maktaba as an emulated HDHomeRun; auto-detection was never solved, Add to Plex adds it as a channel source. (#61, #139)
- Transcription: Whisper turbo plus a Cohere second listen, now its own stage; a human listening test beat an LLM judge. GPU fit is decided from the driver's measured free memory. (#113, #611, #585)
- Updates: machines follow only after the home commits; backups never block an update; TV changes never block updates and a failed TV preview keeps the release; no click in the desktop app. (#434, #397, #429, #404, #389)
- Product shape: native Player, Remote, Server and TV apps (SwiftUI, Compose), none a web wrapper; lists open recently added first. (#305, #306, #375)
- Epic 51 components: one catalog, desired state in Postgres, one gate rule; an asset is on unless `absent`; a dormant process of an off component is not a role that should run. (#608, #635)
- Search moved from Chroma to pgvector (Chroma broke on killed writers). (#319)
- Plugins on joined machines: a manifest's `placement` (`home`, `any`, `gpu`) says where stages run; a machine fetches packages from the home and checks digest, names and signatures before writing anything; it serves reads and media itself and sends every write, quota, lineage and compute call to the home's API, so `plugin_tables_guard` stays and off, hold and revocation reach it at its next call. (#767)
- An admin order kept as a list of ids, the rest after it in install order, needs no row per plugin and no migration, and keeps a plugin's place through a reinstall (`plugins.panel_order`). (#783)
- Plugin job metrics come from the breaker's `plugin_health` rows read at each scrape (job time is a gauge per quantile over the last 20, not a histogram); only the home registers the collector, or every joined machine exports the same rows again. A new plugin admin route goes into `TestEveryPluginRouteIsAdminOnly`'s list. (#801)
- The home's catalog loop (`api:plugins_catalog`) reads the revocation head every 5 minutes and the list and catalog when they change, from the store's CDN, each verified by the chain of trust, the newest seen winning. A store `disable` holds the plugin within one poll and refuses start, enable, install, update and rollback (`409 plugin_revoked`). Offline the kept lists age into a warning that stops no plugin. The trust state keeps each catalog's sha256, so a catalog repeating the newest version with other bytes is refused. (#778)
- Store flows are one upload of their members, refused, reviewed or approved as a whole; what a flow records is its own members, events and subscriptions, not the upstream plugins the checker pulled in. (#770)
- A plugin's state comes from the home: the supervisor's plugins runner writes the gate's decision (waiting with its reason, stopped while held, off, running) into its own status part, and `store.Rollup` (worst first) gives the precedence with no new merge code. (#814, #818)
- Direct remote access (50.15): an authoritative name server answers CAA itself (CAs ask the delegated servers) and the `<home id>.<domain>` level must exist (NODATA, not NXDOMAIN); `x/net/dns/dnsmessage` has no CAA type, so it goes out as an `UnknownResource` of type 257. `stores` imports `direct`, so code needing both lives in `internal/directjobs` and `relay.DBDirect`. The cloud's flat TOML reader passes a list as `a", "b` (`parseList` reads both); EAB HMACs come from the environment only. (#808)
- A catalog top-level key (`feature_any`) goes in five places: `topKeys`, `rawCatalog`, the plugin merge's `Catalog{}` copy, the Python loader's `_TOP_KEYS` and `with_entries`, and both dumps. (#825)
- The store's isolated checks run in a Cloudflare Container per submission from a `CheckRunner` Durable Object (`ctx.container`, `enableInternet: false`, talked to through `getTcpPort`); large inputs stream from storage over the port (RPC arguments are size-limited). Miniflare runs no containers: test with a stand-in namespace and the server as a real process. The `containers` wrangler block is generated at deploy time behind `STORE_RUNNER`, or every deploy would need Containers. A forged `Content-Length` test uses `node:http`; `fetch` refuses it. In the store's jsdom project Node's own `localStorage` shadows jsdom's (stub one), and a module the console's tests import must not import the Worker's types. A worktree for a second store PR needs `npm ci` and a built `store.wasm` (copy it). (#820, #836, #887)
- Paid plugins (54.19): only the supervisor holds the verified store keys, so the API fetches signed entitlements daily and the supervisor checks them over loopback; gates read only `valid_until`. A priced plugin's packages sit in the private bucket and reach a home only through a 10-minute HMAC URL on the CDN's own host (`plugins-cdn.maktaba.tv/paid/*`, a zone route served by `store-web`), while it holds an entitlement; the store's `holds()` reuses the entitlement route's rules so download and entitlement never disagree. A purchase intent is signed by the linked home and the store site's Buy makes the same one. A move's limit (2 per 365 days) sits in the D1 batch's own `INSERT … SELECT`, since a read-then-write lets two moves pass; the old home drops the plugin at its next refresh. When two phases build one format, check the other's constants and secret names first. (#781, #850, #861, #886, #887)
- Browser flows: an "open a tab, then post" flow opens the tab on the click (`about:blank`, opener cut, form posted by name), or it is a blocked pop-up. A `next` check resolves `new URL(next, origin)`, compares origins, then allow-lists paths (test backslash, encoded slash, `javascript:`). A `default-src 'none'` page runs one fixed inline script by its sha256, data in attributes. `ports.Outbound` checks hosts only, so each new kind of call is listed by name. (#836, #850)
- A new field in a signed list goes where old readers keep it unchecked (the catalog entry's raw `extra`), not as a top-level key strict decoders refuse; old Apple and Android clients drop connection kinds they do not know. (#835, #897)
- A home reaches the cloud as itself only through its relay agent's loopback route (only the agent holds the server's secret). Anything leaving the home reuses the problem report's scrub, filtered again after an index answers; data a third party may send is ignored unless it came over the signed path. (#824, #837)
- Relay plans (50.19): homes refuse an unknown license tier, so a rename is two releases (`basic` goes out as `home` until homes alias it, after the signature check, stored normalised). A stream is its session id in the path, ended by silence; a per-home advisory lock around count and insert closes the last-slot race; pace per stream, not per response; strip the cap header before setting it, and filter any "from the relay" header (`X-Maktaba-Via`) on every proxy, `/stream/` included. Put a per-home number the relay needs within a second on the row it already caches and extend that row's notify trigger. Clamp billing at the report, not the meter, with an idempotency key per home and hour. A rehearsal of paid plans needs a build tag that fills the TBD numbers, and one account per pay-as-you-go setting. (#832, #849, #854, #856, #865)
- Direct access (50.15): the direct listener marks requests `X-Maktaba-Via: direct` and drops client forwarding headers, or the internet passes as a local caller; seed `cloud.direct` from the relay fact (a bundle's requirements turn on with it; at runtime nothing turns a requirement on); a new rules section needs English and Arabic words and a help entry; on Ubuntu systemd-resolved's stub holds `:53`. (#845, #877)
- Plugin runners start on demand: the worker runs a job itself while the runner is down and asks the supervisor to start it; a runner exits after ten idle minutes (the health check is not activity), a group is held while none of its components is on, and the supervisor reports each process's start mode, which rehearsal scenario 12 reads rather than special-casing a version. Core's four-process budget is a product promise. The worker's in-process fallback hides a crashed runner: jobs end done and only a log line says so. An on-demand runner is always down right after an update, so the worker waits up to 30 s for a runner it asked for (not without a supervisor, after a refused ask, or for a failed group); `impl = 'plugin'` is written at graph sync and means only that a job may go to the runner. A failed process gets three slow retries, then stays down; a home reports it to admins only (System health, an admin-only `plugin_runners` group in `/status.json`, which is public and drives every viewer banner, so it never touches `overall`). A stage of an extension runs only while its component is on; seeding its row does not turn it on. (#833, #842, #859, #953, #962, #938)
- A first-party runner's file jobs are scoped to their subject by an audit hook installed by the `Runner` itself (the parity harness never runs `__main__`), with the refusal kept so a caught `OSError` still fails the job; a scope built from a table the plugin writes can be widened by it. (#890)
- Plugins on Windows: Python 3.12 there has no `socket.AF_UNIX` and an AppContainer process may not create a named pipe, so the supervisor serves a per-run pipe (protected DACL, first instance) and relays it to the host's AF_UNIX socket; the host opens its socket through Winsock before entering the AppContainer, both sockets in its own run folder (granting `<data>\run` would expose the supervisor's token). Go 1.24+ needs only `x/sys/windows` for an overlapped pipe; GitHub's `shell: bash` runs with `-e`. First-party runners run there too: a process cannot enter an AppContainer itself, so the supervisor starts each group through `maktaba-server runner-sandbox-run`, which grants the group's profile the policy's folders (paths resolved as the filesystem names them, protected folders and drive roots refused, stale grants revoked), and the runner exits 70 unless its token is an AppContainer's; the supervisor serves every pipe and a broker pairs worker and runner connections. Declare `argtypes` and `restype` for every ctypes call taking a handle: an undeclared argument is a C int and `GetCurrentProcess()`'s -1 overflows it. (#863, #879, #956)
- Developer mode's reload is an ordinary update (same version, new digest folder). A middleware wrapping a router cannot read what the router's own middleware puts on the context; test a log field with a value sent in. A queued job carries `origin_request_id`, and a coalesce keeps the first. (#810, #888)
- The cloud: `server.Run` owns each role's listener; `server.trusted_proxies` decides when `CF-Connecting-IP` is believed and the access log logs the resolved IP (it read the client-controlled leftmost `X-Forwarded-For`). A shared i18n provider takes its catalogs from the app that mounts it, and a source map carries comments, so keep admin names out of `web/cloud` comments. (#871, #882, #883)
- Plugin SDK builds: componentize-py (with `--stub-wasi`) and jco (`--disable all`) import every interface of their world, so `mkp world` narrows it from the audit's own tables; a componentize-py component needs 38 core instances (cap now 64; memories stay 4). Wasmtime checks a deadline only at epoch ticks (100 ms). TinyGo's wasip2 runtime always imports stdin and filesystem, so the Go template targets `wasm-unknown` (a reactor: `main` never runs, and it exports its own `cabi_realloc`). (#848, #851)
- A plugin's data version lives in `plugin_installs.data_version`, set while it is behind the manifest's; the plugin's other stages pause behind one home-only `__migrate` job (a reserved stage name passed to `run`, used only when the manifest says `migrate = true`). Rollback compares the data's own version, not manifests. (#799)
- The API's `/metrics` gathers `shared/metrics`' registry plus the default registry's `maktaba_*` families it lacks (a family in both fails the scrape); merged scrape targets keep the first target's copy of a shared series. A machine's numbers reach the API through the supervisor's `/metrics` by prefix (`supervisorMetricsPrefixes`); a reading the system cannot give is left out, never written as 0. The home's `/metrics` asks every joined machine's supervisor agent over the cluster channel (`MachineAgents` plus `CommandWithin`, NAT already handled; a timed command stays out of the hub's event log), side by side since `prometheus.Gatherers` asks sources one after another; another machine's metrics are untrusted: only `maktaba_component_*` and `maktaba_host_*`, repeated series dropped after relabelling, `ContinueOnError`. (#896, #905, #915)
- A folder scan is an engine stage: its new videos are its changed subjects and a declared `videos_changed` edge queues their probes (`videos_of` falls back to every unlinked video when a job names none, right for a listing, wrong for a scan). Each stage taken off `legacy` needs a seed migration (slot 0100 for transcribe, 0210 for scan): the graph sync rewrites the row only when a worker starts. Measure each parity allowance before keeping it; an old-versus-new proof drains both sides, the old graph being the new one with the stage set back to `legacy`. (#847)
- The cloud's config parser ignores unknown keys, so a secret under a plausible key would be dropped silently: such keys are refused by name. `MAKTABA_ENV` defaults to prod, so a production-only requirement (the Turnstile check) is off by default and turned on in the nodes' config; the go-live list names both the GitHub environment's secret and the binary's variable. `fetch` drops a custom `Host` header: test several hosts with a local server per host. (#873)- The admin routes have their own listener and name; the console's sign-in (`/v1/auth/*`, `/v1/account/me`) is served there too as `ConsoleSignIn` until 50.33's `/v1/staff/*`, and the admin Worker carries the same stopgap: a story shipped ahead of the one that replaces a stopgap carries it through every layer. Both Workers attach as Workers Routes, never Custom Domains. (#928, #941)
- A fail-open branch ("not found, so allow") is safe only when not found means no row: compare ids in the form the database compares them (a plugin could write another library's video by spelling its id differently). (#937)
- Wave 9's stages (`probe`, `thumbnail`, `extract`) are seed migrations, one slot each, and main can take a slot between them: renumber the whole stack against main's newest migration right before each merge. A stage that starts firing edges changes other plugins' parity worlds. (#919, #920, #921, #934)
- Wave 4 (#925): a named statement is one fixed text (a list argument is `= ANY($1::uuid[])` on Postgres, `IN (SELECT value FROM json_each($1))` on SQLite); a plugin reads only the `rules.<section>` names its manifest lists; a stage outside its plugin's own runner group takes only that group's network (`books` runs in `net` with `ar.wikipedia.org`). `enrich` stays in the worker: it reads any linked page and its keys are sealed in `home_secrets`. (#925)
- Doors (dizqueTV, since removed, #942, #948): a door holding the upstream's credentials builds request bodies on the home, reads facts from the upstream itself (the rating key alone, items read from Plex) and answers with projections, with a fail-closed leak check on top. A runner with no network can still reach out through any URL field the upstream fetches (ffmpeg inputs, guide pictures): each such field takes only the home's own hosts. A door's extension list defaults to all; give each door only what its callers need. CodeQL `go/request-forgery`: build the URL from the parsed base plus the checked name. (#942, #948)
- Decide what a response reveals from the authentication method actually used (`principal.SourceJWT`), never from headers a page script can set. Recovery calls go through their own client (a 401 there is never sign out), and a refused pass stops the polling, or the recovery lockout locks the admin out within a minute. (#918)
- Apple crash symbolication runs on Linux: Go's `debug/macho` and `debug/dwarf` read DWARF 5 dSYMs, and only a compact table per binary is uploaded (a few MB, not hundreds). The binary lint rejects Mach-O and ELF fixtures: keep them gzipped. (#917)
- The metrics store's key starts `name, labels`, so a time-range read of one name visits every label set (about 2 µs a row in pure-Go SQLite): sums belong in `Record`, which sees one scrape of every machine at once. One machine's label sets are found by skipping through the key by hand. (#960)
- The Overview's offline rule is the Machines page's (`clusterh.Handler.OfflineMachines`), not a stale sample: the store cannot tell an offline machine from a removed one. A caller's own `offline=` is always dropped. (#970)
- A forwarded crash report goes from the home to its relay agent (`maktaba-cloudlink`), which posts it to the cloud API with the home's secret; it does not travel through the relay tunnel. The console listener refuses a sign-in without both the console's `Origin` and `X-Maktaba-Staff: 1`. (#961)
- Symbol tables: Android releases have no binary UUID, so R8 tables are keyed by app and version; native tables by the first 16 bytes of the GNU build id, read only when the whole id matches; a stripped library makes no table, so a crash is never marked read with `.dynsym` names alone. Tombstone caller pcs are looked up as they come; Apple return addresses move back a byte first. The desktop installers run no ProGuard. (#961, #967)
- The home has no central access check: about 15 packages each scope by library, live channel streams and the HDHomeRun routes are unsigned, a channel with no library is visible to every signed-in user, and the browse row cache assumes rows have no per-user part. (#979)
- Epic 57's device side is Apple's Screen Time: the effective movie and TV rating limit is readable without Family Controls (iOS 15+, tvOS 26+), watching it needs the entitlement, which needs Apple's approval per extension, so the app re-reads on foreground. Declared Age Range is iOS, iPadOS and macOS 26+ only and a plain capability. (#979)

**CI and releases**
- Homes' `releaseverify` accepts only a release whose Sigstore certificate's trigger (OID 1.3.6.1.4.1.57264.1.20) is `push`; a dispatched release differs in that claim alone and is refused, and a re-run of a dispatched run stays `workflow_dispatch`. A tag pushed with `GITHUB_TOKEN` starts no run, so tags come from a session's push; v0.3.83 to v0.3.85 have no release, so v0.3.86 is the first published release of their components (`since` says 0.3.86). (#725, #730, #748)
- On main the update rehearsal is a `workflow_run` of CI's successful push run; a `workflow_run` run's `head_sha` is main's head when it started, not the commit it rehearsed. The rehearsed commit is in the run name `Update rehearsal of <sha>` (and its commit status); `tag-guard.sh` and `next_tag.py` read it there. Before this, two separate main concurrency groups finished on different commits and nothing was tagged for a day. (#737)
- A push with `GITHUB_TOKEN` starts no workflow, but `gh workflow run` with it does (`actions: write`). GitHub Pages sends `max-age=600`; fresher clients add a per-minute query. Probe data lives on a data branch (`status-data`), not main. (#739)
- CodeQL reads test code too (a `<script>`-stripping regexp is a high finding), and a flagged line in a PR's diff is fixed in that PR. It flags every int to uint16/uint32 conversion traced to a parse: parse with `strconv.ParseUint(s, 10, 16)` and convert in a bounds-checked helper. A new step in a job that saves caches from `workflow_run` raises the cache-poisoning alert again: dismiss each alert after checking its facts on the code, written in the dismissal. (#739, #845, d20261107-rehearsal-venv-codeql)
- CodeQL's models: its Go path query does not follow `CanonicalUnder`'s prefix check but recognises a `strings.Contains(x, "..")` refusal; clear-text logging matches variable names (credential file paths named `passwords`); a wrapper relaying a captured response must set its own non-HTML content type. Teach it with such changes before dismissing; dismissal comments are capped at 280 characters. (#734)
- A run green except for the ignored iPhone/iPad shards is not tagged; web-release.yml is not a tag workflow. (#699)
- Any `https://host` string in the servers' Go code (a CSP included) trips `TestOutboundHostsAreListed` in lint, unit and integration at once: a browser-only host goes in `notCalled` with its reason, a called one in `ports.Outbound` with its off switch. Gate a new outbound host once, in the one place that makes the request. A rehearsal failure about loops or components the PR never touched usually means main moved: merge origin/main first. (#752, #778)
- The Python logger's redaction (a case-insensitive regex alternation per value) and the ring's second JSON encoding were most of its cost; with plain `in` checks and the console line reused, CI went from p50 55 / p99 82 µs to 12.4 / 18.0 µs and its `log-bench` is strict. On the nightly Mac a p99 is 6-10 times its p50 for Go and Python alike, so there it stays a warning and both benches judge the best of three rounds; the nightly runs the Python bench even when a Go bench fails. No calibration loop follows the logger to better than about 15% across CI's CPUs (the spread is between CPU types; a run's rounds agree within 2–3%), so the strict Python gate fails a change only when it is over budget and over 15% slower than its base on the same runner; `load-small`'s cgroup figure moved 0.000451 to 0.000875 CPU-seconds per Mbit across runners for the same code. In CPython `any()` over a generator costs twice a plain loop. An `lru_cache` on a function taking a large frozen dataclass hashes it on every call. (#740, #802, #834, #838, #839, #860, #866)
- CI builds the Desktop Player on x64 only, so arm-only native-library gaps show first at release; reproduce with `docker --platform linux/arm64 ubuntu:22.04` and `ldd -r`. (#725)
- A public repository cannot use an Action from a private one; GitHub shows a workflow that does not parse by its path and starts no run. (#702)
- With one concurrency group GitHub keeps one running and one pending run; cancel-in-progress on busy main starved tagging, so it is PR-only. (#547, #563)
- A job that `needs` a skipped job is skipped unless it says `!cancelled()`. (#528)
- Under `pipefail` a reader that exits early (`head`, `grep -q`) can SIGPIPE the writer; capture first (`out="$(cmd)"; grep -q x <<<"$out"`). `go test | tail` hides a failing exit. (#658, #675, #166, #271, #684)
- A heavy import for a small helper pulls a module graph into every run; keep helpers stdlib-only and prove it with `GOPROXY=off`. (#533, #546)
- PR caches can evict main's from the 10 GB Actions cache. (#615, #618)
- A plain CI re-run reuses the old merge ref; merge main and push. (#166)
- promtool unit tests compare annotations too; a missing sample (`_`) is not a failed scrape (`stale` is); an SLO over several probes takes the best probe before the burn rate. A `mktemp -d` folder is 0700 and unreadable to an image running as `nobody` on Linux CI, hidden by Docker Desktop; chmod it before mounting. (#727)
- macOS bash 3.2 treats an empty `"${arr[@]}"` as unbound under `set -u`; use `${arr[@]+"${arr[@]}"}`. macOS's `/usr/bin/grep` has no `-P`: match a tab with `grep -E "$(printf '\t')x"`; a script that passed in zsh with a GNU grep on PATH says nothing about bash. Inside `${var/pattern/repl}` within double quotes bash 3.2 reads an apostrophe as a quote. In the agent shells (zsh), `set -e` across a long command line has let later steps run after a failure, a loop over a string does not split it, and there is no `PIPESTATUS`: a sequence that pushes or merges runs as a bash script. (#739, d20261107-watcher-merged-early, #826, #856, d20261107-paid-plugins-merge-problems)
- Lists compiled in with `go:embed` must exist in every job that compiles, Docker builds included (no Go on that runner): `release.yml`'s `plugin-lists` job fetches and verifies them once and passes them as job outputs, written next to each `license-key.sh --require` step. A new job's Go module download runs offline unless listed in `test_go_mod_download.py`'s ONLINE. (#819)
- The rehearsal's pipeline is a stand-in that runs no job; the release that fails its watch has an API that crash-loops by design, so a check during it reads the database. A plugin stage has no `job_stages` row until a worker syncs the graph: enqueue once it exists and check the outcome. `pipeline/run/` is ignored now that workers write `areas.json` there. Scenarios 16.x run each release's real pipeline wheel (no ML extras, one environment per release, the work an 8 s sleep through a `.pth` hook); a wave's own runs enqueue more jobs, so judge the queued jobs by id, as `p.drain` claims follow-ons too. The update's backup refuses when the dump and the disk's reserve do not fit. (#818, #821, #842, #890)
- A recovery script reaches a node through a hook (`NODE_EXEC`), so the workflow uses SSH, the rehearsal `docker exec` and a shell test a stand-in. The fake Hetzner API checks its token before any path, its own controls included. A status incident template keeps its comment after the front matter. Runbooks name real commands only. Shell tests stop stand-in servers by saved PID with errors visible. (#780)
- Release fixtures built for an old state owner break on the next push after the change ships; drive state the way the owner does. (#656)
- The integration tier's time varies 2m33s–5m15s by runner for the same work near its 5-minute budget. `go build -o` into a path holding an up-to-date binary skips the link (a new temp folder paid 6.5 s each call); a scratch database copied from a migrated template is cheap, re-running migrations is not. (#854, #856, #858)
- An update watch that polled every pipeline's version with a 3 s timeout under load about 90 never saw ten healthy minutes, and a hand-stopped pipeline ran it to `watch_timeout`: 0.3.90 rolled back. Liveness is the supervisor's job; a watch confirms each process's version once per pid. (d20261107-update-watch-rollback)
- A client that keeps its own list of fine states turns a new server state into an alarm: the Mac app called the held plugin host "keeps stopping" (held now counts as off). For a status line, quiet on unknown with the server's own summary is safer. (#777)
- Code importing `cloud/internal/...` lives inside the cloud module (`cloud/loadtest`, not `tools/loadtest`); the cloud's golangci-lint forbids `fmt.Print*` in `cmd/` too: write to a given `io.Writer`, run `golangci-lint run` in `cloud/` before pushing. A baseline from main's last run is an artifact (`actions: read`, `gh run download`), and the first run, with none yet, passes saying so. Prometheus 3 range selectors are open at the start: sample a promtool test more finely than the range; a recorded series needs `labels: 'name'` in `exp_samples`. `docker stats` CPU is in cores. Docker Desktop can restart with no images. (#798)
- Mac notarization breaks on empty `APPLE_*` variables; local builds carry a placeholder updater key. (#276, #288)
- TestFlight: the API cannot create app records (Xcode Organizer can; its error after a 201 is harmless); the CI key needs Admin. An unsigned archive has no entitlements; a single-size macOS icon is ignored. (#447, #583)
- Android: names live in `res/values*/strings.xml`, not `resValue`; App Store names must be unique ("Maktaba Remote"). (#564, #560)
- Cloudflare deploys run from main only, each piece gated on its files existing; a token lists its own permissions only with API Tokens: Read, otherwise a read probe per permission, and each piece is checked with its own token. `node --test` takes a quoted glob, not a folder. (#867)
- A rehearsal that builds NAT on Docker bridges switches off Docker's masquerade on the network playing the internet (`enable_ip_masquerade: "false"`); for a forwarded connection that times out, compare conntrack on both ends. (#892)
- `caddy validate` accepts a Caddyfile whose rules never run: beside a catch-all `handle`, a bare `respond`, `redir` or `abort` is sorted after it, so each rule that must run first is a `handle` of its own, written first. The fallback origin's deny for addresses outside Cloudflare's ranges and the witness's token check (50.11) both never ran; a review missed it and the end-to-end scenario caught it. `caddy adapt` needs every module and variable a file uses, so a small tokenizer is the broad check and `deny-first.sh` on the adapted JSON the semantic one. (#927, #930, #932)
- The ci_paths guard reads `git ls-files`: run it after committing a new file. A `tools/ci` script the lint job runs on every pull request belongs in `NOT_IN_CI`. An env var over 128 KB fails on Linux (`MAX_ARG_STRLEN`), not on macOS. `pgrep -f PATTERN` through `bash -c` matches its own command line; bracket one letter (`[T]CP-LISTEN`). (#932, #939, #922)
- Pixel references with a tolerance cannot guard wording; an exact text reference beside them can, and shows the failing line. (#929)
- Without the bounded waits, a hung `-q` pytest run was cancelled at 15 minutes with no traceback; the stuck cluster test did only aiosqlite work (cause not proven). (#945)
- `ci-success` fails whenever any other job fails, so a "rerun only if the flaky job is the sole failure" check must leave it out. (#970)
- In `cloud-rehearsal`, the 4 s aggregate verdict proves nothing alone: read the run's jobs to see that a scenario actually ran. (#967)
- `actions/cache` derives an entry's version from its path list: a save whose paths differ from its restore's writes an entry the restore never finds. An all-or-nothing cache save never fills during an outage. `/archive/` and the REST tarball are different files with different SHA-256s. (#1014)
- An EXIT trap's last status becomes the script's exit status: `[ cond ] && cmd` in a trap fails a good run when `cond` is false. A deploy script run from a Mac avoids `chown --reference` and GNU `sed -i`. (#1010)
- `test_a_slower_plugin_fails_n16_with_both_numbers` trips on some shared runners with code that passes elsewhere (#1018, #1021); classify `detect`'s N16 failures were a real per-call cost that a fixed allowance could not hold. (#1059)
- An integrity check must not take its trusted source from the file it checks. (#1014)

**Database and migrations**
- Number a migration slot only when merging main (slots are taken while a PR builds); a slot above a gap fails migration-lint; `SchemaRev` lives in three places. Check `git ls-tree origin/main shared/db/migrations/` right before each push, not only at the start; at each renumber rebuild `cluster_copy_excluded()` on the newest definition (Up adds, Down restores that one) and raise the three SchemaRev constants together. Slots moved three and four times while a PR waited; a moved migration is written again from its generator (`roles.py sql`, which `roles.py check` compares against every manifest), never sliced from the old file (a slice that kept `--` lines opened `StatementBegin` twice; migration-lint's `goose-annotations` now refuses it). Prove a migration parses with `go run . migrate up` from `api/` on a new database. The store's D1 migrations number separately but still collide within one series. (#669, #692, #678, #672, #657, #693, #762, #767, #778, #781, #815, d20261107-paid-plugins-merge-problems, #925, #949)
- A new table keeps `maktaba_worker`'s default grants (compat-prev) and refuses joined machines' writes with a `BEFORE` trigger; it must be decided for the node copy (`copyReviewed` or `cluster_copy_excluded()`). A new failure cause needs its label and help; a new pool needs its `worker_pools` seed. A function redefined in full by many migrations (`cluster_copy_excluded()`) is a merge hazard git cannot see: migration-lint's `copy-excluded-keeps` makes each redefinition keep the previous tables (or name them on a `-- cluster_copy_excluded drops:` line) and its Down restore the previous list; when merging main, rebuild the list and the Down from main's newest definition. A lint that parses SQL asserts it found something. (#663, #692, #693, #822)
- migration-lint refuses a plain `CREATE INDEX` even on a new empty table: `CONCURRENTLY`, in its own statement block of a NO TRANSACTION file. (#731)
- The SQLite migration runner rewrites `ADD COLUMN IF NOT EXISTS` only when it is the one statement of its `StatementBegin` block. (#733)
- Cloud migrations are numbered by epic slot; goose refuses a version below the head, so `Migrator.Up` passes `goose.WithAllowMissing()`; `goose Down 1` rolls back the highest slot, so test a migration with `goose.DownTo(<version below it>)`. Widening a CHECK drops and re-adds it, a contract: keep it in its own file marked contract. `CREATE OR REPLACE FUNCTION` is expand-safe. (#700, #832, #854)
- `job_stages.stage` is capped at 40 characters; the SQLite sibling cannot widen an inline check without rebuilding the table. (#693)
- `audit_log.category` is a closed list (use `components` for a new action) and its `actor_user_id` references `users` (write it through a subselect). The queues handler's sqlmock tests pin the stage SELECT's 22 columns: read a new column in a separate query. (#750)
- `RollbackSafe` counts any added constraint as destructive, an inline column `CHECK` included, and a destructive slot restores the dump (the 16.x wave scenarios fail on any restore): use a trigger or writer-side checks. Migrations run twice in integration: `DROP CONSTRAINT IF EXISTS` first. (#524, #669, #799)
- A rollback is targeted: when every migration since the previous release has a Down script and loses no data on the way up (no DROP of a table, column or sequence; no DELETE, UPDATE, TRUNCATE or type change outside dollar-quoted function bodies), the new release runs `migrate down-to <previous schema>` and marks the record the previous release's own; the dump returns only when an Up lost data or a Down fails. 0.3.90's 14 Down scripts ran in about 5 s where the dump restore of 73 GB needed about a day. (d20261107-rollback-down-scripts)
- Retention runs in the loop that runs every 15 minutes, never only at a rare event; count-only retention never empties a kind that rarely gets members, so one-event dumps (a failed update) need an age limit, while the newest pre-update dump, the way back, has none. (d20261108-backups-tidy)
- Lock-order comparisons are row comparisons, not joined strings (collation ignores punctuation); enqueue after commit to avoid deadlocks. (#662, #678)
- Add up every program's pool maximum against `max_connections`. (#627)
- Postgres frees an advisory lock only when its backend exits: a lock connection whose ping fails because the context was cancelled must not be discarded, or the lock outlives `stop()`. (#740)
- CI's test URL has no query, so a harness rebuilding URLs by hand passes CI and fails where the scratch server's URL has one (asyncpg's `sslmode=prefer` hides it, lib/pq's `require` shows it). A test binary cached at one fixed temp path is shared by every worktree on the machine. (#769)
- lib/pq bounds only the dial; older lib/pq ignores the passfile on Windows. (#608, #362)
- lib/pq does not stop a connection's startup when the context ends: against a vanished host (killed container, old failover address) a connect hangs until `connect_timeout` (`db.Open` adds `connect_timeout=2`). A killed container is not a refused connection. (#714, #715)
- Triggers are the reliable way to NOTIFY: they cover every writer. (#714)
- Patroni: a partitioned primary demotes only after its WAL senders give up (`wal_sender_timeout: 10s` plus a `/primary` guard); a commit cancelled while waiting for the sync standby is reported done but lost on failover, so RPO 0 holds only for uncancelled commits. A restored Patroni database names the old node's `hba_file`. (#721, #724)
- pgBackRest `backup` writes one repository per run (loop `--repo=1`, `--repo=2`); `archive-push` writes all. MinIO images are gone; SeaweedFS's S3 gateway stands in once a round trip proves it warm. (#724)
- Tests sharing a Postgres must not terminate each other's LISTEN connections; tag them with `application_name`. One connection hears notices in commit order. (#608, #471)
- A stage version bump re-queues every subject; to fix a subset change only their fingerprint and add a self-ending sweep. (#525)
- A restore builds the HNSW vector index (`segment_embeddings_hnsw`, 28 M rows) after the other indexes, alone, with a third of RAM (at most 32 GB) and up to 7 workers through PGOPTIONS, falling back to no workers and then the server's settings; with the server's 64 MB it was 9% done after two hours while the home was down. Building it in the background after the swap was rejected: a rollback's older release would not know to build it. (d20261107-restore-vector-index)
- Restores raced the PC's node-copy grant; after a hand recovery check vectors, post-data and ACLs. (#395, #433)

**Platforms**
- Windows: services cannot see mapped drives (sign-in task instead); `postgres.exe` needs a non-admin token; `os.Rename` cannot cross drives; CTRL_BREAK quirks; PowerShell's cold start on runners is 20-50 s. (#175, #196, #165, #205, #573)
- macOS: announcing the host's own name from a second mDNS responder renames the Mac; use the system responder (dns-sd, Avahi, resolved, DnsServiceRegister). Host names are not identities. (#520, #538, #551, #609)
- systemd-resolved ignores mDNS from its own addresses; test from a peer namespace. Code that never ran in CI had three bugs. (#562)
- macOS `t.TempDir()` is under a `/var` symlink; `/home` is an automount. (#546, #572)
- Local Network grants need LC_UUID and, like Full Disk Access, apply only to later processes. (#295, d20261031-tooling-local-gotchas)
- SQLite fsync per commit is nearly free on macOS and costly on Linux and Windows runners; on SQLite every engine loop shares one connection behind a lock. (#521, #540, #561)
- Relay drain: Docker's 10 s default stop grace kills a 150 s drain (set `stop_grace_period`); `http.Server.Shutdown` returns while hijacked websockets stay open, so the registry tracks every tunnel. Tunnel auth is the in-band AUTH frame, not HTTP headers. (#701)
- Relay tunnel directory: a tunnel's seq must be its creation time (a per-process counter ordered tunnels wrongly across blue and green); a home closing its drained tunnel the instant streams end races the relay (the home waits a quiet second, the relay retries idempotent requests); a fixed reconnect window makes a 1,000-home drain take the whole 15 s budget. (#705)
- The shared log redactor rewrites IP addresses (`10.50.0.11` shows as `10.50.0.0`) and the home folder as `~`: read container and test logs with that in mind: two addresses in one /24 look the same, a test reading a client IP from a Go service's log compares redacted values, and a `strings.Contains` on the raw address can never fail. (#705, #707, #922)
- CodeQL's go/reflected-xss flags a header value reaching a page even when only fixed values can match: return constants. (#705)
- A health check must not do the work it reports on: check in the background, answer from the last result. A meter that swaps its map before a flush must keep the snapshot when the flush fails. (#714)
- Caddy: one certificate per name (`relay.test, *.relay.test` is two); DNS-01 needs the test DNS to answer the zone's SOA; the cloudflare module needs a token-shaped test value. Reloading Caddy's config cuts streams in flight, so blue/green moves by readiness (draining colour answers `/readyz` 503) after a stability window, with no edge change. (#720, #722)
- Docker: moving a network alias reconnects the container (the Floating IP gets its own network); `docker compose down` needs the same variables as `up`; read effective config with `docker compose config`, since logs redact IPs. Go's `flag` stops at the first non-flag argument. (#721, #715, #722)
- On Windows an atomic replace guarantees whole contents, not a successful open: a reader treats a failed open as "try again". A test reading two facts from one snapshot needs both under one lock. (#684)
- In a length-prefixed format bound the length by the bytes received before allocating. (#691)
- Canonical JSON for signatures needs its own small parser per language: Go's `encoding/json` escapes `<`, `>`, `&` and U+2028/9 unless `SetEscapeHTML(false)` and keeps the last duplicate key, JavaScript keeps lone surrogates, serde_json refuses them. Hash `files.json` in canonical form. WebCrypto imports an Ed25519 seed as PKCS 8 with the prefix `302e020100300506032b657004220420`. (#733, #743)
- `node:sqlite` (Node 22.13+) runs D1's SQL as is, so store tests need no Miniflare. A far-future ISO date (`+275760…`) sorts before `2026` as text; compare against `9999-12-31T…`. (#735)
- Cloudflare Workers tests (Miniflare): workerd refuses a compatibility date newer than its build; Argon2id in wasm makes each test account slow, so Worker tests get 30 s; a queue consumer races tests driving the same work by hand, so a binding (`CHECKS_FROM_QUEUE`) turns it off. (#753)
- Signed-list versions never go below the copy already on the CDN, or a restored database publishes a list homes refuse as a rollback. Split an `Authorization` value on its first separator only: splitting on `=` cuts base64 padding. (#753)
- A feed on another origin needs a CSP change (`connect-src` was `'self'`) in the home's headers and in the cloud's player and remote editions, which copy them (`cloud/internal/handlers/relay/editions.go`). "Through the cloud" is the home's address under `relay.maktaba.tv`: match the suffix with its dot, or `evilrelay.maktaba.tv` passes. (#752)
- A WIT variant gains no case without breaking 1.0 guests; a plugin loop refusal reuses `scope` with the API code `plugin_loop`. (#751, #762)
- Wasmtime's component linker matches a guest's `maktaba:plugin/x@1.0.0` import against the host's `@1.1.0` by semver (old guests keep working while no shared type changes) and links a newer minor too, so the import audit (`plugin_api_newer`) alone refuses it, and a WIT minor bump moves only the audit's version check. `cargo build` of the guests misses a WIT change (`generate!` does not track the file): touch the guest sources, check with `strings x.wasm | grep maktaba:plugin`. A host test running prebuilt guests (`plugind/guests`) fails with a missing stage when they are stale: `make plugind-guests` before `cargo test`. (#731, #762, #778)
- Go's `rules.Effective` does not apply the pipeline's `ENV_DEFAULTS` (`MAKTABA_OLLAMA_URL`); Go code reading those rules applies the variable itself. (#731)
- `propagate.finish` refuses any request outside a stage's `may_request`: a cross-plugin event needs its own allowance (`plugin-event:`), never a `may_request` entry. The engine's graph validation refuses request cycles through non-monotone stages with a GraphError that stops every graph build, so the home's flow check refuses a self-requesting plugin before it is enabled. (#759, #762)
- First-party plugins: moved stage code stays unchanged when the runner's database maps each SQL text to its registered name, run under `SET LOCAL ROLE maktaba_fp_<plugin>`; their code in the pipeline wheel needs a hatch build hook (`force-include` also applies to editable installs, and its stale copy shadows the tree); `display_title` calls the language model, so it moves with `compute.generate` (wave 4). (#750)
- On the rehearsal's seeded home the previous release's first seed writes every component of its catalog (by what is on disk); only components newer than it go through SeedMissing. (#748)
- First-party waves 1-2 (#760, #773): a moved stage flips to `plugin` at each worker's graph sync. The parity harness found transport bugs no unit test did (a client reading only `Content-Length` corrupted the next request after a chunked answer; an exception frame's `type` overwrote the frame's own). A statement under `SET LOCAL ROLE` cost five round trips until the connection took the role once. Parity seeding patches `uuid.uuid4` with random-looking ids (low-bit ids collide on hex prefixes) and gets its own `MonkeyPatch.context()`; restore every file area between sides. A roles migration that may not create or grant a role only notices (`role_unusable`); a runner that cannot start judges neither an update's start nor its watch. Parity timing takes p95 over each job's CPU (`/proc/<pid>/schedstat`) and p50 over wall time: CI showed 30-180 ms stalls on both sides with CPU flat. (#760, #773)
- Seatbelt `(deny default)`: a child process (ffmpeg, even `/usr/bin/true`) aborts in its loader unless the profile allows reading `literal "/"` and mapping `/usr/lib` and `/System` (Homebrew tools need their whole prefix); its regex has no `{m,n}` and a bracket starts with its `-`; a profile that does not compile fails the whole sandbox (exit 70). Import everything before `apply`. Seatbelt matches real paths (`/tmp` is `/private/tmp`), so the policy names sockets by their real paths; a long data path's socket falls back to `/tmp`, the rule on rehearsal homes. Same-basename pytest files in folders without `__init__.py` cannot collect together. (#773, #842, #843)
- ffmpeg's `correct_ts_overflow` (on by default) takes 2^33 off a stream starting in the last 60 s before the PTS wrap, so with `-copyts` a muxer started then ran 30-40 s ahead of the wall clock for viewers too; `MuxerArgs` passes `-correct_ts_overflow 0`, and ffprobe needs the same flag near the wrap. A muxer that runs across the wrap is unaffected. (#797)
- A "last done at" stamp taken at the start of slow work makes its waiters think it stale: stamp it when the work returns and compare with when the waiter arrived (the JWKS cache). (#792)
- Python loads one top-level `tests` package per session, so two test trees each with a `tests` package cannot share a session; the root `tests/` has no `__init__.py` and its folders are the packages. (#794)
- First-party waves 5-8 (#800, #806, #823, #844): what only the engine may write (`enqueue`, a duplicate merge `dedup_commit`, `unsupersede`, the TV swap's `tv_commit` and `tv_rollback`) the worker writes for a plugin's job after checking it against the job, as one relayed op, rather than granting tables; a relayed enqueue of a row in the plugin's uncommitted transaction is held until the job succeeds. `may_request` is a graph rule: a new edge can make a cycle the graph refuses. Move core code a stage module imports that the core itself needs into a core module before moving the stage. A runner has no per-process registry: send names with the job and fall back when one does not resolve. A joined machine reaches the home only through its Python worker's agent. Code telling first-party from extension stages by `job_stages`' `extension` tag must learn the handover (the token mint, the catalog's plugin rule). Per-job work in `run_native` costs every plugin job. `$1 IS NULL` does not type a parameter on Postgres: cast per dialect. A secret a door adds can come back when the far side stores what it was sent (dizqueTV programme bodies), so ask whether a plugin can read that body back before filling one. (#800, #806, #823, #844)
- The cloud's logs (53.17): each process keeps its lines on its node and forwards them to W numbered and acknowledged; lines name a home by `home_h`. A server that returns while background writers run loses their last writes and fails `t.TempDir` cleanup: wait for them and cancel an inner context on every return path. A role test calling every admin route needs a rule for an honest 503. promtool refuses a 7-day rate over 1-minute samples; sample every 5 minutes. (#852)
- A `no-cors` fetch tells "answers" from "doesn't" with no CORS headers on the target (opaque response resolves; DNS refusal, refused connection or bad certificate rejects); a browser cannot test a certificate against a name other than the URL's. (#906)
- First-party wave 3 (#789): a plugin job that falls back to the worker writes the same rows, so parity passes without the plugin path; the worker counts fallbacks (`native.FALLBACKS`) and parity fails on any. Lazy imports inside stage functions are invisible to the statement scan, and two such helpers swallow every exception; `sql += " AND …"` is a second statement. MLX on Metal works in Seatbelt once the profile opens the GPU's user clients (else "No Metal device"). (#789)
- The apps reach a home's raw LAN address (`local_ip`) by checking its certificate against the entry's pinned `tls_name` (an OkHttp `HostnameVerifier`; `SecPolicyCreateSSL(true, name)` on Apple), which keeps the platform's chain and expiry check; a pin counts only when the name's first label decodes to the URL's own address and its second is `direct.HomeID(server_id)`. Pins are kept per home, and an address two homes pin is pinned to neither: a trust override keyed by address alone lets one home answer for another, since private networks reuse addresses. Apple refuses a TLS leaf whose validity exceeds its maximum even under a custom anchor, so test certificates are short-lived with a pinned verify date. OkHttp's pool keeps a connection's earlier hostname check: a test that changes pins between calls calls `connectionPool.evictAll()`. (#903, #944)
- `web/`'s tsconfig has no Node types: a test that reads a file imports `node:fs` dynamically. `wrangler deploy --dry-run` with the devportal's pinned version checks a Worker's bundle and config without an account. (#941)
- Random bytes are not reliably corrupt media; build fixtures from a real header plus seeded junk. (#526, #537)
- This Mac: the iOS 27.1 runtime has no iPhone device types (make throwaway simulators on iOS 27.0); bash is 3.2 (no `mapfile`; build `-only-testing` arguments as an array); Android screenshots record locally with Homebrew's `openjdk@17` and `-Proborazzi.test.record=true`; ELF fixtures build with `docker run --platform linux/arm64 alpine:3.20`. (#959, #967)
- The Android apps build no native code of their own; the only `.so` files are prebuilt AndroidX ones Google strips, so native tables appear only once an app builds native code. (#967)
- A Windows venv is not one process: its `python.exe` is a launcher. Anything that trusts a pid starts the base interpreter named in `pyvenv.cfg`, refuses the start if it cannot be named, and holds the created process's handle (which also stops pid reuse); a broker accepting by Job Object or process tree would admit the runner's tools. (#986, #1018)
- A file shim must be total: every lookup Windows answers without `os.stat`, and every call that swallows `OSError`, is another place a refusal reads as absence. A server loop answers every request whatever it raises, and a caller that falls back on a refusal treats a peer that closed without answering as one too. (#986, #997)
- A read-only broker root is enough for a probe: ffprobe reads its one share input through the `fd:` handle. (#1019)
- A relay failure (no answer, 502, 503, 504, not `home_reconnecting`) reads the cloud's status at once, at most every 10 s. Running a test once with the old behaviour put back is the cheap proof that it checks the wiring. (#907)
- A plugin's own events go through `events/emit` (manifest `emits`, schema, 16 KB, scope); the native relay is a separate back channel, so its rule is own job only. (#1021)

**Apps and UI tests**
- Under Robolectric a bare `waitUntil` only polls; the v1 compose rule runs effects unconfined. Use settling waits and v2 rules. (#508)
- Lazy grids anchor on the first visible key, so out-of-order answers open a page scrolled. A BackHandler always composed keeps its old order. (#529, #523)
- A read that started before a write must not overwrite it on screen: one `ReadOrder` per platform. (#613, #616, #624)
- SwiftUI: an identifier on a wrapping Section replaces its children's; a `LazyHStack` in a horizontal scroll can stay 0 pt; not every `INFOPLIST_KEY_*` is generated (check the built Info.plist); a `.task` on a view that draws nothing never starts, so put it on a container that always exists. On Android and Apple alike an empty lazy-list or grid item still takes the list's spacing: add the item only with content, on every platform at once. (#541, #569, #579, #787, d20261107-paid-plugins-merge-problems)
- Apple localization: `LocalizedStringKey` lookups with values need `String(format: localized(key), args)` and positional `%1$@` in both languages; a line's values belong in its close signature so a new value reopens a closed strip. Edit `.xcstrings` and the wiki's Arabic JSON in place, inserting entries as text: a JSON round trip that re-sorts rewrites hundreds of lines (Python's sort is not Xcode's, and MaktabaKit's catalog is not fully sorted). (#752, #732, #742, #824)
- Android unit tests use OkHttp 5's `mockwebserver3` (`MockResponse.Builder()`, `server.close()`); local gradle needs `JAVA_HOME` (Homebrew openjdk@17) and `ANDROID_HOME=~/Library/Android/sdk`. The `client` module is plain JVM (`:client:test`), `core` and `player` are Android (`testDebugUnitTest`); `home.features()` in the Player is composable, so read it in the screen body and key the effect on it. A Kotlin backticked test name cannot contain a period. (#752, #742)
- XCUITest does not close the app after a test; settings leak between tests; the `.textClipped` audit relays at 12 text sizes; the simulator can drop taps. On a loaded runner one look at the app's tree can take 4 s, so `waitUntil` takes at least three looks as well as its seconds; a long-press waits for Home to go quiet (`waitUntilHomeIsQuiet`); a looped test's failing variant is the one whose screenshot is missing. A mock's delay is no hold when the client has its own timeout (`ImageCache` gives a fetch 15 s): make the app never ask. (#539, #582, #580, #626, #661, #803)
- Apple crash reports: a one-time question after sign-in would block every signing-in UI test, so a UI-test launch shows it only with `-maktaba-crash-question`; MetricKit's call tree puts the crashing frame at the root (Sentry frames are oldest first, so the walk is reversed); `#require` cannot nest; Swift Testing suites run in parallel even when each is `.serialized`, so suites sharing a URLProtocol stub go under one serialized parent. The shared redaction masks `token=` only in a URL query. (#732)
- Android crash reports: `InputStream.readNBytes` needs API 33; a native crash's `traceInputStream` is a binary tombstone, read on Android 12+ by a hand-written reader of the needed `tombstone.proto` fields (crashed thread's frames, signal, scrubbed abort message; nothing else), its reason alone kept otherwise; the crash question stays off under Robolectric (`Build.FINGERPRINT == "robolectric"`). (#745, #952)
- Panels (#936): Compose's "text is shown" checks pass for clipped or scrolled-out content (only a screenshot caught it); a CSS variable with a fallback hides a wrong name, so test each is defined; an app that sets only TV Material's theme must give Material 3 its palette for any phone component. Homebrew `openjdk@17` plus the Android SDK records Roborazzi goldens on the Mac. (#936)
- Desktop crash reports: Maktaba's shell has no user token, so it hands its scrubbed panics to the web app in its window, which has the session and the consent switch. `File.renameTo` does not replace a file on Windows (`Files.move(REPLACE_EXISTING)`). Two Gradle builds at once in one checkout can fail one silently. (#774)
- `Collections.synchronizedList` does not make iteration safe; a list one thread writes while another reads needs `CopyOnWriteArrayList` or a lock around the read, and a test fixture a session writes from `Dispatchers.Unconfined` callbacks is read while written (`MapStore` is a `ConcurrentHashMap`). (#788, #841)
- AVPlayer can stall forever on a late segment; reopen after 15 s. HLS subtitles need `X-TIMESTAMP-MAP`. (#614, #468)
- A rate limit's or "did not happen" test must bound by elapsed time or by the thing's own size. (#694, #696)
- A per-line performance budget needs enough load to amortise setup: at a tenth of the load the capture bench read 28 µs a line against 15.6 µs. (#690)
- A setting a separate process consumes needs a test from the file to that process: the `logs` role silently ignored every configured retention. (#688)
- A per-role level file polled by each logger can say "debug until a time" and works on Windows; SIGUSR1 cannot. (#688)
- React: a window `keydown` listener re-attached by `useEffect` can be the previous one when a key arrives between commit and effect; `useWindowKeydown` attaches once. A flake fix that will not reproduce locally is a reading of the code until CI stops failing. (#698)
- Scrape over HTTP once in a test, then feed parsed samples directly; 720 scrapes broke the per-test cap. The Prometheus text format parses in about 60 lines, no module needed. (#689)
- A test that reaches a real `os._exit(0)` ends pytest green with no summary. The conftest's teardown check (since 2026-11-02) fails a test that leaves a non-daemon thread, so a thread a test sees started inside it. A coroutine that never awaits a real loop can be driven with `coro.send(None)` where the unit tier refuses sockets. (#565, #868, #781)
- Web tests under load: vitest is main-process bound past about 7 workers, so `maxWorkers` is half the cores, 2 to 8 (CI keeps 2); axe's first run in a worker is several times slower, so warm it in `beforeAll`; parallel module warm-up can expose a half-evaluated module; `shouldAdvanceTime` fake timers make "not yet at T−1" racy, so read times off the fake clock. On a loaded machine a few web unit tests still time out at 5 s, different ones each run: run the failing files alone before suspecting the change. Waiting for any call before reading a specific one (`Logs.test.tsx`), or counting elements as soon as the first exists (the shimmer gallery), is the same one-render-early race: wait for the specific call or the count. (#746, #752, #732, #765)
- Web tests: a page that loads names in a separate call is tested with `findBy`, not `getBy`; accessible names keep `bidiIsolate`'s isolates, so match with a regular expression; a browser cannot read a `.mkplugin` before upload, so the typed-name check is a second upload. An error fingerprint takes the message line and first frame's function, numbers folded. (#691, #717)
- A page test using `Link` needs a `MemoryRouter`. The web's page harness gives every page the all-on capabilities, which have `panels: false`: test a page with panels on by mocking `useCapabilities` in a file of its own. A store test importing the SDK's source needs smol-toml resolved from the store's node_modules (a vitest alias and a tsconfig path). (#765, #759, #795)
- The installed Maktaba's bundled Postgres binaries can start a scratch cluster in the scratchpad (`-k ''`, a free port), never the app's data. (#693, #677)
- The wiki's Arabic store translates only each spec file's title and first 600 characters, so deep README edits need no Arabic but a changed story header does (`--ar-todo --epic 54`); `generate_wiki.py --check` shows which entries a spec edit reaches; its JSON is `indent=2`, keep it. (#751, #735, #911)
- maktaba.dev: `rewriteHref` joins an absolute `/path` onto the source folder, so generated pages link in-site pages by full URL. It builds with npm, not pnpm; a new guide is one line in `GUIDES` and one in `src/site.test.ts`. `media.info` is gated in `server.go`'s `capabilityOf`, not in `capabilities.toml`. (#704, #723)
- Screenshots without a stack: the desktop Player through `runDesktopComposeUiTest` plus `captureToImage` (wrap `Shell` in the window's `Surface`); the web through vite and playwright-core with `/api/*` from a route table and `mkt:usage:consent` set first; Apple TV shots are in CI's `tv-ui-tests` artifact. The devportal lazy-loads images, so scroll each into view to check it. (#703)
- Plugins: stages load at the pipeline worker's start, so a changed installed set needs the supervisor's `Restart`; the pipeline needs `MAKTABA_SUPERVISOR_TOKEN_FILE`; a deep data folder pushes `<data>/run/*.sock` past macOS's 104-byte `sun_path`; Wasmtime under the hardened runtime needs `allow-jit` and `allow-unsigned-executable-memory`; a new catalog group needs its place in the web's `GROUP_ORDER`. (#707)
- The affected-tests gate maps from main's coverage: tests in a new file count for an old source only when the coverage map names its package, schema-revision constants still need a `No-Test-Change` trailer, and shared JSON tables map to no test (add a direct test beside the row); `tools/ci/tests` needs PyYAML. On the Mac with under 20 GB free some cmd/maktaba-server tests fail on main too; `make compat-prev` needs `psql` on PATH. (#707, #731)
- `@maktaba/flowchart` is the SDK's source reached by a tsconfig and Vite alias, so every build copying `web/` alone needs it too (`web/Dockerfile`, `cloud/Dockerfile`'s web stage, the dev compose mounts); only the e2e image build shows it. Node 22 imports an erasable-syntax TypeScript file with `--experimental-strip-types`, so the portal draws with the SDK's source. The web draws charts with React's SVG elements from a geometry model, so no plugin string is set as markup. (#763)
- The home's auth gate must list the machine routes (`/internal/cluster/plugins/`, `/internal/cluster/plugin-api/`) beside the delegation middleware, or a signed machine gets 401. A CPU budget on a shared CI runner is timed as the process's own CPU time (getrusage, `process.cpuUsage`), not wall clock. A stopped `make compat-prev` can leave `api/internal/compatprobe`; it is now git-ignored and the script cleans up on INT and TERM. The first-party parity self-test fails locally on main with a dev API binary; CI is its judge. (#767)
- Repository rules bind every new workflow: each tag workflow takes a `tag` input, each artifact download is a three-attempt chain naming its artifact, each file sits under a ci-paths filter (`ci_paths.py guard`). A D1 `CHECK` on a status cannot widen without a table rebuild: add a column (`flow_state`). The shared flow checker's lookup is synchronous: run it, load what it asked for, run again until it asks for nothing new. (#770)
- A snapshot's `#N` must be a PR with an event; other numbers are written without `#`, an issue as `issue #N`. (#706)
- Homebrew's cargo shadows rustup's and has no wasm target (the SDK template needs rustup's `wasm32-wasip2`; the store's wasm builds with `~/.rustup/toolchains/stable-aarch64-apple-darwin/bin` first on PATH); the root `.gitignore` `MANIFEST` hides a `manifest/` folder on the Mac. (#683, #694, #717, #753, #770, #886)
- Claude Code reserves plugin names starting with `claude-`; the plugin is `trail`. Hook machines have python3 without `tomllib`. (#702)
- Apple UI tests seed `UserDefaults` from launch arguments (`-key value`; `<hex>` reads back as Data), giving a mocked cloud with no project change. The cloud web app's Playwright tests serve it with Vite's dev server and answer `/v1` with `page.route`. Apple and Android keep the cloud token in memory only, so the saved home list is keyed by the typed email. The relay's errors are `{"error": ...}`, the home's carry `code`: a client classifying by `code` reads `error` too. Appending at a Kotlin test file's end can land in its last (helper) class. A new server answer gets an error kind per app (web, Apple, Android), and the desktop Player's `StringsTest` wants a sentence for each Android `ErrorKind`. The cloud web app's API client has no PUT. (#791, #856, #854)
- A T3 server restart drops a session's tracking of its background watchers, not the processes: find an orphan by command line and stop it by PID. (#782)
- The Apple Player's pages are compared with committed references (`PageSnapshots`, 0.2 % of pixels; read from the test bundle, since the host app is sandboxed; `tools/apple/snapshots.py <run id>` copies a CI run's drawn pages in). The tolerance guards layout; exact text references guard wording. A `String` built with `String(localized:)` or `.formatted()` follows the process language, not SwiftUI's `\.locale`: build page strings with the environment's locale. Player UI-test bundles compile some files alone (`ImageCache.swift`), so a MaktabaKit symbol there links only in the app: check the project's source phases before adding an import. A failed job cannot be re-run until its whole run ends. (#893, #910, #903, #929)
- SMAppService errors on register or unregister do not mean the login item is gone: check `status` and `launchctl print` first, since launchd may still hold a good job. A Developer ID app's designated requirement survives updates and so does its registration; only ad-hoc builds need re-registering per build. After the updater swaps the bundle, the old process reads the new build through `current_exe`, so anything keyed on "this build" is captured at process start. (d20261108-login-item-relaunch)
- A `workflow_run` rehearsal takes its matrix from main's head and checks out the rehearsed commit, so a row added to `update-rehearsal.yml` fails any older commit whose CI finishes after the row lands, with `no tests to run`. (d20261108-rehearsal-matrix-skew)
- Import cycles show only in a fresh interpreter importing in the child's own order (`pipeline/tests/test_child_imports.py` does that in `python -I`; a suite importing `engine.graph` first hid one). A rehearsal that only checks the site serves passes a release whose runners crash-loop; the wave scenarios now fail on any process marked failed after the update (`assertNoCrashLoop`). (d20261108-runners-wait-venv)
- A SwiftUI `Label` with `.accessibilityIdentifier` can expose the SF Symbol's spoken name as the element's label: use `Text` when a UI test reads the words. (#970)
- `merge-main.sh` stops on `Localizable.xcstrings` conflicts: merge them as JSON keeping main's case-insensitive key order; Python's `sorted()` reorders the whole file. (#959)
- Robolectric drives a file picker with `LocalActivityResultRegistryOwner` and a fake `ActivityResultRegistry`; `registerOutputStreamSupplier` captures what the app writes. (#959)
- The Arabic store keeps only a story's opening excerpt (about 600 characters), so editing deep in a story needs no new Arabic; `generate_wiki.py --check` confirms it. The devportal tests need `npm run build`, which needs `npm ci` in `web/wiki-app`. (#954, #960)
- Windows AppContainer: a process may neither create a named pipe nor open `nul`, so any runner code that opens `os.devnull` hits the refusal too. `winproc` starts tools with `Popen`, anonymous pipes and a Job Object each; close a Job Object handle under the same lock as its terminate, clearing the handle first, or a reused handle value can end another tool. `Popen` closes the child's thread handle, so a tool cannot start suspended and join its job first. (#976, #977)
- Windows refusals must propagate: `os.path.exists`, `isfile`, `isdir` and `lexists` are `nt._path_*` and answer False on any error, with no `os.stat` and no audit event, so each is wrapped; `glob` and `os.walk` swallow `OSError`, so a stage that lists a share folder uses `os.listdir`; a helper or a catch that turns an `OSError` into an exit code or a failure hides the refusal from the fallback. (#976, #988, #990)
- Windows share broker: a capability-free AppContainer reaches no SMB share, and the network capabilities open the whole LAN. The launcher opens each file, refuses reparse points and duplicates a read-only handle into the runner, which keeps confinement at no throughput cost. Compare share paths by ASCII case only: Go's Unicode folding and Windows' upcase table disagree. ffmpeg seeks a handle passed as `fd:`; `pipe:` does not seek. (#968, #974)
- Windows grants: apply them by handle (`FILE_FLAG_OPEN_REPARSE_POINT`, `SetKernelObjectSecurity`), and read a descriptor as stored with `GetKernelObjectSecurity`, since `GetSecurityInfo` infers inheritance on a DACL without `SE_DACL_AUTO_INHERITED`. Keeping that bit on a set drops the new entry. A fixture that tests inheritance creates its files with `CreateFile`, because `os.WriteFile` left explicit entries only. Log from the test (`t.Log`), not from inside the code under test. (#966)
- A forced rerun is the repair for a wrong "done": the engine skips a job whose input fingerprint matches its last success. (#990)
- GitHub release downloads: on 2026-11-08 the github.com door answered 504 for long stretches while the API's asset endpoint served the same files. curl's `--retry 3` is over in 11 s, and a wider backoff alone did not help. A cache saved only by main's push leaves pull requests downloading; one saved on any miss serves that PR's reruns. Tauri's bundlers fetch NSIS and the AppImage tools with no retry unless each file is already under tauri's name in its tools folder. curl reports code `000` for `file://`. A composite action or an edit to `ci.yml` runs every job. (#1003)
- CodeQL default-setup runs cannot be rerun (HTTP 403): only a new head commit retriggers them. Probe the bundle URL (`curl -r 0-1023`) until several rounds succeed before pushing one. (d20261108-codeql-bundle-504)
- A status a waiter shows on arrival must be ordered with the broadcasts it can receive: read the state and show it in the same critical section. (#1002)
- `database/sql` over modernc SQLite keeps no statement cache, so a loop running one statement per row prepares it once; a CPU profile, not a guess at WAL locking, showed about half of `Record`'s time under `-race` in SQL parsing. Fixtures several tests read share one recorded database file, copied per test. (#992, #983)
- A fake that must stay byte-identical can still be fast: `getrandbits(32 * n)` is n 32-bit draws, little-endian, and leaves the generator in the same state. A bench measures something only where the stage does its work. (#996)
- A tap on a scrolling SwiftUI list stops the scroll and selects nothing: wait for the target to stand still, then check that the tap did what it should. (#1000)
- "N tests, 0 failures" with exit 65 is a failure before a relaunch: search the log above the totals for "Restarting after unexpected exit, crash, or test timeout". (#1001)
- An API handler adding a field to an answer proxied from the logs role buffers and re-encodes it, so the log index stays ignorant of the cluster. (#983)
- kotlinx.serialization's `coerceInputValues` turns only a null into the default; a value of the wrong type still fails the whole answer, so optional fields use per-field lenient serializers. On this Mac the Android tests use Homebrew's openjdk@17 as `JAVA_HOME`, and Robolectric also needs `ANDROID_HOME=$HOME/Library/Android/sdk`. (#993)
- A column that can only be filled from stored data is a nullable column filled the first time it is read: migrations stay DDL only. A vitest of a Copy button replaces `navigator.clipboard` after `userEvent.setup()`. (#994)
- A first-party write's checks are worked out by the build from the parse tree and `tools/plugins/scope_tables.toml` (a top-level `AND` term on an owner column, every owner column of an `INSERT`, the conflict target of `DO UPDATE`), and a write it cannot bind fails the build; 91 of 187 statements had needed more than their header listed. Only `pgstore.write_vectors` (keys from `unnest($5)`) and `transcribe.forget_gone` (no argument) are set by hand. A `CASE WHEN $flag THEN $value` sets the owner column from `$value` only: checking the flag would refuse every call. (#1005)
- A first-party token has `AllLibraries = true`, so a granted-library test passes for any library: a first-party job's own library is `inv.LibraryID`. YouTube rows are shared (a channel belongs to the first library that claims it), so one counts as in scope for every library that follows or downloads it. Bigint ids follow a predictable sequence, so only found rows are cached: a cached "no such row, allowed" could be replayed once another library creates that id. (#1005)
- A home's `pgbin` fetches Postgres from Maktaba's CDN, then GitHub's API asset route (by tag and name, `Accept: application/octet-stream`, no credentials), then the github.com door, three tries each. The updater, self-update, the update catalog and packs inside an update job keep the door: they need an update-rehearsal T-row first. The same outage reached CI through the Windows job's WiX download and the Gradle wrapper. (#1007)
- Moving core stages into a plugin conflicts with every wave merged meanwhile: drop both sides' moved entries and rebuild the generated files (`bundle.py --write`, `roles.py sql`). A parity world runs every stage the graph queues, including other plugins': switch off what is not under test, or it depends on the machine's tools. A runner exits after one idle minute (`RunnerIdleSec = 60`); a job that finds it gone waits for it to start. A real-ffmpeg Go timing test fails when pytest shares the machine, so run the full Go suites alone. (#998, #1041)
- Core stages are not switchable, but the flip at update is the graph sync (`engine/sync.py`), which does not read `switchable`: only the admin route and the CLI refuse them. (#1041)
- The graph sync moves every first-party stage with no `impl_changed_at` to `plugin`, so a request for a stage's current impl must still be recorded as set by hand, or the next sync undoes it; any control that automation also writes records a person's choice even when the value does not change. (#1074)
- A rehearsal step on the previous release runs that release's code, so a fix on main does not reach it: the scenario waits for a state that release makes observable. (#1074)
- Parity harness: starting the dev server while the databases are copied saved more than `STRATEGY FILE_COPY` or concurrent `CREATE DATABASE`. A fake that refuses a stage's call turns it into retries and a weaker proof: read a parity world's job outcomes, not only its pass. (#1068)
- Every plugin-API read by id is typed from every column-to-`$N` comparison in the statement (`write_scope.read_checks`). `processing_jobs.subject_id` is polymorphic: the `job_subject` kind resolves it through the tables that own each subject kind and reads job rows only when none has the id, counting only job rows with a library, because `checkRow` allows as soon as a row has no library. A scratch cluster from `initdb` without `-E UTF8` is SQL_ASCII, and so is the `template1` the harness copies: livetv parity then fails with `22P05`, which looks like a code fault. (#1025, #1050)
- A removal passes its own rehearsal while the previous release still has the component, and breaks the rehearsal one release later, when a release without it becomes the previous one. (#1032)
- A tool that runs another tool can hide its downloads: appimagetool fetched its runtime unpinned inside linuxdeploy until given `--runtime-file`, and tauri-cli keeps linuxdeploy's output only at `--verbose`, so `failed to run linuxdeploy` with nothing after it means the output was dropped. `gh run rerun --job` refuses while the run is in progress. (#1034, d20261108-ci-cache-eviction-linuxdeploy)
- Over GitHub's 10 GB limit an Actions cache entry can be evicted minutes after it is saved, and churn reports little loss because jobs re-save and go green: read a main run's "No cache found" and "Cache saved" lines per job. A cache-hit proof needs a rerun right after the save. The working set is accepted above the limit, with bounded churn and `cache-prune.yml`; the rehearsal seeds are about 4.1 GB a release. Go's build cache is addressed by its inputs, so the runner image need not be in the key. (#1034, #1036, d20261108-ci-cache-eviction-linuxdeploy)
- Epic 58 platform facts: Roku states no right-to-left text; Samsung Public Seller accounts launch only in the US; Samsung MSE caps at FHD (4K needs AVPlay); Cast receivers take no HEVC in MPEG-TS; neither Tizen nor webOS gives a web app mDNS or SSDP. The wiki generator's Latin-share rule trips on brand-heavy excerpts (transliterate outside code spans), a new epic renames the chunk files, and `docs/wiki/epics/epic-NN-*.md` and the INDEX row are written by hand. (#1038)
- A refusal's detail text can leak the payload: an error that echoes its input carries user data into the log. (#1048)
- On small fixed-size images Python's big integers act as a wide vector unit: one int with a 16-bit lane per pixel beat `map`/`operator` chains 3 to 6 times with no dependency, and `to_bytes()` + `sum()` beats the multiply-by-ones trick at these sizes. A fixture from random inputs uses stdlib `random.Random`: numpy's `Generator` streams are not promised stable. (#1050)
- A best-effort step that skips itself quietly hides a test's real precondition: the restore test saw empty `asides`, not a lack of room. (#1047)
- A fixed allowance over a cost that scales with call count looks flaky. N16 now states the per-call cost and judges CPU (which ignores stalls) and paired wall time (which cancels a slow machine); the runner runs inside the test process, so `time.process_time()` covers worker, runner and plugin code. (#1059)
- Moving a stage out of process drops every process-wide setting it read: the parity suite ran with no path map, so both sides agreed on the wrong answer and only the live home's NFS map exposed it. An `item_permanent` failure across a whole share is a deployment-shape bug first. (#1062)
- A package-level value that does real work runs in every role of a multi-role binary: keep expensive ones lazy; load-small's per-tunnel memory comparison catches what unit tests cannot. A stopgap carried through several layers goes from all of them in one PR. (#1027)
- A delegated agent cannot wait on PR checks, so stacked PRs need the coordinator to drive merges in order. Go 1.26 deprecates raw `ecdsa` coordinates: use `PublicKey.Bytes()` or `ecdsa.ParseUncompressedPublicKey`. (#1067)

## Open

- Not verified on real hardware: Apple TV playback and AirPlay, background audio on device, foldables and tablets, the desktop Player against a live home. (#512, #579, #523, #667)
- Remove the tag guard's `IGNORED_CI_JOBS` once the iPhone/iPad shards are fixed. (#577)
- Player UI shards left in progress after their run completed (issue #649). (#632)
- Whether a tap lost to the simulator race shows as a cancelled touch (issue #639). (#661)
- Docker Postgres on the home Mac was stopped, never removed. (#148)
- The VPS kernel patch for CVE-2026-98432, and a `SECURITY.md`. (#518)
- Epic 52: the real gold set and calibration curves; the rehearsal's "one component more" step. (#672)
- Epic 54 on Windows: no GPU device grants (an `ml` runner may fall back to CPU) until CI has a GPU. While a library root is on a share, only `thumb_small`, `chapter_thumbs` and `subtitle_gen` run in their runners (`SHARE_RUNNER_STAGES`); every other library stage stays in the worker (`windows_share_library`). Still to come: a write broker for the full-write groups (convert, dedup, repair, youtube), and runner proof for transcribe and second_listen on a share. A share the group writes in full is served read-only by the broker, and a write op there goes to the worker. `serving_group("fp")` does not wait for its runner. `events/emit` refusals are not logged with the plugin, event and reason, as relay refusals now are. (#956, #953, #968, #976, #977, #990, #986, #1018, #1019, #1021)
- Epic 54 Phase 2 and paid plugins (signing ships dark, no root keys): Phase 2 is built through the store browser, and buying, entitlements, private paid packages and purchase moves are in; the store is not deployed (its go-live list, with `PAID_URL_KEY`, the email provider, the Cloudflare resources, DNS and secrets, and the 90-day lifecycle rule on `report-logs/`, is in `store/README.md`); once the root keys are committed a release fails while the store's CDN is down (re-run); the isolated runner waits for Containers on the account; Apple snapshot references for the plugin panels (issue #876). (#733, #735, #743, #751, #753, #770, #778, #819, #820, #815, #837, #861, #886)
- Epic 54 Phase 4: waves 1-3 and 5-8 are in; YouTube's download swap is not file-scoped; the nightly YouTube hosts check meets a bot wall on data-centre runners, and yt-dlp needs a JS runtime the plugin cannot run (issue #900); the legacy `extract` and `thumbnail` stages with their hand chaining turned into edges (54.29), moving `enrich` (a secrets route for first-party plugins and a rule for its page reads, an owner decision), the rest of wave 9 (54.31, 54.36; the core stages now run in `tv.maktaba.core-media`; next the legacy handlers' removal one release later, in its own pull request, if no stage was flipped back; `extract` has no ml runner yet and runs in the transcription plugin's group; `categorize` has never had a handler, and removing it or keeping it declared is an owner decision), the job-scoped path check (issue #796), every wave's rehearsal scenarios (issue #804), N16 for `tv_stage`, `tv_swap` and `tv_rollback` (the bench cannot rerun a state transition), a runner call for a stage's `refresh` (still run from the worker), and removal of the in-process path; per-plugin `.mkplugin` packages in `checksums.txt` wait for a release-workflow change. (#731, #750, #760, #773, #789, #800, #806, #823, #842, #844, #890, #899, #847, #919, #920, #921, #925, #942, #948, #998, #1041)
- Epic 54 joined machines: a machine's plugin compute always asks the home's models; its own GPU models are not offered to plugins. (#767)
- Epic 50: series that 53.17 emits for the status page; W gets the `maktaba-cloud` binary with 50.07 (`deploy.sh` ships to A and B only); 50.15's direct-remote rehearsal passes, but 50.06's scenario table says UPnP where the fake router speaks NAT-PMP; the web home settings' `direct.rebinding` notice for when the LAN name fails and the address works (the apps use the raw-IP entry; the probe cannot tell a blocked name from a LAN firewall on the port; the advice holds either way); 50.19 is in, with the owner's prices, Pro's allowance and the payment provider on the go-live list, the cloud issuing `basic` once homes that read it are out, and the rehearsal's plan numbers removed when real ones are set; 50.31's rehearsal scenarios T06–T08 are in, and the relay check will also require the attacker's (redacted) address once the relay logs its resolved client IP; 50.32 §1, §4 and §5 (§3's app and console Workers are in); 50.33 removes `ConsoleSignIn` in the listener and the Worker; the api and worker in 50.08's blue/green (a restart costs about a second per deploy); 50.33 and 53.18 follow (50.30's edge rules are in: whether Pro accepts `log` as a rate-limit action shows at the first apply, and 50.32's CSP must allow `https://challenges.cloudflare.com`; 50.34's Cloudflare workflows are in; 50.34 deploys both Workers with `cloud-web-deploy.yml` and smoke-checks them) (50.12 to 50.15 parts 1–2, 50.35 and the console's own bundle are in); the direct-name domains, their NS delegation and the ACME EAB accounts are on the go-live list. 50.13's T09 waits for a home stack in the cloud rehearsal; 50.14's absolute numbers, the epic's sizing figures and `RELAY_MEASURED_*` wait for a `loadtest.yml` run on the staging pair. 50.12's incident handling is in; `tunnel_connections_low` and the `staff_*` alerts come with 50.31 and 50.33, and the game day waits for the real pair. `nodecopy`'s tests still build maktaba-api per test. (#727, #739, #752, #780, #791, #798, #808, #825, #845, #856, #865, #871, #877, #883, #895, #897, #858, #852, #906, #892, #867, #873, #903, #922, #928, #941, #952)
- An address two of a user's homes share loses its by-IP path for both, which fall back to the LAN name, direct or relay: a deliberate fail-closed trade until there is a per-home client. (#944)
- Rare web test races of one shape (a check one render too early), listed on issue #697; BigScreen still re-attaches its key listener. (#746)
- Apple Player references: iPhone- and iPad-only layouts have none, and those UI tests still fill `{{now…}}` from the clock (a new screenshot reference there needs the pin); no tvOS snapshot job (the Apple TV form is drawn on the Mac). (#893, #910, #929, #936, d20261108-snapshot-fixture-clock)
- Plugin scope: an id that names no video at all still takes the "not found, so allow" path; harmless for an UPDATE or DELETE by id, but an INSERT could reference a missing video; check per statement mode. (#937)
- Story 51.03's spec still names the removed `loops-full-home.txt`. (#748)
- Other whole-list functions may share `cluster_copy_excluded()`'s merge hazard (`maktaba_min_node_schema()` is checked against the MANIFEST); add a lint rule when a second such list appears. (#822)
- The tag guard requires the cloud rehearsal once 50.07's scenarios are live. (#715)
- agent-trail's PyPI publishing waits for its trusted publisher; the CLI runs from git. (#702)
- Epic 53: the desktop Player offers no screenshot (needs screen recording); 53.14 T09 (the simulator does not deliver MetricKit on demand); 53.16's token now reaches both deploy paths, but no cloud deploy runs yet (`RELAY_DEPLOY` is unset, and the single-VPS stack runs only `--role relay`, while `/v1/symbols` is `--role api`): which path goes live first is undecided, and uploads are confirmed only after that; the first tag after that shows the native-symbols notice in a real run, and native frames get app-own tables only once the apps build native code; the first symbols list after the deploy unpacks each older table once, one by one; `docs/app-privacy.md`; native Remote Errors, Logs and Storage screens; the Overview's summary series fill only from this release. (d20261108-android-native-symbols-path, #994, #983, #992, #896, #905, #688, #689, #691, #732, #745, #774, #782, #824, #915, #917, #918, #952, #957, #959, #960, #961, #967, #970, #978, #954, #1010)
- Epic 53 log bench: the strict Python bench's margin at the reference is about 20% (the secret scrub, about 2.6 µs, is next to look at), and the Go `logbench` keeps its runner factor until it flakes; the `logs` role's CPU and memory on a live home; the log pipeline's numbers on the live Overview. (#690, #740, #802, #838, #866)
- maktaba.dev shots of the Remote's word review and the phone Players' queue. (#703)
- DASH top-level segments are not on the stream gate's allow-list. (#474)
- `tests/engine/fixtures_db.py` drops the URL's query when it names the template database; harmless under asyncpg's default today. (#769)
- `test_node_cli` depends on other tests' threads in the same pytest worker. (d20261107-paid-plugins-merge-problems)
- A hung pipeline-cluster test on Windows: if the stack dump shows one again, read it for the root cause (aiosqlite's wakeup or something else). (#945)
- Dependabot dismissals to revisit when tauri moves to GTK4 and eslint adopts minimatch 11. (d20261102-dependabot-dismissals)
- The rehearsal could skip, as not applicable, a row whose test does not exist at the rehearsed commit (for example `go test -list` first). (d20261108-rehearsal-matrix-skew)
- Only the wave scenarios run the crash-loop check: the others use a fake pipeline wheel with no `maktaba_plugin_runner`; a fake runner module there would let every scenario run it. (d20261108-runners-wait-venv)
- Epic 57 (parental controls, spec only): the build, Phase 1 first (57.01, 57.02, 57.03, 57.10), and a real-device check of whether the effective rating limit reflects the parent's own Content & Privacy Restrictions (57.04 §4), before the Phase 3 release tag. (#979)
- Classify parity: every parity open still clones two databases and starts the dev server (about 1 s). In `compute_metrics`, `statistics.pstdev` is about 20 % of what is left for a season; replacing it would change the last bits of three metrics, so it stays. (#996, #1050)
- One slow iPad shard's warm audits took 15.5 then 14.0 s: on such a runner a test's first audit can still reach XCTest's 15 s deadline. (#1001)
- Plex removal's cleanup migration, a release or more later: the seven stages' rows, the two pools, the dizqueTV extension and component rows, settings keys, `external_links` targets, `plex_items` and the channel audit tables, the `maktaba_fp_dizquetv` role, the retired lists and the `rules.legacy_tv_live` fallback. The mediaintel scripts still write Plex collections on their own. (d20261108-remove-plex-sync)
- CodeQL's default setup downloads its own tools and cannot use the API route: a rerun is the remedy. TinyGo and wasm-tools in `_plugins.yml`, and the packs' recipes (`tools/release/packs/`), still use `curl --retry 3`. linuxdeploy-plugin-appimage has no tagged release and stays pinned to a `continuous` build; the step warns when it moves. The first release after this one's Linux legs are the first to run `tauri-tools.sh` in `desktop-release.yml`: check that they restore main's entry and bundle with no runtime download. (#1003, #1014, #1034)
- Plugin scope: the worker's `publish` relay op still takes any payload. The `job` kind lets any job read a catalog job's row (no library's; flags and hashes only, harmless today). (#1005, #1025)
- Runtime downloads: move the updater and the update catalog to the API route, with an update-rehearsal T-row; the store CDN's bucket (`maktaba-plugins`) and domain do not exist yet, so the publish step warns and skips, and homes go on to the API route (an owner decision, with the release token's write access); CI's WiX download could get the same fallback or be cached. (#1007)
- Add an option in `.trail.toml` for trail-only events, so AGENTS.md needs no override note. (#1020)
- Epic 58 (TV platforms, spec only): the build, Phase 1 first (58.01–58.04, 58.15's catalog check, 58.16), and a real-device spike of whether Cast receivers enforce Chrome's Local Network Access for LAN HTTPS names (58.07). (#1038)
- Caches: compare job times on main's second run after the new entries exist (unit near 8.7 min) and re-measure usage; whether the rehearsal's `full` seed (2.7 GB) needs a cache is a question for its job's time. (#1036)
- Plugin event emits: the `unavailable` and `home_unreachable` refusals are logged but have no test. (#1048)
- Other parity tests are over the 5 s soft cap on CI (classify waves 1 and 4, repair, the N16 selftest, livetv staging); core-media's now fits, split by N16 group. (#1049, #1068)
- The customer auth handler (`cloud/internal/handlers/auth`) still computes its argon2id timing hash at init, so every role holds that 64 MiB. (#1027, #1067)
- No sweep was made of other process-wide state the moved first-party stages may read; the guard covers only `to_local`. (#1062)
- N16: CPU p95 over 11 runs is their maximum, and a rare job costing about 4x CPU in both processes came within 0.8 ms of failing `topic_assign`; the plugin API's ~0.58 ms of CPU a call is the bulk of the per-call cost. (#1059)

## Missing events

| PR | Merged | Title |
| --- | --- | --- |
| #784 | 2026-11-07 | fix(update): a busy pipeline no longer rolls an update back |
| #812 | 2026-11-07 | fix(restore): the vector index is built alone, with memory of its own |
| #946 | 2026-11-07 | fix(backups): old backups go on their own |
| #969 | 2026-11-08 | feat: remove Plex sync and dizqueTV; Plex Live TV stays |
| #973 | 2026-11-08 | fix(supervisor): plugin runners wait for the venv like the pipeline |
| #1023 | 2026-11-08 | test(rehearsal): scenario 16.7, wave 7's TV change across an update |
| #1042 | 2026-11-08 | fix(web): an open screen keeps its feature gate when the feature goes off right after the first answer |
| #1077 | 2026-11-09 | perf(cloud/auth): make the customer sign-in timing hash at the first sign-in |

## Index

| Event | Date | Kind | Title |
| --- | --- | --- | --- |
| d20261014-pc-dizquetv-origin-and-rules | 2026-10-14 | decision | dizqueTV on the PC led to mediaintel, Maktaba's queues and Maktaba TV; the first working rules |
| 0047-arch-queue-engine | 2026-10-17 | decision | Change-driven multi-level queue engine |
| 0054-arch-whole-library-transcription | 2026-10-18 | decision | Whole library transcribed on the queue engine |
| 0056-arch-cluster-phase1 | 2026-10-18 | decision | Cluster phase 1 - one home, machines pull jobs |
| 0058-arch-cluster-channel | 2026-10-19 | decision | Cluster phase 2 - the cluster channel |
| 0061-arch-plex-hdhomerun | 2026-10-19 | decision | Plex sees Maktaba as an emulated HDHomeRun tuner |
| 0075-pc-node-storage-unknown-size | 2026-10-19 | pr | Storage check accepts files of unknown size, so the joined PC takes jobs |
| 0078-pc-plex-through-the-home | 2026-10-19 | decision | Nodes reach Plex through the home and never hold its token |
| 0081-pc-canonical-smb-paths | 2026-10-19 | decision | Paths are canonical smb:// share paths |
| 0097-arch-tv-blue-green-slots | 2026-10-19 | decision | Two TV slots, blue and green, with staging and switchover |
| 0113-arch-cohere-second-listen | 2026-10-19 | decision | Transcription model - turbo plus a Cohere Arabic second listen |
| 0127-pc-tests-loopback-no-binaries | 2026-10-19 | pr | Drop the committed api.exe; tests bind 127.0.0.1 |
| 0128-pc-stub-audit-gaps | 2026-10-19 | pr | Stub audit fixes after the no-placeholder rule |
| d20261019-pc-windows-joins-the-mac | 2026-10-19 | decision | Windows joins the Mac's home as a node, not the other way around |
| 0138-pc-release-v0-2-0 | 2026-10-20 | release | v0.2.0 tagged; four of five release workflows failed |
| 0139-pc-plex-hdhr-discovery | 2026-10-20 | pr | Plex can discover the emulated HDHomeRun tuner |
| 0141-pc-classifier-needs-review | 2026-10-20 | pr | Test race fixes that held v0.2.0 until a human review |
| 0142-pc-release-blockers | 2026-10-20 | pr | Release workflow blockers after v0.2.0 |
| 0143-pc-full-node-reaches-home-db | 2026-10-20 | pr | A full node's API and pipeline reach the home's database |
| 0178-pc-node-database-copy | 2026-10-20 | pr | Every full node keeps a live copy of the home's database (Epic 37) |
| d20261020-pc-coordinator-merge-rules | 2026-10-20 | decision | The PC's coordinating session and its review, merge and tag rules |
| 0165-arch-windows-linux-one-install | 2026-10-21 | decision | Windows and Linux get the one-installation app |
| 0165-pc-windows-one-app-epic | 2026-10-21 | pr | Epic 38, one Windows app that runs everything |
| 0166-pc-linux-one-app-epic | 2026-10-21 | decision | Epic 39, one Linux app; AppImage first, no apt or dnf repo |
| 0167-arch-tv-open-change | 2026-10-21 | decision | TV updates join one open change until approved |
| 0175-pc-windows-signin-task | 2026-10-21 | decision | The Windows server runs from a sign-in task, not a service |
| 0177-arch-queue-health | 2026-10-21 | decision | Queue health - failure classes, parking, failing items |
| 0196-pc-pinned-postgres | 2026-10-21 | pr | Pinned Postgres per platform, Postgres on Windows |
| 0197-tooling-hung-share-timeout | 2026-10-21 | pr | A listing on a hung NAS share times out |
| 0198-pc-windows-installer | 2026-10-21 | decision | One per-user Windows installer, no admin, every download pinned |
| 0201-pc-transcription-on-mac | 2026-10-21 | decision | Transcription stays the Mac's by default |
| 0204-pc-windows-postgres | 2026-10-21 | pr | The Windows app runs its own Postgres |
| 0205-pc-windows-ctrl-break | 2026-10-21 | pr | Every role stops cleanly on Windows |
| 0211-ciquirk-docs-only-skip | 2026-10-21 | pr | Docs-only PRs skip the heavy gates |
| 0211-pc-docs-only-filter | 2026-10-21 | pr | The docs-only CI filter never fired |
| 0212-pc-vc-runtime-merge-guard | 2026-10-21 | pr | Visual C++ runtime pin; merged while a check was still running |
| 0216-pc-curl-retry-521 | 2026-10-21 | pr | Linux payload downloads retry a host that is down |
| 0218-pc-one-version-order | 2026-10-21 | pr | One version order everywhere |
| 0223-pc-shutdown-flake | 2026-10-21 | pr | Streaming shutdown flake fixed, 0 failures in 600 runs |
| 0229-pc-linux-distro-ffmpeg | 2026-10-21 | decision | Linux uses the distribution's ffmpeg |
| 0178-arch-full-node-db-copy | 2026-10-22 | decision | Full nodes keep a live copy of the home's DB |
| 0200-pc-adopt-existing-install | 2026-10-22 | decision | The Windows app adopts the Maktaba a PC already runs |
| 0232-pc-vod-nvidia-hwaccel | 2026-10-22 | pr | VOD on NVIDIA used a nonexistent hwaccel |
| 0241-flaky-unit-cap-rerun | 2026-10-22 | pr | A test past the unit tier's hard cap is re-run alone |
| 0243-pc-node-json-writes | 2026-10-22 | pr | node.json writes get a lock, user-only ACLs and safe encodings |
| 0250-arch-islamic-theme | 2026-10-22 | decision | The Islamic library theme |
| 0258-pc-rpm-version-dash | 2026-10-22 | pr | Linux app bundles for a pre-release tag |
| 0148-arch-mac-app-bundled-postgres | 2026-10-23 | decision | Live install is Maktaba.app with bundled Postgres |
| 0271-arch-cluster-self-update | 2026-10-23 | decision | The cluster updates itself |
| 0271-pc-cluster-self-update | 2026-10-23 | pr | The cluster updates itself (Epic 35) |
| 0276-pc-update-failure-reasons | 2026-10-25 | pr | Every update failure says why; Mac app builds again |
| 0287-pc-update-spec-parallel | 2026-10-25 | pr | The rest of the update spec, built by ten parallel agents |
| 0288-arch-live-update-path | 2026-10-25 | decision | Updating live means a tagged, signed CI build |
| 0295-tooling-lc-uuid-local-network | 2026-10-25 | pr | Local Network grants need LC_UUID in the binaries |
| 0305-arch-product-shape | 2026-10-25 | decision | Product shape - Player, Remote, Server and the service admin |
| 0306-arch-native-apps | 2026-10-25 | decision | Apps are native, not web wrappers |
| 0307-pc-node-db-access-back | 2026-10-25 | pr | A machine gets its database access back by itself |
| 0309-arch-linked-accounts | 2026-10-25 | decision | Linked accounts for remote play through Maktaba Cloud |
| 0314-ciquirk-empty-checks-merged | 2026-10-25 | incident | A red PR merged on an empty check list |
| d20261025-pc-windows-app-node | 2026-10-25 | decision | The PC's node moves from a hand-built copy to the installed Windows app |
| 0319-arch-pgvector-search | 2026-10-26 | decision | Search moved from Chroma to Postgres pgvector |
| 0320-pc-update-live-fixes | 2026-10-26 | pr | Live update fixes, no cluster sign-in, tray at sign-in, no blank window |
| 0329-flaky-crashloop-race | 2026-10-26 | pr | TestCrashLoopBacksOffThenFails was a real race |
| 0334-arch-licensing | 2026-10-26 | decision | Licensing end to end |
| 0344-arch-analytics-every-way | 2026-10-26 | decision | Analytics counts every way of watching TV |
| 0346-ciquirk-apt-cache | 2026-10-26 | pr | apt stalls were a slow Azure mirror |
| 0348-arch-usage-data-anonymous | 2026-10-26 | decision | Usage data is anonymous and per admin or per app |
| 0349-pc-client-certificates | 2026-10-26 | pr | Client certificates on the home's database broke the node's pipeline |
| 0350-arch-data-on-big-disk | 2026-10-27 | decision | Data and backups live on the big disk |
| 0362-pc-libpq-passfile-windows | 2026-10-27 | pr | A Windows node's API and copy read the passfile |
| 0364-flaky-switch-the-home | 2026-10-27 | pr | TestSwitchTheHome was a real bug |
| 0366-pc-node-plays-local-video | 2026-10-27 | pr | A full node plays videos it can read |
| 0373-tooling-nas-unicode-forms | 2026-10-27 | pr | NAS file names may be NFC or NFD |
| 0375-arch-recently-added-first | 2026-10-27 | decision | Lists open recently added first |
| 0389-arch-no-click-updates | 2026-10-27 | decision | D1 - no click in the desktop app for updates |
| 0395-outage-v0336-rollback | 2026-10-27 | incident | Outage - the v0.3.36 rollback |
| 0397-arch-backups-never-block | 2026-10-28 | decision | Backups never block an update |
| 0404-arch-tv-preview-keeps-release | 2026-10-28 | decision | A failed TV preview keeps the release |
| 0411-arch-loading-placeholders | 2026-10-28 | decision | Loading placeholders wait 0.4 s; returns are instant |
| 0412-arch-epic47-queue-order | 2026-10-28 | decision | Epic 47 - convert to MP4, dedup, YouTube refresh, in order |
| 0429-arch-tv-never-blocks-updates | 2026-10-28 | decision | Only manual TV changes need approval |
| 0433-outage-v0342-restore | 2026-10-28 | incident | Outage - the v0.3.42 update and failed restore |
| d20261028-pc-update-orders-stuck | 2026-10-28 | incident | Update orders waited on a click; the rollback stalled the PC's copy |
| 0434-arch-machines-follow-home | 2026-10-31 | decision | Machines update only after the home commits |
| 0435-arch-update-rehearsal-gate | 2026-10-31 | decision | The update rehearsal gates every tag |
| 0440-arch-port-registry | 2026-10-31 | decision | One registry of Maktaba's ports |
| 0441-ciquirk-rehearsal-cache-seed | 2026-10-31 | pr | Rehearsal scenarios take their seed from the run |
| 0443-ciquirk-macos15-runners | 2026-10-31 | pr | macos-14 runners retire, moved to macos-15 |
| 0443-outage-tv-release-mismatch | 2026-10-31 | incident | TV held on the old release - release_mismatch |
| 0444-flaky-nodeconfig-race | 2026-10-31 | pr | Flaky - a node config write between two looks |
| 0447-testflight-first-upload | 2026-10-31 | pr | TestFlight upload on tags, and the three fixes it took |
| 0451-flaky-fixed-ports | 2026-10-31 | pr | Flaky - integration servers on fixed ports |
| 0453-log-claude-trail-created | 2026-10-31 | pr | The Claude trail file created |
| 0454-log-apple-arabic-catalogs | 2026-10-31 | pr | Arabic in the Apple Remote, Player and TV |
| 0456-pc-nodecopy-hung-worker | 2026-10-31 | pr | End a copy worker that hangs on the home before a fresh copy |
| 0457-pc-windows-agent-installs | 2026-10-31 | pr | The Windows server agent installs the home's update orders |
| 0458-log-epic48-players-spec | 2026-10-31 | pr | Epic 48 spec, the native Players |
| 0459-log-small-posters | 2026-10-31 | pr | 48.5: small posters for phone grids |
| 0460-log-server-words-as-codes | 2026-10-31 | pr | Server texts for admin screens go out as codes |
| 0461-log-channel-stream-access | 2026-10-31 | pr | 48.3: a channel's stream follows library access |
| 0462-log-design-token-source | 2026-10-31 | pr | 48.2: one design token source |
| 0463-log-kit-live-client | 2026-10-31 | pr | 48.7: MaktabaKit's Live client |
| 0464-log-android-browse-client | 2026-10-31 | pr | 48.8: the Android client's browse calls |
| 0465-log-android-live-client | 2026-10-31 | pr | 48.9: the Android client's Live calls |
| 0466-log-android-player-shell | 2026-10-31 | pr | 48.15: the Android Player shell and test harness |
| 0468-log-subtitle-hls-renditions | 2026-10-31 | pr | 48.4: subtitles as HLS renditions |
| 0469-log-apple-player-shell | 2026-10-31 | pr | 48.13: the Apple Player shell and UI-test harness |
| 0470-log-flaky-tests-root | 2026-10-31 | pr | Three flaky tests fixed at the root |
| d20261031-ciquirk-codeql-notarization | 2026-10-31 | migration | CI quirks - CodeQL default setup and notarization timeouts |
| d20261031-flaky-root-cause-fixes | 2026-10-31 | migration | More flaky tests fixed at their root |
| d20261031-pc-maktaba-link-msix | 2026-10-31 | incident | The PC stuck on v0.3.42 by a maktaba:// link registered only in Claude's MSIX registry |
| d20261031-release-history-to-v0344 | 2026-10-31 | release | Release history up to v0.3.44 |
| d20261031-standing-rules | 2026-10-31 | migration | Standing rules from the old trail |
| d20261031-tooling-local-gotchas | 2026-10-31 | migration | Local tooling gotchas on the owner's Mac |
| d20261031-waiting-on-owner | 2026-10-31 | migration | What waited on the owner on 2026-10-31 |
| 0467-log-kit-browse-client | 2026-11-01 | pr | 48.6: MaktabaKit's browse client |
| 0471-log-rowcache-test-barrier | 2026-11-01 | pr | The row-cache test waits on its own notice marker |
| 0472-log-apt-install-timeouts | 2026-11-01 | pr | CI apt installs fail over a stalled mirror |
| 0473-log-replica-ca-on-connect | 2026-11-01 | pr | The home pushes its database CA when a machine connects |
| 0474-log-signed-stream-urls | 2026-11-01 | pr | 48.12: signed stream URLs pass the web port's gate |
| 0475-log-relay-tunnel-states | 2026-11-01 | pr | The relay records tunnel states in order |
| 0476-log-testflight-findings-spec | 2026-11-01 | pr | Spec of the TestFlight findings for Epic 48 |
| 0477-log-silent-streams-audio | 2026-11-01 | pr | Streams keep their audio when transcribe wrote the track row first |
| 0479-log-android-brand-defaults | 2026-11-01 | pr | 48.11: Android brand defaults |
| 0480-log-apple-brand-defaults | 2026-11-01 | pr | 48.10: the Apple brand, dark by default |
| 0483-log-epic48-batch-trail | 2026-11-01 | decision | Story agents stop editing the trail and changelog |
| 0512-log-airplay-pip-nowplaying | 2026-11-01 | pr | 48.24: AirPlay, Picture in Picture and Now Playing in the Apple Player |
| 0516-log-affected-tests-gate | 2026-11-01 | pr | affected-tests: changed code needs its tests changed |
| 0517-log-play-bundles-tags | 2026-11-01 | pr | Signed Google Play bundles on every tag; one package per Android app |
| 0518-log-mptcp-off-cve | 2026-11-01 | pr | Servers listen without multipath TCP (CVE-2026-98432) |
| 0508-libraries-test-settle-and-logs | 2026-11-02 | pr | LibrariesSectionTest waits settle; tests keep the app's log; Player JVM tests on v2 compose rules |
| 0513-fast-search-series-pages | 2026-11-02 | pr | Fast search and Series tab pages; sign-in submits with Return |
| 0516-affected-tests-gate | 2026-11-02 | pr | affected-tests: changed code needs its tests changed |
| 0519-tv-gradle-wrapper-jar | 2026-11-02 | pr | v0.3.51 had no Play bundles: the TV wrapper jar was not Gradle's |
| 0520-mdns-mac-system-responder | 2026-11-02 | pr | Maktaba renamed the Mac on the network: announce through dns-sd on macOS |
| 0521-browse-sqlite-test-fsync | 2026-11-02 | pr | TestLibraryTabsSQLite over the unit budget: fsync during setup; embed-cache test compared timings |
| 0522-sign-in-next-field | 2026-11-02 | pr | Sign-in forms: Return goes to the next field, the field stays above the keyboard |
| 0523-android-rail-divider-postures | 2026-11-02 | pr | Android Player: collapsible rail, draggable list-detail divider, half-fold layouts |
| 0524-stage-pause-owner-fair-claim | 2026-11-02 | pr | Extract paused by nobody, thumbnail starved: pauses need an owner, pools share fairly |
| 0525-thumbs-subfolder-sidecars | 2026-11-02 | pr | Thumbnails in a channel's .thumbs/ folder were never found |
| 0526-convert-corrupt-fixture | 2026-11-02 | pr | A corrupt-input test got done: random bytes are not always corrupt |
| 0527-extract-skips-on-streaming | 2026-11-02 | pr | Extract writes no WAV on a streaming STT backend |
| 0528-ci-runs-what-changed | 2026-11-02 | pr | CI runs only the jobs a change touches |
| 0529-library-page-stays-at-top | 2026-11-02 | pr | Android Player library page opened scrolled on a slow home |
| 0530-dependabot-sweep | 2026-11-02 | pr | Dependabot Go: chi 5.4.0, klauspost/compress 1.18.7 |
| 0531-dependabot-sweep | 2026-11-02 | pr | Dependabot Rust: zip 9.2.2, tokio 1.54.1 |
| 0532-trail-only-prs-run-nothing | 2026-11-02 | pr | A PR that changes only the trail runs no CI job |
| 0533-release-order-offline | 2026-11-02 | pr | The tag guard downloaded Sigstore to compare version strings |
| 0534-dependabot-sweep | 2026-11-02 | pr | Dependabot Python: certifi, filelock, setuptools |
| 0535-dependabot-sweep | 2026-11-02 | pr | Dependabot npm: vite 6.4.5, glob 14 in mobile |
| 0536-android-book-posture-home-search | 2026-11-02 | pr | Android book posture: Home and Search split at the hinge |
| 0537-convert-rejects-guessed-inputs | 2026-11-02 | pr | Convert rejects inputs ffprobe only guesses at |
| 0538-mdns-avahi-windows-responder | 2026-11-02 | pr | mDNS through the system responder on Linux and Windows |
| 0539-apple-arabic-app-names | 2026-11-02 | pr | Apple app names in Arabic |
| 0539-apple-libraries-audit-warmup | 2026-11-02 | pr | Libraries accessibility audit: warm-up and lazy posters |
| 0539-apple-permissions-gate-state | 2026-11-02 | pr | Permissions walkthrough stayed up after Continue: @State init side effect |
| 0539-apple-routestub-redirect-race | 2026-11-02 | pr | RouteStub redirect race |
| 0539-apple-ui-integration | 2026-11-02 | pr | Apple integration PR: faster Player UI tests and folded fixes |
| 0539-apple-ui-whole-app-log | 2026-11-02 | pr | Apple UI tests keep the app's whole log |
| 0539-apple-watch-tests-close-app | 2026-11-02 | pr | PlayerWatchTests clock stuck: a previous test left the Player playing |
| 0540-worker-undrain-sqlite-durability | 2026-11-02 | pr | Worker agent undrain timeout on Windows: SQLite durability cost in test DBs |
| 0541-connect-restore-unsigned-ci | 2026-11-02 | pr | Saved sign-in restore UI test runs on unsigned CI |
| 0543-extract-only-on-request | 2026-11-02 | pr | Extract only on request; WAVs released and swept |
| 0544-step-code-scan-fast | 2026-11-02 | pr | Step-code scan fast: one literal-prefixed regexp per form |
| 0545-play-internal-upload | 2026-11-02 | pr | Release tags upload to Google Play internal tracks |
| 0546-go-modules-verified-cache | 2026-11-02 | pr | CI Go modules from a verified cache; root-store test on macOS |
| 0547-ci-main-concurrency | 2026-11-02 | pr | CI concurrency: one group for main pushes |
| 0548-updates-sqlite-schema-once | 2026-11-02 | pr | api/internal/updates back under the 30 s budget |
| 0549-capacitor-version-code | 2026-11-02 | pr | Capacitor app versionCode from play-version.sh |
| 0550-series-tab-ipad-tap | 2026-11-02 | pr | Apple Series tab test lost its tap on iPad |
| 0551-mdns-systemd-resolved | 2026-11-02 | pr | mDNS through systemd-resolved; dotted names stay one label |
| 0553-trail-push-runs-no-ci | 2026-11-02 | pr | A trail-only push to main starts no CI or rehearsal run |
| 0555-sign-in-keyboard-real | 2026-11-02 | pr | Sign-in keyboard tests measure the real on-screen keyboard and IME |
| 0556-streaming-backend-audio-track | 2026-11-02 | pr | A streaming STT backend decodes the chosen audio track |
| 0557-play-keyless-upload | 2026-11-02 | pr | Keyless Google Play upload (Workload Identity Federation) |
| 0558-android-restore-failure-race | 2026-11-02 | pr | Android AppSession: a failed restore says Failed before its problem |
| 0559-mac-view-menu-no-tabs | 2026-11-02 | pr | Mac View menu test root-fixed: no window tabs |
| 0560-apple-remote-name | 2026-11-02 | pr | App Store rejected the name Remote: Maktaba Remote |
| 0561-sqlite-reconciler-units | 2026-11-02 | pr | The SQLite reconciler frees the shared connection between units |
| 0562-mdns-resolved-ci | 2026-11-02 | pr | mDNS through systemd-resolved runs for real in CI; three bugs fixed |
| 0563-ci-main-never-cancelled | 2026-11-02 | pr | Never cancel main's running CI or rehearsal run |
| 0564-android-app-names | 2026-11-02 | pr | Android app names carry Maktaba |
| 0565-pipeline-tests-leaked-thread | 2026-11-02 | pr | A leaked model thread ended the pipeline test run with exit 0 |
| 0569-apple-rail-builds-all-cards | 2026-11-02 | pr | Apple Player Home rows could show a title with no cards |
| 0570-postgres-reconciler-units | 2026-11-02 | pr | The Postgres reconciler commits each unit; its leader lock is a session lock |
| 0571-serve-listeners-bound-ports | 2026-11-02 | pr | Integration tests learn the server's bound ports |
| 0572-pipeline-numpy-declared | 2026-11-02 | pr | Pipeline declares numpy; test_paths_per_os off the real /home |
| 0573-desktop-task-stand-in-agent | 2026-11-02 | pr | Windows task tests run a stand-in agent that starts at once |
| 0574-desktop-agent-loop-launcher | 2026-11-02 | pr | Agent-loop tests start the test binary as their launcher, not PowerShell |
| 0576-live-guide-drag-not-flick | 2026-11-02 | pr | Live guide UI tests drag the guide, not flick it |
| 0577-tag-guard-ignores-ios-shards | 2026-11-02 | pr | Tag guard no longer waits on the iPhone/iPad Player UI shards |
| 0578-testflight-apple-tv | 2026-11-02 | pr | Apple TV app goes to TestFlight on every tag |
| 0579-apple-background-audio-mode | 2026-11-02 | pr | The Player's background audio mode never shipped |
| 0580-audits-only-audited-section | 2026-11-02 | pr | Player UI accessibility audits timed out: lay out only the audited section |
| d20261102-coordinator-rules | 2026-11-02 | decision | Coordinator rules: follow-ups start at once, trail in trail-only PRs |
| d20261102-dependabot-dismissals | 2026-11-02 | decision | Two Dependabot alerts dismissed |
| d20261102-v0356-tagged-by-hand | 2026-11-02 | release | v0.3.56 tagged by hand; tags stop waiting on iPhone/iPad shards |
| d20261102-worktree-cleanup | 2026-11-02 | decision | Removed 140 merged worktrees (82 GB) |
| 0582-live-guide-opens-on-guide | 2026-11-03 | pr | Live guide tests open on the guide; settings reset on test launch |
| 0583-testflight-mac-ipad-player | 2026-11-03 | pr | Mac Player and iPad Player join the TestFlight upload |
| 0584-epic50-cloud-no-downtime | 2026-11-03 | decision | Epic 50 spec: Maktaba Cloud with no downtime |
| 0585-epic52-word-review | 2026-11-03 | decision | Epic 52 spec: word-level transcript review |
| 0586-epic53-logs-errors | 2026-11-03 | decision | Epic 53 spec: logs, errors, problem reports, a UI that never goes dark |
| 0587-epic51-components-spec | 2026-11-03 | decision | Epic 51 spec: configurable components |
| 0588-epics-50-53-answers | 2026-11-03 | decision | Answers to Epics 50-53's open questions |
| 0589-epic54-plugins-store | 2026-11-03 | decision | Epic 54 spec: offline plugins and a Maktaba store |
| 0590-android-fold-cover-bar | 2026-11-03 | pr | Android: foldable cover bar, cached pages, readable attention groups |
| 0591-epic54-decisions-no-stripe | 2026-11-03 | decision | Epic 54 decisions: no Stripe; own stages as first-party plugins |
| 0592-attention-words-web-apple | 2026-11-03 | pr | Needs attention titles in English and Arabic on web and Apple Remote |
| 0593-remove-stripe | 2026-11-03 | pr | Stripe removed everywhere; provider-neutral billing |
| 0595-apple-tv-four-fixes | 2026-11-03 | pr | Apple TV: live banner, logo on filler, Home rows, library art |
| 0596-approve-tv-in-sight | 2026-11-03 | pr | Approve a TV in sight: button, waiting banner, notification |
| 0597-readme-user-guide-current | 2026-11-03 | pr | README and user guide brought up to date with main |
| 0598-devportal-maktaba-dev | 2026-11-03 | pr | maktaba.dev developer portal is live |
| 0599-hardware-requirements | 2026-11-03 | pr | docs/hardware.md measured on the live home |
| 0600-epic50-home-broadcasting | 2026-11-03 | decision | Epic 50 spec: home broadcasting and the Broadcaster tier |
| 0601-windows-gpu-transcribe | 2026-11-03 | pr | The Windows NVIDIA node never transcribed: six causes |
| 0602-devportal-guides-only | 2026-11-03 | pr | maktaba.dev cut to developer guides; rolling security.txt; wiki from the repo |
| 0603-trail-no-owner-quotes | 2026-11-03 | pr | Trail: no quotes of the owner's words |
| 0604-no-owner-quotes-repo | 2026-11-03 | pr | Repo text states requirements neutrally, no owner quotes |
| 0605-epic50-cloudflare-front | 2026-11-03 | decision | Epic 50 section 15: Cloudflare in front of the portal and console |
| 0606-plugin-store-independent | 2026-11-03 | decision | Epic 54 plugin store independent of Maktaba Cloud |
| 0607-devportal-arabic-parity | 2026-11-03 | pr | maktaba.dev: every page in English and Arabic, with parity checks |
| 0608-epic51-components | 2026-11-03 | pr | Epic 51: configurable components, all 19 stories |
| 0609-cluster-status-files-pruned | 2026-11-03 | pr | Cluster status and process files removed at stop and pruned |
| 0610-devportal-smoke-markers | 2026-11-03 | pr | devportal deploy smoke check looks for the bilingual markers |
| 0611-second-listen-stage | 2026-11-03 | pr | The second listen is its own stage; an 8 GB card takes turns |
| 0612-stall-test-steps-clock | 2026-11-03 | pr | Writing-encoder stall test steps the watcher's clock itself |
| 0613-android-video-toggle-reload | 2026-11-03 | pr | Android video page: a toggle during a reload is kept |
| 0614-player-home-shelves-tvos-buffering | 2026-11-03 | pr | Player Home on HomeShelves; tvOS buffering banner; stalled players reopen |
| 0615-setup-gradle-cache | 2026-11-03 | pr | Gradle builds cached with setup-gradle; Google repo only for Android groups |
| 0616-read-race-everywhere | 2026-11-03 | pr | A change made while a page is read again is kept, everywhere |
| 0618-cache-main-only-saves | 2026-11-03 | pr | Only main saves the Actions caches |
| 0619-help-epic47-conversion | 2026-11-03 | pr | In-app help: Epic 47 stages shipped, conversion explained, YouTube sources on Manage libraries |
| 0620-wiki-back-bilingual | 2026-11-03 | pr | The epics wiki back on maktaba.dev in English and Arabic |
| 0621-library-dashboard | 2026-11-03 | pr | Library dashboard and a page for each library |
| 0624-read-order-removals | 2026-11-03 | pr | Removals and toggles made while a list is read again are kept: ReadOrder |
| 0625-testflight-ipad-remote | 2026-11-03 | pr | Maktaba Remote iPad joins TestFlight |
| 0622-readme-apps-by-platform | 2026-11-04 | pr | README opens with apps by platform; one Get the apps section |
| 0623-devportal-apps-pages | 2026-11-04 | pr | maktaba.dev Apps pages with screenshots on test data |
| 0626-neutral-test-data | 2026-11-04 | pr | Neutral test data; no raw {percent}; Remote Arabic attention |
| 0627-postgres-connection-slots | 2026-11-04 | pr | Live home hit Postgres 53300: room for every program's pools |
| 0628-queue-read-paths | 2026-11-04 | pr | Library dashboard and queue snapshot stop scanning the job history |
| 0629-youtube-source-switch | 2026-11-04 | pr | A switch on every YouTube source; sources from Tartube |
| 0630-integration-flakes-metrics-port | 2026-11-04 | pr | Integration flakes: agent metrics file and real-Postgres ports |
| 0631-ci-download-retries | 2026-11-04 | pr | CI artifact and Go module downloads retry and name what never arrived |
| 0632-ci-conflicting-branch-head | 2026-11-04 | pr | CI runs on a conflicting pull request's branch head |
| 0634-youtube-sources-from-links | 2026-11-04 | pr | Each library's YouTube sources come from its linked videos |
| 0635-epic51-followups | 2026-11-04 | pr | Epic 51 follow-ups: drawer, off/held health, first boot, Arabic, offline install |
| 0648-epics-build-on-epic51 | 2026-11-04 | decision | Epics 50, 52, 53 and 54 specs build on Epic 51 as built |
| d20261104-filed-issues | 2026-11-04 | decision | Issues filed for problems that did not fail CI |
| d20261104-releases-v0379-v0381 | 2026-11-04 | release | Releases v0.3.79, v0.3.80, v0.3.81 |
| 0651-epic56-tv-library-view | 2026-11-05 | decision | Epic 56 spec: the TV library view |
| 0652-epic55-desktop-player-parity | 2026-11-05 | decision | Epic 55 spec: desktop Player parity |
| 0655-trail-v2 | 2026-11-05 | pr | Trail v2 — per-PR events, per-tag snapshots, a skill and a hook |
| 0656-rehearsal-seeded-prev | 2026-11-06 | pr | The rehearsal airs the TV on a seeded previous release; secrets store system writers as no user |
| 0657-migration-gaps | 2026-11-06 | pr | Unused migration slots listed as permanent gaps, and a lint rule for them |
| 0658-pipefail-early-exit | 2026-11-06 | pr | No step pipes into a reader that leaves early |
| 0659-after-merge | 2026-11-06 | pr | After the merge of #659 — the components conftest clash, and a stale local server binary |
| 0659-catchup-leader-handoff | 2026-11-06 | pr | The worker that opens a component's stages queues its catch-up |
| 0660-pg-url-sslmode | 2026-11-06 | pr | One MAKTABA_TEST_PG_URL works for every Go suite |
| 0661-ui-test-lost-taps | 2026-11-06 | pr | A Player UI test failure after a lost tap says the simulator dropped it |
| 0662-done-subjects-lock-order | 2026-11-06 | pr | processing_done_subjects checks its lock order across a transaction |
| 0663-restart-loops-anywhere | 2026-11-06 | pr | A restart for components whose loops run in another process |
| 0664-epic53-log-schema | 2026-11-06 | pr | Epic 53 story 53.01 — one log line and one redaction list for every part |
| 0665-pack-paths-follow-current | 2026-11-06 | pr | A new dictionary or ffmpeg pack is used without a restart |
| 0666-whole-server-process-scenario | 2026-11-06 | pr | A whole-server components-live scenario for a process component |
| 0667-epic-55-desktop-player | 2026-11-06 | pr | Epic 55, the desktop Player catches up with the Android and Apple Players |
| 0668-offline-switch | 2026-11-06 | pr | One switch that stops every outbound call (serve --offline) |
| 0669-epic-56-tv-library-view | 2026-11-06 | pr | Epic 56: the TV library view (Play all, Shuffle, series, collections, playlists) |
| 0672-epic52-word-review | 2026-11-06 | pr | Epic 52, word review: suspect transcript words go to a home admin, in every language |
| 0673-after-merge | 2026-11-06 | pr | After the merge of #673 — e2e tests that read a role's text log, and a keeper close that did not wait |
| 0673-epic53-keeper-status | 2026-11-06 | pr | Epic 53 stories 53.02 and 53.03 — the log keeper and /status.json |
| 0674-trail-review-fixes | 2026-11-06 | pr | The trail checker sees more secret shapes; the hook reads prs: from git history |
| 0675-pipefail-deploy-and-installer | 2026-11-06 | pr | The pipefail guard covers deploy/ and actions; install.sh resolves latest without SIGPIPE |
| 0676-testpg-pghost-pgsslmode | 2026-11-06 | pr | testpg reads PGHOST for an empty host and leaves PGSSLMODE alone |
| 0677-offline-pipeline | 2026-11-06 | pr | serve --offline reaches the pipeline; the dictionary survives a failed pack switch |
| 0678-import-dry-run-lock | 2026-11-06 | pr | The MediaIntel dry run takes no summary lock; detect is queued after the commit |
| 0679-desktop-player-review-fixes | 2026-11-06 | pr | Seven fixes from the review of the desktop Player's sections |
| 0680-catchup-once | 2026-11-06 | pr | A catch-up is queued once, and a non-leader forgets an off time that is over |
| 0683-plugins-host-sandbox | 2026-11-06 | pr | Plugins part 1, the manifest, the package and the sandboxed plugin host (Epic 54, 54.01-54.03) |
| 0684-after-merge | 2026-11-06 | pr | After the merge of #684 — reading start-failed.json on Windows, and a supervisor test's snapshot |
| 0684-epic53-recovery-safemode | 2026-11-06 | pr | Epic 53 stories 53.04 and 53.05 — recovery page, safe mode, cut-offs |
| 0685-epic53-strip-rehearsal | 2026-11-06 | pr | Epic 53 stories 53.06 and 53.07 — the status strip in every app, fault rehearsal |
| 0686-after-merge | 2026-11-06 | pr | After the merge of #686 — the log index on one host with two machines, and on homes seeded earlier |
| 0686-epic53-index-reqids | 2026-11-06 | pr | Epic 53 stories 53.08 and 53.09 — the log index and request ids |
| 0687-epic53-error-groups | 2026-11-06 | pr | Epic 53 story 53.10 — error groups on the home |
| 0688-epic53-admin-pages | 2026-11-06 | pr | Epic 53 story 53.11 — the Logs, Errors and diagnostics settings pages |
| 0689-epic53-dashboards | 2026-11-06 | pr | Epic 53 story 53.12 — dashboards from this machine's metrics |
| 0690-after-merge | 2026-11-06 | pr | After the merge of #690 — the log bench's calibration on an x86 runner |
| 0690-epic53-perf | 2026-11-06 | pr | Epic 53 story 53.13 — a performance budget for the log path |
| 0691-epic53-reports | 2026-11-06 | pr | Epic 53 stories 53.14 and 53.15 — report a problem, crash reports to the home |
| 0692-restart-requests-guarded | 2026-11-06 | pr | Restart requests are the home's, retried when a read fails, and counted apart |
| 0693-plugins-api-queue | 2026-11-06 | pr | Plugins part 2, the plugin API, media handles and plugin stages in the queue (Epic 54, 54.04-54.06) |
| 0694-plugind-log-flood-bound | 2026-11-06 | pr | The plugin host's log-flood test bounds by elapsed time |
| 0696-launcher-75-bound | 2026-11-06 | pr | The launcher's restart-on-75 test bounds by the backoff it must skip |
| 0698-review-test-effects | 2026-11-06 | pr | The review page's key tests flush the listener's effect before pressing |
| 0699-auto-tag | 2026-11-06 | pr | Releases are automatic: auto-tag.yml tags each green commit of main |
| 0700-cloud-names | 2026-11-06 | pr | Epic 50 starts: the cloud's names, reserved infrastructure slugs, a foreign-domain lint |
| 0701-tunnel-handover | 2026-11-06 | pr | Homes move their relay tunnel without a gap (DRAIN, resume tickets, jitter) |
| 0702-trail-plugin | 2026-11-06 | pr | The trail's tools move to Hamza-Labs-Core/agent-trail (CLI, agent integrations, Action) |
| 0703-devportal-catch-up | 2026-11-06 | pr | maktaba.dev Apps pages catch up with Epics 52, 53, 55 and 56, and the portal-follows rule |
| 0704-devportal-plugin-reference | 2026-11-06 | pr | maktaba.dev's plugin reference, generated from the plugin system's own files |
| 0705-tunnel-directory | 2026-11-06 | pr | Any relay process answers for any home: the tunnel directory, peer forwarding, holding |
| 0706-first-snapshots | 2026-11-06 | pr | The first trail snapshots, v0.3.83 (full) and v0.3.84 |
| 0707-plugins-lifecycle-runtime | 2026-11-06 | pr | Plugins part 3, install, run and manage a plugin (Epic 54, 54.03, 54.07-54.09, 54.37, 54.38) |
| 0714-database-blip | 2026-11-06 | pr | The relay rides out a database blip: home cache, buffered metering, lenient readiness |
| 0715-cloud-rehearsal | 2026-11-06 | pr | The cloud rehearsal: the cloud's cluster in Docker, broken on purpose in CI |
| 0716-live-off-notice-test | 2026-11-06 | pr | The Live TV off notice is looked for before it hides itself |
| 0717-plugins-page-settings-sdk | 2026-11-06 | pr | Plugins part 4, the Plugins page, settings as data and the SDK (Epic 54, 54.10-54.13) |
| 0720-wildcard-certificate | 2026-11-06 | pr | One wildcard certificate on every relay node, by DNS-01, proven against Pebble |
| 0723-devportal-plugins-phase1 | 2026-11-06 | pr | maktaba.dev, installing and managing plugins, and Phase 1 on main (Epic 54) |
| 0725-release-signing-deploy-key | 2026-11-06 | pr | Tags are pushed with a deploy key, so releases are signed as a push |
| 0726-snapshot-v0385 | 2026-11-06 | pr | Trail snapshot v0.3.85 |
| 0730-tag-by-session | 2026-11-06 | pr | Tags are pushed by the release session; auto-tag.yml is removed |
| 0731-plugins-local-compute | 2026-11-06 | pr | Plugins ask the home's own models, local compute (Epic 54, story 54.21) |
| 0732-epic53-apple-native | 2026-11-06 | pr | Epic 53 on the Apple apps — Remote diagnostics, crash reports to the home, Report a problem |
| 0733-plugins-signing-trust | 2026-11-06 | pr | Plugins Phase 2 part 1, signing and the chain of trust (Epic 54, 54.14) |
| 0734-codeql-alerts | 2026-11-06 | pr | The open CodeQL alerts on main, fixed or dismissed |
| 0735-pluggable-payments | 2026-11-06 | pr | Paid plugins through pluggable payment providers, the store side (Epic 54, story 54.19) |
| 0740-log-bench-nightly | 2026-11-06 | pr | Epic 53.13 — the Python logger bench and the nightly Mac log benches |
| 0742-plugin-panels | 2026-11-06 | pr | Plugin panels declared as data, drawn by every app (Epic 54, story 54.20) |
| 0743-home-entitlements | 2026-11-06 | pr | The home checks plugin entitlements offline (Epic 54, story 54.19 §6) |
| 0745-epic53-android-native | 2026-11-06 | pr | Epic 53 on Android — Remote diagnostics, crash reports to the home, Report a problem |
| 0750-first-party-foundation | 2026-11-06 | pr | Maktaba's own stages as first-party plugins, the foundation (Epic 54, 54.22-54.27) |
| 0751-epic54-flows-spec | 2026-11-06 | decision | Epic 54 spec, flows, cross-plugin events, loop checks and joined machines |
| 0753-plugin-store-service | 2026-11-06 | pr | Plugins Phase 2 part 2, the plugin store service, store accounts and linking a home (Epic 54, 54.15) |
| 0759-plugin-flows | 2026-11-06 | pr | Flows, the leaf rule and the cycle check, on the home and for the store (Epic 54, 54.39) |
| d20261106-epic53-ci-flakes | 2026-11-06 | incident | Epic 53's merge series — the CI failures that were not the change |
| 0691-after-merge | 2026-11-07 | pr | After the merge of #691 — a crash envelope's item length |
| 0721-two-nodes | 2026-11-07 | pr | Two nodes, one address: Patroni, etcd and the Floating IP, rehearsed with kill and partition |
| 0722-zero-downtime-deploys | 2026-11-07 | pr | Cloud deploys with no downtime: standby first, expand-only migrations, blue/green by readiness |
| 0724-backups-restore | 2026-11-07 | pr | Backups to two repositories and point-in-time restore, rehearsed by destroying both nodes |
| 0727-monitoring | 2026-11-07 | pr | Monitoring: the canary home, the witness's Prometheus, alerts with unit tests, the outside probe |
| 0737-rehearsal-after-ci | 2026-11-07 | pr | On main the update rehearsal runs after CI passes, on the same commit |
| 0738-plugind-runaway-timing | 2026-11-07 | pr | A runaway plugin job is timed from its start |
| 0739-status-page | 2026-11-07 | pr | The status page, status.maktaba.tv, and its feed from the probes |
| 0739-status-page-ci | 2026-11-07 | pr | What #739's CI taught after its event was written |
| 0746-web-tests-under-load | 2026-11-07 | pr | The web unit suite keeps passing on a loaded machine |
| 0748-rehearsal-loops-v0386 | 2026-11-07 | pr | Scenario 12 reads the previous release's loops and components from the home itself |
| 0752-status-banners | 2026-11-07 | pr | Maktaba Cloud's line in every app's status strip |
| 0752-status-banners-ci | 2026-11-07 | pr | What #752's CI taught after its event was written |
| 0760-wave1-classify | 2026-11-07 | pr | Four classify stages run as Maktaba's built-in plugin (Epic 54 wave 1, 54.28) |
| 0762-plugin-events-lineage | 2026-11-07 | pr | Events between plugins and run-time lineage (Epic 54, 54.40) |
| 0763-plugin-flow-chart | 2026-11-07 | pr | The flow chart, on the plugin page, in the SDK and on maktaba.dev (Epic 54, 54.41) |
| 0765-panels-series-library | 2026-11-07 | pr | Plugin panels on the web's series and library pages (Epic 54, story 54.20) |
| 0767-plugins-on-joined-machines | 2026-11-07 | pr | Third-party plugins on joined machines (Epic 54, 54.42) |
| 0769-fp-selftest-urls | 2026-11-07 | pr | The first-party parity self-test keeps the test URL's sslmode; per-checkout dev API |
| 0770-store-checks-flows-review | 2026-11-07 | pr | Plugins Phase 2 part 3, automated checks, the flow check and human review (Epic 54, 54.16) |
| 0773-wave2-thumbnails-subtitles | 2026-11-07 | pr | Thumbnails and subtitle files run as Maktaba's built-in plugins (Epic 54 wave 2, 54.29) |
| 0774-epic53-desktop-native | 2026-11-07 | pr | Epic 53 on the desktop — crash reports to the home and Report a problem (Maktaba's shell, the desktop Player) |
| 0777-desktop-held-is-off | 2026-11-07 | pr | The Mac app counts a held process as off, not one that keeps stopping |
| 0778-plugin-catalog-kill-switch | 2026-11-07 | pr | Plugins Phase 2 part 4, the store's catalog, updates and the kill switch on the home (Epic 54, 54.17) |
| 0780-incident-handling | 2026-11-07 | pr | Incident handling, runbooks and one-click recovery |
| 0781-entitlement-gate | 2026-11-07 | pr | Paid plugins wait for their entitlement (Epic 54, story 54.19 §5, §6) |
| 0782-epic53-native-followups | 2026-11-07 | pr | Epic 53 native follow-ups — Report this in the desktop Player, the last-crash notice, the phone screenshots |
| 0783-panel-order | 2026-11-07 | pr | The admin orders plugin panels (Epic 54, story 54.20 §1) |
| 0785-lint-bash3-empty-array | 2026-11-07 | pr | cloud-expand-only passes with no new migrations under macOS's bash 3.2 |
| 0786-stage-titles-manifests | 2026-11-07 | pr | Stage titles cover the stages first-party plugin manifests declare (main red since #760) |
| 0787-native-panel-pages | 2026-11-07 | pr | Plugin panels on the native series, library and TV pages (Epic 54, story 54.20) |
| 0788-tv-library-test-race | 2026-11-07 | pr | The Android TV library test's request list is safe to read while the fake server writes |
| 0789-wave3-transcription-search | 2026-11-07 | pr | Transcription and semantic search run as Maktaba's built-in plugins (Epic 54 wave 3, 54.30) |
| 0791-apps-ride-out | 2026-11-07 | pr | The apps ride out a Maktaba Cloud outage |
| 0792-jwks-slow-read | 2026-11-07 | pr | A request that waited for a slow key read uses it instead of reading again |
| 0793-launcher-trap-ready | 2026-11-07 | pr | The launcher test sends the stop once the script's trap is set |
| 0794-components-one-session | 2026-11-07 | pr | Both components test folders collect in one pytest session |
| 0795-plugins-update-pauses | 2026-11-07 | pr | The Updates page names the plugins an update will pause (Epic 54, 54.07 §3) |
| 0797-pts-wrap-muxer | 2026-11-07 | pr | A muxer started just before the PTS wrap keeps the wall clock's timestamps |
| 0798-load-test | 2026-11-07 | pr | Load test and sizing |
| 0799-plugins-data-migrate | 2026-11-07 | pr | A plugin's data migrates before its new version runs (Epic 54, 54.07 §5) |
| 0799-rollback-check | 2026-11-07 | pr | An inline CHECK made 0209 destructive, so the wave rehearsal restored the dump |
| 0800-wave5-convert-dedup | 2026-11-07 | pr | Converting, duplicates and the file trash run as Maktaba's built-in plugins (Epic 54 wave 5, 54.32) |
| 0801-plugins-metrics-report-logs | 2026-11-07 | pr | Plugin job metrics, and problem reports keep plugin logs out (Epic 54, 54.09 §3, §4) |
| 0802-python-logger-cost | 2026-11-07 | pr | A Python log call from 19.5 to 11.5 µs, and the bench's runner factor measured |
| 0803-ipad-waits | 2026-11-07 | pr | iPad Player UI waits count looks as well as seconds, and wait for Home to settle |
| 0806-wave6-youtube-repair | 2026-11-07 | pr | YouTube and the repair of broken downloads run as Maktaba's built-in plugins (Epic 54 wave 6, 54.33) |
| 0808-direct-remote-cloud | 2026-11-07 | pr | Direct remote access, the cloud's side (part 1) |
| 0810-plugins-cli-dev-mkp-run | 2026-11-07 | pr | Plugins from the command line, developer mode and mkp run (Epic 54, 54.10 §4-5, 54.12) |
| 0811-ar-pack-no-analyser | 2026-11-07 | pr | The Arabic pack's "no analyser" test hides CAMeL Tools itself |
| 0813-apple-panel-spacing | 2026-11-07 | pr | Apple pages add From plugins only when they have a panel (Epic 54, story 54.20) |
| 0814-plugin-state-without-pipeline | 2026-11-07 | pr | A plugin's state shows while the pipeline is down (Epic 54, 54.07 §8) |
| 0815-plugin-store-browser | 2026-11-07 | pr | Plugins Phase 2 part 5, the store browser, listing pages, ratings and reports (Epic 54, 54.18) |
| 0818-rehearsal-15-chaos-plugins | 2026-11-07 | pr | Update-rehearsal scenario 15 and plugin stages in components-chaos (Epic 54, 54.07 T09, T10, T14) |
| 0819-release-embeds-plugin-lists | 2026-11-07 | pr | Each release embeds the plugin store's current lists, verified (Epic 54, #755) |
| 0820-store-runner-containers | 2026-11-07 | pr | The plugin store's isolated runner on Cloudflare Containers (Epic 54, #756) |
| 0821-tests-runner-dir-tmp | 2026-11-07 | pr | Plugin runner areas go to /tmp in tests, never the checkout |
| 0822-copy-excluded-keeps | 2026-11-07 | pr | migration-lint: cluster_copy_excluded() keeps every table |
| 0823-wave7-livetv | 2026-11-07 | pr | Live TV's channel planning and TV changes run as Maktaba's built-in plugins (Epic 54 wave 7, 54.34) |
| 0824-reports-to-maktaba | 2026-11-07 | pr | Problem reports to Maktaba, when the home allows it and the sender chooses it |
| 0825-feature-any | 2026-11-07 | pr | feature_any, a feature's groups where any member is enough (story 50.35) |
| 0826-merge-main-changelogs | 2026-11-07 | pr | tools/dev/merge-main.sh keeps both sides of the CHANGELOGs |
| 0828-log-bench-best-of-three | 2026-11-07 | pr | The Python logger bench is judged on the best of three rounds |
| 0832-plans-free-basic-pro | 2026-11-07 | pr | Plans Free, Basic and Pro; relay modes; usage by mode (story 50.19, part 1) |
| 0833-firstparty-import-cycle | 2026-11-07 | pr | The first-party runners start again (no import cycle) |
| 0834-go-bench-rounds | 2026-11-07 | pr | The Go logger bench is judged on the best of three rounds too |
| 0835-browse-draws-flows | 2026-11-07 | pr | The store browser draws a plugin's flow; the store draws with the shared chart (Epic 54, 54.18, 54.41) |
| 0836-listing-open-at-home | 2026-11-07 | pr | Open in my Maktaba without an app, at the home's own address (Epic 54, 54.18, #757) |
| 0837-report-logs | 2026-11-07 | pr | A plugin report can carry the plugin's own logs, for the store's reviewers (Epic 54, 54.18, #758) |
| 0838-log-bench-tail | 2026-11-07 | pr | The log bench's budget follows the runner's tail; the logger's context merge and chain cost less |
| 0839-load-small-same-runner | 2026-11-07 | pr | load-small compares the relay with its base on the same runner |
| 0841-mapstore-concurrent | 2026-11-07 | pr | The Android tests' in-memory store is concurrent |
| 0842-rehearsal-waves | 2026-11-07 | pr | The update rehearsal runs Epic 54's waves across an update (scenarios 16.1-16.3) |
| 0843-runner-socket-realpath | 2026-11-07 | pr | A runner binds its socket through a linked folder (macOS) |
| 0844-wave8-plex-door | 2026-11-07 | pr | Plex runs as Maktaba's built-in plugin, through the home's service door (Epic 54 wave 8, 54.35) |
| 0845-direct-remote-home | 2026-11-07 | pr | Direct remote access, the home's side (story 50.15, part 2) |
| 0846-merge-main-blank-lines | 2026-11-07 | pr | merge-main.sh merges when main adds more entries than the branch |
| 0847-scan-on-the-engine | 2026-11-07 | pr | A folder scan is a queue stage like the others (Epic 54 wave 9, step 1, 54.36 §2) |
| 0848-sdk-python-js-templates | 2026-11-07 | pr | Python and JavaScript plugin templates (mkp new, build, world) |
| 0849-relay-enforces-plans | 2026-11-07 | pr | The relay enforces each plan (story 50.19, part 2) |
| 0850-buy-paid-plugin | 2026-11-07 | pr | Buying a paid plugin (Epic 54, story 54.19 §5) |
| 0851-plugind-go-exempt | 2026-11-07 | pr | The plugind job's Go setup is exempt from the module-download rule |
| 0851-sdk-go-template | 2026-11-07 | pr | The Go plugin template (TinyGo wasm-unknown) |
| 0852-cloud-logs | 2026-11-07 | pr | The cloud's logs, errors and metrics on the home's model, feeding Epic 50 |
| 0854-payg-extra-streams | 2026-11-07 | pr | Pay as you go and extra streams on Pro, usage reports (story 50.19, part 3) |
| 0856-plans-home-side | 2026-11-07 | pr | The home's side of the plans: cap, basic licenses, the apps' words (story 50.19, part 4) |
| 0858-integration-tier-waste | 2026-11-07 | pr | The integration tier's waste cut, and its slowest tests in the log |
| 0859-runners-on-demand | 2026-11-07 | pr | Maktaba's built-in plugin runners start only when they have work (the Core budget) |
| 0860-log-bench-structlog-factor | 2026-11-07 | pr | The Python log bench's factor follows structlog's own p99 |
| 0861-paid-packages-private | 2026-11-07 | pr | Paid plugins' packages by signed URL only (Epic 54, story 54.19 §6) |
| 0863-plugins-windows-relay | 2026-11-07 | pr | The pipeline's relay to the plugin host on Windows (#710, 1 of 2) |
| 0864-fakes-ignore-strays | 2026-11-07 | pr | The api tests' fake servers ignore a stray request |
| 0865-relay-plans-rehearsal | 2026-11-07 | pr | The relay-plans rehearsal (story 50.19 T13) |
| 0866-log-bench-vs-base | 2026-11-07 | pr | The Python log bench is judged beside its base on the same runner |
| 0867-cloudflare-deploys | 2026-11-07 | pr | Cloudflare deploys from CI only (Epic 50, 50.34) |
| 0868-sqlite-thread-at-exit | 2026-11-07 | pr | A closed SQLite database waits for aiosqlite's thread |
| 0870-library-line-wait | 2026-11-07 | pr | The hub's queues/library test waits for the library line |
| 0871-console-own-bundle | 2026-11-07 | pr | The operator console is its own bundle (story 50.32, part 1) |
| 0873-cloudflare-edge-rules | 2026-11-07 | pr | Cloudflare in front of the cloud (Epic 50, 50.30) |
| 0874-pr-closing-refs | 2026-11-07 | pr | A PR body must not close another open pull request |
| 0877-dns-role-deploy | 2026-11-07 | pr | The dns role on nodes A and B (story 50.15, part 3a) |
| 0879-plugins-on-windows | 2026-11-07 | pr | Plugins on Windows (the host in its AppContainer, the relay) |
| 0881-direct-frames-flake | 2026-11-07 | pr | A flake in TestTheAgentCarriesTheDirectFrames (from #845) |
| 0882-worker-double-listen | 2026-11-07 | pr | The cloud worker exited at start since June (two listeners on its control port) |
| 0883-origin-via-edge | 2026-11-07 | pr | The origin only through Cloudflare, and the trusted client IP (Epic 50, 50.31) |
| 0886-purchase-move | 2026-11-07 | pr | A purchase moves to another home, twice a year |
| 0887-buy-on-store-site | 2026-11-07 | pr | Buy on plugins.maktaba.tv, by choosing a linked home |
| 0888-request-id-jobs-playback | 2026-11-07 | pr | Epic 53 story 53.08's leftovers — a request's id on its jobs and its playlists |
| 0890-runner-job-scope | 2026-11-07 | pr | A runner file job moves only its own subject's files (54.32 §2, T02) |
| 0891-server-log-warn | 2026-11-07 | pr | server.log keeps the roles' warnings and errors only (Epic 53 story 53.02's rollout) |
| 0892-direct-remote-rehearsal | 2026-11-07 | pr | The direct-remote rehearsal passes, and the two bugs that kept direct access from ever running |
| 0893-apple-page-snapshots | 2026-11-07 | pr | The Apple Player's pages compared with references |
| 0895-cloud-binary-never-installed | 2026-11-07 | pr | Deploys never installed the binary the api and worker run |
| 0896-queue-processing-series | 2026-11-07 | pr | Epic 53 story 53.12 — the Queue, Processing and Streaming boards get real series, the Overview the API's p95; /metrics serves the plugins' metrics |
| 0897-lan-entries | 2026-11-07 | pr | The home list's LAN entries, by name and by address pinned to the home's name |
| 0899-youtube-hosts-nightly | 2026-11-07 | pr | A nightly run checks the hosts yt-dlp contacts for the YouTube plugin (54.33 T02) |
| 0902-test-run-toy-role | 2026-11-07 | pr | A test passed only when an earlier run had left its Postgres role behind |
| 0903-apps-local-ip | 2026-11-07 | pr | The apps use the home list's local_ip entries, pinned to the home's name |
| 0904-log-bench-compare-flake | 2026-11-07 | pr | The log bench's compare test checks both rounds ran, not their speed |
| 0905-host-metrics | 2026-11-07 | pr | Epic 53 story 53.12 — each machine's memory, disk free and load on the Overview |
| 0906-rebinding-notice | 2026-11-07 | pr | The home's settings say when the router blocks its LAN name |
| 0907-cloud-status-recheck | 2026-11-07 | pr | The apps read the cloud's status at once when the relay fails |
| 0910-page-locale | 2026-11-07 | pr | A page's built strings follow its locale; the page references' bundle trap |
| 0911-specs-admin-urls | 2026-11-07 | pr | Specs 53.16 and 53.17 name the operator console's URLs (admin.maktaba.tv) |
| 0915-joined-machines-dashboards | 2026-11-07 | pr | Epic 53 story 53.12 — the home's dashboards carry every joined machine |
| 0917-symbolication | 2026-11-07 | pr | Epic 53 story 53.16 — Apple crash reports symbolicated on the cloud |
| 0918-remote-recovery | 2026-11-07 | pr | Epic 53 — native recovery screens in the Apple and Android Remotes |
| 0919-probe-on-the-engine | 2026-11-07 | pr | Reading a video's length and audio tracks is a queue stage like the others (Epic 54 wave 9, step 2, 54.36 §2) |
| 0920-thumbnail-on-the-engine | 2026-11-07 | pr | Making a video's poster and sprite is a queue stage like the others (Epic 54 wave 9, step 4, 54.36 §2) |
| 0921-extract-on-the-engine | 2026-11-07 | pr | Pulling a video's audio for transcription is a queue stage like the others (Epic 54 wave 9, step 5, 54.36 §2) |
| 0922-origin-rehearsal | 2026-11-07 | pr | The origin-only-via-edge and origin-aop rehearsals (Epic 50, 50.31, T06 to T08) |
| 0927-aop-deny-first | 2026-11-07 | pr | The fallback origin's deny rule for addresses outside Cloudflare's ranges never ran (50.31) |
| 0928-admin-listener | 2026-11-07 | pr | The operator console on its own listener and name |
| 0929-page-text-references | 2026-11-07 | pr | The Apple page snapshots check the page's words too |
| 0930-witness-token-first | 2026-11-07 | pr | The witness's token check never ran (50.11), and one deny-first check for every Caddyfile |
| 0932-caddy-deny-first-lint | 2026-11-07 | pr | A CI check that keeps every Caddyfile from hiding a deny behind its handle blocks |
| 0934-convert-parity-mtime | 2026-11-07 | pr | The convert parity compared fingerprints that hash when each side ran |
| 0945-cluster-windows-hang-dump | 2026-11-07 | pr | A hung pipeline test prints its stack; the cluster tests' waits are bounded |
| d20261107-852-admin-console-move | 2026-11-07 | migration | 53.17's cloud Logs and Errors moved to the separate operator console when main came in |
| d20261107-closing-keyword-closed-a-pr | 2026-11-07 | incident | A PR body's "resolved" closed another open pull request |
| d20261107-correction-0685-player-sentence | 2026-11-07 | correction | Correction to 0685 — the Apple player already has its own sentence when streaming is down |
| d20261107-orphaned-cpu-load | 2026-11-07 | incident | Sixteen orphaned `yes` processes held the owner's Mac for three days and crashed WindowServer |
| d20261107-paid-plugins-merge-problems | 2026-11-07 | incident | Problems on the way to merging the paid-plugin PRs (#781, #783, #787, #813) |
| d20261107-plugin-stream-takeover | 2026-11-07 | incident | Taking over the plugin stream after the weekly limit |
| d20261107-rehearsal-venv-codeql | 2026-11-07 | incident | CodeQL flagged the rehearsal's venv cache as cache poisoning; dismissed as before |
| d20261107-relay-test-failed-mypy | 2026-11-07 | incident | The relay PR failed lint on mypy errors its push never checked |
| d20261107-restore-vector-index | 2026-11-07 | decision | A restore builds the vector index alone, with memory of its own |
| d20261107-rollback-down-scripts | 2026-11-07 | decision | A rollback runs the release's Down scripts, the dump only as a last resort |
| d20261107-server-log-warn-e2e | 2026-11-07 | incident | server.log at warn+ hid the info lines the e2e harnesses read |
| d20261107-stacked-pr-merged-into-its-base | 2026-11-07 | incident | A stacked PR merged into its base branch, not main |
| d20261107-store-stream-merges | 2026-11-07 | incident | Landing #867, #873 and #883 after main's Core bundle check went green |
| d20261107-test-infra-missteps | 2026-11-07 | incident | The test-infrastructure work's missteps on 2026-11-07, and what each taught |
| d20261107-update-watch-rollback | 2026-11-07 | incident | 0.3.90 rolled back — the update watch timed out on a busy pipeline |
| d20261107-watcher-check-name | 2026-11-07 | incident | Two merge watchers for PR 859 could never merge it: they matched the check's name without its workflow prefix |
| d20261107-watcher-merged-early | 2026-11-07 | incident | A merge-on-green watcher merged four PRs with their checks still running |
| 0925-wave4-classify | 2026-11-08 | pr | Epic 54 wave 4 moves the rest of classify into tv.maktaba.classify, and enrich stays in the worker |
| 0936-panel-snapshots | 2026-11-08 | pr | Plugin panel snapshots on Android TV, Apple TV and the web found four real bugs (54.20 T05) |
| 0937-plugin-scope-canonical-uuid | 2026-11-08 | pr | A plugin could write another library's video by spelling its id differently |
| 0938-rehearsal-16-8-plex | 2026-11-08 | pr | Rehearsal 16.8 turns the Plex extension on as a real home does |
| 0939-fip-retry-leak | 2026-11-08 | pr | The Floating IP retry stops when the node no longer leads; a test leaked it for a day |
| 0941-cloud-web-workers | 2026-11-08 | pr | The app and console Workers on Workers Routes |
| 0942-dizquetv-door | 2026-11-08 | pr | dizqueTV runs as a first-party plugin, with programmes built on the home (Epic 54, issue #827) |
| 0944-pins-per-home | 2026-11-08 | pr | Each home's LAN address pins stay with that home |
| 0948-dizquetv-door-reads-plex | 2026-11-08 | pr | The service door reads dizqueTV programmes' playable fields from Plex itself (issue #827) |
| 0949-migration-lint-goose-annotations | 2026-11-08 | pr | migration-lint refuses goose annotations out of order |
| 0952-android-tombstones | 2026-11-08 | pr | Android native crashes keep the crashed thread's frames (story 53.14) |
| 0953-runner-start-wait | 2026-11-08 | pr | The first jobs after an update ran in the worker beside a runner that was starting |
| 0954-spec-label-caps | 2026-11-08 | pr | Spec 53.12 states the label caps as built |
| 0956-runners-on-windows | 2026-11-08 | pr | First-party plugin runners on Windows, each group in its own AppContainer (story 54.23) |
| 0957-symbols-release-upload | 2026-11-08 | pr | 53.16: the Apple release uploads symbol tables; the cloud deploy provisions their token |
| 0959-remote-restore-diagnostics | 2026-11-08 | pr | The native Remotes restore a backup and download diagnostics |
| 0960-overview-many-machines | 2026-11-08 | pr | 53.12: the Overview dashboard scales with many machines |
| 0961-android-desktop-symbols | 2026-11-08 | pr | 53.16: Android R8 mappings, desktop stacks, and rehearsal T07 |
| 0962-failed-runners-reported | 2026-11-08 | decision | A home reports a failed plugin runner, to admins only, with the stages that run in the worker |
| 0963-snapshot-scrollbar | 2026-11-08 | pr | The Mac page snapshots no longer compare the scroll bar |
| 0966-grants-by-handle | 2026-11-08 | pr | A Windows runner's grant or revoke lands only on the folder that was checked (story 54.23) |
| 0967-android-native-symbols | 2026-11-08 | pr | 53.16: Android native crash frames symbolicated |
| 0968-share-broker | 2026-11-08 | pr | A Windows runner's library on an SMB share through its launcher's handle broker (story 54.23) |
| 0970-overview-offline-first | 2026-11-08 | pr | 53.12: an offline machine ranks first on the Overview |
| 0971-testboards-budget | 2026-11-08 | pr | TestBoards outgrew the unit test-time budget |
| 0974-share-tools | 2026-11-08 | pr | A Windows runner's ffmpeg and ffprobe read one share file through a brokered handle (story 54.23) |
| 0976-windows-stages-in-worker | 2026-11-08 | pr | First-party stages stayed in the worker on Windows until tools started through anonymous pipes (story 54.23) |
| 0977-runner-tool-pipes | 2026-11-08 | pr | A Windows runner starts its tools through anonymous pipes (story 54.23) |
| 0978-iphone-recovery-shots | 2026-11-08 | pr | maktaba.dev shows the iPhone Remote's recovery screen |
| 0979-epic57-parental-controls | 2026-11-08 | decision | Epic 57 spec: parental controls on iPhone and iPad, on Apple's Screen Time |
| 0983-overview-machine-names | 2026-11-08 | pr | 53.12: the Overview names machines; lighter 60-machine tests |
| 0986-runner-stages-windows-e2e | 2026-11-08 | pr | First-party stages end to end in their Windows runners; the venv launcher was the share miss (story 54.23) |
| 0988-share-refusal-not-missing | 2026-11-08 | pr | A share file a runner's sandbox refuses runs in the worker, never as missing (story 54.23) |
| 0990-share-media-in-worker | 2026-11-08 | pr | Share media stays in the worker on Windows; v0.3.105's misses run again (story 54.23) |
| 0992-record-prepared-statements | 2026-11-08 | pr | 53.12: Record prepares its upserts once per scrape; TestOverviewManyMachines under its soft cap |
| 0993-android-lenient-overview | 2026-11-08 | pr | 53.12: the Android Remote decodes the Overview's optional fields leniently |
| 0994-symbols-table-type | 2026-11-08 | pr | 53.16: the Crash symbols page names each table's type and key |
| 0996-classify-parity-fast | 2026-11-08 | pr | Classify's parity proof ran past the soft cap and failed unrelated pull requests |
| 0997-fingerprint-refusal-answered | 2026-11-08 | pr | A fingerprint a Windows runner refuses is answered, and the worker takes it (story 54.23) |
| 0998-wave9f-core-media | 2026-11-08 | pr | Wave 9f moves the core stages into tv.maktaba.core-media |
| 1000-remote-ui-still-row | 2026-11-08 | pr | The Remote's diagnostics UI tests tap a Home row once it stands still |
| 1001-player-ui-warmup-audits | 2026-11-08 | pr | Player UI warm-up audits until the process is warm |
| 1002-shared-step-report-order | 2026-11-08 | pr | A process waiting on the venv showed the waiting text over the install's progress |
| 1003-pinned-downloads-api-first | 2026-11-08 | pr | Pinned downloads by the GitHub API first, retried and cached |
| 1005-write-scope-every-id | 2026-11-08 | pr | Every first-party write checks each id that binds its rows (README §15.12) |
| 1007-resilient-runtime-downloads | 2026-11-08 | pr | A home's Postgres download survives github.com's download door failing |
| 1010-single-vps-symbols-token | 2026-11-08 | pr | The single VPS's deploy writes the symbol uploaders' token |
| 1014-ci-downloads-survive-504 | 2026-11-08 | pr | CI's remaining downloads survive github.com's 504s (golangci-lint, archives, caches, e2e, Gradle, WiX) |
| 1018-runner-identity | 2026-11-08 | pr | A Windows runner is the process its launcher created, or it does not serve (story 54.23) |
| 1019-share-runners | 2026-11-08 | pr | Thumbnails, chapter frames and subtitles run in their runner on a Windows share; the broker serves full-write shares read-only (story 54.23) |
| 1020-trail-events-own-prs | 2026-11-08 | pr | Trail events go in their own trail-only pull requests (CLAUDE.md rule 2, AGENTS.md) |
| 1021-publish-rules | 2026-11-08 | pr | The worker checks each event a plugin's runner asks it to publish |
| 1025-read-scope-every-id | 2026-11-08 | pr | Every plugin-API read by id is checked against the job (README §15.12) |
| 1032-rehearsal-12-retired | 2026-11-08 | pr | Update rehearsal scenario 12 expects a retired component to have no row |
| 1034-appimage-runtime-pin | 2026-11-08 | pr | The AppImage runtime pinned and handed to appimagetool; linuxdeploy's and appimagetool's output in the log |
| 1036-cache-budget | 2026-11-08 | pr | One Go cache entry per runner and Go version, free cache cuts, and a prune of stale entries |
| 1038-epic58-smart-tv-platforms | 2026-11-08 | decision | Epic 58 spec: apps for the TVs Maktaba does not reach yet (Fire TV, Google Cast, Samsung, LG, Roku) |
| 1041-rehearsal-16-9-core-stages | 2026-11-08 | pr | Rehearsal 16.9 takes wave 9's core stages through a rollback and an update |
| 1047-restore-tests-disk-room | 2026-11-08 | pr | The restore's tests no longer depend on the disk's free space |
| 1048-emit-refusal-log | 2026-11-08 | pr | Every refused plugin event emit is logged with its plugin, kind, size and reason |
| 1049-dev-api-per-session | 2026-11-08 | pr | Each test session builds and runs its own plugin-API dev server |
| 1050-compute-metrics-lanes | 2026-11-08 | pr | compute_metrics runs 4 to 7 times faster with exactly the same outputs |
| 1053-rehearsal-rows-every-stage | 2026-11-08 | pr | Every 16.x rehearsal row has a job of each of its stages in every round |
| 1057-ci-paths-firstparty-imports | 2026-11-08 | pr | The first-party proofs run for the pipeline code their stages import |
| 1059-n16-per-call-cost | 2026-11-08 | pr | N16 states the plugin path's per-call cost and judges CPU and paired wall time |
| d20261108-android-native-symbols-path | 2026-11-08 | decision | The Play workflow's native-symbols path is right on AGP 9.4; nothing to change |
| d20261108-backups-tidy | 2026-11-08 | incident | Old backups were pruned only at an update, and a lone failed dump stayed for good |
| d20261108-ci-cache-eviction-linuxdeploy | 2026-11-08 | incident | After #1014, a pinned tool's cache was evicted within an hour, and the aarch64 AppImage bundle failed once in linuxdeploy |
| d20261108-codeql-bundle-504 | 2026-11-08 | incident | CodeQL's bundle download failed with HTTP 504 and held every PR for about an hour |
| d20261108-login-item-relaunch | 2026-11-08 | incident | The Mac update to 0.3.96 left the server stopped behind a login item macOS would not re-register |
| d20261108-rehearsal-matrix-skew | 2026-11-08 | incident | A rehearsal of an older commit ran a scenario that commit does not have |
| d20261108-remove-plex-sync | 2026-11-08 | decision | Plex sync and dizqueTV removed; Plex Live TV stays |
| d20261108-runners-wait-venv | 2026-11-08 | incident | The 0.3.96 plugin runners started before the venv and crash-looped |
| d20261108-snapshot-fixture-clock | 2026-11-08 | incident | The Mac page snapshots took their fixture dates from the clock |
| 1027-staff-signin-unstalled | 2026-11-09 | pr | Staff sign-in for the operator console, unstalled and merged |
| 1062-moved-stages-path-map | 2026-11-09 | pr | Moved stages open the file through the job's path map |
| 1066-n16-report-per-test | 2026-11-09 | pr | Each N16 report belongs to one test; classify's wave 4 writes its own |
| 1067-staff-passkeys | 2026-11-09 | pr | Staff passkeys for the operator console (story 50.33, part 2) |
| 1068-core-media-parity-cap | 2026-11-09 | pr | core-media's parity fits its soft cap; every parity test starts faster |
| 1074-impl-pin-update-flip | 2026-11-09 | pr | A stage's impl asked while unchanged is kept through the update's flip (rehearsal 16.7) |

