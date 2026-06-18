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
| Server (admin) | `POST /api/admin/system/update {"confirm":true}` downloads the platform asset, verifies SHA-256 against `checksums.txt`, swaps the binary (keeps `.bak`), re-execs. |
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
| `MAKTABA_UPDATE_REPO` | `Hamza-Labs-Core/Maktaba` | Retarget a fork's releases. |
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

`desktop-release.yml` reads those secrets so `tauri-action` signs each
bundle and emits `latest.json` (the updater feed) onto the Release. A
fork **without** the secrets still produces unsigned installers and skips
`latest.json` — the build stays green.

**Key rotation:** ship one release signed by both the old and new keys
before swapping the committed `pubkey`, so already-installed apps can
still verify the transition release.

> Note: the updater signature is independent of OS code-signing
> (Apple notarisation / Windows Authenticode), which remains the deferred
> TODO in `desktop-release.yml`.

## Verifying server artifacts manually

The release `checksums.txt` is cosign-signed (keyless, Sigstore):

```bash
cosign verify-blob --certificate checksums.txt.pem \
  --signature checksums.txt.sig \
  --certificate-identity-regexp 'https://github.com/.+/Maktaba/.github/workflows/release.yml@.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  checksums.txt
```
