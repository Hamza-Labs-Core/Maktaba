#!/usr/bin/env bash
# Remove this machine's Maktaba node worker (macOS and Linux, Epic 31).
#
#   uninstall-node.sh [--system] [--purge] [--dry-run]
#
# Stops and removes the services and the worker's Python environment; --purge also
# removes the logs and the node*.env settings. The node config (node.json) is kept:
# remove the machine on the home (Admin -> Machines), then delete it by hand.
# Same as: install-node.sh --uninstall ...
set -euo pipefail
exec "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/install-node.sh" --uninstall "$@"
