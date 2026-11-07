---
schema: 1
kind: pr
pr: 895
title: Deploys never installed the binary the api and worker run
date: 2026-11-07
session: c0d50e69-00c4-47e2-9b33-1ac69218ae34
prs: [877, 882, 892]
---

## What happened
- `maktaba-cloud-api.service` and `-worker.service` run `/usr/local/bin/maktaba-cloud`, and nothing installed it. `bin/bluegreen` installs the relay's colours, `bin/dns-update` the dns role's copy (#877), and `setup.sh` neither. On a real pair the api and worker would have failed at every leader start, and would never have run a release.
- The previous Epic 50 session found it while reading the units and handed it over unfixed. It is the third start-up gap on the same day, after the worker's double listen (#882) and the dns role refused by `config.Validate` (#892).
- `bin/app-update` installs the release there, keeping the old binary. On the leader it restarts the running api and worker and rolls back if one doesn't stay up; on the standby it only installs. It runs as `deploy.sh` step 6, and `scripts-test.sh` covers it.

## What we learned
- Every unit file names a binary, and each binary needs a step that puts it there. Reading the units against the deploy steps found this in minutes, and no test would have: the rehearsal's nodes have the image's binaries built in.
- The api and worker restart rather than going through story 50.08's blue/green, so the api is down for about a second per deploy until that is built.

## Keep doing
- For each systemd unit added, check which script installs its `ExecStart` binary and keeps it updated.

## Open
- The api and worker in 50.08's blue/green (the worker in the colour that holds the worker lock).
