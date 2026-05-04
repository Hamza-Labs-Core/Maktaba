# Implementation Plan — Story 11.9 Keyboard Shortcuts

> Companion to [story-11-09-keyboard-shortcuts.md](story-11-09-keyboard-shortcuts.md).
> Player shortcuts owned by Story 11.3; this story owns the global layer + help overlay.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Library | `tinykeys` (~1 KB) — no heavy dependency. |
| Placement | `web/src/keymap/`. |
| Provider | `<KeymapProvider>` registers global bindings; per-context bindings are registered by the route via `useShortcut(key, handler, { context })`. |
| Help overlay | `?` toggles `<ShortcutsOverlay>`; filterable; RTL-aware. |
| Out of scope | OS / global media keys (Story 13.7); player shortcuts (11.3). |

## 1. Bindings

| Key | Action | Context |
|---|---|---|
| `g l` | Go to /library | global |
| `g s` | Go to /search | global |
| `g q` | Go to /queue | global |
| `g h` | Go to / | global |
| `g ,` | Go to /settings | global |
| `/` | Focus header search | global |
| `?` | Toggle help overlay | global |
| `j` / `k` | Move focus next/previous | list |
| `Enter` | Open focused item | list |
| `Esc` | Close overlay / blur input | global |

Player keys (`Space`, `← →`, `J K L`, `M`, `,` `.`, `0–9`, `F`, `C`, `+/-`) registered by Story 11.3 with `context: 'player'`.

## 2. File layout

| Path | Purpose |
|---|---|
| `web/src/keymap/KeymapProvider.tsx` | Sets up tinykeys; manages contexts. |
| `web/src/keymap/useShortcut.ts` | Register a binding for a context with cleanup on unmount. |
| `web/src/keymap/leader.ts` | Leader-key sequence handler (`g` + next char within 1.2 s). |
| `web/src/keymap/imeGuard.ts` | `isComposing` listener — disables shortcuts during IME composition. |
| `web/src/keymap/components/ShortcutsOverlay.tsx` | Filterable list, grouped by context, RTL-aware. |
| `web/src/keymap/registry.ts` | Single source of truth: `Shortcut[]` (id, keys, label key, context). |
| `web/src/keymap/util.ts` | Platform key map (`mod` → `Cmd` on mac else `Ctrl`); `isInputElement(target)`. |

## 3. Leader sequence

```ts
// leader.ts
export function makeLeader(timeoutMs = 1200) {
  let active = false;
  let t: number | null = null;
  return {
    enter() { active = true; clear(); t = window.setTimeout(reset, timeoutMs); },
    consume(key: string): string | null {
      if (!active) return null;
      reset();
      return key;
    },
  };
  function reset() { active = false; if (t) window.clearTimeout(t); t = null; }
}
```

`g` triggers `enter()`. The next non-leader keystroke (`l`, `s`, `q`, `h`, `,`) calls `consume()` and runs the action. Holding `g` for 2 s drops the leader silently.

## 4. Input guard

```ts
function shouldFireShortcut(target: EventTarget | null, key: string): boolean {
  if (key === 'Escape') return true;       // Esc always fires
  if (!target || !(target instanceof Element)) return true;
  const tag = target.tagName;
  return !(tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || (target as HTMLElement).isContentEditable);
}
```

Combined with `imeGuard` (listens to `compositionstart`/`compositionend` to suppress shortcuts during IME).

## 5. RTL handling

Player `←`/`→` semantics flip based on:
1. `document.documentElement.dir === 'rtl'`, AND
2. `Settings.useLogicalArrows === true` (default `true`).

When both true, `←` posts forward, `→` posts back. Other shortcuts are direction-neutral.

## 6. Help overlay

`<ShortcutsOverlay>` reads `registry.ts` and groups by context. Each row shows the localized label and a stylized key visual. The overlay is searchable via a top input (`Esc` closes; `/` doesn't fire because the overlay's input is focused).

```tsx
{visibleShortcuts.map(s => (
  <li key={s.id}>
    <span>{t(s.labelKey)}</span>
    <KeyHint chord={s.keys}/>
  </li>
))}
```

`KeyHint` substitutes the `mod` token: `Cmd` on macOS, `Ctrl` elsewhere (`navigator.platform`).

## 7. Edge cases

| Case | Handling |
|---|---|
| Shortcut conflicts with browser (e.g., `Cmd+S`) | Don't bind it; documented in registry. |
| `Cmd` not available (Linux/Windows) | `mod` substitution maps to `Ctrl`. |
| IME composing Arabic/Japanese | All shortcuts suppressed until `compositionend`. |
| Leader `g` then no follow-up | Silent drop at 1.2 s. |
| Multiple modals stacked | Only the topmost `<Esc>` handler fires (focus-trap library decides). |

## 8. Test cases

### 8.1 Unit

| Test | Asserts |
|---|---|
| `g l navigates to /library` | History push to `/library`. |
| `? toggles overlay outside inputs` | `<ShortcutsOverlay>` open after press. |
| `? in input does nothing` | Focus on `<input>`; press `?` → no overlay. |
| `leader times out at 1.2s` | `g` then wait 1.5 s then `l` → no nav. |
| `IME compositionstart suppresses shortcut` | While composing, `g l` is ignored. |
| `RTL logical arrows flip player binding` | With `dir=rtl` + setting on, `→` fires "back" handler. |

### 8.2 e2e

| Test | Asserts |
|---|---|
| `g l from Home navigates within 200 ms` | Playwright `page.keyboard.press` sequence. |
| `Cmd+S not preempted` | Browser's save dialog still appears. |
| `Help overlay listing` | Reads all registered shortcuts; matches snapshot. |

## 9. Dependencies

- Story 11.3 registers player shortcuts.
- Story 11.12 provides `dir` from i18n.
- Story 11.6 surfaces "use logical arrows" toggle in Playback settings.
