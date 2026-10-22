#!/usr/bin/env bash
# Bundles the Linux app (AppImage, .deb, .rpm) at a version, after payload.sh: build-app.sh
# runs it, and so do desktop-release.yml's Linux legs. Run it in apps/desktop.
#
# The app carries its version as semver (its updater compares that with latest.json), and
# so does a release's AppImage. A pre-release's packages need their own forms, to install and
# to sort before the release:
#   .deb  Version 0.3.0~rc.1: "0.3.0-rc.1" would be upstream 0.3.0 with Debian revision
#         rc.1, after 0.3.0, and the release would then be a downgrade.
#   .rpm  Version 0.3.0, Release 0.1.rc.1: rpm takes no "-" in a version (the bundle
#         failed), and tauri takes only semver for one, so no "~" either; the pre-release
#         goes in the release, before the release's own Release 1 (Fedora's traditional
#         pre-release form).
# So a pre-release takes two passes: `tauri build` of the AppImage and the .deb at the
# version, then `tauri bundle` of the .rpm from the same binary with the version's core
# and that release. The .deb then gets its version in its control file, and with the
# updater's signing key both are signed again, for the app's own version.
#
# Usage: bundle.sh VERSION [tauri options...]   (VERSION: semver, a leading "v" taken off;
#                                                the options, but --bundles, go to both
#                                                passes)
#        bundle.sh --versions VERSION           (prints the .deb's version, the .rpm's
#                                                version and release; fails on a VERSION
#                                                that is not semver)
# Output: src-tauri/target/release/bundle/{appimage,deb,rpm}/
set -euo pipefail

die() { echo "bundle.sh: $*" >&2; exit 1; }

# package_versions VERSION: prints the .deb's version, then the .rpm's version and release
# ("1": tauri's own, a release's)
package_versions() {
    local v="${1#v}" core pre build
    [[ "$v" =~ ^([0-9]+\.[0-9]+\.[0-9]+)(-([0-9A-Za-z.-]+))?(\+([0-9A-Za-z.-]+))?$ ]] ||
        die "not a semver version: $1"
    core="${BASH_REMATCH[1]}" pre="${BASH_REMATCH[3]}" build="${BASH_REMATCH[5]}"
    if [ -z "$pre" ]; then
        echo "$v $v 1"
        return
    fi
    pre="${pre//-/.}" # neither takes a "-" in it
    echo "$core~$pre${build:++$build} $core 0.1.$pre${build:++$build}"
}

# set_deb_version DEB VERSION: the .deb's control file names VERSION
set_deb_version() {
    local deb="$1" version="$2" work
    work="$(mktemp -d)"
    dpkg-deb -R "$deb" "$work/pkg"
    sed -i "s/^Version: .*/Version: $version/" "$work/pkg/DEBIAN/control"
    dpkg-deb --root-owner-group -b "$work/pkg" "$work/out.deb" >/dev/null
    mv "$work/out.deb" "$deb"
    rm -rf "$work"
}

main() {
    [ $# -ge 1 ] || die "usage: bundle.sh VERSION [tauri options...]"
    if [ "$1" = --versions ]; then
        [ $# -eq 2 ] || die "usage: bundle.sh --versions VERSION"
        package_versions "$2"
        return
    fi
    local version="${1#v}" versions deb_version rpm_version rpm_release bundle f
    shift
    versions="$(package_versions "$version")"
    read -r deb_version rpm_version rpm_release <<<"$versions"
    bundle="src-tauri/target/release/bundle"
    rm -rf "$bundle/appimage" "$bundle/deb" "$bundle/rpm"

    if [ "$rpm_release" = 1 ]; then
        pnpm exec tauri build --bundles appimage,deb,rpm "$@" --config "{\"version\":\"$version\"}"
    else
        pnpm exec tauri build --bundles appimage,deb "$@" --config "{\"version\":\"$version\"}"
        pnpm exec tauri bundle --bundles rpm "$@" --config \
            "{\"version\":\"$rpm_version\",\"bundle\":{\"linux\":{\"rpm\":{\"release\":\"$rpm_release\"}}}}"
        for f in "$bundle"/deb/*.deb; do
            set_deb_version "$f" "$deb_version"
        done
        # the .deb changed and the .rpm was signed for its package version: both again,
        # for the app's (a .sig is there when the signing key was)
        for f in "$bundle"/deb/*.deb "$bundle"/rpm/*.rpm; do
            [ -f "$f.sig" ] || continue
            pnpm exec tauri signer sign --app-version "$version" "$f" >/dev/null
        done
    fi

    # what the packages say they are
    for f in "$bundle"/deb/*.deb; do
        [ "$(dpkg-deb -f "$f" Version)" = "$deb_version" ] || die "$f: version $(dpkg-deb -f "$f" Version), want $deb_version"
    done
    if command -v rpm >/dev/null 2>&1; then
        for f in "$bundle"/rpm/*.rpm; do
            [ "$(rpm -qp --queryformat '%{VERSION}-%{RELEASE}' "$f" 2>/dev/null)" = "$rpm_version-$rpm_release" ] ||
                die "$f: $(rpm -qp --queryformat '%{VERSION}-%{RELEASE}' "$f" 2>/dev/null), want $rpm_version-$rpm_release"
        done
    fi
    echo "bundle.sh: $version (deb $deb_version, rpm $rpm_version-$rpm_release)"
}

if [ "${BASH_SOURCE[0]}" = "$0" ]; then
    main "$@"
fi
