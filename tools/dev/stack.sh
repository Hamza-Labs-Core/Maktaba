#!/bin/zsh
# Maktaba dev stack on one Mac (plan 03-10): the API, the main worker (every stage but
# transcription; serves the pipeline gRPC for semantic search) and N transcription
# workers (one batched Whisper each: MLX and torch stay in separate processes).
#
#   tools/dev/stack.sh start [N]   N transcription workers (default 1)
#   tools/dev/stack.sh stop
#   tools/dev/stack.sh restart [N]
#
# Settings come from the environment (defaults in brackets):
#   MAKTABA_DATABASE_URL  [postgresql://maktaba:maktaba@localhost:5432/maktaba]
#   MAKTABA_PATH_MAP      [/Volumes/Originals/=$HOME/nas-nfs/]
#   MAKTABA_LOG_DIR       [$HOME/.maktaba/logs]
#   transcription: MAKTABA_STT_AUDIO_HOURS [6], MAKTABA_STT_GPU_GB [24],
#   MAKTABA_STT_GPU_CACHE_GB [2], MAKTABA_STT_MIN_FREE_PCT [20], MAKTABA_STT_WORDS [1]
# Memory, GPU and progress are in health_samples / the control centre, not here.
set -u
ROOT=${0:A:h:h:h}
DB=${MAKTABA_DATABASE_URL:-postgresql://maktaba:maktaba@localhost:5432/maktaba}
PM=${MAKTABA_PATH_MAP:-/Volumes/Originals/=$HOME/nas-nfs/}
LOGS=${MAKTABA_LOG_DIR:-$HOME/.maktaba/logs}
export MAKTABA_CHROMA_DIR=${MAKTABA_CHROMA_DIR:-$HOME/.maktaba/chroma}
mkdir -p "$LOGS"

stop() {
  pkill -TERM -f "Python -m maktaba_pipeline run" 2>/dev/null
  for p in $(lsof -nP -iTCP:8080 -sTCP:LISTEN -t 2>/dev/null); do kill "$p"; done
  for _ in {1..30}; do pgrep -f "Python -m maktaba_pipeline run" >/dev/null || break; sleep 1; done
  # a transcription worker drains minutes-long jobs on TERM; its claims are reaped anyway
  pkill -KILL -f "Python -m maktaba_pipeline run" 2>/dev/null
  return 0
}

start() {
  local n=${1:-1} stamp
  stamp=$(date +%s)  # worker ids are unique per start: the reaper keys claims on them
  (cd "$ROOT/api" && DATABASE_URL="${DB/postgresql:/postgres:}?sslmode=disable" \
     MAKTABA_HTTP_ADDR=:8080 MAKTABA_ADMIN_ADDR=:9100 MAKTABA_ENV=dev \
     MAKTABA_PIPELINE_ADDR=127.0.0.1:50051 MAKTABA_SEARCH_SEMANTIC_BUDGET_MS=500 \
     nohup go run . serve >> "$LOGS/api.log" 2>&1 &)
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
  start) start "${2:-1}" ;;
  stop) stop ;;
  restart) stop; start "${2:-1}" ;;
  *) echo "usage: $0 start|stop|restart [transcription workers]" >&2; exit 2 ;;
esac
