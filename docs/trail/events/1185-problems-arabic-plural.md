---
schema: 1
kind: pr
pr: 1185
title: "problems: Arabic texts address the household in the plural (60.06)"
date: 2026-11-11
session: a9777a08-3c18-40b9-9e6d-104d2cbe061d
---

## What happened
- #1185 (merge c6ddab096) moved the Arabic of all 288 entries in `shared/api/problems.json`
  (525 of 864 title, means and do strings) to the voice guide's household plural. Where the bare
  «مكتبة» named the product and could read as "a library", it became «برنامج مكتبة», with
  masculine agreement. Requests for help now go to «فريق مكتبة». English, slugs, codes and links
  are unchanged.
- Quoted app labels («افحص الآن»، «اربط هذا الجهاز الرئيسي»، «إرسال تقارير الأعطال إلى جهازك
  الرئيسي») stayed in the singular because they must match `web/src/i18n/ar.json`.
- Five Arabic errors were fixed along the way: «بدأه» became «بدءه»; an ambiguous «لا تعرفه»;
  «خمس» became the figure 5; the whole product had been named as the component manager; and
  «خطة من مكتبة» became «خطة من خطط مكتبة».
- The problem pages' `who.person` and `who.app` lines (`problems.mjs`, its test and story 60.06's
  strings table) now name «برنامج مكتبة». The API's copy was regenerated with `go generate
  ./internal/httperror`. The problem-types guide states the rule in both languages, and there is
  one CHANGELOG entry.
- Six Arabic references changed (`problems-invalid-query-*-ar`, `problems-node-signature-*-ar`).
  They were re-rendered with `scripts/e2e-update.sh -g problems` in the CI image.
- First CI run, two failures:
  - "changed reference images are listed": the PR body had a `## Reference images` heading with
    brace-grouped names (`{360,768,1280}`). The check needs a `Reference images:` line and one
    full path per item.
  - `unit / unit`: `handlers/tv::TestInfoRemoteUnavailableReason` breached the test-budget soft
    cap (1.28s re-run alone on a loaded runner; 0.08s locally). No Go code changed.
  - Both passed on re-run, after the body was fixed.

## What we learned
- `--update-snapshots=changed` keeps a reference whose new pixels fall inside the 0.1% tolerance.
  `problems-invalid-query-1280-ar.png` still showed the old singular title after the first
  update. To refresh text, delete the language's references for that page and render again.
- The wiki generator's Arabic store doesn't carry story 60.06's strings table. Editing its
  Arabic cells left `generate_wiki.py --check` green without a store change.
- macOS has no `timeout`. A bounded background job has to rely on the harness timeout or a loop
  that counts its own polls.

## Keep doing
- Check a reference-image list with `python3 tools/ci/reference_images.py --body F --files F`
  before pushing the PR body.
- Keep quoted UI labels verbatim in registry text, even when the surrounding sentence changes
  voice.

## Stop doing
- Brace or glob shorthand for file names in a `Reference images:` list.

## Open
- The registry still calls the home's admin «المسؤول», where the voice guide prefers «من يدير
  الجهاز الرئيسي» on pages for everyone. That wording follow-up belongs with the app catalog,
  which uses the same word.
- The app labels quoted in the registry (for example «إرسال تقارير الأعطال إلى جهازك الرئيسي»)
  are singular in the web catalog. Moving them belongs to the app catalog, not the registry.
- The design-shell child's pull request also changes these references. Whichever merges second
  brings main in and re-renders.
