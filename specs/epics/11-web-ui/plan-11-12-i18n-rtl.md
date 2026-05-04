# Implementation Plan — Story 11.12 i18n (Arabic RTL + English LTR)

> Companion to [story-11-12-i18n-rtl.md](story-11-12-i18n-rtl.md).
> Adding a third locale must require only translation files, not code changes.
> RTL layout system anchored by Story 17.7.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Library | `i18next` + `react-i18next` + `i18next-icu` (ICU MessageFormat) + `i18next-browser-languagedetector`. |
| Translation files | `web/src/i18n/{locale}/common.json` (and namespaces per feature). |
| Locale source order | URL path (`/ar/...`, `/en/...`) → `Accept-Language` → fallback `en`. |
| Direction | `<html dir="rtl|ltr">` set on locale change; CSS uses logical properties (`margin-inline-start`, etc.). |
| Numbers / dates | `Intl.NumberFormat`, `Intl.DateTimeFormat` keyed by active locale. |
| Plurals | ICU MessageFormat. |
| Out of scope | Server-side localization (Epic 7); subtitle bidi inside player (Story 11.3 owns). |

## 1. File layout

| Path | Purpose |
|---|---|
| `web/src/i18n/index.ts` | i18next init (`use(initReactI18next).use(ICU).use(LanguageDetector).init(...)`). |
| `web/src/i18n/en/common.json` | English defaults. |
| `web/src/i18n/ar/common.json` | Arabic translations. |
| `web/src/i18n/en/library.json`, `…/search.json`, etc. | Per-feature namespaces. |
| `web/src/i18n/format.ts` | `formatNumber`, `formatDate`, `formatDuration` helpers using `Intl`. |
| `web/src/i18n/dir.ts` | Maps locale → direction (RTL set: `ar`, `he`, `fa`, `ur`). |
| `web/src/i18n/useT.ts` | Re-exports `useTranslation`; ESLint rule blocks raw `useTranslation` outside this file (forces import path). |
| `web/scripts/i18n-extract.ts` | CI script: scans JSX/TS for `t('key')` and reports missing keys. |
| `web/src/components/Bdi.tsx` | `<bdi dir="auto">{children}</bdi>` for mixed-direction strings. |

## 2. Initialization

```ts
// web/src/i18n/index.ts
import i18next from 'i18next';
import ICU from 'i18next-icu';
import LanguageDetector from 'i18next-browser-languagedetector';
import { initReactI18next } from 'react-i18next';
import { dirOf } from './dir';

await i18next
  .use(ICU)
  .use(LanguageDetector)
  .use(initReactI18next)
  .init({
    fallbackLng: 'en',
    supportedLngs: ['en', 'ar'],
    ns: ['common', 'library', 'search', 'queue', 'settings', 'watch'],
    defaultNS: 'common',
    detection: { order: ['path', 'localStorage', 'navigator'], caches: ['localStorage'] },
    interpolation: { escapeValue: false },
    saveMissing: import.meta.env.DEV,
    missingKeyHandler: (lngs, ns, key) => console.warn(`[i18n] missing ${ns}:${key} for`, lngs),
  });

i18next.on('languageChanged', (lng) => {
  document.documentElement.lang = lng;
  document.documentElement.dir = dirOf(lng);
});
```

## 3. URL routing for locale

```tsx
// router setup
{
  path: '/:locale(en|ar)?/*',
  loader: ({ params }) => {
    if (params.locale && i18next.language !== params.locale) i18next.changeLanguage(params.locale);
  },
}
```

A redirect at startup picks `/en` or `/ar` based on detector if the URL has no prefix.

## 4. Direction & logical CSS

- Tailwind's logical-properties plugin or `tailwindcss-rtl` provides `ms-*`, `me-*`, `ps-*`, `pe-*`, `start-*`, `end-*` utilities.
- All custom CSS uses `margin-inline-start`, `padding-inline-end`, `text-align: start`, `inset-inline-start`.
- Icons that imply direction (chevrons, arrows) carry a `data-flip-rtl` attribute; CSS rotates them under `[dir="rtl"]`.

## 5. Number, date, duration formatting

```ts
// format.ts
export const fmtNum = (n: number, lng = i18next.language) =>
  new Intl.NumberFormat(lng).format(n);
export const fmtDate = (iso: string, lng = i18next.language) =>
  new Intl.DateTimeFormat(lng, { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(iso));
export const fmtDuration = (sec: number, lng = i18next.language) => /* "1ساعة و23دقيقة" via Intl.RelativeTimeFormat / custom */;
```

Arabic locale honors Arabic-Indic digits by default; "Use Western digits" toggle in Settings → Appearance maps to `Intl.NumberFormat('ar', { numberingSystem: 'latn' })`.

## 6. Bidi & isolates

- Mixed-direction strings render inside `<Bdi>`.
- Templates use `{count, plural, one {…} other {…}}` and emit Unicode bidi-isolate characters (`U+2068` … `U+2069`) where strings concatenate variables of different directions.

## 7. Translation extraction in CI

`web/scripts/i18n-extract.ts` walks `*.tsx` and `*.ts` files, parses calls to `t('…')`, and ensures every key exists in every supported locale. Missing keys fail CI with file:line context.

## 8. Edge cases

| Case | Handling |
|---|---|
| 70% translation expansion | Layouts use `min-content`/`auto`; CI snapshot includes a "long pseudo-locale" (de-DE expansions). |
| Arabic font not loaded | `font-display: swap`; Latin glyph fallback while loading. |
| Right-aligned scrollbar conflict in player | Player chrome anchored to logical-end via `inset-inline-end: 0`. |
| Search snippets in opposite-direction language | `<bdi dir="auto">` per snippet. |

## 9. Test cases

### 9.1 Unit

| Test | Asserts |
|---|---|
| `dirOf('ar') === 'rtl'` | Direction map. |
| `format number ar` | `1234567` → `1٬234٬567` (default), `1,234,567` (latn override). |
| `missing key dev warning` | Console warning when key not found in ar. |
| `Bdi wraps mixed text` | Mounts `<bdi>` with `dir="auto"`. |

### 9.2 e2e

| Test | Asserts |
|---|---|
| `switch to Arabic flips layout` | After change, `document.dir = 'rtl'`; sidebar renders on the right. |
| `Arabic search shows LTR snippet inside RTL container` | Visual diff matches baseline. |
| `Path-based locale` | `/ar/library` boots Arabic without flicker. |
| `expansion does not truncate` | Pseudo-locale baselines pass. |

## 10. Performance

- Locale bundles split per namespace; only `common` ships in the initial bundle. Other namespaces lazy-load via `i18next.loadNamespaces()` per route.
- Total Arabic locale data ≤ 30 KB gz.

## 11. Dependencies

- Story 17.7 (RTL layout system) supplies utilities + container hooks.
- Story 11.8 (theme) and Story 11.7 (responsive) play well with logical CSS.
