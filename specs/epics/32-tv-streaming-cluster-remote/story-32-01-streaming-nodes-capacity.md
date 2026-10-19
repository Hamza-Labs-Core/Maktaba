# Story 32.1 — Streaming nodes and capacity

## Description

*As the owner of a Maktaba cluster, I want to let a second machine (the Windows PC with an
NVIDIA GPU) encode TV channels too, so that more channels can be watched at once than the home
alone can carry — without pairing it again or typing its database address anywhere.*

Today the channel engine runs only where `maktaba-server serve` runs with the streaming role, and
Epic 31's `join` makes a node a pipeline worker only. This story makes a joined node able to run
the streaming role from its existing node config, and makes every streaming host (home included)
report what it can carry. Nothing is placed on a node yet (that is 32.2); a node that is switched
on simply appears, healthy, with its capacity.

Design: [design.md](design.md) §2, §5.

## Acceptance criteria

- **AC1** Cluster setting `tv_cluster.nodes_stream` (rules section `tv_cluster`, default `false`)
  and a per-host switch "Stream on this machine" (`streaming_hosts.enabled`, default `true` for the
  home, `false` for nodes). A node takes channels only when both are on.
- **AC2** `maktaba-server serve --role streaming` on a machine with an Epic 31 node config runs in
  node mode: database URL, node id and path map come from the node config and
  `cluster_node_beat`; no extra flag or environment variable is required.
- **AC3** In node mode the streaming service mounts only the signed channel routes and
  `/internal/tv/health`; HDHomeRun routes, XMLTV, M3U and video-session routes answer 404, and SSDP
  never starts (whatever `hdhr_device.enabled` says).
- **AC4** Every streaming host calls `tv_host_beat` every 5 s with: version, encoders it can use
  (detected at start: VideoToolbox, NVENC, software), capacity (`EffectiveMaxActive`, capped by
  the NVENC session cap / 2 on NVENC hosts), active channel ids, CPU and encoder load, stream base
  URL, `can_push` (rtmps, srt) from `ffmpeg -protocols`, `can_live` (yt-dlp present).
- **AC5** `tv_host_beat` writes only the caller's own row (node role checked against
  `cluster_nodes.db_role`; an error otherwise) and returns `enabled`, `draining`,
  `capacity_override` and the cluster stream key.
- **AC6** A node whose Epic 31 storage check cannot read the library reports `unfit: storage` and
  takes no channels; a node whose streaming protocol version differs from the home's reports
  `unfit: version`.
- **AC7** Channel art (logo, watermark, loading picture) for a station on a node is fetched from
  the home's signed `/internal/tv/assets/{channel}/{kind}` and cached by content hash; an art
  change on the home is picked up within the station's look TTL (1 min).
- **AC8** Slot 0123 extends `streaming_hosts` (created by tv-core in 0117; no second hosts table)
  with the columns of the README data model, creates `channel_placements` (used by 32.2) with
  `maktaba_worker` limited to SELECT, `tv_cluster_secret` (revoked), and the `tv_host_beat` /
  `tv_placement_ack` functions; SQLite twin without roles.
- **AC9** With `nodes_stream = false` nothing changes for users: the home is the only online host.

## Test cases

- **TC1** `TestNodeModeConfigFromNodeJSON` — a temp node config → DSN, node id, path map used.
- **TC2** `TestNodeModeRoutes` — `/discover.json`, `/xmltv.xml`, `/auto/v1` → 404 on a node;
  unsigned `/stream/channel/{id}/index.m3u8` → 403; signed → served.
- **TC3** `TestCapacityNVENCCap` — nvenc_max 6 → capacity 3; override 2 → 2; VideoToolbox → 16.
- **TC4** `TestHostBeatOwnRowOnly` (Postgres) — a node role beating another host id → error.
- **TC5** `TestCanPushDetection` — fake `ffmpeg -protocols` output with and without `rtmps`, `srt`.
- **TC6** `test_0123_up_down` — SQLite twin up/down; columns and defaults.
- **TC7** `TestAssetCacheFetchAndRefresh` — art fetched once, fetched again on a hash change.

## Edge cases

- **EC1** A node with two network interfaces: the stream base URL is the address on the route to
  the home; the admin can override it.
- **EC2** A node revoked in Epic 31: its role cannot log in, beats stop, its row turns down.
- **EC3** The PC sleeps: missed beats → down (32.4 handles its channels).
- **EC4** A home on SQLite (single machine): no nodes possible (as in Epic 31); the home row still
  exists.

## Dependencies

`feat/tv-core` (output settings, encoder gates, slot 0117), Epic 31 (node config, roles,
`cluster_node_beat`, storage check).
