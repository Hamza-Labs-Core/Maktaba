# Implementation Plan — Story 23.7 Supply-chain security

> Companion to [story-23-07-supply-chain-security.md](story-23-07-supply-chain-security.md).
> Story states *what* and *why*; this plan states *how*.
> Hooks the CI gates from [Story 22.1](plan-22-01-ci-pipeline.md) and
> the release artifacts from [Story 22.5](plan-22-05-release-management.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| SBOM | `cyclonedx-gomod` (Go), `cyclonedx-py` (Python), `@cyclonedx/cyclonedx-npm` (Node). Generated per release artifact; published as `*.cdx.json` alongside the binary/image. (Note: `cyclonedx-go` is the underlying library; the **CLI tool for Go** is `cyclonedx-gomod`.) |
| CVE scanning | `govulncheck` (Go), `pip-audit` (Python), `npm audit` (Node). Wired into CI lint gate's tail; fails on high. Suppression mechanism mirrors Story 22.4's lints.json. |
| Base-image pin enforcement | `tools/dockerfile-pin-lint.sh` (lint gate) refuses any `FROM` without `@sha256:`. |
| Dependency upgrades | `renovate.json` (no Dependabot — Renovate's grouping is more flexible). Weekly schedule + auto-merge for security updates **gated on full CI green** (the security PR must pass the smoke gate from [plan-22-01](../22-devops-delivery/plan-22-01-ci-pipeline.md) before automerge fires). |
| Suppressions | `security/suppressions/<cve-id>.md` with rationale, owner, expiry. Expired files fail CI. |
| Signing | **SBOM artifacts are signed with `minisign`** (this plan). **Container images are signed with `cosign`** (owned by [plan-22-02](../22-devops-delivery/plan-22-02-reproducible-builds.md)). Both signing keys are loaded from env via the [plan-23-04](plan-23-04-secrets-management.md) registry (`MAKTABA_MINISIGN_KEY_PATH` and `COSIGN_PRIVATE_KEY` respectively). |
| Air-gapped builds | `GOFLAGS=-insecure`, `pip --no-verify`, `pnpm --strict-ssl=false`, and `git config http.sslVerify false` are **forbidden**. Air-gapped builds use a locally-cached vendor tree (`go mod vendor`, `uv export --frozen`, `pnpm fetch`) populated via a separately-audited mirror. The supply-chain gate is **not** bypassed; it runs against the vendor tree. |
| Out of scope | Disclosure/response process (Story 23.8); release signing for images (plan-22-02 cosign). |

## 1. Architecture diagram

```
                ┌─────────────────────────┐
   PR push  ──► │ CI lint gate            │
                │  govulncheck / pip-audit│  fail on high
                │  npm audit              │
                │  dockerfile-pin-lint    │
                │  expired-suppression    │
                └────────────┬────────────┘
                             │
                ┌────────────▼────────────┐
                │ release.yml             │
                │  cyclonedx → *.cdx.json │
                │  attach to release      │
                └─────────────────────────┘

                weekly cron → renovate → PRs
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `.github/workflows/_supply-chain.yml` | Reusable CI gate. |
| `tools/sbom-generate.sh` | Wraps the four SBOM tools; outputs CycloneDX JSON. |
| `tools/dockerfile-pin-lint.sh` | Asserts every `FROM ... @sha256:` and rejects `:latest`. |
| `tools/suppression-lint.sh` | Reads `security/suppressions/`; expired files fail. |
| `security/suppressions/README.md` | Suppression policy + template. |
| `security/suppressions/.gitkeep` | Initial empty dir. |
| `renovate.json` | Renovate config. |
| `.github/workflows/_renovate.yml` | Self-hosted Renovate runner (or use the GitHub-hosted app's config). |
| Tests — `tests/security/sbom_smoke_test.sh`, `tests/security/cve-fixture/...`. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `.github/workflows/_lint.yml` | Append the supply-chain gate after the language linters. |
| `.github/workflows/release.yml` | After build, run `tools/sbom-generate.sh` and attach outputs to the release. |
| Every `Dockerfile` | Add `@sha256:<DIGEST>` to `FROM`. |

### 2.3 SBOM generation

`tools/sbom-generate.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail
out=${OUT:-dist/sbom}
mkdir -p "$out"

# Go services
for svc in api streaming; do
  ( cd "$svc" && cyclonedx-gomod app -json -output "../${out}/${svc}.cdx.json" ./... )
done

# Python pipeline
( cd pipeline && cyclonedx-py environment -o "../${out}/pipeline.cdx.json" )

# Web bundle
( cd web && pnpm dlx @cyclonedx/cyclonedx-npm --output-format json --output-file "../${out}/web.cdx.json" )

# Container images (top-level merge of OS layer + Go SBOM + Python SBOM)
for img in api streaming pipeline web; do
  syft "ghcr.io/maktaba/${img}:${VERSION:-latest}" -o cyclonedx-json="${out}/image-${img}.cdx.json"
done

# Sign the SBOM bundle.
( cd "$out" && tar --sort=name --mtime=@${SOURCE_DATE_EPOCH:-0} \
    --owner=0 --group=0 -cf - *.cdx.json | gzip -n > sbom.tar.gz )
minisign -S -s "$MAKTABA_MINISIGN_KEY_PATH" -m "${out}/sbom.tar.gz"
```

Each artifact is a CycloneDX JSON (industry standard, machine-readable
diff). The release workflow attaches `dist/sbom/*.cdx.json` and the
signed `sbom.tar.gz` to the GitHub release.

### 2.4 CI supply-chain gate

`.github/workflows/_supply-chain.yml`:

```yaml
on: { workflow_call: {} }
jobs:
  vuln-scan:
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

      - name: Suppression lint
        run: tools/suppression-lint.sh

      - name: Dockerfile pin lint
        run: tools/dockerfile-pin-lint.sh

      - name: govulncheck (api)
        # NOTE: govulncheck takes its mode flag BEFORE the package
        # spec — `govulncheck -mode source ./...` (NOT `govulncheck
        # ./... -mode source`). The `tools/cve-suppress.sh` wrapper
        # validates positional-arg parsing and rejects flag-after-spec
        # invocations.
        run: cd api && go run golang.org/x/vuln/cmd/govulncheck@latest -mode source ./... || tools/cve-suppress.sh govulncheck-api

      - name: govulncheck (streaming)
        run: cd streaming && go run golang.org/x/vuln/cmd/govulncheck@latest -mode source ./... || tools/cve-suppress.sh govulncheck-streaming

      - name: pip-audit (pipeline)
        run: cd pipeline && uv run pip-audit -r <(uv export --frozen --no-hashes) --strict || tools/cve-suppress.sh pip-audit

      - name: npm audit (web)
        run: cd web && pnpm audit --audit-level=high || tools/cve-suppress.sh npm-audit
```

`tools/cve-suppress.sh` parses the failing tool's output and looks up
`security/suppressions/<cve-id>.md` for each unique CVE; if every
flagged CVE has a non-expired suppression, exit 0 with a summary; else
exit 1.

### 2.5 Suppression mechanism

`security/suppressions/README.md`:

```
# Vulnerability suppressions

Each high-severity CVE we knowingly accept (false positive, mitigated
by configuration, awaiting upstream fix) lives in this directory as a
markdown file:

  security/suppressions/CVE-2024-99999.md

Required frontmatter:

  ---
  cve: CVE-2024-99999
  scope: govulncheck-api | pip-audit | npm-audit | govulncheck-streaming
  rationale: <one paragraph; what's the actual exposure>
  owner: <github @handle>
  expires: 2026-12-31
  ---

  ## Detail
  <free-form context>
```

`tools/suppression-lint.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail
today=$(date -u +%Y-%m-%d)
fail=0
for f in security/suppressions/CVE-*.md; do
  [[ -e "$f" ]] || continue
  expires=$(awk '/^expires:/ { print $2 }' "$f")
  if [[ "$expires" < "$today" ]]; then
    echo "Expired suppression: $f (expires=$expires)"
    fail=1
  fi
done
exit $fail
```

### 2.6 Dockerfile pin lint

`tools/dockerfile-pin-lint.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail
fail=0
while IFS= read -r dockerfile; do
  while IFS= read -r line; do
    [[ "$line" =~ ^FROM[[:space:]] ]] || continue
    img=$(awk '{print $2}' <<< "$line")
    if [[ "$img" == *":latest"* ]]; then
      echo "$dockerfile: forbidden :latest tag in $line"; fail=1
    fi
    if [[ "$img" != *"@sha256:"* ]]; then
      echo "$dockerfile: missing @sha256: digest pin in $line"; fail=1
    fi
  done < "$dockerfile"
done < <(find . -name 'Dockerfile*' -not -path '*/vendor/*')
exit $fail
```

Renovate's `pinDigests` rule keeps the `@sha256:` values current.

### 2.7 Renovate config

`renovate.json`:

```json
{
  "$schema": "https://docs.renovatebot.com/renovate-schema.json",
  "extends": ["config:recommended", ":timezone(UTC)"],
  "schedule": ["before 9am on monday"],
  "labels": ["dependencies"],
  "vulnerabilityAlerts": {
    "enabled": true,
    "labels": ["security"],
    "automerge": true,
    "automergeType": "branch",
    "automergeStrategy": "squash",
    "_comment_automerge_gate": "automerge requires full CI green; plan-22-01's required-status-checks (smoke gate, supply-chain gate, build) must pass before Renovate merges. The branch-protection rule on main enforces this server-side; Renovate respects it."
  },
  "pinDigests": true,
  "packageRules": [
    {
      "matchUpdateTypes": ["minor", "patch"],
      "matchPackagePatterns": ["^github.com/", "^golang.org/"],
      "groupName": "go-deps",
      "automerge": true
    },
    {
      "matchPackagePatterns": ["^@?eslint", "^prettier"],
      "groupName": "linters"
    },
    {
      "matchManagers": ["dockerfile"],
      "rangeStrategy": "pin",
      "groupName": "container-bases"
    }
  ]
}
```

Auto-merge applies only to security PRs and minor/patch Go-dep PRs.
Major bumps still require human review.

### 2.8 Release flow integration

`.github/workflows/release.yml` adds:

```yaml
  sbom:
    needs: build
    runs-on: ubuntu-22.04
    steps:
      - uses: actions/checkout@v4
        with: { fetch-depth: 0 }
      - run: tools/sbom-generate.sh
      - uses: actions/upload-artifact@v4
        with: { name: sbom, path: dist/sbom/ }

  github-release-attach:
    needs: [github-release, sbom]
    runs-on: ubuntu-22.04
    steps:
      - uses: actions/download-artifact@v4
        with: { name: sbom, path: dist/sbom }
      - run: |
          gh release upload "${GITHUB_REF_NAME}" dist/sbom/*.cdx.json \
                                                  dist/sbom/sbom.tar.gz \
                                                  dist/sbom/sbom.tar.gz.minisig
```

## 3. Test plan

### 3.1 SBOM (TC1)

| Test | What it pins |
|---|---|
| `TestEveryReleasePublishesFourSboms` | A release tag produces `api.cdx.json`, `streaming.cdx.json`, `pipeline.cdx.json`, `web.cdx.json` (and image-* files); each is valid CycloneDX. |
| `TestSbomCarriesAllTransitiveDeps` | The Go SBOM contains `golang.org/x/text` (a transitive dep); the Python SBOM contains `fastapi-cli` if `fastapi` is installed. |
| `TestSbomSignatureVerifies` | `minisign -V` against `sbom.tar.gz.minisig` succeeds. |

### 3.2 CVE block (TC2)

| Test | What it pins |
|---|---|
| `TestGovulncheckHighFails` | A fixture branch with `replace golang.org/x/net => golang.org/x/net v0.0.0-2018…` (a known CVE) fails the supply-chain gate. |
| `TestPipAuditHighFails` | Add `requests==2.18.0` to the pipeline; CI fails with the CVE listed. |
| `TestNpmAuditHighFails` | Add a known-vulnerable package; CI fails. |
| `TestSuppressionLetsThrough` | Add a suppression file matching the CVE; CI passes; logs show the suppressed CVE summary. |
| `TestExpiredSuppressionFails` | Set `expires: 2020-01-01`; CI fails with "expired suppression". |

### 3.3 Pin lint (TC3)

| Test | What it pins |
|---|---|
| `TestDockerfileLatestRefused` | A PR editing a Dockerfile to `FROM alpine:latest` fails the lint. |
| `TestDockerfileNoDigestRefused` | A PR removing the `@sha256:` digest fails the lint. |

## 4. Edge cases

| Case | Behaviour | Where pinned |
|---|---|---|
| False-positive CVE (EC1) | Operator creates `security/suppressions/CVE-X.md` with rationale + expiry. CI accepts during the validity window; expired files fail the lint. | `TestSuppressionLetsThrough`, `TestExpiredSuppressionFails` |
| Vendored Go module CVE (EC2) | `govulncheck` runs against the vendor tree (default behavior); a vendored CVE fails until the vendor is rebuilt. | `TestGovulncheckHighFails` |
| Air-gapped builds (EC3) | `make build-airgap` produces a tarball that includes all deps (`go mod vendor`, `uv export --frozen`, `pnpm fetch` then `--offline`); CI smoke tests an offline install with `--network=none`. The supply-chain gate **is NOT bypassed** — it runs against the vendor tree using a pre-fetched CVE database snapshot. `GOFLAGS=-insecure`, `--no-verify`, `--strict-ssl=false`, and `http.sslVerify=false` are forbidden everywhere; the lint gate greps for these patterns. | `TestAirgapBuild`, `TestNoInsecureFlagsInRepo` |
| Renovate API rate-limit | Renovate's GitHub-app token retries with backoff; the schedule constraint (`before 9am on monday`) bounds concurrency. | n/a |
| New CVE published mid-PR | A PR opened before the CVE is published merges fine; the next PR after publication fails until either the dep is bumped or a suppression is added. | `TestNewCveFailsNextPr` |
| Suppression for the wrong scope | The `scope:` field in the frontmatter must match the failing tool's name; a `pip-audit` suppression doesn't shield `govulncheck`. | `TestSuppressionScopeMismatchFails` |
| Multi-arch image SBOM | `syft` runs per-arch; the SBOM bundle includes all arches. | `TestMultiArchSbom` |
| Renovate disables a major bump that's actually a security fix | `vulnerabilityAlerts.automerge: true` overrides the major-restriction; security PRs still merge. | `TestSecurityPrAutoMergeMajor` |
| Missing CVE database (govulncheck offline) | The job fails fast with a clear error; ops can mark the gate temporarily skipped via the suppression mechanism (one-time). | `TestGovulncheckOfflineFailsLoudly` |
| `pnpm audit` API change | `pnpm audit --audit-level=high` is a stable interface; if it changes, CI fails — easier to update than to silently miss. | n/a |

## 5. Dependencies

| Dep | Version | Why |
|---|---|---|
| `cyclonedx-gomod` | latest | Go SBOM. |
| `cyclonedx-py` | latest | Python SBOM. |
| `@cyclonedx/cyclonedx-npm` | latest | Node SBOM. |
| `syft` | latest | Image SBOM. |
| `govulncheck` | latest | Go CVE scan. |
| `pip-audit` | latest | Python CVE scan. |
| `pnpm audit` | bundled | Node CVE scan. |
| `renovate` | GitHub app | Dep upgrades. |
| `minisign` | already (Story 22.2) | SBOM bundle signing. |

## 6. Acceptance checklist

**SBOM**
- [ ] Four CycloneDX JSON files per release (one per service).
- [ ] Image SBOMs for the four containers.
- [ ] Bundle signed via minisign.

**CVE gate**
- [ ] `govulncheck`, `pip-audit`, `npm audit --audit-level=high` in CI.
- [ ] High-severity vulns block merge.
- [ ] Suppression files require rationale + expiry; expired ones fail.

**Pin lint**
- [ ] No `:latest` Dockerfile tags.
- [ ] Every `FROM` carries `@sha256:`.

**Renovate**
- [ ] `renovate.json` checked in.
- [ ] Weekly schedule with security PRs auto-merging.
