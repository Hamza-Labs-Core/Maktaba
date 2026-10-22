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
  answer (a row filter and a column list). What this machine's own code reads with the home away
  is copied, tables the machines' role may not read on the home among them (`users` with their
  password hashes, personal access token hashes): the copy is read by this machine's programs
  only, under a read-only role of its own. Web sessions are not copied as they are: the copy
  gets `web_session_keys` (the SHA-256 of each session's id and of its CSRF token, kept by a
  trigger on `web_sessions`), and a node checks a cookie by its hash; `web_sessions` itself
  travels only in a switch's kit. A test fails when a new table is neither reviewed for the copy
  nor left out (`api/internal/cluster/copy_tables_pg_test.go`).
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

- The bundled Postgres's cluster bootstrap (superuser, once per revision: an older home runs it
  again) gives the app role `CREATEROLE`, `REPLICATION`, `pg_monitor`, `pg_signal_backend`, the
  right to set `max_slot_wal_keep_size` and to reload the configuration, makes
  `maktaba_replicas`, which the app role administers without taking its rights, and makes the
  SQL functions that make, read, move or drop a slot the app role's alone in the app's database
  (below). On a server older than Maktaba's own Postgres 16 it leaves out what that server
  lacks (a parameter's grant before 15, a membership without its rights before 16); copies
  need 15 at least (a machine's row is published by a row filter, without its keys by a column
  list): the grant refuses an older one.
- **What a machine can read.** A machine's replication role has `REPLICATION`, which lets a role
  make a slot with any output plugin the server has and decode every table through it, past
  its publication and its grants. So:
  - Maktaba's Postgres has **no output plugin but `pgoutput`**: the Mac and Linux payloads strip
    the others (`test_decoding`) and their checks fail on one; `pgbin` strips the installs it
    fetches (and ones fetched before). `pgoutput` streams only what a publication publishes,
    and every publication on the home's database is no more than a machine's own: each
    machine's (`maktaba_copy_<id>`: the copied tables and its own row), an emergency's fork
    (the copied tables).
  - The slot functions are not the replication roles' to call: a copy follows over the
    replication protocol.
  - **The home drops every 5 s any slot on its database that is not Maktaba's** (`cluster.Copies`):
    one made with another plugin, one named for a machine that may not have a copy, one of a
    name Maktaba does not make (a machine's, an emergency's fork, a table's first copy), and on
    Maktaba's own Postgres an idle physical one; one in use has its connection ended first
    (`pg_terminate_backend`) and is dropped once free, and one it cannot end or drop is said
    once, with what to do. It also reclaims what such a slot would hold.
  - **A home on an operator's own Postgres** may carry output plugins besides `pgoutput`
    (test_decoding, wal2json), and its user may lack `pg_signal_backend`: it keeps **no copies**
    unless the admin turns on `rules.cluster.copies_on_operator_postgres` (off by default, read at
    every beat; a beat that cannot read it changes nothing). Until then the machines' group may
    not call `cluster_node_replica_grant` (its owner revokes EXECUTE, and gives it back with the
    rule on or on Maktaba's own Postgres), a machine that asks is told why (its copy shows as
    unavailable), the copies the machines have end as a removal ends them (role, connections,
    slot, publication), each refusal is logged once, and serve warns at each start of such a
    home. With the rule on, the sweep is the only guard: what a role could decode in the seconds
    before it is the residual risk of such a server, which its operator removes with the
    plugins.
- **What the machines reach of it follows the cluster** (`pgbundle.WatchReach`, every 30 s): it
  listens beyond this machine, and its `pg_hba.conf` lets anyone in from off it, only while a
  machine may connect (one is approved, or joins are open). Then it lets in only the machines'
  roles and their replication roles, into the app's database, over TLS, from the networks this
  machine is on (`samenet`) and each approved machine's own address (a machine over a VPN);
  `[postgres] machines_from` can name networks instead (those only) or `all`. There is no
  `replication` line: a copy is a logical subscription, and a physical one (the whole cluster,
  every row and key) is never let in. `pg_hba.conf` follows with a reload, the listen address
  by starting Postgres again (the supervisor's `Restart`); `[postgres] listen = "loopback"`
  never opens. A machine promoted by a switch opens at its first start as the home.
- **The machines verify the home's database** (`verify-ca`) by its CA, which reaches them three
  ways: the join's answer (`database_ca`, `database_tls`), the machine-agent channel
  (`home.database`, sent at every connection while it changes, for a machine joined before it
  had one), and a switch's command or notice. Each machine keeps it beside its `node.json`
  (`home-database-ca.crt`); its connections to the home's database use it, and its copy waits
  (state unavailable, "cannot verify its home's database yet") until it has one. Nothing is
  trusted on first use: a grant's answer never sets it. A home on another database (an
  operator's own) tells its machines it has no TLS when it has none at all (`ssl` off: every
  connection to it is plain); one with TLS of its own has the operator's CA, which the home does
  not hold, and its machines' copies wait (below, follow-ups).
- Slot 0147 (`shared/db/migrations/0147_node_replicas.sql`):
  - `cluster_node_replica_grant(node, verifier, fresh)` — a machine asks for its copy **over its
    own database connection**, for its own role only (`cluster_node_guard`), and only an approved
    full machine (its row read `FOR SHARE`, so a revocation waits for the grant or follows it;
    else SQLSTATE `MK403`). It makes or updates the replication role with the SCRAM-SHA-256
    verifier of a password the machine chose (the password never leaves the machine), brings its
    publication up to date (`cluster_copy_publication`), drops an inactive slot when `fresh`, and
    returns the role, the slot (and whether it exists), the publication and its tables, the
    home's schema version, system identifier and server version. It refuses a home whose
    `wal_level` is not `logical`.
  - `cluster_replica_beat(at, ca_pem)` — the home's clock, written every 5 s while a machine
    follows (`api/internal/cluster/copies.go`), and its database's CA.
  - `cluster_nodes.replica` + `cluster_node_report_replica` — what each machine reports of its
    copy.
  - The machines' group made late, for a home whose migrations ran before its app role could
    make roles: created with the privileges slots 0103–0143 give it, proven equal to the normal
    ones (`worker_baseline_pg_test.go`).
- `cluster.Copies` (home API): the beat, the CA published, and **the cap**: an inactive slot that
  holds more than `rules.cluster.replica_wal_keep_gb` of changes is dropped — the home's disk
  never fills for a machine that is off; that machine subscribes anew when it is back. Postgres's
  own cap (`max_slot_wal_keep_size`) is kept at the rule too, so it holds while the API is down.
- Revoking a machine ends its copy: the machine is revoked first (a grant after that is
  refused), then its replication role may no longer sign in, its sessions are ended, its slot
  and publication are dropped; a revocation that failed half way is done again by revoking
  again. Removing it drops the role.

### The node's side (`cmd/maktaba-server/internal/nodecopy`, run by `serve`)

The manager runs for as long as the supervisor does, on every full node — a node joined before
this epic converts at its first start on this version:

1. fetch (or find) the Postgres programs; make this machine's database (a database of its own
   from before it joined is moved aside, never emptied), start it as the supervisor's on-demand
   `copy` role, make the read-only role this machine's programs read it as, and migrate it;
2. wait until it can verify the home's database (its CA, above); ask the home
   (`cluster_node_replica_grant`) with the verifier of a password it chose, kept in a passfile
   in the keys directory (never in the data directory);
3. subscribe (tables emptied, then copied in parallel, `streaming = on`) when there is no copy
   yet, the home's database is another one (system identifier), this machine's Maktaba was
   updated, the home dropped the slot, or the home's changes keep failing to apply for ten
   minutes — first pausing the old subscription and waiting for its worker to stop, so the slot
   is free; otherwise keep the subscription current (its connection over `verify-ca` with
   keepalives and a timeout, new tables, enabled);
4. pause the subscription while the home's schema is ahead of this machine's (it resumes once
   this machine is updated and migrated);
5. watch it: following (the beat applied less than 30 s ago and every table ready, measured on
   the home's clock; or, with the beat late because the home's API is not running, its slot
   within a WAL segment of the home's WAL position, as each report measures it), copying,
   behind, starting, incompatible; write the state to
   `<data>/node/copy.json` (the node's `/api/cluster/self` shows the public part) and to the home
   (`cluster_nodes.replica`, the Machines page) when it changes, at most once a minute otherwise;
6. the home refusing this machine three times over three minutes (revoked: `MK403`, or its role
   may not sign in) ends the subscription and wipes the copy and its secrets; approved again, the
   machine copies anew by itself.

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
(reads answer; a write fails as it would have). **Lag is measured on the home's clock**: the
node measures how far its clock is from the home database's `now()` every 30 s, so a machine
whose clock is off neither reads a stale copy nor gives up a current one. **Read-your-writes:**
after a change, its author's reads go to the home until the copy has applied the home's beat of
at least the change's moment on the home's clock (plus a second), for ten minutes at most. The
node's role check and its reports always go to the home's database.

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
  the home's database asks for a sign-in with the home (401 `signin_required`); a sign-in made
  with the home answering again ends the node-local session and goes to the home. A user who
  must change their password, or whose sessions the home revoked lately, cannot sign in on the
  node (503 `password_change_waits` asks them to wait for the home); a revocation the home pushes
  ends that user's node-local sessions. The home's lockout is mirrored on the node (five
  failures in 15 minutes, 423); spraying other names never unlocks a locked one.
- One probe the home misses is not an outage: the node answers from its copy after two misses in
  a row, or after a request found the home's database not answering.
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
  opened meanwhile is recorded on the node (the copy is read only), kept on disk
  (`<data>/node/stream-sessions.json`, so a restart of the API keeps it), and its
  `streaming_sessions` row — and its closing, when it was closed meanwhile — is written once the
  home's database answers again, so the progress the player then reports (the home's route)
  finds it. A video this machine cannot read, with the home away, is
  "unavailable offline", whether the home refused or never answered.
- **TV:** a channel's position comes from its schedule and the clock (`channel_programs`,
  `tv_live_gen()`), and the copy has both. The node's streaming service runs **its own station**
  on the copy (`streaming/offline_tv.go`), answering this machine's web port only. Not served
  offline: the HDHomeRun tuner, XMLTV and the lineup (the home's), and the art the home serves.

### Jobs

The node's pipeline pauses while the home's database does not answer: it claims nothing, lets
jobs in flight finish, and stops them before the claim lease lapses — measured from the last
beat the home answered, checked every 2 s (a home that hangs leaves each beat waiting its
timeout), 70 s for the default 90 s lease. A job's late write (heartbeat, progress, a
transcript's segment, its end) changes nothing once another worker holds its claim
(`db/claim_owner.py`; `commit_segment` takes the worker). It resumes on its own. The paused page
reloads itself every two minutes and once the home answers again.

## Switching the home

The user asked for a way "to switch the main node, to a joined node", carrying "all external
connections, plex, cloudflare to that node". A **planned switch** is one button on the Machines
tab ("Make B the home", after a confirm step) and runs by itself to the end. A is the old home,
B the machine taking over, C the other machines.

1. **Check.** Refused, with the reason on the tab, unless B is approved, a full machine, its copy
   following and reporting, at A's schema, its machine agent connected, no switch under way, and
   A runs Maktaba's own Postgres. One switch at a time: the check and the switch's row are one
   step, and the database refuses a second row under way (`cluster_switchovers_one_open`), so two
   starts at once (also on two processes) run one.
2. **Pause.** Every machine's jobs drain (each machine's own drain kept, to give back), the
   home's own pipeline too.
3. **Freeze.** The machines' database roles are kept out of A's Postgres (`pg_hba.conf`, a
   reload: a session may set itself read-write, a role not let in has none; their copies'
   replication roles stay in), then A's app roles lose their rights to write its tables
   (`dbfence`: INSERT, UPDATE, DELETE, TRUNCATE on every public table from every role but the
   superusers, recorded in the same transaction in a schema no app role may use, `maktaba_fence`,
   so a thaw gives back exactly those; a session's own `BEGIN READ WRITE`, which lib/pq sends,
   overrides the database's `default_transaction_read_only`, which is set too; while they are
   taken, an event trigger of the database, `maktaba_fence`, refuses every DDL command, GRANT and
   REVOKE of a role but the superusers: the tables' owner could give its rights back to itself,
   and a migration's DDL, in a transaction or not, would stick); the app's open
   sessions are ended, waiting for each to go, and only then does its last write set the switch's
   row (`cluster_switchovers`) to `frozen`: nothing the app wrote commits after it. That row is the
   marker: a copy holding it has everything A wrote. This machine's own superuser writes despite a
   freeze (`pgbundle.Bundle.SuperURL`): the thaw and the demotion need it, and a copy's
   subscription applies as its owner, the superuser (`run_as_owner` on Postgres 16).
4. **Kit.** What replication leaves out, gathered on A: the home-only tables as rows (secrets,
   machines, tokens, devices, audit, …), the sequences, the machines' database roles with their
   SCRAM verifiers (their passwords stay), and the key files (the secrets key, the cluster key,
   the database CA and its key, the Hugging Face token, an operator's JWT keys). It is sealed for
   B alone: X25519 between A's cluster key and B's machine key, HKDF-SHA256 with the switch's id;
   each seal draws a random salt (its first chunk carries it) and seals under a key derived from
   the kit's key and it, ChaCha20-Poly1305 per 1 MiB chunk under its index, the last one marked
   (a chunk dropped, moved or added is refused; no two seals share a key). It travels over the
   machine-agent channel, never through the replica.
5. **Promote.** That the promotion is asked is recorded first (on disk, synced), or it is not
   asked. B checks it holds the marker, that every table of its copy has its first copy done, and
   the CA the machines will verify it by, then loads the kit's machines (each machine's copy has
   its own row already: their load is not published to it), then makes every machine's
   publication and slot before anything else is written, so every copy can follow it without
   copying again; then, **in one transaction**, stops its subscription, reads the switch's row
   in its copy (`cancelled` or `failed`: A called it off, and it is not promoted), loads the rest of the kit
   with triggers off, sets each sequence past A's value, makes the machines' roles, registers A
   and starts term N+1 (`cluster_terms`). A promotion that fails leaves B's copy as it was (the
   transaction undone, the machines' rows and the slots and publications it made removed), calls
   the switch off there and starts B again as A's machine. Through, it seals for every machine a
   notice of its address (below), keeps its answer, removes the kit, makes its `server.toml` a
   home's (its Postgres on, on the LAN with TLS from the same CA, the CA required), puts its
   `node.json` aside, and starts again as the home.

   **Once A asked for the promotion it is never made writable again but on B's word.** Its
   answer lost (a timeout, the channel gone), A stays frozen and asks B (`switch.status`): B says
   it is the home (with what it answered), that the promotion runs, or that it called the switch
   off, which it does when it was not promoted, so it never is after; or A reads, at B's address,
   the notice B sealed to A's identity once it is the home. Only "called off" undoes the switch;
   B, calling it off (or failing its promotion), drops the kit it was sent, and never finishes a
   promotion once it is called off there. Whether B promoted is read from its copy; a copy that
   does not answer says "not known", never "called off".

   **An admin can call a switch off** ("Call the switch off" on the tab, `POST
   /api/cluster/switchover/{id}/cancel`): before the promotion is asked it is undone at its next
   step (the promotion's start and the call share a lock, so it never leaves after); after, B is
   asked, and calls it off when it has not promoted (a B that is, or is becoming, the home is not
   called off). A B that does not answer is called off only with its name typed: should it be the
   home after all, A hears its newer term when it is back and fences itself (what it wrote
   meanwhile is kept aside, as after an emergency): for a day after, A keeps asking B
   (`switch.status`) and its supervisor looks every minute for a notice of a newer term
   (`check-term`) under each identity one may be sealed to, its planned switches' old-home
   identity among them once their promotion was asked. The switch ends `cancelled`. A machine
   starting with no switch open removes the kit chunks of one called off or done.
6. **Move the machines.** A waits for B to answer as the home, then tells each machine where it
   is, with B's CA: a copy holding the marker follows B from its slot (nothing copied), one
   without it copies anew; one told again follows already. A machine that was off asks its home
   when it is back, then the standby homes and every other machine it knows (the home tells each
   where the others are: node.json `peers`); any of them answers, a machine now, with the notice B
   sealed for it (`GET /api/cluster/moved/{node}`, from any copy: the notices are in the switch's
   row), so the old home may be retired. The machine opens it with its own key and the cluster's
   public key, so a notice it did not get from its cluster does not open, and follows.
7. **The old home becomes a machine.** A stops serving its machines (their publications and
   slots: following B it would pass B's changes on to one still following it), follows B from its
   slot, and **only once its database receives B's changes** empties its home-only rows (its own
   row as B's machine kept), writes its own `node.json` (its machine identity was chosen in the
   kit), wipes the home's keys (the secrets key, the cluster key, the CA's key, JWT keys, the
   relay's credentials, the kit, its identity's file: overwritten, then removed) and starts again
   as a machine. With B unreachable it stays as it was, frozen, and is asked again. Until then
   its database stays frozen for its app's roles (the subscription applies all the same), so its
   home processes, still running, write nothing that would collide with B's changes; its copy
   ends the freeze when it starts, giving back the app's rights to write.
8. **Finish, on B.** B marks each machine as it follows (on its own clock: from the term's start),
   moves and checks each outside service, and once every machine follows and no service is still
   moving (or after an hour) gives the machines their jobs back and calls the switch done.

A switch that fails before the promotion is asked is undone: A writable, the drains as they
were, the home's jobs back, B told to call it off and drop the kit, the reason on the tab. A thaw
that fails is asked again until A is writable, also after a restart (the record says it is
owed). Past it nothing goes back but on B's word; the
old home and the machines keep trying to follow B until it answers. **A restart of A's API or
supervisor mid-switch** takes it up: A's steps are recorded through its supervisor at each one
(`<state>/switchover/<id>/progress.json`, and the switch's row while A writes it); at the next
start one whose promotion was not asked is undone, one whose promotion was asked goes on (B asked
where it is, if its answer was not in). Switching back is a switch the other way.

**On the tab:** the term and since when this machine is the home; the machines that could take
over, each with why it cannot now; a switch under way step by step; then each machine
(following, copying anew, off) and each outside service (moved, being moved, failed, needs
you). What needs a person on the new home is a **checklist** there for a week: each item ticked
off by hand (`POST /api/cluster/switchover/{id}/checklist`), or by itself where a check can tell
(a model runtime started since is checked again while the list is shown). `GET
/api/cluster/self` carries the term.

### Outside services

| Service | What the switch does |
|---|---|
| Secrets (Cloudflare Stream, simulcast and web-search keys, …) | They move with the secrets key; each is checked to open on B. |
| Plex, dizqueTV | Checked from B: each must answer at the address the pipeline has for it (Plex with its token). An address that was A's own (its loopback: the service runs on A) is moved to A's network address when the service answers there (the extension's setting changed); one set only in A's environment (`MAKTABA_PLEX_URL`, `MAKTABA_DIZQUETV_URL`) or answering nowhere needs a person, with the extension's settings one click away. |
| Plex Live TV | The `plex_livetv` stage moves Maktaba's tuner to B's address. Plex keeps the device (the same DeviceID) and takes the new address in place, or else the old tuner leaves its DVR and Plex and B's is added; **a DVR is never deleted** (its recording schedules are the DVR's): one that carried only Maktaba's tuner takes B's in its place, one shared is joined as a registration does, and one Plex itself dropped with its last tuner is made again and said (its schedules did not move). The guide at A's address gives way to B's, the channel map is pushed and the guide reloaded. Being moved until the stage answers. A tuner address set by hand at another machine (`rules.plexlive.tuner_url`) needs a person. |
| Cloudflare notifications (`/api/tv/remote/webhook`) | Cloudflare sends Live input notifications to the public address set in its dashboard. Moved once one reaches B since the switch (its own test notification will do), or when the cloud relay moved (the only public address of a Maktaba home Cloudflare can call); else the checklist gives the path to set at B's public address. |
| Hugging Face | The token moves with the kit. |
| JWT keys | An operator's `MAKTABA_JWT_*_PEM` move with the kit; B's supervisor hands them to its API. |
| YouTube (yt-dlp `cookies_from_browser`) | A browser profile is its machine's: someone signs in to YouTube in a browser on B; the checklist opens the browser's setting. |
| Model runtime (Ollama) | A model server of another machine (`[llm] url`) moves with the kit (B keeps its own when it names one); one bundled on A stays there and B runs its own when it has one. Checked on B: moved once one answers; not used while the library holds no answer of a model's; else the checklist opens the model server's setting, and ticks itself once one answers. |
| The machines' database TLS | B serves it from the same CA (it came in the kit); every machine pins the CA B sends. |
| Cloud relay (`maktaba-cloudlink`, Epic 25) | The home's supervisor runs the relay agent (`[cloud] enabled`, its credentials in the keys directory). A stops its agent before B is promoted (two agents of one server take the tunnel from each other) and starts it again if the switch is undone; the credentials, their key and the `[cloud]` section move with the kit; B runs the agent from its next start; A puts its copies aside. Moved once B's tunnel is up. |

## Emergency promotion (the home gone for good)

The user's choices: a kit only on **standby homes**, and the old home's late writes **reported,
with a one-click apply of what does not conflict**.

**Standby homes.** The admin marks an approved full machine a standby home on the Machines tab
(`PUT /api/cluster/switchover/standby`). The home keeps a kit sealed to it, as a planned switch's
(X25519 between the cluster key and the machine's key, `HKDF` with `standby:<machine>`,
ChaCha20-Poly1305 per chunk, each seal with a salt and a key of its own: the kit is sealed
again under the same kit key at every change), gathered without a freeze: the secrets and their key, the cluster
key, the database CA and its key, the Hugging Face token, the JWT keys, the relay's credentials,
the machines and their roles' verifiers, the sessions, refresh tokens and devices, and this
home's own identity as a machine of the standby. It leaves out the history and the tokens that
last minutes (the audit log, idempotency keys, reset tokens, pairing tickets, SSO codes). The
home looks every two minutes (at once when a mark changes) and sends it again when its stable
part changed (`Kit.StableHash`: the term, the key files, the roles, the secrets' and the
machines' rows; not the sessions) or it is six hours old. The standby keeps it sealed in its keys
directory (`standby-kit.chunks`, with what it is beside it) and takes only a kit that opens for
it, and never one older than it holds (an old seal sent again). The mark removed, the home tells it to forget the kit; a revoked machine's copy is wiped
with it. Every machine is told where the standbys are (`home.standbys`, node.json
`standby_homes`).

**Taking over.** On a standby's own Machines page (its admin, signed in on it while the home is
away) a card offers it, only while it holds the kit, has a whole copy (every table's first copy
done) whose last change was at most 10 minutes older than the home's last answer (on the home's
clock), and the home does not answer (two homes never write); the home's name is typed to
confirm (`POST /api/cluster/emergency`, the standby's supervisor checks again). **Two standbys
never both take over:** before it promotes, the standby claims the term at every other standby
and every machine it knows (`POST /api/cluster/term-claim` on each, the claim sealed from its key
to that machine's and the answer sealed back): a machine keeps the first standby that claimed a
term for ten minutes and refuses another, or a claim for a term no newer than its own home's,
and one that answers as the home of that term or newer refuses it outright. It takes over only
if no machine it reached refused; two at once refuse each other and withdraw their claims, to be
asked again. One cut off from every other machine is the partition the warning names. Then, as a planned promotion: its copy promoted with the kit,
the sequences a million past the copy's highest values (the old home's late writes keep their
ids), the old home registered as one of its machines with the identity its kit gave it, a **fork
slot** kept from the promotion on of the tables a machine's copy holds (what this home changes
since; what an old home wrote applies in no other), term N+1 and the switch recorded in the
promotion's transaction (a switch the copy showed under way is cancelled), every machine's notice
sealed (the old home's with the cut: where in its WAL
what it wrote since begins), its Postgres and relay the home's; it starts again as the home. A
machine whose home does not answer asks the standbys for its notice too (`GET
/api/cluster/moved/{node}`) and follows the new home, copying it anew.

**The old home's return.** Every minute a home asks the standbys it knows, then its other
machines, for a notice sealed to any identity it may be known by: a standby's, the old home's of
a planned switch whose promotion it asked, the machine it was before it took over (its
`node.json` put aside). One of a newer term means it is not the home; so does one of its own term
from another home whose machine id is higher (the term claim is best effort: two standbys cut off
from each other may each take one term, and the one whose id is lower gives way, as each machine
decides alike; every home keeps the machine it was when it began its term, `cluster_terms.home_node`,
a machine's `node.json` home `node`). Then it fences its database (its machines' roles kept out, the app's rights to write taken,
read-only for every session) and stops its cloud relay agent (the new home runs it),
reads what it wrote after the cut from the slot the new home's copy had on it (`KeptAside`:
peeked, never consumed), keeps it in its state directory and reports it to the new home sealed
from its identity to the cluster's key (`POST /api/cluster/kept-aside/report`, again until
taken), wipes the home's keys, then becomes one of the new home's machines: its database is set
aside, never emptied, and the new home's copied. Its slot gone, cut by the cap, or not read, it
says so (all of it stays in the database it set aside) and steps down all the same. A report
carries up to 100,000 changes (more are counted, and said to be in that database). The new home
takes it only from the old home its term's emergency registered, or from a standby of that term
that is not its home (one that gave way to it), who and which term in the address, checked before
the body is read. Giving way to a home of its own term, a home reports all it wrote since it took
over (its slot was made at its own promotion).

**Applying it** (the new home's Machines tab, "Changes kept aside"): download the report, set it
aside, or review what would apply. Each of the old home's transactions applies **whole or not
at all**, in its commit order, and not when it:

- touches the secrets or the cluster's own rows (the home-only tables, `cluster_*`,
  `instance_identity`, `workers`);
- makes or changes a credential (a session, a refresh or personal token, a reset link, a pairing
  ticket, a user's password other than the one this home has): the person sets it again here;
- empties a table;
- touches a table without a primary key here (its rows cannot be matched), or one this home lacks;
- touches a row this home inserted, changed or deleted since the fork (from the fork slot), or a
  table the fork does not keep (one made since: what this home changed in it is not known);
- updates a row this home does not have;
- fails when tried here (a constraint, a type, a transaction before it not applied), with why.

The review shows each transaction (when, what it changed, applies or why not). Apply runs that
plan in **one transaction**, only while it is still the plan shown (its hash: else nothing is
applied and the new plan is shown), and records what was applied, what was not and why, by whom.
Once every report of the term is applied or set aside the fork slot is dropped; with none
reported, after 30 days (an old home back later still reports, to download, but nothing of it
applies on its own: what this home changed since is not known any more).

## Rules (`rules.cluster`, on the settings page)

| Rule | Default | Meaning |
|---|---|---|
| `delegate_unreachable_grace_seconds` | 30 | After this long without an answer from the home, a node stops asking it per request and answers from its copy. |
| `offline_copy_max_lag_minutes` | 10 | A copy answers with the home away only if it had followed the home up to this close to when the home was last heard. |
| `copy_reads_max_lag_seconds` | 30 | With the home there, a node reads from its copy only while the copy is this close behind it. |
| `offline_session_hours` | 12 | A sign-in made on a node while the home is away lasts this long. |
| `offline_session_grace_minutes` | 30 | …and ends this long after the home answers again. |
| `replica_wal_keep_gb` | 20 | The home keeps this much of its changes for a machine behind; past it the machine subscribes anew. |
| `copies_on_operator_postgres` | off | A home whose database is not Maktaba's own Postgres keeps the machines' copies only with this on (such a server may carry other decoding plugins; above). Maktaba's own Postgres needs no rule. |

## Security

- **What a node holds.** The copy is the home's database without its secrets: password hashes
  (argon2id), web session ids, personal access token hashes and the library — not the secrets
  table, the push and stream keys, refresh tokens, other machines' rows or the machines' keys.
  The replication role reads only what is published to it. The copy's files are readable by the
  node's own account only; revoking the machine stops its subscription at once and wipes its
  copy at its next check.
- **On the wire.** Every connection of a machine to the home's database (its programs', the
  grant's, the subscription's) runs over TLS that verifies the home's CA (`verify-ca`), which
  came with the join, over the machine channel or with a switch, never from the database itself;
  without it the machine does not connect its copy. With `[postgres] tls = "off"` on the home
  they run in the clear, and the machines are told so the same three ways; the setting's help
  says so.
- **What reaches the home's Postgres.** Off this machine, nothing while no machine may connect
  (it does not listen beyond loopback); then only the machines' roles, into the app's database,
  from the machines' networks and addresses, with their SCRAM passwords. No physical
  replication; the replication roles read only what is published to them. A replication role's
  password is never sent: the machine sends its verifier.
- **Sessions on a copy.** The copy holds web sessions' hashes only (`web_session_keys`): a
  copy's file, read off a stolen disk, signs no one in.
- **Node-local sessions and sign-outs** are stored hashed and never carry a credential; the copy
  cannot be written: nothing a node does offline changes the home's data but the sign-outs and
  sessions it hands over once the home answers.
- **A switch of the home.** Only an admin starts it. The kit (the secrets key, the cluster key,
  the CA's key) is sealed to the machine taking over and travels over the authenticated machine
  channel, never through the replica; the new home removes it once loaded, the old home once
  undone or demoted, and the old home keeps none of the home's keys once it follows the new home
  (overwritten, then removed). A machine follows a new home only on the home's own command over
  that channel, or on a notice sealed to it by the cluster key; the copy pins the CA that came
  with it. **Two homes never write**: the old home stays frozen from the promotion's request
  until the new home says it called the switch off (or an admin calls it off with that machine's
  name typed, while it does not answer); a home that learns of a newer term stops writing (its
  machines' roles kept out, the app's rights to write taken, which no session's own "read write"
  overrides) and becomes a machine. It gives up the server certificate its database had too: one
  the home's CA signed passes as the home's database (`verify-ca`).
- **A standby home** holds the home's secrets and keys, sealed to it alone, in its keys directory
  (this account's only), and only while the admin keeps the mark: unmarked or revoked, the kit is
  removed. It can take over only with the home not answering, a whole and current copy, its admin's
  typed confirmation and the term claimed at the other machines (no two standbys that reach each
  other take one term; two cut off from each other may, and the one whose machine id is lower
  gives way).
  An old home's report is taken only from the machine that term's emergency registered, sealed by
  that identity to the cluster key; it is never applied on its own, and what touches the secrets,
  the cluster's rows or a credential never applies.
- **Sign-outs a machine hands over.** It keeps one only for a session it knows (in its copy, or
  answered for it by the home), at most 4096, handed over in batches the home takes; the home ends
  only the sessions made through that machine (`web_sessions.origin_node_id`), and "everywhere"
  only for a person who signed in through it. The revocations the home pushes are kept on the
  machine's disk while they matter. A sign-out everywhere is kept with its session or not at
  all; a person whose sessions on the machine are all node-local (the home cannot tell such a
  sign-in from the machine's word) signing out everywhere is signed out of all of them, with the
  home there or not, and told that the others end through the home (`signed_out: "here"`, code
  `signed_out_here_only`; the sign-in page says so); nothing is handed to the home.
- **Known limits.** A pipeline write of a job is guarded by its worker's claim, not by a claim
  generation: a machine stops its jobs before their lease lapses, so its worker never holds a job
  again while a run of it from before is alive. On an operator's own Postgres, the output
  plugins it carries (above).

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
| Two switches started at once | One runs; the other is refused (also from two processes of the home). |
| The promotion's answer lost | The old home stays frozen, asks the machine where the switch is and reads its notice at its address; it goes on, or is undone only when the machine called the switch off. |
| The promotion fails on the machine taking over | Its copy is as it was (following the old home), the switch called off there; the old home, asking, undoes it. |
| The old home's API or supervisor restarted mid-switch | Taken up at its start: undone before the promotion was asked, carried on after it. |
| The new home unreachable when the old one would follow it | The old home keeps its data, frozen, and tries again; it gives up nothing before it receives the new home's changes. |
| The new home slow to start after its promotion | The old home and the machines keep trying to follow it. |
| A machine off during a switch | When back, its old home (or a standby, or any other machine) hands it the new home's notice; it follows, copying anew. |
| A slot on the home's database that is not Maktaba's | Dropped within 5 s (another output plugin, an unknown name, a machine that may not have a copy). |
| A machine's first copy that makes no progress | After two hours with no table done it subscribes anew. |
| The home's Postgres lock file left by a postmaster that did not end cleanly, its PID another program's now | Removed before the server starts. |
| A machine whose Plex is unreachable from the new home | The tuner stays where it was; the stage retries; the tab says being moved. |
| A machine without its home database's CA (joined before this version) | Its copy waits, saying so; the home sends the CA over the machine channel within 30 s of its connection. |
| Joins opened, or the last machine revoked | The home's Postgres starts again within 30 s to listen for the machines, or no longer: its connections drop for a moment and come back. |
| The node's API restarted during an outage | The sessions it opened offline are kept on disk and written to the home's history once its database answers. |
| A machine's clock off by minutes | Lag and read-your-writes use the home's clock: reads are neither stale nor sent to the home for nothing. |
| The home's API stopped, its database running | The beat stops, but a copy whose slot keeps up with the home's WAL still follows. |
| The home gone for good | A standby's admin makes it the home; the machines find it at the standbys and follow it, copying anew. |
| The old home only cut off, and back | It sees the newer term within a minute, stops writing, reports what it wrote meanwhile and becomes a machine of the new home. |
| A standby off when the home's secrets change | It gets the current kit when it is connected again (its kit says how old it is). |
| What the old home wrote meets this home's own changes | Not applied: the review says which row this home changed, or which constraint failed. |

## Tests

- `api/internal/cluster/copies_pg_test.go`: the grant (a verifier, full machines only, `MK403`),
  the publication and what the replication role may read (no slot function: it cannot make a
  `test_decoding` slot or read one), `web_session_keys`, refusals, the beat, the CA, the cap and
  Postgres's own cap, the slots that are not Maktaba's dropped (another plugin, unknown names, a
  machine that may not have a copy, an idle physical slot on Maktaba's own) and Maktaba's kept,
  revoke and remove, on a real Postgres 16 set up as the bundled home is.
  `copy_tables_pg_test.go`: every table decided for the copy. `pgbin`: no output plugin but
  `pgoutput` left in an install (also one unpacked before). `pgbundle`: the bootstrap again on a
  cluster an older release provisioned; on a real Postgres a replication role refused the slot
  functions, the app role not; `pg_hba.conf` frozen; a stale lock file of another program
  removed.
- `cmd/maktaba-server/internal/pgbundle`: `pg_hba.conf` and the listen address per the cluster's
  reach (`reach_test.go`), the watch on a real Postgres (`reach_pg_test.go`: joins opened, a
  machine approved from another network, the last one revoked, a look the database does not
  answer), `machines_from`, the CA never remade on its own. The supervisor's `Restart`
  (`restart_test.go`). `config`: the home's DSN with the CA, and without one (fail closed).
- `cmd/maktaba-server/internal/nodecopy/copy_pg_test.go`: **two Postgres servers on 127.0.0.1** —
  a home made by `pgbundle.Prepare` and migrated by `maktaba-api`, and the copy kept by the
  manager alone: subscribes over TLS (`verify-ca`), follows, holds no secrets nor other
  machines' rows or keys, refuses writes, keeps following with the home's beat stopped (its
  slot caught up), subscribes anew after the slot is dropped (also while streaming), is wiped on
  revocation. Opt-in: `MAKTABA_TEST_PG_BIN`.
- CI runs all of these on real Postgres 16 (`.github/workflows/_replica.yml`, ubuntu-24.04's
  Postgres: the switch's and the copies' Postgres tests and the three end-to-end runs).
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
  session opened after the freeze, a promotion that fails leaving the copy as it was (its
  subscription, no machines' rows, slots or publications of it), the old home giving up nothing
  before it receives the new home's changes, the fork published the copy's tables only.
  `api/internal/cluster/switchover`: the steps in order, the undo, the refusals, two starts at
  once, a lost promotion answer (never thawed: asked, or the new home's notice read), a switch
  called off (and, called off without B's word, B asked and the term watched for a day), a
  restart mid-switch taken up, Plex, dizqueTV and Cloudflare's notifications checked from the new
  home, Plex Live TV's move, the notices, the report of a home that gave way to another of its
  term (`MAKTABA_TEST_PG_URL`). The supervisor's side (`serve_switchover_test.go`): a switch
  called off is never promoted, nor by a copy that does not answer, the record of a switch, the
  kits of switches over removed at a start, the term watched under every identity, the old home
  keeping none of the home's keys; a machine finding the new home at a peer, the home of two of
  one term that a machine follows, a promoted standby watching as the machine it was
  (`serve_standby_test.go`). A fenced database refusing the app's writes, its owner's DDL and
  grants, and giving every right back (`internal/dbfence`, `MAKTABA_TEST_PG_BIN`).
  `api/switchover_e2e_test.go` (`MAKTABA_E2E_SWITCHOVER=1`): three `maktaba-server serve`
  machines on the pinned Postgres 16.15: A the home, B and C its machines; C is switched off, the
  admin switches the home to B; B takes over without copying again, A follows it as a machine,
  C follows it by itself once back, the secret opens on B, B's change reaches A, and the owner
  signs in on both, and the cloud relay agent (a stand-in) moves from A to B with its
  credentials. `api/emergency_e2e_test.go`: B marked a standby on A gets A's kit and C learns where
  the standbys are; A is switched off; B's admin takes over; C finds B at the standby and follows
  it; A comes back, takes a change as a home, sees the newer term, stops, reports the change
  sealed and becomes B's machine; on B the change is reviewed and applied. The emergency's
  database steps on real Postgres (`switchover_pg_test.go`): the fork, the plan (a new row and an
  untouched row's change apply; a row the new home also changed, a user name the new home gave
  another user, the old home's cluster rows do not), an old plan refused, one apply recorded, the
  fork dropped. The standby's kit and the notices: `serve_standby_test.go`, `standby_test.go`,
  `standby_pg_test.go` (sent, unchanged, sent again, forgotten; the machines told), the report's
  seal on both modules against one vector, the checklist (`checklist_pg_test.go`), the relay
  (`relay_test.go` on both sides). The pipeline's `test_plex_livetv.py`: the tuner moved in place,
  re-added, with a DVR of its own, and kept when Plex cannot reach the new home. The web's
  `SwitchHomeCard.test.tsx` (the checklist, the standby homes, the review and apply) and
  `EmergencyCard.test.tsx`.
- Unit tests: `nodeauth` (copy-first answers, freshness, node-local sessions and sign-outs,
  revocations in flight and pushed, a sign-in with the home back, one missed probe, must-change
  and lockout spraying, bearer tokens, grace, a hanging home), `nodedb` and the node's routing
  (per statement, lag on the home's clock, read-your-writes until the copy has the change),
  `homeproxy` (node-local cookies never reach the home), `pgdsn` (bounded connections, on a real
  Postgres behind a hanging proxy), `nodecopy` (names, secrets, decisions, the subscription's
  connection), `streaming` (write-back, a closing written back, sessions kept across a restart,
  outage classification), the agent channel's `home.database`, the pipeline's lease and claim
  guard (`commit_segment` from a worker that lost the job), the web outage and paused pages.

## Follow-ups

- The cloud relay's first claim from the control centre (today `[cloud] claim_code` and `slug`
  in server.toml, used on the agent's first run).
- A home on an operator's own Postgres with TLS: a setting for its CA, so its machines' copies
  verify it (today they wait; a home on Maktaba's own Postgres, or a database without TLS, needs
  nothing).
- A machine taking over that is gone for good in the middle of its promotion leaves the old home
  frozen (it never thaws without that machine's word). An admin's way out — taking the home back
  under a newer term the gone machine would step down to if it returned — is to be designed.

- Native apps: offline sign-in (an access token from the node's own key, refreshed by the home on
  its return).
- TV art offline (serve the station the art the node last fetched).
- Converting this PC's live node (`E:\tv\maktaba-local`) happens after the merge, with the user's
  OK; it is automatic once it runs this version.
