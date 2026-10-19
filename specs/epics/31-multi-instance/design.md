# Design — Multi-instance Maktaba (Epic 31)

Requirements: [README.md](README.md). Plan: [plan.md](plan.md).

## 1. Shape

```
          LAN (multicast DNS-SD: _maktaba._tcp, TXT role=home join=0|1)
                                   │
 ┌──────────── HOME (e.g. the Mac) ┼───────────────┐      ┌──── NODE (Linux / Windows / Mac) ───┐
 │ API  ── mDNS advertiser         │                │      │ maktaba-pipeline join  (once)       │
 │      ── /api/cluster/* (join,   │  HTTP  ◄───────┼──────┤   browse → POST join → show code    │
 │          approve, revoke)       │                │      │   poll → decrypt credentials → save │
 │ Postgres (catalog + queue)  ◄───┼── SQL (own role) ────┤ maktaba-pipeline run (node mode)    │
 │ pipeline workers (role home):   │                │      │   claims stages it can run          │
 │   index, thumbnails, review     │                │      │   path map → the share              │
 │   pages + anything else         │                │      └─────────────────────────────────────┘
 └───────────────┬─────────────────┘                                │
                 └────────────── NAS share (media, subtitles) ──────┘
```

The home is unchanged in kind: its API, its database, its own workers. A node is a
pipeline worker process like the Mac's transcription workers today (plan 06-11 notes
N2), with three additions: it found the home by itself, it holds its own database role
(handed over encrypted after the admin approved it), and the stages it claims are
decided by what it can run, where it may run (home-only stages) and the admin's
switches — re-evaluated while it runs.

## 2. Identity and discovery

**Instance identity** (slot 0103): `instance_identity` singleton row — `instance_id`
(uuid, made at first start), `name` (default: the home's stable host name),
`accept_joins` (bool, default false), `accept_joins_until` (timestamptz; the toggle
turns itself off after 1 h).

**Advertiser (Go, API).** `api/internal/discovery/mdns.go` implements the existing
`Publisher` interface with `github.com/grandcat/zeroconf`. Service `_maktaba._tcp`,
instance name `Maktaba (<name>)`, the API's HTTP port, TXT:

| key | value |
|---|---|
| `protocol` | `1` |
| `role` | `home` |
| `id` | first 8 hex of `instance_id` |
| `version` | API version |
| `schema_rev` | latest migration slot |
| `join` | `1` while joins are accepted, else `0` (re-published on change) |

Never a library or user name (Epic 15 privacy rule). `MAKTABA_DISCOVERY_MDNS=0`
turns it off (tests, CI, hosts where multicast is unwanted).

**Browser (Python, node).** `maktaba_pipeline/cluster/discover.py` browses with
`zeroconf` (pure Python: macOS, Linux, Windows) for 3 s and returns
`Home(url, instance_id, name, version, schema_rev, join)`. Fallback when nothing
answers: the existing `discovery/probe.py` subnet sweep of `GET /api/system/version`.
`--home URL` skips both.

## 3. Joining

### 3.1 Protocol

```
node                                                     home
 │ keypair (X25519), node_id (uuid) — kept in node config │
 │ POST /api/cluster/join {node_id, name, os, arch,       │
 │   versions, capabilities, storage, pubkey}  ──────────►│ joins on? rate ok?
 │                                                        │ new ephemeral X25519 for this request
 │                                                        │ row cluster_nodes(state=pending, expires +15 min)
 │ ◄──── 202 {request_id=node_id, home_pubkey, home:{…},  │
 │            expires_at}                                 │
 │ code = SAS(shared, node_pub, home_pub)  → shown        │
 │                                      admin types code ─┤ POST /api/cluster/nodes/{id}/approve {code}
 │                                                        │  code == SAS? (5 tries) → CREATE ROLE,
 │                                                        │  seal {database_url, …} to the node
 │ GET /api/cluster/join/{node_id}?pubkey=…  (every 2 s) ─►│
 │ ◄──── {state: approved, sealed: {nonce, ciphertext}}   │
 │ decrypt → node config → run                            │
```

### 3.2 Cryptography (both sides must match byte for byte)

- Keys: X25519 (RFC 7748), 32-byte raw keys, standard base64 on the wire.
- `shared = X25519(own_private, peer_public)`.
- `salt = node_pub || home_pub` (raw bytes, node first).
- SAS: `okm = HKDF-SHA256(ikm=shared, salt, info="maktaba-join-sas", L=4)`;
  `code = f"{int.from_bytes(okm, 'big') % 1_000_000:06d}"` (6 digits, shown as `123 456`).
- Seal key: `HKDF-SHA256(ikm=shared, salt, info="maktaba-join-seal", L=32)`.
- Seal: ChaCha20-Poly1305 (RFC 8439), 12-byte random nonce, AAD = the node id as ASCII
  (its canonical lowercase uuid). Envelope `{"nonce": b64, "ciphertext": b64}`
  (ciphertext includes the 16-byte tag).
- A middle machine that swaps keys makes the two sides derive different shared secrets,
  so the code the node shows differs from the one the home expects: the approval fails.
- Interop: `shared/testdata/cluster/vectors.json` holds fixed keys with their SAS and a
  sealed payload; the Go and the Python tests both check against it.

Sealed payload (JSON):

```json
{
  "v": 1,
  "node_id": "…",
  "database_url": "postgres://maktaba_node_1a2b3c4d:<secret>@192.168.1.20:5432/maktaba?sslmode=prefer",
  "home": {"api_url": "http://192.168.1.20:8080", "cluster_pub": "<base64 X25519>", "instance_id": "…", "name": "Mahmouds-Mac-Studio"}
}
```

`home.cluster_pub` (phase 2) is the home's cluster key, which a machine's agent uses to
authenticate the hub ([design-agent.md](design-agent.md) §4.2); a machine joined before
phase 2 has none and is asked to re-join.

The database host is the address the node used to reach the API (the request's Host),
the port `MAKTABA_CLUSTER_PG_PORT` (default 5432), the database name the home's own.

### 3.3 Database roles

Slot 0103 creates the group role `maktaba_worker` (NOLOGIN) with `SELECT, INSERT,
UPDATE, DELETE` on all tables, `USAGE, SELECT, UPDATE` on all sequences, and default
privileges so tables added by later migrations follow — only when the migrating user may
create roles (`rolcreaterole` or superuser); otherwise the home reports joins as
unavailable ("the database user cannot create roles").

Least privilege: the group gets no access to accounts and sessions (`users`,
`web_sessions`, `refresh_tokens`, `personal_access_tokens`, `password_reset_tokens`,
`pairing_tickets`), `licenses`, devices and viewing data, nor to the cluster tables;
`audit_log` takes inserts only. A machine reads and updates **its own** cluster row only
through `SECURITY DEFINER` functions that check `cluster_nodes.db_role = session_user`:
`cluster_node_beat(node)` (last seen; returns state, drain, path map),
`cluster_node_report(node, capabilities, storage, inferred_path_map)` (never replaces an
admin's map) and `cluster_node_stage_switches(node)`. A migration that adds a table of
secrets revokes it from `maktaba_worker` the same way (default privileges grant data
tables). On SQLite (tests, single-machine) the same operations are plain SQL.

Approval runs `CREATE ROLE "maktaba_node_<the node id's 32 hex digits>" LOGIN PASSWORD
'<32 random bytes, base64url>' IN ROLE maktaba_worker`. Revocation runs `ALTER ROLE …
NOLOGIN`, rotates the password, and `pg_terminate_backend` on its sessions (when the
home's user may; otherwise the node notices `revoked` at its next heartbeat, within
10 s, and exits). Workers never need DDL: migrations are the API's (`MAKTABA_AUTO_MIGRATE`).

### 3.4 Rules

- Join endpoints answer only while `accept_joins` (403 `joins_closed` otherwise), are on
  the public allowlist, and are rate-limited (10 requests/min per IP).
- A request expires after 15 min; 5 wrong codes reject it.
- `sealed` is kept until the node's worker first registers with that node id (or 15 min
  after approval), then cleared; the home's ephemeral private key is cleared at approval.
- A node id that is approved cannot ask again (409 `already_member`) until it is removed.
- A pending request is renewed only with the key it was made with (409
  `pending_elsewhere` otherwise): the node id travels in the poll URL, so whoever saw it
  must not swap in their own key.
- The role name carries the whole node id: the node chooses its id, and a shared prefix
  would give a second machine the first one's role.

## 4. Schema (slot 0103)

```sql
instance_identity(id smallint pk = 1, instance_id uuid, name text,
                  accept_joins bool, accept_joins_until timestamptz,
                  created_at, updated_at)
cluster_nodes(id uuid pk,                -- the node's own id
              name text, state text check in (pending, approved, rejected, revoked),
              os text, arch text, versions jsonb, capabilities jsonb, storage jsonb,
              path_map jsonb,             -- null: inferred by the node
              path_map_source text,       -- 'inferred' | 'admin'
              drain bool default false,
              node_pubkey text, home_pubkey text, home_privkey text,  -- privkey cleared at approval
              code_attempts int default 0, expires_at timestamptz,
              sealed jsonb,               -- cleared once fetched and in use
              db_role text, remote_addr text,
              created_at, approved_at, approved_by, revoked_at, last_seen_at, updated_at)
cluster_node_stages(node_id uuid fk, stage text fk job_stages, enabled bool,
                    primary key (node_id, stage))   -- absent row: automatic
workers + node_id uuid null              -- null: a home worker
        + role text default 'home'       -- 'home' | 'node'
        + os text, capabilities jsonb,
        + stages_off jsonb               -- {stage: reason} for the stages it does not claim
job_stages + placement text default 'any' check in ('any', 'home')
```

SQLite siblings as usual (no roles: a SQLite home cannot take nodes; the API says so).

## 5. Which stages a worker claims

Every 30 s (and at start) a worker computes, for each graph stage, either *served* or
the first reason it is not:

| order | reason (shown on the page) | source |
|---|---|---|
| 1 | not in `--stages` / in `--exclude-stages` | CLI, as today |
| 2 | newer version in the database | `stages_ahead`, as today |
| 3 | cannot run here: *why* | `StageImpl.unavailable()` (platform, extras) |
| 4 | runs on the home only | `StageDef.placement == "home"` and role is `node` |
| 5 | storage not reachable | storage check (§6) for stages that read/write media |
| 6 | switched off for this machine | `cluster_node_stages.enabled = false` |
| 7 | draining | `cluster_nodes.drain` |

Home-only stages (`placement="home"`): `index` (the vector index is a local directory
the home's gRPC search reads), `thumb_small` and `review_pages` (the API serves their
files from the home's disk). The served set and `stages_off` are written to `workers`;
the claim loop reads the current set each poll, so a change applies without a restart.

A node never runs `sync_graph` or the extensions' `ensure_rows` (the home's workers own
the registry; a node with older code would roll it back); it still refuses stages whose
database version is ahead of its code.

## 6. Shared storage

> Story 31.3 ([story-31-03](story-31-03-canonical-share-paths.md)) stores media paths as
> `smb://<server>/<share>/...` and resolves them from each machine's mounts; the inference
> below remains the fallback for paths stored before the migration.

**Path map inference** (`cluster/storage.py`, at the node's first start and whenever the
map is not admin-set): for each library root take up to 5 videos (path, size); for each
sample try the candidate mount roots joined with ever-shorter suffixes of the sample's
path; a hit (exists and same size) gives `db_prefix → local_prefix`. The mapping found by
most samples wins; it needs 2 agreeing samples (or the library's only video). Candidates:

| OS | candidates |
|---|---|
| macOS | `/Volumes/*`, `~/*` symlinks to mounts (as `~/nas-nfs`) |
| Linux | network and fuse mounts from `/proc/mounts` (nfs, cifs, smb3, fuse.sshfs, 9p), `/mnt/*`, `/media/*/*`, `/srv/*` |
| Windows | existing drive letters `A:`–`Z:`, `\\server\share` from `net use`, and the database path itself when it is a drive or UNC path |
| any | `MAKTABA_STORAGE_HINTS` (os.pathsep-separated) |

The result is stored in `cluster_nodes.path_map` (`path_map_source='inferred'`); an admin
edit sets `'admin'` and is never overwritten. The worker uses the stored map
(`PathMap.parse`); a node started with `--path-map` uses that.

**Storage check** (start, then every 5 min): read — the samples stat with the right
size; write — create and delete `.maktaba-<node8>.tmp` in a library root. Stored in
`cluster_nodes.storage` (`{read: ok|error, write: ok|error|skipped, checked_at, detail}`);
reason 5 in §5 follows it: `nas_read`-pool stages need read, `nas_write` ones need write.

**PathMap fixes**: `to_db` matches case-insensitively like `to_local`; UNC prefixes
(`\\nas\share\…` → `//nas/share/…`) round-trip.

## 7. The node process

- `maktaba-pipeline join [--home URL] [--name NAME] [--run]`: discover, request, show the
  code, wait, store the config, optionally start.
- `maktaba-pipeline run` with no `--database-url` and a node config present runs as a
  node: `database_url`, `role=node`, `node_id`, worker id `node-<node8>-<pid>`, host = the
  stable host name.
- Node config file (mode 0600 on POSIX): macOS `~/Library/Application Support/Maktaba/node.json`,
  Linux `${XDG_CONFIG_HOME:-~/.config}/maktaba/node.json`, Windows
  `%APPDATA%\Maktaba\node.json`; `MAKTABA_NODE_CONFIG` overrides. Holds `node_id`, `name`,
  the X25519 private key, `home`, `database_url`, `joined_at`.
- Heartbeat (10 s) also calls `cluster_node_beat` (Postgres; plain SQL on SQLite), reading `state` and `drain`:
  `revoked` → graceful stop; `drain` → claim nothing new.
- Capabilities reported at join and on each start: OS and version, architecture, CPU
  count, memory, Apple Silicon, MLX-Whisper importable, NVIDIA GPUs (pynvml), ffmpeg path
  and version (`MAKTABA_FFMPEG` overrides `ffmpeg` on PATH), installed extras, Python and
  pipeline versions.

## 8. API (Go)

Public (allowlisted, rate-limited):

| method | path | body / answer |
|---|---|---|
| POST | `/api/cluster/join` | `{node_id, name, os, arch, versions, capabilities, storage, pubkey}` → 202 `{request_id, home_pubkey, home:{instance_id,name}, expires_at}`; 403 `joins_closed`; 409 `already_member`; 429 |
| GET | `/api/cluster/join/{node_id}?pubkey=` | `{state: pending\|approved\|rejected\|expired\|revoked, sealed?}` (pubkey must match) |

Admin:

| method | path | body / answer |
|---|---|---|
| GET | `/api/cluster` | `{home:{instance_id,name,accept_joins,accept_joins_until,roles_supported,roles_reason}, nodes:[Node]}` |
| PUT | `/api/cluster/settings` | `{accept_joins?, name?}` |
| POST | `/api/cluster/nodes/{id}/approve` | `{code}` → Node; 400 `wrong_code` (`attempts_left`); 410 `expired` |
| POST | `/api/cluster/nodes/{id}/reject` · `/revoke` | → Node |
| PUT | `/api/cluster/nodes/{id}` | `{name?, drain?, path_map?: {..}\|null, stages?: {stage: bool\|null}}` |
| DELETE | `/api/cluster/nodes/{id}` | only rejected / revoked |

`Node` = the row (no keys, no secrets) + its live workers (`workers.node_id`): online,
last_seen, served stages, `stages_off`. `GET /api/workers` gains `node_id`, `node_name`, `role`.

## 9. Web

Admin → **Machines** (`/admin/machines`, nav `nav.admin.machines`): the home card
(name, "Accept new machines" toggle with its remaining time, why joins are unavailable
if roles are not supported); pending requests (name, OS, time left, code field, Approve,
Reject); machines (status, OS / CPU / GPU chips, storage check, path map — editable,
"inferred" or "set by you" —, a stage table: served / off with the reason / switch,
Drain, Revoke, Remove). The Processing page's worker strip shows machine names and links
here. English and Arabic.

## 10. Cross-platform notes

- Python deps for every worker: `zeroconf`, `cryptography` (base dependencies: a node
  needs them before it has any extra).
- Paths: the database keeps the scanner's form (`Y:/Channel/a.mp4` or `/Volumes/…`);
  maps translate; output always uses `/`, which Windows accepts.
- Windows: the Proactor loop and signal handling exist (`pipeline/signals.py`); config
  under `%APPDATA%`; drive letters and UNC as candidates; no directory fsync.
- CI: a `windows-latest` job runs the cluster unit tests (config paths, path inference,
  crypto vectors); Linux already runs the whole suite.

## 11. Security summary

The trust boundary is the LAN plus the admin's approval. Credentials only travel sealed to
the node's key; the code defeats a machine in the middle; each node has its own role,
revocable at once. A node's role can read and write all data (a worker needs it) but
cannot change the schema. Not covered here: TLS for the database connection and the
API (follow-up: `sslmode=require` with the home's certificate pinned at join).
