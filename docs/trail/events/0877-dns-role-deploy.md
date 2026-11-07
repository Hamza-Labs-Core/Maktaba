---
schema: 1
kind: pr
pr: 877
title: The dns role on nodes A and B (story 50.15, part 3a)
date: 2026-11-07
session: c8daad15-7bc0-4b14-a10c-8b559dcb37d3
prs: [845, 871]
---

## What happened
- `maktaba-cloud-dns.service` runs the direct-name name server on A and B, always, with only `CAP_NET_BIND_SERVICE`.
- `setup.sh` opens 53/udp and 53/tcp on A and B. With `DIRECT_DNS=yes`, it also frees port 53 from systemd-resolved's stub and enables the unit.
- `deploy.sh` step 5 runs `bin/dns-update` on the standby, then on the leader. A release whose dns role doesn't stay up goes back to the old binary on that node and stops the deploy.

## What we learned
- On Ubuntu, systemd-resolved's stub holds `127.0.0.53:53`, so a server binding `:53` fails unless the stub is off.
- The api and worker units run `/usr/local/bin/maktaba-cloud`, which neither `deploy.sh` nor `bluegreen` installs. Only the relay colours get binaries. This was raised with the owner, not fixed here.
- A background Bash command defaults to a 30-minute limit, so a merge watcher started without `timeout` dies before a slow CI run finishes. #856's watcher was stopped that way. Start watchers with `timeout: 7200000`.

- A build output made on one branch can follow you to another, if that branch's `.gitignore` doesn't list it yet. `web/dist-admin` (ignored only from #871) got into this PR through `git add -A`, and CI's prettier check failed on it. It was removed in a follow-up commit.

## Keep doing
- Put a deploy's per-node logic in a script with env overrides (`dns-update`: RELEASES, BIN, SETTLE). Then `scripts-test.sh` can test it with a stubbed `systemctl`, without SSH.

## Stop doing
- Pushing without `git diff --name-only origin/main...HEAD`. Run it before every push, and delete local build outputs after switching branches.

## Open
- 50.15 part 3: the `direct-remote` rehearsal (T06, T10, T12), and the apps' raw-IP LAN entry pinned by home id with `direct.rebinding`.
