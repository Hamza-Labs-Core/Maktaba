# Implementation Build Order

> Phase-by-phase sequence derived from [`specs/REVIEW.md` §7](../../specs/REVIEW.md). Stories at later phases assume their predecessors are landed.


**Source.** `specs/REVIEW.md` §7 "Dependency Sequencing" — independent spec review across the four epic documents.

## Phase summary

| Phase | Name | Stories |
|------:|------|--------:|
| 0 | Foundations | 8 |
| 1 | Core API + Auth | 10 |
| 2 | Library + Pipeline core | 24 |
| 3 | Transcription + Subtitles + Search | 27 |
| 4 | Streaming | 19 |
| 5 | WebSocket / GraphQL | 2 |
| 6 | Library long-tail | 14 |
| 7 | Observability + Hardening | 26 |
| 8 | Clients (parallel: web / mobile / desktop / TV) | 60 |
| 9 | NFR coverage and integrity | 32 |
| 10 | Optional / packaging | 14 |

## Phase 0 — Foundations

| # | Story | Title |
|--:|-------|-------|
| 1 | [22.04](../../specs/epics/22-devops/story-22-04-database-migrations.md) | DB migrations infrastructure |
| 2 | [22.01](../../specs/epics/22-devops/story-22-01-ci-pipeline.md) | CI gates |
| 3 | [24.03](../../specs/epics/24-data-integrity/story-24-03-database-constraints.md) | DB constraints (schema-level invariants) |
| 4 | [06.01](../../specs/epics/06-job-queue/story-06-01-schema-indexes.md) | Job queue schema + indexes |
| 5 | [06.02](../../specs/epics/06-job-queue/story-06-02-claim-loop.md) | Worker claim loop |
| 6 | [06.03](../../specs/epics/06-job-queue/story-06-03-heartbeat-progress.md) | Heartbeat + progress |
| 7 | [10.01](../../specs/epics/10-auth-security/story-10-01-user-store.md) | User store |
| 8 | [10.06](../../specs/epics/10-auth-security/story-10-06-rs256-keys-jwks.md) | RS256 keys + JWKS |

## Phase 1 — Core API + Auth

| # | Story | Title |
|--:|-------|-------|
| 1 | [07.01](../../specs/epics/07-api-server/story-07-01-http-server-skeleton.md) | HTTP server skeleton |
| 2 | [07.19](../../specs/epics/07-api-server/story-07-19-validation-rate-limiting.md) | Validation + rate limiting |
| 3 | [07.02](../../specs/epics/07-api-server/story-07-02-cursor-pagination.md) | Cursor pagination |
| 4 | [10.02](../../specs/epics/10-auth-security/story-10-02-web-login.md) | Web login |
| 5 | [10.03](../../specs/epics/10-auth-security/story-10-03-native-login.md) | Native login |
| 6 | [10.04](../../specs/epics/10-auth-security/story-10-04-token-refresh.md) | Token refresh |
| 7 | [10.05](../../specs/epics/10-auth-security/story-10-05-logout-revocation.md) | Logout / revocation |
| 8 | [10.10](../../specs/epics/10-auth-security/story-10-10-csrf-protection.md) | CSRF protection |
| 9 | [10.15](../../specs/epics/10-auth-security/story-10-15-transport-security.md) | Transport security (TLS) |
| 10 | [07.18](../../specs/epics/07-api-server/story-07-18-grpc-clients.md) | gRPC clients |

## Phase 2 — Library + Pipeline core

| # | Story | Title |
|--:|-------|-------|
| 1 | [09.01](../../specs/epics/09-library-management/story-09-01-library-config-schema.md) | Library config schema |
| 2 | [09.16](../../specs/epics/09-library-management/story-09-16-multi-root-overlap.md) | Multi-root overlap |
| 3 | [09.05](../../specs/epics/09-library-management/story-09-05-ignore-rules.md) | Ignore rules |
| 4 | [07.03](../../specs/epics/07-api-server/story-07-03-library-crud.md) | Library CRUD |
| 5 | [01.01](../../specs/epics/01-scanner/story-01-01-file-discovery.md) | File discovery |
| 6 | [01.02](../../specs/epics/01-scanner/story-01-02-content-identity.md) | Content identity (BLAKE3) |
| 7 | [01.03](../../specs/epics/01-scanner/story-01-03-filesystem-watcher.md) | Filesystem watcher |
| 8 | [01.04](../../specs/epics/01-scanner/story-01-04-manual-control.md) | Manual control |
| 9 | [01.05](../../specs/epics/01-scanner/story-01-05-schema-decisions.md) | Schema decisions |
| 10 | [09.02](../../specs/epics/09-library-management/story-09-02-filesystem-watcher.md) | Library filesystem watcher |
| 11 | [09.03](../../specs/epics/09-library-management/story-09-03-periodic-sweep.md) | Periodic sweep |
| 12 | [09.04](../../specs/epics/09-library-management/story-09-04-content-hash-dedup.md) | Content-hash dedup |
| 13 | [09.06](../../specs/epics/09-library-management/story-09-06-manual-scan.md) | Manual scan |
| 14 | [02.01](../../specs/epics/02-audio-extraction/story-02-01-audio-probe.md) | Audio probe |
| 15 | [02.02](../../specs/epics/02-audio-extraction/story-02-02-track-selection.md) | Track selection |
| 16 | [02.03](../../specs/epics/02-audio-extraction/story-02-03-stream-extraction.md) | Stream extraction |
| 17 | [02.04](../../specs/epics/02-audio-extraction/story-02-04-resource-accounting.md) | Resource accounting |
| 18 | [06.04](../../specs/epics/06-job-queue/story-06-04-pause-resume-cancel.md) | Pause/resume/cancel |
| 19 | [06.05](../../specs/epics/06-job-queue/story-06-05-backoff-retry.md) | Backoff retry |
| 20 | [06.06](../../specs/epics/06-job-queue/story-06-06-reaper.md) | Reaper |
| 21 | [06.07](../../specs/epics/06-job-queue/story-06-07-concurrency-caps.md) | Concurrency caps |
| 22 | [06.08](../../specs/epics/06-job-queue/story-06-08-graceful-shutdown.md) | Graceful shutdown |
| 23 | [06.09](../../specs/epics/06-job-queue/story-06-09-observability.md) | Observability |
| 24 | [06.10](../../specs/epics/06-job-queue/story-06-10-resume-invariant.md) | Resume invariant |

## Phase 3 — Transcription + Subtitles + Search

| # | Story | Title |
|--:|-------|-------|
| 1 | [03.01](../../specs/epics/03-transcription/story-03-01-backend-protocol.md) | Backend protocol |
| 2 | [03.02](../../specs/epics/03-transcription/story-03-02-whisper-mlx-backend.md) | whisper.cpp/MLX backend |
| 3 | [03.03](../../specs/epics/03-transcription/story-03-03-faster-whisper-backend.md) | faster-whisper backend |
| 4 | [03.04](../../specs/epics/03-transcription/story-03-04-openai-api-backend.md) | OpenAI API backend |
| 5 | [03.05](../../specs/epics/03-transcription/story-03-05-backend-registry.md) | Backend registry |
| 6 | [03.06](../../specs/epics/03-transcription/story-03-06-segment-commit.md) | Per-segment commit (correctness keystone) |
| 7 | [03.07](../../specs/epics/03-transcription/story-03-07-pause-resume.md) | Pause/resume |
| 8 | [03.08](../../specs/epics/03-transcription/story-03-08-crash-recovery.md) | Crash recovery |
| 9 | [04.01](../../specs/epics/04-subtitles/story-04-01-generate-from-segments.md) | Generate from segments |
| 10 | [04.02](../../specs/epics/04-subtitles/story-04-02-formatting-wrapping.md) | Formatting/wrapping |
| 11 | [04.03](../../specs/epics/04-subtitles/story-04-03-external-discovery.md) | External discovery |
| 12 | [04.04](../../specs/epics/04-subtitles/story-04-04-embedded-extraction.md) | Embedded extraction |
| 13 | [04.05](../../specs/epics/04-subtitles/story-04-05-live-vtt-contract.md) | Live VTT contract |
| 14 | [05.01](../../specs/epics/05-search-indexing/story-05-01-unit-chunking.md) | Unit chunking |
| 15 | [05.02](../../specs/epics/05-search-indexing/story-05-02-fts-tsvector.md) | FTS / tsvector |
| 16 | [05.03](../../specs/epics/05-search-indexing/story-05-03-chroma-vector.md) | Chroma vector |
| 17 | [05.04](../../specs/epics/05-search-indexing/story-05-04-hybrid-rrf.md) | Hybrid RRF |
| 18 | [05.05](../../specs/epics/05-search-indexing/story-05-05-incremental-indexing.md) | Incremental indexing |
| 19 | [05.06](../../specs/epics/05-search-indexing/story-05-06-query-suggestions.md) | Query suggestions |
| 20 | [07.06](../../specs/epics/07-api-server/story-07-06-transcript-window.md) | Transcript window |
| 21 | [07.07](../../specs/epics/07-api-server/story-07-07-subtitles-chapters-read.md) | Subtitles + chapters read |
| 22 | [07.08](../../specs/epics/07-api-server/story-07-08-search-api.md) | Search API |
| 23 | [07.09](../../specs/epics/07-api-server/story-07-09-saved-searches.md) | Saved searches |
| 24 | [07.04](../../specs/epics/07-api-server/story-07-04-video-crud.md) | Video CRUD |
| 25 | [07.05](../../specs/epics/07-api-server/story-07-05-video-processing-control.md) | Video processing control |
| 26 | [07.12](../../specs/epics/07-api-server/story-07-12-job-control.md) | Job control |
| 27 | [07.13](../../specs/epics/07-api-server/story-07-13-queue-stats.md) | Queue stats |

## Phase 4 — Streaming

| # | Story | Title |
|--:|-------|-------|
| 1 | [08.01](../../specs/epics/08-streaming/story-08-01-server-skeleton.md) | Server skeleton + signed-URL middleware |
| 2 | [08.15](../../specs/epics/08-streaming/story-08-15-probe-cache.md) | Probe cache |
| 3 | [10.07](../../specs/epics/10-auth-security/story-10-07-streaming-jwt-verify.md) | Offline JWT verify |
| 4 | [10.08](../../specs/epics/10-auth-security/story-10-08-signed-url-minter.md) | Signed-URL minter |
| 5 | [08.02](../../specs/epics/08-streaming/story-08-02-capability-matrix.md) | Capability matrix |
| 6 | [08.03](../../specs/epics/08-streaming/story-08-03-direct-play.md) | Direct play |
| 7 | [08.04](../../specs/epics/08-streaming/story-08-04-direct-stream-remux.md) | Direct stream remux |
| 8 | [08.05](../../specs/epics/08-streaming/story-08-05-hls-transcode.md) | HLS transcode |
| 9 | [08.07](../../specs/epics/08-streaming/story-08-07-hwaccel-detect.md) | Hwaccel detect |
| 10 | [08.10](../../specs/epics/08-streaming/story-08-10-concurrency-caps.md) | Concurrency caps |
| 11 | [08.06](../../specs/epics/08-streaming/story-08-06-dash-manifest.md) | DASH manifest |
| 12 | [08.08](../../specs/epics/08-streaming/story-08-08-grpc-server.md) | gRPC server |
| 13 | [08.09](../../specs/epics/08-streaming/story-08-09-session-store.md) | Session store |
| 14 | [07.10](../../specs/epics/07-api-server/story-07-10-streaming-session-lifecycle.md) | Session lifecycle |
| 15 | [07.11](../../specs/epics/07-api-server/story-07-11-watch-progress-sync.md) | Watch progress sync |
| 16 | [08.11](../../specs/epics/08-streaming/story-08-11-live-subtitle.md) | Live subtitle |
| 17 | [08.12](../../specs/epics/08-streaming/story-08-12-chapter-delivery.md) | Chapter delivery |
| 18 | [08.13](../../specs/epics/08-streaming/story-08-13-posters-sprites.md) | Posters + sprites |
| 19 | [08.14](../../specs/epics/08-streaming/story-08-14-cache-gc.md) | Cache GC |

## Phase 5 — WebSocket / GraphQL

| # | Story | Title |
|--:|-------|-------|
| 1 | [07.16](../../specs/epics/07-api-server/story-07-16-websocket-fanout.md) | WebSocket fan-out |
| 2 | [07.17](../../specs/epics/07-api-server/story-07-17-graphql-schema.md) | GraphQL schema |

## Phase 6 — Library long-tail

| # | Story | Title |
|--:|-------|-------|
| 1 | [09.07](../../specs/epics/09-library-management/story-09-07-library-stats.md) | Library stats |
| 2 | [09.08](../../specs/epics/09-library-management/story-09-08-language-tag.md) | Language tag |
| 3 | [09.09](../../specs/epics/09-library-management/story-09-09-topic-tag.md) | Topic tag |
| 4 | [09.10](../../specs/epics/09-library-management/story-09-10-content-type-classifier.md) | Content-type classifier |
| 5 | [09.11](../../specs/epics/09-library-management/story-09-11-speakers.md) | Speakers |
| 6 | [09.12](../../specs/epics/09-library-management/story-09-12-tag-crud.md) | Tag CRUD |
| 7 | [09.13](../../specs/epics/09-library-management/story-09-13-collections-manual.md) | Manual collections |
| 8 | [09.14](../../specs/epics/09-library-management/story-09-14-smart-collections.md) | Smart collections |
| 9 | [09.15](../../specs/epics/09-library-management/story-09-15-library-deletion.md) | Library deletion |
| 10 | [09.17](../../specs/epics/09-library-management/story-09-17-library-audit.md) | Library audit |
| 11 | [09.18](../../specs/epics/09-library-management/story-09-18-chapter-inference.md) | Chapter inference |
| 12 | [01.06](../../specs/epics/01-scanner/story-01-06-video-state-machine.md) | Video state machine |
| 13 | [03.09](../../specs/epics/03-transcription/story-03-09-diarization.md) | Diarization |
| 14 | [05.07](../../specs/epics/05-search-indexing/story-05-07-chapter-inference.md) | Chapter inference (search-side) |

## Phase 7 — Observability + Hardening

| # | Story | Title |
|--:|-------|-------|
| 1 | [21.01](../../specs/epics/21-observability/story-21-01-structured-logging.md) | Structured logging |
| 2 | [21.02](../../specs/epics/21-observability/story-21-02-metrics-surface.md) | Metrics surface |
| 3 | [21.03](../../specs/epics/21-observability/story-21-03-distributed-tracing.md) | Distributed tracing |
| 4 | [21.04](../../specs/epics/21-observability/story-21-04-health-readiness-probes.md) | Health/readiness probes |
| 5 | [21.05](../../specs/epics/21-observability/story-21-05-error-reporting.md) | Error reporting |
| 6 | [21.06](../../specs/epics/21-observability/story-21-06-audit-log.md) | Audit log |
| 7 | [21.07](../../specs/epics/21-observability/story-21-07-job-pipeline-visibility.md) | Job pipeline visibility |
| 8 | [21.08](../../specs/epics/21-observability/story-21-08-telemetry-privacy.md) | Telemetry privacy |
| 9 | [07.14](../../specs/epics/07-api-server/story-07-14-collections-tags-speakers.md) | Collections/tags/speakers REST |
| 10 | [07.15](../../specs/epics/07-api-server/story-07-15-settings-system.md) | Settings + system endpoints |
| 11 | [07.20](../../specs/epics/07-api-server/story-07-20-health-version-metrics.md) | Health/version/metrics |
| 12 | [23.01](../../specs/epics/23-security/story-23-01-authentication.md) | Authentication hardening |
| 13 | [23.02](../../specs/epics/23-security/story-23-02-authorization-acls.md) | Authorization & ACLs |
| 14 | [23.03](../../specs/epics/23-security/story-23-03-transport-security.md) | Transport security |
| 15 | [23.04](../../specs/epics/23-security/story-23-04-secrets-management.md) | Secrets management |
| 16 | [23.05](../../specs/epics/23-security/story-23-05-input-validation.md) | Input validation |
| 17 | [23.06](../../specs/epics/23-security/story-23-06-rate-limiting.md) | Rate limiting |
| 18 | [23.07](../../specs/epics/23-security/story-23-07-supply-chain-security.md) | Supply-chain security |
| 19 | [23.08](../../specs/epics/23-security/story-23-08-coordinated-disclosure.md) | Coordinated disclosure |
| 20 | [10.09](../../specs/epics/10-auth-security/story-10-09-single-user-mode.md) | Single-user mode |
| 21 | [10.11](../../specs/epics/10-auth-security/story-10-11-brute-force-protection.md) | Brute-force protection |
| 22 | [10.12](../../specs/epics/10-auth-security/story-10-12-rate-limiting-auth.md) | Auth rate limiting |
| 23 | [10.13](../../specs/epics/10-auth-security/story-10-13-permission-model.md) | Permission model |
| 24 | [10.14](../../specs/epics/10-auth-security/story-10-14-secret-loading.md) | Secret loading |
| 25 | [10.16](../../specs/epics/10-auth-security/story-10-16-security-audit.md) | Security audit |
| 26 | [10.17](../../specs/epics/10-auth-security/story-10-17-auth-pair.md) | Auth pair |

## Phase 8 — Clients (parallel: web / mobile / desktop / TV)

| # | Story | Title |
|--:|-------|-------|
| 1 | [17.01](../../specs/epics/17-ux-design-system/story-17-01-design-tokens.md) | Design tokens |
| 2 | [17.02](../../specs/epics/17-ux-design-system/story-17-02-component-library.md) | Component library |
| 3 | [11.01](../../specs/epics/11-web-ui/story-11-01-library-browser.md) | Library browser |
| 4 | [11.02](../../specs/epics/11-web-ui/story-11-02-video-detail-page.md) | Video detail page |
| 5 | [11.03](../../specs/epics/11-web-ui/story-11-03-video-player.md) | Video player |
| 6 | [11.04](../../specs/epics/11-web-ui/story-11-04-search-interface.md) | Search interface |
| 7 | [11.05](../../specs/epics/11-web-ui/story-11-05-processing-queue-dashboard.md) | Processing-queue dashboard |
| 8 | [11.06](../../specs/epics/11-web-ui/story-11-06-settings-page.md) | Settings page |
| 9 | [11.07](../../specs/epics/11-web-ui/story-11-07-responsive-design.md) | Responsive design |
| 10 | [11.08](../../specs/epics/11-web-ui/story-11-08-dark-light-theme.md) | Dark/light theme |
| 11 | [11.09](../../specs/epics/11-web-ui/story-11-09-keyboard-shortcuts.md) | Keyboard shortcuts |
| 12 | [11.10](../../specs/epics/11-web-ui/story-11-10-offline-pwa.md) | Offline PWA |
| 13 | [11.11](../../specs/epics/11-web-ui/story-11-11-accessibility.md) | Accessibility |
| 14 | [11.12](../../specs/epics/11-web-ui/story-11-12-i18n-rtl.md) | i18n + RTL |
| 15 | [11.13](../../specs/epics/11-web-ui/story-11-13-pat-management-api.md) | PAT management API |
| 16 | [11.14](../../specs/epics/11-web-ui/story-11-14-active-sessions-api.md) | Active sessions API |
| 17 | [12.01](../../specs/epics/12-mobile/story-12-01-ios-app.md) | iOS app |
| 18 | [12.02](../../specs/epics/12-mobile/story-12-02-android-app.md) | Android app |
| 19 | [12.03](../../specs/epics/12-mobile/story-12-03-native-player.md) | Native player |
| 20 | [12.04](../../specs/epics/12-mobile/story-12-04-push-notifications.md) | Push notifications |
| 21 | [12.05](../../specs/epics/12-mobile/story-12-05-background-playback.md) | Background playback |
| 22 | [12.06](../../specs/epics/12-mobile/story-12-06-offline-downloads.md) | Offline downloads |
| 23 | [12.07](../../specs/epics/12-mobile/story-12-07-share-cast.md) | Share / cast |
| 24 | [12.08](../../specs/epics/12-mobile/story-12-08-haptics.md) | Haptics |
| 25 | [12.09](../../specs/epics/12-mobile/story-12-09-deep-linking.md) | Deep linking |
| 26 | [12.10](../../specs/epics/12-mobile/story-12-10-device-registration-api.md) | Device registration API |
| 27 | [12.11](../../specs/epics/12-mobile/story-12-11-downloaded-flag-api.md) | Downloaded flag API |
| 28 | [13.01](../../specs/epics/13-desktop/story-13-01-macos.md) | macOS |
| 29 | [13.02](../../specs/epics/13-desktop/story-13-02-windows.md) | Windows |
| 30 | [13.03](../../specs/epics/13-desktop/story-13-03-linux.md) | Linux |
| 31 | [13.04](../../specs/epics/13-desktop/story-13-04-system-tray.md) | System tray |
| 32 | [13.05](../../specs/epics/13-desktop/story-13-05-mdns-discovery.md) | mDNS discovery |
| 33 | [13.06](../../specs/epics/13-desktop/story-13-06-drag-drop.md) | Drag-drop |
| 34 | [13.07](../../specs/epics/13-desktop/story-13-07-keyboard-shortcuts.md) | Keyboard shortcuts |
| 35 | [13.08](../../specs/epics/13-desktop/story-13-08-auto-update.md) | Auto-update |
| 36 | [17.03](../../specs/epics/17-ux-design-system/story-17-03-motion.md) | Motion |
| 37 | [17.04](../../specs/epics/17-ux-design-system/story-17-04-loading-states.md) | Loading states |
| 38 | [17.05](../../specs/epics/17-ux-design-system/story-17-05-error-empty-states.md) | Error/empty states |
| 39 | [17.06](../../specs/epics/17-ux-design-system/story-17-06-onboarding.md) | Onboarding |
| 40 | [17.07](../../specs/epics/17-ux-design-system/story-17-07-rtl-layout.md) | RTL layout |
| 41 | [17.08](../../specs/epics/17-ux-design-system/story-17-08-player-controls.md) | Player controls |
| 42 | [17.09](../../specs/epics/17-ux-design-system/story-17-09-search-results.md) | Search results |
| 43 | [17.10](../../specs/epics/17-ux-design-system/story-17-10-processing-progress.md) | Processing progress |
| 44 | [17.11](../../specs/epics/17-ux-design-system/story-17-11-transcript-presentation.md) | Transcript presentation |
| 45 | [14.01](../../specs/epics/14-tv-apps/story-14-01-tvos.md) | tvOS |
| 46 | [14.02](../../specs/epics/14-tv-apps/story-14-02-android-tv.md) | Android TV |
| 47 | [14.03](../../specs/epics/14-tv-apps/story-14-03-10-foot-ui.md) | 10-foot UI |
| 48 | [14.04](../../specs/epics/14-tv-apps/story-14-04-voice-search.md) | Voice search |
| 49 | [14.05](../../specs/epics/14-tv-apps/story-14-05-continue-watching.md) | Continue watching |
| 50 | [14.06](../../specs/epics/14-tv-apps/story-14-06-recommendations-ui.md) | Recommendations UI |
| 51 | [14.07](../../specs/epics/14-tv-apps/story-14-07-recommendations-api.md) | Recommendations API |
| 52 | [15.01](../../specs/epics/15-discovery/story-15-01-mdns.md) | mDNS |
| 53 | [15.02](../../specs/epics/15-discovery/story-15-02-cloud-relay.md) | Cloud relay |
| 54 | [15.03](../../specs/epics/15-discovery/story-15-03-federation.md) | Federation |
| 55 | [15.04](../../specs/epics/15-discovery/story-15-04-dlna-upnp.md) | DLNA / UPnP |
| 56 | [15.05](../../specs/epics/15-discovery/story-15-05-qr-pairing.md) | QR pairing |
| 57 | [15.06](../../specs/epics/15-discovery/story-15-06-pairing-api.md) | Pairing API |
| 58 | [15.07](../../specs/epics/15-discovery/story-15-07-federation-api.md) | Federation API |
| 59 | [07.21](../../specs/epics/07-api-server/story-07-21-recommendations.md) | Recommendations |
| 60 | [07.22](../../specs/epics/07-api-server/story-07-22-devices-register.md) | Devices register |

## Phase 9 — NFR coverage and integrity

| # | Story | Title |
|--:|-------|-------|
| 1 | [18.01](../../specs/epics/18-performance/story-18-01-latency-budgets.md) | Latency budgets |
| 2 | [18.02](../../specs/epics/18-performance/story-18-02-search-performance.md) | Search performance |
| 3 | [18.03](../../specs/epics/18-performance/story-18-03-streaming-hot-path.md) | Streaming hot path |
| 4 | [18.04](../../specs/epics/18-performance/story-18-04-pipeline-throughput.md) | Pipeline throughput |
| 5 | [18.05](../../specs/epics/18-performance/story-18-05-memory-cpu-envelopes.md) | Memory/CPU envelopes |
| 6 | [18.06](../../specs/epics/18-performance/story-18-06-client-perceived-performance.md) | Client-perceived performance |
| 7 | [18.07](../../specs/epics/18-performance/story-18-07-database-query-performance.md) | DB query performance |
| 8 | [18.08](../../specs/epics/18-performance/story-18-08-cache-layout-hit-rates.md) | Cache layout / hit rates |
| 9 | [19.01](../../specs/epics/19-scalability/story-19-01-single-host-capacity.md) | Single-host capacity |
| 10 | [19.02](../../specs/epics/19-scalability/story-19-02-api-scale-out.md) | API scale-out |
| 11 | [19.03](../../specs/epics/19-scalability/story-19-03-streaming-scale-out.md) | Streaming scale-out |
| 12 | [19.04](../../specs/epics/19-scalability/story-19-04-pipeline-scale-out.md) | Pipeline scale-out |
| 13 | [19.05](../../specs/epics/19-scalability/story-19-05-database-scaling.md) | Database scaling |
| 14 | [19.06](../../specs/epics/19-scalability/story-19-06-storage-scaling.md) | Storage scaling |
| 15 | [19.07](../../specs/epics/19-scalability/story-19-07-concurrency-caps.md) | Concurrency caps |
| 16 | [19.08](../../specs/epics/19-scalability/story-19-08-multi-tenant-readiness.md) | Multi-tenant readiness |
| 17 | [20.01](../../specs/epics/20-testing/story-20-01-test-pyramid.md) | Test pyramid |
| 18 | [20.02](../../specs/epics/20-testing/story-20-02-fixtures-seed-data.md) | Fixtures + seed data |
| 19 | [20.03](../../specs/epics/20-testing/story-20-03-unit-test-coverage.md) | Unit test coverage |
| 20 | [20.04](../../specs/epics/20-testing/story-20-04-integration-tests.md) | Integration tests |
| 21 | [20.05](../../specs/epics/20-testing/story-20-05-e2e-smoke-flows.md) | E2E smoke flows |
| 22 | [20.06](../../specs/epics/20-testing/story-20-06-contract-tests.md) | Contract tests |
| 23 | [20.07](../../specs/epics/20-testing/story-20-07-perf-regression-ci.md) | Perf regression CI |
| 24 | [20.08](../../specs/epics/20-testing/story-20-08-flaky-test-policy.md) | Flaky test policy |
| 25 | [24.01](../../specs/epics/24-data-integrity/story-24-01-atomic-writes.md) | Atomic writes |
| 26 | [24.02](../../specs/epics/24-data-integrity/story-24-02-idempotent-jobs.md) | Idempotent jobs |
| 27 | [24.04](../../specs/epics/24-data-integrity/story-24-04-concurrency-locking.md) | Concurrency locking |
| 28 | [24.05](../../specs/epics/24-data-integrity/story-24-05-backup-restore.md) | Backup / restore |
| 29 | [24.06](../../specs/epics/24-data-integrity/story-24-06-disaster-recovery.md) | Disaster recovery |
| 30 | [24.07](../../specs/epics/24-data-integrity/story-24-07-integrity-verification.md) | Integrity verification |
| 31 | [24.08](../../specs/epics/24-data-integrity/story-24-08-identity-stability.md) | Identity stability |
| 32 | [24.09](../../specs/epics/24-data-integrity/story-24-09-forward-back-compat.md) | Forward / back compat |

## Phase 10 — Optional / packaging

| # | Story | Title |
|--:|-------|-------|
| 1 | [16.01](../../specs/epics/16-subscriptions/story-16-01-free-tier.md) | Free tier |
| 2 | [16.02](../../specs/epics/16-subscriptions/story-16-02-premium-features.md) | Premium features |
| 3 | [16.03](../../specs/epics/16-subscriptions/story-16-03-subscription-management.md) | Subscription management |
| 4 | [16.04](../../specs/epics/16-subscriptions/story-16-04-license-validation.md) | License validation |
| 5 | [16.05](../../specs/epics/16-subscriptions/story-16-05-telemetry-opt-in.md) | Telemetry opt-in |
| 6 | [16.06](../../specs/epics/16-subscriptions/story-16-06-feature-flags.md) | Feature flags |
| 7 | [16.07](../../specs/epics/16-subscriptions/story-16-07-telemetry-api.md) | Telemetry API |
| 8 | [16.08](../../specs/epics/16-subscriptions/story-16-08-feature-flags-api.md) | Feature flags API |
| 9 | [22.02](../../specs/epics/22-devops/story-22-02-reproducible-builds.md) | Reproducible builds |
| 10 | [22.03](../../specs/epics/22-devops/story-22-03-container-images.md) | Container images |
| 11 | [22.05](../../specs/epics/22-devops/story-22-05-release-management.md) | Release management |
| 12 | [22.06](../../specs/epics/22-devops/story-22-06-upgrade-rollback.md) | Upgrade / rollback |
| 13 | [22.07](../../specs/epics/22-devops/story-22-07-multi-platform-packaging.md) | Multi-platform packaging |
| 14 | [22.08](../../specs/epics/22-devops/story-22-08-local-developer-workflow.md) | Local developer workflow |

## Critical path

> Stories that block the largest fan-outs. Prioritize these.


| Story | Blocks | Reason |
|-------|--------|--------|
| [06.01](../../specs/epics/06-job-queue/story-06-01-schema-indexes.md) | every Pipeline epic, every job-control endpoint | DB schema is foundational |
| [03.06](../../specs/epics/03-transcription/story-03-06-segment-commit.md) | 03.07, 03.08, 05.05, 06.10, 24.02 | THE correctness keystone (real-time per-segment commit) |
| [07.01](../../specs/epics/07-api-server/story-07-01-http-server-skeleton.md) | every Epic 7 story, every Epic 9 endpoint, every client story | HTTP skeleton — foundational |
| [07.18](../../specs/epics/07-api-server/story-07-18-grpc-clients.md) | 07.08, 07.10, 07.15 | gRPC inter-service plumbing |
| [08.01](../../specs/epics/08-streaming/story-08-01-server-skeleton.md) | every Streaming endpoint | signed-URL middleware perimeter |
| [10.06](../../specs/epics/10-auth-security/story-10-06-rs256-keys-jwks.md) | 10.07, 10.08, every authenticated endpoint, every signed URL | RS256 keys + JWKS — crypto foundation |
| [22.04](../../specs/epics/22-devops/story-22-04-database-migrations.md) | every schema change | migrations infra tooling |
| [17.01](../../specs/epics/17-ux-design-system/story-17-01-design-tokens.md) | every UI epic | design tokens block all UI work |

## Parallelizable tracks

Once Phase 1 lands, the following streams can run concurrently:

- Pipeline (Epics 1–6) and API/Streaming (Epics 7, 8) — independent after Phase 1; gRPC contract is the shared interface.
- Web (Epic 11), Mobile (Epic 12), Desktop (Epic 13) — all wrap the same web bundle; sequential or parallel.
- TV (Epic 14) — shares no code with web; can be built in parallel by a separate person.
- NFR epics (18–24) — accrete tests/infrastructure on top of feature work; run in parallel with feature epics.
- Discovery (15), Subscriptions (16), Design system (17) — largely orthogonal to backend work.

## Dependency diagrams

- Epic-level: [specs/diagrams/epic-dependencies.drawio](../../specs/diagrams/epic-dependencies.drawio)
- Pipeline (01–06): [specs/diagrams/pipeline-stories.drawio](../../specs/diagrams/pipeline-stories.drawio)
- API/Streaming (07–08): [specs/diagrams/api-streaming-stories.drawio](../../specs/diagrams/api-streaming-stories.drawio)
- Clients (11–14): [specs/diagrams/client-stories.drawio](../../specs/diagrams/client-stories.drawio)
- NFR (18–24): [specs/diagrams/nonfunctional-stories.drawio](../../specs/diagrams/nonfunctional-stories.drawio)
