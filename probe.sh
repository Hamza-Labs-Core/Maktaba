cd /opt/maktaba-relay && docker compose --env-file .env down 2>&1 | tail -5; docker network ls | grep maktaba || echo "network gone"; docker volume ls | grep maktaba
