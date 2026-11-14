# Fire TV (Amazon Appstore)

The Android TV app's `amazon` flavor (story 58.05). Story 58.04 adds the listing and the
submission notes here; story 58.18 adds Vega OS.

## Real-device checklist

Run before each release candidate is promoted from Live App Testing to the store (58.05 T06).
The rule and the result line are in [README.md](README.md).

**What CI covers instead:** Robolectric screenshots, the instrumented tests on Google's Android TV
emulator (API 30, no Google APIs), the dependency check (no Google Play code) and the manifest
check. **Only a device shows:** codecs, HDR, Dolby, Amazon's remote and launcher behaviour.

### Devices

- Fire TV Stick 4K Max (Fire OS 8)
- one Fire OS 7 device

Install the release candidate from Live App Testing (as a tester would), or `adb install` the
candidate APK over the network (`adb connect <tv-ip>:5555`, ADB debugging on in Developer
options). Capture `adb logcat` for the whole run.

### Checklist

| # | Check | Pass when |
|---|---|---|
| 1 | Sign in by code against a home, then against the review home | the code screen shows, the phone approves, Home loads |
| 2 | Play a 4K HDR10 file | plays in HDR at 4K (the TV's info overlay says so), no transcode on the home |
| 3 | Play a Dolby Vision profile 8 file | plays as DV or HDR10, never washed-out SDR |
| 4 | Play a file with EAC3 audio on a receiver or soundbar | passthrough (the receiver shows Dolby Digital Plus) |
| 5 | Switch the app to Arabic | every screen mirrors, text joined, times and episode codes read left to right |
| 6 | Arabic subtitles | joined, right to left, readable on the 10-foot screen |
| 7 | Sleep the device mid-video, wake it | the app resumes at the same place, still signed in |
| 8 | The Amazon remote: Back, Home, play/pause, the microphone button | each does what its label says; Back from Home leaves the app |
| 9 | Leave and return from Amazon's launcher | the app returns to where it was |

## Results

No result yet (the first release candidate records the first line).
