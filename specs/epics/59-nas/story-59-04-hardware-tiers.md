# Story 59.04 — Hardware detection and tiers: memory (with ZFS ARC), CPU features, GPUs, disks

> Epic 59 · Phase 1 · Depends on: — · Needed by: 59.02, 59.05, 59.06, 59.07, 59.09, 59.10, 59.13

## Why

Maktaba's defaults assume a desktop with 16 GB (`docs/hardware.md`, the Full bundle). NAS boxes
range from 4 GB with four ARM cores to 32 GB with a GPU, and they share their memory with the
NAS's own system.

Two readings mislead today:
- **Memory on TrueNAS:** it is read from `MemAvailable`, which does not count the ZFS ARC, and
  the ARC fills nearly all RAM. A healthy TrueNAS box looks full.
- **Container limits:** a container limited to 4 GB still sees the host's `MemTotal`.

Speech-to-text speed depends on AVX2, which the J4125 in many Synology models lacks. Video
depends on whether a render device exists.

## Description

**What is detected** (`internal/hwinfo`, at setup and at each start, cached for the server's
life):

| Item | Source |
|---|---|
| Memory limit | cgroup v2 `memory.max`, else v1 `memory.limit_in_bytes`, else `MemTotal` |
| Memory available | `MemAvailable` + ZFS ARC reclaimable (`arcstats`: `size − c_min`, when `/proc/spl/kstat/zfs/arcstats` is readable), capped at the limit |
| CPU | `nproc` (cgroup-aware), architecture, `avx2`/`avx512f` from `/proc/cpuinfo` flags |
| GPU | `/dev/dri/renderD*` (driver name from `/sys/class/drm/*/device/driver`), `/dev/nvidia*` + `nvidia-smi -L` when present |
| Data disk | rotational (59.02) |
| Kernel | version; Landlock ABI (`landlock_create_ruleset` with the version flag); user namespaces (`unshare(CLONE_NEWUSER)` in a child) |
| Platform | `dsm` (`/etc.defaults/VERSION` when mounted, or a DSM kernel string such as `+` builds with `synology`), `truenas` (`/etc/version` when mounted, or a `truenas` kernel string), `unraid` (`/etc/unraid-version` when mounted), else `docker` or `linux`. `MAKTABA_PLATFORM` overrides |

Platform detection is best-effort from inside a container. The compose files of 59.09–59.11 set
`MAKTABA_PLATFORM` explicitly, so detection only matters for hand-made installs.

**Tiers.** The rules and defaults are in the README (§6.4). The tier is computed from the memory
limit and the GPU, and stored as `server.tier` with how it was chosen. An admin can pick
another tier in Settings → This machine, which re-applies that tier's defaults and leaves
individual overrides alone.

| Setting the tier drives | Light | Standard | Strong |
|---|---|---|---|
| Postgres memory (59.02) | 256MB | 512MB | 25% |
| Pipeline workers | 1 | 2 | today's |
| Concurrent transcodes | 1, ≤ 720p in software | 2 | GPU-limited |
| Speech to text (59.06) | off; `base` when turned on | `small` (`base` without AVX2) | `large-v3-turbo` |
| Quiet hours (59.02) | 01:00–06:00 | 01:00–06:00 | none |
| Thumbnail sprites | quiet hours | normal | normal |

**The setup check** keeps its refusal under 4 GiB, now measured against the memory **limit**. On
a NAS platform, the refusal text says:

> This NAS has {n} GB of memory for Maktaba. Maktaba needs at least 4 GB. Run the Maktaba home
> on another computer and keep your media on this NAS: that computer will read it from here.

**Settings → This machine** shows:
- the tier and why it was chosen ("Standard: 8 GB, no GPU for video");
- memory with the ZFS cache shown as available;
- CPU and its features;
- the GPU and whether video uses it (59.05);
- the data disk's type;
- the plugin isolation level (59.07).

All of it is also in the diagnostics bundle.

## Acceptance criteria

- **Given** a container with `--memory=6g` on a 32 GB host, **then** the tier is Standard and
  Settings shows 6 GB.
- **Given** TrueNAS with 30 GB of ARC and 1 GB `MemAvailable` on a 32 GB box, **then** available
  memory shows about 30 GB, not 1 GB.
- **Given** a J4125, **then** AVX2 shows as absent and Standard picks `base` for speech.
- **Given** 3 GB, **then** setup refuses with the NAS text on a NAS platform, and today's text
  elsewhere.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | Go unit | fake cgroup v2 `memory.max=6442450944` | detect | limit 6 GiB |
| T02 | Go unit | cgroup v1 file | detect | limit read |
| T03 | Go unit | fake arcstats: size 30G, c_min 1G, MemAvailable 1G | detect | available ≈ 30G |
| T04 | Go unit | cpuinfo of a J4125 (fixture) | detect | avx2=false |
| T05 | Go unit | fake `/sys/class/drm` with i915 and amdgpu | detect | render device + driver |
| T06 | Go unit | each platform fixture (DSM, TrueNAS, Unraid) | detect | right platform |
| T07 | Go unit | rule table | tier for (4G, arm64), (8G, no GPU), (8G, NVIDIA) | Light, Standard, Strong |
| T08 | CI container (59.15) | `--memory=4g` | setup | Light, defaults applied |
| T09 | web unit | This machine panel | render | tier + reason in both languages |

## Strings

| Key | English | Arabic |
|---|---|---|
| `machine.tier.light` | Light | خفيف |
| `machine.tier.standard` | Standard | قياسي |
| `machine.tier.strong` | Strong | قوي |
| `machine.tier.reason` | {tier}: {memory} GB, {gpu} | {tier}: ‏{memory} غيغابايت، {gpu} |
| `machine.gpu.none` | no GPU for video | لا معالج رسوميات للفيديو |
| `machine.memory.zfs` | including {n} GB the ZFS cache will give back | منها {n} غيغابايت سيعيدها تخزين ZFS المؤقت |
| `setup.memory.nas_refused` | This NAS has {n} GB of memory for Maktaba. Maktaba needs at least 4 GB. Run the Maktaba home on another computer and keep your media on this NAS: that computer will read it from here. | لدى هذا الخادم {n} غيغابايت من الذاكرة لـ Maktaba، ويحتاج Maktaba إلى 4 غيغابايت على الأقل. شغّل جهاز Maktaba الرئيسي على حاسوب آخر وأبقِ وسائطك على هذا الخادم: سيقرؤها ذلك الحاسوب من هنا. |

## Edge cases

- **cgroup limit set to `max`:** fall back to `MemTotal`.
- **ARC stats not readable** (TrueNAS but `/proc/spl` hidden): available is `MemAvailable`, with a
  note in Settings that the ZFS cache could not be read. The TrueNAS app (59.10) does not hide
  it.
- **Memory added later:** the next start recomputes the tier. If the computed tier is higher and
  the admin never picked one, defaults move up, and a notice says so.

## Build plan

| File | Change |
|---|---|
| `cmd/maktaba-server/internal/hwinfo/` (new) | detection, tier rules |
| `cmd/maktaba-server/setup_steps.go` | limit-based check, NAS text |
| `cmd/maktaba-server/internal/config/` | `server.tier`, apply defaults |
| `api/internal/handlers/system/` | `GET /api/system/machine` (admin; 59.16) |
| `web/` | This machine panel |
| `docs/hardware.md` | the NAS tiers |
