---
schema: 1
kind: pr
pr: 1296
title: "tools/ci/tests fails when an image misses a file the web typecheck reads"
date: 2026-11-14
session: 4d82a426-89c7-4209-b1b8-638a046330d9
agent: claude-code
---

## What happened
- #1296 merged as eb69f986a. `tools/ci/tests/test_docker_typecheck_inputs.py` reads
  `web/tsconfig.json` (JSONC), walks the included files' relative, JSON and `paths`-alias imports
  transitively, and requires each file outside `web/` to be copied to `/src/<repo path>` in the
  pnpm build stage of both `web/Dockerfile` and `cloud/Dockerfile`. It runs in the lint job's
  `tools/ci/tests` step on every pull request.
- Proved both ways: green on main, red when `COPY shared/compat` is dropped from either image
  (an in-repo negative control repeats it), and red on the 58.11 branch (#1290) when its
  caps-vectors line is dropped.

## What we learned
- `web/tsconfig.json` has one alias that leaves `web/`: `@maktaba/flowchart` points into
  `tools/plugin-sdk`. The guard resolves aliases and asserts that none leaving `web/` is a wildcard.
- A JSONC stripper has to read strings: `"src/*"` contains a `/*`.

## Keep doing
- A file the web typecheck reaches outside `web/` gets a COPY line in both images in the same
  pull request; the guard names the line to add.

## Open
- The guard reads only `tsc`'s inputs. Vite build-time reads (`?raw`, `new URL(…, import.meta.url)`)
  outside `web/` are not covered.
