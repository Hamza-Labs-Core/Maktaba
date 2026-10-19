# The agent protocol (version 2)

This page is for developers implementing or debugging either end of the channel between a
Maktaba worker process (the **agent**) and the home's **hub**. The design and its reasons
are in [design-agent.md](../../specs/epics/31-multi-instance/design-agent.md) (§4–§5); this
page is the reference for the bytes.

Interop vectors: [`shared/testdata/cluster/agent_vectors.json`](../../shared/testdata/cluster/agent_vectors.json).
Both implementations (Go hub, Python agent) must reproduce them exactly.

Version 2 authenticates **both** ends and gives **forward secrecy**. Version 1 (an earlier
draft in which the hub was not proven) is not served.

## 1. Conventions

- **JSON**: UTF-8, one object per frame, with a string member `t` naming the frame type.
  Receivers must not depend on member order. (The vectors use compact JSON with sorted
  keys so their bytes are reproducible.)
- **Binary values in JSON**: standard base64 (RFC 4648 §4) with padding.
- **Times**: RFC 3339, UTC, e.g. `2026-10-19T08:00:02Z`. Durations in milliseconds
  (`*_ms`) or seconds (`*_s`). Sizes in bytes; rates per second.
- `‖` is byte concatenation. Strings in byte layouts are ASCII.
- MUST, SHOULD: as in RFC 2119.

## 2. Keys

| Key | Who holds the private half | Where | The other side knows |
|---|---|---|---|
| **Pairing key** (`node_static`, X25519) | the machine | its node config (phase 1: `private_key`), mode 0600 | the hub: `cluster_nodes.node_pubkey`, recorded at join |
| **Home cluster key** (`home_static`, X25519) | the home's API | `~/.maktaba/cluster/home-cluster.key` of the user running the API (or `MAKTABA_HOME_CLUSTER_KEY`), local disk, mode 0600, never on the share | every machine: `home.cluster_pub` in its node config |
| **Home agent key** (32 bytes, symmetric) | the home's API and the home's workers | `~/.maktaba/cluster/home-agent.key` (or `MAKTABA_HOME_AGENT_KEY`), local, mode 0600 | — (both hold it) |
| **Ephemeral keys** (`client_eph`, `server_eph`, X25519) | each side, one per connection | memory only, discarded after the handshake | the peer, from `hello.eph` / `challenge.server_eph` |

**How a machine gets `home.cluster_pub`.** At approval (phase 1 join), the home seals the
machine's credentials to it; the sealed payload's `home` object carries `cluster_pub`
(32 bytes, base64) next to `instance_id`, `name` and `api_url`. The machine stores it in its
node config with the rest. A machine that joined before version 2 has no `cluster_pub` and
cannot connect: it must **re-join** (revoke and remove it on the home, delete its node
config, run `join` again). Until then it keeps pulling jobs without the channel and logs
"re-join this machine".

## 3. Connection

```
GET /api/cluster/agent HTTP/1.1          (on the API's public port, 8080 by default)
Upgrade: websocket
```

- The route needs no HTTP authentication; the handshake below authenticates both sides.
- A machine (`kind: "node"`) connects to the home URL in its node config; a home process
  (`kind: "home"`) to `MAKTABA_API_URL` (default `http://127.0.0.1:8080`).
- **Before `welcome`**: WebSocket **text** messages, one JSON object each (`hello`,
  `challenge`, `auth`).
- **From `welcome` on**: WebSocket **binary** messages, one sealed frame each (§6). A text
  message after `welcome` is a protocol error (close 4000).
- The handshake MUST complete within 10 s of the upgrade (close 4008).

## 4. Handshake — machine (`kind: "node"`)

```
agent                                                             hub
  │ (client_eph fresh)                                              │
  │ text hello {v:2, kind:"node", node_id, …, eph, nonce: Nc}       │
  │ ───────────────────────────────────────────────────────────────►│ node approved? else close 4003
  │                                                                 │ (server_eph fresh)
  │ text challenge {server_eph, nonce: Ns}                          │
  │ ◄───────────────────────────────────────────────────────────────│
  │ dh_ee, dh_se, dh_es, th → k_c2s, k_s2c                          │ the same values
  │ text auth {proof: client proof}                                 │
  │ ───────────────────────────────────────────────────────────────►│ client proof ok? else close 4004
  │ binary sealed(k_s2c, #0, welcome {proof: server proof, …})      │
  │ ◄───────────────────────────────────────────────────────────────│
  │ opens and checks server proof, else close 4004                  │
  │ ("this is not the home I joined")                               │
  │ binary sealed(k_c2s, #0, metrics) …                             │
```

### 4.1 `hello` (agent → hub, text)

```json
{"eph":"CCZK5pC0Edjj7eQPhxklheoRSggnRKd2EFqsBeqxBS4=","host":"studio-linux","kind":"node","node_id":"6f1c2a3b-4d5e-4f60-8a71-92b3c4d5e6f7","nonce":"yMnKy8zNzs/Q0dLT1NXW1w==","pid":4242,"t":"hello","v":2,"worker_id":"node-6f1c2a3b-4242"}
```

| Member | Required | Meaning |
|---|---|---|
| `v` | yes | protocol version, `2` |
| `kind` | yes | `"node"` |
| `node_id` | yes | the machine's id (canonical lowercase UUID), as approved in phase 1 |
| `worker_id` | yes | this process's worker id (unique per process start) |
| `pid`, `host` | yes | the process id and the machine's stable host name |
| `eph` | yes | the public half of a fresh X25519 key pair for this connection (32 bytes, base64) |
| `nonce` | yes | `Nc`: 16 random bytes from a CSPRNG, base64 |
| `ip`, `os`, `version` | no | the process's IP folder name, `darwin` \| `linux` \| `windows`, the pipeline version |

The agent MUST keep the exact bytes of the message it sent (`hello_bytes`): the transcript
covers them as sent, not a re-serialisation.

The hub looks the node up. If it is not `approved` (unknown, pending, rejected, revoked) the
hub closes with **4003**, without saying which.

### 4.2 `challenge` (hub → agent, text)

```json
{"nonce":"2Nna29zd3t/g4eLj5OXm5w==","server_eph":"ZLEBsdC+WocEvQePmJUAH8A+jp+VIvGI3RKNmEbUhGY=","t":"challenge"}
```

`server_eph` is the public half of an X25519 key pair the hub makes for this connection only
(32 bytes); `nonce` (`Ns`) is 16 random bytes. The hub keeps the exact bytes it sent
(`challenge_bytes`); the agent keeps the exact bytes it received.

### 4.3 Key derivation

```
dh_ee = X25519(client_eph_priv,  server_eph_pub)      = X25519(server_eph_priv,  client_eph_pub)    32 bytes
dh_se = X25519(node_static_priv, server_eph_pub)      = X25519(server_eph_priv,  node_static_pub)   32 bytes
dh_es = X25519(client_eph_priv,  home_static_pub)     = X25519(home_static_priv, client_eph_pub)    32 bytes
        any all-zero result MUST be refused (close 4004)

th    = SHA-256(hello_bytes ‖ challenge_bytes)                                                     32 bytes
        the UTF-8 payloads of the two WebSocket text messages, exactly as sent

ikm   = dh_ee ‖ dh_se ‖ dh_es                                                                       96 bytes
salt  = Nc ‖ Ns                                                                                     32 bytes
        offset 0–15: Nc   offset 16–31: Ns

okm   = HKDF-SHA256(IKM = ikm, salt = salt, info = "maktaba-agent-v2", L = 64)          (RFC 5869)
        info bytes (16): 6d 61 6b 74 61 62 61 2d 61 67 65 6e 74 2d 76 32

k_c2s = okm[0:32]      key for frames agent → hub
k_s2c = okm[32:64]     key for frames hub → agent
```

What each result proves: `dh_ee` gives forward secrecy (both halves are thrown away);
`dh_se` can only be computed by the holder of the machine's pairing key; `dh_es` only by the
holder of the home's cluster key. A side that is not who it claims derives different keys,
and its proof fails.

### 4.4 `auth` (agent → hub, text) — the client proof

```
client_proof = HMAC-SHA256(key = k_c2s, message = "maktaba-agent-auth" ‖ th)             32 bytes
               message (50 bytes): 6d 61 6b 74 61 62 61 2d 61 67 65 6e 74 2d 61 75 74 68 ‖ th
```

```json
{"t": "auth", "proof": "sApXg8IEEJnKypGLCak5S2F12TquclN1GDD2g7VEHYE="}
```

The hub computes the same value and compares in constant time. A mismatch closes with
**4004**.

### 4.5 `welcome` (hub → agent, first sealed frame) — the server proof

```
server_proof = HMAC-SHA256(key = k_s2c, message = "maktaba-agent-server" ‖ th)           32 bytes
               message (52 bytes): 6d 61 6b 74 61 62 61 2d 61 67 65 6e 74 2d 73 65 72 76 65 72 ‖ th
```

The hub sends `welcome` sealed with `k_s2c`, counter 0, carrying `proof` = base64 of
`server_proof` (§8.1). The agent MUST open it and compare the proof in constant time before
it sends any sealed frame. A `welcome` that does not open, or carries a wrong proof, means
the peer does not hold the home's cluster key: the agent closes with **4004**, logs
"this is not the home I joined", and retries at the longest delay.

## 5. Handshake — home process (`kind: "home"`)

A process on the home authenticates with the **home agent key** (§2) instead of a pairing key
and the cluster key.

| Step | Machine | Home process |
|---|---|---|
| `hello` | `kind: "node"`, `node_id` | `kind: "home"`, no `node_id` |
| `challenge` | `server_eph`, `nonce` | the same |
| `ikm` | `dh_ee ‖ dh_se ‖ dh_es` (96 bytes) | `home_key ‖ dh_ee` (64 bytes) |

The transcript hash, salt, HKDF (`info = "maktaba-agent-v2"`), both proofs and sealing are
identical. Holding `home_key` proves each side to the other; `dh_ee` gives forward secrecy.

## 6. Sealed frames

```
nonce   = 00 00 00 00 ‖ counter                              12 bytes
          counter: unsigned 64-bit, big-endian

sealed  = ChaCha20-Poly1305-Seal(key = k_dir, nonce,          (RFC 8439)
                                 plaintext = UTF-8 JSON, aad = empty)
        = ciphertext ‖ tag                                    len(plaintext) + 16 bytes

WebSocket binary message = sealed                             (the nonce is not sent)
```

- `k_dir` is `k_c2s` for agent → hub and `k_s2c` for hub → agent.
- Each direction has its own counter. It starts at **0** with that direction's first sealed
  frame (hub → agent: `welcome`; agent → hub: its first frame after checking `welcome`) and
  increases by 1 per frame. Unknown frame types still use a counter value.
- The receiver opens each frame only with the counter it expects next. A failed tag, a gap
  or a repeat MUST close the connection (**4011**) — except the `welcome`, whose failure is
  **4004** (§4.5). There is no reordering and no window.
- A connection never reuses keys: both ephemeral keys and both nonces are new every time.

## 7. Worked example (from the vectors)

The private keys are in the vectors (`node_static_private`, `client_eph_private`,
`server_eph_private`, `home_static_private`). The node variant:

| Value | Hex |
|---|---|
| `Nc` (client_nonce) | `c8c9cacbcccdcecfd0d1d2d3d4d5d6d7` |
| `Ns` (server_nonce) | `d8d9dadbdcdddedfe0e1e2e3e4e5e6e7` |
| `hello_bytes` | the vectors' `hello` string, 236 bytes |
| `challenge_bytes` | the vectors' `challenge` string, 112 bytes |
| `th` | `d67d5d450c843fcdf15fd86f1f4425b74ae5c85a7ed2e2b87506c60107d28282` |
| `dh_ee` | `e43977fad22cd72ea578c2c65eba17e510f68d4952053e9f9445dbc983f92249` |
| `dh_se` | `26c2c17fdb82161cb21ad16e721315355b64d1763119b10bfc962530dc7cc163` |
| `dh_es` | `32cc35499add56279cb075bcc8a88db28f429555cfe7c1d213715260022f8a24` |
| `k_c2s` | `41e217619a23e80700abb10316bf2fd908e9b38bceb2ddfe6bb9835e402fcc06` |
| `k_s2c` | `a8e395b35abf5c4fb4a5632167f422332512cb0992b84d244781b09c214f12d6` |
| `client_proof` | `b00a5783c2041099caca918b09a9394b6175d93aae7253751830f683b5441d81` |
| `server_proof` | `0701bb6691f685f631a583a4e894f151dfcb3237c1f2dfde8548a1d5eb800ebb` |

The `hello` and `challenge` of §4.1 and §4.2 are those exact strings. The first hub → agent
frame (counter 0, nonce `000000000000000000000000`) seals the 135-byte plaintext

```json
{"proof":"BwG7ZpH2hfYxpYOk6JTxUd/LMjfB8t/ehUih1euADrs=","session":"s1","settings":{"log_level":"info","metrics_every":2},"t":"welcome"}
```

into 151 bytes, beginning `93b4f5e67e8fcf85…` and ending with the tag
`63c337856585c10781296265c55a61ba`. The vectors also hold two agent → hub frames (counters 0
and 1) and the home variant (`home`: `home_key`, its own `hello` and `challenge`,
`transcript_hash`, `k_c2s`, `k_s2c`, `client_proof`, `server_proof`; the same ephemeral keys
and nonces).

## 8. Frame catalogue

All frames below are sealed. Members marked `?` are optional.

### 8.1 Hub → agent

**`welcome`** — once, counter 0.

```json
{"t": "welcome", "proof": "<server proof, base64>", "session": "s-7Qm2…",
 "settings": {"log_level": "info", "metrics_every": 2, "share_root": "/Volumes/Originals",
              "retention_days": 14, "file_mb": 256}}
```

`session` is opaque. Unknown `settings` keys MUST be ignored. A local environment setting
on the machine overrides `retention_days` and `file_mb`.

**`cmd`** — `{"t": "cmd", "id": "c-12", "name": "...", "args": {}}`

| `name` | `args` | The agent MUST |
|---|---|---|
| `drain` | — | stop pulling new jobs (this process, until it restarts) |
| `undrain` | — | resume pulling |
| `reload_stages` | — | re-evaluate now which queues it pulls from |
| `log_level` | `{"level": "debug"\|"info"\|"warning"\|"error"}` | ship and write records at or above it |
| `disconnect` | `{"reason": "..."}` | close; if `reason` is `"revoked"`, stop the worker gracefully and not reconnect (exit code 77) — this sealed command, not the `4010` close after it, is the authority |

Every `cmd` is answered with `cmd.result`. Unknown names are answered `ok: false`.

### 8.2 Agent → hub

**`metrics`** — every `metrics_every` seconds, and a last one when the worker stops
gracefully.

```json
{"t": "metrics", "ts": "…",
 "system":   {"cpu_pct": 0, "load1": 0, "mem_total": 0, "mem_available": 0, "swap_used": 0,
              "disks": [{"path": "/", "total": 0, "free": 0}], "net": {"rx_bps": 0, "tx_bps": 0}},
 "gpu":      {"nvidia": [{"name": "", "util_pct": 0, "mem_used": 0, "mem_total": 0, "temp_c": 0}],
              "apple": {"active_gb": 0, "cache_gb": 0, "peak_gb": 0}},
 "process":  {"rss": 0, "cpu_pct": 0, "threads": 0, "uptime_s": 0},
 "pipeline": {"role": "node", "stages": ["probe"], "stages_off": {"index": "home_only"},
              "jobs": [{"job_id": 812, "stage": "extract", "subject": "video:…", "since": "…",
                        "progress": {"step": "", "done": 0, "total": 0}}],
              "claims_per_min": 0, "stt": {"audio_sec_per_s": 0, "files_in_flight": 0}},
 "llm":      {"in_flight": 0, "tokens_per_s": 0},
 "files":    {"ip": "192.168.50.31", "dir": "…", "on_share": true, "reason": null, "dropped": 0},
 "health":   {"nas.open_ms_p50": 0, "nas.open_ms_p95": 0, "nas.timeouts": 0,
              "cpu.pct": 0, "mem.free_pct": 0, "mem.available_gb": 0,
              "stt.audio_sec:stt-1": 0}}
```

`rx_bps`/`tx_bps` are bytes per second. Sections the machine cannot measure are omitted or
empty. `health` is a flat map of the process's health numbers under the metric names of the
`health_samples` table (NAS probe, CPU, memory, network probes about once a minute, and
metrics stages register such as `stt.audio_sec:<slot>`); counters are cumulative since the
process started, the rest are gauges; `nas.reads_busy` is not sent (the hub counts it). The
hub writes these to `health_samples` at most every 10 s per process; workers never write that
table.

**`logs`** — batched, sent within 250 ms of the first record in the batch; at most 1 000
records per frame.

```json
{"t": "logs", "records": [{"ts": "…", "level": "warning", "event": "nas_slow",
                           "logger": "maktaba_pipeline.engine", "fields": {}}]}
```

A record over 64 KiB has its `fields` cut and `"truncated": true` added.

**`job`** — one per job event; `progress` at most once a second per job.

```json
{"t": "job", "event": "done", "job_id": 812, "stage": "transcribe",
 "subject_kind": "video", "subject_id": "…", "title": "…", "attempt": 1,
 "started_at": "…", "ts": "…", "duration_ms": 412000,
 "progress": null, "outcome": null, "output": {"audio_sec": 3600, "segments": 812}}
```

`event` ∈ `claimed`, `progress`, `done`, `failed`, `skipped`, `paused`, `deferred`.
`outcome` = `{"kind": "...", "retryable": true, "error": "..."}` for `failed`, `skipped`,
`deferred`.

**`dropped`** — after a reconnect, when records were lost while disconnected:
`{"t": "dropped", "logs": 1234, "since": "…", "until": "…"}`.

**`cmd.result`** — `{"t": "cmd.result", "id": "c-12", "ok": true, "detail": ""}`.

**`llm.models`** — after `welcome`, then whenever it changes.

```json
{"t": "llm.models", "runtimes": [
  {"provider": "ollama", "base_url": "http://127.0.0.1:11434", "max_parallel": 1,
   "models": [{"name": "qwen2.5:7b", "size": 4683087332, "family": "qwen2", "quant": "Q4_K_M", "loaded": true}]}]}
```

### 8.3 Both directions

**`ping` / `pong`** — `{"t": "ping", "ts": "…"}`; the answer `{"t": "pong", "ts": "<the ping's ts>"}`.
Each side sends `ping` every 15 s.

**LLM frames**

| Frame | Members |
|---|---|
| `llm.request` | `id`, `model`, `op` (`chat` \| `generate` \| `embed`), `body`, `stream` |
| `llm.chunk` | `id`, `data` |
| `llm.done` | `id`, `usage` {`prompt_tokens`, `completion_tokens`, `ms`}, `error`? {`code`, `detail`} |
| `llm.cancel` | `id` |

- Agent → hub `llm.request`: this process asks the cluster to run a request. Hub → agent
  `llm.request`: run this on your machine's runtime.
- `id` is chosen by the sender of the `llm.request` and is unique within the session; the
  hub uses its own ids toward the machine that runs the request and maps them back.
- `body` is the OpenAI request body: `chat` → `/v1/chat/completions`, `generate` →
  `/v1/completions`, `embed` → `/v1/embeddings`. The runner sends it to its runtime's
  OpenAI-compatible endpoint.
- With `stream: true`, each `llm.chunk.data` is one OpenAI stream chunk (the JSON of one SSE
  `data:` line, without `[DONE]`); otherwise a single `llm.chunk` carries the whole response.
- Every request ends with exactly one `llm.done`. Error codes: `no_model`, `no_capacity`,
  `machine_gone`, `runtime_error`, `cancelled`, `too_large`, `no_channel` (local only).
- `llm.cancel` from the requester (or the hub, when the requester left) asks the runner to
  stop; the runner still sends `llm.done` (`error.code: "cancelled"`).

**Service door frames** (the extension relay: a machine calls Plex without the token)

| Frame | Direction | Members |
|---|---|---|
| `svc.request` | agent → hub | `id`, `ext` (`plex`), `method`, `path` (path and query, never a URL), `accept`?, `content_type`?, `body`? (base64), `timeout_ms`? |
| `svc.chunk` | hub → agent | `id`, `data` (base64): the answer's body, in order |
| `svc.done` | hub → agent | `id`, `status`, `content_type`, `bytes`, `error`? {`code`, `detail`} |
| `svc.cancel` | agent → hub | `id` |

- The hub checks the session (an approved machine now, or a home process), the setting
  (rules section `relay`), the extension's allow-list (method, path pattern, query names and
  values) and the machine's limits; then it adds the secret (the Plex token) and calls the
  server configured on the home. Only `Content-Type` comes back, redirects are not followed,
  and every copy of the token is removed from the body.
- Every `svc.request` ends with exactly one `svc.done`. Refusal codes: `relay_off`,
  `unknown_extension`, `home_only`, `not_allowed`, `bad_request`, `not_configured`,
  `not_member`, `rate_limited`, `busy`, `too_large`; failures: `timeout`,
  `upstream_unreachable`, `cancelled`; `no_door` when the home runs without it.

## 9. Liveness and reconnecting

- 45 s without any frame from the other side: close **4008**.
- Reconnect delay: 1 s, doubling to at most 30 s, ±20 % jitter; reset to 1 s once a session
  has lasted 60 s. "Longest delay" below means 30 s.
- While disconnected the agent keeps at most 10 000 log records (oldest dropped, counted for
  `dropped`) and only its latest metrics; job events of that time are not resent.
- The worker MUST NOT wait for the channel: pulling and running jobs never depends on it.
- A machine without `home.cluster_pub` in its node config does not connect at all; it logs
  "re-join this machine" once an hour.

## 10. Close codes

| Code | Name | Sent by | Cause | Agent reaction |
|---|---|---|---|---|
| 1000 | `normal` | either | clean stop | reconnect if still running |
| 1001 | `going_away` | hub | API restarting | reconnect with backoff |
| 4000 | `protocol_error` | either | bad JSON, unexpected frame, text after `welcome` | reconnect with backoff |
| 4001 | `unsupported_version` | hub | `hello.v` not served (version 1 never is); reason lists served versions, e.g. `unsupported_version 2` | retry at the longest delay (with a common version if any); log once an hour |
| 4003 | `not_member` | hub | node unknown, pending, rejected or revoked | retry at the longest delay; check membership through the database at once; never stop for good on this close alone |
| 4004 | `auth_failed` | either | hub: wrong client proof; agent: `welcome` does not open or its proof is wrong; either: an all-zero DH result | retry at the longest delay; if the hub failed, log "this is not the home I joined" |
| 4008 | `timeout` | either | handshake over 10 s, or 45 s of silence | reconnect |
| 4009 | `replaced` | hub | a newer session with the same `worker_id` | retry at the longest delay; log an error |
| 4010 | `revoked` | hub | the machine was revoked | retry at the longest delay; check membership through the database at once (stop for good only on the sealed `disconnect` `revoked` or the database's answer) |
| 4011 | `seal_error` | either | failed tag, counter gap or repeat (after `welcome`) | reconnect |
| 4012 | `too_large` | either | frame over 4 MiB | reconnect |
| 4013 | `rate_limited` | hub | over 30 handshakes a minute from one address | retry at the longest delay |
| 4014 | `disconnect` | hub | an admin's `disconnect` command | reconnect with backoff |

The close reason (at most 123 bytes of UTF-8) is the name, optionally followed by a space and
a short detail.

Close codes are **hints**: a WebSocket close frame is not authenticated (`4003` comes before
any proof; after `welcome` a close is still outside the sealed stream). An agent MUST NOT
stop for good on a close code. The authorities are the sealed `cmd disconnect`
`{"reason": "revoked"}` (the hub sends it before closing `4010`) and the database (the
machine's cluster row, its database login). On `4003` / `4010` the reference agent waits
the longest delay and checks membership through the database at once.

## 11. Versioning and compatibility

- `hello.v` is the version the agent speaks. Version 2 is defined by the labels
  `maktaba-agent-v2` (HKDF info), `maktaba-agent-auth` (client proof) and
  `maktaba-agent-server` (server proof), the transcript rule, the layouts above and the
  vectors.
- Any change to a label, a layout, a key size or an algorithm is a **new version** with new
  labels and new vectors. A hub MAY serve several versions; the agent sends its highest and,
  on 4001, retries with the highest version both serve.
- Version 1 is not served.
- Within version 2 only additive changes are allowed: new optional members and new frame
  types. Receivers MUST ignore unknown members, unknown `settings` keys and unknown frame
  types (the frame still consumes a counter value). Because the transcript covers `hello`
  and `challenge` as sent, optional members need no canonical form.

## 12. Limits

| Limit | Value |
|---|---|
| Sealed frame size | 4 MiB (close 4012) |
| Records per `logs` frame | 1 000 |
| One log record | 64 KiB, then `fields` cut and `truncated: true` |
| Handshake time | 10 s from the upgrade |
| Handshakes per address | 30 per minute |
| Silence before close | 45 s |
| Agent log buffer while disconnected | 10 000 records |

## 13. Implementation checklist

- [ ] Nonces from a CSPRNG; fresh ephemeral key pairs on both sides for every connection,
      discarded after the handshake.
- [ ] Every all-zero X25519 result refused.
- [ ] `th` computed over the exact bytes sent and received, never a re-serialisation.
- [ ] Both proofs compared in constant time; the agent checks the hub's proof before sending
      any sealed frame.
- [ ] Strict per-direction counters starting at 0; close on gap, repeat or bad tag.
- [ ] No text frames after `welcome`; no frames other than `hello`/`challenge`/`auth` before it.
- [ ] Unknown members, settings and frame types ignored (counter still advances).
- [ ] No connection attempt without `home.cluster_pub`; "re-join this machine" logged.
- [ ] Commands limited to the five names; nothing else is executed.
- [ ] Prompts and outputs of LLM requests never logged or written.
- [ ] The vectors reproduced byte for byte (node and home variants).

## 14. The browser socket

Browsers do not speak this protocol. They use `GET /api/cluster/ws`, a plain JSON
WebSocket authenticated like the rest of the API, with topics (`queues`, `jobs`, `workers`,
`library`, `metrics`, `llm`, `processes`, `logs:<who>:<level>`). See
[design-agent.md §6.4](../../specs/epics/31-multi-instance/design-agent.md#64-browsers-one-websocket).
