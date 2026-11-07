---
schema: 1
kind: pr
pr: 732
title: Epic 53 on the Apple apps — Remote diagnostics, crash reports to the home, Report a problem
date: 2026-11-06
session: 03d72f4f-f05c-4d72-897b-0e3d0c10f530
---

## What happened
- Maktaba Remote reads the home's Errors, Logs, Overview and Storage dashboards and the problem-report inbox through the web's routes (#688, #689, #691).
- Every Apple app asks once per home whether to send crash reports. The switch stays off until the person says yes. MetricKit crashes (iPhone, iPad, Mac) and PLCrashReporter crashes (Apple TV, which has no MetricKit) go to the home as scrubbed Sentry envelopes.
- Report a problem previews what it sends, and a screenshot is sent only when it is ticked.
- `docs/app-privacy.md` keeps the store answers.
- The Remote's UI tests did not run in CI at all. A new `remote-ui-tests` job runs the diagnostics UI tests and keeps their screenshots for maktaba.dev.

## What we learned
- A one-time question after sign-in would block every existing UI test that signs in. It shows in a UI-test launch (one carrying `-maktaba-permissions-fake`, `-uitest-reset` or `-maktaba-session-reset`) only when that launch passes `-maktaba-crash-question`.
- MetricKit's call stack tree puts the crashing frame at the root and the callers in each frame's first `subFrames`. Sentry frames are oldest first, and the home groups on the last own frame, so the walk is reversed.
- `#require` cannot be nested inside another `#require` (recursive macro expansion). Swift Testing suites run in parallel with each other even when each is `.serialized`. Suites that share one URLProtocol stub go under one serialized parent suite.
- MaktabaKit's String Catalog is not fully sorted (hand edits). A tool that re-sorts it rewrites 300 lines, so new keys are inserted in place.
- The shared redaction does not mask a bare `token=` in free text, only in a URL query. Tests use a bearer header or an email.

- The web's `Logs.test.tsx` failed on CI with `'undefined'` for the files' url. It waited for any call to `get`, which the index probe makes first, then looked for the `/stream` call that comes after the probe's answer. It now waits for the `/stream` call itself. A "wait for any call" before reading a specific one is the same race as #698's.

## Open
- Android (Remote screens, crash capture, forms), the desktop apps, the nightly Mac log bench and the Python logger bench follow in their own PRs.
- 53.16's "Also send to Maktaba" box needs the home's sharing switch.
