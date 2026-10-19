# Story 35.11 — Discovery of every machine

## Description

*As a user, I want to find Maktaba from any device on my network and land on the right cluster,
whether I pick the home or another machine; as the owner, I want every machine to be found without
typing an address.*

Today only the home advertises `_maktaba._tcp` (the supervisor's advertiser in
`cmd/maktaba-server/internal/mdns` and the API's in `api/internal/discovery/mdns.go`, Epic 31 R1.1).
A full node (story 35.8) is a complete Maktaba with its own web address, so it advertises too, with
TXT keys that say which home it belongs to. The apps group what they find by cluster, prefer the home,
and fall back to any reachable machine of the same cluster. `maktaba-pipeline join` keeps finding
homes only.

Design: [design.md](design.md) §12; README R7.

## Acceptance criteria

- **AC1** In node mode only the supervisor advertises (the API's advertiser is not started); on the
  home the two advertisers converge into one publication (no duplicate instance names).
- **AC2** TXT keys on every machine: `protocol=1`, `role=home|node`, `id` (this machine's instance or
  node id, 8 hex), `home` (the home's instance id, 8 hex; equal to `id` on the home), `web` (web port),
  `version`, `name` (machine name); the home keeps `schema_rev` and `join`. Never a library or user
  name (Epic 15 privacy rule).
- **AC3** A node re-publishes when its version changes (after an update) or its web port changes;
  `MAKTABA_DISCOVERY_MDNS=0` turns advertising off on any machine.
- **AC4** `maktaba-pipeline join` (`cluster/discover.py`) ignores entries with `role=node`; the
  subnet-probe fallback ignores answers whose `/api/system/version` says `role: node`.
- **AC5** Desktop (`apps/desktop/src-tauri/src/lib.rs` `discover_servers`), mobile and TV app pickers
  group results by `home`: one row "<home name> (N machines)", expandable to the machines.
- **AC6** Connecting chooses the home when it answers `/api/system/version` within 1.5 s, else the
  first reachable machine of the same `home` id; the stored server is remembered by home instance id
  plus the last good addresses, not by a single address.
- **AC7** `/api/system/version` (local class) reports `role`, `home_instance_id`, `node_id` (nodes) and
  `web_url`, so a manually typed node address is recognised as part of its cluster.
- **AC8** The Machines page links each machine's name to its `web_url` (from `cluster_node_report`).

## Test cases

- **TC1** `TestAdvertiseTXTNodeMode` (`cmd/maktaba-server/internal/mdns`): node config present → TXT
  has `role=node`, `home=<8 hex>`, `web`, no `join`.
- **TC2** `TestAdvertiseHomeSinglePublisher`: home → one publication carrying `schema_rev`, `join`.
- **TC3** `TestAdvertiseRepublishOnVersionChange`.
- **TC4** `test_discover_skips_nodes` (`pipeline/tests/cluster/test_discover.py`): a fake zeroconf
  answer with `role=node` is not offered as a home.
- **TC5** `discover_groups_by_home` (Rust unit test in the desktop crate).
- **TC6** `serverPicker.test.ts` (mobile/desktop shared picker): grouping, home preferred, fallback to
  a node when the home times out, remembered by instance id.
- **TC7** `TestVersionEndpointNodeFields` (`api/internal/system`).

## Edge cases

- **EC1** A node whose home is gone (revoked or unreachable) keeps advertising with its `home` id; the
  picker shows the cluster as "home unreachable" and still lets the user open the node (paused page).
- **EC2** Multicast blocked: typing any machine's address works (AC7) and the picker learns the
  cluster's other addresses from `GET /api/cluster/self` on that machine.
- **EC3** Two clusters on one LAN: grouped separately by `home` id; names may collide, ids do not.
- **EC4** An old app that does not know `role`: it lists nodes as servers; opening one works (a full
  node is a full Maktaba).

## Dependencies

35.8 (node mode). Epic 31 R1 (advertiser, browser); Epic 15 (privacy rule).
