---
schema: 1
kind: pr
pr: 1621
title: "The real Google Cast SDK runs in CI; how Cast goes on for App Store review"
date: 2026-11-24
session: 366f9ce3-e03b-40f4-944e-650e7dafc5b7
agent: claude-code
---

## What happened
- Decision `apple-cast` (2026-11-24): turn Cast on and test it for review.
- The Player's Cast code and its UI tests already existed, but only against a fake context.
- The first version of #1621 (713fda9f4) started the SDK with a receiver from a launch argument
  and checked the picker's empty state. Review (Kimi K3.1 Pro) found that the build declared
  none of Cast's Bonjour services, and that the empty state is the picker's own five-second
  timer. Both were right: on that build the test passed while the SDK logged that iOS refused
  its search.
- 2c728a30e replaces it. CI's UI-test build names Google's public Default Media Receiver
  (`CC1AD845`), `cast-app-id.py --built` checks the built Info.plist, and the test reads the
  SDK's own state and has it find a Cast device that the runner advertises over Bonjour.
- `docs/stores/cast.md` gives the order for turning Cast on: register, deploy, checklist,
  publish, then the App Privacy answers and review notes.

## What we learned
- **Cast cannot go on from code alone.** `shared/cast/app.json` has no `receiver_app_id`, and
  `cast.maktaba.tv` did not resolve on 2026-11-24. Registering the receiver is an owner step
  in the Cast console, and deploying it is gated on `CAST_RECEIVER_LIVE`.
- **An unpublished receiver loads only on devices registered in the Cast console**
  (https://developers.google.com/cast/docs/registration). App Review's devices need it
  published.
- **The iOS sender SDK 4.8.x needs no multicast entitlement.**
  - Google's sender page asks only for `_googlecast._tcp`, `_<app id>._googlecast._tcp` and
    the local network prompt.
  - The permissions page says Apple will not grant the multicast entitlement just for Cast.
  - The #1574 audit had said it was needed; that is corrected there.

- **iOS enforces `NSBonjourServices` on the simulator too.** Without the service the SDK's
  `NSNetServiceBrowser` fails with -72008, and only the SDK's log says so:
  `discoveryState` stays running and `didStartDiscovery` is still called.
- **Declaring `_googlecast._tcp` alone let the SDK find a device by a receiver subtype that was
  not declared**, on the simulator. The plist check asks for both services, as Google does.
- **The SDK keeps state between launches:** devices it found (a SQLite store) and
  `kGCKDiscoveryEverStarted`, after which it searches from launch. A test needs a new device id
  each run and that key cleared.

## Keep doing
- Prove a search by finding something, and run the new test against the configuration it
  replaces to see it fail.

## Stop doing
- Asserting an SDK by the app's own screen, when the screen has a timer of its own.

## Open
- **Owner: register the receiver and give its ID.** It costs $5 once, at
  `https://cast.maktaba.tv/r/`. Then set `CAST_RECEIVER_LIVE`, register the test devices, run
  the checklist and publish.
- **The change that turns Cast on follows once the ID exists:** the ID in `app.json` and the
  regenerated `Config/Cast.xcconfig`.
- **The Cast SDK crashes on a malformed TXT record.** In 4.8.6 a device on the network that
  advertises `_googlecast._tcp` with a TXT key and no `=` ends the app:
  `-[GCKMDNSService attributeForKey:]` sends a string message to `NSNull`. It matters once Cast
  is on; not reported to Google and not worked around.
- **A denied local network prompt** cannot be produced on a simulator. Only the state check's own
  test covers it.
