---
schema: 1
kind: pr
pr: 686
title: After the merge of #686 — the log index on one host with two machines, and on homes seeded earlier
date: 2026-11-06
session: ad829843-879e-4c35-b9bb-b62a3b316df1
---

## What happened
- The two-machine e2e tests run a home and a node on one host. Both started a log index on the registry's loopback port 9105, the node's failed to bind, and its update watch rolled back. Each machine's `server.toml` in those tests now names its own `[logs] addr`, as the rehearsal harness already did.
- Update rehearsal 12 ("no behaviour change") required every process and loop new with a release to be held and not wanted. `logs.index` is new and on in Full, so the rule became 52.17's: a component a release adds may run when the home's bundle has it.
- #686 waited for #672 (story 52.17's new-id seed) to merge first. Without that step a Core home that updated would run the log index too, because no desired row reads as on.

## What we learned
- A new process with a fixed loopback port breaks every test that runs two machines on one host. Grep the e2e tests for their `server.toml` templates when adding a port.
- A rule in a rehearsal that says "an update changes nothing that runs" needs the catalog's new-component policy, not a blanket ban.
