# Plan 32.1 — Streaming nodes and capacity — implementation

> Implementation plan for [story-32-01](story-32-01-streaming-nodes-capacity.md).
> Design: [design.md](design.md) §2, §5. Writes slot **0120** (extends `streaming_hosts` from 0117,
> `channel_placements`, `tv_placement_generation_seq`, `tv_cluster_secret`, `tv_host_beat`,
> `tv_placement_ack`).
> `feat/tv-core` is on `main` (PR #63; slot 0117, `OutputSettings`, encoder gates).

## 0. Decisions

| # | Decision | Rationale |
|---|---|---|
| D1 | A node's streaming role reuses the Epic 31 node config; no second join. | One approval per machine; the node's role already reads channel tables. |
| D2 | Hosts write their row only through `tv_host_beat` (SECURITY DEFINER, own row); tv-core's direct heartbeat upsert of `streaming_hosts` (0117) moves into the function. | Same guard as `cluster_node_beat`; a node cannot edit another host or the placements. |
| D3 | Capacity is computed by the host (it knows its encoder) and may be overridden by the admin. | `EffectiveMaxActive` lives in tv-core's streaming code. |
| D4 | The cluster stream key is 32 random bytes in `tv_cluster_secret` (singleton, REVOKE from `maktaba_worker`), created by the home at first boot and returned by `tv_host_beat` to each host. | The function must be able to read it, so it cannot be in `home_secrets` (sealed with a key file outside the database). Rotation: replace the row; hosts pick it up within one beat; the front door accepts both keys for 10 s. |
| D5 | Node mode mounts only signed channel routes and health. | R1.4; closes the open streaming port on nodes. |
| D6 | Art for stations on nodes comes from the home over signed HTTP, cached by hash. | Art files live on the home's disk (`plan_channels`/`write_channels` are home-only). |

## 1. Files

**Create**
- `shared/db/migrations/0120_tv_stream_cluster.sql`, `.sqlite.sql`
- `streaming/internal/cluster/host.go` — host identity, heartbeat loop, capability detection
- `streaming/internal/cluster/capability.go` — encoders, `ffmpeg -protocols`, yt-dlp, load (CPU per OS: `/proc/stat`, `host_processor_info` via `sysctl`, Windows PDH; NVENC sessions from the encoder gate)
- `streaming/internal/cluster/sign.go` — HMAC sign/verify (shared with 32.3)
- `streaming/internal/cluster/assets.go` — art cache client (node) + `/internal/tv/assets` handler (home)
- `streaming/internal/cluster/*_test.go`
- `pipeline/tests/db/test_tv_stream_cluster_migration.py`

**Modify**
- `streaming/main.go` — node mode wiring: mount set, no SSDP, heartbeat goroutine, asset resolver into `StationConfig`
- `streaming/internal/channel/station.go` — `localAsset` through an `AssetResolver` interface (home: local path; node: cache)
- `cmd/maktaba-server/cmd_serve.go`, `internal/supervisor/env.go` — when a node config is present and the role is `streaming`, pass `DATABASE_URL`, `MAKTABA_NODE_ID`, `MAKTABA_TV_HOST_ROLE=node`, home API URL
- `cmd/maktaba-server/internal/config` — read the Epic 31 node config path (same resolution as the pipeline: `MAKTABA_NODE_CONFIG`, OS default)
- `api/internal/handlers/rules/schema.json` + `schema.go` — section `tv_cluster` (`nodes_stream`, `policy`, `spill_at`, `hls_mode`, `failover`, `dead_after_seconds`, `pins`), en + ar descriptions
- `shared/db/migrations/MANIFEST.md` — row for 0120

## 2. Migration 0120 (Postgres)

```sql
-- +goose Up
-- streaming_hosts exists since slot 0117 (feat/tv-core, PR #63): host_id TEXT pk, host, role,
-- stream_base_url, encoder, max_active (= capacity), active_channels, nvenc_free, vt_free,
-- started_at, heartbeat_at. Epic 32 extends it instead of adding a second hosts table.
ALTER TABLE streaming_hosts ADD COLUMN IF NOT EXISTS enabled BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE streaming_hosts ADD COLUMN IF NOT EXISTS draining BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE streaming_hosts ADD COLUMN IF NOT EXISTS stream_base_url_source TEXT NOT NULL DEFAULT 'reported'
  CHECK (stream_base_url_source IN ('reported','admin'));
ALTER TABLE streaming_hosts ADD COLUMN IF NOT EXISTS encoders JSONB NOT NULL DEFAULT '[]';
ALTER TABLE streaming_hosts ADD COLUMN IF NOT EXISTS capacity_override INTEGER;
ALTER TABLE streaming_hosts ADD COLUMN IF NOT EXISTS weight INTEGER NOT NULL DEFAULT 1;
ALTER TABLE streaming_hosts ADD COLUMN IF NOT EXISTS active JSONB NOT NULL DEFAULT '[]';
ALTER TABLE streaming_hosts ADD COLUMN IF NOT EXISTS load JSONB NOT NULL DEFAULT '{}';
ALTER TABLE streaming_hosts ADD COLUMN IF NOT EXISTS can_push JSONB NOT NULL DEFAULT '{}';
ALTER TABLE streaming_hosts ADD COLUMN IF NOT EXISTS can_live BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE streaming_hosts ADD COLUMN IF NOT EXISTS unfit TEXT;
ALTER TABLE streaming_hosts ADD COLUMN IF NOT EXISTS version TEXT;
-- existing rows: the home keeps streaming; nodes stay off until the admin switches them on
UPDATE streaming_hosts SET enabled = (role = 'home') WHERE NOT enabled;
CREATE SEQUENCE IF NOT EXISTS tv_placement_generation_seq;
CREATE TABLE IF NOT EXISTS channel_placements (
  channel_id   UUID PRIMARY KEY REFERENCES channels(id) ON DELETE CASCADE,
  host_id      TEXT NOT NULL REFERENCES streaming_hosts(host_id) ON DELETE CASCADE,
  generation   BIGINT NOT NULL,
  state        TEXT NOT NULL CHECK (state IN ('placing','live','moving','released')),
  reason       TEXT NOT NULL,
  placed_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  acked_at     TIMESTAMPTZ,
  released_at  TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS channel_placements_host_idx ON channel_placements (host_id) WHERE state <> 'released';
CREATE TABLE IF NOT EXISTS tv_cluster_secret (
  id          SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
  stream_key  BYTEA NOT NULL,
  previous    BYTEA,
  rotated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- tv_host_beat(host_id, info) / tv_placement_ack(channel_id, generation): SECURITY DEFINER,
-- guard: the home's own user, or cluster_nodes.db_role = session_user for host_id (as 0103).
-- maktaba_worker: REVOKE ALL; GRANT SELECT on hosts and placements; nothing on tv_cluster_secret;
-- EXECUTE on the two functions only.
-- Node rows: FK-less link to cluster_nodes (host_id = cluster_nodes.id), cleaned by the
-- cluster revoke/remove path (API) — not a FK, because the home row has no cluster_nodes row.
```

SQLite twin: the same `ADD COLUMN`s on `streaming_hosts` (JSONB as TEXT) and tables, no roles, no functions (the Go store does the upsert directly).

## 3. Steps

1. Migration + MANIFEST row + Python up/down test.
2. `cluster.Capability`: VideoToolbox (darwin + test encode of 1 frame, as `DetectEncoder` does for
   NVENC), NVENC (existing detection), software always; `can_push` by parsing `ffmpeg -protocols`
   (`rtmps` needs TLS; `srt` needs libsrt); `can_live` = yt-dlp resolvable (`ytdlp.go`).
3. Capacity: `OutputSettings.EffectiveMaxActive(enc)`; NVENC: `min(that, nvenc_max/2)` when
   `nvenc_max > 0`; then `capacity_override` from the beat answer.
4. `cluster.HostLoop`: at start upsert the home row (home) or rely on the first beat (node); every
   5 s call `tv_host_beat`; apply `enabled/draining/capacity_override` to the engine (the registry
   cap follows `capacity`); keep the stream key in memory.
5. Node mode in `streaming/main.go`: env `MAKTABA_TV_HOST_ROLE=node` → mount `HTTPHandler` behind
   `cluster.VerifyMiddleware`, mount `/internal/tv/health`; skip `hdhr` mount and
   `SSDPController`; skip guide handlers and session routes.
6. Path map: on start and every 5 min, `cluster_node_beat(node)` → `pathmap.Parse` into the
   `StationConfig` (only when `MAKTABA_STREAM_PATH_MAP` is unset).
7. Assets: home handler `GET /internal/tv/assets/{channel}/{kind}` (signed) serves the effective art
   file with `ETag: sha256`; node `AssetResolver` downloads to `<cache>/tv-assets/<sha>` and returns
   the local path; `Station.currentLook` uses it (TTL 1 min as today).
8. Rules section `tv_cluster` (schema + validation + en/ar), read by streaming through the same
   settings reader tv-core adds (`settings.go`).
9. `maktaba-server`: detect the node config; for `--role streaming` export the env above; docs line
   in `cmd/maktaba-server/README.md`.

## 4. Tests

Unit: capability parsing, capacity table, sign/verify, asset cache (httptest), node-mode route set,
heartbeat loop with a fake store and fake clock. Postgres (skip if absent): `tv_host_beat` guard
with two node roles. Python: migration up/down on SQLite.

## 5. Rollout

Ships dark: `nodes_stream=false`; the home's row is created and beats; nothing else changes. The
Streaming hosts page (32.5) is the first visible piece.

## 6. Ordering

Slot 0119 (`home_secrets`, plan 32.6 §2) is independent and may land first; this plan does not
need it. Slot 0120 needs the cluster tables of Epic 31 (slot 0103) only.
