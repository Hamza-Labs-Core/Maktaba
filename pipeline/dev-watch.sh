#!/usr/bin/env bash
# Live-reload supervisor for the pipeline service (Story 22.8).
#
# `watchmedo auto-restart` watches src/ for any *.py change and SIGTERMs
# the wrapped process. SIGTERM (not SIGKILL) lets asyncio shut down
# cleanly so any in-flight claims/heartbeats release before re-exec.
#
# The wrapped command (`python -m maktaba_pipeline`) is currently a
# loop-forever stub (Story 22.1 placeholder). Real serve loops land
# under Epic 1–6.

set -euo pipefail

cd /src/pipeline

# Sync deps into the in-image venv (cached on the uv-cache volume).
# We use --extra dev so watchdog/pytest are available; the running
# python is the host's python, not uv's, so watchmedo can re-exec
# the same module path on every change.
echo "[dev-watch] uv sync (this is fast after the first run)"
uv sync --extra dev --frozen 2>/dev/null || uv sync --extra dev

exec watchmedo auto-restart \
  --directory /src/pipeline/src \
  --pattern '*.py' \
  --recursive \
  --signal SIGTERM \
  -- uv run python -m maktaba_pipeline
