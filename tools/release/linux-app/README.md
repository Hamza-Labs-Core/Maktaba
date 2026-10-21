# The Linux server payload (Epic 39, story 39.2)

The Linux app carries the whole server in `usr/share/maktaba/server` (AppImage, .deb, .rpm), and a
machine without a desktop gets the same tree as a tarball. It is not among the app's resources
(`usr/lib/Maktaba`): linuxdeploy rewrites the rpath of, and strips, the ELF files it finds under
`usr/lib` when it makes the AppImage. The server never runs from there: the
app copies it into `~/.local/share/maktaba/runtime/<v>` first (story 39.3). This directory builds
the payload for x86_64 or aarch64.

| File | What it does |
|---|---|
| `payload.sh` | builds `build/linux-app/server` (layout below) for this machine's architecture, and writes the tauri overlay `build/linux-app/tauri.linux-app.conf.json` |
| `check.sh` | smoke test of the payload. It needs only bash and coreutils, so CI also runs it in minimal containers |
| `payload_tool.py` | the manifest (`files.sha256`), its verification, the ELF scan (glibc versions, libraries found), and Postgres's library closure. Standard library only: it runs with the payload's own Python |
| `pins.env` | the third-party downloads per architecture: version, URL, SHA-256 |
| `build-app.sh` | the app around the payload: `tauri build --bundles appimage,deb,rpm` with the overlay, then `check-app.sh` (story 39.10) |
| `check-app.sh` | checks each AppImage, .deb and .rpm: the shell, the payload in `usr/share/maktaba/server` installed the way the app installs it, the packages' dependencies and their removal script |
| `package-remove.sh` | the .deb's `prerm` and the .rpm's `%preun` (below) |
| `test_package_remove.sh` | tests it in a user and mount namespace with fake user managers |

```bash
make linux-app-payload    # payload.sh, then check.sh
make linux-app            # the payload, then the AppImage / .deb / .rpm, then check-app.sh
```

Needs Linux on the architecture it builds for (x86_64 or aarch64), with glibc no newer than the
payload's baseline (Ubuntu 22.04 in CI), plus Go, uv, pnpm (web deps installed), patchelf and zip.
Run `check.sh` as an ordinary user: Postgres refuses root.

## Layout

```
server/
  bin/       maktaba-server (web UI embedded) maktaba-api maktaba-streaming maktaba-launcher
  postgres/  bin/{postgres,initdb,pg_ctl,pg_dump,pg_dumpall,pg_restore,psql,pg_isready,
                  pg_upgrade,pg_controldata,pg_resetwal,pg_basebackup}
             lib/   libpq, the extensions, and the libraries copied in from the build machine
             share/
  ffmpeg/    ffmpeg ffprobe
  python/    CPython 3.12 (bin/python3), stdlib precompiled
  pipeline/  maktaba_pipeline-<v>-py3-none-any.whl  constraints.txt
  licenses/  postgresql.txt postgresql-binaries.txt postgresql-libraries.txt ffmpeg.txt python.txt
  files.sha256   every file and symlink but itself and payload.json
  payload.json   versions, sources, SHA-256s, min_glibc, the manifest's count, bytes and SHA-256
```

`files.sha256` holds one tab-separated line per entry, sorted by path (bytes):
`f <mode> <size> <sha256> <path>` for a file, `l <target> <path>` for a symlink. The runtime copy
(39.3) checks every file against it, and `payload.json`'s `files.sha256` names the manifest itself.

Not in the payload: the wheels the venv needs (torch and the rest, a few GB) and the models. The
first run builds the venv from `pipeline/` with the bundled Python and downloads them.

## Where each piece comes from

| Piece | Source | Why | Licence |
|---|---|---|---|
| Go binaries | this repo, `make server build-go` (linux/amd64 or arm64, `-tags embed_web`, release ldflags) + the launcher | the same envelope as `release.yml` | this repo's |
| PostgreSQL 16.15 | [theseus-rs/postgresql-binaries](https://github.com/theseus-rs/postgresql-binaries) `<arch>-unknown-linux-gnu` | the build Maktaba.app bundles and `pgbin` fetches for a node (Epic 37), with the same SHA-256s; relocatable (`$ORIGIN/../lib`), every contrib extension, pg_dump/psql/pg_basebackup | PostgreSQL Licence |
| Postgres's libraries | the build machine's OpenSSL 3, libxml2 (and the ICU it links), lz4, zstd, zlib, MIT Kerberos, readline | the theseus-rs Linux build links them from the system. A minimal Debian lacks some of them (OpenSSL 3, libxml2, Kerberos), so they are copied in with an `$ORIGIN` rpath, and the modules' `/opt/postgresql/lib` rpath is replaced | each its own (`licenses/postgresql-libraries.txt`) |
| ffmpeg / ffprobe 8.1.2 | [ffmpeg.martin-riedl.de](https://ffmpeg.martin-riedl.de) linux amd64/arm64 release zips, SHA-256 from the site's `.sha256` | the Mac's source and release; links glibc and libstdc++ only | **GPLv3** (as on the Mac: `licenses/ffmpeg.txt` points at the source) |
| CPython 3.12.15 | [astral-sh/python-build-standalone](https://github.com/astral-sh/python-build-standalone) `install_only_stripped`, SHA-256 from the release's `SHA256SUMS` | relocatable, glibc 2.17, OpenSSL built in; the Mac's release | PSF; others in `python/lib/python3.12/LICENSE.txt` |
| pipeline wheel + constraints | `deploy/packaging/node/build-release.sh` | one build of the pipeline for every install | this repo's |

Left out of Postgres: PL/Python (it links the build machine's libpython 3.11), `xml2` (libxslt) and
`uuid-ossp` (libossp-uuid), with their extension files. Maktaba creates `pg_trgm`, `pgcrypto` and
`unaccent`. `check.sh` creates all three.

**The baseline.** Every ELF file of the payload needs glibc ≤ 2.35 (`MAX_GLIBC` in `pins.env`; the
ffmpeg builds set it) and, beside glibc, only libgcc_s and libstdc++ (GCC 12's). That means Ubuntu
22.04+, Debian 12+, Fedora 36+ and RHEL 10. `payload.sh` refuses to write a payload that needs more,
and `check.sh` repeats the scan wherever it runs.

## The packages: what they need, and removing them

The .deb depends on `libwebkit2gtk-4.1-0`, `libgtk-3-0` and `libayatana-appindicator3-1` (the
tray). The .rpm requires the same libraries by soname (tauri adds them; the config names them too)
(`libwebkit2gtk-4.1.so.0()(64bit)`, `libgtk-3.so.0()(64bit)`,
`libayatana-appindicator3.so.1()(64bit)`), so it installs on any rpm distribution whose packages
provide them, whatever their names. On RHEL, AlmaLinux and Rocky 10 the AppIndicator library is
in EPEL: `sudo dnf install epel-release` first. It stays a hard dependency: the tray library is
loaded when the app starts, and a missing one stops the app (libappindicator-sys panics), so it
cannot be a weak one until the shell starts without a tray when the library is absent.

**Removing the app** (`sudo apt remove maktaba` / `sudo dnf remove maktaba`) runs
`package-remove.sh`: for every user whose systemd user manager runs (logged in, or lingering, the
server at boot), a `maktaba-server.service` that `maktaba-server service install` wrote (its first
line says so) is stopped and disabled, so nothing of the app keeps running. An upgrade never does
this. The data, the runtime copy and the unit file stay in each home, so reinstalling the app brings
the same server back. A user who was not logged in keeps the unit enabled until they run
`systemctl --user disable --now maktaba-server`. To remove everything of one user as well:
`~/.local/share/maktaba/runtime/current/bin/maktaba-server service uninstall` before removing the
package, then delete `~/.local/share/maktaba`, `~/.config/maktaba`, `~/.local/state/maktaba` and
`~/.cache/maktaba`. The tray's autostart entry names the app with `TryExec` (story 39.8), so the
desktop ignores it once the app is gone. An AppImage has no removal step: deleting it leaves the
server running until `service uninstall`.

Bumping a piece: change `pins.env` (version, URL, SHA-256 from the publisher), then run
`make linux-app-payload`. CI's Linux app gate builds the payload on both architectures and runs
`check.sh` on the runner and in `ubuntu:22.04`, `debian:12-slim` and `fedora:40`.

**Downloads.** Each download is tried again after 15, 30, 60 and 120 s when it fails, and kept only
with its pinned SHA-256. CI keeps them in a cache on main (`linux-app-downloads` in `ci.yml`),
which pull requests restore: the cache of an earlier `pins.env` too, so a pin change downloads
only the pieces it changed.
