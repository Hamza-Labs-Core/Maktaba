# Join a machine to the cluster

This page adds a computer — macOS, Linux or Windows — to your Maktaba cluster as a
**machine** that pulls work from the home's queues. It takes about fifteen minutes. You need
to be an admin on the home.

In short:

1. On the home, allow new machines.
2. On the machine, install the Maktaba pipeline.
3. Mount the NAS share on the machine.
4. Run `maktaba-pipeline join` on the machine; it shows a 6-digit code.
5. Type the code on the home to approve the machine.
6. Start the worker, then set it up to run as a service.

> **About the command.** These pages write the pipeline's command as `maktaba-pipeline`:
> the node installer and the release's wheel both install it. Only if you run the pipeline
> from a git checkout without installing it, type `uv run python -m maktaba_pipeline` in the
> `pipeline` folder wherever you see `maktaba-pipeline` (for example
> `uv run python -m maktaba_pipeline join`).

## Before you start

- The home is running and you can sign in to its web app as an admin.
- The home uses **PostgreSQL** (a home running on SQLite cannot take machines; the Machines
  page says so).
- The machine is on the same local network as the home and can reach it on ports **8080**
  and **5432** (see [Troubleshooting → Firewall ports](troubleshooting.md#firewall-ports)).
- You know your NAS share's address (for example `\\nas\originals` or `nas.local:/volume1/originals`)
  and have an account that can read the videos and write to the share.

## Step 1 — On the home: allow new machines

1. Open the home's web app and go to **Admin > Machines**.
2. Turn on **Accept new machines**.

It stays on for one hour and then turns itself off. Machines can only ask to join while it
is on.

## Step 2 — On the machine: install the pipeline

The machine needs **uv** (the Python package tool; it fetches the right Python by itself)
and **ffmpeg**. It does **not** need git or a copy of the source code: every Maktaba release
publishes the pipeline as a package (a wheel) and a node installer that installs it. Use
the release your home runs: a machine and its home run the same version. A machine whose
pipeline is newer than the home's database (or older than a breaking migration allows)
pauses: it takes no work until the two match again, and the Machines page says which one to
update.

```sh
brew install uv ffmpeg                                # macOS (Homebrew: https://brew.sh)
sudo apt install -y ffmpeg curl                       # Linux (Debian, Ubuntu), then:
curl -LsSf https://astral.sh/uv/install.sh | sh       #   uv for your user
```

```powershell
winget install --id astral-sh.uv                      # Windows; close and reopen PowerShell after
winget install --id Gyan.FFmpeg
```

### The easy way: the node installer

Download the release's node archive and run its installer:

```sh
v=1.4.0     # your home's version (`maktaba-server --version` on the home)
curl -fsSLO "https://github.com/Hamza-Labs-Core/Maktaba/releases/download/v$v/maktaba-node-$v.tar.gz"
tar -xzf "maktaba-node-$v.tar.gz"
"./maktaba-node-$v/install-node.sh"                   # macOS, Linux
```

```powershell
$v = '1.4.0'
Invoke-WebRequest "https://github.com/Hamza-Labs-Core/Maktaba/releases/download/v$v/maktaba-node-$v.zip" -OutFile "maktaba-node-$v.zip" -UseBasicParsing
Expand-Archive "maktaba-node-$v.zip" -DestinationPath .
powershell -ExecutionPolicy Bypass -File ".\maktaba-node-$v\windows\install-node.ps1"
```

It installs the release's pipeline in its own environment (dependencies pinned to the
versions the release was tested with), then does **Steps 4, 5 and 7 for you**: it runs
`join` (approve the code on the home when it shows it) and registers the worker as a
service. Mount the share (Step 3) before or after; until the share is there, the worker
leaves the work that needs it. Its options (`--home`, `--name`, extra workers, a system
service on Linux, ...) are in [deploy/packaging/node](../../deploy/packaging/node/README.md).

To check the archive before you unpack it, download the release's `checksums.txt`,
`checksums.txt.sig` and `checksums.txt.pem` next to it and run (cosign:
https://docs.sigstore.dev/cosign/system_config/installation/):

```sh
cosign verify-blob --certificate checksums.txt.pem --signature checksums.txt.sig \
  --certificate-identity-regexp '^https://github.com/Hamza-Labs-Core/Maktaba/\.github/workflows/release\.yml@' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com checksums.txt
grep " \./maktaba-node-$v\.tar\.gz$" checksums.txt | shasum -a 256 -c   # Linux: or sha256sum -c
```

The installer then checks the wheel and constraints inside the archive against the
digests the release wrote into its `release.conf`.

### By hand: the wheel

To install only the `maktaba-pipeline` command and run the remaining steps yourself,
download the release's wheel and its constraints file, and install them with uv:

```sh
v=1.4.0
base="https://github.com/Hamza-Labs-Core/Maktaba/releases/download/v$v"
curl -fsSLO "$base/maktaba_pipeline-$v-py3-none-any.whl"
curl -fsSLO "$base/maktaba-pipeline-$v-constraints.txt"
uv tool install --python 3.12 --constraints "maktaba-pipeline-$v-constraints.txt" \
    "./maktaba_pipeline-$v-py3-none-any.whl"          # Apple Silicon: ...whl[stt]  (transcription)
maktaba-pipeline --version
```

On Windows the same `uv tool install` line works in PowerShell (download with
`Invoke-WebRequest`). `--python 3.12` matters: the pinned dependencies have ready-made
packages for the release's Python, and a newer Python would try to compile some of them.

### From the source code (a git checkout)

For development, or a release older than the node archives:

```sh
git clone https://github.com/Hamza-Labs-Core/Maktaba.git ~/Maktaba
cd ~/Maktaba/pipeline
uv sync                      # on an Apple Silicon Mac add: --extra stt   (transcription)
```

On Windows: `winget install --id Git.Git` first, then the same in PowerShell
(`$HOME\Maktaba`). The checkout's `deploy/packaging/node/install-node.sh` (Windows:
`deploy\packaging\node\windows\install-node.ps1`) is the same installer; from a checkout
it builds the checkout's `pipeline/`.

Optional extras: `--extra stt` (transcription, Apple Silicon Macs only), `--extra search`
(the semantic search embedder); with the node installer, `--extras stt,search`. A machine
without an extra simply does not pull the queues that need it.

If ffmpeg is not on the PATH, set `MAKTABA_FFMPEG` to its full path.

## Step 3 — On the machine: mount the NAS share

The machine must see the same videos as the home. It works out by itself where the share is
mounted (it looks for known videos), so mount it in the usual place for your system.

### macOS

1. In Finder choose **Go > Connect to Server…** and enter the share, for example
   `smb://nas.local/originals`. It appears under `/Volumes/originals`.
2. To mount it at every login: **System Settings > General > Login Items**, add the share.

### Linux

```sh
sudo mkdir -p /mnt/nas

# NFS (install nfs-common first):
echo 'nas.local:/volume1/originals  /mnt/nas  nfs  defaults,_netdev  0 0' | sudo tee -a /etc/fstab

# or SMB (install cifs-utils first; put username= and password= in /etc/nas-credentials, mode 600):
echo '//nas.local/originals  /mnt/nas  cifs  credentials=/etc/nas-credentials,uid=YOUR_USER,_netdev  0 0' | sudo tee -a /etc/fstab

sudo mount -a
ls /mnt/nas        # you should see your video folders
```

Use the user that will run the worker in `uid=`.

### Windows

- For a worker you start yourself: in File Explorer, **This PC > Map network drive**, pick a
  letter (for example `Y:`), enter `\\nas\originals`, tick **Reconnect at sign-in**.
- For a worker that runs as a service (Step 7): services cannot see mapped drive letters.
  Use the share's network path `\\nas\originals` instead, and set the machine's path map to
  it (Step 6).

The machine needs to **read** the videos and **write** to the share (subtitles are saved
next to the videos, and every machine saves its files in the share's `maktaba` folder).

## Step 4 — On the machine: ask to join

```sh
maktaba-pipeline join
```

It looks for the home on the network for a few seconds, asks it to take this machine, and
shows a pairing code:

```
============================================
  Pairing code:   482 913
============================================

On the home, open Admin > Machines, find this machine (studio-linux) and enter this code.
Waiting for approval (up to 15 minutes)...
```

Options:

- `--home http://192.168.50.20:8080` — the home's address, when it is not found
  automatically (networks that block multicast; see
  [Troubleshooting](troubleshooting.md#no-maktaba-home-found-on-this-network)).
- `--name "Studio PC"` — the name shown on the home (default: the computer's name).
- `--run` — start working as soon as you approve (Step 6).

If several homes answer, it lists them and asks which one to join.

## Step 5 — On the home: approve with the code

1. On **Admin > Machines**, under **Asking to join**, find the machine by its name.
2. Type the code the machine shows, and choose **Approve**.

The code proves you are approving the very machine in front of you. A wrong code is
refused; after five wrong codes the request is rejected and you start again from Step 4. A
request expires after 15 minutes.

On the machine, `join` prints `Approved. This machine joined …; settings saved to …` and
stops (or starts working, with `--run`). The settings file holds this machine's identity,
the home's public key (so the machine can tell its home from an impostor) and its database
login, readable only by your user:

| System | Settings file |
|---|---|
| macOS | `~/Library/Application Support/Maktaba/node.json` |
| Linux | `~/.config/maktaba/node.json` |
| Windows | `%APPDATA%\Maktaba\node.json` |

## Step 6 — Start working and check

```sh
maktaba-pipeline run
```

With no other options, it runs as the machine that joined. Within a few seconds, on the
home's **Admin > Machines**, the machine shows:

- **Online**, with its system (CPU, memory, GPU);
- **Shared storage**: read and write checks — both should be *ok*;
- **Path map**: how the home's video paths translate to this machine's
  (for example `/Volumes/Originals/` → `/mnt/nas/`). It is worked out automatically; if it is
  wrong, choose **Edit path map** and set *path on the home* → *path on this machine*;
- the queues it pulls from, and why any others are off.

On the machine, `maktaba-pipeline cluster where` prints where it saves its files on the
share, and says so if it cannot reach the share.

## Step 7 — Run it as a service

So the worker starts with the computer and comes back after a crash. Two rules for every
system:

- **Same user, or tell it where the settings are.** `join` saved the settings file for the
  user who ran it. Run the service as that user, or set `MAKTABA_NODE_CONFIG` in the
  service to the settings file's full path (and make sure the service's user can read it).
- **The NAS first.** The service must start after the share is mounted.

**The easy way: the node installer** ([Step 2](#the-easy-way-the-node-installer); if you
installed with it, this step is done). From the release's node archive, it installs the
pipeline, joins the home if the machine has not joined yet, and registers the worker as a
service for your system (launchd on macOS, systemd on Linux, a scheduled task or an NSSM
service on Windows), with extra workers if you want them:

```sh
./maktaba-node-1.4.0/install-node.sh                      # macOS, Linux
```

```powershell
.\maktaba-node-1.4.0\windows\install-node.ps1              # Windows
```

(From a git checkout: `deploy/packaging/node/install-node.sh`,
`deploy\packaging\node\windows\install-node.ps1`.)

See [deploy/packaging/node](../../deploy/packaging/node/README.md) for its options, logs,
updates and removal. The examples below do the same by hand.

The examples use `maktaba-pipeline` as installed for your user (`~/.local/bin`, where
`uv tool install` puts it). From a source copy, use `uv --directory /path/to/Maktaba/pipeline run maktaba-pipeline run --node`
as the command. `--node` makes the worker refuse to start (exit 78) until the machine has
joined, instead of running as a home worker on an empty database.

### macOS (launchd)

Save as `~/Library/LaunchAgents/com.maktaba.worker.plist` (replace `you`):

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>com.maktaba.worker</string>
  <key>ProgramArguments</key>
  <array>
    <string>/Users/you/.local/bin/maktaba-pipeline</string>
    <string>run</string>
    <string>--node</string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict>
  <key>StandardOutPath</key><string>/Users/you/Library/Logs/maktaba-worker.log</string>
  <key>StandardErrorPath</key><string>/Users/you/Library/Logs/maktaba-worker.log</string>
</dict>
</plist>
```

```sh
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.maktaba.worker.plist
```

It runs while you are logged in, which is also when your Finder-mounted share is there.

### Linux (systemd)

Save as `/etc/systemd/system/maktaba-worker.service` (replace `you` and the mount point):

```ini
[Unit]
Description=Maktaba worker
After=network-online.target remote-fs.target
Wants=network-online.target
RequiresMountsFor=/mnt/nas

[Service]
User=you
ExecStart=/home/you/.local/bin/maktaba-pipeline run --node
Restart=on-failure
RestartSec=10
RestartPreventExitStatus=77

[Install]
WantedBy=multi-user.target
```

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now maktaba-worker
journalctl -u maktaba-worker -f      # watch it start
```

### Windows (Task Scheduler)

Simplest — start at sign-in (mapped drive letters work):

```powershell
schtasks /Create /TN "Maktaba worker" /SC ONLOGON /RL LIMITED `
  /TR "`"$env:USERPROFILE\.local\bin\maktaba-pipeline.exe`" run --node"
```

Without signing in — start with Windows (use `\\nas\share` paths, not drive letters):

```powershell
schtasks /Create /TN "Maktaba worker" /SC ONSTART /RU "$env:USERDOMAIN\$env:USERNAME" /RP * `
  /TR "`"$env:USERPROFILE\.local\bin\maktaba-pipeline.exe`" run --node"
```

Then open **Task Scheduler**, find *Maktaba worker*, and on its **Settings** tab tick **If
the task fails, restart every 1 minute**, and untick **Stop the task if it runs longer
than**.

A worker stops for good when you revoke its machine — the home's sealed revocation on the
live connection, or its database, says so (a bare closed connection never does): it exits
with code 77. The
installer's services and the systemd example (`RestartPreventExitStatus=77`) do not restart
it then; the hand-made launchd and Task Scheduler entries above do, and keep being refused —
unload them when you remove the machine.

## Leaving the cluster

On the home, **Admin > Machines**:

- **Drain** — the machine finishes what it is doing and takes nothing new. Turn it off to
  resume.
- **Revoke** — the machine loses access at once: its database login stops, its live
  connection is closed, its unfinished jobs go back to the queue, and its worker stops. The
  record stays.
- **Remove** — deletes a revoked or rejected machine's record. After removing it, the same
  computer can ask to join again from Step 4.

To make a computer forget the cluster entirely, stop its worker and delete its settings
file (Step 5's table).
