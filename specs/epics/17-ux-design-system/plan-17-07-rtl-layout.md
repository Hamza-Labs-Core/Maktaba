# Implementation Plan — Story 17.07 Arabic RTL layout system

> Companion to [story-17-07-rtl-layout.md](story-17-07-rtl-layout.md).
> The story states *what* and *why*; this plan states *how*.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Lint rules | New ESLint plugin `eslint-plugin-rtl-discipline` flagging `padding-left`, `margin-right`, `text-align: left/right`, `<svg>` chevrons without `<DirectionalIcon>`, etc. |
| `<DirectionalIcon>` | `design/components/src/RTL/DirectionalIcon.tsx` — selects the LTR or RTL variant based on `dir`. |
| `<NumeralFormat>` | `design/components/src/RTL/NumeralFormat.tsx` — renders Arabic-Indic or Western numerals per setting. |
| Numeral preference | Settings → Advanced toggle; persisted to user profile. |
| Bidi isolate | A `<Bidi>` component wrapping spans containing the opposite script. |
| Native | tvOS uses `Environment.layoutDirection`; AndroidTV uses `LocalLayoutDirection.current`. Token outputs unchanged. |
| Out of scope | Localized strings (existing i18n infrastructure); Arabic STT model selection (Story 17.6). |

## 1. Logical CSS lint

`design/eslint-plugin-rtl-discipline/rules/no-physical-direction.ts`:

```ts
export const noPhysicalDirection: Rule.RuleModule = {
    meta: { messages: { physical: 'Use logical property: {{logical}} instead of {{physical}}.' } },
    create(context) {
        return {
            'Property[key.name=/^padding(Left|Right)$/]'(node) {
                context.report({
                    node, messageId: 'physical',
                    data: { physical: node.key.name, logical: node.key.name === 'paddingLeft' ? 'paddingInlineStart' : 'paddingInlineEnd' },
                });
            },
            // …more rules: marginLeft/Right, left/right, textAlign: 'left'/'right', borderLeft/Right
        };
    },
};
```

Applies to `.css`/`.scss` (via `stylelint-logical-properties` integration) and inline JS styles.

Allowed exceptions:
- `right: 0` for absolutely-positioned elements within RTL-tested contexts (`/* rtl-ok */` comment exemption).
- Third-party components without RTL support → wrap with `dir="ltr"` and add `// rtl-ok: vendor lacks RTL` comment.

## 2. `<DirectionalIcon>`

```tsx
type DirIconKey = 'chevron-right' | 'arrow-right' | 'play' | 'next-track' | 'rewind';

const VARIANTS: Record<DirIconKey, { ltr: string; rtl: string }> = {
    'chevron-right': { ltr: 'chevron-right.svg', rtl: 'chevron-left.svg' },
    'arrow-right':   { ltr: 'arrow-right.svg',   rtl: 'arrow-left.svg' },
    'play':          { ltr: 'play.svg',          rtl: 'play-rtl.svg' },
    // ...
};

export function DirectionalIcon({ name, ...rest }: { name: DirIconKey }) {
    const dir = useDirection();
    const src = VARIANTS[name][dir];
    return <Icon src={src} {...rest} />;
}
```

`useDirection` reads from a React context that mirrors `<html dir>`.

## 3. Numeral format

```tsx
const ARABIC_INDIC = ['٠','١','٢','٣','٤','٥','٦','٧','٨','٩'];

export function NumeralFormat({ value, ...rest }: { value: number | string }) {
    const useArabic = useNumeralPreference();   // user setting
    const s = String(value);
    const out = useArabic ? s.replaceAll(/\d/g, d => ARABIC_INDIC[+d]) : s;
    return <span {...rest}>{out}</span>;
}
```

The story AC: "times always Western for consistency in scrubbers." Implementation: the player scrubber explicitly imports `<NumeralFormat value={time} forceWestern />` when displaying timestamps; `forceWestern` short-circuits the user preference.

## 4. Bidi isolate

```tsx
export function Bidi({ children, dir = 'auto' }: { children: React.ReactNode; dir?: 'auto' | 'ltr' | 'rtl' }) {
    return <span dir={dir} style={{ unicodeBidi: 'isolate' }}>{children}</span>;
}
```

Used within transcript snippets, search results, and any UI element that may contain mixed-direction content. The story AC: "Mixed-direction text: bidi isolates required on every span that may contain the opposite script."

The transcript snippet example: an Arabic transcript may include a Latin name. We render:

```tsx
<p>... <Bidi dir="ltr">{englishWord}</Bidi> ...</p>
```

## 5. Direction-aware player

The story TC: "Player controls in RTL: skip-back is on the right of skip-forward (logically next/previous, not physically)."

Implementation: the player chrome uses `flex` ordering with logical icons; in RTL, the visual order naturally reverses. Concretely:

```tsx
<div className="player-controls" style={{ flexDirection: 'row' /* logical */ }}>
    <IconButton onClick={prev}><DirectionalIcon name="rewind" /></IconButton>
    <IconButton onClick={togglePlay}><Icon name="play" /></IconButton>
    <IconButton onClick={next}><DirectionalIcon name="next-track" /></IconButton>
</div>
```

CSS uses `flex-direction: row` and `justify-content: start/end` (logical), so the same code lays out correctly in both directions.

## 6. Native parity

### tvOS

```swift
@Environment(\.layoutDirection) var layoutDirection
HStack {
    ChevronImage(direction: layoutDirection)   // chooses LTR or RTL asset
}
```

Apple's SwiftUI handles most `HStack`/`VStack` mirroring automatically for `.leading`/`.trailing`; we just need to ensure assets are mirrored.

### AndroidTV

```kotlin
val layoutDirection = LocalLayoutDirection.current
Row {
    DirectionalIcon(name = ChevronRight, direction = layoutDirection)
}
```

## 7. Font fallback

The story EC: "Arabic font fails to load: fall back to system Arabic (Helvetica Arabic, Geeza Pro); never to a Latin font that renders Arabic as boxes."

CSS:

```css
:lang(ar) { font-family: "IBM Plex Sans Arabic", "Helvetica Arabic", "Geeza Pro", sans-serif; }
```

The fallback chain ends at `sans-serif` only after Arabic-capable fonts; on macOS/iOS, "Geeza Pro" is built-in. On Android, "Noto Naskh Arabic" is built-in and added to the chain. Latin fallback is never adjacent to Arabic glyphs in the chain.

We additionally use `font-display: swap` so the user always sees something readable while the network font loads.

## 8. RTL visual regression

Storybook ([Story 17.2](story-17-02-component-library.md)) runs every story in both `dir="ltr"` and `dir="rtl"`. Chromatic captures both. The story AC: "RTL visual regression: every Storybook story has both LTR and RTL snapshots."

A Storybook `globalDecorator` flips `<html dir>` on a story-level parameter; tests are generated as `Component/Variant + Direction`.

## 9. Test plan

### 9.1 Lint

| Test | What it pins |
|---|---|
| `testNoPhysicalPaddingFlagged` | `padding-left: 8px` → ESLint error. |
| `testNoMarginRightFlagged` | Same. |
| `testTextAlignLeftFlagged` | `text-align: left` → error. |
| `testRTLOKExempts` | A line with `/* rtl-ok */` comment is exempted. |

### 9.2 DirectionalIcon

| Test | What it pins |
|---|---|
| `testRendersLTRAssetByDefault` | `dir=ltr` → `chevron-right.svg`. |
| `testRendersRTLAssetWhenRTL` | `dir=rtl` → `chevron-left.svg`. |
| `testUnknownNameTSError` | TypeScript blocks unknown name. |

### 9.3 NumeralFormat

| Test | What it pins |
|---|---|
| `testWesternByDefault` | Renders `42`. |
| `testArabicWhenPrefSet` | Renders `٤٢`. |
| `testForceWesternForcesWestern` | Even with Arabic pref, renders `42`. |
| `testRerendersOnPrefChange` | Toggle pref → re-renders. |

### 9.4 Bidi isolate

| Test | What it pins |
|---|---|
| `testMixedDirectionDoesNotBleed` | Snapshot of an Arabic paragraph with embedded Latin name; visual diff vs. baseline. |

### 9.5 Native

| Test | What it pins |
|---|---|
| `testTVOSChevronMirroredInRTL` | UI test toggles language to ar; chevron asset is the RTL variant. |
| `testAndroidTVHSTACKMirrors` | LayoutDirection RTL → Row reads right-to-left. |

## 10. Edge cases — handling table

| Case | Behaviour | Where pinned |
|---|---|---|
| Third-party RTL-broken component | Wrap with `dir="ltr"`; document deviation; do not silently break. | n/a (manual; documented per use) |
| Arabic font fails to load | Falls back to system Arabic; never to Latin. | CSS chain; visual test. |
| Numerals pref toggled mid-session | Re-renders all `<NumeralFormat>` instances. | `testRerendersOnPrefChange` |
| Mixed direction transcript with English name | `<Bidi dir="ltr">{name}</Bidi>` keeps name LTR inside RTL paragraph. | `testMixedDirectionDoesNotBleed` |
| Player time uses Arabic numerals | `<NumeralFormat forceWestern />` keeps scrubber consistent. | `testForceWesternForcesWestern` |
| RTL focus geometry on TV | "Right" arrow moves to *previous* logically. | `testRTLDpadDirection` (Story 14.3) |
| Hard-coded `right: 0` in legacy CSS | Lint catches; refactored to `inset-inline-end: 0`. | `testNoPhysicalDirectionFlagged` |
| User locale ar but stays in LTR view (browser override) | `<html dir="rtl">` set by the framework based on locale; user can override via Settings; preference precedence: user > locale. | `testUserDirOverridesLocale` |

## 11. Acceptance checklist

**Lint**
- [ ] `eslint-plugin-rtl-discipline` integrated; CI fails on physical CSS.

**Components**
- [ ] `<DirectionalIcon>`, `<NumeralFormat>`, `<Bidi>` shipped.

**Native**
- [ ] tvOS / Compose primitives consume the layout direction.

**Fonts**
- [ ] Arabic fallback chain ends in system Arabic, never Latin-only.

**Visual regression**
- [ ] Every Storybook story has LTR + RTL snapshot.

**Tests**
- [ ] All §9 tests pass; Chromatic clean.

**Docs**
- [ ] `design/docs/rtl.md`.
- [ ] `specs/epics/17-ux-design-system/README.md` ticks story 17.7.
