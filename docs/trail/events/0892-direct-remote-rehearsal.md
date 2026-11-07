---
schema: 1
kind: pr
pr: 892
title: The direct-remote rehearsal passes, and the two bugs that kept direct access from ever running
date: 2026-11-07
session: c0d50e69-00c4-47e2-9b33-1ac69218ae34
prs: [877, 882]
---

## What happened
- The `direct-remote` scenario (story 50.15 T06, T10, T12) was started by the previous Epic 50 session, handed over unfinished at the usage limit, and finished here. It passes locally in 131 s and joins the cloud rehearsal's matrix.
- It found two bugs. Each alone meant direct access could never have worked in any deployment:
  - **`config.Validate` refused `--role dns` as unknown,** so the name server, which the CAs validate DNS-01 against, never started. #877 deployed its unit (off by default) before this fix. Fixed here, with `TestDNSRoleStaysUpAndAnswers`, which runs the role in its own process.
  - **The worker listened on its control port twice** and exited 1 at start since c15649f76 (June), so no certificate was ever issued and no outside check ever ran. Fixed in #882, with a child-process test; merged into this branch.
- **The rehearsal's own trap:** Docker's masquerade rule for the wan network (`-s 198.51.100.0/24 ! -o br-wan -j MASQUERADE`) also rewrote the fake router's forwarded connections as they entered the LAN bridge.
  - Homes saw each one from the Docker host (192.168.77.1) and answered it directly. The router's conntrack entries stayed `UNREPLIED`, and the outside check timed out.
  - Found by reading the home's `/proc/net/nf_conntrack` beside the router's.
  - The wan network now has `enable_ip_masquerade: "false"`.
- An open question from the handoff (the agent logging its relay as `198.51.100.0`) was the shared log scrubber folding IPs to their /24, not a wrong address.

## What we learned
- Both bugs were in how a role starts, not in its jobs. Every job had unit tests, and no test started the role's process. A role that never starts passes every unit test.
- A rehearsal that builds real NAT on Docker bridges has to switch off Docker's own NAT on the network that plays the internet, or the host rewrites the traffic under test.
- For a forwarded connection that times out, compare conntrack on both ends: the router's `UNREPLIED` and the home's peer address showed the problem in one read.

## Keep doing
- Start each role and binary for real in a test (`startRole` in `cloud/cmd/maktaba-cloud`) before its deploy ships.
- When a scenario is handed over unfinished, list its known bugs in the handoff with the files; this one's list made the worker fix a direct start.

## Open
- Story 50.06's scenario table says "UPnP mapping"; the fake router speaks NAT-PMP (the home speaks both).
- Nothing installs or updates `/usr/local/bin/maktaba-cloud` on the nodes, which the api, worker and dns units run; next.
