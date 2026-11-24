---
schema: 1
kind: pr
pr: 1575
title: "The app checks' scratch Postgres servers take free ports below the ephemeral range"
date: 2026-11-24
session: e1c12b82-6431-40f4-b0ae-96e43fdfa26e
agent: claude-code
---

## What happened
- `mac-app / Bundle size (Core payload)` failed on #1566 (run 45308282401). No size was
  over budget. Two of pg18-check.sh's checks failed:
  - "the PostgreSQL 16 cluster to upgrade: could not create listen socket for 127.0.0.1";
  - "relation upgrade_probe does not exist", which follows from the first.

  #1566 touched no payload code. Among the last 40 failed CI runs, only that one showed it,
  and main's runs passed the job.
- The script drew `port=$((55000 + RANDOM % 900))` and used port to port+3 without checking
  them. mac-app/check.sh and linux-app/check.sh drew 54000-54899 the same way.
- tools/release/free-ports.sh now gives N consecutive ports from 20000-29999 that nothing
  listens on, probed with bash's /dev/tcp. The three scripts use it. free-ports-test.sh runs
  in node-packaging, with shellcheck.

## What we learned
- 54000-55999 is inside every runner OS's ephemeral range: Linux from 32768, macOS and
  Windows from 49152. An outgoing connection's own end, the script's psql sessions included,
  can hold such a port.
- Assigning RANDOM seeds bash's generator, but a `$( )` subshell reseeds it. A test that
  seeds RANDOM and then calls a function in `$( )` draws differently each time. The first
  version of the test passed by chance. The helper now takes FREE_PORTS_FROM for an exact
  start.

## Keep doing
- Read a failing release check's own lines (pg18-check prints each FAIL) before blaming the
  change under test. Then look for the same pattern in the sibling scripts.

## Stop doing
- Picking a scratch server's port at random inside the ephemeral range without probing it.
