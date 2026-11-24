cd /opt/maktaba-relay && docker compose --env-file .env ps --format '{{.Service}} {{.Status}} {{.Ports}}'
docker version --format 'docker {{.Server.Version}}'; docker info --format '{{json .}}' | python3 -c "import json,sys; d=json.load(sys.stdin); print('firewall backend:', d.get('FirewallBackend'))"
grep -E 'DEFAULT_FORWARD_POLICY|IPV6' /etc/default/ufw; update-alternatives --display iptables 2>/dev/null | head -3
echo '--- iptables -S FORWARD'; iptables -S FORWARD | head -30
echo '--- nat PREROUTING/DOCKER'; iptables -t nat -S | grep -E 'DOCKER|PREROUTING' | head -20
echo '--- nft tables'; nft list tables 2>/dev/null
ss -tlnp | grep -E ':(80|443) '
curl -s -o /dev/null -w 'local 443: %{http_code}\n' -k --max-time 5 --resolve staging-api.maktaba.dev:443:127.0.0.1 https://staging-api.maktaba.dev/healthz
