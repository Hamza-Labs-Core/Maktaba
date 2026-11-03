# Story 50.08 — Deploys with no downtime

> Epic 50 · Phase 1 · Depends on: 50.04, 50.07 · Needed by: 50.14, 50.16, 50.17

## Why

Today `deploy/cloud-relay/deploy.sh` runs `docker compose up -d --wait`, which replaces the
running container: every tunnel and stream drops, every home reconnects, and migrations run
right before the swap with no rule that the old binary can still use the new schema. On a
paid service a release must be invisible.

## Description

### 1. The order of a deploy

`deploy/cloud-ha/deploy.sh` (CI and a laptop run the same script):

1. **Standby first.** On the standby node: pull the image, start it once as a smoke test
   against the standby's own Postgres replica in read-only mode (`/healthz`), then stop it. A
   bad image fails here without touching users.
2. **Migrations, expand only.** On the leader, run `maktaba-cloud migrate up` in a throwaway
   container. Only **expand** migrations are allowed here (add a table, add a nullable column,
   add an index `CONCURRENTLY`). The running old binary keeps working on the new schema.
3. **Blue/green on the leader.** The leader runs one colour (say blue on :8443). Start green on
   :8444 with the new image; wait for `/readyz` and the 50.04 directory registration.
4. **Switch Caddy.** Through Caddy's admin API (`127.0.0.1:2019`), point the upstream at green.
   New requests and new tunnels go to green. Requests for homes still on blue are forwarded by
   green to blue (50.04).
5. **Drain blue.** `SIGTERM` to blue: it sends `DRAIN` to every home at 100 a second (50.03);
   homes reconnect to green with resume tickets; blue's streams finish; blue exits (≤ 150 s).
6. **Watch 10 minutes.** If, compared with the 10 minutes before the deploy, the 5xx rate on
   the relay or API rises above 1 % absolute, `/readyz` fails twice, or the canary (50.10)
   fails, roll back: start blue again with the old image, switch Caddy back, drain green.
7. **Record.** Write the deployed tag to `/opt/maktaba-cloud/deployed` on both nodes, so a
   failover starts the same version on the standby (the callback in 50.07 reads it).

The **api** and **worker** roles go through the same blue/green. The worker runs only in the
colour that holds the worker lock (50.17's advisory lock, used here already so blue and green
never both run jobs).

### 2. The expand/contract rule

- A release may only **expand** the schema. Anything that breaks the previous binary (drop or
  rename a column or table, `SET NOT NULL` on a column the old binary doesn't fill, change a
  type) is a **contract** migration, and ships in a later release, once no running binary uses
  the old shape.
- A contract migration's file carries a first line `-- contract: <what the previous release no
  longer uses>`.
- `tools/migration-lint` gets a cloud mode: it flags `DROP`, `RENAME`, `ALTER … TYPE` and
  `SET NOT NULL` in `cloud/migrations/` without the `-- contract:` marker.
- The rehearsal `migration-n-minus-1` (50.06) runs the **previous release's** binary against
  the **new** schema and runs its integration smoke tests.

### 3. CI

`.github/workflows/relay.yml`'s `deploy` job:

- needs the `cloud-rehearsal` verdict (50.06) green on the exact sha, like the update rehearsal
  gates release tags;
- reads `RELAY_HOST_A`, `RELAY_HOST_B`, `RELAY_SSH_USER`, `RELAY_SSH_KEY` from the
  `production` environment (replaces `RELAY_SSH_HOST`);
- asks Patroni (`patronictl list` over SSH) which node is leader, and deploys standby first,
  then leader;
- runs one deploy at a time (`concurrency: relay-deploy`).

## User-facing strings

None: a deploy is meant to show nothing. (A rollback that fails pages the owner; 50.12 covers
what users see during an incident.)

## Acceptance criteria

- **Given** 1,000 simulated homes and a client stream through each of 50 of them,
  **when** a new release is deployed,
  **then** no request fails, no stream stops, and every home ends on green.
- **Given** a release whose new binary fails `/readyz`,
  **when** it is deployed,
  **then** the deploy stops at the standby smoke test, or at green's readiness, and blue keeps
  serving untouched.
- **Given** a release that starts healthy but returns 5xx on 5 % of relay requests,
  **when** the 10-minute watch sees it,
  **then** blue is restarted with the old tag, Caddy points back to it, green drains, and the
  deploy job fails with the reason.
- **Given** a migration with `DROP COLUMN` and no `-- contract:` line,
  **when** the PR runs CI,
  **then** `migration-lint` fails it.
- **Given** the previous release's binary,
  **when** it runs against the schema after this release's migrations,
  **then** its smoke tests pass.
- **Given** the cloud rehearsal failed on this sha,
  **when** the deploy job starts,
  **then** it refuses to deploy.
- **Given** a deploy has finished,
  **when** the leader fails over to the standby,
  **then** the standby starts the newly deployed tag.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit (`bats`) | `deploy.sh` with fake SSH/Caddy/Patroni | normal deploy | order: standby smoke → migrate → green up → switch → drain |
| T02 | unit | `deploy.sh` | green not ready in 60 s | green stopped; Caddy untouched; exit 1 |
| T03 | unit (Go) | `migration-lint` cloud mode | DROP without marker / with marker | fail / pass |
| T04 | rehearsal (CI) | 50.06 `deploy-blue-green` | deploy N over N-1 under load | 0 failed requests, 0 stopped streams |
| T05 | rehearsal (CI) | `deploy-bad-release-rollback` | deploy an image that 5xx's 5 % | rolled back within the watch; 0 lasting impact |
| T06 | rehearsal (CI) | `migration-n-minus-1` | N-1 binary on N schema | smoke passes |
| T07 | rehearsal (CI) | `failover-kill-leader` after a deploy | kill leader | standby runs the new tag |

## Edge cases

- **A deploy during a failover.** The script reads the leader at start and again before the
  switch; if it changed, it stops and asks for a rerun (exit code `leader_changed`).
- **Old homes without `drain`.** They are closed at the drain deadline and reconnect by
  backoff (50.03); their requests in that gap are held (50.04).
- **Long downloads through the relay.** Cut at the 120 s drain deadline; the client resumes
  with a range request.
- **Caddy itself needs an update.** Done per node: on the standby, then a switchover (50.07),
  then on the old leader. Announced on the status page.

## Build plan

### Files to touch

| File | Change |
|---|---|
| `deploy/cloud-ha/deploy.sh` (new) | §1. |
| `deploy/cloud-ha/bin/caddy-switch` (new) | Points Caddy's upstream at a colour through the admin API, checks it took. |
| `deploy/cloud-ha/systemd/maktaba-cloud@.service` (new) | Templated per colour and role. |
| `.github/workflows/relay.yml` | Deploy job: two hosts, leader lookup, gate on `cloud-rehearsal`, concurrency. |
| `tools/migration-lint/main.go`, `tools/migration-lint/cloud.go` (new) | Cloud mode and the contract rule. |
| `cloud/migrations/README.md` | The expand/contract rule. |
| `Makefile` | `relay-deploy` uses `deploy/cloud-ha/deploy.sh` when `RELAY_HOST_B` is set. |
| `deploy/cloud-relay/deploy.sh` | Unchanged (single-VPS / dev). |

### Tests

- `bats` for the scripts, Go tests for the lint rule, in `relay.yml`.
- **CI only:** `deploy-blue-green`, `deploy-bad-release-rollback`, `migration-n-minus-1`.

### Rollout

1. Merge; the old single-VPS deploy keeps working while only `RELAY_SSH_HOST` is set.
2. Once the pair exists (50.07), the owner sets the new secrets; the next tag deploys with this
   script.
3. First production deploy watched end to end (canary, held requests, drain counts).

### Rollback

- Automatic rollback is §1 step 6.
- If the new script itself misbehaves: run the previous tag through it with
  `MAKTABA_CLOUD_TAG=<previous>` (same path), or set `RELAY_DEPLOY=false` and deploy by hand
  with the runbook in 50.12.

### Dependencies

50.04 (forwarding and holding during the switch), 50.07 (two nodes and Patroni). Needed by
50.14, 50.16, 50.17.
