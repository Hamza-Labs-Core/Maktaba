# Story 25.31 — NAS support (Synology, QNAP, TrueNAS, Unraid)

> Epic 25 · Cloud relay · Phase 6 (server distribution)

## Description

NAS units are where most lifelong media collections actually live, and
we want Maktaba to be a 1-click install in each platform's package
manager. Each NAS vendor has a different idiom; we support the four
that matter:

1. **Synology DSM** — `.spk` package in our own repository.
   Users add `https://syno.maktaba.app` to Package Center →
   Settings → Package Sources. SPK includes a JSON manifest, an
   embedded set of Docker images (or native binaries depending on
   DSM version), an entry in the DSM control panel, and a port
   forward via DSM's reverse proxy.
2. **QNAP QTS / QuTS hero** — QPKG packaged via QDK. Same shape
   as Synology: control panel entry, web hook into QTS, native
   start/stop on QTS.
3. **TrueNAS SCALE** — official "App" via the TrueNAS Apps
   catalog (Helm-based). The Helm chart wraps our Docker image
   (25.30) with sane defaults and a UI form for library paths.
4. **Unraid** — Community Apps template (XML) that installs
   our Docker image with sensible defaults; pinned to the
   `cache` path layout Unraid expects.

Common posture across all four:

- The NAS uses our **Docker image (25.30)** as the runtime; the
  per-vendor package wraps Docker with vendor-specific UI glue.
- Permissions: each vendor has a default UID/GID for "shared
  folders" (Synology `100:100`, QNAP `1000:100`, TrueNAS
  `568:568`, Unraid `99:100`). The package configures the
  container to run as that UID so library files are readable
  without chown.
- Resources: by default we cap CPU at 50% and memory at 4 GB;
  user can override.
- Storage: separate volumes for `data` (DB), `cache`
  (transcodes; can be put on SSD on Synology DSM), and `media`
  (read-only mount of the user's library shares).
- Updates: each vendor's package manager handles updates; we
  publish new package files in lockstep with the Docker image.

## Acceptance criteria

- **Given** a Synology DS920+ user adds our package source,
  **when** they install Maktaba via Package Center,
  **then** the Maktaba app appears in the DSM main menu,
  starts within 60s, and the UI is reachable at
  `http://<nas>:8080`.
- **Given** the Synology user accesses Maktaba via DSM
  reverse proxy at `https://maktaba.<domain>`,
  **when** the proxy is set up via the package's helper,
  **then** TLS termination, headers, and websocket upgrade
  all work without manual config.
- **Given** a QNAP TS-453BeT3 user installs the QPKG,
  **when** they open the App Center,
  **then** Maktaba appears running with library shares
  pre-mounted at `/share/Multimedia/...`.
- **Given** a TrueNAS SCALE user installs from the App
  catalog,
  **when** they fill in library paths and click "Install",
  **then** the Helm release deploys, the pod is ready, and
  the service is reachable from the LAN.
- **Given** an Unraid user adds our community app
  template,
  **when** they apply,
  **then** the container is mapped to the right `appdata`
  paths and `media` shares without manual editing.
- **Given** the NAS reboots,
  **when** the system comes back,
  **then** Maktaba auto-starts as part of the NAS package
  daemon supervision.
- **Given** the user uninstalls,
  **when** they confirm,
  **then** the container, package, and (optionally) data
  volumes are removed.

## Test cases

| ID  | Type        | Setup | Action | Expected |
|-----|-------------|-------|--------|----------|
| T01 | manual      | Synology DS920+ DSM 7.2 | install SPK | works |
| T02 | manual      | Synology DSM 7.0 | install | works (min DSM target) |
| T03 | manual      | QNAP QTS 5.1 | install QPKG | works |
| T04 | manual      | QNAP QuTS hero | install | works (ZFS underlay) |
| T05 | manual      | TrueNAS SCALE 24.04 | install via Apps | works |
| T06 | manual      | Unraid 6.12 | apply community app | works |
| T07 | integration | UID matching on each vendor | scan media | reads without chown |
| T08 | regression  | NAS reboot | observe | auto-starts |
| T09 | manual      | DSM reverse proxy + Maktaba subdomain | request | TLS + WS works |
| T10 | manual      | uninstall on each | observe | clean |
| T11 | integration | resource caps | sysstat | within configured CPU/memory |

## Edge cases

- **Sandboxing on DSM.** Maktaba runs as a Container
  Manager (formerly Docker) container; DSM 7.2 deprecated
  the old Docker UI. SPK must support both legacy and
  current. Document min DSM = 7.0.
- **Underlying file systems.** btrfs (Synology), ext4
  (QNAP default), ZFS (TrueNAS, QNAP QuTS hero). All
  fine for SQLite; for Postgres mode we recommend
  ext4 or ZFS.
- **NFS or SMB shares as library roots.** On NAS,
  library shares are usually local mounts; Maktaba reads
  them as POSIX paths inside the container.
- **Default ports.** 8080 is unused on most NAS; if
  collision (Plex, Calibre-Web), the package sets
  `MAKTABA_HTTP_PORT=8090` and updates the reverse-proxy
  rule.
- **Update windows.** Synology and QNAP push package
  updates respecting users' update schedules; we don't
  force-restart.
- **Backup considerations.** NAS backup tools
  (Hyperbackup, HBS, Time Machine over SMB) should
  exclude Maktaba's `cache/` volume to save space; the
  package marks it.
- **Hardware transcode.** On Synology with Intel iGPU,
  we expose `/dev/dri/renderD128` for QuickSync; on QNAP
  the equivalent path. Documented per device tier.
- **ARM Synologies (DSx18+).** Our arm64 image runs;
  some lower-end ARMv7 boxes are out (per 25.30).
- **Container Manager auto-update.** Disabled by default
  for our image; updates are package-level.
- **TrueNAS catalog approval.** Submission to the
  community catalog is a process; document.

## Files / packages

- `packaging/synology/INFO`, `scripts/`, `WIZARD_UIFILES/`.
- `packaging/qnap/qpkg.cfg`, `package_routines/`.
- `packaging/truenas/Chart.yaml`, `templates/`.
- `packaging/unraid/maktaba.xml`.
- Build pipeline: `release/.github/workflows/release-nas.yml`.

## Open questions

- **ASUSTOR.** Smaller share, similar idiom to Synology.
  Defer.
- **OpenMediaVault.** Docker plugin makes it work via
  25.30; native package out for v1.
