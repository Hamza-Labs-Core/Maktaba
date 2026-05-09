#!/usr/bin/env bash
# tools/verify-reproducibility.sh — Story 22.2 TC1.
#
# Builds the Go binaries twice into separate output directories and
# diffs the sha256 sums. A non-zero exit means the build is no longer
# byte-stable — usually because someone re-introduced a non-deterministic
# input (wall-clock time in -X, missing -trimpath, missing -buildid=,
# CGO leaking absolute paths, …).
#
# We only check Go binaries here. Web bundle reproducibility lands when
# the real Vite config does (Story 22.2 plan §2.4 + Story 09.x).

set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "${ROOT}"

A=$(mktemp -d -t maktaba-repro-a.XXXXXX)
B=$(mktemp -d -t maktaba-repro-b.XXXXXX)
trap 'rm -rf "${A}" "${B}"' EXIT

build_into() {
	local stamp_dir=$1
	# Force the same SOURCE_DATE_EPOCH for both runs so the only thing
	# that could differ is genuine non-determinism.
	SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH:-$(git log -1 --pretty=%ct)}" \
	make --no-print-directory build-go
	mkdir -p "${stamp_dir}"
	# Snapshot binaries into the per-run directory before the next run
	# overwrites them.
	cp api/bin/* "${stamp_dir}/"
	cp streaming/bin/* "${stamp_dir}/"
}

echo "==> first build"
build_into "${A}"

# A small sleep so wall-clock-derived timestamps would diverge if any
# leaked into the build — this guards against future regressions where
# someone replaces SOURCE_DATE_EPOCH with $(date).
sleep 2

echo "==> second build"
build_into "${B}"

echo "==> diffing sha256 sums"
diff \
	<(cd "${A}" && shasum -a 256 ./* | awk '{print $1, "  ", $2}' | sort) \
	<(cd "${B}" && shasum -a 256 ./* | awk '{print $1, "  ", $2}' | sort)

echo "==> reproducibility verified"
