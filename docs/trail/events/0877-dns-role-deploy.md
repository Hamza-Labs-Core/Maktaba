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

- The role this PR deploys couldn't start yet: `config.Validate` had no case for `dns`, so `serve --role dns` exited 2 with "unknown role". The unit tests never started the role. The `direct-remote` rehearsal (branch `claude/epic50-15-direct-rehearsal`) found it the first time it ran the real binary, and that branch has the fix. Until it merges, `dns-update` would put the old binary back and stop the deploy, which is why `DIRECT_DNS` stays `no`.
- The same rehearsal found that `runWorker` listens on 127.0.0.1:9090 twice: once in a goroutine, and again in `server.Run`. So the worker has exited 1 at start since commit c15649f76 (June), and none of its jobs ever ran. The cloud isn't live. The fix goes in that branch's PR.
- A coordination message for the store delegate went to the owner's session. I took the recipient from a thread-search snippet ("Resume Prior Task") without checking whose session it was. Confirm a session's identity, for example from a message it sent, before relying on it.

## Keep doing
- Run each new role or binary for real once (a rehearsal, or a container) before its deploy ships. Unit tests of the parts didn't show that the process itself couldn't start.
- Put a deploy's per-node logic in a script with env overrides (`dns-update`: RELEASES, BIN, SETTLE). Then `scripts-test.sh` can test it with a stubbed `systemctl`, without SSH.

## Stop doing
- Pushing without `git diff --name-only origin/main...HEAD`. Run it before every push, and delete local build outputs after switching branches.

## Open
- 50.15 part 3: the `direct-remote` rehearsal (T06, T10, T12), and the apps' raw-IP LAN entry pinned by home id with `direct.rebinding`.
