# Raspberry Pi / ARM installer (Story 25.32)

We ship two artifacts for ARM:

1. **A Raspberry Pi OS Lite image** with maktaba-server preinstalled.
   Built from the official Raspberry Pi OS Lite (Bookworm 64-bit) image
   via [pi-gen](https://github.com/RPi-Distro/pi-gen). The build adds:
   - `maktaba-server` Debian package from the linux installer.
   - A first-boot oneshot service that runs the setup wizard on the
     console (claim-code prompt).

2. **A raw `.deb` for ARM Linux** (Pi OS, Ubuntu Server) — see
   `cloud/installers/linux/`; pi-gen consumes the same `.deb`.

## Build

```sh
# In a Debian/Ubuntu builder VM with binfmt qemu-user-static:
git clone https://github.com/RPi-Distro/pi-gen.git
cp -r stage-maktaba pi-gen/
cd pi-gen
./build.sh -c config-maktaba
# Output: deploy/<date>-Maktaba.img.xz
```

## First boot

The image ships with a oneshot systemd unit `maktaba-first-boot.service`
that prompts on TTY1 for the 8-char claim code, calls
`POST /v1/servers/claims/redeem`, persists the server secret to
`/var/lib/maktaba/secret`, and disables itself so it never runs again.
