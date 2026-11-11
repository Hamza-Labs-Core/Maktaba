---
platform: synology
devices: DS425+ (J4125, 6 GB: Quick Sync), DS923+ (Ryzen R1600, 4 GB: no GPU for video), DS223 (Realtek RTD1619B, ARM, 2 GB: storage only)
status: draft
---

# Synology device checklist

Run on each device, before Synology's first release and after each DSM major (7.2 → 7.3). The
install follows the Synology guide on maktaba.tv (`/install/synology`, Epic 59 story 59.09)
with the project file from its install page; the result goes in [results/](results/README.md),
one file per device. Screenshots are dark, in English and Arabic, under
`docs/nas/checklists/screenshots/synology/`. Steps 1 to 16 run on the DS425+; the steps
marked for another model run on that one.

The `nas-shapes` cells `dsm`, `synology-compose` and `dsm-data-denied` (tests/nas) already
check in CI what a container can imitate: the DSM user 1026:100, the compose file on Docker
24.0.2, DSM's folders skipped, the fixes in DSM's words, no Landlock. This list is what only a
DiskStation shows: Container Manager's screens, DSM's permission dialogs, real Quick Sync, the
kernel's cgroup and Landlock, disk sleep.

## Install from the guide (DS425+)

### 1. Create the DSM user `maktaba`
- **Do:** start a timer. Control Panel → User & Group → Create, name `maktaba`, no admin rights, no apps. Read its uid with Task Scheduler running `id maktaba` once (output to a file in a share).
- **See:** the user exists; the task's output shows a uid of 1026 or higher and group 100 (users).
- **Screenshots:** none

### 2. Create the data folder
- **Do:** create `docker/maktaba` on the SSD volume when there is one (else volume1), and give `maktaba` Read/Write on it (File Station → docker → maktaba → Properties → Permission).
- **See:** the folder's Permission tab lists `maktaba` with Read/Write.
- **Screenshots:** none

### 3. Give the user its shares
- **Do:** Control Panel → Shared Folder → Movies → Edit → Permissions: `maktaba` Read only. Recordings: Read/Write.
- **See:** both shares list `maktaba` with those rights; no other share does.
- **Screenshots:** none

### 4. Create the project from the guide's compose file
- **Do:** on maktaba.tv/install/synology pick the model (J4125), the memory (6 GB), the data volume, the two shares (Movies read only), the time zone and the NAS's address; copy the J4125 file. Container Manager → Project → Create, name `maktaba`, path `docker/maktaba`, source "Create docker-compose.yml", paste it, Next, Done. Note Container Manager's Docker version (Container Manager → Settings, or `docker version` in Task Scheduler).
- **See:** the project builds with no warning; the container `maktaba` is Running as user 1026:100 (Container → maktaba → Details); Docker is 24.0.2 or newer.
- **Screenshots:** `synology/04-project.en.png`, `synology/04-project.ar.png`

### 5. Finish setup in the browser
- **Do:** open `http://<nas>:8088`, set up in English, then again in a second browser profile in Arabic up to the first screen. Stop the timer at the home page.
- **See:** setup ends on the home page within 15 minutes of step 1; Settings → This machine names the platform DSM, user 1026, the tier (Light: the DS425+'s 6 GB reads under 6 GiB), and the memory limit of the compose file.
- **Screenshots:** `synology/05-this-machine.en.png`, `synology/05-this-machine.ar.png`

## Permissions

### 6. The shares as granted
- **Do:** add a library on `/volume1/Movies` and one on `/volume1/Recordings`; press Check access on each.
- **See:** Movies shows Read only, its fix in DSM's words, the `:ro` mount named; Recordings shows Read and write.
- **Screenshots:** `synology/06-shares.en.png`, `synology/06-shares.ar.png`

### 7. A share Maktaba cannot read
- **Do:** add a library on a share `maktaba` has no permission on (mounted in the project's YAML, no permission in DSM).
- **See:** the library shows No access with DSM's fix: Control Panel → Shared Folder → {share} → Edit → Permissions, give the user `maktaba` Read/Write (or Read only).
- **Screenshots:** `synology/07-no-access.en.png`, `synology/07-no-access.ar.png`

### 8. The ACL fix flow
- **Do:** follow the fix text exactly (Read only), then press Check access on the library page.
- **See:** the library turns Read only without being added again; a scan finds its videos; convert and delete are disabled with "This library is read-only for Maktaba".
- **Screenshots:** `synology/08-read-only.en.png`, `synology/08-read-only.ar.png`

### 9. DSM's own folders
- **Do:** with Universal Search or Synology Photos indexing Movies (so `@eaDir` folders exist), delete one video through File Station with the recycle bin on (`#recycle`), and take a snapshot of the share if Snapshot Replication is installed (`#snapshot`); scan the library again.
- **See:** the library lists no `@eaDir` preview, no recycled video and no snapshot copy; the deleted video is gone from the library.
- **Screenshots:** none

### 10. The data folder without permission
- **Do:** remove `maktaba`'s Read/Write from `docker/maktaba` and restart the project; read the container's log (Container → maktaba → Log); give the permission back and start it again.
- **See:** the container stops (exit 78); the log says, in English then Arabic, which folder and uid, with DSM's screens (Control Panel → Shared Folder → docker … or File Station → docker/maktaba → Properties → Permission); after the fix Maktaba starts with nothing lost.
- **Screenshots:** none

### 11. Subtitles on a read-only library
- **Do:** let subtitles run for one video of the read-only library; play it in the web player.
- **See:** the subtitle track plays; nothing new appears in the share (File Station shows no `.srt` and no `.maktaba` folder there).
- **Screenshots:** `synology/11-subtitles.en.png`, `synology/11-subtitles.ar.png`

## Hardware and kernel

### 12. VA-API on the J4125
- **Do:** DS425+: open Settings → This machine (the J4125 file passes `/dev/dri` and the render group; check the group with `stat -c %g /dev/dri/renderD128` over SSH), then play a 1080p HEVC file on a browser that needs a transcode; note whether HEVC decode works in the container (`-hwaccel vaapi` in the server log's transcode line) on DSM 7.2.2 or newer, and the speed from the log's `vod_transcode` lines. Then remove `group_add` from the YAML, build the project again and open This machine; put it back.
- **See:** This machine says "Video: hardware (iHD), H.264 and HEVC"; playback is smooth and the transcode runs at 2× real time or faster; CPU stays under half in Resource Monitor. Without `group_add`, This machine names the group to add in Container Manager's words and video is software.
- **Screenshots:** `synology/12-vaapi.en.png`, `synology/12-vaapi.ar.png`

### 13. The DSM kernel: Landlock and cgroups
- **Do:** Settings → This machine → plugin isolation; in Task Scheduler run `uname -r; stat -fc %T /sys/fs/cgroup` (tmpfs: cgroup v1, cgroup2fs: v2) once, and copy the machine report's memory section.
- **See:** the isolation level is Container on a kernel without Landlock (4.4, 5.10), and third-party plugins are held until an admin allows them; the memory limit read is the compose file's `mem_limit` on cgroup v1 as on v2. Write the kernel, the cgroup version and the level in the result file.
- **Screenshots:** `synology/13-isolation.en.png`, `synology/13-isolation.ar.png`

## Disks and updates

### 14. Disk sleep
- **Do:** set the media drives' hibernation to 10 minutes (Control Panel → Hardware & Power → HDD Hibernation, enable its log); leave the NAS idle outside quiet hours for an hour with Maktaba running and no playback.
- **See:** the hibernation log shows the media drives asleep for most of the hour; nothing in Maktaba's log touched them.
- **Screenshots:** none

### 15. A real update in the window
- **Do:** set the update window to the next hour (Settings → Updates); install the release before the newest one, then wait through the window. Then read the container's log and copy the report from Settings → This machine.
- **See:** the home updates inside the window, the pre-update dump is in backups, the container keeps running as 1026:100, and Settings shows the new version; the log and the report are attached to the result file, with no password, token or pairing code in them.
- **Screenshots:** `synology/15-updated.en.png`, `synology/15-updated.ar.png`

## Networking

### 16. The apps find the home (59.08)
- **Do:** with the default install (host network, no D-Bus socket mounted), open the Apple Player and the Android app on the same Wi-Fi and look for homes; then, on a computer, run `avahi-browse -art` (Linux) or `dns-sd -B _maktaba._tcp` (Mac).
- **See:** both apps list the home by itself, at `<nas>-maktaba.local`; DSM's own names (`<nas>.local` in Finder or `avahi-resolve -n <nas>.local` from a Linux PC) and its SMB share in Finder's sidebar still resolve and show, and the NAS was not renamed (no `-2` in its name). With the system D-Bus socket mounted as the guide's option, the home is listed at the NAS's own name instead.
- **Screenshots:** `synology/16-discovered.en.png`, `synology/16-discovered.ar.png`

## Other models

### 17. DS923+ (Ryzen, 4 GB): Light tier, software video
- **Do:** DS923+: steps 1 to 5 with the file for other models (no `/dev/dri`) and 4 GB; open Settings → This machine and play the same HEVC file.
- **See:** the tier is Light; This machine says "Video: software", that no render device is available to the container, and that this home converts video in software, so players play files as they are when they can; the transcode is at most 720p, and a second one waits with "Another video is being converted on this home". Speech to text is off, with base offered in the quiet hours.
- **Screenshots:** `synology/17-ryzen.en.png`, `synology/17-ryzen.ar.png`

### 18. DS223 (ARM, 2 GB): storage only
- **Do:** DS223: steps 1 to 4 with the file for other models (Container Manager runs on ARMv8 since DSM 7.2); run setup in the browser.
- **See:** setup refuses with the NAS-as-storage text (the memory read, the 3.5 GiB floor, and that the NAS can still hold the library for a Maktaba home on another computer); nothing is half installed. The guide's model table says the same before the household starts.
- **Screenshots:** `synology/18-storage-only.en.png`, `synology/18-storage-only.ar.png`

## The guide's DSM screenshots

DSM's own screens, for maktaba.tv/install/synology. Take each on the DS425+ in the dark theme
where DSM has one, once with DSM in English and once in the language the Arabic page shows
(Arabic if DSM offers it; else English, and say so in the result file). Save them under
`screenshots/synology/` and also as `web/packages/install/public/install/synology/<name>.<en|ar>.png`
(e.g. `guide-user.en.png`), then mark each one ready in `web/packages/install/src/synologyGuide.ts`
(`SCREENS`): the page shows a screenshot only once it is marked.

### 19. The DSM user
- **Do:** Control Panel → User & Group → maktaba → Edit, on the User Groups tab (users only, no administrators).
- **See:** the dialog shows `maktaba` in users and not in administrators.
- **Screenshots:** `synology/guide-user.en.png`, `synology/guide-user.ar.png`

### 20. The uid from Task Scheduler
- **Do:** Task Scheduler → Create → Scheduled Task → User-defined script, user root, script `id maktaba > /volume1/docker/maktaba-id.txt`; Run; open the file in File Station (Text Editor); delete the task.
- **See:** `uid=1026(maktaba) gid=100(users) …`.
- **Screenshots:** `synology/guide-uid.en.png`, `synology/guide-uid.ar.png`

### 21. The folder's and the shares' permissions
- **Do:** File Station → docker → maktaba → Properties → Permission; then Control Panel → Shared Folder → Movies → Edit → Permissions.
- **See:** `maktaba` with Read/Write on the folder, Read only on Movies.
- **Screenshots:** `synology/guide-folder.en.png`, `synology/guide-folder.ar.png`, `synology/guide-share.en.png`, `synology/guide-share.ar.png`

### 22. The project
- **Do:** Container Manager → Project → Create, the YAML pasted, before Next.
- **See:** name `maktaba`, path `docker/maktaba`, the YAML in the editor.
- **Screenshots:** `synology/guide-project.en.png`, `synology/guide-project.ar.png`

### 23. The firewall
- **Do:** Control Panel → Security → Firewall → Edit Rules → Create: ports 8088, 8080 and 8081 (TCP), source your LAN's subnet, Allow.
- **See:** the rule above the deny-all rule; `http://<nas>:8088` opens from a phone on the LAN.
- **Screenshots:** `synology/guide-firewall.en.png`, `synology/guide-firewall.ar.png`
