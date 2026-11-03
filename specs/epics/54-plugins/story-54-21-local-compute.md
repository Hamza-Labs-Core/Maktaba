# Story 54.21 — Local compute: embeddings and the language model

> Epic 54 · Phase 3 · Depends on: 54.04, Epic 51 (`llm`, `search.semantic`) · Needed by: —

## Why

Many good plugin ideas need a model: group videos by topic, write a summary from a transcript,
suggest tags in Arabic. A WebAssembly plugin cannot bring torch (research §3.2), and shipping a
model per plugin would be huge and duplicate what the home already runs. Maktaba already runs
an embedding model (semantic search) and a local language model (ollama, the `llm` component).
Plugins may ask those, on the home, and never anything outside it.

## Description

### 1. Two capabilities

| Capability | WIT | Runs on | Needs component |
|---|---|---|---|
| `compute.embed` | `compute.embed(texts: list<string>) -> list<list<f32>>` (≤ 64 texts, ≤ 2,000 chars each) | the pipeline's embedder (the same model as `search.semantic`) | `search.semantic` |
| `compute.llm` | `compute.generate(prompt, {max-tokens ≤ 1,024, temperature, json-schema?}) -> string` | the home's ollama, the model the admin chose for plugins (default: the classify model) | `llm` |

- The plugin API forwards to the existing internal services on the home (the pipeline's gRPC
  semantic service; the `llm` component's local endpoint). The plugin host still has no network:
  calls go through the plugin API, which is Maktaba's code.
- Both are `wants` of the plugin component (Epic 51 §4.6): if `llm` is off, the plugin runs and
  `compute.generate` answers `error(unavailable)`; the plugin decides what to do. A plugin that
  lists `compute.llm` as **required** is *Waiting* `dependency_off:llm` instead.
- **Never a remote model.** If the home's `llm` settings point at a remote URL (the
  `model_rules` case in `graph.py`), `compute.llm` is refused for plugins (`compute_remote`):
  sending library text to another machine on the plugin's behalf would break the no-internet
  rule's intent. Only a model on the home or a cluster machine on the LAN the admin added counts
  as local.

### 2. Fair use

- Shared with core stages through the existing `lookup` pool (one question at a time) and a
  plugin share: plugin compute calls get at most 1 in 3 slots when core work is queued.
- Per plugin per day: 2,000 generate calls and 50,000 embed texts by default (admin-adjustable);
  over → `plugin_quota`.
- Prompts and outputs are not logged by Maktaba (only sizes and timings), so plugin compute
  does not copy library text into logs.

### 3. Results are plain data

Generated text written to fields or panels goes through the same rules as any plugin write
(54.04: field allow-list, plain text, attribution).

## Acceptance criteria

- **Given** a plugin with `compute.embed` and `search.semantic` running,
  **when** it embeds 10 texts,
  **then** it gets 10 vectors of the embedder's dimension, and the call used the pipeline's
  embedder.
- **Given** `llm` disabled and `compute.llm` optional,
  **when** the plugin calls generate,
  **then** it gets `error(unavailable)` and keeps running.
- **Given** the home's model URL points at another host on the internet,
  **when** a plugin calls generate,
  **then** `403 compute_remote`.
- **Given** 20 transcription-classify jobs queued,
  **when** a plugin floods generate,
  **then** core jobs keep at least 2 of every 3 `lookup` slots.
- **Given** a plugin over its daily quota,
  **when** it calls,
  **then** `403 plugin_quota`.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | integration (PG, go + py) | fake embedder | embed | vectors; limits enforced |
| T02 | integration | fake ollama, `llm` off / on | generate | unavailable / text |
| T03 | unit (go) | remote, LAN, localhost model URLs | generate | refused / allowed / allowed |
| T04 | integration (PG, py) | core and plugin lookup demand | run | ≥ 2/3 core |
| T05 | unit (go) | daily quota | exceed | 403 |
| T06 | unit (go) | logs | generate | no prompt text in logs |

## Edge cases

- **Model changed by the admin**: embeddings from the old model are the plugin's concern; the
  call returns the model id with the vectors so a plugin can notice.
- **JSON-schema-constrained output** the model fails to follow: `error(bad-output)` after one
  retry.

## Build plan

### Files to touch

| File | Change |
|---|---|
| `shared/plugins/wit/maktaba-plugin.wit` | `compute` interface (plugin API 1.x minor bump). |
| `api/internal/plugins/pluginapi/routes_compute.go` (new) | Forwarding, quotas, remote check. |
| `pipeline/src/maktaba_pipeline/grpc_server.py` | Embed call for the plugin API (machine auth). |
| `pipeline/src/maktaba_pipeline/engine/claim.py` | Plugin share of `lookup`. |
| `shared/plugins/capabilities.toml` | Two capabilities with words (en + ar). |
| `plugind/src/host/compute.rs` (new) | Forwarders. |

### Tests

- Integration T01, T02, T04; unit T03, T05, T06.

### Rollout

A plugin API minor version; plugins built for older minors are unaffected.

### Rollback

Capabilities refused with `error(unavailable)`; plugins degrade.

### Dependencies

54.04; Epic 51 components `llm` and `search.semantic`.
