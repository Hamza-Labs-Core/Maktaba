# Maktaba

[![CI](https://github.com/Hamza-Labs-Core/Maktaba/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/Hamza-Labs-Core/Maktaba/actions/workflows/ci.yml)

Maktaba (مكتبة, "library") is a self-hosted media server for large video libraries, with
Arabic-first transcription. It turns a folder tree of video files into a streamable library
where **every word in every video is searchable**: it transcribes each video, indexes what is
said, and opens any result at the exact second. It streams to its own apps for phones, tablets,
computers, TVs and the browser, makes live TV channels from the library, and works with Plex.

It is built for one household with tens of terabytes on a NAS or a workstation. Arabic is
first-class throughout (right to left, in every app and the in-app help); transcription and
search work in English and other languages too. Several machines can share the work as one
home, updates install and roll back by themselves, and an optional cloud relay gives remote
access without opening the home to the internet.

## Install

Every release is on the [Releases page](https://github.com/Hamza-Labs-Core/Maktaba/releases/latest),
signed with keyless cosign (Sigstore); its notes say how to verify each file. Each install
below updates itself from the signed releases. The step-by-step guides for households are at
[maktaba.tv/install](https://maktaba.tv/install/).

How much machine you need depends on the size of the library:
[docs/hardware.md](docs/hardware.md) has the tiers, measured on a live home.

### Mac

Download `Maktaba_<version>_aarch64.dmg` (Apple silicon, macOS 15 or later) and open
Maktaba.app: its setup does the rest, and the whole server runs inside the app.
`Maktaba-Core_<version>_aarch64.dmg` is the same app without the local language model.
Guide: [apps/desktop](apps/desktop/README.md#maktabaapp-runs-the-server-epic-36).

### Windows

Run `Maktaba_<version>_x64-setup.exe`. It installs for the current user (no administrator),
carries the server, PostgreSQL, ffmpeg and Python, starts at sign-in, and can take over a PC
that already runs Maktaba.
Guide: [docs/wiki/deployment.md](docs/wiki/deployment.md#12-windows--the-maktaba-app-epic-38).

### Linux

With a desktop, open `Maktaba_<version>_amd64.AppImage` (or install the `.deb` or `.rpm`;
aarch64 builds too) and follow its setup. Without a desktop (a mini PC, a VM, a NAS that runs
plain Linux), run the release's installer as the user the server is for, not as root:

```sh
curl -fsSLO https://github.com/Hamza-Labs-Core/Maktaba/releases/latest/download/install.sh
bash install.sh --app
```

Either way it runs as a systemd user service with your distribution's ffmpeg (glibc 2.35 or
newer: Ubuntu 22.04, Debian 12, Fedora 36, RHEL 10 or later).
Guide, with verifying the script, ffmpeg per distribution and uninstalling:
[deploy/packaging](deploy/packaging/README.md#linux-the-app-or-these-packages).

### NAS

Every NAS runs the same image, as a non-root user, with its data on local storage. Which guide
to follow, by model: [maktaba.tv/install/nas](https://maktaba.tv/install/nas/).

| NAS | What to use | Guide |
|---|---|---|
| Synology (DSM 7.2+) | Container Manager project: `maktaba-synology-compose.yaml` (`.j4125.yaml` on a J4125) | [guide](https://maktaba.tv/install/synology/), [the files](deploy/nas/synology/) |
| TrueNAS (25.04+) | Custom App: `maktaba-truenas-custom-app.yaml`, until the app is in TrueNAS's catalog | [guide](https://maktaba.tv/install/truenas/), [the files](deploy/nas/truenas/README.md) |
| Unraid | the Community Applications template | [guide](https://maktaba.tv/install/unraid/), [the files](deploy/nas/unraid/README.md) |
| QNAP | Container Station, with the Docker compose file | [guide](https://maktaba.tv/install/qnap/) |
| UGREEN | Docker in UGOS Pro, with the Docker compose file | [guide](https://maktaba.tv/install/ugreen/) |
| ASUSTOR | Portainer in ADM, with the Docker compose file | [guide](https://maktaba.tv/install/asustor/) |

After setup: [permissions, hardware video, updates](https://maktaba.tv/install/after-setup/),
[troubleshooting](https://maktaba.tv/install/troubleshooting/) and
[the network](docs/nas/networking.md). All the NAS files: [deploy/nas](deploy/nas/README.md).

### Docker or Compose

Download the release's `maktaba-docker-compose.yml` as `docker-compose.yml` (it runs
`ghcr.io/hamza-labs-core/maktaba-server`, multi-arch, as the user you set), then:

```sh
docker compose up -d
docker compose run --rm maktaba setup
```

It works the same under Portainer or Dockge.
Guide: [maktaba.tv/install/docker](https://maktaba.tv/install/docker/), which fills the file in,
and [the one-container install](docs/wiki/deployment.md#the-one-container-install-docker-a-nas).

### Other ways

The server alone, as a system service (`.deb`, `.rpm`, archives, Homebrew):
[deploy/packaging](deploy/packaging/README.md). A worker machine that joins a home:
[docs/cluster](docs/cluster/README.md). The optional cloud relay:
[deploy/cloud-relay](deploy/cloud-relay/README.md).

## Apps

| App | For | Platforms |
|---|---|---|
| **Maktaba** | runs the home, with the web app | Mac, Windows, Linux; the web app in any browser |
| **Maktaba Player** | watching | iPhone, iPad, Mac, Android, Windows, Linux |
| **Maktaba Remote** | running the home (admins) | iPhone, iPad, Android |
| **Maktaba TV** | watching on a TV | Apple TV, Android TV and Google TV; Samsung and LG (store packages) |

The phone, tablet and TV apps are in testing (TestFlight, Google Play internal testing). Which
file to install, how testers get them, signing, and building them from source:
[apps/README.md](apps/README.md). The user guide for every app is in the web app at `/help`.

## For developers

- [CONTRIBUTING.md](CONTRIBUTING.md): setup (`make prereqs && make dev && make test`), the
  inner loop, hooks and the pull request gates.
- [The developer guides](docs/developers/contributing/README.md) (published at maktaba.dev):
  architecture, repository layout, tests, CI, conventions, and the plugin guides in
  [docs/developers/plugins](docs/developers/plugins/README.md).
- [specs/](specs/epics/README.md): the design, one folder per epic, and
  [specs/architecture.md](specs/architecture.md).
- [CHANGELOG.md](CHANGELOG.md) ([العربية](CHANGELOG.ar.md)), [docs/updates.md](docs/updates.md)
  (how releases are made, updates and rollback) and [docs/testing.md](docs/testing.md).
- [The wiki](docs/wiki/INDEX.md), served by the wiki app in [web/wiki-app](web/wiki-app/)
  ([why not GitHub Wiki](docs/WIKI_SYNC.md)).

## Licence and security

Maktaba is under the MIT licence ([LICENSE](LICENSE)), and the licences of the software it ships with
are in [THIRD_PARTY_NOTICES](THIRD_PARTY_NOTICES). To report a vulnerability, follow
[SECURITY.md](SECURITY.md).
