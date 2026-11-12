---
schema: 1
kind: pr
pr: 1164
title: "Epic 59: the NAS guides followed on Docker; setup's race, the reserved admin name, the data folder, Unraid's appdata"
date: 2026-11-11
session: c9b60789-2339-4101-a5d3-301d1abda815
agent: claude-code
---

## What happened
- The maktaba.tv NAS guides (docker, synology, truenas, unraid, qnap, ugreen, after-setup,
  troubleshooting, en+ar) were followed literally on NAS-shaped containers on Docker 29
  (linux/arm64):
  - inside a `docker:dind` "NAS" whose /volume1, /share, /mnt and /srv are ext4 volumes:
    - the plain compose;
    - Synology 1026:100;
    - the TrueNAS Custom App YAML (568:568, `:ro` media, `cap_drop: ALL`);
    - the Unraid template's `docker run` (99:100);
    - QNAP and UGREEN;
  - on Docker Desktop itself, the bridge block.
- `ghcr.io/hamza-labs-core/maktaba-server:latest` refuses an anonymous pull (`unauthorized`):
  the package is private. The image was built from `deploy/docker/server/Dockerfile` with the
  v0.3.128 arm64 payload. Each fix was then tested with this branch's `maktaba-server` overlaid
  on that payload (its `files.sha256` line and `payload.json`'s hash rewritten).
- #1164 fixed five product faults:
  - **setup's race:** the container started the server in the gap between setup's first answer
    (which writes `server.toml`) and the admin step (which writes `state/setup.json`). The
    server's initdb then made setup say "set up already", with no admin;
  - **the default admin name:** `admin` is migration 0029's sentinel account, so `adduser`
    failed at finish. The terminal and the wizard offered it as the default;
  - **the data folder in a container:** the storage step suggested the roomiest listed disk,
    which was a media mount (TrueNAS's read-only dataset, so the default failed five times);
  - **Unraid's appdata:** Docker makes it as root, so the container stopped at Apply;
  - **`bridge_network`** was missing from the components catalog.
- #1164 also corrected the guides' text: libraries are added after setup (Manage → Manage
  libraries); a new library's first scan waits for the quiet hours; setup needs the internet
  (about 2 GB plus the models); "Manage", not "Admin"; This machine shows no "runs as" user;
  Unraid's chown step; and the install page refuses a Docker volume for a uid other than 1000.
- Verified as written (on the fixed image):
  - non-root everywhere (`CapEff` 0), and the first container start works offline;
  - `:ro` libraries read Read only and refuse convert with the promised sentence;
  - `@eaDir` and `#recycle` are skipped;
  - a listener on 8080 moves the API to 8782, logged in both languages;
  - restart and kill-then-start recover;
  - the write-denied, root, privileged, Docker-socket and under-3.5-GiB refusals match
    Troubleshooting;
  - `<host>-maktaba.local` and `_maktaba._tcp` answer inside the NAS's network.

## What we learned
- The CI cells run setup before the server container exists, so they could not see the race.
  Every guide starts the container first. A harness should follow the household's order.
- A container's `/mnt`, `/srv` and `/media` mounts are the household's libraries, not disks
  to offer for data.
- A pexpect run of the terminal setup is a cheap way to follow a guide literally. Keep the log
  of every run, and save logs before removing a container (the Unraid run's container log was
  lost that way; its Maktaba logs were kept).
- On macOS, unpack a Linux payload on a case-sensitive volume: terminfo's `2621A` and `2621a`
  collide, and the image then refuses its own payload (`payload_invalid`).
- Docker Desktop's host network is the VM's: it neither reaches nor clashes with the Mac's
  ports. A published port on the Mac can sit beside another process's 127.0.0.1 listener, so
  check every port with `lsof` before publishing.

## Keep doing
- Follow each guide literally after any change to setup or the container entrypoint, from an
  empty data folder, with the container started before setup.

## Open
- **Owner:** make the GHCR package `maktaba-server` public. Every guide pulls it anonymously.
- After a restart the server rebuilds the pipeline venv in place while pipeline workers run.
  This gives transient `ModuleNotFoundError`s and runner exits with code 75. It recovers, but
  it should not rebuild under running workers.
- Setup downloads `large-v3-turbo` even on a Light NAS, where speech is off by default.
- Setup's step errors say "the details are in the log", but the terminal setup keeps no log.
- Only real hardware proves the NAS UIs, GPUs and VA-API, the NAS's own mDNS responder beside
  Maktaba's, Unraid's shfs refusal, the screenshots, and discovery from other devices.
