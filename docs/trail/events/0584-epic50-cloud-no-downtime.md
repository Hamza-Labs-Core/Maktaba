---
schema: 1
kind: decision
pr: 584
title: Epic 50 spec: Maktaba Cloud with no downtime
date: 2026-11-03
session: unknown
source: claude-trail.md
---

## What happened
- Merge 7aa1af48. Phase 1 is two Hetzner nodes (FSN1/NBG1) plus a HEL1 witness: Patroni with etcd on all three, the Floating IP moved by Patroni's role-change callback, synchronous replication, blue-green deploys with a DRAIN frame and resume tickets, pgBackRest backups. Phase 2 is a relay pool with rendezvous-hash assignment and a tunnel directory.
- Rejected: keepalived (two nodes cannot break a tie), pg_auto_failover (its monitor is a single point of failure), routing at Hetzner's load balancer (it cannot), per-home DNS records (slow to fail over).
- Hetzner's 2026 price rises put Phase 1 at about EUR 79 a month.

## Open
- Six owner questions in README section 13 (answered in #588).
