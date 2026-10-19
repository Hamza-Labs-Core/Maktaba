# Plan 35.1 — Signed release manifest and verification — implementation

> Implementation plan for [story-35-01-release-manifest-verification](story-35-01-release-manifest-verification.md). Design: [design.md](design.md) §3.

## 0. Decisions

| # | Decision | Rationale |
|---|---|---|
| D1 | Verify in process with `github.com/sigstore/sigstore-go`, no `cosign` binary. | Nodes and Windows machines do not have cosign; one code path everywhere. |
| D2 | Exact SAN for the tag, not the release notes' regexp. | `https://github.com/.+/Maktaba/…` accepts any fork; the tag binding stops replaying another tag's signature. |
| D3 | Sigstore bundle (`--new-bundle-format`) next to the legacy `.sig`/`.pem`. | The bundle carries the Rekor proof: offline verification on nodes. |
| D4 | `release.json` is covered by `checksums.txt`, not signed separately. | One signature; a manifest digest mismatch is a checksum failure. |
| D5 | One module `shared/go/releaseverify` imported by api, cmd/maktaba-server and the launcher's `--verify`. | No drift between the home and nodes. |
| D6 | Watermark checks live in the same package (`CheckMonotonic`). | Downgrade and replay rules applied identically wherever something is staged. |

## 1. Files

**Create**
- `shared/go/releaseverify/verify.go` — `Verify(Input) (Result, error)`, typed errors, identity constants
- `shared/go/releaseverify/manifest.go` — `release.json` types and parsing; `checksums.txt` parser
- `shared/go/releaseverify/root.go` — embedded `trusted_root.json`, TUF refresh into `<data_dir>/sigstore/`
- `shared/go/releaseverify/monotonic.go` — watermark, replay, integrated-time rules
- `shared/go/releaseverify/testdata/` — fixture release (bundle, checksums, release.json, tiny archives)
  and negative variants
- `tools/release/gen-release-json.go` (new directory) — builds `release.json` from the built archives, the migrations
  MANIFEST and the schema revs
- `.github/workflows/_release-fixture.yml` — manual workflow that signs the test fixture once

**Modify**
- `.github/workflows/release.yml` — `sign-and-release`: run `gen-release-json`, add it to checksums,
  `cosign sign-blob --bundle checksums.txt.sigstore.json --new-bundle-format`, upload; a
  `release-manifest-check` step (schema_rev, MANIFEST markers); release notes print the exact identity
- `api/internal/system/selfupdate.go` — download → `releaseverify.Verify` → swap; delete local
  `lookupChecksum`/`sha256hex` path
- `api/internal/system/updater.go` — fetch the three small files per release; record verify state
  (consumed by 35.3)
- `cmd/maktaba-server/internal/selfupdate/selfupdate.go`, `cmd/maktaba-server/cmd_update.go` — GitHub
  Releases + `releaseverify`; drop `DefaultManifestURL`
- `deploy/packaging/install.sh` — verify the bundle with `cosign verify-blob --bundle` when cosign is
  present, else print the exact command
- `shared/db/migrations/MANIFEST.md` — document the "Breaking for the previous release" marker

## 2. Steps

1. Manifest types, `checksums.txt` parser, generator tool and its unit tests.
2. `release.yml` changes on a dry-run tag in a fork-free branch; produce the fixture via the manual
   workflow; commit fixtures.
3. `Verify` on top of sigstore-go (`verify.NewSignedEntityVerifier` with `WithTransparencyLog(1)`,
   `WithIntegratedTimestamps(1)`, certificate identity with exact SAN and extensions).
4. Trusted root embedding and TUF refresh; expiry → critical notification hook (interface, wired in 35.13).
5. Monotonic rules.
6. Switch Epic 28.3 and the CLI; remove the old feed.

## 3. Tests

Story TCs; plus `FuzzParseChecksums`, `FuzzParseReleaseJSON`; a CI check that the embedded root is not
older than 90 days at build time.

## 4. Rollout

Phase A. The first release with `release.json` is the minimum version for automatic installs
(`min_upgrade_from` of later releases); older releases remain installable only by hand (EC4).
