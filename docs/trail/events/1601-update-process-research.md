---
schema: 1
kind: pr
pr: 1601
title: "Research: a separate update process, every updater issue, and how other software updates"
date: 2026-11-24
session: 12b9467a-110c-44ce-b564-d2529e4b33e2
agent: claude-code
---

## What happened
- After the update failures of 2026-11-14, -23 and -24 (d20261123-cic-lock-timeout,
  d20261124-stale-catalog-step-down), the update process was researched.
- The result is `docs/research/update-process.md`, with no code change:
  - about 80 issues, each with its evidence, severity and status;
  - how 20-odd other systems update, from primary sources;
  - a recommended design and a ranked list of changes.
- The research ran on four agents, two over the web and two over the code:
  1. desktop and OS updaters;
  2. media servers and NAS;
  3. database migration practice;
  4. each install kind's update path.

## What we learned
- **From the database step on, the release being installed judges and rolls itself back.**
  - On the Mac app the launcher ships inside the swapped bundle, so after the swap no old code
    watches at all.
  - On layout installs the old launcher's guard remains, but the new release pushes its deadline
    without a cap (`machine.go:105`, `layout/guard.go:198`).
- **Epic 35's design had the old supervisor migrate before the switch, with the old API serving
  (§5.3 step 9).** The build moved the migrations to the new release's start, with the library
  stopped.
- **The rehearsal on a copy is skipped above `rehearse_max_db_gb` (20).** The 35 GB live home has
  never had one, and its scratch copy has no walsenders, so it could not have caught the
  incidents.
- **`maktaba-server update` on a layout install is never confirmed.** It activates with run id
  `cli`, and the server confirms only a switch with no run id, so the launcher rolls it back after
  15 minutes (`cmd_update.go:192`, `serve_launcher.go:162`).
- **After the launcher's guard fires, the old release's start always puts the whole dump back.**
  #1598 does not change that path (`serve_appbundle.go:1041`).
- **Homes before #1592 still cannot see releases.** On 2026-11-24 the anonymous
  `/releases?per_page=30` was 10,474,679 bytes: 4,964 assets, 1,320 of them `lang-hunspell-*`
  packs. Without the packs it would be about 7.7 MB, under the old 8 MiB cap.
- **Every other mature updater keeps an old, trusted judge apart from the release it installs.**
  Home Assistant's Supervisor puts the image back but not the data, and its users report the old
  version failing on the newer data.
- **The live home's data volume is APFS**, so PostgreSQL 18's `file_copy_method = clone` could
  make the rehearsal copy and the safety point in seconds.

## Keep doing
- Ask, for each update step, which release's code runs it before trusting a fix to reach homes.
- Measure a growing response the way a home reads it (anonymous, the same `Accept` header) before
  sizing a cap.

## Open
- **Every issue is open**, apart from those marked fixed or in flight in the research's §2.
- **The small changes come first** (§6.3 items 1–5):
  - no automatic restore;
  - unstick the homes that cannot see releases;
  - the four small bugs P3, P4, P5 and P10;
  - hold automatic installs on a stale catalog;
  - a replication gate before the migrations.
- **E8 is unverified:** the machines' paused copies as the cause of the repeated re-copies. It
  needs a measurement on the live home.
- **Shrinking the release list for old homes** needs a decision, and first a check of how packs
  are found.
