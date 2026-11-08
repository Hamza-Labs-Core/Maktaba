---
schema: 1
kind: pr
pr: 960
title: 53.12: the Overview dashboard scales with many machines
date: 2026-11-08
session: 0e9a3649-4e54-455d-9465-045c6636722d
prs: [915]
---

## What happened
- With two or more machines, the Overview shows totals across them (components up and down, least disk and memory free, highest load, restarts per minute) and the five machines most in need of attention, ranked by latest values: components down, then least disk free, then load per CPU.
- The answer carries `machines: {total, shown, all}` (clients say how many of how many are shown and offer a machine picker) and `omitted`; `machine=<node id>` gives one machine's lines plus each component that was down or restarted. The 50-series and 2,000-point bounds and the admin-only role test stand.
- Web, the Apple Remote and the Android Remote have the picker and both lines, in English and Arabic.
- Summing every component's rows at read time took 3.6–5.4 s for 50 machines × 45 components; the log index now writes small per-machine and fleet summary series at each `Record`, and the board reads full lines only for the machines shown (about 40–60 ms). A performance test, `dashboard_30d_machines`, keeps both views under 500 ms.

## What we learned
- On this store a time-range read of one name visits every label set (the key starts `name, labels`), at about 2 µs a row in pure-Go SQLite: sums belong in `Record`, which sees one scrape of every machine at once. `EXPLAIN QUERY PLAN` showed the board query using only the key's name part plus temporary sorts.
- One machine's label sets are found by skipping through the key by hand (`labels > ? ORDER BY labels LIMIT 1`, jumping per component).
- The devportal tests need `npm run build` first, which needs `npm ci` in `web/wiki-app`.

## Open
- The summary series fill only from this release; older ranges show those panels empty until data builds up.
- A machine that went offline has no fresh values and ranked last (being fixed in #970).
