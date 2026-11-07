---
schema: 1
kind: incident
title: The relay PR failed lint on mypy errors its push never checked
date: 2026-11-07
session: b17a35c4-3127-455d-bdb2-c21890fa281a
prs: [863]
---

## What happened
- The first push of the relay PR (863) failed `lint / lint` on two mypy errors in `pipeline/tests/plugins/test_dispatch.py`.
  - The new test patched `dispatch.sys`, which the module does not export.
  - It compared the stub's return value with a tuple of strings, where `_connect` returns a stream pair.
- Before pushing, ruff and the tests had been run, and mypy only on the changed source files, not on the tests.
- The fix: patch `sys.platform` on the real `sys` module, and give the stub `Any`-typed stand-ins compared by identity. `make lint-py` then reported only the 3 known errors of `stt/cohere_cuda.py` (the ML extras, which CI installs and this machine does not).

## What we learned
- The pipeline's mypy covers `tests/` too (pyproject's `files`): a new test is type-checked like the code.

## Keep doing
- Run `make lint-py` before pushing pipeline changes, tests included; the cohere_cuda errors are the only expected ones on a machine without the ML extras.

## Open
- None.
