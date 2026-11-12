---
schema: 1
kind: pr
pr: 1204
title: "Epic 57: parental controls, built from the reworked spec as one pull request"
date: 2026-11-12
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
agent: claude-code
prs: [1128, 1130, 1139]
---

## What happened
- #1204 merged as 7ebffe1e4: every story of Epic 57 except 57.16 (which needs Apple's Family
  Controls entitlement). The home: age tiers with one writer, restricted accounts and guardians,
  one content filter behind a route registry, the device's age signal, Ask a parent and the PIN,
  activity, live TV, time limits counted by the home, tiers detected from transcripts, off by
  default in an existing home, migrations 0220 to 0225. Clients: web (family settings, time
  rules, review), iPhone and iPad, Apple TV, Android phone and TV, the desktop Player.
- Built by one agent per story in its own worktree, merged one at a time into an integration
  branch, then reviewed whole in six areas. The review found one critical and nine high
  defects after every story had passed its own tests: a pipeline-collection page with no
  filter, live TV not held to time rules or to the device's limit after tuning, detection that
  rated a film from the windows the model did answer, a duplicate merge that dropped an admin's
  rating and block rules, a lifted restriction that streaming went on refusing.
- The bypass rehearsal had missed the unfiltered route: it filled a path parameter it had no
  fixture for with `x`, got "not found", and counted the route as asked. It now walks every
  mounted content route with a real fixture and fails on one it cannot probe.
- The branch was not pushed until the build was over. By then main had moved 153 commits and
  had new rules the branch had never met (the problem type registry, stricter lint), and the
  first CI run failed 16 checks. Two of the failures were defects no local run could show:
  - With the home's database down, the gate refused `/api/cluster/emergency` (503), the route
    for that moment; and a second machine's API was never given the gate's cache, so it
    refused every signed-in user. Routes that must answer with the database down now read no
    policy, and a node filters from its own copy.
  - A rollback past slot 0225 restored the whole database: the slot updated an existing
    setting, which the updater counts as losing data. Slot 0221 no longer seeds the key and
    0225 inserts it, so its Down is a delete.
- Two budgets failed and neither cause was where it first looked:
  - Integration ran 5m04 against 5m00. Epic 57's tests were spread thin; the cost was the
    scratch database copy and tests queueing. A cheaper template copy where fsync is off and
    113 parental tests run side by side took the job to 2m33.
  - Idle memory read 711 MB against 700, with the API 37 MB over main. It was not Epic 57: the
    API's idle memory has two levels on main too, set by whether GitHub answers the update
    check (two loops each decoded the 6.9 MB release list whole). The main run compared against
    had been rate-limited. Both loops now read one release at a time.
- A layout shift of the website's navigation, on a different page each run, was a browser
  painting a half-parsed header (the nav drawn before the language switch after it was
  parsed), not fonts. Every page now carries `<link rel="expect" blocking="render">` for the
  shell's last element; with the race forced, 55 of 60 runs failed before and 0 of 60 after.

## What we learned
- A gate in front of every route must say, per route, what it needs to answer. A route that
  returns no content and must work when the database cannot be read gets no policy read at
  all; a guard beside the registry lists those routes so the next one is not forgotten.
- A second process that mounts the same router (a node) needs every dependency the first one
  got. A test that walks each mount and checks the gate has its cache now holds this.
- A fallback written to fix "refuses everyone when the policy cannot be read" is itself a
  place to fail open: the note that an account had no policy has to be saved only after a
  fresh read with an unchanged cache generation, and a ping must never resume a suspended
  cache. The security review of the fix caught both before merge.
- A migration that changes a row another slot inserted cannot be rolled back without a
  restore. Insert the row in the slot that owns its value.
- On CI's older simulator OS a SwiftUI `Label` row is read by its symbol's name, and an
  off-screen row of a lazy `Form` is in the tree only as an empty element carrying the
  identifier. Make the row one element labelled by its words, and scroll a control into view
  before reading it. Neither shows on a newer local simulator.
- A test helper that advances a fake clock to the next scheduled task turns every wait into a
  fast loop of the code's own timers (1,249 progress posts in one test under load). Run what
  is due and never move the clock while waiting; a player released while the main looper is
  being idled can keep it from ever going idle.
- macOS keeps memory Go has freed in a process's resident size, so releasing does not lower
  an idle-memory reading; only not allocating does.
- CodeQL does not follow a response's content type through a helper method: set it in the
  method that writes.

## Keep doing
- Review the whole branch by area after the stories pass their own tests, with each finding
  as file, line, scenario and fix, and a test per fix.
- When a budget fails, compare the same measurement on several runs of main before blaming
  the branch.
- Reproduce a CI-only failure by forcing the suspected race (a held response, a loaded CPU, a
  split document) before changing code, and keep the forcing test.
- Give a stalled agent a strict order and a time bound, and ask for an interim report so the
  part that is done can be merged.

## Stop doing
- Holding a long branch back from CI until it is finished. Open the one pull request as soon
  as the integration branch has real work, merge main in often, and keep it green.
- Reading a single green run of main as the baseline for a number that has two levels.
- Naming a cause from a log without reproducing it: three guesses in this work were wrong
  (the Live TV section for a crash that was Picture in Picture, a double reopen for a failed
  fake stream, a wrapper element for a lazy row).

## Open
- 57.16, the home's time rules through Screen Time, waits for the Family Controls
  entitlement.
- The Declared Age Range capability on the Player App IDs, and the checks on a real child
  device (the rating limit, Downtime); steps are in `docs/testflight.md`.
- Guardian screens on Android and the desktop Player; Notifications and the iPad split view
  on iPhone.
- The policy cache's listener holds one more Postgres connection at idle, also on a home with
  no restricted account.
- Restricted Home measured over the story's 60 ms bound; the Home row cache is bypassed for
  restricted accounts.
- `testNotInterestedRemovesTheCard` and `testAlsoSendToMaktabaWhenTheHomeAllowsIt` fail now
  and then on CI's simulators, on main too; their cause is not established.
- CI's simulator device pick will be refused under Xcode 27 (noted in `docs/testflight.md`).
