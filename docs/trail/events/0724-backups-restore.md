---
schema: 1
kind: pr
pr: 724
title: Backups to two repositories and point-in-time restore, rehearsed by destroying both nodes
date: 2026-11-07
session: c8daad15-7bc0-4b14-a10c-8b559dcb37d3
prs: [722]
---

## What happened
- Story 50.09: pgBackRest to Hetzner Object Storage and Cloudflare R2 (encrypted, 30 days of PITR), a leader-only backup, a restore script, the witness's weekly drill, backup alerts with a promtool test, and the disaster runbook. The rehearsal destroys both nodes and restores exactly the rows up to T from either repository.

## What we learned
- A pgBackRest `backup` writes to one repository per run (it says "repo option not specified, defaulting to repo1"): with two repositories the job must loop over `--repo=1` and `--repo=2`. `archive-push` writes all of them at once.
- A database restored from a Patroni node does not start elsewhere as is: Patroni's `postgresql.conf` names the old node's `hba_file`; the restore passes its own.
- MinIO's container images are gone from Docker Hub and quay. SeaweedFS's S3 gateway is a working stand-in for pgBackRest (with `-s3.cert.file` for the TLS pgBackRest requires), but a bucket created while its gateway is still warming up accepts writes and serves no reads: wait for the gateway and prove a round trip first.
- A restore drill's freshness check needs a write that happens on a quiet day: the relay directory's 5 s heartbeat works; the audit log does not.
