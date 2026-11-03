# Story 54.03 — The OS sandbox and the egress tests

> Epic 54 · Phase 1 · Depends on: 54.02 · Needed by: 54.16

## Why

The WebAssembly layer (54.02) is the main wall: a plugin cannot name a socket. But one bug in
Wasmtime would turn a plugin into native code with the server user's rights, which include the
network. The rule is no internet, enforced, not promised. This story puts the host
process itself in each operating system's sandbox, with no network and almost no files, and
proves it in CI on every OS with native code that tries to get out and must fail.

## Description

### 1. When the sandbox is applied

`maktaba-plugind` applies its sandbox in `main()` after opening its two unix sockets and its
cache folder, and **before** reading any plugin. If any required step fails it exits with code
78 and `sandbox_unavailable:<step>`; the supervisor reports `plugins.host` *Failed* with
reason `sandbox_unavailable` (an Epic 51 reason code, added by 54.37; the step goes in the
condition's `params`), and no plugin runs.

### 2. macOS

- A Seatbelt profile `plugind.sb` ships in the app bundle (`Contents/Resources/`), applied with
  `sandbox_init_with_parameters` (the API Chromium uses), parameters `DATA_DIR`, `PLUGINS_DIR`,
  `RUN_DIR`, `CACHE_DIR`.
- Profile: `(version 1) (deny default)`; allow `process-exec` of nothing after start
  (`(deny process-fork) (deny process-exec*)`); `(deny network*)` then
  `(allow network-outbound (remote unix-socket (path-literal (param "RUN_DIR") "/plugin-api.sock")))`;
  `(allow file-read*)` on its own binary, `PLUGINS_DIR`, `CACHE_DIR`, system libraries it links
  (`/usr/lib`, `/System/Library/Frameworks` read-only, as Chromium's renderer profile does);
  `(allow file-write*)` on `RUN_DIR` and `CACHE_DIR` only; `mach-lookup` limited to what the
  Rust runtime needs (none expected; found by running the test suite with `(deny default)` and
  `(debug deny)`).
- The private API is documented as such; if a future macOS removes it, the fallback is a
  sandboxed helper app with the App Sandbox entitlement and no `com.apple.security.network.client`
  (tested in CI today on a branch flag, so it is ready).

### 3. Linux

In order, no setuid helper:

1. `prctl(PR_SET_NO_NEW_PRIVS)`.
2. If unprivileged user namespaces work: `unshare(CLONE_NEWUSER | CLONE_NEWNET | CLONE_NEWNS |
   CLONE_NEWIPC | CLONE_NEWPID)`, map the user to itself, leave the new network namespace with
   no interface up (not even loopback), make the mount tree private and bind-mount only
   `PLUGINS_DIR` (read-only), `CACHE_DIR`, `RUN_DIR` into a tmpfs root, then `pivot_root`.
   (This is what bubblewrap's `--unshare-net` does, in-process.) If user namespaces are blocked
   (Ubuntu 24.04's AppArmor restriction, hardened kernels), skip this step and record it.
3. **Always** Landlock (ABI ≥ 1, kernel ≥ 5.13): read-only `PLUGINS_DIR` and the binary's
   libraries, read-write `RUN_DIR` and `CACHE_DIR`; with ABI ≥ 4 also deny TCP bind and connect.
4. **Always** seccomp-bpf (allow-list): `socket()` only with `AF_UNIX`; no `ptrace`,
   `process_vm_*`, `bpf`, `perf_event_open`, `mount`, `keyctl`, `add_key`, `kexec_*`,
   `init_module`, `userfaultfd`, `io_uring_*`, `clone` with namespace flags, `execve` after
   start. Default action `EPERM` for socket families, `KILL_PROCESS` for the rest.
5. Resource limits: `RLIMIT_AS` = 2 GB + pool reservation, `RLIMIT_NOFILE` = 256,
   `RLIMIT_NPROC` = current threads + 16, `RLIMIT_CORE` = 0; and a cgroup v2 `memory.max`
   when the supervisor runs under systemd with delegation (Epic 39 packages).

Seccomp missing → refuse to run. Landlock or namespaces missing → run *Degraded*
(`sandbox_partial`, listing what is missing) only because seccomp alone already refuses every
network socket family.

### 4. Windows

- The supervisor creates an **AppContainer profile** `Maktaba.Plugins` once (no capabilities:
  no `internetClient`, `internetClientServer`, `privateNetworkClientServer`, no
  `lpacCom`-style extras) and starts `maktaba-plugind.exe` in it with
  `PROC_THREAD_ATTRIBUTE_SECURITY_CAPABILITIES`, inside a **Job Object** with
  `JOB_OBJECT_LIMIT_PROCESS_MEMORY`, `JOB_OBJECT_LIMIT_ACTIVE_PROCESS = 1`,
  `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`, and a CPU rate cap.
- File access: the installer grants the AppContainer SID read on `plugins\` and read-write on
  `run\plugins\` and `plugins\cache\` only.
- Network: blocked by the Windows Filtering Platform's default rule for AppContainers without
  network capabilities. Because a WFP default-rule bypass was found in 2021, the host also
  refuses to call `WSAStartup` after start (a self-check fails the start if Winsock is already
  loaded by a dependency) and the escape probes cover loopback and LAN explicitly.
- The host checks it is in an AppContainer (`GetTokenInformation(TokenIsAppContainer)`) and
  refuses to run otherwise.

### 5. The egress tests (CI only)

Job `plugins-sandbox` (README §5.4), matrix `macos-15`, `ubuntu-24.04`, `ubuntu-22.04`,
`windows-2022`:

1. **Canary.** The job starts a canary listener on `127.0.0.1:47001`, `[::1]:47001`, the
   runner's primary LAN address `:47001` (TCP and UDP), and a fake DNS server on the LAN
   address, all logging every connection or packet. On Linux and Windows the step's outbound
   traffic to anything but the canary is blocked by the runner firewall (`iptables` /
   `New-NetFirewallRule`); on macOS by `pf` anchors. So even a failing test calls nothing on the
   internet.
2. **Escape probes.** `maktaba-plugind --escape-probes` (built with `--features escape-probes`,
   never shipped) applies the real sandbox, then as native code tries: TCP connect and UDP send
   to each canary address and to `203.0.113.10` (TEST-NET-3, routed to the canary by the
   firewall rule); `getaddrinfo("example.invalid")`; raw and packet sockets; `bind(0.0.0.0:0)`;
   opening `/etc/passwd`, `~/.ssh/id_ed25519` (a planted fake), `C:\Users\runneradmin\.ssh\…`,
   the database socket folder and `<data>/state/`; writing to `<data>/`; `fork`/`exec`
   (`CreateProcess` on Windows); loading a library from the plugins folder. Each probe prints
   `PROBE <name> DENIED|ALLOWED <errno>`.
3. **Assertions.** Every probe `DENIED`; the canary logged **zero** connections and packets
   (N8); the host exit code is 0.
4. **Positive control.** The same binary with `--escape-probes --no-sandbox` must report every
   network and file probe `ALLOWED` and the canary must log them, proving the probes work.
5. **Wasm guests.** The hostile components of 54.02 run inside the sandboxed host too (both
   layers on).
6. **Partial sandbox.** On `ubuntu-24.04` the job also runs with user namespaces disabled
   (`sysctl kernel.apparmor_restrict_unprivileged_userns=1`) and asserts *Degraded*
   `sandbox_partial` and still zero network.

The job is **CI only**: it changes firewall rules and runs native escape attempts, so it never
runs on the owner's Mac (standing rule: such tests run only in CI), and the pre-push hook only
compiles it.

## Acceptance criteria

- **Given** the sandboxed host on each of the four runners,
  **when** the escape probes run,
  **then** every probe is denied and the canary logs zero connections.
- **Given** the same probes without the sandbox,
  **when** they run,
  **then** every network and file probe succeeds (the control is valid).
- **Given** a Linux runner where user namespaces are blocked,
  **when** the host starts,
  **then** it runs *Degraded* with `sandbox_partial: namespaces`, and every network probe is
  still denied by seccomp.
- **Given** a Linux kernel without seccomp, or a Mac where `sandbox_init_with_parameters`
  fails, or Windows outside an AppContainer,
  **when** the host starts,
  **then** it exits 78 with `sandbox_unavailable` and no plugin runs.
- **Given** a PR that changes `plugind.sb` to allow `network-outbound` to TCP,
  **when** CI runs,
  **then** `plugins-sandbox` fails on macOS.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | CI (4 OS) | canary + firewall | escape probes, sandboxed | all DENIED; canary 0 |
| T02 | CI (4 OS) | same | escape probes, `--no-sandbox` | network and file probes ALLOWED; canary counts them |
| T03 | CI (ubuntu-24.04) | userns blocked | start | Degraded `sandbox_partial`; T01 passes |
| T04 | unit (rust, linux) | seccomp filter | decode BPF program | socket families other than AF_UNIX → EPERM |
| T05 | unit (rust) | each OS, sandbox step fails (injected) | start | exit 78, `sandbox_unavailable:<step>` |
| T06 | integration (go, supervisor) | host exits 78 | supervise | `plugins.host` *Failed* `sandbox_unavailable`, no restart (exit 78 is not retried; any other crash stops at Epic 51's limit with `crash_loop`) |
| T07 | CI (4 OS) | hostile components inside sandboxed host | run | as 54.02 T02–T04 |
| T08 | lint | `plugind.sb` | grep | no `allow network` rule except the unix-socket literal |

## Edge cases

- **The data folder on a network share** (SMB on Windows, NFS on Linux): Landlock and the
  AppContainer ACL still apply to the local mount path; the escape probes include a write to
  the share's parent.
- **Antivirus on Windows** injecting a DLL that loads Winsock: the start self-check catches it
  and reports `sandbox_unavailable:winsock_loaded` with words telling the owner which product.
- **Older macOS** without a profile operation the `.sb` uses: CI runs the oldest supported macOS
  in the matrix when the Mac app's minimum changes.

## Build plan

### Files to touch

| File | Change |
|---|---|
| `plugind/src/sandbox/mod.rs`, `macos.rs`, `linux.rs`, `windows.rs` (new) | §2–§4. Crates: `landlock`, `seccompiler`, `nix`, `windows-sys`. |
| `plugind/resources/plugind.sb` (new) | The macOS profile. |
| `plugind/src/probes.rs` (new, `escape-probes` feature) | §5.2. |
| `cmd/maktaba-server/internal/supervisor/appcontainer_windows.go` (new) | Profile creation and start inside AppContainer + Job Object. |
| `apps/` Mac app bundle scripts | Ship `plugind.sb` in Resources; sign `maktaba-plugind`. |
| Windows installer (Epic 38) | ACLs for the AppContainer SID. |
| `.github/workflows/plugins-sandbox.yml` (new, reusable from `ci.yml`) | The matrix job, canary, firewall, probes, controls. |
| `tools/ci/plugins/canary.py` (new) | Canary listener and fake DNS. |
| `tools/ci/tests/test_plugind_profile.py` (new) | T08. |

### Tests

- Unit: T04, T05, T08. Integration: T06.
- **CI only:** T01–T03, T07 in `plugins-sandbox` on four runners; required for PRs touching
  `plugind/`, the profile, the supervisor's plugin role or the Windows installer.

### Rollout

Ships with 54.02; the host is not started until a plugin is enabled. The Windows AppContainer
profile is created on first start of the role, not at install.

### Rollback

Release rollback. No data.

### Dependencies

54.02. Epic 38 (Windows installer) and Epic 36 (Mac bundle) for packaging.
