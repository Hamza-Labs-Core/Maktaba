---
schema: 1
kind: decision
pr: 1087
title: "Epic 59 spec: Maktaba on a NAS (Synology, TrueNAS, Unraid and plain Docker)"
date: 2026-11-10
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
agent: claude-code
---

## What happened
- Spec only: 16 stories, research, the wiki page and its Arabic store. It replaces Epic 25's NAS
  plan (`plan-25-31-nas-support.md`), which targeted TrueNAS Helm charts and an image name that
  was never published.
- **Decisions:**
  - one runtime, the existing self-updating `maktaba-server` image, on every NAS: a Container
    Manager project on Synology, a community-train app on TrueNAS, a Community Applications
    template on Unraid, plain compose elsewhere; no SPK or QPKG;
  - the image runs as the uid it is given, never root, with no PUID/PGID;
  - tiers by memory alone, read-only libraries supported, VA-API, speech to text on arm64;
  - third-party plugins held while plugind reports `partial`, until the admin's yes.
- An independent review against the code found 18 problems before merge; all were fixed in
  the same PR.

## What we learned
- **The image and Postgres:**
  - The image's `/etc/passwd` has only uid 1000, and the bundled Postgres's `initdb` fails for any
    uid without an entry. Running as another uid needs `nss_wrapper`, as the official Postgres
    image does.
  - A privileged container is invisible in `CapEff` when the process is not root. Read `CapBnd`
    from `/proc/self/status` instead.
  - `pgbundle` passes `max_connections=300` as a `-c` argument, and the home alone uses about 130
    connections; settings in `postgresql.auto.conf` lose to `-c` and win over `postgresql.conf`.
- **Disks and memory:**
  - btrfs (Synology) and ZFS (TrueNAS) give `stat` an anonymous device number, so finding the disk
    under a path needs `/proc/self/mountinfo` and the `slaves/` chain.
  - TrueNAS's ZFS ARC is not counted in `MemAvailable`; `/proc/spl/kstat/zfs/arcstats` has it.
  - Setup's memory floor reads `sysinfo` total memory, which ignores the cgroup limit, and a
    "4 GB" NAS reports about 3.6–3.8 GiB.
- **What today's code already does:**
  - plugind already treats namespaces and Landlock as optional (`partial`); only no-new-privs,
    rlimits and its own seccomp filter are required.
  - The library sweep default is 0 (no periodic scan), and the walker already skips every
    dot-folder.
  - FUSE (Unraid's shfs) is already refused for the data dir.
  - `announce.go` already publishes through `avahi-publish-service` or its own zeroconf responder.
    On Linux, `SO_REUSEPORT` shares a port only between sockets of the same uid; `SO_REUSEADDR`
    is what lets a container's responder sit beside the host's avahi-daemon.
  - `update-rehearsal.yml` rehearses the Mac app's layout in Go, not the Docker image.
  - `:latest` already means the highest stable release.
  - `GET /api/system/updates` is public.
- **Where things live:** OpenAPI is `shared/api/openapi.yaml`; role tests are per-handler
  `roles_test.go`; subtitles are served by streaming (`handlers/subtitle.go`, `sidecar.go`);
  share-path rules exist in Python and Go (`api/internal/sharepath`) with shared fixtures.
- **The wiki's Arabic store:** an edit to a story's first 600 characters drifts its excerpt
  hash; rerun `generate_wiki.py --ar-todo --epic N` after review fixes.

## Open
- Unverified facts each story checks on real devices: DSM kernels and Landlock per model, DSM's
  responder, J4125 HEVC decode on DSM 7.2.2+, whether TrueNAS 25.04+ runs Avahi.
- Owner questions in README §12 (catalog submission, a thin SPK, test devices) keep their
  defaults until answered.
- The build PRs, starting with Phase 1: 59.01, 59.02, 59.03, 59.04 and 59.15.
