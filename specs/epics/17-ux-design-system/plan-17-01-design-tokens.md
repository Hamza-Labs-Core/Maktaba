# Implementation Plan — Story 17.01 Design tokens

> Companion to [story-17-01-design-tokens.md](story-17-01-design-tokens.md).
> The story states *what* and *why*; this plan states *how*.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Source of truth | `design/tokens/tokens.json` plus `design/tokens/tokens.dark.json` and `design/tokens/tokens.high-contrast.json`. Schema validated via `design/tokens/schema.json`. |
| Build pipeline | `design/tools/build-tokens.{ts,sh}` using Style Dictionary 4.x. |
| Outputs | Web (`web/src/styles/tokens.css`), iOS/tvOS (`apps/tvos/Sources/UI/Generated/Tokens.swift`), Android/AndroidTV (`apps/androidtv/src/main/java/io/maktaba/tv/ui/Generated/Tokens.kt`), Capacitor plugin (`apps/mobile/plugins/tokens/dist/tokens.json`). |
| Versioning | `design/tokens/package.json` with semver; major bump = breaking; minor = additive; patch = visual only. |
| CI | A separate workflow `.github/workflows/tokens.yml` runs `make tokens` and fails on diff. |
| Out of scope | Component bindings ([Story 17.2](story-17-02-component-library.md)); motion (17.3); RTL primitives (17.7); the TV-specific spacing scale ([Story 14.3](../14-tv-apps/plan-14-03-10-foot-ui.md) §1) which extends this token set. |

## 1. Token domains

```jsonc
// design/tokens/tokens.json
{
  "color": {
    "brand": {
      "50":  { "value": "#F5F8FF" },
      "500": { "value": "#1E5AD8" },
      "900": { "value": "#0A1F4D" }
    },
    "neutral": {
      "0":   { "value": "#FFFFFF" },
      "50":  { "value": "#FAFAFA" },
      "100": { "value": "#F4F4F5" },
      "200": { "value": "#E4E4E7" },
      "300": { "value": "#D4D4D8" },
      "400": { "value": "#A1A1AA" },
      "500": { "value": "#71717A" },
      "600": { "value": "#52525B" },
      "700": { "value": "#3F3F46" },
      "800": { "value": "#27272A" },
      "900": { "value": "#18181B" },
      "950": { "value": "#09090B" }
    },
    "semantic": {
      "bg":      { "value": "{color.neutral.50}" },
      "fg":      { "value": "{color.neutral.900}" },
      "accent":  { "value": "{color.brand.500}" },
      "success": { "value": "#0E8C4F" },
      "warn":    { "value": "#B0790E" },
      "error":   { "value": "#B6253A" }
    }
  },
  "type": {
    "display": { "family": { "value": "Inter" }, "size": { "value": "48px" }, "weight": { "value": "700" } },
    "body":    { "family": { "value": "Inter" }, "size": { "value": "16px" }, "weight": { "value": "400" } },
    "mono":    { "family": { "value": "JetBrains Mono" } },
    "transcript": { "family": { "value": "Inter" }, "size": { "value": "18px" } }
  },
  "type-arabic": {
    "body": { "family": { "value": "IBM Plex Sans Arabic" } }
  },
  "spacing": {
    "0": { "value": "0px" }, "1": { "value": "4px" }, "2": { "value": "8px" },
    "3": { "value": "12px" }, "4": { "value": "16px" }, "6": { "value": "24px" },
    "8": { "value": "32px" }, "12": { "value": "48px" }, "16": { "value": "64px" },
    "24": { "value": "96px" }
  },
  "radius": { "sm": { "value": "4px" }, "md": { "value": "8px" }, "lg": { "value": "12px" }, "full": { "value": "9999px" } },
  "elevation": {
    "1": { "value": "0 1px 2px rgba(0,0,0,0.06)" },
    "2": { "value": "0 4px 8px rgba(0,0,0,0.08)" },
    "3": { "value": "0 12px 24px rgba(0,0,0,0.12)" }
  },
  "motion": { "ease-out": { "value": "cubic-bezier(0,0,0.2,1)" }, "ease-in": { "value": "cubic-bezier(0.4,0,1,1)" } },
  "z-index": { "base": { "value": 0 }, "modal": { "value": 1000 }, "toast": { "value": 1100 } },
  "breakpoints": { "sm": { "value": "640px" }, "md": { "value": "768px" }, "lg": { "value": "1024px" }, "xl": { "value": "1280px" } }
}
```

`tokens.dark.json` overrides only the `color.semantic.*` group:

```json
{
  "color": {
    "semantic": {
      "bg": { "value": "{color.neutral.900}" },
      "fg": { "value": "{color.neutral.50}" }
    }
  }
}
```

`tokens.high-contrast.json` overrides for OS-level high-contrast preference; semantic layer flips to a high-WCAG palette.

## 2. Style Dictionary config

`design/tokens/sd.config.cjs`:

```js
module.exports = {
  source: ["design/tokens/tokens.json"],
  platforms: {
    "css/light": {
      transformGroup: "css",
      buildPath: "web/src/styles/",
      files: [{ destination: "tokens.css", format: "css/variables" }],
    },
    "css/dark": {
      include: ["design/tokens/tokens.dark.json"],
      transformGroup: "css",
      buildPath: "web/src/styles/",
      files: [{ destination: "tokens.dark.css", format: "css/variables", options: { selector: "[data-theme='dark']" } }],
    },
    "swift/ios": {
      // Capacitor-bundled iPhone/iPad target (Epic 12).
      transformGroup: "ios-swift",
      buildPath: "apps/mobile/ios/App/Generated/",
      files: [{ destination: "Tokens.swift", format: "ios-swift/class.swift", className: "DesignTokens" }],
    },
    "swift/tvos": {
      transformGroup: "ios-swift",
      buildPath: "apps/tvos/Sources/UI/Generated/",
      files: [{ destination: "Tokens.swift", format: "ios-swift/class.swift", className: "DesignTokens" }],
    },
    "kotlin/androidtv": {
      transformGroup: "android",
      buildPath: "apps/androidtv/src/main/java/io/maktaba/tv/ui/Generated/",
      // The default `kotlin/object` format emits a flat Kotlin object — NOT a
      // Compose `Colors` instance. Use the project-local custom format
      // `kotlin/compose-colors` (registered in `design/tokens/formats/`) to
      // emit `lightColors()` / `darkColors()` factories that Compose can
      // consume directly.
      files: [{ destination: "Tokens.kt", format: "kotlin/compose-colors", className: "DesignTokens", packageName: "io.maktaba.tv.ui.Generated" }],
    },
  },
};
```

Build:

```bash
# Makefile
tokens:
	cd design/tokens && pnpm install && pnpm run build
```

## 3. Schema validation

`design/tokens/schema.json` is a JSON-Schema describing token structure. CI step:

```yaml
- name: validate
  run: ajv validate -s design/tokens/schema.json -d design/tokens/tokens*.json
```

A native target requesting a token that doesn't exist (story EC: "build fails loud, never falls back to a hard-coded default") is enforced by Style Dictionary's `failOnMissing` flag plus Swift/Kotlin compiler errors when a `DesignTokens.color.semantic.foo` reference doesn't resolve.

## 4. Versioning policy

- `design/tokens/package.json` has its own `version` field (semver).
- Token additions = MINOR.
- Token removal/rename = MAJOR (with a `tokens.deprecated.json` shim listing the old key → new key alias for one major release; emits a build-time warning when the old key is used).
- Visual-only tweaks (color shade adjustments) = PATCH.
- Clients pin a major version via `package.json`/`Cargo.toml` and break the build when a non-aligned major lands. CI enforces.

## 5. Theme switching

### Web

```css
/* tokens.css */
:root { --color-bg: var(--color-neutral-50); ... }
[data-theme="dark"] { --color-bg: var(--color-neutral-900); ... }
@media (prefers-contrast: more) { :root { --color-bg: ... } }
```

Theme toggle sets `data-theme="dark"` on `<html>`; OS preference is honored by `prefers-color-scheme`.

### tvOS

`DesignTokens.swift` resolves at runtime via `traitCollection.userInterfaceStyle`:

```swift
public extension DesignTokens.Color.Semantic {
    static var bg: UIColor {
        UIColor { traits in
            traits.userInterfaceStyle == .dark ? darkBg : lightBg
        }
    }
}
```

### AndroidTV

Compose `MaterialTheme` is wired to the `isSystemInDarkTheme()` flag; the generated `DesignTokens` exposes `lightColors()` / `darkColors()` and the theme picks at composition.

## 6. Test plan

### 6.1 Schema

| Test | What it pins |
|---|---|
| `TestSchemaCatchesMissingValue` | A token without a `value` → schema validation fails. |
| `TestSchemaCatchesUnknownDomain` | A top-level key not in `[color, type, spacing, ...]` → error. |
| `TestPatchVersionForVisualTweaks` | A color shade change with no key add/remove → patch bump only. |

### 6.2 Build

| Test | What it pins |
|---|---|
| `TestBuildCSSContainsAllSemanticVars` | `tokens.css` has every semantic key. |
| `TestBuildSwiftCompiles` | Swift target compiles. |
| `TestBuildKotlinCompiles` | Android target compiles. |
| `TestBuildFailOnMissing` | A reference `{color.brand.999}` (non-existent) → build error. |

### 6.3 Theme

| Test | What it pins |
|---|---|
| `TestThemeSwitchUpdatesAllTokens` | Toggle dark; every `--color-*` resolves to dark variant. |
| `TestHighContrastOverridesApply` | `prefers-contrast: more` → semantic colors use high-contrast palette. |

## 7. Edge cases — handling table

| Case | Behaviour | Where pinned |
|---|---|---|
| Missing token at native call-site | Compile error; never falls back to hard-coded default. | `TestBuildFailOnMissing` |
| User in OS high-contrast mode | `tokens.high-contrast.json` overrides; auto-applied. | `TestHighContrastOverridesApply` |
| Token rename mid-version | Old name shipped via `tokens.deprecated.json` for one major; build warning. | `TestDeprecatedAliasWarn` |
| Two simultaneous theme toggles (race) | Latest wins; Compose recomposition idempotent. | `TestRaceToggleIdempotent` |
| Token graph cycle (alias → alias → first) | Style Dictionary errors; CI fails. | `TestCycleDetected` |
| Arabic font missing on TV | Swift falls back to system Arabic ("Helvetica Arabic"); never to Latin. (Implemented in Story 17.7 too.) | `TestArabicFontFallback` |
| Type scale at 4K vs 1080p (TV) | TV-specific scale lives in `tokens.json#tv` (added by [Story 14.3](../14-tv-apps/plan-14-03-10-foot-ui.md)). | n/a |
| New target added (e.g., desktop Tauri Rust) | Add `style-dictionary` platform; outputs to `apps/desktop/src-tauri/src/tokens.rs`. | n/a |
| Manual edit to generated file | CI workflow regenerates and asserts no diff; PR fails if generated file modified by hand. | `tokens.yml` workflow |
| Multiple themes selected (dark + high-contrast) | Apply layered: dark + high-contrast overrides; merge order documented. | `TestLayeredThemes` |

## 8. Acceptance checklist

**Source**
- [ ] `tokens.json`, `tokens.dark.json`, `tokens.high-contrast.json`.
- [ ] Schema validation in CI.

**Build**
- [ ] CSS, Swift, Kotlin, JSON outputs generated by `make tokens`.

**Theme**
- [ ] Dark + light + high-contrast switching.

**Versioning**
- [ ] semver enforced by CI; deprecation alias mechanism.

**Tests**
- [ ] All §6 tests pass.

**Docs**
- [ ] `design/docs/tokens.md` documents the schema and build.
- [ ] `specs/epics/17-ux-design-system/README.md` ticks story 17.1.
