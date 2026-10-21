# Auto-Update (Epic 28)

Maktaba keeps itself current from **GitHub Releases**
(`Hamza-Labs-Core/Maktaba`). This page covers operator configuration, the
update channels, and the one-time signing-key setup the desktop updater
needs.

## How it works

| Surface | Mechanism |
|---|---|
| All binaries | Version/commit/build-date stamped at compile time; reported at `GET /api/system/version` (`{version, commit, build_date, channel}`). |
| Server | Background poller hits the GitHub Releases API, caches the result, serves `GET /api/system/updates`. |
| Server (admin) | `POST /api/admin/system/update {"confirm":true}` downloads the release and verifies it in process (below), refuses a downgrade, swaps the binary (keeps `.bak`), raises the watermark, re-execs. |
| CLI | `maktaba-server update` reads the same GitHub Releases feed and verifies with the same code (`--check`, `--version vX.Y.Z`, `--rollback`). |
| Desktop (Tauri) | Built-in updater reads the Release's `latest.json`, applies on relaunch (signature-verified). |
| Mobile | In-app banner → App Store / Play Store / `.apk` (apps can't self-update). |

## Channels

- **stable** (default) — ignores prereleases.
- **beta** — includes `-beta`/`-rc` prereleases.

A build's channel is derived from its version string (a prerelease
suffix ⇒ `beta`), overridable with `MAKTABA_UPDATE_CHANNEL`. Switching
`beta`→`stable` never downgrades a running server.

## Server configuration (env)

| Variable | Default | Meaning |
|---|---|---|
| `MAKTABA_UPDATE_CHANNEL` | derived from version | `stable` or `beta`. |
| `MAKTABA_UPDATE_INTERVAL_SEC` | `86400` | Background check cadence; `0` disables the poller. |
| `MAKTABA_UPDATE_DISABLE` | unset | `1`/`true` = no network checks at all. |
| `MAKTABA_UPDATE_REPO` | `Hamza-Labs-Core/Maktaba` | Retarget a fork's releases. It changes the expected signing identity too (the fork's own `release.yml`); it is a startup setting, no API field can change it. |
| `MAKTABA_DATA_DIR` | `/var/lib/maktaba` | The Sigstore trusted root refreshed through TUF is kept in `<data_dir>/sigstore/`. |
| `MAKTABA_GITHUB_TOKEN` | unset | Optional; raises the GitHub API rate limit. |

## Install-type behaviour for self-update

- **Plain binary / Homebrew / manual archive** — binary swap + re-exec,
  with a `.bak` kept for rollback on a failed post-restart health check.
- **`.deb` / `.rpm`** — the server invokes the package manager
  (`apt-get install --only-upgrade` / `dnf upgrade`) so package state
  stays consistent.
- **Docker** — returns `409` with
  `docker compose pull && docker compose up -d` (a container can't
  replace its own image).

## Desktop updater signing (one-time)

Tauri's updater requires a key pair. The **public** key is committed in
`apps/desktop/src-tauri/tauri.conf.json`; the **private** key lives only
as CI secrets.

```bash
# On a trusted machine, generate the key pair:
cd apps/desktop
pnpm exec tauri signer generate -w ~/.tauri/maktaba.key

#  → prints a PUBLIC key   → paste into tauri.conf.json:
#       plugins.updater.pubkey   (replace REPLACE_WITH_PUBKEY)
#  → writes the PRIVATE key → add as the repo secret:
#       TAURI_SIGNING_PRIVATE_KEY
#    and its passphrase as:
#       TAURI_SIGNING_PRIVATE_KEY_PASSWORD
```

`desktop-release.yml` reads those secrets so `tauri build` signs each
updater bundle (`.sig`). Its `publish` job, after every platform's build
and checks, attaches the bundles that passed to the Release and writes
`latest.json` (the updater feed, `tools/release/cmd/updater-feed`) over
them. A fork **without** the secrets still produces unsigned installers
and no `latest.json` — the build stays green.

**Key rotation:** ship one release signed by both the old and new keys
before swapping the committed `pubkey`, so already-installed apps can
still verify the transition release.

> Note: the updater signature is independent of OS code-signing
> (Apple notarisation / Windows Authenticode), which remains the deferred
> TODO in `desktop-release.yml`.

## How a release is verified (Epic 35 story 35.1)

Every release publishes, next to its archives:

- `release.json`: version, tag, commit, schema revisions (`schema_rev`,
  `min_node_schema`, `breaks_previous_home`), protocol numbers, the
  per-platform archive with the SHA-256 of every binary inside it, the
  pipeline wheel and constraints. It is listed in `checksums.txt`.
- `checksums.txt` and `checksums.txt.sigstore.json`, a Sigstore bundle
  (`cosign sign-blob --new-bundle-format`) holding the Fulcio certificate,
  the signature and the Rekor entry with its inclusion proof. The legacy
  `checksums.txt.sig` / `.pem` stay for humans.

The server, `maktaba-server update` and (later) every node verify with one
in-process package, `shared/go/releaseverify` (no cosign binary, no
network needed):

1. the bundle against the Sigstore trusted root, with at least one Rekor
   inclusion proof and a signed log time inside the certificate's validity
   (checked against that time, not the local clock);
2. the certificate identity, exactly: issuer
   `https://token.actions.githubusercontent.com`, SAN
   `https://github.com/Hamza-Labs-Core/Maktaba/.github/workflows/release.yml@refs/tags/<tag>`,
   source repository `https://github.com/Hamza-Labs-Core/Maktaba`, ref
   `refs/tags/<tag>`, trigger `push`, build config (the workflow the tag
   push started) the same `release.yml@refs/tags/<tag>`, a GitHub-hosted
   runner, and GitHub's numeric repository and owner ids (so a repository
   re-registered under the same name after a rename is refused). A fork,
   another workflow (also one calling `release.yml`), a branch run, a
   self-hosted runner or another tag's signature is refused; `release.json`'s
   `commit` must be the commit the signing run was built from;
3. `checksums.txt` (duplicates or bad digests are refused), then
   `release.json` against it and against the tag, every downloaded file
   against `checksums.txt`, the extracted `maktaba-server` against
   `release.json`, and every constraints line has `--hash=sha256:`;
4. the watermark, the highest version this machine ever installed
   (`maktaba-server.watermark` next to the binary): a lower version is
   refused, the same version with other checksums is refused, and so is a
   release signed before the installed one. `maktaba-server update
   --rollback` (the kept `.bak`) is the only way to run a lower version;
   it does not lower the watermark.

Releases published before story 35.1 have no bundle and no `release.json`:
they are never installed automatically (install them by hand).

**Trusted root.** The public-good Sigstore `trusted_root.json` is compiled
into the binary (`shared/go/releaseverify/trusted_root.json`, refreshed by
`go generate` there; a release refuses to build with one older than 90
days). The server refreshes it daily through Sigstore's TUF repository,
anchored on the TUF root compiled into sigstore-go, into
`<data_dir>/sigstore/`; the CLI refreshes into the user cache directory
before it verifies. A failed refresh keeps the last good root; a root not
refreshed for 180 days is treated as expired and verification fails closed.
The cache is trusted only while no other user can write it (the directory
and its parents owned by the running user or root and not group/other
writable, no symlinked files) and its `fetched_at` is not in the future;
otherwise the embedded root is used. So `sudo maktaba-server update` with
the invoking user's `$HOME` never trusts a root that user could have planted.

`install.sh` fails closed as well: a release missing `checksums.txt`, its
Sigstore bundle or the archive's entry is refused (`MAKTABA_NO_VERIFY=1`
overrides). Without `cosign` installed it can only check the SHA-256 (it
warns); `MAKTABA_REQUIRE_COSIGN=1` makes a missing cosign fatal.

## Verifying server artifacts manually

```bash
v=1.5.0
cosign verify-blob --bundle checksums.txt.sigstore.json \
  --certificate-identity "https://github.com/Hamza-Labs-Core/Maktaba/.github/workflows/release.yml@refs/tags/v$v" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  checksums.txt
sha256sum --ignore-missing -c checksums.txt
```

Use the exact `--certificate-identity`, never a
`--certificate-identity-regexp` over the repository name: that accepts a
fork's release workflow too. `--certificate checksums.txt.pem --signature
checksums.txt.sig` in place of `--bundle` checks the same identity. A fork
(`MAKTABA_UPDATE_REPO`) uses its own `owner/name` in the identity.
