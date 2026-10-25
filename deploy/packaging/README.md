# Server-side native packaging

This directory owns native packaging for the **Maktaba home server** — the
unified `maktaba-server` binary that supervises the API, streaming, and
pipeline roles and serves the embedded web UI. Mobile / desktop / TV
packaging lives in `apps/` (Story 11).

## What each tag release produces

The [`release.yml`](../../.github/workflows/release.yml) workflow runs on a
`v*` tag and publishes these assets to the GitHub Release:

| Artifact | Built by | Platforms |
|---|---|---|
| `maktaba-server-<ver>-<os>-<arch>.tar.gz` | `package-archives` | linux/{amd64,arm64}, darwin/{amd64,arm64} |
| `maktaba-server-<ver>-windows-amd64.zip` | `package-archives` | windows/amd64 |
| `maktaba-server_<ver>-1_<arch>.deb` | `package-nfpm` (nfpm) | amd64, arm64 |
| `maktaba-server-<ver>-1.<arch>.rpm` | `package-nfpm` (nfpm) | x86_64, aarch64 |
| `maktaba.rb` (Homebrew formula) | `sign-and-release` | macOS arm64 + amd64; [`packages.yml`](../../.github/workflows/packages.yml) commits it to the tap, with the Mac app's cask, once the release is published as the newest stable one |
| `maktaba-cloud-<os>-<arch>` (relay) | `build-artifacts` | linux/{amd64,arm64} |
| `maktaba-web-<tag>.tar.gz` (web bundle) | `build-artifacts` | n/a |
| `maktaba-linux-payload-<ver>-<x86_64\|aarch64>.tar.gz` | `package-linux-payload` ([`tools/release/linux-app`](../../tools/release/linux-app/README.md)) | the Linux app's server payload (Go binaries, Postgres 16 with its libraries, Python, the pipeline wheel; ffmpeg is the distribution's) for a machine without a desktop: `install.sh --app` ([Epic 39](../../specs/epics/39-linux-app/headless.md)) |
| `maktaba-node-<ver>.tar.gz` / `.zip` (node installer + wheel) | `package-node` | macOS + Linux / Windows; see [`node/README.md`](node/README.md#getting-the-installer) |
| `maktaba_pipeline-<ver>-py3-none-any.whl` + `maktaba-pipeline-<ver>-constraints.txt` | `package-node` | any (pure Python, 3.12+) |
| `release.json` | `sign-and-release` (`tools/release/cmd/gen-release-json`) | version, schema revisions, protocol numbers, per-platform archive + binary digests ([Epic 35 design §3.1](../../specs/epics/35-cluster-full-nodes-and-updates/design.md)); listed in `checksums.txt` |
| `checksums.txt` + `checksums.txt.sigstore.json` (+ legacy `.sig` / `.pem`) | `sign-and-release` | keyless cosign; the bundle carries the Rekor inclusion proof, so machines verify offline. Identity: exactly `https://github.com/Hamza-Labs-Core/Maktaba/.github/workflows/release.yml@refs/tags/<tag>` ([how](../../docs/auto-update.md#verifying-server-artifacts-manually)) |
| Container images (multi-arch) | `build-images` | linux/{amd64,arm64} on GHCR |

Each archive is flat and contains the `maktaba-server`, `maktaba-api`, and
`maktaba-streaming` binaries (the unified server forks the latter two at
runtime), plus `README.md`, `LICENSE`, `server.toml.example`, and — on
Linux — the systemd unit.

### Linux: the app, or these packages

The **Linux app** ([Epic 39](../../specs/epics/39-linux-app/README.md)) is the one installation
that runs everything for a person: the AppImage / `.deb` / `.rpm` from
[`desktop-release.yml`](../../.github/workflows/desktop-release.yml), or `install.sh --app` on a
machine without a desktop. It runs as the user's systemd **user** service from a copy of the payload
in `~/.local/share/maktaba/runtime`, with its own Postgres and Python and the distribution's
ffmpeg (the `.deb` depends on `ffmpeg`, the `.rpm` requires it: RPM Fusion's on Fedora and RHEL,
whose `ffmpeg-free` has no libx264; setup checks the one found and names the command that installs
the right one, see [ffmpeg](../../tools/release/linux-app/README.md#ffmpeg-the-distributions)). It
starts at boot when linger is on: setup turns it on, and polkit may ask for an administrator's
password for that (over SSH, in the terminal); without it the service runs while the user is
logged in. Nothing else needs root but installing that ffmpeg when the system has none.

- **Updates.** With a desktop, the app's updater installs each release's signed update (the
  AppImage in place, a `.deb` / `.rpm` through the system's admin prompt) once its signature
  matches the key built into the app ([auto-update](../../docs/auto-update.md)), and the app moves
  the server to the new payload at its next start. Without a desktop, an admin updates from the
  signed releases: Update now in the web app, or `maktaba-server update` (`--check` only verifies,
  `--rollback` goes back to the version that ran before); each verifies the release, installs its
  payload beside the running one and switches to it. It also installs updates by itself in the
  quiet window (`rules.updates`, Epic 35's update orchestrator; docs/auto-update.md).
- **A machine without a desktop, verified.** `install.sh --app` never runs a payload it has not
  verified: it checks the Sigstore signature over the release's `checksums.txt` (with the
  machine's cosign, or cosign's own release build, which it fetches and checks against a pinned
  SHA-256), then the payload's SHA-256; `MAKTABA_NO_VERIFY` and root are refused. A cosign on the
  PATH is used only from 2.4.3 on, the first that reads the release's (new format) Sigstore bundle;
  an older one is passed over for the pinned build. Take the script from the release, where
  `checksums.txt` lists it, and check it first if you want the same guarantee for the script
  itself (with cosign 2.4.3 or newer: Debian 13 packages 2.5.0, Ubuntu from 26.04 2.6.2, Fedora
  its own; elsewhere cosign's release build, checked against its `cosign_checksums.txt`):

  ```sh
  v=v1.5.0   # the release
  base=https://github.com/Hamza-Labs-Core/Maktaba/releases/download/$v
  curl -fLO "$base/install.sh" -fLO "$base/checksums.txt" -fLO "$base/checksums.txt.sigstore.json"
  cosign verify-blob --bundle checksums.txt.sigstore.json \
    --certificate-identity "https://github.com/Hamza-Labs-Core/Maktaba/.github/workflows/release.yml@refs/tags/$v" \
    --certificate-oidc-issuer https://token.actions.githubusercontent.com checksums.txt
  sha256sum --check --ignore-missing checksums.txt
  MAKTABA_VERSION=$v bash install.sh --app
  ```

- **Uninstall.** `~/.local/share/maktaba/runtime/current/bin/maktaba-server service uninstall`
  stops, disables and removes the service. Removing the `.deb` / `.rpm` (`sudo apt remove
  maktaba`, `sudo dnf remove maktaba`) stops and disables it for every user whose systemd user
  manager runs (logged in, or lingering); another user's starts again at their next login until
  they run `service uninstall` or `systemctl --user disable --now maktaba-server`. An AppImage has
  no removal step: deleting it leaves the server running until `service uninstall`. The data stays
  in `~/.local/share/maktaba`, `~/.config/maktaba`, `~/.local/state/maktaba` and
  `~/.cache/maktaba` until they are deleted.
- **Systems.** x86_64 or aarch64 with glibc 2.35 or newer and a systemd user manager. The desktop
  packages' tray library (`libayatana-appindicator`) comes from EPEL on RHEL, Alma and Rocky 10;
  the payload for a machine without a desktop needs nothing else but the distribution's ffmpeg,
  which its setup checks and offers to install.

The `maktaba-server` `.deb` / `.rpm` and archives here install the server binaries alone, as a
**system** service (`User=maktaba`, `/var/lib/maktaba`), for operators who bring their own
Postgres, ffmpeg and pipeline environment. The app's user unit has the name a self-managed user
install (`setup --self-managed`) uses; `maktaba-server service install` refuses a unit that another
install wrote.

## Files in this directory

| Path | Purpose |
|---|---|
| `nfpm.yaml` | [nfpm](https://nfpm.goreleaser.com) config → `.deb` + `.rpm` |
| `systemd/maktaba-server.service` | hardened systemd unit (`/usr/lib/systemd/system`) |
| `etc/maktaba-server.toml` | default config installed by the packages → `/etc/maktaba/server.toml` |
| `scripts/postinstall.sh` | create `maktaba` user, dirs, enable the service on a first install; on an upgrade restart it if it was running (migrations are the release's own start's: its database step) |
| `scripts/preremove.sh` | on uninstall, stop and disable the service (an upgrade leaves it to postinstall) |
| `scripts/postremove.sh` | `systemctl daemon-reload` after removal |
| `homebrew/maktaba.rb` | Homebrew formula **template** (`__VERSION__` / `__SHA256_*__` filled at release; the server, self-managed: its service runs the launcher) |
| `homebrew/maktaba-app.rb` | Homebrew cask **template** for the Mac app (filled by `packages.yml` from the release's checked app bundle) |
| `launchd/com.maktaba.*.plist` | macOS launchd units |
| `install.sh` | curl-pipe-bash installer (detect OS/arch, download, verify, install) |
| `node/` | worker-only install for a machine that joined a home (Epic 31): launchd agent, systemd units, Windows task/service, installers, and `build-release.sh` for the release's node assets — see [`node/README.md`](node/README.md) |

## Install paths (.deb / .rpm)

Side-by-side layout (Epic 35, story 35.2): the package owns the launcher, the
updater owns the releases.

```
/usr/bin/maktaba-launcher                     what the service runs (package-owned)
/usr/bin/maktaba-server                       CLI wrapper: runs the current release
/usr/lib/maktaba/release/                     the binaries this package shipped
/var/lib/maktaba/bin/releases/<version>/      releases (postinstall copies the package's here)
/var/lib/maktaba/{current,previous,pending,watermark}   pointer files
/var/lib/maktaba/updates/launcher.log         launcher events (start, restart, rollback)
/usr/lib/systemd/system/maktaba-server.service     systemd unit (ExecStart = the launcher)
/etc/maktaba/server.toml                      config (conffile — survives upgrades)
/var/lib/maktaba, /var/log/maktaba            state + logs (owned by maktaba user)
/usr/share/doc/maktaba-server/                README + LICENSE
```

postinstall writes `current` only when the package is newer than `watermark`
(a package older than what the updater installed only refreshes the
launcher). `maktaba-launcher --status` shows current, previous, pending and
the last rollback.

## Self-managed installs (archive, manual, Homebrew, Windows)

```sh
maktaba-server setup --self-managed            # copy the running binaries into the layout, write the service
maktaba-server setup --self-managed --dry-run  # print what it would do
maktaba-server setup --service [--user|--system]   # only (re)write the OS service
```

| OS | Default root | Service |
|---|---|---|
| Linux (root) | `/var/lib/maktaba` | systemd system unit |
| Linux (user) | `~/.local/share/maktaba` | systemd user unit + linger |
| macOS | `~/Library/Application Support/Maktaba/server` | LaunchAgent (`--system`: LaunchDaemon, `/usr/local/var/maktaba`) |
| Windows | `%LOCALAPPDATA%\Maktaba` | logon task "Maktaba" (`--system`: Windows service, `%ProgramData%\Maktaba`) |

On Windows the Maktaba app (Epic 38, `Maktaba_<ver>_x64-setup.exe` on the release)
is the install: it carries the server, Postgres, ffmpeg and Python, runs setup in
its window and starts the server at sign-in. Its setup finds an install made here
(the "Maktaba" task) and moves it into the app.

The conversion copies (does not move) `maktaba-server`, `maktaba-api`,
`maktaba-streaming` and `maktaba-launcher` into `bin/releases/<version>/`,
records their digests in `.verified` (`identity: "migrated"`), writes
`current` and `watermark`, puts the launcher at the root and rewrites the
service. `server.toml`, `secrets.key` and `node.json` stay where they are.

The **Homebrew formula** is self-managed already, as the `.deb`/`.rpm` are: brew owns the
launcher, the updater owns the releases. Its service stages the formula's release under
`$(brew --prefix)/var/maktaba/server` (`setup --self-managed --identity package`, only when it
is newer than what the updater installed there) and runs the launcher, so the server installs
each new release by itself in its quiet window; `brew upgrade maktaba` brings a new launcher
(the one thing a release can need from brew: the updater says so, `launcher_protocol`). The
`maktaba-server` on PATH runs the service's current release. A formula from before this
(its service ran `maktaba-server serve` itself) takes the launcher at its service's next
restart (`brew services restart maktaba`).

## Installing

```sh
# Debian / Ubuntu, from the repository (every stable release: maktaba-server, and the Linux
# app, maktaba)
curl -fsSL https://packages.maktaba.dev/maktaba.gpg | sudo tee /usr/share/keyrings/maktaba.gpg >/dev/null
curl -fsSL https://packages.maktaba.dev/apt/maktaba.list | sudo tee /etc/apt/sources.list.d/maktaba.list
sudo apt update && sudo apt install maktaba-server
# ... or one release's package
sudo apt install ./maktaba-server_<ver>-1_amd64.deb

# Fedora / RHEL / CentOS, from the repository
sudo curl -fsSL -o /etc/yum.repos.d/maktaba.repo https://packages.maktaba.dev/rpm/maktaba.repo
sudo dnf install maktaba-server
# ... or one release's package
sudo dnf install ./maktaba-server-<ver>-1.x86_64.rpm

# macOS (Homebrew tap): the server (it updates itself), or the Mac app
brew install hamza-labs-core/tap/maktaba
brew install --cask hamza-labs-core/tap/maktaba-app

# Linux, the whole server for a machine without a desktop (Epic 39): the app's
# setup in the terminal, a systemd user service; run as that user, not root, with
# the release's own install.sh (verified: "Linux: the app, or these packages")
curl -fsSLO https://github.com/Hamza-Labs-Core/Maktaba/releases/latest/download/install.sh
bash install.sh --app

# Any Linux/macOS (script), from a release (verified), with the system service: the
# side-by-side layout, the launcher and the unit (`setup --self-managed --system`),
# so the install updates itself; the `maktaba-server` on PATH then runs the service's
# current release (the archive's binaries are its fallback, in <prefix>/../lib/maktaba,
# or MAKTABA_LIBDIR)
curl -fsSLO https://github.com/Hamza-Labs-Core/Maktaba/releases/latest/download/install.sh
sudo MAKTABA_SYSTEMD=1 bash install.sh
```

After installing a package: edit `/etc/maktaba/server.toml` (set your
`[media].roots`), then `sudo systemctl start maktaba-server`. The web UI is
on port `8088`. An upgrade of the package restarts a running service; the new
release's start dumps and migrates the database (the database step), as an
automatic update does.

## Local validation

```sh
# Build the binaries the package references, then dry-run nfpm:
make server                       # builds cmd/maktaba-server/bin/.../maktaba-server
go install github.com/goreleaser/nfpm/v2/cmd/nfpm@latest
mkdir -p pkgroot
cp <staged binaries> pkgroot/{maktaba-server,maktaba-api,maktaba-streaming}
ARCH=amd64 VERSION=0.0.0 nfpm package -f deploy/packaging/nfpm.yaml -p deb -t /tmp/out/
ARCH=amd64 VERSION=0.0.0 nfpm package -f deploy/packaging/nfpm.yaml -p rpm -t /tmp/out/

# Lint the shell:
shellcheck deploy/packaging/install.sh deploy/packaging/scripts/*.sh
```

## Release flow (Story 22.5 + 22.6)

1. Tag main with `v{MAJOR}.{MINOR}.{PATCH}` (annotated, signed), or `-rc.N` for a release
   candidate.
2. **The tag guard** (`_tag-guard.yml`, `tools/release/tag-guard.sh`) runs first in every
   tag workflow (release, desktop, mobile, tv): it refuses a tag at or below a published
   release (an rc of a version already released among them), a commit that is not on
   `main`, one whose history lacks the newest published release's commit, and one without a
   passing CI run (it waits for CI up to an hour; CI runs on `main` and `release/**`). A
   deliberate maintenance line (a hotfix on an older minor, its commit on `main` or a
   `release/*` branch) passes with the repository variable `MAKTABA_RELEASE_ALLOW_TAG` naming
   the tag, and never becomes the latest.
3. **Draft first.** The guard opens the tag's release as a draft; every workflow uploads to
   it, and whichever finishes last publishes it, once `release.json`, `checksums.txt` and its
   signature are in and the apps' update feed has every server-carrying app. It becomes
   GitHub's *latest* only when it is the highest stable release and every desktop platform is
   in the feed. Each workflow runs once per tag at a time (a per-tag concurrency group).
4. **A published release is never rewritten**: a re-run of a published tag builds and signs
   nothing (only a draft is rebuilt); a desktop re-run keeps the published bundles and their
   signatures, and adds only a missing leg's; a re-run fails when its `checksums.txt` differs.
   The artifacts are reproducible (`published` and `built_at` from the tag's time, sorted
   fixed-time archives, `SOURCE_DATE_EPOCH`, buildx `rewrite-timestamp`). A failed leg is
   recovered with `gh run rerun <run> --failed`.
5. The release manifest (`release-manifest.json`, inside the signed checksums) records the
   version, sha, build time, and the sha256 of every published image (release.yml alone
   publishes the images of a tag, `maktaba-cloud` among them). The images' `:latest` follows
   the latest release only (never an rc or a maintenance release), and the relay is deployed
   only for the latest release, once it is published (its images signed) and its own lint,
   tests and build passed.
6. `release.json`'s previous release is the highest published version below the tag (for a
   stable tag, the previous stable): it decides `breaks_previous_home` and
   `min_upgrade_from`.
7. The embedded Sigstore trusted root is refreshed by `trusted-root.yml` every week (a PR
   when it changed or is 45 days old; set `MAKTABA_BOT_TOKEN` so that PR runs CI). Lint warns
   from day 60 and fails at day 90 (`tools/release/trusted-root-age.sh`).
8. Upgrade: an automatic update (docs/auto-update.md), or the package / image by hand.
   Rollback: the updater's own; by hand, see `upgrade.md`.

### Package repositories and the Homebrew tap

[`packages.yml`](../../.github/workflows/packages.yml) brings them up to each stable release by
itself once it is published (it runs when release.yml or desktop-release.yml completes, and acts
only on a published release that is the newest stable one; a pre-release, a draft or a
maintenance release changes nothing). It publishes only the release's own files, checked first:
the server's packages and the formula against the release's Sigstore-signed `checksums.txt`, the
Linux app's packages and the Mac app's bundle against the apps' updater key. Nothing changes what
a machine verifies: an installed server still verifies every release it installs itself.

- **apt and dnf** ([`tools/release/pkg-repo.sh`](../../tools/release/pkg-repo.sh)): one static
  tree (`apt/`: `dists/stable`, `pool/main`; `rpm/`: `repodata`, `packages`) in an S3-compatible
  bucket (Cloudflare R2 by default) served at `https://packages.maktaba.dev`, with the three
  newest versions of each package. The indexes are signed with the repository's key (apt's
  `InRelease`, dnf's `repomd.xml.asc`, `repo_gpgcheck=1`) and name every package's SHA-256; the
  packages stay the release's bytes. The key's public half is `maktaba.gpg` / `maktaba.asc` at
  the root, and `index.html` says how to add the repositories. Configuration: the variables
  `MAKTABA_PACKAGES_URL`, `MAKTABA_PACKAGES_BUCKET`, `MAKTABA_PACKAGES_ENDPOINT` (R2's S3 API;
  empty for AWS, with `MAKTABA_PACKAGES_REGION`), and the secrets `MAKTABA_PACKAGES_ACCESS_KEY_ID`,
  `MAKTABA_PACKAGES_SECRET_ACCESS_KEY`, `MAKTABA_REPO_SIGNING_KEY` (the armored private key) and
  `MAKTABA_REPO_SIGNING_PASSPHRASE` (when it has one).
- **Homebrew** ([`tools/release/homebrew-tap.sh`](../../tools/release/homebrew-tap.sh)): the
  release's `maktaba.rb` to `Formula/maktaba.rb` and the Mac app's cask to
  `Casks/maktaba-app.rb`, one commit; the tap is never taken back to an older release.
  Configuration: the variable `HOMEBREW_TAP_REPO` (e.g. `hamza-labs-core/homebrew-tap`) and the
  secret `HOMEBREW_TAP_TOKEN` (a token with `contents:write` on the tap).

Each part is skipped, with a notice on the run, until its configuration is there. The Linux app's
packages and the cask also need `TAURI_UPDATER_PUBKEY` (without it the apps cannot update
either, and they are left out).
