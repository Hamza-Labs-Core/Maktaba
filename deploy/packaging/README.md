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
| `maktaba.rb` (Homebrew formula) | `sign-and-release` | macOS arm64 + amd64 |
| `maktaba-cloud-<os>-<arch>` (relay) | `build-artifacts` | linux/{amd64,arm64} |
| `maktaba-web-<tag>.tar.gz` (web bundle) | `build-artifacts` | n/a |
| `maktaba-linux-payload-<ver>-<x86_64\|aarch64>.tar.gz` | `package-linux-payload` ([`tools/release/linux-app`](../../tools/release/linux-app/README.md)) | the Linux app's server payload (Go binaries, Postgres 16 with its libraries, ffmpeg, Python, the pipeline wheel) for a machine without a desktop: `install.sh --app` ([Epic 39](../../specs/epics/39-linux-app/headless.md)) |
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
in `~/.local/share/maktaba/runtime`, with its own Postgres, ffmpeg and Python, and updates through
the app (or, without a desktop, through the signed releases).

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
| `scripts/postinstall.sh` | create `maktaba` user, dirs, run migrations, enable service |
| `scripts/preremove.sh` | stop (and on uninstall, disable) the service |
| `scripts/postremove.sh` | `systemctl daemon-reload` after removal |
| `homebrew/maktaba.rb` | Homebrew formula **template** (`__VERSION__` / `__SHA256_*__` filled at release) |
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

The conversion copies (does not move) `maktaba-server`, `maktaba-api`,
`maktaba-streaming` and `maktaba-launcher` into `bin/releases/<version>/`,
records their digests in `.verified` (`identity: "migrated"`), writes
`current` and `watermark`, puts the launcher at the root and rewrites the
service. `server.toml`, `secrets.key` and `node.json` stay where they are.
Homebrew installs are not converted automatically (brew owns the Cellar);
stop brew's service first (`brew services stop maktaba`).

## Installing

```sh
# Debian / Ubuntu
sudo apt install ./maktaba-server_<ver>-1_amd64.deb

# Fedora / RHEL / CentOS
sudo dnf install ./maktaba-server-<ver>-1.x86_64.rpm

# macOS (Homebrew tap)
brew install hamza-labs-core/tap/maktaba

# Linux, the whole server for a machine without a desktop (Epic 39): the app's
# setup in the terminal, a systemd user service, nothing as root
curl -fsSL https://raw.githubusercontent.com/Hamza-Labs-Core/Maktaba/main/deploy/packaging/install.sh | bash -s -- --app

# Any Linux/macOS (script)
curl -fsSL https://raw.githubusercontent.com/Hamza-Labs-Core/Maktaba/main/deploy/packaging/install.sh | bash
```

After installing a package: edit `/etc/maktaba/server.toml` (set your
`[media].roots`), then `sudo systemctl start maktaba-server`. The web UI is
on port `8088`.

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

1. Tag main with `v{MAJOR}.{MINOR}.{PATCH}` (annotated, signed).
2. The release workflow rebuilds artifacts from the tag commit. The
   image's `org.opencontainers.image.revision` label must match the
   tag's sha (CI gate).
3. The release manifest (`release-manifest.json`) records the version,
   sha, build time, and the sha256 of every published image.
4. Upgrade: clients pull the new image / package; `maktaba-server migrate
   up` runs forward migrations. Rollback: revert image + run the down
   migration to a known schema revision (see `upgrade.md`).

### Optional: Homebrew tap auto-publish

Set the `HOMEBREW_TAP_REPO` repo variable (e.g. `hamza-labs-core/homebrew-tap`)
and a `HOMEBREW_TAP_TOKEN` secret (a PAT with `contents:write` on the tap) to
have `sign-and-release` commit the generated `maktaba.rb` to `Formula/` on each
release. Without them, the formula is still attached as a release asset.
