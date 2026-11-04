#!/usr/bin/env bash
# fetch URL SHA256 NAME: the file at URL, in $WORK/downloads/NAME, checked against SHA256
# (a mismatch fails the recipe and keeps nothing); prints its path. Sourced by the recipes
# that download (pins.env holds their URLs and digests).
fetch() {
    local url="$1" sum="$2" name="$3" dir="${WORK:?}/downloads"
    mkdir -p "$dir"
    if [ ! -f "$dir/$name" ]; then
        curl -fsSL --retry 3 --retry-delay 5 -o "$dir/$name.part" "$url" ||
            { echo "fetch: $url did not download" >&2; return 1; }
        mv "$dir/$name.part" "$dir/$name"
    fi
    check_sha256 "$dir/$name" "$sum" || { rm -f "$dir/$name"; return 1; }
    echo "$dir/$name"
}

# check_sha256 FILE SHA256: FILE's digest is SHA256.
check_sha256() {
    local got
    if command -v sha256sum >/dev/null 2>&1; then
        got="$(sha256sum "$1" | awk '{print $1}')"
    else
        got="$(shasum -a 256 "$1" | awk '{print $1}')"
    fi
    [ "$got" = "$2" ] || { echo "fetch: $1 has sha256 $got, not the pinned $2" >&2; return 1; }
}
