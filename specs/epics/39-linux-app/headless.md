# Epic 39 — Maktaba on a Linux machine without a desktop (stories 39.6, 39.9, 39.10)

A NAS, a mini PC or a VM runs the same payload as the desktop app, with no window. The setup asks
the desktop's questions in the terminal, through the same `setup --json` steps.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/Hamza-Labs-Core/Maktaba/main/deploy/packaging/install.sh | bash -s -- --app
```

`install.sh --app` (39.10):

1. picks `maktaba-server-<v>-linux-<x86_64|aarch64>.tar.gz` from the release;
2. checks it against `checksums.txt` and that file's Sigstore bundle. cosign is used when it is
   installed. Without cosign, `maktaba-server` verifies the bundle itself as soon as it is unpacked
   (`releaseverify`, the same code as Epic 35's updates) before anything is installed;
3. unpacks it into a temporary directory and runs its `bin/maktaba-server setup`.

`maktaba-server setup` (on a machine that has a payload and no server.toml) runs the steps in screen
order. It shows each step's `progress` as a line and each `error` with its help text, and asks for
the input fields:

| Step | In the terminal |
|---|---|
| `check` | what will run, the disk, RAM and GPU, the warnings; stops when `ok` is false |
| `admin` | user name and password (asked twice, not echoed) |
| `libraries` | library folders, then the NAS path map (`/mnt/media => /srv/media`), each checked |
| `models` | Hugging Face token (not echoed), or the path of the home's copy on the share |
| `plex` | optional: the PIN to enter at plex.tv/link, polled until linked |
| `venv` | installs the runtime (`runtime install --owner headless`), then builds the venv with a progress bar |
| `finish` | writes server.toml, makes the database, then `service install` and `service linger` (polkit asks for the admin password over SSH) |

Non-interactive (automation, CI): `maktaba-server setup --answers answers.json` gives each step its
input from the file (`{"admin": {...}, "libraries": {...}, ...}`). Secrets can come from
`--answers-fd N` instead of a file on disk.

## Where things are

The same per-user XDG paths as the desktop install ([README](README.md#shape)). The service is
`systemctl --user status maktaba-server`. The log is `~/.local/state/maktaba/logs/server.log`.

## Updates

`owner: headless`. The machine updates itself the way Epic 35 machines do:

- **Automatically:** the API's update check and `rules.updates` (quiet window, minimum release age,
  pin, defer, skip; on a cluster, the home first, then the machines one by one). The release's
  `release.json` lists the payload tarball for each Linux platform. The install step fetches it,
  verifies it against the signed `checksums.txt` (and the watermark: never a downgrade), and runs
  `runtime install`. The launcher then exits 75, and systemd restarts the unit into the new
  `current`. The supervisor's next start takes the backup, migrates, and keeps or rebuilds the venv.
- **By hand:** `maktaba-server update` (check, then install), `maktaba-server update --check`.

A payload that does not come up (the supervisor's 3-minute confirmation fails, as in Epic 35) is
rolled back: `current` goes back to the previous directory. The database rolls back only through
the pre-update dump, as on the Mac.

## Uninstall

`maktaba-server service uninstall` stops and removes the unit. The data stays in
`~/.local/share/maktaba` and `~/.config/maktaba` until it is deleted by hand.
