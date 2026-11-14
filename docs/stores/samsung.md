# Samsung (Tizen)

The Samsung launcher, a `.wgt` hosting the home's `/tv/` UI (story 58.10). Story 58.04 adds the
listing and the Seller Office notes here.

## Submission notes

The store's reviewers sign in to the project's review home, review.maktaba.tv (story 58.04).
The notes, what reviewers get and how a submission round goes are in
[review-home.md](review-home.md): its text goes in Seller Office's **Test account** fields and the notes for the verification test. The password is never written
here: the notes name the release secret `REVIEW_HOME_VIEWER_PASSWORD` (and, for the parental
checks, `REVIEW_HOME_CHILD_PASSWORD`), and the release session fills each in when it submits.
After the round, the release checklist rotates both ([review-home.md](review-home.md#a-submission-round)).

## Real-device checklist

Run before each launcher release candidate is uploaded to Seller Office (58.10 T05). The rule
and the result line are in [README.md](README.md).

**What CI covers instead:** the launcher and `/tv/` in Playwright with a `webapis` shim, the
package lint, and the Samsung TV emulator as a smoke test (it has no AVPlay and no UHD).
**Only a device shows:** AVPlay playback, HDR, passthrough, the keys on the real remote,
performance.

### Devices

- a 2020 Samsung TV (Tizen 5.5, the oldest supported)
- a 2022 Samsung TV
- a 2025 Samsung TV

Each in Developer Mode, the candidate installed with `sdb` (`sdb connect <tv-ip>`, then
`tizen install -n <file>.wgt`). Capture `sdb dlog` for the whole run.

### Checklist

| # | Check | Pass when |
|---|---|---|
| 1 | Launch, sign in by the cloud code, and find a home on the network | both paths reach Home |
| 2 | Play a 4K HDR10 file | AVPlay plays it in HDR at 4K |
| 3 | Play a Dolby Vision profile 8 file | plays as HDR10 |
| 4 | EAC3 audio on a receiver or soundbar | passthrough |
| 5 | Arabic subtitles | drawn by Maktaba's layer, joined, right to left |
| 6 | Switch to Arabic | every screen mirrors, Left and Right swap in the focus order |
| 7 | The remote's keys: arrows, Enter, Back, play/pause, rewind, fast-forward, the colour keys | each registered key works; Back from Home leaves the app |
| 8 | Sleep the TV mid-video, wake it | the launcher resumes, still signed in |
| 9 | The performance budget (58.09 and 58.10) on the 2020 TV | launch to Home and Home to playback within the budget |

## Results

No result yet (the first release candidate records the first line).
