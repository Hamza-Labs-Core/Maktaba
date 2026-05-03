# Story 10.6 — RS256 key generation, rotation, JWKS

The API mints JWTs with a private RS256 key; both the API and Streaming
verify with the public key. Keys rotate without breaking in-flight tokens.

**AC-1 — Key material loading.**
- **Given** `MAKTABA_JWT_PRIVATE_KEY_PEM` and `MAKTABA_JWT_PUBLIC_KEY_PEM`
  env vars,
- **When** the API boots,
- **Then** keys are loaded; the `kid` is the SHA-256 of the public key DER
  truncated to 16 chars; if either key is missing, the API refuses to
  start with a clear error.

**AC-2 — Bootstrap key generation.**
- **Given** an empty install with no key env vars,
- **When** `maktaba-api keys init` is run,
- **Then** a 4096-bit RSA keypair is generated and printed in PEM form,
  with the env-var names that should hold them. The command never writes
  to disk (operator-controlled).

**AC-3 — JWKS publication.**
- **Given** the API is running,
- **When** `GET /api/.well-known/jwks.json` is called,
- **Then** the response is a JWKS containing every `kid` currently
  trusted: at minimum the active signing key and the previous one
  (during rotation). `Cache-Control: public, max-age=300`.

**AC-4 — Key rotation.**
- **Given** the admin runs `maktaba-api keys rotate`,
- **When** processed,
- **Then** a new keypair is generated, its public key is added to the
  JWKS, and after `rotation_overlap_sec` (default 24 h) the old key is
  removed. New tokens are signed with the new key. Old tokens remain
  valid until the overlap ends. A `LISTEN jwks_changed` notification
  fires so Streaming refreshes its cache immediately rather than
  waiting for the 5-min poll.

**AC-5 — Immediate rotation requires confirmation.**
- **Given** `maktaba-api keys rotate --immediate`,
- **When** invoked,
- **Then** the command prompts for explicit confirmation
  (`yes-invalidate-all-tokens`); only with the magic string typed in
  does the overlap window collapse to 0 and every in-flight token is
  invalidated. Audit row `category='security', event='key.rotated',
  payload={mode: 'immediate'}` is written.

**Test cases:**
- Integration: generated keys verify a token end-to-end.
- Integration: rotation — JWTs signed before rotation continue to verify
  for the overlap window.
- Integration: JWKS endpoint reflects rotation in <1 s.
- Integration: `--immediate` without confirmation aborts; with
  confirmation, every in-flight token is rejected within 1 s.

**Edge cases:**
- A leaked private key — the operator forces rotation with
  `--immediate` per AC-5. Documented as a security incident response.
- Key shorter than 2048 bits → refused at boot with a clear error.
- JWKS endpoint blocked by a firewall — Streaming caches the last-seen
  JWKS indefinitely (Epic 8 Story 8.1 AC-2). Documented in operations.
