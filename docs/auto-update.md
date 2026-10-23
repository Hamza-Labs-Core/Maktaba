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
| Server (automatic) | The home installs a new signed release in its quiet window, then every joined machine, one at a time (Epic 35: [the whole cluster](#automatic-updates-of-the-whole-cluster-epic-35), below); a desktop app that runs the server stages it and installs it when someone clicks there. |
| Server (admin) | Admin -> Updates (`/admin/updates`): Update now, Check now, Skip, pause; a run's steps on the home and on each machine, Cancel (before the home switches), Retry, Skip and Roll back a machine, Resume a held rollout. `POST /api/admin/system/update {"confirm":true}` is Update now. |
| CLI | `maktaba-server update` reads the same GitHub Releases feed and verifies with the same code (`--check`, `--version vX.Y.Z`, `--rollback`). |
| Desktop (Tauri) | Built-in updater reads the Release's `latest.json`, applies on relaunch (signature-verified). An app that runs the server stages the release the home's update orders and installs it on a click there (below); a client-only app keeps its own "restart to install" badge. |
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
| `MAKTABA_DATA_DIR` | `maktaba-server`'s `data_dir`; run on its own, `/var/lib/maktaba` as root, else `$XDG_DATA_HOME/maktaba` (`~/.local/share/maktaba`) | The Sigstore trusted root refreshed through TUF is kept in `<data_dir>/sigstore/`. |
| `MAKTABA_GITHUB_TOKEN` | unset | Optional; raises the GitHub API rate limit. |

## Automatic updates of the whole cluster (Epic 35)

The home checks GitHub every 6 hours. A verified release newer than the cluster's
(`rules.updates`: channel, pin, skipped versions, deferral, release age, the quiet window,
default `03:00-05:00`) is installed with no one at a keyboard on headless and plain server
installs; a desktop app that runs the server (Maktaba.app, the Windows or Linux app) gets it
staged and installs it when someone clicks "Install" there (the steps below wait for that
click):

1. **The home prepares** it: downloads and verifies it (the signature, every file),
   stages it, runs it once (`--version`), and rehearses its migrations on a scratch copy
   of the database (a migration that fails on the real data stops here; nothing changed).
2. **It waits for its moment**: inside the window, with nobody watching TV
   (`tv_staging.quiet_max_viewers`, 0): the home restarts, live TV with it. The
   cluster's jobs are drained.
3. **The home switches** to it and restarts. The new release's start dumps the database
   (`backups/pre-<old>-to-<new>.dump`, checked to read back), migrates it, and puts the
   dump back if the migrations fail. Its health is watched (every role running, the API
   ready and answering as the new version) for 10 minutes; then it is kept.
4. **Every machine follows**, after the home: they download and verify in parallel, then
   switch one at a time, each watched before the next (`node_failure_policy`, default
   `hold_platform`: one that fails holds the others of its platform). A machine offline
   meanwhile is caught up when it comes back, in the next window. A desktop app's machine
   waits for its click while the others go; its click takes the next turn. A run that waits
   only for clicks gives way to a newer release.

A release that does not stay healthy is **rolled back**: the previous release runs again.
When it had already migrated the database, the dump taken before its migrations is put
back first (every release refuses a database a newer one migrated); writes made between
that dump and the rollback are lost — minutes, inside the window, with the jobs drained —
and the failed state is dumped aside (`backups/failed-<version>-<time>.dump`). The release
is then blocked until a newer one appears or an admin retries it. A power cut at any step
is recovered at the next start from the update's journal (`updates/journal.json`).

How each kind of install takes a release
([install-kinds.md](../specs/epics/35-cluster-full-nodes-and-updates/install-kinds.md)):

| Install | Automatically | Still needs a person |
|---|---|---|
| **Maktaba.app** (Mac) | The server orders the release from the app (`updates/app-install.json`, or starts it with `maktaba://install-update`); the app downloads and checks exactly that release from its own updater feed and shows it ("Install Maktaba <v>"). After the click, the home's go: the app installs what it staged and restarts the server. Before, the server keeps a copy of the payload it ran, which the launcher runs if the new one cannot stay up. | **The click** in the app (owner decision D1). The first update from a release before automatic updates (the app's "Install update"). An app in `/Applications` that needs an admin password to replace. |
| **Windows app** | The same; the installer stops and starts the server. | The click. The first update from a release before automatic updates. |
| **Linux app** (AppImage, `.deb`, `.rpm`) | The server installs the release's signed Linux payload as a new runtime directory, and switches to it after the click in the app (no root needed); `previous` is kept for the rollback. | The click. The first update from a release before automatic updates. The app's own window updates with its updater. |
| **Headless** (`install.sh --app`) | The same runtime switch, by itself in the window. | The first update from a release before automatic updates. |
| **Plain server** on the side-by-side layout (`setup --self-managed`, `.deb`/`.rpm` in the layout) | Staged in `bin/releases/<v>` with its migrations and its own pipeline environment (`venv`, built from the release's wheel with the machine's Python 3.12), switched to under the launcher's guard; the pipeline runs the new environment. A release that needs a newer launcher replaces it after its commit. | A Python 3.12 for the pipeline (or `MAKTABA_PIPELINE_PYTHON`). |
| Plain binary copied by hand (no launcher) | — (reported: `not_self_managed`) | One `maktaba-server setup --service`, then automatic. |
| **Docker** | — | `docker compose pull && docker compose up -d` (Epic 28.3's `409`). |

Automatic installs are on by default (`rules.updates.auto_install`); with them off (Admin ->
Updates -> Pause) the home only checks and notifies. `MAKTABA_UPDATE_DISABLE=1` turns the
checks off altogether.

## Install-type behaviour for the manual self-update (Epic 28)

- **Plain binary / Homebrew / manual archive** — binary swap + re-exec,
  with a `.bak` kept for rollback on a failed post-restart health check.
- **`.deb` / `.rpm`** — the server invokes the package manager
  (`apt-get install --only-upgrade` / `dnf upgrade`) so package state
  stays consistent.
- **Docker** — returns `409` with
  `docker compose pull && docker compose up -d` (a container can't
  replace its own image).

## Desktop updater signing (one-time)

Tauri's updater requires a key pair. Both halves live in the repository
settings, never in a commit: the **private** key and its passphrase are
the secrets `TAURI_SIGNING_PRIVATE_KEY` and
`TAURI_SIGNING_PRIVATE_KEY_PASSWORD`, the **public** key is the variable
`TAURI_UPDATER_PUBKEY`. `desktop-release.yml` stamps the variable into
`plugins.updater.pubkey` at build time, so the committed
`apps/desktop/src-tauri/tauri.conf.json` keeps the placeholder
`REPLACE_WITH_PUBKEY`.

```bash
# On a trusted machine, generate the key pair:
cd apps/desktop
pnpm exec tauri signer generate -w ~/.tauri/maktaba.key

#  → prints the PUBLIC key  → repository variable TAURI_UPDATER_PUBKEY
#  → writes the PRIVATE key → repository secret TAURI_SIGNING_PRIVATE_KEY
#    and its passphrase as:   TAURI_SIGNING_PRIVATE_KEY_PASSWORD
```

`desktop-release.yml` reads those secrets so `tauri build` signs each
updater bundle (`.sig`). Its `publish` job, after every platform's build
and checks, attaches the bundles that passed to the Release and writes
`latest.json` (the updater feed, `tools/release/cmd/updater-feed`) over
them. A fork **without** the secrets still produces unsigned installers
and no `latest.json` — the build stays green.

The private key without the variable fails the build, since no app could
verify its updates. An app checks each update against the public key it
was built with. So apps update themselves from the first tagged release
built with the key on; an app installed from an earlier release carries
the placeholder, can verify no update, and moves to a signed release by
one download by hand.

**Key rotation:** build one release with the new public key in
`TAURI_UPDATER_PUBKEY` while the secrets still hold the old private key.
Installed apps verify that release with the old key, and once installed
it verifies the next ones with the new key. Then put the new private key
in the secrets.

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
