# Maktaba server installers

Stories 25.27–25.36 deliver one-binary installers for each platform we
support. The cloud doesn't ship the on-prem server itself — that lives
in `api/` — but the installer pipelines, signing, and first-run wizard
are owned here because they consume cloud-issued claim tokens and
entitlements.

## Layout

```
installers/
  macos/         # signed/notarized .dmg + sparkle feed
  windows/       # MSI via WiX + Authenticode
  linux/         # .deb, .rpm, .pkg.tar.zst (debian/redhat/arch families)
  docker/        # multi-arch images: amd64, arm64
  nas/           # Synology .spk, QNAP .qpkg
  rpi/           # Raspberry Pi images: 64-bit Lite + Maktaba overlay
  cloud-vps/     # one-click templates: DigitalOcean / Hetzner / Linode
```

Every installer's job is the same:

1. Place the `maktaba-server` binary + config at the platform-conventional path.
2. Register a service unit (launchd / systemd / SCM / Synology pkg-svc).
3. Run the **first-run setup wizard** which:
   a. Generates a host keypair.
   b. Asks the user for the **8-char claim code** they get from
      https://app.maktaba.app → "Add server".
   c. Calls `POST /v1/servers/claims/redeem` to exchange the code for
      a `server_id` + `server_secret`.
   d. Stores the secret in the platform secret store (Keychain on macOS,
      DPAPI on Windows, `secret-service`/`gnome-keyring` on Linux, file
      at `/etc/maktaba/secret` mode 0600 on headless boxes).
4. Connect to the relay (`wss://relay.maktaba.app/v1/relay/ws`).

## Signing

| Platform | Signing tooling | Key location |
|---|---|---|
| macOS | `codesign --options=runtime` + `xcrun notarytool` | Apple Developer ID, stored in 1Password vault `maktaba-codesign` |
| Windows | `signtool` (Authenticode) | EV cert on YubiKey, off-network |
| Linux .deb/.rpm | `dpkg-sig` / `rpm --addsign` (GPG) | GPG key `0xMAKTABASIGNING`, retained in `vault.hamzalabs.com/keys/release` |
| Docker | `cosign sign-blob` | Cosign keyring, GitHub OIDC-bound |
| iOS/Android (clients) | xcode automated; Play store upload key | platform-managed |

## Auto-update

Each installer registers an update channel:

- macOS → Sparkle XML feed at `https://releases.maktaba.app/macos/appcast.xml`
- Windows → Squirrel feed
- Linux → distro package repo
- Docker → image tag (`stable`, `beta`)
- RPi → `apt-get upgrade` against our hosted repo

The auto-update mechanism never bypasses signature checks. A failed
signature aborts the update and keeps the previous binary running.

## Uninstaller

Each installer ships an uninstaller that:

- Stops + removes the service.
- Removes the binary and config.
- Leaves user library data on disk by default; an interactive prompt
  offers complete wipe (data + secret).
- Calls `DELETE /v1/servers/{id}` so the cloud forgets the server.

## Status

| Story | Asset | Status |
|---|---|---|
| 25.27 | macOS DMG | scaffold |
| 25.28 | Windows MSI | scaffold |
| 25.29 | Linux .deb/.rpm | scaffold |
| 25.30 | Docker image | scaffold |
| 25.31 | NAS .spk/.qpkg | scaffold |
| 25.32 | Raspberry Pi image | scaffold |
| 25.33 | Cloud VPS templates | scaffold |
| 25.34 | Auto-update | scaffold |
| 25.35 | First-run wizard | scaffold |
| 25.36 | Uninstaller | scaffold |

Scaffolding means the file layout, signing hooks, and CI matrix entries
exist — real builds wire up in follow-up PRs once the cloud relay is
green in staging.
