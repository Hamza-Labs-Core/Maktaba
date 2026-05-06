# Mockups Catalog

Every HTML mockup in [`web/mockups/`](../../web/mockups/), with the story it covers, the owning epic, the platform, and the UI states it demonstrates.

- **Web** = canonical PWA layouts (top-level `mockup-*.html`).
- **Admin** = web administration surfaces, all of which include an explicit "All UI states" panel (alerts, toasts, confirms, dropdowns, skeletons, empty, tooltips, dialogs).
- **Mobile** = Capacitor shell (iOS/Android).
- **Desktop** = Tauri shell (macOS/Windows/Linux).
- **TV** = Epic 14 (out of the 07–13 scope but listed for completeness).
- **Theme library** = stand-alone component reference; consumed by Epics 11/12/13/17.

> Story IDs reference [`specs/epics/`](../../specs/epics/). For the canonical state taxonomy, see any `web/mockups/admin/*.html` (e.g. [admin-dashboard.html](../../web/mockups/admin/admin-dashboard.html#all-ui-states)).

---

## Web (Epic 11)

| File | Story | Platform | UI states / contents |
|------|-------|----------|----------------------|
| [mockup-11-01-library-browser.html](../../web/mockups/mockup-11-01-library-browser.html) | 11.1 | web | Grid + list view, sort/filter rail (single screen) |
| [mockup-11-02-video-detail.html](../../web/mockups/mockup-11-02-video-detail.html) | 11.2 | web | Hero, metadata, transcript jumps, related (single screen) |
| [mockup-11-03-video-player.html](../../web/mockups/mockup-11-03-video-player.html) | 11.3 | web | Vidstack chrome, captions, chapters, sprite scrubber |
| [mockup-11-04-search-interface.html](../../web/mockups/mockup-11-04-search-interface.html) | 11.4 | web | FTS / semantic / hybrid toggle, facets, transcript hits |
| [mockup-11-05-processing-queue.html](../../web/mockups/mockup-11-05-processing-queue.html) | 11.5 | web | Job pipeline, per-stage counts, retry/cancel actions |
| [mockup-11-06-settings.html](../../web/mockups/mockup-11-06-settings.html) | 11.6 | web | Settings layout (libraries, STT, players) |
| [mockup-11-07-theme.html](../../web/mockups/mockup-11-07-theme.html) | 11.7, 11.8 | web | Dark / light theme demo, responsive breakpoints |
| [mockup-11-10-offline-pwa.html](../../web/mockups/mockup-11-10-offline-pwa.html) | 11.10 | web | Service-worker install banner, offline fallback, sync state |
| [mockup-11-12-i18n.html](../../web/mockups/mockup-11-12-i18n.html) | 11.12 | web | Arabic RTL + English LTR side-by-side |
| [mockup-17-06-onboarding.html](../../web/mockups/mockup-17-06-onboarding.html) | 17.6 (consumed by Epic 11) | web | First-run onboarding wizard |

## Admin (Epics 09, 10, 15, 16, 21)

Every file below ends with an "All UI states" panel listing the alerts, toasts, dialogs, dropdowns, skeletons, empty states, and inline-validation states for that surface.

| File | Story | Epic | Platform | UI states |
|------|-------|------|----------|-----------|
| [admin/admin-dashboard.html](../../web/mockups/admin/admin-dashboard.html) | 21.x (observability dashboard) | 21 | admin | Alert · disk space critical; Alert · queue backed up; Alert · all systems healthy; Toast · stream started; Toast · service down; Confirm · restart server; Dropdown · widget overflow menu; Dropdown · time range picker; Skeleton · loading widgets; Empty · no streams active; Empty · queue clean; Tooltip · queue stage info; Dialog · widget config |
| [admin/cloud-relay.html](../../web/mockups/admin/cloud-relay.html) | 15.x (relay) | 15 | admin | Confirm · disable relay; Confirm · regenerate keys; Dialog · bandwidth quota; Toast · relay connected; Toast · relay disconnected; Toast · auth required; Dropdown · region picker; Skeleton · connecting; Empty · relay disabled; Tooltip · TOFU explained; Inline · throttled by quota; Toast · key changed warning |
| [admin/duplicates.html](../../web/mockups/admin/duplicates.html) | 9.4 | 9 | admin | Confirm · move to trash; Confirm · permanently delete; Confirm · merge metadata; Toast · 19 duplicates removed; Toast · scan failed; Toast · file in use; Dropdown · file context menu; Inline progress · scanning; Skeleton · loading groups; Empty · no duplicates found; Empty · scan never run; Tooltip · what is pHash |
| [admin/feature-gate.html](../../web/mockups/admin/feature-gate.html) | 16.6 | 16 | admin | Toast · feature unlocked; Toast · trial ending; Toast · feature locked; Mini gate · inline lock; Dialog · trial expired; Dialog · downgrade impact; Dialog · upgrade success; Empty · feature requires data; Skeleton · loading feature; Tooltip · why locked?; Lock pill · all variants; Mini gate · usage limit |
| [admin/job-pipeline.html](../../web/mockups/admin/job-pipeline.html) | 7.12, 7.13 | 7 | admin | Confirm · retry failed job; Confirm · cancel jobs; Confirm · pause pipeline; Toast · pipeline drained; Toast · job failed; Toast · bottleneck warning; Dropdown · job context menu; Dialog · stage details; Skeleton · pipeline loading; Empty · pipeline idle; Empty · stage paused; Tooltip · bottleneck explained; Inline · job retrying |
| [admin/library-config.html](../../web/mockups/admin/library-config.html) | 9.1, 9.5, 9.16 | 9 | admin | Modal · folder picker; Confirm · remove folder; Confirm · discard changes; Toast · settings saved; Toast · folder not found; Toast · invalid pattern; Dropdown · folder context menu; Inline progress · scan running; Skeleton · loading folders; Empty · no folders watched; Tooltip · ignore syntax help; Validation · path doesn't exist |
| [admin/lockout.html](../../web/mockups/admin/lockout.html) | 10.11 | 10 | admin | Toast · lockout active; Toast · unlock now; Toast · password reset sent; Toast · reset email failed; Confirm · send reset email; Dialog · contact admin; Dialog · admin override unlock; Loading · sending reset; Empty · no recent attempts; Tooltip · why we lock |
| [admin/log-viewer.html](../../web/mockups/admin/log-viewer.html) | 21.x (logs) | 21 | admin | Confirm · export logs; Confirm · clear logs; Toast · export ready; Toast · stream paused; Toast · query syntax error; Dropdown · service filter; Dropdown · row context menu; Loading · running query; Skeleton · loading log rows; Empty · no results; Empty · log retention; Tooltip · query syntax; Inline · saved filter |
| [admin/login.html](../../web/mockups/admin/login.html) | 10.2 | 10 | admin | Toast · sign-in success; Toast · invalid credentials; Toast · new device alert; Dialog · connection failed; Dialog · 2FA required; Dialog · password reset sent; Inline · invalid email; Tooltip · caps lock warning; Loading · signing in; Skeleton · initial load; Bottom sheet · saved accounts; Empty · SSO disabled |
| [admin/plans.html](../../web/mockups/admin/plans.html) | 16.x (plans) | 16 | admin | Modal · checkout summary; Confirm · cancel subscription; Toast · subscription active; Toast · payment failed; Toast · renewal upcoming; Dialog · payment failed; Dialog · trial offer; Loading · processing payment; Skeleton · loading prices; Empty · no payment methods; Banner · current plan; Tooltip · why subscribe |
| [admin/qr-pairing.html](../../web/mockups/admin/qr-pairing.html) | 10.17 | 10 | admin | Toast · pairing complete; Toast · code expired; Toast · invalid code; Dialog · approve pairing on desktop; Dialog · pairing failed; Dialog · camera permission denied; Tooltip · what is pairing?; Loading · waiting for scan; Skeleton · QR generating; Empty · no camera available; Manual code entry; Bottom sheet · select server |
| [admin/register.html](../../web/mockups/admin/register.html) | 10.1 | 10 | admin | Toast · account created; Toast · email taken; Toast · server busy; Dialog · email verification sent; Dialog · weak password warning; Dialog · ToS not accepted; Password strength · all variants; Tooltip · privacy guarantee; Loading · creating account; Skeleton · ToS preview loading |
| [admin/server-discovery.html](../../web/mockups/admin/server-discovery.html) | 13.5 | 13 | admin | Confirm · trust new server; Confirm · forget server; Dialog · key changed warning; Toast · connected; Toast · connection refused; Toast · timeout; Dropdown · server overflow menu; Skeleton · scanning network; Empty · no servers found; Empty · mDNS blocked; Tooltip · what is mDNS?; Inline · validation error |
| [admin/sessions.html](../../web/mockups/admin/sessions.html) | 10.5, 11.14 | 10/11 | admin | Confirm · revoke session; Confirm · sign out everywhere; Toast · session revoked; Toast · suspicious login; Toast · revoke failed; Dropdown · session overflow menu; Empty · single session only; Empty · no failed attempts; Skeleton · loading sessions; Tooltip · what is "trusted"?; Dialog · session details |
| [admin/speaker-manager.html](../../web/mockups/admin/speaker-manager.html) | 9.11 | 9 | admin | Modal · rename speaker; Confirm · delete speaker; Confirm · split speaker; Toast · merge complete; Toast · re-cluster started; Toast · merge failed; Dropdown · speaker overflow menu; Skeleton · loading speaker list; Empty · no speakers yet; Empty · search no results; Tooltip · confidence score; Inline progress · re-clustering |

## Mobile (Epic 12)

| File | Story | Platform | UI states / contents |
|------|-------|----------|----------------------|
| [mobile/home.html](../../web/mockups/mobile/home.html) | 12.1, 12.2 | mobile | Home grid, continue-watching row, library shelves |
| [mobile/video-detail.html](../../web/mockups/mobile/video-detail.html) | 12.1, 12.2 | mobile | Video detail with download / cast / share affordances |
| [mobile/player.html](../../web/mockups/mobile/player.html) | 12.3, 12.5 | mobile | Native player chrome, background-playback controls |
| [mobile/search.html](../../web/mockups/mobile/search.html) | 12.1, 12.2 | mobile | Mobile search UI |
| [mobile/downloads.html](../../web/mockups/mobile/downloads.html) | 12.6 | mobile | Download manager: queued, in-progress, paused, complete |
| [mobile/push-notification.html](../../web/mockups/mobile/push-notification.html) | 12.4 | mobile | Push permission prompt, sample alerts |

## Desktop (Epic 13)

| File | Story | Platform | UI states / contents |
|------|-------|----------|----------------------|
| [desktop/main-window.html](../../web/mockups/desktop/main-window.html) | 13.1, 13.2, 13.3 | desktop | Native menu bar, window chrome, multi-window persistence |
| [desktop/tray-menu.html](../../web/mockups/desktop/tray-menu.html) | 13.4 | desktop | Tray icon menu (active streams, queue, quit) |
| [desktop/drag-drop.html](../../web/mockups/desktop/drag-drop.html) | 13.6 | desktop | Drag-and-drop ingest target, OS file source |

## TV (Epic 14)

| File | Story | Platform | UI states / contents |
|------|-------|----------|----------------------|
| [tv/home-row.html](../../web/mockups/tv/home-row.html) | 14.x | tv | Row-and-shelf TV home, focus ring |
| [tv/detail-tv.html](../../web/mockups/tv/detail-tv.html) | 14.x | tv | TV video detail, large-text layout |
| [tv/player-tv.html](../../web/mockups/tv/player-tv.html) | 14.x | tv | TV player chrome, D-pad seek |
| [tv/search-tv.html](../../web/mockups/tv/search-tv.html) | 14.x | tv | TV search with on-screen keyboard |

## Theme library (Epics 11, 17)

Stand-alone component references shared across web, mobile, desktop, and TV. No story-specific UI; they are the visual ground truth for tokens and primitives.

| File | Component |
|------|-----------|
| [theme-library/buttons.html](../../web/mockups/theme-library/buttons.html) | Buttons (primary, secondary, danger, icon) |
| [theme-library/inputs.html](../../web/mockups/theme-library/inputs.html) | Text inputs, textarea, select, switch, slider |
| [theme-library/cards.html](../../web/mockups/theme-library/cards.html) | Cards (video, library, queue, stat) |
| [theme-library/modals.html](../../web/mockups/theme-library/modals.html) | Modals + drawers |
| [theme-library/tables.html](../../web/mockups/theme-library/tables.html) | Tables (sortable, filterable, dense) |
| [theme-library/navigation.html](../../web/mockups/theme-library/navigation.html) | Top nav, side rail, breadcrumbs, tabs |
| [theme-library/badges-tags.html](../../web/mockups/theme-library/badges-tags.html) | Badges, status chips, tags |
| [theme-library/colors.html](../../web/mockups/theme-library/colors.html) | Color tokens (dark + light) |
| [theme-library/typography.html](../../web/mockups/theme-library/typography.html) | Type scale (LTR + RTL) |
| [theme-library/player-controls.html](../../web/mockups/theme-library/player-controls.html) | Player control primitives |

## Shared CSS

[`web/mockups/_shared.css`](../../web/mockups/_shared.css) — common tokens and primitives included by every mockup.

---

## Epic-to-mockup index

- **[Epic 07 — API Server](epics/epic-07-api-server.md):** `admin/job-pipeline.html`, `admin/sessions.html`, `mockup-11-05-processing-queue.html`, `mockup-11-04-search-interface.html`.
- **[Epic 08 — Streaming](epics/epic-08-streaming.md):** consumed by `mockup-11-03-video-player.html`, `mobile/player.html`, `tv/player-tv.html`. No own UI.
- **[Epic 09 — Library Management](epics/epic-09-library-management.md):** `admin/library-config.html`, `admin/duplicates.html`, `admin/speaker-manager.html`.
- **[Epic 10 — Auth & Security](epics/epic-10-auth-security.md):** `admin/login.html`, `admin/register.html`, `admin/lockout.html`, `admin/qr-pairing.html`, `admin/sessions.html`.
- **[Epic 11 — Web UI](epics/epic-11-web-ui.md):** `mockup-11-01..12`, `mockup-17-06-onboarding.html`, theme library, `admin/sessions.html`.
- **[Epic 12 — Mobile](epics/epic-12-mobile.md):** all `mobile/*.html`.
- **[Epic 13 — Desktop](epics/epic-13-desktop.md):** all `desktop/*.html`, `admin/server-discovery.html` (mDNS trust UX).
