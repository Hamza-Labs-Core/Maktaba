# Implementation Plan — Story 25.29 Linux packages

> Companion to [story-25-29-linux-packages.md](story-25-29-linux-packages.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Channels | `.deb` (apt.maktaba.app), `.rpm` (rpm.maktaba.app), Snap, Flatpak, AppImage, plain tarball. |
| Build | One Go binary `maktaba` (subcommands wrap the underlying api/streaming/pipeline binaries). Per format wrapping in CI. |
| systemd unit | `maktaba.service` with `ProtectSystem=strict`, `NoNewPrivileges=true`, `LimitNOFILE=65536`. |
| System user | `maktaba` (no shell, no home); created by post-install. |
| Signing | GPG for deb/rpm; Snap auto-signed by store; Flatpak via repo signing key. |
| Out of scope | CentOS 7 / RHEL 7 EOL. AUR (community-maintained). Distros without systemd. |

## 1. Repository structure

```
packaging/
  debian/
    debian/
      control                # binary package metadata
      postinst, prerm, postrm
      maktaba.install        # file copy map
      maktaba.service        # systemd unit (same as below)
      rules
    apt-repo/                # CI uploads .deb here; reprepro config
  rpm/
    maktaba.spec
    rpm-repo/
  snap/
    snapcraft.yaml
  flatpak/
    app.maktaba.Server.yml
  appimage/
    AppRun
  systemd/
    maktaba.service
  scripts/
    create-maktaba-user.sh
    rotate-gpg-key.sh
```

## 2. systemd unit

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
ReadWritePaths=/var/lib/maktaba /var/log/maktaba /etc/maktaba
PrivateTmp=true
NoNewPrivileges=true
EnvironmentFile=-/etc/default/maktaba

[Install]
WantedBy=multi-user.target
```

## 3. Debian package

`debian/control`:

```
Source: maktaba
Section: misc
Priority: optional
Maintainer: HamzaLabs <ops@maktaba.app>
Build-Depends: debhelper-compat (= 13)
Standards-Version: 4.6.0

Package: maktaba
Architecture: amd64 arm64
Depends: ${shlibs:Depends}, ${misc:Depends}, adduser, libvips42, avahi-daemon | systemd-resolved (>= 233)
Recommends: ffmpeg
Description: Maktaba Media Server
```

`postinst`:

```bash
#!/bin/sh
set -e
if [ "$1" = configure ]; then
  if ! getent passwd maktaba >/dev/null; then
    adduser --system --group --no-create-home --shell /usr/sbin/nologin maktaba
  fi
  install -d -m 0750 -o maktaba -g maktaba /var/lib/maktaba /var/log/maktaba /etc/maktaba
  systemctl daemon-reload || true
  systemctl enable --now maktaba.service || true
fi
```

`prerm` stops service; `postrm purge` removes data and `maktaba` user.

## 4. RPM spec

```rpm
Name: maktaba
Version: 1.0.0
Release: 1%{?dist}
Summary: Maktaba Media Server
License: MIT
URL: https://maktaba.app

Requires: vips, ffmpeg, avahi
%description
Maktaba Media Server (Go + Python).

%install
install -Dm 0755 maktaba %{buildroot}/usr/bin/maktaba
install -Dm 0644 maktaba.service %{buildroot}/lib/systemd/system/maktaba.service
install -d -m 0750 %{buildroot}/var/lib/maktaba %{buildroot}/var/log/maktaba %{buildroot}/etc/maktaba

%post
getent passwd maktaba >/dev/null || useradd --system --no-create-home --shell /sbin/nologin maktaba
systemctl daemon-reload || true
systemctl enable --now maktaba.service || true

%preun
[ $1 -eq 0 ] && systemctl disable --now maktaba.service || true

%postun
[ $1 -eq 0 ] && userdel maktaba || true
```

## 5. Snap

```yaml
name: maktaba
base: core22
confinement: strict
adopt-info: maktaba
apps:
  maktaba:
    command: bin/maktaba serve
    daemon: simple
    plugs: [network, network-bind, home, removable-media]
parts:
  maktaba:
    plugin: dump
    source: ./staging/
    override-pull: |
      craftctl set version=$(cat VERSION)
```

Snap auto-update opt-in. Library access requires:

```
sudo snap connect maktaba:home
sudo snap connect maktaba:removable-media
```

## 6. Flatpak

`app.maktaba.Server.yml`:

```yaml
app-id: app.maktaba.Server
runtime: org.freedesktop.Platform
runtime-version: '23.08'
sdk: org.freedesktop.Sdk
command: maktaba
finish-args:
  - --share=network
  - --filesystem=home
  - --socket=session-bus
  - --filesystem=xdg-videos
modules:
  - name: maktaba
    buildsystem: simple
    build-commands:
      - install -Dm 0755 maktaba /app/bin/maktaba
```

`org.freedesktop.portal.FileChooser` prompts user for library paths.

## 7. AppImage

`AppRun`:

```bash
#!/bin/sh
HERE="$(dirname "$(readlink -f "$0")")"
export PYTHONHOME="$HERE/usr/share/python"
exec "$HERE/usr/bin/maktaba" "$@"
```

`AppImageTool` packages the `AppDir`. `--install` flag creates per-user systemd unit at `~/.config/systemd/user/maktaba.service`.

## 8. APT/DNF repos

Hosted on Cloudflare R2 (cheap). Static repo metadata generated via `reprepro` (Debian) and `createrepo_c` (RPM). GPG signed.

`apt.maktaba.app`:

```
deb https://apt.maktaba.app stable main
```

CI publishes new packages on tag push.

## 9. Test plan

### 9.1 Smoke (CI matrix)

| Test | Pins |
|---|---|
| Ubuntu 22.04 / 24.04 / Debian 12 | `apt install maktaba` works; healthz 200. |
| Fedora 40 / Rocky 9 | `dnf install maktaba` works. |
| Snap | install + library access via connect. |
| Flathub | install + portal file chooser. |
| AppImage | run on Ubuntu without systemd unit; `--install` writes user unit. |
| Arch (community PKGBUILD) | Recipe builds (no host). |

### 9.2 Regression

| Test | Pins |
|---|---|
| `TestDpkgUpgradePreservesDB` | New version installed; data dir untouched. |
| `TestPurgeRemovesEverything` | `apt purge` removes user + dirs. |
| `TestSELinuxEnforcing` | RHEL enforcing → service starts. |
| `TestAppArmorUbuntu` | Default profile fine. |
| `TestSystemdProtectSystem` | No writes outside ReadWritePaths. |
| `TestRepoSignatureValid` | GPG verifies on a fresh box. |

## 10. Edge cases — handling table

| Case | Behaviour | Pinned |
|---|---|---|
| SELinux enforcing | Default policy ok; test pinned. | Spec. |
| Snap confinement | `home` + `removable-media` interfaces. | Doc. |
| Flatpak portal | Per-path prompts. | Spec. |
| Avahi soft-dep | mDNS discovery (15.2). | Recommends. |
| musl Alpine | Out — point to Docker (25.30). | Doc. |
| CAP_NET_BIND_SERVICE | Not needed; ports > 1024. | Spec. |
| Library on NFS/SMB | Document group membership. | Doc. |
| Reboot | systemd ordering after network-online. | Unit. |
| Devuan/OpenRC | Out for v1. | Doc. |
| Repo key rotation | 90d overlap. | Doc. |

## 11. Dependencies

- Single binary build pipeline shared with Docker (25.30) and macOS (25.27).
- 25.34 (auto-update meets apt/dnf channels).

## 12. Acceptance checklist

- [ ] APT + DNF repos on Cloudflare R2 with GPG signatures.
- [ ] Snap + Flatpak listings.
- [ ] AppImage with `--install`.
- [ ] systemd unit with hardening.
- [ ] `maktaba` system user created on install; removed on purge.
- [ ] CI matrix from §9 green.
