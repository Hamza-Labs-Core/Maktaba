#!/usr/bin/env bash
# Image size guard for the four compose images (Story 22.3 AC4 / TC3).
#
# Reads `docker image inspect` for each {api,streaming,pipeline,web}
# image and fails if any of them is over its budget. Wired into CI's
# build-artifacts gate; the regression message includes the delta so
# the offending PR can see exactly how much it overshot.
#
# Override the registry or version via env:
#
#   MAKTABA_REGISTRY=ghcr.io/maktaba \
#   MAKTABA_VERSION=v0.1.0 \
#   tools/image-size-guard.sh
#
# Limits track AC4:
#   api       <=   60 MiB
#   streaming <=   80 MiB  (FFmpeg static excluded, per AC4)
#   pipeline  <= 1200 MiB  (Whisper + Chroma)
#   web       <=   30 MiB

set -euo pipefail

REGISTRY="${MAKTABA_REGISTRY:-ghcr.io/maktaba}"
VERSION="${MAKTABA_VERSION:-dev}"

declare -A MAX_BYTES=(
    [api]=$((60 * 1024 * 1024))
    [streaming]=$((80 * 1024 * 1024))
    [pipeline]=$((1200 * 1024 * 1024))
    [web]=$((30 * 1024 * 1024))
)

human() {
    # Prints bytes as MiB to one decimal — readable in a CI log line.
    awk -v b="$1" 'BEGIN { printf "%.1f MiB", b / (1024*1024) }'
}

failed=0
for svc in api streaming pipeline web; do
    image="${REGISTRY}/${svc}:${VERSION}"
    size=$(docker image inspect "$image" --format '{{.Size}}' 2>/dev/null || echo "")
    if [[ -z "$size" ]]; then
        printf "MISS %-9s image=%s (not built locally)\n" "$svc" "$image"
        failed=1
        continue
    fi
    max=${MAX_BYTES[$svc]}
    if (( size > max )); then
        delta=$(( size - max ))
        printf "FAIL %-9s size=%s max=%s overshoot=%s image=%s\n" \
            "$svc" "$(human "$size")" "$(human "$max")" "$(human "$delta")" "$image"
        failed=1
    else
        slack=$(( max - size ))
        printf "OK   %-9s size=%s max=%s slack=%s image=%s\n" \
            "$svc" "$(human "$size")" "$(human "$max")" "$(human "$slack")" "$image"
    fi
done

exit "$failed"
