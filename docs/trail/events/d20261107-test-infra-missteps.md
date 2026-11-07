---
schema: 1
kind: incident
title: The test-infrastructure work's missteps on 2026-11-07, and what each taught
date: 2026-11-07
session: 03d72f4f-f05c-4d72-897b-0e3d0c10f530
---

## What happened
- **The Python log-bench gate took four fixes in one day:** #828 (best of three rounds), #838 (a tail term), #860 (structlog's p99) and #866 (comparing with the base commit on the same runner). Each of the first three tuned the runner factor from a few failing runs. Only when 48 runners were sampled and grouped by CPU model did it show that no factor judges every CPU alike: there is a 1.3–1.4× spread whatever the loop. #806's failures were first read as noise within a run. A run's rounds agree within 2–3%; the spread is between CPU types.
- **The Core bundle check's failure (#857) was put down to #843 alone.** #833 fixed the plugin runners' start-up crash and #843 their socket path; together they let the runners stay up past the idle measurement. #857 also asked which way to go when the choice had already been made: keep the budget at 4 and start runners on demand, as #859 does.
- **#858 failed `lint / repo`** because its slowest-tests list piped `sort` into `head`, which the repo forbids under pipefail. It was pushed without running `tools/ci/tests`, which carries that rule (`test_no_head_pipes.py`).
- **#868 failed `affected-tests`.** The test of `runtime.py`'s change sat in `tests/test_exit_after_stop.py`, which imports `runtime` only inside a function, so the gate did not count it as one of `runtime.py`'s tests. It moved to `tests/engine/test_runtime.py`.
- **Linking and merging:**
  - #869, someone else's PR, was linked to this session's thread in place of #868, and was unlinked.
  - A merge of #864 written for zsh stopped at its `[ … ]` test. The fail-closed read held, and nothing merged until the sequence ran in `bash -c`.
- **#862's first guess was wrong.** The guess was another test leaking its thread into `test_run_hands_its_mode_to_the_stage_handlers`. The conftest's teardown check, on main since 2026-11-02, rules that out: the thread was the worker's own aiosqlite connection (#868).

## What we learned
- For a flaky gate on shared runners, sample many runners and group them by CPU model before changing the gate. A fix tuned to a few runs moves the failure to the next CPU.
- Before writing up an issue's options, look for a decision already made. Before blaming one PR, check the PRs merged around it.
- `tools/ci/tests` and `affected-tests` are cheap to run before a push, and each would have caught one of these failures.

## Keep doing
- Run `pytest tools/ci/tests` before pushing any change under `tools/ci`, `tools/dev` or `.github`.
- Put a test of a module's change in a test file that imports that module at the top.
- Run read-then-merge sequences in `bash -c`, never in the session's zsh. Check a PR number against `gh pr create`'s output before linking it.

## Open
- Nothing.
