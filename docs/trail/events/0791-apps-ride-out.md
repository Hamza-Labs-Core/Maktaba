---
schema: 1
kind: pr
pr: 791
title: The apps ride out a Maktaba Cloud outage
date: 2026-11-07
session: c8daad15-7bc0-4b14-a10c-8b559dcb37d3
prs: [752, 780]
---

## What happened
- Story 50.13: `connections` in `GET /v1/homes` (the relay by the plan's tier). Every app keeps a saved home list per account, shown with its time while the cloud can't be reached, removed at sign-out. A cloud sign-in the cloud can't answer shows the saved list and hands over to the home's own sign-in. `503 home_reconnecting` is ridden out for 60 s with a status-strip line, in the home's web app, MaktabaKit and the Android client. maktaba.dev's Apps overview says what the apps do then.

## What we learned
- Today's Free tier still has the relay, so "a Free home lists no relay" depends on 50.19. The view follows the tier (`billing.RelayIncluded`, `RelayQoS == "none"`), so no home loses remote access before plans launch.
- Apple and Android keep the cloud's token in memory only. After a restart there is no cloud session, so a saved list keyed by the account id can't be found then. Key it by the email the person types, and use it when that sign-in can't reach the cloud.
- Run the affected-tests gate locally before pushing: commit, then `cd tools/affected-tests && go run . gate -C ../.. --range origin/main...HEAD`. Its map ties a source file to the tests that ran it (`CloudScreens.kt` to the player's `SignInKeyboardTest`, `plans.go` to `plans_test.go`), and a new test file is in no map yet.
- Kotlin test files may hold more than one class: appending at the file's end puts tests in the last class (a private helper), not the suite. Insert before the suite's own closing brace.
- The relay's errors are `{"error": ...}`, the home's problems carry `code`. A client that classifies by `code` must read `error` too for the relay's reasons.

## Open
- T09 (the `apps-cloud-down` rehearsal scenario) needs a home stack in the cloud rehearsal; T08 runs in the client libraries meanwhile. T11's UI shards with the cloud mocked down: not added.
- 50.14, 50.15, 50.19, 50.30 to 50.35, 53.16 to 53.18; 50.11's leftovers: the Apple and Android re-check after a failed request, and the Android TV strip.
