# Implementation Plan — Story 12.8 Haptics

> Companion to [story-12-08-haptics.md](story-12-08-haptics.md).
> Light haptic cues; respects OS-level haptic toggle and Settings → Accessibility → Haptics.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Plugin | `@capacitor/haptics` (official). |
| Wrapper | `apps/mobile/plugins/haptics-bridge/` adds throttling + setting check. |
| Settings | Stored client-side at `localStorage.maktaba.haptics` ∈ `off|light|full`; default `light`. Synced to user prefs by Story 11.6. |
| Throttle | ≤ 1 fire / 100 ms globally (rapid input). |
| Out of scope | Per-OS waveform tuning beyond what `@capacitor/haptics` exposes. |

## 1. API

```ts
// apps/mobile/plugins/haptics-bridge/src/index.ts
type HapticEvent =
  | 'tap-nav'           // light tap
  | 'card-long-press'   // medium impact
  | 'selection-change'  // selection
  | 'success'           // success notification
  | 'warning';          // warning notification

export const haptics = {
  fire(event: HapticEvent): Promise<void> { /* throttled, gated */ },
  setIntensity(level: 'off'|'light'|'full'): void,
};
```

## 2. Implementation

```ts
let lastFireMs = 0;
const MIN_INTERVAL_MS = 100;
let level: 'off'|'light'|'full' = (localStorage.getItem('maktaba.haptics') as any) ?? 'light';

export const haptics = {
  setIntensity(l) { level = l; localStorage.setItem('maktaba.haptics', l); },
  async fire(event) {
    if (level === 'off') return;
    const now = performance.now();
    if (now - lastFireMs < MIN_INTERVAL_MS) return;
    lastFireMs = now;

    if (level === 'light' && (event === 'card-long-press' || event === 'warning')) return;

    const { Haptics, ImpactStyle, NotificationType } = await import('@capacitor/haptics');
    switch (event) {
      case 'tap-nav':          await Haptics.impact({ style: ImpactStyle.Light }); break;
      case 'card-long-press':  await Haptics.impact({ style: ImpactStyle.Medium }); break;
      case 'selection-change': await Haptics.selectionChanged(); break;
      case 'success':          await Haptics.notification({ type: NotificationType.Success }); break;
      case 'warning':          await Haptics.notification({ type: NotificationType.Warning }); break;
    }
  },
};
```

The plugin native-side respects the OS's "Reduce motion / haptics" toggle automatically (system simply ignores haptic calls). We don't re-implement that gate.

## 3. Wiring

| Surface | Event |
|---|---|
| `<NavTab onTap>` (Story 11.7) | `tap-nav` |
| `<VideoCard onLongPress>` (Story 11.1) | `card-long-press` |
| `<Toggle onChange>` (Story 17.2) | `selection-change` |
| Download complete (Story 12.6) | `success` |
| Error toast (network/server) | `warning` |

Validation errors (e.g. form constraints) do **not** fire haptics.

## 4. Settings UI

`<HapticsControl>` lives in Settings → Accessibility (Story 11.6 has an Accessibility subsection):

```tsx
<RadioGroup value={level} onChange={haptics.setIntensity}>
  <Radio value="off">{t('settings.a11y.haptics.off')}</Radio>
  <Radio value="light">{t('settings.a11y.haptics.light')}</Radio>
  <Radio value="full">{t('settings.a11y.haptics.full')}</Radio>
</RadioGroup>
```

## 5. Edge cases

| Case | Handling |
|---|---|
| Devices without haptics (older Android tablets) | Plugin no-ops; no error. |
| Rapid input (typing in search) | Throttle gates to ≤ 1 / 100 ms. |
| OS-level "Reduce haptics" | OS ignores; no extra logic needed. |

## 6. Test cases

### 6.1 Unit

| Test | Asserts |
|---|---|
| `level=off blocks all` | No plugin call. |
| `level=light skips long-press` | Plugin call only for `tap-nav`/`selection-change`/`success`. |
| `throttle 100ms` | Two fires within 50 ms → only first dispatched. |
| `setIntensity persists` | Wrote to localStorage. |

### 6.2 Manual

- iPhone 13 with Settings → Sounds → System Haptics off: nothing fires regardless.
- Pixel: long-press card on `level=full` distinct from tap.

## 7. Performance

- Plugin call overhead ≤ 1 ms.
- `import('@capacitor/haptics')` is dynamic so the module isn't loaded on web.

## 8. Dependencies

- Story 11.6 surfaces the setting.
- Story 17.2 component primitives wire the events.
