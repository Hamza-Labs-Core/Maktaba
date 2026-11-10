---
platform: truenas
devices: an N100 box on TrueNAS SCALE 25.04, then the same box on 25.10
status: draft
---

# TrueNAS device checklist

Run on TrueNAS 25.04 and again on 25.10, before TrueNAS's first release and after each TrueNAS
major. The install is the Maktaba app (Epic 59 story 59.10); the result goes in
[results/](results/README.md). Screenshots are dark, in English and Arabic, under
`docs/nas/checklists/screenshots/truenas/`.

## Install from the app

### 1. The datasets
- **Do:** create `apps/maktaba` on the SSD pool with the Apps preset, and a media dataset `tank/media` with a few videos.
- **See:** `apps/maktaba`'s ACL lists the `apps` user (568) with Modify.
- **Screenshots:** none

### 2. Install the app
- **Do:** Apps → Discover → Maktaba (or the custom catalog while the app is under review) → Install: data on `/mnt/<ssd>/apps/maktaba`, media `/mnt/tank/media` read-only, memory 8 GB, GPU Intel-AMD.
- **See:** the app deploys and goes Running; its workload runs as 568:568.
- **Screenshots:** `truenas/02-install.en.png`, `truenas/02-install.ar.png`

### 3. Finish setup in the browser
- **Do:** open the app's Web UI button (`http://<nas>:8088`), set up in English, then in a second browser profile in Arabic up to the first screen.
- **See:** Settings → This machine names the platform TrueNAS, tier Standard, memory 8 GB, the iHD driver on the N100's render device.
- **Screenshots:** `truenas/03-this-machine.en.png`, `truenas/03-this-machine.ar.png`

### 4. ARC counts as available memory
- **Do:** copy a few large files from the pool so ARC grows (the dashboard's ZFS cache), then reload This machine.
- **See:** available memory includes the ARC (close to the container's limit), not only MemAvailable.
- **Screenshots:** `truenas/04-arc.en.png`, `truenas/04-arc.ar.png`

## Permissions

### 5. A dataset Maktaba cannot read
- **Do:** add a library on a dataset with no ACL entry for 568.
- **See:** No access, with TrueNAS's fix: Datasets → {dataset} → Permissions, add an ACL entry for user 568 with Modify (or Read).
- **Screenshots:** `truenas/05-no-access.en.png`, `truenas/05-no-access.ar.png`

### 6. The ACL fix flow
- **Do:** add the entry with Read, then press Check access.
- **See:** the library turns Read only; a scan finds its videos; convert and delete are disabled with the reason.
- **Screenshots:** `truenas/06-read-only.en.png`, `truenas/06-read-only.ar.png`

### 7. ixVolume
- **Do:** install a second copy of the app with ixVolume for its data.
- **See:** the catalog's permissions step owns the volume for 568 before Maktaba starts; Maktaba starts with no write-denied exit.
- **Screenshots:** none

## Hardware and updates

### 8. VA-API on the N100
- **Do:** with the app's GPU setting passing the N100's `/dev/dri` (the render group added), open Settings → This machine, then play a 1080p HEVC file on a browser that needs a transcode; note the speed from the server log's `vod_transcode` lines.
- **See:** This machine says "Video: hardware (iHD), H.264 and HEVC"; playback is smooth and the transcode runs at 2× real time or faster.
- **Screenshots:** `truenas/08-vaapi.en.png`, `truenas/08-vaapi.ar.png`

### 9. Plugin isolation
- **Do:** Settings → This machine → plugin isolation.
- **See:** the level matches TrueNAS's kernel (Full with Landlock, Container without); write it in the result file.
- **Screenshots:** `truenas/09-isolation.en.png`, `truenas/09-isolation.ar.png`

### 10. A catalog update and a self-update
- **Do:** with an older app version installed, update the app from Apps; on another run, let the self-update install a release in the update window.
- **See:** both end on the new version; an older image never replaces a newer runtime; the pre-update dump is in backups.
- **Screenshots:** `truenas/10-updated.en.png`, `truenas/10-updated.ar.png`

### 11. The apps find the home (59.08)
- **Do:** with the default install (host network, no D-Bus socket mounted), open the Apple Player and the Android app on the same Wi-Fi and look for homes; then, on a computer, run `avahi-browse -art` (Linux) or `dns-sd -B _maktaba._tcp` (Mac).
- **See:** both apps list the home by itself, at `<nas>-maktaba.local`; TrueNAS's own name (`<nas>.local`) where 25.04+ still answers mDNS (note whether it runs a responder at all) still resolve and show, and the NAS was not renamed (no `-2` in its name). With the system D-Bus socket mounted as the guide's option, the home is listed at the NAS's own name instead.
- **Screenshots:** `truenas/11-discovered.en.png`, `truenas/11-discovered.ar.png`

### 12. Logs and the setup report
- **Do:** save the app's logs (Apps → Maktaba → Logs, or `docker logs` in the shell) and copy the report from This machine.
- **See:** both are attached to the result file, with no secret in them.
- **Screenshots:** none
