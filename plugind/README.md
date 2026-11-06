# maktaba-plugind: the plugin host

Epic 54 ([spec](../specs/epics/54-plugins/README.md)), stories 54.02 and 54.03. Third-party
plugin code runs here and nowhere else: one process per home, started by the supervisor
(role `plugins`) only while a plugin is enabled.

| Crate | What |
|---|---|
| `crates/pluginmanifest` | the Rust manifest validator (story 54.01): `shared/plugins/manifest.schema.json`, the capability and event lists, the same rules and normalised JSON as the Go, Python and TypeScript validators |
| `crates/core` (`plugind-core`) | the host as a library: the Wasmtime engine and linker, one store per job with fuel, an epoch deadline and memory caps, the import audit, the compile cache, the `maktaba:plugin` forwarders, plugin log limits and the dispatch protocol. The SDK's harness (54.12) runs this same code |
| `crates/plugind` | the `maktaba-plugind` binary: flags, the OS sandbox (`src/sandbox/`), the dispatch socket; `--features escape-probes` adds the CI-only escape probes |
| `guests/` | test plugins built for `wasm32-wasip2` (`make plugind-guests`): a well-behaved one, a hostile one, and one written with Rust std that reaches for sockets and files |

## Two walls

1. **WebAssembly.** The linker holds the `maktaba:plugin` interfaces and, from WASI, only
   clocks, random, `io` streams, stdout/stderr, an empty environment and exit. Sockets,
   HTTP, filesystem, stdin, terminals and processes are never linked, so a component that
   imports one is refused by the audit (`plugin_forbidden_import`) and, with the audit
   bypassed, cannot instantiate. A gated interface imported without a declared capability is
   `plugin_undeclared_import`. An ungranted call answers `call-error.capability` without
   leaving the host; the plugin API checks every call again.
2. **The OS sandbox** (`resources/plugind.sb`, `src/sandbox/`), applied in `main` after the
   dispatch socket is bound and before any plugin is read; a required layer that fails is
   exit 78 `sandbox_unavailable:<step>`. macOS: Seatbelt, deny by default, no network but the
   two unix sockets. Linux: no-new-privs, user/network/IPC namespaces when allowed,
   Landlock, and always seccomp refusing every socket family but `AF_UNIX`, exec and fork.
   Windows: the supervisor starts the host in an AppContainer with no capabilities inside a
   Job Object (`cmd/maktaba-server/internal/appcontainer`); the host refuses to run outside one.

CI (`.github/workflows/_plugins.yml`, job `plugins-sandbox`) runs native escape probes in
the sandboxed host on macOS, Ubuntu 22.04 and 24.04 and Windows against a local canary, and
the same probes unsandboxed as the positive control. Those probes run only in CI.

## Decisions made while building it

- **Dispatch frames are JSON**, length-prefixed, not protobuf (54.02 §2): the pipeline,
  the host and the harness need no code generator, and bytes travel as base64.
- **One `run(stage)` export.** The manifest's per-stage `export` names are kept; the SDK
  dispatches on the stage name, so every language needs one export.
- **The job's subject is `job.job-subject()`**, and the list functions are `%list` in WIT
  (`subject` and `list` clash with a type and a keyword).
- **Default fuel is the story's 2×10⁹ per 60 s of timeout.** A plugin that needs more CPU
  declares `resources.fuel`.
- **Instances, memories and tables per store are 16, 4 and 16**, not 1: a component holds
  several core instances (its module and shims).
- **The Linux seccomp filter denies by name** (EPERM) rather than allowing by name: an
  allow-list breaks with each libc that adopts a new syscall, and the network rule (the
  socket-family check) is exact either way. Namespaces are tried in a forked child first, so
  a half-refused `unshare` never leaves the host unmapped. No mount namespace or
  `pivot_root`: Landlock is the file layer. `RLIMIT_NPROC` is not set (it counts every
  process of the server's user, not the host's threads).
- **The Seatbelt profile is compiled into the binary** (`include_str!`), so a signed binary
  cannot be handed another profile beside it.
- **Package paths are checked by their words**, not resolved, because a sandboxed process may
  not stat the folders above its own; the supervisor's unpacker makes no links.
- **The Rust template is `no_std`** (see `guests/hello`): Rust std on `wasm32-wasip2` imports
  `wasi:cli/stdin` and the terminal interfaces, which the host does not link.

## The seams later stories plug into

- **Signatures (54.14, Phase 2):** `componentpack.UnpackPlugin` takes `PluginExpect{FilesSHA256}`
  from the verifier and keeps `signatures/developer.sig` and `signatures/store.sig` beside
  `files.json`, which lists every file (`manifest.toml` included), so a signature over
  `files.json` covers the whole package. Nothing in this PR reads a signature.
- **Plugin logs and Epic 53 (53.02):** `plugind-core/src/logging.rs`, trait `LogSink`. Lines
  are Epic 53 lines (`service = plugin.<id>`, version, job id, stage) on the host's stdout
  today; the log keeper connects there.
- **The supervisor, the catalog entry `plugins.host` and the plugin API** are the next parts
  of this series (54.04–54.07, 54.37, 54.38).
