# Epic 39 — Maktaba on a Linux machine without a desktop (stories 39.6, 39.9, 39.10)

A NAS, a mini PC or a VM runs the same payload as the desktop app, with no window. The setup asks
the desktop's questions in the terminal, through the same `setup --json` steps.

## Install

Nothing runs before it is verified. Everything comes from one tagged release (never from a branch),
and the payload is checked against the release's `checksums.txt`, whose Sigstore bundle must have
been signed by the release workflow of exactly that tag:

```sh
v=v1.5.0                       # the release to install
arch=$(uname -m)               # x86_64 or aarch64
base=https://github.com/Hamza-Labs-Core/Maktaba/releases/download/$v
asset=maktaba-linux-payload-${v#v}-$arch.tar.gz
curl -fLO "$base/$asset" -fLO "$base/checksums.txt" -fLO "$base/checksums.txt.sigstore.json"
cosign verify-blob --bundle checksums.txt.sigstore.json \
  --certificate-identity "https://github.com/Hamza-Labs-Core/Maktaba/.github/workflows/release.yml@refs/tags/$v" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com checksums.txt
sha256sum --check --ignore-missing checksums.txt
tar -xzf "$asset" && server/bin/maktaba-server setup
```

- **The payload tarball** (39.10) is `maktaba-linux-payload-<v>-<x86_64|aarch64>.tar.gz`: the same
  `server/` tree as the desktop app's `usr/share/maktaba/server`, listed in the release's
  `checksums.txt` like every asset.
- **cosign** is the verifier. It comes from the distro (`apt install cosign` on Debian 13+ and
  Ubuntu 24.04+, `dnf install cosign` on Fedora) or from its own release, checked against that
  release's checksums. A failed signature or checksum stops the install before anything is
  unpacked.
- **`install.sh --app`** scripts the same steps. The release publishes `install.sh` as an asset
  listed in `checksums.txt` (39.10), so the script is verified the same way before it runs (download
  it with the two files above, `cosign verify-blob`, `sha256sum --check`, then `bash install.sh
  --app`); it is never piped from `main`. With `--app` it refuses to unpack anything it could not
  verify: `MAKTABA_REQUIRE_COSIGN` is on.
- **The unpacked tree is only a source.** `setup` copies it into the runtime (below) and the server
  never runs from it. It can be deleted after setup.

`maktaba-server setup` (on a machine that has a payload and no server.toml) runs the steps in screen
order. It shows each step's `progress` as a line and each `error` with its help text, and asks for
the input fields:

| Step | In the terminal |
|---|---|
| `check` | what will run, the disk, RAM and GPU, the distribution's ffmpeg, the warnings; stops when `ok` is false (a system-wide `maktaba-server.service` is one: see below). When the ffmpeg is what blocks, check names the command that installs it (`ffmpeg_install`) and the terminal offers to run it (sudo asks for the password there; never from an answers file), then runs check again |
| `admin` | user name and password (asked twice, not echoed) |
| `libraries` | library folders, then the NAS path map (`/mnt/media => /srv/media`), each checked |
| `models` | Hugging Face token (not echoed), or the path of the home's copy on the share |
| `plex` | optional: the PIN to enter at plex.tv/link, polled until linked |
| `venv` | installs the runtime (`runtime install --owner headless`: the first runtime becomes `current` by itself), then builds the venv with a progress bar |
| `finish` | writes server.toml, makes the database, then `service install` and `service linger` (polkit asks for the admin password over SSH) |

Non-interactive (automation, CI): `maktaba-server setup --answers answers.json` gives each step its
input from the file (`{"admin": {...}, "libraries": {...}, ...}`). Secrets can come from
`--answers-fd N` instead of a file on disk.

### A system-wide Maktaba service already installed

The nfpm `.deb`/`.rpm` (and `install.sh` without `--app` as root) install a system unit,
`/etc/systemd/system/maktaba-server.service` (or one in `/usr/lib/systemd/system`), run as the user
`maktaba`. Two servers would contend for the same ports, and each would have its own database.
`check` reports it, and `service install` refuses with `service_taken` even with `--replace`: a user
cannot take over a root-owned unit, and it must not be stopped behind its owner's back. The message
names the unit and how to remove it (`sudo apt remove maktaba-server`, or `sudo systemctl disable
--now maktaba-server`), and the setup runs again once it is gone. Moving that server's data into the
app is a separate migration, not part of this setup.

## Where things are

The same per-user XDG paths as the desktop install ([README](README.md#shape)). The service is
`systemctl --user status maktaba-server`. The server's log is `~/.local/state/maktaba/logs/server.log`;
the launcher's own events are in `~/.local/share/maktaba/updates/launcher.log`.

## Updates

`owner: headless`. The launcher then leaves the API's update check on (no `MAKTABA_UPDATE_DISABLE`)
and tells the server the owner (`MAKTABA_RUNTIME_OWNER=headless`). Updates install a new runtime;
the running binaries are never replaced.

- **`maktaba-server update`** (by hand, `--check` to only verify, `--version` for one release) finds
  the release on GitHub, verifies it in process (the Sigstore bundle over `checksums.txt` from the
  release workflow of that tag, its Rekor inclusion proof, `release.json`), downloads the payload
  tarball into `runtime/.download-*` and checks it against `checksums.txt`, unpacks it, checks its
  payload.json names the same version, and runs `runtime install` then `runtime activate` (the
  service stopped while `current` moves). Older than the newest version this machine ran is refused.
- **Update now in the web app** (`POST /api/admin/system/update`) starts that same command as its own
  transient user unit (`systemd-run --user`, `maktaba-update-<time>`), since activation stops the
  service the API runs in, and answers 202. Its output is in `journalctl --user -u maktaba-update-*`.
- **Automatic updates** follow `rules.updates` (quiet window, minimum release age, pin, defer, skip)
  once Epic 35's orchestrator (35.4) installs by itself: it runs the same update. Until then the
  update check notifies, and an admin clicks Update now.
- **Back:** a new runtime whose service does not start is switched back at once (activation order).
  `maktaba-server update --rollback` switches to `previous`, the runtime that ran before, by hand.
  Switching back by itself when the new server starts but never confirms (Epic 35's 3-minute
  confirmation) comes with 38.12's next start (39.9). The database rolls back only through the
  pre-update dump, as on the Mac.

## Uninstall

`maktaba-server service uninstall` stops and removes the unit. The data stays in
`~/.local/share/maktaba` and `~/.config/maktaba` until it is deleted by hand.
