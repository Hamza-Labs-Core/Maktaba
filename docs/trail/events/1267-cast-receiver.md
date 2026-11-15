---
schema: 1
kind: pr
pr: 1267
title: "Maktaba's Cast receiver at cast.maktaba.tv (story 58.07)"
date: 2026-11-15
session: 43ed6e31-0740-4d65-b657-61c1a1d3ba1b
agent: claude-code
---

## What happened
- #1267 merged as d8f0de32c, completing story 58.07 after the cast grant in #1261. It added:
  - `web/packages/castreceiver`, a CAF Web Receiver bundled with esbuild, with a loader at
    `/r/` that picks the build for the sender's `api_level` from `/r/builds.json`;
  - the `cast-receiver` CI job (Vitest, then Playwright with dark EN and AR references);
  - `cast-receiver-release.yml`, which deploys only once `CAST_RECEIVER_LIVE` is `true`.
- `1295-integration-tier-time.md` names #1267 only for the integration time budget. This event
  records the receiver's own lessons, which the 58.07 session drafted but never filed.
- #1368 later made the release's first run work before cast.maktaba.tv exists.

## What we learned
- esbuild's `es2019` target drops `import.meta` silently: the build passes and the receiver
  breaks at run time. The receiver resolves its own files through `import.meta`, so
  `scripts/build.mjs` targets `es2020` / `chrome80`, which every Cast device runs.
- A `package-lock.json` generated on the Mac fails `npm ci` in the Playwright container, which
  runs another npm version. Generate the lock inside the CI image, the same way the visual
  references are rendered there (`scripts/e2e-update.sh`).
- Re-running an older run in the same concurrency group cancels the newer run, so re-run only
  the current head's run.
- Two pull requests that add a job at the same spot in `ci.yml` always conflict on the output,
  job and merge-gate lines. Keep both sides, then let `tools/dev/merge-main.sh` finish the
  CHANGELOGs.

## Keep doing
- Render visual references and generate lockfiles in the CI image, never on the Mac.
- Before arming a watcher, check that the pull request is still mergeable against main.

## Open
- Owner actions: the Cast console registration ($5) at `https://cast.maktaba.tv/r/`,
  `receiver_app_id` in `shared/cast/app.json`, the Cloudflare token and the custom domain,
  then `CAST_RECEIVER_LIVE=true`, the test devices, and the T05 checklist in
  `docs/stores/cast.md`.
