# Implementation Plan — Story 20.5 End-to-End Smoke Flows

> Companion to [story-20-05-e2e-smoke-flows.md](story-20-05-e2e-smoke-flows.md).
> Five Playwright golden flows on a dockerized stack; visual diff < 0.5 % on RTL flip;
> `make e2e` runs locally; HTML+trace artifacts on failure.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Stack | `deploy/compose/test.yml` — api + streaming + pipeline + postgres + chromadb. |
| Runner | Playwright Test on Chromium (and Webkit for the player flow). |
| Visual diff | `@playwright/test`'s `expect(page).toHaveScreenshot()` with 0.5 % pixel ratio threshold. |
| Reports | HTML report into `web/playwright-report/`; trace zip into `test-results/`. |
| Network | `--block-service-worker=false` only for warm-load tests; otherwise no external network beyond compose. |

## 1. Project layout

```
web/e2e/
├── playwright.config.ts
├── flows/
│   ├── 01_first_run_setup.e2e.spec.ts
│   ├── 02_drop_video_processing.e2e.spec.ts
│   ├── 03_search_jump_to_timestamp.e2e.spec.ts
│   ├── 04_transcribe_pause_resume.e2e.spec.ts
│   └── 05_rtl_visual_diff.e2e.spec.ts
├── helpers/
│   ├── stack.ts                    # spin-up / teardown
│   ├── fixtures.ts
│   ├── api.ts
│   └── stable-screenshot.ts        # masks dynamic regions
└── snapshots/                      # baselines committed
deploy/compose/test.yml
Makefile
apps/mobile/maestro/                 # EC2 mobile spot-check
└── flows/library_open.yaml
```

## 2. Playwright config

```ts
// web/e2e/playwright.config.ts
import { defineConfig, devices } from '@playwright/test';

export default defineConfig({
    testDir: './flows',
    timeout: 90_000,
    expect: { timeout: 10_000, toHaveScreenshot: { maxDiffPixelRatio: 0.005 } },
    fullyParallel: false,                       // shared compose stack
    retries: process.env.CI ? 1 : 0,            // AC4 retry only at e2e tier
    reporter: [['html', { open: 'never' }], ['junit', { outputFile: 'junit.xml' }]],
    use: {
        baseURL: 'http://localhost:8080',
        trace: 'retain-on-failure',
        video: 'retain-on-failure',
        screenshot: 'only-on-failure',
    },
    projects: [
        { name: 'chromium', use: { ...devices['Desktop Chrome'] } },
        { name: 'webkit',   use: { ...devices['Desktop Safari'] } },
    ],
    globalSetup: require.resolve('./helpers/global-setup'),
    globalTeardown: require.resolve('./helpers/global-teardown'),
});
```

## 3. Compose stack

```yaml
# deploy/compose/test.yml
services:
  postgres:
    image: postgres:16.4-alpine3.20
    environment: [POSTGRES_USER=test, POSTGRES_PASSWORD=test, POSTGRES_DB=maktaba_test]
    healthcheck: { test: ["CMD","pg_isready"], interval: 1s }
  chromadb:
    image: chromadb/chroma:0.5.4
  api:
    build: { context: ../../, dockerfile: api/Dockerfile }
    depends_on:
      postgres: { condition: service_healthy }
    environment:
      MAKTABA_ADMIN_TOKEN: e2e-admin-token
  streaming:
    build: { context: ../../, dockerfile: streaming/Dockerfile }
    depends_on: [api]
  pipeline:
    build: { context: ../../, dockerfile: pipeline/Dockerfile }
    depends_on: [api, chromadb]
  web:
    build: { context: ../../, dockerfile: web/Dockerfile.test }
    ports: ["8080:8080"]
    depends_on: [api, streaming]
```

## 4. Stack helper

```ts
// web/e2e/helpers/stack.ts
import { spawn } from 'node:child_process';

export async function up() {
    await run('docker', 'compose', '-f', 'deploy/compose/test.yml', 'up', '-d', '--build', '--wait');
}
export async function down() {
    await run('docker', 'compose', '-f', 'deploy/compose/test.yml', 'down', '-v');
}
async function run(...cmd: string[]) {
    return new Promise<void>((res, rej) => {
        const p = spawn(cmd[0], cmd.slice(1), { stdio: 'inherit' });
        p.on('exit', code => code === 0 ? res() : rej(new Error(cmd.join(' ') + ' failed')));
    });
}
```

`globalSetup` calls `up()` and seeds the admin token; `globalTeardown` calls `down()`.

## 5. Flow 01 — First-run setup

```ts
test('first-run setup creates admin and library', async ({ page, request }) => {
    await page.goto('/setup');
    await page.getByLabel('Admin token').fill('e2e-admin-token');
    await page.getByRole('button', { name: 'Continue' }).click();
    await page.getByLabel('Library name').fill('Library');
    await page.getByLabel('Media root').fill('/media/maktaba/library');
    await page.getByRole('button', { name: 'Create library' }).click();
    await expect(page.getByRole('heading', { name: 'Library' })).toBeVisible();
});
```

## 6. Flow 02 — Drop a video

```ts
test('dropped video transitions processing → ready', async ({ page, request }) => {
    // Drop fixture into the library volume from inside the api container.
    await execInContainer('api', 'cp', '/fixtures/arabic-lecture-60s.mp4', '/media/maktaba/library/');
    await page.goto('/library/L');
    const card = page.getByTestId('video-card-arabic-lecture-60s');
    await expect(card.getByTestId('badge-processing')).toBeVisible({ timeout: 10_000 });
    await expect(card.getByTestId('badge-ready')).toBeVisible({ timeout: 90_000 });
});
```

## 7. Flow 03 — Search jump-to-timestamp

```ts
test('search "بسم الله" jumps to correct ts', async ({ page }) => {
    await page.goto('/search');
    await page.getByPlaceholder('Search videos').fill('بسم الله');
    await expect(page.getByTestId('result-segment-0')).toBeVisible();
    await page.getByTestId('result-segment-0').click();
    const video = page.locator('video');
    await expect.poll(async () => await video.evaluate((v: HTMLVideoElement) => v.currentTime), { timeout: 5000 })
        .toBeCloseTo(seed.expected.firstHitTs, 0);     // within 1s
});
```

## 8. Flow 04 — Pause / resume transcribe

```ts
test('pause and resume transcribe job preserves progress', async ({ page, request }) => {
    const job = await request.post('/api/jobs', { data: { kind: 'transcribe', video_id: seed.video.id } }).then(r => r.json());
    await page.goto(`/jobs/${job.id}`);
    await page.getByRole('button', { name: 'Pause' }).click();
    await expect(page.getByTestId('job-state')).toHaveText('paused');
    const seenSegments = await request.get(`/api/videos/${seed.video.id}/segments`).then(r => r.json());
    await page.getByRole('button', { name: 'Resume' }).click();
    await expect(page.getByTestId('job-state')).toHaveText('done', { timeout: 90_000 });
    const final = await request.get(`/api/videos/${seed.video.id}/segments`).then(r => r.json());
    expect(final.length).toBeGreaterThanOrEqual(seenSegments.length);
    expect(uniqueByID(final).length).toBe(final.length);     // no dups
});
```

## 9. Flow 05 — RTL visual diff

```ts
test('home + player snapshot matches in EN and AR', async ({ page }) => {
    await page.goto('/');
    await stableScreenshot(page, 'home-en.png');

    await page.goto('/?lang=ar');
    await expect(page.locator('html')).toHaveAttribute('dir', 'rtl');
    await stableScreenshot(page, 'home-ar.png');

    await page.goto(`/watch/${seed.video.id}`);
    await stableScreenshot(page, 'player-ar.png');
});
```

```ts
// helpers/stable-screenshot.ts
export async function stableScreenshot(page, name) {
    await page.evaluate(() => document.fonts.ready);
    await page.locator('[data-dynamic="true"]').evaluateAll(els => els.forEach(e => (e.style.visibility = 'hidden')));
    await expect(page).toHaveScreenshot(name, {
        maxDiffPixelRatio: 0.005,
        animations: 'disabled',
        fullPage: false,
    });
}
```

## 10. Test cases

### TC1 — Cold run
`make e2e` from a clean machine. All 5 flows pass on first run.

### TC2 — Re-run idempotency
Run `pnpm playwright test` twice in a row without restarting the stack. All flows pass on second run. (Flow 02 is idempotent because dropping the same file results in `path_updated`, not `inserted`.)

### TC3 — RTL diff
Modify `home.css` to introduce a 1 px LTR-only padding. Re-run flow 05; assert it fails with diff image showing the discrepancy attached.

## 11. Edge cases summary

| Case | Source | Handling |
|---|---|---|
| EC1 Headless Chrome HLS | story | Project `chromium` uses Vidstack JS player; project `webkit` uses native HLS. Flow 03 runs in both projects. |
| EC2 Capacitor mobile | story | Maestro flow `library_open.yaml` runs separately on iOS sim; not part of `make e2e`. |
| EC3 tvOS / Android TV | story | Documented out-of-scope; their own per-platform suites. |
| Compose port collision | impl | Use random host ports; helper writes baseURL after up(). |
| Test artifact disk usage | impl | `--max-failures=5` in CI; old reports rotated by retention policy. |

## 12. Make targets

```makefile
.PHONY: e2e
e2e:
	pnpm --filter web exec playwright test
```

CI:

```yaml
- run: make e2e
- if: failure()
  uses: actions/upload-artifact@v4
  with: { name: e2e-trace, path: web/test-results }
- if: always()
  uses: actions/upload-artifact@v4
  with: { name: e2e-html-report, path: web/playwright-report }
```

## 13. Dependencies

- Story 20.1 (e2e tier).
- Story 20.2 (fixtures).
- Story 20.4 (integration backbone).
- Story 20.8 (retry-once policy).
- Epic 11 web UI; Epic 22 devops (compose).
