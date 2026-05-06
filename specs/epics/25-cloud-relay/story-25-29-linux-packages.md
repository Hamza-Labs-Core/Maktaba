# Story 25.29 — Linux packages

> Epic 25 · Cloud relay · Phase 6 (server distribution)

## Description

Linux distribution across the major package formats so users on every
mainstream distro have a one-line install. The same binaries flow
through every channel; only the packaging diverges.

Channels:

1. **`.deb`** — Debian / Ubuntu / Mint / Pop!_OS via our APT repo
   `deb https://apt.maktaba.app stable main`. GPG-signed by the
   release key. `apt install maktaba`.
2. **`.rpm`** — Fedora / RHEL / CentOS Stream / openSUSE via DNF
   repo `https://rpm.maktaba.app/maktaba.repo`. GPG-signed.
   `dnf install maktaba`.
3. **Snap** — Canonical Snap Store, `snap install maktaba`.
   Confined; auto-updates by default.
4. **Flatpak** — Flathub: `flatpak install flathub app.maktaba.Server`.
   Sandboxed; user-space autostart.
5. **AppImage** — single executable
   `Maktaba-<version>-x86_64.AppImage` for distros without
   the above. Self-update via AppImageUpdate.
6. **Tarball** — `maktaba-<version>-linux-amd64.tar.gz` for
   advanced users (bring-your-own systemd unit).

systemd integration (deb / rpm / tarball):

- Unit file `/lib/systemd/system/maktaba.service`:
  ```
  [Unit]
  Description=Maktaba Media Server
  After=network-online.target
  Wants=network-online.target

  [Service]
  Type=notify
  User=maktaba
  Group=maktaba
  ExecStart=/usr/bin/maktaba serve --config /etc/maktaba/config.toml
  Restart=on-failure
  RestartSec=10
  LimitNOFILE=65536
  ProtectSystem=strict
  ReadWritePaths=/var/lib/maktaba /var/log/maktaba
  PrivateTmp=true
  NoNewPrivileges=true

  [Install]
  WantedBy=multi-user.target
  ```
- Auto-start enabled on install (`systemctl enable --now`);
  user can disable via `systemctl disable`.

User & permissions:

- Package post-install creates `maktaba` system user (no shell,
  no home), owns `/var/lib/maktaba/` and `/var/log/maktaba/`.
- Library roots remain owned by the human user; Maktaba reads
  them via the `media` group (or by the `maktaba` user being
  added to a group that has read access).

## Acceptance criteria

- **Given** an Ubuntu 22.04 user adds the APT source and
  runs `sudo apt install maktaba`,
  **when** install completes,
  **then** `maktaba` user exists, the systemd service is
  enabled and started, and `curl http://localhost:8080/healthz`
  returns 200.
- **Given** a Fedora 40 user adds the DNF repo and runs
  `sudo dnf install maktaba`,
  **when** install completes,
  **then** the same expectations as Debian.
- **Given** a Snap user runs `sudo snap install maktaba`,
  **when** install completes,
  **then** Snap auto-update is enabled, library access
  requires explicit `snap connect maktaba:removable-media`
  and `snap connect maktaba:home`.
- **Given** a Flatpak user runs `flatpak install flathub
  app.maktaba.Server`,
  **when** install completes,
  **then** the app appears in the user's session services
  and runs sandboxed; documents folder access prompts on
  first scan.
- **Given** an AppImage user runs the binary,
  **when** they execute it,
  **then** Maktaba runs in user-space; `--install`
  optionally installs a per-user systemd unit at
  `~/.config/systemd/user/maktaba.service`.
- **Given** an upgrade arrives,
  **when** apt/dnf upgrade,
  **then** the service is restarted with the new binary
  with no library re-scan; in-flight scans resume.
- **Given** the user purges (`apt purge maktaba`),
  **when** purge completes,
  **then** the user, data dirs, and unit file are removed.

## Test cases

| ID  | Type        | Setup | Action | Expected |
|-----|-------------|-------|--------|----------|
| T01 | smoke       | Ubuntu 22.04 LTS clean | apt install | success |
| T02 | smoke       | Ubuntu 24.04 | apt install | success |
| T03 | smoke       | Debian 12 | apt install | success |
| T04 | smoke       | Fedora 40 | dnf install | success |
| T05 | smoke       | RHEL 9 (rocky) | dnf install | success |
| T06 | smoke       | Snap | install | confined, library access works after `snap connect` |
| T07 | smoke       | Flathub | install | sandboxed, file portal access works |
| T08 | unit        | GPG signature on packages | verify | valid |
| T09 | regression  | upgrade preserves DB | dpkg -i new | DB intact |
| T10 | regression  | purge removes everything | apt purge | clean |
| T11 | integration | systemd unit ProtectSystem | start | no writes outside listed paths |
| T12 | integration | AppImage `--install` | run | per-user unit created |
| T13 | smoke       | Arch (AUR) | install via `yay` | (AUR community-maintained; we publish a PKGBUILD recipe but don't host) |

## Edge cases

- **SELinux on RHEL/Fedora.** Default policy allows
  systemd-managed services. We test in `enforcing` mode.
- **AppArmor on Ubuntu.** Default policy fine; no
  custom profile needed.
- **Snap confinement.** Cannot read arbitrary paths;
  `removable-media` and `home` interfaces required for
  external library volumes. Documented at install time.
- **Flatpak portal access.** `org.freedesktop.portal.FileChooser`
  prompts the user per library root; bookmarks persist.
- **systemd-resolved vs. dnsmasq.** mDNS discovery (Epic
  15.2) requires multicast; we add `Avahi` as a soft
  dependency (recommends) for distros that don't ship it.
- **glibc vs. musl.** We ship glibc-linked binaries; AppImage
  wraps a glibc compatibility layer. Alpine users can use
  the Docker image (25.30) instead.
- **Capabilities.** `CAP_NET_BIND_SERVICE` not needed
  (we use ports > 1024); no setuid binaries.
- **Library on NFS / SMB.** The `maktaba` user must be in
  the right group; documented. systemd `ReadWritePaths`
  needs the mount to be accessible at start time —
  document `Requires=remote-fs.target`.
- **Reboot.** systemd ensures startup ordering after
  network-online.
- **Distros without systemd.** Out for v1 (Devuan,
  Slackware, Alpine OpenRC). Tarball + manual init
  scripts is the path.
- **Repo signing key rotation.** We publish a key
  rotation procedure; old key honored 90 days post-rotate.

## Files / packages

- `packaging/debian/` — `debian/control`, `postinst`,
  `prerm`.
- `packaging/rpm/maktaba.spec`.
- `packaging/snap/snapcraft.yaml`.
- `packaging/flatpak/app.maktaba.Server.yml`.
- `packaging/appimage/AppRun`.
- `packaging/systemd/maktaba.service`.
- `release/.github/workflows/release-linux.yml` — build
  matrix.

## Open questions

- **APT repo hosting.** Cloudflare R2 + a static script
  is the cheapest path. Document.
- **CentOS 8 / RHEL 7.** End-of-life; v1 doesn't target.
