# Implementation Plan — Story 18.6 Client-Perceived Performance

> Companion to [story-18-06-client-perceived-performance.md](story-18-06-client-perceived-performance.md).
> Lighthouse + Playwright budgets in CI; assert LCP, TBT, TTFF, search-keystroke-to-paint.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Lighthouse | `lighthouse-ci` (LHCI) against the production build served by `vite preview`. |
| Player measurement | Playwright fixture intercepts `play` event → first `timeupdate` callback. |
| Search keystroke | Playwright; types char-by-char with 250 ms debounce mock. |
| Native budgets | Capacitor + iOS-Safari mobile measurement spot-checked monthly via Maestro flow; not blocking CI. |
| Out of scope | Bundle-size budgets (separate story 11.x); CDN edge perf (no CDN in v1). |

## 1. Project layout

```
web/
├── lighthouse/
│   ├── lighthouserc.cjs          # LHCI config
│   ├── budget.json               # LCP/TBT thresholds
│   └── ar.lighthouserc.cjs       # lang=ar variant
├── tests/perf/
│   ├── time_to_first_frame.spec.ts
│   ├── search_keystroke.spec.ts
│   └── helpers.ts
└── package.json                  # scripts: perf:lh, perf:player

apps/mobile/maestro/
└── flows/cold-launch.yaml
```

## 2. Lighthouse CI config

```js
// web/lighthouse/lighthouserc.cjs
module.exports = {
  ci: {
    collect: {
      startServerCommand: 'pnpm --filter web preview --port 4173',
      url: ['http://localhost:4173/?lang=en', 'http://localhost:4173/?lang=ar'],
      numberOfRuns: 3,
      settings: {
        preset: 'desktop',
        throttlingMethod: 'simulate',
        throttling: {
          // 4G profile per AC1
          rttMs: 150, throughputKbps: 1638.4, cpuSlowdownMultiplier: 4,
        },
        formFactor: 'mobile',
      },
    },
    assert: {
      assertions: {
        'largest-contentful-paint': ['error', { maxNumericValue: 2000 }],   // AC1 cold
        'total-blocking-time':       ['error', { maxNumericValue: 200 }],   // AC1
        'speed-index':               ['warn',  { maxNumericValue: 3500 }],
      },
    },
    upload: { target: 'filesystem', outputDir: '.lighthouseci' },
  },
};
```

Warm budget AC2 is verified by a second LHCI run with `--collect.staticDistDir` set after a warming visit (LHCI persists no SW state across runs by default, so we use a custom flag `--collect.psiStrategy=warm` via a Puppeteer setup script).

## 3. Time-to-first-frame test

```ts
// web/tests/perf/time_to_first_frame.spec.ts
import { test, expect } from '@playwright/test';

async function measureTTFF(page, videoUrl: string) {
    await page.goto(`/watch/${encodeURIComponent(videoUrl)}`);
    return await page.evaluate(() => {
        return new Promise<number>((resolve) => {
            const v = document.querySelector('video') as HTMLVideoElement;
            const t0 = performance.now();
            const onTU = () => { v.removeEventListener('timeupdate', onTU);
                                 resolve(performance.now() - t0); };
            v.addEventListener('timeupdate', onTU);
            v.play();
        });
    });
}

test('TTFF warm path p95 <= 1.5s', async ({ page }) => {
    // Warm: pre-prime probe + segment cache via API.
    await fetch('/admin/perf/warm-segment?id=fixture-1');
    const samples = [];
    for (let i = 0; i < 5; i++) samples.push(await measureTTFF(page, 'fixture-1'));
    samples.sort((a, b) => a - b);
    const p95 = samples[Math.floor(samples.length * 0.95)] ?? samples[samples.length - 1];
    expect(p95).toBeLessThanOrEqual(1500);
});

test('TTFF cold transcode p95 <= 3.5s', async ({ page }) => {
    await fetch('/admin/cache/segments/flush?id=fixture-2');
    const ttff = await measureTTFF(page, 'fixture-2');
    expect(ttff).toBeLessThanOrEqual(3500);
});
```

## 4. Search keystroke test

```ts
// web/tests/perf/search_keystroke.spec.ts
test('search keystroke p95 <= 750ms (warm)', async ({ page }) => {
    await page.goto('/search');
    let requestFiredCount = 0;
    page.on('request', r => { if (r.url().includes('/api/search')) requestFiredCount++; });

    const input = page.getByTestId('search-input');
    await input.focus();
    const t0 = Date.now();
    await input.pressSequentially('بسم الله', { delay: 50 });

    await page.waitForResponse(r => r.url().includes('/api/search') && r.status() === 200);
    await page.getByTestId('search-results').waitFor();
    const elapsed = Date.now() - t0;

    expect(requestFiredCount).toBe(1);                  // EC: single fire after debounce
    expect(elapsed).toBeLessThanOrEqual(750);
});
```

## 5. RTL Lighthouse variant

```js
// web/lighthouse/ar.lighthouserc.cjs
module.exports = {
  ci: {
    collect: {
      url: ['http://localhost:4173/?lang=ar'],
      numberOfRuns: 3,
    },
    assert: {
      assertions: {
        'largest-contentful-paint': ['error', { maxNumericValue: 2000 }],
        'total-blocking-time':       ['error', { maxNumericValue: 200 }],
      },
    },
  },
};
```

CI runs both files; either failure blocks merge.

## 6. Per-browser tracking

Playwright `playwright.config.ts`:

```ts
projects: [
  { name: 'chromium-vidstack',  use: { ...devices['Desktop Chrome'] } },
  { name: 'webkit-hls-native',  use: { ...devices['Desktop Safari'] } },
],
```

Both projects run `time_to_first_frame.spec.ts`; budgets recorded per project in the JUnit report.

## 7. Capacitor/native spot-check (EC3)

```yaml
# apps/mobile/maestro/flows/cold-launch.yaml
appId: com.maktaba.mobile
---
- launchApp:
    clearState: true
- assertVisible: "Library"
- evalScript: |
    output.coldLaunchMs = epoch_now() - launchTimestamp
- assertEqual:
    expected: true
    actual: ${output.coldLaunchMs <= 3000}
```

Runs nightly on iOS/Android simulators; informational, not blocking.

## 8. Test cases

### TC1 — Lighthouse CI
PR job runs `lhci autorun --config=lighthouse/lighthouserc.cjs && lhci autorun --config=lighthouse/ar.lighthouserc.cjs`. Median LCP/TBT must hit budget.

### TC2 — Playwright TTFF
`web/tests/perf/time_to_first_frame.spec.ts` — warm and cold; runs in `chromium-vidstack` and `webkit-hls-native` projects.

### TC3 — Synthetic search
`web/tests/perf/search_keystroke.spec.ts` — assert single network request and response paint within 750 ms.

## 9. Edge cases

| Case | Source | Handling |
|---|---|---|
| EC1 RTL LCP | story | Lighthouse runs `lang=ar` and `lang=en`; both must pass. |
| EC2 Safari HLS native | story | TTFF tracked per project; native HLS path measured via `loadedmetadata` since `timeupdate` fires later in Safari. |
| EC3 Capacitor budget | story | Maestro flow on simulator; budget tracked separately, doesn't block PR merge. |
| Lighthouse flake | impl | `numberOfRuns: 3`, take median. |
| Service-worker dirty state | impl | Each Playwright spec calls `await context.unregisterAll()` before navigation. |

## 10. Helpers

```ts
// web/tests/perf/helpers.ts
export async function p95(samples: number[]) {
    const sorted = [...samples].sort((a, b) => a - b);
    const idx = Math.min(sorted.length - 1, Math.floor(sorted.length * 0.95));
    return sorted[idx];
}
```

## 11. CI integration

- `web/package.json` adds `"perf:lh"` and `"perf:player"` scripts.
- `.github/workflows/web-perf.yml` runs both on every PR touching `web/**`.
- Artifacts: LHCI HTML report, Playwright trace zip, JUnit per-budget breakdown.

## 12. Dependencies

- Story 18.2 (warm-search budget; this story validates the round-trip including paint).
- Story 18.3 (segment cache flush admin endpoint for cold TTFF).
- Story 11.x (web build pipeline; vite preview).
- Epic 17 design system (LCP element is the page hero; tracked element class `lcp-target` makes assertions stable).
