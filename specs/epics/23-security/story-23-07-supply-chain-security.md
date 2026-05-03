# Story 23.7 — Supply-chain security

Every dependency, base image, and binary that ships in a release is
auditable.

## Acceptance criteria

- AC1. SBOM (`cyclonedx-go`, `cyclonedx-py`, npm SBOM) generated for
  every release artifact; published alongside.
- AC2. CVE scanning gate in CI (`govulncheck`, `pip-audit`, `npm
  audit --audit-level=high`); a high-severity vuln blocks merge
  unless explicitly suppressed with a recorded reason and a date
  ceiling.
- AC3. Base images pinned by digest; `Dockerfile`s use
  `--platform=$BUILDPLATFORM` correctly; no `:latest`.
- AC4. Dependency upgrades are managed by `dependabot` /
  `renovate`; a weekly run opens PRs; security PRs are auto-approved
  if green.

## Test cases

- TC1. SBOM: every release tag publishes four SBOM files (api,
  streaming, pipeline, web); each contains all transitive deps
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
