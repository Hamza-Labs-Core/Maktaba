# Implementation Plan — Story 14.3 10-foot UI design (large text, D-pad navigation)

> Companion to [story-14-03-10-foot-ui.md](story-14-03-10-foot-ui.md).
> The story states *what* and *why*; this plan states *how*.
> Tokens come from [Story 17.1](../17-ux-design-system/story-17-01-design-tokens.md);
> this story owns the **TV-specific** spacing scale + focus primitives.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Token additions | New token group `tv` inside `design/tokens/tokens.json` with type sizes, spacing scale, safe-area inset, focus-ring, focus-grow factors. Generated outputs land in tvOS Swift + AndroidTV Kotlin from [Story 17.1](../17-ux-design-system/story-17-01-design-tokens.md)'s pipeline. |
| tvOS primitives | `apps/tvos/Sources/UI/{FocusRing,FocusableCardStyle,FocusGrid,SafeAreaInset}.swift`. |
| AndroidTV primitives | `apps/androidtv/src/main/java/io/maktaba/tv/ui/{FocusRing.kt,FocusGrid.kt,SafeArea.kt,FocusableCard.kt}`. |
| Documentation | `design/docs/10-foot.md` — checklist, geometry diagrams, do/don't. |
| Out of scope | Tokens themselves (Story 17.1 owns the source-of-truth and pipeline); the actual screens (Stories 14.1, 14.2 own composition). |

## 1. Token additions (TV section)

```json
// design/tokens/tokens.json (excerpt)
{
  "tv": {
    "type": {
      "body":     { "1080p": "28pt", "4k": "36pt" },
      "title":    { "1080p": "44pt", "4k": "60pt" },
      "display":  { "1080p": "72pt", "4k": "96pt" },
      "row-label":{ "1080p": "32pt", "4k": "44pt" }
    },
    "spacing": {
      "xs":  "8pt",  "sm":  "16pt", "md":  "32pt",
      "lg":  "48pt", "xl":  "64pt", "row-gap": "56pt"
    },
    "safe-area": {
      "horizontal-pct": "0.05",
      "vertical-pct":   "0.05"
    },
    "focus": {
      "ring-width":   "4pt",
      "ring-blur":    "12pt",
      "ring-color":   "{color.brand.500}",
      "grow-scale":   "1.04",
      "grow-duration":"150ms"
    }
  }
}
```

The pipeline generates `TVTokens.swift` and `TVTokens.kt`, which the primitives below consume. Web does **not** consume `tv.*` tokens.

## 2. tvOS primitives

### 2.1 `FocusableCardStyle`

```swift
struct FocusableCardStyle: ButtonStyle {
    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .scaleEffect(isFocused ? TVTokens.Focus.growScale : 1.0)
            .overlay(
                RoundedRectangle(cornerRadius: 12)
                    .stroke(TVTokens.Focus.ringColor, lineWidth: TVTokens.Focus.ringWidth)
                    .blur(radius: isFocused ? TVTokens.Focus.ringBlur : 0)
                    .opacity(isFocused ? 1 : 0)
            )
            .animation(.easeOut(duration: TVTokens.Focus.growDuration), value: isFocused)
    }
    @Environment(\.isFocused) private var isFocused
}
```

The animation respects `prefers-reduced-motion` via `@Environment(\.accessibilityReduceMotion)` — when reduced, the scale is omitted but the focus ring still appears (focus must always be visible).

### 2.2 `FocusGrid`

A wrapper around SwiftUI's focus engine that enforces grid geometry:

```swift
struct FocusGrid<Content: View>: View {
    let columns: Int
    let content: () -> Content
    var body: some View {
        LazyVGrid(columns: Array(repeating: GridItem(.flexible(), spacing: TVTokens.Spacing.md),
                                  count: columns),
                  spacing: TVTokens.Spacing.md, content: content)
        .focusSection()    // confines D-pad horizontal moves to the row
    }
}
```

`.focusSection()` is the SwiftUI primitive that prevents focus from "jumping out" of a row on a horizontal D-pad press; this is what gives the AC's "rows use horizontal-snap focus" guarantee.

### 2.3 Safe-area inset

```swift
struct TVSafeArea: ViewModifier {
    func body(content: Content) -> some View {
        GeometryReader { proxy in
            content.padding(EdgeInsets(
                top:    proxy.size.height * TVTokens.SafeArea.verticalPct,
                leading:proxy.size.width  * TVTokens.SafeArea.horizontalPct,
                bottom: proxy.size.height * TVTokens.SafeArea.verticalPct,
                trailing:proxy.size.width * TVTokens.SafeArea.horizontalPct))
        }
    }
}

extension View { func tvSafeArea() -> some View { modifier(TVSafeArea()) } }
```

### 2.4 Back-stack focus restoration

```swift
struct FocusRestoringNavigation<Content: View>: View {
    @Namespace private var ns
    let content: () -> Content
    var body: some View {
        NavigationStack { content() }
            .focusScope(ns)        // restores focus on pop
    }
}
```

This wires the AC: `"Back" returns to the previous focus, not the top of the row`. Without `.focusScope`, SwiftUI's default is to restore to the first focusable in the new view.

## 3. AndroidTV primitives

### 3.1 `Modifier.focusableCard()`

```kotlin
fun Modifier.focusableCard(
    onClick: () -> Unit,
): Modifier = composed {
    val isFocused by remember { mutableStateOf(false) }
    var focused by remember { mutableStateOf(false) }
    val scale by animateFloatAsState(
        targetValue = if (focused) TvTokens.Focus.growScale else 1f,
        animationSpec = tween(TvTokens.Focus.growDurationMs, easing = FastOutLinearInEasing)
    )
    this
        .onFocusChanged { focused = it.isFocused }
        .focusable()
        .scale(scale)
        .drawWithContent {
            drawContent()
            if (focused) drawRoundRect(
                color = TvTokens.Focus.ringColor,
                style = Stroke(width = TvTokens.Focus.ringWidthPx),
                cornerRadius = CornerRadius(12.dp.toPx())
            )
        }
        .clickable(onClick = onClick)
}
```

### 3.2 `Modifier.focusRestorer()`

We use the official `androidx.tv.foundation.lazy.list.focusRestorer()` modifier on the parent `LazyRow`/`LazyColumn` to preserve column focus across vertical/horizontal moves. This is the AC's "rows use horizontal-snap focus; columns use vertical-snap" guarantee.

### 3.3 Safe-area

```kotlin
@Composable
fun TvSafeArea(content: @Composable BoxScope.() -> Unit) {
    BoxWithConstraints {
        val hp = maxWidth  * TvTokens.SafeArea.horizontalPct
        val vp = maxHeight * TvTokens.SafeArea.verticalPct
        Box(Modifier.padding(horizontal = hp, vertical = vp), content = content)
    }
}
```

## 4. D-pad geometry

The AC says "every focusable element sits on a predictable grid; diagonal moves not required for any flow." Implementation:

- Rows use `LazyRow` (AndroidTV) / `ScrollView(.horizontal) { LazyHStack }` (tvOS) only — never offset cards by an arbitrary `y`.
- Card sizes within a row are uniform width × height. If content varies (e.g., a tall poster vs. a square thumbnail), pad to the dominant aspect.
- Modal triggers focus the modal's first focusable on open and restore the prior focus on close (handled by `.focusScope` on tvOS, by `FocusRequester` on AndroidTV).

## 5. Documentation

`design/docs/10-foot.md` is the human-readable companion. Sections:

1. Type scale at 1080p / 4K with sample renderings.
2. Grid geometry diagrams.
3. Focus-ring spec with reduced-motion variant.
4. Safe-area diagram.
5. Do/don't gallery: "don't put a focusable button inside a row that scrolls horizontally without `focusSection`/`focusRestorer`."
6. Acceptance checklist for designers handing off to engineering.

## 6. Test plan

### 6.1 tvOS UI tests

| Test | What it pins |
|---|---|
| `testFocusRingVisibleOnFocus` | Snapshot of an unfocused vs. focused card — ring opacity transitions from 0 to 1 in 150 ms. |
| `testRowConfinesHorizontalDpad` | A row of 5 cards inside a column; press right 5×: focus stays in the row (does not jump down). |
| `testBackRestoresPriorFocus` | Focus card 3 → push detail → BACK → focus is still on card 3 (not card 1). |
| `testReducedMotionPreservesFocusRing` | Set `accessibilityReduceMotion = true`; ring still appears, scale-up is skipped. |
| `testSafeAreaInsetAt1080p` | Render at 1920×1080; first focusable's CGRect.origin = (96, 54). |

### 6.2 AndroidTV instrumentation tests

| Test | What it pins |
|---|---|
| `focusableCardScalesOnFocus` | Compose test rule asserts `TestTagFocusedCard` size ratio = 1.04. |
| `dpadHorizontalConfinedToRow` | Inject `KEYCODE_DPAD_RIGHT` × 49 in a 50-card row → last card focused; no row change. |
| `dpadVerticalSnapsToColumn` | Focus column 3 of row A; `KEYCODE_DPAD_DOWN` → row B's column 3 focused (focusRestorer). |
| `safeAreaInsetMatchesPercent` | Compose layout assertion: padding equals 5% of width / height. |
| `noDiagonalRequired` | Test harness records every focus traversal in a critical path; no two consecutive focuses differ in both x and y. |

### 6.3 Visual regression (Storybook for native)

- Snapshot every primitive (`FocusableCard`, `FocusGrid`, `TVSafeArea`) under: focused/unfocused, light/dark, LTR/RTL, 1080p/4K viewport.

## 7. Edge cases — handling table

| Case | Behaviour | Where pinned |
|---|---|---|
| Row with one item | Left/right wraps within the row (single item is its own neighbor). | `testSingleItemRowWraps` |
| Modal opens | First focusable in the modal gets focus; BACK exits the modal, restores prior focus. | `testModalFocusTrap` |
| User toggles reduce-motion mid-session | Focus ring transitions disabled instantly; scale animation skipped on next focus change. | `testReduceMotionToggle` |
| 4K display reports physical 1080p mode | Type scale follows the *render* resolution, not panel pixels — body falls to 28pt. | `testTypeScaleByRenderRes` |
| RTL locale | Focus geometry mirrors: D-pad right moves to the *previous* item visually but to `next` logically. | `testRTLDpadDirection` |
| A row whose card heights differ | Padding equalizes the card heights to the tallest, so focus geometry stays predictable. | `testMixedCardHeights` |
| Focus loss after deep-link from Top Shelf | App focuses the deep-linked card; if that view scrolls into the top, no jump-cut. | `testDeepLinkFocusOrigin` |

## 8. Acceptance checklist

**Tokens**
- [ ] `tv.*` tokens exist in `design/tokens/tokens.json`.
- [ ] Generated `TVTokens.swift` and `TVTokens.kt` compile.

**Primitives**
- [ ] tvOS: `FocusableCardStyle`, `FocusGrid`, `TVSafeArea`, `FocusRestoringNavigation` shipped.
- [ ] AndroidTV: `Modifier.focusableCard`, `Modifier.focusRestorer` wired, `TvSafeArea` shipped.

**Documentation**
- [ ] `design/docs/10-foot.md` published with diagrams.

**Tests**
- [ ] All §6 tests pass on the iOS Simulator (tvOS variant) and Android TV emulator.

**Docs**
- [ ] `specs/epics/14-tv-apps/README.md` ticks story 14.3.
