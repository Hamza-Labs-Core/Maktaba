# Plan 31.3 — Canonical share paths — implementation

> Implementation plan for [story-31-03](story-31-03-canonical-share-paths.md). Writes slot
> **0124** (`0124_canonical_share_paths`: `storage_servers`, `storage_path_migrations`,
> `instance_identity.storage` / `.path_map`). The open-time NFC/NFD fallback shipped first
> on its own (PR #79, `engine/paths.py: find_existing`, `PathMap.rewrite`).

## 0. Decisions

| # | Decision | Rationale |
|---|---|---|
| D1 | Paths name a `storage_servers` record, not an IP; the IP is one of its addresses. | An IP change is one edit; Windows mounts by host name, the Mac by IP. |
| D2 | `smb://` even for an NFS mount of the same share (export table / `/volumeN/<share>`). | One file, one canonical path, whichever way a machine mounts it. |
| D3 | URI-shaped, not URL-encoded; NFC; server lower case; share compared case-insensitively. | Readable, `LIKE`-friendly; SMB semantics. |
| D4 | The canonical path holds the on-disk name; `char_map` (default `none`) says what an SMB client sees. | Plex on the NAS sees on-disk names; yt-dlp fullwidth names are genuine. |
| D5 | `Resolver` *is a* `PathMap`. | Every caller (`ctx.local_path`, `to_local`, the storage check, the share agent) resolves canonical paths unchanged. |
| D6 | Precedence: explicit > admin > mounts > UNC fallback > legacy. | Admin rows win over discovery; legacy prefixes keep un-migrated rows working. |
| D7 | The migration is a pipeline maintenance command on the home with a backup table, not a SQL migration. | The mapping is the home's mount table; reversible row by row. |
| D8 | Go services take the share map the pipeline reports (`instance_identity.storage.path_map`; nodes: `cluster_nodes.path_map`), refreshed every 5 min. | No second mount-discovery implementation in Go; the pipeline already runs on every machine. |

## 1. Files

**Pipeline (`pipeline/src/maktaba_pipeline/`)**
- `sharepath/canonical.py` — parse/format, `normalise_rel` (NFC), `CharMap` (fullwidth, SFM, catia lists).
- `sharepath/mounts.py` — `parse_net_use`, `windows_drive_mounts` (WNetGetConnectionW), `parse_mac_mount`, `parse_proc_mounts`, `discover`.
- `sharepath/resolver.py` — `StorageServer`, `match_server` (names, addresses, resolved IPs), `share_of` (NFS exports), `share_mounts`, `Resolver` (to_local with char map + Unicode fallback, `canonical`, `to_db`), `build_resolver`.
- `sharepath/servers.py` — `load_servers`, `save_server`, `build_for_machine`.
- `sharepath/migrate.py` — `COLUMNS`, `plan` (dry run, verification sample), `apply` (backup rows, batched `unnest` updates), `revert`, `batches`.
- `sharepath/cli.py` + `__main__.py` — `maktaba-pipeline storage mounts|servers|server-add|canonicalize|batches|revert`.
- `engine/paths.py` — `to_db` helper; docs.
- `cluster/node.py` — the agent builds the resolver from servers + admin map + legacy map + mounts (`_rebuild`), at start and every storage check; `start_home` for the home; the report carries `shares`, `mounts`, `path_map`.
- `cluster/storage.py` — `check_shares` (per share read / write / via); canonical samples keep server + share in inference.
- `runtime.py` — the home calls `agent.start_home()`.
- `scanner/service.py` — walk the root's local mount, store `to_db` paths, hash the local file.
- `library_mgmt/roots.py` — canonical roots are their own normal form; components compare case-insensitively.
- `engine/stages/thumb_hash.py`, `thumb_small.py` (poster through the resolver), `repair_broken.py` (`backup_path` stored canonical), `subtitle/manager.py` (delete through the resolver), `grpc_server.py` (paths through the resolver).
- `tools/import_mediaintel.py` — output through `to_db`, comparisons in the database's form, resolver built from the servers.
- `extensions/plex/__init__.py` — the path map documented per share.

**Schema** — `shared/db/migrations/0124_canonical_share_paths.sql` + `.sqlite.sql`, MANIFEST row.

**API (`api/`)** — `internal/sharepath` (map from the home's report, `ToLocal`, `ToCanonical`, `Start`); `main.go` starts it; `handlers/videos` (playable check), `handlers/libraries` (roots stored canonical, checked through the mount, `canonicalPath` / overlap understand `smb://`); `internal/cluster/storage.go` (`StorageStore`: PG + memory); `handlers/cluster/storage.go` (`GET/PUT/DELETE /api/cluster/storage-servers[/{name}]`, `PUT /api/cluster/home/path-map`, `home.storage` / `home.path_map` in `GET /api/cluster`); `router/p31.go`.

**Streaming (`streaming/`)** — `internal/pathmap`: canonical schemes go through the rules then the process default (`SetDefault`, `Resolve`, `IsCanonical`, `FromEntries`, `Merge`), `file:///` is the path; `sharemap.go` installs the home's map (env wins) every 5 min; `handlers/direct.go`, `server/server.go` (sidecar), `grpcsrv/server.go` (transcode input) resolve; `channel/station.go`, `hdhr/guide.go` let canonical assets through to the map.

**Web (`web/src/`)** — `lib/clusterApi.ts` (types, endpoints); `pages/Admin/Machines.tsx` (per-share rows in the storage check, "Share → local mount" editor for nodes and the home, Storage servers editor); `i18n/en.json`, `ar.json`.

**Fixtures** — `shared/testdata/sharepath/net_use_windows.txt` (recorded on the Windows node), `mount_macos.txt`, `proc_mounts_linux.txt`.

## 2. Tests

| Area | File |
|---|---|
| parse / format, NFC, char maps both ways, catia lists | `pipeline/tests/sharepath/test_canonical.py` |
| `net use`, WNet drives, macOS `mount`, `/proc/mounts` on the fixtures | `pipeline/tests/sharepath/test_mounts.py` |
| resolver per OS (drives + UNC fallback, `/Volumes` + NFS of the same share, Linux cifs/nfs4), precedence, `file:///`, char map on SMB only, NFC/NFD and the two real Arabic names, share list | `pipeline/tests/sharepath/test_resolver.py` |
| migration on SQLite and Postgres: dry run, apply, idempotent, revert, changed-since kept, `--include-local`, verification sample, every value NFC | `pipeline/tests/cluster/test_sharepath_migrate.py` |
| per-share storage check, inference with canonical samples, scanner storing canonical paths, stage context opening a canonical path, the home reporting its shares (SQLite + Postgres) | `pipeline/tests/cluster/test_sharepath_access.py` |
| open-time Unicode fallback (PR #79) | `pipeline/tests/cluster/test_pathmap_unicode.py` |
| API map, roots stored canonical, overlap, storage-server CRUD, home override, PG store | `api/internal/sharepath/*_test.go`, `api/internal/handlers/libraries/canonical_test.go`, `api/internal/handlers/cluster/storage_test.go`, `storage_pg_test.go` |
| streaming map with canonical paths, merge precedence, direct play through the share map | `streaming/internal/pathmap/canonical_test.go`, `streaming/internal/handlers/direct_canonical_test.go` |
| Machines page: per-share rows, home mounts and storage servers | `web/src/pages/Admin/Machines.test.tsx` |

Postgres runs use the scratch server (`MAKTABA_TEST_PG_URL=postgresql://postgres@127.0.0.1:55432/postgres`).

## 3. Coordination

- `feat/tv-cluster-streaming` (Epic 32.1): streaming nodes read `cluster_nodes.path_map`
  (`NodePathMap`). A node without an admin map now reports its whole resolver there,
  shares included, and `pathmap.Apply` resolves `smb://` paths. Rebase onto main after it
  merges. Its `FromJSON` and this branch's `FromEntries` / `SetDefault` are separate
  functions.
- `fix/storage-check-unknown-size` (PR #75) touches `check_storage`. This story adds
  `check_shares` next to it.
- Slot 0124: main holds 0122 (`display_titles`) and 0123 (`tv_stream_cluster`).
