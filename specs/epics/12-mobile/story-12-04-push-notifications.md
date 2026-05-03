# Story 12.4 — Push notifications (processing complete, new content)

APNs (iOS) and FCM (Android), bridged through the API. Notifications are
strictly opt-in.

**Anchors:** [`architecture.md` §6.3](../../architecture.md). Depends on
[Story 12.10](story-12-10-device-registration-api.md) for the API surface
that owns `/api/devices/register` and the FCM/APNs fan-out.

## AC

- First-launch flow: ask for notification permission only after the user
  enters the Queue page or finishes onboarding (Story 17.6); never on
  first paint.
- Categories: "Processing complete", "New content added", "Job failed",
  "Subscription expiring" (if applicable, see Epic 16).
- User can toggle each category in Settings → Notifications.
- Notifications carry a deep-link payload (`maktaba://watch/{id}`) and
  open directly to the right page (Story 12.9).
- Token registration: client posts the device token to
  `POST /api/devices/register {token, platform, locale}` (owned by
  [Story 12.10](story-12-10-device-registration-api.md)).
- Token rotation: client refreshes on every cold launch; server dedupes.
- Notification sound and vibration follow the device defaults.

## TC

- Process a 4-hour video; on completion, the user receives a notification
  within 30 s. Tapping opens the video detail page.
- Toggle off "New content" notifications: the next library scan does not
  emit a notification.
- Revoke notification permission at the OS level: the in-app settings
  reflect this within one app launch.

## EC

- APNs / FCM rate-limits (e.g., 100/sec/user): server batches into a
  single "5 jobs completed" notification.
- Token invalidated server-side (user logged out): notifications are
  silently dropped; client re-registers on next login.
- Locale mismatch (server speaks Arabic, device speaks English):
  notifications use the device locale via the `locale` field at
  registration time.
- Quiet hours (a future setting): notifications respected on the server,
  not the device, so they're consistent across platforms.
