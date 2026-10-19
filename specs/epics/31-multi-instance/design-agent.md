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
to files per node IP, routes LLM requests to the machines that hold the models, and sends
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
| `dropped` | C→S | counts of log records dropped while disconnected (bounded buffer) |
| `llm.models` | C→S, on change | runtimes `[{provider, base_url, models [{name, size, family, quant, loaded}], max_parallel}]` |
| `llm.request` | both | `{id, model, op: chat\|generate\|embed, body, stream}`: S→C to the machine that runs it; C→S when a process asks the cluster |
| `llm.chunk` / `llm.done` | both | `{id, data}` / `{id, usage {prompt_tokens, completion_tokens, ms}, error?}` |
| `cmd` | S→C | `{id, name: drain\|undrain\|reload_stages\|log_level\|disconnect, args}` |
| `cmd.result` | C→S | `{id, ok, detail}` |
| `ping` / `pong` | both, 15 s | liveness; 45 s silence closes |

Back-pressure: the agent keeps a bounded buffer (logs 10 000 records, metrics the latest
only); the hub's per-browser queues drop the oldest frames for slow viewers and say so.

## 4. Files by node IP, on the shared storage

The files live **on the share with the videos**, under a `maktaba` folder, so every
machine can read them through its own path to the share (owner, 2026-10-19).

- **Share root** (database form, like `libraries.root_path`): `MAKTABA_SHARE_ROOT` when
  set; otherwise the deepest directory common to every library root; when the libraries
  share nothing but the filesystem root, the root of the library with the most videos.
- **Cluster directory**: `<share root>/maktaba/cluster`, translated to the local path by
  the process's path map (phase 1 §6: the home's API takes `MAKTABA_PATH_MAP` in the same
  format as the workers' `--path-map`; a node uses its stored map). `MAKTABA_CLUSTER_DIR`
  overrides with a local path. If the share is not reachable the hub writes to
  `~/.maktaba/cluster` and says so in its events and on the Machines page.
- **Secrets stay local**: `home-agent.key` is in `~/.maktaba/` on the home, never on the share.
- **One writer per file**: the hub writes every file; any machine reads them —
  `maktaba-pipeline cluster logs|metrics|events [--ip IP] [--date D] [--follow]` on a node
  reads the share directly with its own map.

The hub writes what each process sends, one directory per **node IP** (the remote address of the
connection; the home's own processes use the home's LAN address), JSON Lines, rotated
daily, kept `MAKTABA_CLUSTER_RETENTION_DAYS` (default 14), each file capped at
`MAKTABA_CLUSTER_FILE_MB` (default 256; past it records are counted, not written):

```
~/.maktaba/cluster/192.168.50.31/
    node.json                     # node id, name, host, os — rewritten on connect
    logs/2026-10-19.jsonl         # {ts, worker_id, pid, level, event, logger, fields}
    metrics/2026-10-19.jsonl      # one line per metrics frame
    llm/2026-10-19.jsonl          # {ts, id, model, op, from, to, prompt_tokens, completion_tokens, ms, error}
    events/2026-10-19.jsonl       # connect, disconnect, auth failure, commands and results
```

IPv6 addresses are written with `:` replaced by `-`. LLM files record usage, never prompt
or output text. History endpoints read these files; nothing goes to the database.

## 5. Real-time view (browser)

`GET /api/cluster/stream` (admin, Server-Sent Events, the transport the web already uses
for live pages): `snapshot` on connect (every connected process: identity, latest
metrics, models), then `metrics`, `log` (when the viewer subscribed `?logs=<node>&level=`),
`llm` (request start/finish), `node` (connect / disconnect / auth refused). REST:
`GET /api/cluster/nodes/{ip}/logs?date=&level=&q=&limit=` and
`/metrics?date=&from=&to=` read the files; `POST /api/cluster/processes/{session}/cmd`.

The Machines page gets, per machine and per process: live CPU / memory / GPU / disk /
network with sparklines, running jobs with progress, transcription speed, LLM models and
load; a log viewer (live tail with level filter and search, earlier days from the files);
the LLM panel (every model in the cluster, where it runs, requests in flight, tokens/s).

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
