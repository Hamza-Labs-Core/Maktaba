# Design — The cluster channel (Epic 31, phase 2)

Phase 1 ([design.md](design.md)) made machines join one home and share its queue. This
phase makes them a cluster you can watch and use as one: every Maktaba process holds a
live, authenticated, encrypted channel to the home, over which flow **real-time
metrics**, **logs**, **commands** and **LLM work**. Owner's requirements (2026-10-18):
metrics over a socket for every worker; LLMs running on any machine usable by all;
real-time streams; nothing of this through the database; what the machines send is
**saved to files by node IP** on the home; built as a solid layer, not a patch.

The database keeps what it is for: the catalog and the job queue.

## 1. Shape

```
 node process ─┐                                         ┌─ browser (admin)
 node process ─┼── WebSocket /api/cluster/agent ──► HOME API ── SSE /api/cluster/stream ──┤
 home process ─┘   (sealed frames, both ways)     │  hub ──► files <dir>/<ip>/…            └─ OpenAI-compatible
                                                  │  LLM router ◄── /api/cluster/llm/v1/*      clients
                                                  └─ commands, revocation (live)
```

Every pipeline process (home workers and node workers alike) runs an **agent**: it dials
the home's API over WebSocket on the API's own port (one port to open; works through
the same proxies as the web), proves who it is, then exchanges frames for as long as it
runs, reconnecting with backoff. The home's **hub** keeps the live state of every
connected process in memory, fans it out to browsers over Server-Sent Events, writes it
to the live state for browsers (each machine saves its own files on the share), routes LLM requests to the machines that hold the models, and sends
commands (drain, stage reload, log level, disconnect on revoke) down the same channel.

## 2. Handshake and encryption

Transport: WebSocket (`GET /api/cluster/agent`, public route — authentication is in the
protocol). Text frames before authentication, binary sealed frames after.

**A node process** proves it holds the X25519 key it paired with (`cluster_nodes.node_pubkey`):

1. C→S `{"t":"hello","v":1,"kind":"node","node_id","worker_id","pid","host","nonce":b64(16)}`
2. S→C `{"t":"challenge","server_pub":b64(32),"nonce":b64(16)}` — a fresh X25519 key per
   connection; refused unless the node is `approved`.
3. Both: `shared = X25519(node_priv, server_pub) = X25519(server_priv, node_pub)`;
   `okm = HKDF-SHA256(ikm=shared, salt=client_nonce‖server_nonce, info="maktaba-agent-v1", L=64)`;
   `k_c2s = okm[0:32]`, `k_s2c = okm[32:64]`.
4. C→S `{"t":"auth","proof":b64(HMAC-SHA256(k_c2s, "maktaba-agent-auth"‖server_nonce))}`
5. S→C sealed `{"t":"welcome","session":…,"settings":{…}}`; from here every frame is sealed.

**A home process** (same machine as the API) uses the home agent key instead of a pairing
key: the API writes 32 random bytes to `<cluster dir>/home-agent.key` (mode 0600) at start;
`hello.kind = "home"`, step 2 sends only the nonce, and `shared = home_key`.

**Sealed frame**: `ChaCha20-Poly1305(k_dir, nonce = 4 zero bytes ‖ 8-byte big-endian
counter, plaintext = JSON, aad = "")`; each direction's counter starts at 0 and increments
per frame; a gap, a repeat or a failed tag closes the connection. Interop vectors:
`shared/testdata/cluster/agent_vectors.json` (Go and Python both check them).

The node's credential is its pairing key, so revoking a node (phase 1) also stops its
channel: the hub closes its sessions at once and refuses new ones.

## 3. Frames (JSON inside the seal)

| `t` | direction | content |
|---|---|---|
| `metrics` | C→S, every 2 s | `ts`, `system` {cpu_pct, load1, mem_total, mem_available, swap_used, disks [{path,total,free}], net {rx_bps,tx_bps}}, `gpu` {nvidia [{name,util_pct,mem_used,mem_total,temp_c}], apple {active_gb,cache_gb,peak_gb}}, `process` {rss, cpu_pct, threads, uptime_s}, `pipeline` {role, stages, stages_off, jobs [{job_id,stage,subject,since,progress}], claims_per_min, stt {audio_sec_per_s, files_in_flight}}, `llm` {in_flight, tokens_per_s} |
| `logs` | C→S, batched ≤ 250 ms | `[ {ts, level, event, logger, fields} ]` at or above the session's log level |
| `job` | C→S, as it happens | `{event: claimed\|progress\|done\|failed\|skipped\|paused\|deferred, job_id, stage, subject_kind, subject_id, title, attempt, started_at, ts, duration_ms, progress {step, done, total}, outcome {kind, retryable, error}, output {audio_sec, segments, bytes, …}}` — every job this process runs |
| `dropped` | C→S | counts of log records dropped while disconnected (bounded buffer) |
| `llm.models` | C→S, on change | runtimes `[{provider, base_url, models [{name, size, family, quant, loaded}], max_parallel}]` |
| `llm.request` | both | `{id, model, op: chat\|generate\|embed, body, stream}`: S→C to the machine that runs it; C→S when a process asks the cluster |
| `llm.chunk` / `llm.done` | both | `{id, data}` / `{id, usage {prompt_tokens, completion_tokens, ms}, error?}` |
| `cmd` | S→C | `{id, name: drain\|undrain\|reload_stages\|log_level\|disconnect, args}` |
| `cmd.result` | C→S | `{id, ok, detail}` |
| `ping` / `pong` | both, 15 s | liveness; 45 s silence closes |

Back-pressure: the agent keeps a bounded buffer (logs 10 000 records, metrics the latest
only); the hub's per-browser queues drop the oldest frames for slow viewers and say so.

## 4. Files by node IP, on the shared storage — collected by all, readable by all

Every machine **collects its own** logs, metrics and LLM usage and saves them **on the
share with the videos**, under a `maktaba` folder; every machine can read everyone's files
through its own path to the share (owner, 2026-10-19). The live stream to the home (§5) is
the real-time view; the files are the record, and they keep being written when the home is
down.

**Where.**
- **Share root** (database form, like `libraries.root_path`): `MAKTABA_SHARE_ROOT` when
  set; otherwise the deepest directory common to every library root; when the libraries
  share nothing but the filesystem root, the root of the library with the most videos.
- **Cluster directory**: `<share root>/maktaba/cluster`, translated to the local path by
  the process's path map (phase 1 §6: the home's API takes `MAKTABA_PATH_MAP` in the same
  format as the workers' `--path-map`; a node uses its stored map). `MAKTABA_CLUSTER_DIR`
  overrides with a local path. When the share is not reachable a process writes to
  `~/.maktaba/cluster` and reports `files: {dir, on_share, reason}` in its metrics.
- **Secrets stay local**: `~/.maktaba/cluster/home-agent.key` on the home, never on the share.

**The machine's IP** (its folder): the local address of a socket aimed at the home's API
host (no packet is sent); when that is a loopback address (a home process), the machine's
first non-loopback private IPv4. IPv6 written with `:` → `-`.

**Layout — one writer per file.** Several processes run on one machine and an append from
two processes to one file is not safe on NFS/SMB, so each process writes its own files:

```
<share>/maktaba/cluster/192.168.50.31/
    processes/<worker_id>.json             # identity: node id, name, host, os, pid, role, started_at
    logs/2026-10-19/<worker_id>.jsonl      # {ts, level, event, logger, fields}
    metrics/2026-10-19/<worker_id>.jsonl   # one line per metrics frame
    llm/2026-10-19/<worker_id>.jsonl       # requests this process ran: {ts, id, model, op, from, prompt_tokens, completion_tokens, ms, error}
    jobs/2026-10-19/<worker_id>.jsonl      # every job event this process had (claimed … done / failed), with durations and outcome
    events/2026-10-19/hub.jsonl            # the home's hub only: connect, disconnect, auth refused, commands and results
    queues/2026-10-19/hub.jsonl            # the home's hub only: queue snapshot every 10 s (per stage: waiting, ready, running, paused, failed, done/min, oldest wait; pools: busy/size; flows)
    library/2026-10-19/hub.jsonl           # the home's hub only: library snapshot every 5 min (per library: videos, hours, bytes, transcribed videos/hours, series, seasons, tagged, indexed; totals)
```

Queue and library numbers are the cluster's, not one machine's: the home's hub takes them
from the queue it already watches (the same snapshot that feeds the Processing page) and
writes them under the home's IP folder; job events are each machine's own.

Rotated daily (a new day directory), kept `MAKTABA_CLUSTER_RETENTION_DAYS` (default 14;
each process removes its own expired files, the hub any left by processes gone for good),
each file capped at `MAKTABA_CLUSTER_FILE_MB` (default 256; past it records are counted, not
written). Writes are buffered and flushed at least every 2 s, never blocking the worker.
LLM files record usage, never prompt or output text.

**Readers.** The home's history endpoints and `maktaba-pipeline cluster logs|metrics|llm|events
[--ip IP] [--date D] [--level L] [--follow]` on any machine read these files directly,
merging a day's per-process files by timestamp. Nothing goes to the database.

## 5. The hub and the browsers: one WebSocket

Owner (2026-10-19): the Processing page talks to a central hub on the home over a
WebSocket, and every job on every machine talks to that hub. The hub is the real-time
centre of the cluster: processes push to it (§3: `metrics`, `logs`, `job`, `llm`), it holds
the live state, and browsers subscribe to it.

**Endpoint**: `GET /api/cluster/ws` (WebSocket; signed-in users; the admin-only topics and
commands need an admin). JSON messages both ways.

- Browser → hub: `{"op":"subscribe","topics":[…]}` / `unsubscribe`, where a topic is
  `queues`, `jobs`, `workers`, `metrics`, `library`, `llm`, `processes`, or
  `logs:<ip|node_id|session>:<level>`; `{"op":"cmd", …}` (admin) for the per-process
  commands of §3; `{"op":"ping"}`.
- Hub → browser: `snapshot` per topic on subscribe, then `queues` (the queue snapshot the
  Processing page already renders, pushed as it changes, at most every second), `job`
  (every job event from every machine: claimed, progress, done, failed, skipped, paused,
  deferred — so the Processing page shows each job live wherever it runs), `workers`,
  `metrics`, `library`, `llm`, `process` (connect / disconnect / auth refused), `log`,
  `cmd.result`, `dropped` (frames dropped for a slow browser), `pong`.

**Processing page** moves to this socket: the queue lanes, the running jobs with their live
progress, the activity feed and the workers strip all come from the hub; it reconnects with
backoff and re-subscribes; the existing SSE stream stays as a fallback for networks that
block WebSockets. The Machines page uses the same socket for its live views.

REST (history, from the files on the share): `GET /api/cluster/files`,
`/api/cluster/files/{ip}/logs|metrics|jobs|llm|events|queues|library?date=…`;
`POST /api/cluster/processes/{session}/cmd`.

**Stage selection** stays as in phase 1 (each process applies its capabilities, placement,
the admin's switches, drain and storage), and the hub pushes `reload_stages` the moment an
admin changes a switch or drain, so changes apply at once instead of within 30 s.

## 6. LLMs across the cluster

**Runtimes.** An agent detects local runtimes: Ollama (`MAKTABA_OLLAMA_URL`, default
`http://127.0.0.1:11434`: `/api/tags`, `/api/ps`) and OpenAI-compatible servers listed in
`MAKTABA_LLM_ENDPOINTS` (LM Studio, vLLM, llama.cpp server: `/v1/models`). It re-checks
every 30 s and sends `llm.models` when something changes. Runtimes stay bound to
localhost: the agent is their only way in.

**Router (home).** A request names a model; the router picks a machine that has it:
loaded first, then fewest in flight relative to `max_parallel` (default 1 per runtime,
`MAKTABA_LLM_MAX_PARALLEL`), then the requester's own machine; it queues when all are busy
(fair, FIFO per model) and fails after `MAKTABA_LLM_QUEUE_TIMEOUT_S` (default 300) with
`no_capacity`, or at once with `no_model` when nobody has it. A machine that disconnects
mid-request fails that request with `machine_gone`.

**Doors in.**
- *Pipeline code*: `maktaba_pipeline.llm.cluster_llm()` → `chat()` / `generate()` /
  `embed()` with streaming; uses the local runtime directly when it has the model, else
  sends `llm.request` over its agent channel.
- *Anything OpenAI-compatible*: the home serves `GET /api/cluster/llm/v1/models` and
  `POST /api/cluster/llm/v1/chat/completions` (+ `/v1/embeddings`), streaming as SSE per
  the OpenAI wire format, authenticated like the rest of the API (admin token or a
  personal access token). Point a tool at `http://<home>:8080/api/cluster/llm/v1`.

## 7. Settings without the database

Home: environment / config file (`MAKTABA_CLUSTER_DIR`, `…_RETENTION_DAYS`, `…_FILE_MB`,
`MAKTABA_CLUSTER_LOG_LEVEL` default `info`). Per process: the hub sends `settings` in
`welcome` and `cmd log_level` changes it live; the agent applies it to its log shipper.

## 8. Failure behaviour

- Home unreachable: workers keep working (the queue is in the database); the agent
  buffers logs (bounded), drops old metrics, retries with backoff to 30 s.
- Hub restart: agents reconnect and resend `llm.models`; the snapshot rebuilds within one
  metrics period.
- Slow browser: its stream drops frames, never the hub.
- Disk full on the home: file writes fail closed (counted, logged once a minute); the live
  view keeps working.

## 9. Security

Authentication by possession of the pairing key (nodes) or the home agent key (home
processes); per-connection keys (forward secrecy per session: a fresh server X25519 key);
every frame authenticated and encrypted with a strict counter; revocation closes sessions
immediately. The OpenAI-compatible door requires API authentication. LLM prompts and
outputs are never written to disk.
