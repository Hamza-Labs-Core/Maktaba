#!/usr/bin/env bash
# tools/media-e2e.sh — the media e2e tier: the Go tests that need a real
# ffmpeg/ffprobe (and, for the two end-to-end ones, Postgres), which every
# other tier skips. Run by `make test-media-e2e` (CI job media-e2e).
#
# Needs: ffmpeg + ffprobe on PATH, an Arabic font for the strip tests
# (fonts-noto-core), and MAKTABA_TEST_PG_URL naming a Postgres whose user
# may create databases and roles (each test makes its own scratch database).
#
# MAKTABA_REQUIRE_FFMPEG=1 turns the tests' "tool missing" skips into
# failures, and this script fails on any other skip too, and unless every
# test below reported PASS: a green run means each one really ran.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
export MAKTABA_REQUIRE_FFMPEG=1

if [[ -z "${MAKTABA_TEST_PG_URL:-}" ]]; then
  echo "::error::MAKTABA_TEST_PG_URL is required for the media e2e tier"
  exit 1
fi
for b in ffmpeg ffprobe; do
  command -v "$b" >/dev/null || { echo "::error::$b not on PATH"; exit 1; }
done
ffmpeg -hide_banner -version | head -1

# module | package | tests (exact names)
suites=(
  "api|./internal/tvremote/|TestRemoteEndToEnd TestSimulcastReachesRTMPDestination"
  "api|./internal/tvremote/rtmptest/|TestFFmpegPublishes TestTapRepublishes"
  "api|./internal/tvremote/|TestSwapRollbackEndToEnd"
  "streaming|.|TestTwoHostsOneMachine"
  "streaming|./internal/channel/|TestStation_RealFFmpeg_ContinuousWallClock TestEngine_RealFFmpeg_LazyHLSAndSharedTS TestStation_RealFFmpeg_MuxerRestart TestBlockArgs_RealFFmpegVariants TestStripRendersArabic TestStripRendersPages"
)

log="$(mktemp)"
rc=0
for s in "${suites[@]}"; do
  IFS='|' read -r mod pkg tests <<<"$s"
  run="^($(echo "$tests" | tr ' ' '|'))\$"
  echo "==> media-e2e ($mod $pkg): $tests"
  set +e
  ( cd "$REPO_ROOT/$mod" && go test -count=1 -v -timeout=8m -run "$run" "$pkg" ) 2>&1 | tee -a "$log"
  st=${PIPESTATUS[0]}
  set -e
  if [[ "$st" != 0 ]]; then
    echo "::error::go test failed in $mod $pkg (exit $st)"
    rc=1
  fi
  for t in $tests; do
    if ! grep -qE -- "^--- PASS: $t \(" "$log"; then
      echo "::error::$t did not pass (it failed, skipped or never ran)"
      rc=1
    fi
  done
done

if grep -E -- "^\s*--- SKIP" "$log"; then
  echo "::error::the media e2e tier skipped tests (above): every one must run here"
  rc=1
fi
rm -f "$log"
exit "$rc"
