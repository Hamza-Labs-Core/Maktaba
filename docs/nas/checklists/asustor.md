---
platform: asustor
devices: an ASUSTOR with an Intel Celeron N5105 (AS5402T or AS6702T) on ADM 4.2 or later
status: draft
---

# ASUSTOR device checklist

Run before ASUSTOR's guide is first called tested, and after each ADM major. The install is
maktaba.tv/install/asustor: the compose file from /install/docker as a Portainer stack (Epic 59
story 59.11); the result goes in [results/](results/README.md). Screenshots are dark, in English
and Arabic, under `docs/nas/checklists/screenshots/asustor/`. Each step also confirms the guide's
wording of ADM's own menus; a difference is a bug in the guide.

### 1. The user and its numbers
- **Do:** Access Control → Local Users → Add `maktaba`, not an administrator; read its numbers with `id maktaba` over SSH.
- **See:** the guide's menu names match ADM; `id` prints the uid and gid.
- **Screenshots:** none

### 2. The data folder and the shares
- **Do:** create `maktaba` inside the `Docker` shared folder, give `maktaba` Read/Write on it, and Read only on one media folder.
- **See:** the paths are what the guide says (`/volume1/Docker/maktaba`).
- **Screenshots:** none

### 3. Deploy the stack
- **Do:** make the file at /install/docker with ASUSTOR chosen, install Portainer CE from App Central, then Stacks → Add stack, paste it and deploy.
- **See:** Portainer opens from ADM's menu at `https://<nas>:19943` (the guide's port, from App Central's page), its first password in `Docker/PortainerCE/portainer_password.txt`; the container waits for setup, saying how to run it.
- **Screenshots:** `asustor/03-stack.en.png`, `asustor/03-stack.ar.png`

### 4. Setup
- **Do:** run the guide's setup command over SSH, add a library.
- **See:** setup asks each folder's share name; the library scans.
- **Screenshots:** `asustor/04-setup.en.png`, `asustor/04-setup.ar.png`

### 5. Hardware video
- **Do:** turn on hardware video in the file (the render group from `stat -c %g /dev/dri/renderD128`), redeploy, and play an HEVC file in a browser.
- **See:** This machine says Video: hardware (iHD).
- **Screenshots:** `asustor/05-video.en.png`, `asustor/05-video.ar.png`

### 6. A folder Maktaba cannot read
- **Do:** remove `maktaba`'s permission from one media folder, then press Check access on its library.
- **See:** the library shows No access, and nothing in it is treated as deleted.
- **Screenshots:** `asustor/06-no-access.en.png`, `asustor/06-no-access.ar.png`

### 7. A port another app holds (59.08)
- **Do:** start another container on the host network that listens on 8088, then restart Maktaba with no port variables; then remove the `#` in front of `MAKTABA_WEB_PORT` in the `[ports]` block, set `8090`, and recreate the container.
- **See:** first, the log and Settings → This machine say 8088 moved to 8780 and name the holder; second, the web app opens at `:8090`, nothing is moved, and Settings → Storage & discovery shows the field locked. Remove the other container afterwards.
- **Screenshots:** `asustor/07-port-pinned.en.png`, `asustor/07-port-pinned.ar.png`

### 8. An update
- **Do:** with the release before the newest one installed, set the update window to the next hour (Manage → Updates) and wait through it; on another run, recreate the container with a newer image.
- **See:** both end on the new version, the pre-update backup is in the data folder's backups, and the container still runs as the user from step 1.
- **Screenshots:** `asustor/08-updated.en.png`, `asustor/08-updated.ar.png`

### 9. Logs and the setup report
- **Do:** save `docker logs maktaba` and copy the report from Settings → This machine.
- **See:** both are attached to the result file, with no password, token or pairing code in them.
- **Screenshots:** none

### 10. The guide's screenshots
- **Do:** with the NAS's UI in its dark theme where it has one, take each screenshot maktaba.tv/install/asustor shows as a placeholder, in English, and in Arabic where the NAS's UI has it.
- **See:** each file under `web/packages/install/site/screenshots/asustor/` as the placeholder names it; each placeholder figure then becomes the image.
- **Screenshots:** none
