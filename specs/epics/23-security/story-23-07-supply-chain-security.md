# Story 23.7 — Supply-chain security

Every dependency, base image, and binary that ships in a release is
auditable.

## Acceptance criteria

- AC1. SBOM (`cyclonedx-gomod`, `cargo-cyclonedx`, `cyclonedx-py`, pnpm
  lockfile SBOM) generated for every shipped release artifact (Go
  binaries, Rust `maktaba-plugind`, Python pipeline default + CUDA
  sets, and web); published alongside.
- AC2. CVE scanning gate in CI (`govulncheck` over shipped Go main
  packages with release build tags, `cargo-audit` over
  `plugind/Cargo.lock`, `pip-audit` over shipped non-dev Python sets,
  `pnpm audit --prod` over web production dependencies, plus a nightly
  main run); a called Go vulnerability or high/critical advisory blocks
  merge and release unless explicitly suppressed with a recorded reason
  and a date ceiling.
- AC3. Base images pinned by digest; `Dockerfile`s use
  `--platform=$BUILDPLATFORM` correctly; no `:latest`.
- AC4. Dependency upgrades are managed by `dependabot` /
  `renovate`; a weekly run opens PRs; security PRs are auto-approved
  if green.

## Test cases

- TC1. SBOM: every release tag publishes SBOM files for every shipped
  component (Go binaries, Rust `maktaba-plugind`, Python pipeline
  default + CUDA sets, and web); each contains all transitive deps
  with versions.
- TC2. CVE block: a deliberate `pin` of an old `golang.org/x/net`
  with a known high CVE fails the merge gate.
- TC3. Digest pin: a Dockerfile editing a base image to `:latest`
  fails CI's lint.

## Edge cases

- EC1. False-positive CVE — suppression requires a markdown file
  under `security/suppressions/<cve-id>.md` with rationale and
  expiry date; expired suppressions auto-fail.
- EC2. Vendored Go module with a CVE — `govulncheck` against the
  vendor tree finds it; build fails until rebuilt.
- EC3. Air-gapped builds — `make build-airgap` produces a tarball
  including all deps; CI smoke runs an air-gapped path.

## Deferred from P0

- Container image signing with cosign/minisign deferred from 22.2.
- SBOM generation (CycloneDX) is currently a stub in `tools/sbom.sh`
  — needs real `cyclonedx-gomod` / `cyclonedx-py` / `cyclonedx-npm`
  tooling and a CI gate.
