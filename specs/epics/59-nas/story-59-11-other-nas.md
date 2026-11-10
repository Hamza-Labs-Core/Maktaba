# Story 59.11 — Unraid, QNAP, UGREEN and plain compose; the old scaffolds go

> Epic 59 · Phase 3 · Depends on: 59.01–59.08 · Needed by: 59.14, 59.15

## Why

After Synology and TrueNAS, home NAS boxes mostly run one of these:
- Unraid, with Community Applications Docker templates;
- QNAP, with Container Station;
- UGREEN UGOS Pro, ASUSTOR ADM or a plain Linux mini PC, all with Docker and compose.

One plain compose file covers all of them except Unraid, which expects a template.

Two leftovers in the repo mislead:
- the scaffolds `cloud/installers/nas/synology/INFO` and `cloud/installers/nas/qnap/qpkg.cfg`;
- the development compose (`deploy/compose/docker-compose.yml`), which names images
  (`ghcr.io/maktaba/<svc>`) that release does not publish.

## Description

**The plain compose file** is the existing published Docker compose,
`cloud/installers/docker/docker-compose.yml` (release already rewrites it to the pinned tag), published at
maktaba.tv/install/docker):
- the same shape as Synology's (59.09), with `MAKTABA_PLATFORM: docker`;
- `user:` commented with how to choose it (`id <user>`);
- media mounts at their host paths;
- commented blocks for:
  - `/dev/dri` + `group_add` (Intel/AMD);
  - NVIDIA (`deploy.resources.reservations.devices`);
  - the D-Bus socket, with its cost stated (59.08);
  - a bridge-network variant (59.08).
- The maktaba.tv install page fills it in from the household's answers, as for Synology. It is
  the one source: a release step derives the Synology files (59.09) from it, and the TrueNAS app
  (59.10) and Unraid template are checked against it in CI so their settings do not drift.

**Unraid** (`deploy/nas/unraid/maktaba.xml`, submitted to Community Applications):
- Repository `ghcr.io/hamza-labs-core/maktaba-server:latest` (the highest stable release), Network Type `host`.
- Extra Parameters: `--user 99:100 --stop-timeout 90`.
- Config paths:
  - `/var/lib/maktaba` → `/mnt/cache/appdata/maktaba`, with the description "keep this on a
    cache pool or a single disk";
  - one media path → `/mnt/user/<share>`, mounted at the same path;
  - more media paths can be added.
- Device `/dev/dri` (optional). Variables `TZ` and `MAKTABA_PLATFORM=unraid`.
- Icon and the support link (maktaba.tv).
- Unraid's media under `/mnt/user` is shfs (FUSE). That is fine for media; 59.02 refuses it only
  for the **data** dir.

**QNAP and UGREEN** use the plain compose through their container apps: Container Station on
QNAP, App Center's Docker on UGREEN. The guide has one short section for each, naming where the
compose file goes and how to find the user's uid.
- QTS uses 8080 for its own UI, which collides with Maktaba's cluster API port. `ReconcilePorts`
  moves Maktaba's port, and setup shows the new one (59.08). The moved port is kept in
  `server.toml`, so nodes find it at the same port after restarts.
- The device checklist covers QNAP when one is available. Until then, the guide says the QNAP
  section is untested.

**What is removed:**
- `cloud/installers/nas/` (the Synology `INFO` and QNAP `qpkg.cfg` scaffolds) and its README
  section;
- `specs/epics/25-cloud-relay/plan-25-31-nas-support.md` is marked "Replaced by Epic 59" at the
  top, with a link. It is kept for history; nothing else in it changes.
- The development compose's image names are fixed to what release publishes
  (`ghcr.io/hamza-labs-core/maktaba-{api,streaming,pipeline,web}`). A header comment says it is for
  development, not for a NAS, and points to `cloud/installers/docker/docker-compose.yml`.

## Acceptance criteria

- **Given** the plain compose on a UGREEN DXP4800 Plus (N100, 8 GB), **then** Maktaba is ready,
  VA-API is used, and speech runs with `small`.
- **Given** the Unraid template installed from a local CA repository, **then** Maktaba is ready
  as 99:100 with data on the cache pool.
- **Given** the repo, **then** `cloud/installers/nas/` no longer exists, and the dev compose's
  images resolve (`docker compose config` + `docker manifest inspect` in CI).

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | CI | plain compose with each commented block on | `docker compose config` | valid |
| T02 | CI `uid-unraid` cell | 99:100 | first run | ready |
| T03 | CI | `maktaba.xml` | schema check + render to `docker run` args | valid; host network; user 99:100 |
| T04 | CI | dev compose | resolve each image | all published names |
| T05 | Device checklist | Unraid 7, an N100 box with UGOS | install | pass; logs attached |

## Strings

None in the product beyond 59.03 and 59.08. Guides are in 59.14.

## Edge cases

- **Unraid array stopped:** media paths vanish. Libraries show No access, and nothing is treated
  as deleted (the root-probe rule).
- **Portainer or Dockge users:** the plain compose works as a stack. The guide mentions it
  in one line.

## Build plan

| File | Change |
|---|---|
| `cloud/installers/docker/docker-compose.yml` | the plain file: commented GPU, NVIDIA, D-Bus and bridge blocks; `MAKTABA_PLATFORM` |
| `deploy/nas/unraid/maktaba.xml` (new) | the template |
| `cloud/installers/nas/` | removed; README updated |
| `deploy/compose/docker-compose.yml` | image names, header comment |
| `specs/epics/25-cloud-relay/plan-25-31-nas-support.md` | "Replaced by Epic 59" note |
| maktaba.tv install page | plain compose generator |
