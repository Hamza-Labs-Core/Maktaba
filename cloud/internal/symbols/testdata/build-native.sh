#!/bin/sh
# Rebuilds native.so.gz, the native (ELF) symbol fixture: native.c as an unstripped aarch64
# shared library with DWARF (nodebug.c without) and a GNU build id, built in /src, as an Android
# app's own library is before the release strips it, and native.stripped.so, the same library
# stripped as the release packages it (only its dynamic symbols). Built in an Alpine container (any
# Docker host); kept gzip'd, since the repo's lint refuses committed binaries.
set -eu
cd "$(dirname "$0")"
docker run --rm --platform linux/arm64 -v "$PWD":/src -w /src alpine:3.20 sh -c '
	apk add --no-cache gcc musl-dev >/dev/null
	gcc -O1 -fPIC -c nodebug.c -o /tmp/nodebug.o
	gcc -g -O1 -shared -fPIC -Wl,--build-id=sha1 -o /tmp/native.so native.c /tmp/nodebug.o
	cp /tmp/native.so native.so
	strip --strip-unneeded -o native.stripped.so /tmp/native.so'
gzip -n -9 -f native.so native.stripped.so
