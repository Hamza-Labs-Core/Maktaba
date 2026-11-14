#!/usr/bin/env bash
# Story 58.05 T04, run by _android-tv.yml's firetv-emulator job inside the Android TV emulator
# (from apps/tv/android): keeps logcat for the whole run, takes Google Play services off the image
# for this run (Fire OS has none), then runs the Fire TV build's journey test.
set -euo pipefail

out=app/build/emulator
mkdir -p "$out"
adb logcat -c || true
adb logcat -v threadtime >"$out/logcat.txt" 2>&1 &
logcat=$!
trap 'kill "$logcat" 2>/dev/null || true' EXIT

adb shell getprop >"$out/getprop.txt"
# Fire OS has no Google Play services: if the image carries them, they are removed for user 0
# (the run's only user) so nothing the app does can lean on them
packages="$(adb shell pm list packages | tr -d '\r')"
for pkg in com.google.android.gms com.android.vending; do
  if grep -qx "package:$pkg" <<<"$packages"; then
    echo "removing $pkg for this run: $(adb shell pm uninstall --user 0 "$pkg" | tr -d '\r')"
  fi
done
adb shell pm list packages | tr -d '\r' | sort >"$out/packages.txt"
if grep -qx "package:com.google.android.gms" "$out/packages.txt"; then
  echo "::error::Google Play services are still on the emulator"
  exit 1
fi

./gradlew --no-daemon --stacktrace :app:connectedAmazonDebugAndroidTest \
  -Pandroid.testInstrumentationRunnerArguments.class=com.hamzalabs.maktaba.tv.FireTvJourneyTest
