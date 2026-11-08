---
schema: 1
kind: pr
pr: 918
title: Epic 53 — native recovery screens in the Apple and Android Remotes
date: 2026-11-07
session: 0e9a3649-4e54-455d-9465-045c6636722d
prs: [684]
---

## What happened
- The Remotes opened the web page for recovery. Both now have a native screen with the same routes as the web (`GET /recovery/api/state`, `/recovery/api/logs`, `POST /recovery/api/action`). It shows safe mode, each group and part with its state, reason, restarts and last error, the disk, and the last warnings and errors. Its actions are restart a part or everything, start, leave or stay in safe mode, and go back a version (confirmed twice), with a retry on 428. iPhone: Home → Diagnostics → Recovery. Android: account menu → Recovery. On both, the status strip's Details for `/recovery` opens the native screen. English and Arabic, with Android screenshots in both and the maktaba.dev Apps page updated.
- `POST /api/auth/recovery-pass` now also returns the pass in the body to a native app, which keeps it to reach recovery while the API is down.
- **Security review, fixed before merge:** at first the pass went into the body when the request looked native (the `X-Maktaba-Client` header or any `Authorization` header). A script on the web page can set both while riding the session cookie. The body now depends on how the request signed in: only a bearer access token the middleware verified (`principal.SourceJWT`) gets the pass in the body. A cookie session gets only the HttpOnly cookie, whatever headers it sends. There is a test for that case.

- **Found in CI, fixed before merge:** the last action's outcome (Done., or why not) was a line at the top of the list, while a part's Restart and the actions are far down it. It landed out of sight, and off screen a SwiftUI `List` row is not in the accessibility hierarchy, nor is a `LazyColumn` item composed. On both platforms it is now pinned below the list (Apple `safeAreaInset(edge: .bottom)`, Android the Scaffold's `bottomBar`), with the same strings. The Android rollback test checks it shows while the list is still scrolled down.
- Two flakes it met, both fixed in their tests: the recovery alert's title and its button are both "Restart", so `app.alerts.buttons["Restart"]` matched twice (now `.firstMatch`; the POST it causes is asserted); and the desktop client's `CloudTest` iterated a synchronized list the mock servers append to from their threads (`ConcurrentModificationException` on Windows), now read through a copy taken under its lock.

## What we learned
- The recovery lockout counts every refused credential, and the Remotes poll every 10 s. Drop a refused pass and stop polling, or the admin is locked out within a minute.
- Recovery calls need their own path, not the normal API client: a 401 there means not allowed here, never sign out.
- Decide what a response reveals from the authentication method actually used, never from headers the caller can set.

## Open
- maktaba.dev has no iPhone recovery screenshot yet; it needs `remote-iphone-recovery` from a CI UI run.
- Restoring a backup and downloading diagnostics are still only on the web page.
