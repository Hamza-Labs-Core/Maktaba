# Story 18.6 — Client-perceived performance

Time-to-interactive and player-start budgets for the web client; native
apps inherit and are spot-checked.

## Acceptance criteria

- AC1. PWA cold load (no service worker cache) — Largest Contentful Paint
  ≤ 2.0 s on a simulated 4G profile, Total Blocking Time ≤ 200 ms.
- AC2. PWA warm load (SW cache hit) — LCP ≤ 600 ms.
- AC3. Time-to-first-frame in the player from "tap play" to first decoded
  frame — p95 ≤ 1.5 s warm, ≤ 3.5 s cold transcode.
- AC4. Search keystroke-to-results latency (with debounced input, 250 ms)
  end-to-end p95 ≤ 750 ms (warm path; consistent with Story 18.2 warm
  budget plus debounce + paint).

## Test cases

- TC1. Lighthouse CI runs on the production build; LCP/TBT thresholds
  fail the build on regression.
- TC2. Playwright records `play` → first `timeupdate` event for a warm
  and a cold session and asserts both budgets.
- TC3. Synthetic search: type "بسم الله" character by character with a
  250 ms debounce; assert the request fires once and the response paints
  within budget.

## Edge cases

- EC1. RTL paint regressions: Lighthouse runs once with `lang=ar` and
  once with `lang=en`; both must hit budget.
- EC2. Slow video element initialization on Safari (HLS native) vs.
  Vidstack on Chrome — budgets are tracked per browser.
- EC3. Capacitor WebView vs. mobile Safari: the mobile budget is the
  Capacitor measurement, not browser Safari.
