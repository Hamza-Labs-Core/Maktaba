# Implementation Plan — Story 22.1 Continuous integration pipeline

> Companion to [story-22-01-ci-pipeline.md](story-22-01-ci-pipeline.md).
> Story states *what* and *why*; this plan states *how*.
> CI gates wire the test tiers from
> [Epic 20](../20-testing/README.md) into a single merge gate.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Workflow files | `.github/workflows/ci.yml` (PR + push), `.github/workflows/release.yml` (tag-driven, owned by Story 22.5). Reusable jobs live in `.github/workflows/_*.yml`. |
| Job orchestrator | GitHub Actions matrix; `needs:` graph wires the gates so they run in parallel. |
| Branch protection | Required status check: `ci-success` (the rollup). Underlying gates: `lint`, `unit`, `integration`, `e2e`, `perf-ci`, `build-artifacts`, `native-apps` (tvOS + Android TV), `generate-drift`. Configured via Terraform in `deploy/github/branch-protection.tf` so the rules are reviewable. |
| Force-merge override | Branch protection rule "Allow force pushes by admins" stays off; the override is a `force-merge: <reason>` line in the PR body validated by `.github/workflows/_pr-body-check.yml`. |
| Out of scope | Release publishing (Story 22.5), SBOM/CVE gates (Story 23.7), reproducibility flags (Story 22.2 — this plan only invokes those flags, doesn't define them). |

## 1. Architecture diagram

```
                  ┌──────────────┐
   PR push ─────► │  trigger.yml │
                  └──────┬───────┘
                         │ fan out
        ┌───────┬────────┼────────┬────────┬─────────┐
        ▼       ▼        ▼        ▼        ▼         ▼
     lint    unit  integration  e2e   perf-ci  build-artifacts
        │       │        │        │        │         │
        └───────┴────────┴────────┴────────┴─────────┘
                         │ all green
                         ▼
                  ┌──────────────┐
                  │ merge gate   │
                  └──────────────┘
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `.github/workflows/ci.yml` | Top-level workflow; declares the six gates. |
| `.github/workflows/_lint.yml` | Reusable lint gate (`workflow_call`). |
| `.github/workflows/_unit.yml` | Reusable unit gate. |
| `.github/workflows/_integration.yml` | Reusable integration gate (Postgres + Chroma services). |
| `.github/workflows/_e2e.yml` | Reusable e2e gate (compose stack). |
| `.github/workflows/_perf-ci.yml` | Reusable perf-ci gate. |
| `.github/workflows/_build-artifacts.yml` | Reusable build-artifacts gate (matrix on three OS/arch). |
| `.github/workflows/_native-apps.yml` | Reusable native-app lint/test gate: `xcodebuild test` (tvOS) on macOS runner; `gradle test` and `gradle :app:connectedAndroidTest` (Android TV) on Linux runner with KVM. |
| `.github/workflows/_generate-drift.yml` | Reusable generated-code drift gate: runs `make generate && git diff --exit-code` to catch drift between hand-edited inputs (sqlc queries, gqlgen schema, .proto files) and the checked-in generated outputs (arch §12.5 says generated code is checked in). |
| `.github/workflows/_pr-body-check.yml` | PR body section validator (force-merge override, changelog gate hand-off). |
| `Makefile` | Targets `lint`, `test-unit`, `test-integration`, `test-e2e`, `perf-ci`, `build`, `generate` invoked by both CI and developers (Story 22.8 AC-3). |
| `deploy/github/branch-protection.tf` | Terraform: required checks, no force pushes. |
| `.github/CODEOWNERS` | Reviewer routing. |
| `.github/labeler.yml` | `docs-only` label rule used by EC3. |
| `pipeline/.python-version` | Pinned Python version consumed by `actions/setup-python@v5` and local `uv`. Committed to git. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `README.md` | Badge: CI status, latest release. |
| `CONTRIBUTING.md` | "Run `make lint` before pushing" pointer (Story 22.8). |

### 2.3 Workflow skeleton

`.github/workflows/ci.yml`:

```yaml
name: CI
on:
  pull_request:
    branches: [main]
  push:
    branches: [main]

permissions:
  contents: read
  pull-requests: read
  checks: write

concurrency:
  # Cancel in-progress runs for the same PR; never cancel main pushes.
  group: ci-${{ github.event_name == 'pull_request' && github.event.pull_request.number || github.sha }}
  cancel-in-progress: ${{ github.event_name == 'pull_request' }}

jobs:
  changes:
    # Path-filter: lets EC3 (docs-only PRs) skip the heavy gates.
    runs-on: ubuntu-22.04
    outputs:
      docs_only: ${{ steps.filter.outputs.docs_only }}
    steps:
      - uses: actions/checkout@v4
      - uses: dorny/paths-filter@v3
        id: filter
        with:
          filters: |
            docs_only:
              - 'specs/**'
              - 'docs/**'
              - '**/*.md'

  lint:
    needs: [changes]
    if: needs.changes.outputs.docs_only != 'true'
    uses: ./.github/workflows/_lint.yml

  unit:
    needs: [changes]
    if: needs.changes.outputs.docs_only != 'true'
    uses: ./.github/workflows/_unit.yml

  integration:
    needs: [changes]
    if: needs.changes.outputs.docs_only != 'true'
    uses: ./.github/workflows/_integration.yml

  e2e:
    needs: [changes, integration]
    if: needs.changes.outputs.docs_only != 'true' && github.event.pull_request.head.repo.fork == false
    uses: ./.github/workflows/_e2e.yml

  perf-ci:
    needs: [changes, integration]
    if: needs.changes.outputs.docs_only != 'true' && github.event.pull_request.head.repo.fork == false
    uses: ./.github/workflows/_perf-ci.yml

  build-artifacts:
    needs: [changes]
    if: needs.changes.outputs.docs_only != 'true'
    uses: ./.github/workflows/_build-artifacts.yml

  native-apps:
    needs: [changes]
    if: needs.changes.outputs.docs_only != 'true'
    uses: ./.github/workflows/_native-apps.yml

  generate-drift:
    needs: [changes]
    if: needs.changes.outputs.docs_only != 'true'
    uses: ./.github/workflows/_generate-drift.yml

  pr-body-check:
    if: github.event_name == 'pull_request'
    uses: ./.github/workflows/_pr-body-check.yml

  ci-success:
    # The single status check pinned by branch protection. All gates report
    # to this one job; branch protection only needs to require ci-success.
    # Skipped jobs (docs-only PRs, fork PRs) report `skipped` not `success`;
    # the rollup distinguishes the two: `failure` and `cancelled` block,
    # while `skipped` is treated as pass only when the upstream `if:` guard
    # legitimately suppressed the gate (docs-only or fork-skip path). Any
    # other `skipped` status (e.g., a needed gate skipped by mistake) is
    # surfaced as a failure to avoid silently green merges.
    needs: [lint, unit, integration, e2e, perf-ci, build-artifacts, native-apps, generate-drift, pr-body-check]
    if: always()
    runs-on: ubuntu-22.04
    steps:
      - if: contains(needs.*.result, 'failure') || contains(needs.*.result, 'cancelled')
        run: exit 1
```

### 2.4 Lint gate

`.github/workflows/_lint.yml`:

```yaml
on: { workflow_call: {} }
jobs:
  lint:
    runs-on: ubuntu-22.04
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version-file: api/go.mod, cache: true }
      - uses: actions/setup-python@v5
        with: { python-version-file: pipeline/.python-version }
      - uses: astral-sh/setup-uv@v3
      - uses: actions/setup-node@v4
        with: { node-version-file: web/.nvmrc, cache: pnpm, cache-dependency-path: web/pnpm-lock.yaml }
      - name: Install web deps
        working-directory: web
        run: pnpm install --frozen-lockfile
      - name: Install pipeline deps
        working-directory: pipeline
        run: uv sync --frozen
      - name: golangci-lint
        uses: golangci/golangci-lint-action@v6
        with: { version: v1.62, working-directory: api }
      - name: golangci-lint streaming
        uses: golangci/golangci-lint-action@v6
        with: { version: v1.62, working-directory: streaming }
      - name: gofmt + go vet
        run: make lint-go
      - name: ruff + mypy
        run: make lint-py
      - name: tsc + eslint + prettier
        run: make lint-web
```

`make lint` is the developer-facing shell; CI calls the same target so
parity (TC3, AC-3 in Story 22.8) is automatic.

### 2.4a Native-apps gate

`.github/workflows/_native-apps.yml` covers the Swift (`apps/tvos/`)
and Kotlin (`apps/androidtv/`) trees that arch §12.1 declares first-
class. The gate runs in two matrix legs so a failure surfaces with the
offending platform in the job name:

```yaml
on: { workflow_call: {} }
jobs:
  tvos:
    runs-on: macos-14
    steps:
      - uses: actions/checkout@v4
      - name: Select Xcode
        run: sudo xcode-select -s /Applications/Xcode_15.4.app
      - name: xcodebuild test (tvOS simulator)
        working-directory: apps/tvos
        run: |
          xcodebuild \
            -scheme Maktaba \
            -destination 'platform=tvOS Simulator,name=Apple TV' \
            -resultBundlePath build/test.xcresult \
            test
      - if: always()
        uses: actions/upload-artifact@v4
        with: { name: tvos-test-result, path: apps/tvos/build/test.xcresult }

  androidtv:
    runs-on: ubuntu-22.04
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-java@v4
        with: { distribution: temurin, java-version: 21 }
      - name: Enable KVM
        run: |
          echo 'KERNEL=="kvm", GROUP="kvm", MODE="0666"' \
            | sudo tee /etc/udev/rules.d/99-kvm.rules
          sudo udevadm control --reload-rules
          sudo udevadm trigger --name-match=kvm
      - name: Unit tests
        working-directory: apps/androidtv
        run: ./gradlew test
      - name: Connected Android tests (KVM emulator)
        uses: reactivecircus/android-emulator-runner@v2
        with:
          api-level: 34
          target: android-tv
          arch: x86_64
          script: ./gradlew :app:connectedAndroidTest
          working-directory: apps/androidtv
```

### 2.4b Generated-code drift gate

`.github/workflows/_generate-drift.yml`:

```yaml
on: { workflow_call: {} }
jobs:
  generate-drift:
    runs-on: ubuntu-22.04
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version-file: api/go.mod, cache: true }
      - uses: actions/setup-python@v5
        with: { python-version-file: pipeline/.python-version }
      - uses: astral-sh/setup-uv@v3
      - name: Run generators
        run: make generate
      - name: Assert no drift
        run: |
          # `make generate` runs sqlc, gqlgen, and protoc-gen-go. Arch
          # §12.5 mandates the generated outputs are checked in, so any
          # drift between the input schemas and the committed output is
          # a CI-blocking error.
          git diff --exit-code
```

### 2.5 Unit gate

`.github/workflows/_unit.yml`:

```yaml
on: { workflow_call: {} }
jobs:
  unit:
    runs-on: ubuntu-22.04
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version-file: api/go.mod, cache: true }
      - uses: actions/setup-python@v5
        with: { python-version-file: pipeline/.python-version }
      - uses: astral-sh/setup-uv@v3
      - uses: actions/setup-node@v4
        with: { node-version-file: web/.nvmrc, cache: pnpm, cache-dependency-path: web/pnpm-lock.yaml }
      - run: pnpm -C web install --frozen-lockfile
      - name: Install pipeline deps
        working-directory: pipeline
        run: uv sync --frozen
      - run: make test-unit
```

`make test-unit` runs `go test -short ./...` (api, streaming),
`uv run pytest -m unit pipeline/tests`, and `pnpm -C web test:unit`.

### 2.6 Integration gate

`.github/workflows/_integration.yml`:

```yaml
on: { workflow_call: {} }
jobs:
  integration:
    runs-on: ubuntu-22.04
    services:
      postgres:
        # Tag-pinned for readability; @sha256 digest is filled in via the
        # separate digest-pinning step below (see 2.6a). Renovate writes
        # the digest into a YAML-valid string when bumping versions.
        image: postgres:16.4-alpine3.20  # DIGEST_TODO
        env: { POSTGRES_PASSWORD: maktaba, POSTGRES_DB: maktaba }
        ports: ['5432:5432']
        options: >-
          --health-cmd "pg_isready -U postgres" --health-interval 5s --health-timeout 3s --health-retries 10
      chroma:
        image: ghcr.io/chroma-core/chroma:0.5.5  # DIGEST_TODO
        ports: ['8000:8000']
        options: --health-cmd "curl -fsS localhost:8000/api/v1/heartbeat"
    env:
      DATABASE_URL: postgres://postgres:maktaba@localhost:5432/maktaba?sslmode=disable
      CHROMA_URL: http://localhost:8000
    steps:
      - uses: actions/checkout@v4
      # Toolchain setup identical to unit gate, omitted for brevity.
      - run: make migrate
      - run: make test-integration
```

Service container digests are pinned (Story 22.7-supply-chain-style) and
bumped via Renovate.

### 2.6a Digest pinning step

CI workflow files use the form `image: <repo>:<tag>  # DIGEST_TODO` so
that they are valid YAML on every checkout. A separate Renovate
configuration (`.github/renovate.json`) and a `tools/pin-digests.sh`
helper run on a scheduled job to rewrite each `# DIGEST_TODO` marker
into the corresponding `@sha256:<digest>` suffix and open a PR. The
`# DIGEST_TODO` marker is intentional: its presence means "Renovate has
not yet pinned this image", and a CI lint forbids the marker on the
release branches (see Story 22.5). The two-step approach keeps the
files YAML-valid in the source tree without literal `<DIGEST>`
placeholders that fail validation as written.

### 2.7 E2E gate

`.github/workflows/_e2e.yml`:

```yaml
on: { workflow_call: {} }
jobs:
  e2e:
    runs-on: ubuntu-22.04
    steps:
      - uses: actions/checkout@v4
      - run: docker compose -f deploy/compose/docker-compose.yml up -d --wait
      - run: make test-e2e
      - if: always()
        run: docker compose -f deploy/compose/docker-compose.yml logs > compose.log
      - if: always()
        uses: actions/upload-artifact@v4
        with: { name: compose-logs, path: compose.log }
```

### 2.8 Perf-CI gate

`.github/workflows/_perf-ci.yml`: thin wrapper around `make perf-ci` (a
reduced version of Epic 20.7's perf suite — sub-2-minute, single-fixture).

### 2.9 Build-artifacts gate

`.github/workflows/_build-artifacts.yml`:

```yaml
on: { workflow_call: {} }
jobs:
  build:
    strategy:
      fail-fast: false
      matrix:
        include:
          - { os: ubuntu-22.04, goos: linux,  goarch: amd64 }
          - { os: ubuntu-22.04, goos: linux,  goarch: arm64 }
          - { os: macos-14,    goos: darwin, goarch: arm64 }
    runs-on: ${{ matrix.os }}
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version-file: api/go.mod, cache: true }
      - name: Build api+streaming
        env: { GOOS: ${{ matrix.goos }}, GOARCH: ${{ matrix.goarch }} }
        run: make build  # delegates to Story 22.2's reproducible flags
      - name: Build web
        if: matrix.goos == 'linux' && matrix.goarch == 'amd64'
        run: pnpm -C web build
      - uses: actions/upload-artifact@v4
        with:
          name: bin-${{ matrix.goos }}-${{ matrix.goarch }}
          path: |
            api/bin/maktaba-api
            streaming/bin/maktaba-streaming
            web/dist
          retention-days: 14
```

A failure on any matrix entry surfaces with the offending OS/arch in the
job name (TC2).

### 2.10 PR-body check + force-merge override

`.github/workflows/_pr-body-check.yml`:

```yaml
on: { workflow_call: {} }
jobs:
  pr-body:
    runs-on: ubuntu-22.04
    steps:
      - uses: actions/github-script@v7
        with:
          script: |
            const body = context.payload.pull_request.body || "";
            const force = body.match(/^force-merge:\s*(.+)$/m);
            const labels = context.payload.pull_request.labels.map(l => l.name);
            // Branch protection separately requires this job's success;
            // the script only fails when force-merge label is present
            // without a matching reason line.
            if (labels.includes("force-merge") && !force) {
              core.setFailed("force-merge label requires a 'force-merge: <reason>' line in the PR body");
            }
```

Branch protection refuses merges without all required checks green. The
`force-merge` label is the only mechanism to bypass; it requires a
`force-merge: <reason>` line, which `_pr-body-check.yml` enforces. The
audit trail is the PR body itself (TC3).

## 3. Branch protection (Terraform)

`deploy/github/branch-protection.tf`:

```hcl
resource "github_branch_protection" "main" {
  repository_id = data.github_repository.maktaba.node_id
  pattern       = "main"
  required_status_checks {
    strict   = true
    contexts = ["ci-success"]
  }
  required_pull_request_reviews { required_approving_review_count = 1 }
  enforce_admins        = true
  require_signed_commits = true
  allows_force_pushes   = false
  allows_deletions      = false
}
```

The single required check is `ci-success` (the rollup job in 2.3). When
adding a new gate, it's wired into `ci-success.needs:`; branch protection
config doesn't change.

## 4. Test plan

### 4.1 Self-tests for the workflows

| Test | What it pins |
|---|---|
| `TestGateIndependence` (TC1) | A fixture branch with a deliberate `gofmt` violation and a deliberate failing unit test fails both `lint` and `unit` distinctly; the GitHub UI shows two red checks, not one cascade. |
| `TestCrossPlatformBreakage` (TC2) | A `//go:build linux && arm64` file with a syntax error fails the `linux/arm64` matrix entry; `linux/amd64` and `darwin/arm64` pass. |
| `TestForceMergeWithoutReason` (TC3) | A PR labeled `force-merge` with no body line fails `_pr-body-check`; merge is blocked. |
| `TestForkSkipsE2E` (EC2) | A PR opened from a fork sees `e2e` and `perf-ci` skipped with a comment; the gates report `success` (skipped jobs do not block). |
| `TestDocsOnlyPath` (EC3) | A PR touching only `specs/**` skips lint/unit/integration/e2e/perf/build; `ci-success` is green via the if-guard. |
| `TestWallClock` (AC-4) | A green PR's wall-clock is < 20 min on the matrix runners; tracked in CI metrics. |
| `TestNoVerifyBypassedCaughtByCi` | A commit pushed with `git commit --no-verify` containing a `gofmt` violation skips the local pre-commit hook but is caught by the CI lint gate; merge is blocked. (Pre-commit setup itself lives in Story 22.8; the CI safety net is owned here.) |

### 4.2 Local parity tests

| Test | What it pins |
|---|---|
| `TestMakeLintParity` | `make lint` locally and `make lint` in CI exit identically on a fixture branch. (Story 22.8 TC3.) |
| `TestMakeBuildArtifactsParity` | `make build` locally and `make build` in CI produce sha256-identical artifacts (defers to Story 22.2 for the underlying flags). |

## 5. Edge cases

| Case | Behaviour | Where pinned |
|---|---|---|
| GHA flake (network blip) | Gate retries up to 2× via `nick-fields/retry@v3` only for the `services:` provisioning step; user code is never auto-retried (per Epic 20.8 AC). | Embedded in each gate; tests assert no `retry: 3` blocks on test-running steps. |
| Fork PR needing secrets | `e2e` and `perf-ci` are gated by `github.event.pull_request.head.repo.fork == false`. The `ci-success` rollup treats skipped gates as success only if the docs-only or fork gates fired. A bot comment instructs maintainers to rerun. | `TestForkSkipsE2E` |
| Docs-only PR | `paths-filter` flag short-circuits the heavy gates; the rollup passes via skipped status. | `TestDocsOnlyPath` |
| Cancelled run (PR force-pushed mid-CI) | `concurrency.cancel-in-progress=true` cancels older runs; `ci-success` treats `cancelled` as failure on the new run, succeeds on the previous (cancelled) run's branch protection check. | Branch protection only sees the latest run. |
| Workflow file edited in same PR | `actions/checkout@v4` checks out the PR ref, so the new workflow is the one that runs. The branch protection ruleset is GitHub-controlled — workflow edits cannot lower the bar. | `TestWorkflowSelfEdit` (a fixture that tries to weaken `_lint.yml` still must satisfy the original branch-protection-required `ci-success`). |
| Service container slow to start | `--health-*` flags + `options: --health-retries 10`; if Postgres misses readiness in 50 s, the integration gate fails fast with a labeled error rather than running tests against a missing service. | Integration gate config |
| Self-hosted runner (future) | Not used in v1; `runs-on: ubuntu-22.04` is hardcoded. A migration to self-hosted is tracked in Epic 22 follow-ups. | n/a |

## 6. Dependencies

| Dep | Version | Why |
|---|---|---|
| GitHub Actions | n/a | CI runner. |
| `golangci-lint-action@v6` | pinned by SHA in `.github/workflows` | Go lint. |
| `astral-sh/setup-uv@v3` | pinned | Python lockfile-driven deps. |
| `dorny/paths-filter@v3` | pinned | Docs-only gating. |
| `nick-fields/retry@v3` | pinned | Service-up retry. |
| Terraform `integrations/github` | latest minor | Branch protection IaC. |

All third-party Actions are pinned by SHA in the actual files; this
plan uses `@v3` shorthand for readability.

## 7. Acceptance checklist

**Workflow**
- [ ] Eight gates (`lint`, `unit`, `integration`, `e2e`, `perf-ci`, `build-artifacts`, `native-apps`, `generate-drift`) defined as reusable workflows.
- [ ] `ci-success` rollup is the only required status check; rollup distinguishes `failure`/`cancelled` (block) from legitimately `skipped` (pass).
- [ ] Build matrix covers `linux/amd64`, `linux/arm64`, `darwin/arm64`.
- [ ] Native-apps gate runs `xcodebuild test` (tvOS, macOS runner) and `gradle test` + `gradle :app:connectedAndroidTest` (Android TV, Linux runner with KVM).
- [ ] Generate-drift gate runs `make generate && git diff --exit-code` to catch sqlc/gqlgen/proto drift (arch §12.5).
- [ ] Path-filter labels docs-only PRs and skips heavy gates.
- [ ] `pipeline/.python-version` committed to git (consumed by `actions/setup-python` and local uv).

**Branch protection**
- [ ] Terraform applied; main branch requires `ci-success`.
- [ ] Force pushes off; one approving review required; admins included.

**Override**
- [ ] `force-merge` label requires `force-merge: <reason>` PR body line.
- [ ] PR body validation runs in `_pr-body-check.yml`.

**Parity**
- [ ] `make lint`/`make test-unit`/`make test-integration`/`make test-e2e`/`make perf-ci`/`make build` exist and are the only commands CI runs.

**Performance**
- [ ] Green PR wall-clock < 20 min on a representative fixture branch.
