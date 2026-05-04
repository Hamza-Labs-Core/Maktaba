# Implementation Plan — Story 22.2 Reproducible builds and artifacts

> Companion to [story-22-02-reproducible-builds.md](story-22-02-reproducible-builds.md).
> Story states *what* and *why*; this plan states *how*.
> Sits underneath the build-artifacts gate from
> [Story 22.1](plan-22-01-ci-pipeline.md) and the release flow from
> [Story 22.5](plan-22-05-release-management.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Go reproducibility | `-trimpath -ldflags='-buildid= -X main.version=…'`, vendored deps under `api/vendor/` and `streaming/vendor/`, `GOFLAGS=-mod=vendor`. `SOURCE_DATE_EPOCH` exported by the build script. |
| Python reproducibility | `uv lock` + `uv export --frozen`; `cibuildwheel` for native-extension wheels. |
| Web reproducibility | `pnpm` lockfile + `vite build` with `rollup.output.entryFileNames` deterministic; sorted globs. |
| Container reproducibility | `ko` for the three Go images and the web image (Caddy-fronted static), `docker buildx --provenance=true --sbom=true` for the Python pipeline image (because of native deps), pinned base images by digest. |
| Signing | `cosign sign` with keyless OIDC for images, `minisign` for binaries; pubkey published in `SECURITY.md`. |
| Out of scope | CVE/SBOM gates (Story 23.7); release publishing (Story 22.5 wires this plan's outputs to `gh release`); mobile/desktop signing (Story 22.7). |

## 1. Architecture diagram

```
            ┌─────────────────┐
            │ tools/build.sh  │ ◄── invoked by `make build`, by CI matrix,
            └────────┬────────┘     by Story 22.5's release workflow.
                     │
       ┌─────────────┼─────────────┬───────────────┐
       ▼             ▼             ▼               ▼
   go build       uv build     vite build       ko build
   -trimpath      --frozen     deterministic     pinned
   vendored       cibuildwheel  rollup output     digest
       │             │             │               │
       ▼             ▼             ▼               ▼
   sha256sum     wheel sha   web/dist sha       image sha
       │             │             │               │
       └─────────────┴──────┬──────┴───────────────┘
                            ▼
                 ┌──────────────────────┐
                 │ checksums.txt        │ ◄── signed by minisign
                 │ checksums.txt.minisig│
                 └──────────────────────┘
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `tools/build.sh` | Single bash entry that all build paths run through. Sets `SOURCE_DATE_EPOCH`, `TZ=UTC`, `LANG=C.UTF-8`, dispatches to the right tool. |
| `tools/sign.sh` | `cosign sign` + `minisign -S` wrapper; reads keys from env. |
| `tools/verify-reproducibility.sh` | Build twice in two temp dirs, diff the sha256s; used in CI by TC1. |
| `tools/.go-build-flags` | Single source for `-trimpath -ldflags '-buildid= -s -w -X …'`. |
| `api/vendor/`, `streaming/vendor/` | `go mod vendor` output, checked in. |
| `pipeline/uv.lock` | uv lockfile, checked in. |
| `web/pnpm-lock.yaml` | Already present; locked for byte-stable builds. |
| `.github/workflows/_reproducibility-check.yml` | CI job for TC1. |
| `SECURITY.md` (extended) | Maintainer pubkeys for cosign + minisign. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `Makefile` | `build` delegates to `tools/build.sh`. |
| `api/Dockerfile`, `streaming/Dockerfile`, `web/Dockerfile` | Replaced with `ko` config (`.ko.yaml`) — build images via `ko build`. |
| `pipeline/Dockerfile` | Pinned base by digest, `BUILDKIT_INLINE_CACHE=0`, `--provenance=true`. |
| `web/vite.config.ts` | Sorted output, fixed chunk-name template. |
| `pipeline/pyproject.toml` | `[build-system] requires = …`, pinned. |

### 2.3 The build script

`tools/build.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail

# Reproducibility envelope (EC3): a build at midnight in Tokyo and a build
# at noon in NYC must produce byte-identical artifacts.
export SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH:-$(git log -1 --pretty=%ct)}"
export TZ=UTC
export LANG=C.UTF-8
export GOFLAGS="${GOFLAGS:-} -mod=vendor"

GO_FLAGS=$(cat tools/.go-build-flags)
VERSION=$(git describe --tags --dirty --always)
GIT_SHA=$(git rev-parse HEAD)
LDFLAGS="-buildid= -s -w \
  -X maktaba/internal/version.Tag=${VERSION} \
  -X maktaba/internal/version.Sha=${GIT_SHA} \
  -X maktaba/internal/version.BuildTime=${SOURCE_DATE_EPOCH}"

case "${1:-all}" in
  api)
    cd api && go build -trimpath -ldflags="${LDFLAGS}" -o bin/maktaba-api ./cmd/api
    ;;
  streaming)
    cd streaming && go build -trimpath -ldflags="${LDFLAGS}" -o bin/maktaba-streaming ./cmd/streaming
    ;;
  pipeline)
    cd pipeline && uv build --wheel --no-sources
    ;;
  web)
    cd web && pnpm build --emptyOutDir
    # Strip non-deterministic banner timestamps if present.
    find dist -name '*.js' -o -name '*.css' \
      | xargs -I{} sh -c "sed -i 's/Built on .*/Built on REPRODUCIBLE/' {}"
    ;;
  images)
    KO_DOCKER_REPO=ghcr.io/maktaba ko build --bare --sbom=spdx --tags="${VERSION}" \
      ./api/cmd/api ./streaming/cmd/streaming
    docker buildx build pipeline \
      --provenance=true --sbom=true \
      --tag="ghcr.io/maktaba/pipeline:${VERSION}" \
      --build-arg SOURCE_DATE_EPOCH \
      --build-arg PYTHON_BASE_DIGEST=$(cat pipeline/.base-digest)
    ;;
  all)
    "$0" api && "$0" streaming && "$0" pipeline && "$0" web && "$0" images
    ;;
esac
```

### 2.4 Web determinism

`web/vite.config.ts`:

```ts
export default defineConfig({
  build: {
    rollupOptions: {
      output: {
        // entry/asset names without timestamp; chunks ordered.
        entryFileNames: 'assets/[name]-[hash].js',
        chunkFileNames: 'assets/[name]-[hash].js',
        assetFileNames: 'assets/[name]-[hash][extname]',
      },
    },
  },
  // Ensure CSS module hashes are content-derived only.
  css: { devSourcemap: false, modules: { generateScopedName: '[hash:base64:6]' } },
});
```

A `vite-plugin-sort-output` hook walks `bundle` and re-emits entries in
sorted-key order so `Object.entries` order isn't determined by file
discovery (filesystem nondeterminism).

### 2.5 Container determinism

Go images via `ko`:

```yaml
# .ko.yaml
defaultBaseImage: cgr.dev/chainguard/static:latest@sha256:<DIGEST>
defaultPlatforms: [linux/amd64, linux/arm64]
builds:
- id: api
  dir: ./api
  main: ./cmd/api
  ldflags: ['-buildid=', '-s', '-w', '-X maktaba/internal/version.Tag={{.Env.VERSION}}']
  env: [CGO_ENABLED=0, GOOS=linux]
- id: streaming
  dir: ./streaming
  main: ./cmd/streaming
  ldflags: ['-buildid=', '-s', '-w', '-X maktaba/internal/version.Tag={{.Env.VERSION}}']
  env: [CGO_ENABLED=0, GOOS=linux]
```

`ko` produces byte-stable OCI images by construction (sorted layers,
zeroed timestamps from `SOURCE_DATE_EPOCH`).

Python image — `pipeline/Dockerfile`:

```dockerfile
ARG PYTHON_BASE_DIGEST
FROM python:3.12-slim-bookworm@${PYTHON_BASE_DIGEST}
ARG SOURCE_DATE_EPOCH
ENV SOURCE_DATE_EPOCH=${SOURCE_DATE_EPOCH} TZ=UTC LANG=C.UTF-8 \
    UV_FROZEN=1 UV_NO_PROGRESS=1 UV_COMPILE_BYTECODE=1
WORKDIR /app
COPY pipeline/uv.lock pipeline/pyproject.toml ./
RUN --mount=type=cache,target=/root/.cache/uv \
    uv sync --frozen --no-dev
COPY pipeline/src ./src
ENTRYPOINT ["uv", "run", "maktaba-pipeline"]
```

`UV_COMPILE_BYTECODE=1` builds .pyc on install with deterministic
`SOURCE_DATE_EPOCH` so the layer hash is stable. Base image is pinned by
digest, captured in `pipeline/.base-digest` and bumped via Renovate.

### 2.6 Python wheels (cibuildwheel)

`pipeline/cibuildwheel.toml`:

```toml
[tool.cibuildwheel]
build = "cp312-*"
skip = ["*-musllinux_*", "*-win32"]
build-frontend = "build"
test-command = "pytest -m unit {project}/tests"
environment = { SOURCE_DATE_EPOCH = "$SOURCE_DATE_EPOCH" }
```

Per-platform wheels are reproducible *within* a platform (linux glibc,
macos arm64, etc.); cross-platform identity isn't a goal. Platform-band
hashes are stable across runs on the same platform, which is what AC-1
requires.

### 2.7 Lockfile drift gate

CI step in the lint gate (Story 22.1):

```bash
# Python
uv lock --check
# Web
pnpm install --frozen-lockfile --lockfile-only --reporter=silent && \
  git diff --exit-code web/pnpm-lock.yaml
# Go
go mod verify && go mod tidy -diff
```

Any drift exits non-zero (TC3).

### 2.8 Signing

`tools/sign.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail

# Binaries: minisign with maintainer key (key file in CI secrets).
echo "$MAKTABA_MINISIGN_KEY" > /tmp/minisign.key
chmod 600 /tmp/minisign.key
for f in api/bin/maktaba-api streaming/bin/maktaba-streaming; do
  minisign -S -s /tmp/minisign.key -m "$f"
done

# Container images: cosign keyless via OIDC.
for img in ghcr.io/maktaba/api:${VERSION} ghcr.io/maktaba/streaming:${VERSION} \
           ghcr.io/maktaba/pipeline:${VERSION} ghcr.io/maktaba/web:${VERSION}; do
  COSIGN_EXPERIMENTAL=1 cosign sign --yes "$img"
done

# Web bundle tarball.
( cd web && tar --sort=name --mtime=@${SOURCE_DATE_EPOCH} \
    --owner=0 --group=0 --numeric-owner \
    -czf "../dist/web-${VERSION}.tar.gz" dist )
minisign -S -s /tmp/minisign.key -m "dist/web-${VERSION}.tar.gz"

shred -u /tmp/minisign.key
```

`tar --sort=name --mtime=@$SDE` is the deterministic tarball recipe (web
bundle is multi-file; not byte-stable without these flags).

### 2.9 Reproducibility self-check

`tools/verify-reproducibility.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail

build_to() {
  local dir="$1"
  cp -r . "$dir"
  ( cd "$dir" && tools/build.sh all )
}

A=$(mktemp -d); B=$(mktemp -d)
trap 'rm -rf "$A" "$B"' EXIT

build_to "$A"
sleep 2  # Ensure mtime drift cannot hide reproducibility bugs.
build_to "$B"

diff <(cd "$A" && find api/bin streaming/bin web/dist -type f -exec sha256sum {} \; | sort) \
     <(cd "$B" && find api/bin streaming/bin web/dist -type f -exec sha256sum {} \; | sort)
```

Wired in `.github/workflows/_reproducibility-check.yml`, run weekly and
on demand on `release/*` branches.

## 3. Test plan

### 3.1 Reproducibility

| Test | What it pins |
|---|---|
| `TestGoBinariesReproducible` (TC1) | `verify-reproducibility.sh` returns exit 0; sha256 of `maktaba-api` and `maktaba-streaming` identical across two runs. |
| `TestWebBundleReproducible` (TC1) | `web/dist` directory tree's sorted sha256 list identical across two runs. |
| `TestSourceDateEpochHonored` | Two runs with `SOURCE_DATE_EPOCH=1700000000` and `=1800000000` produce identical-content binaries (the value is in the version string only via `-X main.BuildTime`). |
| `TestVendoredDepsHonored` | Removing `api/vendor/` causes `go build` to fail with `-mod=vendor`; CI lint catches missing vendor. |

### 3.2 Signature verification

| Test | What it pins |
|---|---|
| `TestCosignVerifyImages` (TC2) | `cosign verify --certificate-identity-regexp '…' ghcr.io/maktaba/api:vN.M.P` exits 0. |
| `TestMinisignVerifyBinaries` (TC2) | `minisign -V -p public.key -m maktaba-api` exits 0. |
| `TestMissingSignatureFails` | An unsigned image fails `cosign verify`; the release workflow's verify step blocks publish. |

### 3.3 Lockfile drift

| Test | What it pins |
|---|---|
| `TestUvLockDrift` (TC3) | A deliberate edit to `pipeline/pyproject.toml` adding a new dep without `uv lock` fails CI's `uv lock --check`. |
| `TestPnpmLockDrift` | Same, for `web/package.json`. |
| `TestGoModTidyDrift` | A new import without `go mod tidy` fails `go mod tidy -diff`. |

## 4. Edge cases

| Case | Behaviour | Where pinned |
|---|---|---|
| Native wheels (EC1) | `cibuildwheel` runs per-platform; per-platform sha256 stable, cross-platform hashes naturally differ. The release publishes platform-tagged wheels. | `TestNativeWheelsReproducible` per-platform job |
| iOS/Android signing (EC2) | Mobile signing uses maintainer-held keys; CI uses dev signing only. The mobile build job in Story 22.7 imports the dev key from CI secrets; release-channel signing is a manual step on a maintainer machine. | Documented in Story 22.7 |
| Timezone / locale (EC3) | `tools/build.sh` exports `TZ=UTC` and `LANG=C.UTF-8`. Tested by running the verify script under `TZ=Asia/Tokyo`. | `TestReproducibleAcrossTimezones` |
| `git describe --dirty` in dev | A dirty worktree adds `-dirty` suffix to `version.Tag`; the version differs across dev runs but the *hash* of the binary differs only by that string. The build path is the same; reproducibility test runs from a clean checkout. | n/a (dev-only behavior) |
| ldflags trim too aggressive | `-s -w` strips DWARF; if a user reports they need debug builds, `make build-debug` opts out. The reproducibility test runs against the production flags only. | `TestDebugBuildOptOut` |
| Vendored module CVE | Story 23.7 govulncheck runs against the vendor tree directly; reproducibility doesn't shield us from supply-chain bugs. | Story 23.7 |
| Container layer cache poisoning | `--provenance=true --sbom=true` records the build graph; cosign attestation pins it. A poisoned layer would fail attestation verification at deploy. | `TestProvenanceAttached` |
| `ko` vs Buildx hash divergence | `ko` is reproducible by construction (no Dockerfile); `pipeline/` uses Buildx with `SOURCE_DATE_EPOCH` because of native deps. The verify script asserts hash stability *within* a tool, not across tools. | n/a |

## 5. Dependencies

| Dep | Version | Why |
|---|---|---|
| `ko` | latest minor | Deterministic Go OCI builds. |
| `cosign` | latest | Image signing + verify. |
| `minisign` | latest | Binary signing. |
| `uv` | pinned via `setup-uv@v3` | Lockfile-driven Python. |
| `pnpm` | pinned in `package.json#packageManager` | Web lockfile. |
| `cibuildwheel` | latest | Native-extension wheels. |
| `goreleaser` | NOT used — chose `ko` + `tools/sign.sh` for explicit control. | (Documenting the road-not-taken.) |

## 6. Acceptance checklist

**Go**
- [ ] `go build` uses `-trimpath -ldflags='-buildid=' -mod=vendor`.
- [ ] `tools/.go-build-flags` is the single source of truth for ldflags.
- [ ] `verify-reproducibility.sh` passes for two runs on the same OS/arch.

**Python**
- [ ] `uv.lock` checked in; `uv lock --check` is part of the lint gate.
- [ ] `cibuildwheel` produces platform-tagged wheels in CI.

**Web**
- [ ] `pnpm-lock.yaml` checked in; `vite build` byte-stable across runs.

**Containers**
- [ ] Go images built via `ko` with platform digest pins.
- [ ] Python image base pinned via `.base-digest`.
- [ ] All four images signed via `cosign`.

**Signing**
- [ ] Maintainer minisign pubkey published in `SECURITY.md`.
- [ ] CI's release workflow refuses to publish if `cosign verify` or `minisign -V` fail.

**Self-check**
- [ ] `_reproducibility-check.yml` runs weekly + on `release/*`.
