# Story 59.10 — TrueNAS: a community app, datasets and ACLs, GPUs, the ZFS cache

> Epic 59 · Phase 3 · Depends on: 59.01–59.08 · Needed by: 59.14, 59.15

## Why

TrueNAS Community Edition (25.04 Fangtooth, 25.10 Goldeye) runs apps as Docker Compose since
24.10. Its catalog, `truenas/apps`, has a **community** train that takes third-party apps; Immich
is one. Each app gets:
- a form for storage, user, network and GPU;
- updates when the catalog moves.

TrueNAS's own conventions:
- **User:** apps run as `apps` (568).
- **Storage:** host paths need NFSv4 ACL entries for the run-as user, or the app fails to
  deploy. The dataset **Apps** preset handles this.
- **GPUs:** an "allocate GPU" control for Intel/AMD, and an NVIDIA driver checkbox (25.10:
  open kernel modules; Pascal and older dropped).
- **Memory:** the ZFS ARC fills nearly all RAM (research.md §3).

TrueNAS CORE (FreeBSD) is not a target.

## Description

**The app** (`deploy/nas/truenas/ix-dev/community/maktaba/`, in `truenas/apps`' source layout:
`app.yaml`, `questions.yaml`, `ix_values.yaml`, `item.yaml`, `README.md`,
`templates/docker-compose.yaml` and `templates/test_values/*.yaml`; the exact file set is taken
from the catalog's contributing guide at the commit 59.15 pins).

The form's questions:

| Group | Field | Default |
|---|---|---|
| Maktaba | Time zone | the system's |
| User | Run as user / group | 568 / 568 |
| Network | Host network | on (mDNS); off → web port 8088 published, bridge text (59.08) |
| Storage | **Data** | a host path: a dataset on an SSD pool with the Apps preset (the default the form asks for); ixVolume offered as the alternative |
| Storage | **Media** (list) | host paths, each mounted at its own host path (59.13), read-only toggle per path |
| Resources | Memory limit | 8 GB; tier follows (59.04) |
| Resources | GPU | none / Intel-AMD (`/dev/dri`) / NVIDIA |

- **The compose template** sets:
  - `user: "{uid}:{gid}"`, `network_mode: host`, `stop_grace_period: 90s`;
  - environment: `MAKTABA_PLATFORM=truenas`, `TZ`, `MAKTABA_HOST_IP` from the node's IP;
  - no D-Bus socket (Maktaba's own responder, 59.08);
  - `/proc/spl/kstat/zfs/arcstats`, which is readable in the container by default; the template
    adds no mask over it, so 59.04 can read the ARC;
  - `devices` and `group_add` for Intel/AMD GPUs;
  - the catalog's NVIDIA resource block for NVIDIA.
- **Maktaba's image chowns nothing.** For host paths, the app's README and form text say to use
  the Apps preset or add an ACL entry for the run-as user.
- **ixVolume** is created root-owned, and 59.01's write check would fail on it. When the household
  picks ixVolume, the app uses the catalog library's own permissions step for ixVolumes, which
  sets the owner to the run-as user before Maktaba starts. That step is the catalog's code and
  runs as the catalog's container, not as Maktaba's image, which still never runs as root.
- **Image tag:** the app pins the release's exact version, so catalog updates move it (59.12).

**Permissions in TrueNAS words.**
- With `MAKTABA_PLATFORM=truenas`, fix texts name **Datasets → {dataset} → Permissions** and the
  ACL entry (59.03). The dataset is taken from `/mnt/<pool>/<dataset>`.
- The write-denied message (59.01) says: "Edit the dataset's permissions and add an ACL entry
  for user 568 with Modify, or use the Apps preset."

**Memory and the ARC.**
- 59.04 counts reclaimable ARC as available, so the tier is right on a box with 32 GB "used".
- The app's memory limit is what Maktaba plans for.
- The README notes that the ARC will give memory back. It does not tell households to cap the
  ARC, which is a TrueNAS-wide choice.

**GPUs.**
- **Intel/AMD:** the app passes `/dev/dri`. VA-API is tried (59.05).
- **NVIDIA:** the 25.10 drivers serve NVENC and CUDA for speech (`large-v3-turbo` on the Strong
  tier).
- **Dropped cards:** a card 25.10 dropped (Pascal and older) fails the test encode and falls
  back, with the reason shown. The README lists the 25.10 change.

**Getting into the catalog.**
- The app is developed and tested in our repo. CI runs `truenas/apps`' own tooling against it:
  the render test, lint and a `docker compose config` of each test values file (59.15).
- After the device checklist passes on 25.04 and 25.10 (README Q2), a pull request goes to
  `truenas/apps`' community train under the project's GitHub account.
- **Until it merges**, the guide offers the same compose as a **Custom App** (Apps → Discover →
  Custom App → Install via YAML), with the questions as comments.
- **Keeping it current:** each release renders the app pinned to its own image
  (`tools/release/truenas_app.py`), as it renders the published Docker compose file, rather than
  committing a new pin to main (such a commit would itself be the next release candidate). The
  release page carries the Custom App YAML pinned the same way, and, once the app is in the
  catalog, a release step opens the bump PR to `truenas/apps`. This is the catalog's normal
  flow, and nobody needs to remember it.
- **Setup and health:** the household runs the setup from the app's Shell (`maktaba-server
  container setup`); a container not set up says so in TrueNAS's words. The image has no curl,
  so the healthcheck is `maktaba-server healthcheck`: healthy while the container waits for its
  first setup, then only when the web app answers.

## Acceptance criteria

- **Given** TrueNAS 25.10 with a data dataset on an SSD pool (Apps preset) and a media dataset
  with an ACL entry for 568 (Read), **when** the app is installed with defaults, **then** Maktaba
  is ready as 568. Media shows Read only, the tier is from the app's memory limit, and available
  memory counts the ARC.
- **Given** a media dataset with no ACL for 568, **then** that library shows No access with the
  TrueNAS fix, and the app still deploys.
- **Given** an Intel N100 box with GPU allocation, **then** VA-API is used for transcodes.
- **Given** `truenas/apps`' tooling in our CI, **then** the app passes its render and lint
  checks.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | CI | `truenas/apps` tooling at a pinned commit | render + lint `ix-dev/community/maktaba` with each test values file | pass |
| T06 | CI | test values with ixVolume data, run as 568 | render + start | the permissions step owns the volume; Maktaba's write check passes |
| T02 | CI `uid-568` cell | 568, group-writable data | first run | ready |
| T03 | Go unit | `MAKTABA_PLATFORM=truenas`, `/mnt/tank/media` | fix text | names dataset tank/media |
| T04 | Go unit | arcstats fixture | memory | ARC counted |
| T05 | Device checklist | 25.04 and 25.10, Intel N100 and a CPU-only box | install from the app form; Custom App YAML | pass; logs attached |

## Strings

TrueNAS fix strings are in 59.03. The app form's labels are English only (TrueNAS's catalog UI
has no Arabic). Maktaba's own screens are in both languages.

## Edge cases

- **ixVolume for data:** works through the catalog's permissions step, but it lives in the apps
  dataset, possibly on HDDs. The form's help text recommends a dataset on an SSD pool, and
  59.02's rotational check reports what it finds.
- **Apps pool moved or the app reinstalled:** the data dataset keeps the home. Reinstalling with
  the same host path picks it up.
- **24.10 (end of life):** not tested. The app's `min_version` is 25.04.

## Build plan

| File | Change |
|---|---|
| `deploy/nas/truenas/ix-dev/community/maktaba/` (new) | the app in `truenas/apps`' source layout |
| `deploy/nas/truenas/custom-app.yaml` (new) | the Custom App version |
| `.github/workflows/nas.yml` (from 59.15) | the catalog tooling job |
| `tools/release/` | the catalog bump PR step |
| `cmd/maktaba-server/internal/mediaaccess/` (from 59.03) | TrueNAS wording |
| `docs/nas/checklists/truenas.md` (new, in 59.15's folder) | device checklist |
| `cmd/maktaba-server/` | the `healthcheck` command; the setup hint and the write-denied fix in TrueNAS's words |
| `tests/nas/truenas_app.py`, `tests/nas/truenas/` | the catalog's tooling against the app: render, lint, metadata, deploy |
| `.github/workflows/release.yml`, `packages.yml` | the Custom App YAML on each release; the catalog bump PR |
| `web/packages/install/site/{,ar/}install/truenas/` (new) | the maktaba.tv guide, en and ar |
