# Multi-platform packaging (Story 22.7)

| Target | Subdir | Build artifact |
|---|---|---|
| macOS Homebrew tap | `homebrew/` | `maktaba.rb` formula |
| macOS launchd | `launchd/` | three `com.maktaba.*.plist` units |
| Debian / Ubuntu | `debian/` | `.deb` via `dpkg-deb` |
| Fedora / RHEL | `rpm/` | `.rpm` via `rpmbuild` |
| Linux systemd | `systemd/` | three `maktaba-*.service` units |

Mobile / desktop / TV packaging lives in `apps/` per Story 11; this
directory owns server-side native packaging only.

## Release flow (Story 22.5 + 22.6)

1. Tag main with `v{MAJOR}.{MINOR}.{PATCH}` (annotated, signed).
2. The release workflow rebuilds artifacts from the tag commit. The
   image's `org.opencontainers.image.revision` label must match the
   tag's sha (CI gate).
3. The release manifest (`release-manifest.json`) records the version,
   sha, build time, and the sha256 of every published artifact.
4. Upgrade: clients pull the new image; ``goose up`` runs forward
   migrations. Rollback: revert image + run ``goose down`` to a known
   schema revision (see `upgrade.md`).
