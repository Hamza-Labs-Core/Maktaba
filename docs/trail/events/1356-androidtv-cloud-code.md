---
schema: 1
kind: pr
pr: 1356
title: "The cloud code on Android TV and Fire TV (58.03's TV side, 58.05)"
date: 2026-11-14
session: 1858de07-50bb-4568-960d-fc30a29571ce
agent: claude-code
---

## What happened
- The Android TV app and its Fire TV build open the home picker on the cloud code: a panel that
  asks Maktaba Cloud for a code (`TvCodes`, as `androidtv` or `firetv`), shows it with a QR of
  the approve page and polls until it is approved (`waitForTvApproval`), beside the homes on this
  network and the typed address. On approval the shared client redeems the grant itself
  (`AppSession.signInWithTvApproval`) and signs in. Robolectric covers the states, Arabic, the
  sign-in at an https home, and dark screenshots for both flavors; amazon.md gained real-device
  rows 12 to 15.
- Merged as 166f8f2a4 after two CI fixes: Android lint's BidiSpoofing rejected literal U+2066 and
  U+2069 in Kotlin source (written as `⁦`/`⁩` escapes now, as SignInScreen does), and
  the Fire TV journey test had gained a second companion object.

## What we learned
- The cloud's `home_id` is its own server id, not the instance id a home answers with at
  `/api/discovery/home`, so a TV cannot know the approving home by id. Two security reviews
  narrowed how it is known by address: plain http and raw IP addresses prove nothing (the cloud
  also lists the home's plain public address as `direct` for older apps, and a discovery `urls`
  list is whatever answers there says). The grant goes only to the cloud's https names, or on
  Android a `local_ip` pinned to its `tls_name`. The launcher (#1348) and Roku follow the same
  rule.
- Using the session's `use()` from the picker disposes the picker mid-call (the phase changes to
  SigningIn). The grant is redeemed first and the tokens kept for the address, then `use()` runs.
- Roborazzi references at full TV size pass 1 MB; `RecordOptions(resizeScale = 0.5)` keeps them
  near 335 KB.
- `HeldCertificate` fails under an Arabic default locale (its date formatting); build the test
  certificates lazily in `Locale.ROOT`.

## Keep doing
- Compile every source set a CI job builds (`compileAmazonDebugAndroidTestKotlin`) and run lint
  before pushing an Android TV change; the unit suites alone missed both CI failures.

## Open
- The Fire TV and Android TV rows in docs/stores/amazon.md (12 to 15) need a real device.
