# Implementation Plan — Story 17.08 Video player controls design

> Companion to [story-17-08-player-controls.md](story-17-08-player-controls.md).
> The story states *what* and *why*; this plan states *how*.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Web player | `web/src/features/player/PlayerControls.tsx` consuming `Vidstack` ([Story 11.3](../11-web-ui/story-11-03-video-player.md)). This story owns the visual language; not the engine. |
| Mobile/native | tvOS / AndroidTV use AVPlayer / ExoPlayer with control composition consuming the same shape (separate impl, same UX contract). |
| Mini-player | `web/src/features/player/MiniPlayer.tsx`. Decision (open question): we **persist the Vidstack instance** via a portal-mounted root that survives route changes. Justification below. |
| Subtitle styling controls | `web/src/features/player/SubtitleStyleSheet.ts` — a settings-bound style state that emits CSS custom properties applied to the subtitle layer. |
| Sprite preview | `<Scrubber>` consumes `sprite_url` from `manifest.json` (Epic 8); fades in within 200 ms. |
| Out of scope | Streaming engine (Epic 8), JWT minting (Epic 10), accessibility specifics for captions ([Story 17.11](story-17-11-transcript-presentation.md)). |

## 1. Mini-player decision

The story carries the Epic 03 open question: persist Vidstack instance vs. recreate-with-saved-position?

**Decision: persist instance via React portal.**

Rationale:
- HLS source segments are fetched continuously; a recreate-with-saved-position would refetch the manifest, lose buffer state, and re-run HDR negotiation — visible latency.
- React 18's portal + `<RouterProvider>` allow the player to live above the route tree, surviving navigation.
- The trade-off is a single-instance constraint (one mini-player at a time), which matches user expectation.

Anti-trade-off: increased memory while idle. Mitigated by an idle timeout (5 min after pause) that destroys the instance and restarts at the saved position on next play.

### 1.1 Switching to a different video

Persisting one instance across navigation also means navigating from
`/watch/A` to `/watch/B` must reuse the same instance with a *new
source*, not destroy and recreate. The Vidstack API for this is
`player.src = newSrc` (or `setProvider(newProvider)` for HLS-flavor
swaps), which is the web equivalent of AVPlayer's
`replaceCurrentItem`. The reload triggers a new HLS manifest fetch
and HDR re-negotiation, so the latency cost of recreate is paid here
anyway — but the instance survives so its event subscribers, the
mini-player window, and the auth interceptor stay attached.

```ts
// PlayerInstance.tsx
useEffect(() => {
    if (!playerRef.current) return;
    if (playerRef.current.src?.url !== src.url) {
        playerRef.current.src = src;        // replaceCurrentItem-equivalent
        playerRef.current.currentTime = resumeAt;
    }
}, [src, resumeAt]);
```

## 2. Architecture

```
   <App>
     <RouterProvider />
     <PlayerPortalRoot />     ← always mounted; <PlayerInstance> swaps in/out here
   </App>

   When at /watch/:id      → PlayerInstance is full-screen.
   When elsewhere          → PlayerInstance is repositioned to mini.
```

`<PlayerPortalRoot>` uses CSS `position: fixed` and a `transform` to animate between full-screen and mini placement. The video element doesn't unmount, so playback continues uninterrupted.

## 3. Control bar

```tsx
export function PlayerControls() {
    const ctx = useMediaState();
    const visible = useAutoHide(ctx, /* 3 s inactivity */ 3000);
    return (
        <div className={clsx('mk-player-controls', visible && 'is-visible')}>
            <Scrubber chapters={ctx.chapters} sprite={ctx.spriteURL} />
            <Cluster>
                <PlayPauseBtn />
                <Time current={ctx.current} duration={ctx.duration} forceWestern />
            </Cluster>
            <Cluster>
                <CaptionsBtn current={ctx.captionsTrack} tracks={ctx.captionsTracks} />
                <SettingsBtn>
                    <SpeedMenu />
                    <QualityMenu />
                    <AudioTrackMenu />
                    <SubtitleStyleMenu />
                </SettingsBtn>
                <PiPBtn />
                <CastBtn />
                <FullscreenBtn />
            </Cluster>
        </div>
    );
}
```

`useAutoHide` reveals on mouse-move/tap/D-pad, hides after 3 s of inactivity (story AC).

## 4. Scrubber + sprite preview

```tsx
function Scrubber({ chapters, sprite }: ScrubberProps) {
    const { hoverX, hoverTime } = useScrubHover();
    return (
        <div className="mk-scrubber" role="slider"
             aria-valuemin={0} aria-valuemax={ctx.duration} aria-valuenow={ctx.current}>
            <div className="mk-scrubber__track" />
            <div className="mk-scrubber__buffered" style={{ width: `${(ctx.buffered/ctx.duration)*100}%` }} />
            <div className="mk-scrubber__progress" style={{ width: `${(ctx.current/ctx.duration)*100}%` }} />
            {chapters?.map(c => (
                <span key={c.id} className="mk-scrubber__tick" style={{ insetInlineStart: `${(c.startSec/ctx.duration)*100}%` }} />
            ))}
            {hoverX !== null && (
                <div className="mk-scrubber__preview" style={{ insetInlineStart: hoverX }}>
                    <SpriteImage url={sprite} time={hoverTime} fallback={ctx.posterURL} />
                    <Time forceWestern>{hoverTime}</Time>
                </div>
            )}
        </div>
    );
}
```

The preview fades in within 200 ms (TC); on sprite cache miss (EC), `SpriteImage` falls back to the poster.

## 5. Settings menu

The Settings menu groups four sections; each is a `<Menu>` from [Story 17.2](story-17-02-component-library.md). State sync:

- **Speed**: 0.5×, 0.75×, 1×, 1.25×, 1.5×, 2× (modify `playbackRate`).
- **Quality**: HLS-variant manual override; default `auto`.
- **Audio track**: maps to ExoPlayer/AVPlayer audio selection.
- **Subtitle styling**: see §6.

## 6. Subtitle styling

User options (persisted per user via `PATCH /api/me/preferences`):

```ts
type SubtitleStyle = {
    size:    'sm' | 'md' | 'lg' | 'xl';
    color:   string;        // CSS color
    background: string;     // CSS color, may include alpha
    font:    'sans' | 'serif';
    position: 'bottom' | 'top';
};
```

A `<style>` injected into the player applies these via custom properties:

```ts
function applySubtitleStyle(s: SubtitleStyle) {
    const root = playerRoot.current!;
    root.style.setProperty('--sub-size',  sizeMap[s.size]);
    root.style.setProperty('--sub-color', s.color);
    root.style.setProperty('--sub-bg',    s.background);
    root.style.setProperty('--sub-font',  s.font === 'serif' ? 'var(--font-serif)' : 'var(--font-sans)');
    root.style.setProperty('--sub-pos',   s.position === 'top' ? '6%' : '88%');
}
```

```css
.mk-subtitle {
    font-size: var(--sub-size); color: var(--sub-color); background: var(--sub-bg);
    font-family: var(--sub-font); top: var(--sub-pos);
}
```

The story TC: "Subtitle styling change: live-applied without restarting the video." Updating CSS custom properties is reflowless and instantaneous.

## 7. Touch targets & TV variant

CSS:

```css
@media (pointer: coarse) {
    .mk-player-controls .mk-icon-btn { min-inline-size: 44px; min-block-size: 44px; }
}
```

TV variant (in `web/src/features/player/PlayerControlsTV.tsx`): same composition, larger spacing (TVTokens), `:focus-visible` ring required, no hover dependencies.

## 8. Test plan

### 8.1 Web

| Test | What it pins |
|---|---|
| `testAutoHideAfter3s` | Mouse idle → controls hide. |
| `testRevealOnInput` | Mouse-move/tap reveals. |
| `testScrubberSpritePreviewWithin200ms` | Hover; preview visible within 200 ms. |
| `testSpriteCacheMissShowsPoster` | Block sprite URL; preview shows poster. |
| `testCaptionsCycleAndBadge` | Click captions; track changes; badge updates. |
| `testSpeedSettingPersistsAcrossLoads` | Set 1.25×; reload page; persists. |
| `testSubtitleStyleLiveApplies` | Change size; subtitle font-size updates without source reload. |
| `testMiniPlayerSurvivesNavigation` | Play; navigate to /library; mini-player still playing same buffer. |
| `testMiniPlayerIdleDestroysAfter5min` | Pause + idle 5 min; instance destroyed; resume mints fresh session at saved position. |
| `testNoChaptersHidesTicks` | Empty chapters → no `.tick` DOM. |
| `testNewCaptionTrackUploaded` | Inject a new track via media events; menu updates without pausing. |

### 8.2 TV

| Test | What it pins |
|---|---|
| `testTVDpadAcrossControls` | Inject D-pad; focus moves Scrubber → Play → Captions → Settings. |
| `testTVFocusRingVisible` | `:focus-visible` ring present at all times under D-pad. |

### 8.3 RTL

| Test | What it pins |
|---|---|
| `testRTLPlayerControls` | Skip-back is on the right of skip-forward (logical next/previous). |
| `testTimeAlwaysWestern` | Even with Arabic numerals enabled, scrubber renders Western. |

## 9. Edge cases — handling table

| Case | Behaviour | Where pinned |
|---|---|---|
| Video with no chapters | Ticks hidden. | `testNoChaptersHidesTicks` |
| Sprite cache miss | Preview shows poster. | `testSpriteCacheMissShowsPoster` |
| Caption track upload mid-watch | Menu refreshes; no pause. | `testNewCaptionTrackUploaded` |
| User preference for Arabic numerals | All non-time numerals in Arabic; scrubber forced Western. | `testTimeAlwaysWestern` |
| Mini-player + dialog (modal) | Modal does not steal focus from mini-player; mini stays clickable. | `testModalDoesntDisablePlayer` |
| Mini-player on mobile (small viewport) | Repositioned to bottom-right with safe-area padding. | `testMiniPlayerMobileLayout` |
| Subtitle style on TV | Apply via CSS vars on the player root; no re-render. | `testTVSubtitleStyleLiveApplies` |
| Auto-hide while at-volume input | Hide is **paused** while interacting with the volume slider; resumes 3 s after release. | `testAutoHidePausedDuringInteraction` |
| Sprite URL points to a CDN that requires the same JWT | Auth interceptor adds the bearer per Epic 10 Story 10.7. | (Story 11.3) |
| Casting (AirPlay/Cast) | Controls swap to remote-mode (no scrubber preview, simplified time + transport). | `testCastModeControls` |

## 10. Acceptance checklist

**Web**
- [ ] Auto-hide, scrubber, captions, settings, PiP, fullscreen all wired.
- [ ] Subtitle styling live-applies via CSS vars.
- [ ] Mini-player persists Vidstack across navigation; idle-destroy at 5 min.

**TV**
- [ ] D-pad navigates all controls; focus ring visible.

**RTL**
- [ ] Logical layout; time forced Western.

**Tests**
- [ ] All §8 tests pass.

**Docs**
- [ ] `design/docs/player.md` resolves the mini-player open question.
- [ ] `specs/epics/17-ux-design-system/README.md` ticks story 17.8.
