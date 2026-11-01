# Story 35.1 — Signed release manifest and verification

## Description

*As the owner, I want every machine to install only a release that our own release workflow signed, for
the exact tag it claims, and to refuse anything else without asking me.*

Today the home's self-update (Epic 28.3, `api/internal/system/selfupdate.go`) checks the archive's
SHA-256 against `checksums.txt` but never checks the cosign signature over `checksums.txt`, so whoever can
replace both files is trusted. The CLI `maktaba-server update` reads a different feed
(`releases.maktaba.tv/manifest.json`). This story makes the release publish a machine-readable
`release.json` and a Sigstore bundle, and adds one in-process verification package that every installer
uses: the API's Updater, the supervisor's orchestrator, the node's machine agent, the CLI and the
launcher's offline re-check.

Design: [design.md](design.md) §3.1–§3.5; README R3, R10.1, R10.2.

## Acceptance criteria

- **AC1** `release.yml` (`sign-and-release`) publishes `release.json` (fields of design §3.1: `v`,
  `version`, `tag`, `commit`, `channel`, `published`, `schema_rev`, `min_node_schema`,
  `breaks_previous_home`, `min_upgrade_from`, `edge_protocol`, `launcher_protocol`, `agent_protocol`,
  per-platform `assets` with archive and inner-file digests, `pipeline` wheel and constraints) and
  `checksums.txt.sigstore.json` (`cosign sign-blob --bundle … --new-bundle-format`); both appear in
  `checksums.txt`. The existing `.sig` / `.pem` stay.
- **AC2** CI fails the release when `release.json.schema_rev` differs from `SCHEMA_REV`
  (`pipeline/src/maktaba_pipeline/cluster/schema.py`) or `SchemaRev`
  (`streaming/internal/cluster/schema.go`), or when `min_node_schema` / `breaks_previous_home` disagree
  with the MANIFEST markers ("Breaking for older nodes", "Breaking for the previous release").
- **AC3** `shared/go/releaseverify.Verify` checks, without network and without a `cosign` binary: the
  bundle against the embedded trusted root; issuer `https://token.actions.githubusercontent.com`; SAN
  exactly `https://github.com/Hamza-Labs-Core/Maktaba/.github/workflows/release.yml@refs/tags/<tag>`;
  certificate extensions (source repository URI, ref `refs/tags/<tag>`, trigger `push`); at least one
  Rekor inclusion proof with integrated time inside the certificate validity.
- **AC4** It then checks `release.json`'s digest against `checksums.txt`, `version == tag` without the
  `v`, every installed file's SHA-256 against `checksums.txt`, extracted binaries against
  `assets[platform].files`, and that every constraints line carries `--hash=sha256:` (PR #94 rule).
- **AC5** Failures are typed (`ErrSignature`, `ErrIdentity`, `ErrTlog`, `ErrChecksum`, `ErrManifest`,
  `ErrNoHashes`, `ErrReplay`, `ErrDowngrade`) and carry the file name; nothing is extracted into
  `bin/releases`, made executable or run before `Verify` passes.
- **AC6** Watermark rules (design §3.5): `version < watermark` → `ErrDowngrade`; `version == watermark`
  with a different `checksums.txt` digest → `ErrReplay`; a Rekor integrated time older than the current
  release's → `ErrReplay`.
- **AC7** `MAKTABA_UPDATE_REPO` (startup setting only) changes the repository and the expected identity
  together; no API field can change the identity.
- **AC8** The trusted root is embedded at build time and refreshed daily through Sigstore's TUF repository
  with the embedded root as anchor, kept in `<data_dir>/sigstore/`; a failed refresh keeps the last good
  root; an expired root raises a critical notification and verification fails closed.
- **AC9** Epic 28.3's `POST /api/admin/system/update` and `maktaba-server update` call `releaseverify`;
  their own checksum code is removed; the CLI no longer reads `releases.maktaba.tv/manifest.json`.

## Test cases

- **TC1** `TestVerifyValidFixture` — committed fixture release (bundle, checksums, release.json, a small
  archive) signed once by a test workflow; passes for its tag.
- **TC2** `TestVerifyWrongSAN`, `TestVerifyOtherTag`, `TestVerifyWrongIssuer`, `TestVerifyForkRepo` →
  `ErrIdentity`.
- **TC3** `TestVerifyTamperedChecksums` → `ErrSignature`; `TestVerifyTamperedArchive` → `ErrChecksum`.
- **TC4** `TestVerifyNoTlog` → `ErrTlog`; `TestVerifyExpiredRoot` → fails closed.
- **TC5** `TestManifestVersionMismatch`, `TestManifestBadPlatformDigest` → `ErrManifest`.
- **TC6** `TestConstraintsWithoutHashes` → `ErrNoHashes`.
- **TC7** `TestWatermarkDowngrade`, `TestWatermarkReplaySameVersion`, `TestReplayOlderIntegratedTime`.
- **TC8** `TestSelfUpdateUsesReleaseverify` (api/internal/system), `TestCLIUpdateGitHubOnly`
  (cmd/maktaba-server).
- **TC9** CI job `release-manifest-check` on a dry-run tag: schema_rev and MANIFEST markers.

## Edge cases

- **EC1** A `checksums.txt` with duplicate names or non-hex digests is `ErrManifest`, not "first wins".
- **EC2** A release that adds a platform the running machine is not: verification of the other
  platforms' files is skipped; only files to install are hashed.
- **EC3** Clock far off on a machine: certificate validity is checked against the Rekor integrated time,
  not the local clock; TUF expiry uses the local clock and its failure is reported as such.
- **EC4** Releases published before this story have no bundle: they verify only by the legacy `.sig`/`.pem`
  path through `sigstore-go`'s detached-signature support, or are refused for automatic install (manual
  only); the Updates page says "unsigned bundle: legacy release".

## Dependencies

Epic 22.5 (`release.yml`), Epic 28.1–28.3 (built), PR #94 (hash-checked constraints).
