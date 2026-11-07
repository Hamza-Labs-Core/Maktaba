---
schema: 1
tag: v0.3.88
up_to_commit: 8d2b59e2b
built_from: v0.3.87
---

# Trail snapshot: v0.3.88

## Keep doing

**Working**
- Run the PR flow to the merge without asking; ask only for real product decisions (costly backfill, deleting data). Follow-ups start at once as parallel worktree agents. (d20261031-standing-rules, d20261102-coordinator-rules)
- Verify every claim yourself before acting on it, and check a spec's claims against the code. (d20261014-pc-dizquetv-origin-and-rules, d20261031-standing-rules, #587)
- Build tools for the work instead of doing it by hand; a recurring manual step becomes a feature; rules are settings with a UI, not code. (d20261014-pc-dizquetv-origin-and-rules, d20261020-pc-coordinator-merge-rules)
- No placeholder features: a feature is built or it is not shown. (#128, d20261014-pc-dizquetv-origin-and-rules)
- Review every PR yourself; the Copilot reviewer check and its quota comment are noise. Merge with a merge commit on the reviewed head, every check present and green (main has no branch protection). (d20261031-standing-rules, d20261020-pc-coordinator-merge-rules, #314, #212)
- Fold small Apple fixes into one integration PR; fix agents push branches, not PRs. (#539)
- Keep worktrees under `.claude/worktrees/`, never `$TMPDIR`; remove merged worktrees and branches. (d20261031-standing-rules, d20261102-worktree-cleanup)
- Branch stacks point HOME, `MAKTABA_CLUSTER_DIR` and `MAKTABA_ARCHIVE_DIR` at scratch folders. (#621, #623)
- Use only real PR numbers and shas; attribute history with git (blame, `log -L`, PR file lists). (d20261031-standing-rules, #655)
- Never quote or attribute the owner's words in repo text; state the requirement neutrally. (#603, #604)
- Write each PR's event in the PR before it merges; post-merge learnings go in a trail-only PR. Build snapshots in a scratchpad worktree off origin/main and run `trail check` before pushing; check each carried Open item against newer events, and check origin/main's `snapshots/` again right before opening the PR so two sessions do not snapshot the same tag. On a trail-only PR wait for CodeQL (the slowest job), not the lint. The trail's checks are the agent-trail CLI (`uvx --from git+https://github.com/Hamza-Labs-Core/agent-trail@v0 trail check`); `tools/trail/trail.py` is gone. (#655, #532, #706, #726, #739)
- Keep CI's paths-ignore, `next_tag.py`'s TRAIL_ONLY and `.trail.toml`'s `[trail_only]` paths equal (test_ci_paths.py and test_next_tag.py check it). (#702, #699)

**Releases and live**
- Prove update, rollback, restore and migration paths in CI first: nothing touching update, restore, migration, supervisor, launcher, front door or TV-staging code ships unless `update-rehearsal` passed on that exact commit. One release at a time on the live install. (d20261031-standing-rules, #435, #433, #460, #461)
- Tags are pushed by the scheduled release session with a person's credentials (`next_tag.py --green`): the newest main commit whose CI and rehearsal both concluded `success`, as the next patch; no workflow tags (`auto-tag.yml` is removed). Trail-only commits are never tagged; check remote tags before picking a version. A tag workflow missing a leg is re-run (release.yml) or dispatched by hand on its tag (the others); release.yml is never dispatched. (#730, #725, #699, #563, #553, d20261031-release-history-to-v0344, d20261020-pc-coordinator-merge-rules)
- Keep the Plex token on the home only; nodes go through the home. Keep secrets out of output, commits and exports; only the owner types credentials. (#78, d20261031-standing-rules, d20261020-pc-coordinator-merge-rules)
- Use live data only for justified recovery: tests use scratch Postgres; pause stages through `POST /api/queues/{stage}/pause`, never raw SQL. (d20261031-standing-rules, #524)
- On the PC: restart services only through their sign-in tasks; check an installer's sha256, then the owner runs it. Prove Windows behaviour on CI's runner, not the owner's PC. (d20261025-pc-windows-app-node, #457)
- Prefer keyless federation over downloaded service-account keys. (#557)
- No Stripe or Stripe-owned services for any payment, in specs or code; payment providers are pluggable (`PaymentProvider`, each in its own folder), Adyen for Platforms the first real one. Check a brief's suggestion against the trail before building it. (#591, #593, #735)

**Tests and CI**
- A code change ships with its tests changed in the same push (affected-tests gate; `No-Test-Change: <reason>` only when the mapped tests cannot run locally). (#516, d20261031-standing-rules, #685)
- Fix flaky tests at the root; read the log first, a flaky-looking test is often a real bug (in Epic 53's series as many failures were real as were flakes; a flaky TempDir cleanup was a shutdown bug). A UI failure is the change's only if the same code fails the shard elsewhere and the failing test does not move between runs. Before calling a UI test a flake, read what it waits for: a self-dismissing element checked after slower queries races a loaded simulator; fix the order, not the timeout. Meet test budgets at the cause, never by raising caps or splitting tests. (d20261031-standing-rules, #364, #635, #271, #716, d20261106-epic53-ci-flakes, #673)
- Turn a "should never happen" rule into a test-time error and run the whole suite under it. (#662)
- Verify each new test fails without its fix (origin/main's sources on a WIP commit). Latch tests that hold a read's answer reproduce read/write races every time. (#679, #613, #667)
- Jobs run only when their code changes; run the same check on every PR that the release tag runs. (#528, #519)
- Tests get a server's ports from the server once open; tests bind 127.0.0.1. (#571, #451, #127)
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
- Check a guide written before its code against the code (the WIT, `capabilityOf`), not the spec. (#704)
- A PR that ships user-facing work updates maktaba.dev in the same PR, in English and Arabic (`devportal_follows.py` warns; `No-Portal-Change:` says it needs nothing); run `npm run build`, `npm run scan` and `npm test` in `web/packages/devportal` before pushing. (#703, #723)
- Before changing a log format or adding a fixed loopback port, grep the e2e tests: they match `key=value` output and template `server.toml` for two machines on one host. (#673, #686)
- Calibrate a performance budget with work like the work budgeted; a "share of a core" budget is CPU time, not wall time. A timing assertion times only the thing it is about (the runaway-job test counted guest compile time). (#690, #738, #694, #696)
- Measure a flaky suite under real load before and after (three concurrent full copies on the Mac reproduce CI's web failures every time) and report both counts; profile a slow test (`--pool=forks --execArgv=--cpu-prof`): most of its time was page code. (#746)
- Web: window key handlers that read render state use `useWindowKeydown`; formatters made in render come from `lib/intl`. (#746, #698)
- Write a cross-language check once as an algorithm with a deterministic output, and share test vectors across Go, Rust and TypeScript (Ed25519 is deterministic, so a fixed seed suffices). (#751, #733, #743)
- Chain a workflow that must agree with CI on a commit after CI (`workflow_run` on success) rather than running both beside each other on main. (#737)
- After editing a workflow, load every workflow with PyYAML (`tools/ci/tests`): actionlint passes a step `name:` containing ": ". (#727)
- A rehearsal check reads the previous release's state before the update instead of keeping a hand list or comparing versions, which break each time the previous release moves. (#748)
- When an earlier PR says something is reachable, check that something actually serves it. On an RTL page check the Arabic at phone width before pushing. (#739)

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
- Adding CHANGELOG entries at the top of a section; the end conflicts less. (#673, #659)
- Picking a port without reading the port registry. (#686, #440)
- Chaining `go test … | tail && git commit`: the pipeline's status is tail's, so a failing test still committed. (#705)
- `git add -A` while `make compat-prev` runs: it writes `api/internal/compatprobe/main.go` for the run; commit only named paths. (#707)
- `git reset --soft origin/main` to squash after a fetch: a moved origin/main silently reverted three merged PRs. Reset to the branch's own base sha. (#701)
- Naming a stale host in backticks in prose (CHANGELOG, docs): the foreign-domain lint flags it; write the name out of the text. (#700)

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

**CI and releases**
- Homes' `releaseverify` accepts only a release whose Sigstore certificate's trigger (OID 1.3.6.1.4.1.57264.1.20) is `push`; a dispatched release differs in that claim alone and is refused, and a re-run of a dispatched run stays `workflow_dispatch`. A tag pushed with `GITHUB_TOKEN` starts no run, so tags come from a session's push; v0.3.83 to v0.3.85 have no release, so v0.3.86 is the first published release of their components (`since` says 0.3.86). (#725, #730, #748)
- On main the update rehearsal is a `workflow_run` of CI's successful push run; a `workflow_run` run's `head_sha` is main's head when it started, not the commit it rehearsed. The rehearsed commit is in the run name `Update rehearsal of <sha>` (and its commit status); `tag-guard.sh` and `next_tag.py` read it there. Before this, two separate main concurrency groups finished on different commits and nothing was tagged for a day. (#737)
- A push with `GITHUB_TOKEN` starts no workflow, but `gh workflow run` with it does (`actions: write`). GitHub Pages sends `max-age=600`; fresher clients add a per-minute query. Probe data lives on a data branch (`status-data`), not main. (#739)
- CodeQL reads test code too (a `<script>`-stripping regexp is a high finding), and a flagged line in a PR's diff is fixed in that PR. (#739)
- A run green except for the ignored iPhone/iPad shards is not tagged; web-release.yml is not a tag workflow. (#699)
- CI builds the Desktop Player on x64 only, so arm-only native-library gaps show first at release; reproduce with `docker --platform linux/arm64 ubuntu:22.04` and `ldd -r`. (#725)
- A public repository cannot use an Action from a private one; GitHub shows a workflow that does not parse by its path and starts no run. (#702)
- With one concurrency group GitHub keeps one running and one pending run; cancel-in-progress on busy main starved tagging, so it is PR-only. (#547, #563)
- A job that `needs` a skipped job is skipped unless it says `!cancelled()`. (#528)
- Under `pipefail` a reader that exits early (`head`, `grep -q`) can SIGPIPE the writer; capture first (`out="$(cmd)"; grep -q x <<<"$out"`). `go test | tail` hides a failing exit. (#658, #675, #166, #271, #684)
- A heavy import for a small helper pulls a module graph into every run; keep helpers stdlib-only and prove it with `GOPROXY=off`. (#533, #546)
- PR caches can evict main's from the 10 GB Actions cache. (#615, #618)
- A plain CI re-run reuses the old merge ref; merge main and push. (#166)
- promtool unit tests compare annotations too; a missing sample (`_`) is not a failed scrape (`stale` is); an SLO over several probes takes the best probe before the burn rate. A `mktemp -d` folder is 0700 and unreadable to an image running as `nobody` on Linux CI, hidden by Docker Desktop; chmod it before mounting. (#727)
- macOS bash 3.2 treats an empty `"${arr[@]}"` as unbound under `set -u`; use `${arr[@]+"${arr[@]}"}`. (#739)
- Release fixtures built for an old state owner break on the next push after the change ships; drive state the way the owner does. (#656)
- Mac notarization breaks on empty `APPLE_*` variables; local builds carry a placeholder updater key. (#276, #288)
- TestFlight: the API cannot create app records (Xcode Organizer can; its error after a 201 is harmless); the CI key needs Admin. An unsigned archive has no entitlements; a single-size macOS icon is ignored. (#447, #583)
- Android: names live in `res/values*/strings.xml`, not `resValue`; App Store names must be unique ("Maktaba Remote"). (#564, #560)

**Database and migrations**
- Number a migration slot only when merging main; a slot above a gap fails migration-lint; `SchemaRev` lives in three places. (#669, #692, #678, #672, #657, #693)
- A new table keeps `maktaba_worker`'s default grants (compat-prev) and refuses joined machines' writes with a `BEFORE` trigger; it must be decided for the node copy (`copyReviewed` or `cluster_copy_excluded()`). A new failure cause needs its label and help; a new pool needs its `worker_pools` seed. (#663, #692, #693)
- The SQLite migration runner rewrites `ADD COLUMN IF NOT EXISTS` only when it is the one statement of its `StatementBegin` block. (#733)
- Cloud migrations are numbered by epic slot; goose refuses a version below the head, so `Migrator.Up` passes `goose.WithAllowMissing()`. (#700)
- `job_stages.stage` is capped at 40 characters; the SQLite sibling cannot widen an inline check without rebuilding the table. (#693)
- `ADD CONSTRAINT` makes a release unsafe to roll back; use a trigger. Migrations run twice in integration: `DROP CONSTRAINT IF EXISTS` first. (#524, #669)
- Lock-order comparisons are row comparisons, not joined strings (collation ignores punctuation); enqueue after commit to avoid deadlocks. (#662, #678)
- Add up every program's pool maximum against `max_connections`. (#627)
- lib/pq bounds only the dial; older lib/pq ignores the passfile on Windows. (#608, #362)
- lib/pq does not stop a connection's startup when the context ends: against a vanished host (killed container, old failover address) a connect hangs until `connect_timeout` (`db.Open` adds `connect_timeout=2`). A killed container is not a refused connection. (#714, #715)
- Triggers are the reliable way to NOTIFY: they cover every writer. (#714)
- Patroni: a partitioned primary demotes only after its WAL senders give up (`wal_sender_timeout: 10s` plus a `/primary` guard); a commit cancelled while waiting for the sync standby is reported done but lost on failover, so RPO 0 holds only for uncancelled commits. A restored Patroni database names the old node's `hba_file`. (#721, #724)
- pgBackRest `backup` writes one repository per run (loop `--repo=1`, `--repo=2`); `archive-push` writes all. MinIO images are gone; SeaweedFS's S3 gateway stands in once a round trip proves it warm. (#724)
- Tests sharing a Postgres must not terminate each other's LISTEN connections; tag them with `application_name`. One connection hears notices in commit order. (#608, #471)
- A stage version bump re-queues every subject; to fix a subset change only their fingerprint and add a self-ending sweep. (#525)
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
- The shared log redactor rewrites IP addresses (`10.50.0.11` shows as `10.50.0.0`) and the home folder as `~`: read container and test logs with that in mind. (#705, #707)
- CodeQL's go/reflected-xss flags a header value reaching a page even when only fixed values can match: return constants. (#705)
- A health check must not do the work it reports on: check in the background, answer from the last result. A meter that swaps its map before a flush must keep the snapshot when the flush fails. (#714)
- Caddy: one certificate per name (`relay.test, *.relay.test` is two); DNS-01 needs the test DNS to answer the zone's SOA; the cloudflare module needs a token-shaped test value. Reloading Caddy's config cuts streams in flight, so blue/green moves by readiness (draining colour answers `/readyz` 503) after a stability window, with no edge change. (#720, #722)
- Docker: moving a network alias reconnects the container (the Floating IP gets its own network); `docker compose down` needs the same variables as `up`; read effective config with `docker compose config`, since logs redact IPs. Go's `flag` stops at the first non-flag argument. (#721, #715, #722)
- On Windows an atomic replace guarantees whole contents, not a successful open: a reader treats a failed open as "try again". A test reading two facts from one snapshot needs both under one lock. (#684)
- In a length-prefixed format bound the length by the bytes received before allocating. (#691)
- Canonical JSON for signatures needs its own small parser per language: Go's `encoding/json` escapes `<`, `>`, `&` and U+2028/9 unless `SetEscapeHTML(false)` and keeps the last duplicate key, JavaScript keeps lone surrogates, serde_json refuses them. Hash `files.json` in canonical form. WebCrypto imports an Ed25519 seed as PKCS 8 with the prefix `302e020100300506032b657004220420`. (#733, #743)
- `node:sqlite` (Node 22.13+) runs D1's SQL as is, so store tests need no Miniflare. A far-future ISO date (`+275760…`) sorts before `2026` as text; compare against `9999-12-31T…`. (#735)
- A WIT variant gains no case without breaking 1.0 guests; a plugin loop refusal reuses `scope` with the API code `plugin_loop`. (#751)
- On the rehearsal's seeded home the previous release's first seed writes every component of its catalog (by what is on disk); only components newer than it go through SeedMissing. (#748)
- Random bytes are not reliably corrupt media; build fixtures from a real header plus seeded junk. (#526, #537)

**Apps and UI tests**
- Under Robolectric a bare `waitUntil` only polls; the v1 compose rule runs effects unconfined. Use settling waits and v2 rules. (#508)
- Lazy grids anchor on the first visible key, so out-of-order answers open a page scrolled. A BackHandler always composed keeps its old order. (#529, #523)
- A read that started before a write must not overwrite it on screen: one `ReadOrder` per platform. (#613, #616, #624)
- SwiftUI: an identifier on a wrapping Section replaces its children's; a `LazyHStack` in a horizontal scroll can stay 0 pt; not every `INFOPLIST_KEY_*` is generated (check the built Info.plist). (#541, #569, #579)
- XCUITest does not close the app after a test; settings leak between tests; the `.textClipped` audit relays at 12 text sizes; the simulator can drop taps. (#539, #582, #580, #626, #661)
- AVPlayer can stall forever on a late segment; reopen after 15 s. HLS subtitles need `X-TIMESTAMP-MAP`. (#614, #468)
- A rate limit's or "did not happen" test must bound by elapsed time or by the thing's own size. (#694, #696)
- A per-line performance budget needs enough load to amortise setup: at a tenth of the load the capture bench read 28 µs a line against 15.6 µs. (#690)
- A setting a separate process consumes needs a test from the file to that process: the `logs` role silently ignored every configured retention. (#688)
- A per-role level file polled by each logger can say "debug until a time" and works on Windows; SIGUSR1 cannot. (#688)
- React: a window `keydown` listener re-attached by `useEffect` can be the previous one when a key arrives between commit and effect; `useWindowKeydown` attaches once. A flake fix that will not reproduce locally is a reading of the code until CI stops failing. (#698)
- Scrape over HTTP once in a test, then feed parsed samples directly; 720 scrapes broke the per-test cap. The Prometheus text format parses in about 60 lines, no module needed. (#689)
- A test that reaches a real `os._exit(0)` ends pytest green with no summary. (#565)
- Web tests under load: vitest is main-process bound past about 7 workers, so `maxWorkers` is half the cores, 2 to 8 (CI keeps 2); axe's first run in a worker is several times slower, so warm it in `beforeAll`; parallel module warm-up can expose a half-evaluated module; `shouldAdvanceTime` fake timers make "not yet at T−1" racy, so read times off the fake clock. (#746)
- Web tests: a page that loads names in a separate call is tested with `findBy`, not `getBy`; accessible names keep `bidiIsolate`'s isolates, so match with a regular expression; a browser cannot read a `.mkplugin` before upload, so the typed-name check is a second upload. An error fingerprint takes the message line and first frame's function, numbers folded. (#691, #717)
- The installed Maktaba's bundled Postgres binaries can start a scratch cluster in the scratchpad (`-k ''`, a free port), never the app's data. (#693, #677)
- The wiki's Arabic store translates only each spec file's title and first 600 characters, so deep README edits need no Arabic but a changed story header does (`--ar-todo --epic 54`); its JSON is `indent=2`, keep it. (#751, #735)
- maktaba.dev: `rewriteHref` joins an absolute `/path` onto the source folder, so generated pages link in-site pages by full URL. It builds with npm, not pnpm; a new guide is one line in `GUIDES` and one in `src/site.test.ts`. `media.info` is gated in `server.go`'s `capabilityOf`, not in `capabilities.toml`. (#704, #723)
- Screenshots without a stack: the desktop Player through `runDesktopComposeUiTest` plus `captureToImage` (wrap `Shell` in the window's `Surface`); the web through vite and playwright-core with `/api/*` from a route table and `mkt:usage:consent` set first; Apple TV shots are in CI's `tv-ui-tests` artifact. The devportal lazy-loads images, so scroll each into view to check it. (#703)
- Plugins: stages load at the pipeline worker's start, so a changed installed set needs the supervisor's `Restart`; the pipeline needs `MAKTABA_SUPERVISOR_TOKEN_FILE`; a deep data folder pushes `<data>/run/*.sock` past macOS's 104-byte `sun_path`; Wasmtime under the hardened runtime needs `allow-jit` and `allow-unsigned-executable-memory`; a new catalog group needs its place in the web's `GROUP_ORDER`. (#707)
- The affected-tests gate maps from main's coverage: tests in a new file do not count for an old source, and shared JSON tables map to no test (add a direct test beside the row); `tools/ci/tests` needs PyYAML. On the Mac with under 20 GB free some cmd/maktaba-server tests fail on main too; `make compat-prev` needs `psql` on PATH. (#707)
- A snapshot's `#N` must be a PR with an event; other numbers are written without `#`, an issue as `issue #N`. (#706)
- Homebrew's cargo shadows rustup's and has no wasm target (the SDK template needs rustup's `wasm32-wasip2`); the root `.gitignore` `MANIFEST` hides a `manifest/` folder on the Mac. (#683, #694, #717)
- Claude Code reserves plugin names starting with `claude-`; the plugin is `trail`. Hook machines have python3 without `tomllib`. (#702)

## Open

- Not verified on real hardware: Apple TV playback and AirPlay, background audio on device, foldables and tablets, the desktop Player against a live home. (#512, #579, #523, #667)
- Remove the tag guard's `IGNORED_CI_JOBS` once the iPhone/iPad shards are fixed. (#577)
- Player UI shards left in progress after their run completed (issue #649). (#632)
- Whether a tap lost to the simulator race shows as a cancelled touch (issue #639). (#661)
- One pytest session for both components test folders (issue #670). (#659)
- Docker Postgres on the home Mac was stopped, never removed. (#148)
- The VPS kernel patch for CVE-2026-98432, and a `SECURITY.md`. (#518)
- Epic 52: the real gold set and calibration curves; the rehearsal's "one component more" step. (#672)
- Epic 54: TypeScript, Python and Go templates (issue #718); signing, `mkp run`, developer mode and the plugins CLI (issue #719); the migrate job (issue #712), rehearsal scenario 15 (issue #708), the updater's pre-update list (issue #709), Windows (issue #710), job metrics and problem-report opt-in (issue #711). (#683, #693, #704, #707, #717)
- Epic 54 Phase 2 (signing ships dark, no root keys): the store service, checks and review (with flow check C12), the home's catalog loop and kill switch, the store browser; release-time embedding of the revocation list and store keys; the home's `entitlement_missing` gate, daily refresh and store link; the store's webhook route and sales calls. Flows (§4.12) and joined machines (§4.13) build PRs. (#733, #735, #743, #751)
- Epic 50: 50.11 part 2 status banners in every app; series that 53.17 emits for the monitoring alerts and status page; 50.12 onward. (#727, #739)
- Rare web test races of one shape (a check one render too early), listed on issue #697; BigScreen still re-attaches its key listener. (#746)
- Story 51.03's spec still names the removed `loops-full-home.txt`. (#748)
- The tag guard requires the cloud rehearsal once 50.07's scenarios are live. (#715)
- agent-trail's PyPI publishing waits for its trusted publisher; the CLI runs from git. (#702)
- Epic 53: native crash capture and report forms (Apple, Android, TV, desktop), 53.16 sharing, `docs/app-privacy.md`; native Remote Errors, Logs, Overview and Storage screens; pipeline queue series, API latency histograms and per-machine host metrics on `/metrics`. (#688, #689, #691)
- Epic 53 log bench: the nightly macOS run with a regression issue, the Python logger bench, the log pipeline's numbers on the live Overview. (#690)
- maktaba.dev shots of the Remote's word review and the phone Players' queue. (#703)
- DASH top-level segments are not on the stream gate's allow-list. (#474)
- Dependabot dismissals to revisit when tauri moves to GTK4 and eslint adopts minimatch 11. (d20261102-dependabot-dismissals)

## Missing events

None.

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
| 0733-plugins-signing-trust | 2026-11-06 | pr | Plugins Phase 2 part 1, signing and the chain of trust (Epic 54, 54.14) |
| 0735-pluggable-payments | 2026-11-06 | pr | Paid plugins through pluggable payment providers, the store side (Epic 54, story 54.19) |
| 0743-home-entitlements | 2026-11-06 | pr | The home checks plugin entitlements offline (Epic 54, story 54.19 §6) |
| 0751-epic54-flows-spec | 2026-11-06 | decision | Epic 54 spec, flows, cross-plugin events, loop checks and joined machines |
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

