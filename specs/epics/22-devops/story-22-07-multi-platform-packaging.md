# Story 22.7 — Multi-platform packaging

Beyond compose, native packages for the user's platform.

## Acceptance criteria

- AC1. **macOS (Homebrew tap):** `brew install maktaba/tap/maktaba`
  installs the three native binaries and a `uv`-managed Pipeline
  venv, drops three `launchd` plists, and starts them.
- AC2. **Linux (Debian / RPM):** packages ship for current Debian /
  Ubuntu / Fedora; they install a `systemd` unit per service and
  create the `maktaba` user.
- AC3. **Mobile (iOS / Android):** Capacitor-built apps published to
  the App Store and Play Store, signed and versioned per Story 22.5;
  builds gated on a minimum platform version.
- AC4. **Desktop (mac/Win/Linux):** Tauri-built installers (`.dmg`,
  `.msi`, `.AppImage`) signed and notarized where required; auto-
  update is opt-in.
- AC5. **TV apps:** XCode and Gradle builds for tvOS / Android TV
  produce signed packages; published manually for v1.

## Test cases

- TC1. Homebrew end-to-end: a CI job on a clean macOS runner installs
  via the tap, brings the three plists up, and passes the smoke
  test.
- TC2. Debian end-to-end: a CI job installs the .deb on a fresh
  Ubuntu LTS runner, starts via systemd, runs smoke.
- TC3. Mobile build: Capacitor sync produces an .ipa and .apk that
  open against a mock backend; smoke test exercises login + library.

## Edge cases

- EC1. Homebrew tap with the user's existing Postgres — the formula
  detects and uses it; a clean-room install creates a Postgres role
  and DB.
- EC2. Linux distro without `ffmpeg` ≥ minimum — package declares
  the dep; install fails with a clear message rather than silently
  shipping a broken Pipeline.
- EC3. Auto-update for desktop on Linux AppImage — uses the
  AppImage updater; the user can disable.
