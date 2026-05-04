# Implementation Plan — Story 17.06 Onboarding flow (first-time setup wizard)

> Companion to [story-17-06-onboarding.md](story-17-06-onboarding.md).
> The story states *what* and *why*; this plan states *how*.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Web wizard | `web/src/features/onboarding/{Wizard.tsx,steps/{Step1Account.tsx,Step2Library.tsx,Step3STT.tsx,Step4LangTheme.tsx},useWizardState.ts,api.ts}`. |
| Server-side state | New table `onboarding_state` storing the resume position so an interrupted wizard restarts where it left off (story EC). |
| Resume detection | The router checks `onboarding_state.completed_at IS NULL` on every page load and redirects to `/onboarding/{step}` if non-completed. |
| STT auto-detect | The actual capability probe runs in **Pipeline (Python)** at `pipeline/src/maktaba_pipeline/transcribe/probe.py` (the `transcribe` package lives in Pipeline per architecture §1.2/3.4, not API). `GET /api/setup/stt-probe` is a thin Go shim in `api/internal/setup/probe.go` that calls Pipeline's RPC and caches the result for the wizard's lifetime. Both halves ship with this story. |
| Library probe | Reuses Epic 9 Story 9.6's manual-scan endpoint internally. The new `POST /api/libraries/probe` (size, file count, codec mix preview) is owned here because it's wizard-specific; future Library Settings can import it. |
| Tour carousel | `web/src/features/onboarding/TourCarousel.tsx` shown after step 4; dismissible. |
| Out of scope | Per-step business logic that already lives elsewhere (account creation in Epic 10, library config in Epic 9, STT in Epic 3). |

## 1. Schema

`shared/db/migrations/0070_onboarding.sql`:

```sql
-- +goose Up
-- +goose StatementBegin
CREATE TABLE onboarding_state (
    id           SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    -- Generous range so a future story 17.6.x can extend the wizard
    -- without a CHECK migration; the canonical step set today is 1–4
    -- but adding an "import settings" or "join household" step in v2
    -- shouldn't require a schema change.
    current_step INTEGER NOT NULL DEFAULT 1 CHECK (current_step BETWEEN 1 AND 16),
    completed_at TIMESTAMPTZ,
    tour_dismissed_at TIMESTAMPTZ,
    started_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Snapshots of step inputs in case the user closes mid-wizard:
    step_data    JSONB NOT NULL DEFAULT '{}'::jsonb
);
INSERT INTO onboarding_state (id) VALUES (1) ON CONFLICT DO NOTHING;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS onboarding_state;
-- +goose StatementEnd
```

Singleton row on a single-server install. For multi-user installs, a future `(user_id, ...)` shape would replace this; the v1 wizard targets the admin's first-run setup so a singleton fits.

## 2. Endpoints

```go
// GET /api/setup/state -> { current_step, completed_at, tour_dismissed_at }
// PATCH /api/setup/state body: { current_step?, step_data? }
// POST /api/setup/complete -> { completed_at }
// POST /api/setup/tour/dismiss
// GET /api/setup/stt-probe -> { recommended: "mlx" | "cuda" | "whisper-cpu", reason: "..." }
```

The wizard stores incremental progress on each step transition (`PATCH /api/setup/state`); step data is in `step_data` so we can resume mid-form.

## 3. Wizard architecture

```tsx
// Wizard.tsx
export function OnboardingWizard() {
    const { state, set } = useWizardState();
    if (state.completed_at) return <Navigate to="/" replace />;
    const Step = STEPS[state.current_step - 1];
    return (
        <div className="mk-onboarding">
            <ProgressBar value={state.current_step / 4} />
            <header>
                {state.current_step > 1 && <BackButton onClick={() => set({ current_step: state.current_step - 1 })} />}
            </header>
            <Step state={state} onAdvance={(data) => set({ current_step: state.current_step + 1, step_data: data })} />
        </div>
    );
}

const STEPS = [Step1Account, Step2Library, Step3STT, Step4LangTheme];
```

`useWizardState` is a thin `useQuery` + `useMutation` pair against `/api/setup/state`.

## 4. Step 1 — Account

In multi-user install: server-name + admin password. In single-user mode (bootstrap token present): skip entirely.

```tsx
function Step1Account({ onAdvance }: StepProps) {
    if (isSingleUserMode()) return <SkippedStep onAdvance={() => onAdvance({})} />;
    const schema = z.object({
        serverName: z.string().min(1).max(64),
        password:   z.string().min(8).max(128),
        confirm:    z.string(),
    }).refine(d => d.password === d.confirm, { path: ['confirm'], message: 'mismatch' });
    return (
        <Form schema={schema} onSubmit={async (d) => { await api.adduser(d); await onAdvance({}); }}>
            <FormField name="serverName" label={t('onboarding.server_name')} />
            <FormField name="password" type="password" autoComplete="new-password" />
            <FormField name="confirm"  type="password" autoComplete="new-password" />
            <Button type="submit">{t('action.next')}</Button>
        </Form>
    );
}
```

The password creation uses Epic 10 Story 10.1's `POST /api/users` admin endpoint.

## 5. Step 2 — Library

Picks a library root. The browser's filesystem-API isn't available in all contexts, so we offer two paths:
- "Pick a folder…" → `showDirectoryPicker()` (where supported).
- "Paste path" → text field with server-side validation.

Estimated size: `POST /api/libraries/probe { path }` returns `{ size_bytes, video_count, ... }`.

EC: "Disk has no writable library root: surface 'Create a folder for me?' CTA that creates `$HOME/Maktaba/Library`." Implementation: probe returns `writable=false`; we render a CTA `POST /api/libraries/init-default`.

## 6. Step 3 — STT

```tsx
function Step3STT({ onAdvance }: StepProps) {
    const { data: probe } = useQuery('stt-probe', () => api.sttProbe());
    return (
        <RadioGroup defaultValue={probe?.recommended}>
            <Option value="mlx" disabled={!probe?.candidates.includes('mlx')}>
                <h3>MLX (Apple Silicon)</h3>
                <p>Best speed. Requires Apple Silicon.</p>
            </Option>
            <Option value="cuda" disabled={!probe?.candidates.includes('cuda')}>
                <h3>CUDA</h3>
                <p>Fastest on NVIDIA GPUs.</p>
            </Option>
            <Option value="whisper-cpu">
                <h3>CPU (whisper.cpp)</h3>
                <p>{probe?.recommended === 'whisper-cpu' ? 'Auto-detected.' : 'Slow on CPU; expect 5–10× slower than realtime.'}</p>
            </Option>
        </RadioGroup>
    );
}
```

The story TC: "Choose `whisper-cpu`: Step 3 warns about realtime factor." The radio's body text encodes the warning.

## 7. Step 4 — Language + theme

```tsx
function Step4LangTheme() {
    return (
        <>
            <Section title={t('onboarding.language')}>
                <RadioGroup options={[{ value: 'en', label: 'English' }, { value: 'ar', label: 'العربية' }]} />
            </Section>
            <Section title={t('onboarding.theme')}>
                <RadioGroup options={[
                    { value: 'system', label: t('theme.system') },
                    { value: 'light',  label: t('theme.light') },
                    { value: 'dark',   label: t('theme.dark') },
                ]} />
            </Section>
            <Button onClick={complete}>{t('action.finish')}</Button>
        </>
    );
}
```

## 8. Tour carousel

After completion: a 4-step pop-over carousel: Library, Search, Queue, Player. Dismissible from any panel; once dismissed (`tour_dismissed_at`), never shown unless the user clicks "Show me again" in Settings → About (which sends `POST /api/setup/tour/show-again`).

```tsx
function TourCarousel() {
    const [step, setStep] = useState(0);
    return (
        <Modal preventClose={false} title={t('tour.title')}>
            <Slide n={step} />
            <ProgressDots count={4} active={step} />
            <div className="actions">
                {step > 0 && <Button variant="ghost" onClick={() => setStep(step - 1)}>{t('action.back')}</Button>}
                {step < 3
                    ? <Button onClick={() => setStep(step + 1)}>{t('action.next')}</Button>
                    : <Button onClick={dismiss}>{t('action.done')}</Button>}
            </div>
        </Modal>
    );
}
```

## 9. Test plan

### 9.1 Server

| Test | What it pins |
|---|---|
| `TestStateSingletonInsertedAtMigration` | Row exists with `current_step = 1`. |
| `TestPatchAdvancesStep` | PATCH `current_step=2` → row updated. |
| `TestCompleteSetsCompletedAt` | POST → row `completed_at != null`. |
| `TestSTTProbeReturnsCandidates` | On Apple Silicon CI runner, `mlx` is in candidates. |
| `TestSTTProbeAlwaysIncludesCPU` | `whisper-cpu` always present. |
| `TestLibrariesProbePathReturnsSizes` | Stub a 100-file directory; `video_count = 100`. |
| `TestInitDefaultCreatesHomeMaktaba` | Disk has no library; init-default creates `$HOME/Maktaba/Library`. |

### 9.2 Wizard UI

| Test | What it pins |
|---|---|
| `testSingleUserModeSkipsStep1` | Bootstrap token → wizard renders Step 2 first. |
| `testCancelLandsResumeBanner` | Close mid-wizard; reload home → "Resume setup" banner. |
| `testSkipDefaultsApplied` | Skip step 4 → defaults `system` + `en` saved. |
| `testCompleteRedirectsHome` | Finish → router replaces to `/`. |
| `testTourDismissPersistsAcrossLaunches` | Dismiss → reload → no tour. |
| `testTourShowMeAgainReopens` | Click "Show me again" → tour appears next launch. |
| `testCompletionResumeIfInterrupted` | Mock OS reboot mid-Step 3 → server-persisted state intact; reopen → land in Step 3. |

## 10. Edge cases — handling table

| Case | Behaviour | Where pinned |
|---|---|---|
| Single-user mode | Step 1 skipped; lands on Step 2. | `testSingleUserModeSkipsStep1` |
| Whisper-CPU chosen | Body text warns about realtime factor. | n/a (rendered text) |
| Cancel mid-wizard | Resume banner on next launch. | `testCancelLandsResumeBanner` |
| OS reboot mid-wizard | Server-persisted; resume exact. | `testCompletionResumeIfInterrupted` |
| Disk has no writable library root | "Create a folder for me?" CTA. | `TestInitDefaultCreatesHomeMaktaba` |
| STT auto-detect fails (no GPU/MLX) | Default `whisper-cpu` with warning. | `TestSTTProbeAlwaysIncludesCPU` |
| User picks Arabic on a server with no Arabic STT model bundled | The STT step warns and queues a model download. | `testArabicModelDownloadQueued` |
| Wizard reopened by an admin who is not the bootstrap admin | Same `current_step` view; permitted only for admins (middleware). | `testNonAdminBlocked` |
| Concurrent completion (admin + admin) | Idempotent; second completion sees the row already complete. | `testCompleteIdempotent` |
| User dismisses tour and clicks "Show me again" | Resets `tour_dismissed_at = null`; tour appears once on next launch. | `testTourShowMeAgainReopens` |

## 11. Acceptance checklist

**Schema**
- [ ] `onboarding_state` exists; resume works.

**Endpoints**
- [ ] state, complete, tour, stt-probe, libraries/probe, libraries/init-default.

**Wizard**
- [ ] All four steps; skip/back; bootstrap-mode skips Step 1.

**Tour**
- [ ] Carousel after completion; dismissible; show-me-again.

**Tests**
- [ ] All §9 tests pass.

**Docs**
- [ ] `specs/epics/17-ux-design-system/README.md` ticks story 17.6.
