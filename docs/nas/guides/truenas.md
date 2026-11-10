# Install Maktaba on TrueNAS

For TrueNAS Community Edition **25.04 (Fangtooth) and newer** (25.10 Goldeye included).
TrueNAS CORE is not supported. About 20 minutes.

Maktaba runs as one app on your NAS: the server, the web app, live TV, the pipeline that reads
your library, and its own database. It updates itself, inside the update window you choose.

## Before you start

- **Memory:** 8 GB or more is comfortable; Maktaba runs from 4 GB. Don't worry when TrueNAS's
  dashboard shows memory as "used": that is ZFS's cache (the ARC), which gives memory back when
  an app needs it. Maktaba counts it as available.
- **An SSD pool** if you have one: Maktaba's database and thumbnails are much faster there. Your
  media can stay on hard disks.
- **A GPU** (optional): an Intel chip (an N100, a Core with Quick Sync) or AMD converts video for
  phones and browsers without loading the CPU. An NVIDIA card also speeds up speech to text.

## 1. Make the data dataset

Datasets → **Add Dataset** on your SSD pool (or your main pool), name it `apps/maktaba`, and set
**Dataset Preset** to **Apps**. The Apps preset gives TrueNAS's `apps` user (568), which Maktaba
runs as, the right to write there. Maktaba never changes permissions itself.

> 📷 **Screenshot to come:** `screenshots/truenas/dataset-apps-preset.en.png`: Add Dataset with the
> Apps preset.

## 2. Let Maktaba read your media

For each dataset with films or series: Datasets → choose it → **Permissions** → **Edit** → **Add
Item**: Who **User**, User **apps**, Permissions **Read** (or **Modify** if Maktaba may write
subtitles beside your videos) → **Save Access Control List**.

> 📷 **Screenshot to come:** `screenshots/truenas/acl-entry.en.png`: the ACL editor with the apps
> user's entry.

If you skip a dataset, Maktaba still installs; that library shows **No access**, with this same
fix.

## 3. Install the app

Apps → **Discover Apps** → search **Maktaba** → **Install**.

- **Timezone:** yours.
- **User and Group:** keep 568 and 568.
- **Network:** keep **Host Network** on. Your phones and TVs then find Maktaba by themselves.
- **Storage → Maktaba Data Storage:** Host Path, `/mnt/<your SSD pool>/apps/maktaba`.
- **Storage → Media Folders:** add each media dataset, for example `/mnt/tank/media`. Tick
  **Read Only** if Maktaba should never write there.
- **Resources → Memory:** 8192 MB is a good start. Maktaba sizes itself to this limit.
- **Resources → GPU:** tick your Intel or AMD GPU, or choose your NVIDIA card (install TrueNAS's
  NVIDIA drivers first: Apps → Configuration → Settings).

> 📷 **Screenshot to come:** `screenshots/truenas/install-storage.en.png` and
> `screenshots/truenas/install-resources.en.png`: the install form's Storage and Resources.

**Maktaba not in Discover Apps yet?** Until it is in TrueNAS's catalog, install it as a Custom
App: download `maktaba-truenas-custom-app.yaml` from the
[newest release](https://github.com/Hamza-Labs-Core/Maktaba/releases/latest), change the lines
marked `CHANGE` (your time zone, your data dataset, your media), then Apps → Discover Apps → **⋮**
→ **Install via YAML**, name it `maktaba`, paste the file and **Save**.

> 📷 **Screenshot to come:** `screenshots/truenas/install-via-yaml.en.png`: Install via YAML.

## 4. Run the setup

When the app shows **Running**: Apps → Installed → Maktaba → **Workloads** → maktaba → **Shell**
(the terminal icon), and run:

```
/opt/maktaba/server/bin/maktaba-server container setup
```

Answer its questions (the admin's name and password, your libraries). Maktaba starts by itself
when it finishes. Open **Web UI** on the app's page, or `http://<your NAS>:8088`.

> 📷 **Screenshot to come:** `screenshots/truenas/app-shell.en.png`: the app's Shell with the
> setup command.

## After setup

- **Settings → This machine** shows what Maktaba found: TrueNAS, its tier (from the memory
  limit), the memory available (ZFS's cache counted), and the video hardware.
- **A library shows No access or Read only:** add the ACL entry of step 2 for user 568, then
  press **Check access**.
- **Maktaba stopped right after install** and its logs say it cannot write to its data folder:
  edit the dataset's permissions and add an ACL entry for user 568 with Modify, or use the Apps
  preset.
- **Video:** with an Intel or AMD GPU allocated, This machine says "Video: hardware (iHD), H.264
  and HEVC". TrueNAS 25.10's NVIDIA drivers no longer support Pascal and older cards (GTX 10xx and
  earlier): Maktaba then converts on the CPU and says why.
- **Updates:** Maktaba updates itself inside its update window (Admin → Updates), and TrueNAS
  updates the app when the catalog moves it. Either way your home carries on.
- **Reinstalling** with the same data dataset brings your home back as it was.
- **Host Network off:** set **Host IP** to the NAS's address. Phones and TVs then can't find
  Maktaba by themselves; they connect by the NAS's address, the home's HTTPS name, or a code from
  Maktaba Cloud.
