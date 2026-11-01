# Claude trail

The one file a new Claude session reads to carry on the owner's Maktaba work. It holds the
owner's standing rules, the decisions and why they were made, what broke and how it was fixed,
the live state, what is in flight and what waits on the owner.

**How to use it**

- Read it all before you start. `CLAUDE.md` at the repo root says the same.
- Before your session ends, add what you decided, what failed, and what changed in the live
  state. Add only: never rewrite another session's entry. If something is no longer true, add
  a dated line under it saying so.
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
| **PR flow runs to the merge without asking:** implement → test → CI green → review it yourself → fix → CI green → merge with a merge commit → move live. Never ask "merge?". Ask only for real product decisions (costly backfill, deleting data).                                                                                | 2026-10-19             | "why tell you, process is, implement, test, checks pass, review, fix, checks pass, merge".                                                                                           |
| **Claude reviews PRs, not Copilot.** The `copilot-pull-request-reviewer` check and its "quota" comment are noise: say so in one line and move on.                                                                                                                                                                                 | 2026-10-18             | "You review and stop asking copilot". Copilot comes from the author's personal GitHub setting and runs out of quota.                                                                 |
| **Update, rollback, restore and migration paths are proven in CI first.** No release that touches update / restore / migration / supervisor / launcher / TV-staging code reaches the live Mac unless `update-rehearsal` passed on that exact commit. The tag guard enforces it ([docs/update-rehearsal.md](update-rehearsal.md)). | 2026-10-28             | "THIS NEED TO BE ON CI", after two outages in one day (see §6). "when ppl use this how are you going to fix it for them": recovery must be automatic or one click, never a terminal. |
| **One release at a time.** No stacking fixes on the live install in one day.                                                                                                                                                                                                                                                      | 2026-10-28             | v0.3.39 → v0.3.42 shipped in one day, each fixing the last; two outages.                                                                                                             |
| **Arabic ships with English, on every platform, in the same PR.** Web `ar.json`, Android `values-ar`, Apple String Catalog `ar`. Reuse the web's wording. Keep a test that fails when an English string has no Arabic. Check RTL.                                                                                                 | 2026-10-31             | "Why no Arabic, it should go hand in hand" / "All platforms", after the Remote status screens shipped English-only.                                                                  |
| **One Arabic word for the home:** «الجهاز الرئيسي» (replaces «الخادم الرئيسي»).                                                                                                                                                                                                                                                   | 2026-10-31             | Same wording in every app.                                                                                                                                                           |
| **The API enforces every role rule**, with a role test (try the route as no one and as a viewer). Hiding UI is not a rule. A modified app with a viewer's token stays a viewer; cloud-linked sign-ins are play-scoped unless a home admin grants manage.                                                                          | 2026-10-25             | "Remember the apis need to enforce the user roles". See `TestAdminOnly` in `api/internal/handlers/cluster/cluster_test.go`.                                                          |
| **Only admins can join a machine.**                                                                                                                                                                                                                                                                                               | 2026-10-25             | Owner, while separating viewers, home admins and the Maktaba service admin ([#310]).                                                                                                 |
| **Secrets are never printed or committed:** Plex token, DB password, signing keys, API keys. Credentials are typed by the owner only. Test accounts are used through the API (`login.sh` reads the Keychain unseen); never type passwords into sign-in forms.                                                                     | standing               | Security. [#328] removed the DB password from every URL a process gets.                                                                                                              |
| **Live data only for justified recovery.** Tests never touch the live DB: use scratch Postgres clusters. Read-only checks are fine. Don't change Plex DVRs (use Add to Plex). Never call live Cloudflare, Stripe, DNS or the VPS without the owner's OK. Don't bypass the admin API to create users.                              | standing               | Outages and the classifier's denials taught this.                                                                                                                                    |
| **Fix flaky tests at the root.** Never only re-run, never skip a test.                                                                                                                                                                                                                                                            | 2026-10-26             | "wtf skipping tests? fix them".                                                                                                                                                      |
| **Throwaway simulators.** iOS sim tests create a device (`xcrun simctl create`), run, and delete it. Never reboot the shared "iPhone 17". Stop any `simctl log stream` you start.                                                                                                                                                 | 2026-10-31             | SpringBoard crashed 19 times in a day and every crash put a dialog in front of the owner.                                                                                            |
| **Screenshots in the dark theme** unless showing a light-only issue.                                                                                                                                                                                                                                                              | 2026-10-28             | Owner preference.                                                                                                                                                                    |
| **Plain, short answers.** Explain without jargon.                                                                                                                                                                                                                                                                                 | standing               | "don't understand" came often.                                                                                                                                                       |
| **Verify yourself; don't assume. Don't ask, find the issue and fix.** No restart workarounds unless macOS needs one (Full Disk Access, Local Network).                                                                                                                                                                            | 2026-10-23 → 25        | "why the you don't verify?", "no stupid restart solutions".                                                                                                                          |
| **Testing features on the live Mac is fine while in dev**, but say first that an update restarts the server and watch it through its whole confirm window. Update paths themselves: CI only (rule above).                                                                                                                         | 2026-10-27             | "live test as we are in dev".                                                                                                                                                        |
| **Big multi-track work:** parallel worktree agents, keep going without check-ins, escalate only when a plan's premise turns out false.                                                                                                                                                                                            | 2026-05                | Gap-closure program.                                                                                                                                                                 |
| **Worktrees live under `.claude/worktrees/`, never `$TMPDIR`** (macOS purges it).                                                                                                                                                                                                                                                 | 2026-10-18             | A purged worktree broke a live venv.                                                                                                                                                 |
| **Never use a made-up sha** in a command or doc.                                                                                                                                                                                                                                                                                  | 2026-10-27             | A fabricated sha was used in a release command once; caught and killed.                                                                                                              |
| **Clean up what's done:** archive finished sessions, remove merged worktrees and branches (keep a branch only if it has work not in main).                                                                                                                                                                                        | 2026-10-17, 2026-10-20 | "if the pr is closed why keep the branch?"                                                                                                                                           |

---

## 2. Architecture decisions and why

### Product shape (2026-10-25, owner's words)

- Phone, foldable and tablet apps on Android and iOS: three **Player** and three **Remote**.
  Desktop Player and desktop Server. TV Player. Web is part of the server. A cloud web service
  on the VPS acts as a Remote and a Player for a connected home. Plus the **Maktaba service
  admin**, separate from home admins. Spec: [#305], `specs/epics/42-apps-player-remote-server/`.
- Apps are **native** (SwiftUI, Kotlin/Compose), not web wrappers: [#306], [#321], [#325], [#342].
- **Linked accounts** for remote play through Maktaba Cloud ([#309], [#327], [#388]). Rejected:
  "a video link that plays without sign-in" (owner: "then don't do that idea", 2026-10-25).
- Player builds are viewer-only, like the TV apps (2026-10-25).

### Live install: Maktaba.app with bundled Postgres (2026-10-20 → 23)

- Owner: "all need to be part of one mac installation … postgres bundled if small". Epic 36
  ([#148]); the home Mac moved in with `migrate-from-dev` ([#158]) on 2026-10-23. The Docker
  Postgres was stopped, not deleted ("don't finalize").
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
  a long-term archive on the home. The DB is only a cache. Owner rejected a central scheduler:
  "all is split into queue, each machine will have jobs pulling from the queues on main".
- Full nodes keep a live copy of the home's DB ([#178]); per-machine client certificates
  ([#349]).
- **Machines update only after the home commits, never ahead of it** ([#434], 2026-10-31).

### Updates (Epic 35, then hardening)

- The cluster updates itself ([#271], [#287]).
- **D1, 2026-10-27:** no click in the desktop app. Updates started from the Updates page install
  everywhere; automatic ones in the window with a notice ([#389]).
- **Backups never block an update:** taken on the side, complete (vectors kept) and fast
  ([#397]). Owner: "backup should happen on the side … faster than 10 mins".
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
- Plex: Maktaba emulates an HDHomeRun tuner + XMLTV, "like dizqueTV" ([#61], [#139], [#159],
  [#214]). Auto-detection by Plex was never solved; Add to Plex adds it as a channel source.

### Transcription and search

- Whole library transcribed on the queue engine with batched MLX Whisper ([#54]).
- Model choice (2026-10-19): turbo + a second listen with Cohere Arabic, merged word by word
  ([#113]). Why: the owner's own listening test on 20 disputed spots scored Cohere 17, turbo 0;
  an LLM judge had favoured turbo's tidy spelling and was wrong. Turbo kept for Quran.
- Search moved from Chroma to Postgres pgvector ([#319], 2026-10-26). Owner: "nothing better and
  more efficient? … just migrate". Chroma broke whenever a worker was killed mid-write.

### Queues

- Change-driven multi-level queue engine ([#47]). Everything is a queue job, one item at a time,
  run in parallel by workers. Owner, 2026-10-17: "there is something called splitting work to
  jobs".
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
  release at a time exist. Owner: "why do you keep rolling back changes and fucking things up?"
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
6. Ship it as its own docs PR against main, with a CHANGELOG line under [Unreleased].

Traced on 2026-10-31 from the PC. Its Maktaba transcripts sit under `~/.claude/projects/E--tv`
and `E--tv-dizquetv` (the checkouts live in `E:\tv`), not `*aktaba*`. Left out: `c58dc8c2` (an
iperf question), `56a527d9` (another repo) and two empty transcripts. The 362 subagent
transcripts were not traced one by one; their work is in the PRs cited.

### 2026-10-14 · PC · 89e92223 · dizqueTV on the PC, then mediaintel and Maktaba TV

- Asked: Plex Live TV from the YouTube archive. dizqueTV 1.7.0 was built from source on the PC
  (port 8002, tuned by Plex on the NAS as an HDHomeRun), which is why Maktaba TV emulates an
  HDHomeRun with XMLTV. The owner then said "create tools for the work, don't do manually": that
  became mediaintel ([#46], [#48]), then Maktaba's queues ([#47]), channel rules ([#52], [#55])
  and Maktaba TV ([#57], which took in [#55]).
- Owner rules set here: "this should all be baseed on config with ui for that config" (rules are
  settings, not code); "verify the claim" (a guess at failing NAS disks was wrong: SMB timeouts
  from 32 parallel reads); no placeholder features (a stub audit followed: [#123], [#128],
  [#130]–[#136]). Claude's own working rules from it: never change security settings (the owner
  added the Defender exclusion himself); never use `git stash` (a shared stash swapped two
  worktrees); tests bind 127.0.0.1 and build output is never committed (`api.exe` landed in
  [#119]; [#127]).
- Decided: "windows will join mac and not the other way around". The PC's own home was retired
  and the PC joined `Mahmouds-Mac-Studio` as node `windows-pc` on 2026-10-19; [#75] then let its
  storage check accept files of unknown size, so the worker took jobs. Paths are canonical
  `smb://` ([#81], [#101], [#107]), and nodes reach Plex through the home and never hold its
  token ([#73], [#78]). The owner also asked for TV hot swap ([#97], [#104], [#108]) and
  automatic updates.
- PC facts: 192.168.50.24, RTX 2080 SUPER. `O:` = `\\Home-NAS\Originals`, `Y:` =
  `\\Home-NAS\Originals\Backup\Youtube`. Splunk holds 8000 and 8088 and MiniTool ShadowMaker
  8080, so setup picks a free port from 8780 (v0.1.0, `6b88722f`). The mediaintel data and a
  hand-over README for the Mac are in `Y:\.maktaba`.
- Ended with the Plex discovery fix ([#139]: relative SSDP location, no UDP 65001, a device ID
  that failed its check digit) and v0.2.0 ([#138]) waiting on [#141], which the classifier
  would not merge without a human review.

### 2026-10-20 · PC · 798feb8d · v0.2.0 and the first node PRs

- Merged [#141] and, on the owner's "Why this one? Merge it", [#138]; tagged v0.2.0 on
  `e539c7af` ("Release tag no need for rc").
- Four of the five release workflows failed (as on v0.1.0): opened [#142] for the icons, the
  Android SDK and the relay deploy.
- Ran the PC as a full node from source: opened [#143] so its API and pipeline reach the home's
  database.
- The owner asked why the whole site goes blank when the node can't reach its home "instead of
  working and showing popup and maybne all actions disabled": that became [#145] (later folded
  into [#178]).

### 2026-10-20 · PC · f43fad60 · "Maktaba worktree setup", the PC's coordinating session

- 2026-10-20 → 10-31. Asked: keep the PC's node and dizqueTV running, and review, fix and merge
  every PR from this side. It ran the side sessions below (node copy, Windows and Linux apps,
  updates), merged about 70 PRs and tagged about 17 releases between v0.3.0-rc.1 and v0.3.33
  (the Mac tagged the rest). On 10-31 it traced these PC sessions.
- Owner rules set here (for §1): "review all prs from this side, then fix and merge" (a merge
  commit on the exact reviewed head, `--match-head-commit` with the full sha, every check green);
  "why you keep creating prs, fix and merge" (fold findings and new CI failures into PRs already
  open); "why are you not polling it???" (wait on CI yourself, merge on green); "tag every …
  merge" (the next patch tag at once, after main's `ci.yml` passes on the merge commit, pushed
  with the owner's `gh` so the release builds start; since 10-31, not while §3–§5 hold a
  release); "this is not a manual step, its a feature of maktaba".
- PC setup: the node and dizqueTV start from sign-in scheduled tasks (logon +30 s), because `O:`
  and `Y:` exist only in the signed-in session. On 2026-10-25 the owner chose "Install the
  Windows app instead": the node moved from the hand-built copy in `E:\tv\maktaba-local` to
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
  in Claude's private registry copy. Handed to `c17dd289`, where the owner then answered both
  open questions.

### 2026-10-20 · PC · ce7a4c40 · A node that loses its home, then its database copy (Epic 37)

- Asked: fix [#145]'s review findings. The owner's outage rules: "no sign in when in outage,
  videos should play if you have path, jobs should pause". Playback with the home's database
  down became [#157].
- Then the owner changed the design: every joined node keeps the home's whole database,
  read-only, "so it can function as read only"; sign-in works offline; "all in a feature branch
  with everything and merge once". [#145] and [#157] closed into [#178].
- Why logical replication: Postgres can't stream physically from an arm64 Mac to an x86 PC, and
  a physical copy would carry every secret and session id. One slot per node; the copy pauses
  while the home's schema is ahead; secrets and other machines' rows stay out. Pinned Postgres
  went into [#196] (`postgres.exe` needs a restricted, non-admin token).
- Also built: switching the home, Plex and Cloudflare included ("credentials through the
  encrypted handoff, never the replica"). Owner decisions: standby homes only, late writes kept
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

- Stopped at once by the owner; the work went on in `ce7a4c40`.

### 2026-10-21 · PC · 295d4839 · Windows one-app install (Epic 38)

- Asked (relayed): "also need the same one app setup on windows similar to mac". About 30 PRs,
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

- Asked (relayed): "and another for linux". Spec [#166] and the story PRs, merged by
  `f43fad60`.
- Why: the AppImage is the main download, with .deb and .rpm from the same build, all updated by
  the Tauri updater; no apt/dnf repo (a second updater and trust root, hosting, root for every
  update). The server runs from a per-user runtime copy with a `current` link and rollback
  ([#174]), as a systemd user service with linger ([#173]).
- Owner decision "Use the distro's ffmpeg": the pinned Linux ffmpeg builds were
  `--enable-nonfree`, so not redistributable. Linux bundles none and the packages depend on
  ffmpeg ([#229]); the approved mirror ([#220]) was closed unpublished.
- Lessons: curl's `--retry` doesn't retry a Cloudflare 521 (an ffmpeg host outage, [#216]);
  `find | head` under pipefail exits 141; a plain CI re-run reuses the old merge ref, so merge
  main and push instead; rpm takes no `-` in a version (v0.3.0-rc.1's Linux legs, [#258]).
- Side fixes: one version order ([#218], [#226]); VOD on NVIDIA used a nonexistent
  `-hwaccel nvenc` ([#232]); VOD shares the GPU with TV without starving the hot standby
  ([#237]); VOD playlists list every segment ([#244]).

### 2026-10-23 · PC · 9bd8e01c · Automatic updates (Epic 35) and what broke live

- Asked: "shouldn't it update automaticly? what?", then "build it". [#271] (v0.3.0): one update
  path per install kind, the home's run drives the nodes, and each machine's journal finishes a
  run whichever release starts next.
- Owner decisions: D1 "Ask first" (desktop apps stage the home's order and install on a click;
  reversed to no click on 2026-10-27, [#389]); D2 a rollback restores the pre-update dump; D3 the
  Linux app updates itself without root. The order file `updates/app-install.json` goes ordered
  → staged → go → installing; on Windows the app the server starts must break away from the
  launcher's kill-on-close job.
- "it tried to update and failed and it didn't say why": every attempt shows its reason and
  links the logs ([#276]). The rest of the spec was built by ten parallel worktree agents
  ([#287]).
- Live failures fixed: the Mac moved its Postgres without the node's role, so a node now gets
  its access back by itself ([#276], [#307]: "it should just try to rejoin"); Mac notarization
  broke on empty `APPLE_*` variables ([#276]); "remove the stupid continue with mac", the tray at
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
- Owner decisions, 2026-10-31: "Agent installs (Recommended)": the always-running
  `Maktaba.exe --server-agent` installs a node's update itself instead of waking the desktop
  app (changes §2, Updates). "Yes, open it": a PR so the node's copy notices a dead link to the
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
  after the owner found the Players thin ("is that epic really DONE???"). Epic 42 asked only for
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
[#462]: https://github.com/Hamza-Labs-Core/Maktaba/pull/462
[#470]: https://github.com/Hamza-Labs-Core/Maktaba/pull/470
[#468]: https://github.com/Hamza-Labs-Core/Maktaba/pull/468
