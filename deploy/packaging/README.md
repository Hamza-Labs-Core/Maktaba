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
| `checksums.txt` + `.sig` + `.pem` | `sign-and-release` | keyless cosign |
| Container images (multi-arch) | `build-images` | linux/{amd64,arm64} on GHCR |

Each archive is flat and contains the `maktaba-server`, `maktaba-api`, and
`maktaba-streaming` binaries (the unified server forks the latter two at
runtime), plus `README.md`, `LICENSE`, `server.toml.example`, and — on
Linux — the systemd unit.

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

## Install paths (.deb / .rpm)

```
/usr/bin/maktaba-server                       unified binary
/usr/bin/maktaba-api, /usr/bin/maktaba-streaming   forked role binaries
/usr/lib/systemd/system/maktaba-server.service     systemd unit
/etc/maktaba/server.toml                      config (conffile — survives upgrades)
/var/lib/maktaba, /var/log/maktaba            state + logs (owned by maktaba user)
/usr/share/doc/maktaba-server/                README + LICENSE
```

## Installing

```sh
# Debian / Ubuntu
sudo apt install ./maktaba-server_<ver>-1_amd64.deb

# Fedora / RHEL / CentOS
sudo dnf install ./maktaba-server-<ver>-1.x86_64.rpm

# macOS (Homebrew tap)
brew install hamza-labs-core/tap/maktaba

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
