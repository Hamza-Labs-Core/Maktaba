# Story 59.04 — Hardware detection and tiers: memory (with ZFS ARC), CPU features, GPUs, disks

> Epic 59 · Phase 1 · Depends on: — · Needed by: 59.02, 59.05, 59.06, 59.07, 59.09, 59.10, 59.13

## Why

Maktaba's defaults assume a desktop with 16 GB (`docs/hardware.md`, the Full bundle). NAS boxes
range from 4 GB with four ARM cores to 32 GB with a GPU, and they share their memory with the
NAS's own system.

Two readings mislead today:
- **Free memory on TrueNAS:** it is read from `MemAvailable`, which does not count the ZFS ARC,
  and the ARC fills nearly all RAM. A healthy TrueNAS box looks full.
- **Container limits:** setup's check reads total memory through `sysinfo`
  (`internal/hostinfo`), so a container limited to 4 GB still sees the host's memory.

Speech-to-text speed depends on AVX2, which the J4125 in many Synology models lacks. Video
depends on whether a render device exists.

## Description

**What is detected** (extending the existing `internal/hostinfo`, which already reads memory and
NVIDIA GPUs; at setup and at each start, cached for the server's life):

| Item | Source |
|---|---|
| Memory limit | cgroup v2 `memory.max` (`max` = none), else v1 `memory.limit_in_bytes` (a value near 2⁶³ = none; DSM's 4.4 kernels use v1), else `MemTotal`; the smaller of the limit and `MemTotal` |
| Memory available | `MemAvailable` + ZFS ARC reclaimable (`arcstats`: `size − c_min`, when `/proc/spl/kstat/zfs/arcstats` is readable), capped at the limit |
| CPU | online CPUs, capped by the cgroup's `cpu.max` quota (v1: `cpu.cfs_quota_us` / `cpu.cfs_period_us`), since Go's `NumCPU` ignores it; architecture; `avx2`/`avx512f` from `/proc/cpuinfo` flags |
| GPU | `/dev/dri/renderD*` (driver name from `/sys/class/drm/*/device/driver`), `/dev/nvidia*` + `nvidia-smi -L` when present |
| Data disk | rotational (59.02) |
| Kernel | version; Landlock ABI (`landlock_create_ruleset` with the version flag); user namespaces (`unshare(CLONE_NEWUSER)` in a child) |
| Platform | `dsm` (`/etc.defaults/VERSION` when mounted, or a DSM kernel string such as `+` builds with `synology`), `truenas` (`/etc/version` when mounted, or a `truenas` kernel string), `unraid` (`/etc/unraid-version` when mounted), else `docker` or `linux`. `MAKTABA_PLATFORM` overrides |

Platform detection is best-effort from inside a container. The compose files of 59.09–59.11 set
`MAKTABA_PLATFORM` explicitly, so detection only matters for hand-made installs.

**Tiers.** The rules and defaults are in the README (§6.4). The tier is computed from the memory
limit alone (Light < 6 GiB, or arm64 < 8 GiB; Standard < 16 GiB; Strong ≥ 16 GiB), and stored
as `server.tier` with how it was chosen. A GPU changes individual defaults, not the tier: a CUDA
GPU raises the speech model (59.06), and a working VA-API, QSV or NVENC device raises transcode
limits (59.05). An admin can pick
another tier in Settings → This machine, which re-applies that tier's defaults and leaves
individual overrides alone.

| Setting the tier drives | Light | Standard | Strong |
|---|---|---|---|
| Postgres memory (59.02) | 256MB | 512MB | 25% |
| Pipeline workers | 1 | 2 | today's |
| Concurrent transcodes (software) | 1, ≤ 720p | 2, ≤ 1080p | today's |
| Concurrent transcodes (hardware video) | 2 | 4 | GPU-limited |
| Speech to text (59.06) | off; `base` when turned on | `small` (`base` without AVX2) | `large-v3-turbo` (CUDA, or AVX2) |
| … with a CUDA GPU | `large-v3-turbo` if the GPU has the memory | `large-v3-turbo` | `large-v3-turbo` |
| Quiet hours (59.02) | 01:00–06:00 | 01:00–06:00 | none |
| Thumbnail sprites | quiet hours | normal | normal |

**The setup check** refuses under **3.5 GiB**, measured against the memory limit (today: under
4 GiB of total memory). A "4 GB" NAS reports about 3.6–3.8 GiB after the kernel's share, and the
epic promises 4 GB models. On a NAS platform, the refusal text says:

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
- **Given** a J4125 with 6 GB and a working render device, **then** the tier is Standard (not
  Strong), with hardware transcode limits.
- **Given** a NAS reporting 3.7 GiB, **then** setup continues on the Light tier.
- **Given** TrueNAS with 30 GB of ARC and 1 GB `MemAvailable` on a 32 GB box, **then** available
  memory shows about 30 GB, not 1 GB.
- **Given** a J4125, **then** AVX2 shows as absent and Standard picks `base` for speech.
- **Given** 3 GiB, **then** setup refuses with the NAS text on a NAS platform, and today's text
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
| T07 | Go unit | rule table | tier for (3.7G, x86), (4G, arm64), (8G, no GPU), (8G, NVIDIA), (6G, render device), (16G) | Light, Light, Standard, Standard (+ large-v3-turbo), Standard (+ hardware limits), Strong |
| T10 | Go unit | cgroup v1 limit 9223372036854771712 | detect | no limit |
| T11 | Go unit | `cpu.max` = `200000 100000` on 4 CPUs | detect | 2 cores |
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
| `machine.memory.zfs_unread` | The ZFS cache could not be read, so free memory may look lower than it is. | تعذّرت قراءة تخزين ZFS المؤقت، لذا قد تبدو الذاكرة الحرّة أقل مما هي عليه. |
| `machine.tier.moved_up` | This machine now has more memory, so Maktaba moved it to the {tier} tier. | أصبح لدى هذا الجهاز ذاكرة أكبر، لذا نقله Maktaba إلى الفئة {tier}. |
| `setup.memory.nas_refused` | This NAS has {n} GB of memory for Maktaba. Maktaba needs at least 4 GB. Run the Maktaba home on another computer and keep your media on this NAS: that computer will read it from here. | لدى خادم التخزين الشبكي هذا {n} غيغابايت من الذاكرة لـ Maktaba، ويحتاج Maktaba إلى 4 غيغابايت على الأقل. شغّل جهاز Maktaba الرئيسي على حاسوب آخر وأبقِ وسائطك على خادم التخزين هذا: سيقرؤها ذلك الحاسوب من هنا. |

## Edge cases

- **cgroup limit set to `max`:** fall back to `MemTotal`.
- **ARC stats not readable** (TrueNAS but `/proc/spl` hidden): available is `MemAvailable`, and
  Settings shows `machine.memory.zfs_unread`. The TrueNAS app (59.10) does not hide it.
- **Memory added later:** the next start recomputes the tier. If the computed tier is higher and
  the admin never picked one, defaults move up, and `machine.tier.moved_up` says so.

## Build plan

| File | Change |
|---|---|
| `cmd/maktaba-server/internal/hostinfo/` | cgroup limits, ARC, CPU quota and flags, render devices, platform, kernel facts; tier rules |
| `cmd/maktaba-server/setup_steps.go`, `setup_app_text.go` | limit-based check at 3.5 GiB, NAS text in both languages |
| `cmd/maktaba-server/internal/config/` | `server.tier`, apply defaults |
| `api/internal/handlers/systemstatus/` | `GET /api/system/machine` (admin; 59.16) |
| `web/src/i18n/{en,ar}.json`, `web/` | strings; This machine panel |
| `docs/hardware.md` | the NAS tiers |
