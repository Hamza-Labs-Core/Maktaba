#!/usr/bin/env sh
# Dev server entrypoint for the web frontend (Story 22.8).
#
# Two phases:
#
#   1. If web/ has a `dev` script in package.json, run that. Epic 11
#      will land vite as a real dep with `pnpm dev` wired up; this
#      branch lights up automatically when that happens.
#   2. Otherwise (current stub-era state), run the globally-installed
#      vite directly against /src/web. vite happily serves a directory
#      with no vite.config — the user can save .ts/.tsx files and
#      see the change in the browser within ~1 s.
#
# `--host 0.0.0.0` is required so the dev server is reachable from the
# Docker network bridge; HMR over the websocket uses VITE_HMR_HOST
# (set in docker-compose.dev.yml) to point the browser at the host
# port mapping.

set -eu

cd /src/web

if [ -f package.json ] && node -e "process.exit(require('./package.json').scripts && require('./package.json').scripts.dev ? 0 : 1)" 2>/dev/null; then
  echo "[web dev] running 'pnpm dev' from package.json"
  pnpm install --prefer-offline --no-frozen-lockfile
  exec pnpm dev --host 0.0.0.0
fi

echo "[web dev] no 'dev' script in package.json yet; serving via global vite"
echo "[web dev] (Epic 11 will replace this branch with a real Vite/React app)"
exec vite --host 0.0.0.0 --port 5173 .
