# Implementation Plan — Story 22.5 Release management and versioning

> Companion to [story-22-05-release-management.md](story-22-05-release-management.md).
> Story states *what* and *why*; this plan states *how*.
> Builds on the reproducible/signed artifacts from
> [Story 22.2](plan-22-02-reproducible-builds.md) and the CI gates
> from [Story 22.1](plan-22-01-ci-pipeline.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Trigger | Pushing a `vMAJOR.MINOR.PATCH` tag onto a green main commit triggers `.github/workflows/release.yml`. Tags only — no "release the artifacts CI happened to produce." |
| Version surface | `shared/go/version/` (Go module `github.com/maktaba/shared/go/version`, owned by Story 22.2 §0) is embedded into every Go binary via `-X` ldflags; Pipeline reads `pyproject.toml`'s version; web reads `package.json`. A single `VERSION` file at the repo root is the source of truth that all four read at build time. |
| Changelog | `CHANGELOG.md` follows Keep-a-Changelog. CI's `_changelog-gate.yml` requires every PR to add an entry under `Unreleased` unless labeled `docs-only`. |
| Mobile/desktop versions | Tagged separately: `mobile-vN.M.P`, `desktop-vN.M.P`, `tvos-vN.M.P` — each pinned to a platform `vN.M.P` via the custom `mobileAppCompatibility` field (Capacitor has no built-in `compatibleApiVersion`). The mobile API client reads the field on startup and refuses to talk to incompatible API versions. |
| Out of scope | Mobile/desktop *signing* (Story 22.7); upgrade/rollback (Story 22.6); hotfix branch policy beyond what's documented in EC1. |

## 1. Architecture diagram

```
git tag v1.2.0 → push
       │
       ▼
┌───────────────────────────────┐
│ release.yml                   │
│  1. assert tag on main + green│
│  2. read VERSION; assert ==tag│
│  3. checkout tag              │
│  4. tools/build.sh all        │
│  5. tools/sign.sh             │
│  6. publish images (cosign)   │
│  7. gh release create + assets│
│  8. update Homebrew tap       │
│  9. publish CHANGELOG section │
└───────────────────────────────┘
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `VERSION` | Single line: `1.2.0`. Source of truth for embedded versions. |
| `shared/go/version/version.go` | Shared helper for the two Go services (lives in its own `github.com/maktaba/shared/go/version` module — Story 22.2 §0). |
| `pipeline/src/maktaba_pipeline/__about__.py` | `__version__ = "1.2.0"`. Bumped by `tools/bump-version.sh`. |
| `web/src/version.ts` | Same; `export const version = "1.2.0"`. |
| `tools/bump-version.sh` | Idempotent rewrite of all four version files. |
| `tools/check-version-consistency.sh` | CI helper: asserts `VERSION` == every version file. |
| `tools/changelog-check.sh` | Asserts the PR adds a CHANGELOG line under Unreleased. |
| `.github/workflows/release.yml` | Tag-driven release pipeline. |
| `.github/workflows/_changelog-gate.yml` | Reusable PR-time changelog gate. |
| `CHANGELOG.md` | Initial Keep-a-Changelog file. |
| `RELEASING.md` | Maintainer-only operations doc. |
| `deploy/homebrew/Formulafile.tpl` | Template the release fills with the tag's URLs + sha256s. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `Makefile` | `make bump-version VERSION=1.2.0`, `make release-dryrun`. |
| `api/cmd/api/main.go`, `streaming/cmd/streaming/main.go` | `--version` flag; emits `<tag> sha=<git-sha> built=<ISO8601>`. |
| `api/internal/http/system.go` | `GET /api/system/version` endpoint. |
| `pipeline/src/maktaba_pipeline/cli.py` | `maktaba-pipeline --version`. |
| `web/src/components/AboutDialog.tsx` | Renders `version.ts`. |

### 2.3 Version helper

`shared/go/version/version.go` (module path
`github.com/maktaba/shared/go/version`; api + streaming consume it via
`replace github.com/maktaba/shared/go/version => ../shared/go/version`
in their respective `go.mod` files — see Story 22.2 §0):

```go
package version

import (
    "strconv"
    "time"
)

// All three are populated via -ldflags at build time (Story 22.2). The
// dev fallback is `dev-<short-sha>` when the linker did not set Tag —
// see `Current()` below — so an unstamped binary still surfaces a
// recognizable, sortable identifier instead of the bare literal "dev".
var (
    Tag       = "dev"          // e.g., "v1.2.0"
    Sha       = "unknown"      // git rev-parse HEAD
    BuildTime = "0"            // SOURCE_DATE_EPOCH
)

type Info struct {
    Tag       string `json:"tag"`
    Sha       string `json:"sha"`
    BuildTime string `json:"build_time"` // RFC3339, or "dev" when unstamped
}

func Current() Info {
    tag := Tag
    if tag == "dev" && Sha != "unknown" && len(Sha) >= 7 {
        // Mark dev builds with their commit so two unstamped binaries
        // are distinguishable.
        tag = "dev-" + Sha[:7]
    }
    var bt string
    if BuildTime == "0" {
        bt = "dev"
    } else {
        t, _ := strconv.ParseInt(BuildTime, 10, 64)
        bt = time.Unix(t, 0).UTC().Format(time.RFC3339)
    }
    return Info{Tag: tag, Sha: Sha, BuildTime: bt}
}
```

`/api/system/version` returns `version.Current()` unmodified. The
streaming binary returns the same struct on its admin debug port. The
Pipeline service exposes the same struct via gRPC `Diagnostics.Version`.

### 2.4 CHANGELOG.md format

```
# Changelog

All notable changes to this project will be documented in this file.

The format is based on Keep a Changelog (https://keepachangelog.com),
and this project adheres to Semantic Versioning.

## [Unreleased]
### Added
### Changed
### Deprecated
### Removed
### Fixed
### Security

## [1.1.0] — 2026-04-12
### Added
- Smart collections (#412).
### Fixed
- Race in chapter inference on resume (#421).
```

### 2.5 Changelog gate

`tools/changelog-check.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail
BASE="${1:-origin/main}"

# Skip docs-only PRs.
if [[ "${PR_LABELS:-}" == *"docs-only"* ]]; then exit 0; fi

# Diff CHANGELOG.md against base; require at least one new line under
# the Unreleased section.
diff=$(git diff "${BASE}"...HEAD -- CHANGELOG.md || true)
if [[ -z "$diff" ]]; then
  echo "No CHANGELOG.md change in this PR. Add an entry under Unreleased,"
  echo "or apply the docs-only label."
  exit 1
fi

# Validate the diff actually adds a line under Unreleased (not just edits
# a previous version's section).
unreleased_added=$(awk '
  /^## \[Unreleased\]/ {seen=1; next}
  /^## \[/ {seen=0}
  seen && /^\+\s*-/ {print; exit}
' <<< "$diff")
if [[ -z "$unreleased_added" ]]; then
  echo "CHANGELOG.md change must add a bullet under [Unreleased]."
  exit 1
fi
```

`_changelog-gate.yml` invokes this; the `docs-only` label is the
exemption (TC2).

### 2.6 Bump-version helper

`tools/bump-version.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail
new=$1
[[ "$new" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-rc\.[0-9]+)?$ ]] \
  || { echo "Invalid semver: $new" >&2; exit 1; }

# `sed -i` is BSD/GNU-incompatible: GNU accepts `-i ''` as the empty
# in-place suffix; BSD requires `-i ''`. The portable pattern is to
# write to a `.bak` copy and remove it after — works on both.
portable_sed() {
  local pattern="$1" file="$2"
  sed -i.bak "$pattern" "$file" && rm "${file}.bak"
}

echo "$new" > VERSION
portable_sed "s/^__version__ = .*/__version__ = \"$new\"/" pipeline/src/maktaba_pipeline/__about__.py
portable_sed "s/^export const version = .*/export const version = \"$new\";/" web/src/version.ts

# pyproject.toml has multiple `version = …` lines (e.g., under
# [tool.poetry], [tool.uv], [project], [build-system.requires]). Anchor
# on the [project] section header explicitly so we never edit a
# tool-table version by accident. `tomlq` would be cleaner, but we
# avoid the extra dep; the awk variant below is deterministic.
awk -v new="$new" '
  /^\[project\]/ { in_project = 1; print; next }
  /^\[/ && !/^\[project\]/ { in_project = 0; print; next }
  in_project && /^version *=/ { print "version = \"" new "\""; next }
  { print }
' pipeline/pyproject.toml > pipeline/pyproject.toml.new \
  && mv pipeline/pyproject.toml.new pipeline/pyproject.toml

# Cut Unreleased into a new section header. The `\n` literal embedded
# in a sed replacement is a GNU extension; portable_sed keeps the
# substitution on one line and uses a literal newline via $'…'.
today=$(date -u +%Y-%m-%d)
section=$'## [Unreleased]\\\n### Added\\\n### Changed\\\n### Fixed\\\n\\\n## ['"$new"$'] \xe2\x80\x94 '"$today"
portable_sed "s/^## \[Unreleased\]/${section}/" CHANGELOG.md
```

The release flow runs `bump-version` on a release branch *before*
tagging. The next-`Unreleased` section is left empty for the next round.

### 2.7 The release workflow

`.github/workflows/release.yml`:

```yaml
name: Release
on:
  push:
    tags: ['v*.*.*']

permissions:
  contents: write
  packages: write
  id-token: write       # cosign keyless

jobs:
  guard:
    runs-on: ubuntu-22.04
    outputs:
      tag_sha: ${{ steps.tag.outputs.tag_sha }}
    steps:
      - uses: actions/checkout@v4
        with: { fetch-depth: 0 }
      - name: Resolve tag SHA
        id: tag
        run: |
          tag_sha=$(git rev-list -n 1 "${GITHUB_REF_NAME}")
          # Export for both later steps in this job (via env) and other
          # jobs (via the `outputs:` declaration above). Bash variables
          # and unscoped `$tag_sha` references do NOT propagate between
          # `run:` blocks — see PLAN_REVIEW §22-05.
          echo "tag_sha=$tag_sha" >> "$GITHUB_OUTPUT"
          echo "tag_sha=$tag_sha" >> "$GITHUB_ENV"
      - name: Tag must point at main HEAD
        run: |
          main_sha=$(git rev-parse origin/main)
          # Hotfix branches are exempt: the tag must be on a `release/v*.x`
          # branch instead, asserted by EC1 below.
          if [[ "$tag_sha" != "$main_sha" ]]; then
            git branch -r --contains "$tag_sha" | grep -q "release/" \
              || { echo "Tag must be on main or a release/* branch"; exit 1; }
          fi
      - name: VERSION must match tag
        run: |
          tag_no_v="${GITHUB_REF_NAME#v}"
          file=$(cat VERSION)
          [[ "$tag_no_v" == "$file" ]] || \
            { echo "VERSION=$file does not match tag=$tag_no_v"; exit 1; }
      - name: CI on this commit must be green
        run: |
          gh run list --commit "${tag_sha}" --workflow CI \
            --json conclusion -q '.[0].conclusion' | grep -qx success

  build:
    needs: guard
    uses: ./.github/workflows/_build-artifacts.yml
    with: { reproducible: true, sign: true }

  publish-images:
    needs: build
    runs-on: ubuntu-22.04
    steps:
      - uses: actions/checkout@v4
      - run: tools/build.sh images
      - run: tools/sign.sh

  github-release:
    needs: publish-images
    runs-on: ubuntu-22.04
    steps:
      - uses: actions/checkout@v4
      - name: Extract changelog section
        run: |
          awk '/^## \['"${GITHUB_REF_NAME#v}"'\]/{flag=1;next}/^## \[/{flag=0}flag' \
            CHANGELOG.md > release-notes.md
      - name: Create release
        run: |
          gh release create "${GITHUB_REF_NAME}" \
            --title "${GITHUB_REF_NAME}" \
            --notes-file release-notes.md \
            api/bin/maktaba-api \
            streaming/bin/maktaba-streaming \
            dist/web-${GITHUB_REF_NAME#v}.tar.gz \
            dist/checksums.txt \
            dist/checksums.txt.minisig

  homebrew:
    needs: github-release
    if: ${{ !contains(github.ref, '-rc') }}
    runs-on: ubuntu-22.04
    steps:
      - uses: actions/checkout@v4
        with: { repository: maktaba/homebrew-tap, token: ${{ secrets.TAP_PAT }} }
      - run: tools/render-formula.sh "${GITHUB_REF_NAME}" > Formula/maktaba.rb
      - run: |
          git config user.name "maktaba-bot"; git config user.email bot@maktaba.io
          git add Formula/maktaba.rb && git commit -m "maktaba ${GITHUB_REF_NAME}"
          git push
```

Pre-release (`-rc.N`) tags skip Homebrew (Homebrew formulas are
production artifacts). Production releases force-update the tap.

### 2.8 OCI label lineage

`tools/build.sh images` already feeds `ko` and `buildx`; this story
extends the build to set OCI labels:

```yaml
# .ko.yaml additions
defaultLabels:
  org.opencontainers.image.source: https://github.com/maktaba/maktaba
  org.opencontainers.image.revision: ${GIT_SHA}
  org.opencontainers.image.version: ${VERSION}
```

`pipeline/Dockerfile`:

```dockerfile
LABEL org.opencontainers.image.source=https://github.com/maktaba/maktaba \
      org.opencontainers.image.revision=${GIT_SHA} \
      org.opencontainers.image.version=${VERSION}
```

CI's TC3 verifies `crane manifest ghcr.io/maktaba/api:vN.M.P` includes
the labels with the matching SHA.

## 3. Test plan

### 3.1 Version surface

| Test | What it pins |
|---|---|
| `TestVersionFlagMatchesTag` (TC1) | Build with `VERSION=1.2.0`; `maktaba-api --version` outputs `v1.2.0 sha=… built=…`. |
| `TestSystemVersionEndpoint` | `GET /api/system/version` returns the same fields. |
| `TestVersionConsistencyAcrossSurfaces` | `tools/check-version-consistency.sh` exits 0 with `VERSION=pyproject=package.json=version.ts`; mismatch fails CI. |
| `TestVersionMismatchFailsRelease` | Tag `v1.2.1` against `VERSION=1.2.0` fails the release workflow's `guard` job. |

### 3.2 Changelog gate

| Test | What it pins |
|---|---|
| `TestChangelogMissingFails` (TC2) | A feature PR without a CHANGELOG line fails `_changelog-gate.yml`. |
| `TestChangelogDocsOnlyExempt` | A `docs-only`-labeled PR passes without CHANGELOG. |
| `TestChangelogMustBeUnderUnreleased` | A diff that edits an old version's section but not Unreleased fails. |

### 3.3 Lineage

| Test | What it pins |
|---|---|
| `TestOciLabelMatches` (TC3) | `crane manifest` returns `org.opencontainers.image.revision == HEAD on main at tag time`. |
| `TestImageDigestStableAcrossPublishes` | Re-running the release workflow without bumping the tag refuses to publish (the tag-existence guard). |

### 3.4 Pre-release

| Test | What it pins |
|---|---|
| `TestRcSkipsHomebrew` (EC2) | `v1.2.0-rc.1` runs build/publish/release but the `homebrew` job is skipped via the `if: !contains -rc`. |
| `TestRcArtifactsTagged` | The release notes header is `1.2.0-rc.1`; `gh release view` shows `prerelease: true`. |

## 4. Edge cases

| Case | Behaviour | Where pinned |
|---|---|---|
| Hotfix on old minor (EC1) | Maintainer cuts `release/v1.1.x` from the previous tag; cherry-picks; tags `v1.1.1`. The release workflow's `guard` job recognizes `release/*` branches as a valid alternative to `main`. | `TestHotfixReleaseBranchAccepted` |
| Pre-release identifier (EC2) | `vX.Y.Z-rc.N` tags trigger the same workflow. The Homebrew tap update is skipped; GitHub release marked `prerelease`. | `TestRcSkipsHomebrew` |
| Mobile app version (EC3) | `apps/mobile/capacitor.config.ts` carries a custom `mobileAppCompatibility` field — Capacitor has no built-in `compatibleApiVersion`. Shape: `mobileAppCompatibility: { minApiVersion: "1.0.0", maxApiVersion: "<2.0.0" }`. Stored either via the `appVersion` extras block on `capacitor.config.ts` or under `package.json#maktaba.api_compatibility`; the mobile API client reads the field on startup and refuses to talk to incompatible API versions. The API server independently enforces the same range. | `TestMobileCompatGate` |
| Tagging an old commit by mistake | `guard` job asserts the tag commit is on main or a `release/*` branch; tags on feature branches fail. | `TestTagMustBeOnMain` |
| CI never ran on the tag commit | The guard runs `gh run list --commit ${sha}`; if no green CI run exists, fail. | `TestRequireCiGreen` |
| Release notes empty | `awk` extracts the section by heading match; if empty, `gh release create` still publishes but a soft warning logs "empty release notes — did you forget to bump VERSION?". | `TestEmptyReleaseNotesWarn` |
| Two releases the same minute | Tag uniqueness in git prevents collisions; the workflow's "tag existing" check refuses re-publish. | n/a |
| Bump version forgot one file | `tools/check-version-consistency.sh` fails CI before the release tag is even pushed (the gate runs in lint). | `TestVersionConsistencyFailsCi` |
| Tap PR conflict (concurrent release) | The `homebrew` job uses `git pull --rebase` before push; on conflict, fails with a manual-fix instruction in `RELEASING.md`. | n/a |

## 5. Dependencies

| Dep | Version | Why |
|---|---|---|
| `gh` CLI | bundled | Release creation; lookup of CI runs. |
| `crane` (`go-containerregistry`) | latest | OCI label inspection in tests. |
| `awk`, `sed` | POSIX | Changelog extraction; version bump. |
| Homebrew tap repo | n/a | External: `maktaba/homebrew-tap` with PAT. |

## 6. Acceptance checklist

**Versioning**
- [ ] `VERSION` is the source of truth; four downstream files stay in sync.
- [ ] All three Go binaries embed `Tag/Sha/BuildTime` via `-ldflags`.
- [ ] `--version` and `/api/system/version` agree on every build.

**Changelog**
- [ ] Keep-a-Changelog format.
- [ ] Gate fires on every non-`docs-only` PR.

**Release flow**
- [ ] Tag on a non-main commit (except `release/*`) refuses to release.
- [ ] CI must be green for the tag's commit.
- [ ] Reproducible builds are re-run from the tag (Story 22.2).
- [ ] Images get OCI labels with the tag's git sha.
- [ ] `gh release create` attaches binaries, web tarball, checksums.

**Homebrew**
- [ ] Production releases force-update the tap.
- [ ] `-rc` tags skip the tap.
