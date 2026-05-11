# Uninstaller (Story 25.36)

Every platform installer ships an uninstaller that follows the same
flow. The user-visible commands:

| Platform | Invocation |
|---|---|
| macOS | drag `Maktaba.app` to Trash → uninstaller dialog appears |
| Windows | "Add or Remove Programs" → Maktaba Server |
| Debian/Ubuntu | `sudo apt remove maktaba-server` |
| RHEL/Fedora | `sudo dnf remove maktaba-server` |
| Arch | `sudo pacman -Rns maktaba-server` |
| Docker | `docker stop / docker rm` |
| Synology | Package Center → Uninstall |
| QNAP | App Center → Uninstall |
| RPi image | `sudo maktaba-server uninstall` |

## What the uninstaller does

1. **Notifies the cloud**: `DELETE /v1/servers/{id}` (best-effort —
   skipped if the box is offline; the cloud reaps it after 30 days of
   inactivity anyway).
2. **Stops the service** and disables it from auto-start.
3. **Removes the binary**, the systemd/launchd/SCM unit, and the
   config under `/etc/maktaba` (Linux paths used as illustration).
4. **Preserves user data by default.** The library — sometimes
   terabytes — is left in place at `/var/lib/maktaba/library`. A
   bright-yellow message tells the user where it is and how to wipe it.
5. **Offers a `--purge` flag** that also removes:
   - `/var/lib/maktaba/` (library, metadata, secret)
   - the `maktaba` user/group
   - any keychain entries

## Idempotency

Running the uninstaller against an already-uninstalled system is a
no-op (exit 0). This matters for orchestration tools that re-run
playbooks on each deploy.

## Secret hygiene

The server secret stored at `/var/lib/maktaba/secret` is overwritten
with zeros before deletion to defeat scavenging from undeleted blocks
on cheap SD cards — a cheap defense-in-depth move and standard for any
device that may be resold.
