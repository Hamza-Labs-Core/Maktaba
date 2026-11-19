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
Story 22.03's last gap (TC3): `tools/image-size-guard.sh` ran by hand only. CI's new `images`
job (`.github/workflows/_images.yml`) builds api, streaming, pipeline and web on linux/amd64 and
linux/arm64 (release.yml's platforms, native runners) and runs the guard. Measured by CI on
2026-11-19 (amd64 / arm64): api 54.7 / 50.7, streaming 30.5 / 29.0, pipeline 682.4 / 643.5,
web 79.7 / 77.1 MiB. The owner had AC4's web target (30 MiB, an unexplained round number below
the `caddy:2-alpine` base alone) replaced by the measured size plus ~10%: 88 MiB. The others
kept their spec targets.

## What we learned
- `docker image inspect --format '{{.Size}}'` overstates on a containerd image store (Docker
  Desktop's default; it counts compressed content too, ~40% more). The guard sums `docker
  history --human=false` layer sizes instead, and it treats any non-integer line or a docker
  that fails partway as unreadable (a failure), so a size can never under-count into a pass.
- A size budget measures the whole image, so the job's path filter has to cover every tree
  the Dockerfiles COPY from, not only their manifests. The first filter (Dockerfiles and lock
  files only) would have let a PR adding a web asset go green and turned main red after it.
  A test now holds the `images` filter to each Dockerfile's COPY sources.
- Measure a budget from the CI run on each platform it ships on, not a laptop: arm64 and amd64
  differ by 3-40 MiB here.
- `uv run --with pytest` from the repo root writes a root `uv.lock`, and `git add -A` committed
  it; the ci-paths guard failed lint on the untracked-by-any-filter file. Run it from the
  scratchpad with `--no-project`.

## Open
When the plugin store goes live, release embeds its key and revocation lists into api and
streaming before building; `_images.yml` builds with the committed nulls and won't see those
bytes.
