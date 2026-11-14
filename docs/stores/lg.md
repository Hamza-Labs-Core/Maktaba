# LG (webOS)

The LG launcher, an `.ipk` hosting the home's `/tv/` UI (story 58.11). Story 58.04 adds the
listing, the UX scenario document and the self-checklist here.

## Real-device checklist

Run before each launcher release candidate is submitted in LG's Seller Lounge (58.11 T05). The
rule and the result line are in [README.md](README.md).

**What CI covers instead:** the launcher and `/tv/` in Playwright with a webOS shim, the package
lint, and the webOS TV Simulator as a smoke test (no real media). **Only a device shows:** native
HLS, HDR, Dolby Vision per model year, the Magic Remote, performance.

### Devices

- a webOS 5 TV (2020, the oldest supported)
- a webOS 23 TV
- a webOS 25 TV

Each in Developer Mode, the candidate installed with `ares-install`. **First, Extend** the
Developer Mode session in the Developer Mode app: an expired session removes the test app in the
middle of a run. Note the session's end date with the result. Capture `ares-log` for the whole
run.

### Checklist

| # | Check | Pass when |
|---|---|---|
| 1 | Launch, sign in by the cloud code, and find a home on the network | both paths reach Home |
| 2 | Play a 4K HDR10 file | native `<video>` plays it in HDR at 4K |
| 3 | An HLS fMP4 session | plays, seeks, and survives a quality switch |
| 4 | `INDEPENDENT-SEGMENTS` behaviour | seeking lands on the right segment, no stall |
| 5 | Dolby Vision profile 8, on each model year | DV where the TV reports it, HDR10 otherwise |
| 6 | EAC3 audio on a receiver or soundbar | passthrough |
| 7 | Arabic subtitles | drawn by Maktaba's layer, joined, right to left |
| 8 | Switch to Arabic | every screen mirrors, Left and Right swap in the focus order |
| 9 | The Magic Remote: pointer, wheel, arrows, Back | the pointer focuses what it hovers; Back from Home leaves the app |
| 10 | The performance budget (58.09 and 58.11) on the webOS 5 TV | launch to Home and Home to playback within the budget |

## Results

No result yet (the first release candidate records the first line).
