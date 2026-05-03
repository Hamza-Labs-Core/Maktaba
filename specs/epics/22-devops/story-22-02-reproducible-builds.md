# Story 22.2 — Reproducible builds and artifacts

Every build is byte-stable given the same inputs. Artifacts are signed.

## Acceptance criteria

- AC1. Go binaries built with `-trimpath -ldflags='-buildid='` and
  vendored deps; `sha256` of the resulting binaries is stable across
  two builds on the same OS/arch with the same Go version.
- AC2. Container images use a deterministic builder (`ko`, `kaniko`,
  or `docker buildx --provenance`) and pinned base images by digest.
- AC3. Python pipeline ships as a pinned `uv` lockfile; `uv lock`
  drift fails CI.
- AC4. Web bundle uses pinned `pnpm` lockfile; `vite build` produces
  byte-stable output (sorted file order, deterministic hash).
- AC5. All release artifacts (binaries, images, web tarball, mobile/
  desktop installers) are signed: cosign for images, minisign for
  binaries; signatures published alongside the artifacts.

## Test cases

- TC1. Reproducibility: build twice on the same runner; sha256 sums
  match for every Go binary and the web bundle.
- TC2. Signature verification: `cosign verify` and `minisign -V`
  succeed against the published signatures and the maintainer public
  key.
- TC3. Lockfile drift: a deliberate edit to `pyproject.toml` without
  re-running `uv lock` fails CI.

## Edge cases

- EC1. Python C-extension wheels — built via `cibuildwheel` and
  reproducible per-platform; documented per-platform stability.
- EC2. Native iOS/Android signing — release builds require maintainer-
  held signing material; CI uses dev signing only.
- EC3. Reproducibility under different timezones / locales — builds
  pin `SOURCE_DATE_EPOCH`, `TZ=UTC`, `LANG=C.UTF-8`.
