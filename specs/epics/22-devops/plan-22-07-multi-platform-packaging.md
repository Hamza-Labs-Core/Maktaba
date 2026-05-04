# Implementation Plan — Story 22.7 Multi-platform packaging

> Companion to [story-22-07-multi-platform-packaging.md](story-22-07-multi-platform-packaging.md).
> Story states *what* and *why*; this plan states *how*.
> Builds on the artifacts produced by
> [Story 22.2](plan-22-02-reproducible-builds.md) and the release flow
> from [Story 22.5](plan-22-05-release-management.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Homebrew tap | `maktaba/homebrew-tap` repo; `Formula/maktaba.rb` rendered by `tools/render-formula.sh` from `deploy/homebrew/Formulafile.tpl`. |
| Debian / RPM | Built via `nfpm` from a single `nfpm.yaml` per arch; published to a static apt/yum repo at `pkg.maktaba.io/{deb,rpm}`. |
| Mobile | Capacitor wraps `web/`; iOS via Xcode Cloud (or a self-hosted Mac runner), Android via Gradle. App Store Connect & Play Console for release. |
| Desktop | Tauri builds .dmg/.msi/.AppImage. Auto-update via Tauri's updater pointing at a release JSON in GitHub. |
| TV apps | Xcode (tvOS) and Gradle (Android TV). Manual publish in v1. |
| Mobile/desktop signing | Maintainer-held keys live in 1Password; CI uses dev-signing only via secrets-scoped GitHub environments. |
| Out of scope | The web bundle (Story 22.3 owns `web` image; the Tauri/Capacitor layers wrap the same bundle); auto-update server (uses GitHub release JSON, not a dedicated service). |

## 1. Architecture diagram

```
                   release.yml (Story 22.5)
                            │ produces
   ┌──────────────┬─────────┼─────────┬──────────┬──────────┐
   ▼              ▼         ▼         ▼          ▼          ▼
 brew-tap     deb/rpm   ios .ipa  android .apk  .dmg/.msi  tvos/atv
   ▲              ▲         ▲         ▲          ▲          ▲
   │              │         │         │          │          │
deploy/        deploy/    apps/    apps/      apps/       apps/
homebrew/      packaging/ mobile/  mobile/    desktop/    {tvos,androidtv}/
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `deploy/homebrew/Formulafile.tpl` | Templated Homebrew formula. |
| `tools/render-formula.sh` | Substitutes URLs and sha256 sums for the tag. |
| `deploy/launchd/io.maktaba.api.plist`, `io.maktaba.streaming.plist`, `io.maktaba.pipeline.plist` | macOS service definitions (already named in architecture §12). |
| `deploy/packaging/nfpm.yaml` | Source of truth for deb/rpm metadata. |
| `deploy/packaging/systemd/maktaba-api.service`, `-streaming.service`, `-pipeline.service` | Linux unit files. |
| `deploy/packaging/postinst.sh`, `postrm.sh` | Create `maktaba` user, set perms, enable units. |
| `apps/mobile/capacitor.config.ts` | Already named in architecture; this story adds `compatibleApiVersion`. |
| `apps/mobile/build-mobile.sh` | Wraps `pnpm build && pnpm cap sync && xcodebuild/gradle`. |
| `apps/desktop/src-tauri/tauri.conf.json` | Already named; this story adds `updater.endpoints` and `pubkey`. |
| `apps/desktop/build-desktop.sh` | Wraps `pnpm tauri build`. |
| `apps/tvos/Maktaba.xcodeproj` | Generated. |
| `apps/androidtv/build.gradle.kts` | Generated. |
| `tools/smoke-install/{brew,deb}.sh` | Per-platform smoke tests for TC1, TC2. |
| `.github/workflows/_packaging.yml` | Matrix runner for the five package paths. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `.github/workflows/release.yml` | Adds `packaging` job that fans out into deb/rpm/brew/mobile/desktop/tv. |
| `tools/build.sh` | New target `apps`: drives Tauri + Capacitor builds. |

### 2.3 Homebrew formula template

`deploy/homebrew/Formulafile.tpl`:

```ruby
class Maktaba < Formula
  desc "Self-hosted media library with transcripts, search, and HLS streaming"
  homepage "https://maktaba.io"
  url "{{ARCHIVE_URL}}"
  sha256 "{{ARCHIVE_SHA256}}"
  license "AGPL-3.0-or-later"
  version "{{VERSION}}"

  depends_on "ffmpeg" => :recommended
  depends_on "postgresql@16" => :recommended
  depends_on "uv"

  def install
    bin.install "bin/maktaba-api"
    bin.install "bin/maktaba-streaming"
    libexec.install "pipeline" => "pipeline"
    (bin/"maktaba-pipeline").write <<~EOS
      #!/bin/bash
      cd "#{libexec}/pipeline" && exec uv run maktaba-pipeline "$@"
    EOS

    (var/"maktaba").mkpath
    (var/"log/maktaba").mkpath

    (prefix/"Library/LaunchAgents").install Dir["launchd/io.maktaba.*.plist"]
  end

  def post_install
    # If user already has Postgres, skip role+db creation; otherwise bootstrap.
    if system("psql -lqt | cut -d\\| -f 1 | grep -qw maktaba")
      ohai "Existing 'maktaba' DB detected — skipping bootstrap"
    else
      system "createdb", "maktaba"
      system "psql", "-c", "CREATE ROLE maktaba LOGIN", "maktaba"
    end
  end

  service do
    run [opt_bin/"maktaba-api", "serve"]
    keep_alive true
    log_path var/"log/maktaba/api.log"
    error_log_path var/"log/maktaba/api.err.log"
  end

  test do
    assert_match "v#{version}", shell_output("#{bin}/maktaba-api --version")
  end
end
```

`tools/render-formula.sh` substitutes `{{ARCHIVE_URL}}`, `{{VERSION}}`,
and `{{ARCHIVE_SHA256}}` from the release artifact metadata.

### 2.4 launchd plists

`deploy/launchd/io.maktaba.api.plist`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>io.maktaba.api</string>
  <key>ProgramArguments</key>
  <array>
    <string>/usr/local/bin/maktaba-api</string>
    <string>serve</string>
    <string>--config</string><string>/usr/local/etc/maktaba/api.toml</string>
  </array>
  <key>KeepAlive</key><true/>
  <key>RunAtLoad</key><true/>
  <key>StandardOutPath</key><string>/usr/local/var/log/maktaba/api.log</string>
  <key>StandardErrorPath</key><string>/usr/local/var/log/maktaba/api.err.log</string>
  <key>EnvironmentVariables</key>
  <dict>
    <key>MAKTABA_DATABASE_URL</key>
    <string>postgres:///maktaba</string>
  </dict>
</dict>
</plist>
```

The streaming and pipeline plists follow the same shape.

### 2.5 Debian / RPM via nfpm

`deploy/packaging/nfpm.yaml`:

```yaml
name: maktaba
arch: ${ARCH}                 # populated by the build script: amd64 | arm64
platform: linux
version: ${VERSION}
section: media
priority: optional
maintainer: "Maktaba Maintainers <maintainers@maktaba.io>"
description: |
  Self-hosted media library with transcripts, search, and HLS streaming.
homepage: https://maktaba.io
license: AGPL-3.0-or-later

depends:
  - postgresql-client
  - ffmpeg (>= 6.0)
  - python3 (>= 3.12)

contents:
  - src: api/bin/maktaba-api
    dst: /usr/bin/maktaba-api
  - src: streaming/bin/maktaba-streaming
    dst: /usr/bin/maktaba-streaming
  - src: pipeline/dist/wheel
    dst: /usr/lib/maktaba/pipeline
  - src: deploy/packaging/systemd/maktaba-api.service
    dst: /lib/systemd/system/maktaba-api.service
  - src: deploy/packaging/systemd/maktaba-streaming.service
    dst: /lib/systemd/system/maktaba-streaming.service
  - src: deploy/packaging/systemd/maktaba-pipeline.service
    dst: /lib/systemd/system/maktaba-pipeline.service
  - src: /var/lib/maktaba
    type: dir
    file_info:
      owner: maktaba
      group: maktaba
      mode: 0750

scripts:
  postinstall: deploy/packaging/postinst.sh
  preremove:   deploy/packaging/postrm.sh

overrides:
  rpm:
    depends:
      - postgresql
      - ffmpeg
      - python3 >= 3.12
```

`postinst.sh`:

```bash
#!/bin/sh
set -e
getent passwd maktaba >/dev/null || useradd --system --home /var/lib/maktaba --shell /usr/sbin/nologin maktaba
chown -R maktaba:maktaba /var/lib/maktaba
systemctl daemon-reload
systemctl enable --now maktaba-api maktaba-streaming maktaba-pipeline
```

systemd unit example (`maktaba-api.service`):

```ini
[Unit]
Description=Maktaba API
After=network.target postgresql.service
Wants=postgresql.service

[Service]
Type=notify
User=maktaba
ExecStart=/usr/bin/maktaba-api serve --config /etc/maktaba/api.toml
Restart=on-failure
RestartSec=5s
NoNewPrivileges=true
ProtectSystem=strict
ReadWritePaths=/var/lib/maktaba /var/log/maktaba
ProtectHome=true
PrivateTmp=true

[Install]
WantedBy=multi-user.target
```

The Go binaries gain a `Type=notify` integration via `sd_notify` so
systemd knows when the service is ready.

### 2.6 Mobile build

`apps/mobile/capacitor.config.ts`:

```ts
import { CapacitorConfig } from '@capacitor/cli';
const config: CapacitorConfig = {
  appId: 'io.maktaba.app',
  appName: 'Maktaba',
  webDir: '../web/dist',
  android: { buildOptions: { keystorePath: 'keystore.jks' } },
  plugins: {
    SplashScreen: { launchShowDuration: 0 },
  },
  // EC3 from Story 22.5; the API rejects out-of-range clients.
  compatibleApiVersion: '>=1.0.0 <2.0.0',
};
export default config;
```

`apps/mobile/build-mobile.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail

VERSION=${VERSION:-$(cat ../../VERSION)}
pushd ../../web && pnpm build && popd
pnpm cap sync

# iOS: archive + export. CI uses dev-signing certs from the github
# Environment 'mobile-ci'; release uploads happen from a maintainer Mac.
if [[ "${TARGET:-ios}" == "ios" ]]; then
  xcodebuild -workspace ios/App.xcworkspace -scheme App \
    -configuration Release archive -archivePath build/App.xcarchive \
    CODE_SIGN_IDENTITY="${CODESIGN_IDENTITY:-Apple Development}"
  xcodebuild -exportArchive -archivePath build/App.xcarchive \
    -exportPath build/ipa -exportOptionsPlist ios/ExportOptions.plist
fi

if [[ "${TARGET:-android}" == "android" ]]; then
  pushd android
  ./gradlew assembleRelease bundleRelease
  popd
fi
```

The maintainer-held release-signing keys never enter CI. The release-
channel build flow in `RELEASING.md` instructs the maintainer to:
download the dev-signed CI artifact, re-sign locally with the release
keys, upload via App Store Connect / Play Console.

### 2.7 Desktop (Tauri)

`apps/desktop/src-tauri/tauri.conf.json` (updater additions):

```json
{
  "tauri": {
    "updater": {
      "active": true,
      "dialog": false,
      "endpoints": [
        "https://github.com/maktaba/maktaba/releases/download/{{current_version}}/desktop-latest.json"
      ],
      "pubkey": "<MAINTAINER_PUBLIC_KEY_BASE64>"
    },
    "bundle": {
      "active": true,
      "targets": ["dmg", "msi", "appimage"],
      "identifier": "io.maktaba.desktop"
    }
  }
}
```

A small JSON file shipped in each release describes the latest version
and platform-specific URLs:

```json
{
  "version": "v1.2.0",
  "platforms": {
    "darwin-x86_64":   { "url": "https://…/Maktaba.dmg",     "signature": "…" },
    "darwin-aarch64":  { "url": "https://…/Maktaba_arm64.dmg","signature": "…" },
    "windows-x86_64":  { "url": "https://…/Maktaba.msi",     "signature": "…" },
    "linux-x86_64":    { "url": "https://…/Maktaba.AppImage","signature": "…" }
  }
}
```

Auto-update is opt-in via a settings toggle in the app. The pubkey is
the same minisign key from Story 22.2.

### 2.8 TV apps

Both projects build via `xcodebuild` and `gradle`; the artifacts are
uploaded to GitHub release for v1 (manual store upload). The Apollo
GraphQL client in `apps/tvos/Sources/API/` is regenerated against
`shared/graphql/schema.graphql` on each build.

### 2.9 Smoke tests

`tools/smoke-install/brew.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail
brew tap maktaba/tap
brew install maktaba
brew services start maktaba

for i in 1 2 3 4 5; do
  if curl -fsS http://localhost:8080/api/health > /dev/null; then
    exit 0
  fi
  sleep 5
done
echo "Smoke test failed: API not healthy"; exit 1
```

`tools/smoke-install/deb.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail
apt-get update
dpkg -i ./maktaba_${VERSION}_amd64.deb || apt-get -fy install
systemctl is-active --quiet maktaba-api
curl -fsS http://localhost:8080/api/health > /dev/null
```

## 3. Test plan

### 3.1 Homebrew (TC1)

| Test | What it pins |
|---|---|
| `TestBrewInstallOnFreshMac` | macOS 14 runner; `brew tap` + `install` succeeds; three plists boot; `/api/health` 200. |
| `TestBrewExistingPostgres` (EC1) | The runner's brew Postgres is already running with a `maktaba` DB; the formula skips bootstrap; service starts using it. |
| `TestBrewUninstallClean` | `brew uninstall` removes binaries and plists; `/usr/local/var/maktaba` data persists (per Homebrew convention). |

### 3.2 Debian/RPM (TC2)

| Test | What it pins |
|---|---|
| `TestDebInstallOnUbuntuLts` | Fresh Ubuntu 22.04; `dpkg -i` succeeds; systemd units start; smoke. |
| `TestRpmInstallOnFedora` | Fresh Fedora 40; `dnf install ./maktaba.rpm`; smoke. |
| `TestDebMissingFfmpeg` (EC2) | Strip ffmpeg from the runner; install fails at the `Depends:` resolution with a clear message; the user sees "ffmpeg is required". |

### 3.3 Mobile (TC3)

| Test | What it pins |
|---|---|
| `TestCapacitorBuildIosIpa` | `apps/mobile/build-mobile.sh ios` produces an `App.ipa`; an iOS simulator boot + login + library smoke runs in CI. |
| `TestCapacitorBuildAndroidApk` | `build-mobile.sh android` produces an `app-release.apk`; an emulator smoke run completes login + library list. |
| `TestCompatVersionMismatch` | A mobile build with `compatibleApiVersion: 0.x` connects to a v1 API and sees the documented refuse-message. |

### 3.4 Desktop

| Test | What it pins |
|---|---|
| `TestTauriBuildMacDmg` | `build-desktop.sh` on darwin/arm64 produces `.dmg`; mounts; the bundled binary runs. |
| `TestTauriUpdaterRoundTrip` | Old build → publishes new release JSON → old build's updater detects + applies; signature verified. |
| `TestAppImageUpdaterDisabled` (EC3) | Linux user toggles updater off; the app does not fetch the JSON. |

## 4. Edge cases

| Case | Behaviour | Where pinned |
|---|---|---|
| Existing Postgres on Mac (EC1) | Formula's `post_install` detects existing `maktaba` DB and skips create; documented. | `TestBrewExistingPostgres` |
| Linux distro without ffmpeg ≥ 6.0 (EC2) | Package's `Depends:` line lists `ffmpeg (>= 6.0)`; install fails fast on older distros. The deb is therefore not for Debian 11 (ffmpeg 4.x); supported list documented. | `TestDebMissingFfmpeg` |
| AppImage updater (EC3) | Tauri's AppImage updater path uses the bundled updater; toggle in settings persists to `~/.config/maktaba/`. | `TestAppImageUpdaterDisabled` |
| Mac Gatekeeper / notarization | The release-channel `.dmg` is notarized by the maintainer post-build; CI artifacts are dev-signed and Gatekeeper will warn. Documented in RELEASING.md. | n/a |
| Windows SmartScreen | Initial `.msi` releases will SmartScreen-warn until reputation builds; documented; signing cert is held by maintainer. | n/a |
| Android Play Store integrity | Play Integrity API is integrated optionally; off in v1. | n/a |
| iOS App Store reviewer requires demo creds | The login flow accepts the documented "demo" account (single-user mode against a demo box) for review purposes only. | RELEASING.md |
| TV app store rules forbid web views | Both tvOS and Android TV are *native*; Capacitor and Tauri are not used here. | architecture §6.5 |
| Homebrew tap rate limit | `tools/render-formula.sh` is idempotent; running again is a no-op if the formula is already current. | n/a |

## 5. Dependencies

| Dep | Version | Why |
|---|---|---|
| `nfpm` | latest | deb/rpm packaging. |
| `Homebrew` | n/a | macOS package manager. |
| `Capacitor` | 6.x | Mobile wrapper. |
| `Tauri` | 2.x | Desktop wrapper. |
| `Xcode` | matches Apple SDK rev | iOS / tvOS builds. |
| `Android SDK` | 34+ | Android builds. |
| `gradle`, `xcodebuild` | as installed | Native builds. |

## 6. Acceptance checklist

**Homebrew**
- [ ] `brew install maktaba/tap/maktaba` succeeds on a fresh Mac.
- [ ] Three plists install and run.
- [ ] Post-install detects existing Postgres.

**Debian/RPM**
- [ ] `.deb` and `.rpm` install on Ubuntu LTS and Fedora respectively.
- [ ] `maktaba` user created.
- [ ] systemd units enabled and active.

**Mobile**
- [ ] CI produces `.ipa` and `.apk` artifacts.
- [ ] `compatibleApiVersion` enforced.

**Desktop**
- [ ] `.dmg`, `.msi`, `.AppImage` produced.
- [ ] Auto-update opt-in toggle exists and works.

**TV**
- [ ] tvOS and Android TV builds produce signed artifacts in CI.

**Release flow**
- [ ] `release.yml` fans out into `_packaging.yml` after the core build.
- [ ] Mobile/desktop signing keys never enter CI; release-channel signing is on a maintainer machine.
