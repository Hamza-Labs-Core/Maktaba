#!/bin/zsh
# Maktaba dev stack on one Mac (plan 03-10): the API, the streaming service (Live TV
# and video streams), the main worker (every stage but transcription; serves the
# pipeline gRPC for semantic search) and N transcription workers (one batched Whisper
# each: MLX and torch stay in separate processes). The API and streaming run as
# maktaba-server runs them on every OS: on the machine, one database, one stream
# signing key file (the API signs stream URLs, streaming checks them).
#
#   tools/dev/stack.sh start [N]   N transcription workers (default 2)
#   tools/dev/stack.sh stop
#   tools/dev/stack.sh restart [N]
#   tools/dev/stack.sh api         restart the API alone
#   tools/dev/stack.sh streaming   restart the streaming service alone
#
# Settings come from the environment (defaults in brackets):
#   MAKTABA_DATABASE_URL  [postgresql://maktaba:maktaba@localhost:5432/maktaba]
#   MAKTABA_PATH_MAP      [/Volumes/Originals/=$HOME/nas-nfs/] (the workers', and the API's: the
#                         cluster hub writes <share>/maktaba/cluster through it)
#   MAKTABA_LOG_DIR       [$HOME/.maktaba/logs]
#   MAKTABA_STREAM_SIGNING_KEY [$HOME/.maktaba/stream-signing.key] (created by the API)
#   transcription, per worker (sized for a 96 GB Mac: two workers stay near a third of
#   it, and back off on their own below 25% free):
#   MAKTABA_STT_AUDIO_HOURS [16], MAKTABA_STT_GPU_GB [20], MAKTABA_STT_GPU_CACHE_GB [2],
#   MAKTABA_STT_MIN_FREE_PCT [25], MAKTABA_STT_WORDS [1]
# Memory, GPU and progress are on the Processing page and Admin > Machines, not here;
# every process's logs and metrics are also in <share>/maktaba/cluster/<ip>/.
set -u
ROOT=${0:A:h:h:h}
DB=${MAKTABA_DATABASE_URL:-postgresql://maktaba:maktaba@localhost:5432/maktaba}
PM=${MAKTABA_PATH_MAP:-/Volumes/Originals/=$HOME/nas-nfs/}
LOGS=${MAKTABA_LOG_DIR:-$HOME/.maktaba/logs}
KEY=${MAKTABA_STREAM_SIGNING_KEY:-$HOME/.maktaba/stream-signing.key}
PGURL="${DB/postgresql:/postgres:}?sslmode=disable"
export MAKTABA_CHROMA_DIR=${MAKTABA_CHROMA_DIR:-$HOME/.maktaba/chroma}
export MAKTABA_STT_AUDIO_HOURS=${MAKTABA_STT_AUDIO_HOURS:-16}
export MAKTABA_STT_GPU_GB=${MAKTABA_STT_GPU_GB:-20}
export MAKTABA_STT_GPU_CACHE_GB=${MAKTABA_STT_GPU_CACHE_GB:-2}
export MAKTABA_STT_MIN_FREE_PCT=${MAKTABA_STT_MIN_FREE_PCT:-25}
mkdir -p "$LOGS"

# PIDs listening on the given TCP ports. Not lsof: on this Mac it hangs in the kernel
# on the NFS share's open files (even with -b), which left the whole stack stopped.
port_pids() {
  local re; re=$(printf '|\\.%s' "$@"); re="(${re#|}) "
  netstat -anv -p tcp 2>/dev/null | grep -E " LISTEN " | grep -E "$re" \
    | sed -E 's/.*:([0-9]+) +[0-9]{5} +[0-9a-f]{8} .*/\1/' | grep -E '^[0-9]+$' | sort -u
}

stop() {
  pkill -TERM -f "Python -m maktaba_pipeline run" 2>/dev/null
  for p in $(port_pids 8080); do kill "$p"; done
  for p in $(streaming_pids); do kill "$p"; done
  for _ in {1..30}; do pgrep -f "Python -m maktaba_pipeline run" >/dev/null || break; sleep 1; done
  # a transcription worker drains minutes-long jobs on TERM; its claims are reaped anyway
  pkill -KILL -f "Python -m maktaba_pipeline run" 2>/dev/null
  return 0
}

api_pids() { port_pids 8080 9100; }

api() {
  # the old API drains open live-update streams before it lets go of its ports
  # (public 8080 and admin 9100: the new one exits if either is still taken)
  for p in $(api_pids); do kill "$p"; done
  for _ in {1..60}; do [[ -z $(api_pids) ]] && break; sleep 1; done
  for p in $(api_pids); do kill -9 "$p"; done
  (cd "$ROOT/api" && DATABASE_URL="$PGURL" \
     MAKTABA_HTTP_ADDR=:8080 MAKTABA_ADMIN_ADDR=:9100 MAKTABA_ENV=dev \
     MAKTABA_PIPELINE_ADDR=127.0.0.1:50051 MAKTABA_SEARCH_SEMANTIC_BUDGET_MS=500 \
     MAKTABA_STREAMING_ADDR=127.0.0.1:9050 MAKTABA_STREAMING_HTTP_URL=http://127.0.0.1:8081 \
     MAKTABA_STREAM_SIGNING_KEY="$KEY" MAKTABA_PATH_MAP="$PM" \
     nohup go run . serve >> "$LOGS/api.log" 2>&1 &)
}

# by port, and by name: a stopping instance lets go of its ports before it exits, and
# one that never exits (seen once) must not be left running beside the new one
streaming_pids() {
  { port_pids 8081 9101 9050; pgrep -f "[/]streaming serve"; } 2>/dev/null | sort -u
}

streaming() {
  for p in $(streaming_pids); do kill "$p"; done
  for _ in {1..30}; do [[ -z $(streaming_pids) ]] && break; sleep 1; done
  for p in $(streaming_pids); do kill -9 "$p"; done
  # the API creates the key on its first start: wait for it, streaming only reads it
  for _ in {1..60}; do [[ -s $KEY ]] && break; sleep 1; done
  (cd "$ROOT/streaming" && DATABASE_URL="$PGURL" MAKTABA_DATABASE_URL="$PGURL" \
     MAKTABA_HTTP_ADDR=:8081 MAKTABA_ADMIN_ADDR=:9101 MAKTABA_STREAMING_GRPC_ADDR=:9050 \
     MAKTABA_API_URL=http://127.0.0.1:8080 MAKTABA_GRPC_PEERS=api=127.0.0.1:9100 \
     MAKTABA_STREAM_SIGNING_KEY="$KEY" MAKTABA_ENV=dev \
     MAKTABA_CACHE_ROOT="${MAKTABA_CACHE_ROOT:-$HOME/.maktaba/cache/streaming}" \
     nohup go run . serve >> "$LOGS/streaming.log" 2>&1 &)
}

start() {
  local n=${1:-2} stamp
  stamp=$(date +%s)  # worker ids are unique per start: the reaper keys claims on them
  api
  streaming
  (cd "$ROOT/pipeline" && nohup uv run --all-extras python -m maktaba_pipeline run \
     --database-url "$DB" --worker-id "mac-main-$stamp" --path-map "$PM" \
     --grpc-addr 127.0.0.1:50051 --exclude-stages transcribe >> "$LOGS/worker.log" 2>&1 &)
  for k in $(seq 1 "$n"); do
    # the slot names the worker's stt.* metrics, so a restart continues its series
    (cd "$ROOT/pipeline" && MAKTABA_STT_SLOT="stt-$k" nohup uv run --all-extras python -m maktaba_pipeline run \
       --database-url "$DB" --worker-id "mac-stt-$k-$stamp" --path-map "$PM" \
       --stages transcribe >> "$LOGS/worker-stt-$k.log" 2>&1 &)
  done
}

case ${1:-} in
  start) start "${2:-2}" ;;
  stop) stop ;;
  restart) stop; start "${2:-2}" ;;
  api) api ;;
  streaming) streaming ;;
  *) echo "usage: $0 start|stop|restart [N] | api | streaming" >&2; exit 2 ;;
esac
