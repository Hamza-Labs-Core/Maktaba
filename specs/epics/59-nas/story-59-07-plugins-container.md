# Story 59.07 — Plugins in a container: isolation levels, plugind's layers, the admin's yes

> Epic 59 · Phase 2 · Depends on: 59.01, 59.03, 59.04 · Needed by: 59.09, 59.10, 59.15

## Why

Epic 54 has two kinds of plugin, each with its own isolation:
- **First-party plugins** run as trusted Python in plugin runners, inside a Landlock + seccomp
  sandbox. Without Landlock a runner runs as `sandbox_partial`.
- **Third-party plugins** run as WebAssembly in `maktaba-plugind`, with namespaces, Landlock and
  seccomp. A required layer that fails makes plugind exit 78.

Inside a NAS container, two things work against those layers:
- **DSM kernels** are likely 4.4 or 5.10 (research.md §2.5, unverified), and Landlock needs 5.13.
- **Docker's default seccomp profile** blocks `unshare(CLONE_NEWUSER)`.

So on most DSM boxes plugind would refuse to start, and runners would run with less isolation
than the household was told. Neither should happen silently.

## Description

**Isolation levels.**
- The home computes one level at start from 59.04's kernel facts and the container's own
  settings. The levels and what each allows are in the README (§6.6): Full, Container, None.
- The level is shown in Settings → This machine and on the plugins page, with one line on what it
  means:
  - **Full:** "Plugins run in Maktaba's own sandbox."
  - **Container:** "Plugins run inside this container's limits, without Maktaba's extra file
    sandbox. The NAS kernel doesn't support it."
  - **None:** refused at start (59.01).

**plugind's layers by level.**
- **Full:** as today. No-new-privs, namespaces, Landlock and the `AF_UNIX`-only seccomp are all
  required.
- **Container:**
  - required: no-new-privs, the Wasm sandbox (WASI without sockets or filesystem, host calls only
    through Maktaba's API) and its own seccomp filter. plugind installs that filter itself on top
    of Docker's; stacking filters needs only no-new-privs.
  - best-effort: Landlock and namespaces.
- A required layer that fails still exits 78. plugind reports the level it got to the server, and
  the server shows it.

**First-party runners by level.**
- **Full:** as today.
- **Container:** `sandbox_partial`, as today, and visible. Runner seccomp, which blocks `AF_INET`
  unless the group has `net`, is installed on both levels.
- The `files` and `fp` runners' write grants are resolved per library access (59.03), so a
  read-only library is never written even when Landlock is missing.

**The admin's yes for third-party plugins on the Container level.**
- Installing or enabling a third-party plugin on a Container-level home shows what it means,
  with a switch: **Allow plugins with container-only isolation on this home**. Off by default.
  - While it is off, third-party plugins can be installed but stay disabled, with the reason.
  - Turning it on needs the admin role (server-enforced; 59.16) and is recorded in the audit
    log.
- First-party plugins need no switch. They are part of Maktaba, as on the Mac and Windows apps,
  where Landlock does not exist either.

**GPU and network grants.**
- `gpu` runner groups get `/dev/dri` and `/dev/nvidia*` when the container has them.
- `net` groups (youtube, classify) go through the container's network, with grants as today.

## Acceptance criteria

- **Given** the 59.15 `no-landlock` cell, **then** the level is Container, first-party plugins
  run as `sandbox_partial`, and plugind starts with its required layers.
- **Given** a Container-level home, **when** an admin enables a third-party plugin without the
  switch, **then** it stays disabled with the reason. After the switch it runs, and the audit log
  has the entry.
- **Given** a non-admin calling the switch's endpoint, **then** 403.
- **Given** a read-only library on a Container-level home, **when** a runner's stage tries to
  write there, **then** the grant is refused before the write.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | Go unit | facts: landlock=0, userns=0 | level | Container |
| T02 | Go unit | facts: all present | level | Full |
| T03 | Rust unit (plugind) | Container level, Landlock unavailable | start | runs; reports the level |
| T04 | Rust unit | Container level, no-new-privs fails | start | exit 78 |
| T05 | Go handler (59.16) | viewer / editor / admin | toggle the switch | 403 / 403 / 200 + audit |
| T06 | CI `no-landlock` cell | sample third-party plugin | enable | held, then runs after the switch |
| T07 | pipeline unit | ro library, `files` runner | write | refused by grant resolution |
| T08 | Device checklist | DSM kernel | start | level and kernel facts recorded per model |

## Strings

| Key | English | Arabic |
|---|---|---|
| `plugins.isolation.full` | Plugins run in Maktaba's own sandbox. | تعمل الإضافات داخل بيئة العزل الخاصة بـ Maktaba. |
| `plugins.isolation.container` | Plugins run inside this container's limits, without Maktaba's extra file sandbox. The NAS kernel doesn't support it. | تعمل الإضافات ضمن حدود هذه الحاوية، دون بيئة عزل الملفات الإضافية الخاصة بـ Maktaba، لأن نواة الخادم لا تدعمها. |
| `plugins.allow_container` | Allow plugins with container-only isolation on this home | اسمح بالإضافات ذات العزل على مستوى الحاوية فقط على هذا الجهاز الرئيسي |
| `plugins.allow_container.explain` | Plugins from other developers will be limited by the container and Maktaba's plugin rules, but not by the extra file sandbox. Turn this on only for plugins you trust. | ستُقيَّد الإضافات من المطوّرين الآخرين بالحاوية وبقواعد الإضافات في Maktaba، لكن ليس ببيئة عزل الملفات الإضافية. لا تفعّل هذا الخيار إلا للإضافات التي تثق بها. |
| `plugins.held_isolation` | Off: this home needs your permission for plugins with container-only isolation. | معطّلة: يحتاج هذا الجهاز الرئيسي إلى إذنك لتشغيل إضافات بعزل على مستوى الحاوية فقط. |

## Edge cases

- **The kernel gains Landlock after a DSM update:** the next start computes Full. The switch
  stays as set, but no longer matters.
- **A plugin installed on Full, then the home moves to a Container-level NAS** (restore onto a
  NAS): the plugin is held until the admin says yes.
- **Docker run with a custom seccomp profile that allows userns:** namespaces are used, and the
  level is still Container without Landlock.

## Build plan

| File | Change |
|---|---|
| `cmd/maktaba-server/internal/plugins/isolation.go` (new) | level from facts |
| `plugind/src/sandbox/` | per-level required layers; report level |
| `pipeline/src/maktaba_plugin_runner/sandbox.py` | per-level reporting; grants by library access |
| `api/internal/handlers/plugins/` | switch endpoint (admin), held state |
| `web/` | level line, switch with explanation |
