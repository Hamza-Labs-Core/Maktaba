# Maktaba

[![CI](https://github.com/Hamza-Labs-Core/Maktaba/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/Hamza-Labs-Core/Maktaba/actions/workflows/ci.yml)

**Plex-alternative media intelligence platform for 30 TB+ video libraries, with Arabic-first transcription.**

Maktaba (مكتبة, "library") is a self-hosted media platform that turns a folder
tree of video files into a fully streamable, intelligently-organized library
where **every word in every video is searchable**. It is positioned as a full
Plex alternative — library + transcoding origin + first-party apps for every
platform ([below](#apps-by-platform)) — with content intelligence layered on top: transcription,
diarization, semantic indexing, and second-precise navigation.

The target deployment is a single household with 30 TB+ of content on a NAS or
workstation. Arabic is a first-class language throughout (Unicode, bidi, RTL),
but the STT and indexing layers are language-agnostic and ship with English
support out of the box.

## Apps by platform

Maktaba has apps for phones, tablets, computers, TVs and the browser. What each platform has
today:

| Platform | Maktaba Player (watch) | Maktaba Remote (run the home; admins only) | Maktaba TV | Maktaba (the server and the web app) |
|---|---|---|---|---|
| **iPhone** | Yes (TestFlight) | Yes (TestFlight) | — | — |
| **iPad** | Yes (TestFlight, *Maktaba Player for iPad*) | In the code, not on TestFlight yet | — | — |
| **Mac** | Yes (TestFlight, *Maktaba Player for Mac*) | — | — | Yes (Maktaba.app, Apple silicon) |
| **Apple TV** | — | — | Yes (TestFlight) | — |
| **Android phone and tablet** | Yes (Google Play testing) | Yes (Google Play testing) | — | — |
| **Android TV and Google TV** | — | — | Yes (Google Play testing, or the APK) | — |
| **Windows** | Yes (`.msi`, x64) | — | — | Yes (`-setup.exe`, x64) |
| **Linux** | Yes (`.deb`, `.rpm`) | — | — | Yes (AppImage, `.deb`, `.rpm`, or without a desktop) |
| **Web** | — | — | — | Yes (any browser) |

The web app does what the Player and the Remote do, in any browser. How to install each one,
and the file each release attaches: [Get the apps](#get-the-apps).

---

## Architecture

The home's services (API, streaming, pipeline, web) share one Postgres for state and one
media volume for bytes; the apps and the optional cloud talk to them:

| Service | Language | Role |
|---|---|---|
| **API** | Go 1.26 | REST + GraphQL + WebSocket. Auth, library CRUD, search orchestration, job control, settings, watch state. |
| **Streaming** | Go 1.25 | HLS / DASH origin. FFmpeg-driven transcode/remux, range serving, subtitle muxing, sprites, and the live TV channels. |
| **Pipeline** | Python 3.12 | Scanner, audio extractor, Whisper STT (MLX / faster-whisper / OpenAI), embedding, pgvector indexer (Postgres), diarization. Drains the durable job queue. |
| **Web** | React 18 + TS + Vite | The web app (PWA), with the user guide at `/help`. The Mac, Windows and Linux apps (Tauri) show it and carry the server. |
| **Native apps** | Swift / Kotlin | Maktaba Player (iPhone, iPad, Mac, Android, Windows, Linux), Maktaba Remote (iPhone, iPad, Android), Maktaba TV (Apple TV, Android TV). See [Apps by platform](#apps-by-platform). |
| **Cloud Relay** *(optional)* | Go 1.26 | Hosted SaaS layer for remote access: identity, server linking, WSS-tunneled HTTP relay, push fanout, billing (no payment provider yet). Lives in `cloud/`. |

See [`specs/architecture.md`](specs/architecture.md) for the canonical design,
data flow, and rationale behind the language split.

---

## Key features

- **File scanner with BLAKE3 content identity** — detects every video under a
  library's roots, identifies it by streaming-BLAKE3 fingerprint, and tracks it
  through a deterministic state machine. Move-aware, dedup-aware, atomic
  filesystem watcher.
- **MLX Whisper transcription** — Apple Silicon-optimized STT via `mlx-whisper`,
  with `faster-whisper` (CUDA / CPU) and OpenAI API backends behind a
  pluggable registry. Pause / resume / crash recovery preserve every segment
  already committed.
- **Hybrid FTS + semantic search** — Postgres `tsvector` full-text search fanned
  out with pgvector semantic search in the same Postgres ([Epic 43](specs/epics/43-pgvector-search/README.md)). Search any phrase
  spoken in any video and jump to the exact second from any client.
- **HLS / DASH adaptive streaming** — on-the-fly transcode and remux via FFmpeg
  subprocesses, range-request direct play, subtitle muxing, signed-URL gated
  session capability matrix, hardware-accelerated decode paths.
- **Arabic-first with English support** — bidi/RTL correct from input to
  caption rendering. Pipeline is language-agnostic; UI ships AR and EN.
- **Live TV channels** — channels made from your library, played around the clock
  with a guide (XMLTV) for the web, the apps, the TVs and Plex. The home burns each
  channel's corner watermark into the picture; by default filler and bumpers air
  without it (rules → Maktaba TV → *No corner logo on filler*).
- **Libraries and sections** — libraries, series, collections and playlists; the
  Player apps have Home, Libraries, Live TV, Search and Profile.
- **Processing you can see** — the queue shows what runs and waits; *Needs attention*
  groups failures by cause, in English and Arabic, with **Retry** and **Retry all**
  (web and Maktaba Remote).
- **Updates and rollback** — signed releases install by themselves in a quiet window,
  the home first and then each machine; a failed update goes back by itself, and a
  database backup can be put back from Admin → Updates ([docs/updates.md](docs/updates.md)).
- **Several machines as one home** — other computers join the home and share its
  work and its live TV ([docs/cluster/README.md](docs/cluster/README.md)).
- **Plex integration** — with the Plex extension on, Maktaba keeps a Plex collection
  for every season it finds, adds topic and clip collections, and puts its channels
  and guide in Plex Live TV.
- **Usage data only with consent** — anonymous usage data is off until a person turns
  it on for their app, and an admin for the server (Settings → Privacy).
- **Help in the app** — a user guide and FAQ for every feature at `/help`, in English
  and Arabic, linked from every page.
- **Cloud relay for remote access** — optional hosted layer that links a
  self-hosted server to a Maktaba Cloud account, tunnels HTTP through WSS, and
  fans out APNs / FCM push without exposing the home box to the internet. There is
  no payment provider yet: paid plans show "Payments coming soon". The hosted relay
  that people can pay for is planned in [Epic 50](specs/epics/50-cloud-no-downtime/README.md).

---

## Prerequisites

Day-1 you need these on the host. `make prereqs` checks them and prints
install commands for anything missing.

| Tool | Version | macOS | Linux (Debian/Ubuntu) |
|---|---|---|---|
| **Go** | 1.26+ | `brew install go` | `sudo apt install golang-go` (or [go.dev/dl](https://go.dev/dl/)) |
| **Python + uv** | 3.12+ | `curl -LsSf https://astral.sh/uv/install.sh \| sh` | `curl -LsSf https://astral.sh/uv/install.sh \| sh` |
| **Node + pnpm** | Node 20+, pnpm 9+ | `brew install node && corepack enable && corepack prepare pnpm@latest --activate` | `sudo apt install nodejs npm && corepack enable && corepack prepare pnpm@latest --activate` |
| **Docker + Compose v2** | Docker 24+, Compose v2.27+ | `brew install --cask docker` | `sudo apt install docker.io docker-compose-plugin` |
| **FFmpeg** | 6+ | `brew install ffmpeg` | `sudo apt install ffmpeg` |
| **PostgreSQL 16** | — | runs in Docker via `make dev` — **do not install on host** | runs in Docker via `make dev` — **do not install on host** |

Recommended (sharper inner loop):

| Tool | Install |
|---|---|
| **golangci-lint** | `brew install golangci-lint`  /  `go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest` |
| **goose** (DB migrations) | `go install github.com/pressly/goose/v3/cmd/goose@latest` |
| **pre-commit** | `uv tool install pre-commit`  /  `pip install pre-commit` |
| **jq, shellcheck** | `brew install jq shellcheck`  /  `sudo apt install jq shellcheck` |

Postgres 16 (and the Chroma server the e2e tests still use) are managed by `deploy/compose/docker-compose.yml`
and started by `make dev` — keep them in Docker; a host Postgres install
will collide on port 5432 and skip the migrations we ship.

---

## Quick start

```sh
make prereqs        # verify docker, go, uv, pnpm, node, ffmpeg are present (prints install commands for missing tools)
make dev            # live-reload stack: postgres, chroma, api, streaming, pipeline, web
make test           # unit tier — no network, no sudo
make help           # list every target, grouped by section
```

Or, plain Docker Compose without the live-reload tooling:

```sh
docker compose -f deploy/compose/docker-compose.yml up --build
```

CI runs the same `make` targets you run locally — see
[`CONTRIBUTING.md`](CONTRIBUTING.md) for the canonical dev workflow,
troubleshooting, and pre-commit setup.

---

## Hardware requirements

Transcribing the backlog decides the hardware; everything else is light. Measured on the live
home (Mac Studio M3 Ultra, 96 GB, v0.3.62): about **1,120 audio-hours a day (~47× real time)**,
so ~50,000 videos of 15 minutes take about **11 days** and ~150,000 about a month. Once it has
caught up, the new videos of a day take minutes.

| | Small (~5k videos) | Medium (~50k, a few TV channels) | Large (~150k, 20+ channels) |
|---|---|---|---|
| macOS (Apple Silicon) | 16–24 GB | Max-class GPU, 36–64 GB | Ultra-class GPU, 96 GB+ |
| Windows / Linux (x86_64) | 16 GB RAM, NVIDIA 4 GB+ or CPU only (slow) | 32 GB RAM, NVIDIA 10 GB+ (Linux 8 GB+) | 64 GB+ RAM, NVIDIA 20 GB+ or several machines |
| Disk for Maktaba's data, plus backups | ~20 GB | ~60 GB | ~150 GB |

The media stays on your NAS (SMB or NFS); live TV sends about 10 Mbps per watched channel.
CPU-only transcription runs at about 2.6× real time: fine for keeping up, too slow to catch up
a large library. Linux aarch64 runs the server without transcription.

[docs/hardware.md](docs/hardware.md) has the measurements, how each number was found (measured,
from the code, or estimated), the settings that change the load, worker machines, and running
the home on a dev machine.

---

## Install the home server

**Linux, one installation that runs everything** ([Epic 39](specs/epics/39-linux-app/README.md)).
The server, its database (Postgres 16), Python and the transcription pipeline in one install,
run as your systemd user service, with your distribution's ffmpeg. It starts at boot when linger
is on (setup turns it on, which may ask for an administrator's password once), otherwise when you
log in. x86_64 or aarch64, glibc 2.35 or newer (Ubuntu 22.04, Debian 12, Fedora 36 or later; RHEL,
Alma and Rocky 10 with EPEL for the desktop's tray library), and a systemd user manager. ffmpeg
must be 4.4 or later with libx264: Debian's, Ubuntu's and Arch's are; on Fedora and RHEL it is RPM
Fusion's (their own `ffmpeg-free` has no libx264), on openSUSE Packman's. Setup checks it and names
the command that installs it for your distribution (the terminal setup offers to run it);
`[media] ffmpeg` in `server.toml` names another.

- **With a desktop:** download `Maktaba_<ver>_amd64.AppImage` (or the `.deb` / `.rpm`; `aarch64`
  builds too) from the [latest release](https://github.com/Hamza-Labs-Core/Maktaba/releases/latest),
  open it and follow the setup. The window is the web app; the tray shows the server's status.
  The `.deb` depends on `ffmpeg` and the `.rpm` requires it; on Fedora and RHEL turn RPM Fusion on
  first (`sudo dnf install https://mirrors.rpmfusion.org/free/fedora/rpmfusion-free-release-$(rpm -E %fedora).noarch.rpm`),
  or dnf finds no `ffmpeg` for it.
  The app updates itself from the release's signed updates, installing one only when its signature
  matches the key built into the app ([auto-update](docs/auto-update.md)), and moves the server to
  the new version at its next start.
- **Without a desktop** (a NAS, a mini PC, a VM): run the release's installer as the user the
  server is for (not as root). It downloads the server, checks its Sigstore signature before
  anything runs (with the machine's cosign when it is 2.4.3 or newer, else a pinned cosign it
  fetches), and asks the same setup questions in the terminal, in English or Arabic:

  ```sh
  curl -fsSLO https://github.com/Hamza-Labs-Core/Maktaba/releases/latest/download/install.sh
  bash install.sh --app
  ```

  The script is in the release's signed `checksums.txt` too, so it can be checked the same way
  first ([how](deploy/packaging/README.md#linux-the-app-or-these-packages)). This machine
  updates from the signed releases by itself in the quiet window, at once when an admin clicks
  Update now in the web app, or with `maktaba-server update` (each installs the new version
  beside the running one and switches to it, keeping the previous one for a rollback).
- **Uninstall:** `~/.local/share/maktaba/runtime/current/bin/maktaba-server service uninstall`
  stops and removes the service; removing the `.deb` / `.rpm` stops it too. Your library's data
  stays in `~/.local/share/maktaba` and `~/.config/maktaba` until you delete them.

On a Mac, Maktaba.app is the same one installation ([Epic 36](specs/epics/36-mac-app/README.md)).
The packages below install the server alone, for operators who run it their own way.

The home server ships as the single `maktaba-server` binary (with the API,
streaming, and pipeline roles plus the web UI). Pick the method for your OS —
every artifact is checksummed and signed with keyless cosign:

```sh
# Linux / macOS — one-line installer (detects OS + arch, verifies checksum)
curl -fsSL https://raw.githubusercontent.com/Hamza-Labs-Core/Maktaba/main/deploy/packaging/install.sh | bash

# Debian / Ubuntu — native package (systemd unit, maktaba user), from the release
sudo apt install ./maktaba-server_<ver>-1_amd64.deb

# Fedora / RHEL / CentOS
sudo dnf install ./maktaba-server-<ver>-1.x86_64.rpm

# macOS — Homebrew (the server; it updates itself), once the tap is published
brew install hamza-labs-core/tap/maktaba
```

Each of these updates itself after that, in its quiet window. Once the project's apt and dnf
repositories and its Homebrew tap are set up, the release notes name them: the repositories
carry the same server packages (their page says how to add them), and the tap the formula
(attached to each release as `maktaba.rb` meanwhile).

**Windows: the Maktaba app.** Install `Maktaba_<ver>_x64-setup.exe` from the
[latest release](https://github.com/Hamza-Labs-Core/Maktaba/releases/latest). It
installs for the current user into `%LOCALAPPDATA%\Maktaba` (no administrator) and
carries the whole server: `maktaba-server`, PostgreSQL 16, ffmpeg (NVENC) and Python
3.12. Its setup screens do the rest (the admin, the libraries and the NAS path map,
the models, Plex), and the first start builds the pipeline's Python environment.
From then on the server starts at every
sign-in with no console window, the tray shows how it is doing, and the app updates
itself through signed updates (the Tauri updater), starting with the first tagged release
that includes it. A PC that already runs Maktaba (`maktaba-server setup --service`, the
node installer, a script of its own) is found by the setup, which offers to move it
into the app: its keys, settings, node identity and firewall rules come along (Windows asks
once to allow the firewall rules), and the old folder is kept for going back. See
[Epic 38](specs/epics/38-windows-app/README.md).

Manual installs: download the matching `.tar.gz` / `.zip` from the
[latest release](https://github.com/Hamza-Labs-Core/Maktaba/releases/latest).
After installing, run `maktaba-server setup` (or edit
`/etc/maktaba/server.toml`), then `sudo systemctl start maktaba-server` — the
web UI is on port `8088`. Packaging details, install paths, and the full
artifact list are in [`deploy/packaging/`](deploy/packaging/README.md).

Prefer containers? [`cloud/installers/docker/docker-compose.yml`](cloud/installers/docker/docker-compose.yml)
runs Maktaba in one container (`ghcr.io/hamza-labs-core/maktaba-server`, multi-arch):
`docker compose up -d`, then `docker compose run --rm maktaba setup` once. It updates itself
on its volume like every other install. (`deploy/compose/docker-compose.yml` is the
development and CI stack, built from source.)

---

## Get the apps

The apps are in testing. Testers get them from TestFlight and Google Play's internal testing,
not from the public stores yet. The owner adds each tester: their Apple ID to a TestFlight
testers group in App Store Connect ([docs/testflight.md](docs/testflight.md)), their Google
account to the testers list in Play Console ([docs/google-play.md](docs/google-play.md)).
Every release tag (`git tag vX.Y.Z && git push --tags`) uploads a new build to both, which
reaches testers as an update, and attaches the installable files to the
[GitHub Release](https://github.com/Hamza-Labs-Core/Maktaba/releases) for that tag.

| App | Platform | How to get it | Built by |
|---|---|---|---|
| **Maktaba Player** (watch) | iPhone · iPad (*Maktaba Player for iPad*) · Mac, macOS 15+ (*Maktaba Player for Mac*) | TestFlight | [`testflight.yml`](.github/workflows/testflight.yml) |
| **Maktaba Remote** (run the home; admins only) | iPhone | TestFlight | [`testflight.yml`](.github/workflows/testflight.yml) |
| **Maktaba Remote** for iPad | iPad | not on TestFlight yet: the `Maktaba Remote iPad` scheme (`com.hamzalabs.maktaba.remote.tablet`) builds from source, but no release uploads it | — |
| **Maktaba TV** | Apple TV (tvOS 26) | TestFlight on the Apple TV | [`testflight.yml`](.github/workflows/testflight.yml) |
| **Maktaba Player** and **Maktaba Remote** | Android phones, foldables and tablets | Google Play internal testing (`maktaba-player-<version>.aab`, `maktaba-remote-<version>.aab`, signed with the Play upload key) | [`play-bundles.yml`](.github/workflows/play-bundles.yml) |
| **Maktaba TV** | Android TV and Google TV | Google Play internal testing (`maktaba-tv-<version>.aab`); or sideload `maktaba-tv-<version>.apk` when a release carries it (only signed builds are attached) | [`play-bundles.yml`](.github/workflows/play-bundles.yml), [`tv-release.yml`](.github/workflows/tv-release.yml) |
| **Maktaba Player** | Windows (x64), Linux (x86_64, aarch64) | `Maktaba-Player_<v>_x64.msi`, `maktaba-player_<v>_<amd64\|arm64>.deb`, `maktaba-player-<v>.<x86_64\|aarch64>.rpm` (each with a `.sig`) from the [latest release](https://github.com/Hamza-Labs-Core/Maktaba/releases/latest); it updates itself from `latest-player.json` (Help → Check for updates) | [`desktop-release.yml`](.github/workflows/desktop-release.yml) |
| **Maktaba** (the server and the web app) | Mac, Apple silicon only (no Intel build since v0.3.15) | one `.dmg`, the whole server inside | [`desktop-release.yml`](.github/workflows/desktop-release.yml) |
| **Maktaba** (the server and the web app) | Windows (x64) | NSIS `-setup.exe` with the server inside (current user, `%LOCALAPPDATA%\Maktaba`), plus a portable `.zip` of the client | [`desktop-release.yml`](.github/workflows/desktop-release.yml) |
| **Maktaba** (the server and the web app) | Linux with a desktop (x86_64, aarch64) | `.AppImage`, `.deb`, `.rpm`, the whole server inside (ffmpeg is the distribution's) | [`desktop-release.yml`](.github/workflows/desktop-release.yml) |
| **Maktaba** (the server) | Linux without a desktop (x86_64, aarch64) | `maktaba-linux-payload-<version>-<x86_64\|aarch64>.tar.gz`, through `install.sh --app` ([above](#install-the-home-server)) | [`release.yml`](.github/workflows/release.yml) |
| **Web app** | any browser | the home's address, port `8088`; or through Maktaba Cloud. The bundle alone, to self-host: `maktaba-web-<version>.tar.gz` | [`release.yml`](.github/workflows/release.yml) (each tag) · [`web-release.yml`](.github/workflows/web-release.yml) (rebuild on demand) |

The server apps update themselves ([above](#install-the-home-server)). The server binaries and
container images come from [`release.yml`](.github/workflows/release.yml) too.

**Signing in a TV or a device by code.** A TV (or a phone or computer that chooses *With a
code*) shows a code like `ABCD-1234` and a QR code. Approve it from an app that is already
signed in: in **Maktaba Remote**, tap **Approve a TV or device** at the top of any screen; in
**Maktaba Player**, open **Profile** and tap **Approve a TV or device** (on the Mac, the
**Account** menu); or open the web app's **Sign in a device** page (`/pair`). While a TV waits,
the Remote (and the Android Player) shows a banner with **Enter code**, and the Android apps a
notification when notifications are allowed. Older apps call it **Approve a sign-in**, under the
person icon. The device is signed in as you, with your role.

**The older web-app wrapper** (`apps/mobile`, Capacitor: `maktaba-<version>.apk` and `.aab`,
and an unsigned `maktaba-<version>.ipa`, from
[`mobile-release.yml`](.github/workflows/mobile-release.yml)) is not one of these apps.
Installed by hand, it shows a *New version available* banner with *Get update* when a newer
release exists ([docs/auto-update.md](docs/auto-update.md)).

The user guide for every app is in the web app at `/help` (Help → Apps), in English and Arabic.

**Signing status.** The TestFlight builds are signed by Xcode's cloud signing, and the
Play bundles with the upload key. The desktop installers are signed and notarized only when
the Apple Developer ID and Windows Authenticode secrets are set; until then they are
**unsigned**. The desktop apps' **updates are signed** with the Tauri
updater key: the release workflow signs them with the repository's secret and
stamps the public key (the `TAURI_UPDATER_PUBKEY` variable) into
`tauri.conf.json` at build time, so the apps take only updates signed with it
([auto-update](docs/auto-update.md)). Android signs
automatically when the `ANDROID_KEYSTORE_BASE64` / `ANDROID_KEY_ALIAS` /
`ANDROID_KEY_PASSWORD` secrets are set. Without them the APKs are built
unsigned, kept as workflow artifacts and not attached to the Release.

### Build the apps from source

The Player, Remote and TV apps are native and talk to the home's API
([Epic 42](specs/epics/42-apps-player-remote-server/README.md),
[Epic 48](specs/epics/48-native-players/README.md)). The desktop apps that carry the
server wrap the `web/` bundle. All shells live under [`apps/`](apps/):

| Target | Stack | Build |
|---|---|---|
| Player (iPhone, iPad, Mac) and Remote (iPhone, iPad) | **SwiftUI** + the shared `MaktabaKit` ([`apps/apple/`](apps/apple/)) | `xcodebuild` (see its README) |
| Android: Player and Remote | **Jetpack Compose** on the shared `client` ([`apps/android/`](apps/android/)) | `./gradlew` (see its README) |
| Windows, Linux: Player | **Compose for Desktop** ([`apps/desktop-player/`](apps/desktop-player/)) | `./gradlew packageMsi` / `packageDeb packageRpm` |
| Apple TV | **SwiftUI + AVKit** ([`apps/tv/tvos/`](apps/tv/tvos/), scheme `Maktaba TV`) — native 10-foot UI | `make tv-build-ios` |
| Android TV | **Compose-for-TV + Media3** ([`apps/tv/android/`](apps/tv/android/)) — native 10-foot UI | `make tv-build-android` |
| macOS / Windows / Linux: the server app | **Tauri 2** ([`apps/desktop/`](apps/desktop/)) — wraps `web/dist`, carries the server | `make desktop-build` |
| iOS + Android (older web-app wrapper) | **Capacitor 6** ([`apps/mobile/`](apps/mobile/)) — wraps `web/dist` | `make mobile-build` |

```sh
make mobile-build      # web build + cap sync (needs Xcode/CocoaPods, Android SDK+JDK)
make desktop-build     # Tauri build for the host OS (needs the Rust toolchain)
make native-sync       # rebuild web/dist and cap sync it into the native platforms
make tv-build-ios      # tvOS core (swift build); device builds via xcodebuild
make tv-build-android  # Android TV debug APK (needs the Android SDK)
```

To build any client package locally, mirror CI with the `make package-*`
targets (`package-desktop`, `package-mobile-android`,
`package-mobile-ios`, `package-tv-android`, or `package-all`).

The native apps use `com.hamzalabs.maktaba.player` (`.player.tablet` on iPad,
`.player.mac` on the Mac), `com.hamzalabs.maktaba.remote` (`.remote.tablet` on iPad) and
`com.hamzalabs.maktaba.tv`; the Capacitor and Tauri wrappers share `com.hamzalabs.maktaba`.
All take `maktaba://connect` links. Generated native projects
(`apps/mobile/{ios,android}`, `apps/desktop/src-tauri/{target,gen}`, the
TV build outputs) and signing material are not checked in — see each
shell's README for the toolchain prerequisites and signing setup.

---

## Deploying the Cloud Relay

The optional hosted layer (`cloud/`) ships as a single `maktaba-cloud`
binary that runs an `api`, `relay`, or `worker` role. To stand up the
**relay** — the public ingress self-hosted servers tunnel out to — on a
fresh Ubuntu 22.04/24.04 VPS:

```sh
git clone https://github.com/Hamza-Labs-Core/Maktaba.git
cd Maktaba/deploy/cloud-relay
sudo ./setup.sh                       # Docker, secrets, firewall, systemd
sudo -e /opt/maktaba-relay/.env       # set RELAY_DOMAIN + ACME_EMAIL
sudo systemctl enable --now maktaba-relay
```

This brings up the relay behind Caddy (automatic Let's Encrypt TLS),
with Postgres 16 and Redis 7, managed by systemd. The full guide —
DNS records, wildcard TLS, the `api` role, entitlement key
provisioning, and operations — is in
[`deploy/cloud-relay/README.md`](deploy/cloud-relay/README.md). The
`maktaba-cloud` image is published to
`ghcr.io/hamza-labs-core/maktaba-cloud` (cosign-signed) by the
[release workflow](.github/workflows/release.yml).

---

## Project structure

```
maktaba/
├── api/                  # Go API service (Epic 7) — REST + GraphQL + WS
├── streaming/            # Go streaming service (Epic 8) — HLS/DASH origin
├── pipeline/             # Python pipeline (Epics 1–6) — scan, STT, index
├── cloud/                # Go cloud relay service (Epic 25) — hosted SaaS
├── web/                  # React PWA, the in-app help (src/help), design system, wiki app (Epics 11, 17, 41)
├── apps/
│   ├── apple/            # SwiftUI Player (iPhone, iPad, Mac) + Remote (iPhone, iPad), MaktabaKit (Epics 42, 48)
│   ├── android/          # Compose Player + Remote (phone, foldable, tablet) (Epics 42, 48)
│   ├── desktop-player/   # Compose for Desktop Player for Windows / Linux (Epic 42)
│   ├── desktop/          # Tauri app for mac/Win/Linux that carries the server (Epics 13, 36–39)
│   ├── mobile/           # older Capacitor wrappers for iOS / Android (Epic 12)
│   └── tv/               # Swift (tvOS) + Kotlin (Android TV) (Epics 14, 48)
├── shared/
│   ├── api/              # openapi.yaml, GraphQL SDL — single source of truth
│   ├── db/migrations/    # numbered SQL migrations (Postgres + SQLite duals)
│   ├── log/              # structured logging libraries (go + py)
│   ├── metrics/          # OpenTelemetry helpers
│   └── testtier/         # cross-language test pyramid budgets
├── specs/                # design source of truth: 54 epics (specs/epics/README.md)
│   ├── architecture.md   # canonical system design
│   ├── epics/            # one directory per epic, README + stories + plans
│   └── PLAN_REVIEW*.md   # independent spec reviews
├── docs/
│   ├── testing.md        # test pyramid, tiers, runtime budgets
│   └── wiki/             # generated wiki — INDEX, stories-map, entities, …
├── deploy/               # docker-compose stacks, terraform for repo config
├── tools/                # migration-lint and other dev tooling
├── tests/                # cross-service integration suites
└── Makefile              # canonical entry points (CI uses these too)
```

---

## Tech stack

- **Backend:** Go 1.26 (api, cloud; streaming 1.25), Python 3.12+ (pipeline)
- **Frontend:** TypeScript, React 18, Vite, TailwindCSS, Vidstack
- **Native:** Swift / SwiftUI (iPhone, iPad, Mac, Apple TV), Kotlin / Jetpack Compose
  (Android, Android TV), Compose for Desktop (Windows / Linux Player), Tauri 2 (the
  desktop server app), Capacitor (the older mobile wrapper)
- **Data:** PostgreSQL 16 with pgvector (or SQLite for single-user mode)
- **Media:** FFmpeg for transcoding / remuxing / probing
- **STT / ML:** Whisper via `mlx-whisper`, `faster-whisper`, or OpenAI API;
  multilingual `sentence-transformers` embeddings; `pyannote.audio` diarization
- **Observability:** OpenTelemetry traces + metrics, structured JSON logs
- **Tooling:** `uv` (Python), `pnpm` (Node), `goose` (migrations),
  `golangci-lint`, `ruff` + `mypy --strict`, Vitest + Playwright

---

## Status

| | |
|---|---|
| Epics | **54** in [`specs/epics/`](specs/epics/README.md): most of 01–49 is built on `main` (each epic's README and the [CHANGELOG](CHANGELOG.md) say how far); 50–54 are planned ([Roadmap](#roadmap)) |
| Releases | tagged `v0.3.x`; update, rollback and restore are rehearsed in CI before a tag ships ([docs/update-rehearsal.md](docs/update-rehearsal.md)) |
| Changes | [`CHANGELOG.md`](CHANGELOG.md) |

See:

- [`specs/architecture.md`](specs/architecture.md) — system design.
- [`docs/hardware.md`](docs/hardware.md) — what the home and worker machines need, by library size, measured on the live home.
- [`docs/testing.md`](docs/testing.md) — test pyramid, tier budgets, CI gates.
- [`docs/cluster/README.md`](docs/cluster/README.md) — several machines as one Maktaba home: joining, files on the share, LLMs, troubleshooting.
- [`docs/updates.md`](docs/updates.md) — how updates install, roll back and restore.
- [`docs/testflight.md`](docs/testflight.md), [`docs/google-play.md`](docs/google-play.md) — getting the apps to testers.
- [`docs/wiki/INDEX.md`](docs/wiki/INDEX.md) — generated wiki index. **The wiki is served by the bundled wiki web app in [`web/wiki-app/`](web/wiki-app/), NOT by GitHub Wiki** — see [`docs/WIKI_SYNC.md`](docs/WIKI_SYNC.md) for why and how.
- [`docs/wiki/build-order.md`](docs/wiki/build-order.md) — phase-by-phase implementation order.
- [`CONTRIBUTING.md`](CONTRIBUTING.md) — day-1 setup, day-N inner loop, troubleshooting.

---

## Roadmap

The next epics are specs, not built yet:

| # | Epic | What it brings |
|---|---|---|
| 50 | [Maktaba Cloud with no downtime](specs/epics/50-cloud-no-downtime/README.md) | the hosted relay, from one VPS to a relay people can pay for; payments come with a provider still to be chosen |
| 51 | [Configurable components](specs/epics/51-configurable-components/README.md) | every part of Maktaba can be turned on, off, added later or left out |
| 52 | [Word review](specs/epics/52-transcript-word-review/README.md) | suspect words in transcripts go to a person, in every language |
| 53 | [Logs, errors and reports](specs/epics/53-logs-errors-reports/README.md) | one log schema, problem reports from every app, and a UI that never goes dark |
| 54 | [Plugins](specs/epics/54-plugins/README.md) | new functions in the queue, with no internet, from a Maktaba store at plugins.maktaba.tv that runs apart from Maktaba Cloud |

---

## Domains

- **maktaba.tv** is for users: Maktaba Cloud and everything people use.
- **maktaba.dev** is for developers: the developer wiki, the source and `security.txt`.

No other Maktaba domain is ours.

On maktaba.tv (planned in [Epic 50 §15](specs/epics/50-cloud-no-downtime/README.md#15-cloudflare-in-front-the-user-portal-and-the-operator-console)):

| Name | What | How it is reached |
|---|---|---|
| `app.maktaba.tv`, `api.maktaba.tv` | the user portal: account, billing, the cloud web app and the API | the Cloudflare proxy (WAF, rate limits, bot checks on sign-in), then a Cloudflare Tunnel to the cloud; no open port |
| `admin.maktaba.tv` | the operator console, for Maktaba staff only | the same proxy and Tunnel; staff accounts with required two-step sign-in; never served on a user name |
| `relay.maktaba.tv`, `<home>.relay.maktaba.tv` | remote play through the relay | DNS only, straight to the relay (video never goes through Cloudflare's proxy) |
| `watch.`, `live.`, `ingest.maktaba.tv` | home broadcasting | Cloudflare Workers ([Epic 50 §14](specs/epics/50-cloud-no-downtime/README.md#14-home-broadcasting)) |
| `status.maktaba.tv` | the status page | hosted off the cloud's own machines |
| `plugins.maktaba.tv`, `plugins-cdn.maktaba.tv` | the plugin store: catalog, developer console, review and store admin (`/admin`), and the signed plugin files ([Epic 54](specs/epics/54-plugins/README.md)) | on Cloudflare, apart from Maktaba Cloud, with its own accounts and sign-in |

---

## License

See [`LICENSE`](LICENSE).
