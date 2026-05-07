# Story 22.5 — Release management and versioning

SemVer for the platform; releases are tagged, changelogged, and built
from a green main.

## Acceptance criteria

- AC1. Versions follow `MAJOR.MINOR.PATCH`; the platform's "version"
  spans api + streaming + pipeline + web in lockstep. App-store
  releases for mobile/desktop/TV are tagged separately but pinned to
  a platform version.
- AC2. A release is a Git tag `v{MAJOR}.{MINOR}.{PATCH}` on a green
  main commit; the release workflow rebuilds artifacts from that tag
  (no "release the artifacts CI happened to produce" path).
- AC3. CHANGELOG.md follows Keep-a-Changelog; CI fails a PR that adds
  user-visible behavior without a changelog entry.
- AC4. A `maktaba --version` and `GET /api/system/version` both return
  semver + git-sha + build-time; consistent across the four backend
  services.

## Test cases

- TC1. Version surface: a fresh build's `--version` matches the tag
  and the embedded git sha; mismatch fails the release workflow.
- TC2. Changelog gate: a feature PR without a CHANGELOG line fails
  CI; a docs-only PR is exempt.
- TC3. Tag → artifact lineage: the published image's OCI label
  `org.opencontainers.image.revision` matches the source tag commit.

## Edge cases

- EC1. Hotfix release on an old minor — branch from the tag, cherry-
  pick, tag a new patch; CI handles the release branch identically.
- EC2. Pre-release identifiers (`v1.2.0-rc.1`) — produced by the same
  workflow with a `-rc` channel tag; consumers explicitly opt in.
- EC3. Mobile app version vs. platform — `apps/mobile/capacitor.config.ts`
  embeds a `compatibleApiVersion` range; a mismatch refuses to
  connect with a clear UI message.
