---
platform: unraid
devices: an Unraid server (7.x) with a cache pool; a VM booted from an Unraid USB key will do
status: draft
---

# Unraid device checklist

Run before Unraid's first release and after each Unraid major. The install is the Community
Applications template (Epic 59 story 59.11); the result goes in [results/](results/README.md).
Screenshots are dark, in English and Arabic, under `docs/nas/checklists/screenshots/unraid/`.

### 1. Install from Community Applications
- **Do:** Apps → search Maktaba → Install. Keep `/var/lib/maktaba` on `/mnt/cache/appdata/maktaba`, add one media path `/mnt/user/Movies` at the same path.
- **See:** the template shows Network Type host and `--user 99:100`; the container starts.
- **Screenshots:** `unraid/01-template.en.png`, `unraid/01-template.ar.png`

### 2. The cache-pool data path
- **Do:** open `http://<server>:8088` and finish setup; check `/mnt/cache/appdata/maktaba` in the shell.
- **See:** the data is on the cache pool (not `/mnt/user`), owned by 99:100; setup did not refuse the data dir.
- **Screenshots:** `unraid/02-setup-done.en.png`, `unraid/02-setup-done.ar.png`

### 3. Data on the array is refused
- **Do:** edit the container to put `/var/lib/maktaba` on `/mnt/user/appdata/maktaba` (shfs), start it on an empty folder.
- **See:** setup refuses the data dir on shfs and says to use a cache pool or a single disk.
- **Screenshots:** `unraid/03-shfs-refused.en.png`, `unraid/03-shfs-refused.ar.png`

### 4. Media on shfs
- **Do:** scan the library on `/mnt/user/Movies` and play a video.
- **See:** every video is found and plays; This machine names the platform Unraid.
- **Screenshots:** `unraid/04-library.en.png`, `unraid/04-library.ar.png`

### 5. A share nobody (99) cannot read
- **Do:** add a library on a share with Export No and no access for nobody.
- **See:** No access, with Unraid's fix: Shares → {share}, set the share's export and check that user nobody (99) can read it.
- **Screenshots:** `unraid/05-no-access.en.png`, `unraid/05-no-access.ar.png`

### 6. Disk sleep
- **Do:** set the array disks' spin-down delay to 15 minutes; leave the server idle for an hour outside quiet hours with Maktaba running.
- **See:** Main shows the media disks spun down for most of the hour.
- **Screenshots:** none

### 7. An update
- **Do:** with an older image, apply the update Unraid offers on the Docker tab; on another run, let the self-update install a release in the window.
- **See:** both end on the new version; the container keeps running as 99:100.
- **Screenshots:** `unraid/07-updated.en.png`, `unraid/07-updated.ar.png`

### 8. The apps find the home (59.08)
- **Do:** with the default install (host network, no D-Bus socket mounted), open the Apple Player and the Android app on the same Wi-Fi and look for homes; then, on a computer, run `avahi-browse -art` (Linux) or `dns-sd -B _maktaba._tcp` (Mac).
- **See:** both apps list the home by itself, at `<nas>-maktaba.local`; Unraid's own name (`<nas>.local`) and its SMB shares still resolve and show, and the NAS was not renamed (no `-2` in its name). With the system D-Bus socket mounted as the guide's option, the home is listed at the NAS's own name instead.
- **Screenshots:** `unraid/08-discovered.en.png`, `unraid/08-discovered.ar.png`

### 9. Logs and the setup report
- **Do:** save `docker logs maktaba` and copy the report from Settings → This machine.
- **See:** both are attached to the result file, with no secret in them.
- **Screenshots:** none
