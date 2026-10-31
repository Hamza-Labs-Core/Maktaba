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

## 8. PC sessions

The Windows PC has its own Claude sessions; their transcripts are on the PC, not here. To add
them, a PC session reads this file on main, traces its own Maktaba sessions
(`~/.claude/projects/*aktaba*/*.jsonl`) and appends entries below through a PR. Add only, one
dated entry per decision or failure, real PR numbers only.

_(none yet)_

---

## 9. Session log

Append here: date, session, one to five lines on what changed. Newest last.

- 2026-10-31 · docs-claude-trail: created this file, the `CLAUDE.md` rule and the memory
  pointer.

[#2]: https://github.com/Hamza-Labs-Core/Maktaba/pull/2
[#41]: https://github.com/Hamza-Labs-Core/Maktaba/pull/41
[#47]: https://github.com/Hamza-Labs-Core/Maktaba/pull/47
[#54]: https://github.com/Hamza-Labs-Core/Maktaba/pull/54
[#56]: https://github.com/Hamza-Labs-Core/Maktaba/pull/56
[#58]: https://github.com/Hamza-Labs-Core/Maktaba/pull/58
[#61]: https://github.com/Hamza-Labs-Core/Maktaba/pull/61
[#97]: https://github.com/Hamza-Labs-Core/Maktaba/pull/97
[#104]: https://github.com/Hamza-Labs-Core/Maktaba/pull/104
[#108]: https://github.com/Hamza-Labs-Core/Maktaba/pull/108
[#113]: https://github.com/Hamza-Labs-Core/Maktaba/pull/113
[#119]: https://github.com/Hamza-Labs-Core/Maktaba/pull/119
[#138]: https://github.com/Hamza-Labs-Core/Maktaba/pull/138
[#139]: https://github.com/Hamza-Labs-Core/Maktaba/pull/139
[#148]: https://github.com/Hamza-Labs-Core/Maktaba/pull/148
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
[#295]: https://github.com/Hamza-Labs-Core/Maktaba/pull/295
[#304]: https://github.com/Hamza-Labs-Core/Maktaba/pull/304
[#305]: https://github.com/Hamza-Labs-Core/Maktaba/pull/305
[#306]: https://github.com/Hamza-Labs-Core/Maktaba/pull/306
[#309]: https://github.com/Hamza-Labs-Core/Maktaba/pull/309
[#310]: https://github.com/Hamza-Labs-Core/Maktaba/pull/310
[#313]: https://github.com/Hamza-Labs-Core/Maktaba/pull/313
[#314]: https://github.com/Hamza-Labs-Core/Maktaba/pull/314
[#319]: https://github.com/Hamza-Labs-Core/Maktaba/pull/319
[#321]: https://github.com/Hamza-Labs-Core/Maktaba/pull/321
[#325]: https://github.com/Hamza-Labs-Core/Maktaba/pull/325
[#327]: https://github.com/Hamza-Labs-Core/Maktaba/pull/327
[#328]: https://github.com/Hamza-Labs-Core/Maktaba/pull/328
[#329]: https://github.com/Hamza-Labs-Core/Maktaba/pull/329
[#331]: https://github.com/Hamza-Labs-Core/Maktaba/pull/331
[#334]: https://github.com/Hamza-Labs-Core/Maktaba/pull/334
[#340]: https://github.com/Hamza-Labs-Core/Maktaba/pull/340
[#342]: https://github.com/Hamza-Labs-Core/Maktaba/pull/342
[#343]: https://github.com/Hamza-Labs-Core/Maktaba/pull/343
[#344]: https://github.com/Hamza-Labs-Core/Maktaba/pull/344
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
[#373]: https://github.com/Hamza-Labs-Core/Maktaba/pull/373
[#375]: https://github.com/Hamza-Labs-Core/Maktaba/pull/375
[#388]: https://github.com/Hamza-Labs-Core/Maktaba/pull/388
[#389]: https://github.com/Hamza-Labs-Core/Maktaba/pull/389
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
