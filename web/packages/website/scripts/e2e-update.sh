#!/usr/bin/env bash
# maktaba.tv's visual references (story 60.04): `npm run e2e:update` (or
# `pnpm -C web/packages/website e2e:update`) renders e2e/__screenshots__/ in the image the CI job
# runs in, mcr.microsoft.com/playwright:v<the pinned @playwright/test>-noble (Ubuntu 24.04,
# linux/amd64 as the ubuntu-24.04 runner), so a reference never comes from a Mac's fonts or
# renderer. Only references whose pixels changed are rewritten (--update-snapshots=changed).
#
#   npm run build && npm run e2e:update [-- <playwright args, e.g. -g home>]
#
# Needs Docker. The site's node_modules for Linux live in a Docker volume
# (maktaba-website-e2e-node-modules), never over the host's. wrangler dev runs inside the
# container (playwright.config.ts's webServer); its log is test-results/worker.log.
set -euo pipefail

pkg="$(cd "$(dirname "$0")/.." && pwd)"
cd "$pkg"
version="$(node -p "require('./package.json').devDependencies['@playwright/test']")"
image="mcr.microsoft.com/playwright:v${version}-noble"

if [ ! -f dist/index.html ]; then
  echo "e2e-update: no dist/: run npm run build first" >&2
  exit 1
fi
command -v docker > /dev/null || { echo "e2e-update: needs Docker" >&2; exit 1; }

echo "e2e-update: rendering the references in $image (linux/amd64)"
docker run --rm --platform linux/amd64 --ipc=host --init \
  -e CI= -e WEBSITE_VISUAL=1 \
  -v "$pkg:/site" -v maktaba-website-e2e-node-modules:/site/node_modules \
  -w /site "$image" \
  bash -c 'npm ci --no-audit --no-fund --loglevel=error && npx playwright test e2e/visual.spec.ts --update-snapshots=changed --reporter=line "$@"' \
  e2e-update "$@"

# each reference: a PNG under 1 MB with no metadata chunk (scripts/references.test.mjs)
node --test scripts/references.test.mjs
