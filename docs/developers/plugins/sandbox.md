# The sandbox

> **Status: on `main`.** The plugin host (`maktaba-plugind`, on Wasmtime), its OS sandbox on
> macOS, Linux and Windows, and the CI job that runs escape probes against a canary on each
> are built, and so are the supervisor's `plugins` role (the host runs only while a plugin is
> on), the circuit breaker and the health figures on the Plugins page. The API's own limits are in the
> [plugin reference](reference.md), generated from the code.

## The rule

Your plugin, and the process that runs it, cannot send or receive a byte over any network
interface, including loopback. The only way out is the plugin API over a unix socket, which
reaches only the home's own API. The home server itself may talk to the store; that is
Maktaba's code, not yours.

## Layer 1: the WebAssembly component

Plugins run as WebAssembly components (WASI 0.2, component model) inside a Rust host that
embeds Wasmtime. A component can call only what the host links into it.

**Linked:**

- `maktaba:plugin/*`, the plugin API (see [Queue stages and the plugin API](queue-and-api.md));
- `wasi:clocks` (monotonic and wall), `wasi:random`, `wasi:io/streams`;
- `wasi:cli/stdout`, `stderr`, `environment` and `exit`, with an empty environment and no
  arguments. Stdout and stderr go to your plugin's log, rate-limited.

**Never linked:** `wasi:sockets`, `wasi:http`, `wasi:filesystem`, `wasi:cli/stdin`, terminal
interfaces, and any process or exec interface.

A component that imports a forbidden interface fails the install audit with
`plugin_forbidden_import`, and would fail to instantiate even if it got past the audit. The
audit also checks that every `maktaba:plugin` interface you import is covered by a capability
you declared (`plugin_undeclared_import`).

Each job gets a fresh Wasmtime store. No state survives between jobs except what you save
through the `data` interface or a checkpoint.

## Layer 2: the OS sandbox

The plugin host applies an OS sandbox to itself before it loads any plugin, and refuses to run
if it cannot.

| OS | How | Network | Files |
|---|---|---|---|
| macOS | a Seatbelt profile | all network denied except the two unix sockets | read its binary, the plugins folder and the compile cache; write its run folder and scratch |
| Linux | user, network, mount, IPC and PID namespaces where allowed; always seccomp-bpf (only `AF_UNIX` sockets); Landlock for files | no interfaces; other socket families answer `EPERM` | Landlock: read plugins and cache, write run and scratch |
| Windows | an AppContainer with no capabilities, inside a Job Object | outbound blocked for the AppContainer | only folders whose ACL grants the AppContainer |

On Windows the host opens its own dispatch socket (AF_UNIX) through Winsock before it enters
its AppContainer, so the sockets are the host's, never your plugin's: Maktaba links no
`wasi:sockets` into a plugin, so a plugin that asks for one is refused at install and could not
start anyway. Maktaba reaches the host through a named pipe its supervisor serves, which only
the user Maktaba runs as can open.

If a Linux kernel lacks some layers (no Landlock, no user namespaces), the host still runs only
if seccomp is in force, and reports *Degraded* with `sandbox_partial`. Without seccomp on
Linux, or without the Seatbelt profile or AppContainer, it does not run.

## Maktaba's own runners

Maktaba's own stages run as first-party plugins in `maktaba-plugin-runner`, one Python
process per runner group ([first-party plugins](first-party.md)). A runner is sandboxed on
every OS from the union of its group's `[trusted]` tables, and does not run when the sandbox
cannot be applied (`sandbox_unavailable`):

| OS | How | Network | Files and programs |
|---|---|---|---|
| macOS | a Seatbelt profile the runner applies to itself | the plugin API's socket and its own; outbound only for a group with `net` | read its areas and the interpreter; write its run folder and write areas; run only its tools |
| Linux | Landlock and seccomp-bpf, applied by the runner to itself | no `AF_INET` socket without `net` | Landlock rules per area; only its tools are executable |
| Windows | its group's own AppContainer (`Maktaba.Runner.<group>`) inside a Job Object, applied by the supervisor's launcher | no capability without `net` (every network path blocked, loopback included); `internetClient` alone for a group with `net` | only the folders granted to its profile; no child process for a group without tools, and only its tools for one with them |

On Windows a process cannot put itself in an AppContainer, so the launcher
(`maktaba-server runner-sandbox-run`) asks the runner for its policy first, grants the
group's profile its folders (read on the interpreter, the read areas and the tools; modify,
never full control, on the group's own run folder and the write areas), takes back what an
earlier start granted that the policy no longer names, and starts the runner. No grant may
reach the supervisor's own folders or the keys, and no runner can write the file the areas
are read from. Each grant and revoke changes only the folder that was checked: the launcher
opens it by handle without following a link, refuses it when it is a junction or symbolic
link or when Windows names it otherwise than checked, and sets its ACL on that handle; what
the folder already holds gets the entry through handles opened inside it, never through a
junction or symbolic link. A folder swapped for a junction after its check fails the start
instead of granting the junction's target. The runner then checks that its
token is that AppContainer's with no other capability, or exits. Every AppContainer may run
the system's programs, so the runner's own guard keeps a group with tools to those tools;
subtitles next to a video are written by name only, as on Linux.

An AppContainer reaches no SMB share, so a library on a share is served by the launcher's
file broker. The broker uses a pipe that only that runner's process may use. For each file
the runner asks for, the launcher checks that the path is in the group's share folders and
spelled one way only, with no junction or symbolic link on the way. It then opens the file
itself and hands the runner the open handle, with read access only. A subtitle is written by
the launcher from the bytes the runner sends. The bytes go straight to the share, so a read costs what the share costs. A share the
group writes in full is not brokered, and its jobs run in the queue worker.

A tool (ffmpeg, ffprobe) runs in the same AppContainer and reaches no share either. When it
reads one file of a brokered share as its input, the runner opens that file through the
broker and hands the tool the handle as its standard input, with the input given as `fd:`.
ffmpeg seeks a file handle as it seeks a path, so an mp4 with its index at the end and a seek
before the input both work. A share path the runner cannot hand over that way is refused, and
that job runs in the queue worker: a second share file, a share path as an output or inside
another argument, another share, or another program.

A runner on Windows starts its tools through anonymous pipes. asyncio's own captured starts
make named pipes, which an AppContainer may not create, so the runner replaces
`asyncio.create_subprocess_exec` with one that keeps its API over `subprocess.Popen`. Each
tool runs in a Job Object of its own, so a timeout's kill or a cancel ends what it started. An AppContainer may not open the `nul` device either, so `DEVNULL` becomes an empty pipe for input and a drained pipe for output.

The runner cannot listen there (an AppContainer may not create a named pipe, and Python on
Windows has no unix sockets), so the supervisor serves its pipes: the worker reaches a runner
through a pipe only the user Maktaba runs as can open, and the runner, from its AppContainer,
dials a second pipe where it waits for those connections and a third that leads to the
plugin API. The runner's pipes admit that user and the group's own AppContainer, and no other
group's.

## How it is proven

A CI job runs on macOS, Linux and Windows for every change to the host, the plugin API or the
manifest schema, and nightly:

- **Hostile components** try forbidden imports, infinite loops, memory bombs, calls without a
  capability or outside scope, API and log floods, oversize writes, and URLs in panels. Each
  must be refused or stopped, and the host must keep serving.
- **Escape probes** in a test build of the host apply the real sandbox and then, as native
  code, try to connect to a local canary listener, resolve names, open raw sockets, bind ports,
  read system files, write outside the run folder and spawn processes. Every attempt must fail
  and the canary must see zero connections.
- **Positive controls** run the same probes without the sandbox and must succeed, so a broken
  probe cannot pass.
- **Maktaba's runners on Windows** are started through the real launcher in their
  AppContainer: from inside, a runner reads and writes only its granted folders, reaches no
  canary on loopback or the LAN without `net`, starts no process, and opens no other group's
  pipe; its plugin API calls and the worker's calls through the supervisor's pipes work.

## Resource limits

You set these in `[resources]`. The host enforces them, and the admin can lower them.

| Resource | Default | Max |
|---|---|---|
| Memory per job | 256 MB | 1 GB |
| Wall time per job (`timeout_s`) | 300 s | 1,800 s |
| CPU instructions per job (`fuel`) | derived from wall time | |
| Concurrent jobs (`concurrency`) | 2 | 8 |
| Scratch per job (`scratch_mb`) | 0 | 512 MB |
| Your own data (`data_mb`) | 50 MB | 1 GB |
| Artifacts (subtitles, artwork) | 200 MB | 2 GB |
| Plugin API calls | 200 per second per job | |
| One value / one artifact write | 64 KB / 4 MB | |
| Log lines | 100 per second, 10 MB per day, 8 KB per line | |
| Package size | | 50 MB |

Scratch space is an in-memory byte store reached through `job.scratch-*` calls, not a
filesystem. It is dropped when the job ends.

The whole home shares one `plugins` pool, sized `max(1, cores / 4)` by default, and your
`concurrency` caps your share inside it.

## Crash isolation

| What happens | Effect |
|---|---|
| Your job traps, times out or hits its memory cap | that job fails; the host carries on |
| The host process dies | the supervisor restarts it; jobs in flight go back to the queue without counting an attempt |
| You flood the API or fill your quota | calls are refused and counted |
| You flood the log | lines are dropped and counted |

The pipeline worker never loads plugin code, so a plugin cannot crash it.

### Failure causes

| Cause | Class | Retried |
|---|---|---|
| `plugin_trap`, `plugin_timeout` | transient | once (stages have 2 attempts) |
| `plugin_memory`, `plugin_capability`, `plugin_scope`, `plugin_quota` | permanent for the item | no |
| plugin held or revoked | waits on the plugin | no attempt spent |

### The circuit breaker

Maktaba holds your plugin and tells admins when, within its window:

- 5 or more traps, timeouts or memory failures happen in 10 minutes, or
- more than half of the last 20 jobs failed, or
- 50 or more API refusals happen in 10 minutes.

It stays held until an admin presses Start.

## Health and logs

Your log lines go to Maktaba's logs as `service = plugin.<id>`, with your version, the job id
and the stage. Log with codes and fields, not free text. Maktaba stores and shows plugin text
escaped, and leaves plugin logs out of problem reports unless the admin includes them.

The Plugins page shows per plugin: phase, jobs done and failed by cause, p50 and p95 job time,
peak memory, fuel used, API calls and refusals, data and artifact use against quota, and the
last error.

## What your plugin may and may not do

| May | May not |
|---|---|
| Read what its granted capabilities allow, in granted libraries | Open sockets, resolve names or make HTTP requests |
| Write its own data and the outputs it was granted | See the filesystem or file paths |
| Read media for the job's subject through handles | Read users, sessions, tokens or secrets |
| Queue more of its own stages | Queue Maktaba's or other plugins' stages |
| Log and send admin notices from its own strings | Start processes or run outside a job |
