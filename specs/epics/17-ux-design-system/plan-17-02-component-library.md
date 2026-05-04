# Implementation Plan — Story 17.02 Component library

> Companion to [story-17-02-component-library.md](story-17-02-component-library.md).
> The story states *what* and *why*; this plan states *how*.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Web library package | `design/components/` — pnpm workspace package `@maktaba/components`. Shipped as ESM; consumed by web/Tauri/Capacitor shells. |
| Storybook | `design/storybook/` — Storybook 8.x serving the components, with a11y addon and Chromatic visual diffs. |
| Native parity (TV) | Hand-maintained SwiftUI / Compose components in `apps/tvos/Sources/UI/Components/` and `apps/androidtv/.../ui/components/`. |
| Forms | `Form` wrapper using `react-hook-form` + `zod`; localized errors via `i18next`. |
| Visual regression | Chromatic (CI-only) — cross-platform snapshot diffs. |
| Out of scope | Tokens (Story 17.1); motion (17.3); concrete page-level UI (lives in Epics 11/12/13/14). |

## 1. Component inventory

The story AC enumerates: Button (5 variants × 3 sizes), IconButton, Link, Input, Textarea, Select, Combobox, Toggle, Checkbox, Radio, Card, Modal, Drawer, Toast, Tooltip, Tabs, Pagination, ProgressBar, Skeleton, EmptyState, ErrorState, Avatar, Badge, Chip, Menu, ContextMenu.

We organize into:

```
design/components/src/
├── Button/{Button.tsx, IconButton.tsx, Link.tsx, button.css, *.test.tsx, *.stories.tsx}
├── Input/{Input.tsx, Textarea.tsx, Combobox.tsx, Select.tsx, *.tsx}
├── Toggle/{Toggle.tsx, Checkbox.tsx, Radio.tsx}
├── Surface/{Card.tsx, Modal.tsx, Drawer.tsx, Toast.tsx, Tooltip.tsx}
├── Nav/{Tabs.tsx, Pagination.tsx, Menu.tsx, ContextMenu.tsx}
├── Feedback/{ProgressBar.tsx, Skeleton.tsx, EmptyState.tsx, ErrorState.tsx}
├── Identity/{Avatar.tsx, Badge.tsx, Chip.tsx}
├── Form/{Form.tsx, FormField.tsx, useFormSchema.ts}
└── ThemeProvider/{ThemeProvider.tsx, useTheme.ts}
```

## 2. Button — illustrative

```tsx
// design/components/src/Button/Button.tsx
import { forwardRef } from 'react';
import clsx from 'clsx';
import './button.css';

type Variant = 'primary' | 'secondary' | 'ghost' | 'destructive' | 'link';
type Size    = 'sm' | 'md' | 'lg';

export interface ButtonProps extends React.ButtonHTMLAttributes<HTMLButtonElement> {
    variant?: Variant;
    size?: Size;
    loading?: boolean;
    leadingIcon?: React.ReactNode;
    trailingIcon?: React.ReactNode;
}

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(function Button(
    { variant = 'primary', size = 'md', loading, leadingIcon, trailingIcon,
      disabled, className, children, ...rest }, ref) {
    const isDisabled = disabled || loading;
    return (
        <button ref={ref} disabled={isDisabled} aria-busy={loading || undefined}
                className={clsx('mk-btn', `mk-btn--${variant}`, `mk-btn--${size}`,
                                loading && 'is-loading', className)}
                {...rest}>
            {loading ? <Spinner aria-hidden /> : leadingIcon}
            <span className="mk-btn__label">{children}</span>
            {!loading && trailingIcon}
        </button>
    );
});
```

The width is preserved across the loading toggle by reserving a min-width via the label span (story TC: "spinner replaces the label; button width preserved").

`button.css`:

```css
.mk-btn {
    --pad-y: var(--space-2);
    --pad-x: var(--space-4);
    display: inline-flex; align-items: center; gap: var(--space-2);
    border-radius: var(--radius-md);
    font: inherit; line-height: 1;
    transition: background-color var(--motion-duration-quick) var(--motion-ease-out);
}
.mk-btn:focus-visible { outline: 2px solid var(--color-accent); outline-offset: 2px; }
```

## 3. Modal — focus trap & Esc

```tsx
import { Dialog } from '@reach/dialog';   // accessible primitive

export function Modal({ isOpen, onClose, title, children }: ModalProps) {
    return (
        <Dialog isOpen={isOpen} onDismiss={onClose} aria-label={title}
                className="mk-modal">
            <div className="mk-modal__panel">
                <header className="mk-modal__head"><h2>{title}</h2></header>
                <div className="mk-modal__body">{children}</div>
            </div>
        </Dialog>
    );
}
```

`@reach/dialog` traps focus by default; Esc dismisses. The story TC: "A modal traps focus and closes on `Esc`."

## 4. Form wrapper

```tsx
import { useForm, FormProvider, FieldValues } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { z } from 'zod';

export function Form<TSchema extends z.ZodTypeAny>({
    schema, defaultValues, children, onSubmit,
}: { schema: TSchema; defaultValues?: any; children: React.ReactNode;
     onSubmit: (data: z.infer<TSchema>) => void | Promise<void>; }) {
    const methods = useForm({ resolver: zodResolver(schema), defaultValues });
    return (
        <FormProvider {...methods}>
            <form onSubmit={methods.handleSubmit(onSubmit)}>{children}</form>
        </FormProvider>
    );
}
```

`FormField` uses `register`/`useController` and renders error text translated via `i18next`:

```tsx
const { error } = formState;
return (<>
    <Input {...register(name)} aria-invalid={!!error} />
    {error && <span role="alert">{t(`form.${name}.errors.${error.type}`)}</span>}
</>);
```

## 5. ThemeProvider

```tsx
export function ThemeProvider({ children, theme = 'system' }: { children: React.ReactNode; theme?: 'light' | 'dark' | 'system' }) {
    useEffect(() => { document.documentElement.dataset.theme = resolveTheme(theme); }, [theme]);
    if (process.env.NODE_ENV !== 'production' && !inThemeProvider.current)
        console.warn('Component rendered outside ThemeProvider; falling back to system tokens.');
    return <ThemeContext.Provider value={theme}>{children}</ThemeContext.Provider>;
}
```

The dev-warn matches the story EC: "A component used outside a `<ThemeProvider>`: warns in dev, falls back to defaults in prod."

## 6. Storybook + a11y

`design/storybook/.storybook/main.ts`:

```ts
export default {
    stories: ['../components/src/**/*.stories.tsx'],
    addons: ['@storybook/addon-essentials', '@storybook/addon-a11y', '@chromatic-com/storybook'],
    framework: '@storybook/react-vite',
};
```

Each component has at least:
- One story per variant (Button: 5 × 3 = 15 stories minimum).
- LTR + RTL snapshot.
- Light + dark snapshot.
- `parameters.a11y` set to require AA.

Chromatic runs in CI; visual diffs gate merges. The story TC: "Render Storybook in CI: visual diffs gate merges."

## 7. Native parity

The TV variants are hand-maintained SwiftUI / Compose. Each has a Storybook-on-the-platform analog: a "Component Gallery" screen visible only in debug builds. Visual regression: native screenshot tests captured in CI compared to a baseline; diffs surfaced as a dashboard.

Example: `Card` on tvOS takes focus and grows by 4% (story TC). Implementation reuses `FocusableCardStyle` from [Story 14.3](../14-tv-apps/plan-14-03-10-foot-ui.md).

## 8. Test plan

### 8.1 Unit (Vitest + Testing Library)

| Test | What it pins |
|---|---|
| `Button.loading.button.disabled` | `loading=true` makes button unfocusable (`aria-busy=true`, `disabled`). |
| `Button.widthPreservedOnLoading` | Width with label and width with spinner equal. |
| `Modal.focusTrap` | Tab cycles within modal; Shift-Tab cycles backward. |
| `Modal.escClosesAndRestoresFocus` | Press Esc → close → focus returns to invoker. |
| `Form.validatesAgainstSchema` | Submit invalid → translated error appears. |
| `ContextMenu.keyboardActivation` | Right-click + Enter on item triggers `onSelect`. |

### 8.2 Storybook a11y

| Test | What it pins |
|---|---|
| `axeOnEveryStory` | Storybook a11y addon reports zero violations at AA on all stories. |
| `forceColorsStory` | High-contrast media renders with no visual broken state. |

### 8.3 Visual regression (Chromatic)

| Test | What it pins |
|---|---|
| `chromaticDiff` | PR diffs shown for review; merges blocked until approved. |

### 8.4 Native parity

| Test | What it pins |
|---|---|
| `tvosCardFocusGrows4pct` | Snapshot scaled 1.04 on focus. |
| `composeButtonRendersAtSize` | Three sizes render with token spacing. |

## 9. Edge cases — handling table

| Case | Behaviour | Where pinned |
|---|---|---|
| Child overflows card | Visible scrollbar; never silent-clip. | `Card.overflowScrolls` |
| Component used outside ThemeProvider | Dev warning; defaults applied in prod. | `ThemeProviderWarn` |
| Lazy-loaded heavy component | Skeleton during chunk fetch; error fallback if chunk fails. | `LazyDataTableErrorBoundary` |
| Tooltip near viewport edge | `@radix-ui/react-tooltip` flips/clamps. | `Tooltip.viewportClamp` |
| Disabled button receives Enter | No form submission (disabled prevents). | `Button.disabledIgnoresEnter` |
| Submit during loading | `loading=true` blocks click; second submit ignored. | `Form.preventDoubleSubmit` |
| RTL combobox | Caret on right; menu opens flipped. | `Combobox.rtlSnapshot` |
| Toast queue overflow (10 simultaneous) | Stack; collapse to "8 more"; oldest auto-dismiss. | `Toast.overflowCollapses` |
| Pagination at edge | Prev disabled at 1; Next disabled at last. | `Pagination.edges` |
| Native counterpart drift | Visual-regression diff fails → CI blocks merge. | Chromatic |

## 10. Dependencies

| Dep | Version | Why |
|---|---|---|
| `react-hook-form` | 7.x | Form state. |
| `zod` | 3.x | Schema validation. |
| `@hookform/resolvers` | 3.x | RHF + zod glue. |
| `@reach/dialog` | 0.18.x | Accessible modal primitive. |
| `@radix-ui/react-*` | latest | Tooltip, Tabs, ContextMenu primitives. |
| `clsx` | latest | className composition. |
| `i18next` | latest | error message translation. |
| Storybook | 8.x | docs + visual regression. |

## 11. Acceptance checklist

**Library**
- [ ] All components in §1 inventory shipped.
- [ ] Each accepts `className` escape hatch.

**Storybook**
- [ ] Every component has stories with a11y notes; LTR + RTL; light + dark.

**Forms**
- [ ] `<Form>` + `<FormField>` work with zod schema; errors localized.

**Native**
- [ ] tvOS / Compose counterparts exist; visual snapshots gated.

**Tests**
- [ ] All §8 tests pass; Chromatic clean.

**Docs**
- [ ] `specs/epics/17-ux-design-system/README.md` ticks story 17.2.
