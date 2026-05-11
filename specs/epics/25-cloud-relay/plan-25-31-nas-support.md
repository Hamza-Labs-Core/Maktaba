# Implementation Plan — Story 25.31 NAS support (Synology, QNAP, TrueNAS, Unraid)

> Companion to [story-25-31-nas-support.md](story-25-31-nas-support.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Runtime | The Docker image from 25.30 — every NAS package is *vendor glue*, not a separate build. |
| Synology | `.spk` package via per-platform Synology SDK; pulls Docker image at install. |
| QNAP | QPKG via QDK. |
| TrueNAS SCALE | Helm chart + catalog manifest. |
| Unraid | Community Apps XML template. |
| Permissions | Each vendor's default media-share UID/GID injected via container runtime args. |
| Out of scope | ASUSTOR, OpenMediaVault (use 25.30 directly), custom NAS firmware. |

## 1. Vendor matrix

| Vendor | UID | GID | Min OS | Notes |
|---|---|---|---|---|
| Synology DSM | 100 (`SYNO_DEFAULT_UID`) | 100 (`users`) | DSM 7.0+ | Uses Container Manager; legacy Docker UI compat. |
| QNAP QTS | 1000 (`admin`) | 100 (`everyone`) | QTS 5.0+ | Same idiom. |
| TrueNAS SCALE | 568 (`apps`) | 568 | SCALE 24.04+ | Helm chart wrapper. |
| Unraid | 99 (`nobody`) | 100 (`users`) | 6.12+ | XML template. |

## 2. Synology SPK

`packaging/synology/`:

```
INFO                                # name=maktaba, version=$VERSION, displayname, description
PACKAGE_ICON.PNG (72x72)
package/                            # files installed to /var/packages/maktaba/target/
  bin/maktaba-wrapper               # docker run … --user 100:100 … ghcr.io/...
scripts/
  preinst, postinst, preuninst, postuninst, start-stop-status
  installer
WIZARD_UIFILES/                     # DSM install dialog
  install_uifile, install_uifile_xx_XX
```

`scripts/postinst`:

```bash
#!/bin/sh
docker pull ghcr.io/hamza-labs-core/maktaba:latest
mkdir -p /volume1/@appdata/maktaba/{data,cache}
chown -R 100:100 /volume1/@appdata/maktaba
```

`start-stop-status start`:

```bash
docker run -d --name maktaba \
    --user 100:100 \
    --restart=unless-stopped \
    -p 8080:8080 -p 8081:8081 \
    -v /volume1/@appdata/maktaba/data:/var/lib/maktaba \
    -v /volume1/@appdata/maktaba/cache:/var/cache/maktaba \
    -v /volume1/Multimedia:/media:ro \
    ghcr.io/hamza-labs-core/maktaba:latest
```

DSM reverse-proxy helper script offered post-install (sets up `maktaba.<domain>` via DSM control panel API).

## 3. QNAP QPKG

`packaging/qnap/qpkg.cfg`:

```
QPKG_NAME="maktaba"
QPKG_DISPLAY_NAME="Maktaba"
QPKG_DESC="Maktaba Media Server"
QPKG_AUTHOR="HamzaLabs"
QPKG_LICENSE="MIT"
QPKG_VER="1.0.0"
QPKG_REQUIRE="ContainerStation >= 2.5"
```

`package_routines/install.sh` runs `docker pull` and registers a Container Station application. Library paths default to `/share/Multimedia` (mapped to `/media:ro`).

## 4. TrueNAS SCALE

Helm chart in `packaging/truenas/`:

```
Chart.yaml
values.yaml
templates/
  deployment.yaml
  service.yaml
  ingress.yaml
  pvc.yaml
questions.yaml                       # TrueNAS UI form
```

`values.yaml`:

```yaml
image:
  repository: ghcr.io/hamza-labs-core/maktaba
  tag: "1.0.0"
runAsUser: 568
runAsGroup: 568
service:
  api: { port: 8080 }
  streaming: { port: 8081 }
persistence:
  data: { size: 5Gi }
  cache: { size: 20Gi }
mediaPaths: []
```

`questions.yaml` exposes "library paths" via the TrueNAS Apps form.

## 5. Unraid template

`packaging/unraid/maktaba.xml`:

```xml
<Container version="2">
  <Name>maktaba</Name>
  <Repository>ghcr.io/hamza-labs-core/maktaba:latest</Repository>
  <Network>bridge</Network>
  <Config Name="Web UI" Target="8080" Default="8080" Mode="tcp" Type="Port"/>
  <Config Name="Streaming" Target="8081" Default="8081" Mode="tcp" Type="Port"/>
  <Config Name="Data" Target="/var/lib/maktaba" Default="/mnt/user/appdata/maktaba/data" Type="Path"/>
  <Config Name="Cache" Target="/var/cache/maktaba" Default="/mnt/user/appdata/maktaba/cache" Type="Path"/>
  <Config Name="Media" Target="/media" Default="/mnt/user/Media" Mode="ro" Type="Path"/>
  <Environment>PUID=99</Environment>
  <Environment>PGID=100</Environment>
</Container>
```

Image accepts `PUID`/`PGID` env via entrypoint wrapper (override `--user` flag).

## 6. Update lifecycle

NAS package managers handle updates; we publish bumped package files in lockstep with the Docker image. Container Manager auto-update is disabled by default; the package manager is the source of truth.

## 7. Test plan

### 7.1 Manual matrix

| Test | Pins |
|---|---|
| Synology DS920+ DSM 7.2 | install SPK → start container → healthz 200. |
| Synology DSM 7.0 (min target) | Same. |
| QNAP QTS 5.1 | QPKG install. |
| QNAP QuTS hero (ZFS) | Same. |
| TrueNAS SCALE 24.04 | App install via catalog. |
| Unraid 6.12 | Template applied. |
| UID/GID per vendor | Media read without chown. |
| Reboot | Auto-start by package daemon. |
| Reverse-proxy on DSM | TLS + WS works through DSM proxy. |
| Resource caps | Stay within configured CPU/memory. |

## 8. Edge cases — handling table

| Case | Behaviour | Pinned |
|---|---|---|
| DSM 7.2 Container Manager | SPK supports both legacy and new. | Spec. |
| FS choice | btrfs / ext4 / ZFS all fine for SQLite; PG recommend ext4/ZFS. | Doc. |
| Default port collision | Set `MAKTABA_HTTP_PORT=8090` if 8080 taken. | Scripts. |
| Package update timing | Vendor schedule respected. | Spec. |
| Backup exclusion | Mark `cache/` volume. | Scripts. |
| HW transcode | Expose `/dev/dri/renderD128` on supported devices. | Doc. |
| ARM Synology | arm64 image runs. | Spec. |
| Reset/uninstall data prompt | Vendor wizard "Also delete data?". | UI. |

## 9. Dependencies

- 25.30 (Docker image is the runtime).
- 25.34 (release manifest for update notifications surfacing in the NAS package UI).

## 10. Acceptance checklist

- [ ] SPK / QPKG / Helm / Unraid template artifacts published per release.
- [ ] Vendor-correct UID/GID.
- [ ] One-click install on each vendor smoke-tested.
- [ ] Library shares readable without chown.
- [ ] Tests in §7 pass.
