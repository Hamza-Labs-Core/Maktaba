#!/usr/bin/env bash
# tools/sbom.sh — a CycloneDX SBOM for every shipped Maktaba component (story 23.7 AC1, TC1).
#
#   tools/sbom.sh [VERSION] [OUT_DIR]        # make sbom; release.yml's sbom job
#
# Writes OUT_DIR/maktaba-sbom-<component>-<VERSION>.cdx.json (default VERSION `dev`,
# OUT_DIR artifacts/sbom) for:
#
#   api, streaming, server (cmd/maktaba-server), cloud,  cyclonedx-gomod app (what each binary links)
#   launcher (cmd/maktaba-launcher), healthcheck
#   (tools/healthcheck, in the api and streaming images)
#   pipeline                                             cyclonedx-py over `uv export`
#   web                                                  tools/security/sbom.py over pnpm-lock.yaml
#
# Each SBOM is then checked against the dependency list its own tool chain resolves (`go
# list -deps`, `uv export`, the lockfile's packages): a component that misses one
# transitive dependency fails, so no short SBOM is published. Every tool is required; a
# missing one fails (this is the release's, not a best effort).
#
# Tools: cyclonedx-gomod (go install github.com/CycloneDX/cyclonedx-gomod/cmd/cyclonedx-gomod@v1.9.0),
# uv (cyclonedx-py runs through uvx at CYCLONEDX_PY_VERSION) and python3.
#
# Release images also carry BuildKit's own SBOM attestations (release.yml's `sbom: true`);
# these files are the per-component view, attached to the GitHub release and signed by its
# checksums.txt.

set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
VERSION="${1:-dev}"
OUT="${2:-${ROOT}/artifacts/sbom}"
CYCLONEDX_PY_VERSION="${CYCLONEDX_PY_VERSION:-7.2.1}"
mkdir -p "${OUT}"
# absolute: emit_go writes from inside each module (release.yml passes a relative one)
OUT=$(cd "${OUT}" && pwd)
WORK=$(mktemp -d)
trap 'rm -rf "${WORK}"' EXIT

need() { command -v "$1" >/dev/null 2>&1 || { echo "[sbom] $1 is not on PATH" >&2; exit 2; }; }
need cyclonedx-gomod
need go
need uv
need python3

out_for() { echo "${OUT}/maktaba-sbom-$1-${VERSION}.cdx.json"; }
check() {
    python3 "${ROOT}/tools/security/sbom.py" check "$1" --expect "$2"
    echo "[sbom] $(basename "$1"): every one of $(wc -l <"$2" | tr -d ' ') dependencies listed"
}

# emit_go NAME MODULE MAIN [TAGS]: the modules the binary links (cyclonedx-gomod app, for
# linux/amd64 as released, the Go standard library included), checked against the modules
# of the packages `go list -deps` says the main package builds from (a module replaced by a
# local directory, the shared ones, as `path@`: its version is the commit's, any matches),
# and the standard library
emit_go() {
    local name=$1 mod=$2 main=$3 tags=${4:-} out
    out=$(out_for "${name}")
    echo "[sbom] go ${mod} (${main}) -> ${out}"
    (cd "${ROOT}/${mod}" && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 GOFLAGS="${tags:+-tags=${tags}}" \
        cyclonedx-gomod app -json -std -main "${main}" -output "${out}" >/dev/null)
    (cd "${ROOT}/${mod}" && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
        go list ${tags:+-tags "${tags}"} -deps \
        -f '{{with .Module}}{{if not .Main}}{{.Path}}@{{if .Replace}}{{.Replace.Version}}{{else}}{{.Version}}{{end}}{{end}}{{end}}' "${main}") \
        | sort -u | awk 'NF' >"${WORK}/${name}.expect"
    # and the standard library at the Go that builds it (-std; the healthcheck links nothing else)
    echo "std@$(cd "${ROOT}/${mod}" && go env GOVERSION)" >>"${WORK}/${name}.expect"
    check "${out}" "${WORK}/${name}.expect"
}

emit_python() {
    local out reqs
    out=$(out_for pipeline)
    reqs="${WORK}/pipeline-requirements.txt"
    echo "[sbom] python pipeline -> ${out}"
    (cd "${ROOT}/pipeline" && uv export --frozen --no-hashes --no-emit-project --no-dev --all-extras \
        --format requirements-txt >"${reqs}")
    uvx --from "cyclonedx-bom==${CYCLONEDX_PY_VERSION}" cyclonedx-py requirements "${reqs}" \
        --output-format JSON --output-file "${out}"
    # `name==version ; marker` lines, as name@version
    sed -nE 's/^([A-Za-z0-9_.-]+)(\[[^]]*\])?==([^ ;]+).*/\1@\3/p' "${reqs}" >"${WORK}/pipeline.expect"
    check "${out}" "${WORK}/pipeline.expect"
}

emit_web() {
    local out
    out=$(out_for web)
    echo "[sbom] web -> ${out}"
    python3 "${ROOT}/tools/security/sbom.py" pnpm "${ROOT}/web/pnpm-lock.yaml" web "${VERSION}" "${out}" \
        --expect-out "${WORK}/web.expect"
    check "${out}" "${WORK}/web.expect"
}

emit_go api api .
emit_go streaming streaming .
emit_go server cmd/maktaba-server . embed_web
emit_go cloud cloud ./cmd/maktaba-cloud
emit_go launcher cmd/maktaba-launcher .
emit_go healthcheck tools/healthcheck .
emit_python
emit_web

echo "[sbom] written under ${OUT}:"
ls -1 "${OUT}"
