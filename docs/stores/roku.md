# Roku

The Roku channel (stories 58.12 and 58.13). Story 58.04 adds the listing and the submission notes
here; story 58.13 adds the certification table.

## Submission notes

The store's reviewers sign in to the project's review home, review.maktaba.tv (story 58.04).
The notes, what reviewers get and how a submission round goes are in
[review-home.md](review-home.md): its text goes in the Developer Dashboard's **Channel credentials** (the test account) and the notes for certification. The password is never written
here: the notes name the release secret `REVIEW_HOME_VIEWER_PASSWORD` (and, for the parental
checks, `REVIEW_HOME_CHILD_PASSWORD`), and the release session fills each in when it submits.
After the round, the release checklist rotates both ([review-home.md](review-home.md#a-submission-round)).

## Real-device checklist

Run before each channel release candidate is uploaded to the Roku dashboard (58.12 T04–T05,
58.13 T05–T06). The rule and the result line are in [README.md](README.md).

**What CI covers instead:** the BrighterScript build and lint, and brs-engine logic, focus,
layout and render tests (brs-engine screenshots are not Roku's renderer). Roku has no emulator.
**Only a device shows:** the Video node, rendering, fonts, timing, certification.

### Devices

- Roku Express (HD)
- Roku Express 4K
- Roku Ultra
- a Roku TV

Each in developer mode, the candidate side-loaded from the developer web page. Capture the
debug console (`telnet <roku-ip> 8085`) for the whole run.

### Checklist

| # | Check | Pass when |
|---|---|---|
| 1 | The logic tests with Rooibos on the device | pass |
| 2 | Roku Automated Channel Testing (WebDriver): sign in by code against the review home, browse, a deep link, Back out | pass |
| 3 | Launch to Home on the Roku Express | within Roku's limit (15 s); screens within 3 s; playback within 8 s |
| 4 | Formats: H.264, HEVC, a file the home remuxes or transcodes | each plays |
| 5 | HDR10, and Dolby Vision profile 8 (played as HDR10) on the Ultra and the TV | HDR on the TV |
| 6 | EAC3 audio on a receiver or soundbar | passthrough |
| 7 | Switch to Arabic | text joined and in reading order, rows start at the right, Left moves forward |
| 8 | Arabic subtitles and the player overlay | drawn joined, right to left, in the bundled font |
| 9 | A Persian title with letters outside the shaping table | shown in logical order, logged, no crash |
| 10 | Shaping 20 long descriptions on the Roku Express | within 50 ms per screen |
| 11 | Voice search | the query reaches the home and results show |
| 12 | The audio guide | reads every focusable label |

## Results

No result yet (the first release candidate records the first line).
