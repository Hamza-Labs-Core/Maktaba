# Implementation Plan — Story 11.8 Dark / Light Theme

> Companion to [story-11-08-dark-light-theme.md](story-11-08-dark-light-theme.md).
> Tokens come from Epic 17 Story 17.1; this story owns runtime selection.
> No FOUC: theme applied **before** first paint via inline blocking script.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Mode | `light` | `dark` | `system` (default). |
| Storage | `localStorage.maktaba.theme`; corrupted value → `system`. |
| API | `<ThemeProvider>` + `useTheme()`; both expose `mode`, `effective` (`light|dark`), and `setMode`. |
| Token source | `:root[data-theme="light"]` and `:root[data-theme="dark"]` define CSS custom properties from Story 17.1 token files. |
| Print | Print stylesheet forces light. |
| Out of scope | Token definitions (Story 17.1); a11y contrast (Story 11.11). |

## 1. Boot-time anti-FOUC script

`web/index.html` ships an inline blocking `<script>` (no module, no defer) that resolves the theme **before** React mounts:

```html
<script>
(function () {
  try {
    var stored = localStorage.getItem('maktaba.theme');
    var mode = (stored === 'light' || stored === 'dark' || stored === 'system') ? stored : 'system';
    var effective = mode === 'system'
      ? (window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light')
      : mode;
    document.documentElement.setAttribute('data-theme', effective);
    document.documentElement.style.colorScheme = effective;
  } catch (e) {
    document.documentElement.setAttribute('data-theme', 'light');
  }
})();
</script>
```

The script is < 800 bytes after minification and is the only inline JS in the document.

## 2. File layout

| Path | Purpose |
|---|---|
| `web/src/theme/ThemeProvider.tsx` | React context + `useEffect` listeners. |
| `web/src/theme/useTheme.ts` | `{ mode, effective, setMode }` consumer. |
| `web/src/theme/tokens.css` | Imports per-mode token CSS files from Story 17.1. |
| `web/src/styles/print.css` | Print overrides forcing light. |
| `web/index.html` | Inline boot script. |
| `web/src/test/theme/ThemeProvider.test.tsx` | Vitest. |
| `web/e2e/theme.spec.ts` | Playwright (FOUC, system follow). |

## 3. ThemeProvider

```tsx
type Mode = 'light' | 'dark' | 'system';
type Effective = 'light' | 'dark';

const ThemeContext = createContext<{ mode: Mode; effective: Effective; setMode(m: Mode): void; }>(/*…*/);

export function ThemeProvider({ children }) {
  const [mode, setModeState] = useState<Mode>(() => {
    const v = localStorage.getItem('maktaba.theme');
    return (v === 'light' || v === 'dark' || v === 'system') ? v : 'system';
  });

  const mql = useMemo(() => window.matchMedia('(prefers-color-scheme: dark)'), []);
  const [systemDark, setSystemDark] = useState(mql.matches);

  useEffect(() => {
    const onChange = (e: MediaQueryListEvent) => setSystemDark(e.matches);
    mql.addEventListener('change', onChange);
    return () => mql.removeEventListener('change', onChange);
  }, [mql]);

  const effective: Effective = mode === 'system' ? (systemDark ? 'dark' : 'light') : mode;

  useEffect(() => {
    document.documentElement.dataset.theme = effective;
    (document.documentElement.style as any).colorScheme = effective;
  }, [effective]);

  const setMode = (m: Mode) => {
    localStorage.setItem('maktaba.theme', m);
    setModeState(m);
  };

  return <ThemeContext.Provider value={{ mode, effective, setMode }}>{children}</ThemeContext.Provider>;
}
```

## 4. Switch animation

Switch animates background and surface tokens over 150 ms:

```css
:root {
  transition: background-color 150ms ease, color 150ms ease;
}
@media (prefers-reduced-motion: reduce) {
  :root { transition: none; }
}
```

No layout properties transition (height, width, padding) — only color tokens — so there's no layout shift.

## 5. Posters and player

- Posters: tokens drive the card surface, not the image; image is unchanged.
- Player background: locked to dark token regardless of theme so subtitles stay legible.
- Subtitle background: token `--cue-bg` keeps WCAG contrast in both themes.

## 6. Forced colors

Honor `forced-colors: active`:

```css
@media (forced-colors: active) {
  /* Do not override; the OS picks colors. */
  .surface, .card, .button { forced-color-adjust: auto; }
}
```

## 7. Print

`print.css` forces `data-theme="light"` regardless of selection:

```css
@media print {
  :root { color-scheme: light; }
  * { background: white !important; color: black !important; }
}
```

## 8. Test cases

### 8.1 Unit

| Test | Asserts |
|---|---|
| `corrupted localStorage falls back to system` | Set `darkk` → `mode === 'system'`. |
| `system follows OS toggle` | Mock matchMedia change → effective flips. |
| `setMode persists` | `setMode('dark')` writes localStorage and sets `data-theme`. |

### 8.2 e2e

| Test | Asserts |
|---|---|
| `cold load with prefers-color-scheme dark, no FOUC` | Capture first frame after navigation; background is dark; no white flash. |
| `OS toggle propagates` | Use Playwright `emulateMedia({ colorScheme })`; UI flips. |
| `print stylesheet light` | `page.emulateMedia({ media: 'print' })`; computed background of `body` is white. |

## 9. Dependencies

- Story 17.1 (token files) is the source of `--color-*` custom properties.
- A11y contrast verification by Story 11.11.
