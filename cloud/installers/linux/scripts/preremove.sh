#!/bin/sh
# Pre-removal: stop the service. Data in /var/lib/maktaba is preserved.
set -e
systemctl stop maktaba-server || true
systemctl disable maktaba-server || true
