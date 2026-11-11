---
schema: 1
kind: pr
pr: 1162
title: "Story 60.06, registry half: the problem-type registry the API sends from"
date: 2026-11-11
session: 8377b6f8-6210-4839-9195-25422fd07a3d
agent: claude-code
---

## What happened
- Merged as 28c7f9f608b75658747606de3a3c79ae6001db84 (head f13f6218626b4e6680862d9c9cb6af9a84ca9c98).
- `shared/api/problems.json` holds 288 entries, each with English and Arabic text:
  - 109 slugs come from today's code: 95 literals and the 18 constants, 4 of which overlap;
  - 177 more come from the codes the seven runtime call sites receive (197 site and code pairs);
  - 2 are streaming slugs that the apps compare.
- Five parallel writers drafted the copy from each handler. Each read the code behind its slugs, and validated its own batch.
- `httperror.Write` now checks every type:
  - a test binary appends an unregistered type to `MAKTABA_UNREGISTERED_PROBLEMS`, and the unit job fails when that file has anything in it. A positive control proves the check fails and names the slug;
  - production logs `problem_type_unregistered` and sends the generic type for the status.
- Every literal type is now `Type` or a constant, and the runtime sites call `TypeForCode`. `literal_test.go` scans `api/` and `cloud/` for leftovers.
- Android, Apple and web tests check the slugs and codes each app compares. A test also checks the OpenAPI `type` examples.
- The new maktaba.dev guide is `contributing/problem-types` (en and ar).
- The first CI run failed `lint / repo` on `tools/ci/tests/test_no_head_pipes.py`: one new script piped into `head`, and the other into `grep -q`. Fixed with `sed -n 1,40p` and a `case` statement.

## What we learned
- The story estimated 120 literal slugs. That count was repo-wide: the API's non-test Go has 95.
- The runtime sites are the bulk of the registry. plugins/admin alone receives 114 codes, mostly relayed from the plugin store (`store/web/`, not `cloud/`) and the supervisor.
- Some relayed codes can be any string (a store `error`, an OS error text in the packs relay). Production now sends these as their status's generic type instead of a dead link.
- Word review's undo built its slug from the code as is (`not_active`). It now uses the hyphenated entry. The `code` field is unchanged, and that is what the web reads.
- Two problems carried an empty `Type` (queues: no secrets store). Counting an empty type as unregistered found them.
- The affected-tests gate flags every handler where a literal was swapped for the same string. A `No-Test-Change` trailer saying why is the right answer, because the registry check runs through those handlers' existing tests.
- Local golangci-lint is v1 and CI's is v2, so local lint results on `api/` mean little. Rely on CI's lint job.

## Keep doing
- Read each handler before writing problem text: several generic types are sent with odd statuses (see Open).
- Count a type as code in the Latin-share check when it sits in backquotes.

## Stop doing
- Piping into `head` or `grep -q` in `tools/` scripts: `test_no_head_pipes.py` refuses it.

## Open
- The site pages generator (`problems.mjs`, T09 to T11) waits for the design approval.
- No install troubleshooting anchors exist yet, so every `install` field is null.
- The streaming service sends `maktaba.invalid/problems/*` slugs outside the registry. Only the two the apps compare are registered.
- Odd sends found while writing the copy:
  - `TypeForbidden` sent with 401 (libraries, playqueue, flags);
  - `TypeConflict` sent with 410 (discovery device);
  - `invalid-credentials` also used for a refused cloud link (422);
  - `review.writeEditErr` batch undo answers 409 for a missing edit;
  - `token-expired` is declared but never sent.
- The plugin check's `*pl.Problem` is not recognised by the components handler's `fail`, so `plugin_revoked` and its siblings appear to answer 500 through `/api/components`. Traced, not confirmed by a run.
