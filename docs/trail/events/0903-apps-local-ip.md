---
schema: 1
kind: pr
pr: 903
title: The apps use the home list's local_ip entries, pinned to the home's name
date: 2026-11-07
session: c0d50e69-00c4-47e2-9b33-1ac69218ae34
prs: [897]
---

## What happened
- Android and the Apple apps now try `local_ip` right after `local`. A certificate reached by a raw address is accepted only when it is valid for the entry's `tls_name`. On Android an OkHttp `HostnameVerifier` hands `OkHostnameVerifier` the pinned name; on Apple a `URLSessionDelegate` evaluates the trust with `SecPolicyCreateSSL(true, name)`. Every session that can reach a home uses it, image caches included, and saved homes re-pin when the app starts.
- Both platforms test it over real TLS, with a CA and a leaf for the home's name, reached by 127.0.0.1.

## What we learned
- Apple refuses a TLS leaf whose validity exceeds its maximum ("exceeds maximum temporal validity period"), even when it chains to a custom anchor. A test certificate has to be short-lived (90 days here), and the test pins its verify date so the certificate doesn't expire under the test.
- Swapping the name the chain is checked against keeps the platform's whole chain and expiry check. Skipping that check, which other "trust this IP" workarounds do, would accept any certificate on the LAN.
- The first draft accepted any `tls_name`, so a bad home list could have made a LAN address answer for an unrelated name. A security review caught it before merge. A pin now requires the name's first label to decode to the URL's own address (`direct.EncodeIP`'s form) with a home ID and domain after it; anything else is ignored.
- The two image caches build their own HTTP clients, and the first draft missed them. A search for every `OkHttpClient.Builder` and `URLSession(` that reaches a home found both.
- The Player UI-test bundles compile `ImageCache.swift` alone, without MaktabaKit. A MaktabaKit symbol in that file compiles (the module is visible) but fails to link, only in CI's UI-test build. The file now takes its delegate from the app at launch (`ImageCache.trust`).

## Keep doing
- When adding a TLS rule, find every client that can reach a home, not only the API client.
- Before adding an import to a Player file, check which targets compile it (the project file's source phases); a test bundle may compile it alone.
- When a server-supplied value relaxes a check, bind it to what it is for (here: the address it pins) instead of trusting it whole.

## Open
- The web home settings' `direct.rebinding` notice for when the LAN name fails and the address works.
