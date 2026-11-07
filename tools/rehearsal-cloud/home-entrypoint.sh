#!/bin/sh
# The rehearsal home (Dockerfile.home): its only way out is the router (ROUTER, the LAN side of
# the scenario's NAT), as at a real home; then the relay agent and the home's direct access.
# Either exiting stops the container.
set -eu
ip route replace default via "${ROUTER:?}"
maktaba-cloudlink &
agent=$!
rehearsal-direct-home &
home=$!
while kill -0 "${agent}" 2>/dev/null && kill -0 "${home}" 2>/dev/null; do sleep 1; done
echo "home-entrypoint: a process exited (agent ${agent}, home ${home})" >&2
kill "${agent}" "${home}" || true
exit 1
