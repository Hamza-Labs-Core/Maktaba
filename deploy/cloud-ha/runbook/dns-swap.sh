#!/usr/bin/env bash
# Runbook action: the DNS swap (Epic 50 story 50.12, docs/runbooks/cloud/hetzner-api-down.md),
# when the Floating IP can't follow the leader (the Hetzner API down): relay.maktaba.tv and
# *.relay.maktaba.tv point at the leader's own address through the Cloudflare API, TTL 60 s;
# --to floating puts them back on the Floating IP once the Hetzner API answers. The API, web
# app and console need no swap: they reach the leader through the Tunnel (50.31).
#
#   dns-swap.sh --to leader|floating|<IPv4>
#
# Env: CF_API_TOKEN (Zone → DNS → Edit on the zone only), CF_ZONE_ID, CF_API
# (https://api.cloudflare.com/client/v4), RELAY_DOMAIN (relay.maktaba.tv), FLOATING_IP,
# PUBLIC_IP_A, PUBLIC_IP_B (the nodes' public addresses; default RELAY_HOST_A/B), TTL (60);
# for --to leader, common.sh's.
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
if [ "${1:-}" != --to ] || [ -z "${2:-}" ]; then
    echo "usage: dns-swap.sh --to leader|floating|<IPv4>" >&2
    exit 2
fi
to="$2"
api="${CF_API:-https://api.cloudflare.com/client/v4}"
domain="${RELAY_DOMAIN:-relay.maktaba.tv}"
: "${CF_API_TOKEN:?}" "${CF_ZONE_ID:?}"
log() { echo "${0##*/}: $(date -u +%FT%TZ) $*" >&2; }

case "${to}" in
    floating) ip="${FLOATING_IP:?the Floating IP}" ;;
    leader)
        # shellcheck source=deploy/cloud-ha/runbook/common.sh
        . "${here}/common.sh"
        find_pair || { log "no node leads: nothing to point at"; exit 1; }
        if [ "${leader}" = "${RELAY_HOST_A}" ]; then ip="${PUBLIC_IP_A:-${RELAY_HOST_A}}"; else ip="${PUBLIC_IP_B:-${RELAY_HOST_B}}"; fi
        log "${leader} leads: its address is ${ip}" ;;
    *) ip="${to}" ;;
esac
[[ "${ip}" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]] || { log "not an IPv4 address: ${ip}"; exit 2; }

cf() { # cf <method> <path> [json body]
    curl -fsS --max-time 20 -X "$1" -H "Authorization: Bearer ${CF_API_TOKEN}" -H "Content-Type: application/json" \
        ${3:+--data "$3"} "${api}$2"
}
for name in "${domain}" "*.${domain}"; do
    id="$(cf GET "/zones/${CF_ZONE_ID}/dns_records?type=A&name=$(jq -rn --arg n "${name}" '$n|@uri')" | jq -r '.result[0].id // empty')"
    [ -n "${id}" ] || { log "no A record ${name} in the zone"; exit 1; }
    body="$(jq -cn --arg ip "${ip}" --argjson ttl "${TTL:-60}" '{content: $ip, ttl: $ttl, proxied: false}')"
    got="$(cf PATCH "/zones/${CF_ZONE_ID}/dns_records/${id}" "${body}" | jq -r 'select(.success) | .result.content')"
    [ "${got}" = "${ip}" ] || { log "Cloudflare did not take ${name} -> ${ip}"; exit 1; }
    log "${name} -> ${ip} (TTL ${TTL:-60} s, DNS only)"
done
log "done: resolvers follow within ${TTL:-60} s (the old TTL was 300 s: up to 5 minutes for some)"
