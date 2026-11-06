---
schema: 1
kind: migration
title: Standing rules from the old trail
date: 2026-10-31
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
prs: [453, 532, 516, 469, 310, 328]
---

## What happened
The first trail (#453, 2026-10-31) opened with a table of standing rules for every Claude
session, each with the date it was set and why. Later sessions added the trail-only PR rule
(#532, 2026-11-02) and the tests-with-code rule (#516, 2026-11-01). This event carries them over.

## Keep doing
- Run the PR flow to the merge without asking: implement, test, CI green, review it yourself, fix, CI green, merge with a merge commit, move live. Ask only for real product decisions (costly backfill, deleting data). Since 2026-10-19; asking before each merge slowed every PR.
- Review PRs yourself. The `copilot-pull-request-reviewer` check and its quota comment are noise (the author's personal GitHub setting, runs out of quota): say so in one line and move on. Since 2026-10-18.
- Prove update, rollback, restore and migration paths in CI first. No release touching update, restore, migration, supervisor, launcher or TV-staging code reaches the live Mac unless `update-rehearsal` passed on that exact commit; the tag guard enforces it (docs/update-rehearsal.md). Recovery must be automatic or one click for users, never a terminal. Since 2026-10-28, after two outages in one day.
- One release at a time on the live install. Since 2026-10-28: v0.3.39 to v0.3.42 shipped in one day, each fixing the last, with two outages.
- Ship Arabic with English on every platform in the same PR: web `ar.json`, Android `values-ar`, Apple String Catalog `ar`. Reuse the web's wording, keep a test that fails when an English string has no Arabic, check RTL. Since 2026-10-31, after the Remote status screens shipped English-only.
- Use one Arabic word for the home in every app: «الجهاز الرئيسي» (replaces «الخادم الرئيسي»). Since 2026-10-31.
- Enforce every role rule in the API, with a role test (try the route as no one and as a viewer). A modified app with a viewer's token stays a viewer; cloud-linked sign-ins are play-scoped unless a home admin grants manage. See `TestAdminOnly` in `api/internal/handlers/cluster/cluster_test.go`. Since 2026-10-25.
- Only admins can join a machine. Set 2026-10-25 while separating viewers, home admins and the Maktaba service admin (#310).
- Keep secrets out of output and commits: Plex token, DB password, signing keys, API keys. Only the owner types credentials. Use test accounts through the API (`login.sh` reads the Keychain unseen). #328 removed the DB password from every URL a process gets.
- Use live data only for justified recovery. Tests use scratch Postgres clusters; read-only checks are fine. Use Add to Plex rather than changing Plex DVRs. Ask the owner before calling live Cloudflare, Stripe, DNS or the VPS. Create users only through the admin API.
- Fix flaky tests at the root. Since 2026-10-26, after a failing test was skipped instead of fixed.
- Change a change's tests in the same push. The pre-push hook (`tools/affected-tests/install-hook.sh`) and CI's `affected-tests` job enforce it; the way out is a commit trailer `No-Test-Change: <reason>`. Since 2026-11-01 (#516), after #469 left the Mac UI tests stale.
- Use throwaway simulators: create a device with `xcrun simctl create`, run, delete it. Stop any `simctl log stream` you start. Since 2026-10-31: SpringBoard crashed 19 times in a day and each crash put a dialog on the Mac's screen.
- Take screenshots in the dark theme unless showing a light-only issue. Since 2026-10-28.
- Give plain, short answers without jargon; jargon-heavy answers were often not understood.
- Verify yourself. Find the issue and fix it rather than asking. Since 2026-10-23 to 2026-10-25; unverified claims and restart workarounds cost time.
- Testing features on the live Mac is fine while in development: say first that an update restarts the server and watch it through its whole confirm window. Update paths themselves are CI-only. Since 2026-10-27.
- For big multi-track work, use parallel worktree agents, keep going without check-ins, and escalate only when a plan's premise turns out false. Since 2026-05 (the gap-closure program).
- Keep worktrees under `.claude/worktrees/`. Since 2026-10-18: a purged `$TMPDIR` worktree broke a live venv.
- Clean up what is done: archive finished sessions, remove merged worktrees and branches; keep a branch only if it has work not in main. Since 2026-10-17 and 2026-10-20.
- Use only real PR numbers and shas, looked up with `gh pr view` / `git log`. Date every trail entry.
- Keep trail entries out of code PRs: the old trail took additions only in trail-only PRs, which run no CI job (2026-11-02, #532). Add, never rewrite another session's entry; mark what is no longer true with a dated line.

## Stop doing
- Restart workarounds, unless macOS needs one (Full Disk Access, Local Network).
- Skipping or only re-running a flaky test.
- Running Mac or simulator UI tests locally; the pre-push hook only compiles them.
- Rebooting the shared "iPhone 17" simulator.
- Typing passwords into sign-in forms.
- Bypassing the admin API to create users, or touching the live DB from tests.
- Worktrees under `$TMPDIR` (macOS purges it).
- Made-up shas in a command or doc. Since 2026-10-27: a fabricated sha was used in a release command once, caught and killed.
