# Epic 37 — Every full node keeps a live copy of its home's database

A full node (Epic 35) used to depend on its home for everything a person does: checking a
sign-in, reading the library, playing, watching TV. PR #145 made a node degrade to read-only
on the home's last answers for ten minutes; PR #157 kept a playback catalog for the home's
database being down. This epic replaces both with what the user asked for:

> the joined node should have a passive/active relation with the home node: it has all the
> database of the main node, so it can function as read only; the database in the joined node
> is read only, replicated immediately, so login and all, TV, play, all work in offline mode,
> but from that node and not from the home node with its full features.

Decisions the user made: **sign-in works offline**; a **full live copy** (physical streaming
replication, not logical); **one feature branch, merged once**; and **nothing is a manual
step** — joining sets the copy up, already-joined nodes convert on update, the home's side
configures itself, TLS is automatic, the tunables are rules on the settings page, and the
copy's state shows on the Machines page and the node's own status.

## What a person sees

| The home is… | On a full node |
|---|---|
| answering | Everything as before (Epic 35): sessions checked by the home, changes go to the home. The node reads the library from its copy (faster, and the home's database does less). |
| away (past `delegate_unreachable_grace_seconds`) | A notice: "This machine can't reach its home… it works from its own copy of the home's library", with how recent the copy is. Pages read, videos this machine can read play, TV plays from this machine, **signing in works** (on this machine only). Changes wait for the home (503 `home_unreachable`, under the notice). |
| back | The notice clears. A sign-in made while it was away keeps reading and playing for `offline_session_grace_minutes`; a change asks for a sign-in with the home ("Sign in again: you signed in on this machine while its home was away…"). |

A node with no usable copy (still copying it, a home that keeps none, a copy too far behind)
behaves as a node did before #145: its own routes answer, anything else is 503 with the full
"cannot reach its home" page, and sign-in says it waits for the home.

## Design

### The copy (physical streaming replication)

- The home's Postgres is the primary. Each full node runs a **hot standby** of it on its own
  loopback port (`[postgres] copy_port`, default 54330), made with `pg_basebackup` and following
  over streaming replication on a **physical slot per node** (`maktaba_copy_<node id>`), as a
  **replication role per node** (`maktaba_rep_<node id>`, in the group `maktaba_replicas`).
- Same Postgres major version on both ends (16, the version Maktaba.app bundles: 16.15). The node
  uses the Postgres this machine already has for Maktaba (Maktaba.app's payload, `[postgres] home`,
  `MAKTABA_POSTGRES_HOME`) when it is of that major version, else fetches the pinned build for its
  platform once (`cmd/maktaba-server/internal/pgbin`: theseus-rs builds, size and SHA-256 pinned).
- **Across systems.** A physical copy needs the same on-disk format, not the same OS: a Linux
  home copied to a Windows node was tested (the control file is accepted, recovery completes, WAL
  streams, a C-locale database with a `pg_trgm` index and Arabic text reads correctly, the index is
  used). Two things do differ by system, and the copy handles both: the home's `postgresql.conf`
  may not parse elsewhere (Linux's `C.UTF-8` locales, `dynamic_shared_memory_type = posix` on
  Windows) — so the copy writes **its own** `postgresql.conf` and `postgresql.auto.conf`, carrying
  only the five settings a hot standby needs at least the primary's values of (the home returns
  them with the grant; a later grant with higher values rewrites them and restarts the copy); and
  the database's collation locale must exist on the node — the bundled Postgres's is `C`, which
  every system has; a home whose database uses another locale gets "incompatible" on the Machines
  page. After every base backup the copy's control file is checked against this machine's build
  (`postgres -C data_checksums`): a build that cannot run it is "incompatible" too, never a crash
  loop.
- The bundled Postgres keeps its TLS certificate and key in the keys directory (never in the data
  directory, which is what a copy is made of), and the copy drops any TLS file it finds.

### The home's side (automatic)

- The bundled Postgres's cluster bootstrap (superuser, once) gives the app role `CREATEROLE`,
  `REPLICATION`, `pg_monitor`, `pg_signal_backend`, and makes `maktaba_replicas` (member of
  `pg_read_all_data`), which the app role administers without taking its rights. Its `pg_hba.conf`
  (rewritten at every start) lets `+maktaba_replicas` stream (`hostssl replication`) and read.
- Slot 0142 (`shared/db/migrations/0142_node_replicas.sql`):
  - `cluster_node_replica_grant(node, password, fresh)` — a machine asks for its copy **over its own
    database connection**, for its own role only (`cluster_node_guard`). It makes or updates the
    replication role with the password the machine chose, makes the slot (anew when `fresh`, or
    when the home gave up on it — `slot_made` says so) and returns the system identifier, the
    server version, the CA, the hot-standby settings. The machine chooses its password before it
    asks, so its programs are given the copy's address at their start.
  - `cluster_replica_beat(at, ca_pem)` — the home's clock, written every 5 s while a machine follows
    (`api/internal/cluster/copies.go`), and its database's CA.
  - `cluster_nodes.replica` + `cluster_node_report_replica` — what each machine reports of its copy.
  - The machines' group made late, for a home whose migrations ran before its app role could make
    roles (Maktaba.app before this epic): created with the privileges slots 0103–0141 give it,
    proven equal to the normal ones (`worker_baseline_pg_test.go`).
- `cluster.Copies` (home API): the beat, the CA published, and **the cap**: an inactive slot that
  holds more than `rules.cluster.replica_wal_keep_gb` of changes is dropped — the home's disk never
  fills for a machine that is off; that machine copies the database anew when it is back.
- Revoking a machine ends its copy (its replication role may no longer sign in, its sessions are
  ended, its slot is dropped); removing it drops the role.

### The node's side (`cmd/maktaba-server/internal/nodecopy`, run by `serve`)

The manager runs for as long as the supervisor does, on every full node — a node joined before
this epic converts at its first start on this version:

1. fetch (or find) the Postgres programs;
2. ask the home (`cluster_node_replica_grant`); pin the CA it publishes (first grant), write a
   passfile in the keys directory (the password never goes into the data directory);
3. no copy yet, or the home's database is another one (system identifier), of another major
   version, or the home gave up on the slot: stop the copy, ask for a fresh slot, `pg_basebackup`
   over `verify-ca` TLS into a new directory, make it a standby (own config, own `pg_hba`
   — loopback, scram), check it, swap it in;
4. run it as the supervisor's on-demand `copy` role (`supervisor.BuildCopy`: `pg_ctl stop` on
   Windows, SIGINT elsewhere);
5. watch it every 15 s: following (it applied a beat less than 30 s old), behind, starting; write
   the state to `<data>/node/copy.json` (the node's `/api/cluster/self`) and to the home
   (`cluster_nodes.replica`, the Machines page);
6. the home refusing this machine three times over three minutes (revoked: its role may not sign
   in) wipes the copy and its secrets.

The copy's directory is readable by this account only (0700 on Unix; on Windows its ACL keeps
only this account and SYSTEM). `MAKTABA_NODE_COPY=off` exists for tests that run a node without
Postgres programs; it is not a setting.

### Reads and writes on a node (`api/internal/cluster/nodedb`)

The node's API has one `*sql.DB` over a pair of servers. A statement whose request is a read the
node serves itself (`GET`/`HEAD` of a `local` or `any` route) goes to the copy while it answers;
anything else — a write, a transaction, a statement no request marked — goes to the home's.
With the home's database not answering, everything goes to the copy (reads answer; a write fails
as it would have). **Read-your-writes:** a change sends its author's reads to the home for 5 s,
well past the copy's apply delay on a LAN. The node's role check and its reports always go to the
home's database.

### Sign-in (`api/internal/cluster/nodeauth`)

- While the home answers: unchanged (introspection, 30 s / 5 s caches, pushed revocations).
- With the home away, a credential is looked up **on the copy**, by the home's own code
  (`clusterauth.Copy` runs `Home.Resolve`, the password check of `/api/auth/login`), so expiries,
  revocations, password changes and library rights are exactly the home's as of the copy's last
  applied change. **Freshness rule:** the copy answers only if it had applied the home's changes up
  to no more than `offline_copy_max_lag_minutes` before the home was last heard (that moment is
  kept on disk, `home-last-answered`, so a node restarted in an outage measures against it): a
  copy that stopped following long before might not have a sign-out or a password change made
  since. The outage's length does not count.
- **Node-local sessions.** A sign-in with the home away is checked against the copy and makes a
  session valid on this machine only: cookie `nls_…`, stored by the hash of its id and of its CSRF
  token in `<data>/node/local-sessions.json` (0600), lasting `offline_session_hours`; once the home
  answers again they end within `offline_session_grace_minutes`, and a change through the home's
  database asks for a sign-in with the home (401 `signin_required`; the web app signs out and says
  why). The home's lockout is mirrored on the node (five failures in 15 minutes, 423), and the
  node rate-limits the sign-in it answers. A native app signs in with the home (its tokens are the
  home's); an access token it already has keeps working for its lifetime (the node verifies it with
  the home's keys, which it caches).
- What #145 added and this keeps: the outage detection and grace, the notice and its UX, the
  refused changes, `X-Maktaba-Home: unreachable`, the DB-less start of both processes, the
  pipeline's pause. What it drops: the home's last answers kept in memory and on disk
  (`auth-answers.json.gz`) and `offline_read_minutes` — the copy is the better answer.

### Playback and TV

- **VOD:** the API's and the streaming service's lookups (a video's library, length, path, probe
  facts; the share map) read the copy while the home's database does not answer. #157's catalog
  (API) and probe snapshot (streaming) are removed; its session ledger stays — a session opened
  while the home's database is down is recorded only on the node (the copy is read only).
- **TV:** a channel's position comes from its schedule and the clock (`channel_programs`,
  `tv_live_gen()`), not from state written per segment, and the copy has both. The node's
  streaming service runs **its own station** on the copy (`streaming/offline_tv.go`): unfenced, no
  "owned elsewhere" check, its own cache, answering this machine's web port only (loopback). The
  node's web port sends `/stream/channel/…` there while its API says the home is away, to the
  home's front door otherwise. Its `channel_runtime` writes fail on the copy and are dropped, as
  such a write is anywhere. Not served offline: the HDHomeRun tuner, XMLTV and the lineup (the
  home's), and the art the home serves (the station plays without it).

### Jobs

The node's pipeline pauses while the home's database does not answer (#145, kept): it claims
nothing, lets jobs in flight finish unless the outage nears the claim lease, and resumes on its
own. Jobs are the home's queue; nothing is queued on a copy.

## Rules (`rules.cluster`, on the settings page)

| Rule | Default | Meaning |
|---|---|---|
| `delegate_unreachable_grace_seconds` | 30 | After this long without an answer from the home, a node stops asking it per request and answers from its copy. |
| `offline_copy_max_lag_minutes` | 10 | A copy answers sign-ins with the home away only if it had followed the home up to this close to when the home was last heard. |
| `offline_session_hours` | 12 | A sign-in made on a node while the home is away lasts this long. |
| `offline_session_grace_minutes` | 30 | …and ends this long after the home answers again (a change asks for a sign-in with the home at once). |
| `replica_wal_keep_gb` | 20 | The home keeps this much of its changes for a machine behind; past it the machine copies the database anew. |

## Security

- **What a node holds.** A physical copy is the whole home database: password hashes (argon2id),
  web session ids (stored as is on the home), personal access token hashes, the home's secrets
  table, the machines' keys. A full node is trusted with all of it — the same trust its
  replication role already needs, since it streams every change. The copy's files are readable by
  the node's own account only; revoking the machine stops its replication at once and wipes its
  copy at its next check.
- **On the wire.** Replication and the grant run over TLS that verifies the home's CA
  (`verify-ca`, pinned at the first grant, which itself runs over the node's TLS database
  connection). With `[postgres] tls = "off"` they run in the clear: every replicated row,
  passwords' hashes included, is readable on the network; the setting's help says so.
- **Node-local sessions** never leave the node, are stored hashed, and never reach the home. The
  copy cannot be written: nothing a node does offline changes the home's data.

## Failure modes

| Situation | What happens |
|---|---|
| Home off for days, node on | The copy stays at its last applied change; sign-ins keep working (freshness is measured at the outage's start); the slot holds WAL on the home only while the home runs. |
| Node off for long | The home drops its slot past the cap; the node copies anew on its next start. |
| Home restored from a backup / re-initialized | New system identifier: the node copies anew. |
| Home's Postgres major upgrade | The node copies anew once Maktaba ships that major; until then "incompatible", and the node runs as before this epic. |
| Copy being made or rebuilt | Reads go to the home; with the home away meanwhile, the node serves its own routes only. |
| Home's settings raised (more connections) | The next grant rewrites the copy's settings and restarts it. |
| Machine revoked | Replication refused at once; copy and secrets wiped within ~3 minutes. |

## Tests

- `api/internal/cluster/copies_pg_test.go`: the grant, refusals, the beat, the CA, the cap,
  revoke and remove, on a real Postgres 16 set up as the bundled home is.
- `cmd/maktaba-server/internal/nodecopy/copy_pg_test.go`: **two Postgres servers on 127.0.0.1** —
  a home made by `pgbundle.Prepare` and migrated by `maktaba-api`, and the copy kept by the manager
  alone: copies over TLS, follows, refuses writes, uses its own configuration, copies anew after
  the slot is dropped, is wiped on revocation. Opt-in: `MAKTABA_TEST_PG_BIN`.
- `api/twomachine_e2e_test.go`: two `maktaba-server serve` machines; with `MAKTABA_TEST_PG_BIN` the
  node's supervisor copies the home's database (Linux Postgres home, Windows node in the run that
  proved it), then the home goes away: reads, the session probe, playback, a node-local sign-in,
  refused changes; then the home's database goes too (proxy cut): reads and playback from the copy,
  also after a restart.
- Unit tests: `nodeauth` (copy-first answers, freshness, node-local sessions, lockout, grace,
  sign-in-again), `nodedb` (routing per statement), `nodecopy` (names, secrets, decisions),
  `streaming` (the offline station's routing), the web outage pages.

## Follow-ups

- Native apps: offline sign-in (an access token from the node's own key, refreshed by the home on
  its return).
- TV art offline (serve the station the art the node last fetched).
- Converting this PC's live node (`E:\tv\maktaba-local`) happens after the merge, with the user's
  OK; it is automatic once it runs this version.
