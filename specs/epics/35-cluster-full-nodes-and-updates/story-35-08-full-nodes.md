# Story 35.8 — Full nodes

## Description

*As the owner, I want every machine that joined my home to be a whole Maktaba — the same web app and
API at its own address, its workers, and TV streaming when I enable it — while the home stays the
only database, the only place migrations run and the only holder of secrets.*

`maktaba-server serve` decides node mode from the node config (`node.json`, Epic 31). In node mode
it runs `api` (node mode), `web`, `pipeline --node` and, only when Epic 32 streaming is enabled for
the machine, `streaming`. A Go machine agent in the supervisor holds the cluster channel for update
commands, delegation keys and revocations. Today's worker-only nodes convert with one command.

Design: [design.md](design.md) §9; README R1.

## Acceptance criteria

- **AC1** With a joined node config, `serve` starts `api`, `web`, `pipeline` (node mode) and
  `streaming` only when `tv_cluster.nodes_stream` and the machine's streaming switch are on; no flag.
- **AC2** The node API runs with `MAKTABA_NODE_MODE=1`, the node role's `DATABASE_URL`,
  `MAKTABA_HOME_API_URL` from the node config, and `MAKTABA_AUTO_MIGRATE` unset; it refuses to
  migrate in node mode and refuses to start when its role is superuser or has `CREATE` on the schema.
- **AC3** Home-only components are not constructed in node mode (hub, Updater and scheduler, TV
  controller, publishers, secrets key); their routes are `home` class (story 35.10).
- **AC4** The node API applies the schema guard (`api/internal/cluster/schemaguard`, same table and
  reason texts as Epic 31 §5.1) at start, every 5 min, every 30 s while paused; paused → `any` routes
  answer 503 `{code: "paused", reason}`, `local` routes work, `home` routes are still proxied.
- **AC5** The machine agent (`kind: "node"`, `process: "supervisor"`, `worker_id: "sup-<node8>"`)
  completes the v2 handshake with the node key, receives `keys` and `auth.revoked`, reports
  `update.state {version, prepared_version, platform, layout}`; the node API reaches it on loopback
  with the per-boot token `<data_dir>/supervisor/local.token`.
- **AC6** The node reports its web URL and `full_node = true` (slot S3, next free at implementation
  time); the Machines page links to it.
- **AC7** `maktaba-server setup --node` (and `install-node.sh --full`, `install-node.ps1 -Full`)
  keeps `node.json`, installs the layout with the home's current release from the home's mirror
  (verified), builds the venv, registers the launcher service (Windows: logon task by default,
  `-Mode Service` optional), maps `--instances N` to `[pipeline] instances = N`, and removes the old
  worker units only after the new service is healthy.
- **AC8** Worker-only nodes are converted automatically on their next update (`update.convert` to
  their pipeline agent, design §9.4); one too old for the command is shown "worker-only — re-run the
  installer once".
- **AC9** The node's streaming role runs for VOD and reads media from the NAS through the node's own
  mounts and the share-path resolver (story 31.3); no media byte goes through the home or the link
  except in the storage-failure fallback (design §11.5).

## Test cases

- **TC1** `TestServeNodeModeRoles` (fake node config; streaming on/off).
- **TC2** `TestNodeAPIRefusesMigrate`, `TestNodeAPIRefusesSuperuserRole` (Postgres integration).
- **TC3** `TestNodeModeSkipsHomeComponents`.
- **TC4** `TestSchemaGuardTable` (Go, shares fixtures with `streaming/internal/cluster/schema_test.go`).
- **TC5** `TestMachineAgentHandshakeVectors` (`shared/testdata/cluster/agent_vectors.json`).
- **TC6** `install-node-dry-run.sh --full`, `node-origin.Tests.ps1 -Full` (Pester) for conversion.
- **TC7** Integration: a converted node keeps its node id, path map and stage switches.

## Edge cases

- **EC1** Node config present but the home revoked the machine: the agent gets `4003 not_member`;
  the node shows the paused page with "this machine was removed from <home>".
- **EC2** Node joined before handshake v2 (no `home.cluster_pub`): the machine agent cannot connect;
  the conversion asks to re-join.
- **EC3** Port 8088 taken on the node: `server.toml` overrides; the reported web URL follows it.
- **EC4** A share mounted only in the user's session on Windows: the default logon task sees it, the
  service mode needs UNC paths (documented).

## Dependencies

35.1 (verification), 35.2 (layout, launcher); Epic 31 phase 1 and the channel v2; Epic 32.1
(streaming nodes).
