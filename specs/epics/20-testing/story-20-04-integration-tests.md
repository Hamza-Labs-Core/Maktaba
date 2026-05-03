# Story 20.4 — Integration tests with real backends

Integration tests use a real Postgres, real FFmpeg, real ChromaDB. No
mocks at service boundaries owned by us.

## Acceptance criteria

- AC1. The Go integration suite spins up Postgres via `testcontainers`
  on first test, reuses the container across tests in the same run, and
  truncates between tests with a per-test transaction or savepoint.
- AC2. The Python integration suite spins up Postgres, ChromaDB, and a
  real FFmpeg subprocess against fixture media.
- AC3. gRPC contract tests — the API integration suite stands up a real
  Pipeline gRPC server (or a buffconn in-process variant) against the
  generated stubs from `shared/proto/`.
- AC4. No `gomock` / `unittest.mock` for our own services. Mocks are
  allowed only at external SaaS boundaries (OpenAI Whisper API, etc.)
  and must use `httptest`-style replay tapes recorded once.

## Test cases

- TC1. Spin-up: a fresh CI runner brings up Postgres + ChromaDB in ≤ 30
  s; total integration tier completes within budget.
- TC2. Cross-service: enqueue a transcribe job via API, verify the
  Pipeline worker claims it, and observe the WS event on the API side
  — all without manual coordination.
- TC3. Replay tapes: recorded OpenAI responses are deterministic;
  re-recording requires a flag and is gated by code review.

## Edge cases

- EC1. CI runner without Docker — fall back to a `pg-embed`-style
  local Postgres binary and a Python-backed ChromaDB process.
- EC2. Postgres version drift between dev and CI — tests pin
  `postgres:16` exactly; mismatch fails the spin-up.
- EC3. FFmpeg version skew — the integration suite probes
  `ffmpeg -version` and fails fast if below the minimum supported
  version (documented).
