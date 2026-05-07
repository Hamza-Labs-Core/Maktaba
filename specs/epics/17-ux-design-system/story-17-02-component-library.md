# Story 17.2 — Component library (buttons, cards, modals, forms)

A React component library used by the web, Capacitor, and Tauri shells;
mirrored in SwiftUI and Compose for TV.

**Anchors:** [`architecture.md` §6](../../architecture.md). Depends on
[Story 17.1](story-17-01-design-tokens.md).

## AC

- Components: Button (5 variants, 3 sizes), IconButton, Link, Input,
  Textarea, Select, Combobox, Toggle, Checkbox, Radio, Card, Modal,
  Drawer, Toast, Tooltip, Tabs, Pagination, ProgressBar, Skeleton,
  EmptyState, ErrorState, Avatar, Badge, Chip, Menu, ContextMenu.
- Every component documented in Storybook with controls and a11y notes.
- Every component accepts a className escape hatch but defaults to
  tokens.
- Forms: a `<Form>` wrapper with `react-hook-form` + zod validation;
  errors localized via i18n.
- Native counterparts (TV): equivalent SwiftUI / Compose components,
  hand-maintained but covered by visual-regression snapshots against
  Storybook.

## TC

- Render Storybook in CI: visual diffs gate merges.
- A button with a loading state shows a spinner and is unfocusable.
- A modal traps focus and closes on `Esc`.
- TV variant of `Card` receives focus and grows by 4% under D-pad
  navigation.

## EC

- A child element overflows a card: visible scrollbar, never clipped
  silently.
- A component used outside a `<ThemeProvider>`: warns in dev, falls
  back to defaults in prod.
- Heavy component (DataTable) is lazy-loaded; falls back to a skeleton
  while loading.
