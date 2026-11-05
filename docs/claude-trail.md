# Claude trail

The one file a new Claude session reads to carry on the owner's Maktaba work. It holds the
owner's standing rules, the decisions and why they were made, what broke and how it was fixed,
the live state, what is in flight and what waits on the owner.

**How to use it**

- Read it all before you start. `CLAUDE.md` at the repo root says the same.
- Before your session ends, add what you decided, what failed, and what changed in the live
  state, **in a trail-only PR** (this file and nothing else: CI runs no job on it, merge it at
  once). A code PR never touches this file (2026-11-02, [#532]). Add only: never rewrite another
  session's entry. If something is no longer true, add a dated line under it saying so.
- Each entry has a date (YYYY-MM-DD) and a PR, commit or file. Use only real PR numbers and
  shas: look them up with `gh pr view` / `git log`. Never guess one.
- Keep it short and plain. The owner reads it too.
- Never write secrets here: the Plex token, the DB password, signing keys, API keys, `.p8`
  contents, test-account passwords.

PR links point at `github.com/Hamza-Labs-Core/Maktaba/pull/N` (see the list at the end).

Sources for the first version (2026-10-31): the long working session
`b743b7d4-a714-4de3-9952-e11ad221c8e6` (2026-10-16 → 2026-10-31, compacted 16 times), the
side-session transcripts on this Mac, the auto-memory, `CHANGELOG.md`, `git log`, `gh pr list`
and `docs/`.

---

## 1. Standing rules from the owner

| Rule                                                                                                                                                                                                                                                                                                                              | Since                  | Why                                                                                                                                                                                  |
| --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| **PR flow runs to the merge without asking:** implement → test → CI green → review it yourself → fix → CI green → merge with a merge commit → move live. Never ask "merge?". Ask only for real product decisions (costly backfill, deleting data).                                                                                | 2026-10-19             | Asking before each merge slowed every PR; the process is already set.                                                                                                                |
| **Claude reviews PRs, not Copilot.** The `copilot-pull-request-reviewer` check and its "quota" comment are noise: say so in one line and move on.                                                                                                                                                                                 | 2026-10-18             | Copilot comes from the author's personal GitHub setting and runs out of quota.                                                                                                       |
| **Update, rollback, restore and migration paths are proven in CI first.** No release that touches update / restore / migration / supervisor / launcher / TV-staging code reaches the live Mac unless `update-rehearsal` passed on that exact commit. The tag guard enforces it ([docs/update-rehearsal.md](update-rehearsal.md)). | 2026-10-28             | Two outages in one day (see §6). Recovery must be automatic or one click for users, never a terminal.                                                                                |
| **One release at a time.** No stacking fixes on the live install in one day.                                                                                                                                                                                                                                                      | 2026-10-28             | v0.3.39 → v0.3.42 shipped in one day, each fixing the last; two outages.                                                                                                             |
| **Arabic ships with English, on every platform, in the same PR.** Web `ar.json`, Android `values-ar`, Apple String Catalog `ar`. Reuse the web's wording. Keep a test that fails when an English string has no Arabic. Check RTL.                                                                                                 | 2026-10-31             | The Remote status screens shipped English-only.                                                                                                                                      |
| **One Arabic word for the home:** «الجهاز الرئيسي» (replaces «الخادم الرئيسي»).                                                                                                                                                                                                                                                   | 2026-10-31             | Same wording in every app.                                                                                                                                                           |
| **The API enforces every role rule**, with a role test (try the route as no one and as a viewer). Hiding UI is not a rule. A modified app with a viewer's token stays a viewer; cloud-linked sign-ins are play-scoped unless a home admin grants manage.                                                                          | 2026-10-25             | Hidden UI does not stop a modified app. See `TestAdminOnly` in `api/internal/handlers/cluster/cluster_test.go`.                                                                      |
| **Only admins can join a machine.**                                                                                                                                                                                                                                                                                               | 2026-10-25             | Set while separating viewers, home admins and the Maktaba service admin ([#310]).                                                                                                    |
| **Secrets are never printed or committed:** Plex token, DB password, signing keys, API keys. Credentials are typed by the owner only. Test accounts are used through the API (`login.sh` reads the Keychain unseen); never type passwords into sign-in forms.                                                                     | standing               | Security. [#328] removed the DB password from every URL a process gets.                                                                                                              |
| **Live data only for justified recovery.** Tests never touch the live DB: use scratch Postgres clusters. Read-only checks are fine. Don't change Plex DVRs (use Add to Plex). Never call live Cloudflare, Stripe, DNS or the VPS without the owner's OK. Don't bypass the admin API to create users.                              | standing               | Outages and the classifier's denials taught this.                                                                                                                                    |
| **Fix flaky tests at the root.** Never only re-run, never skip a test.                                                                                                                                                                                                                                                            | 2026-10-26             | A failing test was skipped instead of fixed.                                                                                                                                         |
| **Changed code needs its tests changed, in the same push.** The pre-push hook (`tools/affected-tests/install-hook.sh`) and CI's `affected-tests` job enforce it. The way out is a commit trailer `No-Test-Change: <reason>`. Never run Mac or simulator UI tests locally: the hook only compiles them. | 2026-11-01 | [#469] left the Mac UI tests stale ([#516]). |
| **Throwaway simulators.** iOS sim tests create a device (`xcrun simctl create`), run, and delete it. Never reboot the shared "iPhone 17". Stop any `simctl log stream` you start.                                                                                                                                                 | 2026-10-31             | SpringBoard crashed 19 times in a day and every crash put a dialog on the Mac's screen.                                                                                              |
| **Screenshots in the dark theme** unless showing a light-only issue.                                                                                                                                                                                                                                                              | 2026-10-28             | The preferred default.                                                                                                                                                               |
| **Plain, short answers.** Explain without jargon.                                                                                                                                                                                                                                                                                 | standing               | Jargon-heavy answers were often not understood.                                                                                                                                      |
| **Verify yourself; don't assume. Don't ask, find the issue and fix.** No restart workarounds unless macOS needs one (Full Disk Access, Local Network).                                                                                                                                                                            | 2026-10-23 → 25        | Unverified claims and restart workarounds cost time.                                                                                                                                 |
| **Testing features on the live Mac is fine while in dev**, but say first that an update restarts the server and watch it through its whole confirm window. Update paths themselves: CI only (rule above).                                                                                                                         | 2026-10-27             | The project is still in development.                                                                                                                                                 |
| **Big multi-track work:** parallel worktree agents, keep going without check-ins, escalate only when a plan's premise turns out false.                                                                                                                                                                                            | 2026-05                | Gap-closure program.                                                                                                                                                                 |
| **Worktrees live under `.claude/worktrees/`, never `$TMPDIR`** (macOS purges it).                                                                                                                                                                                                                                                 | 2026-10-18             | A purged worktree broke a live venv.                                                                                                                                                 |
| **Never use a made-up sha** in a command or doc.                                                                                                                                                                                                                                                                                  | 2026-10-27             | A fabricated sha was used in a release command once; caught and killed.                                                                                                              |
| **Clean up what's done:** archive finished sessions, remove merged worktrees and branches (keep a branch only if it has work not in main).                                                                                                                                                                                        | 2026-10-17, 2026-10-20 | A closed PR's branch has no use.                                                                                                                                                     |

---

## 2. Architecture decisions and why

### Product shape (2026-10-25)

- Phone, foldable and tablet apps on Android and iOS: three **Player** and three **Remote**.
  Desktop Player and desktop Server. TV Player. Web is part of the server. A cloud web service
  on the VPS acts as a Remote and a Player for a connected home. Plus the **Maktaba service
  admin**, separate from home admins. Spec: [#305], `specs/epics/42-apps-player-remote-server/`.
- Apps are **native** (SwiftUI, Kotlin/Compose), not web wrappers: [#306], [#321], [#325], [#342].
- **Linked accounts** for remote play through Maktaba Cloud ([#309], [#327], [#388]). Rejected
  (2026-10-25): a video link that plays without sign-in.
- Player builds are viewer-only, like the TV apps (2026-10-25).

### Live install: Maktaba.app with bundled Postgres (2026-10-20 → 23)

- Everything is one Mac installation, with Postgres bundled because it is small. Epic 36
  ([#148]); the home Mac moved in with `migrate-from-dev` ([#158]) on 2026-10-23. The Docker
  Postgres was stopped, not deleted (not finalized).
- Updating live = merge → tag a release → CI builds a Developer ID signed, notarized dmg
  ([#288]) → the app updates itself. Local builds carry a placeholder updater key, so use CI
  builds. Intel Mac builds dropped: Apple silicon only ([#304]).
- Data and backups live on the big disk, not the internal one ([#350], [#351], [#353]).
- Windows and Linux get the same one-installation app: Epics 38 and 39 ([#165], [#166]).

### Cluster: one home, many machines (2026-10-18 → 19)

- Phase 1 ([#56]): the home holds the DB and queues; machines find it over mDNS, join with a
  pairing code, get a least-privilege Postgres role, and pull jobs by capability.
- Phase 2, "the cluster channel" ([#58]): every worker holds a sealed WebSocket to a hub in the
  home's API. Logs and metrics are files on the share (`<share>/maktaba/cluster/<ip>/…`), with
  a long-term archive on the home. The DB is only a cache. A central scheduler was rejected:
  all work is split into queues, and each machine pulls jobs from the queues on the home.
- Full nodes keep a live copy of the home's DB ([#178]); per-machine client certificates
  ([#349]).
- **Machines update only after the home commits, never ahead of it** ([#434], 2026-10-31).

### Updates (Epic 35, then hardening)

- The cluster updates itself ([#271], [#287]).
- **D1, 2026-10-27:** no click in the desktop app. Updates started from the Updates page install
  everywhere; automatic ones in the window with a notice ([#389]).
- **Backups never block an update:** taken on the side, complete (vectors kept) and fast
  (under 10 minutes) ([#397]).
- **A failed TV preview keeps the release:** TV stays on the old streaming software, the owner
  gets Retry / Drop. Undo everything only if TV breaks after it switched ([#404], 2026-10-28).
- **Only manual TV changes need approval; TV changes never block updates** ([#429]).
- **CI proves it:** the update rehearsal ([#435]) gates every tag. Scenario 11 covers the TV
  slot release mismatch ([#446]).
- One registry of ports, checked at start ([#440]).
- Versioning: still 0.3.x. The owner was asked 0.4.0 vs 1.0.0 and hasn't chosen.

### TV: slots, staging, switchover (Epic 33)

- Two TV slots, **blue and green**, with hot standby ([#97]). An update stages the new release
  on the standby slot, checks it, then switches ([#104], [#108]).
- Why the TV lagged on 2026-10-31: three owners (supervisor, staging, switchover) each moved
  slots and nobody reconciled. Fix: every bind or adoption restarts a running slot whose release
  differs from the one it was told to run (`matchBinding`, [#443], [#446]).
- TV changes: updates join one open change until approved; small changes go on air by
  themselves ([#167], [#203], [#248]).
- Plex: Maktaba emulates an HDHomeRun tuner + XMLTV, as dizqueTV does ([#61], [#139], [#159],
  [#214]). Auto-detection by Plex was never solved; Add to Plex adds it as a channel source.

### Transcription and search

- Whole library transcribed on the queue engine with batched MLX Whisper ([#54]).
- Model choice (2026-10-19): turbo + a second listen with Cohere Arabic, merged word by word
  ([#113]). Why: a human listening test on 20 disputed spots scored Cohere 17, turbo 0;
  an LLM judge had favoured turbo's tidy spelling and was wrong. Turbo kept for Quran.
- Search moved from Chroma to Postgres pgvector ([#319], 2026-10-26), the more efficient
  option. Chroma broke whenever a worker was killed mid-write.

### Queues

- Change-driven multi-level queue engine ([#47]). Everything is a queue job, one item at a time,
  run in parallel by workers.
- Queue health: failure classes, parking, dependency watchers, a failing-items page ([#177],
  [#192], [#195]).
- Epic 47: convert to MP4 → dedup → YouTube refresh, in that order, queue-based ([#412]–[#428]).

### Look and feel

- Islamic library theme (2026-10-22): Ink & Brass dark, gold on white light, Amiri + Plex fonts,
  Magnific-made library backgrounds behind frosted panels ([#250], [#251]).
- Lists open recently added first everywhere, except search (rank) ([#375]).
- Loading placeholders wait 0.4 s and match the page; returns are instant from cache ([#411],
  [#413]).

### Analytics and usage data

- Analytics counts every way of watching TV, not Plex usage itself ([#344]).
- Usage data is per admin for the server, per app for players and remotes, and never carries
  data, IPs or identities ([#348], 2026-10-26).

### Licensing

- Epic 16 end to end ([#334]); the seat cap is off while a build can't apply a license ([#331]);
  a Pro license is applied on the live Mac via the CLI ([#340]).

---

## 3. Live state (2026-10-31, evening)

- **Mac (home):** Maktaba.app **v0.3.44**, committed 2026-10-31. API :8080, web :8088, streaming
  :8081, supervisor 127.0.0.1:9103, bundled Postgres 127.0.0.1:54329. Logs:
  `~/Library/Logs/Maktaba/server.log`. Pro license. Search on pgvector.
- **TV:** still on **v0.3.39** streaming. TV change #58 was held with `release_mismatch`; the fix
  ([#443], [#446]) ships in the next release. A retry of #58 was offered; no answer yet.
- **Windows PC (`windows-pc`):** state `ordered`, waiting for its desktop app to stage 0.3.44.
- **TestFlight:** works since 2026-10-31. Player and Remote 0.3.44 uploaded by
  `.github/workflows/testflight.yml` (builds 20261031.214201 and 20261031.214159). Every
  release tag uploads both from now on. See §6 for the three fixes.
- **Held:** the next release (owner hasn't approved cutting it).

---

## 4. In flight (2026-10-31)

- [#452] Android Arabic for Remote, Player and the account menu. CI green; the working session
  merges it.
- Apple Arabic (Remote, Player, tvOS): an agent of the working session is doing it, on a
  throwaway simulator.
- Next release, once approved, would carry: [#440] port registry, [#442] Updates wording,
  [#443]/[#446] TV slot fix, [#444], [#445], [#448]–[#451], and the Arabic PRs. It needs
  `ci.yml` and `update-rehearsal` green on the exact sha, then a live update watched through
  its confirm window.

## 5. Waiting on the owner

- **Cut the next release?** Not approved yet.
- **Retry TV change #58 now?** Offered, unanswered.
- **Delete Actions run 41226231934?** Its log lists the team's bundle IDs and certificate types
  (no secrets). Offered, unanswered.
- **Android debug APKs for sideloading?** Offered, unanswered.
- **Versioning:** 0.4.0 or 1.0.0? Unanswered.
- **TestFlight internal testers:** add yourself to an Internal Testing group in App Store
  Connect for each app.
- **Epic 45 duplicates:** three duplicate libraries await the owner's Merge in Manage libraries
  (as of 2026-10-27).
- **Shelved:** Epic 45 stories 45.22 and 45.23 ([#399]).
- **Epic 48 spec** ([#458], 2026-10-31): review it, and answer its five open questions
  (downloads, hiding Live TV, Chromecast, the Windows/Linux player, keeping Light).

---

## 6. Failures and lessons

### Outages

- **2026-10-27, v0.3.36 rollback.** The TV slot timeout counted from the stage, so the rollback
  ran a 14-minute dump, then a launcher timeout, then a restore that failed on a publication
  conflict (the PC's copy grant rebuilt it). Recovered by hand with the owner's OK, no data lost.
  Fixed in [#395], [#397], [#398].
- **2026-10-28 17:58–18:13, v0.3.42.** The update watch timed out while TV change #57 waited for
  approval, so it rolled back; the full restore then failed ("tuple concurrently updated")
  because the PC's copy grant ran GRANTs mid-restore. Vectors, 630 post-data items and the ACLs
  were missing; recovered by hand (the ACLs were missed at first). Root causes fixed in [#433];
  TV changes stop blocking updates in [#429]. **Lesson:** this is why the CI rule and one
  release at a time exist: each rollback made things worse.
- **2026-10-31, TV `release_mismatch`.** A running TV slot asked to run another release kept the
  old one. Fixed in [#443], [#446]; rehearsal scenario 11 fails on pre-#443 code.

### TestFlight (2026-10-31)

1. The App Store Connect **API can't create apps** and `xcodebuild` reports `missingApp`. The
   two app records were created through **Xcode's Organizer** (bundle IDs
   `com.hamzalabs.maktaba.player`, `com.hamzalabs.maktaba.remote`).
2. Xcode's "create record" step **reported an error although the POST returned 201**: its
   immediate re-check didn't see the new app yet. The record was there.
3. Upload then failed with **"Cloud signing permission error"**: the CI's API key needed the
   **Admin** role (or App Manager with cloud-managed certificate access). The owner changed it,
   and both apps uploaded. Pipeline: [#447], [docs/testflight.md](testflight.md).

### Flaky tests (root causes)

- Nodeconfig race: a write that ends between two looks read as "never joined" ([#359], [#444]).
- Integration servers collided on fixed ports 18080/19100 and 18081/19101: OS-picked ports
  ([#451]).
- `TestSwitchTheHome` was a real bug ([#364]). `TestCrashLoopBacksOffThenFails` was a real race
  ([#329]). More fixed in [#249], [#257], [#343], [#356]–[#358], [#405], [#425].
- A test past the unit tier's hard cap is re-run alone and fails only if it fails again
  ([#241]).

### CI quirks

- **macos-14 runners retire 2026-11-02:** moved to macos-15 ([#443]).
- **CodeQL** is GitHub default setup (no workflow file). The `CodeQL` umbrella check fails a PR
  on new high-severity alerts. Recognised sanitiser: `filepath.Clean` + a prefix check at the
  sink.
- **apt stalls** were a 50 KB/s Azure mirror; fixed with a package cache and a real fallback
  ([#346]).
- **Rehearsal cache eviction** ("Failed to restore cache entry"): seed comes from the run
  ([#441]).
- Docs-only PRs skip the heavy gates ([#211]).
- Main has no branch protection: auto-merge merges at once. The merge script once treated an
  empty check list as green and merged a red PR ([#313] → [#314]). Confirm green yourself.
- Notarization sometimes times out: re-run the failed legs.

### Local tooling gotchas

- **Go API on this Mac:** build with `CGO_ENABLED=0` (cgo link fails on the macOS 27 SDK). Build
  inside each module dir; `go build ./cmd/maktaba-server/...` from the root fails.
- **Android:** `JAVA_HOME=/opt/homebrew/opt/openjdk@17/libexec/openjdk.jdk/Contents/Home` plus
  a `local.properties` with `sdk.dir`.
- **Python pipeline:** `uv run --extra dev ruff|mypy|pytest` from `pipeline/`. Run uv commands
  one at a time (the `.venv` races). mypy is strict over tests too.
- **zsh:** variables don't word-split (use arrays); `$path` is PATH; `log` is a builtin
  (`/usr/bin/log`); `sleep` is blocked in this harness (use until-loops in background).
- **Prettier** prints "All matched files…" even after an error: check the exit code.
- **Live Mac app:** never `launchctl kickstart -k` the server (it SIGKILLs workers); TERM the
  launcher. Never leave a second `Maktaba.app` copy in /Applications. Full Disk Access and
  Local Network grants apply only to processes started after them; Local Network needs LC_UUID
  in the binaries ([#295]).
- **NAS:** shares can wedge the Mac's NFS/SMB client. Listings on a hung share time out
  ([#197]). Paths may be NFC or NFD ([#373]).
- `start_session` is refused over Remote Control: use background agents instead.
- Scratch Postgres: `-k ''` (socket paths too long) and `--encoding=UTF8` (Arabic full text).

---

## 7. Release history

| Release            | Date            | Notes                                                                                 |
| ------------------ | --------------- | ------------------------------------------------------------------------------------- |
| Project start      | 2026-05-03      | Skeleton and architecture spec. 25 epics, 272 stories ([#2]).                         |
| Gap closure        | 2026-05-30      | One umbrella PR, waves 0–3 ([#41]).                                                   |
| v0.1.0             | 2026-10-19      | First release ([#119]).                                                               |
| v0.2.0             | 2026-10-19      | Tag only, no GitHub release; changelog in [#138].                                     |
| v0.3.0-rc.1 → rc.4 | 2026-10-22 → 23 | The Mac app era starts.                                                               |
| v0.3.0             | 2026-10-24      | First non-rc 0.3 release.                                                             |
| v0.3.8             | 2026-10-25      | First Developer ID signed and notarized build.                                        |
| v0.3.20 → v0.3.26  | 2026-10-26      | pgvector search, licensing.                                                           |
| v0.3.27 → v0.3.38  | 2026-10-26 → 28 | Epics 42, 44, 45, 46; the 10-27 outage at 0.3.36.                                     |
| v0.3.39 → v0.3.42  | 2026-10-28      | Four in one day; the 10-28 outage. Led to the CI rule.                                |
| v0.3.43            | 2026-10-31      | First release after the update rehearsal ([#435]).                                    |
| v0.3.44            | 2026-10-31      | Port fix, TV never blocks, kept-schema install fix ([#438], [#439]). Live on the Mac. |

Full list: `gh release list`. Other sessions tag too: check `git ls-remote --tags` before
picking a number.

---

## 8. Session index (this Mac)

Every Maktaba transcript on this Mac (`~/.claude/projects/*aktaba*/*.jsonl`, 190 folders, 697
top-level transcripts plus 198 subagent ones), oldest first, traced on 2026-10-31. The ID is the
first 8 characters of the transcript file name; the folder is the worktree under
`.claude/worktrees/` ("main" = the main checkout). Decisions and failures from these are folded
into §1–§7. The May 2026 work (project start, gap closure, [#1]–[#45]) ran before these
transcripts begin; only git and the CHANGELOG record it.

**Not listed one by one:** 642 automated security reviews of single changes ("Review this change
for security vulnerabilities", 2026-10-16 → 10-31). Their findings went into the PRs they
reviewed, for example [#109], [#137], [#328]. The 198 subagent transcripts were skimmed for
failures; nothing in them is missing from the PRs and audit notes.

| Date               | ID · folder                       | Topic                                                                                                                                  | Outcome                                            |
| ------------------ | --------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------- |
| 2026-06-14         | 2cfad978 · main                   | Review four epic branches and merge in order                                                                                           | Merged and pushed to main                          |
| 2026-06-14         | e24d1c34 · main                   | CI/CD for the Cloud Relay deploy                                                                                                       | `relay.yml` on `feat/relay-ci-cd`                  |
| 2026-06-14         | 5809abe7 · main                   | Merge `feat/relay-ci-cd`                                                                                                               | Fast-forwarded main                                |
| 2026-06-16         | c56306a8 · main                   | Audit: does CI build installable packages?                                                                                             | Report only                                        |
| 2026-06-16         | 0542b5bf · main                   | CI packaging for the client apps                                                                                                       | `feat/client-packaging`                            |
| 2026-06-16         | 625ca515 · main                   | CI packaging for the home server                                                                                                       | Branch pushed                                      |
| 2026-06-17         | 4cd65216 · main                   | Merge both packaging branches                                                                                                          | main at `1def332`                                  |
| 2026-06-17         | 3f498a8a · main                   | Epic 28 auto-update, spec and build                                                                                                    | `feat/auto-update`                                 |
| 2026-06-18         | f72107a0 · main                   | Merge `feat/auto-update`                                                                                                               | Merged                                             |
| 2026-06-18         | 48e4af64 · main                   | Epic 29 watch analytics                                                                                                                | `feat/analytics`                                   |
| 2026-06-20         | 88512958 · main                   | Merge analytics plus two follow-ups                                                                                                    | Merged                                             |
| 2026-06-20         | 220a9a86 · main                   | Merge `feat/analytics-followups`                                                                                                       | Merged                                             |
| 2026-10-16         | 159c0553 · stupefied-blackwell    | Relay stream-delete race                                                                                                               | [#49]                                              |
| 2026-10-16         | 0df6d103 · dazzling-jackson       | PR 46 mypy and CodeQL path-injection                                                                                                   | `acc0f94` on [#46]                                 |
| 2026-10-16 → 10-31 | b743b7d4 · main                   | **The long working session** (11,366 messages, 16 compactions): NAS import, queues, transcription, cluster, TV, Mac app, updates, apps | Most PRs from [#46] to [#452]; the source of §1–§7 |
| 2026-10-17         | 31fea266 · main                   | Chapters, topics and posters                                                                                                           | [#69] (`0689cd42`)                                 |
| 2026-10-19         | 686204e0 · main                   | One design for the queue tiles                                                                                                         | [#110]                                             |
| 2026-10-19         | f4fa9616 · main                   | Join a machine from a release                                                                                                          | [#70]                                              |
| 2026-10-19         | c85ac965 · festive-rubin          | UI test cases become tests                                                                                                             | [#125]                                             |
| 2026-10-19         | bc972219 · loving-babbage         | UI test cases for every page                                                                                                           | [#121]                                             |
| 2026-10-19         | f506462b · wonderful-babbage      | Series-ends test in the unit budget                                                                                                    | [#72]                                              |
| 2026-10-19         | 3b7b86b0 · main                   | Library ACL on collections                                                                                                             | [#62]                                              |
| 2026-10-19         | 32155c41 · main                   | Streaming exits on SIGTERM                                                                                                             | [#122]                                             |
| 2026-10-19         | 94a2eedf · main                   | Second-listen model from the share                                                                                                     | [#117], live                                       |
| 2026-10-20         | 3209c448 · wonderful-babbage      | Epic 36 track A: server in the Mac app                                                                                                 | [#149], [#151], [#154], [#155]                     |
| 2026-10-20         | 9d8cd5a5 · loving-babbage         | Epic 36 track B: venv and models                                                                                                       | [#150]                                             |
| 2026-10-20         | ae7d8fb3 · trusting-dirac         | Maktaba.app carries the server                                                                                                         | [#152]                                             |
| 2026-10-20         | 90ee705b · festive-rubin          | Epic 36 track C: login item, menu bar, setup                                                                                           | [#153]                                             |
| 2026-10-20         | a42e7c51 · main                   | Channel plans pass verification                                                                                                        | [#146]                                             |
| 2026-10-21         | 0e3476e1 · festive-rubin          | `migrate-from-dev` (36.9)                                                                                                              | [#158]                                             |
| 2026-10-25         | 97939984 · happy-goldwasser       | Search index in its own worker, safe stop, throughput                                                                                  | [#290], [#296], [#300]                             |
| 2026-10-25         | cdc7a582 · main                   | App icon                                                                                                                               | [#292]                                             |
| 2026-10-25         | bde6d47f · main                   | Log the reason every operation fails                                                                                                   | [#291]                                             |
| 2026-10-25         | 83883ea0 · main                   | A home has a name                                                                                                                      | [#317]                                             |
| 2026-10-26         | 2a7e42e5 · cranky-banach          | Native Android Player and Remote                                                                                                       | [#321]                                             |
| 2026-10-26         | 04beb6d6 · cool-elbakyan          | Maktaba Cloud accounts, Stripe test billing, service admin                                                                             | [#324]                                             |
| 2026-10-26         | 8ed6fe72 · festive-diffie         | Apple TV app (blocked a while on test accounts)                                                                                        | [#325]                                             |
| 2026-10-26         | 37d15153 · reverent-snyder        | Cloudflare TV end to end                                                                                                               | [#326]                                             |
| 2026-10-26         | 66f668bd · gracious-austin        | Cloud relay and linked accounts                                                                                                        | [#327]                                             |
| 2026-10-26         | 8193aee5 · main                   | Licensing end to end                                                                                                                   | [#334]                                             |
| 2026-10-26         | 8fdb1558 · amazing-mendeleev      | Finish Apple Player iPad/Mac and Remote                                                                                                | [#335]                                             |
| 2026-10-26         | 902d85fc · main                   | Permissions walkthrough                                                                                                                | [#336]                                             |
| 2026-10-26         | 092f1a3a · competent-euler        | mDNS announces `<host>.local.`                                                                                                         | [#339]                                             |
| 2026-10-26         | 9781c1aa · elastic-vaughan        | Android TV on the shared core                                                                                                          | [#342]                                             |
| 2026-10-26         | e4b5eedf · main                   | Epic 45 libraries                                                                                                                      | [#345]                                             |
| 2026-10-26         | 2f2efd80 · main                   | Usage data to Maktaba Cloud                                                                                                            | [#348]                                             |
| 2026-10-27         | cfa09ec2 · main                   | Errors, logging and crashes audit                                                                                                      | [#378]–[#387]                                      |
| 2026-10-27         | 27729aee · epic42-editions-tablet | Web editions, tablet and foldable layouts                                                                                              | [#367]                                             |
| 2026-10-27         | c033af81 · epic42-tv-picker       | Home picker and code sign-in on TV                                                                                                     | [#369]                                             |
| 2026-10-27         | 5a1ae995 · epic45-phase3          | Epic 45 phase 3                                                                                                                        | [#368] (with [#366])                               |
| 2026-10-27         | 9a4a7cfe · main                   | Vertical video thumbnails                                                                                                              | [#374]                                             |
| 2026-10-27         | 0427f3fc · main                   | Recently added first, enforced by the API                                                                                              | [#375]                                             |
| 2026-10-27         | 8886cae6 · epic42-finish          | Finish Epic 42                                                                                                                         | [#372], [#377], [#388], [#390]                     |
| 2026-10-28         | 75742ab6 · main                   | Epic 47 offline, MP4 and dedup                                                                                                         | [#412]–[#431]                                      |
| 2026-10-31         | 3758cb4b · docs-claude-trail      | This file                                                                                                                              | [#453]                                             |

---

## 9. PC sessions

The owner's Windows PC has its own Claude sessions. Their transcripts are on the PC and can't be
reached from the Mac (no SSH), so **this section is to be filled from the PC.**

**Append rules for a PC session** (so its PR never conflicts with the Mac's):

1. Pull main and read this whole file first.
2. Trace your own sessions (`%USERPROFILE%\.claude\projects\*aktaba*\*.jsonl`) oldest first,
   with jq/grep in slices. Never load a file whole.
3. Add **only inside this section**, at its end: one `###` heading per session, in date order:
   `### YYYY-MM-DD · PC · <first 8 chars of the transcript id> · <topic>`. Under it, up to five
   bullets: what was asked, what was decided and why, what failed and how it was fixed, PRs.
4. Don't edit §1–§8 or anyone else's entry. If a PC entry changes a rule, decision or the live
   state, say so in its bullets; a later Mac session folds it into the topic sections.
5. Real PR numbers and shas only (`gh`, `git`). No secrets.
6. Ship it as its own trail-only PR against main (this file only, so CI runs nothing; no
   CHANGELOG line, which would make it run CI).

Traced on 2026-10-31 from the PC. Its Maktaba transcripts sit under `~/.claude/projects/E--tv`
and `E--tv-dizquetv` (the checkouts live in `E:\tv`), not `*aktaba*`. Left out: `c58dc8c2` (an
iperf question), `56a527d9` (another repo) and two empty transcripts. The 362 subagent
transcripts were not traced one by one; their work is in the PRs cited.

### 2026-10-14 · PC · 89e92223 · dizqueTV on the PC, then mediaintel and Maktaba TV

- Asked: Plex Live TV from the YouTube archive. dizqueTV 1.7.0 was built from source on the PC
  (port 8002, tuned by Plex on the NAS as an HDHomeRun), which is why Maktaba TV emulates an
  HDHomeRun with XMLTV. Then the rule: build tools for the work, never do it by hand. That
  became mediaintel ([#46], [#48]), then Maktaba's queues ([#47]), channel rules ([#52], [#55])
  and Maktaba TV ([#57], which took in [#55]).
- Rules set here: everything is config with a UI for that config (rules are
  settings, not code); verify every claim (a guess at failing NAS disks was wrong: SMB timeouts
  from 32 parallel reads); no placeholder features (a stub audit followed: [#123], [#128],
  [#130]–[#136]). Claude's own working rules from it: never change security settings (the
  Defender exclusion was added by hand, not by Claude); never use `git stash` (a shared stash
  swapped two worktrees); tests bind 127.0.0.1 and build output is never committed (`api.exe` landed in
  [#119]; [#127]).
- Decided: Windows joins the Mac, not the other way around. The PC's own home was retired
  and the PC joined `Mahmouds-Mac-Studio` as node `windows-pc` on 2026-10-19; [#75] then let its
  storage check accept files of unknown size, so the worker took jobs. Paths are canonical
  `smb://` ([#81], [#101], [#107]), and nodes reach Plex through the home and never hold its
  token ([#73], [#78]). TV hot swap ([#97], [#104], [#108]) and automatic updates were
  asked for too.
- PC facts: 192.168.50.24, RTX 2080 SUPER. `O:` = `\\Home-NAS\Originals`, `Y:` =
  `\\Home-NAS\Originals\Backup\Youtube`. Splunk holds 8000 and 8088 and MiniTool ShadowMaker
  8080, so setup picks a free port from 8780 (v0.1.0, `6b88722f`). The mediaintel data and a
  hand-over README for the Mac are in `Y:\.maktaba`.
- Ended with the Plex discovery fix ([#139]: relative SSDP location, no UDP 65001, a device ID
  that failed its check digit) and v0.2.0 ([#138]) waiting on [#141], which the classifier
  would not merge without a human review.

### 2026-10-20 · PC · 798feb8d · v0.2.0 and the first node PRs

- Merged [#141] and then [#138]; tagged v0.2.0 on `e539c7af` (a release tag, no rc
  needed).
- Four of the five release workflows failed (as on v0.1.0): opened [#142] for the icons, the
  Android SDK and the relay deploy.
- Ran the PC as a full node from source: opened [#143] so its API and pipeline reach the home's
  database.
- Requirement: when the node can't reach its home, the site keeps working, shows a notice and
  disables actions instead of going blank. That became [#145] (later folded
  into [#178]).

### 2026-10-20 · PC · f43fad60 · "Maktaba worktree setup", the PC's coordinating session

- 2026-10-20 → 10-31. Asked: keep the PC's node and dizqueTV running, and review, fix and merge
  every PR from this side. It ran the side sessions below (node copy, Windows and Linux apps,
  updates), merged about 70 PRs and tagged about 17 releases between v0.3.0-rc.1 and v0.3.33
  (the Mac tagged the rest). On 10-31 it traced these PC sessions.
- Rules set here (for §1): review all PRs from this side, then fix and merge (a merge
  commit on the exact reviewed head, `--match-head-commit` with the full sha, every check green);
  fix and merge instead of opening new PRs (fold findings and new CI failures into PRs already
  open); poll CI yourself (wait on CI yourself, merge on green); tag every merge (the next patch
  tag at once, after main's `ci.yml` passes on the merge commit, pushed
  with the owner's `gh` so the release builds start; since 10-31, not while §3–§5 hold a
  release); a recurring manual step becomes a Maktaba feature.
- PC setup: the node and dizqueTV start from sign-in scheduled tasks (logon +30 s), because `O:`
  and `Y:` exist only in the signed-in session. On 2026-10-25 it was decided to install the
  Windows app instead: the node moved from the hand-built copy in `E:\tv\maktaba-local` to
  `%LOCALAPPDATA%\Maktaba`, task `Maktaba Server <SID>` running `Maktaba.exe --server-agent`.
  `node.json` is in `%APPDATA%\Maktaba` (on `E:`); web 8780, API 8781, streaming 8782. Claude's
  working rules on this PC: restart its services only through their sign-in tasks; never touch
  the dizqueTV task; **never install Maktaba or start `Maktaba.exe` from Claude's shell**: the
  Claude desktop app is an MSIX package, so registry writes from its shell land in Claude's
  private copy (this broke `maktaba://` here, see `c17dd289`). Download the installer and check
  its sha256 against the asset's digest, then the owner runs it or opens Maktaba from the Start
  menu.
- Fixes, each in its own release: a merge guard that let [#212] merge with a check still running;
  the Mac moving its Postgres without the node's role ([#276], [#307]); client certificates
  breaking the node's pipeline ([#349]); lib/pq ignoring the passfile on Windows, plus an
  inherited `PGPASSWORD` ([#362]); no local video on the node, because the API's check ignored
  its inferred path map `/Volumes/Originals/` → `O:/` ([#366], v0.3.33). Lessons: compare with
  main's own runs before blaming a change ([#254] → [#259]); check remote tags before tagging (a
  v0.3.1 collision); scan any export for tokens (the dizqueTV export carried the Plex token in
  about 2,800 files; redacted).
- 10-28: each update order from the Mac waited for a click in the v0.3.33 app (three, none
  installed), so v0.3.42 was installed by hand. In the Mac's rollback the PC's copy rebuild hung
  15 min on a table-sync worker with a dead connection, and with the Mac back at schema 167 the
  v0.3.42 node paused its jobs and streaming. 10-31 (updates §3): the Mac is on v0.3.44, the PC
  on v0.3.42. The order waits because no app window runs and the server's
  `maktaba://install-update` link gets Windows' "Get an app" popup: the link was registered only
  in Claude's private registry copy. Handed to `c17dd289`, where both open questions were then
  answered.

### 2026-10-20 · PC · ce7a4c40 · A node that loses its home, then its database copy (Epic 37)

- Asked: fix [#145]'s review findings. Outage rules: no sign-in during an outage, videos play
  when their path is reachable, jobs pause. Playback with the home's database
  down became [#157].
- Then the design changed: every joined node keeps the home's whole database,
  read-only, so it can work read-only; sign-in works offline; everything goes in one feature
  branch, merged once. [#145] and [#157] closed into [#178].
- Why logical replication: Postgres can't stream physically from an arm64 Mac to an x86 PC, and
  a physical copy would carry every secret and session id. One slot per node; the copy pauses
  while the home's schema is ahead; secrets and other machines' rows stay out. Pinned Postgres
  went into [#196] (`postgres.exe` needs a restricted, non-admin token).
- Also built: switching the home, Plex and Cloudflare included (credentials go through the
  encrypted handoff, never the replica). Decisions: standby homes only, late writes kept
  aside with an apply, the supervisor runs the cloud relay.
- Failures: a node's replication role could read every table through `test_decoding` (only
  `pgoutput` ships now, foreign slots are dropped); the freeze wasn't a fence (lib/pq's
  `BEGIN READ WRITE` beats `default_transaction_read_only`); a stream-key race ([#234]). [#178]
  merged 2026-10-22 after five reviews; [#240] took two lows.

### 2026-10-20 · PC · 2356e2cd · [#143] review items

- Fixed a start-up probe that could hang and home-only database loops running on nodes (the
  node's role is limited on purpose, so those loops stay on the home), then a `make lint-log`
  failure (runtime data goes in log fields, not the message). [#143] merged 2026-10-21.

### 2026-10-20 · PC · e7913aa5 · Release blockers left after [#142]

- Every tag up to v0.2.0 had failed in several legs. Fixed what [#142] reached once it got
  further, merged main after Maktaba.app's own icons landed, and matched the Windows binary name
  `Maktaba.exe` (`mainBinaryName`). [#142] merged 2026-10-21.

### 2026-10-20 · PC · c8ac551a · Rework [#145] (stopped)

- Stopped at once; the work went on in `ce7a4c40`.

### 2026-10-21 · PC · 295d4839 · Windows one-app install (Epic 38)

- Asked (relayed): the same one-app setup on Windows as on the Mac. About 30 PRs,
  spec [#165] and stories 38.1–38.14, reviewed and merged by `f43fad60`.
- Why a sign-in task, not a service: `O:` and `Y:` exist only in the user's session. The task
  `\Maktaba Server <SID>` runs `Maktaba.exe --server-agent` 30 s after sign-in, with a 5-minute
  time trigger as the watchdog, because Task Scheduler's restart-on-failure never retries a
  crash ([#170], [#175]). All state is per user in `%LOCALAPPDATA%\Maktaba`; one current-user
  NSIS installer, no admin, every download sha256-pinned ([#169], [#198]).
- Windows lessons: `os.Rename` can't cross drives (`%APPDATA%` is on `E:`, `%LOCALAPPDATA%` on
  `C:`); CTRL_BREAK can't reach a child that has its own console, and sent to a pid that leads
  no group it hits the whole console ([#205], [#222]); the VC++ runtime pin ([#212]) must match
  the runner's Visual Studio (14.51 on windows-2025-vs2026, not VS 2022's 14.44);
  `postgres.exe` refuses an admin token ([#204]).
- Decided: transcription stays the Mac's by default, and a machine that can't run the second
  listen doesn't claim it (this PC's 8 GB card can't) ([#201], [#221]). Adopting an existing
  install moves its task, keys and firewall rules (one UAC prompt) and never touches dizqueTV
  ([#200], [#230]); `node.json` is never copied, and only the PC that owns it uses it ([#217]).
- CI: the docs-only filter never fired ([#211]); a shutdown flake went to 0 in 600 runs
  ([#223]); `node.json` writes got a lock, ACLs and encodings ([#227], [#231], [#239], [#243]).
  v0.3.0-rc.1 was tagged on [#243].

### 2026-10-21 · PC · 454c33cb · Linux one-app install (Epic 39)

- Asked (relayed): the same for Linux. Spec [#166] and the story PRs, merged by
  `f43fad60`.
- Why: the AppImage is the main download, with .deb and .rpm from the same build, all updated by
  the Tauri updater; no apt/dnf repo (a second updater and trust root, hosting, root for every
  update). The server runs from a per-user runtime copy with a `current` link and rollback
  ([#174]), as a systemd user service with linger ([#173]).
- Decided: use the distro's ffmpeg. The pinned Linux ffmpeg builds were
  `--enable-nonfree`, so not redistributable. Linux bundles none and the packages depend on
  ffmpeg ([#229]); the approved mirror ([#220]) was closed unpublished.
- Lessons: curl's `--retry` doesn't retry a Cloudflare 521 (an ffmpeg host outage, [#216]);
  `find | head` under pipefail exits 141; a plain CI re-run reuses the old merge ref, so merge
  main and push instead; rpm takes no `-` in a version (v0.3.0-rc.1's Linux legs, [#258]).
- Side fixes: one version order ([#218], [#226]); VOD on NVIDIA used a nonexistent
  `-hwaccel nvenc` ([#232]); VOD shares the GPU with TV without starving the hot standby
  ([#237]); VOD playlists list every segment ([#244]).

### 2026-10-23 · PC · 9bd8e01c · Automatic updates (Epic 35) and what broke live

- Asked: why it didn't update by itself, then to build it. [#271] (v0.3.0): one update
  path per install kind, the home's run drives the nodes, and each machine's journal finishes a
  run whichever release starts next.
- Decisions: D1 "Ask first" (desktop apps stage the home's order and install on a click;
  reversed to no click on 2026-10-27, [#389]); D2 a rollback restores the pre-update dump; D3 the
  Linux app updates itself without root. The order file `updates/app-install.json` goes ordered
  → staged → go → installing; on Windows the app the server starts must break away from the
  launcher's kill-on-close job.
- A failed update gave no reason. Now every attempt shows its reason and
  links the logs ([#276]). The rest of the spec was built by ten parallel worktree agents
  ([#287]).
- Live failures fixed: the Mac moved its Postgres without the node's role, so a node now gets
  its access back by itself ([#276], [#307]); Mac notarization
  broke on empty `APPLE_*` variables ([#276]); the unwanted "Continue with Mac" step, the tray at
  sign-in and the blank window after updates ([#320]); client certificates on the home's
  database ([#349]).
- Lessons: Go on Windows ignores `TZ`; `go test … | tail` hides a failing exit code; Defender
  slows tests that start fresh executables; never raise a test tier's cap.

### 2026-10-31 · PC · c17dd289 · PC stuck on v0.3.42: the `maktaba://` link popup (in progress)

- Handed off from `f43fad60`: get the PC onto v0.3.44 and stop Windows' "Get an app" popup for
  the server's `maktaba://install-update` link.
- Cause: the Claude desktop app is an MSIX package. The installs and app starts run from
  Claude's shell (the hand-installs in `f43fad60`) registered `maktaba://` only in Claude's
  private registry copy, so the server, outside it, can't open the link and the app never got
  the v0.3.44 order. Claude's shell shows the key, so `reg query` there proves nothing. Rule:
  never install or start Maktaba from Claude's shell on this PC; the owner runs the installer.
- Decided 2026-10-31: agent installs (the recommended option): the always-running
  `Maktaba.exe --server-agent` installs a node's update itself instead of waking the desktop
  app (changes §2, Updates). Also a PR so the node's copy notices a dead link to the
  home's database within about a minute and ends replication workers that won't stop. Both are
  in progress there; their PRs go in its own later entry.

### 2026-10-31 · PC · c17dd289 · Windows node updates by the agent; the copy stall (done)

- [#456] (merged, be9ba4ef): the 10-28 copy stall. A table's first copy waited 15 min on the
  restarting home, and every fresh copy failed with "workers did not stop within 30s". Quiesce
  now ends workers still there after its wait, and its switch-off waits for the subscription's
  lock at most that wait (review finding). Tested against real Postgres, red before and green
  after.
- [#457]: the Windows server agent (`Maktaba.exe --server-agent`) answers the home's order
  instead of the window app. It stages from the release's own `latest.json`: a setup .exe on that
  release's page, checked with the app's key, its signed version (`requireSignedVersion`) and
  signed file name. At the go it starts the installer out of its own job. The server sends no
  `maktaba://` link (`MAKTABA_AGENT_ORDERS=1`); on Windows the window app only shows the order
  and no longer settles the orders' attempts.
- What it rests on, proved on CI's Windows runner (`check-task-children.ps1`), never on the
  owner's PC: once a task's process exits, the task is `Ready` even with its child still running;
  the task runs again meanwhile; the child outlives it. Two review rounds; fixed in the PR were
  answers that only move forward (`autoinstall::advance`), the window app's settle race, and a
  stricter signature and URL check.
- Neither PR is tagged: the next release is held (§5), and #457 touches the update path. Until
  a release carries #457, the PC needs the owner to open Maktaba once (or run the v0.3.44
  installer) to take v0.3.44.

[#48]: https://github.com/Hamza-Labs-Core/Maktaba/pull/48
[#52]: https://github.com/Hamza-Labs-Core/Maktaba/pull/52
[#55]: https://github.com/Hamza-Labs-Core/Maktaba/pull/55
[#57]: https://github.com/Hamza-Labs-Core/Maktaba/pull/57
[#73]: https://github.com/Hamza-Labs-Core/Maktaba/pull/73
[#75]: https://github.com/Hamza-Labs-Core/Maktaba/pull/75
[#78]: https://github.com/Hamza-Labs-Core/Maktaba/pull/78
[#81]: https://github.com/Hamza-Labs-Core/Maktaba/pull/81
[#101]: https://github.com/Hamza-Labs-Core/Maktaba/pull/101
[#107]: https://github.com/Hamza-Labs-Core/Maktaba/pull/107
[#123]: https://github.com/Hamza-Labs-Core/Maktaba/pull/123
[#127]: https://github.com/Hamza-Labs-Core/Maktaba/pull/127
[#128]: https://github.com/Hamza-Labs-Core/Maktaba/pull/128
[#130]: https://github.com/Hamza-Labs-Core/Maktaba/pull/130
[#136]: https://github.com/Hamza-Labs-Core/Maktaba/pull/136
[#141]: https://github.com/Hamza-Labs-Core/Maktaba/pull/141
[#142]: https://github.com/Hamza-Labs-Core/Maktaba/pull/142
[#143]: https://github.com/Hamza-Labs-Core/Maktaba/pull/143
[#145]: https://github.com/Hamza-Labs-Core/Maktaba/pull/145
[#157]: https://github.com/Hamza-Labs-Core/Maktaba/pull/157
[#169]: https://github.com/Hamza-Labs-Core/Maktaba/pull/169
[#170]: https://github.com/Hamza-Labs-Core/Maktaba/pull/170
[#173]: https://github.com/Hamza-Labs-Core/Maktaba/pull/173
[#174]: https://github.com/Hamza-Labs-Core/Maktaba/pull/174
[#175]: https://github.com/Hamza-Labs-Core/Maktaba/pull/175
[#196]: https://github.com/Hamza-Labs-Core/Maktaba/pull/196
[#198]: https://github.com/Hamza-Labs-Core/Maktaba/pull/198
[#200]: https://github.com/Hamza-Labs-Core/Maktaba/pull/200
[#201]: https://github.com/Hamza-Labs-Core/Maktaba/pull/201
[#204]: https://github.com/Hamza-Labs-Core/Maktaba/pull/204
[#205]: https://github.com/Hamza-Labs-Core/Maktaba/pull/205
[#212]: https://github.com/Hamza-Labs-Core/Maktaba/pull/212
[#216]: https://github.com/Hamza-Labs-Core/Maktaba/pull/216
[#217]: https://github.com/Hamza-Labs-Core/Maktaba/pull/217
[#218]: https://github.com/Hamza-Labs-Core/Maktaba/pull/218
[#220]: https://github.com/Hamza-Labs-Core/Maktaba/pull/220
[#221]: https://github.com/Hamza-Labs-Core/Maktaba/pull/221
[#222]: https://github.com/Hamza-Labs-Core/Maktaba/pull/222
[#223]: https://github.com/Hamza-Labs-Core/Maktaba/pull/223
[#226]: https://github.com/Hamza-Labs-Core/Maktaba/pull/226
[#227]: https://github.com/Hamza-Labs-Core/Maktaba/pull/227
[#229]: https://github.com/Hamza-Labs-Core/Maktaba/pull/229
[#230]: https://github.com/Hamza-Labs-Core/Maktaba/pull/230
[#231]: https://github.com/Hamza-Labs-Core/Maktaba/pull/231
[#232]: https://github.com/Hamza-Labs-Core/Maktaba/pull/232
[#234]: https://github.com/Hamza-Labs-Core/Maktaba/pull/234
[#237]: https://github.com/Hamza-Labs-Core/Maktaba/pull/237
[#239]: https://github.com/Hamza-Labs-Core/Maktaba/pull/239
[#240]: https://github.com/Hamza-Labs-Core/Maktaba/pull/240
[#243]: https://github.com/Hamza-Labs-Core/Maktaba/pull/243
[#244]: https://github.com/Hamza-Labs-Core/Maktaba/pull/244
[#254]: https://github.com/Hamza-Labs-Core/Maktaba/pull/254
[#258]: https://github.com/Hamza-Labs-Core/Maktaba/pull/258
[#259]: https://github.com/Hamza-Labs-Core/Maktaba/pull/259
[#276]: https://github.com/Hamza-Labs-Core/Maktaba/pull/276
[#307]: https://github.com/Hamza-Labs-Core/Maktaba/pull/307
[#320]: https://github.com/Hamza-Labs-Core/Maktaba/pull/320
[#362]: https://github.com/Hamza-Labs-Core/Maktaba/pull/362
[#456]: https://github.com/Hamza-Labs-Core/Maktaba/pull/456
[#457]: https://github.com/Hamza-Labs-Core/Maktaba/pull/457

---

## 10. Session log

Append here: date, session, one to five lines on what changed. Newest last.

- 2026-10-31 · docs-claude-trail: created this file, the `CLAUDE.md` rule and the memory
  pointer; traced all 697 Mac transcripts into §8.
- 2026-10-31 · Apple Arabic agent of `b743b7d4`: [#454] gives Remote, Player (iOS, Mac) and TV
  English and Arabic String Catalogs, with MaktabaKit's own via `Bundle.module`. "The home" is
  «الجهاز الرئيسي». The guard `tools/apple/check-string-catalogs.py` runs in the new
  `apple-apps` CI job. Kept: the hand-rolled Arabic in `ConnectStrings`, `PermissionsStrings`,
  `ErrorStrings` and `TVConnectStrings`.
  - Gotcha: `String(localized:bundle:locale:)` does not pick the language. Build a
    `LocalizedStringResource` with `.locale` (see `kit()` in `Localization.swift`).
  - Gotcha: the shared iPhone 17 simulator crashed SpringBoard repeatedly, so this work used a
    throwaway `maktaba-ar-test` on iOS 27.0 (iOS 27.1 is not compatible with that device type)
    and deleted it afterwards.
  - Known, unrelated failures: run on iOS, MaktabaKit's `thisPlatformAsksForLocalNetwork`
    expects the Mac's list; an unsigned run fails the Mac Player keychain test.
- 2026-10-31 · server-words-as-codes agent of `b743b7d4`: [#460] (not merged; the owner's
  session merges it). Server texts the admin screens show now go out as **code + params beside
  the English**: every TV check (`Check.Code`/`Params`, 48 codes, web key
  `tvchanges.value.<code>`), the failing move (`report_code`), the home's `roles_reason_code`.
  Web, Android and Apple render the code and fall back to the English. Triggers and check names
  got words on Android and iPhone; the iPhone now uses the supervisor's `detail_code`.
  - Decided: params are strings (`map[string]string`), already formatted by Go, so no client
    has to format numbers. The English `value` stays for older apps. A settling position check
    becomes `<code>_settling`; a budget with channels waiting becomes `<code>_rolling`.
  - Rule for new codes: `TestEveryCheckCodeHasItsWords` fails when a code in
    `verify.go`/`streamverify.go` has no en/ar words in `web/src/i18n`.
  - Left alone, see the PR: the streaming binary's own check values, update `error.detail` / step
    details (machine-written), `copy.detail`, `last_check_error`, events' `message`.
  - TV-staging code, additive only: `update-rehearsal` must pass on the exact commit before a
    release carries it.
  - Tooling: throwaway sim `maktaba-codes-throwaway` (iOS 27.0), deleted. `xcodebuild` needs
    `TEST_RUNNER_MAKTABA_SHOTS` to pass `MAKTABA_SHOTS` to UI tests. The Android Remote has
    flavors: run `:remote:testPhoneDebugUnitTest`, not `testDebugUnitTest`. The web uses pnpm
    (`pnpm install --frozen-lockfile`); `npm ci` fails, because there is no npm lockfile.
- 2026-10-31 · Epic 48 spec agent of `b743b7d4`: [#458] writes `specs/epics/48-native-players/`
  after a review found the Players thin. Epic 42 asked only for
  libraries and playback, so its checks passed with no Home, nav, Live TV or brand.
  - Decided: Live TV for the TV apps is *in* 48 (own track, files disjoint); downloads, the
    Remote's screens and the Windows/Linux player are follow-ups; one token source in
    `web/design-system/tokens/` generating Swift and Kotlin (its dark set is stale grey today:
    the shipped values are in `web/src/styles/tokens.css`).
  - Found, a live security gap (G1): the web port's `/stream/channel/{id}` gate checks sign-in
    only, not the channel's library (`cmd/maktaba-server/stream_gate.go`). Story 48.3 fixes it
    first, with role tests.
  - Also found: both apps default `ThemeMode` to System (so a light phone starts light); the
    Apple Player's iOS picture is SwiftUI `VideoPlayer` (no PiP); CI builds the Apple apps but
    runs no UI tests, and runs no Android Player tests.
  - Waiting on the owner: review [#458] (not merged), and its five open questions.
- 2026-10-31 · Story 48.9 agent of `b743b7d4`: Android client Live (`apps/android/client/…/api/Live.kt`):
  now, guide, one channel's guide, logo / poster / stream URLs, `tv/info`, tune, heartbeat, end,
  and `LiveSource` (LAN or relay from `tv/info.remote.via_relay`).
  - Decided: a `LiveApi(client)` wrapper, as Android TV's `TvApi` does, not new methods in
    `ApiClient.kt` (its `send` is private and the file is no story's). It reuses
    `ApiClient.request` (internal: base path and headers), `playbackToken` and `refreshAfter`.
  - Decided: a heartbeat's new token does not change `LiveSource.stream`, so the player is not
    reopened. Only `token` changes, and `LiveSource.interceptor` writes it into each Cloudflare
    request, as hls.js does on the web. Opening a new channel ends the old session first, so
    the per-user stream limit never trips.
  - Tested: JVM tests (`LiveApiTest` on MockWebServer, `LiveSourceTest` on virtual time with a
    fake home). Ran once against a branch stack (scratch Postgres, branch API): every route
    decoded. The stack has no Cloudflare, so tune answered 503 `remote_unavailable` as expected.
  - Surprise: on a fresh DB, `POST /api/channels` answered 201 but wrote no `channels` row (TV
    staging, it seems). The stack check seeded a channel by SQL.
- 2026-10-31 · Epic 48 story 48.8 agent of `b743b7d4`: [#464] adds the Android client's
  browse calls (`apps/android/client/…/core/api/Browse.kt`): Home, recommendations, libraries
  and pins, hub, grid, facets, series, a video's page, marks, search.
  - Decided: calls are `ApiClient` extensions in their own file. To make that possible,
    `ApiClient.get`/`decode`/`send` went from `private` to `internal`; 48.9 needs the same edit.
    A 404 is `NotAvailable`, a new `ApiException` subclass whose cause is the `Status`, so
    `errorKind()` and Details work without touching `HomeError.kt`. Lists decode through
    `LenientList`, which drops elements it cannot read.
  - Fixtures are hand-made from the Go structs, not recorded from a branch stack. To keep them
    honest, `api/internal/handlers/browse/native_fixtures_test.go` decodes each one into the
    handlers' types with unknown fields refused.
  - Left out: the Profile routes (48.31's).
- 2026-10-31 · Epic 48 story 48.7 agent of `b743b7d4`: MaktabaKit's `Live.swift`: now, the
  guide window, one channel's guide, the logo, `tv/info`, the LAN playlist, and remote tune,
  heartbeat and end, as an `APIClient` extension. `LiveSource` picks LAN or relay from
  `tv/info`, tunes through 202s, and heartbeats every 60 s. A new token is swapped in through
  `rewrite(_:)`, with no new tune and no new player item.
  - Premise gap: an extension in another file cannot POST or DELETE, because
    `APIClient.send` and `sendRaw` were `private`. Made both internal (two words in
    `APIClient.swift`). 48.6 (`Browse.swift`) needs the same change.
  - Decided: the clock is an injected `sleep`, as `waitUntilPlayable` already does. The tests
    use a hand-advanced `FakeClock` and a `FakeLiveAPI`. The HTTP tests use their own
    `LiveStub`, separate from the shared `Stub` that other suites use.
  - Left to 48.21: the encoder warm-up retry on the LAN path, and the AVPlayer side of
    `rewrite(_:)` (a resource loader or proxy).
- 2026-10-31 · Story 48.2 agent of `b743b7d4`: [#462], one token source. `tokens.json` /
  `tokens.dark.json` now hold the shipped Ink & Brass (values copied from
  `web/src/styles/tokens.css`, which stays hand-written and is what the web ships), plus
  `accent-fg`, `accent-text`, `accent-soft`, `border`, `running`, `info`, `type.size.*`,
  `spacing.5`, and the apps-only `raised`, `nav`, `scrim`, `chosen`. `build-tokens.mjs` also
  writes `MaktabaKit/Tokens.swift` (`MaktabaTokens.Colors.*` as 0xRRGGBBAA light/dark pairs
  with a dynamic SwiftUI `.color`) and core's `ui/Tokens.kt` (`MaktabaTokens.Light/Dark`,
  Compose `Color`/`dp`/`sp`). CI's unit job runs `make test-tokens`: `--check` stale test,
  CSS parity, contrast on 70 text/surface pairs, and self-tests that each check catches a bad
  input.
  - Decided: generate, not replace, `tokens.css` (its glass/art/shadow values are web-only
    and a swap would touch every screen); light `raised` is `#FFFDF8`, because `#F8EFDB` put
    light `running` text at 4.38:1. `scrim` is not contrast-checked: its text sits on the
    library art, which 48.10 / 48.11 check in screenshots.
  - Gotcha: a MaktabaKit test suite named `Tokens` clashes with an existing one (keychain
    tests); it is `DesignTokens`.
- 2026-11-01 · story 48.6 agent of `b743b7d4`: [#467] MaktabaKit `Browse.swift`, the browse client
  (Home to Profile, not Live, which is 48.7, nor the player's session options, which stay
  `openStream` until 48.23 needs more).
  - Decided: fixtures are real answers. The branch API ran on a scratch Postgres (port 55486,
    a viewer of one library of two, seeded series, chapters, transcript, playlist, search),
    recorded by a script as that viewer into `MaktabaKit/Tests/Fixtures/Browse/` (outside the
    test target, read by `#filePath`, so `Package.swift`, owned by 48.10, is untouched). A Go
    test (`browse/native_fixtures_apple_test.go`) decodes them into the handlers' types with unknown
    fields refused, so renaming a field fails in the PR that renames it.
  - Decided: `send` and `sendRaw` in `APIClient.swift` lost `private` (an extension in another
    file needs them; 48.7 needs the same two-word change, which git merges cleanly).
  - Decided: new model names avoid the apps' views (`BrowseVideo`, `VideoInfo`,
    `VideoListPage`: the Player has a `VideoDetail` view, tvOS `VideoCard` and `VideoPage`).
  - Found: a hidden video answers **403**, not 404 (`videos.canRead`), against FR-18's premise;
    the client maps a video route's 403 to `.notAvailable` too. `GET /api/series/{id}` answers
    its counts as 0 (only the list counts): a series page must count its episodes.
  - Failed first: `initdb` without `-E UTF8` made a SQL_ASCII database, and the facets route
    500'd on Arabic first letters; recreated the DB as UTF8 (not an API bug).
- 2026-10-31 · Epic 48 story 48.5 agent of `b743b7d4`: `?size=small` on
  `GET /api/videos/{id}/poster` (`api/internal/handlers/videos/poster.go`, tests in
  `poster_size_test.go`).
  - Decided: small = the pipeline's `thumb_small` copy (`<hash>/small.jpg`, then the import's
    staged `imported/<id>.jpg`), else the full chain. The API generates nothing: the pipeline
    makes each size once and keeps it in the thumbnail cache, and both sizes keep
    `Cache-Control: private, max-age=3600`. An unknown `size` is a 400.
  - Decided: the spec asks 404 for a viewer without the library, so the poster route now
    answers a signed-in viewer without access exactly as a missing video (it said 403 before);
    no one signed in still gets 403. The other video routes still say 403.
  - Changed: the query reads `library_id` without `::text`, so the handler runs on the SQLite
    test table too (lib/pq scans a uuid into a string either way, as `trickplay.go` does).
- 2026-10-31 · Epic 48 story 48.4 agent of `b743b7d4`: [#468] lists a video session's sidecar
  subtitles as HLS `SUBTITLES` renditions in its master (one-segment WebVTT playlist at
  `subs-{lang}/index.m3u8` over the sidecar route), so AVPlayer, AirPlay and Cast can show them.
  No `stream_gate.go` change: both paths fit its patterns (a test copies the regexes).
  - Failed first: without `X-TIMESTAMP-MAP` AVPlayer and hls.js showed every cue 1.4 s early
    (ffmpeg's MPEG-TS starts 1.4 s in; `-output_ts_offset` keeps video time). Fix: the sidecar
    route with `?hls=1` adds `MPEGTS:126000`; checked with AVPlayer on a real transcode from 30 s
    (cues on time) and a real-ffmpeg test in `tools/media-e2e.sh`.
  - Found: the API never sends `session.subtitles`, so the web's `<track>` list was always
    empty; hls.js now gives the web subtitles through the renditions.
- 2026-10-31 · Epic 48 story 48.3 agent of `b743b7d4`: closes G1. `stream_gate.go` asks
  `GET /api/channels/{id}` (the channels handler's `canRead`, the same rule as the guide's
  `visibleChannels`) for every non-admin `/stream/channel/{id}/…` and
  `/stream/via-home/channel/{id}/…`, caching allowed and refused verdicts per credential hash
  and channel for `streamAuthTTL` (30 s).
  - Decided: 404 and 403 from the API both become a 403, so the gate does not tell which ids
    exist. A 401 from the channel check drops the cached session too. A 5xx is a 502 and is
    never cached. Admins skip the check: the admin's staging `Get` reads draft rows, and
    admins see everything anyway.
  - Added an API role test (`api/internal/handlers/channels/get_access_test.go`). Before it,
    nothing tested `Get`'s library rule, and the gate now depends on it.
  - Needs the update-rehearsal CI green on its exact commit before merge, because it changes
    the front door.
- 2026-10-31 · flaky-tests agent of `b743b7d4`: [#470] fixes three flakes at the root.
  - Enrich near midnight: the test, not the product. `seconds_to_next_day` floors at 60 s, so
    `not_before` can be exactly now + 60 s. Read the clock *before* the run, bound inclusively.
  - Windows Resume: a product bug. `mutex_held` meant "the mutex exists". An exiting agent has
    released it but keeps the handle until its process is gone (Task Scheduler still counts it
    running), so `start_task` sent one `/Run` into IgnoreNew. Now "held" means owned (a
    zero-timeout wait).
  - Unit-tier soft cap: its run-again reused the first run's `tmp_path`, so any test that
    `mkdir`s or appends there failed the run-again, and one runner stall failed the test. Now
    the run-again gets a fresh `tmp_path`. A "> 3x soft cap" failure with no "run again" time
    means the run-again raised, not that the test was slow twice.
  - Cross-check Windows Rust from this Mac: rustup's cargo (Homebrew's shadows it), the
    `x86_64-pc-windows-msvc` target, `CC_x86_64_pc_windows_msvc=clang-cl` from Homebrew LLVM,
    `AR_…=llvm-lib`, plus stub `assert.h`/`string.h`/`stdlib.h` via `CFLAGS_…="/imsvc <dir>"`
    so `ring` builds. `cargo check` and `cargo clippy` then work. No linking, no running.
  - 2026-10-31, later (review of [#470]): swapping only `tmp_path` left fixtures built on it
    (e.g. `_settings` in `tests/models/test_fetch_cli.py`) on the first folder. The soft cap
    now runs a slow test again through `runtestprotocol` (setup, call, teardown, all
    function-scoped fixtures afresh, as pytest-rerunfailures does) and logs the reports only
    after judging. A forced re-run of all 274 cluster unit tests passes. Before, 66 failed.
    Also pinned the enrich test's budget clock, so midnight between its two runs gives no
    fresh allowance. The same signature failed `test_linux_nfs_mount_found_through_proc_mounts`
    on #467: 0.945 s and no run-again time, because the re-run raised.
  - Not mine, seen on #470's integration run: `TestRowCacheInvalidationPostgres`
    (`api/internal/handlers/browse`): "a scan's videos: …b's entry dropped too". Likely a late
    notice of an earlier write dropping libB after `fill()` saw both rows cached.
- 2026-10-31 · Story 48.13 agent of `b743b7d4`: the Apple Player shell and its UI-test harness
  (branch `feat/48.13-apple-player-shell`).
  - Built: `Player/Shell/` holds `PlayerShell` (a `TabView` with `.sidebarAdaptable`, one
    `[PlayerRoute]` stack per section; choosing the chosen tab pops it), `PlayerSection`
    (`enabled` = `[.libraries]`), `PlayerRoute` (video, series, library, channel, watch) with
    one `playerDestinations`, and `Shell.xcstrings`. Placeholder files sit in each screen's
    folder (`Home/`, `Libraries/`, `Video/`, `Search/`, `Live/`, `Profile/`, `Watch/`); each
    screen story owns its file. `LibraryBrowser.swift` moved to `Libraries/`.
  - Decided: with one section enabled, the shell shows that screen alone (no one-tab bar), so
    TestFlight builds look exactly as before until a second section merges. Debug-only launch
    arguments (`-maktaba-sections all`, `-maktaba-section`, `-maktaba-open video/v1`) let the
    tests reach screens that are not turned on yet; Release builds ignore them.
  - Decided: MockHome serves `UITests/Fixtures/*.json` (shapes taken from the Go handlers,
    with a `{{now±N}}` time token) through `routes.json`. It adds `stub`, `delay` and
    `requests` (with query and body). The string guard now checks every table of a target
    against the `<table>.xcstrings` in its folders, so a screen's new table needs no guard edit.
  - CI: a new `player-ui-tests` job in `_apple-apps.yml` runs every `UITests/Player*Tests.swift`
    class on throwaway iPhone and iPad simulators (deleted after) and uploads the screenshots.
  - Failed, then fixed: relaunching without `-maktaba-session-reset` went back to sign-in,
    because an unsigned test build keeps no Keychain token. So every UI test signs in fresh.
    The Duo hinge split was only built with Xcode 27 (it is `LibraryBrowser`'s and unchanged);
    it was not exercised on a Duo simulator.

- 2026-10-31 · Epic 48.15 agent of `b743b7d4`: the Android Player shell. `player/…/shell/`:
  `PlayerShell` (Material's `NavigationSuiteScaffold`: bar compact or tabletop, rail medium,
  permanent drawer expanded with medium height; a phone on its side keeps the rail), one
  Navigation Compose `NavHost` per section kept by a `SaveableStateHolder` (each section's stack
  and scroll survive switching; re-select pops to root; Back at another section's root goes to
  the first), typed `Route`s (`video`, `series`, `library`, `channel`, `watch` as on Apple), and
  `PlayerSection.enabled` = Libraries only. `LibraryBrowser.kt` moved to `libraries/`; the
  Libraries section is that browser until 48.27. Placeholder screens live in each screen's own
  folder (`home/`, `video/`, `search/`, `live/`, `profile/`, `watch/`) so the screen stories
  only replace their own file.
  - Decided: one reachable section shows no navigation at all (a bar of one is no choice), so
    the shipped app looks as before until a second screen merges.
  - Harness: Robolectric 4.17 + Compose UI tests + Roborazzi on the JVM, a `FakeHome`
    (MockWebServer) in `player/src/test/…/harness/`. CI runs `:player:testPhoneDebugUnitTest`
    and `:player:verifyRoborazziPhoneDebug`; references in `player/src/test/screenshots/`.
  - Gotcha: Robolectric's SDK 36 needs Java 21; CI builds with 17, so `robolectric.properties`
    pins `sdk=35`. The references were recorded on the Mac (2 % pixel tolerance for Linux text).
  - Gotcha: core's `TranslationsTest` reads only `strings.xml`; the per-screen
    `strings_<screen>.xml` tables are checked by the new `PlayerTranslationsTest` (and lint).
  - Gotcha: with many agents building at once the Gradle daemon died of GC thrashing; pass
    `-Dorg.gradle.jvmargs=-Xmx6g`.
  - Review fixes: the Libraries browser's state is kept per signed-in home (`PlayerHome.keep`), so
    a section switch keeps its open video, library and search, and re-selecting Libraries closes
    the video (`OnReselect`); `PlayerNavigator.back()` never pops a section's root (Navigation
    Compose will pop it and leave the section blank); `FakeHome.requests` is a locked copy.
- 2026-11-01 · Epic 48 lead of `b743b7d4` (batch from PR bodies; agents no longer edit this
  file or `CHANGELOG.md`, so open PRs stop conflicting):
  - [#463] 48.7, MaktabaKit's Live client (`Live.swift`: now, guide, `tv/info`, tune, heartbeat,
    end) and `LiveSource`, which picks LAN or relay and swaps a heartbeat's new token without
    reopening the player. `APIClient.send`/`sendRaw` went from private to internal so client
    files can be `APIClient` extensions.
  - [#465] 48.9, the Android client's Live calls (`LiveApi`, `LiveSource`), a wrapper class like
    Android TV's `TvApi`, so `ApiClient.kt` is unchanged.
  - [#479] 48.11, Android brand: `ThemeMode.parse` falls back to Dark; one dark-parent
    `Theme.Maktaba` (`values-night` deleted); Material's `scrim` left at its default (the light
    scrim token is cream). Failed first: screenshots drew black headings until the harness had
    MainActivity's `Surface`.
  - [#480] 48.10, Apple brand: `ThemeMode.storageKey` registers `dark` as the default, so the
    apps' `@AppStorage(…) = .system` reads dark without editing other stories' files. Found: the
    plan's hand-edited `Info-*.plist` launch screen can't work (xcodegen regenerates it); it lives
    in `project.yml`'s `IOSApp` template. A new `UITests/*.swift` needs `xcodegen generate` or
    the run executes 0 tests.
  - [#474] 48.12 (G2): `stream_gate.go` lets a `?sig=` request with no credentials through on
    `/stream/{session}/…`, `/stream/direct/{video}` and node `/stream/via-home/`. The gate has no
    key, so it reads only aud and sub (mismatch 403); expiry is left to streaming, whose leeway
    is configurable (a fixed 60 s gate check was stricter, dropped in review). Open: DASH
    top-level segments (`/stream/{session}/init-*.m4s`, `chunk-*.m4s`) are not on the allow-list;
    48.24 or 48.33 must add them before AirPlay or Cast uses DASH.
  - [#476] spec: findings from TestFlight testing. "No audio" traced to codec-less
    `audio_tracks` rows, not the Player's audio session (already `.playback`); AirPlay and the
    video page's poster became criteria of 48.24 and 48.19; 48.19 gained `maktaba://video/{id}`.
  - [#477] silent streams: transcribe's bare `audio_tracks` row (68e9e903) plus the probe's
    `ON CONFLICT DO NOTHING` left 32,381 of 148,589 videos with a codec-less default track,
    which streaming read as no audio. Now the probe `DO UPDATE`s, transcribe copies probe facts,
    streaming's `has_audio` also reads `raw_ffprobe`, and slot 0171 backfills. Rejected: a
    per-play `raw_ffprobe` fallback (hides bad rows). Gotchas: `jsonb_array_elements(...) s`
    let bare `s` resolve to an outer column (use `AS au(stream)`); a third SchemaRev lives in
    `schemaguard/guard.go`.
  - Flakes fixed at the root: [#471] the row-cache test waits on its own `pg_notify` marker
    (one connection hears notices in commit order); [#473] the home pushes its database CA when a
    machine agent connects or leaves, not only every 30 s; [#475] the relay records a home online
    before routable and offline after removal, one at a time per slug; [#472] host apt installs
    get retries, timeouts and an 8-minute step limit after stalled mirrors hung jobs 30 min.
- 2026-11-01 · airplay (side session of `local_7c0a4c88`): [#512] Epic 48 story 48.24 in the
  Apple Player: AirPlay route button, PiP (button and on going home) and Now Playing with
  remote commands, for videos and live TV, English and Arabic.
  - Decided: a receiver can't reach the app's loopback `StreamProxy`. A video hands it the
    signed session URL (48.12). A channel at home has no signed URL (the web port's gate keeps
    channel paths to a signed-in caller), so a second `StreamProxy` listens on the phone's LAN
    address while AirPlay is on, serving that one channel under its own secret: no server
    change. Away, the Cloudflare signed URL is handed over and re-handed at each new token
    (about every 30 min). The phone stays in the loop either way (progress, heartbeat).
  - Not verified: playback on a real Apple TV (no receiver in CI); the spec box for that
    stays unticked until the owner checks it by hand.
  - Gotcha: a closure a system framework calls on its own queue (`MPMediaItemArtwork`'s
    image, KVO handlers, `MPRemoteCommand` targets) must not be made inside a `@MainActor`
    method: Swift 6 gives it main-actor isolation and traps off the main thread. Make it in a
    `nonisolated static func` and hop on. The first CI run of [#512] crashed every watch test so.
  - The iPhone simulator reports no Picture in Picture (the iPad one floats it); the PiP UI
    test asserts there is no PiP button on iPhone (no skip: tests are never skipped). `AVRoutePickerView` adds its inner button late on iPad:
    `AirPlayButton.Picker` names every control in it once it is on screen.
  - The Mac menu helper clicked View ▸ Profile when asked for Live TV: a coordinate on a menu
    item finds it again by identifier, and every SwiftUI menu item's is `menuAction:` (the
    recording showed the pointer move from Live TV to Profile). It now clicks the item's point
    from the menu bar item, whose title is unique.
- 2026-11-01 · play-bundles agent of `b743b7d4`: [#517] (not merged) attaches signed Google Play
  bundles to every `v*` tag (`play-bundles.yml`): `maktaba-{player,remote,tv}-<version>.aab`,
  upload key from `ANDROID_UPLOAD_KEYSTORE_BASE64` / `_KEY_ALIAS` / `_KEY_PASSWORD` (/ optional
  `_STORE_PASSWORD`), unsigned only as a workflow artifact. Docs: `docs/google-play.md`.
  - Decided: the Android Player and Remote lose their `phone`/`foldable`/`tablet` flavours (they
    differed only in the `.foldable`/`.tablet` package; Play makes each package its own app). The
    `X-Maktaba-Client` device now comes from the screen (`ClientHeader.deviceFor`: hinge sensor =
    foldable, smallest width ≥ 600 dp = tablet). Tasks are `testDebugUnitTest`,
    `verifyRoborazziDebug`, `lintDebug` now (the 2026-10-31 note on `testPhoneDebugUnitTest` no
    longer holds). Rejected: keeping the flavours for dev with a base-package release build.
  - Decided: versionCode = MAJOR*1000000 + MINOR*1000 + PATCH (`tools/release/play-version.sh`,
    tested); a pre-release shares its version's code, so the optional Play upload
    (`vars.PLAY_UPLOAD == 'true'` + `PLAY_SERVICE_ACCOUNT_JSON`, internal track) skips pre-releases.
  - The Android gates now build an unsigned `bundleRelease` (R8 breaks show in the PR).
  - Gotchas: emulators on this Mac hang at boot headless with the android-37.0 and android-36
    google-tv images (exit 139); an android-35 google_apis AVD boots. bundletool works from the
    Gradle cache's jar with a hand-built classpath (guava -jre, dagger 2.28.3, `--aapt2` from
    build-tools), or use the universal APK from `build-apks`. `LibrariesSectionTest.everyControlHasWordsForTalkBack`
    failed once under load (R8 + lint in parallel) and passed alone: a load-sensitive flake left
    to fix at the root.
  - Waiting on the owner: make the upload key (keytool in `docs/google-play.md`) and add the
    secrets; create the three apps in Play Console and upload each first bundle by hand; later
    turn on `PLAY_UPLOAD`.
- 2026-11-01 · affected-tests agent of `b743b7d4`: [#516] (not merged; only the PR was asked
  for). **New rule:** a push that changes code also changes the tests that cover it, and
  this fails at once, before CI. Why: [#469] made the Player's sign-in a segmented choice, and the
  Mac UI tests (`PlayerMacTests/UI/PlayerMacMenuUITests.swift`, which tap "With a code") went
  stale with nothing running them.
  - `tools/affected-tests` lists a range's tests by platform. It uses a coverage map, the UI
    search (identifiers and the strings on the changed lines, keyed strings resolved) and naming.
    The web adds its import graph. `gate` blocks in three cases: a source with none of its tests
    changed, a UI platform whose tests use the changed phrase but did not change, or a test still
    using a string that no source has any more. A commit trailer `No-Test-Change: <reason>` lets a
    push through.
  - The hook: `tools/affected-tests/install-hook.sh` writes `.git/hooks/pre-push`, which runs
    `.githooks/pre-push`. `core.hooksPath` was rejected: the pre-commit framework refuses to
    install with it set. The hook runs Go, Python, web, Android/JVM and MaktabaKit tests. Apple
    UI targets are only compiled (`build-for-testing`, unsigned), and `safeToRun` refuses any
    other `xcodebuild test`.
  - CI: the `affected-tests` job reports into `ci-success`. The map is built in its own
    `coverage-map.yml` (`workflow_run` after CI on main), not in ci.yml: the tag guard needs
    CI's whole run on the commit green, so a map job there could block a release and would add
    to its wait.
  - Granularity: Go per package (`-coverpkg` over the module); Python per test (`--cov-context=test`);
    MaktabaKit and the Mac Player per xccov target; Android per JaCoCo module, through an init
    script; web per test file, from its import graph. iPhone/iPad UITests are not measured: that
    run takes most of an hour, so coverage there could hit its 60-minute limit.
  - Failed first, then fixed: the first pytest-context map pinned 100+ modules on whichever test
    imported them first. Now a `-p pytest_preimport` plugin imports the package before any test,
    and only lines inside functions count. A UI hit on any platform used to satisfy the gate,
    which would not have caught [#469]: strict terms (identifiers, phrases with a space) now need
    a change on each test platform that uses them.
  - Not verified until it runs on GitHub: the coverage-map workflow, the xccov and JaCoCo
    fragment steps (JaCoCo via the init script was checked locally for `:client` and `:core`).
  - CodeQL #108 (cache poisoning) was raised against the first `coverage-map.yml`, which checked
    out the triggering run's `head_sha` and used setup-go/setup-uv caches. Fixed rather than
    dismissed. It now runs only for a CI run of a push to main in this repo (or a manual run on
    main), checks out its own commit, has every cache off, and reads artifacts as data only.
    Keep it that way: any `workflow_run` workflow with caches brings the alert back.
  - The `Player UI tests (mac)` job failed on #516 with "the home picker" in
    `PlayerMacMenuUITests`. It runs only because this PR touches `_apple-apps.yml`, and
    `feat/airplay` fails the same way. That is the existing Mac UI window bug that [#510] fixes,
    not this change.
  - Fitted to [#528]'s path filters: `affected-tests` is `# ci-paths: always-run, pr-only` (every
    change may need its tests). `tools/affected-tests/**` sits in the `_go` group (it is in
    GO_MODULES). `.githooks/**` and `coverage-map.yml` are listed in `NOT_IN_CI`.
- 2026-11-01 · `b743b7d4`: CVE-2026-98432 (Linux IPv4 TCP use-after-free, critical). Its reported
  exploit needs a multipath TCP listener, which Go 1.24+ opens by default. `godebug
  multipathtcp=0` in the api, cloud, streaming, maktaba-server and maktaba-launcher `go.mod`s,
  each with `TestListenersUseNoMultipathTCP` (it fails if the line goes; checked). No `go.work`,
  so release builds get it. User advisory (en + ar):
  `docs/security/advisories/2026-11-01-linux-tcp-uaf.md`. The VPS kernel is not patched: the
  owner patches it, or says go.
  - Gap: specs say a `SECURITY.md` exists (story 23.8); there is none in the repo.

[#1]: https://github.com/Hamza-Labs-Core/Maktaba/pull/1
[#2]: https://github.com/Hamza-Labs-Core/Maktaba/pull/2
[#41]: https://github.com/Hamza-Labs-Core/Maktaba/pull/41
[#45]: https://github.com/Hamza-Labs-Core/Maktaba/pull/45
[#46]: https://github.com/Hamza-Labs-Core/Maktaba/pull/46
[#47]: https://github.com/Hamza-Labs-Core/Maktaba/pull/47
[#49]: https://github.com/Hamza-Labs-Core/Maktaba/pull/49
[#54]: https://github.com/Hamza-Labs-Core/Maktaba/pull/54
[#56]: https://github.com/Hamza-Labs-Core/Maktaba/pull/56
[#58]: https://github.com/Hamza-Labs-Core/Maktaba/pull/58
[#61]: https://github.com/Hamza-Labs-Core/Maktaba/pull/61
[#62]: https://github.com/Hamza-Labs-Core/Maktaba/pull/62
[#69]: https://github.com/Hamza-Labs-Core/Maktaba/pull/69
[#70]: https://github.com/Hamza-Labs-Core/Maktaba/pull/70
[#72]: https://github.com/Hamza-Labs-Core/Maktaba/pull/72
[#97]: https://github.com/Hamza-Labs-Core/Maktaba/pull/97
[#104]: https://github.com/Hamza-Labs-Core/Maktaba/pull/104
[#108]: https://github.com/Hamza-Labs-Core/Maktaba/pull/108
[#109]: https://github.com/Hamza-Labs-Core/Maktaba/pull/109
[#110]: https://github.com/Hamza-Labs-Core/Maktaba/pull/110
[#113]: https://github.com/Hamza-Labs-Core/Maktaba/pull/113
[#117]: https://github.com/Hamza-Labs-Core/Maktaba/pull/117
[#119]: https://github.com/Hamza-Labs-Core/Maktaba/pull/119
[#121]: https://github.com/Hamza-Labs-Core/Maktaba/pull/121
[#122]: https://github.com/Hamza-Labs-Core/Maktaba/pull/122
[#125]: https://github.com/Hamza-Labs-Core/Maktaba/pull/125
[#137]: https://github.com/Hamza-Labs-Core/Maktaba/pull/137
[#138]: https://github.com/Hamza-Labs-Core/Maktaba/pull/138
[#139]: https://github.com/Hamza-Labs-Core/Maktaba/pull/139
[#146]: https://github.com/Hamza-Labs-Core/Maktaba/pull/146
[#148]: https://github.com/Hamza-Labs-Core/Maktaba/pull/148
[#149]: https://github.com/Hamza-Labs-Core/Maktaba/pull/149
[#150]: https://github.com/Hamza-Labs-Core/Maktaba/pull/150
[#151]: https://github.com/Hamza-Labs-Core/Maktaba/pull/151
[#152]: https://github.com/Hamza-Labs-Core/Maktaba/pull/152
[#153]: https://github.com/Hamza-Labs-Core/Maktaba/pull/153
[#154]: https://github.com/Hamza-Labs-Core/Maktaba/pull/154
[#155]: https://github.com/Hamza-Labs-Core/Maktaba/pull/155
[#158]: https://github.com/Hamza-Labs-Core/Maktaba/pull/158
[#159]: https://github.com/Hamza-Labs-Core/Maktaba/pull/159
[#165]: https://github.com/Hamza-Labs-Core/Maktaba/pull/165
[#166]: https://github.com/Hamza-Labs-Core/Maktaba/pull/166
[#167]: https://github.com/Hamza-Labs-Core/Maktaba/pull/167
[#177]: https://github.com/Hamza-Labs-Core/Maktaba/pull/177
[#178]: https://github.com/Hamza-Labs-Core/Maktaba/pull/178
[#192]: https://github.com/Hamza-Labs-Core/Maktaba/pull/192
[#195]: https://github.com/Hamza-Labs-Core/Maktaba/pull/195
[#197]: https://github.com/Hamza-Labs-Core/Maktaba/pull/197
[#203]: https://github.com/Hamza-Labs-Core/Maktaba/pull/203
[#211]: https://github.com/Hamza-Labs-Core/Maktaba/pull/211
[#214]: https://github.com/Hamza-Labs-Core/Maktaba/pull/214
[#241]: https://github.com/Hamza-Labs-Core/Maktaba/pull/241
[#248]: https://github.com/Hamza-Labs-Core/Maktaba/pull/248
[#249]: https://github.com/Hamza-Labs-Core/Maktaba/pull/249
[#250]: https://github.com/Hamza-Labs-Core/Maktaba/pull/250
[#251]: https://github.com/Hamza-Labs-Core/Maktaba/pull/251
[#257]: https://github.com/Hamza-Labs-Core/Maktaba/pull/257
[#271]: https://github.com/Hamza-Labs-Core/Maktaba/pull/271
[#287]: https://github.com/Hamza-Labs-Core/Maktaba/pull/287
[#288]: https://github.com/Hamza-Labs-Core/Maktaba/pull/288
[#290]: https://github.com/Hamza-Labs-Core/Maktaba/pull/290
[#291]: https://github.com/Hamza-Labs-Core/Maktaba/pull/291
[#292]: https://github.com/Hamza-Labs-Core/Maktaba/pull/292
[#295]: https://github.com/Hamza-Labs-Core/Maktaba/pull/295
[#296]: https://github.com/Hamza-Labs-Core/Maktaba/pull/296
[#300]: https://github.com/Hamza-Labs-Core/Maktaba/pull/300
[#304]: https://github.com/Hamza-Labs-Core/Maktaba/pull/304
[#305]: https://github.com/Hamza-Labs-Core/Maktaba/pull/305
[#306]: https://github.com/Hamza-Labs-Core/Maktaba/pull/306
[#309]: https://github.com/Hamza-Labs-Core/Maktaba/pull/309
[#310]: https://github.com/Hamza-Labs-Core/Maktaba/pull/310
[#313]: https://github.com/Hamza-Labs-Core/Maktaba/pull/313
[#314]: https://github.com/Hamza-Labs-Core/Maktaba/pull/314
[#317]: https://github.com/Hamza-Labs-Core/Maktaba/pull/317
[#319]: https://github.com/Hamza-Labs-Core/Maktaba/pull/319
[#321]: https://github.com/Hamza-Labs-Core/Maktaba/pull/321
[#324]: https://github.com/Hamza-Labs-Core/Maktaba/pull/324
[#325]: https://github.com/Hamza-Labs-Core/Maktaba/pull/325
[#326]: https://github.com/Hamza-Labs-Core/Maktaba/pull/326
[#327]: https://github.com/Hamza-Labs-Core/Maktaba/pull/327
[#328]: https://github.com/Hamza-Labs-Core/Maktaba/pull/328
[#329]: https://github.com/Hamza-Labs-Core/Maktaba/pull/329
[#331]: https://github.com/Hamza-Labs-Core/Maktaba/pull/331
[#334]: https://github.com/Hamza-Labs-Core/Maktaba/pull/334
[#335]: https://github.com/Hamza-Labs-Core/Maktaba/pull/335
[#336]: https://github.com/Hamza-Labs-Core/Maktaba/pull/336
[#339]: https://github.com/Hamza-Labs-Core/Maktaba/pull/339
[#340]: https://github.com/Hamza-Labs-Core/Maktaba/pull/340
[#342]: https://github.com/Hamza-Labs-Core/Maktaba/pull/342
[#343]: https://github.com/Hamza-Labs-Core/Maktaba/pull/343
[#344]: https://github.com/Hamza-Labs-Core/Maktaba/pull/344
[#345]: https://github.com/Hamza-Labs-Core/Maktaba/pull/345
[#346]: https://github.com/Hamza-Labs-Core/Maktaba/pull/346
[#348]: https://github.com/Hamza-Labs-Core/Maktaba/pull/348
[#349]: https://github.com/Hamza-Labs-Core/Maktaba/pull/349
[#350]: https://github.com/Hamza-Labs-Core/Maktaba/pull/350
[#351]: https://github.com/Hamza-Labs-Core/Maktaba/pull/351
[#353]: https://github.com/Hamza-Labs-Core/Maktaba/pull/353
[#356]: https://github.com/Hamza-Labs-Core/Maktaba/pull/356
[#358]: https://github.com/Hamza-Labs-Core/Maktaba/pull/358
[#359]: https://github.com/Hamza-Labs-Core/Maktaba/pull/359
[#364]: https://github.com/Hamza-Labs-Core/Maktaba/pull/364
[#366]: https://github.com/Hamza-Labs-Core/Maktaba/pull/366
[#367]: https://github.com/Hamza-Labs-Core/Maktaba/pull/367
[#368]: https://github.com/Hamza-Labs-Core/Maktaba/pull/368
[#369]: https://github.com/Hamza-Labs-Core/Maktaba/pull/369
[#372]: https://github.com/Hamza-Labs-Core/Maktaba/pull/372
[#373]: https://github.com/Hamza-Labs-Core/Maktaba/pull/373
[#374]: https://github.com/Hamza-Labs-Core/Maktaba/pull/374
[#375]: https://github.com/Hamza-Labs-Core/Maktaba/pull/375
[#377]: https://github.com/Hamza-Labs-Core/Maktaba/pull/377
[#378]: https://github.com/Hamza-Labs-Core/Maktaba/pull/378
[#387]: https://github.com/Hamza-Labs-Core/Maktaba/pull/387
[#388]: https://github.com/Hamza-Labs-Core/Maktaba/pull/388
[#389]: https://github.com/Hamza-Labs-Core/Maktaba/pull/389
[#390]: https://github.com/Hamza-Labs-Core/Maktaba/pull/390
[#395]: https://github.com/Hamza-Labs-Core/Maktaba/pull/395
[#397]: https://github.com/Hamza-Labs-Core/Maktaba/pull/397
[#398]: https://github.com/Hamza-Labs-Core/Maktaba/pull/398
[#399]: https://github.com/Hamza-Labs-Core/Maktaba/pull/399
[#404]: https://github.com/Hamza-Labs-Core/Maktaba/pull/404
[#405]: https://github.com/Hamza-Labs-Core/Maktaba/pull/405
[#411]: https://github.com/Hamza-Labs-Core/Maktaba/pull/411
[#412]: https://github.com/Hamza-Labs-Core/Maktaba/pull/412
[#413]: https://github.com/Hamza-Labs-Core/Maktaba/pull/413
[#425]: https://github.com/Hamza-Labs-Core/Maktaba/pull/425
[#428]: https://github.com/Hamza-Labs-Core/Maktaba/pull/428
[#429]: https://github.com/Hamza-Labs-Core/Maktaba/pull/429
[#431]: https://github.com/Hamza-Labs-Core/Maktaba/pull/431
[#433]: https://github.com/Hamza-Labs-Core/Maktaba/pull/433
[#434]: https://github.com/Hamza-Labs-Core/Maktaba/pull/434
[#435]: https://github.com/Hamza-Labs-Core/Maktaba/pull/435
[#438]: https://github.com/Hamza-Labs-Core/Maktaba/pull/438
[#439]: https://github.com/Hamza-Labs-Core/Maktaba/pull/439
[#440]: https://github.com/Hamza-Labs-Core/Maktaba/pull/440
[#441]: https://github.com/Hamza-Labs-Core/Maktaba/pull/441
[#442]: https://github.com/Hamza-Labs-Core/Maktaba/pull/442
[#443]: https://github.com/Hamza-Labs-Core/Maktaba/pull/443
[#444]: https://github.com/Hamza-Labs-Core/Maktaba/pull/444
[#445]: https://github.com/Hamza-Labs-Core/Maktaba/pull/445
[#446]: https://github.com/Hamza-Labs-Core/Maktaba/pull/446
[#447]: https://github.com/Hamza-Labs-Core/Maktaba/pull/447
[#448]: https://github.com/Hamza-Labs-Core/Maktaba/pull/448
[#451]: https://github.com/Hamza-Labs-Core/Maktaba/pull/451
[#452]: https://github.com/Hamza-Labs-Core/Maktaba/pull/452
[#453]: https://github.com/Hamza-Labs-Core/Maktaba/pull/453
[#454]: https://github.com/Hamza-Labs-Core/Maktaba/pull/454
[#460]: https://github.com/Hamza-Labs-Core/Maktaba/pull/460
[#458]: https://github.com/Hamza-Labs-Core/Maktaba/pull/458
[#464]: https://github.com/Hamza-Labs-Core/Maktaba/pull/464
[#462]: https://github.com/Hamza-Labs-Core/Maktaba/pull/462
[#467]: https://github.com/Hamza-Labs-Core/Maktaba/pull/467
[#470]: https://github.com/Hamza-Labs-Core/Maktaba/pull/470
[#468]: https://github.com/Hamza-Labs-Core/Maktaba/pull/468
[#463]: https://github.com/Hamza-Labs-Core/Maktaba/pull/463
[#465]: https://github.com/Hamza-Labs-Core/Maktaba/pull/465
[#471]: https://github.com/Hamza-Labs-Core/Maktaba/pull/471
[#472]: https://github.com/Hamza-Labs-Core/Maktaba/pull/472
[#473]: https://github.com/Hamza-Labs-Core/Maktaba/pull/473
[#474]: https://github.com/Hamza-Labs-Core/Maktaba/pull/474
[#475]: https://github.com/Hamza-Labs-Core/Maktaba/pull/475
[#476]: https://github.com/Hamza-Labs-Core/Maktaba/pull/476
[#477]: https://github.com/Hamza-Labs-Core/Maktaba/pull/477
[#479]: https://github.com/Hamza-Labs-Core/Maktaba/pull/479
[#480]: https://github.com/Hamza-Labs-Core/Maktaba/pull/480
[#512]: https://github.com/Hamza-Labs-Core/Maktaba/pull/512
[#517]: https://github.com/Hamza-Labs-Core/Maktaba/pull/517
[#469]: https://github.com/Hamza-Labs-Core/Maktaba/pull/469
[#510]: https://github.com/Hamza-Labs-Core/Maktaba/pull/510
[#516]: https://github.com/Hamza-Labs-Core/Maktaba/pull/516
[#528]: https://github.com/Hamza-Labs-Core/Maktaba/pull/528

[#523]: https://github.com/Hamza-Labs-Core/Maktaba/pull/523
[#524]: https://github.com/Hamza-Labs-Core/Maktaba/pull/524

## 2026-11-02 — LibrariesSectionTest: "library-l1 shows" missed once on CI

- One CI run (41376606909): aLibraryShowsItsHubRowsAndLeavesTheEmptyOnesOut waited 10 s for the libraries list; the other 11 tests took ~0.5 s. Six local runs of the class all passed; the report held no app log (Robolectric drops android.util.Log), so the cause wasn't recorded.
- Changes: a missed wait now says which paths the app asked the home and what the screen showed. Each FakeHome gets its own OkHttp connection pool (it shared defaultHttp's across tests).
- Not root-caused yet: the next miss names it.
- Rule: tests always keep the app's log, so none is dropped. Both Android Gradle roots (apps/android, apps/tv/android) set `robolectric.logging=stdout` on every Test task; LogsKeptTest fails if it goes. Reports now carry the app's log.
- Root cause found (with the new diagnostics): the miss is always the first wait, and the report showed "asked [/api/libraries/summary]; showing [libraries-list, tabs]": the answer came, no error was logged, the screen stayed on its spinner. Under Robolectric a bare `compose.waitUntil` only polls; an answer landing after the last idle isn't applied until the main looper is idled. Reproduced with a 500 ms late answer: poll-only took 4.6 s of a 5 s budget, poll-with-idle 0.6 s.
- Fix: harness/Settle.kt `settledUntil` (waitForIdle in every poll) and `settled` (same, plus the asked/shown message); all three Libraries test classes use them. ~80 other bare waitUntil calls remain in the Player tests; not converted blindly (watch tests wait on things that hide by themselves, and settling advances the clock).
- Converted: every Player-test wait on a fake-home answer or what it causes (tags/text, request counts, HomeCache files, session phase, fake engine calls) is now `settledUntil`, in the 16 Home/Video/Profile/Search/Shell/Start/Connect/Components/Live test files; HomeScreenTest's gated "not answered yet" wait dropped its inner waitForIdle (the gate is the test's own latch, so settling can't let the answer in early); ConnectProgress's AwaitingApproval wait converted too (its countdown is on a fixed clock).
- Left bare, each with a one-line comment: ChannelScreenTest's `controls()` (they hide after 4 s), WatchScreenTest's controls/seek-flash/next waits and the `until` helpers in WatchScreenTest, WatchScreenshotTest and PipBackgroundCastTest (they step the players' clock themselves; settling would also advance Compose's).
## 2026-11-02 — v0.3.51 had no Play bundles: the TV wrapper jar wasn't Gradle's

- play-bundles.yml on v0.3.51 stopped at "Gradle wrappers are Gradle's": apps/tv/android named Gradle 9.8.2 but its jar wasn't 9.8.2's (sha cb0da675…). The phone apps' jar matched.
- Fix: TV takes the phone apps' verified 9.8.2 wrapper (jar, gradlew, gradlew.bat). _android-tv.yml now runs the same check on every PR, so a wrong jar fails the PR, not the release tag.
- v0.3.51 stays without .aab files; the next tag carries them.
- 2026-11-02 — later: settling alone didn't end it. #508's run on 8c6d44b1 missed again in facetsAndSortChangeTheRequestsQuery: answer in, no error logged (the kept log proved it), spinner for 10 s. Not reproducible locally even under full CPU load. The v1 compose rule (deprecated; the build warned) runs effects on an UnconfinedTestDispatcher, so the answer resumes the screen on OkHttp's thread and writes Compose state from there. All 23 Player JVM test classes now use the v2 rules (`junit4.v2.createAndroidComposeRule` / `createComposeRule`), which queue that work for the main thread. Three full suite runs green, screenshots unchanged (verifyRoborazziDebug), tests ~3-5x faster. androidTest (on-device) files still on v1.

## 2026-11-02 — Maktaba renamed the Mac on the network (mDNS name conflict)

- macOS renamed "Mahmouds-Mac-Studio.local" to "…-2.local". Cause: maktaba-api and maktaba-server each ran a zeroconf responder (`lsof -iUDP:5353`) announcing via RegisterProxy with the Mac's own Bonjour name, which publishes A/AAAA for it with the cache-flush (owner) bit. mDNSResponder saw other answerers for its name and renamed the Mac; our servers then claim the new name on restart.
- Fix (fix/mdns-system-responder): register() returns an `announcement` (SetText, Shutdown). On macOS it runs `dns-sd -R` under /bin/sh that ends it when our stdin pipe closes (Shutdown, or our process dying any way), restarted if it ends by itself. Elsewhere zeroconf as before. Same file in api/internal/discovery and cmd/maktaba-server/internal/mdns.
- Gotcha: a POSIX shell gives a background job /dev/null as stdin, so the watcher must read a copy (`exec 3<&0 … read _ <&3`), or it kills dns-sd at once.
- Not done: Linux hosts with avahi-daemon and Windows (own mDNS) have the same conflict risk; still zeroconf there.
- 2026-11-02: removed 140 merged, clean, unused worktrees (82 GB). Kept 20: in use, open PRs, or unmerged work.

## 2026-11-02 — TestLibraryTabsSQLite past the unit test budget: fsync during setup

- CI run 41449508335 (PR #510) failed test-budget: browse TestLibraryTabsSQLite took 1.42 s, and 1.32 s when re-run alone. The 3x400 ms cap is 1.2 s.
- Cause: the shared `testDB` setup (~45 autocommits, plus ~14 from `groupsEnv`). On a file DB in rollback-journal mode, each commit syncs the journal, the db file and the directory. That costs almost nothing on macOS and a lot on the Linux runner. Simulating it locally with `PRAGMA fullfsync`: setup 965 ms out of a 990 ms test. The five handler requests take ~25 ms, so the handler wasn't the problem.
- Fix: `testDB` opens with `_pragma=synchronous(OFF)&_pragma=journal_mode(MEMORY)` in the DSN, so every pooled connection gets them. Same idea as dd1fb356 (tvchangetest). Simulated fsync: 0.99 s -> 0.08 s. Every browse test that uses `newEnv` gets faster.
- The same PR (#521) also fixes a search test that failed integration/compat-prev: `TestEmbedCacheConcurrentSameKeyNoCorruption` compared response bodies byte for byte, including the wall-clock `took_ms` ("semantic":0 vs 1). Both subtests (concurrent, and sequential miss->hit->hit) now decode the bodies, drop `took_ms` and compare the rest (`withoutTiming`). Passed `-race -count=20`.

## 2026-11-02 — thumbnails in a channel's `.thumbs/` were never found

- On the NAS, 69 channel folders keep yt-dlp thumbnails in `<channel>/.thumbs/<stem>.jpg` (e.g. "Abdul Basit- Mojawwad/.thumbs/Abasa： 80.jpg"). `thumb_hash` and `thumb_small` only looked next to the video, so these videos fell back to the poster, and the poster's `thumbnail` stage is the one that has been stuck.
- One rule now: `discovery/ytdlp_sidecars.find_sidecar_thumbnail` checks `<dir>/<stem><ext>`, then `<dir>/.thumbs/<stem><ext>`, for each ext in THUMB_EXTS. Stats only, no listings. Both stages use it through `thumb_hash.find_thumbnail`. The Go API never looks for a sidecar: `videos/poster.go` serves `poster_path` and the cache files. No other reader found.
- How videos that were already processed get fixed, without a version bump. A bump re-queues every subject, and `run.py` only skips a job when the stage version matches, so ~230k thumb_hash jobs would re-read their images. Instead:
  1. Both fingerprints add `"thumbs": <name>`, but only when the image is found in `.thumbs/`. Every other video keeps exactly the fingerprint it had.
  2. A reconciler step, `_thumbs_dir_sweep`, queues `thumb_hash` once for each video whose latest real run used the poster or found nothing (source null or `poster.*`). It skips summaries that already have `thumbs_dir` (written by runs of this release) and imported rows. The reason "thumbnail may be in .thumbs/" marks a video so it is never queued again, so the sweep stops on its own. The affected videos run again and their dhash changes. The rest are skipped after a few stats.
  3. The dhash change triggers `thumb_small`. That stage used to keep any copy already on disk. Now, if its last summary says the copy was made from the poster and the video's own thumbnail is found, it makes a new copy from the sidecar. Copies it keeps also record their `source` in the summary.
- Scan: hidden folders like `.thumbs` were already pruned. `@eaDir` (Synology's index) joins `DEFAULT_IGNORE_DIRNAMES` (walker, watcher, youtube_sync). youtube_sync's os.walk and the watcher now skip hidden folders too.
- Tests: `tests/stages/test_thumbs_subfolder.py` (names with Arabic and fullwidth colons; sqlite and a scratch PG on :55471 started from the app's bundled binaries, data in the scratchpad) and a walker test.
## 2026-11-02 — Android Player: a collapsible rail, a draggable list-detail divider, half-fold layouts ([#523])

- Report: on a foldable open flat (sideways, 841 dp) and a big tablet, Libraries showed a permanent 360 dp drawer, the list (380 dp) and the videos squeezed to ~100 dp. Cause: `navigationFor` gave expanded windows `NavigationDrawer`, and the list had a fixed 380 dp.
- Decided: expanded windows and a book-held foldable get a collapsible rail (`shell/CollapsibleRail.kt`, material3 1.4.0's `WideNavigationRail`, no upgrade needed). It is 96 dp of icons and expands over the content (modal, with a scrim: chosen over push so the content never reflows). The menu button or a mouse hover opens it; choosing, a tap outside, Back or the mouse leaving close it. Compact and tabletop keep the bar, medium keeps the rail.
- Decided: `core/layout/ListDetailSplit` is a draggable divider. List min 240, page min 320, default list 380 but at most 40 %. It has a 48 dp handle, TalkBack widen/narrow actions and arrow keys, and is mirrored in RTL. Kept in saved state and in SharedPreferences `maktaba.splits`. Used by Libraries and the Remote.
- Decided (follow-up: half-fold postures had no navigation changes): in book posture the rail and the list sit on the start page and the list ends at the hinge. The split is the hinge (no drag handle: any other split puts a pane under it), and the expanded rail is capped to the start page. In tabletop, the page is on top and the list and bar on the bottom; non-splitting screens keep to the top half (`OffTheHinge` in PlayerShell). The shell passes its adaptive info down (`LocalAdaptiveInfo`), so tests fake hinges through `WindowAdaptiveInfo` (`harness.folded`).
- Failed first: (1) the rail's Back didn't win over a section's Back. A BackHandler that is always composed keeps its old registration order; it is now composed only while open. (2) Swapping between NavigationSuiteScaffold and the rail frame recomposed the screens, and a live channel going full screen restarted. Both now share one tree. (3) The rail's IconButton was 40 dp, and HomeScreenTest's accessibility audit caught it.
- Gotcha: `recordRoborazziDebug` rewrites every PNG, even unchanged ones (byte noise). Re-record only the tests that `verifyRoborazziDebug` fails. The before shots for the PR body live on the branch `screenshots/android-rail-before`.
- Not verified on a real foldable or tablet, and no emulator run. Open: in book posture, Home/Search (screens that don't split) still span both pages.
- CI failed the first push (run 41472993431): four LayoutScreenshotTest pictures had the library page scrolled down a row. That was unsettled content, not fonts or a layout bug. The hub rows can land after the first videos, and the grid then keeps the first video in view (scroll anchoring), so the picture depended on the order the answers arrived in. The test now waits for idle and scrolls the page to its top before each capture; the goldens are re-recorded and the tolerance is unchanged. The jump itself is still in `LibraryPage` (the rows can end up above the view on a slow home): a follow-up for that screen.
## 2026-11-02 — CI runs what a change touches (ci/run-what-changed)

- Requirement: CI runs only what a change touches. An Android-only PR ran pipeline integration, Go replica, e2e and all nine Player UI shards; a Remote rename ran the Player UI tests.
- Now: `.github/ci-paths.yml` holds one path filter per job or part, derived from what each workflow builds and tests (Go modules + what their tests read, pipeline + the docs/help its tests read, web + the two api files help.coverage reads, tokens, each app). ci.yml's `changes` job evaluates it on a pull request only; on a push to main/release/* every output is true, so main and the tags get the full run (mac/win/linux app builds now run on every push too). The update rehearsal's own triggers are unchanged.
- Parts: `_lint.yml` (repo checks always; go/pipeline/web), `_unit.yml` (go/pipeline/web/tokens/desktop_shell), `_integration.yml` (integration/compat_prev), `_node-packaging.yml` (packaging/release_venv), `_apple-apps.yml` (build/player_ui) take boolean inputs. Makefile: `lockfile-check` split into -py/-web/-go.
- Apple: the Player UI shards run for Player/, Shared/, MaktabaKit sources, UITests/, PlayerMacTests/, the Player schemes. The shared project.pbxproj counts only when `tools/ci/apple_player_scope.py` finds a Player object among its changes (parses the file at HEAD^1 and HEAD; the Remote rename caaf91a0 and Remote icon 3912dca8 come out false, UI-test changes true).
- e2e and perf-ci need integration but now say `!cancelled()` and run when integration was skipped (else a web-only change would skip them silently).
- ci-success loops over every need: success/skipped pass, failure/cancelled fail, `changes` must succeed.
- Guard: `tools/ci/ci_paths.py guard` in the lint job's always-run `repo` part. It simulates ci.yml's real `if:`/`with:` expressions: every job is gated by filters (and runs when any one alone is true) or marked `# ci-paths: always-run|push-only|pr-only|not-a-gate`; every filter is used; every gate is in ci-success's needs; a push runs everything; every tracked file is matched by a filter or listed in NOT_IN_CI. `ci_paths.py which <files>` shows what a change runs.
- Tests: tools/ci/tests (Android-only, Remote-only, Player, tvOS, pipeline-only, API-only, docs-only, workflow change, ci.yml, shared migrations, push, fork, guard negatives) and the pbxproj scope tests on the real project file. pipeline's test_docs_links now reads the `unit_pipeline` filter instead of the old `meta` one.

## 2026-11-02 — extract paused by nobody, thumbnail starved: stage pauses need an owner, pools share fairly

- Read-only look at the live home: extract (`nas_read`) had `paused = true`, `paused_at`/`paused_by` NULL since 2026-10-22 22:31. Thumbnail (70k pending at 900) had not run because thumb_small and chapter_thumbs (~42k at 100) always filled `nas_read`'s 2 slots under `ORDER BY priority, id`.
- Writer: no code. Every pause writer in git history sets both columns. It was a Claude session (`30850266`) on 2026-10-23 02:37 UTC, after a request to pause it: a raw `psql` `update job_stages set paused = true where stage = 'extract'` on the live Docker Postgres. Later that night the same session did the same for `index` and `chapter_infer` on the bundled Postgres. Lesson: never write operator columns by hand. Pause through `POST /api/queues/{stage}/pause` (the DB now refuses anything else).
- Fix ([#524], slot 0172):
  - orphaned pauses are resumed, with `audit_log` `admin` / `queue.stage_orphan_pause_resumed`
  - a trigger (not a CHECK: `ADD CONSTRAINT` would make the release unsafe to roll back on its schema) refuses a pause without `paused_at` and `paused_by`
  - the Queues API adds `paused_by_name`, and the web card and queue page say who paused a stage and when (English and Arabic)
- Fairness: stride scheduling per pool (`claim_shares`, `engine/claim.py`) between priority classes, not stages. Each stage's head is due at `GREATEST(class pass + priority, pool clock)`, and the claim takes the head due first. Equal priorities stay in id order across stages. A first cut kept a share per stage, which round-robined equal stages, split fp_gate's batch and ran detect 4 times (`test_fingerprint_flow` seed 2, CI integration). Priority ≤ 50 (Process now) still goes first, strictly. 900 vs 100 gets 1 claim in 9. The new partial index `processing_jobs_claimable_idx` brought a claim with 203k claimable rows from 64 ms to 0.3–0.8 ms. Legacy `claim_one` (non-pool mode) is unchanged: it has a semaphore per stage, so no cross-stage starvation.
- Live state: unchanged by this session. Extract resumes when a release with slot 0172 is installed (the owner tags and moves it live). Check `index` and `chapter_infer` then too: if they are still hand-paused, 0172 resumes them as well.

## 2026-11-02 — Library page opened scrolled on a slow home (Android Player)

- PR #513's CI (run 41481083121) failed LibrariesScreenshotTest.libraryEnglish: `libraries_library_phone_dark_en` scrolled ~27dp, Continue Watching cut at the top. A real user bug, not a flake.
- Cause: a lazy grid keeps its first visible item's key in place. When the videos answer before the hub, the first layout is [tabs, filters, videos] with `tabs` first; the hub rows then come in above it and the grid scrolls to keep `tabs` at the top, as far as its end allows, so the page opens part way down (the offset is the content's overflow at that moment: 27dp on CI, 185dp in the new test). Same pattern in the Libraries grid: unpinning the first pinned library moves it to a later line and the grid followed it.
- Fix: `StaysAtTopWhen(key)` (libraries/GridTop.kt): when what leads the grid changes and the grid is at its top (index 0, offset 0), `requestScrollToItem(0)` before the next measure. A scrolled grid keeps its key as before. Used by LibraryPage (key: hub rows) and the Libraries grid (key: library order). Series grid only appends: not affected.
- Tests: LibrariesSectionTest `aSlowHubLeavesThePageAtItsTop` (hub held on a latch until v4 shows; failed before the fix: row at -121dp, grid at 64dp) and `unpinningTheFirstLibraryLeavesTheGridAtItsTop` (14 libraries; failed before the fix). Screenshots unchanged (verify).
- Merged main (#523): LayoutScreenshotTest's workaround (`performScrollToIndex(0)` before each picture) is gone. Its wait for `video-v1` also leaned on the bug: on the tabletop's half page the videos are only composed when the grid jumps down, so it now waits for the page's top (`row-continue`), the idling resource holding it until every answer is in. References unchanged; 3/3 runs green.

## 2026-11-02 — extract writes no WAV on a streaming backend

- Live: 1,323 extract jobs waiting (847 pending, 476 paused), stage paused by hand; each would write `~/.maktaba/cache/audio/<hash>.wav` (~115 MB/h) that nothing reads: the engine's transcribe decodes off the share, and the configured backend (`whisper-mlx`) streams.
- Decided: code only, no migration. `extract_handler` asks `stt.transcribe.configured_backend_requires_file()` (backend name resolved as `default_select_backend` does; true only for a class with `requires_file = True`, today `openai-api`). Streaming: `commit_extract_skipped` (no ffmpeg, no file, no `audio_cache` row, no `last_extracted_at`), FSM `PROBED -> AUDIO_EXTRACTED` and the TRANSCRIBE enqueue as after a real extract, except the enqueue is left out when transcription is already handled (active transcript or live transcribe job, the #267 rule), logs `extract_skipped_streaming_backend`. So 0172 resuming the stage is harmless: the backlog finishes in ms.
- Follow-up PR (not done): drop the probe→extract edge and probe's eager enqueue, transcribe requests extract via `may_request` when a backend needs a file, delete orphaned WAVs. Note for it: `--no-engine` mode chains probe→extract→legacy transcribe_handler, which reads `audio_cache`; on a streaming backend that handler now finds no artifact.

## 2026-11-02 — test_a_corrupt_input_fails_at_once got 'done': random bytes aren't always corrupt

- CI failed `test_convert.py::test_a_corrupt_input_fails_at_once` on unrelated PRs: on [postgres] in run 41476408173 (PR #523), and on [sqlite] on PR #525. `assert job["state"] == "failed"` got 'done'.
- Cause: the test's fixture, not the stage. The "corrupt" input was `os.urandom(200_000)`. ffprobe 8.1 accepted 3 of 300 such files: 2 as a raw H.263 video stream, 1 as an LRC subtitle. When probing works, `plan_for` skips the file (`needs_reencode` with re-encoding off, `no_video` for LRC), and the job ends `done`/unchanged with the file untouched. Captured one such file and reproduced 'done' on both dialects.
- Fix: the fixture is now a real Matroska EBML header (DocType "matroska") plus a 200 kB body from a seeded RNG. The matroska demuxer claims the file and always rejects it ("End of file"). The test also checks the error says "the original cannot be read", meaning ffprobe refused the file. Passed 20/20 runs on both dialects.
- Not fixed, noted: with `allow_reencode` on, junk that ffprobe reads as raw H.263 would re-encode into a 1-frame MP4 whose frame count matches. The original goes to the trash, so nothing is lost.
- Same PR: `test_cancel_kills_ffmpeg_and_leaves_nothing` was over its 5 s soft cap on CI (6.9 s). Different cause: about 2.8 s of the 3.95 s local run was making the 60 s 720p VP9 source (8.9 s of CPU). The source is now MPEG-4 Part 2 (0.6 s, 4.5 s of CPU). It still plans a re-encode, and the x264 encode being cancelled is the same. Now 1.8 s locally.

## 2026-11-02 — CI runs only what a change touches ([#528]); trail-only PRs run nothing ([#532])

- Requirement (asked again): jobs run only when their related code changes. ci.yml's `code: '**'` filter ran every job on every PR (Apple UI shards 27–53 min on a Remote rename). [#528] (merge fad707d8): `.github/ci-paths.yml` holds one filter per job/part, applied on PRs only (a push runs everything); `tools/ci/ci_paths.py guard` (in lint) fails on an unfiltered/unmarked job, a job missing from `ci-success`, an unused filter, or a tracked file no filter covers. Until a filter exists, merge on the related checks.
- Rule: trail updates go in their own PRs, with no CI and no tests. Why: every code PR appending here made every open PR conflict on each merge (a main merge + CI restart each). [#532] (merge 091ebe1e): `changes` has a `repo` output, false when only this file changed; lint, pr-body-check and affected-tests are gated by it, so a trail-only PR runs only `changes` + `ci-success`. Still runs: CodeQL default setup (can't be filtered, not required) and the labeler. A push to main still runs full CI (the tag guard needs it). Dropped: one file per change under docs/trail/.
- Rule: follow-ups start at once (parallel worktree agents); never ask "now or later?" or which of two reasonable designs.

## 2026-11-02 — Merges and fixes (coordinator `b743b7d4`)

- 2026-11-02 · affected-tests agent of `b743b7d4`: [#516] merged at 65d09858 (merge commit) once lint, affected-tests, CodeQL, unit, integration, android-apps and apple-apps (Apple apps, Mac UI) passed on 7b93f4d0. The first coverage-map run on main is not checked yet.
- 2026-11-02 · coordinator `b743b7d4`: [#529] merged at 5c8dda56: LibraryPage/Libraries grid stay at top when the hub answers late (lazy grid key anchoring; `StaysAtTopWhen` + `requestScrollToItem(0)`); #523's scroll-to-top test workaround removed. [#525] merged at 2ea78099 (.thumbs/ sidecars, one-time sweep, @eaDir ignored) — its agent reran the ubuntu installer job (rule broken), root-fix launched. [#527] merged at bb4a742b (extract no-op on streaming backends); tag hold lifted. [#526] merged at b9e2efcc (convert corrupt fixture). [#523] merged at 245915cc (Android rail + resizable panes + postures). Rules: follow-ups start at once, never "now or later?"; trail only in trail-only PRs with no CI/tests.

## 2026-11-02 — Sign-in forms: Return goes to the next field, the field stays above the keyboard
- Requirement: on iOS and Android, sign-in fields get a Next key, and the keyboard never covers the field.
- Apple (MaktabaKit `StartViews.swift`, `CloudViews.swift`): `@FocusState` field chain. Username Next -> password Go; cloud email Next -> password (Next -> address when Advanced shows it, else Go) -> address Go; picker address Go. `keepsFocusInView` (`KeyboardFocus.swift`) scrolls the focused row to the middle on each focus change and again 350 ms later, once the keyboard is up. No new visible text, so no new strings.
- Android (core `StartScreens.kt`, `CloudScreens.kt`): FocusRequester chain, same order. The last field takes Go. `rememberKeptAboveKeyboard()` (`KeyboardFields.kt`) brings the focused field into view on focus and on every IME height change. The activities were already edge-to-edge + adjustResize, and `StartFrame` already pads for the IME.
- Tests: `SignInKeyboardTest` (player, Robolectric, v2 compose rule) for home, cloud and cloud+Advanced. `PlayerSignInKeyboardUITests` (iPhone/iPad CI): Return moves keyboard focus to the password, the password is above the keyboard, and Return sends the login. Text goes in through `typeExactly` (#510); only Return uses `typeText("\n")`.
- The PR's first CI run (before main was merged in) failed for reasons outside this change. Mac PlayerMacMenuUITests:155 and Android LibrariesSectionTest were fixed by #510 and #508. The iPhone/iPad accessibility audits also failed (Libraries, Profile, Search). On the iPad run, `PlayerSignInKeyboardUITests` passed.
- A local run of `LibrariesScreenshotTest.tabletEnglish` timed out once, under load from a parallel Xcode build. It passed on the re-run. Unrelated (FakeHome settling, #510).

## 2026-11-02 — Merges and fixes, batch 2 (coordinator b743b7d4)

## 2026-11-02 — worker agent undrain timeout on Windows: SQLite durability cost in test DBs ([#540])
- Failure: `unit / pipeline-cluster-windows` on #526, `test_a_home_worker_runs_its_agent[sqlite]` timed out (3 s) waiting for the `undrain` `cmd.result`; the handler was still running, not losing the reply.
- Cause: on windows-latest each SQLite write cost 0.3–1.8 s (a sync per commit plus a journal file per write); the engine's background loops share one connection behind an asyncio lock, so undrain's `UPDATE workers` waited behind them. Product checked: `_command` always replies.
- Fix (#540, merge 90faa54d): `tests/engine/fixtures_db.py` sets `synchronous=OFF` and `journal_mode=MEMORY` for test DBs, guarded by `tests/engine/test_fixtures_db.py`. Cluster suite 3–5 min → 44 s; the test 10 s → 0.67 s.
- Follow-up launched: the reconciler holds the shared SQLite connection for its whole pass in one transaction (~10 s with 100 ms per statement), enough to time out the 8 s heartbeat.
## 2026-11-02 — The tag guard downloaded Sigstore to compare version strings ([#533])
- Run 41476114340 attempt 1 (PR #525) failed `node-packaging / installer tests (ubuntu-22.04)`: `go run ./cmd/release-order` couldn't fetch `google.golang.org/protobuf@v1.36.11` (proxy INTERNAL_ERROR, no cache). #525's agent reran it to green (against the rules).
- Cause: `releaseorder` imported `shared/go/releaseverify` (Sigstore, gRPC, protobuf: ~40 modules) only for version compares — in this test and every release's tag guard.
- Fix (#533, merge 43dff42d): stdlib-only `shared/go/releaseverify/version` (Compare, IsPrerelease, SameLine, ValidTag moved unchanged; old names kept as wrappers); releaseorder imports only it; tag-guard-test.sh runs `GOPROXY=off -mod=readonly` with an empty GOMODCACHE (old import fails 11/11); the release guard step runs `GOPROXY=off`. First push was blocked by the new affected-tests gate (#516) until tests were added.
- Follow-ups launched: `wheel + archive`'s `gen-release-json` still fetches the full module set each run; `TestRootStoreRefreshKeepsLastGood` fails on the owner's Mac on unchanged main (cached root FetchedAt = embedded 2026-10-20).
## 2026-11-02 — Dependabot sweep (24 alerts → 0 open)
- Go [#530] (326d7d33: chi 5.4.0 in api/cloud/streaming, klauspost/compress 1.18.7 in shared/metrics/go, which now says go 1.24), Rust [#531] (7f3ec369: zip 9.2.2 via tauri-plugin-updater 2.14.1, tokio 1.54.1), Python [#534] (d510180e: certifi, filelock 4.2.7, setuptools; uv.lock relocked with CI's uv 0.13.4), npm [#535] (3cf171f7: vite 6.4.5 in web and wiki-app; apps/mobile override glob@9 → ^14.0.3). apps/mobile installs with pnpm 9.15.4 (pnpm 10 refuses its lock).
- Dismissed #160 glib (`not_used`: tauri 2's GTK3 stack holds glib 0.18; nothing calls VariantStrIter) and #184 web minimatch (`tolerable_risk`: lint-only, eslint has no minimatch-11 release). Revisit when tauri moves to GTK4 / eslint adopts minimatch 11.
## 2026-11-02 — Android book posture: Home and Search split at the hinge ([#536])
- Merge d907aba7. Home's rows go to two pages (`bookPages` in HomeScreen.kt; with a hero the start page takes the smaller half); Search shows results on the start page and `VideoScreen` for `SearchModel.chosen` (else the first result) on the other; new string `search_preview_hint` (en+ar). RTL: start page is the right one.
- androidTest in core and the TV app moved to `junit4.v2.createComposeRule`.
- First push blocked by the affected-tests gate (SearchModel.kt without SearchModelTest.kt); fixed with real tests, no `No-Test-Change` trailer.
## 2026-11-02 — Convert rejects inputs ffprobe only guesses at ([#537])
- Merge 387fc9fd. Bug: seeded junk read by ffprobe 8.1 as raw h263 (no duration, 1 frame) re-encoded to a 1-frame MP4 that passed verify (duration check skipped when the original has none; frame counts matched); the original went to trash.
- `unreadable()` (library_mgmt/convert.py) rejects no-container demuxers (`RAW_GUESSES`: raw video, text subs like lrc, text-art), a video stream with no positive duration, or 0 frames; the job fails `ffmpeg_decode` "the original cannot be read" even with re-encoding off; the library plan skips them as `unreadable`. `verify_media` fails when the original has video but no duration.
- Not gated on ffprobe's probe score (MPEG-PS scores 26, MPEG-TS 50). Fixtures are built (a real H.263 frame + seeded junk), so they don't depend on the ffmpeg version.
- Gotchas: a `.bin` extension makes ffprobe read anything as `bintext`; the scratchpad path is too long for a Postgres Unix socket (`-k ''`, TCP); the bundled Postgres has no `createdb`.
## 2026-11-02 — mDNS through the system responder on Linux/Windows ([#538])
- Merge c23d9835. Following #520 (macOS dns-sd), `register` picks a responder: darwin → dns-sd; any Unix with the avahi-daemon socket → `avahi-publish-service` (same stdin-lifetime script, restart, TXT re-announce); Avahi without avahi-utils → zeroconf under `<host>-maktaba` (`LocalHostName` follows); no Avahi → zeroconf. Windows → DnsServiceRegister via x/sys/windows (request pinned with runtime.Pinner until the callback; one NewCallback per process; refused registrations retried; dereg waits up to 2 s); no API → zeroconf.
- Subprocess, not D-Bus (godbus is only in go.sum). New CI job `unit / mdns-windows` (filter `mdns_windows`) proved real register/withdraw and multicast resolution on windows-latest.
- Follow-up launched: systemd-resolved with `MulticastDNS=yes` still gets zeroconf beside it; a home name with "." isn't escaped in the Windows instance name.
## 2026-11-02 — Step-code scan fast ([#544])
- Merge a24c1c8f. `TestEveryStepErrorHasItsWords` (cmd/maktaba-server) broke test-budget on #532 (1.28 s; cap 1.2 s). Cause: one regexp, a four-way alternation with no literal prefix, over ~1.4 MB of source — RE2 ran every byte through the NFA (1.27 s under -race). Fix: one literal-prefixed pattern per form (`\b` before `coded(` checked by hand); same 61 codes, test 0.08 s; `TestStepCodesInFindsEveryForm` pins the finder.
- Lesson: regex-scanning big sources in a test, give each pattern a literal prefix; don't combine them in one alternation.
- Follow-up launched: `api/internal/updates` over the 30 s package budget (31.2 s) in the same run.
## 2026-11-02 — Release tags upload to Play internal ([#545])
- Merge 8191f3e4. Every `v*` tag uploads each signed AAB (Player, Remote, TV) to its Play internal track (status completed, mapping.txt, whatsnew en-US + ar from the annotated tag message; a `--- ar ---` line starts the Arabic) when the secret `PLAY_SERVICE_ACCOUNT_JSON` is set; else one notice and success. `PLAY_UPLOAD`/`PLAY_RELEASE_STATUS` vars removed. Packages read from the Gradle applicationId (`tools/ci/play_release.py`). Action r0adkll/upload-google-play v1.1.5 @ e738b9dd.
- versionCode = (MAJOR*1000000+MINOR*1000+PATCH)*100+STEP (stable 99, alpha.N = N, beta.N = 32+N, rc.N = 64+N); a pre-release no longer shares its release's code; all above the old codes (v0.3.54 = 3054); MAJOR capped at 20.
- Waiting on the owner: Google Cloud service account, Play Console "Release to testing tracks" on the 3 apps, `gh secret set PLAY_SERVICE_ACCOUNT_JSON` (docs/google-play.md). Follow-up launched: the Capacitor `mobile-release.yml` versionCode still collides for pre-releases.
## 2026-11-02 — CI Go modules from a verified cache; root-store test on macOS ([#546])
- Merge b34dd6f2. node-packaging `wheel + archive` had setup-go `cache: false`, so `gen-release-json` fetched releaseverify's ~40 modules live each run. Now cached on `tools/release/go.sum` + `shared/go/releaseverify/go.sum` with `go mod download && go mod verify`. release.yml `sign-and-release` uses the same go-version-file, runner and key (a tag run restores main's cache), and verifies. nfpm is a cached static binary (key `nfpm-v2.46.3-static-<os>-<arch>`; bump the key in release.yml and _launcher.yml together). coverage-map keeps no cache (CodeQL #108).
- `TestRootStoreRefreshKeepsLastGood` failed only on macOS: `RootInfo.Source` resolves symlinks (`/private/var/...`) vs `t.TempDir()` (`/var/...`). Not a time bomb. The test compares the resolved path; `TestVerifyExpiredRoot` no longer uses the wall clock. Product logic unchanged.

- 2026-11-02 — PR #544 (merge a24c1c8f): `TestEveryStepErrorHasItsWords` (cmd/maktaba-server) broke test-budget on PR #532 (1.28s; cap 3x400ms). Cause: one regexp alternating four step-code forms with no literal prefix scanned ~1.4MB of source, so RE2 stepped every byte (1.27s under -race). Fix: one literal-prefixed pattern per form; same 61 codes, test 0.08s. New `TestStepCodesInFindsEveryForm` pins the finder. Lesson: in tests that regex-scan big sources, give each pattern a literal prefix and don't combine them in one alternation. The `api/internal/updates` 31.2s overrun from the same run is handled in #548.

- 2026-11-02 — CI concurrency ([#547], bdad891d4): main pushes ran a full CI + rehearsal per sha, never cancelled, so ~12 merges/hour queued 12 full runs and the newest (tagged) commit waited hours. Now `ci.yml` and `update-rehearsal.yml` put main pushes in one group (`ci-main` / `update-rehearsal-main`) with cancel-in-progress; PRs stay per-PR; `release/**`, re-runs (`run_attempt > 1`), nightly and manual rehearsals keep per-sha groups. `coverage-map.yml` skips a cancelled CI run in its own group. The tag guard checks only the tagged commit's own runs, so tag main's tip. Tests in `tools/ci/tests/test_ci_paths.py`; runbook in `deploy/packaging/README.md` "Release flow". Cancelled 18 stale main runs, kept 41500155980 on bdad891d4 (contains #512 AirPlay).

## 2026-11-02 — Merges and fixes, batch 3 (coordinator b743b7d4)

### 2026-11-02 — extract only on request; WAVs released and swept ([#543], b1ad4dbc)
- The probe→extract edge is removed. The engine probe (`chain_extract=False`) queues no extract. Transcribe (`may_request=("extract",)`) asks for it only when `configured_backend_requires_file()` is true (openai-api): it ends `waiting_inputs`, extract writes the WAV and re-queues transcribe, which decodes it, records extract's track and releases it. A row whose file is on another machine is decoded from the share, never re-requested (no loop). The live mlx-whisper backend never gets an extract job.
- Found: `db.jobs.enqueue`'s done-row skip treated a `waiting_inputs` run as done work, so extract's re-enqueue of transcribe was dropped as `skipped_done_unchanged`. That outcome and `awaiting_review` no longer count.
- WAV lifetime: `release_audio_cache` runs after the transcribe that read the WAV. `run_audio_cache_sweep` runs on every worker hourly: rows older than 6 h with no live extract/transcribe job, and files older than 6 h of this database's videos with no row. Files of hashes this database doesn't know are kept (branch stacks, tests).
- `--no-engine`: with no `audio_cache` row a streaming backend reads the source on extract's track; a job with no payload uses the video's hash and the selection policy.
- No migration: edges and `may_request` belong to the graph sync. The live extract backlog that 0172 releases drains as #527 no-ops.
- Harness: the fake DB implements `_enqueue_sqlite`; extract and transcribe handler tests run on both dialects.
- Known: the legacy probe and FSM advance SQL is Postgres-only (`now()`, `FOR UPDATE`).
- Failed first: the affected-tests gate; fixed with tests in the mapped files. Local gotchas: scratch PG needs `initdb -E UTF8`; use `uv sync --all-extras` (not `--extra stt`); scratchpad paths are too long for a PG unix socket, start it with `-k ''`.

- 2026-11-02 — PR #549 (merge 9602104a): the Capacitor app (mobile-release.yml, `com.hamzalabs.maktaba`) takes versionName/versionCode from `tools/release/play-version.sh`, the #545 rule; no formula of its own, and the version step runs before any build so a bad tag fails first. Before, a pre-release shared its release's code (v0.3.0-rc.7 and v0.3.0 both 3000). Highest old code 3055 (v0.3.55, run 41498853328); the new rule's first after it is 305601 (v0.3.56-alpha.1), so no offset. Tests: play-version-test.sh and test_play_release.py. The next tag is the first real run.

- 2026-11-02 — #522 (sign-in Next field, entry above in the #542 batch) merged as 0664eb2c. Not checked at merge: CI and local simulators use a hardware keyboard, so "field above the keyboard" ran with no on-screen keyboard; the Android scroll-into-view was checked only for focus and submit. Follow-up agent makes both tests real.

- 2026-11-02 — #553 (merge 01133fa1): a push to main or release/** that changes only docs/claude-trail.md starts no CI or Update rehearsal run (`paths-ignore` on both push triggers), so a trail merge no longer joins the `ci-main` / `update-rehearsal-main` group and cancels the previous code commit's runs, which the tag guard needs. Trail-only commits are never tagged; tag the newest code commit (Release flow, deploy/packaging/README.md). coverage-map.yml follows CI, so it skips them too. `ci_paths.py which --push` models it; test_ci_paths.py checks both triggers. Background: trail PR #552 (684a653b) cancelled bdad891d4's update rehearsal; the coordinator cancelled #552's runs and re-ran bdad891d4's rehearsal (a re-run gets its own group, #547). The coordinator's tag job now follows main (newest commit with both runs green and not cancelled) instead of one pinned sha.

- 2026-11-02 — PR #548 (merge c428badb0): `api/internal/updates` broke the 30 s unit budget (31.2 s, PR #532 run 41493179001). Cause: per-test SQLite setup, ~40 CREATE statements each (SQLite re-reads its schema after each) plus a file lock taken and released on every statement (61% of CPU). Fix (test-only, `db_test.go`): build the schema once per run and copy the file into each test's temp dir; hold the lock for the test's life (exclusive locking); synchronous=OFF and memory journal in the DSN so they survive a reopen. Locally 13.1 s → 5.0 s; CI 31.2 s → under 12.2 s; API unit total 3m55s → 2m53s.

- 2026-11-02 — PR #558 (merge d2e2d711): CloudTest.aHomeNotAnnouncedSaysWhyThenIsAskedWhenItIs failed on Windows desktop-player (job 125472803032, PR #513; real line CloudTest.kt:253). Root cause: a race in `AppSession`. A failed kept-sign-in check finishing on OkHttp's callback thread set `restoreProblem` before `progress = Failed` in two writes, so another thread could see the problem without the Failed state. Now `failWith` stores Failed first and the problem last, under a lock shared with `heard()`; the network look's start and end are under the same lock, so a home announcing itself in between is no longer missed. Test: a `SlowToFail` progress state forces the bad order every time (old code fails at the CI assertion). 163/163 client tests; 15 runs under CPU load clean.

- 2026-11-02 — PR #556 (merge 9fb8b4290), gap left by #543: with `--no-engine` and no `audio_cache` row, whisper-mlx decoded the source's default audio stream while the transcript was labelled with extract's chosen track (wrong language on Arabic + English files). `AudioArtifact.stream_index` / `TranscriptionHints.audio_stream` carry the chosen `-map 0:a:N` when a file has several streams; whisper-mlx and faster-whisper decode just that stream via ffmpeg into memory (no WAV). The engine transcribe stage had the same gap (always `0:a:0` as track 0): it now picks among the probed streams (`media_info.raw_ffprobe`, not `audio_tracks` rows, which can outlive a replaced file). Tests: an ffmpeg-made two-tone two-track file (440 Hz en default, 1000 Hz ar) checks the decoded frequency on the legacy handler, both backends and the engine. Open: pytest stops at ~82% with no summary when `tests/cluster` runs with the other folders in one call (follow-up agent).

- 2026-11-02 — PR #551 (merge 7221566d), follow-ups to #538, both copies (`api/internal/discovery` + `cmd/maktaba-server/internal/mdns`). Linux: systemd-resolved is detected as the responder (resolve1 running, MulticastDNS yes globally and on a link). With RegisterService on its D-Bus interface and polkit `org.freedesktop.resolve1.register-service` allowing it (or root), the announcement goes through D-Bus `RegisterService` (sssqqqaa{say}) on a held-open connection (resolved drops it on close; re-registered if resolved restarts), target = resolved's LLMNRHostname. Otherwise zeroconf announces under `<host>-maktaba` and LocalHostName follows. `.dnssd` files rejected (root-only, read on start/reload). Avahi still wins when both run. Windows: a pktmon capture showed DnsServiceConstructInstance splits the name at every ".", escaped or not, and keeps "\" as a byte, so RFC 6763 escaping doesn't work; a "." in the home name becomes U+2024 on Windows. dns-sd -R, avahi-publish-service and RegisterService take the raw name. zeroconf also split dotted names; its instance is now written as miekg/dns writes a label. Gotchas: the Windows responder answers none of the machine's own queries, so the Windows check reads the adapter with pktmon. Open: the resolved path hasn't run where resolved answers mDNS (follow-up agent turns it on in CI).

- 2026-11-02 — PR #557 (merge b2125c01): play-bundles.yml's Google Play upload is keyless. Workload Identity Federation (GitHub OIDC) via google-github-actions/auth v2.1.13 @ c200f369, `id-token: write` on the play job only; r0adkll/upload-google-play v1.1.5 @ e738b9dd reads the external_account credentials file through `serviceAccountJson` (its google-auth-library 9.15.1 supports it, checked at the sha). Gate: repo variables `PLAY_WIF_PROVIDER` + `PLAY_SERVICE_ACCOUNT`; secret `PLAY_SERVICE_ACCOUNT_JSON` removed everywhere. Why: no downloaded key (Google warns against it). The Google side was set up the same day: project maktaba-513518 (number 714997343823), service account maktaba-play-upload with no key, pool/provider `github`, Workload Identity User for principalSet …/attribute.repository/Hamza-Labs-Core/Maktaba; Play Console grant is "Release apps to testing tracks" only (no production). Both variables set by the coordinator. Setup steps: docs/google-play.md "One-time setup".

- 2026-11-02 — PR #541 (merge baa7e1ec): `PlayerConnectProgressUITests` sign-in-and-restore no longer skips on unsigned CI. Cause: an unsigned iOS app can't use the Keychain (`errSecMissingEntitlement`), so no token survived the relaunch. Decision: a Debug-only `-maktaba-secrets-in-file` seam puts `TokenStore` on `UITestSecrets` (JSON in Application Support, removed by `-maktaba-session-reset`); `PlayerUITestCase.launch` always passes it; the Keychain stays covered by `MacKeychain`/`Tokens` unit suites. Bug the skip hid: `RestoreSection`'s Section-level `accessibilityIdentifier` replaced the progress row's `connect.progress.<step>`, so the restore steps were invisible to VoiceOver and tests (a wrapping container doesn't help; SwiftUI merges it and the outer id wins). Fix: no id on the Section; `signin.restoreProblem` on the problem text. All 4 tests pass on iPhone and iPad CI. Gotcha: the scratchpad is shared between sessions, so `xcodebuild -resultBundlePath` needs a unique subfolder.

- 2026-11-02 — PR #560 (merge 338f813c2): App Store Connect rejected Maktaba Remote 0.3.55 (build 20261102.182725) with ITMS-90129, the display name "Remote" already taken. iPhone/iPad display name is now "Maktaba Remote" (Arabic «ريموت مكتبة») in project.yml, the regenerated pbxproj and Remote/InfoPlist.xcstrings; bundle name and ids unchanged. Player, Player Mac and TV already start with Maktaba. New `tools/ci/tests/test_apple_app_names.py` (lint/repo): every Apple app's display and bundle name in every localization starts with "Maktaba" or contains «مكتبة», and the pbxproj matches project.yml. The next tag re-uploads the Remote.

- 2026-11-02 — PR #550 (merge aa376a47): Apple Series tab test lost its tap on iPad. main's run 41488772111 (iPad 3/4) failed `PlayerLibrariesUITests.testSeriesTabListsEverySeries`. From the xcresult: the test waited only for `libraries.tabs`, which shows while the cards still load; on iPad the cards' answer then picks the first library and its page draws beside the list. The Series tap landed while that page drew: main thread busy, touch-up 185 ms late, the segmented control fell back on Libraries, no GET /api/series. Fix (test only): wait for the cards, `screen.library` beside them (iPad) and `settledPage()`, tap, then wait for GET /api/series. App unchanged. Did not reproduce locally (12/12 on a throwaway iPad Pro 11 M5, iOS 26.4).

- 2026-11-02 — PR #559 (merge c822bf1c8): Mac `PlayerMacMenuUITests.testTheViewMenuChoosesEachSection` root-fixed, two causes. (1) App: automatic window tabbing put Show Tab Bar / Show All Tabs at the top of the View menu as it opened, then removed them while open, so items moved up two rows under the pointer (View ▸ Live TV chose Profile; a person could mis-click too). The Player sets `NSWindow.allowsAutomaticWindowTabbing = false` on macOS (one window, no tabs); the test asserts View has no tab items. (2) Test: the menu helper's straight pointer move from View to Home crossed Playback in the menu bar and opened its menu; it now clicks straight below the bar item. Diagnosed from the xcresult screen recording, UI hierarchy and synthesized-event coordinates.

- 2026-11-02 — PR #563 (merge 294aab707): #547's cancel-in-progress on main starved tagging. With a merge every few minutes and ~1 h runs, every main CI and rehearsal run since bdad891d4 was cancelled, so the tag guard could tag nothing (AirPlay waited). Main pushes keep the single `ci-main` / `update-rehearsal-main` group but cancel-in-progress is now PR-only (`github.event_name == 'pull_request'`): the running main run always finishes and GitHub keeps at most one pending run, a newer commit's replacing the older pending one. release/**, re-runs, nightly and manual runs keep their own never-cancelled group; the trail paths-ignore (#553) stays. Release flow: tag the newest main commit whose own CI and rehearsal finished green. test_ci_paths.py asserts main is not cancel-in-progress and PRs are.

- 2026-11-02 — PR #513 (fast search, Series tab pages) merged 3df44b7a1 after origin/main was merged in. Failure on the way: `PlayerSearchUITests.testAnArabicQueryIsSentUnchanged`, iPad 1/4: the sign-in helper's tap on Connect came ~1 s after typing a Latin address on the Arabic keyboard and was lost (picker still showing, address in the field with the keyboard, Connect enabled, no progress). Fix: `PlayerUITestCase.signIn` submits with the address field's Return (onSubmit runs the same `add`); tapping Connect stays covered by ConnectUITests / PlayerConnectProgressUITests. The Player UI shards now keep the app's log (`<device>-<shard>-app.log`) next to the xcresult, error-level only so far (follow-up agent). Added `MockHome.stubPages`, a `seriesListPage` unit test and `testTheSeriesTabPagesToTheEnd`. Merged with only known failures owned elsewhere (Libraries audit, Mac View menu, Windows CloudTest #558).

- 2026-11-02 — PR #564 (merge b5fe27d): Android app names carry Maktaba. After the Apple Remote's rejection (#560), the Android Remote's launcher name "Remote" / «ريموت» became "Maktaba Remote" / «ريموت مكتبة». The Player's name moved from an English-only `resValue` (Arabic phones showed English) to `res/values*/strings.xml`: "Maktaba Player" / «مشغل مكتبة». TV ("Maktaba" / «مكتبة») and Capacitor ("Maktaba") already passed. New `tools/ci/tests/test_android_app_names.py`: every Android app module's manifest label is a `@string` in `values` and every `values-<lang>` (Arabic required), never a `resValue`, starting with Maktaba or holding «مكتبة»; it also checks the Capacitor `appName`. Application ids unchanged.

- 2026-11-02 — PR #565 (merge ea593c5ee), pipeline tests: running tests/cluster with the other folders stopped at ~87% with no summary and exit 0. On a Mac with mlx-audio and the Cohere model cached, tests/stages/test_transcribe.py loaded the real second-listen model (11.5 s on the GPU) and kept its non-daemon `cohere_0` thread; a later `_serve` test's exit (`_exit_if_threads_linger`) saw it and called the real `os._exit(0)`. Linux CI runs the folders together too (`-m unit`, `-m integration`) and was spared only because mlx-audio is absent there; any leaked thread would have made those jobs report success on a truncated run. conftest now refuses the real model load, fails any test that leaves a non-daemon thread running (named), and fails a test that reaches `os._exit` instead of ending the run (forked children still exit). tests/test_conftest_guards.py proves both with a nested pytest run. Full suite in one process: 3210 passed, 708 skipped.

- 2026-11-02 — PR #561 (merge 62213ab6): the SQLite reconciler held the shared connection for its whole pass. Found by #540: on SQLite every engine loop (reconciler, pruner, channel top-up, queue health, heartbeat) shares one connection behind an asyncio lock, and `reconcile_once` ran its pass in one transaction (a 1000-row sweep held it ~10 s, past the 8 s heartbeat, and stalled undrain). Fix: on SQLite no transaction around the pass; each unit commits on its own (one enqueue via `_enqueue_sqlite`, one legacy repair via `finalize_success` guarded by `propagated_at IS NULL`, one `propagated_at` mark); steps select only what is left, so the next pass finishes a stopped one. Postgres unchanged. Also fixed in `Database.transaction` (SQLite): a cancel during `BEGIN IMMEDIATE` left the connection inside a transaction forever, and a cancel while COMMIT/ROLLBACK was queued released the lock early; both now run to their end under the lock, and a closed connection never turns the cancel into an error (a first cut hung the pruner loop). Tests: statement hook per task (heartbeat waits ≤ 12 reconciler statements, was 999; a pass cancelled after each statement leaves only whole units) and cancelled BEGIN/COMMIT tests. Gotcha: a task made inside a transaction inherits `_TX_CONN` and bypasses the lock. Follow-ups launched: Postgres pass in short transactions; numpy missing from dev extras; `test_paths_per_os` failed once.

- 2026-11-02 — PR #555 (merge 4a785bd1c): the sign-in keyboard tests from #522 now prove the keyboard part. Apple: PlayerSignInKeyboardUITests fails unless `app.keyboards` shows a full keyboard (≥150 pt) and the focused password is wholly above its frame; a new AX3 case needs `keepsFocusInView` to scroll. CI iPhone/iPad simulators are set to ConnectHardwareKeyboard=false and AutomaticMinimizationEnabled=false. A probe showed headless xcodebuild simulators already show the software keyboard; #522's keyboard-less shot was `app.screenshot()` leaving out the keyboard window, so the test's shots are now whole-screen (`shot(wholeScreen:)`). Measured on CI: iPhone keyboard y=583 (233 pt), iPad y=925 (282 pt), the password above it in every case. Android: SignInKeyboardTest dispatches an IME inset in a 360x640 dp window; a negative check (StartFrame without IME padding) fails 3 tests. `rememberKeptAboveKeyboard()` turned out redundant (Compose's scroll container keeps the focused field in view), left as is. Merged with two failures outside the change: the Mac View-menu test (fixed on main by #559) and iphone 3/4 `PlayerComponentsUITests.testHero` "Audit failed to complete in time" on the shard's first cold launch (the case #566 addresses).

- 2026-11-02 — PR #562 (merge 3f97c0d5f): mDNS through systemd-resolved now runs for real in CI (`mdns-linux` in `_unit.yml`, `tools/ci/mdns-linux.sh`): resolved's mDNS on, Avahi masked, questions from a peer network namespace over a veth (resolved ignores every mDNS packet from its own machine's addresses). Both copies run as a polkit-denied user (zeroconf `<host>-maktaba`), an allowed user, and root (RegisterService), with plain, dotted, backslash/quote and Arabic names. It found three bugs, fixed in both copies: zeroconf never answered A/AAAA for its own target host (`announce_hostaddr.go`); RegisterService split dotted names ("Studio 1.2"); a `\` in a home name crashed systemd-resolved 255 (`resolvedInstance` escapes `\` and `.`, ≤63 bytes). `tools/ci/mdns_copies.py` keeps `api/internal/discovery` and `cmd/maktaba-server/internal/mdns` identical. Merge commit `3f97c0d5f`.

- 2026-11-02 — PR #571 (merge de0b76b98): flaky `TestServeIntegration` (streaming and api, plus api `TestServeWithAuth_JWKSAndHeaders`). `runServe` opened its public and admin ports in separate goroutines, and the tests waited only on the admin port before hitting the public one (connection refused on #562). `freeAddr` also picked a port, closed it and passed the number on. Fix: `runServe` opens both ports first (`listenPair`) and reports the real addresses through a test-only `onListening` hook; tests use `startServe(t)` on 127.0.0.1:0, and `freeAddr`/`waitForPort` are gone. Rule: a test gets a server's ports from the server once they are open, never picks a port, releases it and polls. Merge commit `de0b76b98`.

- 2026-11-02 — PR #570 (merge b07e485d): the Postgres reconciler commits each unit separately, as SQLite has since #561 (one enqueue, one legacy repair, one `propagated_at` mark). Before, one transaction held the whole pass and the leader lock (`pg_try_advisory_xact_lock`), so jobs it queued couldn't be claimed until it committed. The leader lock is now a session lock (`engine/leader.py`: `pg_try_advisory_lock` on a pool connection used for nothing else, released in `finally`, freed by the server if the connection dies). The key is unchanged, so old (transaction lock) and new workers still exclude each other during an update. asyncpg gotcha: a connection the server ends while in use may not return its pool slot, and `pool.close()` hangs; `leader()` terminates it on the way out. The queue-health round had the same shape and uses the same lock. Backfill commits each row's class, new job and parking together. Merge commit `b07e485d`.

- 2026-11-02 — PR #572 (merge bdd079fcf): pipeline declares numpy (`dev`, `stt`, `search` extras). The tests and the stt/search code import it, but no extra declared it, so `uv run --extra dev pytest` couldn't collect tests/stt and tests/stages. The default-worker runtime test now asks every stage that can be unavailable (transcribe, index, chapter_infer, topic_recluster). pipeline/README.md gives the full-suite command: `--all-extras` with a UTF8 scratch Postgres (a SQL_ASCII server fails write_channels staging on Arabic text). `test_paths_per_os` used home=/home/u for Windows cases, and /home is an automatic network mount on macOS (0.13 s per lookup, over the 0.3 s hard cap under load); it now uses tmp_path. Merge commit `bdd079fcf`.

- 2026-11-02 — PR #573 (merge 619509bd5): desktop-shell-windows flaked on main (run 41509445044). The real Task Scheduler tests ran a PowerShell stand-in agent, and PowerShell's cold start on a fresh windows-latest runner (20–50 s) exceeded the tests' 30 s first-run wait. The tasks now run the test binary itself (`stand_in_agent`, told what to do by a file in the task's working directory), and a wait that runs out prints the task's `schtasks /V` state. Only failure in the last 300 CI runs; not a product bug. Merge commit `619509bd5`.

- 2026-11-02 — PR #574 (merge 8e0553841): the Windows agent-loop tests in `agent_windows.rs` used PowerShell as a stand-in launcher, and its cold start on a fresh windows-latest runner (20–50 s) took one test to 51 s of its 60 s deadline. They now start the test binary itself through #573's `stand_in_agent` (new `launch` order), about 1 s each, and a timeout puts the agent's server.log in the CI report. No PowerShell is left in the desktop crate's tests. Merge commit `8e0553841`.

- 2026-11-02 — PR #569 (merge 958813593): Apple Player Home rows could show a title with no cards (`testNotInterestedRemovesTheCard` failed once in 53 runs, For You empty). `Rail`'s cards were in a `LazyHStack` inside a sideways `ScrollView`, which is only as tall as the cards built so far; a row just scrolled in is 0 pt, and Home's last row could stay at 0 pt with no card ever built. Fix: `Rail` uses a plain `HStack` (every rail is a short list). Home tests check each row has cards as soon as it shows; `PlayerComponentsUITests.testRails` requires all 6 gallery cards (5 with the lazy rail). Never reproduced locally; CI recording and screenshots gave the cause. Merge commit `958813593`.

- 2026-11-02 — Apple app names in Arabic (#568, folded into #539): Player (iPhone/iPad) and TV InfoPlist.xcstrings localize CFBundleDisplayName "Maktaba" / «مكتبة». The Mac Player has its own `Player/Mac/InfoPlist.xcstrings` (CFBundleDisplayName and CFBundleName "Maktaba Player" / «مشغل مكتبة»), so the menu bar no longer says "Maktaba Player Mac". `test_apple_app_names.py` requires one InfoPlist catalog per app, an Arabic display name containing «مكتبة», and en+ar for every Info.plist key people see. Finder still shows "Maktaba Player Mac.app" (macOS needs the file name to match the display name).
- 2026-11-02 — process: small root-fix PRs (#566–#575) each paid the full Apple CI and each merge replaced main's pending run, holding back the tag. Apple fixes now fold into one integration PR (#539); fix agents push branches, not PRs.

- 2026-11-02 — Apple UI tests keep the app's whole log (#567, folded into #539): `log show --info --debug` was already used, but the simulator keeps a subsystem's info/debug lines only while `log config --subsystem <exact bundle id> --mode "level:debug,persist:debug"` asks it to, and a parent subsystem's setting isn't inherited. Each shard sets it after boot and reads the log once after the tests; the Mac job now keeps `mac-app.log`. Every launch writes `launched …` (info) and `launch arguments: N` (debug) via `Log.launched()`, and `tools/apple/check-app-log.py` fails a job missing either (or says "never ran" when the app never launched). `testADelayKeepsTheQuerysAnswer` reads `MockHome.delay(of:)` instead of timing.
- 2026-11-02 — Libraries accessibility audit (#566, folded into #539): two causes. (1) A shard's first app launch on a new simulator spends 13–36 s setting up the XCTest automation session and stays slow for 1–2 min; PlayerUITestCase now warms up once per process. (2) On the iPad root, rows build cards and fetch posters lazily, and the audit reaches every element, so posters loaded mid-audit; `loadEveryPage` now scrolls each row to its end and back and waits until no more posters are asked for.

- 2026-11-02 — RouteStub flake (folded into #539): `Credentials.aRedirectToAnotherOriginDropsTheToken` sometimes ended as `.status(302)`. The stub sent both `wasRedirectedTo` and the 3xx answer, and URLSession raced them, so a followed redirect sometimes ended as the 302 (2 in 2000 under CPU load). It wasn't shared stub state; those suites already run one at a time. The stub now asks the request's delegate (`RedirectRule` in `Redirects.swift`) and tells URLSession only one of the two. New test `aRedirectEndsOneWayEveryTime`. Branch `fix/routestub-isolation` @ 23cf5b80e.

- 2026-11-02 — Apple permissions walkthrough stayed up after Continue (PlayerVideoUITests deep-link test, folded into #539): `PermissionsGate`'s `@State` initializer called `forThisApp()`, which under `-maktaba-permissions-reset` deleted "seen" every time the app's body ran, so the next refresh showed the sheet again. The reset now runs once from the app's `init` (`applyLaunchArguments`), and `forThisApp()` has no side effects; `buildingTheAppsModelAgainKeepsSeen` fails on the old code. Rule: a `@State` initializer must have no side effects. Branch `fix/permissions-gate-state-init` @ 0569cb052.

- 2026-11-02 — `PlayerWatchTests` "the time moves" timeout (#575, folded into #539): a UI test left the Player playing (XCUITest doesn't close the app at the end of a test), so its non-mixable `.playback` session kept the runner's own AVPlayer from starting sound (HALC `_StartIO` error 35), and AVPlayer's clock never moved. #539's per-method sharding exposed it by putting the play-from-start test right before. Fix: `PlayerUITestCase.tearDown` closes the app, and `PlayerWatchTests.setUp` fails at once if a Player is still running. Not a product bug.

- 2026-11-02 — PR #539 (merge 99e51114c): the single Apple integration PR. Player UI tests build once per device and run 6 duration-balanced shards per device (`tools/apple/ui-durations.json`), with the three text sizes checked in one launch through a Debug-only, flag-gated `TextSizeHook`. Shards boot while the app builds and wait until the simulator's own processes are quiet (`tools/apple/sim-settle.py`, at most 8 min; waiting for the whole runner to go idle cost 12 min per shard), then fetch the build through the API (`actions: read` in `ci.yml`). The longest shard went from 53.6 to 29.8 min. Folded in: #566 (warm-up and Libraries audit), #567 (whole app log), #568 (Arabic names), #575 (close the app after each test), the RouteStub redirect race, and the permissions-gate `@State` fix. #566's rail swipes were removed: after #569 they did nothing, and a swipe back left a row under the iPad sidebar, failing contrast. testShimmer reads each text size only after the app took it.

- 2026-11-02 — PR #576 (merge a6d918a92): Live guide UI tests flaked (4 of about 200: "time scrolled 116→116", "channels scrolled up 427.5→427.5"). Not a product bug: the XCUITest swipes were quick flicks (58 pt in 58 ms on an iPad row), a drag that finishes in under about 20 ms is never scrolled, and on busy runners the swipes moved nothing. A two-axis guide rework didn't change the result and was dropped. `PlayerUITestCase.drag` presses, moves at 400 pt/s and holds before lifting; the guide tests use it, and the time test checks it returns to its start. Lesson: scroll in tests with press-drag-hold, not `swipeUp()`/`swipeLeft()`.
- 2026-11-02 — v0.3.56 tagged by hand at 655b565 (#539's final PR head, the same tree as main's merge commit 99e51114c). Its PR CI passed every check, and the update rehearsal passed on 99e51114c. Main CI on 99e51114c had failed three iPhone/iPad UI shards. Decided: tags no longer wait on those shards; #577 changes the tag guard to match.

- 2026-11-02 — PR #577 (merge d3f00845): the tag guard no longer waits on the iPhone/iPad Player UI shards. `tag-guard.sh` judges a failed CI run by its jobs: all must be success or skipped except `IGNORED_CI_JOBS` (the iphone/ipad shards) and `ci-success`. Mac UI, the UI test builds and cancellations still block, and a run in progress is still waited for. Each release prints the ignored failures as a warning. Remove `IGNORED_CI_JOBS` once the shards are fixed. Merge commit `d3f00845`.

- 2026-11-02 — PR #578 (merge 5e4bd3943): the Apple TV app (Maktaba TV, com.hamzalabs.maktaba.tv) goes to TestFlight on every v* tag, as a third entry in testflight.yml: tvOS archive, the same automatic signing, and fail-fast off so a TV failure leaves Player and Remote alone. The TV target now declares ITSAppUsesNonExemptEncryption NO; its icons, Top Shelf images and Arabic name were already in place. `test_testflight.py` keeps all three apps in the matrix. The owner still has to create the tvOS app record in App Store Connect. Merge commit `5e4bd3943`.

- 2026-11-02 — PR #580 (merge 302453bdb): Player UI accessibility audits timed out ("Audit failed to complete in time" / no snapshot after the audit). Cause: `.textClipped` lays the app out again at all 12 Dynamic Type sizes within the audit's ~15 s limit (10 of 16 s; leaving out `.dynamicType` never stopped that sweep), and with all tabs showing, UIKit's tab bar took about 40% of each relayout (2.0 s vs 1.1 s per check). Libraries' audit now launches the section alone, and `loadEveryPage` counts only the current launch's requests, so grid page c2 no longer loads mid-audit. Every audit prints its time, and sim-settle logs the runner's busiest processes (iPhone 2/6 ran at 0% idle all job).

- 2026-11-02 — PR #579 (merge 055885f19): the Player's background audio mode never shipped. `INFOPLIST_KEY_UIBackgroundModes` isn't a key Xcode generates, so iPhone/iPad builds, TestFlight included, had no `UIBackgroundModes`: PiP was never possible (button disabled by `!pip.isPossible`) and leaving the app cut the sound. It is now in `info.properties`, and the watch and live players use `.continuesIfPossible`. Tests: MaktabaKit `BackgroundPlayback` checks the plists and policy; UI tests press Home and check playback moves on. Lesson: check the built app's Info.plist, not the build settings; the simulator doesn't enforce background modes. Device behaviour still to confirm on the next TestFlight build.

- 2026-11-03 — PR #582 (merge f4527625): PlayerLiveUITests guide flake (seen on #579): the tap on the Now | Guide switch was dropped on a saturated runner (the recording shows the thumb lifting and settling back on Now; the hierarchy has Now selected), so the guide never opened. Not reproduced locally in ~80 taps under load, so the cause of the drop is unproven. The guide tests now launch on the guide (`-maktaba-live guide`), and `testNowGuideSwitch` checks the switch on the spot. Separately, an iPad Up next failure was a settings leak: the Profile test left autoplay off on the simulator. The app now applies `-maktaba-settings-reset` at start on every fresh test launch. Also: Up next now shows when the next episode is found after the video ends (`Playback.nextFound`).

- 2026-11-03 — PR #583 (merge a2b97e892): the Mac Player and the iPad Player join the TestFlight upload on every tag. An unsigned archive carries no entitlements, so the Mac leg signs `Player-Mac.entitlements` into the archived app ad hoc before the export, keeping its sandbox. The Mac app had no icon until now (the single-size macOS icon entry was ignored); it now has every Mac size, plus `ITSAppUsesNonExemptEncryption: NO`. `test_testflight.py` covers it. Records: TV (6830067622) and Mac created through Xcode's Organizer on 2026-11-03 (POST 201, Xcode's error after is harmless); the iPad record's creation got HTTP 500 from Apple, still missing.

- 2026-11-03 — PR #584 (merge 7aa1af48): Epic 50 spec, Maktaba Cloud with no downtime. Phase 1 is two Hetzner nodes (FSN1/NBG1) plus a HEL1 witness: Patroni with etcd on all three, the Floating IP moved by Patroni's role-change callback, synchronous replication, blue-green deploys with a DRAIN frame and resume tickets, pgBackRest backups. Phase 2 is a relay pool with rendezvous-hash assignment and a tunnel directory. Rejected: keepalived (two nodes can't break a tie), pg_auto_failover (its monitor is a single point of failure), routing at Hetzner's load balancer (it can't), per-home DNS records (slow to fail over). Hetzner's 2026 price rises put Phase 1 at about €79 a month; six owner questions are in README §13.

- 2026-11-03 — PR #585 (merge c7b503cc9): Epic 52 spec, word-level transcript review. The second review in the transcript path today is the Cohere second listen, not an LLM. Whisper's word probabilities are computed and then dropped, and word times are stored only when `MAKTABA_STT_STORE_WORDS=1` (off by default). So `transcribe` will capture word evidence and a new CPU `word_check` stage will flag suspect words using language packs (Epic 51 components). The LLM judge flags but never rewrites and is off by default (an LLM judge was wrong in the hearing test). Learned vocabulary stays out of the transcribe fingerprint, so it never re-queues a library; four owner questions in README §8.

- 2026-11-03 — PR #586 (merge 8ea67af5): Epic 53 spec (logs, errors, problem reports, a UI that never goes dark), 17 stories. Logs are written to per-component files by the supervisor; the search index is a separate SQLite file in a crash-isolated `logs` process, chosen over Postgres so search works when Postgres is down and logs stay out of backups and the cluster copy. A recovery page and status endpoint served by the supervisor (admin "recovery pass", because web sessions need Postgres), safe mode, and a CI fault test that blocks releases. Epic 50 edited so 53.17 supplies the cloud's logs, errors and metrics and 53.06 draws 50.11's banner; three owner questions in README §12.

- 2026-11-03 — PR #587 (merge 04f09fc84): Epic 51 spec, configurable components. One catalog of ~30 components; wanted state (absent/installed/enabled plus a hold that survives restarts) in Postgres, actual state per machine with reason codes; disabling stops taking jobs, drains, pauses what is left with no attempt counted, keeps the queue and catches up on enable; dependents wait instead of being disabled; packs come out with each server release under its existing signature; existing installs are seeded from today's switches and checked by update-rehearsal scenario 12; no new paywalls. Rejected: `server.toml` as the store, cascade-disable, one installer per bundle, a third-party store. The story writers found ~25 README errors against the code (wheels download on first run, ollama is Mac only, the relay agent can't be gated, live updates use SSE), all fixed before merge.

- 2026-11-03 — PR #588 (merge b1ed8f51): answers to Epics 50–53's open questions, applied in one docs PR. Epic 50 gains 50.19: Free (no relay), Basic (~3 Mbps relay, 2 streams, 500 GB soft fair use), Pro (full quality, optional capped pay-as-you-go, never cut off), extra streams paid; no downtime credits; direct certificates on several separate domains outside maktaba.tv; R2 backup copy; CPX22 etcd witness. Epic 51: Stop lasts until it is turned back on (no timed holds); plugins point to Epic 54. Epic 52: no LLM judge (52.7 kept as a removed stub), review admins only, background check of all ~45k transcripts, Quran check against the Tanzil text (52.15). Epic 53: crash reports and screenshots off by default, asked at setup; retention 7/30/90 days within 1 GB.

- 2026-11-03 — PR #589 (merge 43b18152f): Epic 54 spec, third-party plugins that extend the queue with no internet access, plus a Maktaba store. Plugins are WebAssembly components run by a sandboxed Rust host (`maktaba-plugind`, Wasmtime). Sockets and files are never given to plugins, the host runs in each OS's sandbox, and a CI-only job proves zero network connections on macOS, Linux and Windows. The pipeline keeps the queue and the API checks every call against admin-granted permissions; each plugin is an Epic 51 component. The store (maktaba.tv; docs on maktaba.dev) has automated checks plus human review, developer and Maktaba signatures under an offline root, a signed revocation list, offline file install behind an "Allow unreviewed plugins" opt-in, and paid plugins via Stripe Connect (85/15 proposed). Open owner questions are in README §14.

- 2026-11-03 — PR #590 (merge 3d1fc7b1b): On a Razr Fold's cover screen, the Remote's "Needs attention" tab label wrapped to two lines and ran into the screen edge. Pages also went blank when switching tabs, because each tab's state was thrown away. PR #590 makes bar labels one line at one shared size, inset from the edges, in both Android apps. Pages now keep what they showed while they refresh, and attention groups show translated titles with clean detail lines instead of raw grouping keys. Screenshot tests now cover the Galaxy Fold, Razr Fold (density estimated) and Pixel Fold cover screens at text sizes up to 2.0, in English and Arabic.

- 2026-11-03 — PR #591 (merge fb955c66b): applied the Epic 54 decisions. No Stripe ever; 85/15 split; seller of record and payment provider decided at Phase 3 (candidates Paddle, Adyen for Platforms, or developers selling directly with offline licence keys; reselling third-party products must be confirmed in writing); $25 one-time fee to sell; English and Arabic listings with labelled machine translation; source to reviewers for every version; a sticky admin keep-off separate from the store switch-off that can't be undone; library checkboxes start empty; panels shown to everyone once installed. Story 54.19 was rewritten with no fixed payment provider, and Epic 50's Stripe became "payment provider TBD (not Stripe)". Same PR: README §15 and stories 54.22–54.36 (Phase 4) spec turning all 56 of Maktaba's own stages into 15 native, trusted first-party plugins on the same interface, using named statements, a database role per plugin, a per-stage switch, CI parity proofs and nine migration waves.

- 2026-11-03 — Stripe removed everywhere (PR #593, merge 1d5bd9a07), per the no-Stripe rule; payment provider TBD. Cloud billing now goes through a `billing.Provider` interface (checkout, portal, verify+parse webhook into neutral events). The only shipped provider is `none` (default `[billing] provider = "none"`; unknown providers fail config validation): paid plans show "Payments coming soon" in English and Arabic, checkout and portal return 503 `payments_not_set_up`, and `/v1/billing/webhook/{provider}` returns 404. Service admins set plans by hand via the new `POST /v1/admin/accounts/{id}/plan` (reason required, audited, not revenue, 409 while a provider bills) and a Set plan dialog in the web console. Migration 00160001 renames the Stripe-named billing columns and ledger to `provider_*` / `provider_events` and adds a `provider` column (up/down tested). Remaining grep hits are old migration SQL, the 0016 rename statements, StripEpisode, visual stripes, "never Stripe" lines in Epics 50/54, and the trail.

- 2026-11-03 — PR #592 (merge 646dee3f6) carries #590's "Needs attention" fix to the web queue page and Maktaba Remote for iPhone/iPad. Both now title a failure group by its cause code, or a generated cause's kind, in English or Arabic, using Android's exact words stored under `queue.issues.cause.<code>` in en.json, ar.json and MaktabaKit's catalog. The home's English label is used only for a code the app has no words for, and never when it is a grouping key with `<n>`/`<path>`. A group's sample message shows its escaped line breaks as real lines, with Arabic path parts bidi-isolated as in AttentionWords.kt. The logic lives in `web/src/lib/attentionWords.ts` and `MaktabaKit/AttentionWords.swift`, with web and MaktabaKit unit tests for English, Arabic and the fallback. QOV-13's crash test now uses an unmapped cause, since known causes no longer render the server label.
- 2026-11-03 — PR #596 (merge 6c8cadc2b): The owner saw a TV's sign-in code and couldn't find where to approve it: it was only in the account menu. Now: the Android Remote has an "Approve a TV or device" button on every top bar (the account menu item stays), and the Player has it as the first Profile row. The Apple Remote has a toolbar button. A new signed-in `GET /api/pairing/pending` lists waiting devices without their code or secret, rate-limited per IP; cloud sign-ins get an empty list. The Android Remote and Player poll it every 12 s while open, show a banner with "Enter code", and post a notification on a "Sign-in requests" channel only if the permission is already granted. The Apple Remote shows the banner only. The device poll's 202 now carries `looked_up`. The Android TV and tvOS code screens name the phones' exact button, plus the older "Approve a sign-in", built from the phones' own string and tested so it can't drift. They also show a countdown, "Get a new code", and "Your phone found this TV". English and Arabic everywhere.
- 2026-11-03 — PR #599 (merge d78f92b2e): `docs/hardware.md` says what the home and worker machines need, measured read-only on the live M3 Ultra home (v0.3.62). It transcribes 1,125 audio-hours a day (~47× real time, 4 files at once, second listen on every transcript). The GPU is 89–98% busy while the CPU is 72% idle, so GPU compute is the bottleneck. The 14,206 audio-hours still queued take about 13 days; the earlier "a day or two" for ~45k videos was wrong by ~10×. The database is 64 GB (44 GB search vectors), about 2.8 GB per 1,000 transcribed audio-hours. Every speed is marked measured, from the code (2080 SUPER 72× Whisper alone, 43× with second listen; CPU 2.6× on 10 threads), or estimated with its method. Live TV encoder CPU is still not measured. The README has a short summary that links to the page.
- 2026-11-03 — PR #600 (merge 51f0c1568): Epic 50 home broadcasting. Specced publishing a home's Maktaba TV channels to outside viewers through Maktaba Cloud as README §14, research-broadcasting.md and stories 50.20–50.29. Owner decisions: viewers never pay (invite or open only); Maktaba charges for infrastructure, not content (Maktaba Cloud is a hosting provider with no cloud-side curation); only publishable items air (own recordings, CC BY detected at YouTube intake, or sources with recorded permission; the scheduler skips the rest); the rights section is practical and marked not legal advice. Design: the home pushes each segment once to a multi-tenant Cloudflare edge (Worker, Durable Objects, R2); token-free cache keys keep the home's upload independent of audience; quality levels made at home; Cloudflare Stream as fallback. Tiers: Pro guest sharing plus a Broadcaster add-on, about $46/month at its allowance on the primary path. Prices, a Cloudflare terms confirmation and a lawyer's review gate the launch.

- 2026-11-03, #597 (docs): the owner asked whether the README and user guide covered everything; they didn't. Checked against main, the merged PRs and the CHANGELOG, the README still described Chroma, Go 1.23, only the Capacitor and Tauri wrappers and 25 epics. It had no way to get the apps, no TestFlight or Play internal testing, no live TV, watermark, Needs attention, updates, machines, Plex or consent, and no roadmap or domains. The in-app help (English and Arabic) didn't say how to get any app, put TV approval in the wrong place, left out the Player's sections and the TV apps' Live TV, and was wrong about Android permissions and background playback. All of it was fixed. #596 merged while the PR was open, so the docs name its "Approve a TV or device" button, banner and notification. The wiki index got rows for Epics 26–54. The wiki JSON was not regenerated because its generator reads a missing May snapshot and would overwrite newer entries. Merged as 107f01d29 after two merges of main (#596, #599).

- 2026-11-03: maktaba.dev is live (#598, 9d1ab6b78). `web/packages/devportal` is a Cloudflare Worker with static assets (wrangler 4.151.0, custom domains for maktaba.dev and www, no workers.dev URL, no KV, no secrets). It serves an EN/AR landing page, the wiki at `/wiki/` built with `--base /wiki/` and `VITE_WIKI_NO_MOCKUPS=1`, Redoc over `shared/api/openapi.json`, the Epic 54 developer stories, the changelog, and a security.txt generated from `disclosure.go`. The build is an allow-list (`DOCS`/`FILES`/`WIKI_ALLOW` in `scripts/build.mjs`). Markdown renders with raw HTML escaped and links filtered by `isSafeUrl()` (`scripts/urls.mjs`); `scripts/scan.mjs` fails the build on internal material. CodeQL raised four high alerts, all root-fixed. `devportal-deploy.yml` deploys on push to main and by hand after its own build, using the org Cloudflare secrets; it skips when they're missing and smoke-checks `/`, security.txt and the www redirect. The first deploy passed. security.txt expires 2027-01-01.

- 2026-11-03, #606 (merge 9046fc512): the Epic 54 plugin store is now independent of Maktaba Cloud. It has its own store accounts (email verification, MFA required for developers, passkeys for reviewers and store admins, re-auth for sensitive actions, server-side roles), its own D1 databases and deploy, and its own hostname `plugins.maktaba.tv`, with signed files on `plugins-cdn.maktaba.tv`. It runs only on Cloudflare (Workers, D1, Durable Objects, R2, Queues, and Containers or a no-secrets CI runner for upload checks), with stated limits, D1 Time Travel plus nightly R2 exports, and an estimated $5 a month at launch. Homes link to a store account with a device code, check store-signed entitlements offline, and poll a signed revocation file on the CDN every 5 minutes, so nothing on the plugin path calls the cloud. Payments are the store's own, separate from cloud billing and never Stripe; the provider, the email provider and the check runner are still open decisions.

- 2026-11-03, #601 (merge 787cfab3f): The Windows node has an RTX 2080 Super but never transcribed, so every transcribe job ran on the Mac. Six causes combined: GPU detection used NVML only, with no nvidia-smi fallback; STT counted only through mlx_whisper; transcribe waited for the second listen, which an 8 GB card cannot run on Windows, and the saved reason said only "unavailable"; the gpu pool size came from one global setting; transcribe did not check that the machine can read the share; the node installers installed no extras by default. #601 fixes all six: detection shares one probe with venv_bootstrap and falls back to nvidia-smi; STT is detected through mlx or ct2 and the node reports which; Machines shows the full reason in English and Arabic; each node sizes its gpu pool from its own card; transcribe is gated on the share read check; the installers and updater add the stt extra on a qualifying NVIDIA GPU. docs/hardware.md now calls this a detection bug. affected-tests only accepts tests from its coverage map, so the stage tests went into tests/cluster/test_stages.py. node-packaging now restores the cached static nfpm instead of building it from source each run.

- 2026-11-03, #605 (merge 1bd26d476): Epic 50 §15 and stories 50.30–50.34 put the user portal and the operator console (`admin.maktaba.tv`) behind the Cloudflare proxy, with WAF, rate limits and Turnstile. The origin is reachable only through a Cloudflare Tunnel from the database leader, and `CF-Connecting-IP` is trusted only from that local connector. Relay video stays DNS only because of Cloudflare's terms. The console uses Maktaba's own staff accounts, separate from customer accounts, with required MFA, short sessions, re-authentication, lockout and full audit. Cloudflare Access was dropped, and admin routes are 404 on every user name. The independent plugin store (`plugins.maktaba.tv`, #606) sits behind the same proxy with its own auth, admin pages, alerts and deploy token. Cloudflare deploys run from CI only, and the owner's setup steps and estimated costs are in §15.7.

- 2026-11-03, #595 (merge 696022a3d): fixed four Apple TV problems. The live banner stayed up after any start slower than 6 s and the mini-guide never hid; both are now timed by a `LiveOverlays` model in MaktabaKit, and a CI UI test checks the picture fills the screen and the logo corner stays clear. The burned-in logo was missing from filler because `tv.watermark_skip_filler` defaulted to true; it now defaults to false, and `write_channels` v5 (migration 0173, SchemaRev 173) re-stages every channel. The TV home called only `/api/recommendations`; it now builds the Player's rows from a shared `HomeShelves` model with retry tiles. Library tiles drew an icon; they now show each library's newest small poster. Added the first CI job for tvOS UI tests and a cross-service test that stage version bumps carry a migration and SchemaRev. Two iPad Player fixes found along the way: `PlayerTracks.sound` read only the first enabled audio track (reported `sound=audio` after a switch, failing on main too), and the permissions walkthrough could stay up after Continue; Continue now calls the sheet's own dismiss.

- 2026-11-03, #609 (merge df3c0bfa7): the cluster folder had kept every worker's status and process file for ever, about 320 status files and about 520 process files across the local folder and the share. Each agent now deletes its own status and process files when it stops cleanly. Every worker and the hub run a pruning pass at start and every hour: files over an hour old whose process is gone (a reused pid counts as gone) plus orphaned `.tmp` files, never a live process's file. On the share, files are matched to this machine by machine id or by being a home process, not by host name, because macOS renamed the Mac twice. The API and the pipeline commands no longer list gone processes. Found because Epic 51's test rig had leaked fake workers (live-N, chaos-N, w1) into the live home, which were deleted by hand with the owner's OK.

- 2026-11-03, #602 (589c4c17a), #607 (bc5c02a9d), #610 (76a42bd82): maktaba.dev was cut down to developer guides (plugins, contributing, security), the API reference and the changelog. The epics stay on GitHub, and `/wiki/` now redirects there. security.txt's `Expires` rolls to 11 months after each build, and a monthly deploy keeps it fresh. The wiki generator reads the repo's own specs, and a CI check keeps its output in step. Linear links are gone from everything served. Every page now also exists in Arabic under `/ar/`, right to left, built from Arabic twin files: `name.ar.md` guides, `CHANGELOG.ar.md`, and an OpenAPI translation file (`shared/api/openapi.ar.json`) keyed by JSON pointer. The build fails when the two languages drift, including when an Arabic page is mostly English (over 20% Latin prose). The plugin publishing guide follows Epic 54's store design. #610 fixed the post-deploy smoke check, which still looked for a marker #607 removed.

- 2026-11-03, #612 (merge 30c420126): TestExecRunner_WritingEncoderRunsOn failed on #604's integration job because its fake encoder, a shell loop starting `date` and `sleep` every 50 ms, got no CPU time for longer than the 400 ms stall limit on a busy runner, and the watcher correctly stopped it. The product code was right and the test was racing the scheduler; running it at background priority under CPU load reproduced it (10 of 40 failed), and the missing ffprobe made no difference. The stall watcher now takes its ticker and start time from test hooks and judges each check by its tick's time; the test steps that clock itself and writes files with their times already set, so it checks both sides exactly with no real waiting, and moved to the unit tier.

- 2026-11-03, #613 (merge e91dab862): the Android video page test `favoriteAndMarkWatchedToggleAndPersistOnReload` failed now and then at its "and off again" step because of a real product bug. `VideoPage.load()` wrote back favorite and watched from a read the server answered before a newer toggle's save, so a tap during a reload was undone on screen while the server kept it. The test waited only until the read was asked, not answered, so its click could land in that window. `VideoPage` now counts toggles, and a read that began before one keeps what was tapped, including Mark watched's playback state. `aToggleWhileThePageIsReadAgainIsKept` holds the read's answer on a latch and reproduces the bug every time without the fix.

- 2026-11-03, #603 (2fa5b86ea) and #604 (6827015f8): every verbatim quote of the owner's words and every attribution like "(owner, date)" was removed from the repo and from maktaba.dev and replaced with neutral statements of the requirement or decision: about 72 in the trail (#603, trail-only), about 330 across specs, wiki and docs, and about 170 in code, migration, app, pipeline, tool and workflow comments (#604). "Owner" as a role (user stories, pending decisions, the home's owner, file and DB owners) stays. CLAUDE.md rule 5 now forbids quoting the owner in repo text. Old git history and old PR bodies still hold the quotes; history was not rewritten. #604 needed four merges of main and waited for the stall-test (#612) and VideoScreen (#613) root fixes.

- 2026-11-03, #611 (merge 44524a4ef): the second listen became its own stage. `transcribe` stores Whisper's transcript with its word timings, and `second_listen` corrects it from that transcript and the audio, so subtitles, the index and tags run once on the corrected words. On a GPU too small for both models (the 8 GB RTX 2080 SUPER on Windows: 6.95 GiB free, Cohere needs 6.06), Whisper and Cohere take turns, one batch each (max 30 min), with the idle model's weights parked in system memory (`stt/gpu_turns.py`); the choice comes from the driver's measured free memory, not the OS. Also fixed: re-running Whisper replaced the corrected transcript and never re-queued the second listen when the text matched. Measured on the M3 Ultra with a fixture: Whisper 43–48×, second listen 91–116×; the 2080 SUPER is ~27× combined (estimate, PC unreachable). Migration 0174 seeds the stage (SchemaRev 174).

- 2026-11-03, #615 (merge 02265023c): `desktop-player (linux)` failed on #604 because the Gradle jobs' `actions/cache` entries were evicted from the repository's over-full Actions cache (11.6 GB of 10 GB, mostly pull-request Go and Rust caches), so every run downloaded all plugins and dependencies, and one wrong answer from Maven Central failed the build. The desktop Player, Android apps and Android TV jobs now use `gradle/actions/setup-gradle@v6.4.0`, keyed on the wrapper, catalogs and build scripts, with only main writing. Google's repository now serves only the `com.android*`/`com.google*`/`androidx*` groups in all three builds. A read-only PR run restored all 8 entries on exact keys.

- 2026-11-03 — PR #619 (merge cc51746c8): The in-app help still said the five Epic 47 stages arrive in a later version after migration 0170 had turned them on; PR #619 (cc51746c) removed that text in English and Arabic. It documents how a conversion decides (skip, remux, AAC 192k audio, or an H.264 re-encode that needs `convert.allow_reencode`), and that migration 0166 left every existing channel as a source set to Off. There is no per-video Convert now and nothing reads `convert.auto` yet; the help says so and points to the item page's Choose queues… and the library menu. Manage libraries now has a YouTube sources… button that reuses the Queues page's dialog. The help coverage test now reads the pipeline's stage registry and unreleased flag, so a shipped stage's entry can't keep "not set up yet" text.

- 2026-11-03 — PR #614 (merge bdc25c10d): The Player's Home now builds its rows with MaktabaKit's `HomeShelves`, as the Apple TV does; a row that fails keeps what it last showed or shows a Try again tile. On tvOS a stall over 1 s shows the banner and "Buffering…", and a bottom-corner logo moves the banner and mini-guide to the top, using the `watermark_position` the guide API now sends. On both TV and Player a player still waiting after 15 s reopens the stream: on loaded CI runners AVPlayer gave up on a late segment and waited for good, including after an audio switch on the iPhone. The Player's accessibility audits now load each text size's posters before they run, and a restored `@SceneStorage live.mode` no longer overrides the launch's view. The secrets store test used to look for the two bytes "v2" in random AES-GCM ciphertext, which matched about once in a couple of thousand runs; it now checks a long, distinctive value.

- 2026-11-03 — PR #620 (merge 90f3578e5): The epics wiki is back on maktaba.dev in English (`/wiki/`) and Arabic (`/ar/wiki/`, RTL); deploy run 41724169451 passed with the new wiki smoke checks. The Arabic lives in a translation store, `docs/wiki/db/ar/` (one file per epic plus `common.json`), keyed by entry id and field with a sha256 of the English; `generate_wiki.py` writes `web/wiki-app/public/db/ar/`, and endpoint summaries reuse `openapi.ar.json`. All 1333 entries of Epics 1–54 are translated (1221 texts written by hand). `generate_wiki.py --check` and the devportal `checkWiki` fail on missing or stale Arabic, dropped code, or mostly-Latin prose, naming the file, entry and field; `--ar-todo` lists what to translate. The repo `.gitignore` `lib/` rule hides new files under `web/wiki-app/src/lib/`, so `i18n.ts` had to be force-added.

- 2026-11-03 — PR #618 (merge eacad4dea): Only a push to main writes the Actions cache now; pull requests and tags restore main's entries. setup-go and setup-node run without their own cache, and `.github/actions/go-cache` and `node-cache` restore under the same keys those actions use, so main's existing entries still hit; each job ends with an `actions/cache/save` step that runs only on a push to main. uv, rust-cache and golangci-lint use their own switches, every other `actions/cache` became a restore step plus a main-only save, the apt keys lost `github.run_id`, and play-bundles and desktop-release use setup-gradle read-only (desktop-release's Linux download keys now match CI's). The PR's own run restored main's Go, pnpm, uv, Rust and Gradle entries and left no `refs/pull/618` entries; 41 old PR entries (about 9.5 GB) were deleted, and the cache went from 10.7 GB to about 5 GB. Two Apple UI flakes were fixed at the root: Continue on the permissions walkthrough is tapped only once the sheet has stopped moving, and the quality-reopen test checks the start between the positions read before and after the tap.

- 2026-11-03 — PR #621 (merge 8648eea4b): The Queues page's libraries table (42,565 px tall for 65 libraries, sideways scroll at 375 px) is replaced by a library dashboard (`/queue/libraries`) with one tile per library and an admin page for each library (`/admin/libraries/:id`); the Queues page keeps a short list of the libraries needing attention. A new admin-only aggregate, `GET /api/queues/libraries/activity`, gives each library's finished and failed jobs per day, cached for a minute. The progress ring always uses the done colour, so its percentage reads as work done; failures show only as the red line and the tile's edge. Running the branch API with the default HOME swept expired files from the live `~/.maktaba/cluster` (the same 14-day rule the live hub applies hourly, so nothing it would keep was lost); branch stacks must point HOME, `MAKTABA_CLUSTER_DIR` and `MAKTABA_ARCHIVE_DIR` at scratch folders.

- 2026-11-03 — PR #616 (merge 0ae136a64): The race #613 fixed on Android's video page was also on the Apple Player video page, the series pages on Apple and Android, the web library grid, and both Remote apps: a change saved while the page's read was in flight was undone on screen while the home kept it. The fix keeps the changes made since the read began; in the Remotes, a read that began before an action's save is dropped. `VideoPage`, `SeriesModel`, `LoadProblem`, `loading` and `RemoteModel` moved into MaktabaKit so `swift test` covers them, each with a latch test that failed before its fix. The Player UI test warm-up now waits longer, records what it hits as an expected failure and goes on, since a new iPad simulator's slow first launch had been failing each shard's first test.

- 2026-11-04 — PR #622 (merge 240e68ff7): The README now opens with an apps-by-platform grid, and Native apps and Downloads are merged into one Get the apps section. Checking the code and release workflows showed no Mac Remote exists, the iPad Remote (`remote.tablet`) is built by no release or CI job, and the Mac server app is one Apple-silicon `.dmg`; the README, `apps/README.md` and the devportal's architecture and repo-layout pages (English and Arabic) now say so.
  - - 2026-11-04 — PR #623 (merge 9af6cbe3c): maktaba.dev gained an Apps section, `/apps/` plus pages for Player, Remote, TV, the server and the web app with big screen, in English and Arabic, built from `web/packages/devportal/scripts/apps.mjs`. It has 47 dark WebP screenshots on test data only: the Apple CI UI-test artifacts and Mac test attachments, the Remote from `StatusUITests` on a throwaway simulator, Android screenshot-test references, and a scratch branch stack (own Postgres, HOME, `MAKTABA_CLUSTER_DIR`, `MAKTABA_ARCHIVE_DIR`) for the web and big screen. The leak scan now checks file names and image metadata (EXIF/XMP refused, PNG text scanned), and a new parity check covers the Apps texts and images. Android screenshots whose fixtures show a personal home name, a LAN address or a media path were left out, so Android TV has no image yet.
  - - 2026-11-03 — PR #625 (merge 4a44ae6fc): Maktaba Remote iPad (`com.hamzalabs.maktaba.remote.tablet`) joined the TestFlight matrix once its App Store Connect record existed (created through Xcode Organizer) and its first build, 0.3.73 (20261104.001839), had been uploaded from Xcode. Every `v*` tag now uploads six Apple apps, and the Apple apps check builds the iPad Remote scheme on pull requests; `tools/ci/tests/test_testflight.py` fails if a TestFlight app is not built by a pull-request job.
  - - 2026-11-04 — PR #627 (merge d933a396b): the live home hit Postgres 53300 (connection slots exhausted) three times on 2026-11-03, failing update scheduler ticks and health samples. The pools could add up to more than 200 against the bundled Postgres default of 100; the streaming service's uncapped pools opened 28 each, the node's home pool was uncapped, and nothing leaked. The bundled Postgres now allows 300, streaming pools are capped with a one-minute idle close, the node's home pool is capped at 4, and the pipeline closes idle connections after 60 seconds.
  - - 2026-11-03 — PR #624 (merge 2f148c602): the race #616 fixed for toggles was also in removals (History Forget and Load more, ending a session, recent searches, Not interested, library pins on Apple and Android, and many web admin, queue and search pages): a read that began before the save brought the removed row or the old state back. Each platform now has one `ReadOrder` (web `lib/readOrder.ts`, MaktabaKit `ReadOrder.swift`, Android player `ReadOrder.kt`); a read's answer applies only if newer than every change and every answer shown, and saves run through `saving()`. Each fix has a latch test that failed without it; two flaky tests were fixed at the cause (iPad quality reopen, Android posture layout).
  - - 2026-11-04 — PR #630 (merge 5f70c51ed): two intermittent integration failures fixed at their cause. The cluster agent's metrics-file test now waits until the lines it checks have been written instead of sleeping a fixed time a loaded runner could overrun. Real-Postgres tests get ports from `internal/testport` (below the outgoing-connection range, each held by a lock file), and a bundled Postgres that exits while starting now puts its own last output in the error.
  - - 2026-11-04 — PR #629 (merge 7ee89e433): every YouTube source now has an on/off switch; turning it on restores the mode it last had (`last_on_mode`, migration 0175). Each library card on Manage libraries shows its YouTube state, can look up a channel from its own files, and flags a likely duplicate library. An admin action (`youtube_tartube` stage) imports every library's sources from Tartube's records, all off; it is idempotent, never creates a library, and reports Tartube channels with no library yet. All 55 live sources had been seeded `off`, and 10 libraries had none because the MediaIntel cache had no channel for their folders.

- 2026-11-04 — PR #631 (merge 3eba2e4ad): A single artifacts-API timeout failed an iPad Player UI shard on #626 before any test ran. Every artifact download in `.github/workflows` now retries twice with a short wait and, if all three attempts fail, stops with an error naming the artifact; a test in `tools/ci/tests` keeps every download in that shape.
  - every Go job's module download in `.github/workflows` now tries up to three times with short waits, and when all three fail it stops with an error naming the module. Jobs that a pull request runs then read the Go module cache only; tests in `tools/ci/tests` keep every download in that shape.

- 2026-11-04 — PR #634 (merge d2f3346cf): each library's YouTube sources are now derived from its linked videos (real playlists that no other library dominates, else its main channel), created off and never changing an existing mode; a channel or playlist already held by another library becomes a duplicate hint instead of a second source. It runs after each new link and in the reconciler every 6 hours, whose first pass after the update covers existing links, and every decision is kept in `youtube_source_decisions` (slot 0182, with `youtube_channel_libraries` as the many-to-many channel map). A read-only dry run on live gave the 10 sourceless libraries 7 playlist sources, 1 channel source and 2 duplicate hints.

- 2026-11-04 — PR #626 (merge 53cef37b3): Android, Android TV, Apple and web test data now use an invented home ("Maktaba Home"), documentation-range addresses or `.local` names, and an invented media path. maktaba.dev now shows the Android start screens, the full Android Remote shot and an Android TV sign-in. Processes in the iPhone Remote, the Android Remote and the web now show the home's English sentence when a detail code arrives without its values, never a raw `{percent}`. The Android Remote says the clips count in Arabic from a plural resource, and its bar label is «انتباه». The Apple Player now keeps a paused video paused when its quality changes.
  - a Player accessibility-audit failure on an iPhone shard was traced to the simulator registering XCTest's per-tap touch device twice, which split the touch-down from its lift and cancelled the touch before the app could act. The audit now opens each Profile page through a Debug launch option instead of a row tap, and the row taps keep their own tests.
  - no CI script or workflow step pipes into `head` under pipefail any more; `tools/ci/tests/test_no_head_pipes.py` keeps it that way.

- 2026-11-04 — PR #628 (merge f010aa7b1): On a home with 3.3 million queue jobs, the library dashboard and the queue snapshot rescanned the whole job history on every request and timed out. Slot 0183 adds `processing_done_subjects`, a trigger-maintained summary of finished subjects that locks rows in key order, plus partial indexes, and a shared stale-while-revalidate cache with one build in flight serves the snapshot and the library rows. On 3.15 million seeded jobs both now build in about 0.1 s and are served from cache in under 1 ms, and the dashboard shows slow and failed states with Try again instead of loading forever.

- 2026-11-03 — PR #608 (merge cdffe7913): Epic 51, configurable components, all 19 stories in one integration PR (story branches `e51/*` merged into `claude/epic51-configurable-components-b0f723` by the coordinator). One catalog (`shared/components/catalog.toml`, copied to the pipeline by `make catalog-check`; every stage sits in exactly one component). Desired state lives in Postgres with SQLite twins at migrations 0178 `components`, 0179 `component_override_seed`, 0180 `component_stage_gate` and 0181 `component_licence`, plus SchemaRev 181. Existing homes are seeded from today's switches. One gate rule, `runner.GatesAt`, is shared by the API's capabilities, the supervisor and the pipeline's `stages.py`. The `[components] reconcile = off` kill switch makes runners report but act on nothing. Packs ship with each server release under its signature. Apps hide or explain what is off (51.11).
  - Migration slots were renumbered twice: once for main's 0173 (`write_channels_logo_on_filler`), and once for #611's 0174 (`second_listen_stage`). Only unshipped files moved (`git mv`). The new `second_listen` stage joined the transcription component.
  - Bug found during the merge: an asset (`runtime.ml`, models) that the seed writes as `installed` was gated off, which turned transcription off on a seeded home. Now an asset is on unless `absent`. Test: `TestGatesInstalledAssetIsMet`.
  - The 20 failing checks were each fixed at their cause (84531b3b4):
    - **Lint:** revive, staticcheck and shellcheck findings.
    - **Update metrics:** the new `packs` journal step was missing from them.
    - **Capabilities coalesce window:** it is now an option, and the test uses 50 ms.
    - **LISTEN tests:** the pq and pgx tests shared one database and terminated each other's listener. Each now tags its connection with `application_name` and terminates only its own.
    - **compat-prev:** v0.3.69's worker-group test can't know Epic 51's newer tables. It gets a test-only patch, `tools/compat-prev/testfixes/v0.3.69/workergroup-newer-tables.patch`.
    - **Update-rehearsal scenario 13:** the release after the rollback now carries both kept migrations, rev+1 and rev+2.
    - **N9 bundle size:** main's mDNS announcer (`sh` running `dns-sd`, a supervisor child) was counted as a fifth service. `bundle_size.process_tree` now lists a supervisor child whose subtree runs a responder as a helper, and its memory still counts.
    - **Cloud billing test:** it steps down past every newer migration instead of `Down(1)`.
    - **CodeQL, 25 high path-injection and zip-slip alerts in packs:**
      - pack and revision folder names are reduced to one path element (`filepath.Base(filepath.Clean("/"+s))`);
      - the routes refuse ids and import paths containing `..`, and import paths must be absolute;
      - offline-file names and release versions containing `..` are refused.
    - **Player UI:** the 51.11 change broke launching straight into Live TV while it is off. `CapabilitiesStore.answered` now rebuilds the shell once, at the first answer. The open-screen test leaves the player before tapping Home. The iPad guide test uses 40 channels, because 16 overflowed the screen by about 40 pt.
  - During story 51.06, a test rig contacted the live API. It was root-fixed with a regression test, so tests never reach the live home.
  - N7 was redefined to measure the core home. N9 now counts services, not helpers.
  - A workspace hook refused writes to worktrees an agent created itself, which blocked some story agents. Agents launched with an isolated worktree worked in their own directory instead.
  - Before the merge: live re-run fixes and the red CI at a31a579e8 (pushed b5f876d23):
    - Missing packs: the packs runner installs an asset with no desired row (the catalog default, enabled), honouring offline mode; the install route writes the row again (component.install, new generation) so Install and Retry in the drawer act (06460a7de, merged fb038f8f9).
    - One requirements rule for GET and preview: an asset whose status row says Installed false makes a switchable component wait (`requirement_missing:<asset>`), also with no process of its own (transcription with workers = 0); a preview's "from" is the actual phase (30b1ad243).
    - Reason texts name components by title, also from params ({component}, {components: [...]}) (77e556eae, ce87fe746); the drawer's Close is localised (4a84c2e81); a process whose component is off reads held, not backoff (3997fbf5f).
    - CI root causes:
      - every update rehearsal and the auto-update e2e rolled back on "ollama is stopped" / role_not_running: the dormant process of an off component counted as a role that should run; the preflight and the home's watch now skip a blocked process with no pid (b5f876d23).
      - replica: a node restarted in a home-database outage never served, because its API dialled the components listener on the startup path (lib/pq bounds only the dial); Start now dials off the caller's path and waits at most ConnectTimeout. The runner restarted a loop in a tight loop when its parent context ended before Close (8d68c83ab).
      - the catalog copy check asserted byte equality, and pytest's untruncated diff under CI=true took over a second (soft cap); TestCrashBackoffAndCrashLoop read the restart count before the sixth crash was counted (351c6f7f1).
      - the packs job's shellcheck needed -x for the recipes' source= directive; SC2015 in runtime.ollama (409e18842).
    - Left as is: the CLI import records the OS user, as every CLI component write does (story 51.08).
    - 2026-11-04 (pushed 9bf413503): the design system's close button follows the page's language through a LabelsProvider given by I18nProvider (37e5ae7ee); the four unnamed process roles have English and Arabic on web, Apple and Android (45ac64dbb); components-live T13 on b5f876d23 caught the pipeline runner reporting phases before its LISTEN was up, so a change in that gap waited for the 30 s poll: it listens first (9bf413503).
    - 2026-11-04 (pushed f598e474d): CI at 9bf413503 caught a data race in the packs tests (a no-row pack now installs beside a removal, and both wrote the manager's log): logf locks, the test marks the other packs absent and waits for the removal. Rehearsal 12 expected a row for every component, but its 0.3.78 install runs the stand-in pipeline wheel with no ML runtime, so the seed rightly leaves runtime.ml with no row: the test allows that for an asset an enabled component needs and that is not on disk.
    - 2026-11-04: scratch Postgres: pg_ctl's own -p is the postgres binary path; the port and -k '' go in -o "-p N -k ''".
    - 2026-11-04 (pushed 4e3796887, the head #608 merged at): CI at f598e474d had three real failures, each fixed at its cause.
      - Rehearsal 12 read `wanted=true` for the dormant process of an off component. The supervisor's snapshot now reports an always-on (not on-demand) process held by its component as not wanted. The internal want is unchanged, so it starts once the gate opens. An on-demand held process keeps Wanted, because TestSlotGateClosed needs it (e34c2a529).
      - The Mac app's .dmg build failed on `hdiutil: create failed - Resource busy`. `tauri-build.sh` tries again up to HDIUTIL_TRIES=3 times, for that error only, with a test using a fake pnpm (ce307cddf). The bundle-size budget was not raised.
      - The Player UI's shimmer contrast audit measured a moving gradient. The gallery's `-maktaba-gallery-still` argument pauses the shimmer, the same as Reduce Motion, so the audit measures a still light (4e3796887).

- 2026-11-04 — PR #635 (merge 2fa5cacfa): Epic 51 follow-ups from the live run at 9bf413503.
  - Three agent branches were merged into `claude/epic51-followups`: followup-web (c508afb81), followup-packs (7572594d3) and followup-phases (9efc49744).
  - An open component drawer re-reads its detail when an install finishes.
  - First boot on a fresh home: the packs runner waits for the desired-state seed. It tries a busy install again every 2 s. An absent asset never reads failed. Before this, `model.refine` read failed or busy, and Health showed Degraded for about 16 minutes.
  - Offline mode refuses an install: `POST /api/components/{id}/install` returns 409 `packs_disabled` before it writes anything, and the CLI refuses the same way. The supervisor answers whether packs are on through a token-protected loopback route.
  - Health counts off and held on purpose as healthy: a held process reads `process_held`, and the components line leaves out a switchable part that is off or held.
  - Arabic for the health summary, the tagcheck section, process instance names (web, Apple, Android), the 59 stage titles (tested against `graph.py`) and offline import errors, which are now stored as a code.
  - The bundled model server reads `dependency_llm_off` while the language model is off. Its jobs stay parked. An outside model server that is down still reads down (706148279).
  - Each of a role's processes is its own health line, with id `role-instance` (fd7889bda).
  - Arabic stage-title tests for the library board and LibraryAdmin (9d9a5a6e4).
  - The test-budget gate was met at its cause, with no cap raised and no test split (348e466ee).
  - The components handler fixtures are built once per test binary and copied for each test: TestMachineRoutesAdminOnly went from 0.63 s to about 0.2 s.
  - TestResults_StablePaging writes one passage per video in a single transaction, with the two indexes it reads: 0.40 s to 0.12 s.
  - TestCrashBackoffAndCrashLoop (#645) was also fixed on main by #634 (243f9d678, merged d2f3346cf). At the merge (f7404d3c6), main's version was kept and this branch's (6244dc472) was dropped.
  - components-live T14 failed on four heads in a row (744dbc9b9, 348e466ee, 6244dc472, f7404d3c6) but never locally.
  - Cause: the new stale-part rule in `settle` relabels a part at an older generation with a closed gate's phase. It fired before any part had acted. Analytics has one part, the API's loops, so the component read off while its row still said running, then draining.
  - A first fix, skipping a draining part (b469e4db5), was not enough. The relabel came before the part had reported at all.
  - The root fix (899b0ee2d): the rule applies only once another part of the component has reported at the new generation (the supervisor stopped the process). A part that is alone, or draining, keeps its word until it reports. The unit test fails on the old code.
  - Main was merged three times (744dbc9b9, f7404d3c6, 225a4b1e5). The conflicts were in runner_test.go (main's version kept) and in LibraryAdmin.tsx and useLibraryBoard.ts (both sides kept: main's loading and slow state, and this branch's titled stages). The branch adds no migration: 0182 is #634's and 0183 is #628's.
  - Left as is: the TestYtDlp_* tests take about 1.0 s each under -race locally, near the 1.2 s hard cap.

- 2026-11-04 — releases: v0.3.79 at cdffe7913 (#608), v0.3.80 at d2f3346cf (#634) and v0.3.81 at f010aa7b1 (#628), each tagged once main's CI was green and the update rehearsal had passed on that exact commit.

- 2026-11-04 — issues filed for problems that neither failed CI nor blocked a release: #636 (a restart for loop-only components), #637 (the pipeline picking up a new Arabic dictionary or ffmpeg pack without a restart), #638 (a Desktop Player build switch for the capabilities gate), #639 (the simulator tap race), #640 (outbound calls from test stacks), #641 (a whole-server components-live scenario), #642 (`processing_done_subjects` lock order across a multi-statement transaction), #643 (the MANIFEST gaps at migration slots 0176 and 0177), #644 (a streaming test that fails on a test database URL without `sslmode=disable`), #646 (one unexplained local components-live failure with no output kept) #647 (finishing the early-exit pipe sweep: a `head` in testflight.yml and `grep -q`) and #649 (Player UI shards left in progress after their run completed). #645 (TestCrashBackoffAndCrashLoop) was closed once #634 fixed it on main.

- 2026-11-04 — PR #648 (merge d6d648c41): the specs for Epics 50, 52, 53 and 54 now build on Epic 51 as built (#608, #635), with its real component ids, routes and migration slots. New stories: 50.35 (remote access is on when any remote path is on), 52.16 (word review and the language packs in the component catalog), 52.17 (a component a later release adds starts as its bundle says), 53.18 (Epic 53's parts in the catalog), and 54.37 and 54.38 (plugin entries in the catalog, and plugin files). Epic 54's packs, manifest, runner and layout stories use Epic 51's signed components index, and broadcasting is gated by the `broadcast` licence flag (51.19). Epic 51 holds slots 0178–0181, #634 holds 0182 and #628 holds 0183, so the next free slot is 0184. Every changed entry has its Arabic, and `generate_wiki.py --check` reports 1363 of 1363 entries.

- 2026-11-04 — PR #632 (merge 8178f6d7a): CI now also runs on a pull request's branch head when the pull request conflicts with its base, through `ci-branch.yml` calling `ci.yml` with path filters measured from the merge-base, so a branch is tested even while main has moved on. A mergeable pull request still gets only its `pull_request` run, and the required `ci-success` check and the tag guard read only `ci.yml`'s own runs. Two Player UI shards that stayed in progress after their run had completed were filed as #649.

- 2026-11-05 — PR #651 (merge 3d7507d30): Epic 56 spec, the TV library view. Android TV and Apple TV get Plex's five library tabs (Recommended, Library, Collections, Playlists, Categories), Play all and Shuffle on a library, series, season, collection and playlist, series grouping with series and season pages, sort, filter, letter jump and Up next. Everything plays through one play queue on the server (56.01), which shuffles by a seeded hash so a whole library can be shuffled, can be continued on another device, and replaces the web's per-tab queue. New automatic collections come from YouTube playlists, YouTube channels and Plex collections; collections stay admin-made and viewers get smart playlists. Slots 0184–0186 are planned and taken at merge time (Epic 52 also names 0184). 14 stories, each with English and Arabic strings and tests on both TV platforms. Rejected: a queue kept in each app (it can't shuffle past one page) and user-made collections (they would duplicate playlists); importing Plex playlists was left out because they belong to Plex users who may not exist in Maktaba.

- 2026-11-05 — PR #652 (merge 51b149bb0): Epic 55 spec, Desktop Player parity. The Windows and Linux Player still ships with every tag but had stopped at Epic 42.4 (sign-in, a library browser and playback). The spec has a 42-row gap inventory against the Android and Apple Players, the plan for sharing `:client`, the JavaFX approach to subtitles and audio tracks, and 10 stories; 55.02 specifies the capabilities-gate switch from #638. The scope changed from building to spec only partway through, so the build agents were stopped and their unpushed scaffold was dropped. #651 merged first and made #652 conflict in the wiki index and generated wiki files; main was merged in, both rows kept and the wiki regenerated (`--check`: 1389 of 1389 entries).
