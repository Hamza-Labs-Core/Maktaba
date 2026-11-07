#!/usr/bin/env bash
# Sets up one machine of Maktaba Cloud's Phase 1 pair (Epic 50 story 50.07): node A or B
# (Caddy, Postgres 16 + Patroni, etcd, Redis, the cloud processes when leader) or the witness W
# (etcd, the third vote). Idempotent: run it again after changing /etc/maktaba/cloud-ha.env.
#
#   sudo deploy/cloud-ha/setup.sh --role a|b|witness
#
# Order: W first, then A (bootstraps the cluster), then B (joins as a replica).
#
# Before it runs, the owner has (deploy/cloud-ha/README.md, "What the owner creates"):
#   /etc/maktaba/cloud-ha.env   this node's settings (cloud-ha.env.example)
#   /etc/maktaba/hcloud-token   the maktaba-prod project's API token (A and B only, 0600)
#   /etc/maktaba/cloudflare.token  the DNS-edit token for the wildcard certificate (A and B)
#   /etc/etcd/pki/              ca.crt and this member's <name>.crt/.key (bin/make-etcd-certs)
# It calls no Hetzner or Cloudflare API itself: only Patroni's callback does, on a role change.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
log()  { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
die()  { printf '\033[1;31mxx \033[0m %s\n' "$*" >&2; exit 1; }

role=""
while [ $# -gt 0 ]; do
	case "$1" in
		--role) role="$2"; shift ;;
		*) die "unknown argument $1 (usage: setup.sh --role a|b|witness)" ;;
	esac
	shift
done
case "${role}" in a|b|witness) ;; *) die "usage: setup.sh --role a|b|witness" ;; esac
[ "$(id -u)" -eq 0 ] || die "must run as root (sudo)"

ENV_FILE=/etc/maktaba/cloud-ha.env
[ -r "${ENV_FILE}" ] || die "${ENV_FILE} is missing: copy deploy/cloud-ha/cloud-ha.env.example there and fill it in"
# shellcheck disable=SC1090
. "${ENV_FILE}"
: "${NODE_NAME:?}" "${NODE_ADDR:?}" "${PRIVATE_NET:?}" "${ETCD_INITIAL_CLUSTER:?}"

ETCD_VERSION="${ETCD_VERSION:-v3.6.5}"
PATRONI_VERSION="${PATRONI_VERSION:-4.1.5}"

# ---------------------------------------------------------------------------------------
# 1. Packages: chrony (etcd leases and ACME want the right time), Postgres 16 (PGDG), Patroni,
#    Redis (a cache: no persistence, 50.05), etcd (pinned release, checksum checked), UFW
# ---------------------------------------------------------------------------------------
log "packages"
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq chrony ufw curl ca-certificates gnupg jq gettext-base python3-venv >/dev/null
systemctl enable --now chrony >/dev/null

install_etcd() {
	if command -v etcd >/dev/null && [[ "$(etcd --version)" == *"${ETCD_VERSION#v}"* ]]; then return; fi
	local arch tar url
	arch="$(dpkg --print-architecture)"
	tar="etcd-${ETCD_VERSION}-linux-${arch}.tar.gz"
	url="https://github.com/etcd-io/etcd/releases/download/${ETCD_VERSION}"
	tmp="$(mktemp -d)"
	curl -fsSL -o "${tmp}/${tar}" "${url}/${tar}"
	curl -fsSL -o "${tmp}/SHA256SUMS" "${url}/SHA256SUMS"
	(cd "${tmp}" && grep " ${tar}\$" SHA256SUMS | sha256sum -c -) || die "etcd ${ETCD_VERSION}: checksum mismatch"
	tar -xzf "${tmp}/${tar}" -C "${tmp}"
	install -m 0755 "${tmp}/etcd-${ETCD_VERSION}-linux-${arch}/etcd" "${tmp}/etcd-${ETCD_VERSION}-linux-${arch}/etcdctl" /usr/local/bin/
	rm -rf "${tmp}"
}
install_etcd
id etcd >/dev/null 2>&1 || useradd --system --home /var/lib/etcd --shell /usr/sbin/nologin etcd
install -d -o etcd -g etcd -m 0700 /var/lib/etcd

if [ "${role}" != witness ]; then
	if [ ! -f /etc/apt/sources.list.d/pgdg.list ]; then
		curl -fsSL https://www.postgresql.org/media/keys/ACCC4CF8.asc | gpg --dearmor -o /usr/share/keyrings/pgdg.gpg
		codename="$(sed -n 's/^VERSION_CODENAME=//p' /etc/os-release)"
		echo "deb [signed-by=/usr/share/keyrings/pgdg.gpg] https://apt.postgresql.org/pub/repos/apt ${codename}-pgdg main" \
			>/etc/apt/sources.list.d/pgdg.list
		apt-get update -qq
	fi
	apt-get install -y -qq postgresql-16 redis-server >/dev/null
	# Patroni runs the cluster, not the distribution's unit
	systemctl disable --now postgresql >/dev/null 2>&1 || true
	if [ ! -x /opt/patroni/bin/patroni ] || [[ "$(/opt/patroni/bin/patroni --version)" != *"${PATRONI_VERSION}"* ]]; then
		python3 -m venv /opt/patroni
		/opt/patroni/bin/pip install -q "patroni[psycopg3,etcd3]==${PATRONI_VERSION}"
	fi
	ln -sf /opt/patroni/bin/patroni /opt/patroni/bin/patronictl /usr/local/bin/
	# Redis is a cache: nothing in it may matter after a restart (50.05)
	sed -i -E 's/^#?\s*appendonly .*/appendonly no/; s/^save .*/save ""/' /etc/redis/redis.conf
	grep -q '^save ""' /etc/redis/redis.conf || echo 'save ""' >>/etc/redis/redis.conf
	sed -i -E "s/^bind .*/bind 127.0.0.1 -::1/" /etc/redis/redis.conf
	systemctl restart redis-server
fi

# ---------------------------------------------------------------------------------------
# 2. etcd: a member on the private address, TLS both ways
# ---------------------------------------------------------------------------------------
log "etcd member ${NODE_NAME}"
for f in ca.crt "${NODE_NAME}.crt" "${NODE_NAME}.key"; do
	[ -s "/etc/etcd/pki/${f}" ] || die "/etc/etcd/pki/${f} is missing (deploy/cloud-ha/bin/make-etcd-certs, on the owner's machine)"
done
chown -R etcd:etcd /etc/etcd/pki && chmod 0600 "/etc/etcd/pki/${NODE_NAME}.key"
ETCD_NAME="${NODE_NAME}" ETCD_CLUSTER_STATE="${ETCD_CLUSTER_STATE:-new}" NODE_ADDR="${NODE_ADDR}" \
	ETCD_INITIAL_CLUSTER="${ETCD_INITIAL_CLUSTER}" envsubst <"${here}/etcd/etcd.conf.yml" >/etc/etcd/etcd.conf.yml
cat >/etc/systemd/system/etcd.service <<'UNIT'
[Unit]
Description=etcd (the Patroni leader lease; three members)
After=network-online.target chrony.service
Wants=network-online.target

[Service]
User=etcd
ExecStart=/usr/local/bin/etcd --config-file /etc/etcd/etcd.conf.yml
Restart=always
RestartSec=2
# etcd's heartbeats come first on the witness (guests run with lower weights, 50.10)
CPUWeight=1000
IOWeight=1000
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
UNIT
systemctl daemon-reload
systemctl enable --now etcd

# ---------------------------------------------------------------------------------------
# 3. The firewall: public 22 (owner and deploy runner addresses), 80 and 443 on A and B; the
#    rest from the private network only (the Hetzner Cloud Firewall says the same)
# ---------------------------------------------------------------------------------------
log "UFW"
ufw --force reset >/dev/null
ufw default deny incoming >/dev/null
ufw default allow outgoing >/dev/null
for src in ${SSH_FROM:-}; do ufw allow from "${src}" to any port 22 proto tcp >/dev/null; done
[ -n "${SSH_FROM:-}" ] || ufw allow 22/tcp >/dev/null
if [ "${role}" != witness ]; then
	ufw allow 80/tcp >/dev/null
	ufw allow 443/tcp >/dev/null
	ufw allow 443/udp >/dev/null
fi
for port in 2379 2380 5432 8008 8081 8444 8454 9100 9187 9443; do
	ufw allow from "${PRIVATE_NET}" to any port "${port}" proto tcp >/dev/null
done
ufw --force enable >/dev/null

# ---------------------------------------------------------------------------------------
# pgBackRest (story 50.09): both repositories, on A and B (backups, the WAL archive) and W
# (the weekly restore drill)
# ---------------------------------------------------------------------------------------
setup_pgbackrest() {
	apt-get install -y -qq pgbackrest >/dev/null
	install -d -o postgres -g postgres -m 0750 /etc/pgbackrest /var/log/pgbackrest /var/lib/pgbackrest
	PG_DATA=/var/lib/postgresql/16/maktaba REPO_VERIFY_TLS="${REPO_VERIFY_TLS:-y}" \
		envsubst <"${here}/pgbackrest/pgbackrest.conf" >/etc/pgbackrest/pgbackrest.conf
	chown postgres:postgres /etc/pgbackrest/pgbackrest.conf && chmod 0600 /etc/pgbackrest/pgbackrest.conf
	install -m 0755 "${here}/bin/backup" /usr/local/bin/maktaba-backup
	install -m 0755 "${here}/bin/restore" /usr/local/bin/maktaba-restore
	install -m 0755 "${here}/bin/drill-checks" /usr/local/bin/drill-checks
	install -m 0755 "${here}/bin/restore-drill" /usr/local/bin/maktaba-restore-drill
	install -d -o postgres -g postgres -m 0755 /var/lib/node_exporter/textfile
}
if [ -n "${REPO1_ENDPOINT:-}" ]; then setup_pgbackrest; fi

# ---------------------------------------------------------------------------------------
# node_exporter (with the textfile collector) and postgres_exporter for the witness to scrape
# (story 50.10); on W, the monitoring stack itself
# ---------------------------------------------------------------------------------------
apt-get install -y -qq prometheus-node-exporter >/dev/null
install -d -m 0755 /var/lib/node_exporter/textfile
echo 'ARGS="--collector.textfile.directory=/var/lib/node_exporter/textfile"' >/etc/default/prometheus-node-exporter
systemctl restart prometheus-node-exporter
install -m 0755 "${here}/bin/alert-textfile" /usr/local/bin/maktaba-alert
setup_monitoring() {
	command -v docker >/dev/null || apt-get install -y -qq docker.io docker-compose-v2 >/dev/null
	install -d -m 0750 /etc/maktaba/monitoring
	for f in prometheus.yml alertmanager.yml; do
		envsubst <"${here}/monitoring/${f}" >"/etc/maktaba/monitoring/${f}"
	done
	install -m 0644 "${here}/monitoring/alerts.yml" "${here}/monitoring/backup-rules.yml" "${here}/monitoring/blackbox.yml" /etc/maktaba/monitoring/
	docker compose -f "${here}/monitoring/compose.yml" up -d
	install -m 0755 "${here}/bin/fip-check" /usr/local/bin/maktaba-fip-check
	cat >/etc/systemd/system/maktaba-fip-check.service <<'UNIT'
[Unit]
Description=Maktaba Cloud: which node holds the Floating IP
[Service]
Type=oneshot
ExecStart=/usr/local/bin/maktaba-fip-check
UNIT
	cat >/etc/systemd/system/maktaba-fip-check.timer <<'UNIT'
[Unit]
Description=Maktaba Cloud: the Floating IP check, every minute
[Timer]
OnCalendar=*-*-* *:*:00
[Install]
WantedBy=timers.target
UNIT
	if [ -s /etc/maktaba/canary.secret ]; then
		cat >/etc/systemd/system/maktaba-canary.service <<'UNIT'
[Unit]
Description=Maktaba Cloud: the canary home (a real cloudlink in front of a stub)
After=network-online.target
[Service]
EnvironmentFile=/etc/maktaba/cloud-ha.env
ExecStart=/usr/local/bin/maktaba-canary
Restart=always
RestartSec=5
CPUWeight=50
[Install]
WantedBy=multi-user.target
UNIT
	fi
	systemctl daemon-reload
	systemctl enable --now maktaba-fip-check.timer
	[ ! -s /etc/maktaba/canary.secret ] || systemctl enable --now maktaba-canary
}

if [ "${role}" = witness ]; then
	if [ -n "${REPO1_ENDPOINT:-}" ]; then
		# the drill restores with the same Postgres major
		if [ ! -f /etc/apt/sources.list.d/pgdg.list ]; then
			curl -fsSL https://www.postgresql.org/media/keys/ACCC4CF8.asc | gpg --dearmor -o /usr/share/keyrings/pgdg.gpg
			codename="$(sed -n 's/^VERSION_CODENAME=//p' /etc/os-release)"
			echo "deb [signed-by=/usr/share/keyrings/pgdg.gpg] https://apt.postgresql.org/pub/repos/apt ${codename}-pgdg main" \
				>/etc/apt/sources.list.d/pgdg.list
			apt-get update -qq
		fi
		apt-get install -y -qq postgresql-16 >/dev/null
		systemctl disable --now postgresql >/dev/null 2>&1 || true
		install -m 0644 "${here}/systemd/maktaba-restore-drill.service" "${here}/systemd/maktaba-restore-drill.timer" /etc/systemd/system/
		systemctl daemon-reload
		systemctl enable --now maktaba-restore-drill.timer
	fi
	# the monitoring (story 50.10): Prometheus, Alertmanager, Grafana, the blackbox exporter and
	# the Pushgateway in Docker (guests, lower weights), the canary home, the Floating IP check
	setup_monitoring
	log "witness ${NODE_NAME} is up: etcd, with the restore drill and the monitoring as guests"
	exit 0
fi

# ---------------------------------------------------------------------------------------
# 4. The watchdog: a leader that hangs is rebooted before its lease runs out
# ---------------------------------------------------------------------------------------
echo softdog >/etc/modules-load.d/softdog.conf
modprobe softdog
cat >/etc/udev/rules.d/61-watchdog.rules <<'RULE'
KERNEL=="watchdog", OWNER="postgres", GROUP="postgres", MODE="0600"
RULE
{ udevadm control --reload && udevadm trigger --name-match=watchdog; } || true

# ---------------------------------------------------------------------------------------
# 5. The Floating IP on the interface (Hetzner routes it to the leader only)
# ---------------------------------------------------------------------------------------
if [ -n "${FLOATING_IP:-}" ]; then
	FLOATING_IP="${FLOATING_IP}" envsubst <"${here}/netplan/60-floating-ip.yaml" >/etc/netplan/60-floating-ip.yaml
	chmod 0600 /etc/netplan/60-floating-ip.yaml
	netplan apply
fi

# ---------------------------------------------------------------------------------------
# 6. Patroni, its callback, and what the callback may do (sudo for exactly these)
# ---------------------------------------------------------------------------------------
log "Patroni"
install -m 0755 "${here}/bin/on-role-change" "${here}/bin/switchover" "${here}/bin/leader-guard" "${here}/bin/bluegreen" /usr/local/bin/
install -d -o postgres -g postgres -m 0700 /var/lib/postgresql/16/maktaba
[ -s /etc/maktaba/hcloud-token ] || die "/etc/maktaba/hcloud-token is missing (the maktaba-prod project's API token)"
chown postgres:postgres /etc/maktaba/hcloud-token && chmod 0600 /etc/maktaba/hcloud-token
chgrp postgres "${ENV_FILE}" && chmod 0640 "${ENV_FILE}"
cat >/etc/sudoers.d/maktaba-patroni <<SUDO
postgres ALL=(root) NOPASSWD: /usr/bin/systemctl start maktaba-cloud-active.target maktaba-cloud-relay@blue.service, /usr/bin/systemctl start maktaba-cloud-active.target maktaba-cloud-relay@green.service, /usr/bin/systemctl stop maktaba-cloud-active.target, /usr/sbin/ip addr add ${FLOATING_IP:-0.0.0.0}/32 dev ${FIP_DEV:-eth0}
SUDO
chmod 0440 /etc/sudoers.d/maktaba-patroni
visudo -cf /etc/sudoers.d/maktaba-patroni >/dev/null

install -d -o postgres -g postgres -m 0750 /etc/patroni
export NODE_NAME NODE_ADDR PRIVATE_NET
ETCD_HOSTS="${ETCD_HOSTS:?}" ETCD_PROTOCOL=https ETCD_CACERT=/etc/patroni/etcd-ca.crt \
	ETCD_CERT="/etc/patroni/etcd-client.crt" ETCD_KEY="/etc/patroni/etcd-client.key" \
	PG_DATA=/var/lib/postgresql/16/maktaba PG_BIN=/usr/lib/postgresql/16/bin \
	PG_LISTEN="${NODE_ADDR}:5432,127.0.0.1:5432" RESTAPI_LISTEN="${NODE_ADDR}:8008" \
	PG_SUPERUSER_PASSWORD="${PG_SUPERUSER_PASSWORD:?}" PG_REPLICATION_PASSWORD="${PG_REPLICATION_PASSWORD:?}" \
	PG_REWIND_PASSWORD="${PG_REWIND_PASSWORD:?}" ARCHIVE_COMMAND="${ARCHIVE_COMMAND:-/bin/true}" \
	POST_BOOTSTRAP="${POST_BOOTSTRAP:-/usr/local/bin/maktaba-post-bootstrap}" \
	ON_ROLE_CHANGE=/usr/local/bin/on-role-change WATCHDOG_MODE=required \
	envsubst <"${here}/patroni/patroni.yml" >/etc/patroni/patroni.yml
install -o postgres -g postgres -m 0640 /etc/etcd/pki/ca.crt /etc/patroni/etcd-ca.crt
install -o postgres -g postgres -m 0640 "/etc/etcd/pki/${NODE_NAME}.crt" /etc/patroni/etcd-client.crt
install -o postgres -g postgres -m 0600 "/etc/etcd/pki/${NODE_NAME}.key" /etc/patroni/etcd-client.key
chown postgres:postgres /etc/patroni/patroni.yml && chmod 0600 /etc/patroni/patroni.yml
install -m 0755 "${here}/bin/post-bootstrap" /usr/local/bin/maktaba-post-bootstrap
cat >/etc/systemd/system/patroni.service <<'UNIT'
[Unit]
Description=Patroni (Postgres 16; the leader runs the cloud)
After=network-online.target etcd.service
Wants=network-online.target

[Service]
User=postgres
ExecStart=/usr/local/bin/patroni /etc/patroni/patroni.yml
ExecReload=/bin/kill -HUP $MAINPID
KillMode=process
TimeoutSec=30
Restart=on-failure

[Install]
WantedBy=multi-user.target
UNIT

# ---------------------------------------------------------------------------------------
# 7. The cloud's units (started on the leader only) and Caddy (on both, always)
# ---------------------------------------------------------------------------------------
id maktaba >/dev/null 2>&1 || useradd --system --home /var/lib/maktaba --shell /usr/sbin/nologin maktaba
install -m 0644 "${here}/systemd/"*.service "${here}/systemd/"*.target "${here}/systemd/"*.timer /etc/systemd/system/
[ -s /etc/maktaba/cloudflare.token ] || die "/etc/maktaba/cloudflare.token is missing (the DNS-edit token for the wildcard, 50.02)"
install -d -m 0755 /etc/caddy
install -m 0644 "${here}/caddy/Caddyfile" /etc/caddy/Caddyfile
cat >/etc/systemd/system/maktaba-caddy.service <<UNIT
[Unit]
Description=Caddy (the wildcard certificate; both nodes, always)
After=docker.service network-online.target
Requires=docker.service

[Service]
ExecStartPre=-/usr/bin/docker rm -f maktaba-caddy
ExecStart=/usr/bin/docker run --name maktaba-caddy --network host \\
  -e RELAY_DOMAIN=${RELAY_DOMAIN:?} -e ACME_EMAIL=${ACME_EMAIL:?} \\
  -e "RELAY_UPSTREAMS=127.0.0.1:8443 127.0.0.1:8453" -e NODE_ADDR=${NODE_ADDR} --env-file /etc/maktaba/caddy.env \\
  -v /etc/caddy/Caddyfile:/etc/caddy/Caddyfile:ro -v caddy_data:/data \\
  ghcr.io/hamza-labs-core/maktaba-caddy:${MAKTABA_CADDY_TAG:-2.11.7-cf0.2.4}
ExecStop=/usr/bin/docker stop -t 10 maktaba-caddy
Restart=always

[Install]
WantedBy=multi-user.target
UNIT
printf 'CF_API_TOKEN=%s\n' "$(cat /etc/maktaba/cloudflare.token)" >/etc/maktaba/caddy.env
chmod 0600 /etc/maktaba/caddy.env
command -v docker >/dev/null || apt-get install -y -qq docker.io >/dev/null

systemctl daemon-reload
grep -q '^PATRONI_URL=' "${ENV_FILE}" || echo "PATRONI_URL=http://${NODE_ADDR}:8008" >>"${ENV_FILE}"
grep -q '^ALERT_CMD=.' "${ENV_FILE}" || echo "ALERT_CMD=/usr/local/bin/maktaba-alert" >>"${ENV_FILE}"
apt-get install -y -qq prometheus-postgres-exporter >/dev/null || true
systemctl enable --now patroni maktaba-caddy maktaba-leader-guard
if [ -n "${REPO1_ENDPOINT:-}" ]; then systemctl enable --now maktaba-backup.timer; fi
log "${NODE_NAME} is up: Patroni decides whether it leads (patronictl -c /etc/patroni/patroni.yml list)"
