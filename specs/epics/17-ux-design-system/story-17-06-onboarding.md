# Story 17.6 — Onboarding flow (first-time setup wizard)

A 4-step setup wizard for first launch: choose admin password, add a
library, configure STT, pick UI language and theme.

**Anchors:** [`architecture.md` §6](../../architecture.md), §9.8.

## AC

- Step 1: server-name + admin password (or skip in single-user mode
  with bootstrap token).
- Step 2: pick a library root (browse FS or paste path); show estimated
  size.
- Step 3: pick STT backend (auto-detected: MLX on Apple Silicon, CUDA
  on NVIDIA GPU, CPU fallback); show the trade-off matrix.
- Step 4: language (Arabic / English) + theme (light / dark / system).
- "Skip" available on every step except Step 1; defaults are sane.
- Progress bar at the top; back-arrow on every non-first step.
- On completion: a one-time "Tour the app" carousel (Library, Search,
  Queue, Player) — dismissable.

## TC

- Single-user mode: wizard skips Step 1; lands on Step 2.
- Choose `whisper-cpu`: Step 3 warns about realtime factor.
- Cancel mid-wizard: the user lands on a "Resume setup" banner on next
  launch.
- Tour carousel: dismissable from any panel; never shown again
  unless the user clicks "Show me again" in Settings → About.

## EC

- Disk has no writable library root: surface "Create a folder for me?"
  CTA that creates `$HOME/Maktaba/Library`.
- STT backend auto-detect fails (no GPU, no MLX): default to
  `whisper-cpu` with a "this will be slow on a CPU" warning.
- Onboarding interrupted by an OS update / reboot: state persisted
  server-side; resume is exact.
