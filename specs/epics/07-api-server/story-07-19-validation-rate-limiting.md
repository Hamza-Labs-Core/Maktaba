# Story 7.19 — Validation, body limits, rate limiting

Cross-cutting middleware that every story above relies on.

**AC-1 — Body size cap.**
- **Given** a request with a JSON body larger than 1 MiB (default),
- **When** received,
- **Then** the response is `413 Payload Too Large` problem+json before
  the handler executes.

**AC-2 — Content-Type enforcement.**
- **Given** a non-GET request without `Content-Type: application/json` (or
  `application/graphql+json` for the GraphQL endpoint),
- **When** received,
- **Then** the response is `415 Unsupported Media Type` problem+json.

**AC-3 — Struct-tag validation.**
- **Given** a handler whose request struct has `validate:"required,uuid"`,
- **When** the body is `{id: "not-a-uuid"}`,
- **Then** the response is `422 Unprocessable Entity` problem+json with
  `errors: [{field: "id", message: "must be a valid UUID"}]`.

**AC-4 — Per-user rate limit.**
- **Given** a user identity (cookie or JWT),
- **When** they exceed `default_rate_per_min` (default 600) on the API
  surface,
- **Then** further requests return `429 Too Many Requests` with
  `Retry-After: <sec>` and a problem+json body.
- **Given** the unauthenticated `/api/auth/*` surface, the broader-auth
  per-IP cap of `auth_rate_per_min = 30` applies (Epic 10 Story 10.12
  AC-1). The narrower `/api/auth/login` endpoint has its own stricter
  cap of `login_rate_per_min = 10` per IP (Epic 10 Story 10.12 AC-3).

**AC-5 — Per-IP rate limit (DoS guard).**
- **Given** any single IP,
- **When** total request rate exceeds `ip_rate_per_min` (default 6000),
- **Then** further requests from that IP return 429 regardless of
  authentication.

**Test cases:**
- Unit: 1 MiB +1 byte body → 413 without invoking the handler.
- Integration: malformed JSON → 400 `type: invalid-json`, not 500.
- Integration: 700 valid requests in 60 s as one user → ~600 200s and
  ~100 429s.
- Integration: 11 `/api/auth/login` requests in 60 s from one IP → at
  least one is 429 (the login-specific cap of 10 fires before the
  broader 30/min auth cap).
- Integration: `Retry-After` header value is consistent with the
  rate-limit window.
- Security: a `Content-Length: 1000000000` header with a small body does
  not trick the limiter (use `LimitReader`).

**Edge cases:**
- A user behind a corporate NAT shares an IP with 100 colleagues — the
  per-IP limit is generous enough (6000/min) that legitimate usage isn't
  cut off; per-user rate limit is the dominant constraint.
- A streaming-progress POST burst (story 7.11) is excluded from the
  general API rate limit and uses its own 1/s/session debounce.
- Body limit configurable per-route — `/api/videos/{id}` PATCH allows 8
  KB, `/api/search` POST allows 16 KB, default 1 MiB. Test case: route
  with 8 KB cap rejects 16 KB body even though the global cap is 1 MiB.
