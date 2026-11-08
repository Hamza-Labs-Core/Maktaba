---
schema: 1
kind: pr
pr: 945
title: A hung pipeline test prints its stack; the cluster tests' waits are bounded
date: 2026-11-07
session: 11d260cf-8041-4254-820a-1857ec779e81
---

## What happened
- `unit / pipeline-cluster-windows` hung once on #831 (run 42431331279) and was cancelled at 15 minutes with no traceback. Matching the `-q` progress dots and their skip pattern against `pytest --co` order names the running test: `tests/cluster/test_claim_dynamic.py::test_a_gpu_node_takes_its_own_gpu_batch_not_the_cluster_pool[sqlite]`.
- `pipeline/pyproject.toml` now sets `faulthandler_timeout = 120` for every tier. A test still running after 120 s prints all thread stacks to stderr, and the run goes on.
- `test_claim_dynamic.py` bounds each drain and gather with `asyncio.timeout(30)`. The Windows `icacls` calls have `timeout=60`. `tests/test_conftest_guards.py` guards the setting, and a pytester run there shows the dump.

## What we learned
- A cancelled `-q` run still names its stuck test. Count the progress characters, then line them up with `pytest --co -q`: the `s` positions confirm the alignment, since `[postgres]` params skip without a server.
- The hung test does only aiosqlite work, and aiosqlite answers through `call_soon_threadsafe` into a loop with no timers. Not proven as the cause.

## Keep doing
- Bound every wait in an async test, `asyncio.timeout` around the waits.

## Open
- If the dump shows a stuck test again, read its stack to find the root cause (the aiosqlite/Proactor wakeup or something else).
