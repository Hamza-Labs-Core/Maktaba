---
schema: 1
kind: pr
pr: 752
title: What #752's CI taught after its event was written
date: 2026-11-07
session: c8daad15-7bc0-4b14-a10c-8b559dcb37d3
prs: [752, 748]
---

## What happened
- #752's first runs failed three ways, none of them in the feature: the outbound-hosts guard, the affected-tests gate, and the update rehearsal's scenario 12. All were fixed in the PR, and it merged green.

## What we learned
- Any `https://host` string in the servers' Go code (a CSP included) trips `shared/go/ports` `TestOutboundHostsAreListed` (#640) in lint, unit and integration at once. A host only the browser reads goes in `notCalled` with its reason; one a server calls goes in `ports.Outbound` with the switch that turns it off.
- The affected-tests gate wants one of a changed file's **mapped** tests changed. A new test file isn't in the map yet, so for each MISSING line, extend a test the gate names (Android's `HomeStatus.kt` maps to the client module's tests, not core's).
- A Kotlin backticked test name can't contain a period ("story 50.11" fails to compile). Put the story in a KDoc above it.
- A rehearsal failure about loops or components the PR never touched usually means main moved (here #748, for v0.3.86's loops and the `lang.*` components). Merge origin/main and push before digging.

## Open
- Nothing new; #752's own event lists what is open.
