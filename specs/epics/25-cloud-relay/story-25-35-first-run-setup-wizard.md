# Story 25.35 — First-run setup wizard

> Epic 25 · Cloud relay · Phase 6 (server distribution)

## Description

When a user first opens Maktaba — whether on macOS, Windows, Linux,
NAS, or Docker — they see a guided wizard that detects what their box
can do, recommends settings, and gets them from "blank slate" to
"library scanning" in under 5 minutes.

Steps:

1. **Welcome.**
   - "Welcome to Maktaba" with a one-paragraph pitch.
   - Language picker (en, ar — Arabic-first product).
   - "Continue" button.

2. **Hardware probe.**
   - Detected: OS, CPU model + cores, RAM total, GPU (if
     available — Apple Silicon NE / NVIDIA / Intel iGPU /
     AMD), free storage on the install volume.
   - Displays "Recommended profile" bubble:
     `pi-default` / `mac-mini` / `pc-desktop` /
     `nas-bay` / `vps-small` / `vps-large`.
   - User can override.

3. **Library folders.**
   - "Where are your videos?" with a folder picker. On
     macOS this triggers the Documents permission prompt;
     on Linux Snap/Flatpak this opens the file portal; on
     Docker this is read from compose `/media`.
   - User can add multiple roots.
   - We probe each path (does it exist? readable? rough
     size estimate via du).
   - Warns if path looks unusual: empty dir, network mount
     not yet connected, very small (< 100 MB).

4. **Transcription engine.**
   - Auto-selected based on hardware. Options:
     - `mlx-whisper` (Apple Silicon)
     - `faster-whisper-cuda` (NVIDIA GPU)
     - `faster-whisper-cpu` (everyone else)
     - `openai-api` (cloud transcription, BYO API key —
       user paid path; gated by Epic 16 entitlement check)
   - Model picker: tiny / base / small / medium / large.
     Default model is profile-driven; tooltip explains
     trade-offs.

5. **Storage.**
   - Where to put the database (SQLite vs Postgres) —
     defaults to local SQLite for solo users; Postgres
     option appears if Postgres is detected (Docker
     compose case).
   - Where to put the cache (transcodes, thumbnails) —
     prefers a fast drive if multiple drives are
     detected.

6. **Cloud account (optional).**
   - "Want to access your library from anywhere?" → "Yes,
     connect" or "Skip for now".
   - If "yes": opens 25.6 claim flow.
   - "Skip" is a first-class option; nothing degrades.

7. **All set.**
   - "Maktaba is ready. We'll start scanning your
     libraries. This can take an hour or more for large
     collections — you can use the app while it works."
   - Button: "Open library".

The wizard is **resumable**: if the user closes mid-flow, the
server boots in a "needs setup" state and re-shows the wizard
on next visit until step 7 completes.

The wizard is **safe**: nothing destructive happens on
"Continue" or "Back"; the user can navigate freely.

## Acceptance criteria

- **Given** a fresh install,
  **when** the user opens the web UI for the first time,
  **then** the wizard's Welcome step is shown and no
  library data is touched until step 7.
- **Given** the wizard's hardware probe runs on Apple
  Silicon,
  **when** the result renders,
  **then** the recommended profile is `mac-mini` and the
  transcription engine is preselected to `mlx-whisper`
  with model `small.en`.
- **Given** the user picks a folder that doesn't exist,
  **when** they click "Add",
  **then** an inline error appears and "Continue" is
  disabled until at least one valid root is added.
- **Given** the user is on Linux Snap and picks a folder
  not connected via the snap interface,
  **when** they add it,
  **then** the wizard surfaces "Snap permissions: run
  `snap connect maktaba:home`" with a copy-button.
- **Given** the user picks `large` Whisper on a 4 GB Pi,
  **when** they confirm,
  **then** the wizard warns "this model needs > 8 GB
  RAM; consider `base` instead" and requires explicit
  confirmation.
- **Given** the user clicks "Skip cloud",
  **when** the wizard advances,
  **then** the cloud-link is unset; the dashboard still
  loads; cloud features are off.
- **Given** the user closes the wizard at step 4,
  **when** they reopen the URL,
  **then** the wizard reopens at step 4 with their step-3
  selections preserved.

## Test cases

| ID  | Type        | Setup | Action | Expected |
|-----|-------------|-------|--------|----------|
| T01 | snapshot    | Welcome step in `ar` | render | RTL valid, copy translated |
| T02 | unit        | hardware probe on Apple M2 | result | gpu="apple-ne", ram>=8gb |
| T03 | integration | folder picker on Snap | add | portal opens, perm granted |
| T04 | unit        | model warning logic | 4 GB + large | warn |
| T05 | integration | resume after close | reopen | state restored |
| T06 | regression  | DB initialization | confirm | schema applied, no data loss on re-run |
| T07 | a11y        | keyboard-only flow | navigate | all steps reachable |
| T08 | regression  | bilingual switch mid-flow | toggle ar | remaining steps in ar |
| T09 | integration | cloud claim flow | "Yes, connect" | redirects to claim then back |
| T10 | unit        | invalid path | submit | inline error |

## Edge cases

- **Empty libraries.** A user with no media yet is fine;
  step 7 says "your libraries are empty — drop videos
  in [path] and Maktaba will pick them up".
- **Read-only library mount.** If readable, fine; if not,
  step 3 surfaces the error and refuses to proceed.
- **No internet.** The cloud-link step gracefully says
  "we couldn't reach Maktaba Cloud — you can connect
  later from Settings".
- **Re-run wizard intentionally.** Settings → "Run setup
  wizard again" lets a user redo steps; doesn't lose
  data.
- **Multi-user host (Mac).** Each macOS user runs their
  own server (per 25.27); each runs their own wizard.
- **Multi-monitor.** UI is responsive; wizard ≥ 720px
  wide for usability; mobile / phone form factor
  supported but discouraged for first run.
- **Power user CLI.** A `maktaba wizard --json --apply
  config.json` runs the wizard non-interactively for
  unattended provisioning (Ansible/Salt).
- **Telemetry.** Wizard step completions can be
  *opt-in* telemetry events (Epic 16.5); off by default.
- **Permissions on shared folders.** When the user picks
  `~/Movies`, on macOS the request is funneled through
  the security-scoped bookmark from 25.27.
- **Probe accuracy.** RAM is reported by OS APIs; CPU
  cores via `runtime.NumCPU`; GPU via vendor probes.
  Tested on each platform.

## Files / packages

- `web/src/pages/Wizard/Welcome.tsx`,
  `Hardware.tsx`, `Libraries.tsx`, `Transcription.tsx`,
  `Storage.tsx`, `Cloud.tsx`, `Done.tsx`.
- `web/src/lib/wizard-state.ts` — resumable state
  persisted in `localStorage` keyed by server id.
- Backend: `internal/setup/probe.go`,
  `internal/setup/wizard.go`,
  `internal/setup/profile.go`.

## Open questions

- **Hardware probe accuracy on VMs.** VMs hide GPU and
  underreport CPU; we accept the probe as-is and let
  user override.
- **Adding cloud later from Setup.** Users who skipped
  always see a "Connect to Cloud" CTA in Settings —
  permanent.
