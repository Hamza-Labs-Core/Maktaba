---
schema: 1
kind: pr
pr: 1593
title: "Store galleries: the defects the first full set showed, fixed at their source before the owner's review"
date: 2026-11-24
session: e1c12b82-6431-40f4-b0ae-96e43fdfa26e
agent: claude-code
---

## What happened
The first full set of the store galleries (store-gallery run 45376900146) showed seven defects.
Each was fixed at its source in #1593, by three child sessions (Android, Apple, Roku and the TV
web UI) whose branches were merged into it.

- **The 7" Android Remote:** at 600dp the Remote showed its list beside the page, squeezed to
  about 223dp, so lines wrapped letter by letter and the Arabic Needs attention broke. The
  list now shows beside the page only where both fit at their least widths (360dp for the
  list plus 320dp for the page). Below that it is one pane, as on a phone. A row's value
  moves under its label when it won't fit beside it.
- **Server English in the Arabic Remote** (Android and Apple): stage names, stage ids, cause
  codes, step names and the "home ·" prefix showed as the home sent them. Both Remotes now map
  every stage, cause, step and job state to their own words in English and Arabic, from the
  web's wording, keeping the home's label only for a code they don't know. Tests guard that
  every code in the home's own lists has both languages:
  - Android: `graph.py`, the first-party manifests, `causes.go`, `causes.py` and every
    `.progress()` step;
  - Apple: the web's stage titles, `causes.go` and the progress steps.
- **LG's "Version dev":** the release already stamps the version. The capture served the
  launcher from source without one. It now builds the launcher as the release does, with the
  next release's version, and fails if the screen doesn't show it.
- **Mac shots at 1024×674:** GitHub's macOS runner has only a 1× display.
  `tools/apple/hidpi-display.m` makes a 2× virtual display (`CGVirtualDisplay`, used only in
  CI) for the shots alone, and a Debug-only launch argument sizes the window to 1440×900
  points. The shots are now 2880×1800.
- **Roku's player on black:** brs-engine draws no video. `video_frame.cjs` paints the film's
  frame (decoded from the fixture stream) in the Video node's own rectangle, under everything
  the channel draws.
- **The iPhone's player as a thin band:** upright, the player is a band on black. The phone's
  subtitles beat now takes the landscape size the stores also accept (App Store 2868×1320 and
  2622×1206; Google Play 16:9), with landscape captures on both phones.
- **The iPad Remote's empty queue pane:** the test now selects Transcribe, by a new
  `queue.stage.<id>` identifier.

A second look, image by image at full size, found more, each fixed in #1593:

- **Caption faces:** the composer's captions were drawn in Arial in about half the images. The
  site's faces are `font-display: optional`, so a face that came a moment late was never used.
  The composer now serves them as `block` and fails when one is not loaded.
- **Subtitles shots with no subtitle** (Apple TV, the Android phone and 7" tablet, Google TV and
  Fire TV), and one cut by the canvas edge on the iPhone and Apple TV in Arabic. Every player
  shot now has its line, and a screen whose last line is the subtitle shows whole.
- **Apple TV's sign-in page:** its three buttons were cut to three letters, and its QR code ran
  off the screen. The backdrop's filled picture sized the stack and moved the panel off centre.
  It is a background now.
- **Samsung and LG:** subtitles were cut by the screen's bottom edge, and the hints named a
  keyboard's Enter, Esc and /.
- **Android TV and Fire TV:** On now's cards cut an Arabic line.
- **Android in Arabic:** "Code ABCD-1234" read left to right.
- **The iPad's status bar** showed an English date in the Arabic set. The panel starts under it.
- **TV guides** cut every title (quarter-hour programmes). The shots are On now.

The review found two majors, fixed in #1593:

- **Subtitles placed at the top left the big screen.** The first fix moved the whole cue
  container up. Now only a cue whose line is "auto" is raised, by its own count of lines; a
  Playwright test on the app's own player fails without it and with the container moved.
- **The word guards did not run where words go missing.** `ServerWordsTest` and
  `StageWordsTests` read the pipeline and the API, whose pull requests run neither app.
  `tools/ci/app_words.py` (text only) runs in the lint job on every pull request, for the web and
  both Remotes, and holds a step to one wording on all three.

Its minors too: the NOAA emblem is out of the demo library's art, the Apple TV's sign-in keeps
its focus and has room for its lines, and the changelog claims less. The rest is #1709.

Two CI failures on the merged branch came from the fixes themselves:
- Android's ask-a-parent shot was taken before Enter parent PIN appeared on the slower runner.
  The test now waits for it.
- `test_xcresult_ran` assumed one xcodebuild run per UI job. The Mac job's two runs (its tests
  at 1×, the shots at 2×) go through one helper, which the test now reads.

## What we learned
- **Look at the composed set, not only its checks.** Each of these defects passed the
  composer's checks: size, format, bytes, count and caption share. Each was visible only in
  the pictures.
- **Look at each image at full size too.** The first review used contact sheets 320 px high,
  where a cut subtitle, a clipped QR code and a wrong font do not show.
- **A filled picture in a ZStack sizes the stack.** Put it in `.background`.
- **tvOS shows no subtitle until one is chosen,** even with a rendition marked DEFAULT. The test
  picks Arabic in the player's own menu (AVLegibleSettings, AVLanguagesMenu).
- **Robolectric's legacy StaticLayout ignores first-strong direction,** so media3's SubtitleView
  draws an Arabic line's full stop on the wrong side there (not on a device). A right-to-left
  mark in the test's cue fixes it.
- **A command that outlives its shell call goes on running.** Two composes into one folder
  deleted each other's files. Run long jobs in the background, each into a new folder.
- **GitHub's macOS runner has a 1× display.** A Retina capture needs a virtual 2× display.
  `CGVirtualDisplay` lists no modes until the process pumps AppKit's event loop, and
  `CGDisplayPixelsWide` returns points on a 2× display.
- **brs-node renders in a worker thread,** so a test can't see its `console.log`. Code
  loaded into it reports through files.
- **A Mac Roborazzi run can pass where CI's Linux fails:** here, a button that loads late on
  the slower runner. A screenshot test waits for everything it shows, not only the screen's
  main element.

- **A guard that reads another part's files must run where those files change.** An app's unit
  test that reads the pipeline passes on the pull request that breaks it, and main goes red.
- **A cue's snapped line counts its first line from the bottom.** `line:-2` raises one line; a
  cue of n lines needs `-(n + 1)`. A percentage line is clamped, and cues shown together overlap.
- **Run the routing guard after committing.** `ci_paths.py guard` reads tracked files, so a new
  file passes it until it is added.
- **`/tmp/claude-501` is shared by every session on the machine.** A pull request body built
  from a file there carried another session's text for some seconds. Use the session's own
  scratchpad, and read the file before posting it.

## Open
- The owner's approval of the corrected set (decision `store-gallery-full-set`).
- **The Mac's Arabic shots:** the Mac app is not mirrored on main. #1632 mirrors it, and the
  Mac shots are taken again after it merges.
- **Roku's home:** the focus ring touches the row's title.
- **A system's own message** ("no space left on device") is English in the Arabic Remote.
- **`affected-tests`:** its map may not run the Remote's `ServerWordsTest` when
  a new stage, cause or step lands in `pipeline/` or `api/`, and that test fails until both
  Remotes have words for it.
- **No package lint checks the launcher's version:** neither `lint-ipk.mjs` nor `lint_wgt.py`
  checks that the bundle carries it.
- **Still in English in Arabic, by design:** the Remote's recovery part ids and the
  Diagnostics error titles, which are the home's own messages; some Arabic strings use
  Western digits.
- **Roku's subtitle line is white with no backing,** and weak on a bright frame. This is the
  channel's own drawing.
