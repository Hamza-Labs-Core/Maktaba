# Implementation Plan — Story 25.33 One-click cloud-VPS deploy

> Companion to [story-25-33-one-click-cloud-deploy.md](story-25-33-one-click-cloud-deploy.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Runtime | Same Docker image (25.30). |
| Marketplaces | DigitalOcean Marketplace (Packer-built image), Hetzner Cloud App (same), Railway template, generic VPS shell installer. |
| Auto-TLS at server | Bundled Caddy with DNS-01 against `*.maktaba.app` (using a one-time short-lived Cloudflare API token issued by the cloud after subdomain claim). Alternative: cloud-side TLS via tunnel (preferred, requires no Caddy on server). |
| Hardening | UFW + fail2ban + no-root-SSH + unattended-upgrades. |
| Out of scope | AWS Lightsail formal listing; GCP / Azure marketplaces. |

## 1. Files

```
packaging/cloud/
  digitalocean/
    packer.json                  # Packer manifest using Ubuntu 22.04
    cloud-init.yaml              # provisioning
    user-data.sh                 # welcome web flow trigger
  hetzner-cloud/
    packer.json
    cloud-init.yaml
  railway/
    template.json
    Dockerfile
  vps/
    get-maktaba-vps.sh           # generic shell installer
  welcome/
    welcome.html                 # claim-token entry page
    welcome.go                   # tiny static server bundled in image
```

## 2. cloud-init (DO + Hetzner)

```yaml
#cloud-config
package_update: true
package_upgrade: true
packages: [docker.io, docker-compose-plugin, ufw, fail2ban, unattended-upgrades]
write_files:
  - path: /etc/maktaba/.env
    permissions: '0600'
    owner: root:root
    content: |
      PG_PASSWORD=$(openssl rand -hex 24)
  - path: /etc/maktaba/docker-compose.yml
    content: |
      services:
        postgres:
          image: postgres:16-alpine
          environment:
            POSTGRES_PASSWORD: ${PG_PASSWORD}
          volumes: [pgdata:/var/lib/postgresql/data]
        maktaba:
          image: ghcr.io/hamza-labs-core/maktaba:latest
          depends_on: [postgres]
          environment:
            MAKTABA_DB_URL: postgres://postgres:${PG_PASSWORD}@postgres:5432/maktaba?sslmode=disable
          ports: ["8080:8080","8081:8081"]
          volumes:
            - maktaba-data:/var/lib/maktaba
            - /opt/maktaba/welcome:/welcome:ro
      volumes:
        pgdata:
        maktaba-data:
  - path: /opt/maktaba/welcome/index.html
    content: |
      <!doctype html>
      <html><head><title>Welcome to Maktaba</title></head>
      <body><h1>Almost done</h1>
      <p>Enter your Maktaba Cloud claim token to finish setup:</p>
      <form method="post" action="/api/admin/cloud-link"><input name="token"/><button>Link</button></form>
      </body></html>
runcmd:
  - ufw allow ssh
  - ufw allow 80
  - ufw allow 443
  - ufw --force enable
  - systemctl enable --now unattended-upgrades
  - cd /etc/maktaba && docker compose up -d
```

A small Caddy container fronts ports 80/443 once a subdomain is bound (claim flow stores the cloud-issued cert from 25.23 via tunnel proxy — simpler: rely on tunnel-only access; expose port 80 to a static welcome page that redirects to claim).

## 3. Packer manifest (DO)

```json
{
  "variables": { "do_token": "{{env `DO_TOKEN`}}" },
  "builders": [{
    "type": "digitalocean",
    "api_token": "{{user `do_token`}}",
    "image": "ubuntu-22-04-x64",
    "region": "ams3",
    "size": "s-2vcpu-2gb",
    "ssh_username": "root",
    "snapshot_name": "maktaba-{{timestamp}}"
  }],
  "provisioners": [
    { "type": "file", "source": "packaging/cloud/digitalocean/cloud-init.yaml", "destination": "/etc/cloud/cloud.cfg.d/99-maktaba.cfg" },
    { "type": "shell", "script": "packaging/cloud/digitalocean/setup.sh" }
  ]
}
```

Hetzner uses the equivalent `hcloud` builder.

## 4. Railway template

`packaging/cloud/railway/template.json`:

```json
{
  "name": "Maktaba Media Server",
  "description": "Self-hosted media library w/ cloud relay",
  "services": [
    {
      "name": "postgres",
      "image": "postgres:16-alpine",
      "envs": { "POSTGRES_PASSWORD": "${{secret(32)}}", "POSTGRES_DB": "maktaba" },
      "volumes": [{ "mountPath": "/var/lib/postgresql/data", "size": 5 }]
    },
    {
      "name": "maktaba",
      "image": "ghcr.io/hamza-labs-core/maktaba:latest",
      "envs": {
        "MAKTABA_DB_URL": "postgres://postgres:${{services.postgres.envs.POSTGRES_PASSWORD}}@postgres:5432/maktaba?sslmode=disable",
        "MAKTABA_CLOUD_LINK_TOKEN": "${{vars.CLAIM_TOKEN}}"
      },
      "volumes": [{ "mountPath": "/var/lib/maktaba", "size": 5 }],
      "ports": [{ "port": 8080, "expose": true }]
    }
  ],
  "variables": [
    { "name": "CLAIM_TOKEN", "description": "Paste your Maktaba Cloud claim token" }
  ]
}
```

## 5. Generic shell installer

`packaging/cloud/vps/get-maktaba-vps.sh`:

```bash
#!/bin/sh
set -e
if [ "$(id -u)" -ne 0 ]; then echo "Run as root"; exit 1; fi
RAM=$(awk '/MemTotal/{print $2}' /proc/meminfo)
if [ "$RAM" -lt 1900000 ]; then  # < 2 GB
  echo "Maktaba requires at least 2 GB RAM. Aborting." >&2
  exit 1
fi
apt-get update && apt-get install -y docker.io docker-compose-plugin ufw fail2ban unattended-upgrades curl
ufw allow ssh; ufw allow 80; ufw allow 443; ufw --force enable
mkdir -p /etc/maktaba && cd /etc/maktaba
curl -fsSL https://raw.githubusercontent.com/hamza-labs-core/maktaba/main/packaging/cloud/vps/docker-compose.yml -o docker-compose.yml
echo "PG_PASSWORD=$(openssl rand -hex 24)" > .env
docker compose --env-file .env up -d
echo "Maktaba is starting. Open http://$(curl -fs ifconfig.me)/welcome"
```

## 6. Test plan

### 6.1 Manual matrix

| Test | Pins |
|---|---|
| DO 1-click smallest droplet | provisions; welcome page reachable. |
| DO 1-click $24 droplet | normal speed; `small` profile. |
| Hetzner CX22 | provisions. |
| Railway template | deploy works. |
| Shell installer on Lightsail | works. |
| Caddy auto-TLS (or tunnel TLS) | issues cert. |
| Reboot droplet | services up. |
| Refuse < 2 GB RAM | aborts cleanly. |

### 6.2 Unit / linters

| Test | Pins |
|---|---|
| `TestCloudInitYAMLValid` | cloud-init schema check. |
| `TestPackerManifestSchema` | Packer dry-run. |
| `TestUFWFlagsCommutative` | Re-running cloud-init is idempotent. |
| `TestRandomPGPasswordEntropy` | `openssl rand -hex 24` produces 24-byte hex. |

## 7. Edge cases — handling table

| Case | Behaviour | Pinned |
|---|---|---|
| DO marketplace review | Hardening pre-applied (UFW, fail2ban, no root SSH). | Doc. |
| Hetzner regions | nbg1 default; user pick. | Spec. |
| Railway pricing | "~$5/mo" estimated; not bundled in Pro. | Doc. |
| Volume durability | Vendor-owned; nightly DB dump documented. | Doc. |
| DDoS at VPS | Vendor-level only. Cloud Cloudflare protects only `*.maktaba.app`. | Doc. |
| IPv6 | Enabled by Caddy by default. | Spec. |
| Custom domain instead of subdomain | Out for v1. | Spec. |
| Sensitive data in cloud-init | PG password generated locally; `.env` mode 0600. | Implementation. |
| Cheap-tier resource starvation | Refuse < 2 GB. | `TestRamRefused`. |

## 8. Dependencies

- 25.30 (Docker image).
- 25.6 (claim token entry triggers cloud-link).
- 25.23 (cloud-issued cert for *.maktaba.app; server side uses tunnel for TLS so Caddy at the host isn't strictly needed for relay traffic).

## 9. Acceptance checklist

- [ ] DO Marketplace listing accepted.
- [ ] Hetzner Cloud App listing accepted.
- [ ] Railway template lives at https://railway.app/template/maktaba.
- [ ] `get-maktaba-vps.sh` works on Ubuntu/Debian/Lightsail.
- [ ] Welcome page redirects to cloud claim.
- [ ] Hardening: UFW, fail2ban, unattended-upgrades, no root SSH after first login.
- [ ] Refuses < 2 GB RAM with clear message.
- [ ] Tests in §6 pass.
