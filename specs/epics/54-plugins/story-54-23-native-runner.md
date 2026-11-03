# Story 54.23 — The native plugin runner

> Epic 54 · Phase 4 · Depends on: 54.02, 54.22, 54.37, Epic 51 (51.01, 51.04) · Needed by: 54.25

## Why

Maktaba's own stages need torch, MLX, faster-whisper, ffmpeg, yt-dlp and the GPU, which cannot
run as WebAssembly today (README §2, H16). They still must run outside the queue worker, with
no database login and no secrets, behind the same dispatch messages as third-party plugins, so
the queue worker treats every plugin the same and imports no stage code.

## Description

### 1. The process

`maktaba-plugin-runner` is a Python entry point in the pipeline venv
(`python -m maktaba_plugin_runner --group <name>`), started by the supervisor as role
`plugin-runner`, one instance per group. Epic 51's catalog check
(`cmd/maktaba-server/internal/supervisor/catalog_roles_test.go`) needs every role/instance in
exactly one component, so the instances are split by what they serve:

| Instance | Catalog component | Why |
|---|---|---|
| `plugin-runner/ml-*` (one per today's stt slot) | `transcription` (its `processes` change from `pipeline/stt-*` in wave 3, 54.30) | it exists only for transcription |
| `plugin-runner/index` | `search.semantic` (beside `pipeline/index`, which keeps the query server) | it exists only for semantic search |
| `plugin-runner/light`, `plugin-runner/net`, `plugin-runner/service`, `plugin-runner/fp` | a new core component, `core.plugin_runners` (`kind = "process"`, `switchable = false`, `on_demand = true`, 54.37 §4) | they serve several components each (`light` carries `core.pipeline`'s core-media) |

On demand, a group's process runs only while at least one component whose first-party plugin
it serves is enabled and passes its gates; `net` does not run on a home with `youtube`,
`library.repair` and `enrich` all off. Switching a served component off drains its stages
(Epic 51 §4.5) and the runner stops when its last one is off. Its environment has **no**
`DATABASE_URL`, no home secrets path, and no cloud or extension secrets; the supervisor strips
them (a test asserts the child environment).

At start it:

1. reads the verified release checksums and checks every first-party package of its group is
   trusted (54.22 §2);
2. builds its OS sandbox from the union of its group's `[trusted]` tables (§3) and applies it to
   itself;
3. imports the plugin packages of its group, only now;
4. opens its dispatch socket `<data>/run/runner-<group>.sock` and sends `Hello` to the queue
   worker with what it serves on this machine: stage names, their `unavailable` reasons (the
   former `StageImpl.unavailable`), and its concurrency.

### 2. The protocol

The same messages as `maktaba-plugind` (54.02): `Run`, `Progress`, `Result`, `Cancel`, from
one schema (`shared/plugins/dispatch.proto`), plus three for native plugins only:

| Message | What |
|---|---|
| `Hello` | §1 step 4; sent again when an `unavailable` reason changes (the former `refresh`) |
| `Fingerprints {stage, kind, subjects[]}` → `{hashes[]}` | the stage's fingerprint export, batched (served by the `fp` group, §4) |
| `StillCurrent {job, earlier_input_hash, input_hash}` → `bool` or `deferred` | the former `StageImpl.still_current` |

A `Run` carries the job, the invocation token, the stage's export (`module.function`), the
settings sections the manifest lists, the checkpoint and the deadline. The handler runs as
`async def run(job: Job, api: PluginAPI) -> StageResult`; the SDK's `PluginAPI` is the
generated Python binding of `maktaba:plugin` plus `maktaba:trusted` (statements, settings,
media paths, service door), calling this machine's plugin API socket with the token attached.

### 3. The sandbox per OS

| OS | How | Network | Files and devices |
|---|---|---|---|
| macOS | Seatbelt profile generated from the group's manifests, applied with `sandbox_init_with_parameters` before any plugin import | `(deny network*)` plus the plugin API and dispatch sockets; `net` groups add `(allow network-outbound)` | read: the venv, its packages, `files.read` areas; write: its run folder and `files.write` areas; `exec` binaries only; `gpu` adds the IOKit and Metal rules |
| Linux | Landlock for files, seccomp-bpf refusing `socket(AF_INET/AF_INET6)` unless the group has `net`, and refusing `ptrace`, `bpf`, `mount`, `keyctl`; `execve` limited by Landlock to the `exec` binaries | as left | Landlock read/write rules for the areas; `/dev/nvidia*` and `/dev/dri` when `gpu` |
| Windows | a restricted token in a Job Object (kill on close, no breakaway), started by the supervisor; a Windows Filtering Platform filter blocks outbound for the runner's image when the group has no `net` | as left | NTFS and share access through the user's own rights (AppContainer can't reach SMB shares); `exec` enforced by the runner's own allow-list |

A runner that cannot apply its sandbox does not run (`sandbox_unavailable`), except on Linux
without Landlock, where it runs *Degraded* `sandbox_partial` if seccomp is in force (as 54.03).

### 4. Groups and the fingerprint runner

The groups and their members come from the manifests (`runner = "<group>"` or `"own"`; a stage
may name another group). Defaults: `light`, `ml` (own), `index` (own), `net`, `service`, and
`fp`. The `fp` group runs on every machine that runs the pipeline, imports only each enabled
first-party plugin's `fingerprints` module, has no network, and its token allows read-only
statements only.

### 5. GPU batching

The `ml` runner accepts up to the `gpu` pool's size of concurrent `Run`s for `transcribe` and
feeds them to one batched Whisper (today's `stt/batched.py` moves into the transcription
plugin). The supervisor's `pipeline/stt-N` instances become `plugin-runner/ml-N` instances with
the same count, slot names and metrics series (`MAKTABA_STT_SLOT` keeps its meaning).

### 6. Crashes

A runner that dies is restarted with Epic 51's backoff. The queue worker returns its in-flight
jobs to the queue without spending an attempt (§4.9). Five crashes in 10 minutes put the
runner's component *Failed* `runner_crash_loop` (an Epic 51 reason code, 54.37 §3; for
`core.plugin_runners` the failed group is in the condition's `params`), and its stages wait as
`dependency:plugin`.

## Acceptance criteria

- **Given** the `light` runner started on Linux,
  **when** a test plugin in it tries `socket(AF_INET)`, opens `~/.ssh/id_ed25519`, or runs
  `/bin/sh`,
  **then** each fails, and a job that reads a library file it was granted succeeds.
- **Given** the runner's environment,
  **when** inspected from inside a plugin,
  **then** no `DATABASE_URL`, `MAKTABA_HOME_SECRETS` or extension secret is present.
- **Given** the `ml` runner and 32 queued `transcribe` jobs,
  **when** the worker dispatches them,
  **then** they run as one batch, as the in-process path does today.
- **Given** a runner killed mid-job,
  **when** it restarts,
  **then** its jobs are back in the queue with attempts unchanged.
- **Given** a package in the group that fails the trusted check,
  **when** the runner starts,
  **then** it refuses to start and reports `plugin_trusted_unsigned`.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | integration (py, CI only, macOS / Linux / Windows) | escape probes inside each group's sandbox | network, files, exec, ptrace | denied unless declared; positive controls without the sandbox succeed |
| T02 | unit (go) | supervisor child env | start runner | secrets and DSN absent |
| T03 | integration (py) | fake worker, `Hello` / `Run` / `Cancel` / `Result` | protocol | messages per schema; cancel returns checkpoint |
| T04 | integration (py) | `ml` runner with a fake STT backend | 32 concurrent `Run`s | one batch |
| T05 | integration (PG, py) | kill runner mid-job | restart | requeued, attempts unchanged |
| T06 | integration (py) | `fp` runner | `Fingerprints` for 1,000 subjects | answers in one batch; read-only token refuses a write statement |
| T07 | bench (CI) | warm runner | 1,000 trivial `Run`s | N17: p95 ≤ 10 ms |
| T08 | unit | reason codes | words | en + ar for `runner_crash_loop`, `sandbox_unavailable` |

## Edge cases

- **The GPU is busy with another process** (ollama): unchanged from today; the `ml` runner's
  `Hello` reports `unavailable` only for reasons today's `unavailable` reports.
- **A plugin import fails** (a missing ML wheel on a Core install): only that plugin's stages
  report `unavailable: runtime_missing`; the rest of the group serves.
- **A Windows home with media on a mapped drive**: the runner runs as the same user as today's
  worker, so the mapping resolves as it does now.

## Build plan

### Files to touch

| File | Change |
|---|---|
| `pipeline/src/maktaba_plugin_runner/` (new) | §1–§6: main, protocol, sandbox per OS, groups, fingerprints. |
| `shared/plugins/dispatch.proto` | `Hello`, `Fingerprints`, `StillCurrent`. |
| `plugins/sdk/python/maktaba_plugin_sdk/native/` (new) | Generated `PluginAPI` binding, `maktaba:trusted` client. |
| `cmd/maktaba-server/internal/supervisor/` | `plugin-runner` role and instances; env stripping; `stt-N` → `ml-N` instances (wave 3); `catalog_roles_test.go`. |
| `shared/components/catalog.toml` | The `core.plugin_runners` entry; `plugin-runner/index` in `search.semantic` (wave 3 moves `transcription` to `plugin-runner/ml-*`). |
| `web/src/i18n/{en,ar}.json` | Reason codes. |

### Tests

- Integration T01 (CI only, three OSes), T03–T06; unit T02, T08; bench T07.

### Rollout

Dark: runners start only for groups with a first-party plugin whose stages have
`impl = plugin` (54.25), so nothing runs until wave 1.

### Rollback

Flip stages back to `inproc` (54.25); runners stop when no stage of their group is `plugin`.

### Dependencies

54.02 (dispatch messages), 54.22 (trusted check).
