#!/usr/bin/env bash
# Runbook action: the forced failover (Epic 50 story 50.12, docs/runbooks/cloud/forced-failover.md),
# for a leader that is sick but alive (a stuck disk, an OOM loop): Patroni fails over to the
# standby, which promotes; the old leader demotes (its relay stops by the leader guard). With
# --stop-old the old node's Patroni is also stopped (a node too sick to demote cleanly), so it
# can never take the lead back until someone starts it.
#
#   failover.sh [--stop-old]
#
# Env: see common.sh; PATRONICTL (patronictl -c /etc/patroni/patroni.yml), STOP_OLD (the command
# that stops Patroni on the old node: systemctl stop patroni), WAIT (120).
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=deploy/cloud-ha/runbook/common.sh
. "${here}/common.sh"
stop_old=""
case "${1:-}" in "") ;; --stop-old) stop_old=yes ;; *) die "usage: failover.sh [--stop-old]" ;; esac
ctl="${PATRONICTL:-patronictl -c /etc/patroni/patroni.yml}"

find_pair || die "no node leads: Patroni already failed over or both are down (see restore.md)"
# the candidate's member name, from Patroni itself (the one that does not lead)
candidate="$(on "${standby}" "${SUDO} ${ctl} list -f json" | jq -r '.[] | select(.Role != "Leader" and .Role != "leader") | .Member' | head -n1)"
[ -n "${candidate}" ] || die "Patroni lists no replica: no failover is possible"
log "${leader} leads; failing over to ${candidate} on ${standby}"
on "${standby}" "${SUDO} ${ctl} failover --candidate ${candidate} --force" || die "Patroni refused the failover (its reason is above)"
wait_leads "${standby}" "${WAIT:-120}" || die "${standby} does not lead after ${WAIT:-120} s"
log "${standby} leads"
if [ -n "${stop_old}" ]; then
    log "stopping Patroni on ${leader}, the old leader"
    on "${leader}" "timeout 60 ${SUDO} ${STOP_OLD:-systemctl stop patroni}" || log "could not stop Patroni on ${leader}: power it off in the Hetzner console"
fi
if leads "${leader}"; then die "${leader} still says it leads: fence it by hand (power it off)"; fi
log "done: ${leader} no longer leads"
