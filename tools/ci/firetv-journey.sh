#!/usr/bin/env bash
# Story 58.05 T04, run by _android-tv.yml's firetv-emulator job inside the Android TV emulator
# (from apps/tv/android): keeps logcat for the whole run, takes Google Play services off the image
# for this run (Fire OS has none), then runs the Fire TV build's journey test and the guest's
# (story 50.27 T06: Watch a broadcast with a short code, played by the app's player).
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

# One run per class: the test platform takes only the first of a comma-separated `class`, and a
# filter that matches nothing passes green, so each run must show its class in the results, which
# are kept with the logs (each run replaces the one before's).
results=app/build/outputs/androidTest-results/connected/debug/flavors/amazon
journey() {
  local class="$1"
  rm -rf "$results"
  ./gradlew --no-daemon --stacktrace :app:connectedAmazonDebugAndroidTest \
    -Pandroid.testInstrumentationRunnerArguments.class="$class"
  if ! grep -qs "classname=\"$class\"" "$results"/*.xml; then
    echo "::error::$class did not run on the emulator"
    exit 1
  fi
  cp "$results"/*.xml "$out/TEST-${class##*.}.xml"
}

journey com.hamzalabs.maktaba.tv.FireTvJourneyTest
# story 50.27 T06: Watch a broadcast with a short code, played by the app's own player
journey com.hamzalabs.maktaba.tv.TvGuestJourneyTest
