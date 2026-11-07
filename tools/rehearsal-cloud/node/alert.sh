#!/usr/bin/env bash
# The rehearsal's pager: an alert is a line the scenario reads.
echo "$(date -u +%FT%TZ) $1 $2" >>/run/maktaba/alerts
