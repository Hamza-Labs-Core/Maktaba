---
schema: 1
kind: pr
pr: 1066
title: Each N16 report belongs to one test; classify's wave 4 writes its own
date: 2026-11-09
session: b7095d77-a4dd-4b22-9468-dcb6f082ae2b
prs: [1059]
---

## What happened
- #1059's Open list said that `detect`'s workload depends on test order: 29 plugin API calls (2 seasons) when classify runs alone, 19 (1 season) in the full suite, cause not found.
- It was not leaked state. Classify's two parity tests both called `p.report("tv.maktaba.classify")`: wave 1 seeds `make_catalog` (two shows of 8 episodes, detect 29 calls a job) and wave 4 seeds `_world` (one show, 19 calls). The report file held whichever test ran last.
- Proven on a scratch Postgres with temporary probes in `StatementDB._run` and detect's `run`: wave 1 alone 29, wave 4 alone 19; in CI's full command wave 1's statements still ran 29 calls a job while the report showed 19. Running wave 4 first left wave 1 unchanged (288 `detect.apply_changes.4` calls = 12×8 + 12×16), and detect's plan, rows and rules were identical in every job of every order.
- Wave 4 now writes `tv.maktaba.classify.wave4`. `Parity.report` claims its name for the running test (`claim_report`, keyed by `PYTEST_CURRENT_TEST`), and a second test asking for a taken name fails, naming both. `test_parity_report.py` pins the claim and scans every first-party parity test for a shared literal report name (it failed on main, naming both classify lines).

## What we learned
- Before hunting shared state behind an order-dependent number, check that the number comes from the test you think: an output file keyed by plugin, not by test, is shared by every test of that plugin.
- Probing the statements actually sent (one line per call) separated "the work changed" from "the report changed" in one run.

## Keep doing
- Give every artifact a test writes a name unique to that test, and make the writer refuse a name already taken in the session.

## Stop doing
- Killing a helper process by name (`pkill -n`) to clean up after a local regression run: on a shared machine it can hit another session's dev server. Kill only PIDs this run recorded; the dev server exits with its test process anyway (`-watch-stdin`).
