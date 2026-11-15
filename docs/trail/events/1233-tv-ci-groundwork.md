---
schema: 1
kind: pr
pr: 1233
title: "ci(tv): TV catalog check, shared app-log upload, TV path groups, store checklists (58.15, 58.16 phase 1)"
date: 2026-11-14
session: 835d6f8c-ea9f-45df-84b9-e8e2cf15474c
agent: claude-code
---

## What happened
- #1233 lands the Phase 1 parts of stories 58.15 and 58.16, the pieces every TV platform story reuses.
- `tools/i18n/check_tv_catalogs.py` checks the TV catalogs (`apps/tv/web/i18n`, `web/packages/castreceiver/i18n`, `apps/tv/roku/i18n`). It fails on a key missing or empty in either language, on placeholders that differ, and on a Latin word in Arabic that is neither a product name (`LATIN_ALLOWED`) nor code. A catalog that does not exist yet is skipped. The lint job's repository checks run it and its tests on every pull request, so `ci.yml` did not change.
- `.github/actions/tv-logs` uploads a TV job's app logs, keeps them 30 days and links them from the job summary. `_android-tv.yml` now uses it on every run; before, it kept reports for 7 days and only on failure.
- `.github/ci-paths.yml` gains the groups `_tv_web`, `_cast_receiver`, `_roku` and `_vega`. A group is no job's filter, so each platform story adds its filter (`*ci`, its group, its workflow) next to its job.
- `docs/stores/{amazon,cast,samsung,lg,roku}.md` hold the real-device checklists, and `docs/stores/README.md` holds the release-note rule. `tools/release/tv_checklist_guard.py` stops a submission whose checklist has no passing result for its version (58.16 T03).

## What we learned
- The website's link check (`web/packages/website/scripts/links.mjs`, job `lint / maktaba.tv links`) reads every tracked file except tests, comments included. An example host in a code comment (`maktaba.tv/code`) failed it. Use a neutral host in examples.
- `.github/actions/**` is in the `_ci` group, so any new composite action runs every CI job once, as a `ci.yml` change does. Check main's state before adding one.
- Fire TV's page is `docs/stores/amazon.md`, the name stories 58.04, 58.05 and 58.18 use, not `fire-tv.md`.

## Keep doing
- A TV job's workflow ends with `.github/actions/tv-logs` under `if: always()`. `tools/ci/tests/test_tv_logs.py` fails any job in `_android-tv.yml`, `_tv-web.yml`, `_tv-web-emulators.yml`, `_cast-receiver.yml`, `_roku.yml` or `_vega.yml` that lacks it.

## Open
- 58.15 Phase 4 (Roku shaping, `shared/i18n/visual-vectors.json`, T02 to T05) was left for
  later. It has since landed: the vectors are on main, and T04 is enforced in CI by #1347 and
  #1351.
- 58.16: each platform's own job and its filter come with its story. No CI job runs actionlint; it was run locally for T02.
- No release step called `tv_checklist_guard.py` when this merged. #1268's
  `tools/release/amazon_submit.py` was the first submission step to call it.
