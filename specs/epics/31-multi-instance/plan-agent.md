# Plan — The cluster channel (Epic 31, phase 2)

Design: [design-agent.md](design-agent.md) (section numbers below, `§n`, refer to it).
Phase 1: [design.md](design.md), [plan.md](plan.md). User documentation:
[docs/cluster](../../../docs/cluster/README.md).

Branch: `feat/cluster-agent` (integration branch; one PR into `main` at the end). Each track
works in its own worktree on its own branch cut from `feat/cluster-agent` and is merged back
by the integration step.

## Order

```
F   foundation: design-agent.md, this plan, docs/cluster, agent_vectors.json   ── done first
├─ T4  hub (Go, api/)                     ┐
├─ T5  agent (Python, pipeline/)          │
├─ T6  web (React, web/)                  ├─ in parallel, each in its own worktree
├─ H1  end-to-end harness (tools/dev/)    │
└─ P1  worker service packaging (deploy/) ┘
I   integration: merge T4 → T5 → T6 → H1 → P1, gates, end-to-end checklist, review, one PR
```

T4 and T5 meet on the wire protocol; both start by writing their shared fixtures (below)
from the design, so neither waits for the other's code.

## Contracts between tracks

| Contract | Source of truth | Producer | Consumers | Fixture (written first, by the producer) |
|---|---|---|---|---|
| C1 Agent handshake and sealing | §4.2–§4.4 | — | T4, T5 | `shared/testdata/cluster/agent_vectors.json` (exists; do not change) |
| C2 Agent frames | §5 | T5 | T4 | `shared/testdata/cluster/agent_frames.json`: one plaintext example of every frame type and `job` event |
| C3 Close codes, limits, versioning | §4.5–§4.7 | T4 | T5 | in `agent_frames.json` (`close_codes` table) |
| C4 Browser socket messages and topics | §6.4 | T4 | T6 | `shared/testdata/cluster/hub_messages.json`: every browser→hub `op` and hub→browser `type`, one per topic |
| C5 Files on the share | §7.4–§7.6 | T5 | T4 (REST reader), T5 (CLI), H1 | `shared/testdata/cluster/files/`: a small cluster directory (two IP folders, every kind, two days, a partial last line, a capped file) |
| C6 Share root, path map, IP folder | §7.1–§7.3 | T5 | T4 | `shared/testdata/cluster/share_root_cases.json`: library roots → share root; path maps → local cluster directory per OS |
| C7 LLM wire | §8.2–§8.4 | T4 (door, router), T5 (executor) | H1 | none shared; each side tests against the OpenAI chunk format |
| C8 REST for the web | §6.7 | T4 | T6 | `hub_messages.json` carries a sample `GET /api/cluster/processes` and a `files` listing |
| C9 Process behaviour for service managers | below (P1) | T5 | P1 | — |

**C9.** `maktaba-pipeline` is an installed command (console script, added by T5). `run`
exits with code 0 after a revocation (so "restart on failure" does not loop) and with code 2
when it is neither given a database URL nor joined; any other stop on error is non-zero.
`MAKTABA_NODE_CONFIG` selects the node config file. SIGTERM (Windows: CTRL_BREAK / service
stop) drains and exits as today.

Fixtures are reviewed against the design by the integration step; a fixture change after
day one is announced to the consuming tracks.

## T4 — Hub (Go, `api/`)

**Scope.** The agent endpoint and protocol, the hub's live state and fan-out, the browser
WebSocket, commands and revocation, the home's own files and the REST readers, the share
root and IP rule in Go, the LLM router and the OpenAI-compatible door.

**Owned files.**

- `api/internal/cluster/agent/` — handshake (node and home kinds), key derivation, sealing
  with counters, frame types, close codes, limits; a Go test client (`agent/agenttest`) that
  speaks the protocol for tests.
- `api/internal/cluster/hub/` — sessions, live state, `replaced`, revocation, commands,
  `reload_stages` triggers, topics and per-browser queues, the queue snapshot loop (reusing
  `queues.Handler.BuildSnapshot`), the library snapshot.
- `api/internal/cluster/clusterfiles/` — share root rule, `MAKTABA_PATH_MAP` parsing (the
  `--path-map` format), IP rule, the hub's writers (`events`, `queues`, `library`),
  retention sweep, the reader (merge by `ts`, partial last line) for REST.
- `api/internal/cluster/llmrouter/` — runtime registry keyed by (machine, provider, base
  URL), selection, FIFO per model with timeout, id mapping, cancel, `machine_gone`.
- `api/internal/handlers/clusterhub/` — `GET /api/cluster/agent`, `GET /api/cluster/ws`,
  `GET /api/cluster/processes`, `POST /api/cluster/processes/{session}/cmd`,
  `GET /api/cluster/files[/{ip}/{kind}]`, `/api/cluster/llm/v1/{models,chat/completions,embeddings}`.
- Wiring: `api/internal/router/p31.go`, `api/main.go`, `api/cluster_bootstrap.go` (home
  agent key, settings), `api/internal/auth/middleware/middleware.go` (allowlist
  `/api/cluster/agent` only) and its allowlist test.
- Phase 1 hooks in `api/internal/handlers/cluster/nodes.go`: revoke → `hub.Revoke(node)`;
  switch or drain change → `hub.ReloadStages(node)`.
- `api/go.mod` / `go.sum`: `github.com/gorilla/websocket` v1.5.3 (as `cloud/` uses).
- Fixtures C4, C8.

**Tasks.**

1. Agent protocol package; vector test (node and home keys, proof, the three frames).
2. Home agent key: create at first start (0600, `~/.maktaba/cluster/home-agent.key` or
   `MAKTABA_HOME_AGENT_KEY`).
3. Agent endpoint: upgrade, 10 s handshake deadline, rate limit 30/min per address, node
   lookup through the phase 1 store, `welcome.settings`, ping/pong, close codes.
4. Hub: live state per §6.1, sessions per worker id, revocation per §6.2, commands per §6.3.
5. Browser socket per §6.4: auth and `Origin`, topic access, snapshots, envelopes, slow
   browser policy; queue snapshot pushed on change at most once a second.
6. Files: share root (resolved once a minute, sent in `welcome`), IP folder, hub writers,
   retention sweep, REST readers with strict validation (`filepath.Clean` and a within-base
   check at the sink — the shape CodeQL recognises).
7. LLM router and the OpenAI door (SSE in the OpenAI format; error mapping of §8.4).
8. Settings of §9 read from the environment.

**Tests.** Vectors; handshake negatives (unknown, pending, revoked → 4003; wrong proof →
4004; slow handshake → 4008; unknown version → 4001; text after welcome → 4000; counter gap,
repeat, bad tag → 4011; oversize → 4012; rate limit → 4013); `replaced`; revocation closes
every session of a node within 1 s and refuses reconnects; commands round-trip with
timeout; browser topics (signed-in vs admin, `Origin`), snapshots, `dropped` for a slow
reader; share root cases (C6) and IP rule; writer one-file-per-writer, daily rotation with a
fake clock, cap, retention; reader merge and partial line (C5); router selection order,
FIFO fairness, `no_model`, `no_capacity` timeout, `machine_gone`, cancel on requester close;
OpenAI door streaming and non-streaming. All with `-race`. Postgres-dependent tests skip
when Postgres is unavailable, as phase 1's do.

**Acceptance.** The Go test client, using the vector keys, completes a handshake and
exchanges sealed frames that decode to the vectors' plaintexts; a fake agent's `job` frames
reach a subscribed browser test client in order; revocation closes the sessions with 4010;
the REST reader serves the C5 tree merged by `ts`; a router test with two fake machines
routes, queues and fails exactly as §8.2 says; Go gates green, coverage floor held.

## T5 — Agent (Python, `pipeline/`)

**Scope.** The agent in every worker process (both kinds), metrics, log shipping, job
events, commands, the files on the share and their CLI readers, the LLM runtimes, executor
and `cluster_llm()`.

**Owned files.**

- `pipeline/src/maktaba_pipeline/cluster/channel/` — `crypto.py` (HKDF, HMAC, sealing with
  counters, on `cryptography`), `protocol.py` (frames, close codes, limits), `client.py`
  (connect, handshake, ping, reconnect with backoff and jitter), `agent.py`
  (`ChannelAgent`; the name `WorkerAgent` is taken by phase 1's `cluster/node.py`).
- `pipeline/src/maktaba_pipeline/cluster/metrics.py` — system, GPU (NVIDIA via `pynvml` when
  present, Apple via MLX when present), process, pipeline and LLM sections of §5.2.
- `pipeline/src/maktaba_pipeline/cluster/files.py` (share root, IP folder, writers, caps,
  retention, `processes/*.json`, local fallback) and `cluster/reader.py` (merge, follow).
- `pipeline/src/maktaba_pipeline/cluster/cli.py` — `maktaba-pipeline cluster where|ls|logs|metrics|jobs|llm|events|queues|library`.
- `pipeline/src/maktaba_pipeline/llm/` — runtime discovery (Ollama, OpenAI-compatible),
  executor (max parallel, cancel, usage record), `cluster_llm()`.
- Hooks: a structlog processor feeding the shipper and the log file
  (`maktaba_pipeline/log/`); job events from the engine (`engine/dispatch.py`,
  `engine/hooks.py`); `reload_stages` → the phase 1 `StageSelector` refresh
  (`cluster/node.py`); `drain`; revocation by `disconnect` → the phase 1 graceful stop.
- `runtime.py` / `__main__.py`: start the agent (unless `MAKTABA_CLUSTER_AGENT=0`); kind
  `home` (home agent key, `MAKTABA_API_URL`) or `node` (node config); the `cluster`
  subcommand; exit codes of C9.
- Scanner and watcher skip `<share root>/maktaba/`.
- `pipeline/pyproject.toml`, `uv.lock`: base dependencies `websockets`, `psutil`; console
  script `maktaba-pipeline = "maktaba_pipeline.__main__:main"`.
- The Windows unit job (`.github/workflows/_unit.yml`) runs `tests/cluster` including the
  new tests.
- Fixtures C2, C5, C6.

**Tasks.**

1. Channel crypto and protocol; vector test.
2. Client and `ChannelAgent`: handshake for both kinds, sealed frames, ping, backoff
   (1 s → 30 s, ±20 %, reset after 60 s), close code handling of §4.6, buffers of §5.5.
3. Metrics every `metrics_every` seconds; log shipping (batched within 250 ms, level from
   `welcome` and `cmd log_level`); `job` frames for every event (progress at most 1/s).
4. Files: share root (from `welcome`, else computed from the database), path map, IP
   folder, writers per kind and day (UTC), flush every 2 s off the worker's path, caps,
   `processes/*.json` refresh, retention, local fallback with 5-minute re-check, `files`
   section in metrics.
5. CLI readers.
6. LLM: discovery every 30 s and `llm.models`; executor for `llm.request` (OpenAI bodies to
   the runtime's `/v1` endpoints, chunks passed through, cancel); `cluster_llm()` local-first,
   else over the channel; usage lines in the `llm` file, no text.
7. Commands of §5.4.
8. Dependencies, console script, scanner and watcher exclusion.

**Tests.** Vectors; handshake and frames against a small in-test fake hub (server side of
the protocol, test-only); every close code's reaction; backoff timing with a fake clock;
buffer limits and `dropped`; metrics shape against C2; log level changes; job events from a
fake job run; files: one writer per file, rotation at UTC midnight (fake clock), caps,
partial-line reader, retention, fallback and return (temp dirs as the share), share root
and IP cases (C6, monkeypatched sockets), Windows paths (drive letters, UNC) on the Windows
job; CLI output and `--follow`; LLM discovery against a fake Ollama and a fake
OpenAI-compatible server (in-test HTTP), executor parallel limit and cancel;
`cluster_llm()` local vs remote. `mypy --strict` (tests included), `ruff`, `pytest -m unit`,
both databases where the database is touched.

**Acceptance.** A worker started against the T4 test hub (in integration) or the fake hub
(here) connects, sends metrics every 2 s, ships logs at the session's level, reports every
job event, writes the C5 layout under a temp share, and stops without reconnecting after
`4010`; `maktaba-pipeline cluster logs --follow` prints lines from two processes merged by
time; Python gates green on Linux and the cluster tests green on Windows.

## T6 — Web (`web/`)

**Scope.** One hub client for the app, the Processing page on the hub with the SSE
fallback, the Machines page's live views and history.

**Owned files.**

- `web/src/lib/hub.ts` — one WebSocket per tab, ref-counted topic subscriptions,
  reconnect with backoff and re-subscribe, fallback detection (two failed opens → SSE
  through the existing `ws.ts`, WebSocket retried every 60 s), `cmd()` resolving on
  `cmd.result`, `dropped` surfaced.
- `web/src/lib/hubTypes.ts` — the message and topic types of §6.4 (checked against C4).
- `web/src/lib/useQueueStream.ts` — fed by the hub, or by the SSE streams in fallback; the
  same outputs for `QueueOverview` and `QueueDetail`.
- `web/src/pages/queue/*` — running jobs on every machine with live progress and the
  machine's name; worker strip with machine names; a quiet fallback indicator.
- `web/src/pages/Admin/Machines.tsx` (and new components beside it) — per machine:
  processes, metrics (small charts), jobs, log tail with level and follow, LLM runtimes and
  models, per-process commands (log level, drain); a history panel reading
  `/api/cluster/files/{ip}/{kind}` with a date picker.
- `web/src/lib/clusterApi.ts` — REST additions of §6.7.
- `web/src/i18n/en.json`, `ar.json` — every new string in English and Arabic.

**Tests (vitest, testing-library, a fake WebSocket).** Subscribe/unsubscribe ref counting;
reconnect re-subscribes and replaces snapshots; fallback after two failed opens and return
to the WebSocket; `cmd()` resolves and times out; Processing page renders lanes and running
jobs from hub messages (C4) and from SSE in fallback; Machines live views render metrics,
logs and LLM sections; admin-only sections hidden for a non-admin; `dropped` notice; Arabic
render; the existing a11y test extended.

**Acceptance.** With the C4 messages replayed through the fake socket, the Processing page
shows a job pulled on a machine from `claimed` to `done` with progress and the machine's
name, and the Machines page shows that machine's processes, metrics and log tail; with the
WebSocket refused, the Processing page works on SSE; web gates green.

## H1 — End-to-end harness (`tools/dev/`)

**Scope.** A one-host cluster for development and for the integration checklist: the home
(API, Postgres, a home worker) and two pretend machines with their own node configs, IP
folders and fake LLM runtimes, on a temporary share.

**Owned files.**

- `tools/dev/cluster.sh` — `start|stop|status|join|approve`: uses the dev database (as
  `tools/dev/stack.sh` does), creates a temporary share root with a tiny library (a few
  short generated videos), starts the API with `MAKTABA_SHARE_ROOT` and `MAKTABA_ADMIN_TOKEN`,
  a home worker, and machines `m1`, `m2` (each: its own `MAKTABA_NODE_CONFIG`,
  `MAKTABA_CLUSTER_IP=10.99.0.N`, `MAKTABA_OLLAMA_URL` to its own fake runtime); joins each
  machine by reading the printed code and approving it through the admin API.
- `tools/dev/fake_llm.py` — a stdlib HTTP server speaking enough Ollama (`/api/tags`,
  `/api/ps`) and OpenAI (`/v1/models`, `/v1/chat/completions` streaming and not,
  `/v1/embeddings`) to exercise routing; a model list and a delay per instance.
- `tests/e2e/cluster/` — pytest scenarios driving the harness through the REST API, a
  WebSocket client on `/api/cluster/ws`, the files on the temporary share and the CLI.
- `Makefile` — `e2e-cluster` target.

**Tests.** The harness is the test; it must start and stop cleanly twice in a row on macOS
and Linux and leave nothing running.

**Acceptance.** `make e2e-cluster` runs the integration checklist below end to end in under
five minutes on a developer machine.

## P1 — Worker service packaging (`deploy/packaging/`)

**Scope.** Installing the pipeline on a machine and running it as a service on macOS,
Linux and Windows, with its node config where a service can read it and the share mounted
before it starts. (The home server's own packaging is unchanged.)

**Owned files.**

- `deploy/packaging/worker/README.md` — what each file is, how it is installed.
- `deploy/packaging/worker/install-worker.sh` (macOS, Linux) and
  `deploy/packaging/worker/install-worker.ps1` (Windows): install `uv` when missing, install
  `maktaba-pipeline` from the release (with the extras asked for), create the service, print
  the next step (`join`).
- `deploy/packaging/worker/systemd/maktaba-worker.service` — runs `maktaba-pipeline run` as
  a dedicated user; `After=network-online.target remote-fs.target`, a drop-in for
  `RequiresMountsFor=<share mount>`; the share mount writable (unlike the server unit's
  `ProtectSystem=strict` set); `Environment=MAKTABA_NODE_CONFIG=/etc/maktaba/node.json`;
  `Restart=on-failure`.
- `deploy/packaging/worker/launchd/com.maktaba.worker.plist` — a LaunchDaemon with
  `KeepAlive` on unsuccessful exit only and `MAKTABA_NODE_CONFIG` set.
- `deploy/packaging/worker/windows/` — a startup scheduled task (native, nothing extra to
  download) running as a chosen account, restart on failure, `MAKTABA_NODE_CONFIG` under
  `C:\ProgramData\Maktaba\`, access cut to that account; UNC paths recommended (mapped
  drive letters are not visible to services).
- `.github/workflows/release.yml` — build and publish the `maktaba-pipeline` wheel with the
  other release assets; a new `.github/workflows/_packaging.yml` (shellcheck, `plutil -lint`,
  `systemd-analyze verify`, PowerShell parse check) wired into `ci.yml` by the integration
  step.

**Tests.** The lint job above; the Linux installer run in a container against a wheel built
from the branch, ending with `maktaba-pipeline --version` and `systemd-analyze verify`; the
Windows script parsed on `windows-latest`.

**Acceptance.** On each OS the README's steps install the worker, `join` as the service's
account writes the config the service reads, the service starts after the share is mounted,
survives a reboot, does not restart after a revocation (C9), and stops cleanly.

## I — Integration

1. Merge T4, then T5 (run T5's channel tests against the real T4 endpoint, not only the
   fake hub), then T6, H1, P1 into `feat/cluster-agent`.
2. Gates: Go (lint, unit with `-race`), Python (ruff, mypy strict, unit on both
   databases), web (lint, typecheck, vitest), the Windows cluster job, packaging lint,
   migration lint (**no migration in this phase**), coverage floors, CodeQL (new alerts on
   the REST file readers and the LLM door are fixed, not dismissed, unless false positives).
3. Update [docs/cluster](../../../docs/cluster/README.md) with what the tracks changed (P1's
   service steps replace the generic ones in `join-a-machine.md`).
4. Run the checklist below with H1, then on real machines (the Mac home, one Linux, one
   Windows machine on the NAS).
5. Review (code-review skill), fix, one PR.

### Integration and end-to-end checklist

Connection and security

- [ ] A machine's worker and a home worker both connect; the hub lists them within 5 s.
- [ ] A wrong pairing key gets 4004; an unapproved machine 4003; a tampered frame 4011.
- [ ] Revoking a machine closes all its sessions within 1 s; its worker stops and does not
      come back; its service does not restart it.
- [ ] Stopping the API: workers keep pulling and writing files; on restart everything
      reconnects and the live view is whole within 5 s; `dropped` is reported when logs
      overflowed.

Queues and the Processing page

- [ ] A job pulled on a machine shows on the Processing page from `claimed` to `done` with
      live progress and the machine's name; the queue lanes follow within a second.
- [ ] Switching a queue off for a machine on the Machines page stops its processes pulling
      from it within 2 s; switching it back resumes.
- [ ] Draining a machine and draining one process both work and are shown.
- [ ] With WebSockets blocked, the Processing page works on the SSE streams and returns to
      the WebSocket when it is allowed again.

Files on the share

- [ ] Every process writes `processes/`, `logs/`, `metrics/`, `jobs/` (and `llm/` when it ran
      a request) under its IP folder; the hub writes `events/`, `queues/` (every 10 s),
      `library/` (every 5 min) under the home's.
- [ ] `maktaba-pipeline cluster logs --ip <other machine> --follow` on a machine shows the
      other's records; `cluster where` reports the right local path on macOS, Linux and
      Windows (drive letter and UNC).
- [ ] With the share unreachable a process falls back to its local folder, reports it in
      its metrics, and moves back when the share returns.
- [ ] Retention removes expired days; a capped file stops growing and the drop is counted.

LLMs

- [ ] A model on machine B only: a request from machine A streams through the hub and
      completes; B's `llm` file has a usage line with no text.
- [ ] `curl` with a personal access token on `/api/cluster/llm/v1/chat/completions` streams;
      `/models` lists every model with its machines; no token → 401.
- [ ] `no_model` at once; `no_capacity` after the timeout with the runtime busy;
      `machine_gone` when B stops mid-request; closing the client cancels B's work.

The database

- [ ] No migration, and no row written for logs, metrics, job events of the channel, LLM
      traffic or settings (the existing `health_samples` and `job_events` writes are
      unchanged — design §13.2).
