---
platform: qnap
devices: a QNAP with an Intel Celeron (TS-464 or TS-264: Quick Sync) on QTS 5.1 or later; a QuTS hero 5.1 model where one is at hand
status: draft
---

# QNAP device checklist

Run before the QNAP guide drops its "not yet tested" notice, and after each QTS major. The install is
maktaba.tv/install/qnap: the compose file from /install/docker in Container Station (Epic 59
story 59.11); the result goes in [results/](results/README.md). Screenshots are dark, in English
and Arabic, under `docs/nas/checklists/screenshots/qnap/`. Each step also confirms the guide's
wording of QNAP's own menus; a difference is a bug in the guide.

### 1. The user and its numbers
- **Do:** Control Panel → Privilege → Users → Create a User `maktaba`, not an administrator; read its numbers with `id maktaba` over SSH, as the guide says.
- **See:** the guide's menu names match QTS; `id` prints the uid and gid the guide's example has the shape of.
- **Screenshots:** none

### 2. The data folder and the shares
- **Do:** create `maktaba` inside the `Container` shared folder, give `maktaba` Read/Write on it, and Read only on one media share.
- **See:** the folder's path is what the guide says (`/share/Container/maktaba`); the media share is at `/share/<name>` or `/share/CACHEDEV1_DATA/<name>`.
- **Screenshots:** none

### 3. Create the application
- **Do:** make the file at /install/docker with QNAP chosen, then Container Station → Applications → Create, paste it, Create.
- **See:** the file says `MAKTABA_PLATFORM: qts`; Container Station downloads the image and the container waits for setup, saying how to run it.
- **Screenshots:** `qnap/03-application.en.png`, `qnap/03-application.ar.png`

### 4. Setup and the shares
- **Do:** run the guide's setup command over SSH, add a library.
- **See:** setup's share step offers each `/share/<name>` folder as the share `<name>`; Manage → Machines → This NAS's shares lists them; This machine says QNAP.
- **Screenshots:** `qnap/04-setup.en.png`, `qnap/04-setup.ar.png`

### 5. QTS keeps 8080
- **Do:** with the host network, look at the log and This machine after setup.
- **See:** the API port moved from 8080 (QTS's own) to a port from 8780 to 8790, and This machine says which program holds 8080 when QTS lets Maktaba see it.
- **Screenshots:** `qnap/05-port-moved.en.png`, `qnap/05-port-moved.ar.png`

### 6. Hardware video
- **Do:** on the Celeron model, turn on hardware video in the file (the render group from `stat -c %g /dev/dri/renderD128`), recreate, and play an HEVC file in a browser.
- **See:** This machine says Video: hardware (iHD); the conversion uses the GPU.
- **Screenshots:** `qnap/06-video.en.png`, `qnap/06-video.ar.png`

### 7. A share Maktaba cannot read
- **Do:** remove `maktaba`'s permission from one media share, then press Check access on its library.
- **See:** the library shows No access with QNAP's own fix, and nothing in it is treated as deleted.
- **Screenshots:** `qnap/07-no-access.en.png`, `qnap/07-no-access.ar.png`

### 8. A port another app holds (59.08)
- **Do:** start another container on the host network that listens on 8088, then restart Maktaba with no port variables; then remove the `#` in front of `MAKTABA_WEB_PORT` in the `[ports]` block, set `8090`, and recreate the container.
- **See:** first, the log and Settings → This machine say 8088 moved to 8780 and name the holder; second, the web app opens at `:8090`, nothing is moved, and Settings → Storage & discovery shows the field locked. Remove the other container afterwards.
- **Screenshots:** `qnap/08-port-pinned.en.png`, `qnap/08-port-pinned.ar.png`

### 9. An update
- **Do:** with the release before the newest one installed, set the update window to the next hour (Manage → Updates) and wait through it; on another run, recreate the container with a newer image.
- **See:** both end on the new version, the pre-update backup is in the data folder's backups, and the container still runs as the user from step 1.
- **Screenshots:** `qnap/09-updated.en.png`, `qnap/09-updated.ar.png`

### 10. Logs and the setup report
- **Do:** save `docker logs maktaba` and copy the report from Settings → This machine.
- **See:** both are attached to the result file, with no password, token or pairing code in them.
- **Screenshots:** none

### 11. The guide's screenshots
- **Do:** with the NAS's UI in its dark theme where it has one, take each screenshot maktaba.tv/install/qnap shows as a placeholder, in English, and in Arabic where the NAS's UI has it.
- **See:** each file under `web/packages/install/site/screenshots/qnap/` as the placeholder names it; each placeholder figure then becomes the image.
- **Screenshots:** none
