# Epic 37 — Every full node keeps a live copy of its home's database

A full node (Epic 35) used to depend on its home for everything a person does: checking a
sign-in, reading the library, playing, watching TV. PR #145 made a node degrade to read-only
on the home's last answers for ten minutes; PR #157 kept a playback catalog for the home's
database being down. This epic replaces both with what the user asked for:

> the joined node should have a passive/active relation with the home node: it has all the
> database of the main node, so it can function as read only; the database in the joined node
> is read only, replicated immediately, so login and all, TV, play, all work in offline mode,
> but from that node and not from the home node with its full features.

Decisions the user made: **sign-in works offline**; a **full live copy**, by **logical
replication** (it replaced the physical standby first built: a copy that is its own database
can leave the home's secrets out and can become the home); **one feature branch, merged
once**; and **nothing is a manual step** — joining sets the copy up, already-joined nodes
convert on update, the home's side configures itself, TLS is automatic, the tunables are rules
on the settings page, and the copy's state shows on the Machines page and the node's own
status.

## What a person sees

| The home is… | On a full node |
|---|---|
| answering | Everything as before (Epic 35): sessions checked by the home, changes go to the home. The node reads the library from its copy (faster, and the home's database does less) while the copy is current. |
| away (past `delegate_unreachable_grace_seconds`), refusing or switched off | A notice: "This machine can't reach its home… it works from its own copy of the home's library", with how recent the copy is. Pages read, videos this machine can read play, TV plays from this machine, **signing in works** (on this machine only), **signing out works**. Changes are refused (503 `home_unreachable`, under the notice). |
| back | The notice clears. A sign-in made while it was away keeps reading and playing for `offline_session_grace_minutes`; a change asks for a sign-in with the home ("Sign in again: you signed in on this machine while its home was away…"). The sign-outs made meanwhile are handed to the home, and the sessions opened meanwhile are written to its history. |

A node with no usable copy (still copying it, a home that keeps none, a copy too far behind)
behaves as a node did before #145: its own routes answer, anything else is 503 with the full
"cannot reach its home" page, and sign-in says it waits for the home.

## Design

### The copy (logical replication)

- The home's Postgres publishes; each full node's own Postgres subscribes. The node's database
  is a `pgbundle` cluster where a home keeps its bundled one (`<state>/postgres`), so the node
  can become the home. It is **migrated by the node's own Maktaba** (`maktaba-api migrate`), not
  copied: its schema is this machine's code's.
- **What is copied:** every table of `public` but `cluster_copy_excluded()` — the secrets
  (`home_secrets`, the TV stream and push keys, simulcast keys, refresh, reset and pairing
  tokens, SSO codes, idempotency keys, the licence, devices' push tokens, the audit log), the
  machines' own rows and caches, and each database's own migration state. The node's own
  `cluster_nodes` row is published to it alone, without the home's key for it or its sealed
  answer (a row filter and a column list). The node's routes read the tables the node role may
  read, so every one of those is copied; a test fails when a new table is neither reviewed for
  the copy nor left out (`api/internal/cluster/copy_tables_pg_test.go`).
- One **publication per node** (`maktaba_copy_<node id>`), kept current with the tables there
  are by `cluster_copy_publication` (a table left out leaves it; one with no primary key gets
  `REPLICA IDENTITY FULL`, without which the home could not update it once published); a
  **slot per node** of the same name, made by the subscription; a **replication role per
  node** (`maktaba_rep_<node id>`, in `maktaba_replicas`, which reads only the published tables
  and the published columns of `cluster_nodes`).
- Same Postgres major version on both ends (16, the version Maktaba.app bundles: 16.15). The
  node uses the Postgres this machine already has for Maktaba (Maktaba.app's payload,
  `[postgres] home`, `MAKTABA_POSTGRES_HOME`) when it is of that major version, else fetches the
  pinned build for its platform once (`cmd/maktaba-server/internal/pgbin`: theseus-rs builds,
  size and SHA-256 pinned; the Linux builds need glibc 2.34 and OpenSSL 3). Logical replication
  carries rows, not pages: the home and the node may run different systems (a Linux home and
  a Windows node were tested).
- Every bundled Postgres runs with `wal_level=logical` and room for every machine's slot, sender
  and parallel first copy. The bundled Postgres keeps its TLS certificate and key in the keys
  directory.

### The home's side (automatic)

- The bundled Postgres's cluster bootstrap (superuser, once) gives the app role `CREATEROLE`,
  `REPLICATION`, `pg_monitor`, `pg_signal_backend`, and makes `maktaba_replicas`, which the app
  role administers without taking its rights. Its `pg_hba.conf` (rewritten at every start) lets
  in from the LAN only the machines' roles and their replication roles, over TLS.
- Slot 0144 (`shared/db/migrations/0144_node_replicas.sql`):
  - `cluster_node_replica_grant(node, password, fresh)` — a machine asks for its copy **over its
    own database connection**, for its own role only (`cluster_node_guard`). It makes or updates
    the replication role with the password the machine chose, brings its publication up to date
    (`cluster_copy_publication`), drops an inactive slot when `fresh`, and returns the role, the
    slot (and whether it exists), the publication and its tables, the home's schema version,
    system identifier, server version and CA. It refuses a home whose `wal_level` is not
    `logical`.
  - `cluster_replica_beat(at, ca_pem)` — the home's clock, written every 5 s while a machine
    follows (`api/internal/cluster/copies.go`), and its database's CA.
  - `cluster_nodes.replica` + `cluster_node_report_replica` — what each machine reports of its
    copy.
  - The machines' group made late, for a home whose migrations ran before its app role could
    make roles: created with the privileges slots 0103–0143 give it, proven equal to the normal
    ones (`worker_baseline_pg_test.go`).
- `cluster.Copies` (home API): the beat, the CA published, and **the cap**: an inactive slot that
  holds more than `rules.cluster.replica_wal_keep_gb` of changes is dropped — the home's disk
  never fills for a machine that is off; that machine subscribes anew when it is back.
- Revoking a machine ends its copy (its replication role may no longer sign in, its sessions are
  ended, its slot and publication are dropped); removing it drops the role.

### The node's side (`cmd/maktaba-server/internal/nodecopy`, run by `serve`)

The manager runs for as long as the supervisor does, on every full node — a node joined before
this epic converts at its first start on this version:

1. fetch (or find) the Postgres programs; make this machine's database (a database of its own
   from before it joined is moved aside, never emptied), start it as the supervisor's on-demand
   `copy` role, make the read-only role this machine's programs read it as, and migrate it;
2. ask the home (`cluster_node_replica_grant`); pin the CA it publishes (first grant), write a
   passfile in the keys directory (the password never goes into the data directory);
3. subscribe (tables emptied, then copied in parallel, `streaming = on`) when there is no copy
   yet, the home's database is another one (system identifier), this machine's Maktaba was
   updated, the home dropped the slot, or the home's changes keep failing to apply for ten
   minutes; otherwise keep the subscription current (its connection, new tables, enabled);
4. pause the subscription while the home's schema is ahead of this machine's (it resumes once
   this machine is updated and migrated);
5. watch it: following (the beat applied less than 30 s ago and every table ready), copying,
   behind, starting, incompatible; write the state to `<data>/node/copy.json` (the node's
   `/api/cluster/self`) and to the home (`cluster_nodes.replica`, the Machines page);
6. the home refusing this machine three times over three minutes (revoked) ends the subscription
   and wipes the copy and its secrets.

The copy's directory is readable by this account only (0700 on Unix; on Windows its ACL keeps
only this account and SYSTEM). `MAKTABA_NODE_COPY=off` exists for tests that run a node without
Postgres programs; it is not a setting.

### Reads and writes on a node (`api/internal/cluster/nodedb`)

The node's API has one `*sql.DB` over a pair of servers. A statement whose request is a read the
node serves itself (`GET`/`HEAD` of a `local` or `any` route) goes to the copy while it answers
and, with the home there, while the copy is within `copy_reads_max_lag_seconds` of it (else the
read goes to the home's database: a change of rights, a removal or a new video is not read
stale); anything else — a write, a transaction, a statement no request marked — goes to the
home's. With the home not answering (its API or its database), everything goes to the copy
(reads answer; a write fails as it would have). **Read-your-writes:** a change sends its
author's reads to the home for 5 s. The node's role check and its reports always go to the
home's database.

The connections to the home's database give a statement up the moment its request ends, even
on a connection that hangs (`pgdsn.BoundedConnector`: lib/pq alone waits for as long as the
socket lives, and a home switched off answers nothing, not even the cancel). Only the database
failing is an outage: an error it answered with, or a request that went away, is not. A server
that cannot take a connection now (SQLSTATE 08, 53, 57P01–57P03, 57P05: starting up, too many
clients, shutting down) is one that does not answer, at the node's start and later; the node's
API stops only for a role found able to run DDL.

### Sign-in and sign-out (`api/internal/cluster/nodeauth`)

- While the home answers: unchanged (introspection, 30 s / 5 s caches, pushed revocations). A
  revocation pushed while an introspection is under way is not undone by caching its answer.
- With the home away, a credential is looked up **on the copy**, by the home's own code
  (`clusterauth.Copy` runs `Home.Resolve`, the password check of `/api/auth/login`), so expiries,
  revocations, password changes and library rights are exactly the home's as of the copy's last
  applied change; a revocation the home pushed lately is honoured on the copy until it has it.
  A bearer access token is verified with the home's cached keys and its user checked on the
  copy, under the same rules. **Freshness rule:** the copy answers only if it had applied the
  home's changes up to no more than `offline_copy_max_lag_minutes` before the home was last
  heard (that moment is kept on disk, `home-last-answered`).
- A home switched off leaves what is sent to it hanging: once it has not answered a probe, the
  session probe, the sign-in and the sign-out are answered on the node at once, and other calls
  to it wait 5 s at most instead of 30.
- **Node-local sessions.** A sign-in with the home away is checked against the copy and makes a
  session valid on this machine only: cookie `nls_…`, stored by the hash of its id and of its
  CSRF token in `<data>/node/local-sessions.json` (0600), lasting `offline_session_hours`; once
  the home answers again they end within `offline_session_grace_minutes`, and a change through
  the home's database asks for a sign-in with the home (401 `signin_required`). The home's
  lockout is mirrored on the node (five failures in 15 minutes, 423).
- **Sign-out with the home away is real.** The cookies are cleared, the session is denied on
  this machine at once (though the copy still says it is live), and the home is told once it
  answers (`POST /api/cluster/delegate/signout`, node-signed: sessions by the SHA-256 of their
  id; "sign out everywhere" by user and moment, refresh tokens too). Until the home has it,
  nothing of that session is sent there. The node keeps them in `<data>/node/signed-out.json`,
  by hash only, saved before it answers. It checks the CSRF double submit, as every change does.
- Node state files are flushed to disk before they replace the old one, and the API waits for
  the last save of `home-last-answered` at shutdown.

### Playback and TV

- **VOD:** the API's and the streaming service's lookups (a video's library, length, path, probe
  facts; the share map) read the copy while the home's database does not answer. A session
  opened meanwhile is recorded on the node (the copy is read only) and its `streaming_sessions`
  row is written once the home's database answers again, so the progress the player then reports
  (the home's route) finds it. A video this machine cannot read, with the home away, is
  "unavailable offline", whether the home refused or never answered.
- **TV:** a channel's position comes from its schedule and the clock (`channel_programs`,
  `tv_live_gen()`), and the copy has both. The node's streaming service runs **its own station**
  on the copy (`streaming/offline_tv.go`), answering this machine's web port only. Not served
  offline: the HDHomeRun tuner, XMLTV and the lineup (the home's), and the art the home serves.

### Jobs

The node's pipeline pauses while the home's database does not answer: it claims nothing, lets
jobs in flight finish, and stops them before the claim lease lapses — measured from the last
beat the home answered, checked every 2 s (a home that hangs leaves each beat waiting its
timeout), 70 s for the default 90 s lease. A job's late write (heartbeat, progress, its end)
changes nothing once another worker holds its claim (`db/claim_owner.py`). It resumes on its own.

## Switching the home

The user asked for a way "to switch the main node, to a joined node", carrying "all external
connections, plex, cloudflare to that node". A **planned switch** is one button on the Machines
tab ("Make B the home", after a confirm step) and runs by itself to the end. A is the old home,
B the machine taking over, C the other machines.

1. **Check.** Refused, with the reason on the tab, unless B is approved, a full machine, its copy
   following and reporting, at A's schema, its machine agent connected, no switch under way, and
   A runs Maktaba's own Postgres.
2. **Pause.** Every machine's jobs drain (each machine's own drain kept, to give back), the
   home's own pipeline too.
3. **Freeze.** A's database is read-only for every session of the app's roles
   (`default_transaction_read_only` on the database, the open sessions ended); its last write
   sets the switch's row (`cluster_switchovers`) to `frozen`. That row is the marker: a copy
   holding it has everything A wrote. This machine's own superuser connects read-write despite a
   freeze (`pgbundle.Bundle.SuperURL`): the thaw and the demotion need it.
4. **Kit.** What replication leaves out, gathered on A: the home-only tables as rows (secrets,
   machines, tokens, devices, audit, …), the sequences, the machines' database roles with their
   SCRAM verifiers (their passwords stay), and the key files (the secrets key, the cluster key,
   the database CA and its key, the Hugging Face token, an operator's JWT keys). It is sealed for
   B alone: X25519 between A's cluster key and B's machine key, HKDF-SHA256 with the switch's id,
   ChaCha20-Poly1305 per 1 MiB chunk under its index, the last one marked (a chunk dropped, moved
   or added is refused). It travels over the machine-agent channel, never through the replica.
5. **Promote.** B checks it holds the marker, stops its subscription, loads the kit with triggers
   off, sets each sequence past A's value, and makes every machine's role, publication and slot
   before its first write, so every copy can follow it without copying again. It starts term
   N+1 (`cluster_terms`) and seals for every machine a notice of its address (below). Its
   `server.toml` becomes a home's (its Postgres on, on the LAN with TLS from the same CA), its
   `node.json` is put aside, and it starts again as the home.
6. **Move the machines.** A waits for B to answer as the home, then tells each machine where it
   is, with B's CA: a copy holding the marker follows B from its slot (nothing copied), one
   without it copies anew. A machine that was off asks its home when it is back; A answers as a
   machine now and hands it the notice B sealed for it (`GET /api/cluster/moved/{node}`, from any
   copy: the notices are in the switch's row). The machine opens it with its own key and the
   cluster's public key, so a notice it did not get from its cluster does not open, and follows.
7. **The old home becomes a machine.** A drops its publications and slots, empties its home-only
   rows, follows B from its slot, writes its own `node.json` (its machine identity was chosen in
   the kit), puts its cluster key aside and starts again as a machine. Until then its database
   stays frozen for its app's roles (the subscription applies all the same), so its home
   processes, still running, write nothing that would collide with B's changes; its copy ends
   the freeze when it starts.
8. **Finish, on B.** B marks each machine as it follows, moves and checks each outside service,
   and once every machine follows and no service is still moving (or after an hour) gives the
   machines their jobs back and calls the switch done.

A switch that fails before the promotion is undone: A writable, the drains as they were, the
home's jobs back, the reason on the tab. Past the promotion nothing goes back; the old home and
the machines keep trying to follow B until it answers. Switching back is a switch the other way.

**On the tab:** the term and since when this machine is the home; the machines that could take
over, each with why it cannot now; a switch under way step by step; then each machine
(following, copying anew, off) and each outside service (moved, being moved, failed, needs
you), with the one action a person takes. `GET /api/cluster/self` carries the term.

### Outside services

| Service | What the switch does |
|---|---|
| Secrets (Plex token, Cloudflare Stream, simulcast and web-search keys, …) | They move with the secrets key; each is checked to open on B. |
| Plex Live TV | The `plex_livetv` stage moves Maktaba's tuner to B's address. Plex keeps the device (the same DeviceID) and takes the new address, or else the old tuner leaves its DVR and Plex and B's is added and joined as a registration does. The guide at A's address gives way to B's, the channel map is pushed and the guide reloaded. Being moved until the stage answers. A tuner address set by hand at another machine (`rules.plexlive.tuner_url`) needs a person. |
| Hugging Face | The token moves with the kit. |
| JWT keys | An operator's `MAKTABA_JWT_*_PEM` move with the kit; B's supervisor hands them to its API. |
| YouTube (yt-dlp `cookies_from_browser`) | A browser profile is its machine's: someone signs in to YouTube in a browser on B. |
| Model runtime (Ollama on the machine) | Moved when B runs one; else B needs one, or its address in Settings. |
| The machines' database TLS | B serves it from the same CA (it came in the kit); every machine pins the CA B sends. |

## Rules (`rules.cluster`, on the settings page)

| Rule | Default | Meaning |
|---|---|---|
| `delegate_unreachable_grace_seconds` | 30 | After this long without an answer from the home, a node stops asking it per request and answers from its copy. |
| `offline_copy_max_lag_minutes` | 10 | A copy answers with the home away only if it had followed the home up to this close to when the home was last heard. |
| `copy_reads_max_lag_seconds` | 30 | With the home there, a node reads from its copy only while the copy is this close behind it. |
| `offline_session_hours` | 12 | A sign-in made on a node while the home is away lasts this long. |
| `offline_session_grace_minutes` | 30 | …and ends this long after the home answers again. |
| `replica_wal_keep_gb` | 20 | The home keeps this much of its changes for a machine behind; past it the machine subscribes anew. |

## Security

- **What a node holds.** The copy is the home's database without its secrets: password hashes
  (argon2id), web session ids, personal access token hashes and the library — not the secrets
  table, the push and stream keys, refresh tokens, other machines' rows or the machines' keys.
  The replication role reads only what is published to it. The copy's files are readable by the
  node's own account only; revoking the machine stops its subscription at once and wipes its
  copy at its next check.
- **On the wire.** The subscription and the grant run over TLS that verifies the home's CA
  (`verify-ca`, pinned at the first grant). With `[postgres] tls = "off"` they run in the clear;
  the setting's help says so.
- **Node-local sessions and sign-outs** are stored hashed and never carry a credential; the copy
  cannot be written: nothing a node does offline changes the home's data but the sign-outs and
  sessions it hands over once the home answers.
- **A switch of the home.** Only an admin starts it. The kit (the secrets key, the cluster key,
  the CA's key) is sealed to the machine taking over and travels over the authenticated machine
  channel, never through the replica; it is kept on neither side once loaded. A machine follows
  a new home only on the home's own command over that channel, or on a notice sealed to it by the
  cluster key; the copy pins the CA that came with it.

## Failure modes

| Situation | What happens |
|---|---|
| Home off for days, node on | The copy stays at its last applied change; sign-ins keep working (freshness is measured at the outage's start); the slot holds WAL on the home only while the home runs. |
| Home switched off (connections hang) | Detected by the probes and the bounded connections; nothing waits on it past a request's bound. |
| Node off for long | The home drops its slot past the cap; the node subscribes anew on its next start. |
| Home restored from a backup / re-initialized | New system identifier: the node subscribes anew. |
| The home updated first | The node's subscription pauses until this machine is updated too (its schema follows its code). |
| Home's database starting up at the node's start | The node starts on its copy and checks its role once the home answers. |
| Copy being made | Reads go to the home; with the home away meanwhile, the node serves its own routes only. |
| Machine revoked | Subscription refused at once; copy and secrets wiped within ~3 minutes. |
| A switch fails before the promotion | Undone: the old home writable, the jobs back, the reason on the tab. |
| The new home slow to start after its promotion | The old home and the machines keep trying to follow it. |
| A machine off during a switch | When back, its old home hands it the new home's notice; it follows, copying anew. |
| A machine whose Plex is unreachable from the new home | The tuner stays where it was; the stage retries; the tab says being moved. |

## Tests

- `api/internal/cluster/copies_pg_test.go`: the grant, the publication and what the replication
  role may read, refusals, the beat, the CA, the cap, revoke and remove, on a real Postgres 16
  set up as the bundled home is. `copy_tables_pg_test.go`: every table decided for the copy.
- `cmd/maktaba-server/internal/nodecopy/copy_pg_test.go`: **two Postgres servers on 127.0.0.1** —
  a home made by `pgbundle.Prepare` and migrated by `maktaba-api`, and the copy kept by the
  manager alone: subscribes over TLS, follows, holds no secrets nor other machines' rows or keys,
  refuses writes, subscribes anew after the slot is dropped, is wiped on revocation. Opt-in:
  `MAKTABA_TEST_PG_BIN`.
- `api/twomachine_e2e_test.go`: two `maktaba-server serve` machines; with `MAKTABA_TEST_PG_BIN`
  the node's supervisor makes its copy (a Linux Postgres home, the node's copy on the pinned
  Windows build), then the home is switched off (its API and its database reached through
  proxies that hang): reads, the session probe, playback, a node-local sign-in, a sign-out,
  refused changes; then its database too: reads and playback from the copy, also after a
  restart.
- Switching the home: `cmd/maktaba-server/internal/switchover/switchover_pg_test.go`, **three
  Postgres servers on 127.0.0.1** (`MAKTABA_TEST_PG_BIN`): a planned switch (the freeze, the
  marker on both copies, the kit sealed and opened, the promotion, the old home and another
  machine following without copying again, the sequences past the old home's, the secrets and
  roles on the new home, the old home writing nothing of its own until it starts as a machine),
  an emergency promotion's database steps (the fence, the changes kept aside), and a thaw from a
  session opened after the freeze. `api/internal/cluster/switchover`: the steps in order, the
  undo, the refusals, Plex Live TV's move, the notices (`MAKTABA_TEST_PG_URL`).
  `api/switchover_e2e_test.go` (`MAKTABA_E2E_SWITCHOVER=1`): three `maktaba-server serve`
  machines on the pinned Postgres 16.15: A the home, B and C its machines; C is switched off, the
  admin switches the home to B; B takes over without copying again, A follows it as a machine,
  C follows it by itself once back, the secret opens on B, B's change reaches A, and the owner
  signs in on both. The pipeline's `test_plex_livetv.py`: the tuner moved in place, re-added,
  with a DVR of its own, and kept when Plex cannot reach the new home. The web's
  `SwitchHomeCard.test.tsx`.
- Unit tests: `nodeauth` (copy-first answers, freshness, node-local sessions and sign-outs,
  revocations in flight, bearer tokens, lockout, grace, a hanging home), `nodedb` (routing per
  statement), `pgdsn` (bounded connections, on a real Postgres behind a hanging proxy),
  `nodecopy` (names, secrets, decisions), `streaming` (write-back, outage classification), the
  pipeline's lease and claim guard, the web outage pages.

## Follow-ups

- Emergency promotion (the home gone for good), from the Machines tab of a machine with a copy,
  with a warning: the database steps are built and tested (the fence, the changes kept aside);
  the orchestration waits on the user's choices of where a standby kit is kept and what becomes
  of the old home's writes it kept aside.
- The cloud relay (`maktaba-cloudlink`) moving with the home: `maktaba-server` does not run it
  yet; how it should is the user's choice.

- Native apps: offline sign-in (an access token from the node's own key, refreshed by the home on
  its return).
- TV art offline (serve the station the art the node last fetched).
- Converting this PC's live node (`E:\tv\maktaba-local`) happens after the merge, with the user's
  OK; it is automatic once it runs this version.
