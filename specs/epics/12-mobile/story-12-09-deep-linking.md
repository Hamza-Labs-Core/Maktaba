# Story 12.9 — Deep linking

Universal links / app links and a custom scheme `maktaba://` that opens
the app to a specific video, search, collection, or settings page.

**Anchors:** [`architecture.md` §6.3](../../architecture.md). Depends on
[Story 12.10](story-12-10-device-registration-api.md) (notification
payload includes deep link).

## AC

- iOS Universal Links: `https://{server}/watch/{id}` → app, with web
  fallback if not installed.
- Android App Links: same scheme via `assetlinks.json` published from
  the server at `/.well-known/`.
- Custom scheme `maktaba://watch/{id}?t=...` for in-app inter-route
  navigation and notification payloads.
- Deep links to: `/watch/{id}?t=...`, `/search?q=...`, `/library`,
  `/library/{id}`, `/queue`, `/settings`, `/collection/{id}`.
- Cold launch via deep link goes to the deep-linked route, not the home
  page.
- Authentication: if the user is not logged in, deep link is preserved
  and replayed after login.

## TC

- Tap `https://{server}/watch/abc?t=120` from an email on a phone with
  the app installed: app opens to the video at 02:00.
- Tap the same URL on a phone without the app: web fallback opens in
  Safari/Chrome.
- Notification with `maktaba://job/123` deep link: app opens to the
  Queue tab scrolled to job 123.

## EC

- Deep link references a deleted resource (404): the app shows an
  inline "Video not found" with a "Return to library" CTA.
- Server URL has changed (user moved hosts): deep links from the old
  host fail; we surface "This link points to a different Maktaba server"
  and offer to switch the configured server.
- Malformed deep link: silently land on `/library`, log warning.
