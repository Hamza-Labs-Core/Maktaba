# Epic 35 — Full-Maktaba cluster machines and automatic updates for the whole cluster

> **Status:** spec. **Source:** `specs/epics/35-cluster-full-nodes-and-updates/`.
> **Anchors:** [Epic 31 — Multi-instance](../31-multi-instance/README.md) (joining, node roles,
> the schema guard [design §5.1](../31-multi-instance/design.md#51-schema-guard-a-nodes-code-and-the-homes-database),
> the cluster channel [design-agent.md](../31-multi-instance/design-agent.md), the service door
> [story 31.R](../31-multi-instance/story-31-relay-service-door.md)),
> [Epic 32 — TV across the cluster](../32-tv-streaming-cluster-remote/README.md) (front door on the
> home, streaming nodes, drain),
> [Epic 33 — TV staging and hot swap](../33-tv-staging-hot-swap/README.md) (slots, `binary` TV
> changes, story 33.14 upgrade via swap, notifications 33.13),
> [Epic 28 — Auto-update](../28-auto-update/README.md) (version stamping, the GitHub Releases
> check, the server self-update — this epic extends them, it does not replace them),
> [Epic 22 — DevOps](../22-devops/README.md) (release pipeline 22.5, upgrade/rollback 22.6,
> process supervision [22.9](../22-devops/story-22-09-process-supervision.md)),
> `deploy/packaging` (release assets, the node installer of PR #70, hash-checked installs of PR #94).

**Documents.** [design.md](design.md) (install layout and the launcher, release verification, the
home's update state machine, the rollout to the machines, full nodes, auth across machines, routing,
failure modes, tests) · stories `story-35-01` … `story-35-15` · plans `plan-35-01` … `plan-35-15`.

## Goal

The owner's decisions (2026-10-19):

1. *Every machine that joins is a full Maktaba* — web UI, API, pipeline worker and, optionally, TV
   streaming. Opening any machine's web address gives the same Maktaba. The home stays the single
   source of truth: one database (the home's Postgres; machines use their own limited role),
   migrations only on the home, the home is the TV front door (HDHomeRun, SSDP, XMLTV, M3U, the push
   relay), and the Plex and Cloudflare secrets never leave the home (machines reach Plex through the
   service door, story 31.R).
2. *Updates are fully automatic.* The home checks for new signed releases, installs them in a quiet
   window (default 03:00–05:00 local) unless a check fails, then rolls the same version out to every
   joined machine one at a time, each verified before the next, with rollback on failure.

Today a joined machine is a Python worker installed by hand (`deploy/packaging/node`), the home's
update is a button that swaps one binary (`POST /api/admin/system/update`, Epic 28.3: SHA-256
against `checksums.txt` only — the cosign signature is not checked, and the sibling `maktaba-api` /
`maktaba-streaming` binaries and the pipeline are not updated), the CLI `maktaba-server update` reads
a different feed (`releases.maktaba.app/manifest.json`), and every machine is updated by someone
re-running its installer. A node updated before its home pauses (schema guard); a home updated
before its nodes leaves them paused until someone visits each one. This epic closes all of that.

## Decisions

Owner decisions are marked **owner**; the rest are this spec's recommendations, marked **accepted
default** and listed again under [Open questions](#open-questions) where the owner may want a say.

| Question | Decision |
|---|---|
| What a machine runs (**owner**) | The same `maktaba-server` as the home, in **node mode**: `api` (node mode), `web`, `pipeline` (node mode, as today), and `streaming` only when Epic 32 streaming is enabled for it. No database, no migrations, no front door, no secrets. |
| Single source of truth (**owner**) | The home's Postgres. A node's API uses the node's own database role (Epic 31 §3.3, no access to accounts, sessions, secrets or cluster tables). Migrations run only on the home (`MAKTABA_AUTO_MIGRATE` is never set on a node; a node's API refuses DDL). |
| Sessions across machines | **Shared sessions in the home's database, resolved by the home** (not a redirect to the home). A node never reads `web_sessions`; it asks the home over a signed delegation call and caches the answer 30 s; a revocation is pushed to every node over the cluster channel and takes effect within a second. A sign-in on a node is a sign-in on the home (the session row is the home's, tagged with the node). An optional one-click **cluster sign-in** carries a session from one machine to another (story 35.9). Accepted default. |
| Node API auth and ACL | The node's API runs the same middleware (principal, roles, CSRF double submit, rate limits) on the principal the home resolved; nothing is trusted from the browser that the home has not confirmed. |
| Home-only actions | Every API route has a **placement class**: `local` (this machine: its version, processes, logs, update state), `any` (served from the database by whichever API gets it) or `home` (proxied to the home's API with the delegated session). Unclassified routes are `home` (safe default); a test fails when a route has no class. |
| Release source | GitHub Releases of `Hamza-Labs-Core/Maktaba` (Epic 28), checked **only by the home**. Machines download the release from the home's artifact mirror (fallback: GitHub directly) and verify it themselves. |
| Verification | Every install checks, in process (no `cosign` binary needed): the Sigstore bundle of `checksums.txt` with the **pinned identity** (issuer `https://token.actions.githubusercontent.com`, SAN exactly `https://github.com/Hamza-Labs-Core/Maktaba/.github/workflows/release.yml@refs/tags/<the tag being installed>`) and a Rekor inclusion proof; then the SHA-256 of every file it installs against `checksums.txt`; then the signed `release.json` (version = tag, schema fields). Pipeline dependencies install with `--require-hashes` from the release's constraints (PR #94). Anything that fails is not installed. |
| Automatic (**owner**) | `updates.auto_install = true`. The home installs a new release in the quiet window unless a check fails, then rolls it out to every machine. "Update now" runs the same flow at once. Checks that fail stop the run before anything changes, or roll it back. |
| Approval override (**owner**) | Epic 33's "every change needs approval" no longer applies to the `binary` kind **when the change comes from the cluster updater** (`source.kind = auto_update`): such a change is staged, verified and swapped with `auto_rule = immediate` (at programme boundaries, `swap_wait = boundary`). Lineup, settings, schedule, art and live-slot changes — and a binary change made by hand — still need an admin's approval. Recorded in Epic 33 (README decision 2, R13, story 33.14 AC2). |
| Install layout | Side by side: `<install>/bin/releases/<version>/` (the Epic 33 §3.3 layout, now used for every binary and the pipeline's environment), `current` and `previous` pointer files, and a small stable **launcher** that the OS service runs. The launcher starts `bin/releases/<current>/maktaba-server serve`, restarts it on exit code 75, and flips back to `previous` when a new version cannot boot. |
| Database rollback | **Forward only; binaries roll back, the schema does not.** Migrations are additive (expand/contract across releases), so the previous release runs on the new schema; a pre-update backup is taken and verified, but restoring it is a manual, confirmed command (it would lose writes made since). A release with migrations is first **rehearsed** on a scratch copy restored from that backup. Down migrations are never run automatically. Accepted default. |
| Contract releases | A release whose migrations break the **previous home release** (a MANIFEST row marked "Breaking for the previous release", published as `release.json: breaks_previous_home = true`) cannot be rolled back by binaries; the updater holds it and asks for an admin's approval (the one exception to fully automatic, the same rule as story 33.14 EC1). Breaking **older nodes** only (`maktaba_min_node_schema()` raised) is fine: nodes pause until their turn, which comes minutes later. |
| Order | Home first, then machines one by one; never a machine ahead of the home. Machines download in parallel while the home updates; only the switch is sequential. |
| Machine failure | The failed machine rolls back to its previous release, is drained and reported. The rollout **holds machines of the same platform** (OS/arch) and continues with the others (`node_failure_policy = hold_platform`); `halt` and `continue` are the alternatives. Accepted default. |
| Skew | Home ≥ every machine. A machine behind runs while the schema guard says its code fits; it pauses (Epic 31 §5.1) when a migration says older nodes break. A machine that comes back online behind is updated at once (catch-up), streaming machines in the quiet window. |
| TV during updates | Through Epic 33's hot swap: the new streaming binary runs as the staging slot, is verified and swapped at programme boundaries; the front door restarts only when its `edge_protocol` changed. Until the two-slot runtime (33.1) is built, the update restarts the streaming role in the window when no more than `quiet_max_viewers` are watching. |
| Quiet window | `updates.window = "03:00-05:00"` (home's local time), default equal to `tv_staging.quiet_window`. A run starts only inside it; the TV swap and node rollout may finish after it (bounded by `window_overrun_minutes`, 120). |
| Package-managed installs | Self-managed layout required for automatic updates. `.deb`/`.rpm` move to the layout (the package installs the launcher and the first release under `/var/lib/maktaba`); Homebrew installs are offered `maktaba-server setup --self-managed`; Docker keeps Epic 28.3's 409 with instructions (out of scope). |
| Downgrades | Never automatic. The installed-version watermark only rises; a pin below it, a channel switch `beta → stable` and a re-published older tag are all refused. The only way back is a rollback to the kept, already verified `previous` release, and only when its code fits the current schema. |

## Scope

- Release manifest (`release.json`) and Sigstore bundle published by `release.yml`; a shared Go
  verification package used by the API, the supervisor and nodes.
- The side-by-side install layout, the launcher and its boot-loop guard, per-OS service units
  (Windows service or logon task, macOS launchd, Linux systemd), migration of existing installs.
- Update policy and discovery on top of Epic 28.2: channel, pin, defer, skip, quiet window, release
  age, watermark, automatic install.
- The home's update orchestrator in the supervisor: journal, steps, health checks, commit or
  rollback, resume after power loss.
- Database safety: verified backup, migration rehearsal, migration rules, manual restore.
- TV through Epic 33 (automatic `binary` changes), and the fallback before the two-slot runtime.
- Supervisor and launcher self-update, re-adopting the TV processes.
- Full nodes: `maktaba-server` in node mode on every joined machine; converting today's worker-only
  nodes.
- Auth across machines: delegated sessions, revocation push, sign-in through a node, CSRF and
  cookies per host, tokens, cluster sign-in.
- Routing on a node: placement classes, the home proxy, caching, the paused page.
- mDNS for every machine.
- The rollout to machines over the cluster channel: prepare, apply, verify, failure policy,
  catch-up, skew.
- Updates page, notifications (33.13 centre), audit, history and logs.
- Observability, failure-mode tests, docs and runbook.

## Non-goals

- A second home, home failover, or a replicated database (Epic 31: one home).
- Docker / compose installs updating themselves (Epic 28.3 answer stays; Watchtower-style is the
  operator's choice).
- Delta updates, staged percentage rollouts across unrelated installs.
- Desktop and mobile app updates (Epic 28.4 / 28.5 unchanged).
- TLS between machines on the LAN (Epic 31 follow-up; this epic keeps the same exposure as today and
  says where it matters, see Security).
- Running the vector index, thumbnails or review pages on nodes (Epic 31 home-only stages stay).
- A node serving HDHomeRun, SSDP, XMLTV/M3U as the advertised source, or the push (Epic 32 R1.4).

---

## Requirements

### R1 — Full nodes
- R1.1 A joined machine runs `maktaba-server serve` in node mode, with roles `api`, `web`,
  `pipeline` and, when Epic 32 streaming is enabled for it, `streaming`. The roles follow the node
  config (Epic 31 `node.json`); no extra flag.
- R1.2 A node never migrates: its API starts with migrations off, opens the home's database with the
  node's role, and refuses to start when the role can create tables (a misconfigured superuser URL).
- R1.3 A node's API applies the schema guard (Epic 31 §5.1) at start and every 5 minutes: paused,
  its `any` routes are not served and the web shows the paused page (R6.4).
- R1.4 A node holds no Plex token, Cloudflare key, push credential or home secret; extension calls go
  through the service door (31.R); admin actions that need them go to the home (R6.2).
- R1.5 Today's worker-only nodes convert to full nodes with one command
  (`maktaba-server setup --node` or the node installer's `--full`), keeping their node id, role and
  path map; worker-only stays supported.
- R1.6 A node's web UI, opened at its own address, is the same app as the home's, of the node's
  version (equal to the home's after a rollout).

### R2 — Auth and sessions across machines
- R2.1 Sessions live in the home's `web_sessions`; only the home reads or writes them.
- R2.2 A node resolves a session cookie, a bearer access token or a personal access token through
  the home's delegation endpoint, signed with the node's delegation key; positive answers are cached
  30 s, negative ones 5 s.
- R2.3 A revocation (sign-out, password change, admin revoke, user disabled) reaches every node over
  the cluster channel and evicts its cache within 1 s while the channel is up; with the channel down
  the cache TTL bounds it (30 s), after which the node fails closed for authenticated routes.
- R2.4 Signing in on a node creates the session on the home (tagged `origin_node_id`); the cookie is
  host-only for the node's address; CSRF double submit works per host.
- R2.5 Cluster sign-in: a signed-in user who opens another machine is offered "Continue as …"; a
  one-time code (60 s, single use, bound to the target node and a PKCE verifier) creates a child
  session on the home for that machine; signing out on the home ends its children.
- R2.6 Node APIs enforce the same roles and ACLs; admin-only routes on a node are admin-only.
- R2.7 Delegation keys are per node, issued over the channel, rotated daily, and stop working at once
  when the node is revoked.

### R3 — Releases and verification
- R3.1 Every release publishes `release.json` (version, commit, schema_rev, min_node_schema,
  breaks_previous_home, edge_protocol, launcher_protocol, min_upgrade_from, per-platform assets with
  SHA-256, the pipeline wheel and constraints) and `checksums.txt.sigstore.json`; both are in
  `checksums.txt`.
- R3.2 Verification is in-process and identical on the home and on every node (R3.3–R3.5); nothing is
  installed, staged as runnable or executed before it passes.
- R3.3 The Sigstore bundle verifies against the embedded trusted root, with the pinned issuer and the
  exact SAN for the tag being installed, and a transparency-log inclusion proof.
- R3.4 Every file installed matches `checksums.txt`; `release.json` matches the tag, the platform
  asset names and the asset digests.
- R3.5 Pipeline dependencies install with `--require-hashes` from the release's constraints; a
  constraints file without hashes is refused (PR #94 rule).
- R3.6 A verification failure is a critical notification naming the file and the reason; the
  version is blocked until a newer one appears.

### R4 — Policy and scheduling
- R4.1 Settings (`rules.updates`, settings UI): `auto_install` (default true), `channel`
  (`stable`|`beta`), `window` (`03:00-05:00`), `window_overrun_minutes` (120), `min_release_age_hours`
  (24), `pinned_version`, `deferred_until`, `skipped_versions`, `node_failure_policy`
  (`hold_platform`), `node_health_timeout_minutes` (10), `drain_timeout_minutes` (10),
  `rehearse_migrations` (true), `rehearse_max_db_gb` (20), `keep_versions` (3), `backups_keep` (3),
  `catch_up` (`immediate`), `quiet_max_viewers` (from `tv_staging`).
- R4.2 The home checks GitHub every 6 h (Epic 28.2 poller, `MAKTABA_GITHUB_TOKEN` optional) and
  re-checks the chosen release just before it installs (withdrawn or turned prerelease ⇒ not
  installed).
- R4.3 A run starts only inside the window, only for a verified release that is newer than the
  watermark, at least `min_release_age_hours` old, not skipped, not past a pin, and not deferred.
- R4.4 "Update now" (admin, confirmed) starts the same run immediately, ignoring the window and the
  release age, never the checks.
- R4.5 With `auto_install = false` the home only checks and notifies (Epic 28 behaviour).

### R5 — The home's update
- R5.1 Steps, each recorded in a journal and in `update_runs`: preflight → download → verify → stage
  side by side → smoke test → backup → rehearse (when the release has migrations) → drain the home's
  pipeline → migrate → TV → restart `api`, `pipeline` (and the front door when `edge_protocol`
  changed) one at a time → supervisor last → health → commit.
- R5.2 Every step is idempotent and resumable; after a power loss the run resumes from the last
  durable step or rolls back.
- R5.3 A failure before migrate leaves everything untouched. A failure at migrate leaves the old
  binaries running on the (additive) schema. A failure after migrate rolls the binaries back to the
  previous release; the schema stays.
- R5.4 Health after each restart: `/readyz` of the role, its reported version, the schema guard, the
  agent channel; after commit: a 10-minute watch in which a crash loop rolls back automatically.
- R5.5 Old releases are kept (`keep_versions`, never fewer than current + previous) and pruned after
  commit.

### R6 — Routing on a node
- R6.1 Every API route has a placement class (`local`, `any`, `home`); the router refuses to start
  with an unclassified route in tests.
- R6.2 `home` routes are reverse-proxied to the home's API with the delegated session and the node's
  signature, including WebSockets (the Processing page's hub socket) and streamed answers.
- R6.3 `/stream/…`, `/xmltv.xml`, `/lineup.m3u` on a node's web port go to the home's front door;
  images from home-only caches (posters, sprites, review pages) are proxied and cached on the node
  (content-addressed, LRU, `proxy_cache_mb`, 2048).
- R6.4 A paused or home-unreachable node shows a holding page naming the reason and linking to the
  home's address; `local` routes keep working.
- R6.5 Pages: every page of the web app works on a node (matrix in design §8.4).

### R7 — Discovery
- R7.1 Every machine advertises `_maktaba._tcp` with `role=home|node`, `home=<instance id 8>`,
  `web=<port>`, `version`; never a library or user name.
- R7.2 The apps' server picker lists every machine of a cluster under the home's name, prefers the
  home, and falls back to any reachable machine.

### R8 — Rollout to the machines
- R8.1 After the home commits, every online machine **prepares** (download from the home's mirror,
  verify, stage, build the pipeline environment) in parallel; machines then **apply** one at a time.
- R8.2 Apply: drain (pipeline; streaming through the Epic 33 per-host binary change or the Epic 32
  drain) → switch `current` → restart → schema guard passes → health (api, pipeline, streaming,
  channel) → undrain → next.
- R8.3 A machine that fails rolls back locally, stays drained, is reported, and the
  `node_failure_policy` applies. "Retry" and "Skip" are admin actions.
- R8.4 A machine offline during the rollout is updated when it reconnects (`catch_up`).
- R8.5 Commands travel over the cluster channel to a new machine agent in the node's supervisor
  (`update.prepare`, `update.apply`, `update.abort`, `update.rollback`); progress comes back as
  `update.progress`.
- R8.6 A machine never installs a version the home has not committed.

### R9 — Updates page, notifications, history
- R9.1 Admin → System → **Updates**: every machine with role, OS/arch, version, state (up to date,
  preparing, prepared, updating, paused, failed, offline, behind), last update; the next planned
  update (version, notes, window) with Update now / Skip / Defer / Pin; settings; history of runs
  with steps, timings, per-machine results and logs (live during a run). English and Arabic.
- R9.2 Notifications (Epic 33.13 `notifications`): update scheduled (tonight vX), installed on the
  home, rollout complete, failed (with the check), rolled back, machine failed, machine paused by
  schema, verification failed (critical), release held (contract), release withdrawn, no successful check for
  14 days (`update_check_stale`).
- R9.3 Every run, step, admin action and machine result is in `audit_log` (category `update`).

### R10 — Security
- R10.1 Only releases signed by the pinned workflow identity install; the identity and trusted root
  ship in the binary.
- R10.2 No downgrade, no replay of an older signed release, no install from a URL the request names.
- R10.3 Rollback only to the kept `previous` release, only when its schema fits.
- R10.4 Update commands are admin-only on the home and are accepted by a node only from its
  authenticated home channel.

### R11 — Observability and docs
- R11.1 Metrics `maktaba_update_*`, events `update_*`, the control centre shows the run.
- R11.2 Runbook: what happens at 03:00, how to pause updates, how to roll back, how to restore a
  backup, what each failure means.

## Acceptance (epic)

- A cluster of a Mac home, a Linux node and a Windows node on v1.4.0, with `auto_install` on: a
  signed v1.5.0 appears; at 03:00 the home downloads, verifies, backs up, rehearses and migrates,
  swaps TV with no viewer interruption, restarts its roles and itself, then updates the Linux and the
  Windows machine one after the other; the Updates page shows all three on v1.5.0; nobody touched a
  machine.
- A v1.5.1 whose `checksums.txt` signature has another identity is never installed; the admin gets a
  critical notification.
- A v1.5.2 whose API fails `/readyz` on the home rolls back to v1.5.1 on the home, nodes are not
  touched, the run is reported failed.
- A v1.5.3 that fails on Windows only leaves the Windows machine on v1.5.2 (drained, reported), the
  Linux machine on v1.5.3.
- Pulling the home's power during migrate: on boot the home resumes or rolls back without an admin.
- Opening `http://<linux-node>:8088` shows the same library; signing in there works; signing out on
  the home signs the user out there within a second; approving a TV change on the node works
  (proxied).

## Architecture

```
                         GitHub Releases (Hamza-Labs-Core/Maktaba)
                         release.json · checksums.txt · checksums.txt.sigstore.json · archives · wheel
                                     │  (home only; 6 h check, Epic 28.2)
 ┌─────────────────────────────── HOME ┼──────────────────────────────────────────────────────────┐
 │ OS service ─► maktaba-launcher ─► bin/releases/<current>/maktaba-server serve (supervisor)       │
 │                 (current/previous, exit 75 = restart, boot-loop guard → previous)               │
 │   supervisor ── update orchestrator (journal) ── loopback admin :9103                           │
 │     ├─ api        Updater (policy, schedule) · /api/cluster/updates · hub · delegation endpoint │
 │     │             artifact mirror /api/cluster/updates/artifacts/<v>/… (node-signed)            │
 │     ├─ web        same origin; /api → api; /stream → front door                                 │
 │     ├─ pipeline   (home workers)                                                                │
 │     ├─ streaming  FRONT DOOR (SSDP, HDHR, XMLTV/M3U, push relay)                                │
 │     └─ tv-slots   live / standby / staging (Epic 33) — new streaming binary verified here first │
 │   Postgres (single source of truth): catalog, queue, web_sessions, update_runs, …               │
 └──────▲───────────────────────▲───────────────────────────────▲─────────────────────────────────┘
        │ cluster channel (WS,  │ SQL (node role,              │ HTTP: delegation (signed),
        │ sealed): update.*,    │ no auth/secret tables)       │ home-class routes (proxied),
        │ auth.revoked, keys    │                              │ artifacts, /stream → front door
 ┌──────┴───────────────────────┴───────────────────────────────┴─────────────────────────────────┐
 │ NODE (Linux / Windows / Mac) — OS service ─► maktaba-launcher ─► bin/releases/<current>/…      │
 │   supervisor (node mode) ── machine agent (Go, channel kind node/process supervisor)            │
 │     ├─ api (node mode)   route classes: local · any (DB) · home (proxy) · session cache         │
 │     ├─ web               same app, node's version; paused page when the schema guard pauses     │
 │     ├─ pipeline          node worker (Epic 31), venv bin/releases/<v>/venv                      │
 │     └─ streaming?        Epic 32 streaming node / Epic 33 node slots, only when enabled         │
 │   mDNS _maktaba._tcp role=node home=<id8> web=<port>                                            │
 └─────────────────────────────────────────────────────────────────────────────────────────────────┘
```

## State machines

**Update run (home)** — one at a time; persisted in `<data_dir>/updates/journal.json` and
`update_runs.state`.

```
 planned ─► preflight ─► downloading ─► verifying ─► staged ─► backed_up ─► rehearsed ─► migrating
    │           │ fail        │ fail         │ fail      │ fail      │ fail        │ fail     │ fail
    ▼           ▼             ▼              ▼           ▼           ▼             ▼          ▼
 cancelled   failed (nothing changed; retried next window, at most 3 times, then blocked)  failed_migrate
                                                                                    (old binaries keep running)
 migrating ─► tv_swapping ─► restarting_roles ─► restarting_supervisor ─► watching ─► home_committed
                 │ verify fail       │ health fail          │ boot-loop (launcher)    │ crash loop
                 ▼                   ▼                      ▼                         ▼
             failed_tv          rolling_back ──────────────────────────────────► rolled_back
 home_committed ─► rolling_out ─► done | done_with_failures | held (platform held / admin paused)
```

**Machine target** — per node in `update_targets`.

```
 pending ─► preparing ─► prepared ─► queued ─► draining ─► switching ─► verifying ─► healthy
   │ offline   │ fail        │                                │ fail        │ fail
   ▼           ▼             ▼ (platform held)                ▼             ▼
 offline    failed_prepare  held                        rolling_back ─► rolled_back (drained, reported)
   │ reconnects (catch_up)
   └─► preparing
```

## Data model (proposed migrations — slots are "next free at implementation time")

Main ends at 0128; 0129/0130 are provisional for Epic 33 (slots, notifications). Slots here are named
`S1`, `S2`, … and take the next free numbers when each story lands; each has a `.sqlite.sql` sibling;
`MANIFEST.md` rows only when the files exist. The watermark exists twice: `update_watermark` in the home's database (the cluster's highest committed version) and the `watermark` file on each machine (design §2.1, §3.5), which a node raises itself after its apply. All new tables are revoked from `maktaba_worker`
(Epic 31 §3.3 rule for cluster tables).

| Slot | Story | Tables / changes |
|---|---|---|
| S1 | 35.3, 35.4, 35.13 | `release_catalog` (version pk, tag, channel, published_at, prerelease, release_url, notes, assets jsonb, manifest jsonb, verify_state `unverified\|verified\|failed`, verify_error, first_seen_at, withdrawn_at, blocked_reason, attempts int); `update_runs` (id uuid, version, from_version, trigger `auto\|manual\|catch_up\|retry`, state, step, started_at, finished_at, error jsonb, backup jsonb, actor_user_id, journal_digest); `update_run_steps` (run_id, seq, host_id `home`\|node uuid, step, state, started_at, finished_at, detail jsonb, PK (run_id, seq)); `update_targets` (run_id, node_id, ord int, platform, state, from_version, to_version, attempts, error jsonb, prepared_at, applied_at, healthy_at, PK (run_id, node_id)); `update_watermark` singleton (highest_installed text, set_at). |
| S2 | 35.9 | `web_sessions` + `origin_node_id uuid null`, `parent_session_id text null` (FK to `web_sessions.id` on delete cascade); `cluster_sso_codes` (code_hash pk, node_id, user_id, parent_session_id, pkce_challenge, return_path, expires_at, used_at); `cluster_nodes` + `delegation_key_id text`, `delegation_rotated_at`. The keys themselves live in `home_secrets` (`cluster.node.<id>.delegation`). |
| S3 | 35.12 | `cluster_nodes` + `version text`, `platform text`, `update_state jsonb` (state, run_id, prepared_version, detail), `full_node bool default false`. |

Settings are `app_settings` rows under `rules.updates` (no migration; `api/internal/handlers/rules/schema.json`
and the settings UI). Rule "defaults = current behaviour" is overridden by the owner for `auto_install` (true), as
Epic 33 did for `tv_staging.enabled`.

## Settings (`rules.updates`)

| Key | Default | Meaning |
|---|---|---|
| `auto_install` | `true` (**owner**) | Install automatically. Off: check and notify only (Epic 28). |
| `channel` | `stable` | `stable` \| `beta` (Epic 28.2). Switching to `stable` never downgrades. |
| `window` | `"03:00-05:00"` | Home's local time; a run starts only inside it. |
| `window_overrun_minutes` | `120` | A started run may finish its TV swap and rollout this long after the window; then the rollout holds until the next window. |
| `min_release_age_hours` | `24` | A release must be this old before an automatic install (a pulled release is never installed). `0` = as soon as seen. Update now ignores it. |
| `pinned_version` | `""` | Stay at or below this version (never below the watermark). |
| `deferred_until` | `null` | No automatic run before this instant. |
| `skipped_versions` | `[]` | Never installed automatically (a newer one is). |
| `node_failure_policy` | `hold_platform` | `hold_platform` \| `halt` \| `continue`. |
| `node_health_timeout_minutes` | `10` | A machine that is not healthy by then has failed. |
| `drain_timeout_minutes` | `10` | Longest wait for running jobs before a restart (transcriptions resume from checkpoints). |
| `rehearse_migrations` | `true` | Rehearse a release's migrations on a scratch copy of the database first. |
| `rehearse_max_db_gb` | `20` | Above this size the rehearsal is skipped (logged; the backup still runs). |
| `keep_versions` | `3` | Releases kept in `bin/releases` (never fewer than current + previous). |
| `backups_keep` | `3` | Pre-update backups kept. |
| `catch_up` | `immediate` | `immediate` \| `window`: when a machine that was offline or behind is updated (streaming machines always in the window). |
| `proxy_cache_mb` | `2048` | Node-side cache for proxied images. |
| `delegate_unreachable_grace_seconds` | `30` | After this long without an answer from the home, a node fails closed for authenticated routes (R2.3). |

Cluster sign-in has its own switch, `rules.cluster.sso` (default `true`; off: each machine asks for its own sign-in, still against the home's sessions).

## APIs

```
# Home (admin) — through any machine (home class)
GET    /api/cluster/updates                    {policy, latest:{version, notes, verified, age}, next_run_at,
                                                run?:{id,state,step}, machines:[{id,name,role,platform,version,state,last_update}]}
PUT    /api/cluster/updates/settings           rules.updates keys
POST   /api/cluster/updates/run                {version?, confirm:true}  Update now → 202 {run_id}
POST   /api/cluster/updates/skip               {version}
POST   /api/cluster/updates/defer              {until}
GET    /api/cluster/updates/runs?limit=        history
GET    /api/cluster/updates/runs/{id}          steps, targets, errors
GET    /api/cluster/updates/runs/{id}/log      text/event-stream (live) or the archived log
POST   /api/cluster/updates/runs/{id}/cancel   before migrate only
POST   /api/cluster/updates/runs/{id}/resume   a held rollout
POST   /api/cluster/updates/runs/{id}/targets/{node}/retry | /skip
POST   /api/admin/system/update                (Epic 28.3, kept) = Update now on the home; returns {run_id}
GET    /api/system/updates                     (Epic 28.2, extended) + verified, policy, next_run_at

# Home — for nodes (signed with the node's delegation key; never through the relay)
POST   /api/cluster/delegate/introspect        {cookie_session? | bearer? | pat?} → {principal, csrf_hash, expires_at} | 401
ANY    /api/…                                  home-class routes proxied by a node: X-Maktaba-Node, X-Maktaba-Node-Sig,
                                               X-Maktaba-Delegated-Session (or Authorization passed through)
GET    /api/cluster/updates/artifacts/{version}/{name}   the verified files of a staged release (mirror)
GET    /api/cluster/sso/authorize?node=&challenge=&return=   (browser, home origin) → 302 node callback ?code=
POST   /api/cluster/sso/exchange               (node, signed) {code, verifier} → session for the node's origin

# Node (local class)
GET    /api/system/version · /api/system/processes · /api/cluster/self   this machine (identity, role, version, home)
GET    /api/cluster/sso/callback?code=         (browser, node origin) sets the node cookie

# Supervisor loopback (127.0.0.1:9103, local token)
POST   /update/start {run_id} · GET /update/status · POST /update/abort (before migrate only) · POST /update/rollback
POST   /update/target-event                (API → supervisor: a machine's update.progress/state, relayed from the hub)

# Cluster channel (additive frames, protocol v2)
S→C  cmd update.prepare {run_id, version, manifest_digest, mirror} · update.apply {run_id} · update.abort · update.rollback
C→S  update.progress {run_id, step, state, pct?, detail} · update.state {version, prepared_version?, platform}
S→C  auth.revoked {session_ids? | user_id?} · keys {delegation:{id, key, not_after}}
```

## UI

Admin → System → **Updates** (story 35.13): a header with the cluster's version and the next planned
update ("v1.5.0 tonight at 03:00 — release notes"), buttons Update now, Skip this version, Defer,
Pin; a machines table (home first; version chip, state, platform, last update, a link to the
machine); the current run's step timeline (home steps, then one row per machine), live; history; the
settings card. Epic 28.6's version section links here; the Machines page shows a version chip and
"behind" badge. A banner on every page during a run ("Updating to v1.5.0 — TV continues"). The
paused page on a node (R6.4). English and Arabic, RTL.

## Security

- **Signature identity pinned** in code: issuer and SAN for the exact tag; the SAN regexp of the
  release notes (`https://github.com/.+/Maktaba/…`) is too loose and is not used. The trusted root is
  embedded and refreshed through Sigstore's TUF repository with the embedded root as anchor.
- **Monotonic**: the watermark (database and a local file on each machine) only rises; a release
  older than it, or with the same version and a different digest, is refused.
- **No path from a request**: the release, asset names and install paths come from the verified
  manifest; API requests name at most a version.
- **Node trust**: a node accepts update commands only on its authenticated channel to its own home
  (v2 handshake proves the home's cluster key); it re-verifies everything it downloads from the home
  (the mirror is a convenience, not a trust anchor).
- **Delegation**: a node can act only as a user whose session the home confirms; the home re-checks
  the session on every proxied home-class request; per-node keys, rotated daily, revoked with the
  node. Session ids travel node → home over the LAN like browser cookies do today (plain HTTP until
  the Epic 31 TLS follow-up); the cluster sign-in uses single-use, short, PKCE-bound codes.
- **Least privilege kept**: the node's database role gains nothing; new tables are revoked from it.
- **Backups** are written mode 0600 in the home's data dir, never to the share.
- **Rollback limits**: only `previous`, only if its schema rev is ≥ `maktaba_min_node_schema()` and
  it is not before a `breaks_previous_home` release.

## Rollout

1. **Phase A — foundations** (35.1, 35.2, 35.3): `release.json` + bundle in `release.yml`; the
   verification package (Epic 28.3 and the CLI `update` switch to it — fixing the unsigned-checksums
   gap); the layout and launcher with migration of existing installs; policy with
   `auto_install = false` until phase B ships.
2. **Phase B — the home updates itself** (35.4, 35.5, 35.6, 35.7): orchestrator, backups,
   rehearsal, TV through Epic 33 (or the restart fallback), supervisor self-update.
   `auto_install` flips to `true`.
3. **Phase C — full nodes** (35.8, 35.9, 35.10, 35.11): node mode, auth, routing, mDNS; conversion of
   today's nodes.
4. **Phase D — cluster rollout** (35.12, 35.13): rollout, Updates page, notifications.
5. **Phase E — hardening** (35.14, 35.15): chaos and E2E tests on three OSes; runbook.

## Stories

| # | Story | Summary |
|---|---|---|
| 35.1 | [Signed release manifest and verification](story-35-01-release-manifest-verification.md) | `release.json`, Sigstore bundle, pinned identity, shared Go `releaseverify`, hash-pinned pipeline deps; Epic 28.3 and the CLI use it. |
| 35.2 | [Side-by-side layout and the launcher](story-35-02-layout-launcher.md) | `bin/releases/<v>/`, `current`/`previous`, `maktaba-launcher`, boot-loop guard, per-OS services, migrating existing installs. |
| 35.3 | [Update policy, discovery and scheduling](story-35-03-policy-discovery-scheduling.md) | `rules.updates`, channel/pin/defer/skip/age/window, watermark, release catalog, the scheduler. |
| 35.4 | [Home update orchestrator](story-35-04-home-orchestrator.md) | Supervisor-side state machine, journal, steps, health, commit/rollback, resume after power loss. |
| 35.5 | [Database safety](story-35-05-database-safety.md) | Verified backup, rehearsal on a scratch copy, migration rules, forward-only, manual restore. |
| 35.6 | [TV during updates](story-35-06-tv-during-updates.md) | Automatic `binary` TV change (Epic 33 override), edge protocol, fallback before the two-slot runtime. |
| 35.7 | [Supervisor and launcher self-update](story-35-07-supervisor-self-update.md) | Handover and re-adoption of TV processes, Windows job handles, launcher replacement. |
| 35.8 | [Full nodes](story-35-08-full-nodes.md) | `maktaba-server` node mode, roles, no migrations, Go schema guard, conversion of worker-only nodes. |
| 35.9 | [Auth across machines](story-35-09-auth-across-machines.md) | Delegated sessions, revocation push, sign-in on a node, cookies/CSRF per host, tokens, cluster sign-in. |
| 35.10 | [Routing on a node](story-35-10-node-routing.md) | Placement classes, home proxy (HTTP + WS), stream and image proxy, cache, paused page, pages matrix. |
| 35.11 | [Discovery of every machine](story-35-11-discovery.md) | mDNS `role=node`, apps' server picker. |
| 35.12 | [Cluster rollout](story-35-12-cluster-rollout.md) | Machine agent, `update.*` commands, mirror, prepare in parallel, apply one by one, failure policy, catch-up, skew. |
| 35.13 | [Updates page, notifications and history](story-35-13-updates-page-notifications.md) | Admin page, live run view, history and logs, notifications, audit. |
| 35.14 | [Failure modes and end-to-end tests](story-35-14-failure-modes-e2e.md) | Power loss, home failure, node offline, signature attacks; three-OS E2E. |
| 35.15 | [Observability, docs and runbook](story-35-15-observability-docs.md) | Metrics, events, control centre; operator docs and runbook. |

## Dependencies

- Epic 28.1–28.3 (built: `api/internal/system/updater.go`, `selfupdate.go`), extended here.
- Epic 31 phase 1 and the cluster channel (built: `api/internal/cluster/agent`), extended with
  frames; story 31.R (service door) for Plex from nodes.
- Epic 32 (built on main: front door, placement, drain) for streaming nodes.
- Epic 33: 33.2/33.3/33.10/33.12 built; 33.1 (slots), 33.7 (swap), 33.9 (rollback), 33.13
  (notifications) and 33.14 for the seamless TV path. Before they land, 35.6's fallback applies and
  notifications fall back to the audit log plus the Updates page.
- Epic 22.9 supervisor (built: per-child restart, probes, events).
- `release.yml` (22.5) and `deploy/packaging/node` (PR #70, #94).

## Open questions

Each has the recommendation this spec uses.

1. **Sessions: shared via the home vs redirect to the home.** Spec: shared (the home resolves every
   session; nodes cache 30 s; revocation pushed). Redirect-to-home would make every machine's UI a
   thin client of the home's address and lose "open any machine". Confirm.
2. **Release age before automatic install** (`min_release_age_hours = 24`). It protects against a
   release pulled within a day; the owner may prefer 0 (install the same night). Confirm 24.
3. **Contract releases held for approval.** A release that breaks the previous home release cannot
   be rolled back by binaries; the spec holds it for an admin. The alternative is to install it
   automatically and accept restore-from-backup as the only way back. Confirm the hold.
4. **Machine failure policy** `hold_platform` (vs `halt` everything, vs `continue`). Confirm.
5. **Windows nodes: service or logon task.** Shares mapped in a user session are invisible to a
   service; the spec keeps Epic 31's default (logon task of the user) and offers the service with UNC
   paths. Confirm the default.
6. **Database rollback** forward-only with manual restore (vs automatic down migrations). Confirm.
7. **Catch-up outside the window** for non-streaming machines (`catch_up = immediate`). Confirm.
8. **TLS on the LAN.** Delegated session ids cross the LAN in plain HTTP like browser cookies do
   today. Should this epic wait for (or include) the Epic 31 TLS follow-up?
9. **Worker-only nodes.** Convert them to full nodes automatically during a rollout, or only when an
   admin runs `setup --node`? Spec: admin-run (the conversion changes the machine's services).
10. **Machines without internet.** A node needs PyPI (hash-checked) to build the pipeline's environment.
    A wheelhouse mirrored by the home is a follow-up; needed now?
11. **Release gate.** Plan 35.14 makes the three-OS cluster E2E required before `release.yml`
    publishes a tag; its networking (a tailnet or a self-hosted lab, since hosted runners cannot
    multicast to each other) is undecided.

## Sources

- Sigstore: cosign keyless signing and `verify-blob` identity flags —
  <https://docs.sigstore.dev/cosign/verifying/verify/>; bundle format and `sigstore-go` verifier —
  <https://github.com/sigstore/sigstore-go>; GitHub Actions OIDC claims (SAN = workflow ref) —
  <https://docs.github.com/actions/security-for-github-actions/security-hardening-your-deployments/about-security-hardening-with-openid-connect>.
- TUF (trusted root updates, rollback and freeze attacks) — <https://theupdateframework.io/security/>.
- systemd `RestartForceExitStatus=` — <https://www.freedesktop.org/software/systemd/man/latest/systemd.service.html>.
- launchd `KeepAlive` — `man launchd.plist`.
- Windows Job Objects and handle inheritance —
  <https://learn.microsoft.com/windows/win32/procthread/job-objects>; service recovery actions —
  <https://learn.microsoft.com/windows/win32/api/winsvc/ns-winsvc-service_failure_actionsa>.
- PostgreSQL `pg_dump`/`pg_restore` custom format — <https://www.postgresql.org/docs/current/app-pgdump.html>.
- pip `--require-hashes` — <https://pip.pypa.io/en/stable/topics/secure-installs/>.
- In repo: Epic 28 README and stories, `api/internal/system/{updater,selfupdate}.go`,
  `cmd/maktaba-server/{cmd_update.go,cmd_serve.go}`, `cmd/maktaba-server/internal/supervisor/{env,release}.go`,
  `deploy/packaging/{README.md,upgrade.md,node/README.md}`, `.github/workflows/release.yml`,
  Epic 31 design §3.3, §5.1, design-agent §4–§6, Epic 33 README and design §2, §3.3, §13.
