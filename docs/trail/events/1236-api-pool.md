---
schema: 1
kind: pr
pr: 1236
title: "Epic 50 story 50.17: a stateless api pool"
date: 2026-11-14
session: 42aa4d02-9b26-4dcc-a143-7b17e1c9df84
agent: claude-code
---

## What happened
- #1236 merged as 661848a1a. The api and worker can run on several nodes behind a load
  balancer:
  - the worker's jobs run only on the node holding `pg_try_advisory_lock(8472613)`
    (`cloud/internal/db/leader.go`, heartbeat in `cloud_leaders`, a 60 s stale holder ended by
    another node; `worker_leader{node}` and the `worker_no_leader` alert);
  - the brute-force limits are shared in `cloud_rate_buckets` (`ratelimit.PG`);
  - `cloud/internal/cache.Bus` is one LISTEN connection per node, now under the relay's home
    cache;
  - `server.drain_delay` turns `/readyz` to 503 before the listeners close;
  - `database.direct_url` carries the sessions PgBouncer can't hold;
  - `setup.sh --role api` and `bin/api-rollout` deploy the pool.
- Migration slot 00500015 (`cloud_rate_buckets`, `cloud_leaders`).
- The new rehearsal scenario `api-pool` (`compose-pool.yml`, HAProxy standing in for LB11)
  passed locally and in CI:
  - the lock moved 6.5 s after its holder was killed;
  - the 31st sign-in across three nodes got 429;
  - both relays dropped a changed home in 5 ms;
  - a rolling deploy failed 0 of 32,163 requests, each node out of the balancer 6–9 s after
    its SIGTERM.
- One CI round-trip: revive's unused-parameter rule in a new test.

## What we learned
- The api role was already stateless apart from the worker and the limiter: account status and
  abuse checks are read from the database on every request, and 50.31 had shipped the trusted
  client IP. Only the relay keeps a cache.
- 50.31 replaced the story's TCP plus Proxy Protocol plan: the pool's balancer is HTTP on the
  private network, behind cloudflared on two of the nodes, with `trusted_proxies` set to its
  address.
- A `*sql.Conn` that holds a session lock must never go back to the pool: `discard` returns
  `driver.ErrBadConn` from `Raw` so closing it ends the session, and with it the lock.
- With burst 30 and a refill of 5 a second, "31 sign-ins in a minute" is only refused when the
  attempts are fast. The tests send malformed sign-ins (counted before any password hash) and
  assert the first refusal comes right after the shared burst, not after 90.
- Two compose services sharing one network (`network_mode: service:api-N`) model a node's api
  and worker: killing the worker alone kills the lock holder, and a recreate replaces both.

## Keep doing
- Run a new rehearsal scenario locally against Docker before pushing; it passed first time in
  CI.
- Lint the integration and rehearsal build tags locally: CI's golangci-lint covers the default
  tags only.

## Open
- Owner actions before the pool serves: create LB11 (HTTP services on 8090 and 8091,
  `/readyz` every 5 s, 2 failures out, least connections) and three api machines, run
  `setup.sh --role api`, list the nodes in `/etc/prometheus/targets/api-nodes.json`, point the
  Tunnel's ingress at the balancer. Nothing is deployed (CLOUD_HA unset).
- 50.18 sets `database.direct_url` once PgBouncer fronts the database.
- Pre-existing revive findings under the integration and cloudrehearsal tags
  (`harness_integration_test.go` receiver names, `scenarios_test.go` `max`) are untouched.
