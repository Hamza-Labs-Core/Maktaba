#!/bin/zsh
# Maktaba dev stack on one Mac (plan 03-10): the API, the main worker (every stage but
# transcription; serves the pipeline gRPC for semantic search) and N transcription
# workers (one batched Whisper each: MLX and torch stay in separate processes).
#
#   tools/dev/stack.sh start [N]   N transcription workers (default 2)
#   tools/dev/stack.sh stop
#   tools/dev/stack.sh restart [N]
#   tools/dev/stack.sh api         restart the API alone
#
# Settings come from the environment (defaults in brackets):
#   MAKTABA_DATABASE_URL  [postgresql://maktaba:maktaba@localhost:5432/maktaba]
#   MAKTABA_PATH_MAP      [/Volumes/Originals/=$HOME/nas-nfs/]
#   MAKTABA_LOG_DIR       [$HOME/.maktaba/logs]
#   transcription, per worker (sized for a 96 GB Mac: two workers stay near a third of
#   it, and back off on their own below 25% free):
#   MAKTABA_STT_AUDIO_HOURS [16], MAKTABA_STT_GPU_GB [20], MAKTABA_STT_GPU_CACHE_GB [2],
#   MAKTABA_STT_MIN_FREE_PCT [25], MAKTABA_STT_WORDS [1]
# Memory, GPU and progress are in health_samples / the control centre, not here.
set -u
ROOT=${0:A:h:h:h}
DB=${MAKTABA_DATABASE_URL:-postgresql://maktaba:maktaba@localhost:5432/maktaba}
PM=${MAKTABA_PATH_MAP:-/Volumes/Originals/=$HOME/nas-nfs/}
LOGS=${MAKTABA_LOG_DIR:-$HOME/.maktaba/logs}
export MAKTABA_CHROMA_DIR=${MAKTABA_CHROMA_DIR:-$HOME/.maktaba/chroma}
export MAKTABA_STT_AUDIO_HOURS=${MAKTABA_STT_AUDIO_HOURS:-16}
export MAKTABA_STT_GPU_GB=${MAKTABA_STT_GPU_GB:-20}
export MAKTABA_STT_GPU_CACHE_GB=${MAKTABA_STT_GPU_CACHE_GB:-2}
export MAKTABA_STT_MIN_FREE_PCT=${MAKTABA_STT_MIN_FREE_PCT:-25}
mkdir -p "$LOGS"

stop() {
  pkill -TERM -f "Python -m maktaba_pipeline run" 2>/dev/null
  for p in $(lsof -nP -iTCP:8080 -sTCP:LISTEN -t 2>/dev/null); do kill "$p"; done
  for _ in {1..30}; do pgrep -f "Python -m maktaba_pipeline run" >/dev/null || break; sleep 1; done
  # a transcription worker drains minutes-long jobs on TERM; its claims are reaped anyway
  pkill -KILL -f "Python -m maktaba_pipeline run" 2>/dev/null
  return 0
}

api() {
  # the old API drains open live-update streams before it lets go of the port
  for p in $(lsof -nP -iTCP:8080 -sTCP:LISTEN -t 2>/dev/null); do kill "$p"; done
  for _ in {1..60}; do lsof -nP -iTCP:8080 -sTCP:LISTEN -t >/dev/null 2>&1 || break; sleep 1; done
  for p in $(lsof -nP -iTCP:8080 -sTCP:LISTEN -t 2>/dev/null); do kill -9 "$p"; done
  (cd "$ROOT/api" && DATABASE_URL="${DB/postgresql:/postgres:}?sslmode=disable" \
     MAKTABA_HTTP_ADDR=:8080 MAKTABA_ADMIN_ADDR=:9100 MAKTABA_ENV=dev \
     MAKTABA_PIPELINE_ADDR=127.0.0.1:50051 MAKTABA_SEARCH_SEMANTIC_BUDGET_MS=500 \
     nohup go run . serve >> "$LOGS/api.log" 2>&1 &)
}

start() {
  local n=${1:-2} stamp
  stamp=$(date +%s)  # worker ids are unique per start: the reaper keys claims on them
  api
  (cd "$ROOT/pipeline" && nohup uv run --all-extras python -m maktaba_pipeline run \
     --database-url "$DB" --worker-id "mac-main-$stamp" --path-map "$PM" \
     --grpc-addr 127.0.0.1:50051 --exclude-stages transcribe >> "$LOGS/worker.log" 2>&1 &)
  for k in $(seq 1 "$n"); do
    (cd "$ROOT/pipeline" && nohup uv run --all-extras python -m maktaba_pipeline run \
       --database-url "$DB" --worker-id "mac-stt-$k-$stamp" --path-map "$PM" \
       --stages transcribe >> "$LOGS/worker-stt-$k.log" 2>&1 &)
  done
}

case ${1:-} in
  start) start "${2:-2}" ;;
  stop) stop ;;
  restart) stop; start "${2:-2}" ;;
  api) api ;;
  *) echo "usage: $0 start|stop|restart [transcription workers]" >&2; exit 2 ;;
esac
