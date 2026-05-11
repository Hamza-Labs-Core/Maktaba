#!/bin/sh
# Post-removal: announce that data was preserved. Full wipe is an
# explicit opt-in via `maktaba-server uninstall --purge`.
set -e
systemctl daemon-reload || true
echo "Maktaba server removed. Library data is preserved at /var/lib/maktaba."
echo "Run 'rm -rf /var/lib/maktaba /etc/maktaba' to fully wipe."
