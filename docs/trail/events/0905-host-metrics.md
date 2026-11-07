---
schema: 1
kind: pr
pr: 905
title: Epic 53 story 53.12 — each machine's memory, disk free and load on the Overview
date: 2026-11-07
session: 0e9a3649-4e54-455d-9465-045c6636722d
prs: [896]
---

## What happened
- The Overview's "CPU, memory, disk free of the home and each machine" had nothing behind it. The supervisor's `/metrics` now writes these `{machine}` gauges:
  - `maktaba_host_memory_bytes`
  - `maktaba_host_memory_free_bytes`
  - `maktaba_host_disk_free_bytes` (the data folder's disk)
  - `maktaba_host_load1`
  - `maktaba_host_cpus`

  The API takes the `maktaba_host_` prefix from it, and the allow-list keeps them.
- `hostinfo.LoadAverage` is new: `vm.loadavg` on darwin (a `struct loadavg` of three `fixpt_t` and a `long` scale) and `sysinfo` on Linux (loads shifted by 16). Windows keeps no load average, so that series is left out.
- Stacked on #896 (base main): both change the Remote's dashboard tests, so a branch from main would have conflicted.

## What we learned
- Leave out a reading the system can't give rather than write 0. A 0 GB disk free or a 0 load looks like an alarm or like idle.
- A machine's own numbers already reach the API through the supervisor's `/metrics`. A new family there only needs its prefix in `supervisorMetricsPrefixes`.

## Open
- The home's Overview shows only its own machine: each machine's log index scrapes only local endpoints. Showing joined machines on the home's board needs the home to read their metrics (Epic 53 left this to the cluster views).
