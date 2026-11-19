---
schema: 1
kind: pr
pr: 1449
title: "The four compose images held to their size budgets in CI, on both release platforms"
date: 2026-11-19
session: 395164eb-55d3-4bf9-b42a-abb243856174
agent: claude-code
---

## What happened
Story 22.03's last gap (TC3) was that `tools/image-size-guard.sh` only ran by hand. CI's new
`images` job (`.github/workflows/_images.yml`) builds api, streaming, pipeline and web on
linux/amd64 and linux/arm64 (release.yml's platforms, on native runners) and runs the guard.
CI's measurements on 2026-11-19 (amd64 / arm64): api 54.7 / 50.7, streaming 30.5 / 29.0,
pipeline 682.4 / 643.5, web 79.7 / 77.1 MiB. AC4's web target of 30 MiB was below the
`caddy:2-alpine` base alone. It is now 88 MiB, the measured size plus ~10%. The other three
images fit their spec targets, which stay.

## What we learned
- `docker image inspect --format '{{.Size}}'` overstates on a containerd image store (Docker
  Desktop's default), because it counts compressed content too, about 40% more. The guard sums
  `docker history --human=false` layer sizes instead. A non-integer line, or a docker that
  fails partway, makes the size unreadable (a failure), so a sum can never under-count into a
  pass.
- A size budget measures the whole image, so the job's path filter must cover every tree the
  Dockerfiles COPY from, not only their manifests. The first filter (Dockerfiles and lock files
  only) would have let a PR that adds a web asset go green and turn main red after it merged.
  A test now holds the `images` filter to each Dockerfile's COPY sources, including continued
  and lowercase lines.
- Measure a budget from the CI run on each platform it ships on, not from a laptop: here the
  two platforms differ by 1.5 to 39 MiB.
- `uv run --with pytest` from the repo root writes a root `uv.lock`, which `git add -A`
  committed, and the ci-paths guard then failed lint on a file no filter covers. Run it from a
  scratch directory with `--no-project`.
- This PR changed `ci.yml`, so it ran every job and hit main's own flaky unit tests twice
  (`Parental/Activity.test.tsx`, and the test-time budget of
  `TestFetchReleasesAllocatesLessThanTheList`) before going green on a rerun.

## Open
When the plugin store goes live, release will embed its key and revocation lists into api and
streaming before building. `_images.yml` builds with the committed nulls, so the gate will not
see those bytes.
