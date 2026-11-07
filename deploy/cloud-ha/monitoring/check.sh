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
docker run --rm -v "${here}:/r" -w /r --entrypoint promtool "${prom}" test rules alerts-test.yml backup-rules-test.yml
docker run --rm -v "${work}:/c" --entrypoint amtool prom/alertmanager:v0.28.1 check-config /c/alertmanager.yml
docker run --rm -v "${work}:/c" prom/blackbox-exporter:v0.27.0 --config.file=/c/blackbox.yml --config.check
# the witness's public site (story 50.11): a token in front of everything
docker run --rm -e WITNESS_DOMAIN=witness.example.com -e ACME_EMAIL=ops@example.com -e WITNESS_TOKEN=t \
    -v "${here}/Caddyfile.witness:/etc/caddy/Caddyfile:ro" caddy:2-alpine caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile
echo "monitoring configs: ok"
