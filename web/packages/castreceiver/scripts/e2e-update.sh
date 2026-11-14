#!/usr/bin/env bash
# Renders the receiver's visual references (e2e/__screenshots__) in the image the CI job runs
# in (mcr.microsoft.com/playwright:v<version>-noble, linux/amd64), never on the host: fonts and
# antialiasing differ between systems. Build first (npm run build).
#
#   bash scripts/e2e-update.sh
set -euo pipefail

pkg="$(cd "$(dirname "$0")/.." && pwd)"
cd "$pkg"
version="$(node -p "require('./package.json').devDependencies['@playwright/test']")"
image="mcr.microsoft.com/playwright:v${version}-noble"

[ -f dist/r/index.html ] || { echo "e2e-update: no dist/: run npm run build first" >&2; exit 1; }
command -v docker > /dev/null || { echo "e2e-update: needs Docker" >&2; exit 1; }

echo "e2e-update: rendering the references in $image (linux/amd64)"
docker run --rm --platform linux/amd64 --ipc=host --init \
  -e CI= -e CAST_VISUAL=1 \
  -v "$pkg:/site" -v maktaba-castreceiver-e2e-node-modules:/site/node_modules \
  -w /site "$image" \
  bash -c 'npm ci --no-audit --no-fund --loglevel=error && npx playwright test --update-snapshots=changed --reporter=line "$@"' \
  e2e-update "$@"
