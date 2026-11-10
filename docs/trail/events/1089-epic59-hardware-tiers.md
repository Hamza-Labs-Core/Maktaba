---
schema: 1
kind: pr
pr: 1089
title: "Epic 59 story 59.04: hardware detection and tiers, Settings → This machine"
date: 2026-11-10
session: 9c9d9447-9828-4af8-9102-073c59d373eb
agent: claude-code
---

## What happened
- `internal/hostinfo` reads the machine from files under a root, so fixture trees drive every
  test: the cgroup memory limit (v2 `memory.max` and its ancestors, v1
  `memory.limit_in_bytes`), `MemAvailable` plus the ZFS ARC's `size − c_min`, cores capped by
  `cpu.max` or the v1 quota, AVX2/AVX-512, `/dev/dri` render nodes with their drivers, NVIDIA
  only when `/dev/nvidia*` exists, Landlock ABI, user namespaces, the platform (DSM, TrueNAS,
  Unraid, Docker, Linux; `MAKTABA_PLATFORM` wins) and the data disk from mountinfo through dm
  and md slaves to `queue/rotational`.
- Tiers by the memory limit alone (`TierFor`, `DefaultsFor`); serve writes `[server] tier` at
  each start unless `tier_chosen = "admin"`; a move up is logged and shown. The supervisor
  plans with the cgroup limit and quota and caps pipeline workers (Light 1, Standard 2, the
  main worker counted).
- Setup's floor is 3.5 GiB of the memory limit, with the NAS text on a NAS platform.
- `GET/PUT /api/server/machine` (admins; proxy of the supervisor's `/config/machine`), the
  bundle's `machine.json`, Settings → This machine in English and Arabic, maktaba.dev's server
  page, `docs/hardware.md`.

## What we learned
- The spec's `GET /api/system/machine` would have sat under `/api/system/`, which a session with
  a temporary password may still reach (`AllowedWhilePasswordChange`); the route went under
  `/api/server/` beside the other supervisor-backed settings, and the specs now name it.
- The supervisor's STT plan counts transcribe-only workers beside the main worker, so a tier's
  "pipeline workers" cap is the plan's `Workers` minus one.
- The Arabic catalogs call the product «مكتبة»; the story's Arabic strings said "Maktaba" and
  were written with «مكتبة» instead.
- Settings' tab test (SAC-12) lists every tab: a new tab updates it.

## Open
- 59.02 (Postgres by tier, spinning disks, quiet hours) builds on `DefaultsFor` and
  `DataDiskOf`; 59.05 transcode limits, 59.06 the speech model, 59.07 the kernel facts.
- T08 (a `--memory=4g` container reaching Light) is 59.15's container rehearsal.
