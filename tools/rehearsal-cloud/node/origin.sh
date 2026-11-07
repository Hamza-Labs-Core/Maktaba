#!/usr/bin/env bash
# The origin of Epic 50 story 50.31 on a rehearsal node (scenarios origin-only-via-edge and
# origin-aop). ORIGIN_MODE is tunnel or aop, as in /etc/maktaba/cloud-ha.env.
#
# Caddy runs the production files (deploy/cloud-ha/caddy/Caddyfile, and origin-aop.caddy in
# aop mode, rendered as setup.sh renders it), with one change: each site's certificate comes
# from Caddy's internal CA, not DNS-01 through Cloudflare. The site blocks, the 421 for other
# names, the CF-Connecting-IP strip on relay names and the AOP checks are production's.
#
# The api role gets production's [server]: loopback listener, server.env = "production",
# trusted_proxies = the loopback (the local connector, or Caddy in aop mode).
set -euo pipefail
: "${ORIGIN_MODE:?}"

tls_internal() {
	sed -e 's/^\([[:space:]]*\)dns cloudflare .*/\1issuer internal/' -e '/^[[:space:]]*resolvers /d' "$@"
}

install -d /etc/caddy/origin.d
tls_internal /opt/cloud-ha/caddy/Caddyfile |
	sed 's/^\([[:space:]]*\)admin 127\.0\.0\.1:2019$/&\n\1skip_install_trust/' >/etc/caddy/Caddyfile
aop_ca=""
case "${ORIGIN_MODE}" in
tunnel) rm -f /etc/caddy/origin.d/aop.caddy ;;
aop)
	install -m 0644 /origin/aop-ca.pem /etc/caddy/maktaba-aop-ca.pem
	tls_internal /opt/cloud-ha/caddy/origin-aop.caddy |
		sed "s|CLOUDFLARE_RANGES|${ORIGIN_RANGES:?the test Cloudflare ranges}|" >/etc/caddy/origin.d/aop.caddy
	aop_ca='aop_ca_file = "/etc/caddy/maktaba-aop-ca.pem"'
	;;
*)
	echo "ORIGIN_MODE must be tunnel or aop" >&2
	exit 1
	;;
esac

cat >/etc/maktaba/cloud.toml <<TOML
[server]
listen_addr = "127.0.0.1:8090"
env = "production"
trusted_proxies = ["127.0.0.1/32", "::1/128"]
origin_mode = "${ORIGIN_MODE}"
${aop_ca}
TOML
caddy validate --adapter caddyfile --config /etc/caddy/Caddyfile >/dev/null
echo "origin: ${ORIGIN_MODE}"
