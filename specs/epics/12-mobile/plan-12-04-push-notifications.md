# Implementation Plan — Story 12.4 Push Notifications

> Companion to [story-12-04-push-notifications.md](story-12-04-push-notifications.md).
> Server contract owned by [Story 12.10](story-12-10-device-registration-api.md).
> This plan owns the on-device plugin + UX flow.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Plugin | `@capacitor/push-notifications` (official plugin), wrapped by `apps/mobile/plugins/push-bridge/` for category mapping + onboarding gates. |
| Permissions | Asked **only** when user enters Queue page or finishes onboarding; never on first paint. |
| Categories | Processing complete · New content added · Job failed · Subscription expiring. Toggleable in Settings → Notifications. |
| Token registration | `POST /api/devices/register` from Story 12.10. |
| Deep links | `maktaba://watch/{id}`, `maktaba://job/{id}` (Story 12.9). |
| Out of scope | APNs/FCM server-side fan-out (Story 12.10). |

## 1. File layout

```
apps/mobile/plugins/push-bridge/
├── src/
│   ├── definitions.ts
│   ├── index.ts
│   └── manager.ts          # category map, opt-in gates, registration
├── ios/Plugin.swift        # APNs registration helpers; UNNotificationCategory
└── android/.../PushBridgePlugin.kt
```

Web layer (`web/src/features/push/`):

| Path | Purpose |
|---|---|
| `usePushPermission.ts` | Hook gating UI prompts on permission state. |
| `PushPermissionPromptModal.tsx` | Native-styled modal shown on Queue first visit. |
| `useNotificationCategories.ts` | Settings→Notifications toggles synced via `PATCH /api/devices/{id}`. |
| `notificationRouter.ts` | Maps `data.deepLink` payloads to React Router pushes. |

## 2. Permission flow

```ts
// usePushPermission.ts
export function usePushPermission() {
  const [state, setState] = useState<'unknown'|'prompt'|'granted'|'denied'>('unknown');

  useEffect(() => { (async () => {
    const { receive } = await PushNotifications.checkPermissions();
    setState(receive);
  })(); }, []);

  async function request() {
    const { receive } = await PushNotifications.requestPermissions();
    setState(receive);
    if (receive === 'granted') await PushNotifications.register();
  }

  return { state, request };
}
```

The Queue route shows `<PushPermissionPromptModal>` once if `state === 'prompt'` and the user hasn't dismissed it before (`localStorage.maktaba.push.dismissed`).

## 3. Token registration

```ts
PushNotifications.addListener('registration', async (token) => {
  await api.post('/devices/register', {
    platform: Capacitor.getPlatform(),  // 'ios' | 'android'
    token: token.value,
    app_version: getAppVersion(),
    os_version: getOSVersion(),
    locale: i18next.language,
    categories: getCategoryPrefs(),
  }, { headers: { 'Idempotency-Key': uuidv4() } });
});

PushNotifications.addListener('registrationError', (e) =>
  reportError('push.registration', e));
```

Token rotation: re-register on every cold launch (server dedupes by token hash).

## 4. Category management

```ts
// useNotificationCategories.ts
const cats = useQuery(['me','device','categories'], fetchMyDeviceCategories);
const update = useMutation((next: Cats) =>
  api.patch(`/devices/${myDeviceId}`, { categories: next }));
```

Settings → Notifications renders four toggles (one per category) and dispatches `update`.

## 5. Notification payload + deep linking

```ts
PushNotifications.addListener('pushNotificationActionPerformed', async (action) => {
  const link = action.notification.data?.deepLink as string | undefined;
  if (link) navigate(routerFromDeepLink(link));   // delegates to Story 12.9 router
});
```

If the OS revokes permission later, the next `checkPermissions()` returns `denied`; the in-app Settings toggle reflects this on next launch (no spammy reprompt).

## 6. Sound and vibration

Use OS defaults; we set `sound: 'default'` and `priority: 'high'` only for `processing complete` and `job failed` (others are normal).

## 7. Edge cases

| Case | Handling |
|---|---|
| APNs/FCM rate limit | Server batches into "5 jobs completed" (owned by Story 12.10). |
| Token invalidated server-side after logout | Notifications drop silently; client re-registers on next login. |
| Locale mismatch (server ≠ device) | Server uses the `locale` field from registration; client just ships its current `i18next.language`. |
| OS-level denial | Settings → Notifications shows an "Open system settings" CTA. |

## 8. Test cases

### 8.1 Unit

| Test | Asserts |
|---|---|
| `permission prompt only on Queue first visit` | After visiting Library, `state === 'prompt'` but no modal; on Queue, modal renders. |
| `dismissed flag persists` | After dismiss, modal does not show on Queue again. |
| `token registration call shape` | POST body matches Story 12.10 contract; `Idempotency-Key` present. |
| `category toggle PATCHes correctly` | Toggle "new_content" off → PATCH body `{ categories: { new_content: false, … } }`. |
| `deep link routes correctly` | `maktaba://watch/abc` → `navigate('/watch/abc')`. |

### 8.2 Device

- iPhone with TestFlight build: trigger `job.completed` server-side; notification arrives within 30 s; tap opens watch page.
- Pixel: toggle category off → trigger `library.video.added`; no notification.
- Revoke OS-level permission then relaunch: in-app settings reflect `denied`.

## 9. Performance

- `request()` returns within 200 ms in optimal state.
- Listener registrations done at app start; payload routing ≤ 16 ms.

## 10. Dependencies

- Server: Story 12.10 (registry + fan-out).
- Story 12.9 for deep-link router.
- Settings UI lives inside Story 11.6 with a nested `<NotificationsSection>`.
