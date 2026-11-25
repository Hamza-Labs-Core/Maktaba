---
schema: 1
kind: incident
title: "Deploying Maktaba Cloud to its QA node found four latent bugs in the single-VPS deploy, a claim that ignored the owner's plan, and two launch blockers (mail, the licence key)"
date: 2026-11-24
session: 570dd6e0-d082-437d-8351-4b31c383043f
agent: claude-code
prs: [1627, 1628]
---

## What happened
- First real deploy of the cloud: one node each for QA and production, both fresh
  Ubuntu 26.04, deployed by a new workflow, `cloud-deploy.yml` (#1628).
  Before merge it ran from scratch branches (`scratch/cloud-deploy-*`, a push trigger added there
  only), because a workflow_dispatch needs the file on main.
- **The shared Cloudflare token could not read or write DNS.** The owner made a DNS-only token
  (`CLOUDFLARE_DNS_TOKEN`) through the decisions inbox (`cloud-dns-token`). The deploy then wrote
  the four A records, and Caddy got a Let's Encrypt wildcard for `*.staging.relay.maktaba.dev`
  within a minute.
- **The single-VPS files had never deployed anything.** Four bugs, each fixed in #1628:
  - `deploy.sh` passed an empty `HEALTH_URL` to `ssh ... bash -s -- a b ""`. ssh joins its
    arguments into one string, so the empty one vanished and the remote script stopped on
    `$3: unbound variable`. Fix: quote with `printf %q`.
  - `docker compose run` (the migrations) read the rest of the remote script from bash's stdin.
    bash then hit EOF and ended at exit 0, and the run said "Deploy finished successfully" with
    only Postgres and Redis up. Fix: `-T </dev/null`, plus an end marker the local side requires.
  - No role was given `--config`, so the mounted `cloud.toml` was never read. The api ran with no
    operator listener, no production checks and no entitlement key, so home assertions were off.
  - Postgres took 172.30.77.2, Caddy's fixed address, so Caddy could not start. Fix: an
    `ip_range` in the upper half. Changing a compose network's IPAM needs a one-off
    `compose down`, done once on QA.
- **A claimed home started on Free whatever its owner's plan was** (`RedeemClaim` used the column
  default), so the relay answered 402 `relay_needs_plan`. Fixed in #1627. Until it ships, the
  operator tool sets the plan again after a claim.
- **The cloud cannot confirm a sign-up.** The only mail driver writes to the log, and the shared
  logger redacts both the address and `?token=`, so the link reaches nobody. The operator tool
  confirms an account by hand, audited as `cli`. Decision `email-setup` is open (it replaced
  `cloud-signup-mail`).
- **A released home trusts only licences signed with the key its release embeds**
  (`MAKTABA_LICENSE_PUBKEY`). Each new node generated its own key. Without a licence it trusts,
  the home turns off relayed access (403) and its link reporter, so the cloud never sees the
  link. The private half is in the operator's Keychain. Decision `cloud-license-key` is open; the
  deploy installs the key from `MAKTABA_LICENSE_SIGNING_KEY` once the secret exists.
- **Everything behind that blocker was then rehearsed on QA with a stand-in home**: the release's
  `maktaba-server` image with its embedded public key replaced, byte for byte, by QA's own (a PEM
  of the same length), and `files.sha256` and `payload.json` updated to match. From a scratch
  branch, without the record job, so it is no QA pass. Every check passed, and it found three more
  problems:
  - The e2e asked for the relay before the home had its licence (it asks once a minute): 403. A
    `licence` check now waits for it.
  - A session opened with no `client_profile` is `generic` (720p at 4 Mbit/s) and transcoded the
    test video; the e2e now names `desktop-player`.
  - A relay restart took 123 s: SIGTERM drained its tunnels for `relay.drain.deadline` (120 s),
    and one node has no second relay to move them to. With `relay.drain.enabled = false` the
    restart took 2 s and the home answered 4 s after it began.
- **Sizing**, 1 vCPU and 2 GB: 24 players, 238 Mbit/s relayed, 45 % CPU, 748 MB, no failed stream.
- **With broadcasting off the cloud's broadcast and moderation routes still answered** (401, 422,
  503; 200 for a signed-in account): `broadcast.enabled` shut only viewing. The e2e's
  `broadcast-off` check now requires 404 from each, so QA passes only a release with the fix.
- **Production's gate counted any branch's QA run** (a scratch run could have recorded a pass). It
  now counts main's dispatched runs only.

## What we learned
- A remote script fed on stdin needs an end marker. Exit 0 proves nothing when a command can
  swallow stdin.
- A deploy path that CI never ran end to end is not a deploy path. The QA e2e found every one of
  these bugs within minutes; the cloud rehearsal never runs `deploy/cloud-relay`'s files.
- `gh run watch` behind macOS's missing `timeout` returns at once: poll `gh run view` in a loop
  with a deadline instead.
- `docker compose run` of a service with a fixed `ipv4_address` fails with "Address already in
  use" while the service runs: use `exec`.
- A blocked step needn't hide the ones after it: a stand-in with a test key proved them, and found
  the restart outage, while the real key waits on a decision.

## Keep doing
- Prove the cloud end to end with a real released home (`maktaba-server`), not only simulated
  homes. Licensing, linking and plan gaps show only there.

## Open
- Decisions `cloud-license-key` and `email-setup`.
- Production deploy and the owner's home, after QA passes in full.
- No off-node backup copy yet (docs/cloud-single-node.md, "Backups").
