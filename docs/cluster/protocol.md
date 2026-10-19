# The agent protocol (version 1)

This page is for developers implementing or debugging either end of the channel between a
Maktaba worker process (the **agent**) and the home's **hub**. The design and its reasons
are in [design-agent.md](../../specs/epics/31-multi-instance/design-agent.md) (§4–§5); this
page is the reference for the bytes.

Interop vectors: [`shared/testdata/cluster/agent_vectors.json`](../../shared/testdata/cluster/agent_vectors.json).
Both implementations (Go hub, Python agent) must reproduce them exactly.

## 1. Conventions

- **JSON**: UTF-8, one object per frame, with a string member `t` naming the frame type.
  Receivers must not depend on member order. (The vectors use compact JSON with sorted
  keys so their ciphertexts are reproducible.)
- **Binary values in JSON**: standard base64 (RFC 4648 §4) with padding.
- **Times**: RFC 3339, UTC, e.g. `2026-10-19T08:00:02Z`. Durations in milliseconds
  (`*_ms`) or seconds (`*_s`). Sizes in bytes; rates per second.
- `‖` is byte concatenation. Strings in byte layouts are ASCII.
- MUST, SHOULD: as in RFC 2119.

## 2. Connection

```
GET /api/cluster/agent HTTP/1.1          (on the API's public port, 8080 by default)
Upgrade: websocket
```

- The route needs no HTTP authentication; the handshake below authenticates.
- A machine (`kind: "node"`) connects to the home URL in its node config; a home process
  (`kind: "home"`) to `MAKTABA_API_URL` (default `http://127.0.0.1:8080`).
- **Before `welcome`**: WebSocket **text** messages, one JSON object each.
- **From `welcome` on**: WebSocket **binary** messages, one sealed frame each (§5). A text
  message after `welcome` is a protocol error (close 4000).
- The handshake MUST complete within 10 s of the upgrade (close 4008).

## 3. Handshake — machine (`kind: "node"`)

```
agent                                                         hub
  │ text hello {v, kind:"node", node_id, …, nonce: Nc}           │
  │ ────────────────────────────────────────────────────────────►│ node approved? else close 4003
  │                                                              │ (Es, Ps) = fresh X25519 key pair
  │ text challenge {server_pub: Ps, nonce: Ns}                   │
  │ ◄────────────────────────────────────────────────────────────│
  │ shared = X25519(node_priv, Ps)                               │ shared = X25519(Es, node_pub)
  │ k_c2s, k_s2c = HKDF(...)                                     │ k_c2s, k_s2c = HKDF(...)
  │ text auth {proof}                                            │
  │ ────────────────────────────────────────────────────────────►│ proof ok? else close 4004
  │ binary sealed(k_s2c, #0, welcome)                            │
  │ ◄────────────────────────────────────────────────────────────│
  │ binary sealed(k_c2s, #0, metrics) …                          │
```

### 3.1 `hello` (agent → hub, text)

```json
{"t": "hello", "v": 1, "kind": "node",
 "node_id": "6f1d2c3b-…", "worker_id": "node-6f1d2c3b-4242", "pid": 4242,
 "host": "studio-linux", "ip": "192.168.50.31", "os": "linux", "version": "0.4.0",
 "nonce": "yMnKy8zNzs/Q0dLT1NXW1w=="}
```

| Member | Required | Meaning |
|---|---|---|
| `v` | yes | protocol version, `1` |
| `kind` | yes | `"node"` |
| `node_id` | yes | the machine's id (canonical lowercase UUID), as approved in phase 1 |
| `worker_id` | yes | this process's worker id (unique per process start) |
| `pid`, `host` | yes | the process id and the machine's stable host name |
| `ip`, `os`, `version` | no | the process's IP folder name, `darwin` \| `linux` \| `windows`, the pipeline version |
| `nonce` | yes | `Nc`: 16 random bytes from a CSPRNG, base64 |

The hub looks the node up. If it is not `approved` (unknown, pending, rejected, revoked) the
hub closes with **4003**, without saying which.

### 3.2 `challenge` (hub → agent, text)

```json
{"t": "challenge", "server_pub": "ZLEBsdC+WocEvQePmJUAH8A+jp+VIvGI3RKNmEbUhGY=", "nonce": "2Nna29zd3t/g4eLj5OXm5w=="}
```

`server_pub` (`Ps`) is the public half of an X25519 key pair the hub makes for this
connection only (32 bytes); `nonce` (`Ns`) is 16 random bytes.

### 3.3 Key derivation

```
shared = X25519(node_priv, Ps)                                  32 bytes   (RFC 7748)
         an all-zero result MUST be refused (close 4004 on the hub, abort on the agent)

salt   = Nc ‖ Ns                                                32 bytes
         offset 0–15: Nc   offset 16–31: Ns

okm    = HKDF-SHA256(IKM = shared, salt = salt,                 64 bytes   (RFC 5869)
                     info = "maktaba-agent-v1", L = 64)
         info bytes (16): 6d 61 6b 74 61 62 61 2d 61 67 65 6e 74 2d 76 31

k_c2s  = okm[0:32]      key for frames agent → hub
k_s2c  = okm[32:64]     key for frames hub → agent
```

### 3.4 `auth` (agent → hub, text)

```
proof  = HMAC-SHA256(key = k_c2s, message = "maktaba-agent-auth" ‖ Ns)      32 bytes
         message (34 bytes): 6d 61 6b 74 61 62 61 2d 61 67 65 6e 74 2d 61 75 74 68 ‖ Ns
```

```json
{"t": "auth", "proof": "u0F4AnQXUQIosYh/+2jlzAPGZQA54q/sw/Di9s8swPs="}
```

The hub computes the same value with `X25519(Es, node_pub)` and compares in constant time.
A mismatch closes with **4004**. On success the hub sends `welcome` as the first sealed frame
(hub → agent counter 0).

## 4. Handshake — home process (`kind: "home"`)

A process on the home authenticates with the **home agent key** instead of a pairing key:
32 random bytes, stored raw in `~/.maktaba/cluster/home-agent.key` (mode 0600) of the user
running the API, or at `MAKTABA_HOME_AGENT_KEY`. It is on the local disk, never on the
share.

Differences from §3:

| Step | Machine | Home process |
|---|---|---|
| `hello` | `kind: "node"`, `node_id` | `kind: "home"`, no `node_id` |
| `challenge` | `server_pub` and `nonce` | `nonce` only |
| `shared` | `X25519(node_priv, Ps)` | the 32-byte home agent key |

Salt, HKDF, keys, proof and sealing are identical.

## 5. Sealed frames

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
  frame (hub → agent: `welcome`; agent → hub: its first frame after `welcome`) and increases
  by 1 per frame. Unknown frame types still use a counter value.
- The receiver opens each frame only with the counter it expects next. A failed tag, a gap
  or a repeat MUST close the connection (**4011**). There is no reordering and no window.
- A connection never reuses keys: every connection derives new ones (fresh nonces, and for
  machines a fresh hub key pair).

## 6. Worked example (from the vectors)

| Value | Hex |
|---|---|
| `Nc` (client_nonce) | `c8c9cacbcccdcecfd0d1d2d3d4d5d6d7` |
| `Ns` (server_nonce) | `d8d9dadbdcdddedfe0e1e2e3e4e5e6e7` |
| `shared` | `26c2c17fdb82161cb21ad16e721315355b64d1763119b10bfc962530dc7cc163` |
| `k_c2s` | `3ae96d8a9b0c08884b9a9e0330f05451e8b060691e1009f1789d465d9f3a4074` |
| `k_s2c` | `7c1fd80038825cadf53283f7f50a650eeae2b3a6171358c61f62341e35575af0` |
| `proof` | `bb4178027417510228b1887ffb68e5cc03c6650039e2afecc3f0e2f6cf2cc0fb` |

The first hub → agent frame (counter 0, nonce `000000000000000000000000`) seals the 80-byte
plaintext

```json
{"session":"s1","settings":{"log_level":"info","metrics_every":2},"t":"welcome"}
```

into 96 bytes, beginning `2e014ed41efb4331…` and ending with the tag
`07ea815f95ce92591a62afa7c08d749e`. The vectors also hold two agent → hub frames
(counters 0 and 1) and the home-key variant (`home`: `home_key`, `k_c2s`, `k_s2c`, `proof`).

## 7. Frame catalogue

All frames below are sealed. Members marked `?` are optional.

### 7.1 Hub → agent

**`welcome`** — once, counter 0.

```json
{"t": "welcome", "session": "s-7Qm2…",
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
| `disconnect` | `{"reason": "..."}` | close; if `reason` is `"revoked"`, stop the worker gracefully and not reconnect |

Every `cmd` is answered with `cmd.result`. Unknown names are answered `ok: false`.

### 7.2 Agent → hub

**`metrics`** — every `metrics_every` seconds.

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
 "files":    {"ip": "192.168.50.31", "dir": "…", "on_share": true, "reason": null, "dropped": 0}}
```

`rx_bps`/`tx_bps` are bytes per second. Sections the machine cannot measure are omitted or
empty.

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

### 7.3 Both directions

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

## 8. Liveness and reconnecting

- 45 s without any frame from the other side: close **4008**.
- Reconnect delay: 1 s, doubling to at most 30 s, ±20 % jitter; reset to 1 s once a session
  has lasted 60 s. "Longest delay" below means 30 s.
- While disconnected the agent keeps at most 10 000 log records (oldest dropped, counted for
  `dropped`) and only its latest metrics; job events of that time are not resent.
- The worker MUST NOT wait for the channel: pulling and running jobs never depends on it.

## 9. Close codes

| Code | Name | Sent by | Cause | Agent reaction |
|---|---|---|---|---|
| 1000 | `normal` | either | clean stop | reconnect if still running |
| 1001 | `going_away` | hub | API restarting | reconnect with backoff |
| 4000 | `protocol_error` | either | bad JSON, unexpected frame, text after `welcome` | reconnect with backoff |
| 4001 | `unsupported_version` | hub | `hello.v` not served; reason lists served versions, e.g. `unsupported_version 1` | retry at the longest delay (with a common version if any); log once an hour |
| 4003 | `not_member` | hub | node unknown, pending, rejected or revoked | retry at the longest delay |
| 4004 | `auth_failed` | hub | wrong proof, or all-zero shared secret | retry at the longest delay |
| 4008 | `timeout` | either | handshake over 10 s, or 45 s of silence | reconnect |
| 4009 | `replaced` | hub | a newer session with the same `worker_id` | retry at the longest delay; log an error |
| 4010 | `revoked` | hub | the machine was revoked | **do not reconnect**; stop the worker gracefully |
| 4011 | `seal_error` | either | failed tag, counter gap or repeat | reconnect |
| 4012 | `too_large` | either | frame over 4 MiB | reconnect |
| 4013 | `rate_limited` | hub | over 30 handshakes a minute from one address | retry at the longest delay |
| 4014 | `disconnect` | hub | an admin's `disconnect` command | reconnect with backoff |

The close reason (at most 123 bytes of UTF-8) is the name, optionally followed by a space and
a short detail.

## 10. Versioning and compatibility

- `hello.v` is the version the agent speaks. Version 1 is defined by the labels
  `maktaba-agent-v1` and `maktaba-agent-auth`, the layouts above and the vectors.
- Any change to a label, a layout, a key size or an algorithm is a **new version** with new
  labels and new vectors. A hub MAY serve several versions; the agent sends its highest and,
  on 4001, retries with the highest version both serve.
- Within version 1 only additive changes are allowed: new optional members and new frame
  types. Receivers MUST ignore unknown members, unknown `settings` keys and unknown frame
  types (the frame still consumes a counter value).

## 11. Limits

| Limit | Value |
|---|---|
| Sealed frame size | 4 MiB (close 4012) |
| Records per `logs` frame | 1 000 |
| One log record | 64 KiB, then `fields` cut and `truncated: true` |
| Handshake time | 10 s from the upgrade |
| Handshakes per address | 30 per minute |
| Silence before close | 45 s |
| Agent log buffer while disconnected | 10 000 records |

## 12. Implementation checklist

- [ ] Nonces from a CSPRNG; a fresh hub key pair per machine connection.
- [ ] All-zero X25519 output refused.
- [ ] Proof compared in constant time.
- [ ] Strict per-direction counters starting at 0; close on gap, repeat or bad tag.
- [ ] No text frames after `welcome`; no frames other than `hello`/`challenge`/`auth` before it.
- [ ] Unknown members, settings and frame types ignored (counter still advances).
- [ ] Commands limited to the five names; nothing else is executed.
- [ ] Prompts and outputs of LLM requests never logged or written.
- [ ] The vectors reproduced byte for byte (node and home variants).

## 13. The browser socket

Browsers do not speak this protocol. They use `GET /api/cluster/ws`, a plain JSON
WebSocket authenticated like the rest of the API, with topics (`queues`, `jobs`, `workers`,
`library`, `metrics`, `llm`, `processes`, `logs:<who>:<level>`). See
[design-agent.md §6.4](../../specs/epics/31-multi-instance/design-agent.md#64-browsers-one-websocket).
