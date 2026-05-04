# Implementation Plan — Story 22.2 Reproducible builds and artifacts

> Companion to [story-22-02-reproducible-builds.md](story-22-02-reproducible-builds.md).
> Story states *what* and *why*; this plan states *how*.
> Sits underneath the build-artifacts gate from
> [Story 22.1](plan-22-01-ci-pipeline.md) and the release flow from
> [Story 22.5](plan-22-05-release-management.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Top-level Go module ownership | Three Go modules at the repo root: `api/` (`go.mod`, `module github.com/maktaba/api`), `streaming/` (`go.mod`, `module github.com/maktaba/streaming`), and `shared/go/version/` (`go.mod`, `module github.com/maktaba/shared/go/version`). Both `api/go.mod` and `streaming/go.mod` declare `replace github.com/maktaba/shared/go/version => ../shared/go/version` so the version-stamping helper is shared without `replace` polluting downstream consumers. A sibling `shared/go/migrations/` module re-exports `shared/db/migrations/*.sql` via `embed.FS` (the `//go:embed` directive cannot escape the package directory, so the embed must live alongside the SQL files). The migrations module is documented here, owned at the build-flag level, and consumed by Story 22.4's `api/cmd/api/migrate.go`. |
| Go reproducibility | `-trimpath -ldflags='-buildid= -s -w -X github.com/maktaba/shared/go/version.Tag=… -X github.com/maktaba/shared/go/version.Sha=… -X github.com/maktaba/shared/go/version.BuildTime=…'`, vendored deps under `api/vendor/`, `streaming/vendor/`, and `shared/go/version/vendor/`, `GOFLAGS=-mod=vendor -trimpath`. `SOURCE_DATE_EPOCH` exported by the build script; banner timestamps in dependent libraries are suppressed via `-buildid=` and the `-X` overrides — no post-build `sed` rewrite. |
| Python reproducibility | `uv lock` + `uv export --frozen`; `cibuildwheel` for native-extension wheels. |
| Web reproducibility | `pnpm` lockfile + `vite build` with `rollup.output.entryFileNames` deterministic; sorted globs. |
| Container reproducibility | Dockerfiles remain authoritative for `api/`, `streaming/`, and `web/` per arch §12.1 (Option A from PLAN_REVIEW_18_24 §22-02). `web/Dockerfile` is multi-stage with Node + Caddy; `api/Dockerfile` and `streaming/Dockerfile` produce the same `ko`-shaped layout (distroless `cgr.dev/chainguard/static`). `.ko.yaml` is added as a *backend-only* (api + streaming) opt-in alternative used by the release workflow; CI's build-artifacts gate exercises both paths and the size guard accepts either. The Python pipeline image uses `docker buildx --provenance=true --sbom=true` because of native deps, with pinned base images by digest. |
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
| `tools/.go-build-flags` | Single source for `-trimpath -ldflags '-buildid= -s -w -X github.com/maktaba/shared/go/version.Tag=… …'`. |
| `shared/go/version/go.mod` | Module `github.com/maktaba/shared/go/version`; declares the `Tag`, `Sha`, `BuildTime` package-level vars overridden via `-X`. |
| `shared/go/version/version.go` | Implementation of the version package; consumed by api + streaming through `replace` directives. |
| `shared/go/migrations/go.mod` | Module `github.com/maktaba/shared/go/migrations`; sibling of `shared/db/migrations/` and re-exports the SQL files via `embed.FS` so callers in `api/cmd/api/` (and any future migrator) can import the embed without violating Go's "embed cannot escape the package directory" rule. |
| `shared/go/migrations/migrations.go` | Holds `//go:embed *.sql` and exports `var FS embed.FS`. The `*.sql` files are symlinked or vendored from `shared/db/migrations/` at build time via a `go:generate` directive. |
| `api/vendor/`, `streaming/vendor/`, `shared/go/version/vendor/` | `go mod vendor` output, checked in. |
| `pipeline/uv.lock` | uv lockfile, checked in. |
| `web/pnpm-lock.yaml` | Already present; locked for byte-stable builds. |
| `.github/workflows/_reproducibility-check.yml` | CI job for TC1. |
| `SECURITY.md` (extended) | Maintainer pubkeys for cosign + minisign. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `Makefile` | `build` delegates to `tools/build.sh`. |
| `api/Dockerfile`, `streaming/Dockerfile`, `web/Dockerfile` | Retained per arch §12.1. `api/Dockerfile` and `streaming/Dockerfile` use a multi-stage build that copies the static binary onto `cgr.dev/chainguard/static@sha256:<digest>`. `web/Dockerfile` keeps the Node→Caddy two-stage shape (Story 22.3). |
| `.ko.yaml` | Added as the backend-only (`api`, `streaming`) opt-in alternative; used by the release workflow when `MAKTABA_BACKEND_BUILDER=ko`. The Dockerfiles remain the canonical CI path. |
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

# Suppress non-deterministic banner timestamps at the build layer:
#  -buildid= zeroes Go's per-build identifier.
#  -s -w strips DWARF + symbol table.
#  -X overrides imported package vars so the binary doesn't need a
#  post-build sed rewrite (which is fragile and matches unrelated
#  strings — see PLAN_REVIEW §22-02).
# Any third-party library that bakes "Built on <timestamp>" into a
# string is opted out via its documented build flag (typically
# `-X path/to/pkg.BuildDate=$SOURCE_DATE_EPOCH`); GOFLAGS picks up
# the additional flags below.
export GOFLAGS="${GOFLAGS:-} -trimpath"
LDFLAGS="-buildid= -s -w \
  -X github.com/maktaba/shared/go/version.Tag=${VERSION} \
  -X github.com/maktaba/shared/go/version.Sha=${GIT_SHA} \
  -X github.com/maktaba/shared/go/version.BuildTime=${SOURCE_DATE_EPOCH}"

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
    # Reproducibility comes from the build itself (vite.config.ts pins
    # entry/asset names, Rollup output is sorted, CSS-module hashes are
    # content-derived). We do NOT post-process dist with `sed` — see
    # PLAN_REVIEW §22-02: the previous heuristic mangled unrelated text
    # and was BSD/GNU-incompatible. Any third-party library that bakes a
    # build timestamp into the bundle is patched via its own
    # `define`/`replace` Vite plugin, not text rewriting.
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

Go images use the canonical `api/Dockerfile` and `streaming/Dockerfile`
(arch §12.1). The Dockerfiles consume the binaries produced by
`tools/build.sh` and copy them onto a digest-pinned distroless base
(`cgr.dev/chainguard/static`). For backend-only releases that opt in
via `MAKTABA_BACKEND_BUILDER=ko`, `.ko.yaml` builds the `api` and
`streaming` images without a Dockerfile:

```yaml
# .ko.yaml — backend-only, opt-in alternative.
defaultBaseImage: cgr.dev/chainguard/static:latest  # DIGEST_TODO (Renovate)
defaultPlatforms: [linux/amd64, linux/arm64]
builds:
- id: api
  dir: ./api
  main: ./cmd/api
  ldflags: ['-buildid=', '-s', '-w', '-X github.com/maktaba/shared/go/version.Tag={{.Env.VERSION}}']
  env: [CGO_ENABLED=0, GOOS=linux]
- id: streaming
  dir: ./streaming
  main: ./cmd/streaming
  ldflags: ['-buildid=', '-s', '-w', '-X github.com/maktaba/shared/go/version.Tag={{.Env.VERSION}}']
  env: [CGO_ENABLED=0, GOOS=linux]
```

`ko` produces byte-stable OCI images by construction (sorted layers,
zeroed timestamps from `SOURCE_DATE_EPOCH`); the Dockerfile path
matches that property because it copies the same `tools/build.sh`
output onto the same digest-pinned base. The web image is *always*
built from `web/Dockerfile` because it requires Node for the multi-
stage Vite build — `ko` only handles Go binaries.

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
- [ ] `go build` uses `-trimpath -ldflags='-buildid= -s -w -X github.com/maktaba/shared/go/version.…' -mod=vendor`.
- [ ] `tools/.go-build-flags` is the single source of truth for ldflags.
- [ ] `shared/go/version/` is its own Go module; api + streaming consume it via `replace` in their go.mod files.
- [ ] `shared/go/migrations/` is its own Go module that re-exports the SQL migrations as `embed.FS`; consumed by `api/cmd/api/migrate.go` (Story 22.4).
- [ ] `verify-reproducibility.sh` passes for two runs on the same OS/arch.

**Python**
- [ ] `uv.lock` checked in; `uv lock --check` is part of the lint gate.
- [ ] `cibuildwheel` produces platform-tagged wheels in CI.

**Web**
- [ ] `pnpm-lock.yaml` checked in; `vite build` byte-stable across runs.

**Containers**
- [ ] Go images built via `api/Dockerfile` and `streaming/Dockerfile` (canonical) with digest-pinned `cgr.dev/chainguard/static` base.
- [ ] Web image built via `web/Dockerfile` (multi-stage Node + Caddy).
- [ ] `.ko.yaml` is the opt-in backend-only alternative; CI exercises both paths under the size guard.
- [ ] Python image base pinned via `.base-digest`.
- [ ] All four images signed via `cosign`.
- [ ] `vendor/` directories listed in arch §12.1 (filed as a separate arch follow-up); `.gitignore` does NOT exclude `vendor/`.

**Signing**
- [ ] Maintainer minisign pubkey published in `SECURITY.md`.
- [ ] CI's release workflow refuses to publish if `cosign verify` or `minisign -V` fail.

**Self-check**
- [ ] `_reproducibility-check.yml` runs weekly + on `release/*`.
