# Settings: Appearance tab

- **Route:** `/settings/appearance`
- **Component:** `src/pages/Settings.tsx` → `AppearanceTab` (three `RadioGroup`s from `@ds/components/Choice/Radio`); theme logic in `src/lib/theme.ts` (`readMode`, `setMode`, `applyResolvedTheme`, `watchSystemTheme`), OS listener mounted by `ThemeSync` in `src/App.tsx`; language in `src/lib/i18n.tsx` (`setLocale`); first-paint theme in `web/public/theme-boot.js`.
- **API calls:** none. State lives in `localStorage`: `mkt:theme` (`light` | `dark` | `system`), `mkt:locale` (`en` | `ar`), `mkt:density` (`comfortable` | `compact`). DOM: `<html data-theme>` (only `light`/`dark`), `<html dir lang>`, `<html data-density>`.
- **Access:** signed-in user (`RequireAuth`). No admin gate.
- **Existing tests:** `src/lib/theme.test.ts` — covers `readMode` defaults/corrupt value, `resolveTheme` with OS light/dark/no preference/no matchMedia, explicit choice beats OS, `watchSystemTheme` re-applies only in system mode, `theme-boot.js` loaded in `<head>`, storage throwing. `src/lib/i18n.test.tsx` — covers key lookup, switching to Arabic sets `dir=rtl` and Arabic digits. Gaps: nothing tests the Appearance UI itself (radios, checked state, persistence through the tab, density, reload).

jsdom has no `matchMedia`: stub `window.matchMedia = vi.fn(q => ({ matches, media: q, addEventListener, removeEventListener }))` and keep the listener to fire `change`.

## Summary

| ID     | Title                                                   | Category      | Priority | Covered by existing test? |
| ------ | ------------------------------------------------------- | ------------- | -------- | ------------------------- |
| SAP-01 | Renders theme, language and density groups              | Happy path    | P1       | Yes (`Settings.test.tsx`) |
| SAP-02 | Initial checked radios reflect stored values            | Happy path    | P1       | Yes (`Settings.test.tsx`) |
| SAP-03 | No stored values: System, browser language, Comfortable | Empty state   | P2       | Yes (`Settings.test.tsx`) |
| SAP-04 | Corrupt stored values fall back                         | Empty state   | P2       | Yes (`Settings.test.tsx`) |
| SAP-05 | Choose Light / Dark sets data-theme and storage         | Happy path    | P1       | Yes (`Settings.test.tsx`) |
| SAP-06 | System follows the OS, including a live OS switch       | Happy path    | P1       | Yes (`Settings.test.tsx`) |
| SAP-07 | Explicit Light/Dark ignores a live OS switch            | Happy path    | P2       | Yes (`Settings.test.tsx`) |
| SAP-08 | Theme persists across reload with no flash              | Navigation    | P1       | No (e2e-only)             |
| SAP-09 | Switch to Arabic: RTL, Arabic strings, persisted        | RTL           | P1       | Yes (`Settings.test.tsx`) |
| SAP-10 | Switch back to English                                  | RTL           | P2       | Yes (`Settings.test.tsx`) |
| SAP-11 | Density compact sets data-density and persists          | Happy path    | P2       | Yes (`Settings.test.tsx`) |
| SAP-12 | Density is not restored on reload until the tab opens   | Navigation    | P2       | No (it.fails: bug)        |
| SAP-13 | localStorage blocked: choices still apply this session  | Error         | P2       | Yes (`Settings.test.tsx`) |
| SAP-14 | Theme change in another browser tab                     | Navigation    | P3       | No                        |
| SAP-15 | Signed-out deep link redirects to /login                | Permissions   | P1       | Yes (`Settings.test.tsx`) |
| SAP-16 | Render crash shows route error fallback; Back works     | Error         | P1       | Yes (`Settings.test.tsx`) |
| SAP-17 | Keyboard: arrow keys move within each radio group       | Accessibility | P2       | Yes (`Settings.test.tsx`) |
| SAP-18 | Radio groups are labelled                               | Accessibility | P2       | Yes (`Settings.test.tsx`) |
| SAP-19 | Phone width: groups stack, no horizontal scroll         | Responsive    | P3       | No (e2e-only)             |
| SAP-20 | Both themes readable on this tab                        | Theme         | P3       | No                        |

## Cases

### SAP-01 — Renders theme, language and density groups

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes (`Settings.test.tsx`)
- **Preconditions / data:** signed in; empty `localStorage`; `navigator.language = "en-US"`; matchMedia stub `matches: false`.
- **Steps:**
  1. Open `/settings/appearance`.
- **Expected:** Three `radiogroup`s (fieldsets) with legends "Theme", "Language", "Density". Radios: "Light", "Dark", "System"; "English", "العربية"; "Comfortable", "Compact".

### SAP-02 — Initial checked radios reflect stored values

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes (`Settings.test.tsx`)
- **Preconditions / data:** `localStorage`: `mkt:theme=dark`, `mkt:locale=en`, `mkt:density=compact`.
- **Steps:**
  1. Open `/settings/appearance`.
- **Expected:** Exactly one checked radio per group: "Dark", "English", "Compact".

### SAP-03 — No stored values: System, browser language, Comfortable

- **Priority:** P2
- **Category:** Empty state
- **Covered by existing test?** Yes (`Settings.test.tsx`)
- **Preconditions / data:** empty storage; `navigator.language = "ar-EG"` in a second run.
- **Steps:**
  1. Open `/settings/appearance`.
- **Expected:** "System" and "Comfortable" checked. Language "English" with `en-US`, "العربية" with `ar-EG` (and the page is RTL).

### SAP-04 — Corrupt stored values fall back

- **Priority:** P2
- **Category:** Empty state
- **Covered by existing test?** Yes (`Settings.test.tsx`)
- **Preconditions / data:** `mkt:theme="purple"`, `mkt:locale="fr"`, `mkt:density="huge"`.
- **Steps:**
  1. Open `/settings/appearance`.
- **Expected:** "System", the browser language, and "Comfortable" are checked. No crash, no radio left unchecked in any group.

### SAP-05 — Choose Light / Dark sets data-theme and storage

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes (`Settings.test.tsx`)
- **Preconditions / data:** start with `mkt:theme` unset, OS dark.
- **Steps:**
  1. Click "Light".
  2. Click "Dark".
- **Expected:** After 1: `<html data-theme="light">`, `localStorage["mkt:theme"] === "light"`, "Light" checked. After 2: `data-theme="dark"`, storage `dark`. Change applies at once, no reload.

### SAP-06 — System follows the OS, including a live OS switch

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes (`Settings.test.tsx`)
- **Preconditions / data:** full `App` (so `ThemeSync` subscribes); matchMedia `(prefers-color-scheme: light)` stub with `matches: true`; `mkt:theme=dark`.
- **Steps:**
  1. Open `/settings/appearance`, click "System".
  2. Set the stub `matches = false` and fire its `change` listener.
  3. Set `matches = true` and fire `change` again.
- **Expected:** After 1: storage `system`, `data-theme="light"`. After 2: `data-theme="dark"` without any click. After 3: `light`. "System" stays checked throughout.

### SAP-07 — Explicit Light/Dark ignores a live OS switch

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes (`Settings.test.tsx`)
- **Preconditions / data:** full `App`; OS light.
- **Steps:**
  1. Click "Dark".
  2. Fire an OS `change` to light, then to dark.
- **Expected:** `data-theme` stays `dark`; "Dark" stays checked.

### SAP-08 — Theme persists across reload with no flash

- **Priority:** P1
- **Category:** Navigation
- **Covered by existing test?** No (e2e-only)
- **Preconditions / data:** browser e2e; OS dark.
- **Steps:**
  1. On `/settings/appearance` click "Light".
  2. Reload the page; also open `/library` in a new tab.
- **Expected:** First paint is already light (`data-theme="light"` set before the bundle runs). "Light" checked after reload. Other pages use light too.

### SAP-09 — Switch to Arabic: RTL, Arabic strings, persisted

- **Priority:** P1
- **Category:** RTL
- **Covered by existing test?** Yes (`Settings.test.tsx`)
- **Preconditions / data:** English start.
- **Steps:**
  1. Click "العربية".
  2. Reload.
- **Expected:** At once: `<html dir="rtl" lang="ar">`, `localStorage["mkt:locale"] === "ar"`, legends become "السمة", "اللغة", "الكثافة"; theme options "فاتح", "داكن", "النظام"; nav and sidebar mirror. "العربية" checked. After reload still Arabic and RTL.

### SAP-10 — Switch back to English

- **Priority:** P2
- **Category:** RTL
- **Covered by existing test?** Yes (`Settings.test.tsx`)
- **Preconditions / data:** `mkt:locale=ar`.
- **Steps:**
  1. Click "English".
- **Expected:** `<html dir="ltr" lang="en">`, English strings, storage `en`. Theme and density radios keep their checked state.

### SAP-11 — Density compact sets data-density and persists

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes (`Settings.test.tsx`)
- **Preconditions / data:** empty storage.
- **Steps:**
  1. Open `/settings/appearance` (check `data-density` right after mount).
  2. Click "Compact"; then click "Comfortable".
- **Expected:** On mount `data-density="comfortable"` and storage `comfortable` (the effect writes the initial value). After "Compact": `data-density="compact"`, storage `compact`, spacing tokens (`--space-4` = 12px) smaller. After "Comfortable": back.

### SAP-12 — Density is not restored on reload until the tab opens

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** No (it.fails: bug)
- **Preconditions / data:** `mkt:density=compact` stored; full app.
- **Steps:**
  1. Open `/library` (fresh load).
  2. Check `<html data-density>`.
- **Expected (wanted):** `data-density="compact"` on every page right after load. **Current:** `applyDensity` only runs inside `AppearanceTab`, so after a reload the app is comfortable until the user opens `/settings/appearance` (currently unhandled — likely bug).

### SAP-13 — localStorage blocked: choices still apply this session

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (`Settings.test.tsx`)
- **Preconditions / data:** `localStorage.getItem` and `setItem` throw `SecurityError`.
- **Steps:**
  1. Open `/settings/appearance`; click "Light", "العربية", "Compact".
- **Expected:** No crash and no error shown. `data-theme="light"`, `dir="rtl"`, `data-density="compact"` apply. Radios show the new choices.

### SAP-14 — Theme change in another browser tab

- **Priority:** P3
- **Category:** Navigation
- **Covered by existing test?** No
- **Preconditions / data:** e2e, two tabs on `/settings/appearance`.
- **Steps:**
  1. In tab A choose "Dark"; switch to tab B.
- **Expected (wanted):** tab B follows (a `storage` event listener) or at least shows the new choice after reload. **Current:** no `storage` listener, so tab B keeps its old theme and radio until reload. Document behaviour; low priority.

### SAP-15 — Signed-out deep link redirects to /login

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** Yes (`Settings.test.tsx`)
- **Preconditions / data:** `GET /api/auth/me` → 401.
- **Steps:**
  1. Open `/settings/appearance`.
- **Expected:** Redirect to `/login`. Note: the stored theme still applies on the login page (ThemeSync is outside `RequireAuth`).

### SAP-16 — Render crash shows route error fallback; Back works

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes (`Settings.test.tsx`)
- **Preconditions / data:** full app with `AppShell`; start at `/settings`. `vi.mock("../lib/theme", ...)` so `readMode` throws `new Error("boom")` (it runs in a `useState` initializer, so the tab throws during render).
- **Steps:**
  1. Click "Appearance".
  2. Press browser Back; then click "Privacy" in the Settings nav.
- **Expected:** A route-level error fallback (not a blank page); shell visible. Back shows the Account tab without a reload; "Privacy" renders.

### SAP-17 — Keyboard: arrow keys move within each radio group

- **Priority:** P2
- **Category:** Accessibility
- **Covered by existing test?** Yes (`Settings.test.tsx`)
- **Preconditions / data:** `mkt:theme=light`.
- **Steps:**
  1. Tab into the Theme group; press ArrowDown twice; press Tab.
- **Expected:** Arrow keys move and select "Dark" then "System" (native radio behaviour; all radios in a group share `name="theme"`). Tab moves to the Language group, not to the next theme radio. Focus ring visible. In RTL, ArrowLeft/Right follow the mirrored order.

### SAP-18 — Radio groups are labelled

- **Priority:** P2
- **Category:** Accessibility
- **Covered by existing test?** Yes (`Settings.test.tsx`)
- **Preconditions / data:** none.
- **Steps:**
  1. Query `getByRole("radiogroup", { name: "Theme" })` etc.; run axe.
- **Expected:** Each group's accessible name is its legend; every radio has its label as name. axe: no violations.

### SAP-19 — Phone width: groups stack, no horizontal scroll

- **Priority:** P3
- **Category:** Responsive
- **Covered by existing test?** No (e2e-only)
- **Preconditions / data:** e2e at 375x812, English and Arabic.
- **Steps:**
  1. Open `/settings/appearance`.
- **Expected:** Groups stack; radios tappable (target at least 24px); `scrollWidth <= innerWidth`.

### SAP-20 — Both themes readable on this tab

- **Priority:** P3
- **Category:** Theme
- **Covered by existing test?** No
- **Preconditions / data:** e2e.
- **Steps:**
  1. Click "Light", screenshot; click "Dark", screenshot.
- **Expected:** Legends, labels and the checked radio indicator are readable in both; no element keeps the old theme's colours after switching.
