#!/usr/bin/env bash
# Build Linux packages for maktaba-server: .deb (Debian/Ubuntu),
# .rpm (Fedora/RHEL), and a tarball for everything else.
#
# Requires: nfpm (https://github.com/goreleaser/nfpm) on PATH and a
# GPG signing key (id in $MAKTABA_GPG_KEY_ID).
set -euo pipefail
ver="${1:-0.1.0}"
mkdir -p dist

for arch in amd64 arm64; do
  echo "==> Building maktaba-server linux/$arch"
  GOOS=linux GOARCH="$arch" CGO_ENABLED=0 go build \
    -trimpath \
    -ldflags "-s -w -X main.Version=$ver" \
    -o "dist/maktaba-server-linux-$arch" \
    ./api/

  for fmt in deb rpm; do
    MAKTABA_ARCH="$arch" MAKTABA_VERSION="$ver" \
      nfpm package -f nfpm.yaml --packager "$fmt" --target "dist/"
  done

  if [[ -n "${MAKTABA_GPG_KEY_ID:-}" ]]; then
    rpmsign --addsign --define "_gpg_name $MAKTABA_GPG_KEY_ID" dist/*.rpm
    dpkg-sig --sign builder dist/*.deb
  fi
done

echo "==> Done. Artifacts in dist/"
