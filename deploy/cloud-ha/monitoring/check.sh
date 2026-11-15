#!/usr/bin/env bash
# Checks the witness's monitoring configs (story 50.10) with the tools of the images it runs:
# promtool (the Prometheus config rendered with test values, the rules, their unit tests) and
# amtool (Alertmanager). Needs docker. relay.yml runs it.
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
work="$(mktemp -d)"; trap 'rm -rf "${work}"' EXIT
# mktemp's 0700 would hide the configs from the images' user (nobody)
chmod 0755 "${work}"
export NODE_A_ADDR=10.50.0.2 NODE_B_ADDR=10.50.0.3 RELAY_DOMAIN=relay.maktaba.tv \
    NTFY_URL=https://ntfy.example/topic NTFY_TOKEN=t ALERT_EMAIL=ops@example.com ALERT_EMAIL_FROM=alerts@example.com \
    SMTP_HOST=smtp.example.com:587 SMTP_USER=u SMTP_PASSWORD=p
for f in prometheus.yml alertmanager.yml; do envsubst <"${here}/${f}" >"${work}/${f}"; done
cp "${here}/alerts.yml" "${here}/backup-rules.yml" "${here}/blackbox.yml" "${work}/"
chmod 0644 "${work}"/*
prom=prom/prometheus:v3.5.0
docker run --rm -v "${work}:/etc/prometheus" --entrypoint promtool "${prom}" check config /etc/prometheus/prometheus.yml
docker run --rm -v "${here}:/r" -w /r --entrypoint promtool "${prom}" test rules alerts-test.yml backup-rules-test.yml capacity-test.yml broadcast-rules-test.yml
docker run --rm -v "${work}:/c" --entrypoint amtool prom/alertmanager:v0.28.1 check-config /c/alertmanager.yml
# the routes (stories 50.10, 50.28): SEV1 and SEV2 page the phone, SEV3 is the email digest;
# the broadcast alerts by their own severities (alerts.yml)
amroute=(docker run --rm -v "${work}:/c" --entrypoint amtool prom/alertmanager:v0.28.1 config routes test --config.file=/c/alertmanager.yml)
while read -r want labels; do
    # shellcheck disable=SC2086 # the labels are separate arguments
    "${amroute[@]}" --verify.receivers="${want}" ${labels} >/dev/null || { echo "alertmanager: ${labels} does not route to ${want}"; exit 1; }
done <<'ROUTES'
phone severity=sev1 alertname=broadcast_canary_down
phone severity=sev2 alertname=broadcast_ingest_lag_high
phone severity=sev2 alertname=broadcast_revocation_list_stale
email severity=sev3 alertname=broadcast_glass_to_glass_high
email severity=sev3 alertname=broadcast_cost_80pct
ROUTES
docker run --rm -v "${work}:/c" prom/blackbox-exporter:v0.27.0 --config.file=/c/blackbox.yml --config.check
# the witness's public site (story 50.11): a token in front of everything, and the 401 for a
# request without it runs before anything is served (tests/deny-first.sh)
witness=(docker run --rm -e WITNESS_DOMAIN=witness.example.com -e ACME_EMAIL=ops@example.com -e WITNESS_TOKEN=t
    -v "${here}/Caddyfile.witness:/etc/caddy/Caddyfile:ro" caddy:2-alpine caddy)
"${witness[@]}" validate --config /etc/caddy/Caddyfile --adapter caddyfile
"${witness[@]}" adapt --config /etc/caddy/Caddyfile --adapter caddyfile >"${work}/witness.json"
bash "${here}/../tests/deny-first.sh" "${work}/witness.json" witness.example.com 401 header
echo "monitoring configs: ok"
