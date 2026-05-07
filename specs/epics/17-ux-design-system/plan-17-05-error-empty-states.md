# Implementation Plan — Story 17.05 Error states & empty states

> Companion to [story-17-05-error-empty-states.md](story-17-05-error-empty-states.md).
> The story states *what* and *why*; this plan states *how*.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Components | `design/components/src/Feedback/{ErrorState.tsx,EmptyState.tsx,Toast.tsx}`. |
| Error classifier | `design/components/src/Feedback/classifyError.ts` — maps API errors / network errors / domain errors to one of `network`, `server`, `permission`, `not_found`, `validation`. |
| Tone guideline | `design/docs/copy.md` — short, direct, no blame. Strings live in `web/src/i18n/locales/{en,ar}.toml` under `error.*` and `empty.*`. |
| Idempotency | Retry actions use the `Idempotency-Key` HTTP header per [Story 11.10](../11-web-ui/story-11-10-offline-pwa.md). |
| Toast deduplication | A `ToastQueue` that dedupes by `(kind, key)` for 5 seconds. |
| Out of scope | Concrete page error screens (Epic 11/12); skeleton/loading ([Story 17.4](story-17-04-loading-states.md)). |

## 1. Error classification

```ts
// design/components/src/Feedback/classifyError.ts
export type ErrorClass = 'network' | 'server' | 'permission' | 'not_found' | 'validation';
export interface ClassifiedError {
    kind: ErrorClass;
    title: string;
    detail?: string;
    primary: { label: string; onClick?: () => void };
    secondary?: { label: string; onClick: () => void };
    sticky: boolean;
}

export function classify(err: unknown, ctx: { retry?: () => void; lookupHelp?: (slug: string) => void }): ClassifiedError {
    if (err instanceof TypeError && err.message.includes('Failed to fetch'))
        return { kind: 'network', title: t('error.network.title'), primary: { label: t('action.try_again'), onClick: ctx.retry }, sticky: false };
    if (isHTTPError(err, 404))
        return { kind: 'not_found', title: t('error.not_found.title'), primary: { label: t('action.go_home') }, sticky: true };
    if (isHTTPError(err, 401) || isHTTPError(err, 403))
        return { kind: 'permission', title: t('error.permission.title'), detail: t('error.permission.detail'), primary: { label: t('action.contact_admin') }, sticky: true };
    if (isHTTPError(err, [400, 422]))
        return { kind: 'validation', title: t('error.validation.title'), detail: extractValidationDetail(err), primary: { label: t('action.fix') }, sticky: false };
    return { kind: 'server', title: t('error.server.title'), primary: { label: t('action.try_again'), onClick: ctx.retry }, sticky: false };
}
```

## 2. ErrorState

```tsx
// design/components/src/Feedback/ErrorState.tsx
export function ErrorState({ error, onRetry, illustration }: ErrorStateProps) {
    const c = classify(error, { retry: onRetry });
    return (
        <div className={`mk-state mk-state--error mk-state--${c.kind}`} role="alert" aria-live="assertive">
            {illustration ?? <Illustration kind={c.kind} />}
            <h2>{c.title}</h2>
            {c.detail && <p>{c.detail}</p>}
            <div className="mk-state__actions">
                <Button onClick={c.primary.onClick}>{c.primary.label}</Button>
                {c.secondary && <Button variant="ghost" onClick={c.secondary.onClick}>{c.secondary.label}</Button>}
            </div>
        </div>
    );
}
```

`Illustration` is one component selecting an SVG by kind; the SVG is a token-driven flat illustration (lines, two semantic fills) so dark/high-contrast themes work without per-illustration art.

## 3. EmptyState

```tsx
// design/components/src/Feedback/EmptyState.tsx
type EmptyKind = 'first_run' | 'filtered_out' | 'cleared';

export function EmptyState({ kind, primary, secondary, headline, body, illustration }: EmptyStateProps) {
    return (
        <div className={`mk-state mk-state--empty mk-state--${kind}`}>
            {illustration ?? <Illustration kind={kind} />}
            <h2>{headline}</h2>
            {body && <p>{body}</p>}
            <div className="mk-state__actions">
                {primary && <Button onClick={primary.onClick}>{primary.label}</Button>}
                {secondary && <Button variant="ghost" onClick={secondary.onClick}>{secondary.label}</Button>}
            </div>
        </div>
    );
}
```

Convenience presets for the three kinds:

```tsx
export const EmptyFirstRun = () => <EmptyState kind="first_run" headline={t('empty.first_run.headline')} body={t('empty.first_run.body')} primary={{ label: t('action.add_library'), onClick: openAddLibrary }} />;
export const EmptyFilteredOut = ({ onClear }: any) => <EmptyState kind="filtered_out" headline={t('empty.filtered_out.headline')} primary={{ label: t('action.clear_filters'), onClick: onClear }} />;
export const EmptyCleared = () => <EmptyState kind="cleared" headline={t('empty.cleared.headline')} />;
```

## 4. Toast dedupe

```tsx
// design/components/src/Feedback/Toast.tsx
class ToastQueue {
    private active = new Map<string, ToastEntry>();   // key = kind|message hash

    push(t: ToastInput) {
        const key = `${t.kind}:${hash(t.message)}`;
        const existing = this.active.get(key);
        if (existing && Date.now() - existing.shownAt < 5_000) {
            return;        // dedupe within 5 s window
        }
        const entry: ToastEntry = { ...t, shownAt: Date.now(),
            sticky: t.kind === 'permission' || t.kind === 'not_found',
            timeoutMs: t.sticky ? Infinity : 4_000 };
        this.active.set(key, entry);
        this.notify();
        if (entry.timeoutMs !== Infinity) {
            setTimeout(() => this.dismiss(key), entry.timeoutMs);
        }
    }
}
```

The story AC pins:
- Default 4 s.
- Sticky for `permission` and `not_found`.
- 5 s dedupe window: "A user dismisses an error then re-triggers it within 5 s: show only once, dedupe."

## 5. Idempotency on retry

`Async.errorFallback` calls `state.retry()`; the retry uses the same `Idempotency-Key` as the first attempt:

```ts
// query-client.ts
function buildRequest(url: string, body?: any, opts: { idempotencyKey?: string } = {}) {
    return fetch(url, {
        method: body ? 'POST' : 'GET',
        body: body ? JSON.stringify(body) : undefined,
        headers: opts.idempotencyKey ? { 'Idempotency-Key': opts.idempotencyKey } : {},
    });
}
```

The key is generated once per logical operation (`crypto.randomUUID()`) and reused across retries. Server semantics live in [Story 11.10](../11-web-ui/story-11-10-offline-pwa.md).

## 6. "Error during error" consolidation

```ts
function withConsolidation(retry: () => Promise<void>): () => Promise<void> {
    let lastErr: Error | null = null;
    return async () => {
        try { await retry(); lastErr = null; }
        catch (e: any) {
            if (lastErr && lastErr.message === e.message) {
                // Same error twice: do not pile up toasts.
                return;
            }
            lastErr = e;
            toast.push({ kind: 'error', message: e.message });
            throw e;
        }
    };
}
```

Story EC: "An error during an error (retry fails the same way): single consolidated message; no error storm."

## 7. Test plan

### 7.1 Classifier

| Test | What it pins |
|---|---|
| `classifyNetworkOnFailedFetch` | TypeError → `network`. |
| `classifyNotFoundOn404` | 404 → `not_found`, sticky. |
| `classifyPermissionOn403` | 403 → `permission`, sticky. |
| `classifyValidationOn422` | 422 → `validation` with detail extracted from body. |
| `classifyServerOnUnknown` | 500 → `server`. |

### 7.2 Toast

| Test | What it pins |
|---|---|
| `toastDefault4sDismiss` | Push; after 4 s, gone. |
| `toastStickyForPermission` | `kind: 'permission'` → no timeout. |
| `toastDedupedWithin5s` | Push twice in 1 s with same content → 1 visible. |
| `toastErrorStormConsolidated` | Retry fails 5× same error → 1 toast. |

### 7.3 EmptyState

| Test | What it pins |
|---|---|
| `emptyFilteredOutCTAClears` | Click "Clear filters" → CTA fires. |
| `emptyFirstRunCTAOpensWizard` | First-run CTA opens setup. |
| `emptyClearedRendersIcon` | `cleared` variant has the matching illustration. |

### 7.4 Visual regression

| Test | What it pins |
|---|---|
| `chromaticAllErrorClasses` | One snapshot per (kind × theme × direction). |
| `chromaticAllEmptyKinds` | Same. |

## 8. Edge cases — handling table

| Case | Behaviour | Where pinned |
|---|---|---|
| Error during error | Single message; no storm. | `toastErrorStormConsolidated` |
| Permission missing library scope | Surface "Ask your admin to grant access"; sticky. | `classifyPermission` |
| 404 deep-linked video | "Video not found" + "Return to library". | `classifyNotFoundOn404` |
| Filter narrows to nothing | EmptyFilteredOut with primary "Clear filters". | `emptyFilteredOutCTAClears` |
| Network drop, then reconnect | Network toast dismisses on success; user retries via the existing Idempotency-Key. | `testIdempotencyAcrossRetries` |
| Validation error with multiple field issues | Inline messages on each field via `<Form>` (Story 17.2); no toast (validation is field-local). | `testValidationFieldLocal` |
| User dismisses then re-triggers within 5 s | Single toast; subsequent suppressed. | `toastDedupedWithin5s` |
| RTL error rendering | Mirrored layout; bidi isolate around any non-localized substring. | `chromaticRTLErrorState` |
| High-contrast | Token-driven illustration + bold border for visibility. | `chromaticHighContrast` |
| Reduced motion | Toast slide-up replaced with fade only. | `testToastReducedMotion` |

## 9. Acceptance checklist

**Components**
- [ ] `ErrorState`, `EmptyState`, `Toast` shipped.
- [ ] Classifier maps errors to one of 5 classes.

**Behavior**
- [ ] Toast 4 s default; sticky for permission/not_found.
- [ ] 5 s dedupe; error-during-error consolidated.

**Idempotency**
- [ ] Retry reuses Idempotency-Key.

**Tests**
- [ ] All §7 tests pass; Chromatic clean.

**Docs**
- [ ] `design/docs/copy.md` for tone.
- [ ] `specs/epics/17-ux-design-system/README.md` ticks story 17.5.
