#!/usr/bin/env bash
# tools/sbom.sh — generate CycloneDX SBOMs for every Maktaba component.
#
# Story 22.2 stub. We emit SBOMs but do NOT gate on CVEs here — that
# lands in Story 23.7 (security-supply-chain). The SBOMs themselves are
# attached to releases by Story 22.5 so downstream consumers can run
# their own scanners.
#
# Tools (all required to be on PATH; install hints in CONTRIBUTING.md):
#   cyclonedx-gomod  -> Go modules (https://github.com/CycloneDX/cyclonedx-gomod)
#   cyclonedx-py     -> Python deps via uv lockfile (pip install cyclonedx-bom)
#   cyclonedx-npm    -> Web deps via pnpm lockfile (npm i -g @cyclonedx/cyclonedx-npm)
#
# If a tool is missing we warn and skip that component rather than fail —
# this is a stub, not a hard CI gate. Story 23.7 promotes this to a gate.

set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
OUT="${ROOT}/artifacts/sbom"
mkdir -p "${OUT}"

have() { command -v "$1" >/dev/null 2>&1; }

emit_go() {
	local mod=$1
	local out="${OUT}/${mod}.cdx.json"
	if ! have cyclonedx-gomod; then
		echo "[sbom] cyclonedx-gomod missing; skipping ${mod}" >&2
		return 0
	fi
	echo "[sbom] go module: ${mod} -> ${out}"
	(cd "${ROOT}/${mod}" && cyclonedx-gomod mod -licenses -json -output "${out}")
}

emit_python() {
	local out="${OUT}/pipeline.cdx.json"
	if ! have cyclonedx-py; then
		echo "[sbom] cyclonedx-py missing; skipping pipeline" >&2
		return 0
	fi
	echo "[sbom] python pipeline -> ${out}"
	# uv exports a requirements.txt-compatible view that cyclonedx-py
	# can consume; we don't keep the intermediate file around.
	local reqs
	reqs=$(mktemp)
	trap 'rm -f "${reqs}"' RETURN
	(cd "${ROOT}/pipeline" && uv export --frozen --no-hashes --format requirements-txt > "${reqs}")
	cyclonedx-py requirements --input-file "${reqs}" --output-format json --output-file "${out}"
}

emit_web() {
	local out="${OUT}/web.cdx.json"
	if ! have cyclonedx-npm; then
		echo "[sbom] cyclonedx-npm missing; skipping web" >&2
		return 0
	fi
	echo "[sbom] web -> ${out}"
	(cd "${ROOT}/web" && cyclonedx-npm --output-format json --output-file "${out}")
}

emit_go api
emit_go streaming
emit_python
emit_web

echo "[sbom] artifacts written under ${OUT}"
ls -la "${OUT}" 2>/dev/null || true
