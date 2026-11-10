---
platform: truenas
devices: an Intel N100 box and a CPU-only box, each on TrueNAS 25.04, then on 25.10 (an NVIDIA card where one is at hand)
status: draft
---

# TrueNAS device checklist

Run on TrueNAS 25.04 (Fangtooth) and again on 25.10 (Goldeye), on the N100 box and on a CPU-only
box, before the app goes to TrueNAS's catalog (Epic 59 README Q2) and after each TrueNAS major.
The install is story 59.10's app (`deploy/nas/truenas`) and its Custom App YAML; the result goes
in [results/](results/README.md) with the logs. Screenshots are dark, in English and Arabic,
under `docs/nas/checklists/screenshots/truenas/`. The step's screenshots are of Maktaba; the
guide's screenshots of TrueNAS's own screens are taken in the same run (step 13).

## Install from the app

### 1. The datasets
- **Do:** Datasets → Add Dataset `apps/maktaba` on the SSD pool (the main pool on a box without one) with the preset **Apps**; a media dataset `tank/media` with a few videos, one 1080p HEVC; a second media dataset `tank/private` with no ACL entry for 568.
- **See:** `apps/maktaba`'s ACL lists the `apps` user (568) with Modify; `tank/private`'s does not list it.
- **Screenshots:** none

### 2. Install the app
- **Do:** Apps → Discover → Maktaba (until it is in the catalog: Apps → Discover → ⋮ → Install via YAML with `maktaba-truenas-custom-app.yaml`, step 7) → Install with the defaults, data host path `/mnt/<ssd>/apps/maktaba`, media `/mnt/tank/media` read-only, GPU: allocate the Intel GPU on the N100.
- **See:** the app goes Deploying, then Running (healthy while it waits for setup); its notes say how to run the setup; Workloads shows the container as 568:568.
- **Screenshots:** none

### 3. Setup from the app's Shell
- **Do:** Apps → Installed → Maktaba → Workloads → maktaba → Shell; run `/opt/maktaba/server/bin/maktaba-server container setup`; answer in English. Then open the app's Web UI button (`http://<nas>:8088`) and, in a second browser profile, sign in in Arabic.
- **See:** the setup finishes; within a few minutes the web app answers and the app stays Running and healthy.
- **Screenshots:** `truenas/03-ready.en.png`, `truenas/03-ready.ar.png`

### 4. This machine
- **Do:** Settings → This machine.
- **See:** platform TrueNAS, tier Standard from the app's 8 GB memory limit, the iHD driver on the N100's render device (none on the CPU-only box); available memory counts the ZFS ARC.
- **Screenshots:** `truenas/04-this-machine.en.png`, `truenas/04-this-machine.ar.png`

### 5. ARC counts as available memory
- **Do:** copy a few large files on the pool so the ARC grows (Dashboard → Memory: ZFS cache), then reload This machine.
- **See:** available memory stays close to the app's limit (ARC counted), and the tier does not drop.
- **Screenshots:** `truenas/05-arc.en.png`, `truenas/05-arc.ar.png`

### 6. Discovery on the host network
- **Do:** open the Maktaba app on a phone and a TV on the same network.
- **See:** both find the home by itself (mDNS); TrueNAS's own name (`truenas.local`) still resolves.
- **Screenshots:** none

### 7. The Custom App YAML
- **Do:** on a second run, install `maktaba-truenas-custom-app.yaml` from the release page as a Custom App, after changing its CHANGE lines; set up as in step 3.
- **See:** the same result as steps 2 to 4.
- **Screenshots:** none

## Permissions

### 8. A dataset Maktaba cannot read
- **Do:** add `/mnt/tank/private` to the app's media folders (Edit → Media Folders), then add it as a library.
- **See:** the app still deploys; the library shows No access with TrueNAS's fix: Datasets → tank/private → Permissions, add an ACL entry for user 568 with Modify (or Read).
- **Screenshots:** `truenas/08-no-access.en.png`, `truenas/08-no-access.ar.png`

### 9. The ACL fix flow
- **Do:** add the entry with Read, Save Access Control List, then press Check access.
- **See:** the library turns Read only; a scan finds its videos; convert and delete are disabled with the reason.
- **Screenshots:** `truenas/09-read-only.en.png`, `truenas/09-read-only.ar.png`

### 10. ixVolume and a data folder Maktaba cannot write
- **Do:** install a second copy with ixVolume for its data; then a third with a host-path data dataset made without the Apps preset.
- **See:** the ixVolume copy: the permissions step owns the volume for 568 and Maktaba starts. The third stops at once; its logs say "Edit the dataset's permissions and add an ACL entry for user 568 with Modify, or use the Apps preset."
- **Screenshots:** none

## Hardware and updates

### 11. VA-API on the N100, NVIDIA on 25.10
- **Do:** play the 1080p HEVC file in a browser that needs a transcode; note the speed from the server log's `vod_transcode` lines. Where an NVIDIA card is at hand: Apps → Configuration → Install NVIDIA Drivers, select the card in the app's GPU setting, play again.
- **See:** This machine says "Video: hardware (iHD), H.264 and HEVC" and the transcode runs at 2× real time or faster. NVIDIA: NVENC used, speech on the Strong tier uses CUDA; a Pascal or older card on 25.10 falls back to the CPU with the reason shown.
- **Screenshots:** `truenas/11-video.en.png`, `truenas/11-video.ar.png`

### 12. Plugin isolation
- **Do:** Settings → This machine → plugin isolation.
- **See:** the level matches TrueNAS's kernel (Full with Landlock, Container without); write it in the result file.
- **Screenshots:** `truenas/12-isolation.en.png`, `truenas/12-isolation.ar.png`

### 13. The guide's TrueNAS screenshots
- **Do:** with TrueNAS's UI in dark mode, take each screenshot the maktaba.tv guide lists as a placeholder (`docs/nas/guides/README.md`): the dataset with the Apps preset, the ACL editor's entry for 568, the app's install form (storage, network, resources and GPU), the app's Shell with the setup command, Install via YAML; in English, and in Arabic where TrueNAS's UI has it.
- **See:** each file at the path the guide names; the guide then shows them instead of its placeholders.
- **Screenshots:** none

### 14. A catalog update and a self-update
- **Do:** with an older app version installed, update the app from Apps; on another run, let the self-update install a release in the update window.
- **See:** both end on the new version; an older image never replaces a newer runtime; the pre-update dump is in backups.
- **Screenshots:** `truenas/14-updated.en.png`, `truenas/14-updated.ar.png`

### 15. Logs and the setup report
- **Do:** save the app's logs (Apps → Maktaba → Logs, or `docker logs` in the shell) and copy the report from This machine.
- **See:** both are attached to the result file, with no secret in them.
- **Screenshots:** none
