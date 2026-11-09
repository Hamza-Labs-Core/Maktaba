---
schema: 1
kind: pr
pr: 1068
title: core-media's parity fits its soft cap; every parity test starts faster
date: 2026-11-09
session: b7095d77-a4dd-4b22-9468-dcb6f082ae2b
prs: [1049, 1066]
---

## What happened
- `test_core_media_wave_9_is_identical` took 7.2 s on CI (run 42539983141), over the integration tier's 5 s soft cap (#1049's Open list); 4.1–4.5 s locally. Phases: building the world ~1.0 s, two drains ~1.3 s, N16's bench of nine stages × 11 runs × 2 sides ~1.4 s (1.27 s of it the measured jobs), teardown ~0.33 s.
- The bench cannot shrink without weakening N16, so the test was split by N16 group, each test building its own world and comparing every row, job and file: scan/probe/thumb_hash/link on the world after the scan; content_hash/fingerprint/thumbnail and style_sample/access_check on the world after LATER, with every assertion of the old test. Each writes its own report; `test_the_groups_time_every_stage_once` pins the coverage.
- The fake ffmpeg refused `thumb_small`'s 320 px copy: its five jobs failed three times each and wrote nothing. It now answers from the source's bytes, so each side's copy in `data.thumbnails` compares.
- Harness, for every parity test: the dev server starts while the two databases are copied, set up and seeded (it reads only the bundle at start; `sql.Open` connects at first use); the two template copies and the two drops run on their own connections at once; a setup that fails still stops the dev server and drops both databases (new selftest, which fails without that clean-up).
- Each core-media test took ~2.0–2.2 s locally; CI's full first-party command took 79 s, down from 93–98 s.
- After #1066 merged, main was merged in and the combined suite run locally before trusting the PR's green checks: #1066's report-name scan used core-media's `p.report(PLUGIN)` as a positive control, which this PR moved into a helper, so one test failed on the combination only. The control now uses names still written as literals; `claim_report` checks core-media's three names at run time. 299 passed, 1 skipped.
- Update rehearsal 16.7 failed once on the merged head (`the change's stage on 0.3.111: job 2 ran plugin on "0.3.111", want inproc`, 23 s after the harness switched `tv_stage` to inproc); the run's automatic second attempt passed it, and main's rehearsal passed at 06:40. Merged as 2dd0d18b2 on head 1fcfd8242 with every check green.

## What we learned
- The soft cap judges the call phase. Profile the phases before choosing: here the fixed cost of a world (~2.2 s) was larger than the part worth splitting, so it had to come down too, or each split test would still be over.
- `STRATEGY FILE_COPY` and concurrent `CREATE DATABASE` barely moved the template copies (~0.35–0.45 s for two): starting the dev server alongside them did.
- A fake that refuses a stage's call silently turns that stage into retries and a weaker proof; read the job outcomes of a parity world, not just its pass.

## Keep doing
- When two of your PRs touch the same test files, merge main into the second after the first lands and run the combined suite: each PR's green checks prove only its own half.

## Open
- Rehearsal 16.7 can see a `tv_stage` job run as a plugin on the previous release after the switch to inproc (run 42576326015, attempt 1); not investigated, passed on the rerun.
- Other parity tests are over the 5 s soft cap on CI as well (classify's waves 1 and 4 at 7.0 and 7.7 s, repair 6.2 s, the N16 selftest 5.1 s, livetv staging 5.1 s in run 42539983141); the harness change helps them all, but each still needs its own check.
