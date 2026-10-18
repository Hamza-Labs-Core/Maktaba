# Plan — Multi-instance Maktaba (Epic 31)

Requirements: [README.md](README.md). Design: [design.md](design.md).
Branch: `feat/multi-instance` (one PR into main at the end).

## Order

```
F  foundation (schema, contracts, vectors)      ── first, on the branch
├─ T1  home: API (Go)                          ┐
├─ T2  node: pipeline (Python)                 ├─ in parallel, each in its own worktree
└─ T3  web: Machines page (React)              ┘
I  integration: merge T1–T3, two-process end-to-end on one host, review, fixes, PR
```

## F — Foundation

- Migration slot 0103 `0103_multi_instance.sql` + `.sqlite.sql` + MANIFEST row: the
  tables and columns of design §4, the `maktaba_worker` role (Postgres, guarded).
- `StageDef.placement` (`any` | `home`) in `engine/graph.py`; `index`, `thumb_small`,
  `review_pages` are `home`; `sync_graph` writes it to `job_stages.placement`.
- `shared/testdata/cluster/vectors.json`: fixed X25519 keys, the shared secret, the SAS
  code, a payload and its sealed envelope (nonce fixed), generated once and checked by
  both languages.
- These docs.

## T1 — Home (Go, `api/`)

1. Instance identity: create the singleton row at start (name = stable host name).
2. `internal/discovery/mdns.go`: `ZeroconfPublisher` (grandcat/zeroconf), started from
   `main.go` unless `MAKTABA_DISCOVERY_MDNS=0`; re-publishes TXT `join` on change.
3. `internal/cluster`: crypto (X25519, HKDF, ChaCha20-Poly1305 from x/crypto) checked
   against the vectors; store (Postgres) for nodes, stages, identity; role provisioning
   (`CREATE ROLE … IN ROLE maktaba_worker`, `NOLOGIN` + terminate on revoke) with a
   capability probe (`rolcreaterole`/superuser → `roles_supported`).
4. `internal/handlers/cluster`: the endpoints of design §8; public ones added to the
   allowlist with a per-IP limiter; admin ones behind the admin check.
5. `GET /api/workers`: `node_id`, `node_name`, `role`.
6. Tests: handlers with the queuestest DB fakes or a real Postgres where roles matter
   (skip when unavailable), crypto vectors, expiry, wrong-code limit, revoke.

## T2 — Node (Python, `pipeline/`)

1. `cluster/crypto.py` (cryptography: X25519, HKDF, ChaCha20Poly1305) + vectors test.
2. `cluster/config.py`: node config load/save per OS, mode 0600.
3. `cluster/discover.py`: zeroconf browse (3 s) → homes; fallback `discovery/probe.py`.
4. `cluster/capabilities.py`, `cluster/storage.py` (candidates per OS, inference, check).
5. `cluster/join.py` + `__main__.py join` subcommand.
6. Node mode in `runtime.py`/`__main__.py`: config → DSN, role, ids; no `sync_graph` /
   `ensure_rows`; heartbeat updates `cluster_nodes`, honours `revoked`/`drain`.
7. Stage selection (design §5) re-evaluated every 30 s; `workers.stages`/`stages_off`
   updated; the claim loop reads the current set; `placement` honoured for role `node`.
8. `PathMap`: case-insensitive `to_db`, UNC round-trip; the node uses the stored map.
9. Deps `zeroconf`, `cryptography` (base), lock updated. Windows CI job for
   `tests/cluster`.
10. Tests: crypto vectors, config paths per OS (monkeypatched), path inference with temp
    dirs as mounts (macOS, Linux, drive letters, UNC), storage check, stage selection
    reasons, node mode skipping `sync_graph`, revoke/drain at heartbeat.

## T3 — Web (`web/`)

1. `lib/clusterApi.ts` (types from design §8).
2. `pages/Admin/Machines.tsx` + route `/admin/machines` + `nav.admin.machines`; the
   sections of design §9; en + ar strings.
3. Worker strip: machine name, link to the page.
4. Tests (vitest + testing-library): toggle, approve with code (wrong code message),
   stage switch, revoke confirm, Arabic render.

## I — Integration

- Merge T1–T3 into `feat/multi-instance`; all gates (Go, Python both databases, web,
  migration lint, coverage floors).
- End-to-end on one machine: the home stack as today; a second pipeline process with its
  own node config (`MAKTABA_NODE_CONFIG`) runs `join --home http://127.0.0.1:8080`;
  approve through the API with the printed code; it registers as `role=node`, claims no
  home-only stage, its path map is inferred; switch a stage off → gone from its set
  within a minute; revoke → it stops.
- Review (code-review skill), fix, one PR.
