---
schema: 1
kind: pr
pr: 675
title: The pipefail guard covers deploy/ and actions; install.sh resolves latest without SIGPIPE
date: 2026-11-06
session: f43ebb40-6f2c-4bbc-984a-059b86779c63
prs: [658]
---

## What happened
- Review fixes for #658. The guard scanned only `tools/**/*.sh` and workflows, so the public installer kept `fetch_stdout …/releases/latest | grep -m1 '"tag_name"'` under `set -euo pipefail`: curl printed `(56) Failure writing output` on every run. It resolved only because `resolve_version` runs inside `$(…)`, where `-e` is not inherited; called directly under errexit it failed. It captures the answer first now, and `install-sh-verify.sh` has a case for it (fails on main).
- The guard now scans `deploy/**/*.sh` and `.github/actions/**/*.y*ml`; nine hits there were rewritten (`sed -n 1p`, a here-string, `[[ =~ ]]`).
- Four bare workflow checks written as `grep -q … < <(cmd)` by #658 capture first, so the command's own failure fails the step.

## What we learned
- `grep -q x < <(cmd)` is safe from SIGPIPE but blind to `cmd` failing. In a bare step, where the command's failure should fail the step, capture first: `out="$(cmd)"; grep -q x <<<"$out"`.
- A test that only checks the output of a function run inside `$(…)` cannot see a pipefail failure there; run the function directly under `set -e`.
