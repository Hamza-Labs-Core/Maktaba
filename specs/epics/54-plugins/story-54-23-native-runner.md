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
   former `StageImpl.unavailable`), and its concurrency. On Windows it has no socket of its
   own (§3): it serves the worker through the supervisor's pipes.

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
| Windows | the group's own AppContainer (`Maktaba.Runner.<group>`) inside a Job Object (kill on close), the way the plugin host runs (54.03 §4): the supervisor's launcher (`maktaba-server runner-sandbox-run`) asks the runner for its policy (`--policy`), grants the profile its folders and starts it there; the runner checks its token is that AppContainer's and refuses to serve otherwise | no capability without `net`: the Windows Filtering Platform blocks every network path, loopback included; `internetClient` alone for a group with `net` | read: the interpreter, `files.read` areas, the tools' installations and the worker's `areas.json`; modify (never full control, so it cannot change an ACL): its own run folder `<runner dir>\<group>` and `files.write` areas (grants an earlier start made that the policy no longer names are taken back); each grant and revoke changes only the object that was checked: it is opened by handle without following a link at its end, refused when it is a reparse point or when the filesystem names it otherwise than checked (a junction swapped in above it), held so it cannot be renamed, and its DACL set on that handle; what a folder already holds gets the entry by handles opened relative to it, never through a junction or symbolic link, each child's DACL read as stored (no inference of what it inherits), a child with a protected DACL is left alone, and one with a NULL DACL or none fails the grant (never changed in passing); the areas come from the worker's `areas.json`, which no runner can write, and no grant may hold or be inside the run folder, the supervisor's folder or the keys; a folder on another machine is never granted (its ACL is not the home's to change, and an AppContainer cannot reach SMB shares): a share folder the group reads or writes by name is served by the launcher's file broker (below), one it writes in full is not; no child process for a group without `exec` (the Job Object's limit), and for one with `exec` only its tools (the runner's own guard: every AppContainer may run the system's programs), started through anonymous pipes (asyncio's own captured starts make named pipes, which an AppContainer may not create: `maktaba_plugin_runner/winproc.py` keeps asyncio's API over `subprocess.Popen`, each tool in a Job Object of its own that a kill or a cancel terminates); `library.subtitles` by name through the runner's guard, as on Linux |

A runner that cannot apply its sandbox does not run (`sandbox_unavailable`), except on Linux
without Landlock, where it runs *Degraded* `sandbox_partial` if seccomp is in force (as 54.03).

On Windows a runner cannot listen: an AppContainer process may not create a named pipe, and
Python on Windows has no `AF_UNIX`. The supervisor serves three pipes per group, under a name
prefix new to each of its runs: the worker's pipe (admitting only the user Maktaba runs as),
through which the worker sends `Hello`, `Run` and `Fingerprints`; the runner's back pipe,
which the runner dials to wait for the worker's connections (the supervisor pairs each
worker connection with a waiting one and then relays the bytes unchanged, or closes it
without its ready byte when none waits, so the worker runs the job itself, as when a runner's
socket refuses it elsewhere); and the runner's API pipe, relayed to the plugin API's socket as
the plugin host's relay is. The runner's pipes admit that user and the group's AppContainer,
and no other group's.

**A library on a share (Windows).** An AppContainer with no capabilities reaches no SMB share,
so a job on a share-hosted library would run in the worker. Three ways were weighed:

- *Capabilities* (`privateNetworkClientServer` with `enterpriseAuthentication`): refused. They
  open the whole local network and every share and host the user's credentials reach, and
  cannot be scoped to one share's library folder.
- *A byte broker*: the supervisor serves the folder's bytes over a pipe. It can be confined,
  but every byte of a video crosses two more copies and a pipe, and seeks become round trips.
- *A handle broker* (chosen): the launcher (`runner-sandbox-run`, outside the sandbox, already
  waiting on the runner) serves a pipe per runner, which admits only the group's AppContainer
  SID and, on each connection, only the runner's own process id (not its tools, not another
  process of the AppContainer). For each file the runner asks for, the launcher confines the
  path: a `\\server\share` path at or under one of the group's share folders (any case), with
  no `.` or `..`, no empty part, no stream, no device name, no trailing dot or space, and no 8.3
  alias. It opens the folders from the share folder down and then the file, each without
  following a link, and refuses a junction or symbolic link anywhere on the way. It also
  refuses a file the filesystem names otherwise than asked. It compares by ASCII case only:
  Windows' upcase table is not Go's, so a root never matches a differently folded sibling.
  Then it duplicates the handle into the runner's process with read access only. A subtitle
  (the runner's `SUBTITLE_NAME`, in a folder the group writes by name) is written by the
  launcher itself from the bytes the runner sends when the file closes (at most 16 MiB). The
  file is opened and checked before a byte changes, so the runner never holds a handle it
  could write with, or turn into a link. A subtitle is renamed within its own folder
  (`.maktaba-tmp` to its name) or removed by handle after the same checks. An administrative
  share (`C$`, `ADMIN$`) is never brokered. A share this machine serves is mapped back to its
  folder and refused, like the folder itself, when it holds or is inside a protected folder.
  The bytes of a read go through the handle straight to the SMB redirector, so a read costs
  what the share costs (CI measures it, T10).

A share folder the group writes in full (`files.write`, the file stages) is not brokered, and
its jobs run in the worker. A tool (ffmpeg) cannot use the runner's handles, so a job that
hands one a share path is refused (`sandbox_refused`) and runs in the worker; the subtitle
stage, which reads and writes through Python's own calls, runs in its runner. The runner
routes its calls on a brokered path (`open`, `os.stat` and `os.path.exists` and the like,
`os.listdir`, `os.replace`, `os.remove`) to the broker, each raising the audit event the plain
call raises, so its subtitle-name and job-scope guards see the path as before
(`maktaba_plugin_runner/winfiles.py`). A broker that cannot start leaves the runner to start
without its shares (`plugin_runner_broker_unavailable`), as before the broker.

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
| T09 | integration (go, CI only, Windows) | a checked folder, or a folder above it, swapped for a junction to a decoy between the last check and the grant or revoke (a test hook) | start the runner | the decoy gets no entry and keeps its own; the start fails closed; without a swap the entry reaches what the folder holds and stops at a junction inside it |
| T10 | integration (go + py, CI only, Windows, two SMB shares made with `New-SmbShare`) | a group whose library is on `\\localhost\<share>`, started through the launcher with its broker | from inside its AppContainer: read, list, stat a video in any case; write a subtitle; write another name, write or remove the video; a traversal, a junction out of the share, an 8.3 alias, a stream, the other share; a tool on a share path; another process of the AppContainer on the pipe | without the broker no share is reached; with it the library is read and the subtitle written, everything else refused; the read throughput through the handle and outside the sandbox is logged |
| T12 | integration (go + py, CI only, Windows) | a captured tool start (asyncio, stdout and stderr PIPE) inside a runner's AppContainer | asyncio's own start; the same through the runner's anonymous pipes; a cancelled communicate | asyncio's own is refused (`WinError 5`, the control); through anonymous pipes the output (4 MiB), error output and exit code come back, and the cancel ends the tool at once |

## Edge cases

- **The GPU is busy with another process** (ollama): unchanged from today; the `ml` runner's
  `Hello` reports `unavailable` only for reasons today's `unavailable` reports.
- **A plugin import fails** (a missing ML wheel on a Core install): only that plugin's stages
  report `unavailable: runtime_missing`; the rest of the group serves.
- **A Windows home with media on a mapped drive or a share**: an AppContainer cannot reach an
  SMB share, and the launcher never changes a remote folder's ACL. A share folder the group
  reads or writes by name is served by the launcher's file broker (§3), so the subtitle stage
  runs in its runner there. A job the broker refuses, a tool's job on a share path, and any
  job on a share the group writes in full is refused (`sandbox_refused`) and runs in the worker
  instead, logged, as the worker runs every job of a runner that is starting or failed.
- **A folder swapped for a junction or symbolic link between its check and its grant** (a
  process of the same user, or another group's runner in a folder it may write): the grant or
  revoke is applied by handle to the object that was checked, so it never lands on the
  junction's target; the swap makes the object fail the check by handle, and the start fails
  closed (`sandbox_unavailable`, the worker runs the group's jobs).
- **The GPU on Windows**: a group with `gpu` starts in its AppContainer as any other, but
  whether a GPU is reachable from inside one is not proven: no CI runner has a GPU, so it
  stays open until one does.

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
