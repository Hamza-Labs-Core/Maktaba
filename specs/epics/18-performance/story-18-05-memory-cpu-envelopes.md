# Story 18.5 — Memory and CPU envelopes

Per-service resident memory and CPU ceilings under steady-state and burst.

## Acceptance criteria

- AC1. API service idle RSS ≤ 80 MiB; under 200 qps sustained ≤ 250 MiB;
  no monotonic growth over 24 h soak (slope < 1 MiB/h).
- AC2. Streaming service idle RSS ≤ 100 MiB; with 8 concurrent transcodes
  the parent Go process ≤ 300 MiB (FFmpeg children excluded).
- AC3. Pipeline service idle RSS ≤ 600 MiB (Whisper model loaded); during
  a transcribe burst total worker RSS does not exceed
  `concurrency.transcribe × per-model RSS + 500 MiB` overhead.
- AC4. Goroutine count (Go) and asyncio-task count (Python) are emitted
  as metrics; tests assert no leak after a soak.

## Test cases

- TC1. Soak: run a representative workload for 24 h; collect RSS at 1 min
  intervals; linear regression slope < 1 MiB/h per service.
- TC2. Burst: hit each service with 10× steady-state load for 5 minutes;
  RSS returns to within 10 % of steady-state ≤ 60 s after burst ends.
- TC3. Goroutine leak: open and close 1,000 streaming sessions; final
  goroutine count is within 50 of the post-warm-up baseline.

## Edge cases

- EC1. CGO heap (Go) is invisible to `runtime.MemStats`; tests must use
  RSS from the OS, not Go runtime numbers.
- EC2. Python multiprocessing workers' RSS is double-counted by `ps`
  shared pages; tests use `smaps_rollup` PSS where available.
- EC3. macOS jetsam pressure: the soak test must not run with the laptop
  asleep (the harness disables App Nap and pins to performance cores).
