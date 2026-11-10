# Epic 59: Maktaba on a NAS: Synology, TrueNAS and the rest

> **Status:** spec (2026-11-10). **Platforms:**
> - Synology DSM 7.2 and 7.3, through Container Manager;
> - TrueNAS Community Edition 25.04 and newer, through its apps catalog;
> - Unraid, QNAP, UGREEN and any Linux box with Docker Compose;
> - the home's server, streaming service, pipeline and plugins, for what they need.
>
> **Replaces:** the NAS plan of Epic 25,
> [plan-25-31-nas-support.md](../25-cloud-relay/plan-25-31-nas-support.md). It targets a TrueNAS
> Helm chart TrueNAS no longer runs and an image that was never published.
>
> **Builds on:**
> - [Epic 28](../28-auto-update/README.md) (updates, the launcher's guard, the update rehearsal);
> - [Epic 31](../31-multi-instance/README.md), [Epic 35](../35-cluster-full-nodes-and-updates/README.md)
>   and [Epic 37](../37-node-replica/README.md) (the cluster, canonical share paths, full nodes);
> - [Epic 39](../39-linux-app/README.md) (the headless Linux install the image carries, the Linux
>   speech-to-text contract);
> - [Epic 50](../50-cloud-no-downtime/README.md) (per-home HTTPS names, story 50.15);
> - [Epic 51](../51-configurable-components/README.md) (components and bundles);
> - [Epic 54](../54-plugins/README.md) (plugin runners, plugind, the sandbox);
> - [Epic 58](../58-smart-tv-platforms/README.md) (the cloud code, for TVs that cannot find a home
>   behind a bridge network).
>
> **Research:** [research.md](research.md). **Wiki:**
> [epic-59-nas.md](../../../docs/wiki/epics/epic-59-nas.md).

## 1. Why

Requirement: a household that keeps its media on a NAS should be able to run its Maktaba home on
that NAS, with no other computer on all the time.

A NAS is where most self-hosted media already lives, and it is always on. Today Maktaba runs well
on a Mac, a Windows PC or a Linux PC, and treats the NAS only as storage that those computers
mount. The single Docker image could run on a NAS, but it was never made for one:
- **Fixed user:** it runs as uid 1000. Synology wants a real DSM user's uid, TrueNAS wants 568,
  Unraid 99.
- **Write access assumed:** it expects to write into every library.
- **One-size defaults:** it has a 16 GB hardware profile, Postgres sized for a cluster, and
  speech to text on by default.
- **Missing hardware path:** it has no VA-API, the only hardware video path most NAS chips give a
  container.
- **No ARM speech to text:** it cannot transcribe on ARM at all.

NAS hardware is weak and varied:
- **CPUs:** a Celeron with Quick Sync but no AVX, a Ryzen with AVX2 but no GPU, or four ARM cores.
- **Memory:** 2 to 8 GB of RAM, shared with the NAS's own system.
- **Disks:** spinning disks that should sleep.
- **Permissions:** each NAS has its own rules (Synology ACLs, TrueNAS NFSv4 ACLs), and each wants
  its own install path (a Container Manager project, a TrueNAS app, an Unraid template).

This epic makes Maktaba a good citizen on each of them.

## 2. What exists, and the gaps

Checked on `main` at 9c415c0fd (2026-11-10). Details and paths are in research.md §1.

| Area | Today |
|---|---|
| Image | `ghcr.io/hamza-labs-core/maktaba-server`, amd64 + arm64, signed, self-updating on its volume; **uid 1000 fixed**, the only uid in its `/etc/passwd` (the bundled Postgres's `initdb` needs one); no root or privileged check in the container entrypoint (only setup refuses root); one volume |
| Data and Postgres | bundled Postgres 16 + pgvector on the volume; refuses network, FAT and FUSE (Unraid's shfs) targets; `max_connections=300` on the command line (the home alone uses about 130); no tuning for RAM or spinning disks |
| Media | mounted read-write; subtitles, convert, dedup, trash and YouTube write into libraries; no check of what the container may write |
| Scans | no periodic sweep by default (`sweep_interval_sec: 0`; good for disk sleep); scans on demand and on schedule; the walker skips `@eaDir` and every dot-folder (`.zfs`, `.snapshot`, …), not `#recycle`, `#snapshot` or `lost+found` |
| Hardware | setup refuses < 4 GiB of total memory (`sysinfo`, blind to the cgroup limit), warns < 8; one profile; free memory read from `MemAvailable` (blind to ZFS ARC) |
| Video | NVENC → QSV → libx264; **no VA-API** |
| Speech to text | faster-whisper on x86_64 only; `large-v3-turbo` by default; **none on arm64** |
| Plugins | runners from one venv built **at first run from PyPI**; Landlock + seccomp sandbox, `sandbox_partial` without Landlock; plugind already treats namespaces and Landlock as optional (reported `partial`) and requires only no-new-privs, rlimits and its own seccomp filter, so third-party plugins run with less isolation **without the admin being asked** |
| Networking | host network for mDNS; announces through Avahi (`avahi-publish-service`) or its own zeroconf responder; ports 8088, 8080, 8081, 54329, moved by `ReconcilePorts` when taken; no trusted-proxy setting on the home |
| NAS packaging | Synology `INFO` and QNAP `qpkg.cfg` scaffolds; an old plan for Helm and PUID/PGID |

### Gap list

- **G1** The image cannot run as the user each NAS expects, so it fails on TrueNAS and Unraid, and
  on Synology it runs as an arbitrary uid with no grants.
- **G2** Nothing checks or explains what Maktaba may read and write, so a read-only share fails
  jobs one by one with permission errors.
- **G3** Defaults assume a 16 GB desktop. A 4 GB NAS swaps or gets killed.
- **G4** No hardware video on AMD and most Intel NAS chips (no VA-API), and no speech to text on
  ARM.
- **G5** First start needs PyPI for the venv, and a home on a NAS kernel runs third-party plugins
  with partial isolation without telling the admin.
- **G6** No install path a NAS owner would recognise: no Container Manager project, no TrueNAS
  app, no Unraid template.
- **G7** Postgres on spinning disks is slow and keeps the array awake, and nothing tells the
  household.
- **G8** A uid missing from the image's `/etc/passwd` cannot initialise the bundled Postgres.

## 3. Goals

1. **One image, every NAS.** The same signed, self-updating image runs on Synology, TrueNAS,
   Unraid, QNAP, UGREEN and plain Docker, as whichever non-root user the NAS gives it (59.01).
2. **Never root, never privileged.** No `privileged`, no Docker socket, no host PID, only the
   devices it uses (`/dev/dri`, NVIDIA).
3. **Permissions said plainly.** Setup checks every folder Maktaba needs. It shows what is
   readable and writable, and gives the exact fix in that NAS's own words. A read-only library
   works, without the features that write (59.03).
4. **Fits the box it is on.** Hardware is detected, not assumed, and each tier gets defaults that
   keep the NAS responsive: Postgres memory, worker counts, transcoding limits, the speech model,
   quiet hours (59.04).
5. **Uses the hardware there is.** VA-API and Quick Sync for video where a GPU exists (59.05);
   speech to text on every x86_64 and arm64 NAS that has the memory for it (59.06).
6. **The database where it belongs:** on local storage, an SSD when there is one, tuned when it
   is on spinning disks, and out of the way of disk sleep (59.02).
7. **Plugins work, safely.** First-party and third-party plugins run inside the container. The
   home says how isolated they are, and a weaker isolation level needs the admin's yes (59.07).
8. **Same updates, same safety.** The image's self-update, the launcher's guard and the update
   rehearsal apply on a NAS, and live alongside each catalog's own image updates (59.12).
9. **English and Arabic** for every new string, setup screen and guide (59.14).
10. **Tested in CI** on NAS-shaped containers (other uids, read-only media, 4 GB memory limits,
    arm64, no Landlock), with written device checklists and app logs in every report (59.15).

## 4. Non-goals

- **A native Synology package (SPK) of the server itself.** DSM has no systemd user manager and
  ships its own libraries per model, so the headless payload's contract does not hold there. A
  native package would be a second install to keep updated and rehearsed. Docker is what
  Synology's own guidance and every comparable project use (research.md §2.2, §5). A thin SPK
  that only installs the Container Manager project is gated (59.09, Q3).
- **TrueNAS CORE (FreeBSD).** In sustaining mode; there is no Linux userland to run the image.
- **QNAP QPKG and Unraid plugins.** Both platforms run containers; the scaffolds in
  `cloud/installers/nas/` are removed (59.11).
- **NAS models under 4 GB of RAM (3.5 GiB reported), and 32-bit ARM.** Setup says so and points to running the home
  on another computer, with the NAS as storage (as today).
- **A storage-only cluster role.** A NAS that only holds files is already served by SMB and NFS
  and the canonical share paths of story 31.03. A NAS either is the home, a full node, or storage
  (59.13).
- **Managing the NAS itself:** creating shares, users, datasets, ACLs, firewall rules or certificates.
  Maktaba checks and explains; the household's NAS admin makes the change.
- **The multi-service compose stack** (`deploy/compose/docker-compose.yml`) as a NAS path. It is
  for development; its wrong image names are fixed in 59.11 so nobody copies them.
- **Running the NAS's own Postgres** (Synology's internal one, or a TrueNAS Postgres app) as
  Maktaba's database. Maktaba keeps its bundled Postgres, with the version and extensions it
  rehearses.

## 5. Platform by platform: the decisions

| Platform | How the household installs it | Runs as | Data | GPU | Order |
|---|---|---|---|---|---|
| **Plain Docker / compose** | the published Docker compose (`cloud/installers/docker/docker-compose.yml`), filled in by maktaba.tv (59.11) | `user:` of the household's choice | a local path or named volume | `/dev/dri` or NVIDIA lines, commented | 1 (the base the others reuse) |
| **Synology DSM 7.2+** | Container Manager → Project, from Maktaba's compose (59.09) | a DSM user made for Maktaba, granted the shares | `/volume1/docker/maktaba` or an SSD volume | `/dev/dri` + render gid on J4125 models | 2 |
| **TrueNAS 25.04+** | the **community** app in `truenas/apps` (59.10); a custom-app YAML until it lands | `apps` (568) | a dataset with the Apps preset, on an SSD pool when there is one | Intel/AMD "allocate GPU", NVIDIA checkbox | 2 (with Synology) |
| **Unraid** | a Community Applications template (59.11) | 99:100 | `/mnt/user/appdata/maktaba` (cache pool) | `/dev/dri` | 3 |
| **QNAP, UGREEN, ASUSTOR, others** | the plain compose file in their container app (59.11) | a user the household creates | local volume | as plain Docker | 3 |

**Why Docker everywhere:**
- Every NAS above runs it.
- The image already carries the headless install and updates itself on its volume, with a
  rehearsed rollback.
- One artifact means one thing to sign, test and rehearse.

**Why a catalog app on TrueNAS but only a compose Project on Synology:**
- TrueNAS's catalog is an open repository with a community train that takes third-party apps
  (Immich is one).
- Synology's Package Center has no open path for third parties, and community packages show an
  "unverified" warning on DSM 7 (research.md §2.1).
- A Container Manager project is Synology's own way to run a compose file, and it needs nothing
  signed by Synology.

## 6. Design

### 6.1 The shape of it

```
NAS (Synology / TrueNAS / Unraid / …)
├── container maktaba-server   (the one image, as the NAS's user, host network)
│   ├── launcher → server → postgres · api · streaming · pipeline · plugin runners · plugind
│   ├── /var/lib/maktaba        → data: local disk, SSD when there is one   (59.02)
│   ├── /volume1/Movies …       → media: mounted at the same path as on the host  (59.03, 59.13)
│   └── /dev/dri (optional)     → VA-API / Quick Sync                        (59.05)
└── the NAS's own services: DSM/TrueNAS UI, its nginx, its Postgres, its mDNS responder
```

### 6.2 Users and permissions (59.01, 59.03)

- **The image runs as whatever uid:gid it is started with** (`user:` in compose, the Run As
  setting in TrueNAS, `--user` in Unraid). It has no root step, no PUID/PGID entrypoint and no
  chown at start. `initdb` and `postgres` need the uid to have a name, so the entrypoint gives
  it one with `nss_wrapper` (what the official Postgres image does) when `/etc/passwd` has none.
  On a volume it cannot write, it stops with a message naming the path, the uid and the fix.
  - Rejected: a root entrypoint that drops to PUID/PGID. It runs root code on every start, and
    TrueNAS and Kubernetes-style platforms set the user themselves.
- **Media folders are checked at setup, on every library save, on Check access and after a
  failed write**, not at every start (a probe would wake sleeping disks). A probe file is written
  and removed in `<root>/.maktaba/`. Each root is shown as Read and write, Read only or No access.
- **A read-only library should be mounted `:ro`.** Inside a container without Landlock, only the
  mount guarantees Maktaba cannot write; the check then reports it, and the guides say so.
  - Each state has a fix in the NAS's terms: on DSM, "give the user `maktaba` Read/Write on the
    shared folder Movies"; on TrueNAS, "add an ACL entry for user 568 with Modify on the dataset
    tank/media".
- **A read-only library is a supported state, not an error.** Features that write declare it in
  their manifests (`files.write = ["library.*"]`), and each takes the path below on a read-only
  library.
- **The cluster's folder and the model copy** live under `<share>/maktaba/` today. A NAS home
  whose shares are all read-only names one writable folder for them at setup (59.13); without
  one, nodes download models themselves and joining says why.

| Feature | On a read-only library |
|---|---|
| Subtitles (`library.subtitles`) | stored in the database (a new `subtitle_files` table), so node replicas and a node taking over have them; streaming serves them like side-car files |
| Convert, dedup, YouTube downloads, repair | refused for that library, with "This library is read-only for Maktaba" and the fix |
| Trash | delete is unavailable; nothing is removed |
| Everything else | unchanged (scans, thumbnails, transcripts, streaming read only) |

### 6.3 Data and the database (59.02)

- The data volume holds config, the bundled Postgres, caches, thumbnails, models and logs.
- It must be on local storage, which `target_not_local` already enforces, and it should be on an
  SSD.
- Setup reads the device under the data dir (`/sys/block/*/queue/rotational`). On spinning disks
  it shows "The database is on spinning disks: Maktaba will work, but browsing and search are
  faster on an SSD, and the disks will not sleep while Maktaba runs." It then tunes Postgres:
  `random_page_cost=4`, `effective_io_concurrency=2`, and a longer checkpoint timeout.
- **Postgres memory is sized by the hardware tier** (6.4), not fixed: `shared_buffers`,
  `work_mem` and `maintenance_work_mem` per tier, passed on the command line with the existing
  server arguments. `max_connections` stays 300: the home alone uses about 130, and lowering it
  would need every pool resized and measured first. A household override key
  (`[postgres] settings`) wins over the tier.
- **Disk sleep:**
  - Heavy work runs in **quiet hours** (default 01:00–06:00 on Light and Standard tiers): the
    first scan of a new library, transcription backlogs, convert.
  - Media disks are touched only by scans, playback and those jobs. The default stays no
    periodic sweep (`sweep_interval_sec: 0`); a household that sets one is told it keeps disks
    awake.

### 6.4 Hardware tiers (59.04)

Detected at setup and at every start, and shown in Settings → This machine:
- **Memory:** the cgroup memory limit (v2 `memory.max`, v1 `memory.limit_in_bytes`, where v1's
  "no limit" is a huge number), `MemTotal`, and `MemAvailable` plus reclaimable ZFS ARC
  (`/proc/spl/kstat/zfs/arcstats`: `size − c_min`).
- **CPU:** cores (capped by the cgroup's `cpu.max` quota), architecture, AVX2 and AVX-512 flags.
- **Devices:** `/dev/dri/renderD*` with its driver, and NVIDIA devices.
- **Disk:** whether the data disk is rotational.
- **Kernel:** Landlock and user-namespace support.

| Tier | Rule | Defaults |
|---|---|---|
| **Light** | memory < 6 GiB, or arm64 with < 8 GiB | Postgres 256 MB; 1 pipeline worker; speech to text **off** (can be turned on with `base`, quiet hours only); 1 software transcode at ≤ 720p; thumbnails in quiet hours |
| **Standard** | 6 GiB ≤ memory < 16 GiB | Postgres 512 MB; 2 workers; speech to text `small` int8 (or `base` without AVX2), quiet hours; 2 transcodes |
| **Strong** | memory ≥ 16 GiB | today's defaults; `large-v3-turbo` with CUDA or AVX2; transcodes limited by the GPU |

- **Memory** here is the container's limit (the cgroup's, else `MemTotal`), not what is free.
- **The tier is set by memory alone.** A CUDA GPU raises the speech model to `large-v3-turbo`
  on any tier with the memory for it; a VA-API or Quick Sync device raises transcode limits
  only. Neither changes quiet hours or Postgres.

- **Under 3.5 GiB:** setup refuses, with the NAS-specific message of §4. The floor moves from
  4 GiB to 3.5 GiB because a "4 GB" NAS reports about 3.6–3.8 GiB after the kernel's share.
- **Tiers are defaults, not limits.** Every setting stays changeable in Settings.
- **Memory limit in compose:** the image's compose files set `mem_limit` from the tier the
  household picks on maktaba.tv's install page. Maktaba reads its cgroup limit, so it never plans
  for memory the container cannot have.

### 6.5 Video and speech (59.05, 59.06)

- **Video:**
  - Streaming gains **VA-API** (`h264_vaapi`, `hevc_vaapi` where the driver lists it), tried
    after NVENC and QSV, kept only after its test encode like the others.
  - The image adds `intel-media-va-driver-non-free` (iHD, from Debian's non-free component),
    `i965-va-driver` and `mesa-va-drivers`. Debian 12's ffmpeg uses libmfx for QSV, which does
    not cover newer Intel GPUs such as the N100's, so on those chips VA-API is the hardware
    path.
  - The compose files pass `/dev/dri` and the render group (`group_add`).
  - On a Ryzen or ARM NAS, Maktaba says plainly that transcoding is software-only and that
    direct play matters (Epic 58's device-reported capabilities).
- **Speech:**
  - The `stt` extra installs ctranslate2 and faster-whisper on **aarch64** as well. Torch, used
    only by the second listen, stays x86_64 + CUDA.
  - At setup Maktaba transcribes a 30-second bundled clip with the tier's model and records the
    real-time factor. If the backlog would take more than 30 days at that speed, it proposes a
    smaller model or turning speech off, and says why.

### 6.6 Plugins in a container (59.07)

- **First start needs no internet beyond pulling the image.** The Linux payload carries a
  wheelhouse for its architecture, and `venv_bootstrap` installs from it with
  `--no-index --find-links`. A self-updated runtime carries its own wheelhouse, so updates do not
  need PyPI either.
- **Isolation levels:** the container is the outer boundary, and inside it plugind and the
  runners keep their layers where the kernel allows. The home reports one of these:

| Level | When | First-party runners | Third-party plugins |
|---|---|---|---|
| **Full** | Landlock + seccomp + user namespaces | as on Linux today | allowed |
| **Container** | no Landlock (DSM's kernels are likely 4.4 and 5.10) or no user namespaces (Docker's default seccomp) | run, reported `sandbox_partial` | **held until an admin turns on "Allow plugins with container-only isolation"**, with what that means in both languages |
| **None** | the container itself is privileged or has the Docker socket | refused to start: Maktaba will not run privileged | refused |

- plugind's layers stay as they are today: no-new-privs, rlimits, the Wasm sandbox (no WASI
  sockets, no filesystem) and plugind's own seccomp filter are required, and namespaces and
  Landlock are optional, reported as `partial`. The new work is policy: the server holds
  third-party plugins while plugind reports `partial`, until the admin's yes.
- **Network grants** (YouTube, classify) work through the container's network. Grants are
  unchanged.

### 6.7 Networking (59.08)

- **Host network by default** (mDNS and SSDP need it). Maktaba's ports (8088, 8080, 8081, 54329)
  do not clash with DSM (80, 443, 5000, 5001, 5432) or TrueNAS (80, 443).
- The bundled Postgres listens on loopback until a node joins. The node's pairing opens it with
  TLS, as today.
- **mDNS next to the NAS's own responder:**
  - By default Maktaba uses its existing zeroconf responder under its own name
    (`<host>-maktaba`), bound to 5353 with `SO_REUSEADDR`. On Linux every such socket receives
    the multicast queries; `SO_REUSEPORT` would not help, since it shares a port only between
    sockets of the same uid.
  - Mounting the host's system D-Bus socket lets Maktaba publish through the host's Avahi (its
    existing `avahi-publish-service` path). It is documented as an option, not a default: the
    socket reaches every system service D-Bus policy allows, and `:ro` does not stop
    `connect()`.
  - Both paths are tested (59.15).
- **Bridge network is supported.** It has no mDNS: apps reach the home by its address, its
  per-home HTTPS name (50.15) or the cloud code (58.03). Setup says which of these will work.
- **HTTPS:** Maktaba's own per-home name is the default. A DSM or TrueNAS reverse proxy in front
  is documented (headers, WebSocket, upload size) but not required. The home gains a
  `[server] trusted_proxies` setting, so forwarded client addresses are believed only from the
  proxy.

### 6.8 Updates (59.12)

- The image's self-update stays the update path. It honours the household's update window
  (Epic 28), the launcher's guard and the pre-update dump.
- Catalogs move the image tag too (TrueNAS app updates, Unraid, Watchtower). A newer image goes
  on the volume at its next start, and an older image never replaces a newer runtime, so the two
  can disagree only in the safe direction.
- Guides and templates use `:latest`, which already means the highest stable release. Release
  already rewrites the published Docker compose to a pinned tag, and the NAS files follow that
  rule.
- The TrueNAS app pins a version tag and moves on each release (as every catalog app does). It
  does not set `MAKTABA_UPDATE_DISABLE`, so homes get security fixes in their window, before the
  catalog's review.
- The update rehearsal today runs Go end-to-end tests of the Mac app's layout, not the image.
  This epic adds a **container rehearsal** for the image with NAS shapes: uid 568, read-only
  media, a 4 GB memory limit, arm64, restarts mid-update.

### 6.9 A NAS in a cluster (59.13)

- **NAS as home:** the default this epic builds.
  - Computers with a GPU can join as full nodes (Epic 35), taking transcription and transcodes.
  - **Media is mounted in the container at the same path as on the host** (`/volume1/Movies`
    stays `/volume1/Movies`; `/mnt/tank/media` stays `/mnt/tank/media`). A new resolver rule
    maps a local root on the home itself, such as Synology's `/volume1/<share>`, to
    `smb://<nas>/<share>` (today the resolver maps only NFS mount exports), and a Mac node's
    `/Volumes/Movies` then matches without a path map.
  - For mounts at other paths, setup asks each one's share name.
- **NAS as full node** of a home elsewhere: allowed on Standard and Strong tiers. On Light, setup
  says a Light NAS should be the home or storage, not a node.
- **NAS as storage:** today's setup, unchanged.

### 6.10 Strings and guides (59.14)

- Every new setup step, check, message and Settings line is in English and Arabic.
- The install guides for each NAS live on **maktaba.tv** (what households use), with Arabic
  pages. Developer notes go to the wiki on maktaba.dev.
- Screenshots are dark, in both languages.

## 7. Phases

| Phase | Stories | Leaves the household with |
|---|---|---|
| 1. A good container | 59.01, 59.02, 59.03, 59.04, 59.15 | the image runs on any NAS as any user, explains permissions, fits the hardware, with CI on NAS-shaped containers |
| 2. Hardware and plugins | 59.05, 59.06, 59.07 | hardware video on Intel and AMD, speech on arm64, plugins with clear isolation |
| 3. Each NAS | 59.08, 59.09, 59.10, 59.11 | install paths a NAS owner recognises, networking that works next to the NAS's services |
| 4. Everything around it | 59.12, 59.13, 59.14, 59.16 | updates rehearsed on a NAS, clusters with a NAS home, guides in both languages, role tests |

## 8. Stories

| # | Story | Area |
|---|---|---|
| 59.01 | [One image for every NAS: any user, never root, first start offline](story-59-01-image-any-user.md) | image, release |
| 59.02 | [The data volume and the database on a NAS: local, SSD first, tuned for spinning disks, quiet hours](story-59-02-data-and-database.md) | server, Postgres |
| 59.03 | [Media permissions: check, explain, and work with read-only libraries](story-59-03-media-permissions.md) | server, pipeline, plugins, web |
| 59.04 | [Hardware detection and tiers: memory (with ZFS ARC), CPU features, GPUs, disks](story-59-04-hardware-tiers.md) | server, pipeline, streaming |
| 59.05 | [Hardware video on NAS chips: VA-API, Quick Sync and the render device](story-59-05-vaapi.md) | streaming, image |
| 59.06 | [Speech to text on NAS CPUs: arm64, the model by tier, a measured speed](story-59-06-stt-nas.md) | pipeline, image |
| 59.07 | [Plugins in a container: isolation levels, plugind's layers, the admin's yes](story-59-07-plugins-container.md) | plugins, plugind, web |
| 59.08 | [Networking on a NAS: host or bridge, mDNS beside the NAS's responder, HTTPS, reverse proxies](story-59-08-networking.md) | server, image |
| 59.09 | [Synology: a Container Manager project, DSM's users and shares, the J4125's GPU](story-59-09-synology.md) | Synology |
| 59.10 | [TrueNAS: a community app, datasets and ACLs, GPUs, the ZFS cache](story-59-10-truenas.md) | TrueNAS |
| 59.11 | [Unraid, QNAP, UGREEN and plain compose; the old scaffolds go](story-59-11-other-nas.md) | Unraid, QNAP, Docker |
| 59.12 | [Updates on a NAS: self-update beside catalog updates, the NAS leg of the rehearsal](story-59-12-updates.md) | release, CI |
| 59.13 | [A NAS in a cluster: home, full node or storage; one path for each share](story-59-13-cluster.md) | server, pipeline |
| 59.14 | [English and Arabic: setup screens, messages, and the install guides on maktaba.tv](story-59-14-strings-guides.md) | web, guides, wiki |
| 59.15 | [CI on NAS-shaped containers, device checklists, logs kept](story-59-15-ci.md) | CI |
| 59.16 | [OpenAPI and role tests for the new admin routes](story-59-16-openapi-roles.md) | API |

## 9. Test and rehearsal plan

Everything automated runs in CI on jobs scoped to their code (CI runs only related jobs), and
nothing runs against the live home. Story 59.15 has the full table. In short:

| Shape | Automated in CI | Real devices only (written checklist before the platform's first release and after each DSM/TrueNAS major) |
|---|---|---|
| Any uid | the image started as 1000, 568, 99:100 and 1026:100 (a DSM user) on amd64 and arm64 | — |
| Permissions | read-only media mount, no-access mount, a mount owned by another uid | DSM ACL grants, TrueNAS Apps preset |
| Memory | `--memory=4g` and `6g` with the full first-run flow; tier chosen and respected | DSM with 4 GB, TrueNAS with ARC full |
| No Landlock | seccomp profiles that answer `landlock_*` with `ENOSYS`, and with `EPERM`; Container level reported | a DSM model's real kernel |
| VA-API | the code path with a fake device list; test-encode fallback | J4125 (DSM), N100 (UGREEN or TrueNAS) |
| arm64 speech | a 30-second clip transcribed on GitHub's arm64 runner | DS223 or DS423 |
| TrueNAS app | `truenas/apps`' own CI for the app, run in our CI | TrueNAS 25.04 and 25.10 install |
| Updates | the NAS leg of `update-rehearsal.yml` | — |

**Logs in every run:** the container's log, the server's and each worker's log, and the
setup report are kept in the CI report. Device checklists attach `docker logs` and the setup
report.

## 10. Acceptance (the epic)

- A DS425+ (J4125, 6 GB) installs Maktaba from a Container Manager project. It runs as a DSM user
  with Read only on Movies and Read/Write on Recordings, shows both states in setup, and plays a
  1080p HEVC file transcoded by Quick Sync.
- A DS923+ (Ryzen R1600, 4 GB) runs the Light tier. Speech to text is off with the reason shown,
  and the NAS stays responsive during the first scan.
- A TrueNAS 25.10 box installs the community app as uid 568 with its data on an SSD pool. It
  reports its memory correctly with the ZFS cache full, and transcribes with `small`.
- A DS223 (arm64, 2 GB) is refused with the NAS message. A UGREEN DXP4800 Plus (N100, 8 GB,
  Standard) transcribes with `small` and transcodes with VA-API.
- A Mac joins a NAS home as a full node and takes transcription jobs on the same files with no
  path map.
- A third-party plugin on a DSM home stays off until the admin allows container-only isolation.
- An update on each NAS installs in the household's window, and a failed one rolls back under the
  guard.
- No container runs as root or privileged. Every string exists in English and Arabic, and every
  CI run keeps the app's log.

## 11. Decisions (2026-11-10)

1. **Docker on every NAS, one image.** Rejected: a native Synology SPK of the server (no systemd
   user manager, per-model libraries, a second install to rehearse), a QNAP QPKG, and the
   multi-service compose stack (it does not self-update or transcribe).
2. **The image runs as the uid it is given; no root entrypoint, no PUID/PGID; `nss_wrapper`
   names a uid `/etc/passwd` lacks.** Rejected: a root entrypoint that chowns and drops
   privileges (root code on every start, and catalogs set the user themselves), and a
   world-writable `/etc/passwd`.
3. **Read-only libraries are supported.** Writing features degrade per library, with the reason
   shown. Rejected: requiring write access to every library, which many households would refuse
   for their originals.
4. **Subtitles on a read-only library are kept in the database**, so replicas have them.
   Rejected: skipping them, and the data dir (a node taking over would lose them).
5. **Hardware tiers set defaults; nothing is hard-limited.** Rejected: one profile for all, and
   per-model tables (wrong for expanded RAM and for new models). Detection decides.
6. **ZFS ARC counts as available memory, and tiers follow the memory limit alone.** Rejected:
   tiers raised by any GPU (a 6 GB J4125 would get the heaviest defaults), and `MemAvailable`
   alone, which reports a healthy
   TrueNAS box as full.
7. **VA-API joins NVENC and QSV** behind the same test encode. Rejected: QSV only (AMD and many
   Intel container setups expose only VA-API).
8. **Speech on arm64 with CTranslate2's CPU kernels**, the model chosen by measured speed.
   Rejected: no speech on ARM, and whisper.cpp as a second engine (a second engine to keep in
   step with the transcript pipeline).
9. **The payload carries a wheelhouse, installed with `--no-index`.** Rejected: building the venv
   from PyPI at first start (fails without outbound internet, slow on NAS CPUs), and copying a
   built venv (venvs are not relocatable).
10. **Isolation levels, with third-party plugins held on the Container level until the admin
    says yes.** Rejected: refusing third-party plugins on every NAS without Landlock (most DSM
    models), and running them with less isolation without asking (today's behaviour).
11. **Host network by default, bridge supported; Maktaba's own responder, the host's D-Bus only
    by choice.** Rejected: bridge only (no mDNS, so TVs and apps lose discovery), and mounting
    the system D-Bus socket by default.
12. **The TrueNAS community app, the Synology compose project, the Unraid template.** Rejected:
    SynoCommunity first (its "unverified" warning, and a native package to maintain) and a
    TrueNAS custom-app YAML only (no catalog updates).
13. **Media mounted at the host's own path.** Rejected: one `/media` mount for everything, which
    breaks share identity for cluster nodes and forces a path map.
14. **Quiet hours for heavy work on Light and Standard tiers.** Rejected: running everything as
    soon as it is queued, which makes a small NAS unusable for its own file serving.
15. **The self-update stays on beside catalog updates.** Rejected: `MAKTABA_UPDATE_DISABLE` on
    catalog installs, which would delay security fixes by each catalog's review.

## 12. Questions only the owner can answer

None blocks the epic. Each has a default the stories use.

| # | Question | Default used |
|---|---|---|
| Q1 | Is the platform order right? | plain compose, then Synology and TrueNAS together, then Unraid and the rest (§5) |
| Q2 | Submit the app to TrueNAS's community train under the project's GitHub account? | yes, once 59.10's checklist passes on 25.04 and 25.10 |
| Q3 | Publish a thin Synology package (SPK) that only installs the Container Manager project, through SynoCommunity? | no for now; revisit if households ask for it in Package Center (59.09) |
| Q4 | Smallest NAS supported | a "4 GB" NAS (3.5 GiB reported), x86_64 or arm64; below that, setup refuses with the NAS-as-storage advice |
| Q5 | Quiet hours default | 01:00–06:00 on Light and Standard, editable |
| Q6 | Buy test devices (a J4125 Synology, a Ryzen Synology, an N100 box for TrueNAS) for the device checklists? | yes, one each, used by hand before each platform's release; no self-hosted runner (59.15) |
| Q7 | Turn speech to text off by default on Light? | yes, with a one-tap turn-on that shows the measured speed |
| Q8 | Allow a third-party plugin on the Container isolation level at all? | yes, after the admin's explicit yes per home (59.07) |
