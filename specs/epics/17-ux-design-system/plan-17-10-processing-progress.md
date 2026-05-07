# Implementation Plan — Story 17.10 Processing progress visualization

> Companion to [story-17-10-processing-progress.md](story-17-10-processing-progress.md).
> The story states *what* and *why*; this plan states *how*.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Web component | `web/src/features/queue/JobProgress.tsx` plus subcomponents `ProgressBar`, `StageStrip`, `ETAReadout`, `ResumeMarker`. |
| Native parity | tvOS / AndroidTV / mobile mirror the same composition; tokens (Story 17.1) keep colors/spacing in sync. |
| Stage names | The canonical set per [REVIEW §1.3.b/c](../../REVIEW.md): `scan, probe, extract, transcribe, subtitle_gen, index, thumbnail`. Imported from a shared TypeScript module so server + client agree. |
| ETA computation | Client-side rolling window over the last 5 progress segments; suppresses ETA until at least 3 segments committed (per [Story 11.5 EC](../11-web-ui/story-11-05-processing-queue-dashboard.md)). |
| Indeterminate mode | When `total_duration_seconds` is null (probe pending), the bar shows animated stripes; ETA hidden. |
| Out of scope | The queue dashboard layout ([Story 11.5](../11-web-ui/story-11-05-processing-queue-dashboard.md)); the underlying job data model (Epic 6). |

## 1. Component composition

```tsx
// web/src/features/queue/JobProgress.tsx
export function JobProgress({ job }: { job: Job }) {
    const reduced = useReducedMotion();
    const eta = useETA(job);
    return (
        <div className="mk-job">
            <header className="mk-job__head">
                <h3>{job.video.title}</h3>
                <Badge state={job.state} />
                <StageStrip current={job.stage} />
            </header>
            <ProgressBar
                total={job.totalDurationSeconds}
                done={job.donePositionSeconds}
                pending={job.pendingPositionSeconds}
                indeterminate={job.totalDurationSeconds == null}
                animated={!reduced}
                resumeAt={job.resumeAtSeconds}
            />
            <footer className="mk-job__foot">
                <Annotation done={job.donePositionSeconds} total={job.totalDurationSeconds} />
                {eta && <ETAReadout value={eta} />}
                <Tooltip body={<JobDetailsTooltip job={job} />}>
                    <Inline state={job.state} />
                </Tooltip>
                <Inline>
                    {job.state === 'running' && <PauseBtn id={job.id} />}
                    {job.state === 'paused' && <ResumeBtn id={job.id} />}
                    {(job.state === 'paused' || job.state === 'pause_pending') &&
                        <ForcePauseBtn id={job.id} after={job.pauseGraceSec} />}
                    <CancelBtn id={job.id} />
                </Inline>
            </footer>
        </div>
    );
}
```

## 2. ProgressBar

```tsx
function ProgressBar({ total, done, pending, indeterminate, animated, resumeAt }: PBProps) {
    if (indeterminate) {
        return <div className="mk-pbar mk-pbar--indeterminate" aria-busy="true" />;
    }
    const donePct    = clamp(done    / total * 100, 0, 100);
    const pendingPct = clamp(pending / total * 100, donePct, 100);
    return (
        <div className="mk-pbar" role="progressbar" aria-valuemin={0} aria-valuemax={100} aria-valuenow={Math.floor(donePct)}>
            <span className="mk-pbar__done"    style={{ insetInlineEnd: `${100 - donePct}%` }} />
            <span className="mk-pbar__pending" style={{ insetInlineStart: `${donePct}%`, insetInlineEnd: `${100 - pendingPct}%` }} />
            <span className={clsx('mk-pbar__current', animated && 'is-animated')}
                  style={{ insetInlineStart: `${donePct}%`, insetInlineEnd: `${100 - pendingPct}%` }} />
            {resumeAt != null && total != null && (
                <span className="mk-pbar__resume" style={{ insetInlineStart: `${(resumeAt/total)*100}%` }} />
            )}
        </div>
    );
}
```

CSS:

```css
.mk-pbar { position: relative; height: 8px; background: var(--color-surface-2); border-radius: var(--radius-sm); overflow: hidden; }
.mk-pbar__done    { position: absolute; inset-block: 0; inset-inline-start: 0; background: var(--color-accent); }
.mk-pbar__pending { position: absolute; inset-block: 0; background: var(--color-surface-3); }
.mk-pbar__current { position: absolute; inset-block: 0; background: var(--color-accent-soft); animation: mk-stripe 1.4s linear infinite; }
.mk-pbar--indeterminate::after {
    content: ''; position: absolute; inset: 0;
    background: linear-gradient(90deg, transparent, var(--color-accent), transparent);
    background-size: 50% 100%; animation: mk-bar-indet 1.6s linear infinite;
}
.mk-pbar__resume { position: absolute; inset-block: 0; inline-size: 2px; background: var(--color-warn); }
@media (prefers-reduced-motion: reduce) {
    .mk-pbar__current.is-animated { animation: none; }
    .mk-pbar--indeterminate::after { animation-duration: 3s; }
}
```

The 1 Hz update cadence (TC) maps to the stripe animation; reduced-motion drops to 0.5 Hz.

## 3. StageStrip

```tsx
const STAGES = ['scan', 'probe', 'extract', 'transcribe', 'subtitle_gen', 'index', 'thumbnail'] as const;

function StageStrip({ current }: { current: typeof STAGES[number] }) {
    const idx = STAGES.indexOf(current);
    return (
        <ol className="mk-stages" aria-label={t('queue.stage')}>
            {STAGES.map((s, i) => (
                <li key={s} className={clsx('mk-stages__node',
                    i < idx && 'is-done', i === idx && 'is-current', i > idx && 'is-pending')}>
                    <Icon name={`stage.${s}`} />
                    <span className="sr-only">{t(`stage.${s}`)}</span>
                </li>
            ))}
        </ol>
    );
}
```

A "model upgraded" hint (story EC) wraps the Transcribe node:

```tsx
{job.modelUpgraded && i === STAGES.indexOf('transcribe') && <UpgradeBadge />}
```

## 4. ETAReadout + suppression

```tsx
function useETA(job: Job): number | null {
    const samples = useRef<{ t: number; pos: number }[]>([]);
    useEffect(() => {
        if (job.state !== 'running') { samples.current = []; return; }
        samples.current.push({ t: Date.now(), pos: job.donePositionSeconds });
        if (samples.current.length > 5) samples.current.shift();
    }, [job.donePositionSeconds, job.state]);
    if (samples.current.length < 3) return null;       // suppress until 3 commits
    const first = samples.current[0], last = samples.current[samples.current.length - 1];
    const dT = (last.t - first.t) / 1000;
    const dP = last.pos - first.pos;
    if (dP <= 0) return null;
    const remaining = job.totalDurationSeconds - last.pos;
    return remaining * (dT / dP);
}
```

The suppression matches [Story 11.5 EC](../11-web-ui/story-11-05-processing-queue-dashboard.md): "ETA next to the bar; updated only after 3 segments have committed."

## 5. Tooltip

```tsx
function JobDetailsTooltip({ job }: { job: Job }) {
    return (
        <dl className="mk-tooltip">
            <dt>{t('queue.backend')}</dt><dd>{job.backend}</dd>
            <dt>{t('queue.model')}</dt><dd>{job.model}</dd>
            <dt>{t('queue.attempts')}</dt><dd>{job.attempts}</dd>
            <dt>{t('queue.last_heartbeat')}</dt><dd>{relTime(job.lastHeartbeatAt)}</dd>
        </dl>
    );
}
```

The 5 s cadence (per [REVIEW §1.4.c](../../REVIEW.md)) is the server's heartbeat interval; the tooltip just renders the latest value.

## 6. Force-Pause affordance

```tsx
function ForcePauseBtn({ id, after }: { id: string; after: number }) {
    const visible = useElapsedSince(after, /* job.pause_pending_at */);
    if (!visible) return null;
    return <Button variant="destructive" onClick={() => api.forcePause(id)}>{t('action.force_pause')}</Button>;
}
```

The button appears only after `pause_grace_sec` has elapsed — giving the worker time to gracefully drain.

## 7. Test plan

### 7.1 Bar

| Test | What it pins |
|---|---|
| `testIndeterminateWhenTotalNull` | `total = null` → indeterminate stripes; ETA hidden. |
| `testDoneAndCurrentSegmentsAtCorrectPercents` | 50% done, 60% pending → segments at 0–50, 50–60. |
| `testResumeMarkerVisibleWhenPaused` | `state = paused` with `resumeAt = 30s` of 60s → vertical line at 50%. |
| `testFailedShowsErrorIcon` | `state = failed` → error icon at the failure point. |
| `testReducedMotionDropsToHalfHz` | With reduced motion: animation duration doubled. |

### 7.2 Stage strip

| Test | What it pins |
|---|---|
| `testStagesUseCanonicalSet` | The 7 stages match the [REVIEW §1.3.b/c](../../REVIEW.md) set. |
| `testCurrentNodeHighlighted` | `current = transcribe` → 4th node has `is-current`. |
| `testModelUpgradedBadgeVisible` | `modelUpgraded = true` → badge near transcribe node. |

### 7.3 ETA

| Test | What it pins |
|---|---|
| `testETASuppressedFor3Samples` | First two samples → null; third → number. |
| `testETAUpdatesEvery1HzWithReducedAt500ms` | Stub clock; readout ticks at 1 Hz / 0.5 Hz. |

### 7.4 Tooltip

| Test | What it pins |
|---|---|
| `testTooltipShowsBackendModelAttemptsHeartbeat` | Hover → fields visible. |
| `testHeartbeatRelTime` | `lastHeartbeatAt = 4s ago` → "4s ago". |

### 7.5 Force-pause

| Test | What it pins |
|---|---|
| `testForcePauseButtonAppearsAfterGrace` | `pause_grace_sec = 10`; not visible at t=5; visible at t=15. |

### 7.6 Mid-flight duration change

| Test | What it pins |
|---|---|
| `testDurationChangedWarning` | `total` updates mid-job → warning chip rendered; bar uses new total as denominator for new progress. |

## 8. Edge cases — handling table

| Case | Behaviour | Where pinned |
|---|---|---|
| `total_duration_seconds = NULL` (probe pending) | Indeterminate stripes; ETA hidden. | `testIndeterminateWhenTotalNull` |
| Job resumes from a different model | Stage strip shows "model upgraded" hint. | `testModelUpgradedBadgeVisible` |
| Duration metadata changes mid-flight | Warning + bar uses new denominator for the post-swap segment. | `testDurationChangedWarning` |
| Reduced motion | Animations clamped; tooltip still functional (Tooltip is not motion-dependent). | `testReducedMotionDropsToHalfHz` |
| Failed job | Error icon at failure offset; State badge red. | `testFailedShowsErrorIcon` |
| Paused job | Resume marker rendered. | `testResumeMarkerVisibleWhenPaused` |
| Force-pause not yet eligible | Button absent (not just disabled). | `testForcePauseButtonAppearsAfterGrace` |
| RTL | All `inset-inline-*` properties; bar fills right-to-left. | `testRTLBarFlipsCorrectly` |
| Heartbeat stale > 30 s | Tooltip badge "stale heartbeat"; UI unaffected. | `testStaleHeartbeatBadge` |
| Cancel mid-progress | API call; UI optimistically removes the job; on error, restore. | `testCancelOptimisticThenError` |

## 9. Acceptance checklist

**Bar**
- [ ] Done/current/pending segments; resume marker; indeterminate stripes; failure icon.

**Stage strip**
- [ ] 7 canonical stages; current highlighted; model-upgrade hint.

**ETA**
- [ ] Suppressed until 3 samples; rolling window.

**Tooltip**
- [ ] Backend, model, attempts, heartbeat.

**Controls**
- [ ] Pause / Resume / Cancel inline; Force-Pause after grace.

**Tests**
- [ ] All §7 tests pass.

**Docs**
- [ ] `specs/epics/17-ux-design-system/README.md` ticks story 17.10.
