---
platform: synology
devices: a J4125 DiskStation with 6 GB or more (Quick Sync), a Ryzen DiskStation (no GPU for video)
status: draft
---

# Synology device checklist

Run on each device, before Synology's first release and after each DSM major. The install
follows the Synology guide on maktaba.tv (Epic 59 story 59.09); the result goes in
[results/](results/README.md). Screenshots are dark, in English and Arabic, under
`docs/nas/checklists/screenshots/synology/`.

## Install from the guide

### 1. Create the DSM user `maktaba`
- **Do:** Control Panel → User & Group → Create, name `maktaba`, no admin rights, no apps. Read its uid with Task Scheduler running `id maktaba` once.
- **See:** the user exists; the task's output shows a uid of 1026 or higher and group 100 (users).
- **Screenshots:** none

### 2. Create the data folder
- **Do:** create `docker/maktaba` on the SSD volume when there is one (else volume1), and give `maktaba` Read/Write on it.
- **See:** the folder's Permissions tab lists `maktaba` with Read/Write.
- **Screenshots:** none

### 3. Create the project from the guide's compose file
- **Do:** on maktaba.tv's install page pick Synology, the model (J4125 or other), the memory tier and the shares; copy the compose file. Container Manager → Project → Create, name `maktaba`, path `docker/maktaba`, paste it, build.
- **See:** the project builds with no warning; the container `maktaba` is Running as user 1026:100 (Container → maktaba → Details).
- **Screenshots:** `synology/03-project.en.png`, `synology/03-project.ar.png`

### 4. Finish setup in the browser
- **Do:** open `http://<nas>:8088`, set up in English, then again in a second browser profile in Arabic up to the first screen.
- **See:** setup ends on the home page; Settings → This machine names the platform DSM, the tier (Light below 6 GB, Standard from 6 GB) and the memory limit of the compose file.
- **Screenshots:** `synology/04-this-machine.en.png`, `synology/04-this-machine.ar.png`

## Permissions

### 5. A share Maktaba cannot read
- **Do:** add a library on a share `maktaba` has no permission on.
- **See:** the library shows No access with DSM's fix: Control Panel → Shared Folder → {share} → Edit → Permissions, give the user `maktaba` Read/Write (or Read only).
- **Screenshots:** `synology/05-no-access.en.png`, `synology/05-no-access.ar.png`

### 6. The ACL fix flow
- **Do:** follow the fix text exactly (Read only), then press Check access on the library page.
- **See:** the library turns Read only without being added again; a scan finds its videos; convert and delete are disabled with "This library is read-only for Maktaba".
- **Screenshots:** `synology/06-read-only.en.png`, `synology/06-read-only.ar.png`

### 7. Subtitles on a read-only library
- **Do:** let subtitles run for one video of the read-only library; play it in the web player.
- **See:** the subtitle track plays; nothing new appears in the share (File Station shows no `.srt` and no `.maktaba` folder there).
- **Screenshots:** `synology/07-subtitles.en.png`, `synology/07-subtitles.ar.png`

## Hardware

### 8. Quick Sync on the J4125
- **Do:** J4125 only: play an HEVC file on a browser that needs a transcode; open Settings → This machine.
- **See:** video uses the GPU (QSV or VA-API, iHD driver); the playback's transcode is hardware; CPU stays under half in Resource Monitor.
- **Screenshots:** `synology/08-quick-sync.en.png`, `synology/08-quick-sync.ar.png`

### 9. Software video on the Ryzen
- **Do:** Ryzen model only: open Settings → This machine and play the same HEVC file.
- **See:** This machine says video is software-only and direct play matters; the transcode is limited to the tier's size.
- **Screenshots:** `synology/09-software-only.en.png`, `synology/09-software-only.ar.png`

### 10. The DSM kernel's Landlock result
- **Do:** Settings → This machine → plugin isolation; note the kernel version (`uname -r` in Task Scheduler).
- **See:** the isolation level matches the kernel: Container on a kernel without Landlock (4.4, 5.10), and third-party plugins held until an admin allows them. Write the kernel and the level in the result file.
- **Screenshots:** `synology/10-isolation.en.png`, `synology/10-isolation.ar.png`

## Disks and updates

### 11. Disk sleep
- **Do:** set the media drives' hibernation to 10 minutes (Control Panel → Hardware & Power → HDD Hibernation, enable its log); leave the NAS idle outside quiet hours for an hour with Maktaba running and no playback.
- **See:** the hibernation log shows the media drives asleep for most of the hour; nothing in Maktaba's log touched them.
- **Screenshots:** none

### 12. A real update in the window
- **Do:** set the update window to the next hour (Settings → Updates); install the release before the newest one, then wait through the window.
- **See:** the home updates inside the window, the pre-update dump is in backups, the container keeps running as 1026:100, and Settings shows the new version.
- **Screenshots:** `synology/12-updated.en.png`, `synology/12-updated.ar.png`

### 13. Logs and the setup report
- **Do:** run `docker logs maktaba` (Task Scheduler, output to a file) and copy the report from Settings → This machine.
- **See:** both are attached to the result file, with no password, token or pairing code in them.
- **Screenshots:** none
