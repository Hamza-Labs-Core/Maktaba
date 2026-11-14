---
schema: 1
kind: pr
pr: 1235
title: "The compatibility contract: API levels, the client floor and the level check (58.01, home side)"
date: 2026-11-14
session: 8f145529-298a-4b09-bbbf-6a03b83bae53
agent: claude-code
---

## What happened
- #1235 merged as 7cf165d01. `GET /api/discovery/home` now carries `api_level`,
  `api_level_oldest` and `client_floor` (package `api/internal/compat`, level 1, oldest 1, an
  empty release floor), and the route is documented in the OpenAPI files for the first time.
- `compat.Gate` answers `426 client_too_old` (a new problem type) at login, refresh and the two
  pairing token routes when the `X-Maktaba-Client` version is below the floor; a missing header
  or an unreadable version passes, so the header stays a label. Each refused app and platform is
  logged once a day.
- The level check lives at `api/internal/compat/levelcheck` with `api/cmd/levelcheck`, not at
  the story's `tools/api/levelcheck`, so the existing Go job runs it. It reduces the OpenAPI
  document to its success responses and diffs them against `shared/api/levels/<level>.yaml`;
  `docs/api/levels.md` needs a `## Level N` entry for every level.
- `shared/compat/vectors.json` holds the decision's cases; Go runs them here, Kotlin and Swift
  in the app pull request (#1244).
- The first CI run failed lint: forbidigo refuses `fmt.Println` in a `cmd/` tool (write to
  `os.Stdout` with `fmt.Fprintln`), and revive refuses local names that shadow the built-ins
  `min` and `new`.

## What we learned
- A route left out of `openapi_undocumented.txt` must be removed from it when documented: the
  list may only shrink.
- The snapshot of the full response surface is about 160 KB of YAML; it is generated, never
  edited, and `-write` refuses to overwrite a snapshot the document is no longer compatible with.

## Keep doing
- Running `golangci-lint` locally before the first push of new Go packages.

## Stop doing
- Naming locals `min`, `max` or `new` in Go: revive fails the lint job on them.

## Open
- The TypeScript and BrightScript runs of the vectors wait for the apps that need them (the
  web-TV launcher, 58.09; Roku, 58.07).
