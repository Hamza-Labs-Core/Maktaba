# Story 20.5 — End-to-end smoke flows

A handful of golden user journeys; broken e2e blocks the merge.

## Acceptance criteria

- AC1. Five flows exist as Playwright specs and pass green:
  1. First-run setup (admin token, library creation, media root pick).
  2. Drop a video into the library, see it appear in the grid as
     `processing`, then `ready`, within fixture-bound wall-clock.
  3. Search "بسم الله" returns the seeded segment; click jumps to the
     correct timestamp in the player.
  4. Pause / resume a transcribe job; resume continues from the same
     segment, no duplicate output.
  5. Switch language to Arabic; UI flips to RTL with no layout
     regression (visual diff < 0.5 % pixel delta on the home and
     player screens).
- AC2. E2E suite is dockerized and runnable locally with one command
  (`make e2e`).
- AC3. Each spec records an HTML report and a video trace on failure;
  artifacts uploaded by CI.
- AC4. E2E tests are not allowed to depend on external network beyond
  the compose stack.

## Test cases

- TC1. Cold run: each flow passes against a fresh `docker compose up`.
- TC2. Re-runnable: the suite passes when run twice in a row without
  stack restart (idempotency under shared state).
- TC3. RTL diff: the visual diff harness produces ≤ 0.5 % delta on the
  baseline screens; > 0.5 % fails with the diff image attached.

## Edge cases

- EC1. Headless Chrome HLS support: tests use `chrome --enable-features
  =NativeHls` or fall back to Vidstack JS playback; both paths exercised.
- EC2. Capacitor-wrapped mobile: e2e on mobile is via Appium spot-checks,
  not Playwright; documented as a separate target.
- EC3. tvOS / Android TV — out of e2e scope for v1; they have their
  own per-platform XCUITest / Espresso suites.
