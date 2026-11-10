# Epic 59 research: Maktaba on a NAS

Checked on `main` at 9c415c0fd (2026-11-10). Web sources were read on 2026-11-10. Anything
not confirmed from a primary source is marked **(unverified)**, and the story that depends on it
names the check that settles it.

## 1. What Maktaba runs today

### 1.1 Processes

One `maktaba-server serve`, started by `maktaba-launcher`, supervises the rest
(`specs/epics/39-linux-app/README.md`, `deploy/packaging/nfpm.yaml:58-70`):
- `postgres` (bundled), `api`, `streaming`;
- `pipeline` and N transcription workers, plus an index worker;
- `plugin-runner/<group>` on demand: groups `light`, `ml-N`, `index`, `net`, `service`, `fp`,
  `files`;
- `maktaba-plugind` (third-party Wasm plugins), only while a third-party plugin is enabled;
- `ollama`, only on the Mac. On Linux, LLM stages read `[llm] url` and point at an Ollama the
  household runs (`docs/hardware.md:373`).

### 1.2 The ways it is installed on Linux

| Install | Runs as | Postgres | ffmpeg | Self-updates | Notes |
|---|---|---|---|---|---|
| Headless payload (Epic 39, `headless.md`) | a systemd **user** service with linger | bundled 16.15 + pgvector 0.8.7 | the distribution's | yes | needs glibc ≥ 2.35 and a systemd user manager; refuses root |
| `.deb`/`.rpm` (`nfpm.yaml`) | user `maktaba`, `/var/lib/maktaba` | SQLite or your own | your own | no | unit has `ProtectSystem=strict`, `PrivateDevices=true` (blocks `/dev/dri` and writes next to media) |
| **Single image** `ghcr.io/hamza-labs-core/maktaba-server` (`deploy/docker/server/Dockerfile`) | **uid/gid 1000, fixed** | bundled, on the volume | Debian 12's (5.1) | **yes**: the runtime on the volume, the launcher's 3-minute guard | amd64 + arm64, cosign-signed; one volume `/var/lib/maktaba`; ports 8088, 8080, 8081, 54329 |
| Multi-service compose (`deploy/compose/docker-compose.yml`) | per service | `pgvector/pgvector:0.8.7-pg16` | per image | no | image names `ghcr.io/maktaba/<svc>` are **not** what release publishes (`maktaba-{api,…}`); the pipeline image has no `stt` extra, so no transcription |

The single image is the bootstrap of the headless install: its entrypoint
`maktaba-server container` puts its payload on the volume when the volume has none or an older
one, then runs the launcher. A newer image pulled later goes on the volume at its first start;
an older one never does (`docs/updates.md:305-326`). Its own compose
(`cloud/installers/docker/docker-compose.yml`) uses `network_mode: host` (mDNS) and mounts
`${MAKTABA_MEDIA}:/media` read-write. NVIDIA passthrough is there, commented out.

### 1.3 Config that matters on a NAS (`cmd/maktaba-server/internal/config/config.go`)

- **`[server]`**: `listen` (`0.0.0.0:8080`), `web_listen` (`:8088`), `stream_addr` (`:8081`),
  `admin_addr` (`:9100`), `public_url`, `data_dir`, `role` (auto/home/node), `mdns` (on/off).
- **`[media]`**: `roots`, `path_map` (`"/Volumes/X/=/mnt/x/"` pairs separated by `;`), `ffmpeg`,
  `ffprobe`, `transcode_min_free_gib`.
- **`[postgres]`**: `enabled`, `port` (54329), `listen` (lan/loopback), `tls`.
- **Environment:** `MAKTABA_CONTAINER=docker`, `MAKTABA_UPDATE_DISABLE`, `MAKTABA_PATH_MAP`,
  `MAKTABA_HWACCEL_PREFER`, `MAKTABA_STT_*`, `MAKTABA_OLLAMA_URL`, `MAKTABA_CLUSTER_IP`.
- **Bundled Postgres:** `max_connections=300`, `wal_level=logical`, 40 replication slots
  (`internal/pgbundle/pgbundle.go:220-252`). Nothing is tuned for memory or for spinning disks.

### 1.4 Plugins (Epic 54)

- **Third-party plugins** are WebAssembly in `maktaba-plugind` (Wasmtime). WASI gets no sockets
  and no filesystem. On Linux plugind adds no-new-privs, user, network and IPC namespaces where
  allowed, Landlock, and a seccomp filter allowing only `AF_UNIX`. A **required** layer that
  fails exits with 78 (`plugind/README.md`).
- **First-party plugins** are trusted Python, served by
  `python -m maktaba_plugin_runner --group <g>` from the one pipeline venv. There is no
  per-plugin venv. That venv is built **on first run** with `python -m venv` and
  `pip --require-hashes`, which needs PyPI or the home's wheelhouse (`venv_bootstrap.py`). Plugin
  wheels ship in the payload; ML wheels come from the `runtime.ml` pack.
- **Path map:** each `run` frame carries the job's `path_map`, passed to
  `RunnerContext(path_map=…)` (`maktaba_plugin_runner/server.py:333`). Since #1062 every
  first-party stage opens files through `ctx.local_path`.
- **Runner sandbox (Linux):** Landlock for files and exec; seccomp blocks `AF_INET/6` unless the
  group has `net`; `gpu` groups get `/dev/nvidia*` and `/dev/dri`. Without Landlock a runner runs
  as `sandbox_partial` (`maktaba_plugin_runner/sandbox.py`).

What the first-party plugins touch (their manifests):

| Plugin | Runs | Writes | Network |
|---|---|---|---|
| transcription | ffmpeg (`gpu=preferred`) | models, cache | – |
| thumbnails | ffmpeg | `data.thumbnails` | – |
| subtitles | – | **`library.subtitles`** (beside the video) | – |
| convert, dedup | ffmpeg, ffprobe | **the library, its trash** | – |
| youtube, repair | yt-dlp, ffmpeg | the library | youtube.com, googlevideo, googleapis |
| classify | – | – | ar.wikipedia.org |

### 1.5 Heavy dependencies

- **ffmpeg** ≥ 4.4 (`speechnorm`), with libx264, drawtext and lavfi
  (`internal/ffmpeg/check.go:20-23`). Debian 12's 5.1 qualifies.
- **Speech to text:** MLX on Apple Silicon; **faster-whisper 1.2.1 on CTranslate2** elsewhere,
  model `large-v3-turbo` (about 1.6 GB), CUDA (driver ≥ 525) or CPU int8
  (`contract-stt-linux.md`). The `stt` extra installs ctranslate2, faster-whisper and torch
  **only on Linux x86_64** (`pipeline/pyproject.toml:35-70`). **An arm64 NAS cannot transcribe
  today.**
- **Video encoding in streaming:** NVENC, then QSV, then libx264, each kept only after a test
  encode (`streaming/internal/ffmpeg/hwaccel.go:17-38`). **There is no VA-API path**, which is
  what AMD and most Intel NAS chips offer to a container.

### 1.6 Files

- **Change detection is polling:** each library's `settings.sweep_interval_sec` enqueues a
  `scan` (`engine/reconcile.py:314-345`). A watchdog-based watcher exists but nothing uses it.
  This is right for a NAS (see §2.4).
- **The walker skips** `@eaDir` and `.maktaba` (`scanner/walker.py:58-62`). It does **not** skip
  `#recycle`, `#snapshot`, `.zfs` or `.snapshot`.
- **Writes into the library:** subtitles beside the video; Epic 47 convert and dedup; the trash
  at `<root>/.maktaba/trash/` by atomic rename (a move across devices is refused), purged after
  30 days (`library_mgmt/trash.py`); the cluster record and a models copy under
  `<share>/maktaba/`.
- **The data dir must be local:** network, FAT and exFAT are refused as `target_not_local`
  (`internal/storage/disk.go:80-95`).

### 1.7 Cluster (Epics 31, 35, 37)

- One **home** holds Postgres and the queue. Other machines pair and become **full nodes** with a
  logical-replication copy that can take over (Epic 37).
- The specs treat a NAS as **storage**: every machine reads media from it through its own mounts,
  matched by the canonical `smb://server/share` resolver (story 31.03). The resolver already knows
  Synology's `/volume1/<share>` (`pipeline/src/maktaba_pipeline/sharepath/`).
- Discovery: mDNS `_maktaba._tcp` through Avahi, systemd-resolved or the in-process responder
  (`internal/mdns/announce.go`); `--home URL` skips it. The TV front door also uses SSDP.

### 1.8 Updates

The headless and Docker installs update the runtime on the volume: Sigstore and checksum checks,
a new runtime, `current` moved under the launcher's guard (confirmed within 3 minutes or switched
back), pre-update dumps. `update-rehearsal.yml` gates every tag. `MAKTABA_UPDATE_DISABLE=1` is
for households whose image tool (Watchtower) manages updates.

### 1.9 Resources

Setup refuses under 4 GiB of RAM, warns under 8 GiB, needs 20 GiB of disk
(`setup_steps.go:92-94`). The Full bundle wants 16 GiB (`setup_bundle.go:28`). Linux tiers in
`docs/hardware.md:250-275` start at 16 GB. Torch's CUDA libraries add about 3 GB to every x86_64
venv.

### 1.10 What already mentions a NAS

- `cloud/installers/nas/synology/INFO` (v0.1.0) and `cloud/installers/nas/qnap/qpkg.cfg`:
  scaffolds, never built.
- `specs/epics/25-cloud-relay/plan-25-31-nas-support.md`: an older plan (Synology SPK, QNAP, a
  TrueNAS **Helm** chart, Unraid XML, PUID/PGID per vendor). It names an image,
  `ghcr.io/hamza-labs-core/maktaba`, that does not exist, and Helm is gone from TrueNAS (§3.1).
  **This epic replaces that plan.**
- `specs/architecture.md:2973` lists the same targets.
- Setup has a "NAS path map" step and a "NAS copy" models source (`setup_steps.go:403-420`,
  `setup_models.go`).

## 2. Synology DSM 7.2 and 7.3

### 2.1 Native packages (SPK)

- **Privilege model** ([developer guide](https://help.synology.com/developer-guide/privilege/privilege_config.html)):
  `conf/privilege` sets `run-as` to `package` or `root`. Running as `system` was banned in DSM
  7.0 ([breaking changes](https://help.synology.cn/developer-guide/breaking_changes.html)). A
  package user is a hidden "system internal user" that the household grants access to each
  shared folder. Plex's package (`PlexMediaServer`) works this way
  ([Plex forum](https://forums.plex.tv/t/dsm-7-plex-media-server-release-info-migration-instructions/653717)).
- **Resource workers** include **Docker** and **Docker Project**, so an SPK can be a thin wrapper
  around a compose project.
- **Distribution:** on DSM 7, Package Center trusts only Synology-signed packages. Community
  packages always show a "not verified" warning
  ([SynoCommunity](https://docs.synocommunity.com/developer-guide/publishing/package-server/)).
  SynoCommunity is the established channel. **(unverified)** No open programme was found for
  third-party packages in Synology's own Package Center. Plex ships its `.spk` as a manual
  install.

### 2.2 Container Manager

- Docker engine **24.0.2** (build 24.0.2-1542, 2025-06-10) with `docker compose` and compose
  "Projects". DSM 7.3 (2025-10-08) did not move it
  ([release notes](https://www.synology.com/en-uk/releaseNote/ContainerManager)).
- ARMv8 models (DS223, DS423, DS124, DS420j, DS223j…) gained Container Manager with DSM 7.2.
- **Jellyfin's only official Synology method is Docker in Container Manager**, with at least
  4 GB of RAM ([Jellyfin](https://jellyfin.org/docs/general/installation/advanced/synology)).

### 2.3 CPUs, RAM and video

| CPU | Models (2022–2025) | iGPU / `/dev/dri` | AVX2 |
|---|---|---|---|
| AMD Ryzen R1600 | DS723+, DS923+, DS1522+ | **none** | yes |
| AMD Ryzen V1500B | DS925+, DS1525+, DS1825+ | **none** | yes |
| Intel Celeron J4125 | DS224+, DS423+, DS720+, DS920+, DS225+, DS425+ | UHD 600, Quick Sync, `renderD128` | **no** (no AVX at all) |
| Realtek RTD1619B (Cortex-A55) | DS223, DS423, DS124, DS223j | none usable by a container | n/a |

- The model-to-CPU table is from knowledge, checked against
  [Plex forum threads](https://forums.plex.tv/t/synology-ds925-vs-ds425/925447). **(unverified)**
  per model: story 59.04 detects, it does not trust a table.
- Plus models ship with 2–4 GB, expandable. j models ship with 1–2 GB and are not expandable
  **(unverified)**.
- DSM 7.2.2 dropped HEVC from Advanced Media Extensions and removed Video Station
  ([Synology](https://www.synology.com/dsm/7.2/software_spec/advanced_media_extensions)). That
  affects Synology's own apps. Containers carry their own ffmpeg and use `/dev/dri`. **(unverified)**
  whether the J4125's HEVC decoding still works in a container on 7.2.2+.
- Jellyfin users pass `/dev/dri` and the render group's gid
  ([guide](https://drfrankenstein.co.uk/jellyfin-in-docker-on-a-synology-nas-hardware-transcoding/)).

### 2.4 Files

- Shares live at `/volume1/<share>` on Btrfs (or ext4), with Synology's Windows-style ACLs.
- Containers usually run as a real DSM user's uid and gid (`id` over SSH). Running as 0 runs as
  root over every share ([linuxserver.io](https://docs.linuxserver.io/images/docker-jellyfin/)).
- `@eaDir` holds DSM's thumbnails, `#recycle` the Recycle Bin, `#snapshot` snapshots. A scanner
  must skip all three.
- **The inotify watch limit is 8192 on DSM**, raised only on the host. Roon exhausted it and broke
  the NAS's IPv6 ([Roon](https://community.roonlabs.com/t/synology-dsm-roon-server-ipv6-dhcpv6-broken-because-roon-exhausted-inotify-watches/322285)).
  Maktaba's polling is the right design here.

### 2.5 Ports and services

- DSM uses 5000/5001 for its UI and 80/443 for its nginx. Its reverse proxy is under
  Control Panel → Login Portal → Advanced → Reverse Proxy, with Let's Encrypt.
- DSM runs its own PostgreSQL, so **host port 5432 is often taken** (community reports). Maktaba
  uses 54329, which does not clash.
- **(unverified)** DSM's own mDNS responder (Bonjour) is on by default. A host-network container
  must coexist with it on 5353.
- Immich's Synology guide pins a compose subnet so DSM's firewall rule keeps matching
  ([Immich](https://docs.immich.app/install/synology)).
- **(unverified)** DSM kernels: 4.4 on older models, 5.10 on newer ones. Landlock needs 5.13, so
  **Landlock is likely missing on every DSM model.** Story 59.07 records it per model.

## 3. TrueNAS

### 3.1 Versions

- **24.10 Electric Eel** moved apps from Kubernetes/Helm to **Docker Compose**. Custom apps come
  from a form or a compose YAML ([linuxiac](https://linuxiac.com/truenas-scale-24-10-shifts-from-kubernetes-to-docker/)).
  24.10 is end-of-life (24.10.2.4).
- **25.04 Fangtooth** is "Community Edition", Debian-based, one Linux line
  ([forum](https://forums.truenas.com/t/fangtooth-unifies-the-truenas-community-editions/30274)).
- **25.10 Goldeye**: NVIDIA open kernel modules (570.172.08), Maxwell, Pascal and Volta dropped;
  drivers are a checkbox (Apps → Configuration → Settings → Install NVIDIA drivers); Intel Arc via
  i915 ([forum](https://forums.truenas.com/t/nvidia-kernel-module-change-in-truenas-25-10-what-this-means-for-you/51070)).
- **CORE (FreeBSD)** is in sustaining mode; 13.3-U1.2 is its last 13.3 release. Not a target.

### 3.2 Apps catalog

- [`truenas/apps`](https://apps.truenas.com/getting-started/contributing-to-apps): a **community**
  train and an **official** train. New apps go to community. Immich is a community app with two
  datasets, `data` and `pgData` ([Immich](https://docs.immich.app/install/truenas)).

### 3.3 Storage and permissions

- Apps run as **`apps`, uid/gid 568** by default
  ([TrueNAS](https://truenas.com/docs/truenasapps/stableapps)).
- A host path needs an ACL entry for each run-as user, or the app fails to deploy. The dataset
  **Apps** preset adds a modify entry for group 568 (NFSv4 ACLs on ZFS)
  ([TrueNAS](https://www.truenas.com/docs/scale/24.10/scaletutorials/datasets/permissionsscale/)).
- ixVolume is TrueNAS-managed storage inside the apps dataset; host paths point at the household's
  own datasets.

### 3.4 Hardware and memory

- x86_64 only. The TrueNAS Mini X+ uses an 8-core Atom C3758 (no iGPU, no AVX2) with 32 GB ECC.
- "Allocate 1 Intel GPU" passes `/dev/dri`. Reports are mixed; YAML installs are a common
  workaround ([NAS-134739](https://ixsystems.atlassian.net/browse/NAS-134739)).
- **Since 24.04 the ZFS ARC may use nearly all RAM** (RAM minus 1 GiB). It gives memory back under
  pressure, but `MemAvailable` in `/proc/meminfo` does not count it as available
  ([forum](https://forums.truenas.com/t/scale-24-04-recommended-way-to-increase-zfs-arc-size/21293)).
  A memory check that reads only `MemAvailable` will see a TrueNAS box as nearly full.

## 4. Other NAS systems

- **Unraid:** Community Applications, Docker templates in XML, moderated
  ([Unraid](https://docs.unraid.net/unraid-os/using-unraid-to/run-docker-containers/community-applications/)).
  Default user `nobody` (99) and group `users` (100).
- **UGREEN UGOS Pro:** an App Center on Docker ([UGREEN](https://support.ugnas.com/detail/article/en-US/539)).
- **QNAP:** Container Station (Docker and compose); native packages are QPKG. **(unverified)**
- **Docker with compose is what all of them share.**

## 5. Precedent

| | Synology | TrueNAS | RAM floor |
|---|---|---|---|
| Jellyfin | Docker only (official) | official app | 4 GB on Synology |
| Plex | own SPK as `PlexMediaServer`; Docker common | official app | – |
| Immich (Postgres + vectors + ML: closest to Maktaba) | Container Manager compose Project | community app | **6 GB minimum, 8 GB recommended; 4 GB only with ML off** |

From Immich ([requirements](https://docs.immich.app/install/requirements)):
- the database on local SSD, **never on a network share**;
- the filesystem must support Unix owners and permissions;
- `DB_STORAGE_TYPE: 'HDD'` changes `effective_io_concurrency` and `random_page_cost` for spinning
  disks;
- it owns its Postgres image and plans its vector-extension moves (pgvecto.rs → VectorChord).

## 6. Performance

- **Postgres on an HDD array** works but is slow for random reads, and it keeps the disks from
  sleeping. A Maktaba database is a few GB. Many Plus models have M.2 slots, though M.2 *pools*
  are limited to listed drives on some. On TrueNAS an SSD pool for apps is normal.
- **Speech to text:** no published faster-whisper or whisper.cpp numbers were found for the J4125,
  N100 or V1500B. Loose figures: `base` faster than real time and `small` near real time on modern
  CPUs. The J4125 lacks AVX, and CTranslate2 falls back to slower kernels without it
  **(unverified)**. Story 59.06 measures at setup instead of guessing.
- **Video on ARM:** the RTD1619B has no ffmpeg hardware path a container can use. Software
  1080p H.264 encoding on four Cortex-A55 cores is not real time **(unverified)**.
- **4 GB of RAM:** DSM, Postgres, a `small` model and ffmpeg do not fit together. The precedent is
  to turn ML off or run it in a window.
