# Story 25.33 — One-click cloud-VPS deploy

> Epic 25 · Cloud relay · Phase 6 (server distribution)

## Description

Some users want a "Maktaba server in 5 minutes" without buying
hardware. We meet them where they are: 1-click VPS deploys for
DigitalOcean, Hetzner Cloud, and Railway. The payload is the same
Docker image (25.30) wrapped in vendor-native deployment artifacts.

Targets:

1. **DigitalOcean Marketplace ("1-Click App").**
   - Submit a Marketplace listing with our Packer-built image.
   - Image bakes Ubuntu 22.04 + Docker + our compose file.
   - On droplet boot, `cloud-init` writes a random Postgres
     password, brings compose up, exposes ports 80/443 via
     Caddy with auto-LE for `<droplet>.maktaba.app` (assigned
     by our cloud after the user enters their cloud claim
     token in the welcome web flow).
2. **Hetzner Cloud "App".**
   - Hetzner Cloud has a similar one-click flow via their
     "Apps" catalog; same Packer base image.
3. **Railway template.**
   - A Railway "Deploy Template" link opens a one-click flow
     that provisions Postgres + Maktaba container with
     volumes; user supplies a cloud claim token via env var.
4. **Generic shell installer.**
   - For any Linux VPS (Linode, OVH, Vultr, AWS Lightsail):
     `curl -sSL https://get.maktaba.app/vps.sh | sudo bash`
     — installs Docker, brings compose up, prompts for
     library volume, prints a claim token URL.

What "5 minutes" includes:

- Provisioning the box (vendor-driven; usually 30-60s).
- Booting cloud-init / templates (60-180s).
- Bringing up Postgres + Maktaba (30s).
- User enters cloud claim token via the welcome page;
  domain provisioned (25.22).
- User uploads or rsyncs media; **not** included in the 5
  minutes (depends on library size).

The "default vps profile" preconfigures:

- Whisper `small` model (most VPSes are 4 vCPU 8 GB RAM).
- Postgres mode (not SQLite) — multi-tenant readiness is
  free, only marginally pricier.
- Caddy auto-TLS via DNS-01 against the user's cloud
  subdomain.
- daily DB dump to `/var/backups/maktaba` plus optional
  rclone target (user supplies remote later).

## Acceptance criteria

- **Given** a user clicks "Deploy on DigitalOcean" from
  our marketing page,
  **when** the droplet boots,
  **then** within 5 minutes `https://<droplet>.maktaba.app`
  serves the welcome page asking for a cloud claim token.
- **Given** the user enters the claim token,
  **when** the welcome page submits it,
  **then** the server links to their cloud account, the
  subdomain is provisioned, and they are redirected to
  the dashboard.
- **Given** a Hetzner Cloud user does the same,
  **when** the App finishes provisioning,
  **then** the same welcome flow runs.
- **Given** a Railway user clicks "Deploy",
  **when** the template completes,
  **then** Maktaba runs with persistent volumes attached
  and reachable on Railway's HTTPS endpoint.
- **Given** a generic VPS user runs the shell installer,
  **when** the script completes,
  **then** Docker is installed, compose is up, and the
  welcome URL is printed to stdout.
- **Given** the user terminates the droplet,
  **when** the next probe-up fails,
  **then** the cloud reports the server offline (25.16);
  no other side effects.
- **Given** a security update releases for Ubuntu base,
  **when** unattended-upgrades runs,
  **then** the host updates without touching containers.

## Test cases

| ID  | Type        | Setup | Action | Expected |
|-----|-------------|-------|--------|----------|
| T01 | manual      | DO 1-click with smallest droplet ($6) | provision | works (Whisper `tiny` profile) |
| T02 | manual      | DO 1-click with $24 droplet (4vCPU 8GB) | provision | `small` profile, normal speed |
| T03 | manual      | Hetzner CX22 | provision | works |
| T04 | manual      | Railway template | deploy | works |
| T05 | unit        | cloud-init script | feed minimal env | exits 0 |
| T06 | regression  | re-provision over existing volume | recover | DB intact |
| T07 | smoke       | shell installer on Lightsail | run | works |
| T08 | regression  | Caddy auto-TLS DNS-01 against `*.maktaba.app` | observe | issues cert |
| T09 | manual      | UFW firewall left default | check | only 22, 80, 443 open |
| T10 | regression  | unattended-upgrades during running container | observe | no service downtime |

## Edge cases

- **DigitalOcean Marketplace approval.** Listings need to
  pass DO security review; bake hardening (fail2ban,
  UFW, no default password, root SSH off after first
  login). Documented.
- **Hetzner regions.** Default `nbg1`; user can pick.
  The cloud relay region (Hetzner `fsn1`) is independent.
- **Railway pricing.** Railway charges by usage; we
  document an estimate ("≈$5/mo for typical small
  library"). Not bundled with our Pro plan.
- **Volume durability.** We rely on the vendor's volume
  durability; Maktaba's nightly dump to S3-like is the
  user-controlled backup.
- **DDoS at the VPS.** Vendor-level protection varies; our
  Cloudflare proxy applies on the cloud side
  (`maktaba.app`), not the VPS direct IP. Document.
- **IPv6.** Hetzner provides IPv6 by default; DO and
  Railway too. Caddy listens on both.
- **Custom domain instead of subdomain.** Out for v1.
  User can manually edit Caddyfile and own their cert.
- **Sensitive data in cloud-init.** Random PG password,
  not seeded from a constant; stored in
  `/etc/maktaba/.env` with mode 0600 owned by root.
- **Over-provisioning.** Cheapest droplets ($4–$6) on
  DO/Hetzner are 1 vCPU 1 GB RAM — barely enough for
  Maktaba. We refuse to deploy on < 2 GB; the
  marketplace page documents the floor.

## Files / packages

- `packaging/cloud/digitalocean/` — Packer manifest,
  cloud-init.
- `packaging/cloud/hetzner-cloud/` — same.
- `packaging/cloud/railway/template.json`.
- `packaging/cloud/vps/get-maktaba-vps.sh`.
- Marketing: `https://maktaba.app/host` page links to
  each option.

## Open questions

- **AWS Lightsail formal listing.** Defer; manual install
  works.
- **GCP / Azure marketplaces.** Lower priority; defer.
