cd /opt/maktaba-relay
docker compose --env-file .env logs --no-log-prefix --since 25m api 2>&1 | grep -E '"path":"/v1/(servers|homes/[^"]+)' | grep -v '"path":"/v1/homes"' | sed -E 's/"request_id":"[^"]*",//; s/"remote_ip":"[^"]*",//' | cut -c1-260 | tail -25
echo ---; docker compose --env-file .env logs --no-log-prefix --since 25m api 2>&1 | grep -iE 'link|assert' | grep -v '"msg":"http"' | cut -c1-300 | tail -10
