# Maktaba's Unraid template

[`maktaba.xml`](maktaba.xml) is a Community Applications (CA) template: Unraid's Apps tab
installs Maktaba from it. It is the published Docker compose
([`cloud/installers/docker/docker-compose.yml`](../../../cloud/installers/docker/docker-compose.yml))
in Unraid's form, checked against it in CI (`tests/nas/install_files.py`) so the two do not
drift.

| Setting | Value | Why |
|---|---|---|
| Repository | `ghcr.io/hamza-labs-core/maktaba-server:latest` | the highest stable release; the server updates itself on its data folder after that |
| Network | `host` | apps and TVs find Maktaba by mDNS; its ports (8088, 8080, 8081, 54329) are its own |
| Ports | Web, API, Live TV and Database port (advanced, empty) | a port another app on the server uses is chosen there (`MAKTABA_*_PORT`, 59.08); empty keeps the default |
| Extra Parameters | `--user 99:100 --stop-timeout 90` | Unraid's `nobody:users`, never root; 90 s for the launcher to drain the pipeline and Postgres |
| Data | `/var/lib/maktaba` ← `/mnt/cache/appdata/maktaba` | a pool, never the array's `/mnt/user` (shfs, FUSE): setup refuses that for the data folder |
| Media | `/mnt/user/Media` at the same path | shfs is fine for media; the same path inside keeps share identity for machines that join |
| Device | `/dev/dri` (optional) | VA-API or Quick Sync; add `--group-add <gid>` (`stat -c %g /dev/dri/renderD128`) |
| Variables | `TZ`, `MAKTABA_PLATFORM=unraid` | quiet hours in local time; Unraid's menu names in permission messages |

**When the array is stopped**, the media paths vanish: libraries show No access and nothing is
treated as deleted (59.03's root probe).

## Trying it before CA lists it

On the Unraid box: Docker → Add Container → Template repositories, add
`https://github.com/Hamza-Labs-Core/Maktaba` and save; or copy `maktaba.xml` to
`/boot/config/plugins/dockerMan/templates-user/my-Maktaba.xml`, then Add Container → Template
→ Maktaba.

## Submitting it to Community Applications (a maintainer's action)

CA lists templates from a repository registered with it. The steps need the project's GitHub
account and an Unraid forum account, so a maintainer does them:

1. **A support thread** on the Unraid forums, in *Docker Containers* (the template's
   `<Support>` may then point there; it points at maktaba.tv/install/unraid until then).
   Title and text:

   > **[Support] Maktaba: a home media library in Arabic and English**
   >
   > Maktaba runs your movies, series, recordings and live TV, with search inside what is
   > said and subtitles made by speech to text, and apps for phones, tablets and TVs. One
   > container runs everything (web app, API, live TV, the pipeline and its own Postgres)
   > and updates itself in a quiet window, each release verified (Sigstore) before it is
   > installed.
   >
   > - Image: `ghcr.io/hamza-labs-core/maktaba-server` (amd64, arm64), runs as 99:100.
   > - Keep the data path on a cache pool, not `/mnt/user`.
   > - Hardware video: pass `/dev/dri` and add `--group-add <gid>` to Extra Parameters.
   > - Guide: https://maktaba.tv/install/unraid · Source: https://github.com/Hamza-Labs-Core/Maktaba

2. **Register the template repository** with CA: the form linked from the CA *Application
   Policies* thread on the Unraid forums, giving the repository
   `https://github.com/Hamza-Labs-Core/Maktaba` (CA reads every template XML in it; this is the
   only one) and the support thread's link.
3. **Check the listing**: Apps → search *Maktaba* on a test box; install with the defaults;
   it must reach ready as 99:100 with the data on the cache pool (story 59.11's acceptance;
   the device checklist, `docs/nas/checklists/`, records it with logs).

A change to the template ships through a normal pull request: CA re-reads the repository
every few hours.
