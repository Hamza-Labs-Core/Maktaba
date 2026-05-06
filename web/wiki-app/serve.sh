#!/usr/bin/env bash
# Convenience launcher for the Maktaba wiki app.
#   ./serve.sh         → npm run dev (Vite dev server, hot reload)
#   ./serve.sh build   → npm run build && npm run preview (production preview)
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$HERE"

if [ ! -d node_modules ]; then
  echo "Installing deps…"
  npm install
fi

case "${1:-dev}" in
  build)
    npm run build
    npm run preview
    ;;
  *)
    npm run dev
    ;;
esac
