---
schema: 1
kind: pr
pr: 658
title: No step pipes into a reader that leaves early
date: 2026-11-06
session: t3-delegate-aa865d4e-422b-48fe-b7d8-1a0afef5db09
---

## What happened
- Finished the #647 sweep: the `testflight.yml` `head` became `sed -n 1p`, and about 40 `| grep -q` / `| grep -m` pipes in `tools/**/*.sh` and the workflows now feed grep without a pipe. The guard test lost its exception list and now catches these, including a pipe split over two lines.

## What we learned
- Under `pipefail`, a reader that exits at its first match can SIGPIPE the writer and fail the step.
- `grep -c` or redirecting to `/dev/null` is not a fix: GNU grep also stops at the first match when its output is `/dev/null`.
