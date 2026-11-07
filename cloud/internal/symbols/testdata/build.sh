#!/bin/sh
# Rebuilds the symbol fixtures on a Mac with Xcode: a fat (arm64 + x86_64) C binary's dSYM
# and an arm64 Swift one, with their source paths mapped to /src. The tests read the dSYMs'
# DWARF files (crashy.dwarf, player.dwarf), and crashy.arm64 is the arm64 binary with no debug
# info (its symbol table, debugging entries stripped). Each is kept gzip'd: the repo's lint
# refuses committed executables, and the tests unpack them.
set -eu
cd "$(dirname "$0")"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
for a in arm64 x86_64; do
	clang -g -O0 -arch "$a" -fdebug-prefix-map="$PWD"=/src -c crashy.c -o "$work/crashy-$a.o"
	clang -arch "$a" "$work/crashy-$a.o" -o "$work/crashy-$a"
done
lipo -create "$work/crashy-arm64" "$work/crashy-x86_64" -output "$work/crashy"
dsymutil "$work/crashy" -o "$work/crashy.dSYM"
strip -S "$work/crashy-arm64" -o crashy.arm64
cp "$work/crashy.dSYM/Contents/Resources/DWARF/crashy" crashy.dwarf
xcrun swiftc -g -Onone -parse-as-library -module-name Demo -debug-prefix-map "$PWD"=/src -c player.swift -o "$work/player.o"
xcrun swiftc "$work/player.o" -o "$work/player"
dsymutil "$work/player" -o "$work/player.dSYM"
cp "$work/player.dSYM/Contents/Resources/DWARF/player" player.dwarf
gzip -n -9 -f crashy.arm64 crashy.dwarf player.dwarf
