---
schema: 1
kind: pr
pr: 1121
title: "Epic 59 story 59.08 follow-up: which forwarded client addresses the home believes"
date: 2026-11-11
session: dea9a844-6ac4-4597-9dbd-99393fec0390
---

## What happened
- An independent security review of #1119's client-address trust (at cc9df187e) sent five
  findings. #1121 (merged at 7d96532ec) fixes each, with tests.
  1. **Medium: approved nodes were trusted by `cluster_nodes.remote_addr`.** That address is
     written once at join and never refreshed. Three ways it went wrong:
     - a reused DHCP address could forge X-Forwarded-For;
     - a carrier NAT was trusted whole;
     - a node approved through a proxy made that proxy trusted for everyone.

     Now:
     - homeproxy sends the client it resolved in `X-Maktaba-Forwarded-Client`.
       `X-Maktaba-Forwarded-Client-Sig` is an HMAC with the node's delegation key over that
       address and the request's own signature, so it is bound to this one request. The
       existing signed string is unchanged, so homes and nodes of either version still verify
       each other.
     - `delegation.Accept` checks it and puts the client in the context
       (`clientip.WithAttested`), and the router uses it ahead of every header. On the
       node's authenticated cluster link the header is believed without the HMAC: HTTPLink
       goes over the link whenever it is connected.
     - api/trusted_nodes.go and the join-time address are gone. A revoked or deleted node is
       refused on its next request.
  2. **Low: node trust needs no setting and cannot be turned off.** The en+ar docs say so plainly,
     and that an empty trusted_proxies still believes signed node clients.
  3. **Low: Docker network ranges and `/0`.** A Docker network's range holds its gateway, where
     docker-proxy traffic appears with client-written headers. The docs point to the proxy
     container's single address. `/0` is refused in trusted_proxies (config, settings, the
     environment variable, the Settings form in en+ar).
  4. **Low: streaming's loopback-only checks.** They (frontdoor, slot server, the slot-exited
     report, the offline station) and `middleware.FromLoopback` read `clientip.Peer`, so an
     all-trusted chain of `127.0.0.1` hops cannot pass as this machine.
  5. **Low: zoned IPv6 hops.** A zoned IPv6 address in X-Forwarded-For keeps its address with the
     zone stripped, instead of collapsing onto the proxy's key.
- The review's test gap is closed: a node revoked or deleted between two requests loses trust
  on the next one. The old test used a `removed` state that does not exist; the real states
  are `revoked` or the row deleted.
- Merging main into it after #1117, #1118 and #1119 conflicted in CHANGELOG.md and
  CHANGELOG.ar.md. The two entries were edited on both sides, which merge-main.sh hands back
  on purpose. It also conflicted in release.yml and tests/nas/cells.json, where main's was
  taken. `git diff origin/main HEAD --name-only` listing only #1121's own files proved nothing
  else was lost.
- The `load-small` relay comparison failed once, with memory per tunnel at 310 KB against
  255 KB. Throughput, stalls and errors were identical, and the only relay-side change is a
  startup parse, so it passed on its one re-run.

## What we learned
- Trust what a peer forwards only through something it proves on each request. An address
  recorded once proves nothing later.
- A signature added beside an existing one, bound to it, extends a protocol without breaking
  either side's older version.
- After a merge, comparing the branch to main by file names is a quick check that a
  resolution kept the other side whole.

## Keep doing
- A security review that blocks nothing still gets its findings fixed at once, in a follow-up
  pull request with a test per finding.

## Stop doing
- Treating a machine's join-time address as its identity.

## Open
- None from the review.
