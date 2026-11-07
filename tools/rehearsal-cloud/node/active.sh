#!/usr/bin/env bash
# The rehearsal's maktaba-cloud-active.target: on-role-change runs it with start|stop.
echo "$1" >/run/maktaba/active
echo "$(date -u +%FT%TZ) active: $1" >>/run/maktaba/active.log
