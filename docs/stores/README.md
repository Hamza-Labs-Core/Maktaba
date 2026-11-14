# TV store pages and real-device checklists

One page per TV platform (Epic 58). Each holds the platform's **real-device checklist** (story
58.16) and, from story 58.04, its store listing and submission notes.

| Page | Platform | What is versioned and submitted |
|---|---|---|
| [amazon.md](amazon.md) | Fire TV (Amazon Appstore) | the Android TV app's `amazon` flavor |
| [cast.md](cast.md) | Google Cast | the Cast receiver |
| [samsung.md](samsung.md) | Samsung (Tizen) | the launcher (`.wgt`) |
| [lg.md](lg.md) | LG (webOS) | the launcher (`.ipk`) |
| [roku.md](roku.md) | Roku | the channel |

## Why a checklist

CI runs what a runner can run: unit tests, Playwright with platform shims, Robolectric and the
Android TV emulator, brs-engine and the webOS Simulator as smoke tests (the Samsung TV emulator is not run: [samsung.md](samsung.md) says why).
None of these is a real TV: no AVPlay, no HDR, no passthrough, no real remote, no Roku renderer,
no Google Cast SDK on a device. What only a device shows is in its page's checklist, so no one
mistakes a fake for a real device.

## The rule

- A person runs the checklist on the devices it lists before a release candidate is promoted
  or submitted to the store.
- The result is one line in the page's `## Results` section, newest last, and the same line
  goes in the release notes:

  ```
  - Samsung checklist passed on 2020/2022/2025 TVs, launcher 1.3.0 (2026-12-01)
  - Roku checklist failed on Roku Express 4K, channel 1.0.0 (2026-12-02): DV p8 stutters
  ```

  The form is `- <Platform> checklist passed|failed on <devices>, <component> <X.Y.Z>`, then
  anything (the date, a note). `<Platform>` and `<component>` are the ones in the table above
  (`Fire TV`/`app`, `Cast`/`receiver`, `Samsung`/`launcher`, `LG`/`launcher`, `Roku`/`channel`).
- **A release session does not submit a TV build whose checklist has no result for that
  version.** Its submission step runs `tools/release/tv_checklist_guard.py --platform <page>
  --version <X.Y.Z>`, which stops with the reason when the version has no result or its newest
  result failed, and prints the passing line for the release notes otherwise.
- The device's log from the run (Roku's debug console on port 8085, `sdb dlog`, `ares-log`,
  logcat, the Cast CaC Tool's capture) is attached to the result: a link after the line, to an
  issue or a release asset. Logs pass the existing redaction (no `sig`, tokens or grants)
  before they are attached.

## A device lab

Not set up (Epic 58, owner question Q6: the default is no lab). A self-hosted runner on the home
network with these devices in developer mode could run the checklists on a schedule and on
release candidates, never on every pull request, since TVs are shared hardware.
