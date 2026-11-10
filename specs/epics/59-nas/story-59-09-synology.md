# Story 59.09 — Synology: a Container Manager project, DSM's users and shares, the J4125's GPU

> Epic 59 · Phase 3 · Depends on: 59.01–59.08 · Needed by: 59.14, 59.15

## Why

Synology is the most common home NAS. On DSM 7.2 and 7.3, the supported way to run a server like
Maktaba is **Container Manager**:
- Docker 24.0.2 with compose "Projects", on x86_64 Plus models and on ARMv8 models since 7.2;
- Jellyfin's official method, and how Immich is run on Synology.

Synology's Package Center has no open path for third parties, and DSM 7 marks community packages
"not verified" (research.md §2.1).

Each model family differs in three ways:
- **J4125 models** have Quick Sync but no AVX.
- **Ryzen models** have AVX2 but no GPU.
- **ARM models** have little memory.

DSM also has its own conventions to respect: per-share ACLs for a DSM user, `#recycle`,
`@eaDir`, its own Postgres, ports 5000/5001 and 80/443, and its firewall.

## Description

**What the household does** (the guide on maktaba.tv, 59.14, in English and Arabic with dark
screenshots):
1. **Create a DSM user `maktaba`** (Control Panel → User & Group), with no admin rights and
   no apps. Note its uid and the `users` group (100). The guide shows how to read them: Task
   Scheduler running `id maktaba` once, or SSH.
2. **Create the folder** `docker/maktaba` on the volume (an SSD volume if there is one) and give
   `maktaba` Read/Write on it.
3. **Give `maktaba` access to each media share:** Read/Write where Maktaba may save subtitles,
   conversions and downloads, Read only elsewhere (59.03).
4. **Create the project.** Container Manager → Project → Create, name `maktaba`, path
   `docker/maktaba`, paste the compose file from maktaba.tv. It comes in two versions:
   - J4125 (Quick Sync);
   - other models.
   The install page fills in the uid, the shares and the memory tier from the household's answers.
5. **Start it** and open `http://<nas>:8088` to finish setup.

**The Synology compose file** (`deploy/nas/synology/compose.yaml`, derived from the published
Docker compose, `cloud/installers/docker/docker-compose.yml`, and published to maktaba.tv):
```yaml
services:
  maktaba:
    image: ghcr.io/hamza-labs-core/maktaba-server:0.3.120   # the release's pinned tag
    container_name: maktaba
    user: "1026:100"                      # the DSM user maktaba (filled in by the install page)
    network_mode: host
    restart: unless-stopped
    stop_grace_period: 90s
    mem_limit: 6g                         # from the chosen tier
    environment:
      MAKTABA_PLATFORM: dsm
      MAKTABA_HOST_IP: 192.168.1.20       # filled in
      TZ: Asia/Riyadh                     # filled in
    volumes:
      - /volume1/docker/maktaba:/var/lib/maktaba
      - /volume1/Movies:/volume1/Movies:ro       # same path as on DSM (59.13)
      - /volume1/Recordings:/volume1/Recordings
    devices:                              # J4125 version only
      - /dev/dri:/dev/dri
    group_add:                            # J4125 version only: the render group of /dev/dri
      - "937"
```
- **The tag** is the release's own version, written by release as it already does for the
  published Docker compose. A household may use `:latest`, which already means the highest
  stable release. The self-update keeps the home current in its window either way (59.12).
- **Version gate:** the file is checked by Container Manager's Docker 24.0.2. Keys newer than
  that compose version are not used.

**DSM detection and wording.**
- With `MAKTABA_PLATFORM=dsm`, every permission fix (59.03), the write-denied message (59.01) and
  the render-group message (59.05) use DSM's menu names in both languages.
- The share name is taken from `/volume<N>/<share>`.

**Ports and services.**
- DSM keeps 80/443/5000/5001, and its internal Postgres keeps 5432. Maktaba's ports do not
  collide (59.08).
- The guide covers:
  - DSM's firewall rules for 8088, 8080 and 8081 on the LAN;
  - optionally, a DSM reverse-proxy entry with WebSocket headers;
  - leaving Maktaba's own HTTPS name as the simplest path.

**DSM-specific behaviour:**
- **Skipped folders:** `#recycle`, `#snapshot` and `@eaDir` are skipped (59.03).
- **No inotify:** Maktaba uses no inotify watches, so DSM's 8192 limit is never touched. The
  guide says so, because other apps' guides ask households to raise it.
- **mDNS:** DSM runs avahi-daemon. Maktaba's own responder sits beside it (59.08); the D-Bus
  option is not used on DSM.
- **Kernel facts:** Landlock is recorded per model on the device checklist. The expected result
  is Container isolation (59.07).
- **J4125 HEVC:** the checklist records whether HEVC decode works through VA-API on DSM 7.2.2+
  (research.md §2.3).

**ARM models** (DS223, DS423, DS124, j series):
- Supported when they have ≥ 4 GB, which is not the case for most j models. They get the Light
  tier: no hardware video, speech `base` only.
- Under 4 GB, setup refuses with the NAS-as-storage text (59.04). The guide's model table says
  which models fit before the household starts.

**The SPK question (README Q3).** No native package is built. If the owner later wants Package
Center presence, it is a thin SPK using DSM's **Docker Project** resource worker that installs
this same compose project, published through SynoCommunity. It would be a separate story, and
its decision would be recorded here.

**The old scaffold** `cloud/installers/nas/synology/` is removed (59.11).

## Acceptance criteria

- **Given** the guide followed on a DS425+ (J4125, 6 GB) on DSM 7.2.2, **then** Maktaba is ready
  in under 15 minutes from the start of the guide, as user `maktaba`. Movies shows Read only and
  Recordings Read and write, and a 1080p HEVC transcode uses VA-API.
- **Given** a DS923+ (Ryzen R1600, 4 GB), **then** the Light tier is chosen, and video is
  software with the explanation.
- **Given** a DS223j (1 GB), **then** setup refuses with the NAS text.
- **Given** the Synology compose file, **then** Container Manager's Docker 24.0.2 accepts it
  unchanged (CI with `docker compose` 2.x at that engine's API version).

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | CI | Docker engine 24.0.x in DinD | `docker compose -f deploy/nas/synology/compose.yaml config` and `up` | valid; ready |
| T02 | CI `uid-dsm` cell (59.15) | `--user 1026:100` | first run | ready |
| T03 | Go unit | `MAKTABA_PLATFORM=dsm`, path `/volume1/Movies` | fix text | names share Movies, DSM menus |
| T04 | web/maktaba.tv unit | install page answers | render compose | uid, shares, tier, J4125 lines correct |
| T05 | Device checklist | DS425+ and a Ryzen model | the guide end to end | each step passes; logs attached |

## Strings

DSM fix strings are in 59.03. The guide's text is in 59.14.

## Edge cases

- **A household runs it as their admin user:** it works. The guide recommends a dedicated user,
  and Settings → This machine shows the uid.
- **Shares encrypted and unmounted at boot:** the container starts before the share is
  unlocked. Libraries show No access until it is, and nothing is deleted or re-scanned as
  missing while a root is unreachable (the existing root-probe rule in `failure_policy`).
- **Volume crash / degraded:** Maktaba sees I/O errors. The diagnostics bundle names the volume,
  and the data dir's health is DSM's to report.

## Build plan

| File | Change |
|---|---|
| `deploy/nas/synology/compose.yaml`, `compose.j4125.yaml` (new) | the two files, derived from `cloud/installers/docker/docker-compose.yml` by a release step |
| maktaba.tv install page (new; where the site's source lives is settled with 59.14) | answers → filled compose |
| `cmd/maktaba-server/internal/mediaaccess/` (from 59.03) | DSM wording |
| `docs/nas/checklists/synology.md` (new, in 59.15's folder) | device checklist |
| `cloud/installers/nas/synology/` | removed |
