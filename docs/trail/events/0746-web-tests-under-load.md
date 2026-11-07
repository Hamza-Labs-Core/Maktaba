---
schema: 1
kind: pr
pr: 746
title: The web unit suite keeps passing on a loaded machine
date: 2026-11-07
session: a21a0c13-3289-4593-bc61-06da7be84c75
prs: [698]
---

## What happened
- #697: the web vitest suite failed random tests whenever the machine was busy, on CI and on a Mac, and they passed alone. Reproduced by running three full copies at once on a 28-core Mac: main failed all 15 runs (5 rounds of 3), 21 to 32 tests each, 378 in all, across 19 files.
- The causes, fixed at the source:
  - **Workers:** vitest's default is every core but one. The run is bound by the main process past about 7 workers (27 workers took 88 s, 7 took 90 s), so more only starve each other. `maxWorkers` is half the cores, 2 to 8 (`web/vitest.config.ts`).
  - **Settings rules:** each rule row built 14 `Intl.DateTimeFormat` objects per render, so one keystroke took about 600 ms and a test 12.8 s. Weekday names are kept per locale. i18n's `n()`/`formatDate()` and the queue and cluster formatters keep their formatters too (`web/src/lib/intl.ts`).
  - **axe:** the first run in a worker is several times slower than the rest (jsdom's user-agent stylesheet selectors, a cold engine): 1.5 s against 0.18 s for the same tree. `test/axe.ts` warms it in a `beforeAll` with its own budget. The components page runs axe once, on the body, instead of on the container and then the body again.
  - **Module warm-up:** App.editions and App.cases import the App and the lazy server pages once in a `beforeAll`, one at a time, instead of in the first test.
  - **Key listeners:** Review, Enrichment and the global shortcuts re-attached their window `keydown` listener in a passive effect after each render, so a key pressed between commit and effect reached the previous render's handler. `lib/useWindowKeydown` attaches once and calls the handler set at commit. Its test fails on the old pattern.
  - **Fake clock:** the HLS backoff test advanced to `wait - 1` under `shouldAdvanceTime`, and real time crossed the boundary; it now reads the reload's time from the fake clock. Download for offline waited out a real 2 s poll; it advances fake time.
  - **Waits that raced:** recovery pass, Enrichment j/k accept, Logs (the stream's read, not the page's first read) and Review's key tests (no flush or polling now).
- After, measured the same way (5 rounds × 3 copies) on each head: 2 failures in 15 runs, then 1, then 1, then 0. Each failure was a different rare race, a check made one render too early, and each was fixed (Enrichment's accept, QueueOverview's first render, the Logs stream read, Search's sid in the URL). Unloaded, the suite took 67–72 s, down from 85 s. A loaded round took 142–153 s, against 188–216 s on main.
- CI's unit job (4 vCPUs, now 2 workers) ran the web suite in 254 s, against 174–220 s on recent main runs with 3. Three workers were tried at the same ratio on the Mac (all cores but one, 3 copies, 2 rounds): faster (116–124 s a round), but 3 timeouts in 6 runs, against 0 in 15 with the cap. So CI keeps 2.

## What we learned
- Three full copies at once on the Mac reproduce CI's load failures every time. One copy alone fails nothing, or only the slowest axe tests.
- A CPU profile of the slow test (`--pool=forks --execArgv=--cpu-prof`) found the Intl cost in minutes. Most "slow test" time was page code, not the test.
- Importing modules in parallel in a warm-up can expose a half-evaluated module: `clusterSocket` was undefined in CI's run of the parallel version.
- `shouldAdvanceTime` fake timers make any "not yet at T−1" assertion racy. Read times off the fake clock instead.

## Keep doing
- Measure a flaky suite under real load (concurrent copies) before and after; report both counts.
- Use `useWindowKeydown` for any window key handler that reads render state.
- Use `lib/intl` for formatters made in render.

## Open
- Rare races of one shape are probably left: a check made one render too early, such as a `getBy`/`expect` right after a `findBy` for something else, or a `waitFor` on "any call" before reading one particular call. Listed on #697.
- BigScreen still re-attaches its key listener. It reads `defaultPrevented`, so it depends on listener order and was left alone.
