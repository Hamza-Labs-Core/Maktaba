#!/usr/bin/env bash
# Runbook action: the planned switchover (Epic 50 story 50.12, docs/runbooks/cloud/switchover.md).
# Finds the leader and runs bin/switchover there: its checks (the standby streaming with no lag,
# its Caddy answering, the 48-hour notice), then Patroni's switchover; the Floating IP and the
# relay follow the new leader (on-role-change). Waits until the other node leads.
#
#   switchover.sh --announced|--unannounced
#
# Env: see common.sh; SWITCHOVER (the command on the node, default switchover), WAIT (180).
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=common.sh
. "${here}/common.sh"
case "${1:-}" in --announced|--unannounced) ;; *) die "usage: switchover.sh --announced|--unannounced" ;; esac

find_pair || die "no node leads: nothing to switch over (see forced-failover.md or restore.md)"
log "${leader} leads; switching to ${standby} ($1)"
on "${leader}" "${SUDO} ${SWITCHOVER:-switchover} $1" || die "the switchover was refused or failed on ${leader} (its reason is above)"
wait_leads "${standby}" "${WAIT:-180}" || die "${standby} does not lead after ${WAIT:-180} s"
log "done: ${standby} leads"
