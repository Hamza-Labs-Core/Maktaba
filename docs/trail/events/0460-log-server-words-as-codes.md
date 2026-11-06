---
schema: 1
kind: pr
pr: 460
title: Server texts for admin screens go out as codes
date: 2026-10-31
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
Server texts that the admin screens show now go out as a code plus params beside the English:
every TV check (`Check.Code`/`Params`, 48 codes, web key `tvchanges.value.<code>`), the failing move (`report_code`) and the home's `roles_reason_code`.
Web, Android and Apple render the code and fall back to the English.
Triggers and check names got words on Android and iPhone; the iPhone now uses the supervisor's `detail_code`.
The PR was left for the owner's session to merge.

## What we learned
- Decided: params are strings (`map[string]string`), already formatted by Go, so no client has to format numbers. The English `value` stays for older apps.
- Decided: a settling position check becomes `<code>_settling`; a budget with channels waiting becomes `<code>_rolling`.
- Left alone (see the PR): the streaming binary's own check values, update `error.detail` and step details (machine-written), `copy.detail`, `last_check_error`, events' `message`.
- `xcodebuild` needs `TEST_RUNNER_MAKTABA_SHOTS` to pass `MAKTABA_SHOTS` to UI tests.
- The Android Remote had flavors then: run `:remote:testPhoneDebugUnitTest`, not `testDebugUnitTest` (no longer true after #517 removed the flavours).
- The web uses pnpm (`pnpm install --frozen-lockfile`); `npm ci` fails because there is no npm lockfile.
- Used a throwaway simulator `maktaba-codes-throwaway` (iOS 27.0), deleted afterwards.

## Keep doing
- New check codes need en/ar words in `web/src/i18n`: `TestEveryCheckCodeHasItsWords` fails when a code in `verify.go`/`streamverify.go` has none.
- TV-staging code is additive only: `update-rehearsal` must pass on the exact commit before a release carries it.
