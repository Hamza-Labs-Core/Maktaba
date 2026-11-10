# Epic 59 — Maktaba on a NAS: Synology, TrueNAS and the rest

> **Status:** spec (2026-11-10). **Source:** [`specs/epics/59-nas/`](../../../specs/epics/59-nas/README.md).
> **Anchors:** [Epic 28](../../../specs/epics/28-auto-update/README.md) (updates, the guard, the rehearsal) · [Epic 31](../../../specs/epics/31-multi-instance/README.md) (canonical share paths) · [Epic 39](../../../specs/epics/39-linux-app/README.md) (the headless install the image carries) · [Epic 54](epic-54-plugins.md) (plugin runners and plugind) · [Epic 58](epic-58-smart-tv-platforms.md) (the cloud code) · [research](../../../specs/epics/59-nas/research.md).

## Goal

A household that keeps its media on a NAS can run its Maktaba home on that NAS (Synology, TrueNAS,
Unraid, QNAP, UGREEN or any Docker host), as the user the NAS gives it, never as root. The home
explains what it may read and write, fits the hardware it finds, uses the GPU when there is one,
runs plugins with a stated isolation level, and updates itself safely, in English and Arabic.

## Key decisions

| | |
|---|---|
| Runtime | the one signed, self-updating `maktaba-server` image on every NAS; no native SPK or QPKG |
| User | runs as whatever uid:gid it is given (TrueNAS 568, Unraid 99:100, a DSM user), named through `nss_wrapper` for the bundled Postgres; no root entrypoint, no PUID/PGID; refuses root, privileged (bounding capabilities) and the Docker socket |
| Permissions | each library checked (read and write / read only / no access) with the fix in the NAS's own words, never at every start; read-only libraries mounted `:ro` and supported, their subtitles kept in the database |
| Data | on local storage, SSD first (disks found through mountinfo, asked on ZFS); Postgres memory by tier and tuned for spinning disks; quiet hours for heavy work; no periodic sweep by default, for disk sleep |
| Hardware | detected, not assumed: cgroup memory and CPU limits, ZFS ARC counted as available, AVX2, render devices; Light / Standard / Strong tiers by memory alone set defaults; a 3.5 GiB floor |
| Video and speech | VA-API joins NVENC and QSV behind the test encode; speech to text on arm64; the model chosen by a measured speed |
| Plugins | the payload carries a wheelhouse (first start and updates offline); isolation levels Full / Container from plugind's `full`/`partial`; third-party plugins on Container held until the admin's yes |
| Platforms | a Container Manager project on Synology, a community app on TrueNAS, a Community Applications template on Unraid, plain compose elsewhere |
| Cluster | media mounted at the host's own path so canonical share paths match nodes without a path map; a Light NAS should be the home or storage |
| Updates | self-update beside catalog image updates (newer wins, older never replaces); a container rehearsal with NAS shapes |
| Out | TrueNAS CORE, NAS under 4 GB, 32-bit ARM, managing the NAS itself, a storage-only role |

## Stories

| # | Story | Phase |
|---|---|---|
| 59.01 | [One image for every NAS: any user, never root, first start offline](../../../specs/epics/59-nas/story-59-01-image-any-user.md) | 1 |
| 59.02 | [The data volume and the database on a NAS: local, SSD first, tuned for spinning disks, quiet hours](../../../specs/epics/59-nas/story-59-02-data-and-database.md) | 1 |
| 59.03 | [Media permissions: check, explain, and work with read-only libraries](../../../specs/epics/59-nas/story-59-03-media-permissions.md) | 1 |
| 59.04 | [Hardware detection and tiers: memory (with ZFS ARC), CPU features, GPUs, disks](../../../specs/epics/59-nas/story-59-04-hardware-tiers.md) | 1 |
| 59.05 | [Hardware video on NAS chips: VA-API, Quick Sync and the render device](../../../specs/epics/59-nas/story-59-05-vaapi.md) | 2 |
| 59.06 | [Speech to text on NAS CPUs: arm64, the model by tier, a measured speed](../../../specs/epics/59-nas/story-59-06-stt-nas.md) | 2 |
| 59.07 | [Plugins in a container: isolation levels, plugind's layers, the admin's yes](../../../specs/epics/59-nas/story-59-07-plugins-container.md) | 2 |
| 59.08 | [Networking on a NAS: host or bridge, mDNS beside the NAS's responder, HTTPS, reverse proxies](../../../specs/epics/59-nas/story-59-08-networking.md) | 3 |
| 59.09 | [Synology: a Container Manager project, DSM's users and shares, the J4125's GPU](../../../specs/epics/59-nas/story-59-09-synology.md) | 3 |
| 59.10 | [TrueNAS: a community app, datasets and ACLs, GPUs, the ZFS cache](../../../specs/epics/59-nas/story-59-10-truenas.md) | 3 |
| 59.11 | [Unraid, QNAP, UGREEN and plain compose; the old scaffolds go](../../../specs/epics/59-nas/story-59-11-other-nas.md) | 3 |
| 59.12 | [Updates on a NAS: self-update beside catalog updates, the NAS leg of the rehearsal](../../../specs/epics/59-nas/story-59-12-updates.md) | 4 |
| 59.13 | [A NAS in a cluster: home, full node or storage; one path for each share](../../../specs/epics/59-nas/story-59-13-cluster.md) | 4 |
| 59.14 | [English and Arabic: setup screens, messages, and the install guides on maktaba.tv](../../../specs/epics/59-nas/story-59-14-strings-guides.md) | 4 |
| 59.15 | [CI on NAS-shaped containers, device checklists, logs kept](../../../specs/epics/59-nas/story-59-15-ci.md) | 1 |
| 59.16 | [OpenAPI and role tests for the new admin routes](../../../specs/epics/59-nas/story-59-16-openapi-roles.md) | 4 |
