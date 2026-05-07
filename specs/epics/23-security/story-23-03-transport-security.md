# Story 23.3 — Transport security

TLS by default; localhost is the only documented exception.

## Acceptance criteria

- AC1. Caddy fronts the stack and terminates TLS; on Mac, Caddy's
  local-CA mode auto-issues a trusted cert to the keychain; on
  Linux, Let's Encrypt against the user's domain.
- AC2. HSTS enabled by default with `max-age=31536000;
  includeSubDomains`; opt-out documented.
- AC3. TLS configuration: TLS 1.2 minimum, modern cipher suites
  only, OCSP stapling on, ALPN h2.
- AC4. Internal gRPC between services uses mTLS when the services
  are not co-located; for `localhost` colocated processes,
  loopback-only gRPC is permitted with a documented threat model.

## Test cases

- TC1. Cipher floor: `nmap --script ssl-enum-ciphers` against the
  default Caddy config reports no `weak` or `broken` entries.
- TC2. HSTS: a fresh load returns the header; the `--no-hsts` flag
  opts it out and observable in the response.
- TC3. mTLS: with a cert mismatch, an inter-service gRPC call fails
  with a clear cert error; loopback path bypasses with a startup
  warning.

## Edge cases

- EC1. Self-signed cert on a fresh install — the web client shows a
  documented "trust this device" flow on mobile, none on desktop.
- EC2. Let's Encrypt rate-limit hit — Caddy retries with backoff;
  health probes still report alive, ready=false until cert acquired.
- EC3. Captive-portal proxies that downgrade — clients refuse to
  send credentials over a downgraded connection.
