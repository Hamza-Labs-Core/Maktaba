# The store reviewers' home (review.maktaba.tv)

> Epic 58 story 58.04. What reviewers get, the submission notes and the submission-round steps:
> [docs/stores/review-home.md](../../docs/stores/review-home.md).

A Maktaba home the project runs, beside the cloud's deploy: one small Linux machine with the
Docker install every household uses (`ghcr.io/hamza-labs-core/maktaba-server`, unchanged) and
Caddy for `review.maktaba.tv`'s certificate. It updates itself like any home (Epic 28, Admin →
Updates), and it is registered with Maktaba Cloud like a customer's home, so the cloud code
(story 58.03) and the relay are exercised exactly as a customer's are.

| File | What it is |
|---|---|
| `docker-compose.yml` | the home and Caddy, both on the host's network |
| `Caddyfile` | `review.maktaba.tv` → the home's web app; `/approve` → `/pair` |
| `review-home.env.example` | the settings (no secret): the name, the ACME email, the media folder, the cloud plan |
| `setup.sh` | the provisioning: Docker, ffmpeg, the firewall (SSH, 80, 443), the content set, the stack |
| `seed.sh` | `seed` (accounts, libraries, channel, titles) and `rotate` (new passwords, devices signed out) |
| `content/manifest.json` | the content set: each item's titles, licence, attribution, source and checksum |
| `content/subtitles/` | Cosmos Laundromat's English subtitles and their Arabic translation (CC BY 4.0) |

The content set is fetched and checked by [`tools/review_home/content.py`](../../tools/review_home/content.py)
(each source against the Internet Archive's published SHA-1; the 4K HDR10, MKV/EAC3/WebVTT,
transcode and audiobook files derived with ffmpeg by the manifest's arguments). The seeding runs
`maktaba-server review-home` inside the home's container ([`api/internal/reviewhome`](../../api/internal/reviewhome)).

## Provisioning (once)

1. A machine (2 vCPU, 4 GB, 60 GB disk is plenty; Ubuntu 22.04 or 24.04) and a DNS-only `A`
   record `review.maktaba.tv` → its address ([docs/cloud-dns.md](../../docs/cloud-dns.md)).
2. On it: `git clone https://github.com/Hamza-Labs-Core/Maktaba.git && sudo Maktaba/deploy/review-home/setup.sh`,
   set `ACME_EMAIL` in `/opt/maktaba-review/.env`, run `setup.sh` again. The first run fetches
   about 2 GB and encodes the 4K HDR10 file, which takes a while.
3. The home's own first-run setup: `cd /opt/maktaba-review && docker compose run --rm maktaba setup`.
   The admin it makes is the owner's, kept in the owner's password manager; reviewers never get it.
4. Admin → Remote access: register the home with Maktaba Cloud under the project's review-home
   account. In the operator console, set that account's plan to `basic` (the lowest plan with the
   relay, `REVIEW_HOME_PLAN`) with the reason "store review home", and exclude it from revenue as
   the canary's account is.
5. With the two release secrets in the environment of the command:
   `deploy/review-home/seed.sh seed`. After the libraries' first scan, run it again: it titles
   what the scan found and prints any item still missing.
6. Turn on the scheduled check: the repository secret `REVIEW_HOME_VIEWER_PASSWORD` and the
   variable `REVIEW_HOME_PROBE=true` (`.github/workflows/review-home-probe.yml`).

## Updates

The home takes each release by itself. While a store submission is open, the release session
holds it (Admin → Updates → Defer, or Skip each new version): see the submission round in
[docs/stores/review-home.md](../../docs/stores/review-home.md#a-submission-round). A change to the
content set is a pull request to `content/manifest.json`, then `setup.sh` and `seed.sh seed` again.
