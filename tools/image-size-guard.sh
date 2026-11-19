#!/usr/bin/env bash
# Image size guard for the four compose images (Story 22.3 AC4 / TC3).
#
# Sums the unpacked layers (`docker history`) of each {api,streaming,pipeline,web}
# image and fails if any of them is over its budget. CI's `images` job
# (.github/workflows/_images.yml) builds the four images and runs this;
# a regression's line names the image, its size, its budget and the
# overshoot, so the offending pull request sees how much it went over.
#
# Override the registry or version via env:
#
#   MAKTABA_REGISTRY=ghcr.io/maktaba \
#   MAKTABA_VERSION=v0.1.0 \
#   tools/image-size-guard.sh
#
# Limits track AC4, set on 2026-11-19 from the images as CI builds them on linux/amd64
# and linux/arm64 (the larger of the two, +~10%, rounded up to a whole MiB) where the
# original targets did not hold:
#   api       <=   60 MiB  (measured 54.7 amd64 / 50.7 arm64)
#   streaming <=   80 MiB  (FFmpeg static excluded, per AC4; measured 30.5 / 29.0)
#   pipeline  <= 1200 MiB  (Whisper + Chroma; measured 682.4 / 643.5)
#   web       <=   88 MiB  (caddy:2-alpine base + the SPA; measured 79.7 / 77.1)
#
# Plain bash 3 (no associative arrays), so it runs on macOS's /bin/bash too.
# Its test: tools/ci/tests/test_image_size_guard.py.

set -euo pipefail

REGISTRY="${MAKTABA_REGISTRY:-ghcr.io/maktaba}"
VERSION="${MAKTABA_VERSION:-dev}"
MIB=$((1024 * 1024))

max_bytes() {
    case "$1" in
        api) echo $((60 * MIB)) ;;
        streaming) echo $((80 * MIB)) ;;
        pipeline) echo $((1200 * MIB)) ;;
        web) echo $((88 * MIB)) ;;
        *) echo "image-size-guard: no budget for $1" >&2; return 1 ;;
    esac
}

human() {
    # Prints bytes as MiB to one decimal — readable in a CI log line.
    awk -v b="$1" 'BEGIN { printf "%.1f MiB", b / (1024*1024) }'
}

failed=0
for svc in api streaming pipeline web; do
    image="${REGISTRY}/${svc}:${VERSION}"
    # The sum of the image's unpacked layers, from its history: `docker image inspect`'s
    # .Size also counts the compressed content on a containerd image store (Docker
    # Desktop's default), which reads the same image as ~40% larger than overlay2 does.
    size=$(docker history --no-trunc --human=false --format '{{.Size}}' "$image" 2>/dev/null \
        | awk '{ t += $1 } END { if (NR) print t }' || echo "")
    if ! [[ "$size" =~ ^[0-9]+$ ]]; then
        printf "MISS %-9s image=%s (not built locally)\n" "$svc" "$image"
        failed=1
        continue
    fi
    max=$(max_bytes "$svc")
    if (( size > max )); then
        delta=$(( size - max ))
        printf "FAIL %-9s size=%s max=%s overshoot=%s image=%s\n" \
            "$svc" "$(human "$size")" "$(human "$max")" "$(human "$delta")" "$image"
        if [[ -n "${GITHUB_ACTIONS:-}" ]]; then
            echo "::error title=Image over its size budget::${svc} is $(human "$size"), $(human "$delta") over its $(human "$max") budget (story 22.3 AC4)"
        fi
        failed=1
    else
        slack=$(( max - size ))
        printf "OK   %-9s size=%s max=%s slack=%s image=%s\n" \
            "$svc" "$(human "$size")" "$(human "$max")" "$(human "$slack")" "$image"
    fi
done

exit "$failed"
