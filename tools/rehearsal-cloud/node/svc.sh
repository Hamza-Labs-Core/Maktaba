#!/usr/bin/env bash
# The rehearsal's systemctl for the units the move to the database nodes starts and stops
# (deploy/cloud-ha/bin/migrate-db-nodes, SVC): svc.sh start|stop|disable patroni|etcd|pgbouncer.
# A stopped unit stays stopped: the entrypoint starts nothing again.
set -uo pipefail
action="${1:?}" unit="${2:?}"
case "${action}:${unit}" in
    start:etcd)
        pgrep -x etcd >/dev/null || (setsid etcd --config-file /etc/etcd/etcd.conf.yml >>/var/log/etcd.log 2>&1 &)
        ;;
    stop:etcd)
        pkill -TERM -x etcd || true
        ;;
    start:patroni)
        pgrep -f "patroni /etc/patroni/patroni.yml" >/dev/null ||
            (setsid gosu postgres env CLOUD_HA_ENV=/etc/maktaba/cloud-ha.env patroni /etc/patroni/patroni.yml >>/var/log/patroni.log 2>&1 &)
        ;;
    stop:patroni)
        # Patroni stops Postgres on its way out
        pkill -TERM -f "patroni /etc/patroni/patroni.yml" || true
        for _ in $(seq 1 60); do pgrep -f "patroni /etc/patroni/patroni.yml" >/dev/null || break; sleep 0.5; done
        ;;
    start:pgbouncer)
        pgrep -x pgbouncer >/dev/null || gosu postgres pgbouncer -d /etc/pgbouncer/pgbouncer.ini
        ;;
    stop:pgbouncer)
        pkill -TERM -x pgbouncer || true
        ;;
    disable:*) ;;
    *) echo "svc.sh: ${action} ${unit}?" >&2; exit 2 ;;
esac
