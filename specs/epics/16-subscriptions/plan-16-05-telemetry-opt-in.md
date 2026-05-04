# Implementation Plan — Story 16.5 Usage analytics (opt-in, client surface)

> Companion to [story-16-05-telemetry-opt-in.md](story-16-05-telemetry-opt-in.md).
> The story states *what* and *why*; this plan states *how*.
> The server-side sink is owned by [Story 16.7](story-16-07-telemetry-api.md);
> this story owns the **client opt-in flow + collection surface**.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Web client | `web/src/lib/telemetry/{client.ts,queue.ts,consent.tsx}`. Init runs at app boot; events queue locally until consent. |
| Mobile (Capacitor) | Reuses the web `client.ts` since the WebView shares JS context; native crashes via Capacitor `App.addListener('error', ...)` enqueue a sanitized stack. |
| Desktop (Tauri) | Same web client; native panics (Rust) reported via a Tauri command that posts a sanitized event. |
| tvOS / AndroidTV | Native clients; `TelemetryClient.swift` and `TelemetryClient.kt` mirror the web shape but use platform queues (UserDefaults / DataStore). |
| Consent dialog | Storybook component `Consent` with the 5-bullet list. Localized in `en` / `ar`. |
| Forget-my-device | `DELETE /api/telemetry/devices/{pseudonym}` (Story 16.7); UI button under Settings → Privacy. |
| Out of scope | Server-side schema, redaction, retention ([Story 16.7](story-16-07-telemetry-api.md)). |

## 1. Client architecture

```
   ┌─────────────────────────────────────┐
   │ App boot                             │
   │  - read consent state                │
   │  - if granted: init client + queue   │
   └─────────────┬───────────────────────┘
                 │
                 ▼
   ┌─────────────────────────────┐         ┌────────────────────────┐
   │ TelemetryClient             │ events  │ TelemetryQueue         │
   │  - track(event_kind, fields)│ ──────► │  - cap 1000             │
   │  - sanitize(...)            │         │  - persist on disk      │
   └─────────────┬───────────────┘         └─────────┬──────────────┘
                 │                                    │
                 │                                    ▼
                 │                          POST /api/telemetry (Story 16.7)
                 │                          POST /api/telemetry/web-vitals
                 ▼
        Settings → Privacy
        ─ Toggle on/off
        ─ Forget my device
```

## 2. Consent flow

```tsx
// web/src/lib/telemetry/consent.tsx
export function FirstLaunchConsent({ onDecide }: { onDecide: (granted: boolean) => void }) {
    return (
        <Modal title={t('telemetry.title')} preventClose>
            <p>{t('telemetry.intro')}</p>
            <ul>
                <li>{t('telemetry.bullet.app_version')}</li>
                <li>{t('telemetry.bullet.feature_counts')}</li>
                <li>{t('telemetry.bullet.os_locale')}</li>
                <li>{t('telemetry.bullet.errors')}</li>
                <li>{t('telemetry.bullet.never')}</li>
            </ul>
            <Button onClick={() => onDecide(true)}>{t('telemetry.opt_in')}</Button>
            <Button variant="ghost" onClick={() => onDecide(false)}>{t('telemetry.skip')}</Button>
        </Modal>
    );
}
```

Decision is persisted to localStorage and synced to the user's profile via `PUT /api/me/preferences` so it follows them across devices for the same user. New devices show the dialog independently (per-device consent, since `device_pseudonym` is per-device).

## 3. Device pseudonym

```ts
// web/src/lib/telemetry/pseudonym.ts
const KEY = 'maktaba.telemetry.pseudonym';

export function getOrCreatePseudonym(): string {
    let p = localStorage.getItem(KEY);
    if (!p) {
        const buf = new Uint8Array(12);
        crypto.getRandomValues(buf);
        p = base32(buf);   // 16 chars
        localStorage.setItem(KEY, p);
    }
    return p;
}

// Called by the opt-out path in Settings → Privacy. The next opt-in
// will generate a fresh pseudonym; the AC `TestPseudonymRotatesOnOptOutOptIn`
// pins this behavior. Without this explicit clear, the same pseudonym
// would be reused and re-link the device across opt cycles.
export function clearPseudonym(): void {
    localStorage.removeItem(KEY);
}
```

The pseudonym is generated at opt-in time (not at app install) so
opting out and back in produces a fresh pseudonym; it's never linked
to `user_id` (per Story 16.7 schema).

## 4. Event allow-list

The server enforces an allow-list (Story 16.7). The client's `track` function declares matching constants:

```ts
export const Events = {
    APP_OPEN:     { kind: 'app.open',     fields: ['cold_start', 'route'] },
    SEARCH_RUN:   { kind: 'search.run',   fields: ['result_count', 'has_filters', 'duration_ms'] },
    PLAYER_START: { kind: 'player.start', fields: ['source', 'codec', 'is_hdr'] },
    ERROR_UNCAUGHT: { kind: 'error.uncaught', fields: ['error_message', 'stack_first_5'] },
} as const;

type EventName = keyof typeof Events;
```

Client-side type-checking ensures we never send an unknown event kind; server rejects unknown kinds with `400 unknown-event-kind` as defense in depth.

### 4.1 Sanitization

Stack traces are truncated to the first 5 frames; file paths in error messages are matched against a known set of library roots and stripped client-side **and** server-side (defense in depth):

```ts
function sanitizeMessage(s: string): string {
    return s
      .replaceAll(/\/Users\/[^\/]+\//g, '<home>/')
      .replaceAll(/[A-Z]:\\Users\\[^\\]+\\/g, '<home>\\')
      // Library roots known via an env-injected pattern set:
      .replace(libraryRootsPattern, '<library>/');
}
```

We never send transcript text, search queries, or filenames. The TC: "Opt in: the next session's events appear on the telemetry server within minutes; What's never collected: video filenames, transcript text, search queries..." — the type system + sanitizer enforces this.

## 5. Queue & flush

```ts
// web/src/lib/telemetry/queue.ts
class TelemetryQueue {
    private events: TelemetryEvent[] = [];
    private timer?: number;
    private readonly MAX = 1000;
    private readonly FLUSH_INTERVAL = 30_000;
    private readonly FLUSH_MAX_BATCH = 100;

    enqueue(e: TelemetryEvent) {
        this.events.push(e);
        if (this.events.length > this.MAX) this.events.shift();   // drop oldest
        this.persist();
        this.scheduleFlush();
    }

    private scheduleFlush() {
        if (this.timer) return;
        this.timer = setTimeout(() => this.flush(), this.FLUSH_INTERVAL) as any;
    }

    private async flush() {
        this.timer = undefined;
        const batch = this.events.splice(0, this.FLUSH_MAX_BATCH);
        if (batch.length === 0) return;
        try {
            await fetch('/api/telemetry', { method: 'POST', body: JSON.stringify({ events: batch })});
        } catch (e) {
            // network error → re-queue (oldest end), back off
            this.events = [...batch, ...this.events];
            this.backoff++;
            setTimeout(() => this.flush(), Math.min(60_000, 2 ** this.backoff * 1000));
            return;
        }
        this.backoff = 0;
        this.persist();
        if (this.events.length > 0) this.scheduleFlush();
    }
}
```

The cap of 1000 events with oldest-dropped is the AC: "Network drops while sending events: queued locally; capped at 1,000 events; oldest dropped first."

The exponential backoff is the AC: "Telemetry endpoint returns 5xx: client retries with exponential backoff; never blocks UI."

## 6. Server-side opt-out

The story AC includes: "Self-host server-side opt-out: `[telemetry] enabled = false`."

This is owned by [Story 16.7](story-16-07-telemetry-api.md): when the config disables telemetry, the endpoints return 204 but never write rows. The client doesn't need to know; it sends events as usual.

For server admins who want to disable telemetry collection on their server entirely (not just their own data), the toggle is in Settings → Advanced → Telemetry: turning it off updates the server config and notifies all online clients via WS to stop sending. (This is a UX nicety, not a privacy requirement — the server-side `enabled = false` is the canonical kill-switch.)

## 7. Toggle off behavior

```ts
// SettingsPrivacy.tsx
function onToggleOff() {
    telemetry.disable();        // stops queue, drops in-memory events
    // Note: does NOT delete server-side data automatically.
    // "Forget my device" is a separate explicit button.
}
```

Toggling off does not call DELETE; the user must explicitly click "Forget my device". This avoids accidental data loss if a user toggles off while exploring.

## 8. Test plan

### 8.1 Unit

| Test | What it pins |
|---|---|
| `TestPseudonymStableAcrossSession` | Two `getOrCreatePseudonym()` calls return the same string. |
| `TestPseudonymRotatesOnOptOutOptIn` | Opt-out clears localStorage; opt-in generates new. |
| `TestSanitizerStripsHomeAndLibraryRoots` | Unix and Windows; both replaced. |
| `TestQueueDropsOldestAtCap` | Enqueue 1001; queue length = 1000; first dropped. |
| `TestFlushBatchSizeCap` | 200 events; flush sends 100; remainder waits next tick. |
| `TestFlushBackoff` | Stub 5xx; second attempt at ~2s, third at ~4s, capped at 60s. |
| `TestStackTracesTruncatedTo5Frames` | 50-frame error → 5 frames in payload. |

### 8.2 Consent

| Test | What it pins |
|---|---|
| `TestConsentDialogShownOnFirstLaunch` | Empty localStorage → modal renders. |
| `TestConsentDeclineQueuesNoEvents` | Decline; client.track('app.open') → queue size 0. |
| `TestConsentSyncsAcrossDevices` | Stub `PUT /api/me/preferences`; another device on next bootstrap reads granted. (Per-device pseudonym still required.) |

### 8.3 Forget my device

| Test | What it pins |
|---|---|
| `TestForgetMyDeviceCallsDelete` | Click → `DELETE /api/telemetry/devices/{pseudonym}` fires. |
| `TestForgetClearsLocalQueue` | After delete, in-memory queue is empty; new pseudonym minted on next opt-in. |

### 8.4 Native parity

| Test | What it pins |
|---|---|
| `TestTVOSTelemetryClientShape` | Same event kinds, same redaction. |
| `TestAndroidTVTelemetryQueueCap` | 1000 events; oldest-dropped. |

## 9. Edge cases — handling table

| Case | Behaviour | Where pinned |
|---|---|---|
| Network drops mid-flush | Re-queue, backoff. Capped 1000; oldest dropped. | `TestFlushBackoff` |
| 5xx persistent | Backoff up to 60 s; never blocks UI. | `TestFlushBackoff` |
| EU consent locale | Same modal acts as the consent record; localized text + dated entry persisted. | `TestEUConsentRecord` |
| User toggles off mid-flush | In-flight POST completes; queue cleared; subsequent events not enqueued. | `TestToggleOffMidFlush` |
| Multi-device same user | Each device has its own pseudonym; `Forget my device` per-device. | `TestPerDevicePseudonym` |
| `[telemetry] enabled = false` server-side | Endpoints return 204; client unaware; events vanish at server. | (Story 16.7) |
| Unknown event kind passes through somehow | Server rejects with 400; client logs warning; event dropped. | `TestUnknownKindServerSide` |
| Stack contains library content path | Sanitizer strips client-side; server strips again. | `TestSanitizerStripsHomeAndLibraryRoots` |
| Pseudonym collision | 96 bits → ~1.5e-12 chance over 10k devices. Not handled. Documented. | n/a |
| Telemetry endpoint URL changed | Config injected at build; no runtime override. | n/a |

## 10. Acceptance checklist

**Consent**
- [ ] First-launch dialog with the 5 bullets, localized.
- [ ] Decline → no telemetry; opt-in → events flow.
- [ ] Settings → Privacy toggle.

**Client**
- [ ] Pseudonym persisted; rotated on opt-out/in.
- [ ] Sanitizer strips paths.
- [ ] Queue cap 1000; oldest-dropped.

**Forget**
- [ ] DELETE call wired; local queue cleared.

**Native**
- [ ] tvOS / AndroidTV mirrors the shape.

**Tests**
- [ ] All §8 tests pass.

**Docs**
- [ ] `docs/privacy.md` mentions the on-server kill-switch.
- [ ] `specs/epics/16-subscriptions/README.md` ticks story 16.5.
